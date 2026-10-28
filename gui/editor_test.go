package main

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cn.cpoet.patch.assistant/internal/decompile"
	"cn.cpoet.patch.assistant/internal/javac"
	"cn.cpoet.patch.assistant/internal/tree"
)

// readZipEntry 从 zip 里读出一个条目的内容。
func readZipEntry(t *testing.T, zipPath, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", zipPath, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开条目 %s 失败: %v", name, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("读条目 %s 失败: %v", name, err)
		}
		return string(b)
	}
	t.Fatalf("%s 里没有条目 %s", zipPath, name)
	return ""
}

// decompClassDir 真 class 样本目录，取自反编译基准（_extract/tools/decomp_golden）。
//
// 从 gui 包上溯两级到项目根：gui → patch-assistant-go → patch-assistant-to-java。
const decompClassDir = "../../_extract/tools/decomp_golden/classes"

// editorFixtureZip 造一个编辑器测试用的补丁包：
// 一个纯文本文件、一个后缀像文本但内容是二进制的文件、两组 .class。
//
// 三组 class 各有分工：
//   - Foo.class      **假字节**（magic 对、内容不是合法 class），钉"反编译失败降级"
//   - Legacy.class   **真 class**（ChineseProbe），钉"默认给源码"
//   - RoundTrip.class + 两个内部类  **自包含的真 class**，钉"编译回 .class"
//
// 为什么编译那条非得单独造样本：ChineseProbe 虽然也是真 class，但它是
// 无包名的孤立类，编不编得回去说明不了什么；RoundTrip 带具名内部类与匿名类，
// 正好覆盖"编译产出要按名字写回对应树节点"这条链路，而且自包含 ——
// 不需要凑 classpath。
//
// 样本读不到时不放进 zip，相关测试自己 Skip。
func editorFixtureZip(t *testing.T) string {
	t.Helper()
	entries := map[string]string{
		"README.txt": "#\n",
		"notes.txt":  "hello world\nsecond line\n",
		// 后缀是 .txt，内容却带 NUL —— 现实中真的存在，必须按二进制对待
		"weird.txt": "abc\x00def",
		"Foo.class": "\xca\xfe\xba\xbe class bytes",
	}
	if b, err := os.ReadFile(filepath.Join(decompClassDir, "ChineseProbe.class")); err == nil {
		entries["Legacy.class"] = string(b)
	}
	for _, name := range roundTripClasses {
		if b, err := os.ReadFile(filepath.Join(decompClassDir, name)); err == nil {
			entries[name] = string(b)
		}
	}
	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, entries)
	return zipPath
}

// roundTripClasses 编译往返样本的三个 class，顺序按「主类在前」。
var roundTripClasses = []string{
	"RoundTrip.class", "RoundTrip$Inner.class", "RoundTrip$1.class",
}

// openEditorApp 打开应用包与编辑器夹具，返回 App。
func openEditorApp(t *testing.T) *App {
	t.Helper()
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(editorFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	return app
}

// TestOpenEditorTextMode 钉住文本文件走文本编辑。
func TestOpenEditorTextMode(t *testing.T) {
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "notes.txt"})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeText {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, editorModeText)
	}
	if v.Text != "hello world\nsecond line\n" {
		t.Errorf("文本内容 = %q", v.Text)
	}
	if v.Size != len("hello world\nsecond line\n") {
		t.Errorf("大小 = %d", v.Size)
	}
	if v.Edited {
		t.Error("没改过就不该带已修改标记")
	}
}

// TestOpenEditorBinaryByContentNotExt 钉住"二进制按内容判断，不只看后缀"。
//
// weird.txt 的后缀是 .txt、解析器也在，但内容里有 NUL —— 必须走十六进制，
// 否则按文本存回去会把那个 NUL 换成别的字节，文件当场损坏。
func TestOpenEditorBinaryByContentNotExt(t *testing.T) {
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "weird.txt"})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeHex {
		t.Fatalf("含 NUL 的文件模式 = %q，期望 %q", v.Mode, editorModeHex)
	}
	got, err := base64.StdEncoding.DecodeString(v.Base64)
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	if string(got) != "abc\x00def" {
		t.Errorf("字节 = %q，期望 %q", got, "abc\x00def")
	}
}

