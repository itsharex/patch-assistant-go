// Package decompile 用 Fernflower 反编译 .class 文件。
//
// 背景与取舍见 MIGRATION.md §2.1。一句话：Go 生态没有成熟的 JVM 字节码
// 反编译器，而原版用的就是 Fernflower（`lib/fernflower-1.3.0.jar`，615 KB）。
// 这里把**同一份 jar** 嵌进二进制，运行时交给**内置的 Java 运行时**执行
// （见 internal/javaruntime）—— 这样反编译结果与原版逐字节一致，而不是
// "另写一个功能相似的替代品"，也不再要求机器上装 Java。
//
// 为什么不直接 `java -jar` 就完事、还要写这个包：需要负责好三件容易出错的事 ——
// jar 的释放与校验、class 文件名的解析（fernflower 的输出文件名取自**字节里的
// 类名**，不是我们给的文件名）、以及 Windows 上不弹出控制台黑框。
//
// **已验证**（`_extract/tools/decomp_consistency.sh`）：
// 命令行方式与原版 API（`SingleClassDecompiler`）的输出逐字节一致 ——
// 40 个单类样本 + 含内部类的样本，0 差异。所以这里可以放心走命令行，
// 不必自己写 Java 驱动去调 API。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package decompile

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"cn.cpoet.patch.assistant/internal/javaruntime"
)

// fernflowerJar 是原项目 `src-java/lib/fernflower-1.3.0.jar` 的原样副本
// （Apache-2.0，JetBrains 出品）。复制到本包目录是因为 go:embed 只能嵌入
// 包目录下的文件。**不要**改动这份 jar —— 换了版本就等于换了行为。
//
//go:embed fernflower-1.3.0.jar
var fernflowerJar []byte

const jarName = "fernflower-1.3.0.jar"

// ErrNoJava 表示既没有内置运行时、本机也找不到 Java。
//
// 内置运行时（internal/javaruntime）正常时**这条永远不会触发** —— 那正是内置它的
// 目的。能走到这里只有两种情况：运行时解压失败（磁盘满 / 缓存目录不可写），
// 或者非 Windows 平台上本机没装 Java。
//
// 顺带记着：内嵌的 fernflower jar 里的 class 是 major 55（Java 11），所以外部
// Java 的版本要求是 **11 或更高**，不是 8 —— 这条以前写成"JRE 8 或更高"，是错的，
// 真机上撞过（见 MIGRATION §五 第 55 项）。
var ErrNoJava = errors.New(
	"未找到可用的 Java 运行环境，无法反编译 .class。\n" +
		"程序自带的 Java 运行时没能解压出来，本机也没有装 Java。\n" +
		"请检查磁盘空间与缓存目录的写入权限后重启本程序；" +
		"或安装 Java 11+ 后重试，也可以把 JAVA_HOME 指向 JDK/JRE 目录")

// 反编译超时。单个类通常几百毫秒，60 秒是给超大类 + 冷启动 JVM 留的余量。
const decompileTimeout = 60 * time.Second

var (
	jarOnce sync.Once
	jarFile string
	jarErr  error
)

// jarPath 把内嵌的 fernflower 释放到用户缓存目录并返回其路径。
//
// 为什么落盘而不是每次写临时目录：jar 有 615 KB，而反编译会反复调用，
// 每次都写一遍纯属浪费。放缓存目录 + 用 sha256 校验，既能复用，
// 又能在内嵌内容变了（换版本）时自动覆盖。
func jarPath() (string, error) {
	jarOnce.Do(func() {
		dir, err := os.UserCacheDir()
		if err != nil || dir == "" {
			dir = os.TempDir()
		}
		dir = filepath.Join(dir, "PatchAssistant2J")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			jarErr = err
			return
		}
		p := filepath.Join(dir, jarName)

		want := sha256.Sum256(fernflowerJar)
		if b, err := os.ReadFile(p); err == nil && sha256.Sum256(b) == want {
			jarFile = p
			return
		}
		if err := os.WriteFile(p, fernflowerJar, 0o644); err != nil {
			jarErr = err
			return
		}
		jarFile = p
	})
	return jarFile, jarErr
}

// FindJava 定位 java 可执行文件。
//
// 顺序：**内置运行时优先** → JAVA_HOME → PATH。
//
// 内置优先是有意的：反编译结果不再随机器上的 Java 版本漂移，也不会再出现
// "那台电脑装的是 JDK 8，双击 class 打不开"（MIGRATION §五 55）。后两级是
// 逃生口 —— 内置运行时解压失败（磁盘满、权限不足）时仍能靠本机 Java 干活。
func FindJava() (string, error) {
	if p, err := javaruntime.Java(); err == nil {
		return p, nil
	}
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		p := filepath.Join(home, "bin", "java"+exeExt)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		return p, nil
	}
	return "", ErrNoJava
}

// Available 报告反编译能力是否可用。
//
// 内置运行时正常时**恒为 true** —— 这正是内置它的目的：不再看机器上有没有 Java。
// 只有内置运行时解压失败、且本机也没装 Java 时才会 false。
func Available() bool {
	_, err := FindJava()
	return err == nil
}

