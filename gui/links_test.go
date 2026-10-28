package main

import (
	"strings"
	"testing"

	"cn.cpoet.patch.assistant/internal/tree"
)

// nodeAtDom 按 dom 路径（`r`、`r/0/3`）在树里取节点。
//
// 测试里用它**独立地**把两端解析回节点，再断言两端的 MappedNode 互为对方 ——
// 这样既能查出"dom 路径算错了"，也能查出"配对配错了"，而不用把
// collectDomPaths 的逻辑再抄一遍（抄一遍就成自证了）。
func nodeAtDom(t *testing.T, root *tree.Node, dom string) *tree.Node {
	t.Helper()
	parts := strings.Split(dom, "/")
	if len(parts) == 0 || parts[0] != domRoot {
		t.Fatalf("dom 路径 %q 不以 %q 开头", dom, domRoot)
	}
	n := root
	for _, p := range parts[1:] {
		var idx int
		for _, c := range p {
			if c < '0' || c > '9' {
				t.Fatalf("dom 路径 %q 里有非数字段 %q", dom, p)
			}
			idx = idx*10 + int(c-'0')
		}
		if idx < 0 || idx >= len(n.Children) {
			t.Fatalf("dom 路径 %q 越界：节点 %q 只有 %d 个子级", dom, n.Name, len(n.Children))
		}
		n = n.Children[idx]
	}
	return n
}

// openBothPacks 打开夹具里的应用包与补丁包（两边都打开才会跑匹配）。
func openBothPacks(t *testing.T) *App {
	t.Helper()
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	return app
}

// TestMappedLinksPairUpstreamNodes 钉住匹配连线的两端确实是一对。
//
// 每个 MappedLink 用 dom 路径解析回节点后，两端的 MappedNode 必须互为对方 ——
// 这条同时守着三件事：dom 路径算对了、配对没配错、两端都在同一棵树里。
func TestMappedLinksPairUpstreamNodes(t *testing.T) {
	app := openBothPacks(t)
	s := app.state()
	if len(s.MappedLinks) == 0 {
		t.Fatal("没有任何匹配连线：夹具里 patch.zip 的 Same.class / Foo.class 应该匹配上")
	}

	appRoot := app.appTreeInfo.RootNode()
	patchRoot := app.patchTreeInfo.RootNode()

	for _, l := range s.MappedLinks {
		a := nodeAtDom(t, appRoot, l.AppDom)
		p := nodeAtDom(t, patchRoot, l.PatchDom)
		if a.MappedNode != p || p.MappedNode != a {
			t.Errorf("连线两端不是一对: 应用侧 %q ↔ 补丁侧 %q", a.Path, p.Path)
		}
	}
}

// TestMappedLinksOnlyFiles 确认只连文件。
//
// 目录的映射是匹配过程的中间产物（"这一片都在补丁里"），连出来是一堆伞骨。
// 目录对上时它下面的文件各自有线，已经表达清楚了。
func TestMappedLinksOnlyFiles(t *testing.T) {
	app := openBothPacks(t)
	appRoot := app.appTreeInfo.RootNode()
	patchRoot := app.patchTreeInfo.RootNode()

	for _, l := range app.state().MappedLinks {
		if nodeAtDom(t, appRoot, l.AppDom).Dir() {
			t.Errorf("应用侧连到了一个目录: %q", l.AppDom)
		}
		if nodeAtDom(t, patchRoot, l.PatchDom).Dir() {
			t.Errorf("补丁侧连到了一个目录: %q", l.PatchDom)
		}
	}
}

// TestMappedLinksFollowDiffSwitch 钉住「总是显示连线」与「补丁文件比对」的关系。
//
// 设置里的约定：
//   - 打开「总是显示」       → 不管比对开关，都连线；
//   - 关掉「总是显示」       → 跟随「开启补丁文件比对」；
//   - 两个都关               → 一条线都没有。
func TestMappedLinksFollowDiffSwitch(t *testing.T) {
	app := openBothPacks(t)

	app.showMappedLinks = true
	app.settings.PatchFileDiff = false
	if n := len(app.state().MappedLinks); n == 0 {
		t.Error("「总是显示」打开时应该连线，实际一条都没有")
	}

	app.showMappedLinks = false
	app.settings.PatchFileDiff = true
	if n := len(app.state().MappedLinks); n == 0 {
		t.Error("关掉「总是显示」但比对开着时应该连线，实际一条都没有")
	}

	app.showMappedLinks = false
	app.settings.PatchFileDiff = false
	if n := len(app.state().MappedLinks); n != 0 {
		t.Errorf("两个开关都关时不该连线，实际 %d 条", n)
	}
}

// TestMappedLinksEmptyWithoutBothPacks 确认只开一个包时不连线。
func TestMappedLinksEmptyWithoutBothPacks(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if n := len(app.state().MappedLinks); n != 0 {
		t.Errorf("只开了应用包，不该有连线，实际 %d 条", n)
	}
}

// TestMappedLinksSurviveReopen 确认重开包之后连线还在（dom 路径是结构算的，
// 不依赖上一次的会话状态）。
func TestMappedLinksSurviveReopen(t *testing.T) {
	app := openBothPacks(t)
	first := len(app.state().MappedLinks)
	if first == 0 {
		t.Fatal("首轮没有连线")
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("重新打开补丁包失败: %v", err)
	}
	if second := len(app.state().MappedLinks); second != first {
		t.Errorf("重开后连线数变了: %d → %d", first, second)
	}
}
