package service

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"cn.cpoet.patch.assistant/internal/config"
	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 本文件是阶段二（补丁匹配与保存）的端到端测试。
//
// 测试数据现场构造，覆盖匹配流程的四条关键路径：
//
//  1. README 指令匹配（MOD / ADD / DEL 三种指令 + 内部类连带绑定）；
//  2. 深入 fat-jar 内部（BOOT-INF/lib/xxx.jar 里的 class）；
//  3. 按文件名匹配（WithName）与路径匹配（WithPath）；
//  4. 保存：重打包出的 jar 条目与内容、以及补丁签名。
//
// 断言分两层：先断言"匹配结果"（每个节点被标成什么、绑到了谁），
// 再断言"保存结果"（输出 jar 里有哪些条目、内容对不对、签名写了没）。

// ---------------------------------------------------------------------------
// 测试夹具
// ---------------------------------------------------------------------------

// zipEntry 一个待写入 zip 的条目。
type zipEntry struct {
	name  string
	body  string
	isDir bool
}

// writeZip 把条目列表写成一个 zip 文件，返回路径。
func writeZip(t *testing.T, dir, name string, entries []zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.isDir {
			hdr.Method = zip.Store
			hdr.SetMode(os.ModeDir | 0o755)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("创建 zip 条目 %s 失败: %v", e.name, err)
		}
		if !e.isDir {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatalf("写入 zip 条目 %s 失败: %v", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写出 %s 失败: %v", path, err)
	}
	return path
}

// innerJarBytes 构造一个内层 jar（模拟 BOOT-INF/lib/xxx.jar）。
func innerJarBytes(t *testing.T, classes map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(classes))
	for n := range classes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("创建内层 jar 条目 %s 失败: %v", n, err)
		}
		if _, err := w.Write([]byte(classes[n])); err != nil {
			t.Fatalf("写入内层 jar 条目 %s 失败: %v", n, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭内层 jar 失败: %v", err)
	}
	return buf.Bytes()
}

// appJarEntries 应用包（Spring Boot fat-jar 布局）的条目定义。
//
// 关键点：应用里 Foo$Inner.class 与 Foo.class 同级 —— 建树时会被
// handleInnerClass 挪成 Foo.class 的子节点，这正是要验证的行为之一。
func appJarEntries() []zipEntry {
	return []zipEntry{
		{name: "META-INF/MANIFEST.MF", body: "Manifest-Version: 1.0\r\n\r\n"},
		{name: "BOOT-INF/classes/com/example/Foo.class", body: "FOO-ORIGINAL"},
		{name: "BOOT-INF/classes/com/example/Foo$Inner.class", body: "FOOINNER-ORIGINAL"},
		{name: "BOOT-INF/classes/com/example/Gone.class", body: "GONE-ORIGINAL"},
		{name: "BOOT-INF/classes/com/example/Same.class", body: "SAME-CONTENT"},
		{name: "BOOT-INF/classes/com/example/Untouched.class", body: "UNTOUCHED"},
	}
}

// buildAppJar 构造应用包。
//
// 注意必须用一个 zip.Writer 一次写完 —— 把两段 zip 字节拼起来是【非法】的：
// 中央目录只在文件末尾出现一次，前半部分的条目会变成孤儿，读不出来。
func buildAppJar(t *testing.T, dir string) string {
	t.Helper()
	innerBar := innerJarBytes(t, map[string]string{
		"com/example/Bar.class":       "BAR-ORIGINAL",
		"com/example/BarHelper.class": "BARHELPER-ORIGINAL",
	})

	path := filepath.Join(dir, "app.jar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建应用包失败: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, e := range appJarEntries() {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatalf("创建条目 %s 失败: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("写入条目 %s 失败: %v", e.name, err)
		}
	}

	// 内层 jar 用 STORED 写入，与 Spring Boot 约定一致
	hdr := &zip.FileHeader{Name: "BOOT-INF/lib/bar-1.0.0.jar", Method: zip.Store}
	hdr.CRC32 = crc32.ChecksumIEEE(innerBar)
	hdr.UncompressedSize64 = uint64(len(innerBar))
	hdr.CompressedSize64 = uint64(len(innerBar))
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("创建内层 jar 条目失败: %v", err)
	}
	if _, err := w.Write(innerBar); err != nil {
		t.Fatalf("写入内层 jar 失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭应用包失败: %v", err)
	}
	return path
}

