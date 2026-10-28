package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cn.cpoet.patch.assistant/internal/config"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 全局搜索的单测（对应 gui/search.go ←→ Java view/search/SearchView）。
//
// 这些用例直接搭内存树，不走 zip 夹具 —— 搜索只看"名字 + 父子关系"，
// 用夹具反而绕远。唯一需要真文件的是"路径式搜索探进嵌套 jar"那两条，
// 因为解压必须有内容来源。

// ---------------------------------------------------------------------------
// 搭树的小工具
// ---------------------------------------------------------------------------

func tdir(name, path string, isPatch bool) *tree.Node {
	n := &tree.Node{Name: name, Path: path, IsPatch: isPatch}
	n.SetDir(true)
	return n
}

func tfile(name, path string, isPatch bool) *tree.Node {
	return &tree.Node{Name: name, Path: path, IsPatch: isPatch}
}

func tadd(parent *tree.Node, kids ...*tree.Node) *tree.Node {
	for _, k := range kids {
		parent.AddChild(k)
	}
	return parent
}

// searchApp 造一个只装了树的 App（语言钉成中文，断言文案才稳定）。
func searchApp(t *testing.T, appRoot, patchRoot *tree.Node) *App {
	t.Helper()
	a := newAppZh()
	if appRoot != nil {
		info := tree.NewAppTreeInfo()
		info.SetRootNode(appRoot)
		a.appTreeInfo = info
	}
	if patchRoot != nil {
		info := tree.NewPatchTreeInfo()
		info.SetRootNode(patchRoot)
		a.patchTreeInfo = info
	}
	return a
}

// demoAppTree 一棵够用的应用树：
//
//	app.jar
//	├── BOOT-INF/            （目录条目，Path 带尾斜杠 —— 与原版一致）
//	│   └── lib/
//	│       └── spring-core.jar   （compress，尚无 children）
//	└── com/
//	    └── example/
//	        ├── Foo.js
//	        ├── Extra.txt
//	        └── deep/inner/Target.js
func demoAppTree() *tree.Node {
	jar := tfile("spring-core.jar", "BOOT-INF/lib/spring-core.jar", false)
	inner := tadd(tdir("inner", "com/example/deep/inner", false),
		tfile("Target.js", "com/example/deep/inner/Target.js", false))
	deep := tadd(tdir("deep", "com/example/deep", false), inner)
	example := tadd(tdir("example", "com/example", false),
		tfile("Foo.js", "com/example/Foo.js", false),
		tfile("Extra.txt", "com/example/Extra.txt", false),
		deep)
	com := tadd(tdir("com", "com", false), example)
	lib := tadd(tdir("lib", "BOOT-INF/lib", false), jar)
	boot := tadd(tdir("BOOT-INF", "BOOT-INF/", false), lib)
	return tadd(&tree.Node{Name: "app.jar"}, boot, com)
}

func demoPatchTree() *tree.Node {
	return tadd(&tree.Node{Name: "patch.jar", IsPatch: true},
		tfile("Foo.js", "Foo.js", true),
		tfile("Target.js", "Target.js", true),
	)
}

