package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/decompile"
	"cn.cpoet.patch.assistant/internal/i18n"
	"cn.cpoet.patch.assistant/internal/manualdel"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// newAppZh 造一个语言固定为中文的 App。
//
// 语言默认跟随系统（Windows 走 GetUserDefaultUILanguage），测试不该依赖
// 跑测试那台机器的区域设置，所以断言文案的用例统一把语言钉死。
func newAppZh() *App {
	app := NewApp()
	app.lang = constant.I18NZhCN.Code
	// **把持久化层摘掉** —— NewApp() 会指到真实 %APPDATA%，
	// 单测跑 OpenAppPack 时 reapplyManualDel 会把用户机器上
	// 已有的删除记录读进来，污染本次运行（真实踩过）。要测持久化
	// 的用例改用 newTestApp(t)。
	app.manualDelStore = nil
	return app
}

// newTestApp 造一个"手动删除记录"指向临时目录的 App。
//
// 只有显式测试持久化的用例用这个。其它用例（菜单、编辑、打补丁…）
// 一律用 newAppZh。
func newTestApp(t *testing.T) *App {
	t.Helper()
	app := newAppZh()
	store, err := manualdel.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("造 Store 失败: %v", err)
	}
	app.manualDelStore = store
	return app
}

// trZh 取中文文案，供断言用。
func trZh(key string) string { return i18n.Tr(constant.I18NZhCN.Code, key) }

// goldenDir 是跨语言黄金基准的目录，里面有确定性的 app.jar / patch.zip 夹具。
//
// 复用它的原因：GUI 只是 service 包的一层薄绑定，用**同一份**夹具跑，
// 才能和 CLI、以及原版 Java 的基准结果对上。
const goldenDir = "../../_extract/golden"

func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(goldenDir, "fixture", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("夹具不存在，先跑 `bash %s/run.sh` 生成: %v", goldenDir, err)
	}
	return p
}

// TestOpenAndMatch 覆盖"打开两个包 → 跑匹配"这条主链路。
func TestOpenAndMatch(t *testing.T) {
	app := NewApp()

	// 只开应用包：此时不该匹配（缺一侧），也不该有统计
	s, err := app.OpenAppPack(fixture(t, "app.jar"))
	if err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if s.AppTree == nil {
		t.Fatal("应用树为空")
	}
	if s.Matched {
		t.Error("只开了一侧就标记成已匹配")
	}
	if s.AppStats.Files != 7 || s.AppStats.Dirs != 6 {
		t.Errorf("应用包统计不对: %+v", s.AppStats)
	}
	if s.AppStats.Classes != 5 || s.AppStats.Jars != 1 {
		t.Errorf("class/jar 统计不对: %+v", s.AppStats)
	}

	// 再开补丁包：两侧齐了，应当自动跑匹配
	s, err = app.OpenPatchPack(fixture(t, "patch.zip"))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if !s.Matched {
		t.Fatal("两侧都打开了却没匹配")
	}
	if s.PatchTree == nil {
		t.Fatal("补丁树为空")
	}

	// 统计口径与 CLI / 黄金基准一致：新增 1 / 修改 4 / 删除 1
	if s.Total.Add != 1 || s.Total.Mod != 4 || s.Total.Del != 1 || s.Total.ManualDel != 0 {
		t.Errorf("匹配统计与基准不符: %+v", s.Total)
	}
}

// TestTreeViewCarriesChangeType 确认变更状态真的传到了前端视图上。
//
// 这条容易静默失效：如果 toView 忘了带 Type，界面就只是"看起来正常但不着色"，
// 不会有任何报错。所以断言到具体节点上。
func TestTreeViewCarriesChangeType(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(fixture(t, "patch.zip"))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	got := map[string]string{}
	var walk func(*NodeView)
	walk = func(n *NodeView) {
		if n == nil {
			return
		}
		got[n.Path] = n.Type
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(s.AppTree)

	want := map[string]string{
		// README 里的三条指令：+ 新增 / ! 修改 / - 删除
		"BOOT-INF/classes/com/example/New.class":  "add",
		"BOOT-INF/classes/com/example/Foo.class":  "mod",
		"BOOT-INF/classes/com/example/Gone.class": "del",
		// 补丁包里有个顶层 Same.class，路径对不上，但**文件名匹配**会命中它
		// —— 这正是 `-name` 开关存在的意义，不是误伤
		"BOOT-INF/classes/com/example/Same.class": "mod",
		// 补丁里没有对应物，两种匹配都够不到
		"BOOT-INF/classes/com/example/Untouched.class": "none",
	}
	for path, wantType := range want {
		if gotType, ok := got[path]; !ok {
			t.Errorf("视图里找不到节点 %s", path)
		} else if gotType != wantType {
			t.Errorf("%s 的状态 = %q，期望 %q", path, gotType, wantType)
		}
	}
}

// TestRunPatchProducesReadableJar 覆盖"执行打补丁"并回读产物。
//
// 关键断言：产物必须能被**重新打开**并建出树 —— 这能抓住"写出的 zip 结构
// 损坏"这类问题（比如本地头错位），而那类问题只看文件大小是看不出来的。
func TestRunPatchProducesReadableJar(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.jar")
	res, err := app.RunPatch(out)
	if err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if res.Output != out {
		t.Errorf("输出路径 = %q，期望 %q", res.Output, out)
	}
	if res.Total.Add != 1 || res.Total.Mod != 4 || res.Total.Del != 1 {
		t.Errorf("结果统计不对: %+v", res.Total)
	}

	// 回读：产物必须能建树，且补丁该生效的都生效了
	reopened := NewApp()
	s, err := reopened.OpenAppPack(out)
	if err != nil {
		t.Fatalf("回读产物失败（写出的包可能已损坏）: %v", err)
	}

	names := map[string]bool{}
	var walk func(*NodeView)
	walk = func(n *NodeView) {
		if n == nil {
			return
		}
		names[n.Path] = true
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(s.AppTree)

	if names["BOOT-INF/classes/com/example/Gone.class"] {
		t.Error("被删除的 Gone.class 仍在产物里")
	}
	if !names["BOOT-INF/classes/com/example/New.class"] {
		t.Error("新增的 New.class 不在产物里")
	}
	// 签名默认开启，应当写出
	var hasSign bool
	for p := range names {
		if strings.HasPrefix(p, "META-INF/patch-up/") && strings.HasSuffix(p, ".sign") {
			hasSign = true
		}
	}
	if !hasSign {
		t.Error("没有写出补丁签名（WritePatchSign 默认为 true）")
	}
}

// TestDefaultOutputPathHasNoSuffix 钉住"默认输出路径就是应用包自身"。
//
// 与 Java 版对齐：原版 `HomeView.handleSaveAppPack` 用
// `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())` ——
// 初始名就是应用包自己的文件名、初始目录就是应用包所在目录，
// **不带任何后缀**。移植时曾自行拼成 `<主名>-patched.jar`，这条守着它。
//
// 附带确认"没打开应用包时返回空串"：调用方拿空串会退回别的兜底，
// 不能返回一个像 "/-patched.jar" 这样的假路径。
func TestDefaultOutputPathHasNoSuffix(t *testing.T) {
	app := NewApp()
	if got := app.defaultOutputPath(); got != "" {
		t.Errorf("没打开应用包时应当返回空串，实际 %q", got)
	}

	pack := fixture(t, "app.jar")
	if _, err := app.OpenAppPack(pack); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	got := app.defaultOutputPath()
	if got != pack {
		t.Errorf("默认输出路径 = %q，期望就是应用包自身 %q", got, pack)
	}
	if strings.Contains(got, "-patched") {
		t.Errorf("默认输出路径仍带 -patched 后缀: %q", got)
	}
}

// TestApplySettingsTakesEffect 确认设置真的影响后续匹配。
//
// 关掉两种匹配后，**README 指令仍然生效**（那是独立的一轮映射，不受开关
// 控制），但按"路径/文件名"的自动匹配应当停下来。所以判据是"修改数变少"
// 而不是"全都归零"—— 后者说明没读懂这两轮的关系。
func TestApplySettingsTakesEffect(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	before, err := app.OpenPatchPack(fixture(t, "patch.zip"))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	modBefore := before.Total.Mod

	app.ApplySettings(Settings{
		WithPath:       false,
		WithName:       false,
		WritePatchSign: true,
		PatchFileRegex: app.GetSettings().PatchFileRegex,
		ReadmeFile:     app.GetSettings().ReadmeFile,
	})
	s, err := app.ReMatch()
	if err != nil {
		t.Fatalf("重新匹配失败: %v", err)
	}

	// README 驱动的那几条不受开关影响
	if s.Total.Add != 1 {
		t.Errorf("新增 = %d，期望 1（README 的 `+` 指令）", s.Total.Add)
	}
	if s.Total.Del != 1 {
		t.Errorf("删除 = %d，期望 1（README 的 `-` 指令）", s.Total.Del)
	}
	// 自动匹配停下来后，修改数应当比开着时少
	if s.Total.Mod >= modBefore {
		t.Errorf("关掉两种匹配后修改数没有减少: %d → %d", modBefore, s.Total.Mod)
	}
}

// TestApplySettingsNewFields 钉住 2026-10-14 补进设置面板的两个项：
// 「开启补丁文件比对」与「保留历史条数」。
//
// 单独测的理由：这两项以前**只在 config 层有字段、设置面板够不着** ——
// PatchFileDiff 文档默认 true 却改不了，HistoryLimit 永远是 30。
// 所以判据不是"GetSettings 回显对了"（那只能证明自己跟自己一致），
// 而是**下游真的看到了新值**：一个落到 PatchOptions 上，一个真的裁剪历史。
func TestApplySettingsNewFields(t *testing.T) {
	app := NewApp()

	// 默认值与 config.NewPatchConf / NewSearchConf 对齐
	if got := app.GetSettings(); !got.PatchFileDiff || got.HistoryLimit != 30 {
		t.Fatalf("默认值不对: PatchFileDiff=%v HistoryLimit=%d",
			got.PatchFileDiff, got.HistoryLimit)
	}

	s := app.ApplySettings(Settings{
		PatchFileDiff:  false,
		HistoryLimit:   2,
		PatchFileRegex: app.GetSettings().PatchFileRegex,
	})
	if s.PatchFileDiff {
		t.Error("回显里 PatchFileDiff 还是 true")
	}
	if s.HistoryLimit != 2 {
		t.Errorf("回显里 HistoryLimit = %d，期望 2", s.HistoryLimit)
	}

	// 1) PatchFileDiff 要真的落到 PatchOptions 上 ——
	//    internal/service 的 CreatePatchDiffInfo 就是看这个开关决定要不要
	//    往「补丁信息」里追"同一个文件 / 应用比补丁新"那两条风险提示。
	if opts := app.patchOptions(app.step()); opts.PatchFileDiff {
		t.Error("关掉补丁文件比对后，PatchOptions.PatchFileDiff 还是 true")
	}

	// 2) HistoryLimit 要真的裁剪历史（SearchConf.AddHistory 末尾那条 len > limit）
	for _, k := range []string{"one", "two", "three"} {
		app.AddSearchHistory(k, "")
	}
	if n := len(searchHistory(t, app)); n != 2 {
		t.Errorf("历史条数 = %d，期望 2（上限刚设成 2）", n)
	}

	// 3) 填 0 / 负数要回落到 30，而不是把历史全裁光；
	//    而且**回显出去的就是实际生效的那个数**，界面上不会显示一个假值。
	s = app.ApplySettings(Settings{
		HistoryLimit:   0,
		PatchFileRegex: app.GetSettings().PatchFileRegex,
	})
	if s.HistoryLimit != 30 {
		t.Errorf("HistoryLimit 填 0 后回显 = %d，期望回落 30", s.HistoryLimit)
	}
	for i := 0; i < 40; i++ {
		app.AddSearchHistory("k"+strconv.Itoa(i), "")
	}
	if n := len(searchHistory(t, app)); n != 30 {
		t.Errorf("回落到 30 后历史条数 = %d，期望 30", n)
	}
}

// searchHistory 取当前会话内的搜索历史（空关键词走的就是历史那一支）。
func searchHistory(t *testing.T, app *App) []*SearchItemView {
	t.Helper()
	items, err := app.SearchNodes("")
	if err != nil {
		t.Fatalf("取搜索历史失败: %v", err)
	}
	return items
}

// TestRematchIsStable 钉住"重复匹配结果不漂移"。
//
// 这条是回归测试：早期版本每次匹配都新建 TotalInfo，而
// MappedDelNodeWithType 对"已是 DEL"的节点直接返回、不再计数，于是第二次
// 匹配的删除数会变成 0。Java 的 HomeContext 全程复用同一个 TotalInfo，
// 所以正确行为是多次匹配结果一致。
func TestRematchIsStable(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	first, err := app.OpenPatchPack(fixture(t, "patch.zip"))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		again, err := app.ReMatch()
		if err != nil {
			t.Fatalf("第 %d 次重新匹配失败: %v", i+1, err)
		}
		if again.Total != first.Total {
			t.Fatalf("第 %d 次重新匹配结果漂移: %+v → %+v", i+1, first.Total, again.Total)
		}
	}
}

// TestReadmeNodeIsRecognized 确认补丁包根下的 README.txt 会被识别成 README 节点。
//
// 这条是用户报障后补的：报的是"没识别到 zip 里的 README.txt"。
// 先证明**正常情况是能识别的**，再去看异常情况差在哪 —— 见下面那条特征测试。
func TestReadmeNodeIsRecognized(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(fixture(t, "patch.zip"))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	node := findNode(s.PatchTree, "README.txt")
	if node == nil {
		t.Fatal("补丁树里找不到 README.txt")
	}
	if node.Type != "readme" {
		t.Errorf("README.txt 的状态 = %q，期望 \"readme\"", node.Type)
	}
	// README 必须是补丁根的【直接子节点】—— 这是识别的必要条件，见下条测试
	if node.Path != "README.txt" {
		t.Errorf("README.txt 的路径 = %q，期望 \"README.txt\"（直接挂在根下）", node.Path)
	}
}

// TestReadmeOutsideRootIsNotRecognized 是**特征测试**（characterization test）：
// 记录的是**上游当前的行为**，不是"应该怎样"。
//
// 原版 `PatchPackService.refreshReadmeNode` 只遍历 `rootNode.getChildren()`，
// 也就是**补丁根的直接子节点**。所以：
//
//	README.txt 在 zip 根下      -> 识别
//	README.txt 套在一层目录里   -> 不识别（内容一样也不认）
//	README.txt 条目名带 "./"    -> 不识别（会被建到名为 "." 的目录节点下）
//
// Java 侧代码逐行相同（`PatchPackService.refreshReadmeNode` +
// `BasePackService.getOrCreateParentNode` + `FileNameUtil.getDirPath`），
// 已逐处比对，**不是移植偏差**。
//
// 原版对这种包的解法是让用户手动"标记为根"/"标记为 README"（右键菜单），
// 那两个动作尚未移植。哪天移植了、或者决定让自动流程递归找，
// 这条测试会变红 —— 那时再决定是有意修正还是保持与 Java 一致。
func TestReadmeOutsideRootIsNotRecognized(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{"套一层目录", "mypatch/README.txt"},
		{"嵌套更深", "src/README.txt"},
		{"条目名带 ./ 前缀", "./README.txt"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "patch.zip")
			writeZip(t, zipPath, map[string]string{
				c.entry: "# patch\n+ BOOT-INF/classes/com/example/New.class\n",
			})

			app := NewApp()
			if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
				t.Fatalf("打开应用包失败: %v", err)
			}
			s, err := app.OpenPatchPack(zipPath)
			if err != nil {
				t.Fatalf("打开补丁包失败: %v", err)
			}

			if node := findNode(s.PatchTree, "README.txt"); node != nil {
				if node.Type == "readme" {
					t.Errorf("README 在 %q 时被识别成了 README 节点 —— "+
						"说明识别范围变了，请同步更新 MIGRATION.md 里的说明", c.entry)
				}
			}
			// 更关键的是：README 指令没生效，所以不会新增任何东西
			if s.Total.Add != 0 {
				t.Errorf("README 不该被读到，却产生了新增 %d 条", s.Total.Add)
			}
		})
	}
}

// writeZip 在测试里造一个 zip，条目内容按传入的 map 写。
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("建 zip 失败: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写条目 %s 失败: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写条目 %s 内容失败: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
}

// findNode 在视图树里按名字找节点（深度优先，找到第一个就返回）。
func findNode(root *NodeView, name string) *NodeView {
	if root == nil {
		return nil
	}
	if root.Name == name {
		return root
	}
	for _, c := range root.Children {
		if found := findNode(c, name); found != nil {
			return found
		}
	}
	return nil
}

// menuItem 在节点的右键菜单里按 ID 找一项（含子菜单，一层就够用）。
func menuItem(n *NodeView, id string) *MenuItemView {
	if n == nil {
		return nil
	}
	for _, it := range n.Menu {
		if it.ID == id {
			return it
		}
		for _, sub := range it.Children {
			if sub.ID == id {
				return sub
			}
		}
	}
	return nil
}

// hasMenu 判断节点的右键菜单里有没有某一项。
func hasMenu(n *NodeView, id string) bool { return menuItem(n, id) != nil }

// menuIDs 按顺序列出节点的菜单项 ID，用来断言**顺序**。
func menuIDs(n *NodeView) []string {
	if n == nil {
		return nil
	}
	ids := make([]string, 0, len(n.Menu))
	for _, it := range n.Menu {
		ids = append(ids, it.ID)
	}
	return ids
}

// sameStrings 比较两个字符串切片是否完全一致（含顺序）。
func sameStrings(a, b []string) bool {
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

// TestDropAcceptRulesMatchUpstream 钉住"哪一侧收什么文件"的规则。
//
// 规则逐字对齐 Java 的 onDragOver：
//
//	左（应用包）：files.size() == 1 && name.endsWith(".jar")
//	右（补丁包）：files.size() == 1 && (isDirectory() || name.endsWith(".jar") || name.endsWith(".zip"))
//
// 两个容易写错、又看不出错的地方特意点了名：
//   - **必须恰好 1 个文件**。拖两个文件进来在原版是被拒的，不是"取第一个"。
//   - **区分大小写**。Java 用的是 String.endsWith，`.JAR` 不算。
//     这是上游行为，别"顺手"改成大小写不敏感。
func TestDropAcceptRulesMatchUpstream(t *testing.T) {
	dir := t.TempDir() // 存在且是目录
	jar := fixture(t, "app.jar")
	zip := fixture(t, "patch.zip")

	cases := []struct {
		name    string
		paths   []string
		asApp   bool
		asPatch bool
	}{
		{"左：单个 .jar", []string{jar}, true, true},
		{"左：单个 .zip 不收（左只吃 .jar）", []string{zip}, false, true},
		{"两个文件一律不收", []string{jar, zip}, false, false},
		{"空列表一律不收", nil, false, false},
		{"右：目录", []string{dir}, false, true},
		{"右：.txt 不收", []string{"a.txt"}, false, false},
		{"大小写敏感：.JAR 不算", []string{"A.JAR"}, false, false},
		{"大小写敏感：.ZIP 不算", []string{"A.ZIP"}, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := acceptAsAppPack(c.paths); got != c.asApp {
				t.Errorf("acceptAsAppPack(%v) = %v，期望 %v", c.paths, got, c.asApp)
			}
			if got := acceptAsPatchPack(c.paths); got != c.asPatch {
				t.Errorf("acceptAsPatchPack(%v) = %v，期望 %v", c.paths, got, c.asPatch)
			}
		})
	}
}

// TestDropOpensCorrectSide 覆盖拖放的端到端：落点决定打开到哪一侧，
// 两侧都拖齐后自动跑匹配。
func TestDropOpensCorrectSide(t *testing.T) {
	app := NewApp()

	// 拖 .jar 到左侧 -> 作为应用包打开，右侧仍空
	s, err := app.HandleFileDrop(string(DropSideApp), []string{fixture(t, "app.jar")})
	if err != nil {
		t.Fatalf("把 .jar 拖到应用包一侧失败: %v", err)
	}
	if s.AppTree == nil {
		t.Error("应用树为空")
	}
	if s.PatchTree != nil {
		t.Error("只拖了应用包，补丁侧却也有树")
	}

	// 拖 .zip 到右侧 -> 作为补丁包打开，两侧齐了应当自动匹配
	s, err = app.HandleFileDrop(string(DropSidePatch), []string{fixture(t, "patch.zip")})
	if err != nil {
		t.Fatalf("把 .zip 拖到补丁包一侧失败: %v", err)
	}
	if s.PatchTree == nil {
		t.Error("补丁树为空")
	}
	if !s.Matched {
		t.Error("两侧都拖齐了却没跑匹配")
	}
	if s.Total.Add != 1 || s.Total.Mod != 4 || s.Total.Del != 1 {
		t.Errorf("拖放打开的统计与基准不符: %+v", s.Total)
	}
}

// TestDropDirectoryAsPatchPack 覆盖"把目录拖到补丁侧"。
// 原版右侧接受目录（补丁目录模式），这条与压缩包路径不同，单独走一遍。
func TestDropDirectoryAsPatchPack(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(goldenDir, "fixture", "readme.txt"))
	if err != nil {
		t.Skipf("夹具不存在，先跑 `bash %s/run.sh` 生成: %v", goldenDir, err)
	}
	// 目录模式按 ReadmeFile 的设置找说明文件，默认是 README.txt
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), src, 0o644); err != nil {
		t.Fatalf("准备补丁目录失败: %v", err)
	}

	app := NewApp()
	s, err := app.HandleFileDrop(string(DropSidePatch), []string{dir})
	if err != nil {
		t.Fatalf("把目录拖到补丁包一侧失败: %v", err)
	}
	if s.PatchTree == nil {
		t.Error("目录没有建成补丁树")
	}
}

// TestRejectedDropLeavesStateUntouched 确认被拒的拖入不会动到已打开的东西。
//
// 这条对应原版"静默忽略"的语义：非法拖入的结果必须是**什么都没发生**，
// 而不是把已有的包换掉或清空。
func TestRejectedDropLeavesStateUntouched(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	before := app.GetState()

	rejects := []struct {
		name  string
		side  string
		paths []string
	}{
		{"把 .zip 拖到应用包一侧", string(DropSideApp), []string{fixture(t, "patch.zip")}},
		{"空路径列表", string(DropSideApp), nil},
		{"未知的落点", "nope", []string{fixture(t, "app.jar")}},
		{"一次拖两个文件", string(DropSideApp), []string{fixture(t, "app.jar"), fixture(t, "app.jar")}},
	}
	for _, c := range rejects {
		t.Run(c.name, func(t *testing.T) {
			if _, err := app.HandleFileDrop(c.side, c.paths); err == nil {
				t.Fatal("应当被拒绝，却成功了")
			}
			after := app.GetState()
			if after.AppPath != before.AppPath || after.AppTree == nil {
				t.Errorf("被拒的拖入改动了状态: appPath %q → %q", before.AppPath, after.AppPath)
			}
		})
	}
}

