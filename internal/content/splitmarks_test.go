package content

import (
	"fmt"
	"strings"
	"testing"
)

func rangesOf(rs []InlineMark) string {
	s := ""
	for i, r := range rs {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf("[%d,%d)", r.Start, r.End)
	}
	return s
}

// 用户要的口径：**只把变了的词染上色**，不是整行。
//
// "var gone = 9;" -> "var extra = 3;" 只有 `gone` 和 `9` 变了，
// `var` / `=` / `;` 以及所有空格都得保持原样（留着语法着色）。
func TestWordDiffRangesOnlyChangedWords(t *testing.T) {
	lr, rr := wordDiffRanges("var gone = 9;", "var extra = 3;")
	if got := rangesOf(toInline(lr)); got != "[4,8),[11,12)" {
		t.Errorf("左侧变化区间 = %s，期望 [4,8),[11,12)（gone 与 9）", got)
	}
	if got := rangesOf(toInline(rr)); got != "[4,9),[12,13)" {
		t.Errorf("右侧变化区间 = %s，期望 [4,9),[12,13)（extra 与 3）", got)
	}
}

// 两行一样 -> 没有任何行内变化（这类行通常连行级标记都不该有）。
func TestWordDiffRangesIdenticalLines(t *testing.T) {
	lr, rr := wordDiffRanges("import java.util.List;", "import java.util.List;")
	if len(lr) != 0 || len(rr) != 0 {
		t.Errorf("相同的行不该有行内变化：左 %v 右 %v", lr, rr)
	}
}

// 只多了一个字符：区间必须精确到那一个字符，不能把整个词算进去。
// （`List` -> `Lists` 在词级切分下是**整词替换**，这里用标点来验精细度。）
func TestWordDiffRangesSingleCharChange(t *testing.T) {
	lr, rr := wordDiffRanges("f(a, b)", "f(a; b)")
	if got := rangesOf(toInline(lr)); got != "[3,4)" {
		t.Errorf("左侧变化区间 = %s，期望 [3,4)（那个逗号）", got)
	}
	if got := rangesOf(toInline(rr)); got != "[3,4)" {
		t.Errorf("右侧变化区间 = %s，期望 [3,4)", got)
	}
}

// 纯插入 / 纯删除的行：整行都是变化。
func TestComputeSplitMarksAddDelAreWholeLine(t *testing.T) {
	left := []string{"a", "gone", "c"}
	right := []string{"a", "c", "new"}

	m := ComputeSplitMarks(left, right)
	if m.Left[1] != MarkDel {
		t.Errorf("左栏第 2 行标记 = %q，期望 %q", m.Left[1], MarkDel)
	}
	if got := rangesOf(m.LeftInline[1]); got != "[0,4)" {
		t.Errorf("删除行的行内区间 = %s，期望 [0,4)（整行）", got)
	}
	if m.Right[2] != MarkAdd {
		t.Errorf("右栏第 3 行标记 = %q，期望 %q", m.Right[2], MarkAdd)
	}
	if got := rangesOf(m.RightInline[2]); got != "[0,3)" {
		t.Errorf("新增行的行内区间 = %s，期望 [0,3)（整行）", got)
	}
	// 没变的行两层标记都不该有
	if m.Left[0] != "" || len(m.LeftInline[0]) != 0 || m.Right[0] != "" || len(m.RightInline[0]) != 0 {
		t.Error("未改动的行不该带任何标记")
	}
}

// 差异块（导航用）的位置必须精确：左栏第 1 行改、左栏第 3 行删。
// 行号是 0 基，没有内容的那一侧 Start = -1。
func TestComputeSplitMarksBlocksPositions(t *testing.T) {
	left := []string{"keep", "L1", "tail", "gone", "end"}
	right := []string{"keep", "R1", "tail", "end"}

	m := ComputeSplitMarks(left, right)
	if got := blocksOf(m.Blocks); got != "mod:1+1/1+1 del:3+1/-1+0" {
		t.Errorf("差异块 = %s，期望 mod:1+1/1+1 del:3+1/-1+0", got)
	}
}

// "只有右栏有"的块：左栏那一侧必须标成 -1，不能写成 0 ——
// 0 是"左栏第 1 行"，前端会去高亮一行根本不存在的差异。
func TestComputeSplitMarksBlocksAddOnly(t *testing.T) {
	left := []string{"a", "b"}
	right := []string{"a", "b", "fresh"}

	m := ComputeSplitMarks(left, right)
	if got := blocksOf(m.Blocks); got != "add:-1+0/2+1" {
		t.Errorf("差异块 = %s，期望 add:-1+0/2+1", got)
	}
}

