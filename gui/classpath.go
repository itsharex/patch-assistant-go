package main

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/util"
)

// 把已打开的应用包摊平成 javac 能直接读的 classpath。
//
// **为什么必须摊平**：Spring Boot 胖包把依赖放在 `BOOT-INF/lib/*.jar`，
// 而那些 jar 是**打包在胖包里面**的 zip 条目 —— javac 的 `-cp` 只认磁盘上
// 真实存在的 jar 或目录，读不到 jar 套 jar。所以要先解出来。
//
// 同理，应用自己的类在 `BOOT-INF/classes/` 下（普通 jar 则在根），
// 被编辑的类引用同包/同项目的类是常态，这部分也要解出来。
//
// 解出来的东西放在用户缓存目录里，按「应用包路径 + 大小 + 修改时间」做 key
// 复用 —— 一个真实胖包有几十上百 MB，每次编译都重解一遍太慢。

// classpathRoot 返回 classpath 缓存的根目录。
func classpathRoot() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "PatchAssistant2J", "classpath"), nil
}

// appClasspathKey 用「路径 + 大小 + 修改时间」算缓存 key。
//
// 不用内容 hash：算一遍 md5 要把整个胖包读一遍，比解压还慢；
// 而"换了个包"或"同一个包被重新构建"这两件事，大小+修改时间足够区分。
func appClasspathKey(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(h[:])[:16], nil
}

// buildAppClasspath 摊平应用包，返回可直接拼给 javac `-cp` 的路径列表。
//
// 没打开应用包时返回空列表 + nil 错误：被编辑的类可能不依赖外部类型
// （比如就改个常量），不该因为"没开应用包"就直接拒绝编译 —— 让 javac
// 自己去报"找不到符号"，那个信息比我们臆测的更有用。
func (a *App) buildAppClasspath() ([]string, error) {
	if util.IsBlank(a.appPath) {
		return nil, nil
	}
	key, err := appClasspathKey(a.appPath)
	if err != nil {
		return nil, fmt.Errorf("读取应用包信息失败: %w", err)
	}
	root, err := classpathRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, key)
	classesDir := filepath.Join(dir, "classes")
	libDir := filepath.Join(dir, "lib")

	// 已解过就直接复用
	if _, err := os.Stat(filepath.Join(dir, doneMarker)); err == nil {
		return classpathPaths(classesDir, libDir), nil
	}

	// 解之前先把别的包的缓存删掉：一个胖包解出来几十上百 MB，
	// 不清理的话用户每换一次包就多占一份。
	cleanOtherClasspathCaches(root, key)

	if err := os.MkdirAll(classesDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return nil, err
	}
	if err := extractForClasspath(a.appPath, classesDir, libDir); err != nil {
		// 解到一半失败就把目录删掉，免得下次误当成"已解好"
		os.RemoveAll(dir)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, doneMarker), []byte("ok"), 0o644); err != nil {
		return nil, err
	}
	return classpathPaths(classesDir, libDir), nil
}

// doneMarker 标记"这个缓存目录已经解完了"。
//
// 为什么要标记文件而不是"目录存在就算好"：解压中途失败（磁盘满、包损坏）
// 会留下一个半成品目录，没有标记的话下次会把它当成完整的用，
// 结果是莫名其妙的"找不到符号"。
const doneMarker = ".done"

// classpathPaths 拼出给 javac 的路径列表。
//
// lib 用**通配符** `lib/*` 而不是把每个 jar 列出来：胖包的依赖动辄上百个，
// 命令行长度会爆掉（Windows 上限 32K）。javac 从 Java 6 起就支持
// classpath 通配符，行为等价。
func classpathPaths(classesDir, libDir string) []string {
	out := []string{classesDir}
	if entries, err := os.ReadDir(libDir); err == nil && len(entries) > 0 {
		out = append(out, filepath.Join(libDir, "*"))
	}
	return out
}

// cleanOtherClasspathCaches 删掉除当前 key 以外的缓存目录。
func cleanOtherClasspathCaches(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep {
			continue
		}
		os.RemoveAll(filepath.Join(root, e.Name()))
	}
}

// extractForClasspath 把应用包里的 .class 与嵌套 jar 解到目标目录。
//
// 只解这两类：javac 编译只需要类型信息，资源文件（properties/xml/图片）
// 一概不需要。一个胖包里资源常常占大头，跳过它们能省掉相当一部分 IO。
func extractForClasspath(appPath, classesDir, libDir string) error {
	zr, err := zip.OpenReader(appPath)
	if err != nil {
		return fmt.Errorf("打开应用包失败: %w", err)
	}
	defer zr.Close()

	// class 的根前缀：Spring Boot 胖包在 BOOT-INF/classes/ 下，
	// 普通 jar 直接在根。两种都要支持 —— 这个工具两个场景都会遇到。
	prefix := ""
	if hasEntryPrefix(zr, constant.ClassesPath) {
		prefix = constant.ClassesPath
	}

	for _, f := range zr.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			continue
		}
		switch {
		case strings.HasSuffix(name, constant.DotJar):
			if err := extractEntry(f, filepath.Join(libDir, jarFlatName(name))); err != nil {
				return err
			}
		case strings.HasSuffix(name, constant.DotClass):
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				// 胖包根下还有别的 class（启动器之类），不属于应用类，
				// 放进 classes 目录反而可能盖住同名类，跳过。
				continue
			}
			rel := strings.TrimPrefix(name, prefix)
			dst := filepath.Join(classesDir, filepath.FromSlash(rel))
			if err := extractEntry(f, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// hasEntryPrefix 判断包里有没有以某个前缀开头的条目。
func hasEntryPrefix(zr *zip.ReadCloser, prefix string) bool {
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, prefix) {
			return true
		}
	}
	return false
}

// jarFlatName 把嵌套 jar 的包内路径压成一个不含分隔符的文件名。
//
// `BOOT-INF/lib/foo-1.0.0.jar` → `BOOT-INF_lib_foo-1.0.0.jar`。
// 为什么要带上前缀而不是只取 basename：不同目录下可能有同名 jar，
// 只取 basename 会让后一个把前一个覆盖掉，编译时随机缺类。
func jarFlatName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_").Replace(name)
}

// extractEntry 把一个 zip 条目写到磁盘（必要时建父目录）。
func extractEntry(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开条目 %s 失败: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("创建 %s 失败: %w", dst, err)
	}
	_, cerr := io.Copy(out, rc)
	werr := out.Close()
	if cerr != nil {
		return fmt.Errorf("写出 %s 失败: %w", dst, cerr)
	}
	if werr != nil {
		return fmt.Errorf("关闭 %s 失败: %w", dst, werr)
	}
	return nil
}
