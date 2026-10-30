package service

import (
	"testing"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 本文件测「整体替换项」（tree.Node.WholeReplace）。
//
// 背景：交付方常把"几个服务各自的完整包"塞进一个外层 zip，形如
//
//	patch.zip
//	├── 20261029_account-server_存量账户区分变更和普通.zip
//	└── 20261029_account-web_存量账户区分变更和普通.zip
//
// 内层包的 README 在第二层，RefreshReadmeNode 只扫根下、扫不到；而它们的
// 文件名（日期_服务名_描述）和应用里的真实文件名（account-server.jar）
// 对不上，MatchPatchName 那套全等/前缀匹配也无从下手。
//
// 用户 2026-10-29 要的是：把这些内层包单独标记成"替换文件"，整个换掉应用
// 里的对应项，内部不再参与逐文件匹配。

// wholeReplaceFixture 造一套"外层补丁包 + 内层整包"的夹具。
//
// 应用包：account-server.jar / account-web.war（都当普通文件，不解压）
// 补丁包：两个带日期前缀的内层 zip，名字里各自含服务名
func wholeReplaceFixture(t *testing.T) (*tree.AppTreeInfo, *tree.PatchTreeInfo, *tree.TotalInfo) {
	t.Helper()
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.war", []zipEntry{
		{name: "account-server.jar", body: "APP-SERVER"},
		{name: "account-web.war", body: "APP-WEB"},
	})
	patchZip := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "20261029_account-server_存量账户区分变更和普通.zip", body: "PATCH-SERVER"},
		{name: "20261029_account-web_存量账户区分变更和普通.zip", body: "PATCH-WEB"},
	})

	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	return appTreeInfo, patchTreeInfo, tree.NewTotalInfo()
}

// TestWholeReplaceMatchesByContainedName 标记后按"名字被包含"认目标，整体替换。
func TestWholeReplaceMatchesByContainedName(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := wholeReplaceFixture(t)
	root := patchTreeInfo.RootNode()

	server := root.Children[0]
	web := root.Children[1]
	server.WholeReplace = true
	web.WholeReplace = true

	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	appServer := findNode(t, appTreeInfo.RootNode(), "account-server.jar")
	if appServer.Type != constant.TreeNodeTypeMod {
		t.Fatalf("account-server.jar 应被整体替换（MOD），实际 %s", appServer.Type)
	}
	if appServer.MappedNode != server {
		t.Error("account-server.jar 应绑定到补丁里那个 account-server 内层包")
	}

	appWeb := findNode(t, appTreeInfo.RootNode(), "account-web.war")
	if appWeb.Type != constant.TreeNodeTypeMod {
		t.Fatalf("account-web.war 应被整体替换（MOD），实际 %s", appWeb.Type)
	}
	if appWeb.MappedNode != web {
		t.Error("account-web.war 应绑定到补丁里那个 account-web 内层包")
	}

	if total.ModTotal() != 2 || total.AddTotal() != 0 {
		t.Errorf("统计不符：mod=%d add=%d（期望 2/0）", total.ModTotal(), total.AddTotal())
	}
}

// TestWholeReplaceChildrenDoNotParticipate 标记后内部文件不再参与逐文件匹配。
//
// 这是"整体替换"与"按名字拆开打补丁"的分水岭：内层包里的 class 不能被
// 挑出来单独映射到应用树的任何地方。
func TestWholeReplaceChildrenDoNotParticipate(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := wholeReplaceFixture(t)
	root := patchTreeInfo.RootNode()

	inner := root.Children[0]
	// 模拟"用户展开过内层包"：给它挂一个子级，名字故意和应用里的某个
	// 顶层项同名，用来验证它不会被常规通道捡走。
	inner.AddChild(tree.NewFile("account-web.war", "account-web.war", 4, time.Now(), nil, true))
	inner.WholeReplace = true

	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	// 子级名字虽然和 account-web.war 全等，但整体替换项的子级不参与匹配 ——
	// 所以 account-web.war 不该被它命中，保持原样。
	appWeb := findNode(t, appTreeInfo.RootNode(), "account-web.war")
	if appWeb.MappedNode != nil {
		t.Errorf("整体替换项的子级不该参与逐文件匹配，account-web.war 却绑到了 %s", appWeb.MappedNode.Name)
	}
	// 而它自己整体替换了 account-server.jar
	if appServer := findNode(t, appTreeInfo.RootNode(), "account-server.jar"); appServer.MappedNode != inner {
		t.Error("account-server.jar 应绑定到那个整体替换项")
	}
}