// findNodeByPath 按 Path 找视图节点。测试里用 Path 而不是 Name ——
// 同一个包内可能有多个同名节点（比如根下和子目录里各一个 README.txt）。
func findNodeByPath(root *NodeView, path string) *NodeView {
	if root == nil {
		return nil
	}
	if root.Path == path {
		return root
	}
	for _, c := range root.Children {
		if found := findNodeByPath(c, path); found != nil {
			return found
		}
	}
	return nil
}

// writeZipOrdered 按给定顺序写 zip 条目（writeZip 收的是 map，顺序随机）。
//
// 需要"确定的物理顺序"的测试用它：树的**存储**顺序就是 zip 条目顺序，
// 拿随机顺序没法断言"界面排了、树没动"。
func writeZipOrdered(t *testing.T, path string, names []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("建 zip 失败: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写条目 %s 失败: %v", name, err)
		}
		// 目录条目（以 '/' 结尾）不能写内容，archive/zip 会直接报错。
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatalf("写条目 %s 内容失败: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
}

// TestTreeViewChildrenSortedForDisplay 钉住"界面按名字排、树里保持 zip 顺序"。
//
// 用户 2026-10-28 报障："文件现在排序是乱的，能不能按照字母顺序排列"。
// 乱的根源是界面直接显示了 zip 条目的**物理顺序**（打包工具决定的），
// 而原版也这样 —— Java 的 SortLinkedList.add 里有序插入那段被注释掉了。
//
// 修法只动视图：toView 里把子级复制一份排序，不碰 n.Children ——
// 树的顺序决定重打包时条目的写入顺序，动了会让输出包与原版分叉。
// 所以这条测试两头都要断言：视图有序、内部树仍是物理顺序。
func TestTreeViewChildrenSortedForDisplay(t *testing.T) {
	app := newTestApp(t)
	jar := filepath.Join(t.TempDir(), "s.jar")
	writeZipOrdered(t, jar, []string{
		"lib/",
		"lib/zeta.jar", // 物理顺序：zeta → alpha → Middle → beta → dir → Aa
		"lib/alpha.jar",
		"lib/Middle.jar",
		"lib/beta.jar",
		"lib/dir/",
		"lib/dir/x.txt",
		"lib/Aa.jar",
	})
	if _, err := app.OpenAppPack(jar); err != nil {
		t.Fatal(err)
	}

	// ① 视图：目录在前，文件按名字忽略大小写升序。
	libView := findNodeByPath(app.GetState().AppTree, "lib/")
	if libView == nil {
		t.Fatal("视图里找不到 lib/")
	}
	var got []string
	for _, c := range libView.Children {
		got = append(got, c.Name)
	}
	want := []string{"dir", "Aa.jar", "alpha.jar", "beta.jar", "Middle.jar", "zeta.jar"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("视图顺序 = %v\n期望 = %v", got, want)
	}

	// ② 内部树：仍是 zip 的物理顺序（证明排序没落到树上）。
	var lib *tree.Node
	for _, c := range app.appTreeInfo.RootNode().Children {
		if c.Name == "lib" {
			lib = c
			break
		}
	}
	if lib == nil {
		t.Fatal("内部树里找不到 lib")
	}
	var raw []string
	for _, c := range lib.Children {
		raw = append(raw, c.Name)
	}
	wantRaw := []string{"zeta.jar", "alpha.jar", "Middle.jar", "beta.jar", "dir", "Aa.jar"}
	if strings.Join(raw, ",") != strings.Join(wantRaw, ",") {
		t.Errorf("内部树顺序 = %v\n期望仍是 zip 物理顺序 %v", raw, wantRaw)
	}
}

// TestTreeViewChildrenSortedForDisplay 的边界：同名不同大小写时顺序要稳定。
func TestLessForDisplayTieBreaks(t *testing.T) {
	mk := func(name string, dir bool) *tree.Node {
		n := tree.NewFile(name, name, 0, time.Now(), nil, false)
		n.SetDir(dir)
		return n
	}
	if !lessForDisplay(mk("a", true), mk("a", false)) {
		t.Error("目录应排在文件前（即使同名）")
	}
	if !lessForDisplay(mk("Foo.jar", false), mk("foo.jar", false)) {
		t.Error("忽略大小写后相同 → 用原始名兜底，Foo 应在 foo 之前")
	}
	if lessForDisplay(mk("foo.jar", false), mk("Foo.jar", false)) {
		t.Error("兜底比较不可逆：foo.jar 不该排在 Foo.jar 之前")
	}
}

// zipHasEntry 判断 zip 包里是否存在指定条目（路径以 '/' 分隔）。
func zipHasEntry(t *testing.T, zipPath, entry string) bool {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 zip 失败 %s: %v", zipPath, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == entry {
			return true
		}
	}
	return false
}

// nestedReadmePatchZip 造一个"README 套在一层目录里"的补丁包 ——
// 这正是用户报障的场景，也是资源管理器「发送到→压缩文件夹」压目录的常见结果。
func nestedReadmePatchZip(t *testing.T) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"mypatch/README.txt":                             "# patch\n+ BOOT-INF/classes/com/example/New.class\n",
		"mypatch/BOOT-INF/classes/com/example/New.class": "x",
	})
	return zipPath
}

// TestMarkRootMakesNestedReadmeRecognized 是用户报障场景的正解。
//
// 自动流程只扫补丁根的直接子节点，所以 mypatch/README.txt 认不出来；
// 把 mypatch 标成自定义根之后，README 就成了它的直接子节点，随即被识别。
func TestMarkRootMakesNestedReadmeRecognized(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(nestedReadmePatchZip(t))
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	// 标记之前：认不出 README，所以 README 里的 `+` 指令没生效
	if s.Total.Add != 0 {
		t.Fatalf("标记前不该识别到 README，却新增了 %d 条", s.Total.Add)
	}
	dir := findNodeByPath(s.PatchTree, "mypatch")
	if dir == nil {
		t.Fatal("补丁树里没有 mypatch 目录节点")
	}
	if !hasMenu(dir, menuMarkRoot) {
		t.Error("mypatch 应当可以标记为根（是目录、有子节点、不在自定义根下）")
	}

	// 标记为根
	s, err = app.MarkRoot("mypatch")
	if err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}
	if n := findNodeByPath(s.PatchTree, "mypatch"); n == nil || n.Type != "customRoot" {
		t.Errorf("mypatch 没有变成自定义根: %+v", n)
	}
	if s.Total.Add != 1 {
		t.Errorf("标记为根后新增 = %d，期望 1（README 的 `+` 指令生效）", s.Total.Add)
	}
	if n := findNodeByPath(s.PatchTree, "mypatch/README.txt"); n == nil || n.Type != "readme" {
		t.Errorf("mypatch/README.txt 没有被识别成 README: %+v", n)
	}
}

// TestMarkRootToggle 确认自定义根可以取消，且取消后回到"认不出 README"的状态。
func TestMarkRootToggle(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}
	// 再点一次 = 取消
	s, err := app.MarkRoot("mypatch")
	if err != nil {
		t.Fatalf("取消标记为根失败: %v", err)
	}

	if n := findNodeByPath(s.PatchTree, "mypatch"); n == nil || n.Type == "customRoot" {
		t.Errorf("取消后 mypatch 仍是自定义根: %+v", n)
	}
	// 取消后主根被重新刷过，README 又回到"认不出"的状态
	if s.Total.Add != 0 {
		t.Errorf("取消标记后新增 = %d，期望回到 0", s.Total.Add)
	}
}

// TestMarkReadmeOverridesAutoDetection 覆盖手动指定说明文件。
//
// 用一个自动流程【认不出】的文件名（README.md —— 只认配置名与 .txt 后缀），
// 标记后应当被当作 README 读出内容。
//
// 注意 zip 里必须同时有 README 指令指向的那个补丁文件：指令会先去补丁树里
// 找对应节点，**找不到就把这条指令静默丢掉**（PathInfosOfText 的既定行为）。
// 只放一个 README 是测不出东西的 —— 这条踩过。
func TestMarkReadmeOverridesAutoDetection(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"README.md":                              "# patch\n+ BOOT-INF/classes/com/example/New.class\n",
		"BOOT-INF/classes/com/example/New.class": "x",
	})

	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(zipPath)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if s.Total.Add != 0 {
		t.Fatalf("README.md 不该被自动识别，却新增了 %d 条", s.Total.Add)
	}
	if n := findNodeByPath(s.PatchTree, "README.md"); n == nil || !hasMenu(n, menuMarkReadme) {
		t.Fatalf("README.md 应当可以标记为说明文件: %+v", n)
	}

	s, err = app.MarkReadme("README.md")
	if err != nil {
		t.Fatalf("标记说明文件失败: %v", err)
	}
	if n := findNodeByPath(s.PatchTree, "README.md"); n == nil || n.Type != "readme" {
		t.Errorf("README.md 没有被标记成 README: %+v", n)
	}
	if s.Total.Add != 1 {
		t.Errorf("标记说明文件后新增 = %d，期望 1", s.Total.Add)
	}
}

// menuFixtureZip 造一个覆盖各种节点形态的补丁包：
// 根下的 README、套一层目录的 README、目录、普通文件、内层 jar。
func menuFixtureZip(t *testing.T) string {
	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"README.txt":         "#\n",
		"mypatch/README.txt": "#\n",
		"mypatch/BOOT-INF/classes/com/example/New.class": "x",
		"leaf.txt":          "#\n",
		"lib/foo-1.0.0.jar": "jar",
	})
	return zipPath
}

// TestPatchMenuClaimsFollowUpstream 钉住右键菜单的**完整内容与顺序**。
//
// 原版 buildPatchTreeContextMenu 是一串 MenuItemClaim，每项各自判断显不显示，
// 加进 ContextMenu 的顺序就是渲染顺序。所以这里断言的是"精确的有序 ID 列表"
// 而不是"含不含某一项" —— 顺序错了观感就不对，得能测出来。
func TestPatchMenuClaimsFollowUpstream(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()

	cases := []struct {
		path string
		want []string
		why  string
	}{
		{
			"README.txt",
			[]string{menuEditContent, menuSaveFile, menuCopy, menuFocusPatchTree},
			"根下的普通文件：能编辑/保存/复制；它已是 README，所以「标记说明文件」不出现" +
				"（claim 要求 Type == NONE）；不是压缩包条目以外的磁盘文件，故无「资源管理器中查看」",
		},
		{
			"leaf.txt",
			[]string{menuMarkReadme, menuEditContent, menuSaveFile, menuCopy, menuFocusPatchTree},
			"根下的普通文件且状态为 NONE -> 可标说明文件",
		},
		{
			"mypatch",
			[]string{menuMarkRoot, menuCopy, menuFocusPatchTree},
			"目录且有子节点 -> 可标根；目录不能标说明文件、不能编辑/保存文件",
		},
		{
			"mypatch/README.txt",
			[]string{menuEditContent, menuSaveFile, menuCopy, menuFocusPatchTree},
			"父级是普通目录而不是根 -> 不能标说明文件（这就是要先标根的原因）",
		},
		{
			"lib/foo-1.0.0.jar",
			[]string{menuEditContent, menuSaveFile, menuCopy, menuFocusPatchTree},
			"压缩包条目没有磁盘路径 -> 没有「资源管理器中查看」",
		},
		{
			"mypatch/BOOT-INF/classes/com/example/New.class",
			[]string{menuEditContent, menuSaveFile, menuSaveSourceFile, menuCopy, menuFocusPatchTree},
			"深处的 .class：比普通文件多一项「保存源文件」（claim 要求后缀是 .class），" +
				"且它紧跟在「保存文件」后面",
		},
	}
	for _, c := range cases {
		n := findNodeByPath(s.PatchTree, c.path)
		if n == nil {
			t.Errorf("补丁树里找不到 %s", c.path)
			continue
		}
		if got := menuIDs(n); !sameStrings(got, c.want) {
			t.Errorf("%s 的菜单 = %v，期望 %v（%s）", c.path, got, c.want, c.why)
		}
	}
}

// TestPatchMenuRootNodeClaims 钉住主根那一份菜单。
//
// 主根独有几项：重新加载（文案与自定义根的"刷新信息"不同）、查看签名、
// 关闭补丁。而「查看绑定信息」在主根上要求"没有自定义根"。
//
// 注意主根**会**出现「保存文件」和「资源管理器中查看」：原版里根节点是
// 一个 `new FileNode()`（isDir() 取自磁盘），补丁包是 zip 时它既不是目录、
// 也不是 CompressNode，两条 claim 都命中。别"看着别扭"就把它们挡掉。
func TestPatchMenuRootNodeClaims(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()

	want := []string{
		menuReload, menuEditContent, menuSaveFile, menuCopy, menuViewSign,
		menuViewNodeMapped, menuOpenInExplorer, menuClosePatch, menuFocusPatchTree,
	}
	if got := menuIDs(s.PatchTree); !sameStrings(got, want) {
		t.Fatalf("主根菜单 = %v，期望 %v", got, want)
	}

	// 文案：主根是「重新加载」，自定义根是「刷新信息」—— 同一个 ID，两种文案
	if it := menuItem(s.PatchTree, menuReload); it == nil || it.Label != trZh("app.view.right-tree.reload") {
		t.Errorf("主根的重新加载文案 = %+v，期望 %q", it, trZh("app.view.right-tree.reload"))
	}
	// 根节点自己不能标成根、不能标说明文件
	if hasMenu(s.PatchTree, menuMarkRoot) || hasMenu(s.PatchTree, menuMarkReadme) {
		t.Error("补丁根节点自己不该可以标记为根或说明文件")
	}

	// 标了自定义根之后：主根的「查看绑定信息」消失（claim 要求没有自定义根），
	// 而自定义根上出现它
	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}
	s = app.GetState()
	if hasMenu(s.PatchTree, menuViewNodeMapped) {
		t.Error("有自定义根时，主根不该再显示「查看绑定信息」")
	}
	my := findNodeByPath(s.PatchTree, "mypatch")
	if !hasMenu(my, menuViewNodeMapped) {
		t.Errorf("自定义根应当显示「查看绑定信息」: %v", menuIDs(my))
	}
	// 自定义根的文案是「刷新信息」
	if it := menuItem(my, menuReload); it == nil || it.Label != trZh("app.view.right-tree.refresh") {
		t.Errorf("自定义根的刷新文案 = %+v，期望 %q", it, trZh("app.view.right-tree.refresh"))
	}
	// 自定义根是压缩包里的目录，不是磁盘文件 -> 仍然没有「资源管理器中查看」
	if hasMenu(my, menuOpenInExplorer) {
		t.Error("压缩包条目不该显示「资源管理器中查看」")
	}
}

// TestPatchMenuNoAppPackHidesViewNodeMapped 确认应用包没打开时
// 「查看绑定信息」不出现（claim 里那句 appTree.getTreeInfo() != null）。
func TestPatchMenuNoAppPackHidesViewNodeMapped(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()
	if hasMenu(s.PatchTree, menuViewNodeMapped) {
		t.Error("应用包没打开时不该显示「查看绑定信息」")
	}
}

// TestPatchMenuRadioAndDisabledStates 钉住单选勾选态与置灰项。
func TestPatchMenuRadioAndDisabledStates(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()

	// 「标记根节点」是单选，未标记时不勾
	if it := menuItem(findNodeByPath(s.PatchTree, "mypatch"), menuMarkRoot); it == nil ||
		!it.Radio || it.Checked {
		t.Errorf("mypatch 的「标记根节点」应当是未勾选单选: %+v", it)
	}
	// 「聚焦补丁」是单选，默认（都显示）不勾
	if it := menuItem(s.PatchTree, menuFocusPatchTree); it == nil || !it.Radio || it.Checked {
		t.Errorf("「聚焦补丁」应当是未勾选单选: %+v", it)
	}

	// 目录的「复制MD5值」置灰但仍显示（对齐原版 setDisable）
	if it := menuItem(findNodeByPath(s.PatchTree, "mypatch"), menuCopyMd5); it == nil || !it.Disabled {
		t.Errorf("目录的「复制MD5值」应当置灰: %+v", it)
	}
	// 文件的「复制MD5值」可点
	if it := menuItem(findNodeByPath(s.PatchTree, "leaf.txt"), menuCopyMd5); it == nil || it.Disabled {
		t.Errorf("文件的「复制MD5值」不该置灰: %+v", it)
	}

	// 「复制」有四个子项，顺序与原版一致
	if it := menuItem(s.PatchTree, menuCopy); it == nil || len(it.Children) != 4 {
		t.Fatalf("「复制」子菜单 = %+v", it)
	} else {
		want := []string{menuCopyName, menuCopyPath, menuCopyRelative, menuCopyMd5}
		got := make([]string, 0, 4)
		for _, c := range it.Children {
			got = append(got, c.ID)
		}
		if !sameStrings(got, want) {
			t.Errorf("「复制」子项 = %v，期望 %v", got, want)
		}
	}
}

// TestPatchMenuFocusPatchTreeChecked 确认聚焦状态会反映到勾选态上。
func TestPatchMenuFocusPatchTreeChecked(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.SetFocusTree(focusTreePatch)
	if s.FocusTree != focusTreePatch {
		t.Errorf("FocusTree = %d，期望 %d", s.FocusTree, focusTreePatch)
	}
	if it := menuItem(s.PatchTree, menuFocusPatchTree); it == nil || !it.Checked {
		t.Errorf("聚焦补丁时该项应当勾上: %+v", it)
	}

	// 非法值归零（"都显示"）
	if s := app.SetFocusTree(99); s.FocusTree != focusTreeAll {
		t.Errorf("非法聚焦值应当归 0，得到 %d", s.FocusTree)
	}
	if it := menuItem(app.GetState().PatchTree, menuFocusPatchTree); it == nil || it.Checked {
		t.Errorf("回到「都显示」时该项不该勾: %+v", it)
	}
}

// ---------------------------------------------------------------------------
// 右键菜单各动作
// ---------------------------------------------------------------------------

// TestReloadOrRefreshOnRootReloadsPack 覆盖「重新加载」走的是"整包重读"。
//
// 判据用"自定义根被清掉了" —— 重读会重新建树，之前标的 mypatch 自然不再是
// 自定义根，Add 也回到 0。如果实现只重跑映射，这两条都会保持原样。
func TestReloadOrRefreshOnRootReloadsPack(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}

	rootPath := app.GetState().PatchTree.Path
	s, err := app.ReloadOrRefresh(treeSidePatch, rootPath)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	if n := findNodeByPath(s.PatchTree, "mypatch"); n == nil || n.Type == "customRoot" {
		t.Errorf("整包重读后 mypatch 不该还是自定义根: %+v", n)
	}
	if s.Total.Add != 0 {
		t.Errorf("整包重读后新增 = %d，期望 0（README 又回到认不出的状态）", s.Total.Add)
	}
}

// TestReloadOrRefreshOnCustomRootKeepsCustomRoot 覆盖「刷新信息」走的是
// "只重跑映射"，**不重读整包**。
//
// 与上一条互为对照：同样是 ReloadOrRefresh，落点在自定义根下时，
// 自定义根必须还在（说明没有重读包），而 README 的映射结果必须保持
// （说明确实重跑了一遍映射，不是什么都不做）。
func TestReloadOrRefreshOnCustomRootKeepsCustomRoot(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}

	s, err := app.ReloadOrRefresh(treeSidePatch, "mypatch/README.txt")
	if err != nil {
		t.Fatalf("刷新信息失败: %v", err)
	}
	if n := findNodeByPath(s.PatchTree, "mypatch"); n == nil || n.Type != "customRoot" {
		t.Fatalf("刷新信息不该重读整包，mypatch 应当还是自定义根: %+v", n)
	}
	if s.Total.Add != 1 {
		t.Errorf("刷新信息后新增 = %d，期望 1（README 的 `+` 指令仍然生效）", s.Total.Add)
	}
}

// TestReloadOrRefreshMissingPackIsNoop 确认补丁包文件不在了时**什么都不做**。
//
// 对应 Java reloadPatchTree 里那句 `if (file.exists())` —— 不报错、不清空，
// 保持现状比抛错更不容易让人懵。
func TestReloadOrRefreshMissingPackIsNoop(t *testing.T) {
	app := NewApp()
	zipPath := nestedReadmePatchZip(t)
	if _, err := app.OpenPatchPack(zipPath); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if err := os.Remove(zipPath); err != nil {
		t.Fatalf("删除夹具失败: %v", err)
	}

	rootPath := app.GetState().PatchTree.Path
	s, err := app.ReloadOrRefresh(treeSidePatch, rootPath)
	if err != nil {
		t.Fatalf("包不在了不该报错，却得到: %v", err)
	}
	if s.PatchTree == nil {
		t.Error("包不在了不该把已打开的补丁树清掉")
	}
}

// TestCancelMappedClearsMapping 覆盖「取消关联」。
//
// 从菜单可见性入手：能取消关联的节点才会出现那一项，取消后它就该消失，
// 同时统计回退。这样断言的是**用户看得见的效果**，不是内部字段。
func TestCancelMappedClearsMapping(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}

	const target = "mypatch/BOOT-INF/classes/com/example/New.class"
	s := app.GetState()
	if s.Total.Add != 1 {
		t.Fatalf("前置条件不成立：新增 = %d，期望 1", s.Total.Add)
	}
	if n := findNodeByPath(s.PatchTree, target); !hasMenu(n, menuCancelMapped) {
		t.Fatalf("已建立映射的节点应当出现「取消关联」: %v", menuIDs(n))
	}

	s, err := app.CancelMapped(treeSidePatch, target)
	if err != nil {
		t.Fatalf("取消关联失败: %v", err)
	}
	if s.Total.Add != 0 {
		t.Errorf("取消关联后新增 = %d，期望 0", s.Total.Add)
	}
	n := findNodeByPath(s.PatchTree, target)
	if n == nil {
		t.Fatal("取消关联后节点不该消失")
	}
	if n.Type != "none" {
		t.Errorf("取消关联后节点状态 = %q，期望 none", n.Type)
	}
	if hasMenu(n, menuCancelMapped) {
		t.Error("取消关联后不该再显示「取消关联」")
	}
}

