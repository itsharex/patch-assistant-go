package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ComparePacks 绑定是直通 internal/service 的薄封装 —— 这里只钉两件事：
// 错误能原样冒出去（前端 toast 要拿到中文提示）、成功时字段不回丢。
// 真正的比对规则由 internal/service/jarcompare_test.go 覆盖。
func TestAppComparePacks_Passthrough(t *testing.T) {
	app := NewApp()

	if _, err := app.ComparePacks("", ""); err == nil {
		t.Fatal("空路径应报错")
	}
	same := t.TempDir()
	if _, err := app.ComparePacks(same, same); err == nil {
		t.Fatal("同一条路径应报错")
	}
	notZip := filepath.Join(t.TempDir(), "x.jar")
	if err := writeFile(notZip, "nope"); err != nil {
		t.Fatal(err)
	}
	notZip2 := filepath.Join(t.TempDir(), "y.jar")
	if err := writeFile(notZip2, "nope2"); err != nil {
		t.Fatal(err)
	}
	_, err := app.ComparePacks(notZip, notZip2)
	if err == nil || !strings.Contains(err.Error(), "不是有效的 jar/zip") {
		t.Fatalf("err=%v，期望\"不是有效的 jar/zip\"", err)
	}
}

func TestCompareReportDefaultName(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"app-1.0.jar", "app-2.0.jar", "app-1.0_vs_app-2.0-对比报告.txt"},
		{"", "b.zip", "包A_vs_b-对比报告.txt"},
		{"a.jar", "", "a_vs_包B-对比报告.txt"},
		{"  ", "  ", "包A_vs_包B-对比报告.txt"},
		{`D:\dir\app.jar`, "b.jar", "app_vs_b-对比报告.txt"},
	}
	for _, c := range cases {
		if got := compareReportDefaultName(c.a, c.b); got != c.want {
			t.Errorf("compareReportDefaultName(%q, %q) = %q，期望 %q", c.a, c.b, got, c.want)
		}
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// 拖入对比弹窗：收不收、哪个是 A 哪个是 B 由 Go 判（前端只管落在哪个槽上）。
func TestHandleCompareDrop(t *testing.T) {
	app := NewApp()

	// 1 个文件：只给 A，B 留空（前端按落点决定填哪个槽）
	r, err := app.HandleCompareDrop([]string{`D:\a\old.jar`})
	if err != nil {
		t.Fatal(err)
	}
	if r.A != `D:\a\old.jar` || r.B != "" {
		t.Fatalf("A=%q B=%q，期望只填 A", r.A, r.B)
	}

	// 2 个文件：直接按 A、B 分配（一次拖两个包时落点不重要）
	r, err = app.HandleCompareDrop([]string{"a.jar", "b.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if r.A != "a.jar" || r.B != "b.zip" {
		t.Fatalf("A=%q B=%q，期望按顺序分配", r.A, r.B)
	}

	// 空列表：挡掉（Java 那边越界会抛，Go 里 panic 会终止进程）
	if _, err := app.HandleCompareDrop(nil); err == nil {
		t.Fatal("空列表应报错")
	}
	// 3 个：槽位只有两个，明确报错而不是静默丢掉
	if _, err := app.HandleCompareDrop([]string{"a.jar", "b.jar", "c.jar"}); err == nil {
		t.Fatal("超过 2 个应报错")
	}
	// 空路径：挡掉
	if _, err := app.HandleCompareDrop([]string{"  "}); err == nil {
		t.Fatal("空路径应报错")
	}
	// 普通文件（非 jar/zip）：**接受** —— 现在支持对比文件，收不收由前端
	// 按扩展名分流（两个包走 ComparePacks、两个文件走 CompareFiles），
	// 拖入这一步不再拦类型。
	r, err = app.HandleCompareDrop([]string{`D:\x\notes.txt`})
	if err != nil {
		t.Fatalf("普通文件应被接受，实际 err=%v", err)
	}
	if r.A != `D:\x\notes.txt` || r.B != "" {
		t.Fatalf("A=%q B=%q，期望只填 A", r.A, r.B)
	}
	// 一次拖一个包 + 一个普通文件：混着比无从下手，拖入层就挡掉
	if _, err := app.HandleCompareDrop([]string{"ok.jar", "notes.txt"}); err == nil {
		t.Fatal("包 + 普通文件混拖应报错")
	}
	// 两个普通文件：同类，接受
	if _, err := app.HandleCompareDrop([]string{"a.txt", "b.txt"}); err != nil {
		t.Fatalf("两个普通文件应被接受，实际 err=%v", err)
	}
	// 目录：明确拒绝（目录不是"要比对的东西"）
	dir := t.TempDir()
	_, err = app.HandleCompareDrop([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("err=%v，期望提示不接受目录", err)
	}
}

// ---------------------------------------------------------------------------
// 单文件对比绑定（CompareFiles）
// ---------------------------------------------------------------------------

// 与 TestAppComparePacks_Passthrough 同理：薄封装，钉"错误冒出去 + 成功字段不丢"。
func TestAppCompareFiles_Passthrough(t *testing.T) {
	app := NewApp()

	if _, err := app.CompareFiles("", ""); err == nil {
		t.Fatal("空路径应报错")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := writeFile(a, "same\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(b, "same\n"); err != nil {
		t.Fatal(err)
	}
	res, err := app.CompareFiles(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Same || res.NameA != "a.txt" || res.NameB != "b.txt" {
		t.Fatalf("Same=%v NameA=%q NameB=%q", res.Same, res.NameA, res.NameB)
	}
	// 来源要立起来 —— 「查看差异」用的就是这两个（与包对比共用同一对字段）
	if app.compareA != a || app.compareB != b {
		t.Fatalf("compareA=%q compareB=%q，期望写进来源", app.compareA, app.compareB)
	}
}

// ---------------------------------------------------------------------------
// 打开"两个文件的差异"（对应文件对比结果里的「查看差异」）
// ---------------------------------------------------------------------------

func TestOpenFileCompare_Split(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	a := filepath.Join(dir, "old.txt")
	b := filepath.Join(dir, "new.txt")
	if err := writeFile(a, "v = 1;\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(b, "v = 2;\n"); err != nil {
		t.Fatal(err)
	}

	v, err := app.OpenFileCompare(FileCompareOptions{Left: a, Right: b})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeSplit {
		t.Fatalf("mode=%q，期望 %q（两侧都有就并排比）", v.Mode, contentModeSplit)
	}
	if !v.DiffMode {
		t.Error("两侧都有时 DiffMode 应为 true")
	}
	if v.Left == nil || v.Right == nil {
		t.Fatal("双栏两侧都要有内容")
	}
	// 左=Left、右=Right：与包对比同口径（哪边是哪个文件是唯一依据）
	if got := paneText(v.Left.Lines); !strings.Contains(got, "v = 1;") {
		t.Errorf("左栏应当是 Left 的内容，实际 %q", got)
	}
	if got := paneText(v.Right.Lines); !strings.Contains(got, "v = 2;") {
		t.Errorf("右栏应当是 Right 的内容，实际 %q", got)
	}
	// 栏标签就是磁盘路径（统一 diff 的 ---/+++ 行就是这两个）
	if v.Left.Path != a || v.Right.Path != b {
		t.Errorf("栏标签=%q / %q，期望 %q / %q", v.Left.Path, v.Right.Path, a, b)
	}
	// 标题用两个文件名区分两侧
	if !strings.Contains(v.Title, "old.txt") || !strings.Contains(v.Title, "new.txt") {
		t.Errorf("标题应当带上两个文件名，实际 %q", v.Title)
	}
}

// 没有解析器的类型：走 unsupported 并给一句带两侧大小的说明（不抛错）。
func TestOpenFileCompare_NoParser(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := writeFile(a, "PNGDATA"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(b, "PNGDATA2"); err != nil {
		t.Fatal(err)
	}

	v, err := app.OpenFileCompare(FileCompareOptions{Left: a, Right: b})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeUnsupported {
		t.Fatalf("mode=%q，期望 %q", v.Mode, contentModeUnsupported)
	}
	if !strings.Contains(v.Message, "字节") {
		t.Errorf("说明文案应带上两侧大小，实际 %q", v.Message)
	}
}

// 空路径挡掉（前端不该这么调，真调到了要给明确错误而不是读空文件）。
func TestOpenFileCompare_EmptyPaths(t *testing.T) {
	app := NewApp()
	if _, err := app.OpenFileCompare(FileCompareOptions{}); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := app.OpenFileCompare(FileCompareOptions{Left: "a.txt"}); err == nil {
		t.Fatal("缺 Right 应报错")
	}
}

// ---------------------------------------------------------------------------
// 展开嵌套 jar（OpenNestedCompare / OpenCompareContent 的 chain）
//
// 造法：先写两个内层 jar（内容不同），把它们各自作为条目塞进外层 jar，
// 再对外层跑 ComparePacks —— 模拟 `BOOT-INF/lib/xxx.jar` 那种结构。
// ---------------------------------------------------------------------------

// nestedPair 造两个外层包，各自含一个内容不同的内层 jar（BOOT-INF/lib/x.jar）。
func nestedPair(t *testing.T, innerA, innerB map[string]string) (app *App, entry string) {
	t.Helper()
	dir := t.TempDir()
	ia := filepath.Join(dir, "inner-a.jar")
	ib := filepath.Join(dir, "inner-b.jar")
	writeZip(t, ia, innerA)
	writeZip(t, ib, innerB)
	ba, err := os.ReadFile(ia)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := os.ReadFile(ib)
	if err != nil {
		t.Fatal(err)
	}
	entry = "BOOT-INF/lib/x.jar"
	outerA := filepath.Join(dir, "old.jar")
	outerB := filepath.Join(dir, "new.jar")
	writeZip(t, outerA, map[string]string{entry: string(ba)})
	writeZip(t, outerB, map[string]string{entry: string(bb)})
	app = NewApp()
	if _, err := app.ComparePacks(outerA, outerB); err != nil {
		t.Fatalf("外层对比失败: %v", err)
	}
	return app, entry
}

func TestOpenNestedCompare(t *testing.T) {
	app, entry := nestedPair(t,
		map[string]string{"com/foo/A.class": "old", "keep.txt": "k"},
		map[string]string{"com/foo/A.class": "new", "keep.txt": "k"})

	// 展开内层包：对它逐条目比（顶层只把它当一个 blob，见"不递归展开"的口径）
	res, err := app.OpenNestedCompare([]string{entry})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "pack" {
		t.Fatalf("Kind=%q，期望 pack", res.Kind)
	}
	if len(res.Changed) != 1 || res.Changed[0].Path != "com/foo/A.class" {
		t.Fatalf("展开后 Changed=%+v，期望只有 com/foo/A.class", res.Changed)
	}
	if res.Same != 1 {
		t.Fatalf("Same=%d，期望 1（keep.txt 相同）", res.Same)
	}
	// 回显名是嵌套 jar 名
	if res.NameA != "x.jar" || res.NameB != "x.jar" {
		t.Fatalf("NameA=%q NameB=%q", res.NameA, res.NameB)
	}
}

func TestOpenNestedCompare_Guards(t *testing.T) {
	app, _ := nestedPair(t,
		map[string]string{"A": "1"}, map[string]string{"A": "2"})

	// 空链：报错
	if _, err := app.OpenNestedCompare(nil); err == nil {
		t.Fatal("空链应报错")
	}
	// 路径链里没有的条目：另一侧（或本侧）没有 → 报错
	if _, err := app.OpenNestedCompare([]string{"BOOT-INF/lib/nope.jar"}); err == nil {
		t.Fatal("不存在的嵌套包应报错")
	}

	// 只有一侧有这个嵌套包：没有可比对的另一侧
	dir := t.TempDir()
	inner := filepath.Join(dir, "only.jar")
	writeZip(t, inner, map[string]string{"A": "1"})
	bi, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}
	outerA := filepath.Join(dir, "a.jar")
	outerB := filepath.Join(dir, "b.jar")
	writeZip(t, outerA, map[string]string{"lib/only.jar": string(bi)})
	writeZip(t, outerB, map[string]string{"other.txt": "x"})
	app2 := NewApp()
	if _, err := app2.ComparePacks(outerA, outerB); err != nil {
		t.Fatal(err)
	}
	if _, err := app2.OpenNestedCompare([]string{"lib/only.jar"}); err == nil {
		t.Fatal("只有一侧有嵌套包时应报错")
	}
}

// 展开后点内层包里的条目：OpenCompareContent 必须按 chain 读**内层包**，
// 而不是外层包（顶层根本没有 com/foo/A.txt）。
func TestOpenCompareContent_NestedChain(t *testing.T) {
	app, entry := nestedPair(t,
		map[string]string{"com/foo/A.txt": "v = 1;\n"},
		map[string]string{"com/foo/A.txt": "v = 2;\n"})

	v, err := app.OpenCompareContent(CompareContentOptions{
		Path:  "com/foo/A.txt",
		Chain: []string{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeSplit {
		t.Fatalf("mode=%q，期望 %q", v.Mode, contentModeSplit)
	}
	if got := paneText(v.Left.Lines); !strings.Contains(got, "v = 1;") {
		t.Errorf("左栏应当是内层包 A 的内容，实际 %q", got)
	}
	if got := paneText(v.Right.Lines); !strings.Contains(got, "v = 2;") {
		t.Errorf("右栏应当是内层包 B 的内容，实际 %q", got)
	}

	// 不带 chain：顶层没有这个条目 → 报错（证明 chain 真的在起作用）
	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "com/foo/A.txt"}); err == nil {
		t.Fatal("不带 chain 时顶层没有该条目，应报错")
	}
}

// ---------------------------------------------------------------------------
// 打开"对比结果里某个文件"（对应前端点明细行）
//
// 来源必须是**上次对比的那两个包**（App.compareA / compareB），所以这些用例
// 都先跑一次 ComparePacks 把来源立起来，再调 OpenCompareContent。
// ---------------------------------------------------------------------------

// comparePair 造两个包：A 有 old-only.txt / com/foo/Bar.txt，B 有 new-only.txt，
// 两边共有的 com/foo/Bar.txt 内容不同（好验"左 A 右 B"）。
func comparePair(t *testing.T) (app *App, pathA, pathB string) {
	t.Helper()
	dir := t.TempDir()
	pathA = filepath.Join(dir, "old-app.jar")
	pathB = filepath.Join(dir, "new-app.jar")
	writeZip(t, pathA, map[string]string{
		"com/foo/Bar.txt": "v = 1;\n",
		"old-only.txt":    "only in A\n",
	})
	writeZip(t, pathB, map[string]string{
		"com/foo/Bar.txt": "v = 2;\n",
		"new-only.txt":    "only in B\n",
	})
	app = NewApp()
	if _, err := app.ComparePacks(pathA, pathB); err != nil {
		t.Fatalf("对比失败: %v", err)
	}
	return app, pathA, pathB
}

func TestOpenCompareContent_Split(t *testing.T) {
	app, _, _ := comparePair(t)

	v, err := app.OpenCompareContent(CompareContentOptions{Path: "com/foo/Bar.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeSplit {
		t.Fatalf("mode=%q，期望 %q（两侧都有就并排比）", v.Mode, contentModeSplit)
	}
	if !v.DiffMode {
		t.Error("两侧都有时 DiffMode 应为 true（右键菜单里才有\"差异模式\"）")
	}
	if v.Left == nil || v.Right == nil {
		t.Fatal("双栏两侧都要有内容")
	}
	// 左=包 A、右=包 B：这是"哪边是哪个包"的唯一依据，错了整个对比就没意义
	if got := paneText(v.Left.Lines); !strings.Contains(got, "v = 1;") {
		t.Errorf("左栏应当是包 A 的内容，实际 %q", got)
	}
	if got := paneText(v.Right.Lines); !strings.Contains(got, "v = 2;") {
		t.Errorf("右栏应当是包 B 的内容，实际 %q", got)
	}
	// 栏标签带包名（统一 diff 的 ---/+++ 行就是这两个名字）
	if v.Left.Path != "old-app.jar!com/foo/Bar.txt" || v.Right.Path != "new-app.jar!com/foo/Bar.txt" {
		t.Errorf("栏标签=%q / %q", v.Left.Path, v.Right.Path)
	}
	// 标题用包名区分两侧 —— 两边条目名一样，光看条目名分不出谁是谁
	if !strings.Contains(v.Title, "old-app.jar") || !strings.Contains(v.Title, "new-app.jar") {
		t.Errorf("标题应当带上两个包名，实际 %q", v.Title)
	}
	// 差异标记真的打上了（与树预览共用同一套渲染，见 renderContentView）
	var marks []string
	for _, l := range v.Left.Lines {
		if l.Mark != "" {
			marks = append(marks, l.Mark)
		}
	}
	if len(marks) == 0 {
		t.Error("内容不同的一行应当带上差异标记")
	}
	// 差异块也要透出（前端的"上一个/下一个差异"靠它跳转与计数）
	if len(v.Blocks) == 0 {
		t.Fatal("双栏预览应当透出差异块（Blocks），否则导航无从跳起")
	}
	for i, b := range v.Blocks {
		if b.LeftStart < 0 && b.RightStart < 0 {
			t.Errorf("第 %d 个差异块两侧都没有行：%+v", i, b)
		}
		if b.LeftStart >= 0 && b.LeftStart+b.LeftCount > len(v.Left.Lines) {
			t.Errorf("第 %d 个差异块的左栏范围越界：%+v（左栏共 %d 行）",
				i, b, len(v.Left.Lines))
		}
		if b.RightStart >= 0 && b.RightStart+b.RightCount > len(v.Right.Lines) {
			t.Errorf("第 %d 个差异块的右栏范围越界：%+v（右栏共 %d 行）",
				i, b, len(v.Right.Lines))
		}
	}
}

// 只有一侧有的条目 -> 单栏，标题标明是哪个包。
func TestOpenCompareContent_OnlyOneSide(t *testing.T) {
	app, _, _ := comparePair(t)

	// 仅 A 有
	v, err := app.OpenCompareContent(CompareContentOptions{Path: "old-only.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeSingle || v.DiffMode || v.Right != nil {
		t.Fatalf("仅 A 有应当是单栏：mode=%q DiffMode=%v Right=%v", v.Mode, v.DiffMode, v.Right)
	}
	if !strings.Contains(v.Title, "仅 old-app.jar 有") {
		t.Errorf("标题应当说明只有哪个包有，实际 %q", v.Title)
	}
	if v.Left.Path != "old-app.jar!old-only.txt" {
		t.Errorf("栏标签=%q", v.Left.Path)
	}

	// 仅 B 有：包 A 没有这个文件，左栏落到包 B（仍是单栏）
	v, err = app.OpenCompareContent(CompareContentOptions{Path: "new-only.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeSingle || v.DiffMode {
		t.Fatalf("仅 B 有应当是单栏：mode=%q DiffMode=%v", v.Mode, v.DiffMode)
	}
	if !strings.Contains(v.Title, "仅 new-app.jar 有") {
		t.Errorf("标题应当说明只有哪个包有，实际 %q", v.Title)
	}
	if v.Left.Path != "new-app.jar!new-only.txt" {
		t.Errorf("仅 B 有时左栏应当落在包 B，实际 %q", v.Left.Path)
	}
	if got := paneText(v.Left.Lines); !strings.Contains(got, "only in B") {
		t.Errorf("单栏内容应当是包 B 的，实际 %q", got)
	}
}

// 差异模式（单栏 unified diff）由 diffModel 决定，且与树预览共用会话开关。
func TestOpenCompareContent_DiffModel(t *testing.T) {
	app, _, _ := comparePair(t)

	yes := true
	v, err := app.OpenCompareContent(CompareContentOptions{Path: "com/foo/Bar.txt", DiffModel: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != contentModeDiff {
		t.Fatalf("diffModel=true 时 mode=%q，期望 %q", v.Mode, contentModeDiff)
	}
	if v.Right != nil {
		t.Error("单栏差异不该有右栏")
	}
	// 会话开关被记住（下一次不带 DiffModel 也沿用）—— 与树预览同一个字段
	if !app.contentDiffModel {
		t.Error("diffModel 应当被记进会话（对应原版 Configuration.content.diffModel）")
	}
	v2, err := app.OpenCompareContent(CompareContentOptions{Path: "com/foo/Bar.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Mode != contentModeDiff {
		t.Fatalf("不带 DiffModel 时应当沿用会话里的 true，实际 mode=%q", v2.Mode)
	}
}

// 没有内容解析器的类型：给 unsupported + 一句带两侧大小的说明，
// 而不是报错（这是可预期的类型限制，不是程序出错）。
func TestOpenCompareContent_Unsupported(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.jar")
	pathB := filepath.Join(dir, "b.jar")
	writeZip(t, pathA, map[string]string{"img/logo.png": "AAAA"})
	writeZip(t, pathB, map[string]string{"img/logo.png": "BBBBBB"})

	app := NewApp()
	if _, err := app.ComparePacks(pathA, pathB); err != nil {
		t.Fatal(err)
	}
	v, err := app.OpenCompareContent(CompareContentOptions{Path: "img/logo.png"})
	if err != nil {
		t.Fatalf("没有解析器不该返回 error，实际 %v", err)
	}
	if v.Mode != contentModeUnsupported {
		t.Fatalf("mode=%q，期望 %q", v.Mode, contentModeUnsupported)
	}
	if !strings.Contains(v.Message, "无法预览") {
		t.Errorf("说明文案应当讲清看不了，实际 %q", v.Message)
	}
	// 看不了内容，至少把两边大小摆出来（4 / 6 字节）
	if !strings.Contains(v.Message, "4 字节") || !strings.Contains(v.Message, "6 字节") {
		t.Errorf("说明文案应当带上两侧大小，实际 %q", v.Message)
	}
}

// 参数与前置状态不对时要报错（前端 toast 要拿到中文提示）。
func TestOpenCompareContent_Errors(t *testing.T) {
	// 还没对比过任何两个包
	app := NewApp()
	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "a.txt"}); err == nil {
		t.Error("没有对比过任何两个包时应当报错")
	}

	app, _, _ = comparePair(t)
	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "  "}); err == nil {
		t.Error("空路径应当报错")
	}
	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "nope.txt"}); err == nil {
		t.Error("两个包里都没有这个文件时应当报错")
	}
}

// .class 走反编译，且**连同内部类**一起喂给 fernflower；结果按会话缓存 ——
// 切换差异模式 / 编码都会重新取一次文本，没缓存的话每切一次都要重新起 JVM。
func TestOpenCompareContent_ClassDecompileAndCache(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.jar")
	pathB := filepath.Join(dir, "b.jar")
	writeZip(t, pathA, map[string]string{"C.class": "aaa", "C$I.class": "inn-a"})
	writeZip(t, pathB, map[string]string{"C.class": "bbb", "C$I.class": "inn-b"})

	app := NewApp()
	if _, err := app.ComparePacks(pathA, pathB); err != nil {
		t.Fatal(err)
	}

	var inners []int
	orig := decompileRun
	decompileRun = func(main []byte, list [][]byte) (string, error) {
		inners = append(inners, len(list))
		return "class C { /* " + string(main) + " */ }\n", nil
	}
	defer func() { decompileRun = orig }()

	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "C.class"}); err != nil {
		t.Fatal(err)
	}
	if len(inners) != 2 {
		t.Fatalf("两侧各要反编译一次，实际 %d 次", len(inners))
	}
	if inners[0] != 1 || inners[1] != 1 {
		t.Errorf("内部类个数=%v，期望两侧各带上 1 个内部类", inners)
	}

	// 再来一次（模拟切差异模式）：应当全部命中缓存、不再起 JVM
	if _, err := app.OpenCompareContent(CompareContentOptions{Path: "C.class"}); err != nil {
		t.Fatal(err)
	}
	if len(inners) != 2 {
		t.Fatalf("第二次应当命中缓存，实际累计反编译 %d 次", len(inners))
	}
	if len(app.decompileCache) != 2 {
		t.Errorf("缓存条目=%d，期望 2（两侧各一条）", len(app.decompileCache))
	}
}

// 对比弹窗的扩展名判定：**不区分大小写**（与主窗口那两处刻意不同 ——
// 那两处的大小写敏感是上游 Java 的保真点，这里是新增功能，按用户直觉走）。
func TestIsComparePackPath(t *testing.T) {
	yes := []string{"a.jar", "b.zip", "C:\\x\\APP.JAR", "C:\\x\\Pkg.Zip", "  spaced.jar  "}
	no := []string{"", "   ", "a.txt", "a.jar.txt", "jar", "a.jarx", "a.zip2", "a.ear", "a.war"}
	for _, p := range yes {
		if !isComparePackPath(p) {
			t.Errorf("isComparePackPath(%q) = false，期望 true", p)
		}
	}
	for _, p := range no {
		if isComparePackPath(p) {
			t.Errorf("isComparePackPath(%q) = true，期望 false", p)
		}
	}
}