// names 取结果的显示名，顺序保持。
func names(items []*SearchItemView) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func itemByName(items []*SearchItemView, name, pathPrefix string) *SearchItemView {
	for _, it := range items {
		if it.Name == name && strings.HasPrefix(it.Path, pathPrefix) {
			return it
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 空关键词 -> 搜索历史
// ---------------------------------------------------------------------------

func TestSearchBlankReturnsHistory(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())

	// 一开始没有历史
	items, err := app.SearchNodes("")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("还没有历史却返回了 %d 条: %+v", len(items), names(items))
	}

	app.AddSearchHistory("Foo", "com/example/Foo.js")

	// 各种"空"都要走历史分支 —— 口径是 Java StringUtil.isBlank，
	// 它用的是 Character.isSpaceChar，只认 Unicode 的 Zs/Zl/Zp 三类。
	// 所以半角空格、全角空格算空，而 `\t` / `\n` 这类**控制字符不算空** ——
	// 后者会被当成正经关键词去搜（原版就是这个口径，别改成 trim()）。
	for _, blank := range []string{"", "   ", "\u3000"} {
		items, err = app.SearchNodes(blank)
		if err != nil {
			t.Fatalf("搜索 %q 失败: %v", blank, err)
		}
		if len(items) != 1 {
			t.Fatalf("关键词 %q 应返回 1 条历史，实际 %d 条", blank, len(items))
		}
		it := items[0]
		if it.Name != "Foo" || it.Path != "Search History" || !it.History {
			t.Errorf("历史项字段不对: %+v", it)
		}
		if it.Target != "com/example/Foo.js" {
			t.Errorf("历史项 target 不对: %q", it.Target)
		}
		if it.SearchTime == "" {
			t.Error("历史项没有时间")
		}
		if _, err := time.Parse("2006-01-02 15:04:05", it.SearchTime); err != nil {
			t.Errorf("历史项时间格式不对: %q", it.SearchTime)
		}
		if it.Side != "" || it.NodePath != "" || len(it.Ancestors) != 0 {
			t.Errorf("历史项不该带节点字段: %+v", it)
		}
		// 有搜索时间才补时钟图标 —— 对应原版 SearchListCell.addSearchHisInfo
		if it.Icon != iconTime {
			t.Errorf("历史项图标应为 %q，实际 %q", iconTime, it.Icon)
		}
	}
}

func TestAddSearchHistoryDedupsByKeywordAndPrepends(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())

	app.AddSearchHistory("a", "p/a")
	app.AddSearchHistory("b", "p/b")
	app.AddSearchHistory("a", "p/a2")

	items, _ := app.SearchNodes("")
	if len(items) != 2 {
		t.Fatalf("同名关键词应去重，实际 %d 条: %+v", len(items), names(items))
	}
	// 头插：最新的是 a
	if items[0].Name != "a" || items[1].Name != "b" {
		t.Errorf("顺序不对（应最新的在最前）: %+v", names(items))
	}
	// 重复添加要覆盖 target，而不是留旧的
	if items[0].Target != "p/a2" {
		t.Errorf("重复关键词应更新 target，实际 %q", items[0].Target)
	}
}

func TestAddSearchHistoryTrimsToLimit(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())
	// 默认上限 30（config.NewSearchConf）
	if app.search.HistoryLimit != 30 {
		t.Fatalf("历史上限应为 30，实际 %d", app.search.HistoryLimit)
	}
	for i := 0; i < 40; i++ {
		app.AddSearchHistory("k"+string(rune('a'+i%26))+string(rune('0'+i/26)), "t")
	}
	items, _ := app.SearchNodes("")
	if len(items) > 30 {
		t.Errorf("历史超出上限: %d 条", len(items))
	}
}

// ---------------------------------------------------------------------------
// 单关键词
// ---------------------------------------------------------------------------

func TestSearchSimpleCoversBothTreesWithPathLabel(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())

	items, err := app.SearchNodes("Foo")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应命中两棵树各一个，实际 %d 条: %+v", len(items), names(items))
	}
	// 应用侧：路径 = 根标签 + 祖先名（**不含节点自己**），这是原版的口径
	appFoo := itemByName(items, "Foo.js", "应用包:")
	if appFoo == nil {
		t.Fatalf("没找到应用侧结果: %+v", items)
	}
	if appFoo.Path != "应用包:/com/example" {
		t.Errorf("应用侧路径不对: %q", appFoo.Path)
	}
	if appFoo.Side != treeSideApp {
		t.Errorf("side 不对: %q", appFoo.Side)
	}
	if appFoo.NodePath != "com/example/Foo.js" {
		t.Errorf("nodePath 不对: %q", appFoo.NodePath)
	}
	if appFoo.Icon != "icons/file-js.png" {
		t.Errorf("图标不对: %q", appFoo.Icon)
	}
	// 祖先链：从根的直接子级往下，**到父节点为止**
	if got := strings.Join(appFoo.Ancestors, ","); got != "com,com/example" {
		t.Errorf("祖先链不对: %q", got)
	}

	// 补丁侧：直接挂在根下 -> 路径只有根标签，祖先链为空
	patchFoo := itemByName(items, "Foo.js", "补丁包:")
	if patchFoo == nil {
		t.Fatalf("没找到补丁侧结果: %+v", items)
	}
	if patchFoo.Path != "补丁包:" {
		t.Errorf("补丁侧路径不对: %q", patchFoo.Path)
	}
	if len(patchFoo.Ancestors) != 0 {
		t.Errorf("补丁侧不该有祖先: %+v", patchFoo.Ancestors)
	}
	if patchFoo.Side != treeSidePatch {
		t.Errorf("side 不对: %q", patchFoo.Side)
	}
}

