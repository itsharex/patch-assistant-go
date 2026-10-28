package content

import (
	"regexp"
	"strconv"
	"strings"
)

// 行号：移植自 Java 的 LineNumberFactory（普通文本）与
// DiffLineNumberFactory（差异文本）。
//
// 两者的"宽度"口径一致：右对齐到"总行数的位数"那么宽，用空格补齐。
// Java 那边是 `String.format("%1$" + digits + "s", x)`，digits 由
// `(int) Math.floor(Math.log10(max)) + 1` 算出 —— 这里改用整数数位数，
// 结果对所有正数都相同，还避开了 log10 的浮点误差（比如 log10(1000)
// 在某些实现里会算出 2.9999…）。

// PlainNumbers 生成 1..n 的行号文本，右对齐。
// 对应 richtextfx 的 `LineNumberFactory.get(codeArea)`。
func PlainNumbers(n int) []string {
	width := digits(n)
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = padLeft(strconv.Itoa(i+1), width)
	}
	return out
}

// diffLinePattern 对应 Java 的
// `DIFF_LINE_PATTERN = @@\s+-(\d+),\d+\s+\+(\d+),\d+\s+@@`，
// 且用的是 `matcher.matches()`（全串匹配），所以这里加了首尾锚点。
var diffLinePattern = regexp.MustCompile(`^@@\s+-(\d+),\d+\s+\+(\d+),\d+\s+@@$`)

// DiffNumbers 按 DiffLineNumberFactory 的口径算差异文本的行号。
//
// 规则（照搬 Java apply + getLineNumber）：
//
//   - `---` / `+++` 开头 -> 空行号
//   - `@@` 开头          -> `...`
//   - 其余               -> 从自己往上找最近的 `@@`，按区间号累加
//
// 两个**必须照搬的怪点**：
//
//  1. 判 `---` / `+++` 只看开头三个字符。所以一条"删除行"如果原文以
//     `--` 开头，渲染出来是 `---...`，会被当成文件头 —— 原版就是这样。
//  2. 往上找 `@@` 时，`@@ -0,0 +0,0 @@` 那个占位区间（首个差异之前的不变行
//     所在的块）区间号是 0，按 `line == 0 ? lineNumber+1 : line+lineNumber`
//     处理，于是这些行从 1 开始编号。
func DiffNumbers(lines []string) []string {
	width := digits(len(lines))
	out := make([]string, len(lines))
	for i, t := range lines {
		var num string
		switch {
		case strings.HasPrefix(t, "---"), strings.HasPrefix(t, "+++"):
			num = ""
		case strings.HasPrefix(t, "@@"):
			num = "..."
		default:
			num = strconv.Itoa(diffLineNumber(lines, i, strings.HasPrefix(t, "-")))
		}
		out[i] = padLeft(num, width)
	}
	return out
}

// diffLineNumber 从 idx 往上回溯，算出这一行的行号。
//
// isReduce 为真表示这是"删除侧"的行（行首是 `-`），取 `@@` 的原文区间号；
// 否则取新文区间号。
func diffLineNumber(lines []string, idx int, isReduce bool) int {
	lineNumber := 0
	for idx > 0 {
		idx--
		t := lines[idx]
		if strings.HasPrefix(t, "@@") {
			if m := diffLinePattern.FindStringSubmatch(t); m != nil {
				g := 2 // 新文区间号
				if isReduce {
					g = 1 // 原文区间号
				}
				if line, err := strconv.Atoi(m[g]); err == nil {
					if line == 0 {
						lineNumber = lineNumber + 1
					} else {
						lineNumber = line + lineNumber
					}
				}
			}
			break
		}
		if isJavaBlank(t) || !strings.HasPrefix(t, "-") {
			if !isReduce {
				lineNumber++
			}
		} else if isReduce {
			lineNumber++
		}
	}
	return lineNumber
}

// digits 返回 n 的十进制位数，等价于 Java 的
// `(int) Math.floor(Math.log10(n)) + 1`（n >= 1）。
func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		n /= 10
		d++
	}
	return d
}

// padLeft 左侧补空格到至少 width 宽（已经更宽就不动）。
// 对应 `String.format("%1$<width>s", x)` —— 注意它不会截断。
func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}
