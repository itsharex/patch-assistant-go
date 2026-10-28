package content

import (
	"regexp"
	"strings"
)

// 语法高亮：移植自 Java 各 `CodeAreaFactory` 的命名捕获正则。
//
// 正则本身**逐字照抄**（只做 Go 语法层面的必要改写），因为分词结果要与
// 原版逐字节一致 —— 见 highlight_golden_test.go 的跨语言基准。
//
// 三处必须改写的语法差异：
//
//  1. Java 的 `(?<NAME>...)` 在 Go 里写成 `(?P<NAME>...)`；
//  2. Java 的 `\R`（任意换行）Go 不支持，展开成
//     `(?:\r\n|[\n\v\f\r\x{85}\x{2028}\x{2029}])`；
//  3. Java 的 `Pattern.compile` 默认不 dotall，Go 的 `.` 默认也不匹配 `\n`
//     —— 这一条一致，不用动。
//
// **样式类的选取规则**（容易漏）：Java 的 `getStyleClass` 按**固定顺序**依次
// 检查命名分组，返回第一个非 null 的分组对应的类名，而着色区间是
// **整个匹配**（matcher.start()..matcher.end()），不是那个分组。
//
// 这在 CSS 上有个反直觉的后果：`(?<PROPERTY>..)(?<PUNCTUATION>..)(?<VALUE>..)(?<TERMINATOR>;)`
// 四个分组同属一个分支，一次匹配里**四个都非 null**，于是 `color: red;`
// 整段被染成 "property" 的颜色，而不是只染属性名。照搬，别改。
//
// 好在所有工厂的"检查顺序"恰好都等于"分组在正则里的出现顺序"，
// 所以按分组下标取第一个非空即可，无需额外维护顺序表。

// hlRule 一种语言的分词规则。
type hlRule struct {
	re    *regexp.Regexp
	names []string          // 分组名，下标即分组号
	class map[string]string // 分组名 -> 样式类
}

// classOf 按 Java getStyleClass 的口径取这次匹配的样式类。
// 返回空串表示"没有样式"（对应 Java 返回 null）。
func (r *hlRule) classOf(m []int) string {
	for gi := 1; gi < len(r.names); gi++ {
		if m[2*gi] < 0 { // 该分组没参与匹配
			continue
		}
		if cls, ok := r.class[r.names[gi]]; ok {
			return cls
		}
	}
	return ""
}

// reAnyBreak 是 Java `\R` 的等价展开。
const reAnyBreak = `(?:\r\n|[\n\v\f\r\x{85}\x{2028}\x{2029}])`

// 各语言的关键字表，逐字抄自 Java 对应工厂。
var (
	javaKeywords = []string{
		"abstract", "assert", "boolean", "break", "byte",
		"case", "catch", "char", "class", "const",
		"continue", "default", "do", "double", "else",
		"enum", "extends", "final", "finally", "float",
		"for", "goto", "if", "implements", "import",
		"instanceof", "int", "interface", "long", "native",
		"new", "package", "private", "protected", "public",
		"return", "short", "static", "strictfp", "super",
		"switch", "synchronized", "this", "throw", "throws",
		"transient", "try", "void", "volatile", "while",
	}
	jsKeywords = []string{
		"abstract", "arguments", "await", "boolean", "break", "byte", "case", "catch",
		"char", "class", "const", "continue", "debugger", "default", "delete", "do",
		"double", "else", "enum", "eval", "export", "extends", "false", "final",
		"finally", "float", "for", "function", "goto", "if", "implements", "import",
		"in", "instanceof", "int", "interface", "let", "long", "native", "new",
		"null", "package", "private", "protected", "public", "return", "short",
		"static", "super", "switch", "synchronized", "this", "throw", "throws",
		"transient", "true", "try", "typeof", "var", "void", "volatile", "while",
		"with", "yield",
	}
	sqlKeywords = []string{
		"SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "IN", "IS", "NULL",
		"LIKE", "BETWEEN", "JOIN", "INNER", "OUTER", "LEFT", "RIGHT", "FULL",
		"ON", "GROUP BY", "HAVING", "ORDER BY", "ASC", "DESC", "INSERT", "INTO",
		"VALUES", "UPDATE", "SET", "DELETE", "CREATE", "ALTER", "DROP", "TABLE",
		"VIEW", "INDEX", "TRIGGER", "PROCEDURE", "FUNCTION", "DATABASE", "GRANT",
		"REVOKE", "COMMIT", "ROLLBACK", "BEGIN", "TRANSACTION", "CASE", "WHEN",
		"THEN", "ELSE", "END", "DISTINCT", "UNION", "ALL", "EXISTS", "WITH",
	}
	yamlKeywords = []string{"true", "false", "null", "yes", "no", "on", "off"}
	cssKeywords  = []string{
		"align-content", "align-items", "align-self", "all", "animation", "animation-delay",
		"animation-direction", "animation-duration", "animation-fill-mode", "animation-iteration-count",
	}
)