// 根本身不参与匹配 —— 原版从 root.getChildren() 开始遍历。
func TestSearchSkipsRootItself(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	items, err := app.SearchNodes("app.jar")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("根节点不该被搜到，实际: %+v", names(items))
	}
}

// contains 是区分大小写的（原版 name.contains）。
func TestSearchIsCaseSensitive(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	items, _ := app.SearchNodes("foo")
	if len(items) != 0 {
		t.Fatalf("小写 foo 不该命中 Foo.js，实际: %+v", names(items))
	}
	items, _ = app.SearchNodes("Foo")
	if len(items) != 1 {
		t.Fatalf("大写 Foo 应命中 1 条，实际: %+v", names(items))
	}
}

// 含 `*` 时走正则**全匹配**（matcher().matches()），不是 contains。
func TestSearchWildcardIsFullMatch(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())

	items, err := app.SearchNodes("*.js")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	got := names(items)
	// 应用树 Foo.js + Target.js，补丁树 Foo.js + Target.js
	if len(items) != 4 {
		t.Fatalf("*.js 应命中 4 条，实际 %d: %+v", len(items), got)
	}
	for _, n := range got {
		if !strings.HasSuffix(n, ".js") {
			t.Errorf("命中了非 .js 的节点: %q", n)
		}
	}

	// 全匹配：`Foo` 不带 * 时是 contains（能命中 Foo.js），
	// 但 `Foo` 变成正则全匹配就命不中 —— 用 `Foo*` 验证"前缀"语义
	items, _ = app.SearchNodes("Foo*")
	if len(items) != 2 {
		t.Fatalf("Foo* 应命中 2 条，实际: %+v", names(items))
	}
	// `*Foo*` 是"包含 Foo"
	items, _ = app.SearchNodes("*Foo*")
	if len(items) != 2 {
		t.Fatalf("*Foo* 应命中 2 条，实际: %+v", names(items))
	}
	// `Extra` 只有 contains 才命中，`Extra*` 全匹配也应命中（Extra.txt 以 Extra 开头）
	items, _ = app.SearchNodes("Extra*")
	if len(items) != 1 {
		t.Fatalf("Extra* 应命中 1 条，实际: %+v", names(items))
	}
	// `*Extra` 要求以 Extra 结尾 -> 命不中 Extra.txt
	items, _ = app.SearchNodes("*Extra")
	if len(items) != 0 {
		t.Fatalf("*Extra 不该命中，实际: %+v", names(items))
	}
}

// 关键词里含 `/`（或 `\`）时走"路径式"搜索：沿父子链逐级匹配。
func TestSearchPathKeywordMatchesAlongParentChain(t *testing.T) {
	app := searchApp(t, demoAppTree(), demoPatchTree())

	items, err := app.SearchNodes("com/example/Foo.js")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应只命中应用侧那一个，实际 %d 条: %+v", len(items), names(items))
	}
	if items[0].Path != "应用包:/com/example" {
		t.Errorf("路径不对: %q", items[0].Path)
	}
	if got := strings.Join(items[0].Ancestors, ","); got != "com,com/example" {
		t.Errorf("祖先链不对: %q", got)
	}

	// 反斜杠也当分隔符（FileNameUtil.splitPath 的口径）
	items, _ = app.SearchNodes(`com\example\Foo.js`)
	if len(items) != 1 {
		t.Fatalf("反斜杠路径应同样命中 1 条，实际: %+v", names(items))
	}

	// 链条断了就不该命中（example 下面没有 deep/Foo.js）
	items, _ = app.SearchNodes("com/deep/Foo.js")
	if len(items) != 0 {
		t.Fatalf("链条不连续不该命中，实际: %+v", names(items))
	}
}

// 路径式搜索的"兜底分支"：整条序列可以从任意位置重新开始，
// 所以中间几级不必从根数起。
func TestSearchPathRestartsAtAnyDepth(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	// `deep/inner/Target.js` 不是从根的直接子级开始，但仍应命中
	items, err := app.SearchNodes("deep/inner/Target.js")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Target.js" {
		t.Fatalf("应从任意深度重启并命中，实际: %+v", names(items))
	}
	if items[0].Path != "应用包:/com/example/deep/inner" {
		t.Errorf("路径不对: %q", items[0].Path)
	}
}

