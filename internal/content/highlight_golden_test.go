package content

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 跨语言黄金基准：语法高亮。
//
// 语料与参考输出由 _extract/tools 下的工具生成：
//
//	python make_hlcases.py                              -> hlcases.tsv
//	java -cp "<cp>;." HighlightCorpus hlcases.tsv hl_ref.tsv  -> hl_ref.tsv
//
// hl_ref.tsv 里存的是**原版** `CodeAreaFactory.computeHighlighting` 的真实
// 结果，按"样式类 + 片段文本"成对记录（不用长度，避免 Java 的 UTF-16 码元
// 与 Go 的字节对不上）。这里逐条比对，任一条对不上就是移植错了。
//
// 语料文件不在时跳过，不误报失败。

var goldenDir = filepath.Join("..", "..", "..", "_extract", "tools")

func unescapeCorpus(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func escapeCorpus(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// readLines 读一个文件的所有非空行（语料里有 64KB 的长行，缓冲区要够大）。
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", path, err)
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	return out
}

// TestHighlightGoldenAgainstJava 全量比对语法高亮。
func TestHighlightGoldenAgainstJava(t *testing.T) {
	casesPath := filepath.Join(goldenDir, "hlcases.tsv")
	refPath := filepath.Join(goldenDir, "hl_ref.tsv")
	if _, err := os.Stat(casesPath); err != nil {
		t.Skipf("语料不在，跳过跨语言比对: %v", err)
	}
	if _, err := os.Stat(refPath); err != nil {
		t.Skipf("参考输出不在，跳过跨语言比对: %v", err)
	}

	cases := readLines(t, casesPath)
	refs := readLines(t, refPath)
	if len(cases) != len(refs) {
		t.Fatalf("语料条数对不上: cases=%d refs=%d", len(cases), len(refs))
	}

	pass, fail, deviate := 0, 0, 0
	for i, c := range cases {
		tab := strings.IndexByte(c, '\t')
		lang := c[:tab]
		text := unescapeCorpus(c[tab+1:])

		want := strings.Split(refs[i], "\t")
		if want[0] != lang {
			t.Fatalf("第 %d 条语言对不上: cases=%q refs=%q", i, lang, want[0])
		}
		// 参考里的**片段文本**是转义过的（\n 写成两字符），要还原后再比；
		// 样式类名不需要还原。第 0/1 两列是语言和 OK/NULL 标记。
		wantPairs := []string{want[0], want[1]}
		for j := 2; j+1 < len(want); j += 2 {
			wantPairs = append(wantPairs, want[j], unescapeCorpus(want[j+1]))
		}

		got := tokenizeToPairs(text, lang)

		// 已知的**刻意偏差**：原版超过 64KB 直接返回 null（整篇不高亮），
		// Go 版改成按行边界切块继续高亮（见 highlight.go 的 maxHighlightBytes）。
		// 语料里那条 >64KB 的用例因此是"期望 NULL、实际 OK" —— 这不是移植错误，
		// 是用户 2026-10-26 报障后定下的增强。单独计数，别混进通过率。
		if want[1] == "NULL" && len(text) > maxHighlightBytes && got[1] == "OK" {
			deviate++
			continue
		}

		if !equalPairs(got, wantPairs) {
			fail++
			if fail <= 5 {
				t.Errorf("用例 %d（%s）不一致\n 文本 = %s\n 期望 = %v\n 实际 = %v",
					i, lang, escapeCorpus(truncate(text, 120)),
					truncatePairs(escapePairs(wantPairs)), truncatePairs(escapePairs(got)))
			}
			continue
		}
		pass++
	}
	t.Logf("高亮跨语言比对：%d/%d 通过（另有 %d 条刻意偏差：>64KB 仍高亮）",
		pass, len(cases)-deviate, deviate)
	if fail > 0 {
		t.Errorf("共 %d 条不一致", fail)
	}
}

// tokenizeToPairs 把分词结果摊平成与参考输出同构的字符串切片：
//
//	{"<lang>", "NULL"}  或  {"<lang>", "OK", 类1, 文本1, 类2, 文本2, ...}
func tokenizeToPairs(text, lang string) []string {
	spans := Tokenize(text, lang)
	if spans == nil {
		return []string{lang, "NULL"}
	}
	out := []string{lang, "OK"}
	for _, sp := range spans {
		out = append(out, sp.Class, text[sp.Start:sp.End])
	}
	return out
}

func equalPairs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// escapePairs 只转义文本片段（奇数下标），样式类名原样保留。
func escapePairs(p []string) []string {
	out := make([]string, len(p))
	for i, s := range p {
		if i%2 == 1 && i >= 3 {
			out[i] = escapeCorpus(s)
		} else {
			out[i] = s
		}
	}
	return out
}

func truncatePairs(p []string) []string {
	const max = 24
	if len(p) <= max {
		return p
	}
	out := append([]string{}, p[:max]...)
	return append(out, "…")
}
