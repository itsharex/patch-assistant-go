package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/content"
	"cn.cpoet.patch.assistant/internal/javac"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// 编辑包内文件内容。
//
// **原版没有这个功能**，属刻意增强：`CodeAreaFactory.getContextMenu` 里那段
// 「编辑模式」菜单项被**整段注释掉了**（连同 `ContentView.handleEditMode` 里的
// `codeArea.setEditable(...)` 一起），`codeArea` 也一直 `setEditable(false)`。
// 原版唯一能改包内内容的路径是 READ ME 编辑器（`ReadmeView` →
// `PatchPackService.updatePatchReadme`），只针对说明文件。
// 已登记在 MIGRATION.md 的偏差清单里。
//
// 三个设计要点：
//
//  1. **只改内存，不动磁盘上的包。** 改完把节点的 source 换成 MemorySource
//     （`tree.Node.SetContent`），之后写包路径自然用新内容 —— 因为读内容的地方
//     全都走 ConsumeBytes（见 service/writer.go 与 service/patchsave.go）。
//     想落盘走「另存为」。重新加载/重新匹配会重建节点，改动随之丢弃。
//  2. **文本文件用文本编辑，其余一律十六进制编辑。** `.class` 归到十六进制
//     那侧：它虽然"能预览"（反编译成 Java 源码），但把源码文本存回 `.class`
//     会把文件写废 —— 那里存的是字节，不是文本。
//  3. **二进制判断不只看扩展名，还看内容里有没有 NUL** —— 后缀是 `.txt` 的
//     二进制文件现实中是存在的。

// 编辑器的三种形态，前端按这个值决定怎么摆。
const (
	// editorModeText 文本文件，等宽文本域直接改。
	editorModeText = "text"
	// editorModeHex 按字节编辑（十六进制网格）。
	editorModeHex = "hex"
	// editorModeSource 反编译出来的 Java 源码，只对 .class 用。
	//
	// 这个形态**不能直接「保存」**：class 里存的是字节码，把源码文本当字节
	// 写回去会把文件写废，而且当场看不出来（要到运行时才炸）。见 SaveContent
	// 的拒绝分支。
	//
	// 想改回 class 得走**重新编译**（`CompileContent` → `internal/javac`），
	// 前提是本机装了 JDK 且节点是顶层类。所以这个形态的按钮条是
	// 「还原 / 另存为 .java / 编译为 .class（条件满足时）/ 改字节」。
	editorModeSource = "source"
)

// EditorOptions 是打开编辑器的参数。
type EditorOptions struct {
	// Side "app" 或 "patch"，决定去哪棵树找节点。
	Side string `json:"side"`
	// Path 节点在包内的路径。
	Path string `json:"path"`
	// Charset 文本模式的字符集；空串表示自动探测。
	//
	// 必须能指定而不是一律 UTF-8：原文件是 GBK 的，改完仍要按 GBK 写回去，
	// 否则这个包从这一刻起整段变乱码。
	Charset string `json:"charset"`
	// AsBytes 强制按字节打开，跳过「.class 反编译成源码」这一步。
	//
	// .class 默认给源码（点「编辑内容」想看的多半是逻辑本身），但有时目标
	// 只是改几个字节 —— 典型是替换一段字符串常量，那时前端带 AsBytes 重开一次。
	AsBytes bool `json:"asBytes"`
}

// EditorView 是编辑器面板的全部数据。
type EditorView struct {
	// Mode editorModeText 或 editorModeHex。
	Mode string `json:"mode"`
	// Side / Path 回传给后端用，前端原样带着。
	Side string `json:"side"`
	Path string `json:"path"`
	// Title 面板标题，用包内路径。
	Title string `json:"title"`
	// Lang 语法高亮语言（文本模式下给等宽字体/配色用）。
	Lang string `json:"lang"`
	// Charset 文本模式实际使用的字符集，供编码切换回显。
	Charset string `json:"charset"`
	// Text 文本模式的当前内容。
	Text string `json:"text,omitempty"`
	// Base64 十六进制模式的原始字节（base64 编码）。
	//
	// 为什么整段传而不是分页传：前端只渲染当前页的 DOM，传输一次就够，
	// 翻页不再往返后端。1 MB 的文件 base64 后约 1.33 MB，WebView 扛得住。
	Base64 string `json:"base64,omitempty"`
	// Size 内容字节数，前端显示。
	Size int `json:"size"`
	// Edited 这个节点是否已经有未落盘的修改。
	Edited bool `json:"edited"`
	// CanCompile 源码形态下能不能编译回 class（摆不摆「编译为 .class」按钮）。
	//
	// 三个条件：形态是 source、节点是**顶层类**、本机有 javac（JDK 而不是 JRE）。
	// 由后端算好而不是让前端拼，是因为这三个条件前端都拿不到。
	CanCompile bool `json:"canCompile,omitempty"`
	// CompileHint 不能编译时的一句话原因；能编译时说明会做什么。
	//
	// 要说清"为什么不能"，不然用户只会看到一个凭空消失的按钮。
	CompileHint string `json:"compileHint,omitempty"`
}

