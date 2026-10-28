package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/content"
	"cn.cpoet.patch.assistant/internal/decompile"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// 文件内容预览，移植自 Java 的 ContentView。
//
// 原版的调用点是**双击节点**：
//
//	HomeLeftTreeView.onMouseClicked  (应用树)  -> new ContentView(node).showDialog(stage)
//	HomeRightTreeView.onMouseClicked (补丁树)  -> new ContentView(node).showDialog(stage)
//
// 前端把双击转成一次 OpenContent 调用，渲染与交互都在前端（gui/frontend）。

// 预览的展示形态。前端按这个值决定怎么摆。
const (
	// contentModeSingle 单栏：只看一个文件。
	contentModeSingle = "single"
	// contentModeDiff 单栏：合并后的差异文本（对应 ContentConf.diffModel = true）。
	contentModeDiff = "diff"
	// contentModeSplit 双栏：左原文 / 右新文（diffModel = false，**这是默认值**）。
	contentModeSplit = "split"
	// contentModeExternal 没有解析器，已经交给系统程序打开（原版 openWithSystem）。
	contentModeExternal = "external"
	// contentModeUnsupported 有解析器但没移植（目前只有 .class 反编译）。
	contentModeUnsupported = "unsupported"
)

// ContentOptions 是打开内容预览的参数。
//
// 为什么用一个结构体而不是一长串参数：字符集切换和差异模式切换都要
// **重新调用一次**（原版是重新 parse / 重新 build），参数会越加越多。
type ContentOptions struct {
	// Side "app" 或 "patch"，决定去哪棵树找节点。
	Side string `json:"side"`
	// Path 节点在包内的路径。
	Path string `json:"path"`
	// Charset 左栏（单栏时就是唯一那栏）的字符集；空串表示自动探测。
	//
	// 注意"自动探测"与"用户选了 UTF-8"是两回事：原版右键菜单里
	// GBK / UTF-8 是单选，点一下就用**指定**编码重新解码。
	Charset string `json:"charset"`
	// RightCharset 右栏的字符集；空串表示自动探测。仅双栏模式有效。
	RightCharset string `json:"rightCharset"`
	// DiffModel 差异显示模式：true 单栏差异、false 双栏。
	// nil 表示沿用当前会话里记住的值（对应 Java 持久化在 Configuration 里）。
	DiffModel *bool `json:"diffModel"`
}

// ContentView 是内容预览对话框的全部数据。
type ContentView struct {
	Mode string `json:"mode"`
	// Title 对应原版 `rightNode == null ? leftNode.getPath()
	// : leftNode.getPath() + " <- " + rightNode.getPath()`。
	Title string `json:"title"`
	// DiffMode 为真表示这次是"有可比对的另一侧"（原版 isLoadDiffMode）。
	// 只有它为真时右键菜单里才有"差异模式"那一项。
	DiffMode bool `json:"diffMode"`
	// DiffModel 当前差异显示模式（供菜单勾选态）。
	DiffModel bool `json:"diffModel"`
	// Lang 高亮语言，取自**左节点**的扩展名（原版就是这么取的）。
	Lang string `json:"lang"`
	// Ext 左节点的扩展名，前端用于显示。
	Ext string `json:"ext"`
	// LeftCharset / RightCharset 实际使用的字符集，供菜单回显。
	LeftCharset  string `json:"leftCharset,omitempty"`
	RightCharset string `json:"rightCharset,omitempty"`
	// Message 不支持时的说明文案。
	Message string `json:"message,omitempty"`
	// Left 左栏（单栏模式下是唯一那栏；差异模式下是差异文本）。
	Left *ContentPane `json:"left,omitempty"`
	// Right 右栏，仅双栏模式有。
	Right *ContentPane `json:"right,omitempty"`
	// Blocks 双栏的差异块列表，供前端的"上一个/下一个差异"导航用
	// （见 content.SplitBlock）。单栏与差异模式没有 —— 那两种形态下
	// 行号列本来就不带差异标记（对齐原版），没有可跳的目标。
	Blocks []content.SplitBlock `json:"blocks,omitempty"`
}

