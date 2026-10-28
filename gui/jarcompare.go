package main

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/content"
	"cn.cpoet.patch.assistant/internal/service"
	"cn.cpoet.patch.assistant/internal/util"
)

// ---------------------------------------------------------------------------
// 包内容对比（对应前端「对比」弹窗）
//
// 与主流程的关系：这里是**独立入口** —— 用户手里有两个 jar/zip，只想问
// "里面哪些文件不同"，不打算打补丁。所以它不读会话里的两棵树、不跑匹配，
// 选完文件点"开始对比"直接出结果。比对规则全在 internal/service.ComparePacks
// （与 CLI 同级共用），GUI 只做选文件、翻译、落盘/剪贴板这四件杂事。
// ---------------------------------------------------------------------------

// PickCompareJar 为「对比」弹窗选文件。
//
// role 取 "a" / "b"，只用来区分对话框标题 —— 两个槽位长得一样，
// 标题是用户唯一能分清"我现在在给哪一边选文件"的线索。
//
// 过滤器给两项：先按 jar/zip 方便挑包，再给"所有文件"以便挑普通文件
// （「对比文件」是移植版新增能力，见 CompareFiles）。
func (a *App) PickCompareJar(role string) (string, error) {
	title := "选择要比对的 A（文件或包）"
	if role == "b" {
		title = "选择要比对的 B（文件或包）"
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		Filters: []runtime.FileFilter{
			{DisplayName: "Java 包 (*.jar;*.zip)", Pattern: "*.jar;*.zip"},
			{DisplayName: "所有文件 (*.*)", Pattern: "*.*"},
		},
	})
}

// ComparePacks 对比两个 jar/zip 的文件内容差异。
//
// 直通 internal/service.ComparePacks —— 比对规则不在这里重复。
// 需要的 UI 元信息（文件名、纯文本报告）service 侧已经算好。
func (a *App) ComparePacks(pathA, pathB string) (*service.ComparePackResult, error) {
	res, err := service.ComparePacks(pathA, pathB)
	if err != nil {
		return nil, err
	}
	// 记下这次比的是哪两个包 —— 结果里每一行"点开看内容"都从这两个包里读
	// （见 OpenCompareContent）。用 res 里规整过（trim 过）的路径，
	// 与屏幕上显示的那一份保持一致。
	a.compareA, a.compareB = res.PathA, res.PathB
	return res, nil
}

// CompareFiles 对比两个**普通文件**的内容差异（md5）。
//
// 直通 internal/service.CompareFiles —— 比对规则不在这里重复。
// 与 ComparePacks 是并列的两条路：两个都是包走 ComparePacks，两个都是
// 普通文件走这里；由前端按扩展名分流（见 app.js 的 isArchivePath）。
func (a *App) CompareFiles(pathA, pathB string) (*service.CompareFileResult, error) {
	res, err := service.CompareFiles(pathA, pathB)
	if err != nil {
		return nil, err
	}
	// 记下这次比的是哪两个文件 —— 结果里「查看差异」从这两个文件读
	// （见 OpenFileCompare）。与包对比共用同一对字段，互不干扰：
	// 每个结果视图里的按钮只调各自那条路。
	a.compareA, a.compareB = res.PathA, res.PathB
	return res, nil
}

// CopyCompareReport 把对比报告写进剪贴板。
//
// 做法与「节点绑定信息」弹窗的 CopyMappedInfo 一致：剪贴板统一走 Go 侧
// runtime.ClipboardSetText，不依赖 WebView 的 navigator.clipboard（那个
// 在 WebView2 里要额外权限，且非用户手势触发的调用会被拒）。
//
// text 由前端原样传回 service 算好的报告文本 —— 这里不存状态，
// 拷的就是用户屏幕上看到的那一份。
func (a *App) CopyCompareReport(text string) error {
	return runtime.ClipboardSetText(a.ctx, text)
}

// SaveCompareReport 把对比报告保存为文本文件。
//
// 默认文件名由 Go 侧按两个包的文件名拼（见 compareReportDefaultName），
// 用户可以在对话框里改。文本为空（没有差异）也允许保存 —— 那也是结论。
//
// 返回是否真的落盘了：用户在对话框里取消时返回 false，让前端能区分
// "已保存"与"已取消"（SaveMappedInfoAs 那个老绑定取消也报已保存，
// 这里不照抄那个毛病）。
func (a *App) SaveCompareReport(nameA, nameB, text string) (bool, error) {
	out, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存对比报告",
		DefaultFilename: compareReportDefaultName(nameA, nameB),
		Filters: []runtime.FileFilter{
			{DisplayName: "文本文件 (*.txt)", Pattern: "*.txt"},
		},
	})
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		return false, nil // 用户取消
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return false, fmt.Errorf("写出文件失败: %w", err)
	}
	return true, nil
}