// SaveOptions 是保存 / 另存为的参数。
type SaveOptions struct {
	Side string `json:"side"`
	Path string `json:"path"`
	// Mode 与 EditorView.Mode 对应，决定按文本还是按字节解释下面两个字段。
	Mode string `json:"mode"`
	// Text 文本模式的新内容。
	Text string `json:"text"`
	// Base64 十六进制模式的新内容（原始字节的 base64）。
	Base64 string `json:"base64"`
	// Charset 文本模式的编码。
	Charset string `json:"charset"`
}

// OpenEditor 打开编辑器，返回内容与形态。
//
// 与 OpenContent 的分工：那个走的是**原版的双击预览**路径（没有解析器的文件
// 直接交给系统程序打开），本方法是新增的编辑通道，两者互不影响 ——
// 这样"双击预览"的行为仍然与原版一致。
func (a *App) OpenEditor(opt EditorOptions) (*EditorView, error) {
	n, err := a.nodeBySide(opt.Side, opt.Path)
	if err != nil {
		return nil, err
	}
	if n.Dir() {
		return nil, fmt.Errorf("目录没有内容可编辑: %s", n.Name)
	}

	raw, err := n.ContentBytes()
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", n.Name, err)
	}

	ext := util.Ext(n.Name)
	parser := content.ParserForExt(ext)
	view := &EditorView{
		Side:   opt.Side,
		Path:   n.Path,
		Title:  n.Path,
		Lang:   content.LangForExt(ext),
		Size:   len(raw),
		Edited: n.Edited(),
	}

	// .class 默认给反编译出来的 Java 源码。
	//
	// 字节码本身"改"不出有意义的结果 —— 用户点「编辑内容」时想看的是逻辑。
	// 反编译不可用（本机没装 Java）时**不报错**，继续往下走十六进制：
	// 这是可预期的前置条件缺失，降级比抛错有用（与 OpenContent 同口径）。
	if parser == content.ParserClass && !opt.AsBytes {
		if src, err := a.decompileClass(n); err == nil {
			view.Mode = editorModeSource
			view.Lang = content.LangForExt("java")
			view.Text = src
			view.CanCompile, view.CompileHint = a.compileAbility(n)
			return view, nil
		}
	}

	// 能当文本读的走文本编辑：要有文本解析器，且内容里没有 NUL。
	if parser == content.ParserText && !looksBinary(raw) {
		cs := util.Charset(opt.Charset)
		if cs == "" {
			cs = util.DetectCharset(raw)
		}
		view.Mode = editorModeText
		view.Charset = string(cs)
		view.Text = util.DecodeString(raw, cs)
		return view, nil
	}

	view.Mode = editorModeHex
	view.Base64 = base64.StdEncoding.EncodeToString(raw)
	return view, nil
}

// SaveContent 把编辑器里的内容写回节点（**只改内存**）。
//
// 返回最新状态而不是"成功/失败"：节点多了个"已修改"标记，
// 前端要拿它去更新那一行的小标记。
func (a *App) SaveContent(opt SaveOptions) (*State, error) {
	n, err := a.nodeBySide(opt.Side, opt.Path)
	if err != nil {
		return nil, err
	}
	if n.Dir() {
		return nil, fmt.Errorf("目录不能保存内容: %s", n.Name)
	}
	if opt.Mode == editorModeSource {
		// 明确拒绝，而不是把源码文本当字节写进去 —— 那会把 class 写废，
		// 而且用户当场看不出来（要到运行时才炸）。
		return nil, fmt.Errorf("class 的源码不能直接存回包：那会把字节码写坏。" +
			"要改回 class 请用「编译为 .class」，或「另存为 .java」后自行编译")
	}
	data, err := contentBytes(opt)
	if err != nil {
		return nil, err
	}
	n.SetContent(data)
	return a.state(), nil
}

