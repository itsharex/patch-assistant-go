package service

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// 本文件是「把编辑后的 README 写回补丁包」这一段的端到端测试，
// 对应 Java PatchPackService.updatePatchReadme 及其三个私有辅助方法。
//
// 覆盖四条路径：
//
//  1. 补丁包（zip）里已有 README.txt —— 更新内容，其余条目原样保留；
//  2. 补丁包里没有 README —— 新建一个条目写进去；
//  3. 补丁包叫 .jar 而不是 .zip —— 节点名会被改成 .zip（上游行为）；
//  4. 补丁是【目录】而不是压缩包 —— README 直接写回磁盘；
//  5. README 挂在嵌套 jar 上 —— 逐级重写（内层 jar → 外层补丁包）。
//
// 另外用一个纯单元用例钉住上游回滚逻辑里的一个 bug（见文末）。

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// newWriteCtx 构造一个指向临时目录的写出上下文。
func newWriteCtx(t *testing.T, patchTreeInfo *tree.PatchTreeInfo) *WriteContext {
	t.Helper()
	return &WriteContext{
		PatchTreeInfo: patchTreeInfo,
		ReadmeFile:    constant.ReadmeFile,
		TempDir:       t.TempDir(),
	}
}

// readZipEntryBytes 读出 zip 里某个条目的原始内容。
func readZipEntryBytes(t *testing.T, zipPath, entryName string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 zip %s 失败: %v", zipPath, err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != entryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开条目 %s 失败: %v", entryName, err)
		}
		defer rc.Close()
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatalf("读取条目 %s 失败: %v", entryName, err)
		}
		return b.Bytes()
	}
	t.Fatalf("zip %s 里没有条目 %s", zipPath, entryName)
	return nil
}

// readZipContentsFromBytes 把内存里的 zip 读成 路径 → 内容 的映射（目录记为 "<dir>"）。
func readZipContentsFromBytes(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析内存 zip 失败: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			out[f.Name] = "<dir>"
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开条目 %s 失败: %v", f.Name, err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatalf("读取条目 %s 失败: %v", f.Name, err)
		}
		rc.Close()
		out[f.Name] = b.String()
	}
	return out
}

// countZipEntries 统计 zip 里指定名字的条目个数，用于确认没有写重复条目。
func countZipEntries(t *testing.T, zipPath, entryName string) int {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 zip %s 失败: %v", zipPath, err)
	}
	defer zr.Close()
	n := 0
	for _, f := range zr.File {
		if f.Name == entryName {
			n++
		}
	}
	return n
}

// bindReadme 走一遍真实流程：建树后由 RefreshReadmeNode 找到并绑定 README 节点。
func bindReadme(t *testing.T, patchTreeInfo *tree.PatchTreeInfo) {
	t.Helper()
	RefreshReadmeNode(patchTreeInfo, patchTreeInfo.RootNode(), constant.ReadmeFile, nil)
}

// ---------------------------------------------------------------------------
// 用例一：补丁包内已有 README.txt，更新内容
// ---------------------------------------------------------------------------

