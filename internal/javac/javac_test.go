package javac

import (
	"runtime"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// requireJavac 在没装 JDK 时跳过 —— 这是**可预期的前置条件缺失**，不是缺陷。
func requireJavac(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("本机没有 javac（需要 JDK 而不是 JRE），跳过编译相关用例")
	}
}

// TestFindJavacWorksWithoutAnyJavaOnMachine 与 decompile 侧同名测试成对：
// **机器上一个 Java 都没有时，「编译为 .class」也要能用**。
//
// 这正是"内置 jdk.compiler 而不是纯 JRE"的理由 —— JRE 里没有 javac，
// 只内置 JRE 的话这条会失败（MIGRATION §五 57）。
func TestFindJavacWorksWithoutAnyJavaOnMachine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("内置运行时仅 Windows 有")
	}
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", "")
	p, err := FindJavac()
	if err != nil {
		t.Fatalf("清空 JAVA_HOME 与 PATH 后仍应拿到内置 javac，实际失败：%v", err)
	}
	if !strings.Contains(p, "runtime-") {
		t.Errorf("应当用内置运行时，实际 %s", p)
	}
}

// TestFindJavacFallsBackWhenBundledDisabled 钉住逃生口：
// 跳过内置后要能退回本机 javac（本机也没有就返回 ErrNoJavac）。
func TestFindJavacFallsBackWhenBundledDisabled(t *testing.T) {
	t.Setenv("PATCH_ASSISTANT_NO_BUNDLED_JAVA", "1")
	p, err := FindJavac()
	if err != nil {
		if err != ErrNoJavac {
			t.Fatalf("应当返回 ErrNoJavac，实际 %v", err)
		}
		return
	}
	if strings.Contains(p, "runtime-") {
		t.Errorf("已要求跳过内置运行时，却仍拿到 %s", p)
	}
}

// TestReleaseFor 钉住 class 版本到 --release 的映射。
//
// 这条看着琐碎，但它是"补丁打上去之后应用还能不能启动"的分水岭：
// 映射错了就会编出比原件版本更高的 class，运行时直接 UnsupportedClassVersionError。
func TestReleaseFor(t *testing.T) {
	cases := []struct {
		major, want int
		why         string
	}{
		{52, 8, "Java 8"},
		{53, 9, "Java 9"},
		{55, 11, "Java 11"},
		{61, 17, "Java 17"},
		{65, 21, "Java 21"},
		{45, 8, "Java 1.1 太老，抬到 8（JDK 21 的 --release 最低只认 8）"},
		{51, 8, "Java 7 同样抬到 8"},
		{50, 8, "Java 6 同样抬到 8"},
	}
	for _, c := range cases {
		if got := ReleaseFor(c.major); got != c.want {
			t.Errorf("ReleaseFor(%d) = %d，期望 %d（%s）", c.major, got, c.want, c.why)
		}
	}
}

// TestVersionArgs 钉住"用哪组参数表达目标版本"。
//
// 这条守的是 **JDK 8 机器上的可用性**：`--release` 是 JDK 9 才引入的选项
// （JEP 247），JDK 8 的 javac 见到它会直接 `javac: invalid flag: --release`，
// 编译根本起不来（真机撞过，见 MIGRATION.md §五 第 56 项）。而 JDK 8 的默认
// 产出就是 class 8，所以那种情况下"不传"才是对的。
func TestVersionArgs(t *testing.T) {
	cases := []struct {
		release, javacMajor int
		want                []string
		why                 string
	}{
		{8, 21, []string{"--release", "8"}, "JDK 21 编 Java 8 目标：必须锁版本，否则产出 class 65"},
		{17, 21, []string{"--release", "17"}, "同上，目标版本跟着原 class 走"},
		{8, 9, []string{"--release", "8"}, "--release 从 JDK 9 起才有"},
		{8, 8, nil, "JDK 8 不认 --release；它的默认产出正好就是 class 8"},
		{0, 21, nil, "release=0 表示不指定版本，交给 javac 默认"},
		{0, 8, nil, "同上"},
	}
	for _, c := range cases {
		got := versionArgs(c.release, c.javacMajor)
		if !sameArgs(got, c.want) {
			t.Errorf("versionArgs(%d, %d) = %v，期望 %v（%s）",
				c.release, c.javacMajor, got, c.want, c.why)
		}
	}
}

func sameArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseJavacMajor 钉住 `javac -version` 输出的解析。
//
// 关键是 **JDK 8 的老式编号**：它输出 `javac 1.8.0_251`，主版本在**第二段**。
// 只取第一段会得到 1，于是 Compile 里的守卫判成 `8 > 1`，把 Java 8 的目标
// 当成"本机 JDK 太老"直接拒掉 —— 真机（本机 `jdk1.8.0_251`）实测撞到过，
// 见 MIGRATION.md §五 第 56 项。
func TestParseJavacMajor(t *testing.T) {
	cases := []struct {
		out  string
		want int
		ok   bool
	}{
		{"javac 21.0.9\n", 21, true},
		{"javac 17\n", 17, true},
		{"javac 9\n", 9, true},
		{"javac 11.0.2+9\n", 11, true},
		{"javac 1.8.0_251\n", 8, true}, // 本机 JDK 8 的真实输出
		{"javac 1.8\n", 8, true},
		{"javac 1.7.0_80\n", 7, true},
		{"", 0, false},
		{"javac\n", 0, false},
		{"javac x.y\n", 0, false},
	}
	for _, c := range cases {
		got, err := parseJavacMajor(c.out)
		if c.ok && err != nil {
			t.Errorf("parseJavacMajor(%q) 意外失败: %v", c.out, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("parseJavacMajor(%q) 应当报错，实际返回 %d", c.out, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("parseJavacMajor(%q) = %d，期望 %d", c.out, got, c.want)
		}
	}
}

// TestClassVersion 钉住 class 头的解析。
func TestClassVersion(t *testing.T) {
	// 造一个最小的 class 头：CAFEBABE + minor 0 + major 52
	b := []byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00, 0x00, 0x00, 0x34}
	v, err := ClassVersion(b)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if v != 52 {
		t.Errorf("版本 = %d，期望 52", v)
	}

	// major 65（Java 21）：0x41
	b[6], b[7] = 0x00, 0x41
	if v, err := ClassVersion(b); err != nil || v != 65 {
		t.Errorf("版本 = %d (err=%v)，期望 65", v, err)
	}

	// 魔数不对
	if _, err := ClassVersion([]byte{0x50, 0x4B, 0x03, 0x04, 0x00, 0x00, 0x00, 0x34}); err == nil {
		t.Error("zip 的 PK 头不该被当成 class")
	}
	// 太短
	if _, err := ClassVersion([]byte{0xCA, 0xFE}); err == nil {
		t.Error("不足 8 字节应当报错，不能越界读")
	}
}

// TestCompileSimpleClass 走通一次真实编译。
func TestCompileSimpleClass(t *testing.T) {
	requireJavac(t)

	src := "public class Hello {\n" +
		"    public static String greet() { return \"你好\"; }\n" +
		"}\n"
	res, err := Compile(Options{Source: src, FileName: "Hello.java"})
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	b, ok := res.Classes["Hello.class"]
	if !ok {
		t.Fatalf("没有产出 Hello.class，实际产出 = %v", keys(res.Classes))
	}
	if v, err := ClassVersion(b); err != nil || v < 52 {
		t.Errorf("产出的 class 版本 = %d (err=%v)，至少应当是 52", v, err)
	}
}

// TestCompileHonorsRelease 钉住"锁版本"这件事真的生效。
//
// 这是本包存在的主要理由：不锁版本的话，用 JDK 21 编出来的类是版本 65，
// 原包跑在 Java 8 上直接加载不了。
func TestCompileHonorsRelease(t *testing.T) {
	requireJavac(t)
	major, err := JavacMajor()
	if err != nil {
		t.Fatalf("取 javac 版本失败: %v", err)
	}
	if major < 8 {
		t.Skipf("本机 JDK %d 太老，跳过", major)
	}

	res, err := Compile(Options{
		Source:   "public class V { public int f() { return 1; } }\n",
		FileName: "V.java",
		Release:  8,
	})
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	v, err := ClassVersion(res.Classes["V.class"])
	if err != nil {
		t.Fatalf("解析产出失败: %v", err)
	}
	if v != 52 {
		t.Errorf("--release 8 产出的 class 版本 = %d，期望 52（Java 8）", v)
	}
}

// TestCompileWithoutReleaseUsesJavacDefault 钉住 release=0 的语义：不传版本参数，
// 产出本机 javac 的默认版本（major + 44）。
//
// 与上一条是一对：上一条证明"给了 release 就锁住"，这条证明"没给就放默认"。
// 两条都在本机 JDK 上跑；JDK 8 那条分支只能靠 TestVersionArgs 覆盖。
func TestCompileWithoutReleaseUsesJavacDefault(t *testing.T) {
	requireJavac(t)
	major, err := JavacMajor()
	if err != nil {
		t.Fatalf("取 javac 版本失败: %v", err)
	}

	res, err := Compile(Options{
		Source:   "public class D { public int f() { return 1; } }\n",
		FileName: "D.java",
	})
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	v, err := ClassVersion(res.Classes["D.class"])
	if err != nil {
		t.Fatalf("解析产出失败: %v", err)
	}
	if want := major + 44; v != want {
		t.Errorf("不指定版本时产出的 class 版本 = %d，期望 %d（本机 javac %d 的默认）",
			v, want, major)
	}
}

// TestCompileRejectsReleaseNewerThanJavac 钉住"编不出同版本要提前报错"。
//
// 硬编会降级成低版本：源码里若用了高版本 API 就会编译失败，
// 就算编过了语义也可能与原版不同。宁可不做，也不能悄悄降级。
func TestCompileRejectsReleaseNewerThanJavac(t *testing.T) {
	requireJavac(t)
	major, err := JavacMajor()
	if err != nil {
		t.Fatalf("取 javac 版本失败: %v", err)
	}

	_, err = Compile(Options{
		Source:   "public class N { }\n",
		FileName: "N.java",
		Release:  major + 5,
	})
	if err == nil {
		t.Fatal("目标版本高于本机 JDK 时应当报错")
	}
	if !strings.Contains(err.Error(), "编不出同版本") {
		t.Errorf("报错应当说清「编不出同版本」，实际 = %q", err.Error())
	}
}

// TestCompileReportsJavacDiagnostics 钉住"javac 的报错要原样传出来"。
//
// 这是用户唯一能用来定位问题的线索 —— 换成笼统的"编译失败"等于把线索扔了。
// 同时钉住编码：中文 Windows 上 javac 的报错是 GBK，没处理好这里会是乱码。
func TestCompileReportsJavacDiagnostics(t *testing.T) {
	requireJavac(t)

	_, err := Compile(Options{
		Source:   "public class Bad { void f() { int x = \"字符串不能赋给 int\"; } }\n",
		FileName: "Bad.java",
	})
	if err == nil {
		t.Fatal("源码有类型错误时应当报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "编译失败") {
		t.Errorf("报错应当标明是编译失败，实际 = %q", msg)
	}
	if !strings.Contains(msg, "Bad.java") {
		t.Errorf("报错应当带上源文件名与位置，实际 = %q", msg)
	}
	// 诊断信息里应当有可读的中文，而不是 U+FFFD 或者乱码字节
	if strings.Contains(msg, "\uFFFD") {
		t.Errorf("诊断信息编码没转对，出现替换字符：%q", msg)
	}
}

// TestCompileMissingDependency 钉住"缺依赖时的报错能看懂"。
//
// 真实场景里这是最常见的失败：被编辑的类引用了一堆其它 jar 里的类型，
// 而 classpath 没给全。报错里必须出现那个找不到的符号名。
func TestCompileMissingDependency(t *testing.T) {
	requireJavac(t)

	_, err := Compile(Options{
		Source: "import com.no.such.pkg.Missing;\n" +
			"public class D { Missing m; }\n",
		FileName: "D.java",
	})
	if err == nil {
		t.Fatal("引用不存在的类时应当报错")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Errorf("报错里应当出现找不到的符号名，实际 = %q", err.Error())
	}
}

// TestCompileRejectsEmptySource 钉住入参校验。
func TestCompileRejectsEmptySource(t *testing.T) {
	if _, err := Compile(Options{Source: "   \n", FileName: "X.java"}); err == nil {
		t.Error("空源码应当直接报错，不必去起 JVM")
	}
	if _, err := Compile(Options{Source: "class X {}", FileName: ""}); err == nil {
		t.Error("缺文件名应当直接报错")
	}
}

// TestDecodeDiag 钉住诊断输出的编码兜底。
//
// javac 正常会按我们给的 -J-Dsun.stderr.encoding=UTF-8 输出；
// 但万一那个属性被忽略（旧 JVM / 非 Windows），输出会落回平台编码。
// 这时要按 GB18030 解回来，而不是丢一堆替换字符给用户。
func TestDecodeDiag(t *testing.T) {
	// ASCII 原样
	if got := decodeDiag([]byte("error: cannot find symbol")); got != "error: cannot find symbol" {
		t.Errorf("ASCII 被改动了: %q", got)
	}
	// UTF-8 原样
	utf8Msg := "错误: 找不到符号"
	if got := decodeDiag([]byte(utf8Msg)); got != utf8Msg {
		t.Errorf("UTF-8 被改动了: %q", got)
	}
	// GBK 转成 UTF-8
	gbk, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(utf8Msg))
	if err != nil {
		t.Fatalf("造 GBK 样本失败: %v", err)
	}
	if got := decodeDiag(gbk); got != utf8Msg {
		t.Errorf("GBK 兜底解码 = %q，期望 %q", got, utf8Msg)
	}
	// 空输入
	if got := decodeDiag(nil); got != "" {
		t.Errorf("空输入应当返回空串，实际 %q", got)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