// join 用 '|' 连接，对应 Java 的 String.join("|", arr)。
func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += "|"
		}
		out += s
	}
	return out
}

// 共用的片段（Java 里 JavaCodeAreaFactory / JavaScriptCodeAreaFactory 各写了一份，
// 只有关键字表和 STRING 不同）。
const (
	parenPattern   = `[()]`
	bracePattern   = `[{}]`
	bracketPattern = `[\[\]]`
	semicolonPat   = `;`
	javaStringPat  = `"([^"\\]|\\.)*"`
	jsStringPat    = `"([^"\\]|\\.)*"|'([^'\\]|\\.)*'`
	// `//...` 或 `/* ... */`（可跨行）。原版写的是 `(.|\R)*?`，`\R` 换成了
	// reAnyBreak —— Go 不支持 `\R`。
	slashCommentPat = `//[^\n]*` + `|` + `/\*(.|` + reAnyBreak + `)*?\*/`
)

// mustRule 编译规则；分组名 -> 样式类的映射由调用方给出。
func mustRule(pattern string, class map[string]string) *hlRule {
	re := regexp.MustCompile(pattern)
	return &hlRule{re: re, names: re.SubexpNames(), class: class}
}

// 各语言规则。惰性编译一次，之后复用（regexp.Regexp 并发安全）。
var (
	ruleJava = mustRule(
		`(?P<KEYWORD>\b(`+join(javaKeywords)+`)\b)`+
			`|(?P<PAREN>`+parenPattern+`)`+
			`|(?P<BRACE>`+bracePattern+`)`+
			`|(?P<BRACKET>`+bracketPattern+`)`+
			`|(?P<SEMICOLON>`+semicolonPat+`)`+
			`|(?P<STRING>`+javaStringPat+`)`+
			`|(?P<COMMENT>`+slashCommentPat+`)`,
		map[string]string{
			"KEYWORD": "keyword", "PAREN": "paren", "BRACE": "brace",
			"BRACKET": "bracket", "SEMICOLON": "semicolon",
			"STRING": "string", "COMMENT": "comment",
		})

	ruleJavaScript = mustRule(
		`(?P<KEYWORD>\b(`+join(jsKeywords)+`)\b)`+
			`|(?P<PAREN>`+parenPattern+`)`+
			`|(?P<BRACE>`+bracePattern+`)`+
			`|(?P<BRACKET>`+bracketPattern+`)`+
			`|(?P<SEMICOLON>`+semicolonPat+`)`+
			`|(?P<STRING>`+jsStringPat+`)`+
			`|(?P<COMMENT>`+slashCommentPat+`)`,
		map[string]string{
			"KEYWORD": "keyword", "PAREN": "paren", "BRACE": "brace",
			"BRACKET": "bracket", "SEMICOLON": "semicolon",
			"STRING": "string", "COMMENT": "comment",
		})

	ruleCSS = mustRule(
		`(?P<KEYWORD>`+join(cssKeywords)+`)`+
			`|(?P<SELECTOR>\.[a-zA-Z][a-zA-Z0-9-]*|#[a-zA-Z][a-zA-Z0-9-]*|:[a-zA-Z][a-zA-Z0-9-]*|\[[a-zA-Z][a-zA-Z0-9-]*\])`+
			`|(?P<PROPERTY>[a-zA-Z-]+)(?P<PUNCTUATION>\s*:\s*)(?P<VALUE>[^;]+)(?P<TERMINATOR>;)`+
			`|(?P<COMMENT>/\*[^*]*\*+(?:[^/*][^*]*\*+)*/)`,
		map[string]string{
			"KEYWORD": "keyword", "SELECTOR": "selector", "PROPERTY": "property",
			"PUNCTUATION": "punctuation", "VALUE": "value",
			"TERMINATOR": "terminator", "COMMENT": "comment",
		})

	ruleXML = mustRule(
		`(?P<TAG>(</?\w+/?>|\?>))`+
			`|(?P<ATTRIBUTE>\s(\w+)\s*=\s*"[^"]*")`+
			`|(?P<COMMENT><!--[\s\S]*?-->)`+
			`|(?P<STRING>"[^"]*")`,
		map[string]string{
			"TAG": "xml-tag", "ATTRIBUTE": "xml-attribute",
			"COMMENT": "xml-comment", "STRING": "xml-string",
		})

	ruleHTML = mustRule(
		`(?P<TAG>(</?)([a-zA-Z][a-zA-Z0-9-]*)([^>]*>))`+
			`|(?P<ATTRIBUTE>\s([a-zA-Z-]+)(="[^"]*")?)`+
			`|(?P<COMMENT><!--.*?-->)`+
			`|(?P<STRING>"([^"\\]|\\.)*")`+
			`|(?P<DOCTYPE><!DOCTYPE.*?>)`,
		map[string]string{
			"TAG": "html-tag", "ATTRIBUTE": "html-attribute",
			"COMMENT": "html-comment", "STRING": "html-string",
			"DOCTYPE": "html-doctype",
		})

	ruleYAML = mustRule(
		`(?P<KEYWORD>\b(`+join(yamlKeywords)+`)\b)`+
			`|(?P<STRING>"([^"\\]|\\.)*"|'([^'\\]|\\.)*')`+
			`|(?P<COMMENT>#[^\n]*)`+
			`|(?P<NUMBER>\b\d+(\.\d+)?\b)`+
			`|(?P<KEY>^\s*[a-zA-Z_][a-zA-Z0-9_]*:)`+
			`|(?P<ANCHOR>&[a-zA-Z_][a-zA-Z0-9_]*)`+
			`|(?P<ALIAS>\*[a-zA-Z_][a-zA-Z0-9_]*)`,
		map[string]string{
			"KEYWORD": "yaml-keyword", "STRING": "yaml-string", "COMMENT": "yaml-comment",
			"NUMBER": "yaml-number", "KEY": "yaml-key",
			"ANCHOR": "yaml-anchor", "ALIAS": "yaml-alias",
		})

	ruleSQL = mustRule(
		`(?P<KEYWORD>\b(`+join(sqlKeywords)+`)\b)`+
			`|(?P<STRING>`+jsStringPat+`)`+
			`|(?P<NUMBER>\b\d+\b)`+
			`|(?P<COMMENT>--[^\n]*`+`|`+`/\*(.|`+reAnyBreak+`)*?\*/)`,
		map[string]string{
			"KEYWORD": "keyword", "STRING": "string",
			"NUMBER": "number", "COMMENT": "comment",
		})
)