// 路径式搜索的"续级"必须是**连续**的：中间插一层就断。
//
// 这条专门钉住原版那个最容易被人"顺手改好"的地方 —— 从一个已命中的节点往下
// 找下一级时，如果那一级没命中，原版走的是兜底分支，而兜底分支调的是
// **3 个参数的重载**（`searchNodeWithPath(child, keywords, paths, results)`），
// 也就是 **index 归 0 重新开始**，而不是"接着往后找"。
//
// 于是 `a/x/b` 这棵树**不**匹配 `a/b`：在 `x` 那一级没命中 `b` 之后，
// 搜索从第一级 `a` 重来，`b` 对不上 `a`，链条就断了。
//
// 这条是被变异逼出来的：把兜底分支的 index 从 0 改成当前 index，别的用例
// 一条都不红（它们命中的路径都走"匹配分支"，压根到不了兜底），只有这条能抓住。
func TestSearchPathChainMustBeContiguous(t *testing.T) {
	b := tfile("b", "a/x/b", false)
	x := tadd(tdir("x", "a/x", false), b)
	a := tadd(tdir("a", "a", false), x)
	root := tadd(&tree.Node{Name: "app.jar"}, a)
	app := searchApp(t, root, nil)

	// 连续命中：a -> x -> b
	items, err := app.SearchNodes("a/x/b")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 1 || items[0].NodePath != "a/x/b" {
		t.Fatalf("连续链条应当命中，实际: %+v", names(items))
	}

	// 中间插一层就断：a -> b 对不上（x 那一级不叫 b）
	items, err = app.SearchNodes("a/b")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("链条不连续不该命中，实际: %+v", names(items))
	}
}

// 关键词里既有 `*` 又有编不出来的正则片段时，原版会抛异常并被后台线程吞掉
// （界面悄悄什么都不发生）。这里改成把错误交回去 —— 刻意偏差，见 MIGRATION.md。
func TestSearchInvalidWildcardRegexReturnsError(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	if _, err := app.SearchNodes("a[*"); err == nil {
		t.Fatal("非法通配符应当返回错误，而不是静默无结果")
	}
	// 不含 `*` 时永远不会去编正则，任何字符都安全
	if _, err := app.SearchNodes("a["); err != nil {
		t.Fatalf("不含 * 的关键词不该报错: %v", err)
	}
}

// 只开了一棵树时不该报错，也不该带出另一棵的结果。
func TestSearchWithOnlyOneTreeOpen(t *testing.T) {
	app := searchApp(t, nil, demoPatchTree())
	items, err := app.SearchNodes("Foo")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 1 || items[0].Side != treeSidePatch {
		t.Fatalf("只开补丁树时应只有补丁侧结果: %+v", items)
	}

	// 两棵都没开 -> 空结果，不报错
	empty := searchApp(t, nil, nil)
	items, err = empty.SearchNodes("Foo")
	if err != nil || len(items) != 0 {
		t.Fatalf("两棵树都没开时应返回空且不报错，实际 err=%v items=%+v", err, items)
	}
}

// ---------------------------------------------------------------------------
// 路径式搜索里的"现场解压"
// ---------------------------------------------------------------------------

// nestedJarTree 造一棵带**真文件**嵌套 jar 的树，好让 BuildChildrenWithCompress
// 真的能把内容解出来。
//
//	app.jar
//	└── BOOT-INF/
//	    └── lib/
//	        └── lib.jar   （真 zip，里面有 org/Deep.class）
func nestedJarTree(t *testing.T, jarEntries map[string]string) (*tree.Node, *tree.Node) {
	t.Helper()
	jarPath := filepath.Join(t.TempDir(), "lib.jar")
	writeZip(t, jarPath, jarEntries)

	jar := tfile("lib.jar", "BOOT-INF/lib/lib.jar", false)
	jar.SetFileSource(jarPath)
	lib := tadd(tdir("lib", "BOOT-INF/lib", false), jar)
	boot := tadd(tdir("BOOT-INF", "BOOT-INF/", false), lib)
	root := tadd(&tree.Node{Name: "app.jar"}, boot)
	return root, jar
}

