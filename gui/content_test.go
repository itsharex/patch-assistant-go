package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/content"
	"cn.cpoet.patch.assistant/internal/decompile"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 内容预览的测试。
//
// 高亮与行号本身有跨语言黄金基准管着（internal/content），这里只管
// **GUI 层的接线**：选哪两个节点当左右、走哪个模式、标题怎么拼、
// 没有解析器时怎么办、字符集覆盖有没有生效。

// textFixture 造一个应用包 + 补丁包。
//
// 两个必须注意的点（都踩过）：
//
//  1. 配对的文件要用 **.js**：默认的 PatchFileRegex 只让
//     `class|html|js|css` 参与匹配（见 config.NewPatchConf），
//     用 .java 造不出"补丁节点映射到应用节点"的现场。
//  2. 补丁包要**扁平**（文件直接放根下），靠文件名匹配命中应用包里
//     深层目录中的同名文件。别在补丁里造 `com/example/` 这样的目录：
//     createPatchNodeMapping 会用同一个正则过滤**同级节点**，目录名
//     （"com"）过不了正则就被丢掉，路径索引里什么都不剩，
//     递归也就下不去 —— 这一条原版 Java 完全一样（`patchNodes.stream()
//     .filter(node -> isAcceptPatchFile(node.getName()))`），属保真行为。
//     真实补丁包也是扁平放 class 的，所以这条路径才是常用路径。
func textFixture(t *testing.T) (appJar, patchZip string) {
	t.Helper()
	dir := t.TempDir()

	appJar = filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/Foo.js", "const a = 1;\nfunction f() { return a; }\n"},
		{"com/example/Note.txt", "hello\nworld\n"},
		{"com/example/Pic.png", "\x89PNG\r\n\x1a\n"},
		{"com/example/Bin.class", "\xca\xfe\xba\xbe"},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	patchZip = filepath.Join(dir, "patch.zip")
	if err := os.WriteFile(patchZip, buildZipBytes(t, [][2]string{
		{"Foo.js", "const a = 2;\nfunction f() { return a; }\n"},
		{"README.txt", "# patch\n"},
	}), 0o644); err != nil {
		t.Fatalf("写 patch.zip 失败: %v", err)
	}
	return appJar, patchZip
}

// TestOpenContentSinglePane 应用树里一个普通文本文件：单栏、带行号。
func TestOpenContentSinglePane(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/Note.txt"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeSingle {
		t.Errorf("模式 = %q，期望 %q（这个节点没有对端）", v.Mode, contentModeSingle)
	}
	if v.DiffMode {
		t.Error("没有对端时 DiffMode 应为 false")
	}
	if v.Title != "com/example/Note.txt" {
		t.Errorf("标题 = %q，期望 %q", v.Title, "com/example/Note.txt")
	}
	// txt 在原版里能预览但**没有**高亮（DefaultCodeAreaFactory）
	if v.Lang != content.LangPlain {
		t.Errorf("txt 的高亮语言 = %q，期望 %q", v.Lang, content.LangPlain)
	}
	if v.Left == nil {
		t.Fatal("单栏模式下 Left 不能为空")
	}
	// "hello\nworld\n" -> 3 段（末尾换行保留一个空段落）
	if len(v.Left.Lines) != 3 {
		t.Fatalf("行数 = %d，期望 3", len(v.Left.Lines))
	}
	if got := lineText(v.Left.Lines[0]); got != "hello" {
		t.Errorf("第 1 行 = %q，期望 \"hello\"", got)
	}
	if got := lineText(v.Left.Lines[2]); got != "" {
		t.Errorf("末尾空段落 = %q，期望空串", got)
	}
	if v.Left.Lines[0].Num != "1" || v.Left.Lines[2].Num != "3" {
		t.Errorf("行号 = %q/%q，期望 \"1\"/\"3\"", v.Left.Lines[0].Num, v.Left.Lines[2].Num)
	}
	// 没有高亮的语言，每行应当就是一个无样式片段
	if cs := segClasses(v.Left.Lines[0]); len(cs) != 1 || cs[0] != "" {
		t.Errorf("txt 不该有样式片段，实际 %v", cs)
	}
}