// TestOpenEditorClassUsesSourceMode 钉住 .class 默认给反编译源码。
//
// 点「编辑内容」时想看的是逻辑本身，而字节码"改"不出有意义的结果。
// 需要本机有 Java —— 反编译本来就依赖 JRE，没装时跳过（那是预期内的前置
// 条件，不是缺陷；降级路径由 TestOpenEditorClassFallsBackToHex 守着）。
func TestOpenEditorClassUsesSourceMode(t *testing.T) {
	if !decompile.Available() {
		t.Skip("本机没有 Java，跳过 class 源码模式")
	}
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "Legacy.class"})
	if err != nil {
		t.Skipf("真 class 样本不可用，跳过：%v", err)
	}
	if v.Mode != editorModeSource {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, editorModeSource)
	}
	if !strings.Contains(v.Text, "ChineseProbe") {
		t.Errorf("源码里应当有类名，实际前 120 字符 = %q", head(v.Text, 120))
	}
	if v.Lang != "java" {
		t.Errorf("语言 = %q，期望 java", v.Lang)
	}
	if v.Base64 != "" {
		t.Error("源码模式不该带 base64 字节")
	}
	if v.Size <= 0 {
		t.Errorf("大小 = %d，应当是 class 的字节数", v.Size)
	}
}

// TestOpenEditorClassAsBytesForcesHex 钉住「改字节」这条路。
//
// 有时目标只是替换一段字符串常量，那时十六进制比源码直接 —— 所以
// AsBytes 必须能压过"默认给源码"这条规则。
func TestOpenEditorClassAsBytesForcesHex(t *testing.T) {
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{
		Side: treeSidePatch, Path: "Foo.class", AsBytes: true,
	})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeHex {
		t.Fatalf("AsBytes 下的模式 = %q，期望 %q", v.Mode, editorModeHex)
	}
}

// TestOpenEditorClassFallsBackToHex 钉住"反编译不可用时降级"。
//
// Foo.class 是假字节（magic 对但内容不是合法 class），反编译必然失败。
// 这时**不能报错**：用户至少还能按字节改。与 OpenContent 对 ParserClass
// 的处理同口径 —— 前置条件缺失是降级，不是故障。
func TestOpenEditorClassFallsBackToHex(t *testing.T) {
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "Foo.class"})
	if err != nil {
		t.Fatalf("反编译失败不该让打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeHex {
		t.Fatalf("模式 = %q，期望降级到 %q", v.Mode, editorModeHex)
	}
}

