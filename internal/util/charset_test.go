package util

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// zhSample 是照用户报障的那个补丁包里的中文串取的，纯中文、不含 ASCII，
// 这样只要解码错了就一定会暴露。
const zhSample = "获取组织机构单位树甘肃特殊处理999虚拟单位"

func gbkEncode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("GBK 编码失败: %v", err)
	}
	return b
}

// TestDecodeStringGBK 钉住 GBK 解码。
//
// 这条曾经是个 TODO 桩：GBK 分支直接 `strings.ToValidUTF8(string(b), "\uFFFD")`
// —— 把 GBK 字节当 UTF-8 处理，非法序列全变 U+FFFD。后果就是中文 Windows 上
// 导出的补丁说明文件在底部「补丁信息」面板里整段变问号。
//
// 注意断言里先确认样本**不是**合法 UTF-8：否则就算解码实现是错的（当 UTF-8
// 透传）也能蒙对，这条测试就没有牙齿了。
func TestDecodeStringGBK(t *testing.T) {
	raw := gbkEncode(t, zhSample)

	if utf8.Valid(raw) {
		t.Fatal("样本是合法 UTF-8，测不出 GBK 解码 —— 换一个样本")
	}
	if got := DecodeString(raw, CharsetGBK); got != zhSample {
		t.Errorf("GBK 解码不对\n得到: %q\n期望: %q", got, zhSample)
	}
}

// TestDetectCharsetOnGBKBytes 确认探测这一层也会把非 UTF-8 的中文认成 GBK。
// 探测和解码是两件事，得分别钉住 —— 探测猜错了，解码实现再对也没用。
func TestDetectCharsetOnGBKBytes(t *testing.T) {
	raw := gbkEncode(t, zhSample)
	if got := DetectCharset(raw); got != CharsetGBK {
		t.Errorf("GBK 字节被认成了 %q", got)
	}
}

// TestDecodeZipEntryNameGBK 钉住「GBK 条目名要解成中文」。
//
// 用户 2026-10-28 报障：打开一个补丁 zip，里面 `服务配置说明.docx` 显示成
// 一串 `?`。真因是那条条目名是 GBK 字节、且没设 UTF-8 标志位（0x800），
// Go 的 archive/zip 在这种情况下原样返回字节，非法 UTF-8 一路传到前端。
//
// 断言前先确认样本不是合法 UTF-8 —— 否则实现写成"透传"也能蒙对。
func TestDecodeZipEntryNameGBK(t *testing.T) {
	raw := string(gbkEncode(t, zhSample+".docx"))
	if utf8.ValidString(raw) {
		t.Fatal("样本是合法 UTF-8，测不出 GBK 解码 —— 换一个样本")
	}
	got := DecodeZipEntryName(raw)
	if got != zhSample+".docx" {
		t.Errorf("GBK 条目名解码不对\n得到: %q\n期望: %q", got, zhSample+".docx")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("解码结果里还有替换字符: %q", got)
	}
}

// TestDecodeZipEntryNameKeepsUTF8 确认 UTF-8 / ASCII 条目名不被改动。
//
// 这一条是"别过度解码"：带 0x800 标志位的 UTF-8 名字、以及纯 ASCII 名字，
// 解码前后必须完全相同 —— 不同就意味着我们把本来正常的包搞乱了。
func TestDecodeZipEntryNameKeepsUTF8(t *testing.T) {
	for _, s := range []string{
		"META-INF/MANIFEST.MF",
		"BOOT-INF/classes/com/example/Foo.class",
		"服务配置说明.docx", // 合法 UTF-8
	} {
		if got := DecodeZipEntryName(s); got != s {
			t.Errorf("解码改动了合法名字\n输入: %q\n得到: %q", s, got)
		}
	}
}

// TestDecodeStringUTF8Unchanged 确认没把 UTF-8 那条路弄坏。
func TestDecodeStringUTF8Unchanged(t *testing.T) {
	raw := []byte(zhSample)
	if got := DetectCharset(raw); got != CharsetUTF8 {
		t.Fatalf("合法 UTF-8 被认成了 %q", got)
	}
	if got := DecodeString(raw, CharsetUTF8); got != zhSample {
		t.Errorf("UTF-8 解码不对: %q", got)
	}
}

