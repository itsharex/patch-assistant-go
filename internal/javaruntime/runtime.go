//go:build windows

// Package javaruntime 内嵌一份最小 JDK 运行时，让反编译与编译不再依赖机器上装 Java。
//
// 背景：原版是 GluonFX native-image，**自带 JVM**、双击即用；Go 版移植时改成了
// "外挂本机 Java"，于是要求机器装 Java 11+（反编译）与 JDK（编译）—— 这是相对
// 原版的**功能退化**（见 MIGRATION §2.1）。本包把它补回来：内嵌一份用 jlink 裁出来的
// 运行时（`java.base` + `jdk.compiler`），首次使用时解压到用户缓存目录，之后
// java / javac 都从这里取。
//
// 为什么是 `jdk.compiler` 而不是纯 JRE：**JRE 里没有 javac**，只内置 JRE 的话
// 「编译为 .class」仍然要机器上装 JDK。带上 jdk.compiler 后 jlink 会自动生成
// `bin/javac.exe`，两件事一起解决。
//
// 为什么不用 GraalVM native-image 把反编译器编成原生 exe（2026-10-16 实测过，不采用）：
// ① 覆盖不了「编译为 .class」（javac 无法 native-image，这是决定性的）；
// ② 输出与原版有边缘差异（varargs 写法）；③ 非 ASCII 路径失败。详见 MIGRATION §五。
//
// 运行时的来源与许可：**Microsoft Build of OpenJDK 21**（GPLv2+CE，可自由再分发；
// 不用 Oracle JDK 是因为它现在是 NFTC 许可，再分发条款对"裁剪派生的运行时"并不明确）。
// 生成命令：
//
//	jlink --add-modules java.base,jdk.compiler --strip-debug \
//	      --no-header-files --no-man-pages --compress=zip-6 --output runtime
//
// 44MB 目录 / 108 个文件 / zip 后 27.5MB。**换运行时必须同时改 runtimeVersion**。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package javaruntime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed runtime.zip
var runtimeZip []byte

// runtimeVersion 既是"这份运行时是什么"的标记，也是解压目录名的一部分。
// 换运行时（升级 JDK、改模块列表、换发行方）必须改这个值，
// 否则旧目录会被误当成新的复用。
const runtimeVersion = "openjdk21.0.12.1-base+compiler"

// readyMark 是解压完成的标记文件名，内容为内嵌 zip 的 sha256。
const readyMark = ".ready"

var (
	once sync.Once
	dir  string
	err  error
)

// Dir 返回内置运行时的根目录，必要时先解压。可反复调用，只解压一次。
func Dir() (string, error) {
	if Disabled() {
		return "", ErrDisabled
	}
	once.Do(func() { dir, err = ensure() })
	return dir, err
}

// Java 返回内置运行时的 java.exe —— 反编译用它。
func Java() (string, error) { return bin("java") }

// Javac 返回内置运行时的 javac.exe —— 编译用它。
func Javac() (string, error) { return bin("javac") }

func bin(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(d, "bin", name+".exe")
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", fmt.Errorf("内置运行时缺少 %s：%s", name, p)
	}
	return p, nil
}

// ensure 保证运行时已解压到缓存目录，返回它的根目录。
//
// 复用判据是"标记文件里的 sha256 == 内嵌 zip 的 sha256"—— 内嵌内容一变
// （换版本）标记就对不上，自动重解压，不会用着半新半旧的目录。
func ensure() (string, error) {
	root, err := runtimeDir()
	if err != nil {
		return "", err
	}
	want := sha256Hex(runtimeZip)

	if b, err := os.ReadFile(filepath.Join(root, readyMark)); err == nil &&
		strings.TrimSpace(string(b)) == want {
		return root, nil
	}

	if err := unpack(root, want); err != nil {
		return "", err
	}
	return root, nil
}

// runtimeDir 是运行时的落盘位置。
//
// 放用户缓存目录而不是 exe 旁边：exe 可能装在 Program Files 这类只读位置，
// 而解压要写 108 个文件。
func runtimeDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "PatchAssistant2J", "runtime-"+runtimeVersion), nil
}

// unpack 把内嵌 zip 解到 root。
//
// 先解到同级临时目录再改名 —— 中途失败（磁盘满、进程被杀）不会留下一个
// "看起来完整、实际缺文件"的目录，下次启动会因为标记不匹配而重来。
func unpack(root, want string) error {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("创建运行时目录失败：%w", err)
	}

	tmp, err := os.MkdirTemp(parent, "runtime-tmp-")
	if err != nil {
		return fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(tmp)

	if err := extractZip(tmp); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, readyMark), []byte(want+"\n"), 0o644); err != nil {
		return fmt.Errorf("写运行时标记失败：%w", err)
	}

	// Windows 上 os.Rename 到已存在的目录会失败，所以先清旧的。
	// 清不掉（例如被另一个实例占用）就用现有的 —— 内容是一样的。
	if err := os.RemoveAll(root); err != nil {
		if _, statErr := os.Stat(filepath.Join(root, readyMark)); statErr == nil {
			return nil
		}
		return fmt.Errorf("清理旧运行时失败：%w", err)
	}
	if err := os.Rename(tmp, root); err != nil {
		return fmt.Errorf("就位运行时失败：%w", err)
	}
	return nil
}

// extractZip 把内嵌 zip 展开到 dst。
func extractZip(dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(runtimeZip), int64(len(runtimeZip)))
	if err != nil {
		return fmt.Errorf("读取内嵌运行时失败：%w", err)
	}
	for _, f := range zr.File {
		p, err := safeJoin(dst, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeZipFile(f, p); err != nil {
			return err
		}
	}
	return nil
}

// safeJoin 防 zip slip —— 条目名里带 `../` 或绝对路径时拒绝。
// zip 是我们自己打的，但"相信输入"不该成为习惯。
func safeJoin(dst, name string) (string, error) {
	p := filepath.Join(dst, filepath.FromSlash(name))
	rel, err := filepath.Rel(dst, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("运行时条目路径非法：%s", name)
	}
	return p, nil
}

func writeZipFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
