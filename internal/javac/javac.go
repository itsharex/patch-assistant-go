// Package javac 调用 JDK 的 javac，把 Java 源码编译成 .class。
//
// javac 从**内置运行时**取（internal/javaruntime 内含 jdk.compiler），所以机器上
// 没装 JDK 也能用；装了的机器可以用 JAVA_HOME 覆盖。见 FindJavac。
//
// 为什么需要它：class 里存的是字节码，反编译出来的源码要变回 class 只能靠
// **重新编译**。原版没有这个能力（它的「编辑模式」被作者整段注释掉了），
// 这是随「编辑包内文件内容」一起新增的刻意增强，见 MIGRATION.md。
//
// 三个必须做对的地方：
//
//  1. **锁 class 版本。** 拿 JDK 21 直接编译出来是 class 版本 65；原包如果跑在
//     Java 8 上，加载时直接 UnsupportedClassVersionError。所以先读原 class 的
//     major version，再让 javac 编出同一个版本 —— 用哪组参数**取决于本机 javac
//     的版本**：JDK 9+ 用 `--release N`；JDK 8 上 `--release` 这个选项还不存在
//     （javac 直接报 `invalid flag: --release`），而它默认产出就是 class 8，正好
//     是要的结果，所以什么都不传。见 versionArgs。
//  2. **报错信息的编码。** javac 的诊断输出走的是**控制台编码**（不是
//     file.encoding），中文 Windows 上是 GBK —— 当 UTF-8 读会整段乱码。
//     用 `-J-Dsun.stderr.encoding=UTF-8` 把它拧成 UTF-8，读回来再兜一层
//     GB18030，防止某些 JVM 忽略这个属性。
//  3. **`-proc:none`。** 依赖 jar 里可能带着注解处理器（Lombok 之类），
//     一旦被 javac 的 ServiceLoader 捡到就会参与编译，行为不可预期。
//     反编译出来的源码里 Lombok 生成的方法已经是显式的，不需要任何处理器。
//
// 已知的**能力边界**（实测数据见 MIGRATION.md）：反编译 → 重新编译不是
// 百分百成功。真实库上约 87% 的类能编回去，失败的常见原因是反编译本身
// 的缺陷 —— 局部变量名冲突、泛型桥接方法没生成、缺依赖、匿名内部类被
// 渲染成非法标识符。所以调用方必须把 javac 的原始报错**原样**给用户看，
// 而不是笼统说一句"编译失败"。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package javac

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"cn.cpoet.patch.assistant/internal/javaruntime"
)

// ErrNoJavac 表示既没有内置运行时、本机也找不到 javac。
//
// 内置运行时带 `jdk.compiler`（jlink 会一并生成 `bin/javac.exe`），所以正常时
// **这条永远不会触发**。能走到这里只有两种情况：运行时解压失败（磁盘满 / 缓存
// 目录不可写），或者非 Windows 平台上本机只装了 JRE 没装 JDK。
//
// 与 decompile.ErrNoJava 分开：反编译只要 JRE，编译要 **JDK**。装了 JRE 没装
// JDK 是很常见的情况，所以错误信息要说清"缺的是 JDK，不是 Java"。
var ErrNoJavac = errors.New(
	"未找到 javac，无法把源码编译成 .class。\n" +
		"程序自带的 Java 运行时没能解压出来，本机也没有装 JDK。\n" +
		"请检查磁盘空间与缓存目录的写入权限后重启本程序；" +
		"或安装 JDK（8 或更高，且不低于要编译的 class 版本），" +
		"也可以把 JAVA_HOME 指向 JDK 目录（不是 JRE 目录）")

// 默认超时。单个类通常 1–3 秒（含 JVM 冷启动），60 秒给超大类和慢磁盘留余量。
const defaultTimeout = 60 * time.Second

// FindJavac 定位 javac 可执行文件。
//
// 顺序：**内置运行时优先** → JAVA_HOME → PATH。
//
// 与 decompile.FindJava 的查找顺序**必须保持一致** —— 两个工具要落在同一个 JDK 上，
// 否则会出现"反编译用 JDK 8、编译用 JDK 21"这种版本错配。
func FindJavac() (string, error) {
	if p, err := javaruntime.Javac(); err == nil {
		return p, nil
	}
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		p := filepath.Join(home, "bin", "javac"+exeExt)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("javac"); err == nil {
		return p, nil
	}
	return "", ErrNoJavac
}

// Available 报告能不能编译。
//
// 内置运行时正常时**恒为 true**（它带 jdk.compiler）。
func Available() bool {
	_, err := FindJavac()
	return err == nil
}

