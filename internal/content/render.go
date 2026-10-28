package content

import (
	"unicode"
)

// Span 一段带样式类的区间，字节偏移 [Start, End)。
type Span struct {
	Start int
	End   int
	Class string
}

// Seg 行内的一段文本。Class 为空表示不加样式。
//
// 为什么直接给"文本 + 类名"而不是"偏移 + 类名"：偏移的单位在 Java 那边是
// UTF-16 码元、Go 这边是字节、JS 那边又是 UTF-16，三方对不齐。直接给文本
// 片段就与编码无关了，前端拼 <span> 时也不会有切错字符的风险。
type Seg struct {
	Text  string `json:"text"`
	Class string `json:"class,omitempty"`
}

// Line 是渲染出来的一行。
type Line struct {
	// Num 行号文本，已按原版口径右对齐；差异模式下可能是空串或 "..."。
	Num string `json:"num"`
	// Segs 行内片段，按顺序拼起来等于这一行的原文。
	Segs []Seg `json:"segs,omitempty"`
	// Mark 差异标记，取值见 MarkMod / MarkAdd / MarkDel；空串表示这行没有差异。
	// 只有双栏预览会填（见 DiffMarks）。
	Mark string `json:"mark,omitempty"`
}

// 差异标记的取值。刻意与树里那套状态名（t-mod / t-add / t-del）对齐，
// 前端样式类名就是 mark-<取值>。
const (
	// MarkMod 这一块两边都有内容 —— 改。
	MarkMod = "mod"
	// MarkAdd 只有右栏（补丁侧的新文件）有 —— 增。
	MarkAdd = "add"
	// MarkDel 只有左栏（应用侧的旧文件）有 —— 删。
	MarkDel = "del"
)

// Paragraphs 按 JavaFX CodeArea 的口径切段落。
//
// 这套口径是**实测**出来的（见 _extract/tools/ParaProbe.java 的输出），
// 有三个必须照搬的点：
//
//   - 换行符是 `\r\n`、`\r`、`\n` 三种；单独一个 `\r` 也算换行
//     （`"a\rb"` 是 2 段）。`\u0085` / `\u2028` / `\u2029` / `\u000b` / `\u000c`
//     **都算普通字符**，实测确认过。
//   - **末尾空段落保留**：`"a\nb\n"` 是 3 段（`a`、`b`、空）。
//     这一点最容易被"顺手 trim 掉"，而绝大多数源码文件都以换行结尾，
//     丢掉就会少一行。
//   - 空串是 **1** 段（一个空段落），不是 0 段。
//
// 注意这跟 util.SplitDiffLines 的口径**不一样**，那套是给差异算法用的
// （只认 `\n` 和 `\r\n`、还要去掉尾部空行）。两者不能互相替换。
func Paragraphs(s string) []string {
	ranges := paragraphRanges(s)
	out := make([]string, len(ranges))
	for i, r := range ranges {
		out[i] = s[r[0]:r[1]]
	}
	return out
}

// paragraphRanges 返回每段在原文里的字节区间。
func paragraphRanges(s string) [][2]int {
	out := make([][2]int, 0, 8)
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			out = append(out, [2]int{start, i})
			start = i + 1
		case '\r':
			out = append(out, [2]int{start, i})
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return append(out, [2]int{start, len(s)})
}

// Render 把一段文本渲染成可显示的行，行号是 1..n 右对齐。
//
// 对应 Java 的 `LineNumberFactory.get(codeArea)`：行号就是 idx+1，
// 补空格到"段落总数的位数"那么宽。
func Render(text, lang string) []Line {
	spans := Tokenize(text, lang)
	if spans == nil {
		// 不高亮：整段当作一个无样式区间，后面的切分逻辑就不用分叉了
		spans = []Span{{Start: 0, End: len(text)}}
	}
	ranges := paragraphRanges(text)
	nums := PlainNumbers(len(ranges))
	lines := make([]Line, len(ranges))
	for i, r := range ranges {
		lines[i] = Line{Num: nums[i], Segs: cutSegs(text, spans, r[0], r[1])}
	}
	return lines
}

// RenderMarked 与 Render 相同，只是把差异标记贴到每一行上。
//
// marks 允许比段落数短、也允许为 nil（缺的位置按"无差异"处理）—— 标记是
// 纯装饰，对不上顶多少高亮几行，不该让整个预览打不开。
//
// inline 是行内差异区间（见 ComputeSplitMarks）。它只对**有行级标记的行**
// 生效：没有行级标记却说"行内某几个词变了"是自相矛盾的，忽略掉更安全。
func RenderMarked(text, lang string, marks []string, inline [][]InlineMark) []Line {
	lines := Render(text, lang)
	for i := range lines {
		if i < len(marks) {
			lines[i].Mark = marks[i]
		}
		if lines[i].Mark != "" && i < len(inline) {
			lines[i].Segs = applyInline(lines[i].Segs, inline[i], lines[i].Mark)
		}
	}
	return lines
}