func TestUpdatePatchReadmeExisting(t *testing.T) {
	dir := t.TempDir()
	oldReadme := "旧的说明\n!BOOT-INF/classes/com/example/Foo.class\n"
	patchPath := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "README.txt", body: oldReadme},
		{name: "BOOT-INF/", isDir: true},
		{name: "BOOT-INF/classes/", isDir: true},
		{name: "BOOT-INF/classes/com/example/Foo.class", body: "FOO-PATCHED"},
	})

	patchTreeInfo, err := GetPatchTreeNode(patchPath, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	bindReadme(t, patchTreeInfo)

	rootNode := patchTreeInfo.RootNode()
	rootInfo := patchTreeInfo.RootInfoByNode(rootNode)
	if rootInfo == nil || rootInfo.ReadmeNode == nil {
		t.Fatal("RefreshReadmeNode 应当已绑定 README.txt")
	}
	if rootInfo.ReadmeNode.Kind != tree.KindCompress {
		t.Fatalf("zip 里的 README 节点应是 KindCompress，实际 %v —— "+
			"退化成 KindFile 会让文本被写到当前工作目录", rootInfo.ReadmeNode.Kind)
	}
	if got := rootInfo.PatchSign.Readme; got != oldReadme {
		t.Fatalf("绑定后 PatchSign.Readme 应等于原内容，实际 %q", got)
	}

	newText := "新的说明\n!BOOT-INF/classes/com/example/Foo.class\n+BOOT-INF/classes/com/example/New.class\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, rootNode, newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	// 磁盘上的补丁包应被就地覆盖
	got := readZipContents(t, patchPath)
	if got["README.txt"] != newText {
		t.Fatalf("补丁包内 README.txt 内容不对\n期望: %q\n实际: %q", newText, got["README.txt"])
	}
	if got["BOOT-INF/classes/com/example/Foo.class"] != "FOO-PATCHED" {
		t.Fatalf("其余条目应原样保留，实际 %q", got["BOOT-INF/classes/com/example/Foo.class"])
	}
	if got["BOOT-INF/"] != "<dir>" || got["BOOT-INF/classes/"] != "<dir>" {
		t.Fatalf("目录条目应当保留且带尾部斜杠，实际: %v", got)
	}
	if n := countZipEntries(t, patchPath, "README.txt"); n != 1 {
		t.Fatalf("README.txt 应只有一个条目，实际 %d 个", n)
	}

	// 树侧状态
	if patchTreeInfo.RootInfo().PatchSign.Readme != newText {
		t.Fatal("PatchSign.Readme 未更新")
	}
	if patchTreeInfo.ReadmeText() != newText {
		t.Fatalf("ReadmeText 未刷新，实际 %q", patchTreeInfo.ReadmeText())
	}
	// 重写后根节点的 md5/大小应按新内容重算
	if rootNode.Size <= 0 || rootNode.Md5 == "" {
		t.Fatalf("根节点应重算大小与 md5，实际 size=%d md5=%q", rootNode.Size, rootNode.Md5)
	}
}

// ---------------------------------------------------------------------------
// 用例二：补丁包里没有 README，新建一个
// ---------------------------------------------------------------------------