// JavacMajor 返回本机 javac 的主版本号（如 21）。
//
// 有两个用途：
//
//  1. **判断"能不能编出原 class 那个版本"。** 原 class 是 Java 17（版本 61）而
//     本机只有 JDK 8 时，必须**提前报错**而不是硬编 —— 硬编会降级成版本 52，
//     源码里若用了 Java 9+ 的 API（`List.of` 之类）会编译失败，就算编过了，
//     产出的字节码语义也可能与原版不同。
//  2. **决定用哪组参数表达目标版本。** `--release` 是 JDK 9 才引入的选项，
//     JDK 8 上要换成"什么都不传"。见 versionArgs。
//
// 每次调用都会起一个 JVM 进程（`javac -version`），别放在逐节点渲染的路径上。
func JavacMajor() (int, error) {
	javac, err := FindJavac()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, javac, "-version")
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("执行 javac -version 失败：%w", err)
	}
	return parseJavacMajor(string(out))
}

// parseJavacMajor 从 `javac -version` 的输出里取主版本号。
//
// 两种编号方式都要认 —— 这是本函数唯一容易写错的地方：
//
//	"javac 21.0.9"    → 21   （JDK 9 起：主版本就是第一段）
//	"javac 1.8.0_251" → 8    （JDK 8 及更早：主版本藏在**第二段**）
//
// 只取第一段会把 JDK 8 读成 **1**。后果不是"少个功能"，而是 `Compile` 里
// "能不能编出原 class 版本"那道守卫会判成 `8 > 1`，于是**拒绝编译 Java 8 的类**，
// 还弹出一句"本机 JDK 只有 1"的胡话（真机撞过，见 MIGRATION.md §五 第 56 项）。
func parseJavacMajor(out string) (int, error) {
	raw := strings.TrimSpace(out)
	s := strings.TrimPrefix(raw, "javac ")
	// 切掉 "_251" / "-ea" / "+9" 这类构建后缀，只留点分的版本号。
	if i := strings.IndexAny(s, "_-+ "); i >= 0 {
		s = s[:i]
	}
	fields := strings.Split(s, ".")
	if len(fields) == 0 || strings.TrimSpace(fields[0]) == "" {
		return 0, fmt.Errorf("无法从 javac -version 的输出里解析版本号：%q", raw)
	}
	// 1.8 → 主版本在第二段；其它（9、17、21）就是第一段。
	idx := 0
	if fields[0] == "1" && len(fields) >= 2 {
		idx = 1
	}
	n, err := strconv.Atoi(strings.TrimSpace(fields[idx]))
	if err != nil {
		return 0, fmt.Errorf("无法从 javac -version 的输出里解析版本号：%q", raw)
	}
	return n, nil
}

// Options 是编译参数。
type Options struct {
	// Source Java 源码文本（UTF-8）。
	Source string
	// FileName 源文件名，必须是 `<顶层类名>.java` —— javac 对 public 类
	// 有这个要求，文件名不对会直接报错。
	FileName string
	// Classpath 依赖路径，可以是 jar 或目录。内部按平台分隔符拼接后交给 `-cp`。
	Classpath []string
	// Release 目标 Java 版本（8、11、17、21 …）。0 表示不指定，用 javac 默认。
	//
	// 调用方应当用 ReleaseFor(原 class 的 major version) 算出来，
	// 否则产出的 class 版本与原件不一致。
	//
	// 注意这是"**要编成哪个版本**"，不是"用什么命令实现"：本机 JDK 是 8 时
	// 不会传 `--release`（那个选项 JDK 8 不认），而是靠 JDK 8 的默认产出
	// （正好是 class 8）达成同样效果。见 versionArgs。
	Release int
	// Timeout 超时，0 用默认值。
	Timeout time.Duration
}

// Result 是编译产物。
type Result struct {
	// Classes 文件名 -> 字节，例如 `Foo.class`、`Foo$Inner.class`、`Foo$1.class`。
	//
	// 键是**文件名**而不是包路径：调用方要拿它跟树节点名（`Foo$Inner.class`）对齐。
	Classes map[string][]byte
}