// TestClosePatchPack 覆盖「关闭补丁」。
func TestClosePatchPack(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if s := app.GetState(); !s.Matched {
		t.Fatal("前置条件不成立：两个包都该是打开状态")
	}

	s, err := app.ClosePatchPack()
	if err != nil {
		t.Fatalf("关闭补丁失败: %v", err)
	}
	if s.PatchTree != nil || s.PatchPath != "" {
		t.Errorf("关闭后补丁侧应当清空: path=%q tree=%v", s.PatchPath, s.PatchTree != nil)
	}
	if s.Matched {
		t.Error("关闭后不该还算「已匹配」")
	}
	// 应用包本身保留
	if s.AppTree == nil {
		t.Fatal("关闭补丁不该把应用包也关掉")
	}

	// 应用树上"由补丁带来"的状态会留着 —— 这是**上游行为**，不是没清干净。
	//
	// 原因：cleanMappedNode 清的是"有 MappedNode 对端"的节点，而纯删除标记
	// 走的是 MappedDelNodeWithType → CountAndSetNodeType，只改类型、**不建立
	// 映射**，所以清映射的遍历够不到它。原版 handleClosePatch 也只清补丁侧，
	// 之后 fireEvent 触发的 refreshPatchMappedNode 因 treeInfo 已置空而直接返回。
	//
	// 结论：关掉补丁后应用树上仍会显示上一次的增/改/删标记。照搬，不"顺手修好"。
	// （由代码推得，未跑 Java 复现 —— 见 MIGRATION.md §3.8。）
	if n := findNode(s.AppTree, "Gone.class"); n == nil || n.Type != "del" {
		t.Errorf("按上游行为，关闭补丁后应用树上的删除标记应当保留: %+v", n)
	}
}

// TestOpenPatchPackCleansPreviousMapping 钉住"换补丁包会先把上一份的映射清掉"。
//
// 这是补做的一处移植缺口：Java refreshPatchTree 开头会先
// cleanPatchMappedNode(旧根)，而 Go 早期版本直接换掉了 treeInfo ——
// 上一份补丁在应用树上留下的映射和增/改标记会一直挂着，看起来像是新补丁
// 带来的，实际是残留。
//
// 判据落在"换成不相干的补丁 B 之后"这一步。别拿 A→B→A 的最终态去比 ——
// 那样比不出来：残留的标记在新补丁重新映射时会被同一批节点重新覆盖成
// 一样的值，最终态反而相同（这是实测踩到的，变异验证时这条测试没变红）。
func TestOpenPatchPackCleansPreviousMapping(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	s, err := app.OpenPatchPack(fixture(t, "patch.zip")) // A：README 里有 `+` 指令
	if err != nil {
		t.Fatalf("打开补丁包 A 失败: %v", err)
	}
	if s.Total.Add == 0 || s.Total.Mod == 0 {
		t.Fatalf("前置条件不成立：补丁包 A 应当产生增/改标记，得到 %+v", s.Total)
	}

	// B 与 app.jar 没有任何能对上的条目，所以换过去之后不该剩下任何增/改
	s, err = app.OpenPatchPack(menuFixtureZip(t))
	if err != nil {
		t.Fatalf("打开补丁包 B 失败: %v", err)
	}
	if s.Total.Add != 0 || s.Total.Mod != 0 {
		t.Errorf("换成补丁包 B 后仍有上一份的残留标记：%+v，期望新增/修改都是 0", s.Total)
	}

	// 应用树上同样不该再挂着上一份带来的增/改
	var dirty []string
	var walk func(*NodeView)
	walk = func(v *NodeView) {
		if v.Type == "add" || v.Type == "mod" {
			dirty = append(dirty, v.Name+"="+v.Type)
		}
		for _, c := range v.Children {
			walk(c)
		}
	}
	walk(s.AppTree)
	if len(dirty) > 0 {
		t.Errorf("应用树上还挂着上一份补丁的标记: %v", dirty)
	}

	// 说明：DEL 标记会留下来（Gone.class）。这不是漏清，与「关闭补丁」同一个
	// 上游行为 —— 纯删除标记不建立映射，清映射的遍历够不到它。
	// 详见 TestClosePatchPack 里的说明。
}

// TestCopyNodePathKinds 覆盖「复制」里那条最容易写错的路径拼接。
func TestCopyNodePathKinds(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()

	cases := []struct {
		path string
		want string
		why  string
	}{
		{
			"mypatch/BOOT-INF/classes/com/example/New.class",
			"mypatch/BOOT-INF/classes/com/example/New.class",
			"从自己往上收集到（不含）根，再反转用 '/' 连接",
		},
		{
			"lib/foo-1.0.0.jar",
			"lib/foo-1.0.0.jar",
			"同上",
		},
	}
	for _, c := range cases {
		n := findNodeByPath(s.PatchTree, c.path)
		if n == nil {
			t.Errorf("补丁树里找不到 %s", c.path)
			continue
		}
		if got := nodeDisplayPathByView(app, c.path); got != c.want {
			t.Errorf("%s 的显示路径 = %q，期望 %q（%s）", c.path, got, c.want, c.why)
		}
	}
}

// nodeDisplayPathByView 走公开接口取节点的显示路径。
//
// 直接调 nodeDisplayPath 要拿 *tree.Node，而前端视图拿不到；
// 这里复现一遍它的算法没有意义 —— 改成从"复制路径"那条真实链路取，
// 只是剪贴板写不进去（没有 Wails 上下文），所以退一步：这里只做
// "拼出来的路径恰好等于包内路径"的等价断言。
func nodeDisplayPathByView(app *App, path string) string {
	n, err := app.patchNode(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return nodeDisplayPath(n)
}

// TestSourceSaveName 覆盖「保存源文件」的默认文件名算法。
//
// 关键点是**把后缀换掉**而不是往后追加：原版先 getName() 去掉原后缀，
// 再拼 ".java"。写成 `Foo.class.java` 虽然也能用，但和原版不一致。
func TestSourceSaveName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Foo.class", "Foo.java"},
		{"Foo$Inner.class", "Foo$Inner.java"},
		{"a.b.C.class", "a.b.C.java"},
		{"README", "README.java"},
	}
	for _, c := range cases {
		n := tree.NewFile(c.name, c.name, 0, time.Time{}, nil, true)
		if got := sourceSaveName(n); got != c.want {
			t.Errorf("sourceSaveName(%q) = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestIsClassNode 钉住「保存源文件」的适用判据。
//
// 菜单显不显示、动作做不做，两处共用这一个函数 —— 各写一遍 endsWith
// 迟早会漂移（比如某天把 `Foo.class/` 这种目录也放进来）。
func TestIsClassNode(t *testing.T) {
	file := func(name string) *tree.Node {
		return tree.NewFile(name, name, 0, time.Time{}, nil, true)
	}
	cases := []struct {
		n    *tree.Node
		want bool
		why  string
	}{
		{file("Foo.class"), true, "普通 class"},
		{file("Foo$Inner.class"), true, "内部类也是 class"},
		{file("Foo.class.bak"), false, "后缀不是 class"},
		{file("Foo.java"), false, "源码不是 class"},
		{file("class"), false, "只有名字没有后缀"},
		{nil, false, "空节点不该 panic"},
	}
	for _, c := range cases {
		if got := isClassNode(c.n); got != c.want {
			name := "<nil>"
			if c.n != nil {
				name = c.n.Name
			}
			t.Errorf("isClassNode(%s) = %v，期望 %v（%s）", name, got, c.want, c.why)
		}
	}

	// 目录即使名字以 .class 结尾也不算 —— 原版 claim 里有 `!node.isDir()`
	dirNode := tree.NewDir("weird.class", "weird.class/", true)
	if isClassNode(dirNode) {
		t.Error("名字像 class 的目录不该算 class（原版有 !isDir() 这一条）")
	}
}

// TestSaveSourceFileMenuOnlyOnPatchTree 钉住「保存源文件」当前只接在补丁树上。
//
// 原版两棵树都注册了这一项（HomeLeftTreeView / HomeRightTreeView 各一份），
// Go 侧本轮只接了补丁树，应用树那一项还没接（见 appMenu 的注释）。
//
// 这条断言把"还没接"显式钉住：哪天有人顺手补上，它会红，逼着一起改
// appMenu 注释、MIGRATION.md 和走查 —— 而不是悄悄漂移成"文档说没接、
// 代码已经接了"。同时钉住 claim 条件（非 .class 不出现）。
func TestSaveSourceFileMenuOnlyOnPatchTree(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(menuFixtureZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()

	patchClass := findNodeByPath(s.PatchTree, "mypatch/BOOT-INF/classes/com/example/New.class")
	if patchClass == nil {
		t.Fatal("补丁树里找不到 .class 夹具")
	}
	if !hasMenu(patchClass, menuSaveSourceFile) {
		t.Errorf("补丁树的 .class 上应当有「保存源文件」: %v", menuIDs(patchClass))
	}
	// 顺序：紧跟在「保存文件」后面（原版两项就是挨着的）
	ids := menuIDs(patchClass)
	for i, id := range ids {
		if id == menuSaveFile && (i+1 >= len(ids) || ids[i+1] != menuSaveSourceFile) {
			t.Errorf("「保存源文件」应当紧跟「保存文件」，实际 = %v", ids)
		}
	}
	// claim 条件：非 .class 不出现
	if hasMenu(findNodeByPath(s.PatchTree, "leaf.txt"), menuSaveSourceFile) {
		t.Error("非 .class 的节点不该出现「保存源文件」")
	}

	// 应用树：本轮**没有**这一项（原版有，只是还没接）
	appClass := findNodeByPath(s.AppTree, "BOOT-INF/classes/com/example/Foo.class")
	if appClass == nil {
		t.Fatal("应用树里找不到 .class 夹具")
	}
	if hasMenu(appClass, menuSaveSourceFile) {
		t.Error("应用树的 .class 目前不该有「保存源文件」—— 本轮只接了补丁树。" +
			"真要接的话，记得同步 appMenu 的注释、MIGRATION.md 与前端走查")
	}
}

// TestSaveSourceNodeAsIgnoresNonClass 钉住「非 .class 静默不做」。
//
// 原版 saveSourceFile 第一句就是
// `if (selectedItem == null || !name.endsWith(DOT_CLASS)) return;`
// —— 连错都不报。菜单项本来就只在 .class 上出现，走到这里说明是前端
// 传错了，弹个错只会打断用户。
//
// 这里能测是因为"提前返回"发生在弹对话框**之前**：真去弹了就卡住了。
func TestSaveSourceNodeAsIgnoresNonClass(t *testing.T) {
	app := openEditorApp(t)

	s, err := app.SaveSourceNodeAs(treeSidePatch, "notes.txt")
	if err != nil {
		t.Fatalf("非 .class 不该报错: %v", err)
	}
	if s == nil {
		t.Fatal("应当返回当前状态")
	}
}

// TestSourceBytesDecompiles 钉住「保存源文件」真正导出的字节。
//
// 这是整条链路上唯一能自动测的一环（SaveSourceNodeAs 本身要弹系统对话框）：
// 导出的是**反编译出来的 Java 源码**而不是 class 字节 —— 后者用
// SaveNodeAs 就能做，用户点「保存源文件」要的显然不是它。
//
// 依赖本机 JRE，没装就跳过（与 TestOpenEditorClassUsesSourceMode 同口径）。
func TestSourceBytesDecompiles(t *testing.T) {
	if !decompile.Available() {
		t.Skip("本机没有 Java，跳过源文件导出")
	}
	app := openEditorApp(t)

	n, err := app.patchNode("Legacy.class")
	if err != nil {
		t.Fatalf("取节点失败: %v", err)
	}
	data, err := sourceBytes(n)
	if err != nil {
		t.Skipf("真 class 样本不可用，跳过：%v", err)
	}
	src := string(data)
	if !strings.Contains(src, "ChineseProbe") {
		t.Errorf("导出的应当是 Java 源码（含类名），实际前 120 字符 = %q", head(src, 120))
	}
	// 关键判据：不能是 class 字节 —— class 文件头是 CA FE BA BE
	if len(data) >= 4 && data[0] == 0xCA && data[1] == 0xFE {
		t.Error("导出的是 class 字节而不是反编译源码")
	}
	// 编码：必须是合法 UTF-8（对齐编辑器「另存为 .java」的口径；
	// 原版 content.getBytes() 走平台默认编码，在中文 Windows 上是 GBK，
	// 见 MIGRATION.md 里登记的偏差）
	if !utf8.Valid(data) {
		t.Error("导出的源码不是合法 UTF-8")
	}
}

// TestDefaultSaveName 覆盖「保存文件」的默认文件名算法。
func TestDefaultSaveName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Foo.class", "Foo.class"},
		{"README", "README"},
		{"foo-1.0.0.jar", "foo-1.0.0.jar"},
		{"a.b.c", "a.b.c"},
	}
	for _, c := range cases {
		n := tree.NewFile(c.name, c.name, 0, time.Time{}, nil, true)
		if got := defaultSaveName(n); got != c.want {
			t.Errorf("defaultSaveName(%q) = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestWriteNodeToFile 覆盖「保存文件」真正写盘那一步。
//
// 用带 0x00 和 0xff 的内容，确认是**按字节**写而不是当文本处理 ——
// 补丁包里的 .class 是二进制，转成文本就废了。
func TestWriteNodeToFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte{0x00, 0x01, 0x02, 0xca, 0xfe, 0xba, 0xbe, 0xff, 0x00}
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("造夹具失败: %v", err)
	}
	n := tree.NewFile("src.bin", src, int64(len(content)), time.Now(),
		&tree.FileSource{Path: src}, true)

	out := filepath.Join(dir, "out.bin")
	if err := writeNodeToFile(n, out); err != nil {
		t.Fatalf("写出失败: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("写出内容 = %v，期望 %v", got, content)
	}
}

// TestNameWithoutVersion 覆盖绑定信息文本里的版本号处理。
//
// **注意这是上游的行为，不是"看起来该怎样"**：正则 `-[0-9.]+-.*` 要求版本号
// 后面**还有一段**（用 '-' 隔开），所以 `foo-1.0.0.jar` 原样保留，
// 只有 `bar-2.3.4-SNAPSHOT.jar` 这种才被削成 `bar`。
// 直觉上"应该把版本号去掉"，但原版就是这么写的，照搬。
func TestNameWithoutVersion(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"bar-2.3.4-SNAPSHOT.jar", "bar", "版本号后面还有一段（SNAPSHOT）-> 整个削掉"},
		{"baz-1.0.0-RELEASE.jar", "baz", "同上"},
		{"foo-1.0.0.jar", "foo-1.0.0.jar", "版本号后没有第二段 '-' -> 正则不匹配，原样保留（上游如此）"},
		{"plain.jar", "plain.jar", "没有版本号 -> 原样"},
		{"note.txt", "note.txt", "不是 jar -> 原样"},
	}
	for _, c := range cases {
		if got := nameWithoutVersion(c.in); got != c.want {
			t.Errorf("nameWithoutVersion(%q) = %q，期望 %q（%s）", c.in, got, c.want, c.why)
		}
	}
}

// TestGetPatchSign 覆盖「查看签名」。
func TestGetPatchSign(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	rootPath := app.GetState().PatchTree.Path

	sign, err := app.GetPatchSign(rootPath)
	if err != nil {
		t.Fatalf("取签名失败: %v", err)
	}
	if sign.Name == "" {
		t.Error("签名里的名称不该为空")
	}
	if sign.Sha1 == "" || sign.Md5 == "" {
		t.Errorf("补丁包是 zip，sha1/md5 都该算出来: %+v", sign)
	}

	// 非根节点没有签名信息
	if _, err := app.GetPatchSign("BOOT-INF/classes/com/example/Foo.class"); err == nil {
		t.Error("非根节点不该能取到签名")
	}
}

// TestGetNodeMappedText 覆盖「查看绑定信息」的文本生成。
func TestGetNodeMappedText(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if _, err := app.MarkRoot("mypatch"); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}

	text, err := app.GetNodeMappedText("mypatch")
	if err != nil {
		t.Fatalf("取绑定信息失败: %v", err)
	}
	t.Logf("绑定信息文本:\n%s", text)

	// README 里是 `+ BOOT-INF/classes/com/example/New.class`，
	// 相对自定义根 mypatch 的路径就是 BOOT-INF/classes/com/example/New.class，
	// 后面跟一个制表符和对应的应用侧路径。
	//
	// 注意标记与路径之间**没有空格**（原版是 sb.append(code) 后直接 append 路径），
	// 别看着别扭就加一个。
	if !strings.Contains(text, "+BOOT-INF/classes/com/example/New.class\t") {
		t.Errorf("绑定信息里缺少新增行: %q", text)
	}

	// 没打开应用包时应当报错（菜单里也挡掉了）
	bare := NewApp()
	if _, err := bare.OpenPatchPack(nestedReadmePatchZip(t)); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	if _, err := bare.GetNodeMappedText(bare.GetState().PatchTree.Path); err == nil {
		t.Error("应用包没打开时不该能取到绑定信息")
	}
}

// patchDirWithNestedReadme 造一个**补丁目录**模式的包：
//
//	<dir>/mypatch/README.txt
//	<dir>/mypatch/BOOT-INF/classes/com/example/New.class
//
// 用目录而不是 zip，是因为下面那条测试要在两次调用之间**改盘上的 README
// 内容** —— zip 改不了。目录模式下节点内容是从磁盘惰性读的，改了就能读到。
func patchDirWithNestedReadme(t *testing.T) string {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	write("mypatch/README.txt", "# patch\n+ BOOT-INF/classes/com/example/New.class\n")
	write("mypatch/BOOT-INF/classes/com/example/New.class", "x")
	return dir
}

// TestReloadOrRefreshOnCustomRootSkipsReadme 精确钉住「刷新信息」传的是
// withReadme=false —— 也就是**不重读说明文件**，只重跑映射。
//
// 上一条测试证明不了这件事：README 已经识别过了，重读一遍结果一样。
// 这里改成"把盘上的 README 内容换掉"，两条路径就分岔了：
//
//	withReadme=false -> 不重读 -> 还用旧的指令 -> 新增 1
//	withReadme=true  -> 重读   -> 新内容没有指令 -> 新增 0
//
// 最后用 ReMatch 走一次 true 的分支做对照，确认"改盘上的文件确实会被读到"，
// 排除"改文件根本不起作用"的假阳性。
func TestReloadOrRefreshOnCustomRootSkipsReadme(t *testing.T) {
	patchDir := patchDirWithNestedReadme(t)

	app := NewApp()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchDir); err != nil {
		t.Fatalf("打开补丁目录失败: %v", err)
	}

	// 目录模式下节点 Path 是绝对路径，所以按名字找节点再取它的 Path
	mypatch := findNodeByName(app.patchTreeInfo.RootNode(), "mypatch")
	if mypatch == nil {
		t.Fatal("补丁树里找不到 mypatch")
	}
	readme := findNodeByName(mypatch, "README.txt")
	if readme == nil {
		t.Fatal("mypatch 下找不到 README.txt")
	}

	if _, err := app.MarkRoot(mypatch.Path); err != nil {
		t.Fatalf("标记为根失败: %v", err)
	}
	if s := app.GetState(); s.Total.Add != 1 {
		t.Fatalf("前置条件不成立：标记根后新增 = %d，期望 1", s.Total.Add)
	}

	// 把盘上的 README 换成"没有指令"的版本
	onDisk := filepath.Join(patchDir, "mypatch", "README.txt")
	if err := os.WriteFile(onDisk, []byte("# patch\n"), 0o644); err != nil {
		t.Fatalf("改写 README 失败: %v", err)
	}

	s, err := app.ReloadOrRefresh(treeSidePatch, readme.Path)
	if err != nil {
		t.Fatalf("刷新信息失败: %v", err)
	}
	if s.Total.Add != 1 {
		t.Errorf("「刷新信息」不该重读说明文件，新增 = %d，期望 1（仍在用改之前的那份指令）", s.Total.Add)
	}

	// 对照：走 withReadme=true 的分支（ReMatch → refreshAllRoots）应当读到新内容
	s, err = app.ReMatch()
	if err != nil {
		t.Fatalf("重新匹配失败: %v", err)
	}
	if s.Total.Add != 0 {
		t.Errorf("「重新匹配」会重读说明文件，新增 = %d，期望 0（新内容里没有指令）", s.Total.Add)
	}
}

// TestStateCarriesReadmeTextAndReadmeNodeType 钉住"README 正文会被送到前端"。
//
// 背景：原版把 README 显示在首页底部的「补丁信息」面板里
// （HomeView.buildBottomCentre + updatePatchInfo），这块面板一度是漏做的。
// 症状很隐蔽 —— README 识别对了、ReadmeText 也算出来了，但 State 里没这个
// 字段，前端无从显示，用户看到的就是"README 没读出来"。
//
// 所以这里断的是**两个交付物**：State.ReadmeText（给底部面板）和
// 节点的 type == "readme"（给树上的高亮与标签）。
func TestStateCarriesReadmeTextAndReadmeNodeType(t *testing.T) {
	const readme = "# patch\n+ BOOT-INF/classes/com/example/New.class\n"

	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"README.txt":     readme,
		"extra/keep.txt": "x",
	})

	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(zipPath)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	if s.ReadmeText != readme {
		t.Errorf("State.ReadmeText 不对\n得到: %q\n期望: %q", s.ReadmeText, readme)
	}

	n := findNode(s.PatchTree, "README.txt")
	if n == nil {
		t.Fatal("补丁树上找不到 README.txt")
	}
	if n.Type != "readme" {
		t.Errorf("README 节点 type = %q，期望 readme（前端靠它上高亮和标签）", n.Type)
	}
}

// TestStateReadmeTextEmptyWhenNoReadme 是上一条的反面：
// 根下没有 README 时，ReadmeText 必须为空、也没有任何节点被标成 readme。
// 否则底部面板会凭空冒出一段"说明："，树也会乱标。
func TestStateReadmeTextEmptyWhenNoReadme(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"extra/keep.txt": "x",
		"a.class":        "y",
	})

	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(zipPath)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	if s.ReadmeText != "" {
		t.Errorf("没有 README 时 ReadmeText 应为空，得到 %q", s.ReadmeText)
	}
	var walk func(*NodeView)
	walk = func(v *NodeView) {
		if v == nil {
			return
		}
		if v.Type == "readme" {
			t.Errorf("没有 README 却把节点 %q 标成了 readme", v.Name)
		}
		for _, c := range v.Children {
			walk(c)
		}
	}
	walk(s.PatchTree)
}

// TestReadmeTextDecodesGBK 端到端钉住：GBK 编码的说明文件必须被正确解码。
//
// 这是用户实际报障的场景 —— 中文 Windows 上导出的补丁说明文件多是 GBK，
// 而底部「补丁信息」面板会把 ReadmeText 原样显示出来，解码错了一眼就能看见
// （整段变问号）。所以这条既覆盖 internal/util 的 GBK 解码，
// 也覆盖 service 层"读 README → 探测字符集 → 解码 → 汇总文本"这条链路。
func TestReadmeTextDecodesGBK(t *testing.T) {
	const zh = "获取组织机构单位树甘肃特殊处理999虚拟单位"
	readme := "# " + zh + "\n+ BOOT-INF/classes/com/example/New.class\n"

	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(readme))
	if err != nil {
		t.Fatalf("GBK 编码失败: %v", err)
	}
	// 先确认样本真的不是合法 UTF-8，否则"当 UTF-8 透传"的错误实现也能蒙对
	if utf8.Valid(gbk) {
		t.Fatal("样本是合法 UTF-8，测不出 GBK 解码")
	}

	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		// Go 的 string 可以承载任意字节，这里就是 GBK 字节
		"README.txt": string(gbk),
	})

	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(zipPath)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	if s.ReadmeText != readme {
		t.Errorf("GBK 说明文件解码不对\n得到: %q\n期望: %q", s.ReadmeText, readme)
	}
	if strings.Contains(s.ReadmeText, "\uFFFD") {
		t.Errorf("解出来还有替换字符，说明仍在按错误字符集处理: %q", s.ReadmeText)
	}
}

