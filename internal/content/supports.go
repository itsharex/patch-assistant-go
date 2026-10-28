// Package content 实现文件内容预览的取数与呈现逻辑。
//
// 移植自 Java 的 `cn.cpoet.patch.assistant.view.content` 包
// （ContentView / ContentSupports / facotry.* / parser.*）。
//
// 这里只做**内容 -> 可渲染数据**的转换，不碰界面：把节点字节解码成文本、
// 按扩展名选高亮规则、切成段落、算行号。界面部分在 gui/frontend。
//
// 之所以要单独一个包：这些规则都有明确的"原版行为"可对照，放在可单测的
// 地方才能用跨语言黄金基准钉住（见 highlight_golden_test.go 与
// linenum_golden_test.go）。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package content

// ParserKind 内容解析器的种类，对应 Java ContentParser 的两个实现。
//
// 注意 `getContentParser` 返回 null 的那一大类（图片、jar、zip、无后缀…）
// 原版走 `openWithSystem()` —— 把字节写到临时目录再用系统程序打开。
type ParserKind int

const (
	// ParserNone 没有解析器，原版会交给系统程序打开。
	ParserNone ParserKind = iota
	// ParserText 纯文本，按探测到的（或指定的）字符集解码。
	ParserText
	// ParserClass class 文件，原版用 fernflower 反编译成 Java 源码再显示。
	//
	// Go 侧**已实现**：把原项目那份 fernflower jar（1.3.0）嵌进二进制，
	// 运行时交给本机 JRE 执行，见 internal/decompile。反编译产出的源码
	// 走与文本相同的渲染/差异路径，高亮用 LangJava。
	//
	// 取文本这一步不放在本包：它要碰字节、临时文件和子进程，不属于
	// "纯规则"，放在可单测的 gui 层更合适。
	ParserClass
)

// ParserForExt 按扩展名选解析器。
//
// 逐条对齐 Java ContentSupports.getContentParser(String ext)：
//
//	class                                   -> ClassContentParser
//	java txt yaml yml xml properties js
//	html htm css sql json sign              -> TextContentParser
//	其余（含 ext 为空串/无后缀）              -> null
//
// 注意几个**容易想当然改错**的点：
//   - 只有 `yaml` / `yml` 这些小写写法，大小写敏感；
//   - `json` 在列表里（所以能预览），但它**没有**高亮规则（见 LangForExt）；
//   - `md`、`log`、`bat` 之类不在列表里 —— 原版就是打不开预览，
//     别顺手"补全"成文本。
func ParserForExt(ext string) ParserKind {
	switch ext {
	case "class":
		return ParserClass
	case "java", "txt", "yaml", "yml", "xml", "properties",
		"js", "html", "htm", "css", "sql", "json", "sign":
		return ParserText
	}
	return ParserNone
}

// 高亮语言标识。前端按这个值选 CSS 类名，取值与 Java 各 CodeAreaFactory 一一对应。
const (
	LangPlain      = "plain"      // DefaultCodeAreaFactory：不高亮
	LangJava       = "java"       // java / class
	LangJavaScript = "javascript" // js
	LangHTML       = "html"       // html / htm
	LangCSS        = "css"
	LangXML        = "xml"
	LangYAML       = "yaml" // yml / yaml
	LangSQL        = "sql"
)

// LangForExt 按扩展名选高亮规则。
//
// 逐条对齐 Java ContentSupports.getCodeAreaFactory(String ext)：
//
//	java class      -> JavaCodeAreaFactory
//	js              -> JavaScriptCodeAreaFactory
//	html htm        -> HtmlCodeAreaFactory
//	css             -> CssCodeAreaFactory
//	xml             -> XmlCodeAreaFactory
//	yml yaml        -> YamlCodeAreaFactory
//	sql             -> SQLCodeAreaFactory
//	其余（含 null）  -> DefaultCodeAreaFactory（不高亮）
//
// 这里是**原版的一处不对称**，务必照搬：能预览的扩展名比能高亮的多。
// `txt` / `properties` / `json` / `sign` 有解析器（能看内容），
// 但落到 DefaultCodeAreaFactory，也就是**没有任何着色**。
// 看起来像漏了，其实是原版就这样，别"顺手补上" json 的高亮。
func LangForExt(ext string) string {
	switch ext {
	case "java", "class":
		return LangJava
	case "js":
		return LangJavaScript
	case "html", "htm":
		return LangHTML
	case "css":
		return LangCSS
	case "xml":
		return LangXML
	case "yml", "yaml":
		return LangYAML
	case "sql":
		return LangSQL
	}
	return LangPlain
}