// TestSaveContentRejectsSourceMode 钉住"源码不能存回包"。
//
// 把反编译出来的源码当字节写回去会把 class 写废 —— 而且当场看不出来，
// 要到运行时才炸。所以要明确拒绝，并引导用户走「另存为 .java」。
func TestSaveContentRejectsSourceMode(t *testing.T) {
	app := openEditorApp(t)

	_, err := app.SaveContent(SaveOptions{
		Side: treeSidePatch, Path: "Foo.class",
		Mode: editorModeSource, Text: "class X {}",
	})
	if err == nil {
		t.Fatal("源码模式不该能保存")
	}
	if !strings.Contains(err.Error(), "另存为") {
		t.Errorf("报错里应当引导「另存为 .java」，实际 = %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// 编译为 .class
// ---------------------------------------------------------------------------

// requireJavac 没装 JDK 时跳过 —— 与反编译那边一样，这是**可预期的前置条件
// 缺失**，不是测试的缺陷。注意编译要的是 JDK，光有 JRE 不够。
func requireJavac(t *testing.T) {
	t.Helper()
	if !javac.Available() {
		t.Skip("本机没有 javac（需要 JDK 而不是 JRE），跳过编译相关用例")
	}
}

// TestCompileAbilityFlags 钉住「编译为 .class」按钮摆不摆的三个条件。
//
// 这三个条件前端都拿不到（形态、是不是顶层类、本机有没有 javac），
// 所以由后端算好下发；算错了的表现是"按钮凭空消失且不给理由"。
func TestCompileAbilityFlags(t *testing.T) {
	requireJavac(t)
	app := openEditorApp(t)

	// 顶层真 class -> 能编译
	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "RoundTrip.class"})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeSource {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, editorModeSource)
	}
	if !v.CanCompile {
		t.Errorf("顶层 class 应当可编译，hint = %q", v.CompileHint)
	}
	if v.CompileHint == "" {
		t.Error("能编译时也该给一句说明（会做什么）")
	}

	// 内部类 -> 不能单独编译，且要说清原因
	iv, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "RoundTrip$Inner.class"})
	if err != nil {
		t.Fatalf("打开内部类失败: %v", err)
	}
	if iv.Mode == editorModeSource {
		if iv.CanCompile {
			t.Error("内部类不该可编译")
		}
		if !strings.Contains(iv.CompileHint, "外层类") {
			t.Errorf("内部类的说明应当指向外层类，实际 = %q", iv.CompileHint)
		}
	}

	// 十六进制形态（强制字节打开）不该有编译能力
	hv, err := app.OpenEditor(EditorOptions{
		Side: treeSidePatch, Path: "RoundTrip.class", AsBytes: true,
	})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if hv.CanCompile {
		t.Error("按字节打开的形态不该可编译")
	}
}

// TestCompileContentRoundTrip 是这条链路的核心：**改源码 -> 编译 -> 字节码真的变了**。
//
// 分四段钉：
//  1. 编译写回后节点内容变了，且改动真的进了字节码（搜得到新字符串）；
//  2. 产出 class 的版本与原样本一致（样本是 --release 8 编的，即版本 52）——
//     这条是"必须锁版本"的守门人，不锁的话 JDK 21 会编出版本 65；
//  3. 内部类节点也被一起写回（编辑的是外层类，改动会牵动内部类）；
//  4. 节点带上「已修改」标记，前端那一行才会出现星号。
func TestCompileContentRoundTrip(t *testing.T) {
	requireJavac(t)
	app := openEditorApp(t)

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "RoundTrip.class"})
	if err != nil {
		t.Fatalf("打开编辑器失败: %v", err)
	}
	if v.Mode != editorModeSource || !v.CanCompile {
		t.Skipf("样本不可用（mode=%q canCompile=%v）", v.Mode, v.CanCompile)
	}

	// 内部类必须是**这个节点的子级** —— checkCompiledSet 靠它算期望集合
	before := findNodeByPath(app.GetState().PatchTree, "RoundTrip.class")
	if before == nil {
		t.Fatal("找不到 RoundTrip.class 节点")
	}
	if len(before.Children) != 2 {
		t.Fatalf("RoundTrip.class 的内部类子节点数 = %d，期望 2（Inner 与匿名类）",
			len(before.Children))
	}

	edited := strings.Replace(v.Text, `"v1"`, `"v2-edited"`, 1)
	if edited == v.Text {
		t.Fatal(`源码里没有 "v1"，样本可能被改过`)
	}

	if _, err := app.CompileContent(CompileOptions{
		Side: treeSidePatch, Path: "RoundTrip.class", Text: edited,
	}); err != nil {
		t.Fatalf("编译失败: %v", err)
	}

	// 视图只用来确认「已修改」标记（前端那一行的星号就靠它）
	if v := findNodeByPath(app.GetState().PatchTree, "RoundTrip.class"); v == nil || !v.Edited {
		t.Error("编译写回后应当带「已修改」标记")
	}

	// 读真实字节要拿 tree.Node（视图类型没有 ContentBytes）
	n, err := app.patchNode("RoundTrip.class")
	if err != nil {
		t.Fatalf("取节点失败: %v", err)
	}
	b, err := n.ContentBytes()
	if err != nil {
		t.Fatalf("读节点内容失败: %v", err)
	}
	if !bytes.Contains(b, []byte("v2-edited")) {
		t.Error("改的字符串没进字节码 —— 写回的不是编译产物")
	}
	major, err := javac.ClassVersion(b)
	if err != nil {
		t.Fatalf("读产出 class 版本失败: %v", err)
	}
	if major != 52 {
		t.Errorf("产出 class 版本 = %d，期望 52（样本是 --release 8 编的）—— "+
			"不锁版本的话 JDK 21 会编出 65，原包跑在 Java 8 上直接加载不了", major)
	}

	// 内部类一起写回
	for _, name := range []string{"RoundTrip$Inner.class", "RoundTrip$1.class"} {
		c, err := app.patchNode(name)
		if err != nil {
			t.Errorf("树里找不到 %s: %v", name, err)
			continue
		}
		if !c.Edited() {
			t.Errorf("%s 应当也被写回并标记", name)
		}
		cb, err := c.ContentBytes()
		if err != nil {
			t.Errorf("读 %s 失败: %v", name, err)
			continue
		}
		if cm, err := javac.ClassVersion(cb); err != nil || cm != 52 {
			t.Errorf("%s 的 class 版本 = %d (err=%v)，期望 52", name, cm, err)
		}
	}
}

