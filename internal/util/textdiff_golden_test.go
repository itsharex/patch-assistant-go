package util

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 跨语言黄金基准：拿**原版 Java 实现**跑同一批语料，逐条比对输出。
//
// 语料与参考输出由 _extract/tools 下的工具生成：
//
//	python make_diffcases.py                          -> diffcases.tsv
//	java  -cp "<cp>;." DiffCorpus diffcases.tsv diff_ref.tsv  -> diff_ref.tsv
//
// 参考输出里存的是 `TextDiffUtil.diff2Str(left, right, "L.txt", "R.txt")` 的
// 真实结果，所以这里只要有一处对不上就是移植错了，不能靠"看起来差不多"放过。
//
// 语料文件不在（比如只拷了 go 目录出来单独跑测试）时跳过，不误报失败。

// goldenDir 相对本包定位到 _extract/tools。
// 测试的工作目录是包目录 internal/util，上溯三级才到 patch-assistant-to-java。
var goldenDir = filepath.Join("..", "..", "..", "_extract", "tools")

// unescapeCorpus 还原语料里的 \n \r \t \\ 转义。
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

// escapeCorpus 与 Java 侧的 escape 对称，便于失败时打印可读的差异。
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

// readTSV 读 `<名字>\t<左>\t<右>` 形态的语料。
func readTSV(t *testing.T, path string) [][3]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开语料失败: %v", err)
	}
	defer f.Close()

	var rows [][3]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		for len(parts) < 3 {
			parts = append(parts, "")
		}
		rows = append(rows, [3]string{parts[0], parts[1], parts[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读语料失败: %v", err)
	}
	return rows
}

// TestDiffGoldenAgainstJava 全量比对 132 条语料。
func TestDiffGoldenAgainstJava(t *testing.T) {
	casesPath := filepath.Join(goldenDir, "diffcases.tsv")
	refPath := filepath.Join(goldenDir, "diff_ref.tsv")
	if _, err := os.Stat(casesPath); err != nil {
		t.Skipf("语料不在，跳过跨语言比对: %v", err)
	}
	if _, err := os.Stat(refPath); err != nil {
		t.Skipf("参考输出不在，跳过跨语言比对: %v", err)
	}

	cases := readTSV(t, casesPath)
	refs := readTSV(t, refPath)
	if len(cases) != len(refs) {
		t.Fatalf("语料条数对不上: cases=%d refs=%d", len(cases), len(refs))
	}

	pass, fail := 0, 0
	for i, c := range cases {
		name := c[0]
		if refs[i][0] != name {
			t.Fatalf("第 %d 条名字对不上: cases=%q refs=%q", i, name, refs[i][0])
		}
		left := unescapeCorpus(c[1])
		right := unescapeCorpus(c[2])
		want := unescapeCorpus(refs[i][1])

		got := Diff2Str(left, right, "L.txt", "R.txt")
		if got != want {
			fail++
			if fail <= 5 {
				t.Errorf("用例 %q 输出不一致\n 左   = %s\n 右   = %s\n 期望 = %s\n 实际 = %s",
					name, escapeCorpus(left), escapeCorpus(right),
					escapeCorpus(want), escapeCorpus(got))
			}
			continue
		}
		pass++
	}
	t.Logf("跨语言比对：%d/%d 通过", pass, len(cases))
	if fail > 0 {
		t.Errorf("共 %d 条不一致", fail)
	}
}