func TestUpdatePatchReadmeCreateWhenMissing(t *testing.T) {
	dir := t.TempDir()
	patchPath := writeZip(t, dir, "patch.zip", []zipEntry{
		{name: "com/example/Foo.class", body: "FOO-PATCHED"},
	})

	patchTreeInfo, err := GetPatchTreeNode(patchPath, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	bindReadme(t, patchTreeInfo)
	if patchTreeInfo.RootInfo().ReadmeNode != nil {
		t.Fatal("补丁包里本没有 README，不应绑定到节点")
	}

	newText := "首次生成的说明\n!com/example/Foo.class\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, patchTreeInfo.RootNode(), newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	got := readZipContents(t, patchPath)
	if got["README.txt"] != newText {
		t.Fatalf("补丁包内应出现 README.txt 且内容正确，实际: %v", got)
	}
	if got["com/example/Foo.class"] != "FOO-PATCHED" {
		t.Fatalf("原有条目应保留，实际: %v", got)
	}

	readmeNode := patchTreeInfo.RootInfo().ReadmeNode
	if readmeNode == nil {
		t.Fatal("新建的 README 节点应已绑定到根信息")
	}
	if readmeNode.Kind != tree.KindCompress {
		t.Fatalf("压缩包根的 README 应是 KindCompress，实际 %v", readmeNode.Kind)
	}
	if readmeNode.Path != constant.ReadmeFile {
		t.Fatalf("压缩包内 README 的 Path 应是裸文件名，实际 %q", readmeNode.Path)
	}
	if readmeNode.Type != constant.TreeNodeTypeReadme {
		t.Fatalf("README 节点状态应为 README，实际 %v", readmeNode.Type)
	}
	if readmeNode.Md5 != util.Md5([]byte(newText)) {
		t.Fatal("README 节点的 md5 应等于新内容的 md5")
	}
}

// ---------------------------------------------------------------------------
// 用例三：补丁包叫 .jar —— 节点名会被改成 .zip（上游行为）
// ---------------------------------------------------------------------------

func TestUpdatePatchReadmeRenamesNonZipRoot(t *testing.T) {
	dir := t.TempDir()
	patchPath := writeZip(t, dir, "patch.jar", []zipEntry{
		{name: "com/example/Foo.class", body: "FOO-PATCHED"},
	})

	patchTreeInfo, err := GetPatchTreeNode(patchPath, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	rootNode := patchTreeInfo.RootNode()
	if rootNode.Name != "patch.jar" {
		t.Fatalf("前置条件：根节点名应为 patch.jar，实际 %q", rootNode.Name)
	}
	if rootNode.Kind == tree.KindCompress {
		t.Fatal("前置条件：补丁包根节点由 GetPatchTreeNode 建成文件节点，Kind 应为 KindFile")
	}

	newText := "说明\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, rootNode, newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	// 保真点：updatePatchPackWithZip 会把非 .zip 结尾的节点名改成 .zip
	if rootNode.Name != "patch.zip" {
		t.Fatalf("根节点名应被改成 patch.zip（上游行为），实际 %q", rootNode.Name)
	}
	// 但磁盘文件名不变 —— 结果会被 copyTo 回原路径
	if _, err := os.Stat(patchPath); err != nil {
		t.Fatalf("补丁包原路径应仍存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "patch.zip")); err == nil {
		t.Fatal("不应在磁盘上额外产生 patch.zip")
	}
	got := readZipContents(t, patchPath)
	if got["README.txt"] != newText {
		t.Fatalf("README 应已写入补丁包，实际: %v", got)
	}
}

// ---------------------------------------------------------------------------
// 用例四：补丁是目录 —— README 直接写回磁盘
// ---------------------------------------------------------------------------

func TestUpdatePatchReadmeDirMode(t *testing.T) {
	dir := t.TempDir()
	patchDir := filepath.Join(dir, "patch-src")
	if err := os.MkdirAll(filepath.Join(patchDir, "com", "example"), 0o755); err != nil {
		t.Fatalf("建补丁目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(patchDir, "com", "example", "Foo.class"), []byte("FOO-PATCHED"), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	patchTreeInfo, err := GetPatchTreeNode(patchDir, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	rootNode := patchTreeInfo.RootNode()
	if !rootNode.Dir() {
		t.Fatal("前置条件：补丁目录模式下根节点应是目录")
	}

	newText := "目录模式的说明\n!com/example/Foo.class\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, rootNode, newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	readmePath := filepath.Join(patchDir, constant.ReadmeFile)
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("README 应已写到磁盘 %s: %v", readmePath, err)
	}
	if string(raw) != newText {
		t.Fatalf("磁盘上 README 内容不对\n期望: %q\n实际: %q", newText, string(raw))
	}

	readmeNode := patchTreeInfo.RootInfo().ReadmeNode
	if readmeNode == nil {
		t.Fatal("README 节点应已绑定")
	}
	if readmeNode.Kind != tree.KindFile {
		t.Fatalf("目录模式下的 README 应是 KindFile，实际 %v", readmeNode.Kind)
	}
	if readmeNode.Size != int64(len(newText)) {
		t.Fatalf("README 节点大小应为 %d，实际 %d", len(newText), readmeNode.Size)
	}
	// 不应把补丁目录本身重写成一个 zip
	if _, err := os.Stat(filepath.Join(dir, "patch-src.zip")); err == nil {
		t.Fatal("目录模式不应把补丁目录改写成 zip")
	}
}

// ---------------------------------------------------------------------------
// 用例五：README 挂在嵌套 jar 上 —— 逐级重写
// ---------------------------------------------------------------------------

// buildNestedPatch 造一个"补丁包里套 jar"的现场：
// p.zip 顶层放 foo.jar，foo.jar 里有一个 class。
// 返回补丁包路径、补丁树信息、以及那个 foo.jar 节点。
func buildNestedPatch(t *testing.T) (string, *tree.PatchTreeInfo, *tree.Node) {
	t.Helper()
	dir := t.TempDir()
	inner := innerJarBytes(t, map[string]string{"com/example/Bar.class": "BAR-PATCHED"})

	patchPath := filepath.Join(dir, "p.zip")
	f, err := os.Create(patchPath)
	if err != nil {
		t.Fatalf("创建 p.zip 失败: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("foo.jar")
	if err != nil {
		t.Fatalf("创建条目 foo.jar 失败: %v", err)
	}
	if _, err := w.Write(inner); err != nil {
		t.Fatalf("写入 foo.jar 失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 p.zip 失败: %v", err)
	}
	f.Close()

	patchTreeInfo, err := GetPatchTreeNode(patchPath, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	fooJar := patchTreeInfo.RootNode().FindChildByName("foo.jar")
	if fooJar == nil {
		t.Fatal("p.zip 里应有 foo.jar")
	}
	if fooJar.Kind != tree.KindCompress {
		t.Fatalf("zip 里的 foo.jar 节点应是 KindCompress，实际 %v", fooJar.Kind)
	}
	return patchPath, patchTreeInfo, fooJar
}

func TestUpdatePatchReadmeNestedTwoLevels(t *testing.T) {
	patchPath, patchTreeInfo, fooJar := buildNestedPatch(t)

	// 必须先展开内层 jar —— 重写时只遍历【现有子级】，未展开的 jar
	// 子级为空、原有内容会被丢掉。真实使用中用户要先在树里展开这个节点
	// （或把它标成自定义根）才会走到这一步。
	// 未展开时的后果由 TestUpdatePatchReadmeNestedUnexpandedDropsContent 固化。
	if !BuildChildrenWithCompress(fooJar, true) {
		t.Fatal("展开 foo.jar 失败")
	}
	fooJar.Type = constant.TreeNodeTypeCustomRoot
	patchTreeInfo.AddCustomRootInfo(fooJar, CreatePatchRootInfo(fooJar))

	newText := "嵌套 jar 里的说明\n!com/example/Bar.class\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, fooJar, newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	// 最外层补丁包仍应是一个可读的 zip，且 foo.jar 条目还在
	outer := readZipContents(t, patchPath)
	if _, ok := outer["foo.jar"]; !ok {
		t.Fatalf("p.zip 里应仍有 foo.jar 条目，实际: %v", outer)
	}

	// 内层 jar 应被重写，README 写在里面，原 class 保留
	innerGot := readZipContentsFromBytes(t, readZipEntryBytes(t, patchPath, "foo.jar"))
	if innerGot[constant.ReadmeFile] != newText {
		t.Fatalf("内层 jar 里应有新 README，实际: %v", innerGot)
	}
	if innerGot["com/example/Bar.class"] != "BAR-PATCHED" {
		t.Fatalf("内层 jar 原有 class 应保留，实际: %v", innerGot)
	}

	// 保真点：内层节点名被改成 .zip，条目名仍用原 Path
	if fooJar.Name != "foo.zip" {
		t.Fatalf("内层 jar 节点名应被改成 foo.zip，实际 %q", fooJar.Name)
	}
	if fooJar.Path != "foo.jar" {
		t.Fatalf("内层节点的 Path 不应被改名影响，实际 %q", fooJar.Path)
	}
}

// TestUpdatePatchReadmeNestedUnexpandedDropsContent 固化一个上游陷阱。
//
// writePatchPackWithZip 只遍历节点的【现有子级】—— 它不会先去展开压缩包。
// 所以对一个"还没展开过"的内层 jar 写 README，重写出来的 jar 里就只剩
// README 一个条目，原有的 class 全没了。
//
// 这不是移植引入的问题：Java 的 writePatchPackWithZip 是同一套写法。
// 该结论来自对 Java 源码的静态分析，未跑原版 Java 复现。
func TestUpdatePatchReadmeNestedUnexpandedDropsContent(t *testing.T) {
	patchPath, patchTreeInfo, fooJar := buildNestedPatch(t)

	// 刻意【不】展开 foo.jar
	fooJar.Type = constant.TreeNodeTypeCustomRoot
	patchTreeInfo.AddCustomRootInfo(fooJar, CreatePatchRootInfo(fooJar))

	newText := "未展开时的说明\n"
	ctx := newWriteCtx(t, patchTreeInfo)
	if err := UpdatePatchReadme(ctx, fooJar, newText); err != nil {
		t.Fatalf("写回 README 失败: %v", err)
	}

	innerGot := readZipContentsFromBytes(t, readZipEntryBytes(t, patchPath, "foo.jar"))
	if innerGot[constant.ReadmeFile] != newText {
		t.Fatalf("内层 jar 里应有新 README，实际: %v", innerGot)
	}
	if _, exists := innerGot["com/example/Bar.class"]; exists {
		t.Fatal("上游行为变了：未展开的内层 jar 现在能保住原有内容了 —— " +
			"若确实改过 writePatchPackWithZip，请一并更新这条用例的注释")
	}
}

// ---------------------------------------------------------------------------
// 用例六：钉住上游回滚逻辑里的 bug
// ---------------------------------------------------------------------------

// TestRollbackPackNodesReplicatesUpstreamBug 验证回滚逻辑【照搬】了上游的 bug。
//
// Java 原文的循环体里漏了 i++，于是所有节点都被恢复成 oldRootNodes[0] 的状态。
// 这个用例是"特征测试"（characterization test）：它记录的是当前行为，
// 而不是"应该怎样"。哪天有人把 i++ 补上，这个用例会红 —— 那时再决定
// 是保持与 Java 一致、还是有意修正。
func TestRollbackPackNodesReplicatesUpstreamBug(t *testing.T) {
	inner := &tree.Node{Name: "inner.jar", Path: "inner.jar"}
	outer := &tree.Node{Name: "outer.zip", Path: "outer.zip"}
	inner.SetFileSource("/tmp/inner-original.jar")
	outer.SetFileSource("/tmp/outer-original.zip")
	inner.SetMd5("md5-inner")
	outer.SetMd5("md5-outer")
	inner.SetSize(11)
	outer.SetSize(22)

	oldRootNodes := []*tree.Node{inner.Clone(), outer.Clone()}
	rootNodes := []*tree.Node{inner, outer}

	rollbackPackNodes(rootNodes, oldRootNodes)

	// 两个节点都被恢复成 oldRootNodes[0]（内层）的状态
	if outer.Name != "inner.jar" {
		t.Fatalf("上游 bug：外层节点名也应被恢复成内层快照的 %q，实际 %q", "inner.jar", outer.Name)
	}
	if outer.FilePath() != "/tmp/inner-original.jar" {
		t.Fatalf("上游 bug：外层文件引用也应指向内层快照，实际 %q", outer.FilePath())
	}
	if outer.Md5 != "md5-inner" || outer.Size != 11 {
		t.Fatalf("上游 bug：外层的 md5/大小也应来自内层快照，实际 md5=%q size=%d", outer.Md5, outer.Size)
	}
	// 内层自己恢复正确（它本来就对应 oldRootNodes[0]）
	if inner.Name != "inner.jar" || inner.FilePath() != "/tmp/inner-original.jar" {
		t.Fatalf("内层节点应恢复正确，实际 name=%q file=%q", inner.Name, inner.FilePath())
	}
}