// ContentPane 是一栏的内容。
type ContentPane struct {
	// Path 这一栏对应的节点路径，也当"这是哪一栏"的标识回传给后端。
	Path  string         `json:"path"`
	Lines []content.Line `json:"lines"`
}

// OpenContent 打开内容预览。
//
// 分支逐条对齐原版 ContentView 的构造函数 + showDialog：
//
//	补丁节点：
//	  映射到的应用节点存在且不是 ADD  -> 左=应用节点，右=补丁节点（可比对）
//	  否则                            -> 左=补丁节点，右=空（单栏）
//	应用节点：
//	  自己是 ADD                       -> 左=它映射到的补丁节点，右=空
//	  否则                             -> 左=自己，右=它映射到的补丁节点
//
// 然后 `if (leftNode.isDir()) return;`（目录不弹），
// `getContentParser(leftNode)` 为 null 就走 openWithSystem。
//
// **应用侧 ADD 节点只出单栏**，这不是遗漏：这类节点是匹配时"凭空造"出来的
// （PatchPackService 里 README 指令的目标在应用包里不存在，就沿路径建
// VirtualNode + MappedNode，状态写死 ADD）。原版对它的处理是
// `leftNode = node.getMappedNode()`（补丁侧内容）、`rightNode = null`，
// 也就是只给新文件本身 —— 另一边确实没有东西可比。已用
// TestOpenContentAppSideModifiedPairsBothPanes 反向钉住"两边都有的节点
// 必须是双栏"。
//
// 注意**解析器和高亮规则都只看左节点**：原版 `contentParser.parse(rightNode)`
// 复用同一个 parser 实例，所以左右扩展名不一致时（比如左 .java 右 .class），
// 右侧也会按左侧的规则解析。照搬。
func (a *App) OpenContent(opt ContentOptions) (*ContentView, error) {
	root, _, err := a.treeRoot(opt.Side)
	if err != nil {
		return nil, err
	}
	tar := findTreeNodeByPath(root, opt.Path)
	if tar == nil {
		return nil, fmt.Errorf("树里找不到节点: %s", opt.Path)
	}
	if tar.Dir() {
		// 原版 showDialog 开头 `if (leftNode.isDir()) return;` —— 什么都不做。
		// 这里给个明确错误而不是静默返回：前端本来就不该对目录调这个方法，
		// 真调到了说明有 bug，静默会把它藏起来。
		return nil, fmt.Errorf("目录没有内容预览: %s", tar.Name)
	}

	left, right := contentPair(tar)
	if left == nil {
		return nil, fmt.Errorf("节点 %s 没有可预览的内容来源", tar.Name)
	}

	ext := util.Ext(left.Name)
	// 解析器只由**左节点**决定，右节点复用同一个（原版 `contentParser.parse(rightNode)`
	// 就是这么调的）。所以左 .java 右 .class 时，右侧也按文本读，而不是去反编译。
	parser := content.ParserForExt(ext)

	// 没有解析器 -> 交给系统程序打开（原版 openWithSystem）。
	if parser == content.ParserNone {
		if err := a.openWithSystem(tar); err != nil {
			return nil, err
		}
		return &ContentView{Mode: contentModeExternal, Title: left.Path, Ext: ext}, nil
	}

	diffMode := right != nil
	diffModel := a.contentDiffModel
	if opt.DiffModel != nil {
		diffModel = *opt.DiffModel
		a.contentDiffModel = diffModel
	}

	lang := content.LangForExt(ext)

	// 先取左栏文本。.class 在这一步走 Fernflower 反编译，其余按字符集解码。
	leftText, leftCS, err := a.sideText(left, parser, opt.Charset)
	if err != nil {
		// 反编译不可用（多半是本机没装 Java）走 unsupported 而不是返回 error：
		// 这是**可预期的前置条件缺失**，不是程序出错。给一句能照做的提示，
		// 比抛一个用户看不懂的错误有用。
		if parser == content.ParserClass {
			return &ContentView{
				Mode:    contentModeUnsupported,
				Title:   left.Path,
				Ext:     ext,
				Message: err.Error(),
			}, nil
		}
		return nil, err
	}

	// 单栏
	if !diffMode {
		return renderContentView(contentTitle(left, nil), lang, ext,
			left.Path, leftText, leftCS, "", "", "", false, diffModel), nil
	}

	// 有可比对的另一侧：两侧都按左栏的 parser 取（见上面 parser 的注释）
	rightText, rightCS, err := a.sideText(right, parser, opt.RightCharset)
	if err != nil {
		return nil, err
	}
	return renderContentView(contentTitle(left, right), lang, ext,
		left.Path, leftText, leftCS, right.Path, rightText, rightCS, true, diffModel), nil
}