// TestOpenContentHighlightsByExtension js 文件要真的带上高亮片段。
func TestOpenContentHighlightsByExtension(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/Foo.js"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Lang != content.LangJavaScript {
		t.Fatalf("高亮语言 = %q，期望 %q", v.Lang, content.LangJavaScript)
	}
	if v.Mode != contentModeSingle {
		t.Errorf("模式 = %q，期望 %q", v.Mode, contentModeSingle)
	}
	// "const a = 1;" 里应当有 keyword(const) 与 semicolon
	classes := segClasses(v.Left.Lines[0])
	if !contains(classes, "keyword") {
		t.Errorf("第一行没有 keyword 片段: %v", classes)
	}
	if !contains(classes, "semicolon") {
		t.Errorf("第一行没有 semicolon 片段: %v", classes)
	}
}

// TestOpenContentPatchNodePairsWithMapped 补丁节点映射到应用节点时应当双栏可比对。
func TestOpenContentPatchNodePairsWithMapped(t *testing.T) {
	appJar, patchZip := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSidePatch, Path: "Foo.js"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if !v.DiffMode {
		t.Fatal("补丁节点映射到了应用节点，DiffMode 应为 true")
	}
	// 默认 diffModel = false -> 双栏
	if v.Mode != contentModeSplit {
		t.Errorf("模式 = %q，期望 %q（默认双栏）", v.Mode, contentModeSplit)
	}
	// 标题：左（应用侧）<- 右（补丁侧）。补丁是扁平的，所以两边路径不一样。
	want := "com/example/Foo.js <- Foo.js"
	if v.Title != want {
		t.Errorf("标题 = %q，期望 %q", v.Title, want)
	}
	if v.Left == nil || v.Right == nil {
		t.Fatal("双栏模式下左右都应有内容")
	}
	if v.Left.Path != "com/example/Foo.js" {
		t.Errorf("左栏路径 = %q，期望应用侧路径", v.Left.Path)
	}
	if v.Right.Path != "Foo.js" {
		t.Errorf("右栏路径 = %q，期望补丁侧路径", v.Right.Path)
	}
	// 应用侧是 a = 1，补丁侧是 a = 2
	if !strings.Contains(lineText(v.Left.Lines[0]), "1") {
		t.Errorf("左栏第 1 行 = %q，期望含 1", lineText(v.Left.Lines[0]))
	}
	if !strings.Contains(lineText(v.Right.Lines[0]), "2") {
		t.Errorf("右栏第 1 行 = %q，期望含 2", lineText(v.Right.Lines[0]))
	}
	// 两栏的路径都要回传，字符集切换要靠它定位
	if v.Left.Path == "" || v.Right.Path == "" {
		t.Error("左右栏都应当带上节点路径")
	}
}

// TestOpenContentSplitMarks 双栏预览的差异高亮标记。
//
// **刻意增强**（原版双栏不做任何标记），所以期望值来自 content.DiffMarks
// 的口径，没有 Java 侧可抄。变异验证：把 gui/content.go 双栏分支里的
// lmarks/rmarks 换成 nil，本测试应当变红。
func TestOpenContentSplitMarks(t *testing.T) {
	appJar, patchZip := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSidePatch, Path: "Foo.js"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeSplit {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, contentModeSplit)
	}

	// 应用侧 `const a = 1;` / 补丁侧 `const a = 2;`：第 1 行改了，
	// 第 2 行一样，第 3 行是末尾空段落。
	marksOf := func(p *ContentPane) string {
		out := make([]string, len(p.Lines))
		for i, l := range p.Lines {
			out[i] = l.Mark
		}
		return strings.Join(out, ",")
	}
	if got, want := marksOf(v.Left), strings.Join([]string{content.MarkMod, "", ""}, ","); got != want {
		t.Errorf("左栏标记 = %q，期望 %q", got, want)
	}
	if got, want := marksOf(v.Right), strings.Join([]string{content.MarkMod, "", ""}, ","); got != want {
		t.Errorf("右栏标记 = %q，期望 %q", got, want)
	}

	// 标记只是"贴上去"的，行数与行号必须与不带标记时完全一致
	for name, p := range map[string]*ContentPane{"左栏": v.Left, "右栏": v.Right} {
		if len(p.Lines) != 3 {
			t.Fatalf("%s行数 = %d，期望 3", name, len(p.Lines))
		}
		if p.Lines[0].Num != "1" || p.Lines[2].Num != "3" {
			t.Errorf("%s行号 = %q..%q，期望 1..3", name, p.Lines[0].Num, p.Lines[2].Num)
		}
	}
}