// compareReportDefaultName 拼默认报告文件名："<A名去扩展名>_vs_<B名去扩展名>-对比报告.txt"。
func compareReportDefaultName(nameA, nameB string) string {
	strip := func(n string) string {
		e := filepath.Base(strings.TrimSpace(n))
		return strings.TrimSuffix(e, filepath.Ext(e))
	}
	a, b := strip(nameA), strip(nameB)
	if a == "" {
		a = "包A"
	}
	if b == "" {
		b = "包B"
	}
	return a + "_vs_" + b + "-对比报告.txt"
}

// ---------------------------------------------------------------------------
// 把包拖进「对比」弹窗
//
// 与主窗口的拖放同一套分工（见 HandleFileDrop）：**前端只回答"落在哪个槽上"**
// （几何），**收不收、哪个是 A 哪个是 B 由这里判**（规则，可单测）。
// 前端从 Wails 的 OnFileDrop 回调拿到的是磁盘绝对路径，正好是这里要的。
// ---------------------------------------------------------------------------

// CompareDropFiles 拖入后各槽位该填什么。
type CompareDropFiles struct {
	// A 包 A 槽的路径
	A string `json:"a,omitempty"`
	// B 包 B 槽的路径（只拖了一个文件时为空）
	B string `json:"b,omitempty"`
}

// HandleCompareDrop 处理"把文件拖到对比弹窗上"。
//
// 规则：只收 1~2 个文件（可以是 jar/zip 包，也可以是普通文件）。拖 1 个填
// 一个槽（填哪个由前端按落点定，前端会把它选中的槽号传回来）；拖 2 个直接
// 按 A、B 分配 —— 一次拖两个是最自然的用法，此时落点不重要。
//
// 为什么最多 2 个：槽位只有两个，多出来的没有去处；与其静默丢掉，
// 不如明确报一句。
//
// 这里**不判 jar/zip**：两个包还是两个普通文件，由前端按扩展名分流
// （包走 ComparePacks、普通文件走 CompareFiles），后端不重复那份规则。
// 只挡掉目录 —— 目录不是"要比对的东西"，在拖入这一步就给出明确提示，
// 比拖进去之后再报"不是有效的 jar/zip"清楚。
func (a *App) HandleCompareDrop(paths []string) (*CompareDropFiles, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("没有拿到文件路径")
	}
	if len(paths) > 2 {
		return nil, fmt.Errorf("一次最多拖 2 个（拖进来 %d 个）", len(paths))
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("空路径")
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return nil, fmt.Errorf("不接受目录: %s", filepath.Base(p))
		}
	}
	// 一次拖两个时必须同类（都是包或都是普通文件）：包比的是"内部各条目"、
	// 文件比的是"整份内容"，混着比无从下手。与前端「开始对比」时的判定一致
	// （见 app.js compareRun），这里多一道是为了拖入时就给出明确提示。
	if len(paths) == 2 && isComparePackPath(paths[0]) != isComparePackPath(paths[1]) {
		return nil, fmt.Errorf("一个是包、一个是普通文件，无法对比（要么两个包、要么两个文件）")
	}
	out := &CompareDropFiles{A: paths[0]}
	if len(paths) > 1 {
		out.B = paths[1]
	}
	return out, nil
}

// isComparePackPath 判定一个路径是不是"包"（.jar / .zip）。
//
// 这是**包的规范定义**，前端 app.js 的 isArchivePath 与它逐字对齐
// （前端据此在 ComparePacks / CompareFiles 两条绑定间分流）。
//
// 与主窗口那两处（acceptAsAppPack / acceptAsPatchPack）**刻意不同**：那两处
// 大小写敏感是上游 Java 的 `String.endsWith` 行为，属保真点，不能动；而
// 「对比」是本移植版新增的功能，没有上游要对齐，按用户直觉 `.JAR` 也是 jar
// 更合适 —— 而且文件对话框的过滤器（*.jar;*.zip）在 Windows 上本来就大小写
// 不敏感，两边口径这样才一致。
func isComparePackPath(p string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" {
		return false
	}
	return strings.HasSuffix(lower, constant.DotJar) || strings.HasSuffix(lower, constant.DotZip)
}