// readmeText 补丁说明文件内容。
//
// 路径一律用【从包根算起的完整路径】—— 这是这套工具的实际用法：
// 补丁包里的文件路径必须与应用包内的路径对得上。
const readmeText = `本次补丁说明
!BOOT-INF/classes/com/example/Foo.class
+BOOT-INF/classes/com/example/New.class
-BOOT-INF/classes/com/example/Gone.class
!BOOT-INF/lib/bar/com/example/Bar.class
`

// patchZipEntries 补丁包的条目定义（"镜像布局"：路径与应用包内一致）。
//
// 另外在补丁包【顶层】放了一个 Same.class —— 这是为了触发"按文件名匹配"：
// PatchMatchProcessor 只从补丁根的【直接子级】里收集候选，所以嵌套在
// 目录里的文件不参与按名匹配。见 match.go 里 Exec 的说明。
func patchZipEntries() []zipEntry {
	return []zipEntry{
		{name: "README.txt", body: readmeText},
		{name: "Same.class", body: "SAME-CONTENT"},
		{name: "BOOT-INF/classes/com/example/Foo.class", body: "FOO-PATCHED"},
		{name: "BOOT-INF/classes/com/example/Foo$Inner.class", body: "FOOINNER-PATCHED"},
		{name: "BOOT-INF/classes/com/example/New.class", body: "NEW-PATCHED"},
		{name: "BOOT-INF/lib/bar/com/example/Bar.class", body: "BAR-PATCHED"},
	}
}

// findNode 按路径在树里查节点（测试辅助）。
func findNode(t *testing.T, root *tree.Node, path string) *tree.Node {
	t.Helper()
	n := tree.FindNodeByPath(root, path)
	if n == nil {
		t.Fatalf("在树里找不到节点: %s", path)
	}
	return n
}

// defaultOptions 按默认配置构造匹配选项。
func defaultOptions() PatchOptions {
	p := config.NewPatchConf()
	return PatchOptions{
		WithPath:       p.PathMatch,
		WithName:       p.FileNameMatch,
		PatchFileRegex: p.PatchFileRegex,
		PatchFileDiff:  p.PatchFileDiff,
	}
}

// readZipContents 把 zip 读成 路径 → 内容 的映射（目录记为 "<dir>"）。
func readZipContents(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开 zip %s 失败: %v", path, err)
	}
	defer zr.Close()

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

// ---------------------------------------------------------------------------
// 用例一：README 指令匹配 + 深入 fat-jar + 保存
// ---------------------------------------------------------------------------