// TestOpenContentSingleHasNoMarks 单栏与差异模式不该带任何差异标记 ——
// 高亮是双栏专有的，漏到别的模式上就会整栏变底色。
func TestOpenContentSingleHasNoMarks(t *testing.T) {
	appJar, patchZip := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	// 单栏
	single, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/Note.txt"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	for i, l := range single.Left.Lines {
		if l.Mark != "" {
			t.Errorf("单栏第 %d 行不该有标记，实际 %q", i, l.Mark)
		}
	}

	// 差异模式
	on := true
	diff, err := app.OpenContent(ContentOptions{Side: treeSidePatch, Path: "Foo.js", DiffModel: &on})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if diff.Mode != contentModeDiff {
		t.Fatalf("模式 = %q，期望 %q", diff.Mode, contentModeDiff)
	}
	for i, l := range diff.Left.Lines {
		if l.Mark != "" {
			t.Errorf("差异模式第 %d 行不该有标记，实际 %q", i, l.Mark)
		}
	}
}

// TestOpenContentDiffModel 差异模式：单栏、内容是 unified diff、行号特殊。
func TestOpenContentDiffModel(t *testing.T) {
	appJar, patchZip := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	on := true
	v, err := app.OpenContent(ContentOptions{
		Side: treeSidePatch, Path: "Foo.js", DiffModel: &on,
	})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeDiff {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, contentModeDiff)
	}
	if !v.DiffModel {
		t.Error("DiffModel 应为 true")
	}
	if v.Right != nil {
		t.Error("差异模式是单栏，Right 应为空")
	}
	if v.Left == nil || len(v.Left.Lines) == 0 {
		t.Fatal("差异模式应当有内容")
	}
	// 头两行是 --- / +++，行号留空；第三行是 @@，行号是 ...
	if got := lineText(v.Left.Lines[0]); !strings.HasPrefix(got, "--- ") {
		t.Errorf("第 1 行 = %q，期望以 \"--- \" 开头", got)
	}
	if v.Left.Lines[0].Num != " " {
		t.Errorf("--- 行的行号 = %q，期望一个空格（右对齐后为空）", v.Left.Lines[0].Num)
	}
	if got := lineText(v.Left.Lines[2]); !strings.HasPrefix(got, "@@ ") {
		t.Errorf("第 3 行 = %q，期望以 \"@@ \" 开头", got)
	}
	if v.Left.Lines[2].Num != "..." {
		t.Errorf("@@ 行的行号 = %q，期望 \"...\"", v.Left.Lines[2].Num)
	}
	// 两份内容不同，差异里应当既有 - 又有 +
	var hasDel, hasAdd bool
	for _, l := range v.Left.Lines {
		txt := lineText(l)
		if strings.HasPrefix(txt, "-") && !strings.HasPrefix(txt, "---") {
			hasDel = true
		}
		if strings.HasPrefix(txt, "+") && !strings.HasPrefix(txt, "+++") {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Errorf("差异里应当同时有增删行（del=%v add=%v）", hasDel, hasAdd)
	}

	// 差异模式开关要记住（对应原版存进 Configuration）
	off := false
	if _, err := app.OpenContent(ContentOptions{
		Side: treeSidePatch, Path: "Foo.js", DiffModel: &off,
	}); err != nil {
		t.Fatalf("切回双栏失败: %v", err)
	}
	if app.contentDiffModel {
		t.Error("显式传 false 后开关应变成 false")
	}
	v2, err := app.OpenContent(ContentOptions{Side: treeSidePatch, Path: "Foo.js"})
	if err != nil {
		t.Fatalf("再次打开失败: %v", err)
	}
	if v2.Mode != contentModeSplit {
		t.Errorf("不传 DiffModel 时应沿用上次的 false -> 双栏，实际 %q", v2.Mode)
	}
}