// ---------------------------------------------------------------------------
// 展开嵌套 jar（包对比结果里 `BOOT-INF/lib/x.jar` 这类条目）
//
// 需求来源：包对比里最常见的差异就是 lib 下的嵌套 jar 不同（用户原话：
// "对比两个包时，出现lib下jar包不同的，能不能也可以查看"）。原来的口径是
// "嵌套 jar 按普通二进制文件比、不递归展开"，点它只会得到"没有内容解析器"。
// 这里补上按需展开：把 A / B 两个包里同一路径链的嵌套包读出来，对它们再跑
// 一次逐条目对比 —— 与顶层包对比同一套规则（service.ComparePackBytes）。
//
// 为什么按需展开而不是建树/比对时递归展开全部嵌套 jar：一个 spring-boot 胖包
// lib 下几十上百个 jar，全展开会让结果列表爆掉，而用户真正关心的只是
// "变了的那几个"。按需点开才看，代价与噪音都最小。
// ---------------------------------------------------------------------------

// OpenNestedCompare 展开对比结果里的一个嵌套 jar/zip。
//
// chain 是从根包往里数的条目路径列表：顶层就是 ["BOOT-INF/lib/x.jar"]；
// 再往里点一层就追加一项（如 ["...x.jar", "lib/y.jar"]）。来源仍是上次
// 对比的那两个包（a.compareA / a.compareB），前端只传这条路径链。
func (a *App) OpenNestedCompare(chain []string) (*service.ComparePackResult, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("没有指定要展开的包")
	}
	pathA, pathB := a.compareA, a.compareB
	if pathA == "" || pathB == "" {
		return nil, fmt.Errorf("还没有对比过任何两个包")
	}
	ba, fa, err := readChainEntry(pathA, chain)
	if err != nil {
		return nil, err
	}
	bb, fb, err := readChainEntry(pathB, chain)
	if err != nil {
		return nil, err
	}
	leaf := chain[len(chain)-1]
	if !fa || !fb {
		// "仅 A 有 / 仅 B 有"的嵌套包没有可比对的另一侧
		return nil, fmt.Errorf("只有一侧有这个包，没有可比对的另一侧: %s", leaf)
	}
	return service.ComparePackBytes(leaf, ba, bb)
}

// readChainEntry 从外层包开始逐层读嵌套包，返回**最内层包**的字节。
//
// chain 是"从根包往里数"的条目路径列表。某一层没找到该条目时返回
// found=false（不是错误 —— 调用方据此给"另一侧没有"的提示）。
func readChainEntry(outerPath string, chain []string) (data []byte, found bool, err error) {
	if len(chain) == 0 {
		return nil, false, fmt.Errorf("空路径链")
	}
	pe, err := service.ReadPackEntry(outerPath, chain[0])
	if err != nil {
		return nil, false, err
	}
	if !pe.Found {
		return nil, false, nil
	}
	cur := pe.Bytes
	for _, entry := range chain[1:] {
		pe, err := service.ReadPackEntryBytes(entry, cur, entry)
		if err != nil {
			return nil, false, err
		}
		if !pe.Found {
			return nil, false, nil
		}
		cur = pe.Bytes
	}
	return cur, true, nil
}

// readContextEntry 在当前对比上下文（chain）里读一个条目的内容。
//
// chain 为空 = 顶层，直接读外层包；否则先逐层读到最内层包，再按名读条目。
// 供 OpenCompareContent 用 —— 展开嵌套 jar 后，条目在内层包里。
func readContextEntry(outerPath string, chain []string, entry string) (service.PackEntry, error) {
	if len(chain) == 0 {
		return service.ReadPackEntry(outerPath, entry)
	}
	data, found, err := readChainEntry(outerPath, chain)
	if err != nil {
		return service.PackEntry{}, err
	}
	if !found {
		return service.PackEntry{}, nil // Found=false：这一侧没有该嵌套包
	}
	return service.ReadPackEntryBytes(chain[len(chain)-1], data, entry)
}

