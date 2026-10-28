package service

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// 本文件钉住「重打包不改条目名编码」这一条。
//
// 背景：打开包时 GBK 条目名被解码成 UTF-8 存进 Node.Path（否则界面全是 `?`），
// 于是写回时**不能**拿 Path 当条目名 —— 否则包里那条名字会从 GBK 悄悄变成
// UTF-8。Node.RawPath 存原始字节就是为了这件事。
//
// 光断言 hdr.Name 不够：真正会出事的是 archive/zip 的 UTF-8 标志位 ——
// 名字是非法 UTF-8 却不设 NonUTF8 的话，标准库会把它当"野 UTF-8"处理，
// 读回来就被按 UTF-8 解。所以这里真写一遍再读回来验。

// gbkNameNode 造一个"从 GBK 条目建出来的"文件节点。
func gbkNameNode(t *testing.T, zh string) (*tree.Node, string) {
	t.Helper()
	raw := string(util.EncodeString(zh, util.CharsetGBK))
	if raw == zh {
		t.Fatalf("%q 的 GBK 编码与 UTF-8 相同，测不出东西", zh)
	}
	now := time.Now()
	n := tree.NewCompressFile(zh, zh, 1, 0, now, now, now, nil, true)
	n.RawPath = raw
	return n, raw
}

// TestNewEntryHeaderKeepsGBKNameBytes 原始字节写出去仍是原始字节、且不带 0x800。
func TestNewEntryHeaderKeepsGBKNameBytes(t *testing.T) {
	const zh = "服务配置说明.docx"
	node, raw := gbkNameNode(t, zh)

	hdr := newEntryHeader(node, time.Now())
	if hdr.Name != raw {
		t.Errorf("hdr.Name = %q，期望原始 GBK 字节 %q", hdr.Name, raw)
	}
	if !hdr.NonUTF8 {
		t.Error("hdr.NonUTF8 应为 true，否则标准库会给 GBK 字节打上 UTF-8 标志")
	}

	// 真写一遍再读回来：确认 archive/zip 没偷偷加标志位、没改写名字。
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("条目数 = %d，期望 1", len(zr.File))
	}
	f := zr.File[0]
	if f.Name != raw {
		t.Errorf("读回的条目名 = %q，期望原始 GBK 字节 %q", f.Name, raw)
	}
	if f.Flags&0x800 != 0 {
		t.Errorf("不该设 UTF-8 标志位，Flags = 0x%04x", f.Flags)
	}
	// 解码回来仍是那个中文名 —— 这才是"包没被搞坏"的最终证据
	if got := util.DecodeZipEntryName(f.Name); got != zh {
		t.Errorf("读回再解码 = %q，期望 %q", got, zh)
	}
}

// TestNewEntryHeaderNewNodeWritesUTF8 新建节点没有原始字节，按 UTF-8 写。
//
// 这一条守的是"别过度编码"：拖进来的文件、补丁新增的文件都走这条，
// 它们的名字本来就是 UTF-8，不能被塞进 NonUTF8 分支。
func TestNewEntryHeaderNewNodeWritesUTF8(t *testing.T) {
	const zh = "服务配置说明.docx"
	now := time.Now()
	node := tree.NewCompressFile(zh, zh, 1, 0, now, now, now, nil, true)

	hdr := newEntryHeader(node, now)
	if hdr.Name != zh {
		t.Errorf("hdr.Name = %q，期望 %q", hdr.Name, zh)
	}
	if hdr.NonUTF8 {
		t.Error("新建节点不该标 NonUTF8")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if got := zr.File[0].Name; got != zh {
		t.Errorf("读回的条目名 = %q，期望 %q", got, zh)
	}
	if zr.File[0].Flags&0x800 == 0 {
		t.Error("合法 UTF-8 名字应带上 0x800 标志位")
	}
}

// TestEntryNameOfDirGetsTrailingSlash 目录的 RawPath 不带斜杠时也要补上。
//
// 占位目录的 RawPath 来自 util.GetDirPath（不带斜杠），而 zip 规范要求目录
// 条目名以 '/' 结尾 —— 少了它，解压出来的就不是目录。
func TestEntryNameOfDirGetsTrailingSlash(t *testing.T) {
	raw := string(util.EncodeString("服务说明", util.CharsetGBK))
	now := time.Now()
	node := tree.NewCompressDir("服务说明", "服务说明", now, now, now, true)
	node.RawPath = raw

	name, nonUTF8 := entryNameOf(node)
	if name != raw+"/" {
		t.Errorf("name = %q，期望 %q", name, raw+"/")
	}
	if !nonUTF8 {
		t.Error("nonUTF8 应为 true")
	}

	// 已经带斜杠的（真目录条目的 RawPath）不能被补成两个
	node.RawPath = raw + "/"
	if name, _ := entryNameOf(node); name != raw+"/" {
		t.Errorf("幂等性坏了：name = %q", name)
	}
}