// RenderDiff 渲染差异文本，行号按 DiffLineNumberFactory 的口径算。
//
// 注意高亮是**对整段差异文本**做的（含 `+`/`-`/`@@` 前缀），
// 与原版一致 —— 那些前缀不参与语法，但也不会被剥掉。
func RenderDiff(diffText, lang string) []Line {
	spans := Tokenize(diffText, lang)
	if spans == nil {
		spans = []Span{{Start: 0, End: len(diffText)}}
	}
	ranges := paragraphRanges(diffText)
	texts := make([]string, len(ranges))
	for i, r := range ranges {
		texts[i] = diffText[r[0]:r[1]]
	}
	nums := DiffNumbers(texts)
	lines := make([]Line, len(ranges))
	for i, r := range ranges {
		lines[i] = Line{Num: nums[i], Segs: cutSegs(diffText, spans, r[0], r[1])}
	}
	return lines
}

// DiffMarks 给双栏预览的左右两侧逐行打差异标记。
//
// 返回两个切片，长度分别等于 left / right，元素是 MarkMod / MarkAdd /
// MarkDel 或空串。入参必须是**已经按显示口径切好的行**（Paragraphs），
// 这样下标才能与渲染出来的行号一一对应 —— 差异算法那套切行口径
// （util.SplitDiffLines）会去掉尾部空行，下标对不上，别混用。
//
// 标记口径（一个差异块里两边的行数决定它是"改"还是"增/删"）：
//
//	两边都有内容 -> 两侧都标 MarkMod
//	只有左栏     -> 左栏标 MarkDel
//	只有右栏     -> 右栏标 MarkAdd
//
// **这是刻意增强**：原版双栏模式不做任何差异标记（差异标记只出现在
// "差异模式"的单栏行号列上，且文本本身也不着色）。用户明确要求双栏也能
// 一眼看出差异处，故补上，见 MIGRATION.md 的偏差登记。
//
// 只关心行级标记的调用方用它；还要行内（词级）差异的用 ComputeSplitMarks ——
// 两者是同一套算法，这里只是把行级那部分摘出来。
func DiffMarks(left, right []string) (lmarks, rmarks []string) {
	m := ComputeSplitMarks(left, right)
	return m.Left, m.Right
}

// fillMark 从 start 起给 n 行打标记，越界的部分丢掉。
//
// 不做越界断言而是静默截断：差异块的下标由 Deltas 保证落在入参范围内，
// 真越界了也只说明标记少了几行，不值得让预览整个失败。
func fillMark(marks []string, start, n int, mark string) {
	if start < 0 {
		start = 0
	}
	end := start + n
	if end > len(marks) {
		end = len(marks)
	}
	for i := start; i < end; i++ {
		marks[i] = mark
	}
}

// cutSegs 把整段的样式区间裁到 [start, end) 这一行上。
//
// spans 必须覆盖整段文本（Tokenize 保证这一点），所以这里只管裁剪，
// 不用操心"缝隙"。
func cutSegs(text string, spans []Span, start, end int) []Seg {
	var out []Seg
	for _, sp := range spans {
		if sp.End <= start {
			continue
		}
		if sp.Start >= end {
			break
		}
		s, e := sp.Start, sp.End
		if s < start {
			s = start
		}
		if e > end {
			e = end
		}
		if e <= s {
			continue
		}
		out = append(out, Seg{Text: text[s:e], Class: sp.Class})
	}
	return out
}

// isJavaBlank 复刻 Java String.isBlank()。
//
// 注意 Java 里有两个长得很像的东西，**口径不同**，别混用：
//
//	StringUtil.isBlank  -> Character.isSpaceChar（只认 Unicode 的 Zs/Zl/Zp）
//	String.isBlank      -> Character.isWhitespace（还包含 \t \n \v \f \r 等控制字符，
//	                       但**排除** \u00A0 \u2007 \u202F 这三个"不换行空格"）
//
// DiffLineNumberFactory 用的是后者（`text.isBlank()`），所以这里按后者实现。
// 前端那边对齐 README 文案用的是前者（见 app.js 的 isBlank），两者不能统一。
func isJavaBlank(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !isJavaWhitespace(r) {
			return false
		}
	}
	return true
}

// isJavaWhitespace 复刻 Character.isWhitespace。
func isJavaWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', 0x1C, 0x1D, 0x1E, 0x1F:
		return true
	case 0x00A0, 0x2007, 0x202F: // 不换行空格：isSpaceChar 认，isWhitespace 不认
		return false
	}
	return unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}
