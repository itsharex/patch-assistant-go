package service

import (
	"archive/zip"
	"bytes"
	"path/filepath"
	"testing"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 本文件测「补丁树 → 应用树」的拖放映射（ApplyPatchNodesToApp）。
//
// 这条链路对应原版 FileTreeCell.onDragDropped 里 dragInfo != null 的分支，
// 落点只决定**目标目录**，文件身份仍然由名字决定：
//
//   - 目标目录里已有同名子项 → 改绑成 MOD；
//   - 没有 → 造一个映射节点（ADD）；
//   - 单个目录落到目录行上 → 把这个目录的**子项**逐个并进去。
//
// 另外两条容易被写错、又不会报错的点也在这里固化：
//   - 落到**文件行**上要退回它的父目录（不是替换那一行）；
//   - 同一个补丁节点重复拖 / 拖到别处，统计不能翻倍（先解绑旧位置）。

// dragFixture 造一套最小可拖拽夹具。
//
// 应用包：a/{x,y}.class + b/x.class —— 两个目录里都有 x.class，
// 用来验证"改指到别处"时旧位置会不会被正确解绑。
// 补丁包：d/{x,z}.class —— x 能命中，z 命不中。
func dragFixture(t *testing.T) (*tree.AppTreeInfo, *tree.PatchTreeInfo, *tree.TotalInfo) {
	t.Helper()
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.jar", []zipEntry{
		{name: "a/x.class", body: "X-ORIGINAL"},
		{name: "a/y.class", body: "Y-ORIGINAL"},
		{name: "b/x.class", body: "X-OTHER"},
	})
	patchZip := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "d/x.class", body: "X-PATCHED"},
		{name: "d/z.class", body: "Z-PATCHED"},
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

// TestApplyPatchFileMatchesExisting 拖一个补丁文件到有同名项的目录 → MOD。
func TestApplyPatchFileMatchesExisting(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	target := findNode(t, appTreeInfo.RootNode(), "a")
	patchX := findNode(t, patchTreeInfo.RootNode(), "d/x.class")

	ApplyPatchNodesToApp(total, appTreeInfo, target, []*tree.Node{patchX}, defaultOptions())

	got := findNode(t, appTreeInfo.RootNode(), "a/x.class")
	if got.Type != constant.TreeNodeTypeMod {
		t.Errorf("a/x.class 应被标成 MOD，实际 %s", got.Type)
	}
	if got.MappedNode != patchX {
		t.Error("a/x.class 应绑定到补丁侧的 d/x.class")
	}
	if patchX.MappedNode != got {
		t.Error("绑定应当是双向的：补丁节点也要指回应用节点")
	}
	if total.ModTotal() != 1 || total.AddTotal() != 0 {
		t.Errorf("统计不符：add=%d mod=%d（期望 0/1）", total.AddTotal(), total.ModTotal())
	}
	// 同名文件只在目标目录里找 —— b/x.class 不该被顺带改掉
	if other := findNode(t, appTreeInfo.RootNode(), "b/x.class"); other.Type != constant.TreeNodeTypeNone {
		t.Errorf("b/x.class 不在落点目录下，不该被匹配，实际 %s", other.Type)
	}
}

// TestApplyPatchFileCreatesWhenMissing 拖到没有同名项的目录 → 造一个 ADD 节点。
func TestApplyPatchFileCreatesWhenMissing(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	target := findNode(t, appTreeInfo.RootNode(), "b")
	patchZ := findNode(t, patchTreeInfo.RootNode(), "d/z.class")

	ApplyPatchNodesToApp(total, appTreeInfo, target, []*tree.Node{patchZ}, defaultOptions())

	got := findNode(t, appTreeInfo.RootNode(), "b/z.class")
	if got.Type != constant.TreeNodeTypeAdd {
		t.Errorf("凭空造出来的节点应为 ADD，实际 %s", got.Type)
	}
	if got.Kind != tree.KindMapped {
		t.Errorf("应当是 MappedNode（虚拟绑定到补丁节点），实际 %s", got.Kind)
	}
	if got.MappedNode != patchZ {
		t.Error("新节点应绑定到补丁侧的 d/z.class")
	}
	if got.Parent != target {
		t.Error("新节点应当挂在落点目录下")
	}
	if total.AddTotal() != 1 || total.ModTotal() != 0 {
		t.Errorf("统计不符：add=%d mod=%d（期望 1/0）", total.AddTotal(), total.ModTotal())
	}
}