// Decompile 反编译一个类，返回 Java 源码。
//
// main 是主类字节；inners 是它的**内部类**字节，可为空。
//
// 为什么要单独传 inners 而不是只给主类：对齐原版 `ClassContentParser.parse`
// —— 节点有子级（内部类挂在外部类下）时，原版把所有子孙类的字节一起交给
// fernflower。只喂主类的话，反编译出来的内部类体是空的。
func Decompile(main []byte, inners [][]byte) (string, error) {
	// 先从字节里读出类名。fernflower 的输出文件名取自**字节里的类名**，
	// 与我们给它的临时文件名无关 —— 不先解析出来就没法知道该读回哪个文件。
	name, err := MainClassName(main)
	if err != nil {
		return "", fmt.Errorf("解析 class 失败：%w", err)
	}
	simple := name
	if i := strings.LastIndexByte(simple, '/'); i >= 0 {
		simple = simple[i+1:]
	}

	java, err := FindJava()
	if err != nil {
		return "", err
	}
	jar, err := jarPath()
	if err != nil {
		return "", fmt.Errorf("释放反编译器失败：%w", err)
	}

	dir, err := os.MkdirTemp("", "pa-decomp-")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(dir)

	inDir := filepath.Join(dir, "in")
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}

	args := []string{"-jar", jar}
	p, err := writeClass(inDir, 0, main)
	if err != nil {
		return "", err
	}
	args = append(args, p)
	for i, b := range inners {
		p, err := writeClass(inDir, i+1, b)
		if err != nil {
			return "", err
		}
		args = append(args, p)
	}
	args = append(args, outDir)

	ctx, cancel := context.WithTimeout(context.Background(), decompileTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, java, args...)
	hideWindow(cmd) // Windows 上不弹控制台黑框
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("反编译超时（%s）", decompileTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", decompileFailure(java, msg)
	}

	src, err := readResult(outDir, simple)
	if err != nil {
		return "", err
	}
	return src, nil
}

// isJavaVersionError 判断 JVM 的 stderr 是不是"本机 Java 版本低于反编译器要求"。
//
// 低于 11 的 JVM 加载 Java 11 的 class（内嵌 jar 是 major 55）时会在**进 main
// 之前**就退出，stderr 形如：
//
//	Error: A JNI error has occurred, please check your installation and try again
//	Exception in thread "main" java.lang.UnsupportedClassVersionError: ...
//	        ... class file version 55.0 ... only recognizes ... up to 52.0
//
// 第一行没有任何信息量 —— 真机上用户就是只看到这一行才来问"为什么打不开"的
// （另一台只装了 Java 8 的电脑，见 MIGRATION §五 第 55 项）。所以要顺着第二行找。
func isJavaVersionError(msg string) bool {
	return strings.Contains(msg, "UnsupportedClassVersionError") ||
		strings.Contains(msg, "A JNI error has occurred")
}

// versionErrorDetail 从 stderr 里挑出真正说明问题的那一行
// （UnsupportedClassVersionError 那行，里面有"本机 class 版本上限"），
// 挑不到就退回前两行。压成单行，免得在 toast 里铺得太长。
func versionErrorDetail(msg string) string {
	for _, line := range strings.Split(msg, "\n") {
		if strings.Contains(line, "UnsupportedClassVersionError") {
			return strings.TrimSpace(line)
		}
	}
	lines := strings.Split(strings.TrimSpace(msg), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

// decompileFailure 把 JVM 的 stderr 翻译成用户能照做的提示。
//
// 单独拆出来是为了可单测 —— 这段"翻译"是用户唯一能看到的线索，不能只靠
// 真机上再撞一次来验证。
func decompileFailure(java, msg string) error {
	if isJavaVersionError(msg) {
		return fmt.Errorf(
			"反编译失败：本机 Java 版本过低 —— 反编译器需要 Java 11 或更高。\n"+
				"当前使用的 java：%s\n"+
				"请安装 Java 11+ 并让它排在 PATH 前面，或把 JAVA_HOME 指向 Java 11+ 的目录后重启本程序。\n"+
				"（JVM 原话：%s）",
			java, versionErrorDetail(msg))
	}
	return fmt.Errorf("反编译失败：%s", firstLine(msg))
}

// writeClass 把字节写进临时输入目录，返回路径。
// 文件名只求唯一可辨，fernflower 认的是文件内容。
func writeClass(dir string, n int, b []byte) (string, error) {
	p := filepath.Join(dir, "c"+strconv.Itoa(n)+".class")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return "", fmt.Errorf("写出临时 class 失败：%w", err)
	}
	return p, nil
}

// readResult 读回反编译产物。
//
// 正常情况下输出目录里只有一个 `<简单类名>.java`。真出现多个时（例如传入的
// 若干类之间并无内部类关系），优先按主类名取，取不到再退回"唯一那个" ——
// 但绝不猜：多个且都对不上就报错，让问题暴露出来。
func readResult(outDir, simple string) (string, error) {
	want := filepath.Join(outDir, simple+".java")
	if b, err := os.ReadFile(want); err == nil {
		return string(b), nil
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return "", fmt.Errorf("读取反编译结果失败：%w", err)
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".java") {
			found = append(found, e.Name())
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("反编译没有产出源码（预期 %s.java）", simple)
	case 1:
		b, err := os.ReadFile(filepath.Join(outDir, found[0]))
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("反编译产出了多个源码文件（%s），无法确定哪个是主类",
			strings.Join(found, ", "))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