// findNodeByName 在补丁树里按节点名找（深度优先）。
func findNodeByName(root *tree.Node, name string) *tree.Node {
	if root == nil {
		return nil
	}
	if root.Name == name {
		return root
	}
	for _, c := range root.Children {
		if found := findNodeByName(c, name); found != nil {
			return found
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 展开嵌套压缩包
// ---------------------------------------------------------------------------

// buildZipBytes 在内存里造一个 zip 并返回字节，条目严格按传入顺序写。
//
// 特意不用 map：同级节点顺序就是 zip 条目顺序（见 tree.Node.AddChild 的说明），
// 用 map 会让断言依赖 Go 的随机遍历顺序。
func buildZipBytes(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("建条目 %s 失败: %v", e[0], err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("写条目 %s 失败: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return buf.Bytes()
}

// nestedJarFixture 造出"fat-jar 里有两个嵌套 jar"的场景，用来复现用户报的那个问题：
//
//	app.jar
//	  BOOT-INF/classes/com/example/Keep.class      没人碰
//	  BOOT-INF/lib/libA-1.0.0.jar                  补丁命中了里面的 Target.class
//	      └ com/example/Target.class               → 匹配时下探，子级【保留】
//	  BOOT-INF/lib/libB-2.0.0.jar                  补丁不碰它
//	      └ com/example/Other.class                → 匹配时下探失败，子级【回滚成 nil】
//	patch.zip
//	  Target.class                                 顶层，靠文件名匹配命中 libA 里的同名类
//
// libB 就是问题现场：它在树里一个子级都没有，但用户双击它本该能打开 ——
// 原版 HomeLeftTreeView.onMouseClicked 对 `name.endsWith(".jar")` 的节点会调
// buildChildrenWithCompress 现场展开。GUI 早期版本没有这个入口，于是这类节点
// 在界面上是"死叶子"，藏在里面的东西（包括高亮节点）永远看不到。
func nestedJarFixture(t *testing.T) (appJar, patchZip string) {
	t.Helper()
	dir := t.TempDir()

	libA := buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/Target.class", "TARGET-OLD"},
	})
	libB := buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/", ""},
		{"com/example/Other.class", "OTHER"},
		{"cn/", ""},
		{"cn/hutool/", ""},
		{"cn/hutool/A.class", "A"},
	})

	appJar = filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/classes/", ""},
		{"BOOT-INF/classes/com/", ""},
		{"BOOT-INF/classes/com/example/", ""},
		{"BOOT-INF/classes/com/example/Keep.class", "KEEP"},
		{"BOOT-INF/lib/", ""},
		{"BOOT-INF/lib/libA-1.0.0.jar", string(libA)},
		{"BOOT-INF/lib/libB-2.0.0.jar", string(libB)},
	}), 0o644); err != nil {
		t.Fatalf("写 app.jar 失败: %v", err)
	}

	patchZip = filepath.Join(dir, "patch.zip")
	writeZip(t, patchZip, map[string]string{
		"Target.class": "TARGET-NEW",
		"README.txt":   "# patch\n",
	})
	return appJar, patchZip
}

// TestNestedJarWithoutChildrenIsStillExpandable 钉住"压缩包节点必须带 compress 标记"。
//
// 这条容易静默失效：前端是"有 children 才画三角"，而后端如果只按 children 判，
// 那些匹配时下探失败、子级被回滚的 jar 就永远打不开 —— 界面上看不出任何异常，
// 只是点不动。所以断言落在**没有 children 的 jar 也带 compress** 这一点上。
func TestNestedJarWithoutChildrenIsStillExpandable(t *testing.T) {
	appJar, patchZip := nestedJarFixture(t)

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s, err := app.OpenPatchPack(patchZip)
	if err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	// 前置条件：libA 被匹配下探过、子级保留；libB 被回滚成没有子级
	libA := findNodeByPath(s.AppTree, "BOOT-INF/lib/libA-1.0.0.jar")
	libB := findNodeByPath(s.AppTree, "BOOT-INF/lib/libB-2.0.0.jar")
	if libA == nil || libB == nil {
		t.Fatal("应用树里找不到两个嵌套 jar")
	}
	if len(libA.Children) == 0 {
		t.Fatalf("前置条件不成立：libA 应当因为匹配下探而保留子级，实际 %+v", libA)
	}
	if len(libB.Children) != 0 {
		t.Fatalf("前置条件不成立：libB 的子级应当被回滚，实际有 %d 个", len(libB.Children))
	}

	// 关键断言：两个 jar 都要标成"可展开"
	if !libA.Compress || !libB.Compress {
		t.Errorf("嵌套 jar 必须带 compress 标记（libA=%v libB=%v）—— "+
			"否则前端画不出三角，用户点不开", libA.Compress, libB.Compress)
	}
	// 非压缩包节点不该带这个标记，否则前端会给出点了没反应的三角
	if n := findNodeByPath(s.AppTree, "BOOT-INF/classes/com/example/Keep.class"); n == nil || n.Compress {
		t.Errorf("普通文件不该标成可展开: %+v", n)
	}
	// 目录也不该标（目录靠 children 判）。注意这里带尾斜杠 —— zip 里
	// 显式写出的目录条目 Path 是**原样的条目名**（`BOOT-INF/lib/`），
	// 只有"补建的占位目录"才不带斜杠（见 internal/pack/zip.go 的保真点）。
	if n := findNodeByPath(s.AppTree, "BOOT-INF/lib/"); n == nil || n.Compress {
		t.Errorf("目录不该标成可展开: %+v", n)
	}
}

// TestExpandedChildrenPathHasNoJarPrefix 是**特征测试**：记录上游的一个既有行为。
//
// 展开嵌套 jar 之后，子节点的 Path 是**条目在 jar 里的原始名字**
// （`com/example/Other.class`），**不会**带上外层 jar 的路径。Java 侧同理：
// BasePackService.getOrCreateParentNode 在 parentPath 为空时返回的是
// `rootNode`（也就是那个 jar 节点），而节点的 Path 始终取自
// `entry.getName()`，从不重新拼接。
//
// 这条之所以要钉住，是因为它有一个**必须知道的副作用**：
// 两个嵌套 jar 里的同名条目会得到**完全相同的 Path**（`META-INF/MANIFEST.MF`
// 这种每个 jar 都有的东西必然重复）。而 GUI 侧是把 Path 当节点标识用的
// （原版用的是 TreeItem 对象引用，不存在这个问题），于是按 Path 找节点
// 只会拿到第一个 —— 对展开出来的第二、三个 jar 里的同名文件做操作
// （保存文件、复制、右键菜单）会落到错的那个上。
//
// 现状：**保持与 Java 一致**，不在这里给 Path 加前缀（那会同时改变
// 内部类归位、README 路径匹配等多处依赖）。要彻底解决应当给节点加一个
// 真正唯一的标识，见 MIGRATION.md §五。这条测试的作用是：哪天有人改了
// Path 的拼法，这里会变红，从而被迫回来读这段说明。
func TestExpandedChildrenPathHasNoJarPrefix(t *testing.T) {
	appJar, patchZip := nestedJarFixture(t)

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	const libBPath = "BOOT-INF/lib/libB-2.0.0.jar"
	v, err := app.ExpandNode(treeSideApp, libBPath)
	if err != nil {
		t.Fatalf("展开嵌套 jar 失败: %v", err)
	}

	got := map[string]string{} // name -> path
	var walk func(*NodeView)
	walk = func(n *NodeView) {
		got[n.Name] = n.Path
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(v)

	cases := []struct{ name, wantPath string }{
		{"com", "com/"},
		{"example", "com/example/"},
		{"Other.class", "com/example/Other.class"},
		{"A.class", "cn/hutool/A.class"},
	}
	for _, c := range cases {
		if p, ok := got[c.name]; !ok {
			t.Errorf("展开结果里没有 %s", c.name)
		} else if p != c.wantPath {
			t.Errorf("%s 的 Path = %q，期望 %q —— "+
				"展开出来的子级不带 jar 前缀是**上游行为**，改动它请先读这条测试的注释",
				c.name, p, c.wantPath)
		}
	}

	// 正面确认"会重复"这件事：同一个 jar 展开两次拿到的 Path 是一样的
	if got["Other.class"] == libBPath+"/com/example/Other.class" {
		t.Error("Path 带上了 jar 前缀 —— 与 Java 的 BasePackService 不一致了")
	}
}

// TestExpandNodeOpensUnexpandedJar 覆盖"把没有子级的嵌套 jar 展开出来"。
//
// 对应原版双击 .jar 节点那一下。展开后子级必须带上正确的路径层级，
// 否则后续按路径找节点（右键菜单、保存文件都靠它）会全部落空。
func TestExpandNodeOpensUnexpandedJar(t *testing.T) {
	appJar, patchZip := nestedJarFixture(t)

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	const libBPath = "BOOT-INF/lib/libB-2.0.0.jar"
	v, err := app.ExpandNode(treeSideApp, libBPath)
	if err != nil {
		t.Fatalf("展开嵌套 jar 失败: %v", err)
	}
	if len(v.Children) == 0 {
		t.Fatal("展开后仍然没有子级")
	}

	got := map[string]bool{}
	var walk func(*NodeView)
	walk = func(n *NodeView) {
		got[n.Path] = true
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(v)

	// 子级路径是**条目在 jar 里的原始名字**，不带 jar 前缀（上游行为，
	// 见 TestExpandedChildrenPathHasNoJarPrefix 的说明）
	for _, want := range []string{
		"com/",
		"com/example/",
		"com/example/Other.class",
		"cn/hutool/A.class",
	} {
		if !got[want] {
			t.Errorf("展开结果里缺少 %s（实际有 %v）", want, got)
		}
	}

	// 展开出来的节点状态一律是 none —— 原版这一步**不重跑匹配**
	if n := findNodeByPath(v, "com/example/Other.class"); n == nil || n.Type != "none" {
		t.Errorf("展开出来的节点状态应为 none（原版不重跑匹配）: %+v", n)
	}

	// 再展开一次必须幂等，且不能把已经有的子级弄丢
	again, err := app.ExpandNode(treeSideApp, libBPath)
	if err != nil {
		t.Fatalf("重复展开失败: %v", err)
	}
	if len(again.Children) != len(v.Children) {
		t.Errorf("重复展开改变了子级数: %d → %d", len(v.Children), len(again.Children))
	}
}

// TestExpandNodeKeepsExistingMapping 确认对"已经展开过"的 jar 调用展开
// 不会把匹配结果冲掉。
//
// 这是上一条的反面：libA 是匹配阶段下探过的，里面 Target.class 已经是 MOD。
// 展开入口必须原样返回现状，不能重建子级 —— 重建就等于把高亮抹了。
func TestExpandNodeKeepsExistingMapping(t *testing.T) {
	appJar, patchZip := nestedJarFixture(t)

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	const libA = "BOOT-INF/lib/libA-1.0.0.jar"
	v, err := app.ExpandNode(treeSideApp, libA)
	if err != nil {
		t.Fatalf("展开失败: %v", err)
	}
	n := findNodeByPath(v, "com/example/Target.class")
	if n == nil {
		t.Fatal("展开结果里找不到 Target.class")
	}
	if n.Type != "mod" {
		t.Errorf("Target.class 状态 = %q，期望 mod —— 展开不该冲掉已有匹配", n.Type)
	}
}

// TestExpandNodeRejectsNonCompress 覆盖两条拒绝路径。
//
// 原版这里没有守卫：双击普通文件走的是 ContentView，双击非压缩包的目录
// `showDialog` 里 `if (leftNode.isDir()) return` 直接什么都不做。我们把它
// 变成明确错误，属 §3.9 的刻意偏差 —— 但**必须真的挡住**，否则前端会拿到
// 一个空视图、把树渲染成"这一支没了"。
func TestExpandNodeRejectsNonCompress(t *testing.T) {
	appJar, patchZip := nestedJarFixture(t)

	app := NewApp()
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}

	cases := []struct{ name, side, path string }{
		{"普通文件", treeSideApp, "BOOT-INF/classes/com/example/Keep.class"},
		{"目录", treeSideApp, "BOOT-INF/lib"},
		{"路径不存在", treeSideApp, "BOOT-INF/lib/nope.jar"},
		{"补丁侧不是压缩包的节点", treeSidePatch, "README.txt"},
		{"未知的树", "nope", "whatever"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := app.ExpandNode(c.side, c.path); err == nil {
				t.Error("应当被拒绝，却成功了")
			}
		})
	}

	// 应用包没打开时也要给明确错误，而不是空视图
	bare := NewApp()
	if _, err := bare.ExpandNode(treeSideApp, "x.jar"); err == nil {
		t.Error("应用包没打开时不该能展开")
	}
}

// TestExpandNodeOnPatchSide 覆盖补丁树一侧（isPatch=true 那条分支）。
//
// 补丁包里也可能嵌 jar（`lib/foo.jar` 这种），原版右侧双击同样能展开。
// 这里顺便验证展开出来的子级会带上右键菜单 —— 菜单是**后端按父级算**的，
// 展开返回的视图如果漏了它，展开出来的文件就没有右键菜单可用。
func TestExpandNodeOnPatchSide(t *testing.T) {
	inner := buildZipBytes(t, [][2]string{
		{"com/", ""},
		{"com/example/Inner.class", "INNER"},
	})

	zipPath := filepath.Join(t.TempDir(), "patch.zip")
	writeZip(t, zipPath, map[string]string{
		"lib/nested-1.0.0.jar": string(inner),
		"README.txt":           "# patch\n",
	})

	app := NewApp()
	if _, err := app.OpenPatchPack(zipPath); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	s := app.GetState()
	jar := findNodeByPath(s.PatchTree, "lib/nested-1.0.0.jar")
	if jar == nil {
		t.Fatal("补丁树里找不到嵌套 jar")
	}
	if !jar.Compress {
		t.Fatal("补丁树里的嵌套 jar 也应当标成可展开")
	}
	if len(jar.Children) != 0 {
		t.Fatalf("补丁树建树阶段不该展开嵌套 jar，实际有 %d 个子级", len(jar.Children))
	}

	v, err := app.ExpandNode(treeSidePatch, "lib/nested-1.0.0.jar")
	if err != nil {
		t.Fatalf("展开补丁树里的嵌套 jar 失败: %v", err)
	}
	child := findNodeByPath(v, "com/example/Inner.class")
	if child == nil {
		t.Fatal("展开结果里找不到 Inner.class")
	}
	if len(child.Menu) == 0 {
		t.Error("展开出来的补丁节点没有右键菜单 —— 菜单是后端按父级算的，别漏")
	}
	if !hasMenu(child, menuSaveFile) {
		t.Errorf("展开出来的普通文件应当能「保存文件」: %v", menuIDs(child))
	}
}

// ---------------------------------------------------------------------------
// 在另一棵树里定位（LocateMapped）
// ---------------------------------------------------------------------------

// locateFixture 打开黄金夹具（app.jar + patch.zip）并跑完匹配。
func locateFixture(t *testing.T) *App {
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

// TestLocateMappedPatchToApp 覆盖"在补丁树点文件、应用树里定位"这条主链路。
//
// 夹具里两边的目录结构**并不一样**：补丁把 Same.class 放在根下，应用把它放在
// BOOT-INF/classes/com/example/ 里 —— 靠文件名匹配才连上的。所以这条同时验证了
// "定位的是对端节点自己"，而不是"路径拼接"。
func TestLocateMappedPatchToApp(t *testing.T) {
	app := locateFixture(t)

	r, err := app.LocateMapped(treeSidePatch, "Same.class")
	if err != nil {
		t.Fatalf("定位失败: %v", err)
	}
	if r.Side != treeSideApp {
		t.Errorf("目标树 = %q，期望 %q", r.Side, treeSideApp)
	}
	if r.Path != "BOOT-INF/classes/com/example/Same.class" {
		t.Errorf("目标路径 = %q", r.Path)
	}
	want := []string{
		"BOOT-INF/",
		"BOOT-INF/classes/",
		"BOOT-INF/classes/com/",
		"BOOT-INF/classes/com/example/",
	}
	if !sameStrings(r.Ancestors, want) {
		t.Errorf("祖先链 = %v，期望 %v", r.Ancestors, want)
	}
	// 祖先链里不该出现根 —— 根那一行永远可见，前端不用展开它
	rootPath := app.appTreeInfo.RootNode().Path
	for _, p := range r.Ancestors {
		if p == rootPath {
			t.Errorf("祖先链里混进了根节点: %q", p)
		}
	}

	// 返回的状态必须是**最新的、完整的**：前端直接拿它整体重渲染
	if r.State == nil {
		t.Fatal("返回的状态为空")
	}
	if r.State.AppTree == nil || r.State.PatchTree == nil {
		t.Error("返回的状态里两棵树不齐")
	}
	if findNodeByPath(r.State.AppTree, r.Path) == nil {
		t.Error("返回的状态里找不到目标节点 —— 前端会定位到空气")
	}
}

// TestLocateMappedIntoNestedJar 覆盖目标藏在**嵌套 jar 内部**的情况。
//
// 这一条的路径口径最反直觉：jar 内部子节点的 Path **不带 jar 前缀**
// （上游行为，见 TestExpandedChildrenPathHasNoJarPrefix），所以目标路径是
// `com/example/Bar.class`。正因如此，祖先链里必须依次出现 jar 自己那一层 ——
// 前端就是靠这条链一层层往下展开 DOM 的，少一层就滚不到目标。
func TestLocateMappedIntoNestedJar(t *testing.T) {
	app := locateFixture(t)

	r, err := app.LocateMapped(treeSidePatch, "BOOT-INF/lib/bar/com/example/Bar.class")
	if err != nil {
		t.Fatalf("定位失败: %v", err)
	}
	want := []string{
		"BOOT-INF/",
		"BOOT-INF/lib/",
		"BOOT-INF/lib/bar-1.0.0.jar",
		"com",
		"com/example",
	}
	if !sameStrings(r.Ancestors, want) {
		t.Errorf("祖先链 = %v，期望 %v", r.Ancestors, want)
	}
	if r.Path != "com/example/Bar.class" {
		t.Errorf("目标路径 = %q", r.Path)
	}
}

// TestLocateMappedReverseDirection 确认反向（应用树 -> 补丁树）也成立。
//
// 用户要的只是补丁 -> 应用；接口做成双向的，这条钉住"另一边没有写死"。
func TestLocateMappedReverseDirection(t *testing.T) {
	app := locateFixture(t)

	r, err := app.LocateMapped(treeSideApp, "BOOT-INF/classes/com/example/Same.class")
	if err != nil {
		t.Fatalf("定位失败: %v", err)
	}
	if r.Side != treeSidePatch {
		t.Errorf("目标树 = %q，期望 %q", r.Side, treeSidePatch)
	}
	if r.Path != "Same.class" {
		t.Errorf("目标路径 = %q", r.Path)
	}
	// Same.class 就挂在补丁根下，中间没有层
	if len(r.Ancestors) != 0 {
		t.Errorf("祖先链应为空，实际 %v", r.Ancestors)
	}
}

// TestLocateMappedAddNode 覆盖"补丁新增的文件"。
//
// 新增文件在应用树上没有实体，匹配会造一个 MappedNode 虚拟节点挂上去；
// 定位照样要能落到它上面 —— 否则用户在补丁树点新增文件会以为功能坏了。
func TestLocateMappedAddNode(t *testing.T) {
	app := locateFixture(t)

	r, err := app.LocateMapped(treeSidePatch, "BOOT-INF/classes/com/example/New.class")
	if err != nil {
		t.Fatalf("定位失败: %v", err)
	}
	if r.Path != "BOOT-INF/classes/com/example/New.class" {
		t.Errorf("目标路径 = %q", r.Path)
	}
	n := findNodeByPath(r.State.AppTree, r.Path)
	if n == nil {
		t.Fatal("返回的状态里找不到目标节点")
	}
	if n.Type != "add" {
		t.Errorf("目标节点状态 = %q，期望 add", n.Type)
	}
}

// TestLocateMappedRejects 覆盖四条拒绝路径。
//
// 这些都要**明确报错**而不是回一份空结果：前端拿到空结果会静默地什么都不做，
// 用户点了没反应又不知道为什么。
func TestLocateMappedRejects(t *testing.T) {
	app := locateFixture(t)

	cases := []struct{ name, side, path string }{
		{"补丁里没有对应项（README 不参与匹配）", treeSidePatch, "README.txt"},
		{"路径不存在", treeSidePatch, "nope.class"},
		{"未知的树", "nope", "whatever"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := app.LocateMapped(c.side, c.path); err == nil {
				t.Error("应当被拒绝，却成功了")
			}
		})
	}

	// 对端树还没打开：只开应用包，补丁树根本不存在
	bare := newAppZh()
	if _, err := bare.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := bare.LocateMapped(treeSidePatch, "Same.class"); err == nil {
		t.Error("补丁包没打开，定位却成功了")
	}
}

// TestNodeViewCarriesMappedFlag 钉住"有没有对应项"这个标记。
//
// 前端靠它决定"这一行点了要不要去另一棵树定位"。漏传不会报错，只是点了
// 没反应 —— 属于静默失效，所以单独钉一条。
func TestNodeViewCarriesMappedFlag(t *testing.T) {
	app := locateFixture(t)
	s := app.GetState()

	cases := []struct {
		side, path string
		want       bool
	}{
		{treeSidePatch, "Same.class", true},
		{treeSidePatch, "README.txt", false},
		{treeSidePatch, "BOOT-INF/lib/bar/com/example/Bar.class", true},
		{treeSideApp, "BOOT-INF/classes/com/example/Same.class", true},
		// 删除的文件在补丁里没有实体，两边都不该有绑定
		{treeSideApp, "BOOT-INF/classes/com/example/Gone.class", false},
	}
	for _, c := range cases {
		root := s.AppTree
		if c.side == treeSidePatch {
			root = s.PatchTree
		}
		n := findNodeByPath(root, c.path)
		if n == nil {
			t.Errorf("%s 树里找不到 %s", c.side, c.path)
			continue
		}
		if n.Mapped != c.want {
			t.Errorf("%s 的 Mapped = %v，期望 %v", c.path, n.Mapped, c.want)
		}
	}
}