// 命中时：解压出来的子级要**留着**（用户双击结果就能看到那个 jar 已经展开）。
func TestSearchPathDecompressesNestedJarOnHit(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	if jar.Children != nil {
		t.Fatal("前置条件不成立：jar 一开始就该是没展开的")
	}

	items, err := app.SearchNodes("BOOT-INF/lib/lib.jar/org/Deep.class")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应命中 jar 里那一个，实际 %d 条: %+v", len(items), names(items))
	}
	if items[0].Name != "Deep.class" {
		t.Errorf("名字不对: %q", items[0].Name)
	}
	// 路径栈里含 jar 自己的名字
	if items[0].Path != "应用包:/BOOT-INF/lib/lib.jar/org" {
		t.Errorf("路径不对: %q", items[0].Path)
	}
	// 祖先链里 `BOOT-INF/` 带尾斜杠 —— 那是 zip 目录条目的原样名字
	// （判定 Spring Boot 胖包就靠它），别"顺手"把斜杠去掉。
	if got := strings.Join(items[0].Ancestors, ","); got != "BOOT-INF/,BOOT-INF/lib,BOOT-INF/lib/lib.jar,org" {
		t.Errorf("祖先链不对: %q", got)
	}
	if len(jar.Children) == 0 {
		t.Error("命中后解压出来的子级应当保留（原版 buildNodeChildren 那一步）")
	}
}

// 没命中时：解压要**回滚**（原版 node.setChildren(oldChildren)），
// 否则用户搜了个不存在的东西，树里却凭空多出一个展开的 jar。
func TestSearchPathRollsBackNestedJarOnMiss(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	items, err := app.SearchNodes("BOOT-INF/lib/lib.jar/org/Nope.class")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("不该命中，实际: %+v", names(items))
	}
	if jar.Children != nil {
		t.Errorf("没命中时应当回滚解压结果，实际留下 %d 个子级", len(jar.Children))
	}
}

// 已经展开过的 jar 直接往下走，不再解压（BuildChildrenWithCompress 开头就
// 挡掉了 Children != nil 的情况）。
func TestSearchPathUsesAlreadyExpandedJar(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	// 先解一次（第一次搜索会把子级留下）
	if _, err := app.SearchNodes("BOOT-INF/lib/lib.jar/org/Deep.class"); err != nil {
		t.Fatalf("第一次搜索失败: %v", err)
	}
	before := len(jar.Children)
	if before == 0 {
		t.Fatal("第一次搜索应当留下子级")
	}

	// 再搜一次，子级不该翻倍
	if _, err := app.SearchNodes("BOOT-INF/lib/lib.jar/org/Deep.class"); err != nil {
		t.Fatalf("第二次搜索失败: %v", err)
	}
	if len(jar.Children) != before {
		t.Errorf("已展开的 jar 不该被重复解压: %d -> %d", before, len(jar.Children))
	}
}

// ---------------------------------------------------------------------------
// 兜底深度搜：SearchNodesDeep
// ---------------------------------------------------------------------------