// renderContentView 把左右两份**已解码文本**摆成 ContentView。
//
// 这是"内容怎么摆"的唯一实现 —— 树预览（OpenContent）与包对比预览
// （OpenCompareContent）都走这里。抽出来是为了让口径只有一处：
// 单栏 / 差异模式（单栏 unified diff）/ 双栏（默认，带行级差异标记）
// 这三条分支、以及"双栏标记的下标与渲染行号一一对应"这个前提，
// 两边分开写迟早会漂移。
//
// hasRight 为 false 表示没有可比对的另一侧（原版 isLoadDiffMode）；
// 此时 rightPath / rightText 传空串即可。
func renderContentView(title, lang, ext, leftPath, leftText string, leftCS util.Charset,
	rightPath, rightText string, rightCS util.Charset, hasRight, diffModel bool) *ContentView {

	view := &ContentView{
		Title:       title,
		DiffMode:    hasRight,
		DiffModel:   diffModel,
		Lang:        lang,
		Ext:         ext,
		LeftCharset: string(leftCS),
	}

	// 单栏
	if !hasRight {
		view.Mode = contentModeSingle
		view.Left = &ContentPane{Path: leftPath, Lines: content.Render(leftText, lang)}
		return view
	}
	view.RightCharset = string(rightCS)

	if diffModel {
		// 单栏差异：把两份文本合成 unified diff 再整段渲染。
		// 名字用 Path（原版传的就是 getPath()）。
		diffText := util.Diff2Str(leftText, rightText, leftPath, rightPath)
		view.Mode = contentModeDiff
		view.Left = &ContentPane{Path: leftPath, Lines: content.RenderDiff(diffText, lang)}
		return view
	}

	// 双栏
	//
	// 两侧各自按显示口径切行（Paragraphs，与 Render 内部同一套），
	// 再按行求差异块打标记 —— 下标与渲染出来的行号一一对应。
	// 原版这里只摆两个 CodeArea、不做任何标记，是刻意增强（见 MIGRATION.md）。
	view.Mode = contentModeSplit
	leftLines := content.Paragraphs(leftText)
	rightLines := content.Paragraphs(rightText)
	sm := content.ComputeSplitMarks(leftLines, rightLines)
	view.Left = &ContentPane{
		Path:  leftPath,
		Lines: content.RenderMarked(leftText, lang, sm.Left, sm.LeftInline),
	}
	view.Right = &ContentPane{
		Path:  rightPath,
		Lines: content.RenderMarked(rightText, lang, sm.Right, sm.RightInline),
	}
	// 差异块一并透出：前端据此做"上一个/下一个差异"的跳转与计数。
	// 这里给的就是**渲染出来的行号**（与 Lines 同下标），前端不必再算。
	view.Blocks = sm.Blocks
	return view
}

// contentPair 复刻原版 ContentView 构造函数里挑左右节点的逻辑。
func contentPair(tar *tree.Node) (left, right *tree.Node) {
	if tar.IsPatch {
		mapped := tar.MappedNode
		if mapped != nil && mapped.Type != constant.TreeNodeTypeAdd {
			return mapped, tar
		}
		return tar, nil
	}
	if tar.Type == constant.TreeNodeTypeAdd {
		return tar.MappedNode, nil
	}
	return tar, tar.MappedNode
}

// contentTitle 拼对话框标题。
func contentTitle(left, right *tree.Node) string {
	if right == nil {
		return left.Path
	}
	return left.Path + " <- " + right.Path
}

