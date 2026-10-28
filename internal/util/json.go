package util

import (
	"bytes"
	"encoding/json"
)

// jsonLineSeparator 是 Jackson DefaultPrettyPrinter 使用的换行符。
//
// Jackson 的 DefaultIndenter.SYSTEM_LINEFEED_INSTANCE 取的是
// System.lineSeparator() —— Windows 下是 "\r\n"，类 Unix 下是 "\n"。
// 黄金基准比对时正是这一项让签名文件差了 15 字节（15 行 × 1 字节），
// 属于真实内容差异而不是压缩差异，所以必须按平台对齐。
var jsonLineSeparator = func() string {
	if IsWindows() {
		return "\r\n"
	}
	return "\n"
}()

// WriteJSONAsBytes 按 Jackson 的默认缩进风格序列化。
//
// 移植自 Java JsonUtil.writeAsBytes —— 它用的是
// `JsonMapper.builder().enable(SerializationFeature.INDENT_OUTPUT)`，
// 即 Jackson 的 DefaultPrettyPrinter。这个 printer 与 Go 的
// json.MarshalIndent 有【三处外观差异】，直接影响写出的
// META-INF/patch-up.sign 的字节内容：
//
//	Jackson:  "name" : "x"        Go:  "name": "x"      （冒号前多一个空格）
//	Jackson:  空对象写作 { } / 空数组写作 [ ]  （Go 写作 {} / []）
//	Jackson:  换行用 System.lineSeparator()，Windows 下是 CRLF
//
// 数组与对象的换行/缩进规则两者一致（2 空格、每项一行，末尾不加换行）。
// 为了让 Go 版写出的签名文件能与 Java 版直接 diff，这里做了对齐处理。
//
// 保真提示：Go 的 encoding/json 按 struct tag 顺序输出字段，与 Jackson
// 按 getter 声明顺序输出一致；omitempty 对应 @JsonInclude(NON_EMPTY)。
// 但两者的"空"判定有细微差别：Go 的 omitempty 把 0/0.0/false 也算空，
// 而 Jackson 的 NON_EMPTY 只排除 null/空串/空集合，数值 0 会保留。
// 本项目写进签名的数值字段都是指针（*int / *int64），所以不受影响。
func WriteJSONAsBytes(v any) ([]byte, error) {
	compact, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return JacksonIndent(compact), nil
}

// JacksonIndent 把紧凑 JSON 重新排版成 Jackson DefaultPrettyPrinter 的风格。
//
// 之所以用"文本重排"而不是"解码成 map 再编码"：Go 的 map 无序，
// 一旦解码就会丢掉字段顺序，写出的签名文件字段顺序会随机化。
// 文本重排逐字符扫描（跳过字符串内部），能完整保留原始顺序。
func JacksonIndent(compact []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(compact) * 2)

	// emptyStack 记录每个容器"开括号后是否紧跟闭括号"，
	// 用来决定闭括号写成 " }" 还是 "\n<indent>}"。
	var emptyStack []bool
	level := 0

	writeIndent := func(n int) {
		out.WriteString(jsonLineSeparator)
		for i := 0; i < n; i++ {
			out.WriteString("  ")
		}
	}

	inString := false
	escaped := false
	// pendingEmpty 表示"刚写完一个开括号，还不知道它是不是空的"
	pendingEmpty := false
	// markContent 一旦容器内出现实义内容，就把它的空标记清掉
	markContent := func() {
		if pendingEmpty {
			emptyStack[len(emptyStack)-1] = false
			writeIndent(level)
			pendingEmpty = false
		}
	}

	for i := 0; i < len(compact); i++ {
		c := compact[i]

		if inString {
			out.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			markContent()
			inString = true
			out.WriteByte(c)
		case '{', '[':
			out.WriteByte(c)
			level++
			emptyStack = append(emptyStack, true)
			pendingEmpty = true
		case '}', ']':
			empty := len(emptyStack) > 0 && emptyStack[len(emptyStack)-1]
			if len(emptyStack) > 0 {
				emptyStack = emptyStack[:len(emptyStack)-1]
			}
			level--
			if empty {
				out.WriteByte(' ')
			} else {
				writeIndent(level)
			}
			out.WriteByte(c)
			pendingEmpty = false
		case ',':
			out.WriteByte(',')
			writeIndent(level)
			pendingEmpty = false
		case ':':
			out.WriteString(" : ")
			pendingEmpty = false
		default:
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				// 丢弃紧凑 JSON 里的空白
				continue
			}
			markContent()
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}
