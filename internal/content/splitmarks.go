package content

import (
	"sort"
	"unicode"
	"unicode/utf8"

	"cn.cpoet.patch.assistant/internal/util"
)

// InlineMark 一行里的一段"行内差异"，字节区间 [Start, End)。
//
// 行内差异与行级差异（Line.Mark）是两层：行级说"这一行属于某个差异块"，
// 行内说"这一行里具体哪几个词变了"。界面上行级落成行号列那条色条，
// 行内落成正文里那几个词的文字色 —— 其余文字保留语法着色。
type InlineMark struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SplitMarks 双栏预览的全部差异标记：行级 + 行内 + 差异块。
type SplitMarks struct {
	// Left / Right 逐行的行级标记，取值 MarkMod / MarkAdd / MarkDel / ""。
	Left  []string `json:"left"`
	Right []string `json:"right"`
	// LeftInline / RightInline 逐行的行内差异区间，与 Left / Right 等长。
	LeftInline  [][]InlineMark `json:"leftInline"`
	RightInline [][]InlineMark `json:"rightInline"`
	// Blocks 差异块（按行序），供前端的"上一个/下一个差异"导航用。
	Blocks []SplitBlock `json:"blocks"`
}

// SplitBlock 是双栏预览里的一处差异：它在左右两栏各占哪几行。
//
// 一个差异块就是一个 delta（Deltas 已把连续的删+增收成一个块），所以
// Blocks 天然是"用户眼里的一处差异"，与行级标记的连通段一一对应。
//
// 为什么由后端给而不是前端扫 DOM 自己配对：一个块里左右各占几行**只有
// 差异算法知道**（"改"块里左第 k 行配右第 k 行）。让前端去猜，等于把
// "哪几行属于同一处差异"这个口径复制一份到两个地方，迟早漂移 ——
// 与行内标记只在 ComputeSplitMarks 里算一次是同一个理由。
//
// 行号是 **0 基**，与 Left / Right / LeftInline 的下标同一套；某侧没有内容时
// Start = -1、Count = 0（"仅右栏有"的块左栏就是这种情况）。
type SplitBlock struct {
	// Mark 取值 MarkMod / MarkAdd / MarkDel（与行级标记同一套语义）。
	Mark       string `json:"mark"`
	LeftStart  int    `json:"leftStart"`
	LeftCount  int    `json:"leftCount"`
	RightStart int    `json:"rightStart"`
	RightCount int    `json:"rightCount"`
}

// ComputeSplitMarks 一趟算出双栏预览需要的三种差异标记
// （逐行行级标记、逐行行内区间、差异块列表）。
//
// 一次遍历而不是算两遍：行内差异要靠差异块内部的**行配对**才能算
// （见下），而配对关系只有遍历差异块时才知道。
//
// 行内配对的口径：一个"改"块里左边第 k 行配右边第 k 行，块内多出来的行
// 整行算变化。这是并排差异视图的通行做法 —— 块内本来就没有共同行
// （有的话会被切成两个块），逐行对齐是最合理的猜测。
//
// 纯增 / 纯删的行整行算变化（那一行整个都是新的 / 被删掉的）。
//
// 顺便把每个差异块在左右两栏的行范围收进 Blocks —— 同一趟遍历里顺手记，
// 不必再跑一次差异算法（见 SplitBlock）。
func ComputeSplitMarks(left, right []string) SplitMarks {
	m := SplitMarks{
		Left:        make([]string, len(left)),
		Right:       make([]string, len(right)),
		LeftInline:  make([][]InlineMark, len(left)),
		RightInline: make([][]InlineMark, len(right)),
	}
	for _, d := range util.Deltas(left, right) {
		switch {
		case len(d.OrigLines) > 0 && len(d.RevLines) > 0:
			fillMark(m.Left, d.OrigStart, len(d.OrigLines), MarkMod)
			fillMark(m.Right, d.RevStart, len(d.RevLines), MarkMod)
			m.Blocks = append(m.Blocks, SplitBlock{
				Mark:      MarkMod,
				LeftStart: d.OrigStart, LeftCount: len(d.OrigLines),
				RightStart: d.RevStart, RightCount: len(d.RevLines),
			})

			paired := len(d.OrigLines)
			if len(d.RevLines) < paired {
				paired = len(d.RevLines)
			}
			for k := 0; k < paired; k++ {
				lr, rr := wordDiffRanges(d.OrigLines[k], d.RevLines[k])
				setInline(m.LeftInline, d.OrigStart+k, lr)
				setInline(m.RightInline, d.RevStart+k, rr)
			}
			// 块内多出来的行：整行都是变化
			for k := paired; k < len(d.OrigLines); k++ {
				setInline(m.LeftInline, d.OrigStart+k, wholeLine(d.OrigLines[k]))
			}
			for k := paired; k < len(d.RevLines); k++ {
				setInline(m.RightInline, d.RevStart+k, wholeLine(d.RevLines[k]))
			}

		case len(d.OrigLines) > 0:
			fillMark(m.Left, d.OrigStart, len(d.OrigLines), MarkDel)
			// 只有左栏有 -> 右栏没这一块（Start = -1）
			m.Blocks = append(m.Blocks, SplitBlock{
				Mark:      MarkDel,
				LeftStart: d.OrigStart, LeftCount: len(d.OrigLines),
				RightStart: -1,
			})
			for k, s := range d.OrigLines {
				setInline(m.LeftInline, d.OrigStart+k, wholeLine(s))
			}

		default:
			fillMark(m.Right, d.RevStart, len(d.RevLines), MarkAdd)
			// 只有右栏有 -> 左栏没这一块（Start = -1）
			m.Blocks = append(m.Blocks, SplitBlock{
				Mark:       MarkAdd,
				LeftStart:  -1,
				RightStart: d.RevStart, RightCount: len(d.RevLines),
			})
			for k, s := range d.RevLines {
				setInline(m.RightInline, d.RevStart+k, wholeLine(s))
			}
		}
	}
	return m
}