// TestCompileContentReportsJavacError 钉住"编译失败要把 javac 的报错带出来"。
//
// 用户唯一能定位问题的线索就是那段报错。吞掉它换成"编译失败"四个字，
// 等于让用户去猜。顺带钉住编码 —— 中文 Windows 上 javac 的报错是 GBK。
func TestCompileContentReportsJavacError(t *testing.T) {
	requireJavac(t)
	app := openEditorApp(t)

	bad := "public class RoundTrip {\n" +
		"    public static String greet() { return missingSymbol; }\n" +
		"}\n"
	_, err := app.CompileContent(CompileOptions{
		Side: treeSidePatch, Path: "RoundTrip.class", Text: bad,
	})
	if err == nil {
		t.Fatal("源码有错时应当报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "编译失败") {
		t.Errorf("报错应当标明是编译失败，实际 = %q", msg)
	}
	if !strings.Contains(msg, "missingSymbol") {
		t.Errorf("报错里应当出现出错的那个符号，实际 = %q", msg)
	}
	if strings.Contains(msg, "\uFFFD") {
		t.Errorf("javac 的报错编码没转对：%q", msg)
	}
}

// TestCompileContentRejectsNonClass 钉住"只有 .class 能编译"。
func TestCompileContentRejectsNonClass(t *testing.T) {
	app := openEditorApp(t)

	_, err := app.CompileContent(CompileOptions{
		Side: treeSidePatch, Path: "notes.txt", Text: "public class X {}",
	})
	if err == nil {
		t.Fatal("文本文件不该能编译成 class")
	}
	if !strings.Contains(err.Error(), ".class") {
		t.Errorf("报错应当说清只有 .class 能编译，实际 = %q", err.Error())
	}
}

// TestCompileContentRejectsInnerClass 钉住"内部类不能单独编译"。
//
// 内部类的源码本来就在外层类的文件里，单独喂给 javac 会得到一堆
// 莫名其妙的"找不到符号"，不如直接说清楚。
func TestCompileContentRejectsInnerClass(t *testing.T) {
	app := openEditorApp(t)

	_, err := app.CompileContent(CompileOptions{
		Side: treeSidePatch, Path: "RoundTrip$Inner.class", Text: "public class Inner {}",
	})
	if err == nil {
		t.Fatal("内部类不该能单独编译")
	}
	if !strings.Contains(err.Error(), "外层类") {
		t.Errorf("报错应当指向外层类，实际 = %q", err.Error())
	}
}

// TestCheckCompiledSet 钉住"产出集合与树节点必须一一对应"这条卡口。
//
// 不卡的话：多出来的类没有对应节点、写不进去（包内缺文件）；少掉的类会在
// 包里留一份**过期的旧 class**。两种都是"当场看不出、上线才炸"。
func TestCheckCompiledSet(t *testing.T) {
	node := tree.NewFile("Foo.class", "Foo.class", 0, time.Time{}, nil, true)
	node.AddChild(tree.NewFile("Foo$Inner.class", "Foo$Inner.class", 0, time.Time{}, nil, true))

	full := map[string][]byte{
		"Foo.class":       {1},
		"Foo$Inner.class": {2},
	}
	if err := checkCompiledSet(node, full); err != nil {
		t.Errorf("集合一致时不该报错: %v", err)
	}

	extra := map[string][]byte{
		"Foo.class":       {1},
		"Foo$Inner.class": {2},
		"Foo$1.class":     {3},
	}
	err := checkCompiledSet(node, extra)
	if err == nil {
		t.Fatal("多出匿名类时应当拒绝替换")
	}
	if !strings.Contains(err.Error(), "多出") || !strings.Contains(err.Error(), "Foo$1.class") {
		t.Errorf("报错应当列出多出来的类，实际 = %q", err)
	}

	missing := map[string][]byte{"Foo.class": {1}}
	err = checkCompiledSet(node, missing)
	if err == nil {
		t.Fatal("少了内部类时应当拒绝替换")
	}
	if !strings.Contains(err.Error(), "缺少") || !strings.Contains(err.Error(), "Foo$Inner.class") {
		t.Errorf("报错应当列出缺少的类，实际 = %q", err)
	}
}

// head 取前 n 个字符，只用于断言失败时的提示。
func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// TestSaveContentChangesNodeAndMarksEdited 钉住"改完节点内容变了、标记也打上了"。
func TestSaveContentChangesNodeAndMarksEdited(t *testing.T) {
	app := openEditorApp(t)

	s, err := app.SaveContent(SaveOptions{
		Side: treeSidePatch, Path: "notes.txt",
		Mode: editorModeText, Text: "changed!\n", Charset: "UTF-8",
	})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	n := findNodeByPath(s.PatchTree, "notes.txt")
	if n == nil {
		t.Fatal("状态里找不到 notes.txt")
	}
	if !n.Edited {
		t.Error("保存后节点应带已修改标记")
	}

	// 再读一遍，内容应当是新的
	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "notes.txt"})
	if err != nil {
		t.Fatalf("重新打开编辑器失败: %v", err)
	}
	if v.Text != "changed!\n" {
		t.Errorf("重新读到的内容 = %q，期望 %q", v.Text, "changed!\n")
	}
	if !v.Edited {
		t.Error("重新打开时也应报告已修改")
	}
}