// TestNodeViewCarriesMd5 钉住"文件节点带 MD5、目录不带"。
//
// 前端靠它显示详情列（修改时间 / 大小 / MD5）。漏传或传错（比如目录也填了 MD5）
// 不会报错，只是显示不对 —— 属于静默失效。
func TestNodeViewCarriesMd5(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	s := app.GetState()

	// 文件节点应当有非空 MD5
	fileNode := findNodeByPath(s.AppTree, "BOOT-INF/classes/com/example/Foo.class")
	if fileNode == nil {
		t.Fatal("找不到 Foo.class")
	}
	if fileNode.Md5 == "" {
		t.Errorf("文件节点的 Md5 不应为空")
	}

	// 目录节点不应有 Md5（omitempty，JSON 里不出现，解出来是空串）
	dirNode := findNodeByPath(s.AppTree, "BOOT-INF/")
	if dirNode == nil {
		t.Fatal("找不到 BOOT-INF 目录")
	}
	if dirNode.Md5 != "" {
		t.Errorf("目录节点的 Md5 应为空，实际 = %q", dirNode.Md5)
	}
}

// TestDeleteAppNodeRemovesFromTree 钉住「删除」的语义：物理摘节点 + 记手动删除。
//
// 对应 Java HomeLeftTreeView.handleManualDel。三个可观测后果：
//  1. 节点从应用树上消失（不是打个标记，是真没了）；
//  2. 统计 manualDel 计入内部类子节点（+2：Foo.class + Foo$Inner.class），
//     而 del 不动 —— 两者是独立计数；
//  3. 输出包里也不会再有它（节点已不在树的遍历路径上）。
func TestDeleteAppNodeRemovesFromTree(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"

	before := app.GetState()
	if findNodeByPath(before.AppTree, target) == nil {
		t.Fatalf("前置条件不成立：应用树里应存在 %s", target)
	}

	// 先取消补丁匹配带来的标记（Foo.class 可能已被补丁标成 ADD/MOD），
	// 恢复到 NONE 才能走「删除」路径。
	s, err := app.CancelMapped(treeSideApp, target)
	if err != nil {
		t.Fatalf("取消关联失败: %v", err)
	}
	n := findNodeByPath(s.AppTree, target)
	if n == nil || n.Type != "none" {
		t.Fatalf("取消关联后状态应为 none，实际 %+v", n)
	}

	s, err = app.DeleteAppNode(target)
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	// 1. 节点真的没了
	if n := findNodeByPath(s.AppTree, target); n != nil {
		t.Errorf("删除后节点仍留在应用树上: %+v", n)
	}
	// 2. 记的是"手动删除"：+2（外部类 + 内部类，CountNodeType 对 .class 递归子级）
	//    而不是"删除"
	if s.Total.ManualDel != before.Total.ManualDel+2 {
		t.Errorf("manualDel = %d，期望 %d（+2：含内部类）", s.Total.ManualDel, before.Total.ManualDel+2)
	}
	if s.Total.Del != before.Total.Del {
		t.Errorf("del = %d，不该被手动删除影响（期望 %d）", s.Total.Del, before.Total.Del)
	}

	// 3. 打出包来，被删的文件确实不在里面
	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if zipHasEntry(t, out, target) {
		t.Error("被删除的文件仍出现在输出包里")
	}
}

// TestMarkDeleteAppNodeToggles 钉住「标记删除」是切换语义，且节点留在树上。
//
// 对应 Java handleMarkDel：
//   - 首次点击 → 状态变 del、统计 del +2（含内部类），**节点仍在树上**；
//   - 再次点击 → 取消标记，状态回 none、统计减回去。
//
// 与「删除」的关键区别：标删的文件会被写进输出包的删除清单（delInfos），
// 而「删除」不会 —— 前者是补丁语义，后者只是把文件从工作视图里拿走。
func TestMarkDeleteAppNodeToggles(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"

	before := app.GetState()

	// 补丁匹配后 Foo.class 可能已是 ADD/MOD 状态，不能直接标删
	// （canMarkDelNode 只放 DEL/NONE 过）。先取消关联恢复 NONE。
	if n := findTreeNodeByPath(app.appTreeInfo.RootNode(), target); n != nil && n.Type != constant.TreeNodeTypeNone {
		if _, err := app.CancelMapped(treeSideApp, target); err != nil {
			t.Fatalf("取消关联失败: %v", err)
		}
	}

	// 首次：标删
	s, err := app.MarkDeleteAppNode(target)
	if err != nil {
		t.Fatalf("标记删除失败: %v", err)
	}
	n := findNodeByPath(s.AppTree, target)
	if n == nil {
		t.Fatal("标记删除后节点不该从树上消失（这是「标记」不是「删除」）")
	}
	if n.Type != "del" {
		t.Errorf("节点状态 = %q，期望 \"del\"", n.Type)
	}
	// +2：外部类 + 内部类（CountAndSetNodeType 对 .class 递归子级）
	if s.Total.Del != before.Total.Del+2 {
		t.Errorf("del = %d，期望 %d（+2：含内部类）", s.Total.Del, before.Total.Del+2)
	}
	if s.Total.ManualDel != before.Total.ManualDel {
		t.Errorf("标记删除不该动 manualDel（期望 %d，实际 %d）", before.Total.ManualDel, s.Total.ManualDel)
	}

	// 再次：取消标记
	s, err = app.MarkDeleteAppNode(target)
	if err != nil {
		t.Fatalf("取消标记失败: %v", err)
	}
	n = findNodeByPath(s.AppTree, target)
	if n == nil {
		t.Fatal("取消标记后节点不该消失")
	}
	if n.Type != "none" {
		t.Errorf("取消标记后状态 = %q，期望 \"none\"", n.Type)
	}
	if s.Total.Del != before.Total.Del {
		t.Errorf("取消标记后 del = %d，期望回到 %d", s.Total.Del, before.Total.Del)
	}

	// 标删状态下打补丁：文件不进包，但统计计入删除
	s2, err := app.MarkDeleteAppNode(target)
	if err != nil {
		t.Fatalf("标记删除失败: %v", err)
	}
	_ = s2
	out := filepath.Join(t.TempDir(), "out.jar")
	res, err := app.RunPatch(out)
	if err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if zipHasEntry(t, out, target) {
		t.Error("被标记删除的文件不应写进输出包")
	}
	if res.Total.Del != before.Total.Del+2 {
		t.Errorf("签名里的 del 统计 = %d，期望 %d（补丁原有 %d + 手动标删 2）", res.Total.Del, before.Total.Del+2, before.Total.Del)
	}
}

// TestDeleteAndMarkDeleteRejectIneligibleNodes 钉住两者的 claim 边界。
//
// 菜单已经挡过一次，这里验证服务端也拦得住 —— 这是公开方法，
// 前端或将来的其它入口不该能绕过「根节点不能删」「非 NONE 不能删」。
func TestDeleteAndMarkDeleteRejectIneligibleNodes(t *testing.T) {
	app := newAppZh()
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	// 根节点不能删
	rootPath := app.GetState().AppTree.Path
	if _, err := app.DeleteAppNode(rootPath); err == nil {
		t.Error("删除根节点应当被拒绝")
	}
	// 根节点也不能标删（类型是 ROOT，不在 DEL/NONE 里）
	if _, err := app.MarkDeleteAppNode(rootPath); err == nil {
		t.Error("标记删除根节点应当被拒绝")
	}

	// 标删之后就不能再「删除」了（状态已不是 NONE）
	const target = "BOOT-INF/classes/com/example/Foo.class"
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatalf("标记删除失败: %v", err)
	}
	if _, err := app.DeleteAppNode(target); err == nil {
		t.Error("对已标记删除的节点执行「删除」应当被拒绝（需先取消标记）")
	}

	// 不存在的路径
	if _, err := app.DeleteAppNode("不存在/路径.class"); err == nil {
		t.Error("删除不存在的节点应当报错")
	}
}

// TestManualDeleteSurvivesReopen 钉住"关掉重开删除还在"。
//
// 用户报障：删掉 bootstrap.yml，关窗口再打开文件又回来了。
// 这条是它的回归测试 —— 删除动作要落进持久化层，重开时自动重应用。
func TestManualDeleteSurvivesReopen(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"
	if findNodeByPath(app.GetState().AppTree, target) == nil {
		t.Fatalf("前置条件不成立：应用树里应有 %s", target)
	}

	if _, err := app.DeleteAppNode(target); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	// 关掉再打开
	if _, err := app.CloseAppPack(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	s, err := app.OpenAppPack(appPath)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}

	if n := findNodeByPath(s.AppTree, target); n != nil {
		t.Errorf("重开后 %s 又回来了（持久化没生效）", target)
	}
	if s.Total.ManualDel != 2 {
		t.Errorf("重开后 manualDel = %d，期望 2（外部类 + 内部类）", s.Total.ManualDel)
	}
}

// TestMarkDeleteSurvivesReopen 标删同样要跨会话保持，
// 且恢复成 NONE 后应撤销这条记录（否则下次打开又标回来）。
func TestMarkDeleteSurvivesReopen(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"

	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatalf("标删失败: %v", err)
	}
	// 重开后状态必须还在
	if _, err := app.CloseAppPack(); err != nil {
		t.Fatal(err)
	}
	s, err := app.OpenAppPack(appPath)
	if err != nil {
		t.Fatal(err)
	}
	n := findNodeByPath(s.AppTree, target)
	if n == nil {
		t.Fatal("标删的节点重开后不该消失（还是'标记'，不是'删除'）")
	}
	if n.Type != "del" {
		t.Errorf("重开后状态 = %q，期望 \"del\"", n.Type)
	}

	// 取消标删 → 记录应被撤掉，下次打开回到 none
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatalf("取消标删失败: %v", err)
	}
	if _, err := app.CloseAppPack(); err != nil {
		t.Fatal(err)
	}
	s, err = app.OpenAppPack(appPath)
	if err != nil {
		t.Fatal(err)
	}
	n = findNodeByPath(s.AppTree, target)
	if n == nil || n.Type != "none" {
		t.Errorf("取消标删并重开后状态 = %+v，期望 none", n)
	}
}

// TestResetManualDel 钉住「恢复原状」这条唯一的整体出口。
//
// 用户报障：标记删除之后关掉重开还在，想取消却取消不掉；而物理删除的节点
// 已经从树上摘掉、右键都点不到，更没法逐条回滚。
// ResetManualDel = 清记录 + 按原包重新解析，两种删除都要能一把撤销。
func TestResetManualDel(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	const target = "BOOT-INF/classes/com/example/Untouched.class"

	open := func() *State {
		t.Helper()
		s, err := app.OpenAppPack(appPath)
		if err != nil {
			t.Fatalf("打开应用包失败: %v", err)
		}
		return s
	}

	s := open()
	if n := findNodeByPath(s.AppTree, target); n == nil || n.Type != "none" {
		t.Fatalf("前置条件不成立：%s 应是未改动的文件，实得 %+v", target, n)
	}
	if s.ManualDelPending {
		t.Error("刚打开就报「还有删除记录」")
	}

	// —— 标记删除 → 撤销 ——
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatalf("标删失败: %v", err)
	}
	s = app.GetState()
	if !s.ManualDelPending {
		t.Error("标删后 ManualDelPending 应为 true（否则右键看不到「恢复原状」）")
	}
	if len(s.ManualDelList) != 1 {
		t.Fatalf("临时补丁清单应有 1 条，实得 %d", len(s.ManualDelList))
	}

	s, err := app.ResetManualDel()
	if err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if n := findNodeByPath(s.AppTree, target); n == nil || n.Type != "none" {
		t.Errorf("撤销标删后状态 = %+v，期望 none", n)
	}
	if len(s.ManualDelList) != 0 {
		t.Errorf("撤销后临时补丁清单应清空，实得 %d 条", len(s.ManualDelList))
	}
	if s.ManualDelPending {
		t.Error("撤销后 ManualDelPending 应为 false")
	}

	// —— 物理删除 → 撤销（节点要回到树上）——
	if _, err := app.DeleteAppNode(target); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	s = app.GetState()
	if n := findNodeByPath(s.AppTree, target); n != nil {
		t.Fatal("物理删除后节点不该还在树上")
	}
	if !s.ManualDelPending {
		t.Error("物理删除后 ManualDelPending 应为 true")
	}

	s, err = app.ResetManualDel()
	if err != nil {
		t.Fatalf("撤销物理删除失败: %v", err)
	}
	if n := findNodeByPath(s.AppTree, target); n == nil || n.Type != "none" {
		t.Errorf("撤销物理删除后 %s 应回到树上且状态 none，实得 %+v", target, n)
	}
	if s.Total.ManualDel != 0 {
		t.Errorf("撤销后 manualDel 统计 = %d，期望 0", s.Total.ManualDel)
	}

	// 记录真的清掉了：再开一次不会又删一遍
	if _, err := app.CloseAppPack(); err != nil {
		t.Fatal(err)
	}
	s = open()
	if n := findNodeByPath(s.AppTree, target); n == nil {
		t.Error("撤销后重开，文件又不见了 —— 记录没清干净")
	}
	if s.ManualDelPending {
		t.Error("撤销后重开仍报「还有删除记录」")
	}
}

// TestReplaceNodeContent 钉住"拖文件替换节点内容"这条新交互：
//  1. 替换后节点带 edited 标记、ContentBytes 变成拖进来的文件；
//  2. 改过的内容真的流进打补丁的产物（单文件替换不依赖补丁包）；
//  3. 目录节点、目录来源被拒绝。
//
// 这条链路和"编辑内容"（SetContent）共用 ConsumeBytes → writer，所以第 2 条
// 断言同时验证了写出侧没有漏掉"来源是磁盘文件"这一支。
func TestReplaceNodeContent(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	const target = "BOOT-INF/classes/com/example/Untouched.class"
	const newContent = "REPLACED-BY-DROP"

	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	// 前置：该节点是个普通文件、还没改过
	if n := findNodeByPath(app.GetState().AppTree, target); n == nil || n.Edited {
		t.Fatalf("前置不成立：%s 应是未改文件，实得 %+v", target, n)
	}

	// 写一份要替换进来的磁盘文件
	tmp := filepath.Join(t.TempDir(), "replacement.class")
	if err := os.WriteFile(tmp, []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := app.ReplaceNodeContent(treeSideApp, target, tmp)
	if err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	if n := findNodeByPath(s.AppTree, target); n == nil || !n.Edited {
		t.Fatalf("替换后 %s 应带 edited 标记，实得 %+v", target, n)
	}

	// 内存里读出来应当就是新内容
	n, err := app.nodeBySide(treeSideApp, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.ContentBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != newContent {
		t.Fatalf("节点内容 = %q，期望 %q", got, newContent)
	}

	// 产物里也必须是新内容（不依赖补丁包，单开应用包即可打）
	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readZipEntry(t, out, target); got != newContent {
		t.Errorf("产物里 %s = %q，期望 %q —— 替换没传导到写出侧", target, got, newContent)
	}

	// 重新加载应丢弃替换（与编辑内容一致）
	if _, err := app.CloseAppPack(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatal(err)
	}
	if n := findNodeByPath(app.GetState().AppTree, target); n == nil || n.Edited {
		t.Errorf("重新加载后 %s 还带着替换，实得 %+v", target, n)
	}
}

// TestHandleDropRoutesToFileRow 钉住拖放分流：落在文件行上的普通文件，
// 目标是**它所在的目录**、判定按**名字**匹配，不是"就地替换这一行"。
//
// 这一条曾经断言成"替换那一行"，对应早期实现里的
// `ReplaceNodeContent(rowPath)` —— 那是与原版的分歧：原版
// `TreeNodeMatchProcessor.exec` 一律先算 `tarAppNode`（文件行取父目录），
// 再在该目录里逐个 `matchPatchName`。照"就地替换"走的话，把 `B.jar` 拖到
// `A.jar` 行上会把 A 的内容换成 B 的、名字还留着 A —— 静默改错文件。
func TestHandleDropRoutesToFileRow(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	const target = "BOOT-INF/classes/com/example/Untouched.class"
	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatal(err)
	}
	origSize := findNodeByPath(app.GetState().AppTree, target).Size

	// ① 名字不同 → 在同一个目录里**新增**，原来那个节点一动不动。
	tmp := filepath.Join(t.TempDir(), "r.class")
	if err := os.WriteFile(tmp, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSideApp, target, false, []string{tmp})
	if err != nil {
		t.Fatalf("落在文件行应能新增同名目录项，却报: %v", err)
	}
	const wantAdd = "BOOT-INF/classes/com/example/r.class"
	if r.Action != "add" || r.Path != wantAdd {
		t.Errorf("动作/路径 = %q/%q，期望 add/%q", r.Action, r.Path, wantAdd)
	}
	if n := findNodeByPath(app.GetState().AppTree, target); n == nil || n.Edited || n.Size != origSize {
		t.Errorf("落点是另一个文件行，%s 不该被动过，实得 %+v", target, n)
	}

	// ② 名字相同 → **覆盖**那个节点（这是"原先就有"的正解）。
	same := filepath.Join(t.TempDir(), "Untouched.class")
	if err := os.WriteFile(same, []byte("REPLACED"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSideApp, target, false, []string{same})
	if err != nil {
		t.Fatalf("落在文件行拖同名文件应走覆盖，却报: %v", err)
	}
	if r.Action != "replace" || r.Path != target {
		t.Errorf("动作/路径 = %q/%q，期望 replace/%q", r.Action, r.Path, target)
	}
	if n := findNodeByPath(app.GetState().AppTree, target); n == nil || !n.Edited {
		t.Error("覆盖后该节点应标记为 Edited")
	}

	// ③ 落在文件行但拖的是 .jar → 仍然是"包内文件"语义（**不是**打开整包）。
	// 原版就是这么分工的：行上的拖放归 FileTreeCell，jar 与普通文件一视同仁
	// （见 TreeNodeFileMatchProcessor）；只有空白处才打开整包。
	// 用真夹具当"拖进来的 jar"，避免无效的 zip 字节干扰路由判定。
	r, err = app.HandleDrop(treeSideApp, target, false, []string{appPath})
	if err != nil {
		t.Fatalf("落在文件行拖 .jar 应走包内文件，却报: %v", err)
	}
	if r.Action != "add" || r.Path != "BOOT-INF/classes/com/example/app.jar" {
		t.Errorf("落在文件行拖 .jar：动作/路径 = %q/%q，期望 add/…/app.jar",
			r.Action, r.Path)
	}
}

// TestHandleDropJarOntoAppPaneOpens 钉住另一半：jar 落在**空白处**才是"打开整包"。
//
// 与 TestHandleDropRoutesToFileRow 是一对 —— 同一个 jar，落点不同结果必须不同：
// 落在行上 = 加/换包内文件，落在空白 = 换整个应用包。
func TestHandleDropJarOntoAppPaneOpens(t *testing.T) {
	app := newTestApp(t)
	first := fixture(t, "app.jar")
	if _, err := app.OpenAppPack(first); err != nil {
		t.Fatal(err)
	}

	// 造第二个应用包（和第一个内容不同，才看得出真的换过去了）
	second := filepath.Join(t.TempDir(), "second.jar")
	writeZip(t, second, map[string]string{
		"BOOT-INF/classes/com/example/Only.class": "ONLY",
	})

	r, err := app.HandleDrop(treeSideApp, "", false, []string{second})
	if err != nil {
		t.Fatalf("落在空白处拖 .jar 应走打开整包，却报: %v", err)
	}
	if r.Action != "open" {
		t.Fatalf("动作 = %q，期望 open", r.Action)
	}
	if r.State == nil || r.State.AppPath != second {
		t.Fatalf("应用包没换过去：%+v", r.State)
	}
	if findNodeByPath(r.State.AppTree, "BOOT-INF/classes/com/example/Only.class") == nil {
		t.Error("新应用包里应能看到 Only.class")
	}
	if findNodeByPath(r.State.AppTree, "BOOT-INF/classes/com/example/Foo.class") != nil {
		t.Error("旧应用包里的 Foo.class 不该还在")
	}
}

// TestHandleDropJarOntoDirRowAddsFile 钉住"拖 jar 到目录行 = 加进这个目录"。
//
// 用户 2026-10-26："左侧 jar 包不支持其他 jar 包直接拖入，应该和文件一样"
// —— 之前拖到 `BOOT-INF/lib` 上会把整个应用包换掉，现在按文件名加进去。
func TestHandleDropJarOntoDirRowAddsFile(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}

	// 造一个内层 jar，准备拖进 BOOT-INF/lib
	inner := filepath.Join(t.TempDir(), "extra.jar")
	writeZip(t, inner, map[string]string{"X.class": "X"})

	r, err := app.HandleDrop(treeSideApp, "BOOT-INF/lib", true, []string{inner})
	if err != nil {
		t.Fatalf("拖 jar 到目录行应加进去，却报: %v", err)
	}
	if r.Action != "add" {
		t.Fatalf("动作 = %q，期望 add（不是 open —— 应用包不该被换掉）", r.Action)
	}
	if r.Path != "BOOT-INF/lib/extra.jar" {
		t.Errorf("新增路径 = %q，期望 BOOT-INF/lib/extra.jar", r.Path)
	}
	if r.State.AppPath != fixture(t, "app.jar") {
		t.Errorf("应用包被换掉了：%q", r.State.AppPath)
	}
	n := findNodeByPath(r.State.AppTree, "BOOT-INF/lib/extra.jar")
	if n == nil {
		t.Fatal("树里没有新增的 extra.jar")
	}
	if !n.Edited {
		t.Error("新增节点应标记为 Edited（只存在于内存）")
	}
}

// TestHandleDropJarOntoExistingLibJarReplaces 钉住"包里原先就有"这个场景：
// `BOOT-INF/lib` 下已经有一个同名 jar，再把一个同名 jar 拖进去 ——
// 结果是**覆盖那个条目的内容**，既不会跳过、也不会加出第二个同路径条目。
//
// 用户 2026-10-28 问的正是这件事（"直接拖入到包中，如果这个包原先就有，
// 那么是覆盖还是怎么样"）。两个落点都要成立：落在那个 jar 的行上、
// 落在 lib 目录行上 —— 前者会先退回父目录，最终判定路径相同。
func TestHandleDropJarOntoExistingLibJarReplaces(t *testing.T) {
	const target = "BOOT-INF/lib/bar-1.0.0.jar"

	for _, tc := range []struct {
		name  string
		row   string
		isDir bool
	}{
		{"落在同名 jar 的行上", target, false},
		{"落在 lib 目录行上", "BOOT-INF/lib", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)
			if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
				t.Fatal(err)
			}
			lib := findNodeByPath(app.GetState().AppTree, "BOOT-INF/lib/")
			before := len(lib.Children)

			// 同名但内容不同的"新版本"
			fresh := filepath.Join(t.TempDir(), "bar-1.0.0.jar")
			writeZip(t, fresh, map[string]string{"Y.class": "Y"})

			r, err := app.HandleDrop(treeSideApp, tc.row, tc.isDir, []string{fresh})
			if err != nil {
				t.Fatalf("拖同名 jar 应能覆盖，却报: %v", err)
			}
			if r.Action != "replace" {
				t.Errorf("动作 = %q，期望 replace（不是 add / open）", r.Action)
			}
			if r.Path != target {
				t.Errorf("覆盖的路径 = %q，期望 %q", r.Path, target)
			}

			// 关键：不能多出一个同路径条目
			lib = findNodeByPath(r.State.AppTree, "BOOT-INF/lib/")
			if len(lib.Children) != before {
				t.Errorf("lib 下的条目数从 %d 变成 %d，不该新增", before, len(lib.Children))
			}
			if n := findNodeByPath(r.State.AppTree, target); n == nil || !n.Edited {
				t.Error("被覆盖的节点应标记为 Edited")
			}
		})
	}
}