// 两份一样 -> 一处差异都没有（导航按钮整组隐藏的依据）。
func TestComputeSplitMarksBlocksEmptyWhenIdentical(t *testing.T) {
	m := ComputeSplitMarks([]string{"a", "b"}, []string{"a", "b"})
	if len(m.Blocks) != 0 {
		t.Errorf("两份一样时不该有差异块，实际 %v", m.Blocks)
	}
}

// 关键不变量：**每一个带行级标记的行，都被恰好一个差异块覆盖**。
//
// 前端的高亮完全按块的行范围来（见 app.js 的 markBlockCurrent），
// 覆盖漏一行就是"跳到了却只高亮一半"；覆盖两遍就是两个块指向同一处、
// "下一个差异"点两下才动一次。行级标记与块是同一趟遍历出来的，
// 这条断言就是它们不许漂移的守门人。
func TestComputeSplitMarksBlocksCoverEveryMarkedLine(t *testing.T) {
	left := []string{"a", "b", "c", "d", "e", "f", "g"}
	right := []string{"a", "B", "c", "e", "f", "G", "h"}

	m := ComputeSplitMarks(left, right)
	lc := make([]int, len(m.Left))
	rc := make([]int, len(m.Right))
	for _, b := range m.Blocks {
		for k := 0; k < b.LeftCount; k++ {
			if i := b.LeftStart + k; i < 0 || i >= len(lc) {
				t.Fatalf("块 %+v 的左栏范围越界", b)
			} else {
				lc[i]++
			}
		}
		for k := 0; k < b.RightCount; k++ {
			if i := b.RightStart + k; i < 0 || i >= len(rc) {
				t.Fatalf("块 %+v 的右栏范围越界", b)
			} else {
				rc[i]++
			}
		}
	}
	for i := range m.Left {
		want := 0
		if m.Left[i] != "" {
			want = 1
		}
		if lc[i] != want {
			t.Errorf("左栏第 %d 行被 %d 个差异块覆盖（行级标记=%q，期望覆盖 %d 次）",
				i, lc[i], m.Left[i], want)
		}
	}
	for i := range m.Right {
		want := 0
		if m.Right[i] != "" {
			want = 1
		}
		if rc[i] != want {
			t.Errorf("右栏第 %d 行被 %d 个差异块覆盖（行级标记=%q，期望覆盖 %d 次）",
				i, rc[i], m.Right[i], want)
		}
	}
	if len(m.Blocks) == 0 {
		t.Fatal("这份输入有差异，不该一个块都没有")
	}
}

// blocksOf 把差异块拍成 "mark:leftStart+leftCount/rightStart+rightCount" 便于比对。
func blocksOf(bs []SplitBlock) string {
	var sb strings.Builder
	for i, b := range bs {
		if i > 0 {
			sb.WriteString(" ")
		}
		fmt.Fprintf(&sb, "%s:%d+%d/%d+%d",
			b.Mark, b.LeftStart, b.LeftCount, b.RightStart, b.RightCount)
	}
	return sb.String()
}

// 行级标记与行内标记必须**同时**落在同一行上 —— 行内是靠行级认色的。
func TestComputeSplitMarksInlineOnlyOnMarkedLines(t *testing.T) {
	left := []string{"keep", "var a = 1;", "keep"}
	right := []string{"keep", "var b = 1;", "keep"}

	m := ComputeSplitMarks(left, right)
	for i := range left {
		if len(m.LeftInline[i]) > 0 && m.Left[i] == "" {
			t.Errorf("第 %d 行有行内变化却没有行级标记", i)
		}
		if len(m.RightInline[i]) > 0 && m.Right[i] == "" {
			t.Errorf("右栏第 %d 行有行内变化却没有行级标记", i)
		}
	}
	if m.Left[1] != MarkMod || len(m.LeftInline[1]) == 0 {
		t.Errorf("第 2 行应同时有行级与行内标记：mark=%q inline=%v",
			m.Left[1], m.LeftInline[1])
	}
}

