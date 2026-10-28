package decompile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// goldenDir 是反编译黄金基准目录，由 `_extract/tools/make_decomp_golden.sh` 生成。
//
// 从包目录（internal/decompile）上溯三级到项目根：patch-assistant-go → 根。
var goldenDir = filepath.Join("..", "..", "..", "_extract", "tools", "decomp_golden")

// TestMainClassName 用真实 class 字节验证类名解析。
//
// 这条不是"顺便测一下"：fernflower 的输出文件名取自字节里的类名，
// 解析错了就会去读一个不存在的文件 —— 而且报出来的会是"没有产出源码"，
// 看起来像反编译失败，实际是这里错了。
func TestMainClassName(t *testing.T) {
	cases := []struct{ file, want string }{
		{"ChineseProbe.class", "ChineseProbe"},
		{"AbsClassDecompiler.class", "cn/cpoet/patch/assistant/fernflower/AbsClassDecompiler"},
		{"NumberField$NumberFormatter.class", "cn/cpoet/patch/assistant/control/form/NumberField$NumberFormatter"},
		{"IConConst.class", "cn/cpoet/patch/assistant/constant/IConConst"},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(goldenDir, "classes", c.file))
		if err != nil {
			t.Skipf("基准不存在，跳过：%v", err)
		}
		got, err := MainClassName(b)
		if err != nil {
			t.Errorf("%s: 解析失败: %v", c.file, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: 类名 = %q，期望 %q", c.file, got, c.want)
		}
	}
}