// setInline 写行内标记，越界静默丢弃（与 fillMark 同一口径：
// 标记是纯装饰，对不上顶多少高亮几行，不该让预览整个失败）。
func setInline(dst [][]InlineMark, i int, rs [][2]int) {
	if i < 0 || i >= len(dst) || len(rs) == 0 {
		return
	}
	out := make([]InlineMark, len(rs))
	for k, r := range rs {
		out[k] = InlineMark{Start: r[0], End: r[1]}
	}
	dst[i] = out
}

// wholeLine 整行都算变化；空行没有可着色的字符，返回 nil。
func wholeLine(s string) [][2]int {
	if s == "" {
		return nil
	}
	return [][2]int{{0, len(s)}}
}

// wordDiffRanges 求两行之间"具体哪几段文字不一样"，返回各自行内的字节区间。
//
// 做法是把两行都切成词（见 wordTokens），对词序列跑同一套 Myers，
// 再把"变了的那些词"映射回字节区间。复用 util.Deltas 而不是另写一个
// 差异算法：行级与词级用同一套选路，行为才可预期。
func wordDiffRanges(a, b string) ([][2]int, [][2]int) {
	if a == b {
		return nil, nil
	}
	ta, oa := wordTokens(a)
	tb, ob := wordTokens(b)

	var ra, rb [][2]int
	for _, d := range util.Deltas(ta, tb) {
		if n := len(d.OrigLines); n > 0 {
			ra = append(ra, [2]int{oa[d.OrigStart][0], oa[d.OrigStart+n-1][1]})
		}
		if n := len(d.RevLines); n > 0 {
			rb = append(rb, [2]int{ob[d.RevStart][0], ob[d.RevStart+n-1][1]})
		}
	}
	return mergeRanges(ra), mergeRanges(rb)
}

// wordTokens 把一行切成"词"，并给出每个词在行内的字节区间。
//
// 切法：连续的标识符字符（字母 / 数字 / _ / $）算一个词，连续的空白算一个词，
// 其余每个字符各算一个词。这样 `fooBar` → `fooBaz` 的差异落在词内
// （`fooBar` / `fooBaz` 整词被标），`a + b` → `a - b` 只标那个运算符 ——
// 正是"只把变了的关键词染上色"。
func wordTokens(s string) ([]string, [][2]int) {
	toks := make([]string, 0, 16)
	offs := make([][2]int, 0, 16)
	for i := 0; i < len(s); {
		start := i
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case isWordRune(r):
			for i < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[i:])
				if !isWordRune(r2) {
					break
				}
				i += sz
			}
		case r == ' ' || r == '\t':
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
		default:
			i += size
		}
		toks = append(toks, s[start:i])
		offs = append(offs, [2]int{start, i})
	}
	return toks, offs
}

// isWordRune 判定"标识符字符"。与语法高亮的分词口径无关 ——
// 这里只关心"差异落在哪个词上"，粗一点更好（切太细会让高亮碎成一片）。
func isWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// mergeRanges 合并重叠或首尾相接的区间。
//
// 词与词之间的空隙（比如被删掉的空格）会让相邻区间首尾相接，
// 不合并就会渲染成一堆紧挨着的小 <span>，白白多出 DOM。
func mergeRanges(rs [][2]int) [][2]int {
	if len(rs) < 2 {
		return rs
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i][0] < rs[j][0] })
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r[0] <= last[1] {
			if r[1] > last[1] {
				last[1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// applyInline 把行内差异区间贴到语法片段上。
//
// 被区间覆盖的部分在原有语法类名后面追加 ` mk-<mark>`，其余部分原样保留 ——
// 所以"变了的词"染状态色、"没变的词"继续显示语法着色，两者共存。
// 一个语法片段可能被区间切成三段（前 / 中 / 后），这里逐段切开。
//
// 注意**不能**只给整行加个类名了事：那样要么整行变色（用户否掉的方案），
// 要么语法着色被盖掉。
func applyInline(segs []Seg, ranges []InlineMark, mark string) []Seg {
	if len(ranges) == 0 || len(segs) == 0 {
		return segs
	}
	extra := "mk-" + mark
	out := make([]Seg, 0, len(segs)+len(ranges)*2)
	base := 0 // 当前片段在行内的起始字节偏移
	ri := 0   // ranges 的下标，只前进不回退
	for _, sg := range segs {
		s, e := base, base+len(sg.Text)
		base = e
		cur := s
		for ri < len(ranges) && ranges[ri].End <= cur {
			ri++
		}
		for cur < e && ri < len(ranges) && ranges[ri].Start < e {
			r := ranges[ri]
			if r.Start > cur {
				out = append(out, Seg{Text: sg.Text[cur-s : r.Start-s], Class: sg.Class})
				cur = r.Start
			}
			stop := r.End
			if stop > e {
				stop = e
			}
			out = append(out, Seg{Text: sg.Text[cur-s : stop-s], Class: withClass(sg.Class, extra)})
			cur = stop
			if r.End <= e {
				ri++
			}
		}
		if cur < e {
			out = append(out, Seg{Text: sg.Text[cur-s : e-s], Class: sg.Class})
		}
	}
	return out
}

// withClass 在原有类名后面追加一个类名（原有为空就只留新的）。
func withClass(base, extra string) string {
	if base == "" {
		return extra
	}
	return base + " " + extra
}