// 用户实测的那个场景：上千行、只差一行。
// 整篇不能被标记，行内也只该落在真正变的那几个词上。
func TestComputeSplitMarksLargeFileWithOneChangedLine(t *testing.T) {
	const n = 1200
	left := make([]string, n)
	right := make([]string, n)
	for i := range left {
		left[i] = fmt.Sprintf("import pkg.Cls%d;", i)
		right[i] = left[i]
	}
	right[600] = "import pkg.Other;"

	m := ComputeSplitMarks(left, right)

	marked := 0
	for i := range left {
		if m.Left[i] != "" {
			marked++
		}
		if m.Right[i] != "" {
			marked++
		}
	}
	if marked != 2 {
		t.Errorf("被标记的行数 = %d，期望 2（只有第 601 行两侧）—— 整篇被标了？", marked)
	}
	if m.Left[600] != MarkMod || m.Right[600] != MarkMod {
		t.Errorf("第 601 行标记 = %q/%q，期望都是 %q", m.Left[600], m.Right[600], MarkMod)
	}
	// `pkg.Cls600` -> `pkg.Other`：变化的只有词 `Cls600` / `Other`
	// （`import pkg.` 是 11 个字节，所以词从偏移 11 开始）
	if got := rangesOf(m.LeftInline[600]); got != "[11,17)" {
		t.Errorf("左栏行内区间 = %s，期望 [11,17)（Cls600）", got)
	}
	if got := rangesOf(m.RightInline[600]); got != "[11,16)" {
		t.Errorf("右栏行内区间 = %s，期望 [11,16)（Other）", got)
	}
}

// 同上的变体：改动落在**第一行**，没有公共前缀可剥。
// 这一条盯的是 util.Deltas 那道闸的**口径** —— 退回 `n+m > maxMyersD`
// 时，1200 行的文件 n+m=2400 会撞闸、整篇被标，这条就会红。
func TestComputeSplitMarksLargeFileChangedAtVeryStart(t *testing.T) {
	const n = 1200
	left := make([]string, n)
	right := make([]string, n)
	for i := range left {
		left[i] = fmt.Sprintf("import pkg.Cls%d;", i)
		right[i] = left[i]
	}
	left[0] = "import pkg.First;"

	m := ComputeSplitMarks(left, right)

	marked := 0
	for i := range left {
		if m.Left[i] != "" {
			marked++
		}
		if m.Right[i] != "" {
			marked++
		}
	}
	if marked != 2 {
		t.Errorf("被标记的行数 = %d，期望 2（只有第 1 行两侧）—— 整篇被标了？", marked)
	}
	if m.Left[0] != MarkMod || m.Right[0] != MarkMod {
		t.Errorf("第 1 行标记 = %q/%q，期望都是 %q", m.Left[0], m.Right[0], MarkMod)
	}
}