// TestOpenContentCharsetOverride 字符集覆盖要真的生效。
func TestOpenContentCharsetOverride(t *testing.T) {
	dir := t.TempDir()
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4} // "中文" 的 GBK 编码
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"a.txt", string(gbk)},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	// 自动探测：应当解出"中文"
	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "a.txt"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.LeftCharset != "GBK" {
		t.Errorf("探测到的编码 = %q，期望 GBK", v.LeftCharset)
	}
	if got := lineText(v.Left.Lines[0]); got != "中文" {
		t.Errorf("自动探测后内容 = %q，期望 \"中文\"", got)
	}

	// 显式指定 UTF-8：必须按 UTF-8 解（解出替换字符），不能"聪明地"回落
	v2, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "a.txt", Charset: "UTF-8"})
	if err != nil {
		t.Fatalf("指定编码打开失败: %v", err)
	}
	if v2.LeftCharset != "UTF-8" {
		t.Errorf("指定后编码 = %q，期望 UTF-8", v2.LeftCharset)
	}
	if got := lineText(v2.Left.Lines[0]); got == "中文" {
		t.Errorf("指定 UTF-8 后不应还是正确的中文（说明覆盖没生效），实际 %q", got)
	}
}

// TestOpenContentDiffModeCharsetAppliesToBothSides 钉住那处刻意的偏差：
// 差异（单栏）模式下原版选编码会 NPE，我们改成两侧一起重解。
func TestOpenContentDiffModeCharsetAppliesToBothSides(t *testing.T) {
	dir := t.TempDir()
	gbk := string([]byte{0xD6, 0xD0, 0xCE, 0xC4}) // "中文"
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"a.js", gbk},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}
	patchZip := filepath.Join(dir, "patch.zip")
	writeZip(t, patchZip, map[string]string{"a.js": gbk, "README.txt": "# patch\n"})

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	on := true
	// 两侧都显式给 UTF-8（前端在差异模式下的做法）
	v, err := app.OpenContent(ContentOptions{
		Side: treeSidePatch, Path: "a.js",
		Charset: "UTF-8", RightCharset: "UTF-8", DiffModel: &on,
	})
	if err != nil {
		t.Fatalf("差异模式打开失败: %v", err)
	}
	if v.Mode != contentModeDiff {
		t.Fatalf("模式 = %q，期望 %q", v.Mode, contentModeDiff)
	}
	if v.LeftCharset != "UTF-8" || v.RightCharset != "UTF-8" {
		t.Errorf("两侧编码 = %q/%q，期望都是 UTF-8", v.LeftCharset, v.RightCharset)
	}
	// 两份文本相同 -> 差异里不应有增删行
	for i, l := range v.Left.Lines {
		txt := lineText(l)
		if strings.HasPrefix(txt, "---") || strings.HasPrefix(txt, "+++") {
			continue
		}
		if strings.HasPrefix(txt, "+") || strings.HasPrefix(txt, "-") {
			t.Errorf("两份文本相同时不该有增删行，第 %d 行 = %q", i, txt)
		}
	}
}