// TestDecodeStringInvalidUTF8BecomesReplacement 钉住 UTF-8 分支的容错口径。
//
// 对齐 Java 的 `new String(bytes, UTF_8)`：非法字节替换成 U+FFFD，不抛异常。
// 补丁说明文件是用户手写的，混进非法字节很常见，不能让它把整条链路搞崩。
func TestDecodeStringInvalidUTF8BecomesReplacement(t *testing.T) {
	got := DecodeString([]byte{'a', 0xFF, 0xFE, 'b'}, CharsetUTF8)
	if !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") {
		t.Errorf("首尾的合法字符应当保留，得到 %q", got)
	}
	if !strings.Contains(got, "\uFFFD") {
		t.Errorf("非法字节应当被替换成 U+FFFD，得到 %q", got)
	}
}

// TestEncodeStringGBKRoundTrip 钉住编码与解码能对上。
//
// 为什么必须先确认编码结果**不是**合法 UTF-8：如果实现是错的（当 UTF-8 透传），
// 那么"解回来等于原串"照样成立 —— 这条测试就没有牙齿了。GBK 的中文双字节
// 序列几乎不可能是合法 UTF-8，正好用来做这个前置检查。
func TestEncodeStringGBKRoundTrip(t *testing.T) {
	raw := EncodeString(zhSample, CharsetGBK)

	if utf8.Valid(raw) {
		t.Fatal("编码结果是合法 UTF-8，说明 GBK 分支没生效（当 UTF-8 透传了）")
	}
	if got := DecodeString(raw, CharsetGBK); got != zhSample {
		t.Errorf("GBK 编解码往返不一致\n得到: %q\n期望: %q", got, zhSample)
	}
	// 与手工用 GBK 编码器算出的字节逐字节相同，确保走的确实是标准 GBK 映射
	if want := gbkEncode(t, zhSample); string(raw) != string(want) {
		t.Errorf("GBK 编码字节不对\n得到: %x\n期望: %x", raw, want)
	}
}

// TestEncodeStringUTF8Unchanged 确认没把 UTF-8 那条路弄坏。
func TestEncodeStringUTF8Unchanged(t *testing.T) {
	raw := EncodeString(zhSample, CharsetUTF8)
	if string(raw) != zhSample {
		t.Errorf("UTF-8 分支不该改动内容: %q", raw)
	}
}

// TestEncodeStringKeepsEmojiAsGB18030NotQuestionMark 钉住【与 Java 的刻意偏差】。
//
// Java 的 `"😀".getBytes("GBK")` 得到的是 '?'（0x3F）—— 严格 GBK 编码器表示
// 不了它就按 REPLACE 丢掉。本实现改用 GB18030，把它写成 4 字节序列。
//
// 为什么这条要单独钉：偏差本身是决策，决策必须有测试守着。哪天有人"顺手对齐
// 原版"把编码器换成严格 GBK，这条会立刻红。
func TestEncodeStringKeepsEmojiAsGB18030NotQuestionMark(t *testing.T) {
	const s = "中😀文"
	raw := EncodeString(s, CharsetGBK)

	if bytes.IndexByte(raw, '?') >= 0 {
		t.Errorf("不该把表示不了的字符写成 '?'（那是 Java 的行为），得到 %x", raw)
	}
	if got := DecodeString(raw, CharsetGBK); got != s {
		t.Errorf("emoji 没能往返\n得到: %q\n期望: %q", got, s)
	}
	// 确认确实是 4 字节序列（GB18030 的特征），而不是别的编码
	if len(raw) != 2+4+2 {
		t.Errorf("字节长度 = %d，期望 8（2+4+2）: %x", len(raw), raw)
	}
}

// TestEncodeStringCoversAllRunes 是 EncodeString 里"不需要逐 rune 兜底"这个
// 结论的证据。
//
// 那条兜底循环（失败的 rune 换成 '?'）实测是死代码 —— GB18030 编码器覆盖全部
// Unicode，对任何输入都不返回 error，连非法 UTF-8 字节也会被它当 U+FFFD 编出去。
// 删掉它之后，用这条测试把结论固定下来：一旦哪天换了编码器、失败开始出现，
// 这条会红，提醒把兜底加回来。
func TestEncodeStringCoversAllRunes(t *testing.T) {
	enc := simplifiedchinese.GB18030.NewEncoder()
	var failed []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		if _, err := enc.Bytes([]byte(string(r))); err != nil {
			failed = append(failed, r)
			if len(failed) >= 5 {
				break
			}
		}
	}
	if len(failed) > 0 {
		t.Fatalf("GB18030 编码器开始有失败的 rune 了（%q）—— "+
			"EncodeString 需要把逐 rune 兜底加回来", failed)
	}

	// 非法 UTF-8 字节也不该让它失败
	if out := EncodeString(string([]byte{0x80, 0xFF}), CharsetGBK); len(out) == 0 {
		t.Error("非法字节输入不该编出空结果")
	}
}