// SaveContentAs 把编辑器里的内容另存到磁盘（**不改节点**）。
//
// 导出的是"编辑器里现在的内容"而不是节点里的旧内容 —— 用户点了另存为，
// 期望拿到的是眼前看到的那份。取消对话框就什么都不做（对齐原版 SaveNodeAs
// 里 `IsBlank(out)` 那句）。
func (a *App) SaveContentAs(opt SaveOptions) (*State, error) {
	n, err := a.nodeBySide(opt.Side, opt.Path)
	if err != nil {
		return nil, err
	}
	if n.Dir() {
		return nil, fmt.Errorf("目录不能另存为文件: %s", n.Name)
	}
	data, err := contentBytes(opt)
	if err != nil {
		return nil, err
	}
	name := defaultSaveName(n)
	if opt.Mode == editorModeSource {
		// 导出的是源码，扩展名要跟着变成 .java —— 否则系统会用错程序打开，
		// 编辑器也不给 Java 高亮。
		name = util.Name(name) + string(util.ExtSeparator) + "java"
	}
	out, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "另存为",
		DefaultFilename: name,
	})
	if err != nil {
		return nil, err
	}
	if util.IsBlank(out) {
		// 用户取消：什么都不做，状态原样返回
		return a.state(), nil
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return nil, fmt.Errorf("写出文件失败: %w", err)
	}
	return a.state(), nil
}

// CompileOptions 是「编译为 .class」的参数。
type CompileOptions struct {
	Side string `json:"side"`
	Path string `json:"path"`
	// Text 编辑器里**当前**的源码，不是节点里的旧内容 ——
	// 用户点了编译，期望生效的是眼前看到的那份。
	Text string `json:"text"`
}

// CompileContent 把编辑器里的 Java 源码编译成 class，写回节点（含内部类）。
//
// 这是「源码形态不能直接保存」的解法：class 里是字节码，源码要变回去只能
// 重新编译。原版没有这个能力，属刻意增强（见 MIGRATION.md）。
//
// 四件事必须做对：
//
//  1. **按原 class 的版本编译。** 用本机 JDK 的默认版本编出来的 class 版本
//     可能比原件高，原包跑在低版本 JRE 上会直接 UnsupportedClassVersionError。
//  2. **classpath 要给全。** 被改的类几乎必然引用别的类型（同包的、依赖 jar
//     里的），不给的话 javac 满屏"找不到符号"。classpath 由
//     `buildAppClasspath` 从已打开的应用包摊平而来。
//  3. **产出的类集合必须与树上节点一一对应。** 编译可能多出匿名类
//     （`Foo$1`）、也可能少掉内部类。不一致时**拒绝替换**并列出差异 ——
//     悄悄写进去会得到一个残缺的 class，问题要到运行时才炸。
//  4. **失败时把 javac 的原始报错原样带出去。** 那是用户唯一能定位问题的线索。
func (a *App) CompileContent(opt CompileOptions) (*State, error) {
	n, err := a.nodeBySide(opt.Side, opt.Path)
	if err != nil {
		return nil, err
	}
	if !isClassNode(n) {
		return nil, fmt.Errorf("只有 .class 能编译回字节码：%s", n.Name)
	}
	if isInnerClassNode(n) {
		return nil, fmt.Errorf("内部类不能单独编译：%s —— "+
			"内部类的源码本来就在外层类的文件里，请打开外层类改完一起编译", n.Name)
	}

	raw, err := n.ContentBytes()
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", n.Name, err)
	}
	major, err := javac.ClassVersion(raw)
	if err != nil {
		return nil, fmt.Errorf("读原 class 版本失败: %w", err)
	}

	cp, err := a.buildAppClasspath()
	if err != nil {
		return nil, err
	}

	res, err := javac.Compile(javac.Options{
		Source: opt.Text,
		// 文件名必须是 `<顶层类名>.java`，否则 javac 会因为 public 类名与
		// 文件名不符而报错。
		FileName:  util.Name(n.Name) + string(util.ExtSeparator) + constant.ExtJava,
		Classpath: cp,
		Release:   javac.ReleaseFor(major),
	})
	if err != nil {
		return nil, err
	}

	if err := checkCompiledSet(n, res.Classes); err != nil {
		return nil, err
	}

	// 逐个写回。SetContent 会清掉 Md5/Size 缓存并打上"已修改"标记，
	// 之后写包路径自然读到新字节。
	for _, c := range append([]*tree.Node{n}, n.Children...) {
		c.SetContent(res.Classes[c.Name])
	}
	return a.state(), nil
}