// TestOpenContentAppSideModifiedPairsBothPanes 端到端钉住用户最直接的那条预期：
// **双击应用包里一个两边都有的文件，出来的必须是双栏对比**。
//
// 为什么要端到端：`contentPair` 的分支已有 TestContentPair 逐条覆盖，但那条路
// 只证明"选对了左右节点"，证明不了"整条链（打开两个包 → 跑匹配 → 取左右文本 →
// 摆成 ContentView）走完之后 mode 真的是 split"。用户报过"点查看差异进去空白"
// 之后，这类"看起来该对比、结果是单栏"的形态就必须有断言守着。
//
// 夹具里 4 个应用侧 MOD 节点全部遍历，避免只挑一个恰好命中的。
func TestOpenContentAppSideModifiedPairsBothPanes(t *testing.T) {
	appJar, patchZip := textFixture(t)
	app := NewApp()
	// 夹具全是 .class，走反编译要起 JVM；换成桩，本用例只关心"单栏还是双栏"。
	orig := decompileRun
	decompileRun = func(main []byte, inners [][]byte) (string, error) {
		return "// stub\npublic class Foo {}\n", nil
	}
	defer func() { decompileRun = orig }()

	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(patchZip)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if !s.Matched {
		t.Fatal("两个包都开了却标记成未匹配")
	}

	appRoot, _, err := app.treeRoot(treeSideApp)
	if err != nil {
		t.Fatalf("取应用树失败: %v", err)
	}
	var mods []*tree.Node
	var walk func(n *tree.Node)
	walk = func(n *tree.Node) {
		if n.MappedNode != nil && n.Type == constant.TreeNodeTypeMod {
			mods = append(mods, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(appRoot)
	if len(mods) == 0 {
		t.Fatal("夹具里应当有应用侧 MOD 节点（否则这条断言是空的）")
	}
	for _, n := range mods {
		v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: n.Path})
		if err != nil {
			t.Fatalf("OpenContent(app, %s) 失败: %v", n.Path, err)
		}
		if v.Mode != contentModeSplit {
			t.Errorf("双击应用侧 MOD 节点 %s 应当双栏，实际 mode=%s title=%q", n.Path, v.Mode, v.Title)
			continue
		}
		if v.Right == nil || len(v.Right.Lines) == 0 {
			t.Errorf("双栏时右栏必须有内容（%s）", n.Path)
		}
		if len(v.Left.Lines) == 0 {
			t.Errorf("双栏时左栏必须有内容（%s）", n.Path)
		}
		if !v.DiffMode {
			t.Errorf("双栏时 DiffMode 必须为真（右键菜单要靠它决定要不要显示" + "“差异模式”那一项）")
		}
	}
}

// TestOpenContentNoParserGoesExternal 没有解析器的节点应当交给系统程序打开。
func TestOpenContentNoParserGoesExternal(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	var opened string
	app.openExternal = func(p string) error { opened = p; return nil }

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/Pic.png"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeExternal {
		t.Errorf("模式 = %q，期望 %q", v.Mode, contentModeExternal)
	}
	if opened == "" {
		t.Fatal("应当调用系统程序打开，但没有")
	}
	// 压缩包条目没有磁盘路径，必须落到临时文件；扩展名要保留
	if !strings.HasSuffix(strings.ToLower(opened), ".png") {
		t.Errorf("临时文件应当保留扩展名，实际 %q", opened)
	}
	if _, err := os.Stat(opened); err != nil {
		t.Errorf("临时文件不存在: %v", err)
	}
	os.Remove(opened)
}

// TestOpenContentClassUnsupported .class 反编译未移植，要给明确提示而不是崩。
// TestOpenContentClassBadBytes 坏掉的 class 字节要**降级提示**，不能崩、
// 也不能被当成文本硬渲染出来。
//
// 注意这条测的不是"不支持 class"（那是老行为）：现在 .class 是**支持**的，
// 这里造的是解析不出类名的假字节，走的是"反编译失败 → 提示"这条路。
func TestOpenContentClassBadBytes(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	// 别让它真去弹系统程序
	app.openExternal = func(string) error { t.Fatal("class 不应走系统程序打开"); return nil }

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/Bin.class"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeUnsupported {
		t.Errorf("模式 = %q，期望 %q", v.Mode, contentModeUnsupported)
	}
	if v.Message == "" {
		t.Error("失败时应当给说明文案")
	}
}

// decompGoldenClasses 指向反编译基准里的 class 样本（internal/decompile 的夹具）。
func decompGoldenClasses(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "_extract", "tools", "decomp_golden", "classes")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("反编译基准不存在（先跑 make_decomp_golden.sh）：%v", err)
	}
	return dir
}

func readGoldenClass(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(decompGoldenClasses(t), name))
	if err != nil {
		t.Fatalf("读样本 %s 失败: %v", name, err)
	}
	return string(b)
}

// TestOpenContentClassDecompiles 双击 .class 要**反编译出 Java 源码**来预览。
//
// 这是用户实际报的那个问题：早先这里只弹一句"未移植"，双击什么都看不到。
func TestOpenContentClassDecompiles(t *testing.T) {
	if !decompile.Available() {
		t.Skip("本机没有 Java，跳过（反编译依赖 JRE，这是预期内的前置条件）")
	}
	probe := readGoldenClass(t, "ChineseProbe.class")

	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/ChineseProbe.class", probe},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/ChineseProbe.class"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeSingle {
		t.Fatalf("模式 = %q，期望 %q（message=%q）", v.Mode, contentModeSingle, v.Message)
	}
	if v.Lang != content.LangJava {
		t.Errorf("高亮语言 = %q，期望 %q", v.Lang, content.LangJava)
	}
	if v.Left == nil || len(v.Left.Lines) == 0 {
		t.Fatal("没有渲染出任何行")
	}

	text := paneText(v.Left.Lines)
	if !strings.Contains(text, "class ChineseProbe") {
		t.Errorf("反编译结果里没有类声明，前几行：\n%s", firstLines(text, 5))
	}
	// 中文字符串常量必须原样带出来 —— 编码不对的话这里会变成问号或乱码
	if !strings.Contains(text, "中文测试：反编译编码验证") {
		t.Error("中文字符串常量丢失或被写坏了")
	}
}