// TestApplyPatchDirMergesChildren 单个目录落到目录行 → 把子项逐个并进去。
//
// 这条与"拖一个文件"的差别就在这儿：落点是目录、拖的也是目录时，不是把
// 整个目录塞进去（那会变成 a/d/x.class），而是把 d 的子项当成 a 的子项。
func TestApplyPatchDirMergesChildren(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	target := findNode(t, appTreeInfo.RootNode(), "a")
	patchD := findNode(t, patchTreeInfo.RootNode(), "d")

	ApplyPatchNodesToApp(total, appTreeInfo, target, []*tree.Node{patchD}, defaultOptions())

	if x := findNode(t, appTreeInfo.RootNode(), "a/x.class"); x.Type != constant.TreeNodeTypeMod {
		t.Errorf("并进来的 x.class 应命中同名项并标成 MOD，实际 %s", x.Type)
	}
	if z := findNode(t, appTreeInfo.RootNode(), "a/z.class"); z.Type != constant.TreeNodeTypeAdd {
		t.Errorf("并进来的 z.class 应新建为 ADD，实际 %s", z.Type)
	}
	// 不该多出一层 a/d/
	if tree.FindNodeByPath(appTreeInfo.RootNode(), "a/d") != nil {
		t.Error("目录落到目录行上时应当合并子项，而不是把目录本身塞进去")
	}
	if total.ModTotal() != 1 || total.AddTotal() != 1 {
		t.Errorf("统计不符：add=%d mod=%d（期望 1/1）", total.AddTotal(), total.ModTotal())
	}
}

// TestApplyPatchOntoFileRowUsesParent 落到文件行上 → 退回它的父目录。
//
// 原版 `appNode.isDir() ? appNode : appNode.getParent()`。这条最容易被误实现成
// "替换被落中的那个文件" —— 但文件身份是由**名字**决定的，落点只给目录。
func TestApplyPatchOntoFileRowUsesParent(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	fileRow := findNode(t, appTreeInfo.RootNode(), "a/y.class")
	patchZ := findNode(t, patchTreeInfo.RootNode(), "d/z.class")

	ApplyPatchNodesToApp(total, appTreeInfo, fileRow, []*tree.Node{patchZ}, defaultOptions())

	if fileRow.Type != constant.TreeNodeTypeNone || fileRow.MappedNode != nil {
		t.Errorf("被落中的文件行不该被改动，实际 type=%s mapped=%v", fileRow.Type, fileRow.MappedNode)
	}
	z := findNode(t, appTreeInfo.RootNode(), "a/z.class")
	if z.Type != constant.TreeNodeTypeAdd {
		t.Errorf("应当加进父目录 a/，实际 %s", z.Type)
	}
	if z.Parent != fileRow.Parent {
		t.Error("新节点的父级应当是被落中文件的父目录")
	}
}

// TestApplyPatchTwiceDoesNotDoubleCount 同一个补丁节点连拖两次 → 统计不翻倍。
func TestApplyPatchTwiceDoesNotDoubleCount(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	target := findNode(t, appTreeInfo.RootNode(), "a")
	patchX := findNode(t, patchTreeInfo.RootNode(), "d/x.class")

	ApplyPatchNodesToApp(total, appTreeInfo, target, []*tree.Node{patchX}, defaultOptions())
	ApplyPatchNodesToApp(total, appTreeInfo, target, []*tree.Node{patchX}, defaultOptions())

	if total.ModTotal() != 1 {
		t.Errorf("拖两次仍然只应算 1 个修改，实际 %d", total.ModTotal())
	}
	n := 0
	for _, c := range target.Children {
		if c.Name == "x.class" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("目标目录下应当只有一个 x.class，实际 %d 个", n)
	}
}