// ruleFor 取语言对应的规则；LangPlain 没有规则（对应 DefaultCodeAreaFactory）。
func ruleFor(lang string) *hlRule {
	switch lang {
	case LangJava:
		return ruleJava
	case LangJavaScript:
		return ruleJavaScript
	case LangHTML:
		return ruleHTML
	case LangCSS:
		return ruleCSS
	case LangXML:
		return ruleXML
	case LangYAML:
		return ruleYAML
	case LangSQL:
		return ruleSQL
	}
	return nil
}

// maxHighlightBytes **单块**分词的上限（不是整段文本的上限）。
//
// 数值来自 Java CodeAreaFactory.computeHighlighting 里的
// `if (text.getBytes().length > 64 * 1024) return null;`。
// 单位是**字节**，且用平台默认字符集编码 —— Java 18 起默认 UTF-8，
// 与 Go 的 len(string) 一致。
//
// ⚠️ **这里与 Java 版有一处刻意的行为差异**：原版超过 64KB 就整篇放弃高亮，
// 于是**反编译出来的 .class**（一个 Spring service 动辄上千行、七八十 KB）
// 在双栏对比里是一片黑字。用户 2026-10-26 报障："两个对比的，代码没有高亮"。
// Go 版把 64KB 当作**切块大小**：超过就按行边界切块、逐块分词再拼偏移，
// 于是大文件仍然有高亮，而单次正则调用的规模仍被钉在 64KB 以内
// （避免正则在大输入上退化）。
//
// 代价：`/* ... */` 恰好**跨过块边界**时认不出来（注释的上下两半各自成段，
// 都不会匹配到完整的注释）。块边界落在换行处、且注释块通常很短，实际很少见。
const maxHighlightBytes = 64 * 1024