// TestOpenContentClassPassesInnerClasses 内部类必须**跟着主类一起**反编译。
//
// 内部类在树里挂在外部类下（原版建树规则），原版 ClassContentParser 会把
// 所有子孙类的字节一并交给 fernflower。只喂主类的话，反编译出来的内部类
// 是空壳 —— 这个差异很隐蔽，只有把内部类体断言出来才抓得住。
func TestOpenContentClassPassesInnerClasses(t *testing.T) {
	if !decompile.Available() {
		t.Skip("本机没有 Java，跳过")
	}
	main := readGoldenClass(t, "NumberField.class")
	in1 := readGoldenClass(t, "NumberField$NumberFormatter.class")
	in2 := readGoldenClass(t, "NumberField$NumberFormatter$1.class")

	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/NumberField.class", main},
		{"com/example/NumberField$NumberFormatter.class", in1},
		{"com/example/NumberField$NumberFormatter$1.class", in2},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	v, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/NumberField.class"})
	if err != nil {
		t.Fatalf("打开内容预览失败: %v", err)
	}
	if v.Mode != contentModeSingle {
		t.Fatalf("模式 = %q，期望 %q（message=%q）", v.Mode, contentModeSingle, v.Message)
	}

	text := paneText(v.Left.Lines)
	if !strings.Contains(text, "class NumberFormatter") {
		t.Errorf("内部类没有被一起反编译，前几行：\n%s", firstLines(text, 5))
	}
}

// TestDecompileClassCaches 同一个 class 反复双击，只反编译一次。
//
// 这是需求 P 里"加载稍微有点慢"的针对性修复：Go 版把 fernflower 内嵌，每次
// Decompile 都要重新起 JVM（本机实测首次冷启动约 1 秒）。加会话内缓存后，
// 命中缓存的那次直接返回字符串、不再起 JVM。这条用 decompileRun 这个 seam
// 数"真正起 JVM 反编译"的次数，断言第二次打开同一 class 时归零 ——
// 否则缓存没生效、每次都白白等 1 秒。
func TestDecompileClassCaches(t *testing.T) {
	if !decompile.Available() {
		t.Skip("本机没有 Java，跳过（反编译依赖 JRE，这是预期内的前置条件）")
	}
	probe := readGoldenClass(t, "ChineseProbe.class")

	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/ChineseProbe.class", probe},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	// seam：统计真正起 JVM 反编译的次数
	var calls int
	orig := decompileRun
	decompileRun = func(main []byte, inners [][]byte) (string, error) {
		calls++
		return orig(main, inners)
	}
	defer func() { decompileRun = orig }()

	v1, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/ChineseProbe.class"})
	if err != nil {
		t.Fatalf("第一次打开失败: %v", err)
	}
	if calls != 1 {
		t.Fatalf("第一次反编译应当起 1 次 JVM，实际 %d", calls)
	}

	v2, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/ChineseProbe.class"})
	if err != nil {
		t.Fatalf("第二次打开失败: %v", err)
	}
	if calls != 1 {
		t.Fatalf("第二次应当命中会话内缓存、不再起 JVM，实际累计 %d 次", calls)
	}
	if paneText(v1.Left.Lines) != paneText(v2.Left.Lines) {
		t.Error("两次反编译结果不一致（缓存返回了不一样的东西）")
	}
	// 内容没变、key 没变，缓存里应当只有 1 条，不随重复打开无限膨胀
	if len(app.decompileCache) != 1 {
		t.Errorf("缓存条目数 = %d，期望 1（同一个 class 不应重复落缓存）", len(app.decompileCache))
	}
}

// TestOpenContentRejectsDir 目录不弹预览。
func TestOpenContentRejectsDir(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "com/example/"}); err == nil {
		t.Error("目录应当返回错误")
	}
}