// TestApplyPatchRemapsFromOldPlace 已经绑在别处的补丁节点改指到新位置。
//
// 【与上游的偏差】原版 onDragOver 里 `isHasMappedNode()` 为真就拒绝整次拖放，
// 而打开补丁包时自动匹配几乎会把所有节点都映射一遍 —— 等于这条拖拽平时用不上。
// 这里改成"先解绑旧位置再落到新位置"，语义是"把这条补丁改指到别处"，
// 所以旧位置必须干净地回到 NONE、统计也不能留残值。
func TestApplyPatchRemapsFromOldPlace(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	opts := defaultOptions()

	// 先像自动匹配那样把补丁的 x.class 绑到 a/x.class
	aX := findNode(t, appTreeInfo.RootNode(), "a/x.class")
	patchX := findNode(t, patchTreeInfo.RootNode(), "d/x.class")
	MappedModNodeWithType(total, appTreeInfo, aX, patchX, opts)
	if total.ModTotal() != 1 {
		t.Fatalf("前置条件不成立：应当已统计到 1 个修改，实际 %d", total.ModTotal())
	}

	// 再拖到 b/ 下
	bDir := findNode(t, appTreeInfo.RootNode(), "b")
	ApplyPatchNodesToApp(total, appTreeInfo, bDir, []*tree.Node{patchX}, opts)

	if aX.Type != constant.TreeNodeTypeNone || aX.MappedNode != nil {
		t.Errorf("旧位置应当被解绑并回到 NONE，实际 type=%s mapped=%v", aX.Type, aX.MappedNode)
	}
	bX := findNode(t, appTreeInfo.RootNode(), "b/x.class")
	if bX.Type != constant.TreeNodeTypeMod || bX.MappedNode != patchX {
		t.Errorf("新位置应当标成 MOD 并绑定到补丁节点，实际 type=%s", bX.Type)
	}
	if total.ModTotal() != 1 {
		t.Errorf("改指之后统计仍应是 1 个修改，实际 %d", total.ModTotal())
	}
}

// TestApplyPatchOntoRootAddsAtRoot 落到包根行 → 加到包内顶层。
//
// 应用包根节点是**文件节点**（对齐 Java 的 FileNode），但它没有 Parent，
// 早期实现会因此静默丢弃整次拖放（用户看到的是"拖了没反应"）。
// 它自己就该当容器用，而且新节点的 Path 不能是磁盘路径拼出来的。
func TestApplyPatchOntoRootAddsAtRoot(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	root := appTreeInfo.RootNode()
	patchZ := findNode(t, patchTreeInfo.RootNode(), "d/z.class")

	ApplyPatchNodesToApp(total, appTreeInfo, root, []*tree.Node{patchZ}, defaultOptions())

	z := findNode(t, appTreeInfo.RootNode(), "z.class")
	if z.Type != constant.TreeNodeTypeAdd {
		t.Errorf("根下新增的节点应为 ADD，实际 %s", z.Type)
	}
	if z.Parent != root {
		t.Error("新节点应当直接挂在包根节点下")
	}
	if z.Path != "z.class" {
		t.Errorf("根下条目的 Path 应当是自己的名字，实际 %q", z.Path)
	}
	if total.AddTotal() != 1 {
		t.Errorf("统计不符：add=%d（期望 1）", total.AddTotal())
	}
}

// TestApplyPatchRootActsAsFile 拖**补丁包的根行**（整个包）→ 当成一个**文件**。
//
// 用户 2026-10-28 第一轮报："右侧的 jar 包不能直接把这个 jar 包拖动到左侧"，
// 第二轮报："我把右侧的 jar 包拖到 lib 文件夹下面时，他把 jar 包分解放里面了"。
// 两条合起来才是真需求：根行代表的是**那个 .jar 文件本身**，拖到 lib 下的意思
// 是"把这个 jar 放进 lib"（有同名就换、没有就加），**不是**把包拆开铺进去。
//
// 早期我把它当"整包套用"（并子项），于是 lib 里会冒出一堆 `com/`、`META-INF/`。
func TestApplyPatchRootActsAsFile(t *testing.T) {
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.jar", []zipEntry{
		{name: "lib/dep-1.0.0.jar", body: "OLD-JAR-BYTES"},
		{name: "a/x.class", body: "X-ORIGINAL"},
	})
	// 补丁包本身就是个 jar（用户的实际形态：拿改过的依赖 jar 当补丁包）
	patchZip := writeZip(t, dir, "dep-1.0.0.jar", []zipEntry{
		{name: "com/example/Dep.class", body: "DEP-NEW"},
	})

	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	patchRoot := patchTreeInfo.RootNode()
	if got := patchRoot.Name; got != "dep-1.0.0.jar" {
		t.Fatalf("补丁根节点的名字应当是包文件名，实际 %q", got)
	}

	total := tree.NewTotalInfo()
	lib := findNode(t, appTreeInfo.RootNode(), "lib")
	ApplyPatchNodesToApp(total, appTreeInfo, lib, []*tree.Node{patchRoot}, defaultOptions())

	// 命中同名 jar → MOD，绑定到补丁根（内容 = 整个新 jar）
	dep := findNode(t, appTreeInfo.RootNode(), "lib/dep-1.0.0.jar")
	if dep.Type != constant.TreeNodeTypeMod {
		t.Errorf("lib/dep-1.0.0.jar 应被标成 MOD，实际 %s", dep.Type)
	}
	if dep.MappedNode != patchRoot {
		t.Error("应当绑定到补丁包的根节点（整个 jar 换掉）")
	}
	// 关键：**不能被拆开铺进去**
	if tree.FindNodeByPath(appTreeInfo.RootNode(), "lib/com") != nil {
		t.Error("拖整包不该把 jar 拆开，lib 下不该出现 com/ 目录")
	}
	if len(lib.Children) != 1 {
		t.Errorf("lib 下应当仍然只有 1 项（被替换的那个 jar），实际 %d 项", len(lib.Children))
	}
	if total.ModTotal() != 1 || total.AddTotal() != 0 {
		t.Errorf("统计不符：add=%d mod=%d（期望 0/1）", total.AddTotal(), total.ModTotal())
	}

	// 落到没有同名项的目录 → 按**文件名**新增一项
	a := findNode(t, appTreeInfo.RootNode(), "a")
	total2 := tree.NewTotalInfo()
	ApplyPatchNodesToApp(total2, appTreeInfo, a, []*tree.Node{patchRoot}, defaultOptions())
	if got := findNode(t, appTreeInfo.RootNode(), "a/dep-1.0.0.jar"); got.Type != constant.TreeNodeTypeAdd {
		t.Errorf("a/ 下没有同名项，应当新增 a/dep-1.0.0.jar，实际 %s", got.Type)
	}
}