// ---------------------------------------------------------------------------
// 查看对比结果里某个文件的内容
//
// 需求来源：对比只回答"哪些文件不同"，用户接下来必然要问"差在哪"。
// 这里复用主界面的**内容预览浮窗**（#content-win，z-index 65，盖在对比
// 遮罩之上）来回答：两侧都有就并排对比（与树预览的双栏是同一套渲染），
// 只有一侧就单栏。
//
// 为什么复用浮窗、而不是在对比弹窗里自己画一块：语法高亮、行号、差异标记、
// 编码切换、面板内 Ctrl+F、最大化 —— 这一整套都只该有一份实现。
// 关掉浮窗就回到对比列表，能连着翻看好几个文件。
// ---------------------------------------------------------------------------

// CompareContentOptions 打开"对比结果里某个文件"的参数。
//
// 比树预览的 ContentOptions 少一个 Side —— 来源不是哪棵树，而是**上一次
// 对比的那两个包**（App.compareA / compareB）。这个来源不从前端传，
// 免得"屏幕上显示的结果"与"实际读的包"对不上。
type CompareContentOptions struct {
	// Path 条目在包内的路径（就是比对结果里那一行的路径）
	Path string `json:"path"`
	// Chain 当前嵌套上下文：从根包往里数的条目路径列表（顶层为空）。
	//
	// 展开嵌套 jar 后，用户看到的条目在**内层包**里 —— 只按 Path 从外层包
	// 直接读是读不到的，必须按这条链逐层读进去。顶层（空）时直接用外层包。
	Chain []string `json:"chain,omitempty"`
	// Charset / RightCharset 左栏 / 右栏字符集；空串表示自动探测
	Charset      string `json:"charset"`
	RightCharset string `json:"rightCharset"`
	// DiffModel 差异显示模式：true 单栏差异、false 双栏。
	// nil 表示沿用当前会话里记住的值（与树预览共用一个开关）。
	DiffModel *bool `json:"diffModel"`
}

// compareSide 对比预览里的一侧（哪个包、有没有这个文件、显示成什么标签）。
type compareSide struct {
	// label 这一栏在差异文本里的名字，形如 "old-app.jar!com/foo/Bar.class"。
	// 用"包名!条目"而不是裸条目名：两侧条目名一模一样，光看 `---/+++`
	// 分不出哪边是哪个包。
	label string
	// cs 这一栏的字符集（空串=自动探测）
	cs string
	// entry 条目内容（没这个文件时 Found=false）
	entry service.PackEntry
}

// OpenCompareContent 打开对比结果里某个文件的内容预览。
//
// 分支：两侧都有 -> 双栏（或单栏差异，看 diffModel）；只有一侧 -> 单栏。
// 解析器按**包 A 那一侧**的扩展名选，与树预览"解析器只看左节点"同口径；
// 包 A 没有这个文件时（"仅 B 有"）才轮到包 B 当左栏。
func (a *App) OpenCompareContent(opt CompareContentOptions) (*ContentView, error) {
	entry := strings.TrimSpace(opt.Path)
	if entry == "" {
		return nil, fmt.Errorf("没有指定要查看的文件")
	}
	pathA, pathB := a.compareA, a.compareB
	if pathA == "" || pathB == "" {
		return nil, fmt.Errorf("还没有对比过任何两个包")
	}

	ea, err := readContextEntry(pathA, opt.Chain, entry)
	if err != nil {
		return nil, err
	}
	eb, err := readContextEntry(pathB, opt.Chain, entry)
	if err != nil {
		return nil, err
	}
	if !ea.Found && !eb.Found {
		return nil, fmt.Errorf("两个包里都没有这个文件: %s", entry)
	}

	nameA, nameB := filepath.Base(pathA), filepath.Base(pathB)
	ext := util.Ext(entry)
	parser := content.ParserForExt(ext)
	title := compareContentTitle(entry, nameA, nameB, ea.Found, eb.Found)

	// 没有解析器（图片、嵌套 jar、无后缀…）：树预览那边是"交给系统程序打开"，
	// 但对比场景下要打开的是两个文件，而且用户问的是"差在哪"不是"打开它"。
	// 所以给一句说明，把两边大小摆出来 —— 看不了内容，至少知道差多少。
	if parser == content.ParserNone {
		return &ContentView{
			Mode:    contentModeUnsupported,
			Title:   title,
			Ext:     ext,
			Message: compareBinaryMessage(ea, eb),
		}, nil
	}

	diffModel := a.contentDiffModel
	if opt.DiffModel != nil {
		diffModel = *opt.DiffModel
		a.contentDiffModel = diffModel
	}

	left := compareSide{label: nameA + "!" + entry, cs: opt.Charset, entry: ea}
	right := compareSide{label: nameB + "!" + entry, cs: opt.RightCharset, entry: eb}
	if !ea.Found {
		// 只有包 B 有：B 当左栏，单栏显示（另一侧没有内容可比）
		left, right = right, left
	}
	hasRight := right.entry.Found

	leftText, leftCS, err := a.compareSideText(left, parser)
	if err != nil {
		if parser == content.ParserClass {
			// 与树预览同一处理：反编译不可用是可预期的前置条件缺失，
			// 不是程序出错，给一句能照做的提示。
			return &ContentView{
				Mode:    contentModeUnsupported,
				Title:   title,
				Ext:     ext,
				Message: err.Error(),
			}, nil
		}
		return nil, err
	}
	if !hasRight {
		return renderContentView(title, content.LangForExt(ext), ext,
			left.label, leftText, leftCS, "", "", "", false, diffModel), nil
	}

	rightText, rightCS, err := a.compareSideText(right, parser)
	if err != nil {
		return nil, err
	}
	return renderContentView(title, content.LangForExt(ext), ext,
		left.label, leftText, leftCS, right.label, rightText, rightCS, true, diffModel), nil
}