// sideText 取某一侧的可显示文本。
//
// parser 由**左节点**决定后透传进来（原版两个 CodeArea 共用一个 parser 实例）。
// .class 走反编译，此时 charset 无意义 —— 原版 ClassContentParser.parse
// 同样忽略 charset 参数。
func (a *App) sideText(n *tree.Node, parser content.ParserKind, charset string) (string, util.Charset, error) {
	if parser == content.ParserClass {
		src, err := a.decompileClass(n)
		if err != nil {
			return "", "", err
		}
		return src, "", nil
	}
	return a.readText(n, charset)
}

// decompileNode 反编译一个 class 节点（连同它的内部类）。
//
// 内部类为什么必须一起传：对齐原版 ClassContentParser.parse —— 节点有子级
// （内部类挂在外部类下）时，原版把所有子孙类的字节一并交给 fernflower。
// 只喂主类的话，反编译出来的内部类是空壳。已用基准钉住
// （internal/decompile 的 NumberField 用例）。
func decompileNode(n *tree.Node) (string, error) {
	main, err := nodeBytes(n)
	if err != nil {
		return "", err
	}
	var inners [][]byte
	for _, c := range classInners(n) {
		b, err := nodeBytes(c)
		if err != nil {
			return "", err
		}
		inners = append(inners, b)
	}
	return decompileRun(main, inners)
}

// decompileRun 是真正的反编译执行点，默认走内嵌 fernflower。
//
// 抽成包级变量纯粹为了可测：单测里临时换成桩，就能断言"同一个 class 只反编译
// 一次"（验证会话内缓存真的生效、没每次都起 JVM）。生产路径默认就是
// decompile.Decompile，行为不变。
var decompileRun = decompile.Decompile

// decompileClass 反编译一个 class 节点，结果按"路径 + 字节哈希"做会话内缓存。
//
// 真正的反编译（起 JVM）仍交给 decompileNode，但这里先用 key 查缓存，
// 命中就直接返回字符串、省掉那次 JVM 冷启动（见 App.decompileCache 字段注释）。
// 并发安全：用 App.decompileMu 护住 map 读写（双检锁，避免两个协程对同一
// class 同时起 JVM 又都写缓存）。
func (a *App) decompileClass(n *tree.Node) (string, error) {
	main, err := nodeBytes(n)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(main)
	for _, c := range classInners(n) {
		b, err := nodeBytes(c)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	key := n.Path + ":" + hex.EncodeToString(h.Sum(nil))
	return a.cachedDecompile(key, func() (string, error) { return decompileNode(n) })
}

// cachedDecompile 取/存反编译结果的会话级缓存；miss 时才调 produce 真去反编译。
//
// 抽出来是因为有两个来源要共用这份缓存：树里的节点（decompileClass，key 含
// 节点路径）与包里的条目（decompileEntry，key 含条目路径）—— 两边都是
// "同一份字节只反编译一次"，但 key 的构成不同，所以 key 由调用方算。
//
// 并发安全：用 App.decompileMu 护住 map 读写（双检锁，避免两个协程对同一份
// 字节同时起 JVM 又都写缓存）。
func (a *App) cachedDecompile(key string, produce func() (string, error)) (string, error) {
	a.decompileMu.Lock()
	if a.decompileCache == nil {
		a.decompileCache = make(map[string]string)
	}
	if v, ok := a.decompileCache[key]; ok {
		a.decompileMu.Unlock()
		return v, nil
	}
	a.decompileMu.Unlock()

	src, err := produce()
	if err != nil {
		return "", err
	}

	a.decompileMu.Lock()
	a.decompileCache[key] = src
	a.decompileMu.Unlock()
	return src, nil
}

// classInners 按原版的顺序收集内部类节点。
//
// 原版用一个栈做 LIFO 深度优先：先取最后一个子节点，展开它的子级，再回头。
// 实测 fernflower 对输入顺序不敏感（_extract/tools 里验过），但既然原版有
// 明确顺序就照抄 —— 别留下"看着一样、其实顺序不同"的隐患。
func classInners(n *tree.Node) []*tree.Node {
	var out []*tree.Node
	stack := append([]*tree.Node(nil), n.Children...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, cur)
		stack = append(stack, cur.Children...)
	}
	return out
}