// TestWholeReplaceUnmarkedDoesNothing 不标记就什么都不做 —— 名字对不上。
//
// 钉住"标记是必要动作"：这套文件名跟应用里的本来就不一样，常规匹配一定
// 匹配不到，也不会自作主张新建节点。
func TestWholeReplaceUnmarkedDoesNothing(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := wholeReplaceFixture(t)
	root := patchTreeInfo.RootNode()

	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	if total.ModTotal() != 0 || total.AddTotal() != 0 {
		t.Errorf("不标记时不该有任何映射，实际 mod=%d add=%d", total.ModTotal(), total.AddTotal())
	}
	if n := findNode(t, appTreeInfo.RootNode(), "account-server.jar"); n.MappedNode != nil {
		t.Error("不标记时 account-server.jar 不该被绑定")
	}
}

// TestWholeReplacePrefersLongestStem 多个候选命中时取主名最长的那个。
//
// 应用里同时有 account.jar 和 account-server.jar，补丁名
// "20261029_account-server_xxx.zip" 把两个主名都包含了 —— 必须选更具体的。
func TestWholeReplacePrefersLongestStem(t *testing.T) {
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.war", []zipEntry{
		{name: "account.jar", body: "APP-SHORT"},
		{name: "account-server.jar", body: "APP-LONG"},
	})
	patchZip := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "20261029_account-server_变更.zip", body: "PATCH-SERVER"},
	})
	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	total := tree.NewTotalInfo()

	root := patchTreeInfo.RootNode()
	root.Children[0].WholeReplace = true
	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	long := findNode(t, appTreeInfo.RootNode(), "account-server.jar")
	short := findNode(t, appTreeInfo.RootNode(), "account.jar")
	if long.MappedNode == nil {
		t.Fatal("account-server.jar 应该被命中（主名更长、更具体）")
	}
	if short.MappedNode != nil {
		t.Error("account.jar 不该被命中 —— 补丁名里更具体的 account-server 已经占用了")
	}
}

// TestWholeReplaceUnknownNameLeavesUnmapped 名字认不出来时不猜、不新建。
func TestWholeReplaceUnknownNameLeavesUnmapped(t *testing.T) {
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.war", []zipEntry{
		{name: "account-server.jar", body: "APP-SERVER"},
	})
	patchZip := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "20261029_payment_变更.zip", body: "PATCH-PAY"},
	})
	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	total := tree.NewTotalInfo()

	root := patchTreeInfo.RootNode()
	root.Children[0].WholeReplace = true
	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	if total.ModTotal() != 0 || total.AddTotal() != 0 {
		t.Errorf("认不出目标时不该有任何映射，实际 mod=%d add=%d", total.ModTotal(), total.AddTotal())
	}
	// 也不该在应用树里凭空造一个 "payment" 节点
	if n := tree.FindNodeByPath(appTreeInfo.RootNode(), "20261029_payment_变更.zip"); n != nil {
		t.Error("认不出目标时不该新建节点")
	}
}

// TestWholeReplaceTwoTargetsAreNotShared 两个整体替换项不会抢同一个目标。
//
// 用 used 集合保证"一个应用节点只能被一个整体替换项占用" —— 否则后一个会
// 覆盖前一个，用户看到的是"明明标了两个，只有一个生效"。
func TestWholeReplaceTwoTargetsAreNotShared(t *testing.T) {
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.war", []zipEntry{
		{name: "account-server.jar", body: "APP-SERVER"},
	})
	patchZip := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "20261029_account-server_第一次.zip", body: "PATCH-1"},
		{name: "20261029_account-server_第二次.zip", body: "PATCH-2"},
	})
	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	total := tree.NewTotalInfo()

	root := patchTreeInfo.RootNode()
	root.Children[0].WholeReplace = true
	root.Children[1].WholeReplace = true
	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, root, defaultOptions())

	if total.ModTotal() != 1 {
		t.Errorf("两个整体替换项抢同一个目标时只应有一个生效，实际 mod=%d", total.ModTotal())
	}
}
