package content

import (
	"strconv"
	"strings"
	"testing"
)

// TestParagraphsMatchesJavaFX 把 ParaProbe 量出来的 JavaFX 段落口径钉死。
//
// 这些期望值**不是**推导出来的，是跑原版 CodeArea 打印出来的
// （见 _extract/tools/ParaProbe.java）。改动这里之前先回去重跑探针。
func TestParagraphsMatchesJavaFX(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		// 末尾换行 -> 多一个空段落（实测确认，别"顺手"去掉）
		{"a\nb\n", []string{"a", "b", ""}},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"a\nb\nc\n", []string{"a", "b", "c", ""}},
		// 空串是 1 段，不是 0 段
		{"", []string{""}},
		{"\n", []string{"", ""}},
		{"\n\n", []string{"", "", ""}},
		{"a\n\nb", []string{"a", "", "b"}},
		// \r\n 算一个换行，\r 也算换行
		{"a\r\nb", []string{"a", "b"}},
		{"a\r\nb\r\n", []string{"a", "b", ""}},
		{"a\rb", []string{"a", "b"}},
		// 这几个实测**不算**换行，是普通字符
		{"a\u0085b", []string{"a\u0085b"}},
		{"a\u2028b", []string{"a\u2028b"}},
		{"a\u2029b", []string{"a\u2029b"}},
		{"a\u000bb", []string{"a\u000bb"}},
		{"a\u000cb", []string{"a\u000cb"}},
	}
	for _, c := range cases {
		got := Paragraphs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("Paragraphs(%q) 段数 = %d，期望 %d（%q）", c.in, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Paragraphs(%q)[%d] = %q，期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestParagraphsDiffersFromDiffSplit 明确两套切行口径**不一样**，
// 防止将来有人"统一"它们。
func TestParagraphsDiffersFromDiffSplit(t *testing.T) {
	// 显示口径保留末尾空段落
	if got := Paragraphs("a\nb\n"); len(got) != 3 {
		t.Errorf("Paragraphs(\"a\\nb\\n\") 应保留末尾空段落（3 段），实际 %d 段", len(got))
	}
	// 差异口径去掉末尾空段落，且只认 \n / \r\n
	if got := Paragraphs("a\rb"); len(got) != 2 {
		t.Errorf("Paragraphs(\"a\\rb\") 应认 \\r 为换行（2 段），实际 %d 段", len(got))
	}
}

// TestPlainNumbersPadding 钉住行号补宽口径。
//
// 对应 Java `LineNumberFactory.format(idx+1, nParagraphs)`：
// 宽度 = nParagraphs 的十进制位数，右侧对齐、左侧补空格。
func TestPlainNumbersPadding(t *testing.T) {
	cases := []struct {
		n    int
		want []string
	}{
		{1, []string{"1"}},
		{9, []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
		{10, []string{" 1", " 2", " 3", " 4", " 5", " 6", " 7", " 8", " 9", "10"}},
		{11, []string{" 1", " 2", " 3", " 4", " 5", " 6", " 7", " 8", " 9", "10", "11"}},
	}
	for _, c := range cases {
		got := PlainNumbers(c.n)
		if len(got) != len(c.want) {
			t.Errorf("PlainNumbers(%d) 长度 = %d，期望 %d", c.n, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("PlainNumbers(%d)[%d] = %q，期望 %q", c.n, i, got[i], c.want[i])
			}
		}
	}
	// 1000 行：宽度必须是 4（用整数位数算，避开 log10(1000) 的浮点误差）
	nums := PlainNumbers(1000)
	if nums[999] != "1000" {
		t.Errorf("PlainNumbers(1000) 末行 = %q，期望 \"1000\"", nums[999])
	}
	if nums[0] != "   1" {
		t.Errorf("PlainNumbers(1000) 首行 = %q，期望 \"   1\"（宽度 4）", nums[0])
	}
}

// TestRenderSegsCoverLine 校验渲染不变量：每行的片段按顺序拼起来必须等于该行原文。
//
// 这条不变量是前端正确性的前提 —— 前端就是按片段直接拼 <span> 的，
// 一旦有字符被漏掉或重复，界面上就会出现"内容对不上"的诡异现象。
func TestRenderSegsCoverLine(t *testing.T) {
	texts := []struct {
		text string
		lang string
	}{
		{"public class A { /* c */ }", LangJava},
		{"/* 跨行\n注释 */ int x;", LangJava},
		{"a\r\nb\r\n", LangJava},
		{"", LangJava},
		{"\n\n", LangJava},
		{".a { color: red; }\n#b { align-items: x; }", LangCSS},
		{"<div a=\"1\">t</div>", LangHTML},
		{"k: 1\ntrue: no", LangYAML},
		{"SELECT a FROM t;", LangSQL},
		{"任意文本 {[( 1 )]}", LangPlain},
		{strings.Repeat("public x;", 100), LangJava}, // 含大量记号
	}
	for _, c := range texts {
		lines := Render(c.text, c.lang)
		paras := Paragraphs(c.text)
		if len(lines) != len(paras) {
			t.Errorf("Render(%q,%s) 行数 = %d，段落数 = %d", c.text, c.lang, len(lines), len(paras))
			continue
		}
		for i, l := range lines {
			var sb strings.Builder
			for _, s := range l.Segs {
				sb.WriteString(s.Text)
			}
			if sb.String() != paras[i] {
				t.Errorf("Render(%q,%s) 第 %d 行片段拼不回原文\n 期望 %q\n 实际 %q",
					c.text, c.lang, i, paras[i], sb.String())
			}
		}
	}
}

// TestRenderDiffSegsCoverLine 差异渲染的同一不变量。
func TestRenderDiffSegsCoverLine(t *testing.T) {
	diff := "--- L.txt\n+++ R.txt\n@@ -0,0 +0,0 @@\n a\n@@ -2,1 +2,1 @@\n-b\n+x\n c"
	lines := RenderDiff(diff, LangPlain)
	paras := Paragraphs(diff)
	if len(lines) != len(paras) {
		t.Fatalf("行数 = %d，段落数 = %d", len(lines), len(paras))
	}
	for i, l := range lines {
		var sb strings.Builder
		for _, s := range l.Segs {
			sb.WriteString(s.Text)
		}
		if sb.String() != paras[i] {
			t.Errorf("第 %d 行拼不回原文: 期望 %q 实际 %q", i, paras[i], sb.String())
		}
	}
}

// TestDiffMarks 钉住双栏差异标记的口径。
//
// **这是刻意增强** —— 原版双栏不做任何标记，所以没有 Java 侧的期望值可抄。
// 期望值是按 DiffMarks 的注释推出来的；改口径就得同时改这里和 MIGRATION.md。
func TestDiffMarks(t *testing.T) {
	cases := []struct {
		name      string
		left      []string
		right     []string
		wantLeft  []string
		wantRight []string
	}{
		{
			// 完全一致：两边都不标
			name:      "identical",
			left:      []string{"a", "b"},
			right:     []string{"a", "b"},
			wantLeft:  []string{"", ""},
			wantRight: []string{"", ""},
		},
		{
			// 一行换成另一行：差异块两侧都有内容 -> 两侧都标 mod
			name:      "modified",
			left:      []string{"a", "old", "c"},
			right:     []string{"a", "new", "c"},
			wantLeft:  []string{"", MarkMod, ""},
			wantRight: []string{"", MarkMod, ""},
		},
		{
			// 左栏多一行：只有左栏标 del
			name:      "deleted",
			left:      []string{"a", "gone", "b"},
			right:     []string{"a", "b"},
			wantLeft:  []string{"", MarkDel, ""},
			wantRight: []string{"", ""},
		},
		{
			// 右栏多一行：只有右栏标 add
			name:      "added",
			left:      []string{"a", "b"},
			right:     []string{"a", "extra", "b"},
			wantLeft:  []string{"", ""},
			wantRight: []string{"", MarkAdd, ""},
		},
		{
			// 三档同时出现，且各自落在自己那一侧
			name:      "all three",
			left:      []string{"old", "same", "gone", "keep", "tail"},
			right:     []string{"new", "same", "keep", "extra", "tail"},
			wantLeft:  []string{MarkMod, "", MarkDel, "", ""},
			wantRight: []string{MarkMod, "", "", MarkAdd, ""},
		},
		{
			// 头尾整体替换：整段两侧都非空 -> 全 mod
			name:      "whole replaced",
			left:      []string{"a", "b"},
			right:     []string{"x", "y"},
			wantLeft:  []string{MarkMod, MarkMod},
			wantRight: []string{MarkMod, MarkMod},
		},
	}
	for _, c := range cases {
		lmarks, rmarks := DiffMarks(c.left, c.right)
		if got, want := strings.Join(lmarks, ","), strings.Join(c.wantLeft, ","); got != want {
			t.Errorf("%s: 左栏标记 = %q，期望 %q", c.name, got, want)
		}
		if got, want := strings.Join(rmarks, ","), strings.Join(c.wantRight, ","); got != want {
			t.Errorf("%s: 右栏标记 = %q，期望 %q", c.name, got, want)
		}
		// 长度必须与入参一一对应，否则前端下标会错位
		if len(lmarks) != len(c.left) || len(rmarks) != len(c.right) {
			t.Errorf("%s: 标记长度 = %d/%d，期望 %d/%d",
				c.name, len(lmarks), len(rmarks), len(c.left), len(c.right))
		}
	}
}

// TestDiffMarksAlignsWithRender 确认标记的下标与渲染出来的行号一一对应。
//
// 这是双栏高亮正确性的前提：DiffMarks 收的是 Paragraphs 切出来的行，
// RenderMarked 内部也按 Paragraphs 渲染，两边口径必须一致。**不能用**
// util.SplitDiffLines —— 它会去掉尾部空行，源码文件基本都以换行结尾，
// 一错位就是整栏高亮漂移。
func TestDiffMarksAlignsWithRender(t *testing.T) {
	left := "a\nb\nc\n"  // 4 段（末尾空段落）
	right := "a\nx\nc\n" // 4 段
	lmarks, rmarks := DiffMarks(Paragraphs(left), Paragraphs(right))
	ll := RenderMarked(left, LangPlain, lmarks, nil)
	rl := RenderMarked(right, LangPlain, rmarks, nil)
	if len(ll) != 4 || len(rl) != 4 {
		t.Fatalf("行数 = %d/%d，期望 4/4", len(ll), len(rl))
	}
	if ll[1].Mark != MarkMod || rl[1].Mark != MarkMod {
		t.Errorf("第 2 行标记 = %q/%q，期望都是 %q", ll[1].Mark, rl[1].Mark, MarkMod)
	}
	if ll[0].Mark != "" || ll[2].Mark != "" || rl[0].Mark != "" || rl[2].Mark != "" {
		t.Error("未改动的行不该带标记")
	}
	// 行号与不带标记时完全一致
	plain := Render(left, LangPlain)
	for i := range plain {
		if ll[i].Num != plain[i].Num {
			t.Errorf("第 %d 行行号 = %q，期望 %q", i, ll[i].Num, plain[i].Num)
		}
	}
}

// TestRenderMarked 校验标记贴到正确的行上，且 marks 长度对不上时不出错。
func TestRenderMarked(t *testing.T) {
	text := "a\nb\nc\n" // 4 段
	// marks 比段落数少一个：多出来那行按"无差异"处理，不该报错也不该丢行
	lines := RenderMarked(text, LangPlain, []string{MarkMod, "", MarkAdd}, nil)
	if len(lines) != 4 {
		t.Fatalf("行数 = %d，期望 4", len(lines))
	}
	want := []string{MarkMod, "", MarkAdd, ""}
	for i, l := range lines {
		if l.Mark != want[i] {
			t.Errorf("第 %d 行标记 = %q，期望 %q", i, l.Mark, want[i])
		}
	}
	// nil marks 也不该炸
	if got := RenderMarked(text, LangPlain, nil, nil); len(got) != 4 {
		t.Errorf("marks 为 nil 时行数 = %d，期望 4", len(got))
	}
}

// TestSupportMatrix 钉住扩展名支持矩阵，尤其是那几处"看起来像漏了"的不对称。
func TestSupportMatrix(t *testing.T) {
	// 能预览、也能高亮
	both := map[string]string{
		"java": LangJava, "js": LangJavaScript, "html": LangHTML, "htm": LangHTML,
		"css": LangCSS, "xml": LangXML, "yml": LangYAML, "yaml": LangYAML, "sql": LangSQL,
	}
	for ext, lang := range both {
		if got := ParserForExt(ext); got != ParserText {
			t.Errorf("ParserForExt(%q) = %v，期望 ParserText", ext, got)
		}
		if got := LangForExt(ext); got != lang {
			t.Errorf("LangForExt(%q) = %q，期望 %q", ext, got, lang)
		}
	}

	// 能预览、但**没有**高亮 —— 原版就是这样，别"补上"
	textOnly := []string{"txt", "properties", "json", "sign"}
	for _, ext := range textOnly {
		if got := ParserForExt(ext); got != ParserText {
			t.Errorf("ParserForExt(%q) = %v，期望 ParserText", ext, got)
		}
		if got := LangForExt(ext); got != LangPlain {
			t.Errorf("LangForExt(%q) = %q，期望 %q（原版落到 DefaultCodeAreaFactory）", ext, got, LangPlain)
		}
	}

	// class：能预览（靠反编译），高亮按 java 的规则
	if got := ParserForExt("class"); got != ParserClass {
		t.Errorf("ParserForExt(\"class\") = %v，期望 ParserClass", got)
	}
	if got := LangForExt("class"); got != LangJava {
		t.Errorf("LangForExt(\"class\") = %q，期望 %q", got, LangJava)
	}

	// 没有解析器：原版交给系统程序打开
	none := []string{"", "png", "jar", "zip", "md", "log", "exe", "JAVA", "TXT"}
	for _, ext := range none {
		if got := ParserForExt(ext); got != ParserNone {
			t.Errorf("ParserForExt(%q) = %v，期望 ParserNone", ext, got)
		}
	}
	// 大小写敏感：只有小写写法在表里
	if got := LangForExt("JAVA"); got != LangPlain {
		t.Errorf("LangForExt(\"JAVA\") = %q，期望 %q（原版大小写敏感）", got, LangPlain)
	}
}

// TestHighlightSizeGate 钉住 64KB 这个"块大小"的两侧。
//
// ⚠️ 这里与 Java 版**刻意不同**：原版超过 64KB 返回 null（整篇不高亮），
// 于是反编译出来的大 .class 在双栏里全是黑字（用户 2026-10-26 报障）。
// Go 版把 64KB 降级成**切块大小**，超过照样有高亮 —— 见 highlight.go 的
// maxHighlightBytes 注释。所以这里断言的是"仍然有区间"，不是"返回 nil"。
func TestHighlightSizeGate(t *testing.T) {
	// 恰好 64KB：高亮
	at := strings.Repeat("a", maxHighlightBytes)
	if spans := Tokenize(at, LangJava); spans == nil {
		t.Errorf("恰好 %d 字节应当仍然高亮，却返回了 nil", maxHighlightBytes)
	}
	// 超一个字节：仍然高亮（走切块），而且区间必须覆盖到文本末尾
	over := strings.Repeat("a", maxHighlightBytes+1)
	if spans := Tokenize(over, LangJava); spans == nil {
		t.Fatalf("超过 %d 字节应当走切块、仍然高亮，却返回了 nil", maxHighlightBytes)
	} else if last := spans[len(spans)-1]; last.End != len(over) {
		t.Errorf("切块后最后一段应当到 %d，实际到 %d", len(over), last.End)
	}
	// 空文本：不高亮
	if spans := Tokenize("", LangJava); spans != nil {
		t.Errorf("空文本应当返回 nil，却返回了 %d 个区间", len(spans))
	}
	// 没有规则的语言：不高亮
	if spans := Tokenize("anything", LangPlain); spans != nil {
		t.Errorf("plain 应当返回 nil，却返回了 %d 个区间", len(spans))
	}
}

// TestHighlightLargeFileKeepsKeywords 大文件不仅要"有区间"，还得**真的把
// 关键字认出来**。只断言非 nil 是不够的：全篇一个无样式区间也能过。
func TestHighlightLargeFileKeepsKeywords(t *testing.T) {
	// 1200 行、约 77KB —— 正好落在原版"直接放弃"的区间里
	var b strings.Builder
	for i := 0; i < 1200; i++ {
		b.WriteString("    private static final String FIELD_")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" = \"value\";  // 注释\n")
	}
	src := b.String()
	if len(src) <= maxHighlightBytes {
		t.Fatalf("样本只有 %d 字节，没越过 %d 的块边界，测不到切块路径",
			len(src), maxHighlightBytes)
	}

	spans := Tokenize(src, LangJava)
	kw, str, cm := 0, 0, 0
	for _, sp := range spans {
		switch sp.Class {
		case "keyword":
			kw++
		case "string":
			str++
		case "comment":
			cm++
		}
	}
	if kw == 0 || str == 0 || cm == 0 {
		t.Fatalf("大文件高亮丢了记号：keyword=%d string=%d comment=%d", kw, str, cm)
	}
	// 每行三个关键字（private / static / final）、一个字符串、一个注释
	if kw != 3*1200 || str != 1200 || cm != 1200 {
		t.Errorf("记号数量对不上：keyword=%d string=%d comment=%d，期望 3600/1200/1200",
			kw, str, cm)
	}
	// 区间必须连续覆盖全文，不能因为切块留下空洞
	if spans[0].Start != 0 || spans[len(spans)-1].End != len(src) {
		t.Errorf("区间没有覆盖全文：首 %d 末 %d，文本长度 %d",
			spans[0].Start, spans[len(spans)-1].End, len(src))
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start != spans[i-1].End {
			t.Fatalf("第 %d 段与上一段之间有空洞：%d != %d",
				i, spans[i].Start, spans[i-1].End)
		}
	}
}