// TestSaveAppPackWritesReplacedWholeJar 整个 jar 被换掉后，**保存出来的必须是新内容**。
//
// 这条是上面那条的"后半段"，也是最容易假绿的地方：映射关系建对了、界面上也标了
// MOD，但写出时仍拿应用节点自己的字节 —— 保存出来的还是旧 jar，用户完全看不出来。
// 原版 `writeTreeNode2PackWithJar` 只看 children、**完全不看 MappedNode**，
// 于是"把 jar 整个换进 BOOT-INF/lib"和 README 里对 jar 写 `!路径` 都是静默无效。
func TestSaveAppPackWritesReplacedWholeJar(t *testing.T) {
	dir := t.TempDir()
	appJar := writeZip(t, dir, "app.jar", []zipEntry{
		{name: "BOOT-INF/lib/dep-1.0.0.jar", body: "OLD-JAR-BYTES"},
		{name: "BOOT-INF/classes/Foo.class", body: "FOO"},
	})
	patchZip := writeZip(t, dir, "dep-1.0.0.jar", []zipEntry{
		{name: "com/example/Dep.class", body: "DEP-NEW"},
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
	lib := findNode(t, appTreeInfo.RootNode(), "BOOT-INF/lib")
	ApplyPatchNodesToApp(total, appTreeInfo, lib, []*tree.Node{patchTreeInfo.RootNode()}, defaultOptions())

	outJar := filepath.Join(dir, "out.jar")
	if err := SaveAppPack(&WriteContext{
		AppTreeInfo:   appTreeInfo,
		PatchTreeInfo: patchTreeInfo,
		Total:         total,
		TempDir:       dir,
	}, outJar); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	contents := readZipContents(t, outJar)
	inner := contents["BOOT-INF/lib/dep-1.0.0.jar"]
	if inner == "OLD-JAR-BYTES" {
		t.Fatal("保存出来的还是旧 jar —— 说明写出环节没看 MappedNode（原版 bug）")
	}
	// 新内容应当是一个能解析的 zip，且带补丁里的条目
	zr, err := zip.NewReader(bytes.NewReader([]byte(inner)), int64(len(inner)))
	if err != nil {
		t.Fatalf("替换后的 jar 不是有效的 zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) != 1 || names[0] != "com/example/Dep.class" {
		t.Errorf("替换后的 jar 应当只含补丁里的条目，实际 %v", names)
	}
	// 没被碰过的条目照旧
	if got := contents["BOOT-INF/classes/Foo.class"]; got != "FOO" {
		t.Errorf("未涉及的条目应原样保留，实际 %q", got)
	}
}

// TestApplyPatchNilTargetIsNoop 落点为空时什么都不做（不 panic）。
func TestApplyPatchNilTargetIsNoop(t *testing.T) {
	appTreeInfo, patchTreeInfo, total := dragFixture(t)
	patchX := findNode(t, patchTreeInfo.RootNode(), "d/x.class")

	ApplyPatchNodesToApp(total, appTreeInfo, nil, []*tree.Node{patchX}, defaultOptions())
	if total.IsChangeNode() {
		t.Errorf("落点为空时不该有任何变更统计，实际 add=%d mod=%d del=%d",
			total.AddTotal(), total.ModTotal(), total.DelTotal())
	}
}