// checkCompiledSet 校验编译产出的类集合与树上节点是否一一对应。
//
// 为什么要卡这一条而不是"有几个写几个"：多出来的类（新增了匿名类）没有
// 对应的树节点，写不进去，包内就会缺文件；少掉的类（原内部类没被产出）
// 会在包里留一份**过期的旧 class**，运行时行为不可预期。两种都是
// "当场看不出、上线才炸"的问题，所以宁可不做。
func checkCompiledSet(n *tree.Node, got map[string][]byte) error {
	want := make(map[string]bool, len(n.Children)+1)
	want[n.Name] = true
	for _, c := range n.Children {
		want[c.Name] = true
	}

	var extra, missing []string
	for name := range got {
		if !want[name] {
			extra = append(extra, name)
		}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(extra) == 0 && len(missing) == 0 {
		return nil
	}
	sort.Strings(extra)
	sort.Strings(missing)

	var parts []string
	if len(extra) > 0 {
		parts = append(parts, "多出 "+strings.Join(extra, "、"))
	}
	if len(missing) > 0 {
		parts = append(parts, "缺少 "+strings.Join(missing, "、"))
	}
	return fmt.Errorf("编译产出的类与包内现有节点对不上（%s）。\n"+
		"这通常说明这次改动动了类的结构（新增或删掉了内部类 / 匿名类）。\n"+
		"为避免写出残缺的 class，这里不做替换 —— "+
		"请用「另存为 .java」导出后自行编译", strings.Join(parts, "；"))
}

// compileAbility 判断一个节点能不能编译回 class，并给一句人话原因。
//
// 三个条件缺一不可，且**都是前端拿不到的**，所以在这里算好下发：
// 形态是源码、节点是顶层类、本机有 javac。
func (a *App) compileAbility(n *tree.Node) (bool, string) {
	if !isClassNode(n) {
		return false, ""
	}
	if isInnerClassNode(n) {
		return false, "内部类不能单独编译 —— 请打开外层类，改完一起编译"
	}
	if !javac.Available() {
		return false, "本机没有 javac，无法编译（反编译只需要 JRE，编译需要完整 JDK）"
	}
	return true, "编译为 .class：按原 class 版本编译后替换包内字节"
}

// isInnerClassNode 判断是不是内部类节点（`Foo$Inner.class`、`Foo$1.class`）。
func isInnerClassNode(n *tree.Node) bool {
	return n != nil && strings.Contains(util.Name(n.Name), "$")
}

// contentBytes 把编辑器里的内容还原成字节。
//
// 文本模式要按 charset 编码而不是一律 UTF-8 —— 见 EditorOptions.Charset。
func contentBytes(opt SaveOptions) ([]byte, error) {
	if opt.Mode == editorModeHex {
		b, err := base64.StdEncoding.DecodeString(opt.Base64)
		if err != nil {
			return nil, fmt.Errorf("十六进制内容解析失败: %w", err)
		}
		return b, nil
	}
	cs := util.Charset(opt.Charset)
	if cs == "" {
		cs = util.CharsetUTF8
	}
	return util.EncodeString(opt.Text, cs), nil
}

// nodeBySide 按 side 取节点，取不到就报错。
func (a *App) nodeBySide(side, path string) (*tree.Node, error) {
	root, _, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	n := findTreeNodeByPath(root, path)
	if n == nil {
		return nil, fmt.Errorf("树里找不到节点: %s", path)
	}
	return n, nil
}

// looksBinary 按内容判断是不是二进制。
//
// 口径：前 8 KB 里出现 NUL 字节就算二进制。这是 git / grep 一类工具的常用
// 口径，够用且不会把普通文本误判 —— 反过来，UTF-16 文本会带 NUL，被归到
// 十六进制编辑那侧，这也是合理的（按文本编 UTF-16 本来就会写坏）。
func looksBinary(raw []byte) bool {
	n := len(raw)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if raw[i] == 0 {
			return true
		}
	}
	return false
}
