package service

import (
	"os"
	"strings"
	"testing"

	"cn.cpoet.patch.assistant/internal/util"
)

// TestParseReadMeMatchesJavaGolden 用 Java 原版跑出来的结果做黄金基准，
// 校验 Go 版 README 解析的忠实度。
//
// 基准文件的产生方式（可复现）：
//  1. 用 JDK 21 编译并运行 _extract/ReadMeVerify.java。该程序里的正则
//     常量 FILE_NAME_REGEX 与 Pattern 表达式，是逐字从
//     FileNameUtil.FILE_NAME_REGEX 与 ReadMeFileService.pattern 抄过来的；
//     AppNodePath 逻辑同样抄自 ReadMePathInfo.getAppNodePath()。
//  2. 让它读取同一份 testdata/README.txt，输出 type/path1/path2/path3/appNodePath。
//  3. 把输出固化成 testdata/readme_golden.tsv。
//
// 也就是说这个测试比对的是"Java 与 Go 是否同行为"，而不是"Go 是否符合我写的期望"——
// 后者等于自说自话。
func TestParseReadMeMatchesJavaGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/README.txt")
	if err != nil {
		t.Fatalf("读取测试 README 失败: %v", err)
	}
	goldenRaw, err := os.ReadFile("testdata/readme_golden.tsv")
	if err != nil {
		t.Fatalf("读取黄金基准失败: %v", err)
	}

	// Java 版用 BufferedReader.readLine() 读入，再按行匹配，
	// 这里先做同样的字符集处理再解析。
	text := util.DecodeString(raw, util.DetectCharset(raw))
	got := ParseReadMe(text)

	wantLines := strings.Split(strings.TrimRight(string(goldenRaw), "\n"), "\n")
	if len(got) != len(wantLines) {
		t.Fatalf("解析条数不一致: Go=%d, Java=%d", len(got), len(wantLines))
	}

	for i, wantLine := range wantLines {
		f := strings.Split(wantLine, "\t")
		if len(f) != 5 {
			t.Fatalf("黄金基准第 %d 行字段数异常: %q", i+1, wantLine)
		}
		wantType, wantP1, wantP2, wantP3, wantAppPath := f[0], f[1], f[2], f[3], f[4]

		g := got[i]
		gotType := ChangeTypeName(g.Type)
		gotAppPath := g.AppNodePath()

		if gotType != wantType || g.Path1 != wantP1 || g.Path2 != wantP2 || g.Path3 != wantP3 || gotAppPath != wantAppPath {
			t.Errorf("第 %d 条不一致:\n  Java: type=%s path1=%q path2=%q path3=%q appNodePath=%q\n  Go  : type=%s path1=%q path2=%q path3=%q appNodePath=%q",
				i+1, wantType, wantP1, wantP2, wantP3, wantAppPath,
				gotType, g.Path1, g.Path2, g.Path3, gotAppPath)
		}
	}
}

// TestParseReadMePrefixMustBeAttached 固化"前缀必须紧贴路径"这一既有行为。
//
// 这不是我们期望的语法，而是原版实现的真实行为，已被 Java 实跑确认。
// 之所以单独写一个测试，是因为它反直觉、容易在后续重构中被"顺手修好"，
// 从而与 Java 版行为分叉。
func TestParseReadMePrefixMustBeAttached(t *testing.T) {
	cases := []struct {
		line     string
		wantType string
		wantP1   string
		wantP2   string
	}{
		{"+foo.class", "ADD", "foo.class", ""},
		{"!foo.class", "MOD", "foo.class", ""},
		{"-foo.class", "DEL", "foo.class", ""},
		{"?foo.class", "IGNORE", "foo.class", ""},
		{"+ foo.class", "NONE", "foo.class", ""},  // 前缀被吞
		{"! foo.class", "NONE", "foo.class", ""},  // 前缀被吞
		{"- foo.class", "NONE", "-", "foo.class"}, // 退化成两段路径
		{"!old.class new.class", "MOD", "old.class", "new.class"},
	}
	for _, c := range cases {
		got := ParseReadMe(c.line)
		if len(got) != 1 {
			t.Errorf("%q: 期望命中 1 条，实际 %d 条", c.line, len(got))
			continue
		}
		g := got[0]
		if ChangeTypeName(g.Type) != c.wantType || g.Path1 != c.wantP1 || g.Path2 != c.wantP2 {
			t.Errorf("%q: 得到 type=%s path1=%q path2=%q，期望 type=%s path1=%q path2=%q",
				c.line, ChangeTypeName(g.Type), g.Path1, g.Path2, c.wantType, c.wantP1, c.wantP2)
		}
	}
}