// compareSideText 取对比预览里某一侧的可显示文本。
//
// 与树预览的 sideText 同一口径：parser 由**左栏**决定后透传给两侧
// （原版两个 CodeArea 共用一个 parser 实例）；.class 走反编译，
// 此时 charset 无意义。
func (a *App) compareSideText(s compareSide, parser content.ParserKind) (string, util.Charset, error) {
	if parser == content.ParserClass {
		src, err := a.decompileEntry(s.label, s.entry)
		if err != nil {
			return "", "", err
		}
		return src, "", nil
	}
	t, cs := decodeRaw(s.entry.Bytes, s.cs)
	return t, cs, nil
}

// decompileEntry 反编译包内一个 .class 条目（连同内部类）。
//
// 结果按"条目路径 + 主类与内部类字节的 sha256"做会话内缓存，与树预览的
// decompileClass 同一套理由：切换差异模式 / 编码都会重新取一次文本，
// 没有缓存的话每切一次都要重新起 JVM（本机实测冷启动约 1 秒）。
//
// key 带 "cmp:" 前缀是为了与树预览的 key（"节点路径:哈希"）分开 ——
// 两者的路径语义不同，混在一个命名空间里只会让缓存难以推理。
func (a *App) decompileEntry(entryPath string, e service.PackEntry) (string, error) {
	h := sha256.New()
	h.Write(e.Bytes)
	for _, b := range e.Inners {
		h.Write(b)
	}
	key := "cmp:" + entryPath + ":" + hex.EncodeToString(h.Sum(nil))
	return a.cachedDecompile(key, func() (string, error) {
		return decompileRun(e.Bytes, e.Inners)
	})
}

// compareContentTitle 对比预览的标题。
//
// 刻意不复用树预览的 "左 <- 右"：两边是同一个条目名，拼出来会是
// "a/b/C.class <- a/b/C.class"。用包名区分才说得清这一栏是哪个包。
func compareContentTitle(entry, nameA, nameB string, inA, inB bool) string {
	switch {
	case inA && inB:
		return entry + "（" + nameA + " ↔ " + nameB + "）"
	case inA:
		return entry + "（仅 " + nameA + " 有）"
	default:
		return entry + "（仅 " + nameB + " 有）"
	}
}

// compareBinaryMessage 没有解析器时的说明文案。
//
// 不照搬树预览的"已用系统默认程序打开"：对比要打开的是两个文件，而用户
// 问的是"差在哪"。所以把两边大小摆出来，缺的那侧显示 "—"。
func compareBinaryMessage(ea, eb service.PackEntry) string {
	side := func(e service.PackEntry) string {
		if !e.Found {
			return "—"
		}
		return fmt.Sprintf("%d 字节", len(e.Bytes))
	}
	return fmt.Sprintf("该类型没有内容解析器，无法预览内容（A %s / B %s）", side(ea), side(eb))
}