// TestSaveContentHonorsCharset 钉住"按指定编码写回去"。
//
// 原文件是 GBK 的，编辑后仍要按 GBK 写 —— 否则这个包从这一刻起整段变乱码。
// 判据：写出来的字节确实是 GBK（不是 UTF-8），且能按 GBK 读回原字符串。
func TestSaveContentHonorsCharset(t *testing.T) {
	app := openEditorApp(t)

	const text = "中文内容"
	s, err := app.SaveContent(SaveOptions{
		Side: treeSidePatch, Path: "notes.txt",
		Mode: editorModeText, Text: text, Charset: "GBK",
	})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	_ = s

	// 换一个字符集重新打开：按 GBK 解出来应当还是原文
	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "notes.txt", Charset: "GBK"})
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	if v.Text != text {
		t.Errorf("按 GBK 读回 = %q，期望 %q", v.Text, text)
	}
	if v.Charset != "GBK" {
		t.Errorf("报告的字集 = %q，期望 GBK", v.Charset)
	}

	// 大小应当是按 GBK 编码的字节数（6 个汉字 = 12 字节），不是 UTF-8 的 12 字节巧合 ——
	// 换一个更明确的判据：和 UTF-8 编码长度不同
	if v.Size == len([]byte(text)) {
		t.Errorf("大小 = %d，与 UTF-8 一致 —— 可能根本没按 GBK 编码", v.Size)
	}
}

