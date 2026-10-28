package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 跨语言黄金基准：差异文本的行号。
//
// 参考输出由 _extract/tools/DiffLineNoCorpus.java 生成：
//
//	java -cp "<cp>;." DiffLineNoCorpus diffcases.tsv dlineno_ref.tsv
//
// 每行是 `<名字>\t<转义后的差异文本>\t<行号0>\t<行号1>...`。
// 差异文本一并写进参考文件，所以这个测试**不依赖**差异算法的正确性
// （差异算法另有 util.Diff2Str 的基准管着）。
func TestDiffLineNumbersGoldenAgainstJava(t *testing.T) {
	refPath := filepath.Join(goldenDir, "dlineno_ref.tsv")
	if _, err := os.Stat(refPath); err != nil {
		t.Skipf("参考输出不在，跳过跨语言比对: %v", err)
	}
	rows := readLines(t, refPath)

	pass, fail := 0, 0
	for _, row := range rows {
		parts := strings.Split(row, "\t")
		if len(parts) < 3 {
			t.Fatalf("参考行格式不对（至少要有 名字/差异文本/一个行号）: %q", row)
		}
		name := parts[0]
		diffText := unescapeCorpus(parts[1])
		want := parts[2:]

		// 行号与高亮无关，用 plain 免得把高亮的因素混进来
		lines := RenderDiff(diffText, LangPlain)
		if len(lines) != len(want) {
			fail++
			if fail <= 5 {
				t.Errorf("用例 %q 行数不一致: 期望 %d 实际 %d\n差异文本 = %s",
					name, len(want), len(lines), escapeCorpus(diffText))
			}
			continue
		}
		bad := -1
		for i := range lines {
			if lines[i].Num != want[i] {
				bad = i
				break
			}
		}
		if bad >= 0 {
			fail++
			if fail <= 5 {
				t.Errorf("用例 %q 第 %d 行行号不一致\n 差异文本 = %s\n 期望 = %v\n 实际 = %v",
					name, bad, escapeCorpus(diffText), want, lineNums(lines))
			}
			continue
		}
		pass++
	}
	t.Logf("差异行号跨语言比对：%d/%d 通过", pass, len(rows))
	if fail > 0 {
		t.Errorf("共 %d 条不一致", fail)
	}
}

func lineNums(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Num
	}
	return out
}
