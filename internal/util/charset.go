package util

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Charset 字符集标识，对应 Java 的 java.nio.charset.Charset。
type Charset string

// 应用内只涉及这两种字符集，与 Java CharsetConst 一致。
const (
	CharsetUTF8 Charset = "UTF-8"
	CharsetGBK  Charset = "GBK"
)

// DetectCharset 猜测字节串的字符集。
//
// 对应 Java CharsetUtil.getCharset（基于 juniversalchardet 的 UniversalDetector）。
//
// 移植说明：Go 标准库没有字符集探测器。这里做了三层近似，
// 精度对 Java 版有偏差，务必知悉：
//  1. BOM 优先（UTF-8 BOM / UTF-16 BOM）；
//  2. 整段是合法 UTF-8 → UTF-8；
//  3. 否则回落 GBK（中文场景下 juniversalchardet 的绝大多数输出）。
//
// 注意这里**只负责"猜"**，真正解 GBK 已经实现（见 DecodeString）。
// 与 Java 版的差距在"猜"这一步：juniversalchardet 还能识别 Big5 / Shift_JIS /
// windows-1252 等，本实现一律当 GBK 处理。中文场景够用，其他语言场景会解错。
// 要做到完全等价需要接 chardet 类库。见 README 的"未完成项"。
func DetectCharset(b []byte) Charset {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return CharsetUTF8
	}
	if len(b) >= 2 {
		if (b[0] == 0xFF && b[1] == 0xFE) || (b[0] == 0xFE && b[1] == 0xFF) {
			// UTF-16 带 BOM：Java 版会返回 UTF-16，Go 侧暂统一按 UTF-8 处理
			return CharsetUTF8
		}
	}
	if utf8.Valid(b) {
		return CharsetUTF8
	}
	return CharsetGBK
}