// 一个语法片段被变化区间从中间切开时，要切成"前 / 中 / 后"三段，
// 只有中间那段追加行内类名 —— 否则整段（可能整行）都会变色。
func TestApplyInlineSplitsSegment(t *testing.T) {
	segs := []Seg{{Text: "abcdefgh", Class: "keyword"}}
	got := applyInline(segs, []InlineMark{{Start: 2, End: 5}}, MarkMod)

	if len(got) != 3 {
		t.Fatalf("片段数 = %d，期望 3：%+v", len(got), got)
	}
	want := []Seg{
		{Text: "ab", Class: "keyword"},
		{Text: "cde", Class: "keyword mk-mod"},
		{Text: "fgh", Class: "keyword"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 段 = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}

// 变化区间跨两个语法片段时，两个片段各自被切，且区间下标只前进不回退。
func TestApplyInlineAcrossSegments(t *testing.T) {
	segs := []Seg{
		{Text: "ab", Class: "keyword"},
		{Text: "cd", Class: ""},
		{Text: "ef", Class: "string"},
	}
	// "abcdef" 上的 [1,5) 覆盖 "bcde" —— 正好横跨三个片段
	got := applyInline(segs, []InlineMark{{Start: 1, End: 5}}, MarkMod)
	want := []Seg{
		{Text: "a", Class: "keyword"},
		{Text: "b", Class: "keyword mk-mod"},
		{Text: "cd", Class: "mk-mod"},
		{Text: "e", Class: "string mk-mod"},
		{Text: "f", Class: "string"},
	}
	if len(got) != len(want) {
		t.Fatalf("片段数 = %d，期望 %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 段 = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}

// 区间正好落在片段边界上：不能切出一个空片段（空 <span> 白占 DOM，
// 而且行内高亮的断言会数错）。
func TestApplyInlineAtSegmentBoundary(t *testing.T) {
	segs := []Seg{{Text: "ab", Class: ""}, {Text: "cd", Class: ""}}
	got := applyInline(segs, []InlineMark{{Start: 2, End: 3}}, MarkMod)
	want := []Seg{
		{Text: "ab", Class: ""},
		{Text: "c", Class: "mk-mod"},
		{Text: "d", Class: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("片段数 = %d，期望 %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 段 = %+v，期望 %+v", i, got[i], want[i])
		}
	}
	for _, sg := range got {
		if sg.Text == "" {
			t.Errorf("出现了空片段：%+v", got)
		}
	}
}

// 没有行内区间时，片段必须原样返回（不能平白多出 span）。
func TestApplyInlineNoRangesIsNoop(t *testing.T) {
	segs := []Seg{{Text: "abc", Class: "keyword"}}
	got := applyInline(segs, nil, MarkMod)
	if len(got) != 1 || got[0] != segs[0] {
		t.Errorf("无区间时片段被改动了：%+v", got)
	}
}

// 端到端：RenderMarked 收到行内区间后，只有变了的词带 mk-* 类名，
// 其余片段保留语法类名。
func TestRenderMarkedAppliesInline(t *testing.T) {
	text := "var n = 1;\n"
	lines := RenderMarked(text, LangJavaScript, []string{MarkMod}, [][]InlineMark{{
		{Start: 8, End: 9}, // 那个 "1"
	}})
	if len(lines) != 2 {
		t.Fatalf("行数 = %d，期望 2", len(lines))
	}
	var marked, plain []string
	for _, sg := range lines[0].Segs {
		if sg.Class == "mk-mod" || sg.Class == "keyword mk-mod" {
			marked = append(marked, sg.Text)
		} else {
			plain = append(plain, sg.Text)
		}
	}
	if len(marked) != 1 || marked[0] != "1" {
		t.Errorf("被行内标记的片段 = %v，期望只有 [1]", marked)
	}
	if len(plain) == 0 {
		t.Error("其余片段不该被丢掉")
	}
	// `var` 是 keyword，必须还带着语法类名（否则语法着色就没了）
	found := false
	for _, sg := range lines[0].Segs {
		if sg.Text == "var" && sg.Class == "keyword" {
			found = true
		}
	}
	if !found {
		t.Errorf("`var` 的语法类名丢了：%+v", lines[0].Segs)
	}
}

// 行级标记为空时，即使给了行内区间也不该贴 —— 两者自相矛盾，
// 而且前端是按行级标记认色的。
//
// 注意这里判的是"有没有 mk- 前缀"，不是"等不等于 mk-mod"：
// 第一版写成等值比较，结果把行级标记去掉后类名变成 `mk-`（空的 mark），
// 断言照样绿 —— 变异验证时"没变红"才发现的。
func TestRenderMarkedIgnoresInlineWithoutLineMark(t *testing.T) {
	text := "var n = 1;\n"
	lines := RenderMarked(text, LangJavaScript, []string{""}, [][]InlineMark{{
		{Start: 8, End: 9},
	}})
	for _, sg := range lines[0].Segs {
		if strings.Contains(sg.Class, "mk-") {
			t.Errorf("没有行级标记却贴了行内标记：%+v", lines[0].Segs)
		}
	}
}

// 空行没有可着色的字符：整行区间应为空，免得前端多出一个空 span。
func TestWholeLineEmptyString(t *testing.T) {
	if r := wholeLine(""); r != nil {
		t.Errorf("空行的整行区间 = %v，期望 nil", r)
	}
	if r := wholeLine("ab"); len(r) != 1 || r[0] != [2]int{0, 2} {
		t.Errorf("整行区间 = %v，期望 [[0 2]]", r)
	}
}

// 中文等多字节字符：区间是**字节**偏移，切出来不能半个字符。
func TestWordDiffRangesMultibyte(t *testing.T) {
	lr, _ := wordDiffRanges("String 名字 = 甲;", "String 名字 = 乙;")
	if len(lr) != 1 {
		t.Fatalf("左侧变化区间数 = %d，期望 1：%v", len(lr), lr)
	}
	src := "String 名字 = 甲;"
	got := src[lr[0][0]:lr[0][1]]
	if got != "甲" {
		t.Errorf("切出来的片段 = %q，期望 %q（不能切半个字符）", got, "甲")
	}
}

func toInline(rs [][2]int) []InlineMark {
	out := make([]InlineMark, len(rs))
	for i, r := range rs {
		out[i] = InlineMark{Start: r[0], End: r[1]}
	}
	return out
}