// Tokenize 对整段文本分词，返回按顺序排列的样式区间（字节偏移，左闭右开）。
//
// 返回 nil 表示"不高亮"（两种情况：文本为空、该语言没有规则），
// 对应 Java computeHighlighting 返回 null。此时调用方按纯文本渲染。
//
// **必须在整段文本上调用，不能逐行调用**：`/* ... */` 这类跨行记号要靠
// 整段匹配才能认出来。切行是后面 Render 的事。超过 maxHighlightBytes 时
// 内部会切块，但切点只在换行处，单块内的跨行记号仍然认得出来。
func Tokenize(text, lang string) []Span {
	if text == "" {
		return nil // Java: StringUtil.isEmpty(text) -> null
	}
	r := ruleFor(lang)
	if r == nil {
		return nil // DefaultCodeAreaFactory.createMatcher -> null
	}

	// 绝大多数文件走这条：整段一次分词，与 Java 版逐字节一致。
	if len(text) <= maxHighlightBytes {
		return tokenizeRange(text, r, 0)
	}

	// 大文件：按行边界切成不超过 maxHighlightBytes 的块。
	// 切点退到最后一个换行**之后**，这样不会把一行（或一个记号）从中间劈开；
	// 单行本身就超长（压缩过的 js/css）时退无可退，只能硬切。
	var spans []Span
	for start := 0; start < len(text); {
		end := start + maxHighlightBytes
		if end >= len(text) {
			end = len(text)
		} else if i := strings.LastIndexByte(text[start:end], '\n'); i >= 0 {
			end = start + i + 1
		}
		// 用 addSpan 逐段并入：它会把"块尾"和"块头"两个同类区间合并，
		// 否则拼接处会多出一个无意义的 <span> 断点。
		for _, sp := range tokenizeRange(text[start:end], r, start) {
			spans = addSpan(spans, sp)
		}
		start = end
	}
	return spans
}

// tokenizeRange 对 text 分词，返回的偏移整体加上 base。
// base 就是这段 text 在整篇文本里的起始字节位置。
func tokenizeRange(text string, r *hlRule, base int) []Span {
	matches := r.re.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		// 一个记号都没有：Java 会走最后那句
		// spansBuilder.add(emptyList, text.length()-0)，得到"全文无样式"一段
		return []Span{{Start: base, End: base + len(text)}}
	}

	spans := make([]Span, 0, len(matches)*2+1)
	last := 0
	for _, m := range matches {
		if cls := r.classOf(m); cls != "" {
			spans = addSpan(spans, Span{Start: base + last, End: base + m[0]})
			spans = addSpan(spans, Span{Start: base + m[0], End: base + m[1], Class: cls})
		} else {
			// 命中了但没认出分组：Java 也是整段并入无样式区
			spans = addSpan(spans, Span{Start: base + last, End: base + m[1]})
		}
		last = m[1]
	}
	spans = addSpan(spans, Span{Start: base + last, End: base + len(text)})
	return spans
}

// addSpan 追加区间，并复刻 StyleSpansBuilder.add 的两个行为：
// 零长度直接丢弃、与上一段样式相同则合并。
//
// 这两条不是可有可无的优化 —— 不合并的话，相邻的同类记号会被切成很多段，
// 渲染出来的 <span> 数量与原版对不上，黄金基准立刻就会红。
func addSpan(spans []Span, sp Span) []Span {
	if sp.End <= sp.Start {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].Class == sp.Class {
		spans[n-1].End = sp.End
		return spans
	}
	return append(spans, sp)
}