// DecodeString 按指定字符集把字节串解码为 UTF-8 字符串。
//
// 对应 Java 的 `new String(bytes, charset)`：遇到非法字节是**替换**成 U+FFFD，
// 不抛异常，所以这里也不返回 error。
//
// GBK 分支用 GB18030 解码器 —— GB18030 是 GBK 的超集，对 GBK 合法的双字节
// 序列两者映射完全一致，所以拿它解 GBK 不会解错；顺带还覆盖了
// juniversalchardet 有时会报的 "GB18030" 和 4 字节序列。
//
// 背景：这一支曾经是个 TODO 桩（按 UTF-8 透传 + 替换非法序列），后果是
// GBK 编码的补丁说明文件在界面上整段变问号 —— 而中文 Windows 上导出的
// README 恰恰多是 GBK。已补上。
func DecodeString(b []byte, cs Charset) string {
	if cs == CharsetGBK {
		if s, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
			return string(s)
		}
		// GB18030 的映射覆盖全部字节组合，正常不会走到这里；兜底仍走替换。
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// DecodeZipEntryName 把 zip 条目名解码成 UTF-8。
//
// 背景：zip 对条目名的编码**没有强制约定** —— 有 UTF-8 标志位（0x800）时按
// UTF-8 读，没有时按"写入方本机编码"读。中文 Windows 上打出来的包基本是 GBK，
// 而 Go 的 archive/zip 在缺标志位时**原样返回字节**（只把 FileHeader.NonUTF8
// 置真），这些字节不是合法 UTF-8，经 JSON 传到前端就成了
// `??????.docx`。原版 Java 用 `new ZipInputStream(in, GBK)` 无条件按 GBK 解，
// 所以那边不出现这个问题 —— 这是移植时漏掉的一处。
//
// 判定顺序：
//  1. 已是合法 UTF-8 → 原样返回。覆盖"带标志位的 UTF-8"、纯 ASCII，以及
//     "没设标志位但写的确实是 UTF-8"这类野包（部分工具就是不打标志）；
//  2. 否则按 GB18030 解 —— GBK 的超集，解 GBK 不会解错，顺带覆盖 GB18030；
//  3. 再失败 → 替换非法字节。宁可显示 �，也不把非法 UTF-8 交给上层。
//
// 代价：CP437 / Big5 / Shift_JIS 的条目名会被当 GBK 解成别的字。取舍与
// DetectCharset 一致（中文场景优先），已登记 MIGRATION.md。
//
// 注意**不要**用它改写要写回包的路径：解码只用于显示与匹配，重打包时原始
// 字节另存（见 tree.Node.RawPath），否则包里的名字会被我们悄悄改成 UTF-8。
func DecodeZipEntryName(raw string) string {
	if utf8.ValidString(raw) {
		return raw
	}
	if s, err := simplifiedchinese.GB18030.NewDecoder().String(raw); err == nil {
		return s
	}
	return strings.ToValidUTF8(raw, "\uFFFD")
}

// EncodeString 按指定字符集把 UTF-8 字符串编码为字节串。
//
// 对应 Java 的 `str.getBytes(charset)` —— 与 DecodeString 对称。
// 编辑包内文件后写回时要用它：原文件是 GBK 的，就必须仍按 GBK 写出去，
// 否则整个包会从这一刻起变成乱码。
//
// 不返回 error，理由与 DecodeString 相同：Java 的 getBytes 也不抛。
//
// 【与 Java 的刻意偏差】Java 的 `Charset.forName("GBK")` 是**严格 GBK** 编码器，
// 遇到 GBK 表示不了的字符（emoji、部分 CJK 扩展区）会按 REPLACE 动作写成
// '?'（0x3F），把内容静默丢掉。这里改用 **GB18030** 编码器 —— 它是 GBK 的超集、
// 覆盖全部 Unicode（穷举 U+0000..U+10FFFF 零失败，见 charset_test.go 的
// TestEncodeStringCoversAllRunes），会把这类字符写成 4 字节序列而不是丢掉。
//
// 为什么宁可偏差也不照抄 Java：解码侧（DecodeString）用的就是 GB18030 —— 那是
// 有意为之，为的是接受"juniversalchardet 有时报成 GB18030"的文件。若编码侧退回
// 严格 GBK，则"打开一个含 4 字节序列的 GB18030 文件 → 改一个字 → 保存"会把那些
// 字符全变成 '?' —— 我们自己刚接受的文件被自己写坏。两害相权取其轻。
//
// 代价：产出的包若被只认严格 GBK 的下游按 GBK 解码，4 字节序列会解不出来。
// 已登记在 MIGRATION.md「编辑包内文件内容」一节。
func EncodeString(s string, cs Charset) []byte {
	if cs != CharsetGBK {
		return []byte(s)
	}
	// 这里刻意不写"逐 rune 兜底、失败的换成 '?'"那一套：早期版本写过，
	// 实测是死代码（GB18030 编码器对任何输入都不失败，连非法 UTF-8 字节
	// 也会被它当 U+FFFD 编出去），已删掉。
	out, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(s))
	if err != nil {
		// 按上面的结论理论不可达。真到了这里宁可原样返回 —— 字节不对还能看出
		// 问题，静默丢内容则看不出来。
		return []byte(s)
	}
	return out
}

// IsBinary 粗判一段字节是不是二进制（而不是文本）。
//
// 判据只有一条：**前 8KB 里出现 NUL 字节**。文本（UTF-8 / GBK / ASCII）里
// 不会有 0x00，而 zip / jar / class 这类格式的头部很快就有 ——
// `PK\x03\x04` 与 `\xCA\xFE\xBA\xBE` 之后紧跟的字段里全是 0。
//
// 为什么需要它：补丁说明文件理论上必须是文本，但"标记说明文件"的菜单项
// 只判了"非目录"，压缩包也能标（用户 2026-10-29 把内层 zip 标成了说明文件，
// 界面直接变乱码）。zip 的二进制丢给 DecodeString 不会报错 ——
// GB18030 的映射覆盖全部字节组合，能"成功"解出一大段方块和生僻字，还会被
// ReadMePattern 匹配出一堆不存在的指令。所以解码前先挡一道。
func IsBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}