// nodeBytes 读出一个节点的原始字节。
func nodeBytes(n *tree.Node) ([]byte, error) {
	var raw []byte
	if err := n.ConsumeBytes(func(buf []byte) error {
		raw = append(raw, buf...)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", n.Name, err)
	}
	return raw, nil
}

// readText 读出节点内容并解码。
func (a *App) readText(n *tree.Node, charset string) (string, util.Charset, error) {
	raw, err := nodeBytes(n)
	if err != nil {
		return "", "", err
	}
	t, cs := decodeRaw(raw, charset)
	return t, cs, nil
}

// decodeRaw 按字符集解码一段字节。
//
// 对应 Java TextContentParser.parse：charset 为 null（这里是空串）时用
// CharsetUtil.getCharset 探测，否则按指定编码解。抽出来给"包内条目"复用
// （见 gui/jarcompare.go 的 compareSideText）—— 解码口径只有这一处。
func decodeRaw(raw []byte, charset string) (string, util.Charset) {
	cs := util.Charset(charset)
	if cs == "" {
		cs = util.DetectCharset(raw)
	}
	return util.DecodeString(raw, cs), cs
}

// openWithSystem 对应原版 ContentView.openWithSystem。
//
// 磁盘上真实存在的文件直接打开；压缩包条目先落到临时目录再打开 ——
// 与原版一致（原版把非 FileNode 的字节写到 AppContext.getTempDir() 下）。
func (a *App) openWithSystem(n *tree.Node) error {
	path := ""
	if isDiskFile(n) {
		if p := n.FilePath(); p != "" {
			if _, err := os.Stat(p); err == nil {
				path = p
			}
		}
	}
	if path == "" {
		p, err := extractNodeToTemp(n)
		if err != nil {
			return err
		}
		path = p
	}
	if a.openExternal == nil {
		return fmt.Errorf("系统打开功能未初始化")
	}
	return a.openExternal(path)
}

// extractNodeToTemp 把节点内容写到一个临时文件，保留扩展名。
//
// 保留扩展名是必须的：系统按扩展名挑默认程序，丢了后缀就打不开了。
func extractNodeToTemp(n *tree.Node) (string, error) {
	pattern := "patchassistant-*"
	if ext := util.Ext(n.Name); ext != "" {
		pattern += "." + ext
	}
	f, err := os.CreateTemp(os.TempDir(), pattern)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	werr := n.ConsumeBytes(func(buf []byte) error {
		_, e := f.Write(buf)
		return e
	})
	cerr := f.Close()
	if werr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("写出临时文件失败: %w", werr)
	}
	if cerr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("关闭临时文件失败: %w", cerr)
	}
	return f.Name(), nil
}

// openPathWithOS 用系统默认程序打开一个路径。
//
// 原版走 JavaFX 的 HostServices.showDocument / Desktop.open，效果就是
// "交给系统默认程序"。这里按平台选命令。
func openPathWithOS(path string) error {
	cmd := openCommandFor(path)
	// 必须在 Start 之前设：Windows 上 `cmd` 是控制台程序，不隐藏就会闪黑框
	// （见 hidewindow_windows.go）。
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("调用系统程序打开失败: %w", err)
	}
	return nil
}

// openCommandFor 按平台挑出"交给系统默认程序"的那条命令。
//
// 单独拆出来是为了能单测 —— 打开包里的 .xlsx 时弹 cmd 窗口这个 bug，
// 修法就落在"用哪条命令 + 有没有隐藏窗口"这两处，构造不拆开就断言不到。
func openCommandFor(path string) *exec.Cmd {
	switch {
	case util.IsWindows():
		// `start` 是 cmd 的内置命令。第一个空参数是窗口标题占位 ——
		// 少了它，带引号的路径会被 start 当成标题，文件就打不开了。
		return exec.Command("cmd", "/c", "start", "", path)
	case util.IsMacOS():
		return exec.Command("open", path)
	default:
		return exec.Command("xdg-open", path)
	}
}