// ---------------------------------------------------------------------------
// 打开"文件对比结果"的差异（对应前端「查看差异」按钮）
//
// 与包对比的 OpenCompareContent 是同一套渲染（都走 renderContentView），
// 区别只在**来源**：这里读的是磁盘上的两个普通文件，不是两个包里的条目。
// 所以左右路径由前端显式传进来（对比结果里存的那两个），后端不猜。
// ---------------------------------------------------------------------------

// FileCompareOptions 打开"两个文件差异"的参数。
type FileCompareOptions struct {
	// Left / Right 两个文件的磁盘路径
	Left  string `json:"left"`
	Right string `json:"right"`
	// Charset / RightCharset 左栏 / 右栏字符集；空串表示自动探测
	Charset      string `json:"charset"`
	RightCharset string `json:"rightCharset"`
	// DiffModel 差异显示模式：true 单栏差异、false 双栏。
	// nil 表示沿用当前会话里记住的值（与树预览共用一个开关）。
	DiffModel *bool `json:"diffModel"`
}

// OpenFileCompare 打开两个普通文件的内容对比。
//
// 解析器按**左侧**文件的扩展名选（与树预览 / 包对比"解析器只看左"同口径）；
// .class 走反编译，其余按字符集解码。没有解析器（图片、无后缀…）时给一句
// 带两侧大小的说明 —— 看不了内容，至少知道差多少。
func (a *App) OpenFileCompare(opt FileCompareOptions) (*ContentView, error) {
	left := strings.TrimSpace(opt.Left)
	right := strings.TrimSpace(opt.Right)
	if left == "" || right == "" {
		return nil, fmt.Errorf("没有指定要比对的文件")
	}

	ext := util.Ext(left)
	parser := content.ParserForExt(ext)
	title := compareFileTitle(left, right)

	if parser == content.ParserNone {
		return &ContentView{
			Mode:    contentModeUnsupported,
			Title:   title,
			Ext:     ext,
			Message: compareFileBinaryMessage(left, right),
		}, nil
	}

	diffModel := a.contentDiffModel
	if opt.DiffModel != nil {
		diffModel = *opt.DiffModel
		a.contentDiffModel = diffModel
	}

	leftText, leftCS, err := a.fileText(left, parser, opt.Charset)
	if err != nil {
		if parser == content.ParserClass {
			// 与树预览 / 包对比同一处理：反编译不可用是可预期的前置条件缺失
			return &ContentView{Mode: contentModeUnsupported, Title: title, Ext: ext, Message: err.Error()}, nil
		}
		return nil, err
	}
	rightText, rightCS, err := a.fileText(right, parser, opt.RightCharset)
	if err != nil {
		return nil, err
	}
	return renderContentView(title, content.LangForExt(ext), ext,
		left, leftText, leftCS, right, rightText, rightCS, true, diffModel), nil
}

// fileText 取磁盘上一个文件的可显示文本。
//
// 与包对比的 compareSideText 同一口径：.class 走反编译（按"路径 + 字节哈希"
// 做会话内缓存，切编码/切差异模式时不重复起 JVM），其余按字符集解码。
func (a *App) fileText(path string, parser content.ParserKind, charset string) (string, util.Charset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("读取文件失败: %s: %w", filepath.Base(path), err)
	}
	if parser == content.ParserClass {
		sum := md5.Sum(raw)
		key := "file:" + path + ":" + hex.EncodeToString(sum[:])
		src, err := a.cachedDecompile(key, func() (string, error) {
			// 磁盘上的 .class 是独立文件，不像 jar 里能把同目录内部类一起喂进来；
			// 只反编译主类字节（内部类会以独立类型被引用，不影响主类方法体）。
			return decompileRun(raw, nil)
		})
		if err != nil {
			return "", "", err
		}
		return src, "", nil
	}
	t, cs := decodeRaw(raw, charset)
	return t, cs, nil
}

// compareFileTitle 文件对比预览的标题："A名 ↔ B名"。
func compareFileTitle(left, right string) string {
	return filepath.Base(left) + " ↔ " + filepath.Base(right)
}

// compareFileBinaryMessage 没有解析器时的说明文案（带两侧字节数）。
func compareFileBinaryMessage(left, right string) string {
	size := func(p string) string {
		fi, err := os.Stat(p)
		if err != nil {
			return "—"
		}
		return fmt.Sprintf("%d 字节", fi.Size())
	}
	return fmt.Sprintf("该类型没有内容解析器，无法预览内容（A %s / B %s）", size(left), size(right))
}