// TestSaveContentHexMode 钉住十六进制保存走原始字节。
func TestSaveContentHexMode(t *testing.T) {
	app := openEditorApp(t)

	raw := []byte{0x00, 0x01, 0xFF, 0x7F}
	s, err := app.SaveContent(SaveOptions{
		Side: treeSidePatch, Path: "weird.txt",
		Mode: editorModeHex, Base64: base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	_ = s

	v, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "weird.txt"})
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	got, _ := base64.StdEncoding.DecodeString(v.Base64)
	if string(got) != string(raw) {
		t.Errorf("字节 = %v，期望 %v", got, raw)
	}
	if v.Size != 4 {
		t.Errorf("大小 = %d，期望 4", v.Size)
	}
}

// TestSaveContentRejectsDir 钉住目录不能编辑。
func TestSaveContentRejectsDir(t *testing.T) {
	app := openEditorApp(t)

	if _, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "BOOT-INF"}); err == nil {
		t.Error("目录不该能打开编辑器")
	}
	if _, err := app.SaveContent(SaveOptions{Side: treeSidePatch, Path: "BOOT-INF", Mode: editorModeText, Text: "x"}); err == nil {
		t.Error("目录不该能保存内容")
	}
}

// TestSaveContentUnknownNodeRejects 钉住"节点不存在要明确报错"。
func TestSaveContentUnknownNodeRejects(t *testing.T) {
	app := openEditorApp(t)

	if _, err := app.OpenEditor(EditorOptions{Side: treeSidePatch, Path: "nope.txt"}); err == nil {
		t.Error("不存在的路径应当报错")
	}
	if _, err := app.OpenEditor(EditorOptions{Side: "bogus", Path: "notes.txt"}); err == nil {
		t.Error("未知的树应当报错")
	}
}

// TestSaveContentFlowsIntoPatchOutput 是这一组里最要紧的一条：
// **改完的内容真的会进到打补丁的产物里**。
//
// 整条链路是"换掉节点的 source → ConsumeBytes 读到新内容 → 写包用新内容"，
// 只在编辑器那一层断言"内容变了"证明不了后半段 —— 而写出侧一旦漏掉某个分支
// （比如某条路径直接从 zip.File 读原始字节），产物里就还是旧内容。
//
// 断言同时覆盖对照组：改之前产物里是旧内容，改之后是新内容。
func TestSaveContentFlowsIntoPatchOutput(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	const target = "BOOT-INF/classes/com/example/Foo.class"
	const newContent = "PATCHED-BY-EDITOR"

	// 对照组：还没改，产物里应当是夹具原值
	beforeOut := filepath.Join(t.TempDir(), "before.jar")
	if _, err := app.RunPatch(beforeOut); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readZipEntry(t, beforeOut, target); got == newContent {
		t.Fatalf("对照组就已是新内容（%q），这条断言没有分辨力", got)
	}

	// 改内容
	b64 := base64.StdEncoding.EncodeToString([]byte(newContent))
	s, err := app.SaveContent(SaveOptions{
		Side: treeSidePatch, Path: target,
		Mode: editorModeHex, Base64: b64,
	})
	if err != nil {
		t.Fatalf("保存内容失败: %v", err)
	}
	if n := findNodeByPath(s.PatchTree, target); n == nil || !n.Edited {
		t.Fatal("保存后节点应带已修改标记")
	}

	// 再打一次补丁，产物里必须是新内容
	afterOut := filepath.Join(t.TempDir(), "after.jar")
	if _, err := app.RunPatch(afterOut); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readZipEntry(t, afterOut, target); got != newContent {
		t.Errorf("产物里 %s 的内容 = %q，期望 %q —— 编辑没有传导到写出侧", target, got, newContent)
	}
}