// Compile 编译源码，返回产出的所有 class 文件。
//
// 编译失败时返回的 error 里带着 javac 的**原始诊断信息**（已转成 UTF-8）——
// 那些信息是用户唯一能用来定位问题的线索，不要吞掉或改写成笼统的一句话。
func Compile(opt Options) (*Result, error) {
	if strings.TrimSpace(opt.Source) == "" {
		return nil, errors.New("源码是空的，没有可编译的内容")
	}
	if strings.TrimSpace(opt.FileName) == "" {
		return nil, errors.New("缺少源文件名")
	}

	javac, err := FindJavac()
	if err != nil {
		return nil, err
	}

	// 目标版本的表达方式按本机 javac 的版本来选（`--release` 只有 JDK 9+ 认）。
	var releaseArgs []string
	if opt.Release > 0 {
		major, err := JavacMajor()
		if err != nil {
			return nil, err
		}
		if opt.Release > major {
			return nil, fmt.Errorf(
				"原 class 是 Java %d，但本机 JDK 只有 %d，编不出同版本的字节码。\n"+
					"请换成 JDK %d 或更高版本（设置 JAVA_HOME 指向它）后重试",
				opt.Release, major, opt.Release)
		}
		releaseArgs = versionArgs(opt.Release, major)
	}

	dir, err := os.MkdirTemp("", "pa-javac-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败：%w", err)
	}
	defer os.RemoveAll(dir)

	srcPath := filepath.Join(dir, filepath.Base(opt.FileName))
	// 源码一律按 UTF-8 落盘：编辑器里的文本就是 UTF-8，配合下面的
	// `-encoding UTF-8` 才不会把中文注释/字符串编错。
	if err := os.WriteFile(srcPath, []byte(opt.Source), 0o644); err != nil {
		return nil, fmt.Errorf("写出源文件失败：%w", err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	args := []string{
		// 诊断信息走控制台编码（中文 Windows 上是 GBK），拧成 UTF-8。
		// 不加这个的话，下面读回来的中文报错全是乱码。
		"-J-Dsun.stderr.encoding=UTF-8",
		"-J-Dsun.stdout.encoding=UTF-8",
		"-nowarn",
		// 禁掉注解处理：依赖 jar 里可能带着 Lombok 之类的处理器，
		// 被 ServiceLoader 捡到就会参与编译。反编译出的源码已经是展开后的，
		// 不需要任何处理器。
		"-proc:none",
		"-encoding", "UTF-8",
		// 带上调试信息。多数生产 jar 是带 `-g` 编的（Maven/Gradle 默认），
		// 不带的话补丁后线上出异常就看不到行号 —— 排查成本远大于那点体积。
		"-g",
		"-d", outDir,
	}
	if len(opt.Classpath) > 0 {
		args = append(args, "-cp", strings.Join(opt.Classpath, string(os.PathListSeparator)))
	}
	args = append(args, releaseArgs...)
	args = append(args, srcPath)

	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, javac, args...)
	hideWindow(cmd) // Windows 上不弹控制台黑框
	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("编译超时（%s）", timeout)
		}
		msg := decodeDiag(stderr.Bytes())
		if strings.TrimSpace(msg) == "" {
			msg = decodeDiag(stdout.Bytes())
		}
		if strings.TrimSpace(msg) == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("编译失败：\n%s", strings.TrimSpace(msg))
	}

	classes, err := collectClasses(outDir)
	if err != nil {
		return nil, err
	}
	if len(classes) == 0 {
		return nil, errors.New("javac 报告成功但没有产出任何 .class")
	}
	return &Result{Classes: classes}, nil
}

// versionArgs 决定用哪组参数告诉 javac"要编成哪个 class 版本"。
//
// 为什么不能一律 `--release N`：`--release` 是 **JDK 9 才引入**的选项
// （JEP 247），JDK 8 的 javac 见到它会直接 `javac: invalid flag: --release`，
// 编译根本起不来 —— 于是"装了 JDK 8 的机器上完全编不了"（见 MIGRATION.md §五 第 56 项）。
//
// JDK 8 上的替代做法是**什么都不传**：JDK 8 的 javac 只能产出 class 8
// （major 52），而调用方给的 release 必然 ≥ 8（`ReleaseFor` 把低于 8 的抬到 8），
// 又必然 ≤ javacMajor（`Compile` 里那道守卫拦掉了更大的），所以走到这里时
// release 唯一可能的值就是 8 —— 正好等于 JDK 8 的默认产出。
//
// 不用 `-source`/`-target` 顶替是因为它们只对"用了新语法"报警告、**拦不住
// 引用新版本的 API**，语义比 `--release` 弱；JDK 8 这条路能省就省。
//
// javacMajor < 8 的组合到不了这里：调用方会先因 `opt.Release > javacMajor` 报错。
func versionArgs(release, javacMajor int) []string {
	if release <= 0 || javacMajor < 9 {
		return nil
	}
	return []string{"--release", strconv.Itoa(release)}
}

// collectClasses 收 outDir 下所有 .class，键用**文件名**。
//
// 用文件名而不是相对路径：内部类在树里就是 `Foo$Inner.class` 这种平铺名字，
// 两边要能直接对上。
func collectClasses(outDir string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	err := filepath.WalkDir(outDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".class") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[d.Name()] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("收集编译产物失败：%w", err)
	}
	return out, nil
}

// decodeDiag 把 javac 的诊断输出转成 UTF-8。
//
// 正常情况下上面那个 `-J-Dsun.stderr.encoding=UTF-8` 已经让它输出 UTF-8，
// 这里再兜一层：万一某个 JVM 忽略了那个属性（非 Windows 或旧版本），
// 输出会落回平台编码，中文 Windows 上就是 GBK —— 按 GB18030 解回来。
// 纯 ASCII 的英文报错两种解法的结果完全一样，所以这个兜底不会帮倒忙。
func decodeDiag(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if isUTF8(b) {
		return string(b)
	}
	if s, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), b); err == nil {
		return string(s)
	}
	return string(b)
}

// isUTF8 判断字节是不是合法 UTF-8（非法序列用 utf8.Valid 判，这里内联是为了
// 少 import 一个包，逻辑完全一致）。
func isUTF8(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 || c < 0xC2 {
				return false
			}
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case c&0xF8 == 0xF0:
			if i+3 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 ||
				b[i+3]&0xC0 != 0x80 || c > 0xF4 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}