// TestParentLoc 钉住 parentLoc 的边界：嵌套定位串、根下文件、根下目录。
//
// 它只在"一次拖多个文件到文件行上"时用到（目标退回父目录），但写错了就会
// 静默加到错误的位置，所以单列。
func TestParentLoc(t *testing.T) {
	cases := []struct{ in, want string }{
		{"BOOT-INF/classes/com/example/Foo.class", "BOOT-INF/classes/com/example/"},
		{"BOOT-INF/lib/x.jar!/com/foo/Bar.class", "BOOT-INF/lib/x.jar!/com/foo/"},
		{"BOOT-INF/lib/x.jar!/Bar.class", "BOOT-INF/lib/x.jar!/"},
		{"Foo.class", ""}, // 根下的文件 → 根目录（空串）
		// 目录定位串（带尾斜杠）原样返回：它本身就是"往这里加"，
		// 再往上退一层反而错了。parentLoc 正常只被文件行调用，这条是兜底。
		{"a/b/", "a/b/"},
	}
	for _, c := range cases {
		if got := parentLoc(c.in); got != c.want {
			t.Errorf("parentLoc(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestHandleDropOnRootRow 钉住"根行按空白处处理"。
//
// 前端把根行也当普通行传过来，而根节点的 Path 是**磁盘路径**（不是包内路径）——
// 照普通行走会 resolveNode 失败、报"树里找不到节点"。根行语义上就是整棵树：
// 拖整包上来 = 换这个包，拖普通文件 = 加到根目录。
func TestHandleDropOnRootRow(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	s, err := app.OpenAppPack(appPath)
	if err != nil {
		t.Fatal(err)
	}
	rootPath := s.AppTree.Path
	// 前提：根节点的 Path 是**磁盘路径**（不是包内路径）—— 这条正是要兜住的坑，
	// 一旦哪天改成包内路径，这个测试就该跟着改口径。
	if rootPath == "" {
		t.Fatal("根节点没有 Path，测试前提不成立")
	}
	t.Logf("根节点 path = %q（磁盘路径，不是包内路径）", rootPath)

	// ① 拖整包到根行 → 打开整包（不是"替换这个节点"）
	second := filepath.Join(t.TempDir(), "second.jar")
	writeZip(t, second, map[string]string{"Only.class": "ONLY"})
	r, err := app.HandleDrop(treeSideApp, rootPath, false, []string{second})
	if err != nil {
		t.Fatalf("拖整包到根行不该报错: %v", err)
	}
	if r.Action != "open" {
		t.Fatalf("动作 = %q，期望 open", r.Action)
	}

	// ② 拖普通文件到根行 → 加到根目录（原来会报"树里找不到节点"）
	s, err = app.OpenAppPack(appPath)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "Root.class")
	if err := os.WriteFile(tmp, []byte("ROOT"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSideApp, rootPath, false, []string{tmp})
	if err != nil {
		t.Fatalf("拖普通文件到根行应加到根目录，却报: %v", err)
	}
	if r.Action != "add" || r.Path != "Root.class" {
		t.Fatalf("动作/路径 = %q/%q，期望 add/Root.class", r.Action, r.Path)
	}
}

// TestDropPatchNodeIntoAppTree 钉住"补丁树 → 应用树"这条拖放。
//
// 前端把补丁树的行标成可拖，落点只认应用树；规则全在后端（DropPatchNode →
// service.ApplyPatchNodesToApp）。这里覆盖四个关键点：落到目录行命中同名 →
// MOD、没命中 → ADD、落到包根行 → 加到包内顶层、说明文件不许拖。
//
// 夹具现场造（不依赖 _extract/golden）：这条链路是纯内存映射，不需要真实胖包。
func TestDropPatchNodeIntoAppTree(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	writeZip(t, appJar, map[string]string{
		"a/x.class": "X-ORIGINAL",
		"a/y.class": "Y-ORIGINAL",
	})
	patchZip := filepath.Join(dir, "patch.zip")
	writeZip(t, patchZip, map[string]string{
		"README.txt": "补丁说明\n",
		"d/x.class":  "X-PATCHED",
		"d/z.class":  "Z-PATCHED",
	})

	open := func(t *testing.T) (*App, *State) {
		t.Helper()
		app := newTestApp(t)
		if _, err := app.OpenAppPack(appJar); err != nil {
			t.Fatalf("打开应用包失败: %v", err)
		}
		s, err := app.OpenPatchPack(patchZip)
		if err != nil {
			t.Fatalf("打开补丁包失败: %v", err)
		}
		// 前置条件：这两个包在自动匹配阶段不该命中任何东西，
		// 否则下面的统计断言就分不清"拖出来的"和"自动匹配出来的"。
		if s.Total.Add != 0 || s.Total.Mod != 0 || s.Total.Del != 0 {
			t.Fatalf("前置条件不成立：自动匹配不该命中，实际 %+v", s.Total)
		}
		return app, s
	}

	t.Run("落到目录行：同名覆盖", func(t *testing.T) {
		app, _ := open(t)
		s, err := app.DropPatchNode("a/", true, []string{"d/x.class"})
		if err != nil {
			t.Fatalf("拖补丁节点到目录行失败: %v", err)
		}
		got := findNodeByPath(s.AppTree, "a/x.class")
		if got == nil || got.Type != "mod" {
			t.Fatalf("a/x.class 应被标成 mod，实际 %+v", got)
		}
		if !got.Mapped {
			t.Error("命中的节点应当带上 mapped 标记（前端靠它做两树联动）")
		}
		if s.Total.Mod != 1 || s.Total.Add != 0 {
			t.Errorf("统计不符：%+v（期望 mod=1 add=0）", s.Total)
		}
	})

	t.Run("落到目录行：没同名就新增", func(t *testing.T) {
		app, _ := open(t)
		s, err := app.DropPatchNode("a/", true, []string{"d/z.class"})
		if err != nil {
			t.Fatalf("拖补丁节点到目录行失败: %v", err)
		}
		got := findNodeByPath(s.AppTree, "a/z.class")
		if got == nil || got.Type != "add" {
			t.Fatalf("a/z.class 应被标成 add，实际 %+v", got)
		}
		if s.Total.Add != 1 {
			t.Errorf("统计不符：%+v（期望 add=1）", s.Total)
		}
	})

	t.Run("落到包根行：加到包内顶层", func(t *testing.T) {
		app, s := open(t)
		rootPath := s.AppTree.Path
		if rootPath == "" {
			t.Fatal("根节点没有 Path，测试前提不成立")
		}
		s, err := app.DropPatchNode(rootPath, false, []string{"d/z.class"})
		if err != nil {
			t.Fatalf("拖补丁节点到根行失败: %v", err)
		}
		got := findNodeByPath(s.AppTree, "z.class")
		if got == nil || got.Type != "add" {
			t.Fatalf("z.class 应被加到包内顶层，实际 %+v", got)
		}
	})

	t.Run("拖整包（根行）到目录行", func(t *testing.T) {
		/* 用户 2026-10-28 两轮反馈合起来才是真需求：
		   ① "右侧的 jar 包不能直接把这个 jar 包拖动到左侧" —— 根行必须能拖；
		   ② "拖到 lib 文件夹下面时，他把 jar 包分解放里面了" —— 根行代表的是
		   **那个 .jar 文件本身**，拖过去应当按**文件名**放进去，不是拆开铺进去。 */
		app, s := open(t)
		rootPath := s.PatchTree.Path
		if rootPath == "" {
			t.Fatal("补丁树根节点没有 Path，测试前提不成立")
		}
		s, err := app.DropPatchNode("a/", true, []string{rootPath})
		if err != nil {
			t.Fatalf("拖整包到目录行失败: %v", err)
		}
		// 按包文件名新增一项
		if got := findNodeByPath(s.AppTree, "a/patch.zip"); got == nil || got.Type != "add" {
			t.Fatalf("整包应当以文件名 a/patch.zip 加进去，实际 %+v", got)
		}
		// 关键：**不能被拆开**
		if findNodeByPath(s.AppTree, "a/d") != nil {
			t.Error("拖整包不该把包拆开，a 下不该出现包内的 d/ 目录")
		}
		if findNodeByPath(s.AppTree, "a/README.txt") != nil {
			t.Error("包内的说明文件不该跟着铺进来")
		}
	})

	t.Run("说明文件不许拖", func(t *testing.T) {
		app, _ := open(t)
		if _, err := app.DropPatchNode("a/", true, []string{"README.txt"}); err == nil {
			t.Fatal("说明文件拖进应用树应当报错（原版 isHasReadmeNode 时整次拖放被拒）")
		}
	})
}

// TestHandleDropSameContentSkips 钉住"同名且内容完全相同 → 什么都不做"。
//
// 用户 2026-10-28 提出："从外面拉入文件到对应位置时，如果存在同名的文件，
// 则迅速做出 md5 比较，如果 md5 相同的话警示一下"。
//
// 实现上做成"跳过 + 标一句"而不是"警示后仍然替换"：替换成同一份内容在语义上
// 是空操作，但 ReplaceNodeContent 会无条件 SetEdited(true) —— 界面上打出
// 「已修改」星号、md5 缓存被清空重算，用户以为改了，实际字节一个没变。
// 假改动比"少一个提示"更难受，所以直接跳过并回一个 same 动作。
func TestHandleDropSameContentSkips(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	writeZip(t, appJar, map[string]string{
		"a/x.class": "X-ORIGINAL",
		"a/y.class": "Y-ORIGINAL",
	})

	openApp := func(t *testing.T) *App {
		t.Helper()
		app := newTestApp(t)
		if _, err := app.OpenAppPack(appJar); err != nil {
			t.Fatalf("打开应用包失败: %v", err)
		}
		return app
	}
	writeFile := func(t *testing.T, name, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("写临时文件失败: %v", err)
		}
		return p
	}

	t.Run("内容相同 → same 且不打已修改", func(t *testing.T) {
		app := openApp(t)
		src := writeFile(t, "x.class", "X-ORIGINAL")

		r, err := app.HandleDrop(treeSideApp, "a/", true, []string{src})
		if err != nil {
			t.Fatalf("拖同内容文件不该报错: %v", err)
		}
		if r.Action != "same" {
			t.Fatalf("动作 = %q，期望 same（内容一致就不该替换）", r.Action)
		}
		if r.Path != "a/x.class" {
			t.Errorf("命中的路径 = %q，期望 a/x.class", r.Path)
		}
		n := findNodeByPath(r.State.AppTree, "a/x.class")
		if n == nil {
			t.Fatal("a/x.class 不见了")
		}
		if n.Edited {
			t.Error("内容相同不该打「已修改」标记 —— 用户会以为改了，其实字节没变")
		}
		if n.Md5 == "" {
			t.Error("跳过后 md5 缓存应当原样保留（被清空说明还是走了替换）")
		}
	})

	t.Run("内容不同 → 照旧替换", func(t *testing.T) {
		app := openApp(t)
		src := writeFile(t, "x.class", "X-NEW")

		r, err := app.HandleDrop(treeSideApp, "a/", true, []string{src})
		if err != nil {
			t.Fatalf("拖文件失败: %v", err)
		}
		if r.Action != "replace" {
			t.Fatalf("动作 = %q，期望 replace", r.Action)
		}
		if n := findNodeByPath(r.State.AppTree, "a/x.class"); n == nil || !n.Edited {
			t.Error("内容变了就该标「已修改」")
		}
	})

	t.Run("不同名 → 照旧新增", func(t *testing.T) {
		app := openApp(t)
		src := writeFile(t, "z.class", "X-ORIGINAL") // 内容相同但名字不同

		r, err := app.HandleDrop(treeSideApp, "a/", true, []string{src})
		if err != nil {
			t.Fatalf("拖文件失败: %v", err)
		}
		if r.Action != "add" {
			t.Fatalf("动作 = %q，期望 add（md5 比较只在同名时才有意义）", r.Action)
		}
	})

	t.Run("一次拖多个且全部内容相同 → same", func(t *testing.T) {
		app := openApp(t)
		x := writeFile(t, "x.class", "X-ORIGINAL")
		y := writeFile(t, "y.class", "Y-ORIGINAL")

		r, err := app.HandleDrop(treeSideApp, "a/", true, []string{x, y})
		if err != nil {
			t.Fatalf("拖文件失败: %v", err)
		}
		if r.Action != "same" {
			t.Errorf("动作 = %q，期望 same —— 一个都没改就不能说「已加入 N 个文件」", r.Action)
		}
	})
}

// TestFindAppCandidatesCarryMd5 钉住候选带着 md5 下发。
//
// 「补丁信息」清单要在用户**点之前**就标出"这一处内容一样、不用换"，
// 所以 md5 必须随候选一起到前端 —— 前端没法自己去包里读内容。
func TestFindAppCandidatesCarryMd5(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	writeZip(t, appJar, map[string]string{"a/x.class": "X-ORIGINAL"})

	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	cands, err := app.FindAppCandidates("x.class")
	if err != nil {
		t.Fatalf("反查失败: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("应当命中 1 处，实际 %d 处", len(cands))
	}
	src := filepath.Join(dir, "x.class")
	if err := os.WriteFile(src, []byte("X-ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := util.Md5([]byte("X-ORIGINAL"))
	if cands[0].Md5 != want {
		t.Errorf("候选 md5 = %q，期望 %q（前端要靠它比「内容是否相同」）", cands[0].Md5, want)
	}
}

// TestHandleDropJarOntoPatchRowStillOpens 钉住"行优先"只对应用侧生效。
//
// 原版补丁树的单元格根本不接拖放（FileTreeCell.onDragOver 里 `getTreeView() != appTree`
// 就 return），事件冒泡到 HomeRightTreeView 后整包照样是"打开补丁包"。所以拖一个 jar
// 到补丁树的行上，必须是 open，而不是往补丁树里塞一个文件。
func TestHandleDropJarOntoPatchRowStillOpens(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	patchPath := fixture(t, "patch.zip")
	s, err := app.OpenPatchPack(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	// 找一个补丁树上的目录行当落点
	var dirPath string
	var walk func(n *NodeView)
	walk = func(n *NodeView) {
		for _, c := range n.Children {
			if dirPath == "" && c.Dir {
				dirPath = c.Path
			}
			walk(c)
		}
	}
	walk(s.PatchTree)
	if dirPath == "" {
		t.Skip("夹具里没有目录节点")
	}

	r, err := app.HandleDrop(treeSidePatch, dirPath, true, []string{patchPath})
	if err != nil {
		t.Fatalf("补丁树上拖整包应打开补丁包，却报: %v", err)
	}
	if r.Action != "open" {
		t.Fatalf("动作 = %q，期望 open（补丁树不该接包内文件的拖放）", r.Action)
	}
}

// TestDropLooseFileMatchesByName 钉住"忘了路径"这条：
// 把普通文件拖到**补丁侧**，按文件名去应用树里反查同名文件。
//   - 命中 1 个 → 直接替换，并回报替换到了哪个路径；
//   - 命中多个 → 回 matchMany + 候选列表，让前端挑；
//   - 命中 0 个 → 回 matchNone。
func TestDropLooseFileMatchesByName(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}

	// —— 命中 1 个：app.jar 里只有一个 Foo.class ——
	tmpDir := t.TempDir()
	foo := filepath.Join(tmpDir, "Foo.class")
	if err := os.WriteFile(foo, []byte("MATCHED-BY-NAME"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{foo})
	if err != nil {
		t.Fatalf("按名匹配失败: %v", err)
	}
	// 命中 1 个也**不自动替换**：回候选让用户先确认落点（见 matchLooseIntoApp 注释）
	const wantPath = "BOOT-INF/classes/com/example/Foo.class"
	if r.Action != "matchMany" || len(dropCands(r)) != 1 || dropCands(r)[0].Path != wantPath {
		t.Fatalf("动作/候选 = %q/%+v，期望 matchMany 且唯一候选是 %q", r.Action, dropCands(r), wantPath)
	}
	if r.SrcPath != foo {
		t.Errorf("SrcPath = %q，期望 %q", r.SrcPath, foo)
	}
	// 体积要带回来：前端拿它和候选的大小并排显示，是判断"版本对不对"最快的线索
	if r.SrcSize != int64(len("MATCHED-BY-NAME")) {
		t.Errorf("SrcSize = %d，期望 %d", r.SrcSize, len("MATCHED-BY-NAME"))
	}
	// 用户点了候选之后才会真正替换；这里模拟那一步，并验证内容进产物
	if _, err := app.ReplaceNodeContent(treeSideApp, wantPath, foo); err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readZipEntry(t, out, wantPath); got != "MATCHED-BY-NAME" {
		t.Errorf("产物里 %s = %q，期望 MATCHED-BY-NAME", wantPath, got)
	}
	// 命中后前端还要把这个节点滚到眼前（LocateNode 给祖先链）
	loc, err := app.LocateNode(treeSideApp, wantPath)
	if err != nil {
		t.Fatalf("定位命中节点失败: %v", err)
	}
	if loc.Path != wantPath || len(loc.Ancestors) == 0 {
		t.Errorf("定位结果 = path %q / ancestors %v，期望 path %q 且有祖先", loc.Path, loc.Ancestors, wantPath)
	}

	// —— 命中 0 个 ——
	nothing := filepath.Join(tmpDir, "NoSuchName.class")
	if err := os.WriteFile(nothing, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSidePatch, "", false, []string{nothing})
	if err != nil {
		t.Fatalf("无命中不应报错: %v", err)
	}
	if r.Action != "matchNone" || r.FileName != "NoSuchName.class" {
		t.Errorf("动作/文件名 = %q/%q，期望 matchNone/NoSuchName.class", r.Action, r.FileName)
	}

	// —— 命中多个：另造一个有两处同名的 jar ——
	multi := filepath.Join(t.TempDir(), "multi.jar")
	if err := os.WriteFile(multi, buildZipBytes(t, [][2]string{
		{"a/Dup.class", "one"},
		{"b/Dup.class", "two"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app2 := newTestApp(t)
	if _, err := app2.OpenAppPack(multi); err != nil {
		t.Fatal(err)
	}
	dup := filepath.Join(t.TempDir(), "Dup.class")
	if err := os.WriteFile(dup, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app2.HandleDrop(treeSidePatch, "", false, []string{dup})
	if err != nil {
		t.Fatalf("多命中不应报错: %v", err)
	}
	if r.Action != "matchMany" {
		t.Fatalf("动作 = %q，期望 matchMany", r.Action)
	}
	if len(dropCands(r)) != 2 {
		t.Fatalf("候选数 = %d，期望 2", len(dropCands(r)))
	}
	if r.SrcPath != dup {
		t.Errorf("SrcPath = %q，期望 %q（前端挑完要用它再调替换）", r.SrcPath, dup)
	}
}

// TestHandleDropAddsNewFile 钉住"树里没有这个文件也能拖进来"这条：
//   - 落在目录行 → 加到该目录里；
//   - 落在空白处 → 加到根目录；
//   - 目录里已有同名文件 → 替换而不是加出两个同路径条目。
//
// 这是用户明确提出的缺口：只想改一个 class 时，那个 class 可能根本不在 jar 里。
func TestHandleDropAddsNewFile(t *testing.T) {
	app := newTestApp(t)
	appPath := fixture(t, "app.jar")
	// 目录节点在树里的 Path 带尾斜杠（与 zip 条目一致，见 writer 里
	// constant.MetaInfoDir 的比较方式），所以这里也带。
	const dir = "BOOT-INF/classes/com/example/"
	const addedPath = dir + "Added.class"

	if _, err := app.OpenAppPack(appPath); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}

	// 造一个"树里没有"的文件，文件名就是它落进包后的名字
	tmpDir := t.TempDir()
	added := filepath.Join(tmpDir, "Added.class")
	if err := os.WriteFile(added, []byte("NEW-CLASS-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}

	// —— 落在目录行 → 加进这个目录 ——
	r, err := app.HandleDrop(treeSideApp, dir, true, []string{added})
	if err != nil {
		t.Fatalf("落到目录行应能新增，却报: %v", err)
	}
	if r.Action != "add" || r.Path != addedPath {
		t.Fatalf("动作/路径 = %q/%q，期望 add/%q", r.Action, r.Path, addedPath)
	}
	if n := findNodeByPath(r.State.AppTree, addedPath); n == nil {
		t.Fatalf("新增后树里应有 %s，实际没有", addedPath)
	} else if !n.Edited {
		t.Errorf("新增的节点应带 edited 标记，实得 %+v", n)
	}

	// 产物里真的多出这个条目
	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readZipEntry(t, out, addedPath); got != "NEW-CLASS-BYTES" {
		t.Errorf("产物里 %s = %q，期望 NEW-CLASS-BYTES", addedPath, got)
	}

	// —— 落在空白处 → 加到根 ——
	rootFile := filepath.Join(tmpDir, "root-added.txt")
	if err := os.WriteFile(rootFile, []byte("ROOT"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSideApp, "", false, []string{rootFile})
	if err != nil {
		t.Fatalf("落到空白处应能新增到根，却报: %v", err)
	}
	if r.Path != "root-added.txt" {
		t.Errorf("新增路径 = %q，期望 root-added.txt", r.Path)
	}
	if n := findNodeByPath(r.State.AppTree, "root-added.txt"); n == nil {
		t.Fatal("根目录下应有 root-added.txt")
	}

	// —— 目录里已有同名文件 → 替换，不重复 ——
	again := filepath.Join(tmpDir, "Added.class")
	if err := os.WriteFile(again, []byte("SECOND"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSideApp, dir, true, []string{again})
	if err != nil {
		t.Fatalf("同名再拖一次应走替换，却报: %v", err)
	}
	dup := 0
	var walk func(*NodeView)
	walk = func(n *NodeView) {
		if n.Path == addedPath {
			dup++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(r.State.AppTree)
	if dup != 1 {
		t.Errorf("同路径条目应只有 1 个，实得 %d 个", dup)
	}
	if got := readZipEntry(t, mustRunPatch(t, app), addedPath); got != "SECOND" {
		t.Errorf("同名覆盖后产物里应是 SECOND，实得 %q", got)
	}

	// —— 补丁侧的普通文件不再是"拒绝"，而是按名反查（见 TestDropLooseFileMatchesByName）——
	if _, err := app.OpenPatchPack(fixture(t, "patch.zip")); err != nil {
		t.Fatalf("打开补丁包失败: %v", err)
	}
	r, err = app.HandleDrop(treeSidePatch, "", false, []string{rootFile})
	if err != nil {
		t.Fatalf("补丁侧拖普通文件应按名反查，不该报错: %v", err)
	}
	// root-added.txt 已经在应用树里，所以这里应当命中它（回候选让用户确认）
	if r.Action != "matchMany" || len(dropCands(r)) != 1 || dropCands(r)[0].Path != "root-added.txt" {
		t.Errorf("动作/候选 = %q/%+v，期望 matchMany 且候选是 root-added.txt", r.Action, dropCands(r))
	}
}

// TestDropLooseFileMatchesInsideNestedJar 钉住"往嵌套 jar 里搜"这条 ——
// Spring Boot 胖包的业务类大多在 BOOT-INF/lib/*.jar 里，不进去搜就等于搜不到
// （第一版就是这么漏的：用户拖一个明明在包里的 class，却提示"没有同名文件"）。
//
// 同时钉住副作用边界：**只展开命中的那个 jar**，没命中的必须原样写出。
func TestDropLooseFileMatchesInsideNestedJar(t *testing.T) {
	appJar, _ := nestedJarFixture(t)
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "Target.class")
	if err := os.WriteFile(src, []byte("NESTED-NEW"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := app.HandleDrop(treeSidePatch, "", false, []string{src})
	if err != nil {
		t.Fatalf("嵌套 jar 匹配失败: %v", err)
	}
	const loc = "BOOT-INF/lib/libA-1.0.0.jar!/com/example/Target.class"
	if r.Action != "matchMany" || len(dropCands(r)) != 1 || dropCands(r)[0].Path != loc {
		t.Fatalf("动作/候选 = %q/%+v，期望 matchMany 且唯一候选是 %q", r.Action, dropCands(r), loc)
	}
	if _, err := app.ReplaceNodeContent(treeSideApp, loc, src); err != nil {
		t.Fatalf("按定位串替换失败: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if got := readNestedEntry(t, out, "BOOT-INF/lib/libA-1.0.0.jar", "com/example/Target.class"); got != "NESTED-NEW" {
		t.Errorf("嵌套条目内容 = %q，期望 NESTED-NEW", got)
	}

	// 没命中的 libB 不该被重打包：字节必须与原包一致
	before := readZipEntryBytes(t, appJar, "BOOT-INF/lib/libB-2.0.0.jar")
	after := readZipEntryBytes(t, out, "BOOT-INF/lib/libB-2.0.0.jar")
	if !bytes.Equal(before, after) {
		t.Error("未命中的 libB 被重打包了（字节变了）—— 搜索不应该展开无关的 jar")
	}
}

// TestFindAppCandidatesSurvivesTrimmedTree 钉住"按名反查不依赖内存树的形状"。
//
// 用户报过：应用树上明明显示着 CallMethodInvokerAbstract.class，把这个文件
// 单独拖到右侧却提示"没有同名文件"。根因是搜索只走内存树 —— 而内存树只反映
// "打开那一刻 + 用户展开过的部分"，折叠着没展开的目录、被裁剪过的子树都不在
// 里面。拿它当全集回答"包里有没有这个文件"，就会给出假否定。
//
// 这里直接模拟那种形状：把中间目录的子级清空，搜索仍必须命中（走磁盘那一遍）。
func TestFindAppCandidatesSurvivesTrimmedTree(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	const want = "Foo.class"
	const wantPath = "BOOT-INF/classes/com/example/Foo.class"

	// 前置：正常形状下能搜到
	cands, err := app.FindAppCandidates(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 || cands[0].Path != wantPath {
		t.Fatalf("前置条件不成立：正常树上应命中 %q，实际 %+v", wantPath, cands)
	}

	// 把 BOOT-INF/classes 的子级清掉（等价于"这一块没展开/被裁剪"）
	classes := findTreeNodeByPathName(app.appTreeInfo.RootNode(), "classes")
	if classes == nil {
		t.Fatal("找不到 classes 目录节点")
	}
	classes.Children = nil

	cands, err = app.FindAppCandidates(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Path != wantPath {
		t.Fatalf("内存树被裁剪后仍应命中 %q，实际 %+v", wantPath, cands)
	}
}

// TestFindAppCandidatesJarInJar 钉住"jar 套 jar 也能找到、也能替换"。
//
// 搜索侧能钻到 `a.jar!/lib/b.jar!/com/foo/Bar.class`，替换侧就必须能解析
// 同样的定位串 —— 否则会出现"清单里点得到、一点替换就报找不到节点"。
func TestFindAppCandidatesJarInJar(t *testing.T) {
	inner := buildZipBytes(t, [][2]string{
		{"gfmis/", ""},
		{"gfmis/bgtex/", ""},
		{"gfmis/bgtex/CallMethodInvokerAbstract.class", "DEEP-OLD"},
	})
	mid := buildZipBytes(t, [][2]string{
		{"lib/", ""},
		{"lib/inner.jar", string(inner)},
	})
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/lib/", ""},
		{"BOOT-INF/lib/pay-common-business.jar", string(mid)},
	}), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}
	const want = "CallMethodInvokerAbstract.class"
	const loc = "BOOT-INF/lib/pay-common-business.jar!/lib/inner.jar!/gfmis/bgtex/CallMethodInvokerAbstract.class"

	src := filepath.Join(dir, want)
	if err := os.WriteFile(src, []byte("DEEP-NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{src})
	if err != nil {
		t.Fatalf("jar 套 jar 匹配失败: %v", err)
	}
	if r.Action != "matchMany" || len(dropCands(r)) != 1 || dropCands(r)[0].Path != loc {
		t.Fatalf("动作/候选 = %q/%+v，期望 matchMany 且唯一候选是 %q", r.Action, dropCands(r), loc)
	}

	// 定位串要能一路解析到最里面那一层（LocateNode 给祖先链，前端逐层展开）
	got, err := app.LocateNode(treeSideApp, loc)
	if err != nil {
		t.Fatalf("多级定位失败: %v", err)
	}
	if got.Path != "gfmis/bgtex/CallMethodInvokerAbstract.class" {
		t.Errorf("定位结果 path = %q", got.Path)
	}
	if len(got.Ancestors) == 0 {
		t.Error("定位结果应当带祖先链")
	}

	// 替换也要能落进去：产物里最内层的 jar 内条目应当是新内容
	if _, err := app.ReplaceNodeContent(treeSideApp, loc, src); err != nil {
		t.Fatalf("多级定位串替换失败: %v", err)
	}
	out := filepath.Join(dir, "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	midBytes := readZipEntryBytes(t, out, "BOOT-INF/lib/pay-common-business.jar")
	innerBytes := readEntryFromBytes(t, midBytes, "lib/inner.jar")
	if got := string(readEntryFromBytes(t, innerBytes, "gfmis/bgtex/CallMethodInvokerAbstract.class")); got != "DEEP-NEW" {
		t.Errorf("最内层条目内容 = %q，期望 DEEP-NEW", got)
	}
}

// TestFindAppCandidatesScanNoteOnMiss 钉住"没找到时要说明查了多少"。
//
// 假否定是这条功能最要命的失败方式。至少把"查了 N 个文件、M 个嵌套 jar"
// 摆出来，用户和我们才能判断是"真没有"还是"没查到"。
func TestFindAppCandidatesScanNoteOnMiss(t *testing.T) {
	appJar, _ := nestedJarFixture(t)
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "NoSuchName.class")
	if err := os.WriteFile(src, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "matchNone" {
		t.Fatalf("动作 = %q，期望 matchNone", r.Action)
	}
	if !strings.Contains(dropNote(r), "已检查") || !strings.Contains(dropNote(r), "嵌套 jar") {
		t.Errorf("ScanNote = %q，期望说明查了多少文件/多少个 jar", dropNote(r))
	}
}

// TestFindAppCandidatesReportsMappedPlaceholders 钉住"树上看得见 ≠ 包里有"。
//
// 真实报障：用户拖 CallMethodInvokerAbstract.class 反查，提示没有；可应用树上
// 明明有一行，还连着映射线。真相是补丁 README 声明了这个目标路径、而应用包里
// 根本没有 —— 匹配阶段沿路径造了 VirtualNode 目录 + 一个 MappedNode 文件。
// 它长得和真文件一样（有名字、有大小），但只是"补丁要新增的位置"。
//
// 所以搜不到时必须能把这类节点单独报出来，前端才解释得清。
func TestFindAppCandidatesReportsMappedPlaceholders(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/classes/", ""},
		{"BOOT-INF/classes/Keep.class", "KEEP"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}

	// README 里把这个类映射到一个应用包里并不存在的路径 → 匹配阶段造占位节点
	readme := "File Name: t\nPatch Author: a\nCreate Date: 2026/01/01 00:00\n\n" +
		"File Paths:\nBrand.class\tBOOT-INF/classes/new/deep/Brand.class\n\n" +
		"Auto Generated by JPatcher\n"
	patchZip := filepath.Join(dir, "patch.zip")
	writeZip(t, patchZip, map[string]string{
		"Brand.class": "BRAND",
		"README.txt":  readme,
	})

	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatal(err)
	}

	// 前置：树上确实有一个同名节点（否则这条用例就没意义了）
	if n := findTreeNodeByPathName(app.appTreeInfo.RootNode(), "Brand.class"); n == nil {
		t.Fatal("前置条件不成立：匹配后应用树上应当出现 Brand.class 占位节点")
	}

	cands, err := app.FindAppCandidates("Brand.class")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("包里没有这个文件，候选应为空，实际 %+v", cands)
	}

	src := filepath.Join(dir, "Brand.class")
	if err := os.WriteFile(src, []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "matchNone" {
		t.Fatalf("动作 = %q，期望 matchNone", r.Action)
	}
	const wantPath = "BOOT-INF/classes/new/deep/Brand.class"
	if len(dropPHs(r)) != 1 || dropPHs(r)[0].Path != wantPath {
		t.Fatalf("占位节点 = %+v，期望唯一一条 %q", dropPHs(r), wantPath)
	}
	if dropNote(r) == "" {
		t.Error("ScanNote 不该为空：没找到时要说明查了多少")
	}
}

// dropCands / dropPHs / dropNote 取"单文件拖放"结果的匹配信息。
//
// DropResult 现在按文件分组（Items），一个文件一条 —— 绝大多数用例只拖一个
// 文件，这几个小助手让断言不用层层下钻。
func dropCands(r *DropResult) []MatchCandidate {
	if r == nil || len(r.Items) == 0 {
		return nil
	}
	return r.Items[0].Candidates
}

func dropPHs(r *DropResult) []MatchCandidate {
	if r == nil || len(r.Items) == 0 {
		return nil
	}
	return r.Items[0].Placeholders
}

func dropNote(r *DropResult) string {
	if r == nil || len(r.Items) == 0 {
		return ""
	}
	return r.Items[0].ScanNote
}

// findTreeNodeByPathName 在树里按**名字**找第一个节点（测试用）。
func findTreeNodeByPathName(n *tree.Node, name string) *tree.Node {
	if n == nil {
		return nil
	}
	if n.Name == name {
		return n
	}
	for _, c := range n.Children {
		if found := findTreeNodeByPathName(c, name); found != nil {
			return found
		}
	}
	return nil
}

// readEntryFromBytes 从内存里的 zip 数据读一个条目（readZipEntryBytes 的 []byte 版）。
func readEntryFromBytes(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析内存 zip 失败: %v", err)
	}
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
		return b
	}
	t.Fatalf("内存 zip 里没有条目 %s", name)
	return nil
}

// readZipEntryBytes 读一个 zip 条目的原始字节（readZipEntry 的 []byte 版）。
func readZipEntryBytes(t *testing.T, zipPath, name string) []byte {
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
		return b
	}
	t.Fatalf("%s 里没有 %s", zipPath, name)
	return nil
}

// readNestedEntry 读"外层 zip 里的一个嵌套 zip 条目"。
func readNestedEntry(t *testing.T, zipPath, outer, inner string) string {
	t.Helper()
	data := readZipEntryBytes(t, zipPath, outer)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析嵌套 zip %s 失败: %v", outer, err)
	}
	for _, f := range zr.File {
		if f.Name != inner {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开嵌套条目 %s 失败: %v", inner, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("读嵌套条目 %s 失败: %v", inner, err)
		}
		return string(b)
	}
	t.Fatalf("%s 里没有 %s", outer, inner)
	return ""
}

// mustRunPatch 打一次补丁到临时文件并返回路径，供上面的断言复用。
func mustRunPatch(t *testing.T, app *App) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out2.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	return out
}

// TestManualDelDoesNotLeakAcrossJars 记录必须按 jar 路径隔离，
// 不能删了 A 包的文件导致 B 包同路径文件也消失。
func TestManualDelDoesNotLeakAcrossJars(t *testing.T) {
	app := newTestApp(t)
	appA := fixture(t, "app.jar")
	const target = "BOOT-INF/classes/com/example/Foo.class"

	if _, err := app.OpenAppPack(appA); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteAppNode(target); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	// 打开另一个包（用夹具里同结构的 jar 副本，路径不同）
	other := filepath.Join(t.TempDir(), "other.jar")
	if err := copyFileForTest(appA, other); err != nil {
		t.Fatalf("复制夹具失败: %v", err)
	}
	s, err := app.OpenAppPack(other)
	if err != nil {
		t.Fatal(err)
	}
	if n := findNodeByPath(s.AppTree, target); n == nil {
		t.Error("另一个 jar 的同路径文件不该被删")
	}
}

// copyFileForTest 拷一份夹具出来，供"同内容不同路径"的隔离用例用。
func copyFileForTest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// TestRunPatchWithoutPatchPack 钉住"只开应用包就能打补丁"。
//
// 这是最常见的用法：只打开一个 jar，右键标记删掉几个文件，直接输出新包。
// 后端 SaveAppPack 本就支持 PatchTreeInfo=nil（Writer 注释写的
// "不打补丁、只是重打包"），但 GUI 层曾把它挡死，前端按钮也置灰 ——
// 两边同时放开后这条用例守回归。
func TestRunPatchWithoutPatchPack(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatalf("打开应用包失败: %v", err)
	}
	if app.patchTreeInfo != nil {
		t.Fatal("前置条件不成立：本用例不该打开补丁包")
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"

	// 标记删除一个文件
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatalf("标记删除失败: %v", err)
	}

	// 没开补丁包也必须能打出新包
	out := filepath.Join(t.TempDir(), "out.jar")
	res, err := app.RunPatch(out)
	if err != nil {
		t.Fatalf("无补丁包时打补丁应成功，实际报错: %v", err)
	}
	if zipHasEntry(t, out, target) {
		t.Error("被标记删除的文件不应写进输出包")
	}
	// 非删除的文件一个都不能少
	for _, keep := range []string{
		"META-INF/MANIFEST.MF",
		"BOOT-INF/classes/com/example/Untouched.class",
		"BOOT-INF/lib/bar-1.0.0.jar",
	} {
		if !zipHasEntry(t, out, keep) {
			t.Errorf("输出包少了条目: %s", keep)
		}
	}
	if res.Total.Del != 2 {
		t.Errorf("删除统计 = %d，期望 2（外部类 + 内部类）", res.Total.Del)
	}

	// 产物落地后记录应被清掉 —— 否则下次打开新包又删一遍，统计虚高。
	entries, _ := app.manualDelStore.Load(app.appPath)
	if len(entries) != 0 {
		t.Errorf("打补丁后应清空记录，实际 %+v", entries)
	}
}

// TestRunPatchWithoutPatchPackPhysicalDelete 物理删 除同样要能独立成包。
func TestRunPatchWithoutPatchPackPhysicalDelete(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"
	if _, err := app.DeleteAppNode(target); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("无补丁包时打补丁应成功: %v", err)
	}
	if zipHasEntry(t, out, target) {
		t.Error("被物理删除的文件不应写进输出包")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("输出包应存在: %v", err)
	}
}

// TestManualDelListCollectsMarkedFiles 临时补丁清单的数据源。
func TestManualDelListCollectsMarkedFiles(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"

	// 空清单
	if got := app.GetState().ManualDelList; len(got) != 0 {
		t.Errorf("无标删时清单应为空，实际 %+v", got)
	}

	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatal(err)
	}
	list := app.GetState().ManualDelList
	// 外部类 + 内部类都会被标删，两条都要出现在清单里
	if len(list) != 2 {
		t.Fatalf("清单应有 2 条（外部类 + 内部类），实际 %d: %+v", len(list), list)
	}
	var found bool
	for _, e := range list {
		if e.Path == target {
			found = true
			if e.Name != "Foo.class" {
				t.Errorf("Name = %q，期望 Foo.class", e.Name)
			}
		}
	}
	if !found {
		t.Errorf("清单里没有 %s: %+v", target, list)
	}

	// 取消后清单应清空
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatal(err)
	}
	if got := app.GetState().ManualDelList; len(got) != 0 {
		t.Errorf("取消标删后清单应为空，实际 %+v", got)
	}
}

// TestLocateNodeWorksWithoutMapping 标删节点没有映射，LocateNode 必须能定位。
func TestLocateNodeWorksWithoutMapping(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	const target = "BOOT-INF/classes/com/example/Foo.class"
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatal(err)
	}

	r, err := app.LocateNode(treeSideApp, target)
	if err != nil {
		t.Fatalf("LocateNode 对标删节点应成功: %v", err)
	}
	if r.Path != target {
		t.Errorf("Path = %q，期望 %q", r.Path, target)
	}
	// 祖先链里不该有根节点（根永远可见，不用前端展开）
	for _, a := range r.Ancestors {
		if strings.HasSuffix(a, ".jar") {
			t.Errorf("祖先链里不该出现根: %q", a)
		}
	}

	// 不存在的路径应报错
	if _, err := app.LocateNode(treeSideApp, "不存在.class"); err == nil {
		t.Error("定位不存在的节点应报错")
	}
}

// TestManualDelListCarriesFullPath 临时补丁清单必须带**完整路径**。
//
// 用户报障：清单里只显示文件名（MANIFEST.MF / build-info.properties），
// 一个 Spring Boot jar 里同名文件遍地都是，根本分不清删的是哪一个。
// 这条钉住 Path 字段 —— 前端就是拿它当主文案的。
func TestManualDelListCarriesFullPath(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.OpenAppPack(fixture(t, "app.jar")); err != nil {
		t.Fatal(err)
	}
	// 标一个嵌套在 BOOT-INF/classes 里的文件，路径带好几层目录
	const target = "BOOT-INF/classes/com/example/Foo.class"
	if _, err := app.MarkDeleteAppNode(target); err != nil {
		t.Fatal(err)
	}

	list := app.GetState().ManualDelList
	if len(list) == 0 {
		t.Fatal("清单不该为空")
	}
	var found bool
	for _, e := range list {
		if e.Path == target {
			found = true
			// Path 必须是带目录的完整路径，不能只有文件名
			if !strings.Contains(e.Path, "/") {
				t.Errorf("Path = %q，缺目录段", e.Path)
			}
			if e.Name != "Foo.class" {
				t.Errorf("Name = %q，期望 Foo.class", e.Name)
			}
			if e.Size <= 0 {
				t.Errorf("Size = %d，应为正数", e.Size)
			}
		}
		// 所有条目都不该有反斜杠（内部路径统一 '/' 分隔）
		if strings.Contains(e.Path, "\\") {
			t.Errorf("Path 里出现反斜杠: %q", e.Path)
		}
	}
	if !found {
		t.Errorf("清单里没有 %s: %+v", target, list)
	}

	// 内部类也要在，且路径同样完整
	const inner = "BOOT-INF/classes/com/example/Foo$Inner.class"
	var innerFound bool
	for _, e := range list {
		if e.Path == inner {
			innerFound = true
		}
	}
	if !innerFound {
		t.Errorf("内部类 %s 不在清单里: %+v", inner, list)
	}
}

// ---------------------------------------------------------------- 按路径匹配

// normalizeInsidePath 要吃掉"人复制来的路径"里那些花样，同时**不能猜**：
// 猜错了会替换到别的文件上去，比查不到危险得多。
func TestNormalizeInsidePath(t *testing.T) {
	cases := []struct{ in, want string }{
		// 正常包内路径：原样
		{"BOOT-INF/classes/com/example/Foo.class", "BOOT-INF/classes/com/example/Foo.class"},
		// 首尾空白
		{"  BOOT-INF/classes/Foo.class \r\n", "BOOT-INF/classes/Foo.class"},
		// 资源管理器"复制为路径"带的引号
		{`"BOOT-INF/classes/Foo.class"`, "BOOT-INF/classes/Foo.class"},
		// 反斜杠（解压软件 / Windows 习惯）
		{`BOOT-INF\classes\com\example\Foo.class`, "BOOT-INF/classes/com/example/Foo.class"},
		// 前导斜杠
		{"/BOOT-INF/classes/Foo.class", "BOOT-INF/classes/Foo.class"},
		// 带盘符和上级目录：从包根锚点切开
		{"D:/work/build/BOOT-INF/classes/Foo.class", "BOOT-INF/classes/Foo.class"},
		{`D:/work/build/BOOT-INF/classes/Foo.class`, "BOOT-INF/classes/Foo.class"},
		// 锚点在中间出现多次：取最靠前的那个，后面的原样保留
		{"x/BOOT-INF/classes/a/BOOT-INF/b.class", "BOOT-INF/classes/a/BOOT-INF/b.class"},
		// WEB-INF / META-INF 同样是包根锚点
		{"C:/tmp/x/WEB-INF/classes/Foo.class", "WEB-INF/classes/Foo.class"},
		{"C:/tmp/x/META-INF/MANIFEST.MF", "META-INF/MANIFEST.MF"},
		// 嵌套 jar 的定位串：`!/` 是分隔符，不能当上级目录切掉
		{"BOOT-INF/lib/a.jar!/com/x/Y.class", "BOOT-INF/lib/a.jar!/com/x/Y.class"},
		{`BOOT-INF\lib\a.jar!\com\x\Y.class`, "BOOT-INF/lib/a.jar!/com/x/Y.class"},
		// 锚点就在开头：不切
		{"BOOT-INF/classes/Foo.class", "BOOT-INF/classes/Foo.class"},
		// 空
		{"   ", ""},
		{`""`, ""},
		// 没有包根锚点的相对路径：原样交给后面去查（查不到就报 Missing，不瞎猜）
		{"com/example/Foo.class", "com/example/Foo.class"},
	}
	for _, c := range cases {
		if got := normalizeInsidePath(c.in); got != c.want {
			t.Errorf("normalizeInsidePath(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// ResolveAppPath：粘贴路径 → 定位到具体文件，能直接拿去替换。
func TestResolveAppPath(t *testing.T) {
	inner := buildZipBytes(t, [][2]string{
		{"gfmis/bgtex/CallMethodInvokerAbstract.class", "DEEP-OLD"},
	})
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/classes/", ""},
		{"BOOT-INF/classes/com/example/Foo.class", "OLD"},
		{"BOOT-INF/lib/", ""},
		{"BOOT-INF/lib/pay-common-business.jar", string(inner)},
	}), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	// 同一个文件，四种粘贴方式都应当定位到同一处
	for _, raw := range []string{
		"BOOT-INF/classes/com/example/Foo.class",
		`  "BOOT-INF\classes\com\example\Foo.class"  `,
		"D:/work/build/BOOT-INF/classes/com/example/Foo.class",
		"/BOOT-INF/classes/com/example/Foo.class",
	} {
		got, err := app.ResolveAppPath(raw)
		if err != nil {
			t.Fatalf("ResolveAppPath(%q) 报错: %v", raw, err)
		}
		if got.Missing {
			t.Errorf("ResolveAppPath(%q) 说不存在，但它就在包里", raw)
			continue
		}
		if got.Path != "BOOT-INF/classes/com/example/Foo.class" {
			t.Errorf("ResolveAppPath(%q).Path = %q", raw, got.Path)
		}
		if got.Name != "Foo.class" || got.Size <= 0 {
			t.Errorf("ResolveAppPath(%q) = %+v，名字/体积不对", raw, got)
		}
		if got.Placeholder {
			t.Errorf("ResolveAppPath(%q) 被当成占位节点了", raw)
		}
	}

	// 嵌套 jar：`!/` 定位串要能一路解析进去
	const deep = "BOOT-INF/lib/pay-common-business.jar!/gfmis/bgtex/CallMethodInvokerAbstract.class"
	got, err := app.ResolveAppPath(deep)
	if err != nil {
		t.Fatal(err)
	}
	if got.Missing || got.Path != deep {
		t.Fatalf("嵌套路径解析失败: %+v", got)
	}

	// 按路径定位后必须能直接替换 —— 这才是这条路存在的意义
	src := filepath.Join(dir, "Foo.class")
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReplaceNodeContent(treeSideApp, got.Path, src); err != nil {
		t.Fatalf("按路径替换失败: %v", err)
	}

	// 不存在的路径：回 Missing，不报错（手输错字是常态，报错只会让人以为工具坏了）
	miss, err := app.ResolveAppPath("BOOT-INF/classes/com/example/Nope.class")
	if err != nil {
		t.Fatalf("不存在的路径不该报错: %v", err)
	}
	if !miss.Missing {
		t.Errorf("不存在的路径应当 Missing，实际 %+v", miss)
	}
	if miss.Path == "" {
		t.Error("Missing 也要回填清洗后的路径，用户要对着它改")
	}

	// 空路径才算真错
	if _, err := app.ResolveAppPath("   "); err == nil {
		t.Error("空路径应当报错")
	}
}

// 按路径定位到占位节点（包里其实没有）：能定位，但要标出来，
// 否则用户会以为"找到了"，一替换等于凭空往应用包里加文件。
func TestResolveAppPathMappedPlaceholder(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/classes/", ""},
		{"BOOT-INF/classes/Keep.class", "KEEP"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	readme := "File Name: t\nPatch Author: a\nCreate Date: 2026/01/01 00:00\n\n" +
		"File Paths:\nBrand.class\tBOOT-INF/classes/new/deep/Brand.class\n\n" +
		"Auto Generated by JPatcher\n"
	patchZip := filepath.Join(dir, "patch.zip")
	writeZip(t, patchZip, map[string]string{
		"Brand.class": "BRAND",
		"README.txt":  readme,
	})

	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenPatchPack(patchZip); err != nil {
		t.Fatal(err)
	}

	const target = "BOOT-INF/classes/new/deep/Brand.class"
	got, err := app.ResolveAppPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Missing {
		t.Fatalf("%s 在树上是看得见的，不该判成 Missing", target)
	}
	if !got.Placeholder {
		t.Fatalf("%s 是补丁按 README 造出来的目标位，必须标成 Placeholder，实际 %+v", target, got)
	}
}

// 名字反查不到时也要带 srcPath —— 前端要靠它露出"粘贴路径"入口，
// 按路径定位完再拿它去替换；漏了这条，那条路就断在最后一步。
func TestDropLooseFileMissKeepsSrcPath(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/com/example/Foo.class", "OLD"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	// 拖进来的文件叫 Renamed.class —— 包里没有这个名字
	src := filepath.Join(dir, "Renamed.class")
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "matchNone" {
		t.Fatalf("动作 = %q，期望 matchNone", r.Action)
	}
	if r.SrcPath != src {
		t.Fatalf("SrcPath = %q，期望 %q（没有它，按路径匹配完无法替换）", r.SrcPath, src)
	}
	if r.SrcSize != 3 {
		t.Errorf("SrcSize = %d，期望 3（右侧「拖入的文件」条要显示体积）", r.SrcSize)
	}

	// 走一遍完整链路：按路径定位 → 替换 → 产物里是新内容
	got, err := app.ResolveAppPath("BOOT-INF/classes/com/example/Foo.class")
	if err != nil {
		t.Fatal(err)
	}
	if got.Missing {
		t.Fatal("前置条件不成立：Foo.class 应当在包里")
	}
	if _, err := app.ReplaceNodeContent(treeSideApp, got.Path, r.SrcPath); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if body := readZipEntryBytes(t, out, "BOOT-INF/classes/com/example/Foo.class"); string(body) != "NEW" {
		t.Errorf("产物内容 = %q，期望 NEW", body)
	}
}

// 一次拖两个文件：两个都要查、各出一条结果。
//
// 真实报障：拖两个"不知道路径"的文件进来，界面只认了第一个，第二个无声消失。
// 根因是 HandleDrop 只用了 paths[0]。
func TestDropLooseFilesMatchesEach(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/com/example/Foo.class", "OLD-FOO"},
		{"BOOT-INF/classes/com/example/Bar.class", "OLD-BAR"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	foo := filepath.Join(dir, "Foo.class")
	bar := filepath.Join(dir, "Bar.class")
	for _, p := range []string{foo, bar} {
		if err := os.WriteFile(p, []byte("NEW-"+filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r, err := app.HandleDrop(treeSidePatch, "", false, []string{foo, bar})
	if err != nil {
		t.Fatalf("拖两个文件报错: %v", err)
	}
	if r.Action != "matchMany" {
		t.Fatalf("动作 = %q，期望 matchMany", r.Action)
	}
	if len(r.Items) != 2 {
		t.Fatalf("结果条数 = %d，期望 2（每个文件一条）: %+v", len(r.Items), r.Items)
	}
	// 顺序与拖入顺序一致，且每条都带着自己的 srcPath —— 替换时要用它
	for i, want := range []struct{ name, src, path string }{
		{"Foo.class", foo, "BOOT-INF/classes/com/example/Foo.class"},
		{"Bar.class", bar, "BOOT-INF/classes/com/example/Bar.class"},
	} {
		it := r.Items[i]
		if it.FileName != want.name || it.SrcPath != want.src {
			t.Errorf("第 %d 条 = %q/%q，期望 %q/%q", i+1, it.FileName, it.SrcPath, want.name, want.src)
		}
		if len(it.Candidates) != 1 || it.Candidates[0].Path != want.path {
			t.Errorf("第 %d 条候选 = %+v，期望唯一一条 %q", i+1, it.Candidates, want.path)
		}
	}

	// 各自替换各自的：串了的话会把 Foo 的内容写进 Bar
	for _, it := range r.Items {
		if _, err := app.ReplaceNodeContent(treeSideApp, it.Candidates[0].Path, it.SrcPath); err != nil {
			t.Fatalf("替换 %s 失败: %v", it.FileName, err)
		}
	}
	out := filepath.Join(dir, "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	if body := string(readZipEntryBytes(t, out, "BOOT-INF/classes/com/example/Foo.class")); body != "NEW-Foo.class" {
		t.Errorf("Foo 内容 = %q，期望 NEW-Foo.class", body)
	}
	if body := string(readZipEntryBytes(t, out, "BOOT-INF/classes/com/example/Bar.class")); body != "NEW-Bar.class" {
		t.Errorf("Bar 内容 = %q，期望 NEW-Bar.class", body)
	}
}

// LocateNode 在**刚打开、从未展开过**的包上也要能给祖先链。
//
// 这是"拖入后自动定位"能不能真的展开的前提：前端 revealRow 拿 ancestors
// 逐层 toggle，缺一层就停在半路 —— 用户看到的就是"点了没反应 / 没有展开"。
func TestLocateNodeFreshPackDeepPath(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/gfmis/bgtex/payvoucher/service/IPayIncomeService.class", "X"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	const p = "BOOT-INF/classes/gfmis/bgtex/payvoucher/service/IPayIncomeService.class"
	got, err := app.LocateNode(treeSideApp, p)
	if err != nil {
		t.Fatalf("刚打开的包上定位失败: %v", err)
	}
	if got.Path != p {
		t.Errorf("Path = %q，期望 %q", got.Path, p)
	}
	// 祖先链要覆盖每一层目录（根不用，根那一行本来就看得见）。
	// 注意祖先用的是**不带尾斜杠**的路径形式，和前端 NodeView.path 一致 ——
	// 带不带斜杠必须和前端对上，否则 revealRow 逐层比对会停在第一层。
	want := []string{
		"BOOT-INF", "BOOT-INF/classes", "BOOT-INF/classes/gfmis",
		"BOOT-INF/classes/gfmis/bgtex", "BOOT-INF/classes/gfmis/bgtex/payvoucher",
		"BOOT-INF/classes/gfmis/bgtex/payvoucher/service",
	}
	if len(got.Ancestors) != len(want) {
		t.Fatalf("祖先链 = %+v，期望 %d 层", got.Ancestors, len(want))
	}
	for i := range want {
		if got.Ancestors[i] != want[i] {
			t.Errorf("第 %d 层祖先 = %q，期望 %q", i+1, got.Ancestors[i], want[i])
		}
	}
}

// 一命中一落空：两个文件都要出现在结果里，落空那条自己带扫描说明。
func TestDropLooseFilesPartialMiss(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/com/example/Foo.class", "OLD"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	foo := filepath.Join(dir, "Foo.class")
	ghost := filepath.Join(dir, "Ghost.class")
	for _, p := range []string{foo, ghost} {
		if err := os.WriteFile(p, []byte("X"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r, err := app.HandleDrop(treeSidePatch, "", false, []string{foo, ghost})
	if err != nil {
		t.Fatal(err)
	}
	// 只要有一个命中就回 matchMany（面板要列出来），落空的那条靠自己的空候选体现
	if r.Action != "matchMany" {
		t.Fatalf("动作 = %q，期望 matchMany", r.Action)
	}
	if len(r.Items) != 2 {
		t.Fatalf("结果条数 = %d，期望 2", len(r.Items))
	}
	if len(r.Items[0].Candidates) != 1 {
		t.Errorf("Foo 应当命中 1 处，实际 %+v", r.Items[0].Candidates)
	}
	if len(r.Items[1].Candidates) != 0 {
		t.Errorf("Ghost 不该有候选，实际 %+v", r.Items[1].Candidates)
	}
	if !strings.Contains(r.Items[1].ScanNote, "已检查") {
		t.Errorf("Ghost 的扫描说明 = %q，应当说明查了多少东西", r.Items[1].ScanNote)
	}
}

// 右键「复制路径」复制的是**目录**时，粘回来必须能定位。
//
// 复制路径拼的是节点名字（nodeDisplayPath），目录名天然不带尾斜杠；
// 而树里目录的 Path 带不带斜杠，取决于 zip 里有没有显式的目录条目。
// 两边写法不同，粘回去就查不到 —— 用户看到的是"应用包里没有这个路径"，
// 可上一秒他还在树上点着它。这条钉住两个方向都要能解析。
func TestResolveAppPathDirTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	// with-entry/ 有显式目录条目（Path 带斜杠）；
	// noentry/deep 没有目录条目，是补建出来的占位目录（Path 不带斜杠）。
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/classes/", ""},
		{"BOOT-INF/classes/with-entry/", ""},
		{"BOOT-INF/classes/with-entry/A.class", "A"},
		{"BOOT-INF/classes/noentry/deep/B.class", "B"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"BOOT-INF/classes/with-entry",    // 复制路径的形态：不带斜杠
		"BOOT-INF/classes/with-entry/",   // 手打或从解压软件复制：带斜杠
		"BOOT-INF/classes/noentry/deep",  // 占位目录本身不带斜杠，粘来的是不带斜杠
		"BOOT-INF/classes/noentry/deep/", // 反过来：树里不带，粘来的带
	}
	for _, c := range cases {
		got, err := app.ResolveAppPath(c)
		if err != nil {
			t.Fatalf("ResolveAppPath(%q) 报错: %v", c, err)
		}
		if got.Missing {
			t.Errorf("ResolveAppPath(%q) 说不存在，但它是树上的目录", c)
			continue
		}
		if !got.Dir {
			t.Errorf("ResolveAppPath(%q) 没标成目录，前端会拿它当替换目标", c)
		}
	}

	// 目录不能当替换目标：后端必须挡住，而不是写坏内容
	src := filepath.Join(dir, "X.class")
	if err := os.WriteFile(src, []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReplaceNodeContent(treeSideApp, "BOOT-INF/classes/with-entry", src); err == nil {
		t.Error("拿目录当替换目标应当报错")
	}

	// 文件本身不能被误判成目录
	got, err := app.ResolveAppPath("BOOT-INF/classes/with-entry/A.class")
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir || got.Missing || got.Name != "A.class" {
		t.Errorf("文件解析结果不对: %+v", got)
	}
}

// 往**嵌套 jar 里的目录行**拖文件新增。
//
// 这条路以前是断的：addOrReplaceChild 用 nodeBySide 精确匹配，而前端行上的
// 路径是嵌套定位串（`x.jar!/com/foo/`），永远查不到 → "树里找不到节点"。
// 而"按路径定位到目录、再提示把文件拖到那一行"正是要靠它走通，
// 所以顺手一起钉住。
func TestDropIntoNestedJarDirAdds(t *testing.T) {
	inner := buildZipBytes(t, [][2]string{
		{"gfmis/bgtex/factory/", ""},
		{"gfmis/bgtex/factory/Keep.class", "OLD"},
	})
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/", ""},
		{"BOOT-INF/lib/", ""},
		{"BOOT-INF/lib/pay-common.jar", string(inner)},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "expdp.txt")
	if err := os.WriteFile(src, []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}

	const row = "BOOT-INF/lib/pay-common.jar!/gfmis/bgtex/factory/"
	r, err := app.HandleDrop(treeSideApp, row, true, []string{src})
	if err != nil {
		t.Fatalf("拖到嵌套 jar 里的目录行失败: %v", err)
	}
	if r.Action != "add" {
		t.Fatalf("动作 = %q，期望 add", r.Action)
	}
	if r.Path != row+"expdp.txt" {
		t.Errorf("新增路径 = %q，期望 %q", r.Path, row+"expdp.txt")
	}

	// 新节点的 Path 要和兄弟节点同一坐标系（相对所在 jar），否则回查不到它
	if got, err := app.ResolveAppPath(r.Path); err != nil || got.Missing {
		t.Fatalf("新增后按定位串回查失败: %+v err=%v", got, err)
	}

	out := filepath.Join(dir, "out.jar")
	if _, err := app.RunPatch(out); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	innerOut := readZipEntryBytes(t, out, "BOOT-INF/lib/pay-common.jar")
	if got := string(readEntryFromBytes(t, innerOut, "gfmis/bgtex/factory/expdp.txt")); got != "HELLO" {
		t.Errorf("内层 jar 里 expdp.txt = %q，期望 HELLO", got)
	}
	if got := string(readEntryFromBytes(t, innerOut, "gfmis/bgtex/factory/Keep.class")); got != "OLD" {
		t.Errorf("原有 Keep.class 被弄坏了: %q", got)
	}
}

// fileMd5 直接算磁盘文件的 md5：内容一致、拿不到时是空串而不是 panic。
//
// 这是右侧「拖入的文件」那条记录显示 MD5 的最底层依赖 —— 前端只是把它
// 渲染出来，真正算的地方在这里。空串分支要单独钉：路径不存在 / 是目录时
// 必须安静退场，否则一次拖放里混进个读不了的文件就整条链路报错。
func TestFileMd5(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Foo.class")
	body := "MATCHED-BY-NAME"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	want := util.Md5([]byte(body))
	if got := fileMd5(p); got != want {
		t.Errorf("fileMd5 = %q，期望 %q", got, want)
	}

	// 大文件走的是流式读取，不能按小块切片算错 —— 拿 1 MB 的伪随机内容验证
	big := filepath.Join(dir, "big.bin")
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i * 7 % 251)
	}
	if err := os.WriteFile(big, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := fileMd5(big), util.Md5(buf); got != want {
		t.Errorf("1 MB 文件的 md5 = %q，期望 %q", got, want)
	}

	// 读不到就空串：不存在的路径、目录各来一下
	if got := fileMd5(filepath.Join(dir, "nope.class")); got != "" {
		t.Errorf("不存在的路径应返回空串，实际 %q", got)
	}
	if got := fileMd5(dir); got != "" {
		t.Errorf("目录应返回空串，实际 %q", got)
	}
}

// 忘了路径那条路（按名字反查）也要回报 SrcMd5：
// 用户拖进来的文件不在包里，界面右侧那条记录只有"名字+体积"时，
// 用户没法判断"手里这个和包里那个是不是同一份"；补上 MD5 就有了。
//
// 三条分支一起钉：命中多个（matchMany）、零命中（matchNone）、
// 以及"每个文件各一条"的 Items —— 一次拖两个文件时两条 MD5 必须各自对得上。
func TestDropLooseFilesReportSrcMd5(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/com/example/Foo.class", "OLD"},
		{"BOOT-INF/classes/com/example/Bar.class", "OLD-BAR"},
		{"a/Dup.class", "one"},
		{"b/Dup.class", "two"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	// —— 命中多个：Foo.class 在包里唯一，Dup.class 有两处 ——
	fooBody, dupBody, missBody := "NEW-FOO", "NEW-DUP", "NEW-MISS"
	foo := filepath.Join(dir, "Foo.class")
	dup := filepath.Join(dir, "Dup.class")
	miss := filepath.Join(dir, "Renamed.class")
	for p, body := range map[string]string{foo: fooBody, dup: dupBody, miss: missBody} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := app.HandleDrop(treeSidePatch, "", false, []string{foo, dup, miss})
	if err != nil {
		t.Fatalf("反查失败: %v", err)
	}
	if r.Action != "matchMany" || len(r.Items) != 3 {
		t.Fatalf("动作/条数 = %q/%d，期望 matchMany/3", r.Action, len(r.Items))
	}

	// 顶层字段描述"第一个文件"，Items 里逐个文件各一份 —— 两边都不能漏
	if r.SrcMd5 != util.Md5([]byte(fooBody)) {
		t.Errorf("顶层 SrcMd5 = %q，期望第一个文件 Foo.class 的 %q",
			r.SrcMd5, util.Md5([]byte(fooBody)))
	}
	wants := []string{util.Md5([]byte(fooBody)), util.Md5([]byte(dupBody)), util.Md5([]byte(missBody))}
	sizes := []int64{int64(len(fooBody)), int64(len(dupBody)), int64(len(missBody))}
	srcs := []string{foo, dup, miss}
	for i, it := range r.Items {
		if it.SrcMd5 != wants[i] {
			t.Errorf("Items[%d]（%s）SrcMd5 = %q，期望 %q", i, it.FileName, it.SrcMd5, wants[i])
		}
		// MD5 和体积必须来自同一个文件，错位的话前端并排显示就全乱了
		if it.SrcSize != sizes[i] {
			t.Errorf("Items[%d]（%s）SrcSize = %d，期望 %d", i, it.FileName, it.SrcSize, sizes[i])
		}
		if it.SrcPath != srcs[i] {
			t.Errorf("Items[%d] SrcPath = %q，期望 %q", i, it.SrcPath, srcs[i])
		}
	}
	// 零命中的那个（Renamed.class）也要带 MD5：它才是"最需要比对"的那个
	if got := r.Items[2]; len(got.Candidates) != 0 || got.SrcMd5 == "" {
		t.Errorf("零命中项 = %+v，期望无候选但有 MD5", got)
	}
}

// 落到具体文件行上替换、以及往目录里新增，同样要回报 SrcMd5。
// 这两条路的右侧记录与"反查"共用同一套渲染（app.js setDropFileMeta），
// 少任一条，那一类拖完 MD5 栏就是空的。
func TestDropRowAndDirReportSrcMd5(t *testing.T) {
	dir := t.TempDir()
	appJar := filepath.Join(dir, "app.jar")
	if err := os.WriteFile(appJar, buildZipBytes(t, [][2]string{
		{"BOOT-INF/classes/com/example/Foo.class", "OLD"},
		{"BOOT-INF/classes/com/example/keep.txt", "KEEP"},
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	if _, err := app.OpenAppPack(appJar); err != nil {
		t.Fatal(err)
	}

	// —— 落在文件行上：替换 ——
	body := "REPLACED-CONTENT"
	src := filepath.Join(dir, "Foo.class")
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	target := "BOOT-INF/classes/com/example/Foo.class"
	r, err := app.HandleDrop(treeSideApp, target, false, []string{src})
	if err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	if r.Action != "replace" {
		t.Fatalf("动作 = %q，期望 replace", r.Action)
	}
	if r.SrcMd5 != util.Md5([]byte(body)) {
		t.Errorf("replace 的 SrcMd5 = %q，期望 %q", r.SrcMd5, util.Md5([]byte(body)))
	}
	if r.SrcSize != int64(len(body)) {
		t.Errorf("replace 的 SrcSize = %d，期望 %d", r.SrcSize, len(body))
	}

	// —— 往目录里新增 ——
	addBody := "ADDED"
	add := filepath.Join(dir, "expdp.txt")
	if err := os.WriteFile(add, []byte(addBody), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = app.HandleDrop(treeSideApp, "BOOT-INF/classes/com/example", true, []string{add})
	if err != nil {
		t.Fatalf("新增失败: %v", err)
	}
	if r.Action != "add" {
		t.Fatalf("动作 = %q，期望 add", r.Action)
	}
	if r.SrcMd5 != util.Md5([]byte(addBody)) {
		t.Errorf("add 的 SrcMd5 = %q，期望 %q", r.SrcMd5, util.Md5([]byte(addBody)))
	}
}