func TestPatchMatchEndToEnd(t *testing.T) {
	dir := t.TempDir()
	appJar := buildAppJar(t, dir)
	patchZip := writeZip(t, dir, "patch.zip", patchZipEntries())

	// ---- 1. 建应用树 ----
	appTreeInfo, err := GetAppTreeInfo(appJar)
	if err != nil {
		t.Fatalf("建应用树失败: %v", err)
	}
	appRoot := appTreeInfo.RootNode()
	if appRoot.Dir() {
		t.Error("应用包根节点应当是文件节点（对齐 Java 的 FileNode）")
	}
	if appTreeInfo.AppPackSign().Md5 == "" || appTreeInfo.AppPackSign().Sha1 == "" {
		t.Error("应用包签名应当算出 md5 与 sha1")
	}
	barNode := findNode(t, appRoot, "BOOT-INF/lib/bar-1.0.0.jar")
	if barNode.Children != nil {
		t.Error("建树阶段不应展开内层 jar（应保持惰性，等匹配需要时再展开）")
	}
	// handleInnerClass：Foo$Inner.class 应当被挪到 Foo.class 之下
	foo := findNode(t, appRoot, "BOOT-INF/classes/com/example/Foo.class")
	if foo.FindChildByName("Foo$Inner.class") == nil {
		t.Fatal("Foo$Inner.class 应当被归位为 Foo.class 的子节点")
	}
	if tree.FindNodeByPath(appRoot, "BOOT-INF/classes/com/example/Foo$Inner.class") != nil {
		t.Error("Foo$Inner.class 不应再作为 example 目录的直接子节点存在")
	}

	// ---- 2. 建补丁树 ----
	patchTreeInfo, err := GetPatchTreeNode(patchZip, nil)
	if err != nil {
		t.Fatalf("建补丁树失败: %v", err)
	}
	patchRoot := patchTreeInfo.RootNode()
	if patchRoot.Dir() {
		t.Error("补丁包（zip）根节点应当是文件节点")
	}
	if patchTreeInfo.RootInfo() == nil || patchTreeInfo.RootInfo().PatchSign == nil {
		t.Fatal("补丁根信息未建立")
	}
	if patchTreeInfo.RootInfo().PatchSign.Md5 == "" || patchTreeInfo.RootInfo().PatchSign.Sha1 == "" {
		t.Error("补丁根签名应当有 md5 与 sha1")
	}
	// 保真点：Java 版中间那个 PatchSign 对象被丢弃了，所以 path 永远是空
	if got := patchTreeInfo.RootInfo().PatchSign.Path; got != "" {
		t.Errorf("保真点被破坏：补丁根签名的 path 应当为空（Java 上游行为），实际 %q", got)
	}

	// ---- 3. 刷新 README ----
	RefreshReadmeNode(patchTreeInfo, patchRoot, "README.txt", nil)
	if got := patchTreeInfo.RootInfo().PatchSign.Readme; !strings.Contains(got, "Foo.class") {
		t.Fatalf("README 内容未读入，实际: %q", got)
	}
	readmeNode := patchTreeInfo.RootInfo().ReadmeNode
	if readmeNode == nil || readmeNode.Type != constant.TreeNodeTypeReadme {
		t.Fatal("README 节点未被标记为 README 类型")
	}
	if !strings.Contains(patchTreeInfo.ReadmeText(), "Foo.class") {
		t.Error("PatchTreeInfo 的汇总 readme 文本未更新")
	}

	// ---- 4. 执行匹配 ----
	total := tree.NewTotalInfo()
	opts := defaultOptions()
	RefreshMappedNode(total, appTreeInfo, patchTreeInfo, patchRoot, opts)

	// ---- 5. 断言匹配结果 ----
	if foo.Type != constant.TreeNodeTypeMod {
		t.Errorf("Foo.class 应为 MOD，实际 %s", foo.Type)
	}
	if foo.MappedNode == nil || foo.MappedNode.Path != "BOOT-INF/classes/com/example/Foo.class" {
		t.Errorf("Foo.class 应绑定到补丁侧同路径节点，实际 %v", foo.MappedNode)
	}

	// 内部类应当被 mappedInnerClassNode 连带绑定
	fooInner := foo.FindChildByName("Foo$Inner.class")
	if fooInner.Type != constant.TreeNodeTypeMod {
		t.Errorf("Foo$Inner.class 应为 MOD，实际 %s", fooInner.Type)
	}
	if fooInner.MappedNode == nil {
		t.Error("Foo$Inner.class 应当被绑定到补丁侧内部类")
	}

	// ADD 指令：应用里没有 New.class，应当被"凭空造"出来
	newNode := findNode(t, appRoot, "BOOT-INF/classes/com/example/New.class")
	if newNode.Type != constant.TreeNodeTypeAdd {
		t.Errorf("New.class 应为 ADD，实际 %s", newNode.Type)
	}
	if newNode.Kind != tree.KindMapped {
		t.Errorf("凭空造出来的节点应当是 MappedNode，实际 %s", newNode.Kind)
	}
	if newNode.MappedNode == nil {
		t.Error("New.class 应当绑定到补丁侧节点")
	}

	// DEL 指令：标记删除
	gone := findNode(t, appRoot, "BOOT-INF/classes/com/example/Gone.class")
	if gone.Type != constant.TreeNodeTypeDel {
		t.Errorf("Gone.class 应为 DEL，实际 %s", gone.Type)
	}

	// 深入内层 jar
	if barNode.Children == nil {
		t.Fatal("内层 jar 应当在匹配过程中被展开")
	}
	barClass := findNode(t, barNode, "com/example/Bar.class")
	if barClass.MappedNode == nil {
		t.Error("内层 jar 里的 Bar.class 应当被绑定到补丁侧节点")
	}

	// 未被指令提到的文件不应被改动
	untouched := findNode(t, appRoot, "BOOT-INF/classes/com/example/Untouched.class")
	if untouched.Type != constant.TreeNodeTypeNone || untouched.MappedNode != nil {
		t.Errorf("Untouched.class 不应被匹配，实际 type=%s mapped=%v", untouched.Type, untouched.MappedNode)
	}

	// 统计
	if total.AddTotal() < 1 || total.ModTotal() < 2 || total.DelTotal() < 1 {
		t.Errorf("统计不符预期: add=%d mod=%d del=%d",
			total.AddTotal(), total.ModTotal(), total.DelTotal())
	}

	// 按文件名匹配：补丁顶层的 Same.class 命中应用里的同名文件，
	// 两边内容一致 → 应当产生"是同一个文件"提示
	same := findNode(t, appRoot, "BOOT-INF/classes/com/example/Same.class")
	if same.MappedNode == nil {
		t.Error("Same.class 应当被按文件名匹配命中")
	}
	if !strings.Contains(appTreeInfo.PatchDiffInfo(), "Same.class") {
		t.Errorf("应当记录 Same.class 的内容相同提示，实际: %q", appTreeInfo.PatchDiffInfo())
	}

	// ---- 6. 保存打补丁后的应用包 ----
	outJar := filepath.Join(dir, "out.jar")
	writeCtx := &WriteContext{
		AppTreeInfo:    appTreeInfo,
		PatchTreeInfo:  patchTreeInfo,
		Total:          total,
		WritePatchSign: true,
		Username:       "tester",
		TempDir:        dir,
	}
	if err := SaveAppPack(writeCtx, outJar); err != nil {
		t.Fatalf("保存应用包失败: %v", err)
	}
	contents := readZipContents(t, outJar)

	// 补丁内容应当覆盖应用内容
	if got := contents["BOOT-INF/classes/com/example/Foo.class"]; got != "FOO-PATCHED" {
		t.Errorf("Foo.class 应被替换为补丁内容，实际 %q", got)
	}
	if got := contents["BOOT-INF/classes/com/example/Foo$Inner.class"]; got != "FOOINNER-PATCHED" {
		t.Errorf("Foo$Inner.class 应被替换为补丁内容，实际 %q", got)
	}
	// 新增文件应当出现在输出包里
	if got, ok := contents["BOOT-INF/classes/com/example/New.class"]; !ok || got != "NEW-PATCHED" {
		t.Errorf("New.class 应当被写入输出包，实际 (存在=%v, 内容=%q)", ok, got)
	}
	// 删除的文件不应出现
	if _, ok := contents["BOOT-INF/classes/com/example/Gone.class"]; ok {
		t.Error("Gone.class 已被标记删除，不应出现在输出包里")
	}
	// 未涉及的条目应当原样保留
	if got := contents["BOOT-INF/classes/com/example/Untouched.class"]; got != "UNTOUCHED" {
		t.Errorf("Untouched.class 应原样保留，实际 %q", got)
	}
	if got := contents["META-INF/MANIFEST.MF"]; !strings.Contains(got, "Manifest-Version") {
		t.Errorf("MANIFEST.MF 应原样保留，实际 %q", got)
	}

	// 内层 jar 应当被重打包（含补丁后的 Bar.class，且未涉及的类保持原样）
	innerJarName := "BOOT-INF/lib/bar-1.0.0.jar"
	innerData, ok := contents[innerJarName]
	if !ok {
		t.Fatalf("输出包里应当有 %s", innerJarName)
	}
	innerReader, err := zip.NewReader(bytes.NewReader([]byte(innerData)), int64(len(innerData)))
	if err != nil {
		t.Fatalf("重打包后的内层 jar 无法解析: %v", err)
	}
	innerContents := map[string]string{}
	for _, f := range innerReader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开内层条目 %s 失败: %v", f.Name, err)
		}
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		innerContents[f.Name] = b.String()
	}
	if got := innerContents["com/example/Bar.class"]; got != "BAR-PATCHED" {
		t.Errorf("内层 jar 里的 Bar.class 应被打补丁，实际 %q", got)
	}
	if got := innerContents["com/example/BarHelper.class"]; got != "BARHELPER-ORIGINAL" {
		t.Errorf("内层 jar 里未涉及的类应原样保留，实际 %q", got)
	}

	// 补丁签名应当被写入
	signName := ""
	for name := range contents {
		if strings.HasPrefix(name, constant.PatchUpPath) && strings.HasSuffix(name, constant.DotSign) {
			signName = name
			break
		}
	}
	if signName == "" {
		t.Fatal("应当写入 META-INF/patch-up/*.sign")
	}
	sign := contents[signName]
	for _, want := range []string{
		"addTotal", "modTotal", "delTotal", "manualDelTotal",
		"operUser", "tester", "originAppMd5", "originAppSha1",
		"delInfos", "addInfos", "modInfos", "readme",
	} {
		if !strings.Contains(sign, want) {
			t.Errorf("签名里应当包含 %q，实际:\n%s", want, sign)
		}
	}
	if !strings.Contains(sign, appTreeInfo.AppPackSign().Md5) {
		t.Error("签名里应当带源应用包的 md5")
	}
	if !strings.Contains(sign, `"name" : `) {
		t.Errorf("签名应当按 Jackson 风格缩进（冒号前有一个空格），实际:\n%s", sign)
	}
	if !strings.Contains(sign, "Gone.class") {
		t.Errorf("签名里的 delInfos 应当记录 Gone.class，实际:\n%s", sign)
	}
}