// TestReloadAppPackDropsEdits 钉住"重新加载会丢弃未落盘的修改"。
//
// 这是刻意的：改动只存在内存里，重新加载重建整棵树。
// 不写下来，日后有人"顺手保留"反而会与这条约定冲突。
func TestReloadAppPackDropsEdits(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	const target = "BOOT-INF/classes/com/example/Same.class"

	if _, err := app.SaveContent(SaveOptions{
		Side: treeSideApp, Path: target,
		Mode: editorModeHex, Base64: base64.StdEncoding.EncodeToString([]byte("edited")),
	}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	// 重新加载应用包（菜单里的「重新加载」）
	s, err := app.ReloadOrRefresh(treeSideApp, target)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if n := findNodeByPath(s.AppTree, target); n == nil || n.Edited {
		t.Error("重新加载后不该还带已修改标记")
	}
}

// TestAppMenuClaimsFollowUpstream 钉住应用树右键菜单的内容与顺序。
//
// 原版 HomeLeftTreeView.buildAppTreeContextMenu 有 13 项，这里落了 11 项
// （未做的见 MIGRATION.md §四）。claim 条件逐条照原版：
//
//	重新加载       只对主根（应用树没有自定义根）
//	取消关联       有映射
//	复制 ▸         任何节点
//	删除           单选、不是根、且状态为 NONE
//	标记删除       单选、状态为 DEL 或 NONE
//	编辑内容       非目录（本轮新增）
//	保存文件       非目录
//	资源管理器中查看  磁盘上的普通文件
//	关闭应用包     主根
//	聚焦应用树     无条件
func TestAppMenuClaimsFollowUpstream(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s := app.GetState()

	// 根：应用包本身是个磁盘上的 jar（FileNode），所以「编辑内容 / 保存文件 /
	// 资源管理器中查看」都命中 —— 与原版补丁根节点那套判定一致，别"看着别扭"
	// 就把它们挡掉。
	//
	// 根节点是 ROOT 类型，**既不能「删除」也不能「标记删除」**：原版
	// isAcceptManualDel 挡掉根，标记删除的 claim 也只放 DEL / NONE 两种状态过。
	wantRoot := []string{
		menuReload, menuCopy, menuEditContent, menuSaveFile,
		menuOpenInExplorer, menuCloseAppPack, menuFocusAppTree,
	}
	if got := menuIDs(s.AppTree); !sameStrings(got, wantRoot) {
		t.Errorf("应用根菜单 = %v，期望 %v", got, wantRoot)
	}

	// 目录：能编辑/保存（那是文件才有的）不成立，也不在磁盘上（压缩包条目），
	// 不是主根所以没有「重新加载 / 关闭应用包」。目录也能删/标删。
	wantDir := []string{menuCopy, menuDelete, menuMarkDelete, menuFocusAppTree}
	if got := menuIDs(findNodeByPath(s.AppTree, "BOOT-INF/")); !sameStrings(got, wantDir) {
		t.Errorf("目录菜单 = %v，期望 %v", got, wantDir)
	}

	// 压缩包里的文件：能编辑/保存/复制，能删/标删，但**没有**「资源管理器中查看」
	// （压缩包条目没有可定位的磁盘路径）。
	wantFile := []string{
		menuCopy, menuDelete, menuMarkDelete,
		menuEditContent, menuSaveFile, menuFocusAppTree,
	}
	if got := menuIDs(findNodeByPath(s.AppTree, "BOOT-INF/classes/com/example/Foo.class")); !sameStrings(got, wantFile) {
		t.Errorf("文件菜单 = %v，期望 %v", got, wantFile)
	}

	// 文案：本轮新增的那一项要能翻出来
	it := menuItem(s.AppTree, menuEditContent)
	if it == nil || it.Label != trZh("app.view.tree.edit-content") {
		t.Errorf("「编辑内容」文案 = %+v，期望 %q", it, trZh("app.view.tree.edit-content"))
	}

	// 「标记删除」是 RadioMenuItem：从文件节点取（根节点是 ROOT，不显示标删），
	// 未标删时不该是勾选态。
	fileNode := findNodeByPath(s.AppTree, "BOOT-INF/classes/com/example/Foo.class")
	if fileNode == nil {
		t.Fatal("找不到文件节点")
	}
	if md := menuItem(fileNode, menuMarkDelete); md == nil || !md.Radio || md.Checked {
		t.Errorf("「标记删除」应为单选且初始未勾选，实际 %+v", md)
	}
}