// TestMainClassNameRejectsBadInput 非法输入要报错而不是 panic 或返回空串。
func TestMainClassNameRejectsBadInput(t *testing.T) {
	cases := map[string][]byte{
		"空":        {},
		"太短":       {0xCA, 0xFE},
		"magic 不对": {0x50, 0x4B, 0x03, 0x04, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	for name, b := range cases {
		if got, err := MainClassName(b); err == nil {
			t.Errorf("%s: 期望报错，却得到 %q", name, got)
		}
	}
}

// TestDecompileFailure_JavaTooOld 钉住"本机 Java 版本过低"这条翻译。
//
// 背景（真机）：另一台只装了 Java 8 的电脑上双击 .class 报的是
// `反编译失败：Error: A JNI error has occurred, please check your installation
// and try again` —— JVM 第一行就是这个，用户完全不知道该怎么办。
// 内嵌 jar 是 Java 11（class major 55），低于 11 的 JVM 进不到 main。
// 真正的原因在 stderr 第二行（UnsupportedClassVersionError），必须捞出来。
func TestDecompileFailure_JavaTooOld(t *testing.T) {
	stderr := "Error: A JNI error has occurred, please check your installation and try again\n" +
		`Exception in thread "main" java.lang.UnsupportedClassVersionError: ` +
		"org/jetbrains/java/decompiler/main/decompiler/ConsoleDecompiler has been compiled " +
		"by a more recent version of the Java Runtime (class file version 55.0), this version " +
		"of the Java Runtime only recognizes class file versions up to 52.0\n" +
		"        at java.base/java.lang.ClassLoader.defineClass1(Native Method)"

	got := decompileFailure(`C:\Program Files\Java\jre1.8.0_301\bin\java.exe`, stderr).Error()
	for _, want := range []string{
		"Java 11 或更高", // 版本要求写清楚
		`C:\Program Files\Java\jre1.8.0_301\bin\java.exe`, // 用的是哪个 java
		"UnsupportedClassVersionError",                    // 把 JVM 真正那句话带出来
		"up to 52.0",                                      // 里面有"本机 class 版本上限"
		"JAVA_HOME",                                       // 给出可照做的动作
	} {
		if !strings.Contains(got, want) {
			t.Errorf("提示里缺少 %q：\n%s", want, got)
		}
	}
}

// TestDecompileFailure_JNIWithoutDetail 只有那句 JNI 提示、没有第二行时也要走这条分支。
func TestDecompileFailure_JNIWithoutDetail(t *testing.T) {
	got := decompileFailure("java", "Error: A JNI error has occurred, please check your installation and try again").Error()
	if !strings.Contains(got, "Java 11 或更高") {
		t.Errorf("应翻译成 Java 版本提示，得到：%s", got)
	}
}

// TestDecompileFailure_Generic 其它失败照旧只取第一行（别把整段堆栈倒给用户）。
func TestDecompileFailure_Generic(t *testing.T) {
	got := decompileFailure("java", "boom\nat Foo.bar(Foo.java:1)\nat Baz.qux(Baz.java:2)").Error()
	if got != "反编译失败：boom" {
		t.Errorf("普通失败应只保留第一行，得到：%q", got)
	}
}

// TestDecompileMatchesJavaGolden 是核心：与**原版 Java** 的反编译结果逐字节比对。
//
// 基准由原版 `SingleClassDecompiler` 产出（见 make_decomp_golden.sh），
// 所以这里验证的是"Go 版与 Java 版是否同行为"，而不是"是否符合我的期望"。
//
// 本机没有 Java 时跳过 —— 反编译本来就依赖 JRE，这是预期内的前置条件，
// 不是测试的缺陷。
func TestDecompileMatchesJavaGolden(t *testing.T) {
	if !Available() {
		t.Skip("本机没有 Java，跳过反编译基准")
	}

	raw, err := os.ReadFile(filepath.Join(goldenDir, "manifest.tsv"))
	if err != nil {
		t.Skipf("基准不存在（先跑 make_decomp_golden.sh）：%v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		t.Fatal("基准清单是空的")
	}

	for _, line := range lines {
		cols := strings.Split(strings.TrimSpace(line), "\t")
		if len(cols) < 2 {
			t.Errorf("清单行格式不对: %q", line)
			continue
		}
		name, files := cols[0], cols[1:]

		t.Run(name, func(t *testing.T) {
			read := func(f string) []byte {
				b, err := os.ReadFile(filepath.Join(goldenDir, "classes", f))
				if err != nil {
					t.Fatalf("读样本 %s 失败: %v", f, err)
				}
				return b
			}

			main := read(files[0])
			var inners [][]byte
			for _, f := range files[1:] {
				inners = append(inners, read(f))
			}

			got, err := Decompile(main, inners)
			if err != nil {
				t.Fatalf("反编译失败: %v", err)
			}

			wantB, err := os.ReadFile(filepath.Join(goldenDir, name+".java"))
			if err != nil {
				t.Fatalf("读基准失败: %v", err)
			}
			want := string(wantB)

			if got == want {
				return
			}
			// 不一致时给出可读的定位信息，别只丢两个大字符串
			gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
			t.Errorf("与 Java 基准不一致：Go %d 行 / Java %d 行", len(gl), len(wl))
			for i := 0; i < len(gl) && i < len(wl); i++ {
				if gl[i] != wl[i] {
					t.Errorf("  第 %d 行起：\n    Go  : %q\n    Java: %q", i+1, gl[i], wl[i])
					break
				}
			}
		})
	}
}

// TestFindJavaPrefersBundled 钉住本次改动的核心语义：**内置运行时优先于 JAVA_HOME**。
//
// 机器上装了 JDK 8 也不该影响反编译 —— 那正是"双击 class 打不开"的成因
// （MIGRATION §五 55）。这里故意把 JAVA_HOME 指到一个不存在的地方，
// 证明 FindJava 根本没去看它。
func TestFindJavaPrefersBundled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("内置运行时仅 Windows 有")
	}
	t.Setenv("JAVA_HOME", `D:\definitely\not\a\jdk`)
	p, err := FindJava()
	if err != nil {
		t.Fatalf("FindJava 失败：%v", err)
	}
	if !strings.Contains(p, "runtime-") {
		t.Errorf("应当优先用内置运行时，实际拿到 %s", p)
	}
}

// TestFindJavaFallsBackWhenBundledDisabled 钉住逃生口：跳过内置后要能退回本机 Java。
//
// 这条与上一条是一对 —— 上一条证明"默认走内置"，这条证明"能关掉"。
// 关掉之后本机没装 Java 才会报 ErrNoJava，所以两种结果都算通过。
func TestFindJavaFallsBackWhenBundledDisabled(t *testing.T) {
	t.Setenv("PATCH_ASSISTANT_NO_BUNDLED_JAVA", "1")
	p, err := FindJava()
	if err != nil {
		if err != ErrNoJava {
			t.Fatalf("应当返回 ErrNoJava，实际 %v", err)
		}
		return
	}
	if strings.Contains(p, "runtime-") {
		t.Errorf("已要求跳过内置运行时，却仍拿到 %s", p)
	}
}

// TestFindJavaWorksWithoutAnyJavaOnMachine 钉住整个内置运行时方案要解决的问题本身：
// **机器上一个 Java 都没有，反编译也要能用**（MIGRATION §五 57）。
//
// 把 JAVA_HOME 清空、PATH 也清空，模拟"那台只装 JDK 8（甚至什么都没装）的电脑"。
// 这条比 TestFindJavaPrefersBundled 更狠 —— 那条只是把 JAVA_HOME 指错，
// 本机的 PATH 上仍然有 java 兜底；这条把兜底也拿掉。
func TestFindJavaWorksWithoutAnyJavaOnMachine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("内置运行时仅 Windows 有")
	}
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", "")
	p, err := FindJava()
	if err != nil {
		t.Fatalf("清空 JAVA_HOME 与 PATH 后仍应拿到内置 java，实际失败：%v", err)
	}
	if !strings.Contains(p, "runtime-") {
		t.Errorf("应当用内置运行时，实际 %s", p)
	}
}

// TestDecompileWorksWithoutAnyJavaOnMachine 是端到端的那一条：
// 清空 JAVA_HOME 与 PATH 之后，**真的把 class 反编译出源码来**。
//
// 前面几条只证明"路径找得到"，这条证明"拿这个路径真的能干活" ——
// 少了它，"内置运行时可用"就只是个路径断言。
func TestDecompileWorksWithoutAnyJavaOnMachine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("内置运行时仅 Windows 有")
	}
	main, err := os.ReadFile(filepath.Join(goldenDir, "classes", "ChineseProbe.class"))
	if err != nil {
		t.Skipf("黄金样本缺失，跳过：%v", err)
	}
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", "")

	src, err := Decompile(main, nil)
	if err != nil {
		t.Fatalf("清空 JAVA_HOME 与 PATH 后反编译失败：%v", err)
	}
	if !strings.Contains(src, "class ChineseProbe") {
		t.Errorf("反编译结果里没有类声明，首行 = %q", firstLine(src))
	}
}