// TestOpenContentUnknownNode 路径不存在 / 树标识不对都要报错。
func TestOpenContentUnknownNode(t *testing.T) {
	appJar, _ := textFixture(t)
	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenContent(ContentOptions{Side: treeSideApp, Path: "nope.txt"}); err == nil {
		t.Error("找不到节点时应当返回错误")
	}
	if _, err := app.OpenContent(ContentOptions{Side: "bogus", Path: "a.txt"}); err == nil {
		t.Error("未知的树应当返回错误")
	}
	if _, err := app.OpenContent(ContentOptions{Side: treeSidePatch, Path: "a.txt"}); err == nil {
		t.Error("补丁包没打开时应当返回错误")
	}
}

// TestContentPair 直接单测左右节点的挑选逻辑。
//
// 逐条对应 Java ContentView 的构造函数 —— 那是个纯函数式的选择，
// 直接喂 Node 比走整个匹配流程再断言清楚得多。
func TestContentPair(t *testing.T) {
	mk := func(isPatch bool, typ constant.TreeNodeType) *tree.Node {
		n := tree.NewBareFile(tree.KindFile)
		n.Name = "x.js"
		n.Path = "x.js"
		n.IsPatch = isPatch
		n.Type = typ
		return n
	}

	// 1. 补丁节点 + 映射到普通应用节点 -> 左应用、右补丁
	{
		app := mk(false, constant.TreeNodeTypeNone)
		patch := mk(true, constant.TreeNodeTypeMod)
		app.MappedNode = patch
		patch.MappedNode = app
		l, r := contentPair(patch)
		if l != app || r != patch {
			t.Error("补丁节点映射到普通应用节点时应当左=应用、右=补丁")
		}
	}

	// 2. 补丁节点 + 映射到的应用节点是 ADD -> 左=补丁、右=空
	{
		app := mk(false, constant.TreeNodeTypeAdd)
		patch := mk(true, constant.TreeNodeTypeMod)
		app.MappedNode = patch
		patch.MappedNode = app
		l, r := contentPair(patch)
		if l != patch || r != nil {
			t.Error("对端是 ADD 时应当退化成单栏（左=补丁、右=空）")
		}
	}

	// 3. 补丁节点没有映射 -> 左=补丁、右=空
	{
		patch := mk(true, constant.TreeNodeTypeAdd)
		l, r := contentPair(patch)
		if l != patch || r != nil {
			t.Error("补丁节点没有映射时应当左=补丁、右=空")
		}
	}

	// 4. 应用节点自己是 ADD -> 左=它映射到的补丁节点、右=空
	{
		app := mk(false, constant.TreeNodeTypeAdd)
		patch := mk(true, constant.TreeNodeTypeMod)
		app.MappedNode = patch
		patch.MappedNode = app
		l, r := contentPair(app)
		if l != patch || r != nil {
			t.Error("应用侧 ADD 时应当左=映射到的补丁节点、右=空")
		}
	}

	// 5. 应用节点是普通节点 + 有映射 -> 左=应用、右=补丁
	{
		app := mk(false, constant.TreeNodeTypeNone)
		patch := mk(true, constant.TreeNodeTypeMod)
		app.MappedNode = patch
		patch.MappedNode = app
		l, r := contentPair(app)
		if l != app || r != patch {
			t.Error("应用侧普通节点有映射时应当左=应用、右=补丁")
		}
	}

	// 6. 应用节点没有映射 -> 左=应用、右=空
	{
		app := mk(false, constant.TreeNodeTypeNone)
		l, r := contentPair(app)
		if l != app || r != nil {
			t.Error("应用节点没有映射时应当左=应用、右=空")
		}
	}
}

// ---- 小工具 ----

// paneText 把一栏的所有行拼回纯文本。
func paneText(lines []content.Line) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lineText(l))
	}
	return b.String()
}

// firstLines 取前 n 行，只用于断言失败时给出可读的上下文。
func firstLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[:n]
	}
	return strings.Join(ls, "\n")
}

func lineText(l content.Line) string {
	var b strings.Builder
	for _, s := range l.Segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// segClasses 取一行里所有片段的样式类名。
func segClasses(l content.Line) []string {
	var out []string
	for _, s := range l.Segs {
		out = append(out, s.Class)
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