// 普通搜只搜"已经加载进树的节点"，所以嵌在没展开的嵌套 jar 里的文件看不见。
// SearchNodesDeep 多走一步：把没展开的 jar 现场展开再搜一遍，命中就找到。
//
// 这一条钉住用户报的那个 bug：搜 `Deep.class`（在 lib.jar 里）普通搜是 0 条，
// 深度搜应当命中，且路径里带着 jar 自己的名字、祖先链带 jar 的完整路径。
func TestSearchDeepFindsFileInsideNestedJar(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	if got, err := app.SearchNodes("Deep.class"); err != nil || len(got) != 0 {
		t.Fatalf("普通搜只搜已加载节点，应 0 条，实际 %d (err=%v)", len(got), err)
	}

	items, err := app.SearchNodesDeep("Deep.class")
	if err != nil {
		t.Fatalf("深度搜失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("深度搜应命中 jar 里那一个，实际 %d 条: %+v", len(items), names(items))
	}
	if items[0].Name != "Deep.class" {
		t.Errorf("名字不对: %q", items[0].Name)
	}
	// 路径栈含 jar 名字（与路径式搜索命中时的口径一致）
	if items[0].Path != "应用包:/BOOT-INF/lib/lib.jar/org" {
		t.Errorf("路径不对: %q", items[0].Path)
	}
	if got := strings.Join(items[0].Ancestors, ","); got != "BOOT-INF/,BOOT-INF/lib,BOOT-INF/lib/lib.jar,org" {
		t.Errorf("祖先链不对: %q", got)
	}
	// 命中 → 保留展开（前端双击结果时靠 GetState 拿到展开后的层级才能跳过去）
	if len(jar.Children) == 0 {
		t.Error("命中的 jar 应当保留展开")
	}
}

// 深度搜没命中 → 整支回滚，树里不能凭空多出一个展开的 jar。
func TestSearchDeepRollsBackNestedJarOnMiss(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	items, err := app.SearchNodesDeep("Nope.class")
	if err != nil {
		t.Fatalf("深度搜失败: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("不该命中，实际: %+v", names(items))
	}
	if jar.Children != nil {
		t.Errorf("没命中时应当回滚解压结果，实际留下 %d 个子级", len(jar.Children))
	}
}

// 深度搜命中过一次之后，那个 jar 已经在树里展开了，**普通搜**也该跟着命中 ——
// 这是"保留展开"的天然好处：第二次再搜同一个名字不必再花 2.8s。
func TestSearchDeepThenInstantFindsIt(t *testing.T) {
	root, _ := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	if _, err := app.SearchNodesDeep("Deep.class"); err != nil {
		t.Fatalf("深度搜失败: %v", err)
	}
	got, err := app.SearchNodes("Deep.class")
	if err != nil {
		t.Fatalf("深度搜之后的普通搜失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("jar 已展开，普通搜应命中，实际 %d 条", len(got))
	}
}

// 路径式关键词不该触发兜底（它自己就会沿途展开嵌套 jar，
// searchNodes 里 len(paths)>1 直接 return 就是这个意思）。
func TestSearchDeepSkipsPathKeyword(t *testing.T) {
	root, jar := nestedJarTree(t, map[string]string{"org/Deep.class": "x"})
	app := searchApp(t, root, nil)

	// 故意给一个路径式关键词：即便它搜不到，也不该触发全量扫嵌套 jar
	if _, err := app.SearchNodesDeep("BOOT-INF/lib/lib.jar/org/Nope.class"); err != nil {
		t.Fatalf("深度搜失败: %v", err)
	}
	if jar.Children != nil {
		t.Errorf("路径式关键词走的是 searchWithPath（自带回滚），不该由兜底逻辑留下子级: %d", len(jar.Children))
	}
}

// ---------------------------------------------------------------------------
// 去重规则
// ---------------------------------------------------------------------------

// SearchItem 的 equals/hashCode 只看 (path, name)，所以同一 (path,name)
// 只留第一条 —— 对应原版的 LinkedHashSet。
func TestSearchCollectorDedupsByPathAndName(t *testing.T) {	c := newSearchCollector()
	c.add(&SearchItemView{Name: "a", Path: "p"})
	c.add(&SearchItemView{Name: "a", Path: "p"})
	c.add(&SearchItemView{Name: "a", Path: "q"})
	c.add(&SearchItemView{Name: "b", Path: "p"})

	if len(c.items) != 3 {
		t.Fatalf("应去重成 3 条，实际 %d 条", len(c.items))
	}
	// 插入顺序要保住
	got := []string{}
	for _, it := range c.items {
		got = append(got, it.Path+"/"+it.Name)
	}
	if strings.Join(got, ",") != "p/a,q/a,p/b" {
		t.Errorf("顺序不对: %v", got)
	}
}

// 历史项与节点项即便 (path,name) 相同也不互相去重 —— 原版是两个不同的类，
// equals 里有 getClass() != o.getClass() 这道闸。
func TestSearchHistoryAndNodeItemsDoNotCollide(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	// 历史项的 path 固定是 "Search History"，节点项的 path 是"根标签+祖先"，
	// 天然不会撞；这里钉住的是"两类结果各自独立存在"这件事。
	app.AddSearchHistory("Foo", "com/example/Foo.js")
	hist, _ := app.SearchNodes("")
	nodes, _ := app.SearchNodes("Foo")
	if len(hist) != 1 || len(nodes) != 1 {
		t.Fatalf("历史 %d 条 / 节点 %d 条", len(hist), len(nodes))
	}
	if !hist[0].History || nodes[0].History {
		t.Error("History 标志不对")
	}
}

// 历史项的 path 就是原版写死的字面量。
func TestSearchHistoryPathLiteral(t *testing.T) {
	app := searchApp(t, demoAppTree(), nil)
	app.AddSearchHistory("k", "t")
	items, _ := app.SearchNodes("")
	if items[0].Path != "Search History" {
		t.Errorf("历史项 path 应为字面量 Search History，实际 %q", items[0].Path)
	}
}

// 历史只在会话内 —— Go 版 GUI 没有配置文件（见 MIGRATION.md），
// 这条钉住"默认上限来自 config.NewSearchConf"，防止有人把默认值改没了。
func TestSearchConfDefaultComesFromConfig(t *testing.T) {
	app := NewApp()
	if app.search.HistoryLimit != config.NewSearchConf().HistoryLimit {
		t.Errorf("App 的搜索配置没取 config 的默认值: %d", app.search.HistoryLimit)
	}
}