// ---------------------------------------------------------------------------
// 用例二：路径匹配与文件名匹配两条开关各自生效
// ---------------------------------------------------------------------------

// flatPatchZipEntries 一个"扁平"补丁包：文件都放在顶层。
//
// 这是路径/文件名匹配真正能起作用的形态 —— 因为 PatchMatchProcessor 只从
// 补丁根的【直接子级】收集候选（见 match.go）。用这个夹具可以分别验证
// 两个开关的效果。
func flatPatchZipEntries() []zipEntry {
	return []zipEntry{
		{name: "Foo.class", body: "FOO-FLAT-PATCHED"},
		{name: "Gone.class", body: "GONE-FLAT"},
	}
}

func TestPatchMatchFlatByNameAndPath(t *testing.T) {
	dir := t.TempDir()
	appJar := buildAppJar(t, dir)
	patchZip := writeZip(t, dir, "flat-patch.zip", flatPatchZipEntries())

	build := func(t *testing.T) (*tree.AppTreeInfo, *tree.PatchTreeInfo, *tree.TotalInfo) {
		t.Helper()
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

	t.Run("仅文件名匹配", func(t *testing.T) {
		appTreeInfo, patchTreeInfo, total := build(t)
		opts := defaultOptions()
		opts.WithPath = false
		opts.WithName = true
		RefreshMappedNode(total, appTreeInfo, patchTreeInfo, patchTreeInfo.RootNode(), opts)

		foo := findNode(t, appTreeInfo.RootNode(), "BOOT-INF/classes/com/example/Foo.class")
		if foo.MappedNode == nil {
			t.Fatal("按文件名匹配应当命中 Foo.class")
		}
		if got := foo.MappedNode.Path; got != "Foo.class" {
			t.Errorf("应当绑定到补丁包顶层的 Foo.class，实际 %q", got)
		}
		if total.ModTotal() < 1 {
			t.Errorf("应当至少统计到 1 个 MOD，实际 %d", total.ModTotal())
		}
		// 补丁里还有 Gone.class，按名匹配会把它也配上
		// —— 这正是"按文件名匹配"的语义：不问路径，只认名字
		gone := findNode(t, appTreeInfo.RootNode(), "BOOT-INF/classes/com/example/Gone.class")
		if gone.MappedNode == nil {
			t.Error("按文件名匹配应当同样命中 Gone.class")
		}
	})

	t.Run("两个开关都关掉则不做任何匹配", func(t *testing.T) {
		appTreeInfo, patchTreeInfo, total := build(t)
		opts := defaultOptions()
		opts.WithPath = false
		opts.WithName = false
		RefreshMappedNode(total, appTreeInfo, patchTreeInfo, patchTreeInfo.RootNode(), opts)

		foo := findNode(t, appTreeInfo.RootNode(), "BOOT-INF/classes/com/example/Foo.class")
		if foo.MappedNode != nil {
			t.Error("开关全关时不应发生任何匹配")
		}
		if total.IsChangeNode() {
			t.Errorf("开关全关时不应有变更统计，实际 add=%d mod=%d del=%d",
				total.AddTotal(), total.ModTotal(), total.DelTotal())
		}
	})

	t.Run("路径匹配在扁平补丁上命中", func(t *testing.T) {
		// 补丁顶层是 Foo.class / Gone.class，应用顶层是 META-INF / BOOT-INF，
		// 名字对不上 → 路径匹配这一轮应当什么都匹配不到。
		// 这固化了"路径匹配只从补丁根的直接子级取候选"这个上游行为。
		appTreeInfo, patchTreeInfo, total := build(t)
		opts := defaultOptions()
		opts.WithPath = true
		opts.WithName = false
		RefreshMappedNode(total, appTreeInfo, patchTreeInfo, patchTreeInfo.RootNode(), opts)

		foo := findNode(t, appTreeInfo.RootNode(), "BOOT-INF/classes/com/example/Foo.class")
		if foo.MappedNode != nil {
			t.Error("补丁顶层文件与应用顶层目录不同名，路径匹配不应命中")
		}
	})

	t.Run("正则放空后目录才能参与路径匹配", func(t *testing.T) {
		// 这条固化"默认正则会挡掉目录"这个上游行为：
		// 默认正则 (.*\.class)|(.*\.html)|(.*\.js)|(.*\.css) 不认 "BOOT-INF"
		// 这类目录名，于是路径匹配根本无法沿目录下探；
		// 把正则清空后目录才进入候选，路径匹配才能一路走到目标 class。
		dir2 := t.TempDir()
		appJar2 := buildAppJar(t, dir2)
		patchZip2 := writeZip(t, dir2, "mirror-patch.zip", []zipEntry{
			{name: "BOOT-INF/classes/com/example/Foo.class", body: "FOO-MIRROR"},
		})

		run := func(regex string) *tree.Node {
			t.Helper()
			appTreeInfo, err := GetAppTreeInfo(appJar2)
			if err != nil {
				t.Fatalf("建应用树失败: %v", err)
			}
			patchTreeInfo, err := GetPatchTreeNode(patchZip2, nil)
			if err != nil {
				t.Fatalf("建补丁树失败: %v", err)
			}
			total := tree.NewTotalInfo()
			opts := defaultOptions()
			opts.WithPath = true
			opts.WithName = false
			opts.PatchFileRegex = regex
			RefreshMappedNode(total, appTreeInfo, patchTreeInfo, patchTreeInfo.RootNode(), opts)
			return findNode(t, appTreeInfo.RootNode(), "BOOT-INF/classes/com/example/Foo.class")
		}

		if foo := run(defaultOptions().PatchFileRegex); foo.MappedNode != nil {
			t.Fatal("默认正则下目录被过滤，路径匹配应当命中不了（上游行为）")
		}
		foo := run("")
		if foo.MappedNode == nil {
			t.Fatal("正则放空后，路径匹配应当能沿目录下探命中 Foo.class")
		}
		if got := foo.MappedNode.Path; got != "BOOT-INF/classes/com/example/Foo.class" {
			t.Errorf("应当绑定到补丁侧同路径节点，实际 %q", got)
		}
	})
}
