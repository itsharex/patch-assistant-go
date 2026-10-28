package pack

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
	"unicode/utf8"

	"cn.cpoet.patch.assistant/internal/util"
)

// entrySpec 是造测试 zip 用的条目描述。
type entrySpec struct {
	name string
	when time.Time // 零值表示"不设置时间戳"，模拟很多补丁 zip 的情况
}

func buildTestZip(t *testing.T, items []entrySpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range items {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Store}
		if !e.when.IsZero() {
			hdr.Modified = e.when
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("CreateHeader %s: %v", e.name, err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatalf("write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// buildRawNameZip 造一个条目名是**原始字节**的 zip（模拟 GBK 名字、无 0x800
// 标志位的包 —— 中文 Windows 上的打包工具多半是这么写的）。
func buildRawNameZip(t *testing.T, rawName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: rawName, Method: zip.Store, NonUTF8: true}
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader %q: %v", rawName, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("write %q: %v", rawName, err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestBuildZipTreeDecodesGBKEntryName 钉住「GBK 条目名进树后是中文」。
//
// 用户 2026-10-28 报障：补丁 zip 里 `服务配置说明.docx` 在树上显示成一串 `?`。
// 这条测试同时钉住两件事：
//   - Path / Name 是解码后的 UTF-8（界面与匹配用）；
//   - RawPath 留着原始 GBK 字节（重打包时原样写回，见 service.entryNameOf）。
func TestBuildZipTreeDecodesGBKEntryName(t *testing.T) {
	const zh = "服务配置说明.docx"
	raw := string(util.EncodeString(zh, util.CharsetGBK))
	// 前置自检：真不是合法 UTF-8，否则这条测试蒙对也能过
	if utf8.ValidString(raw) {
		t.Fatal("样本是合法 UTF-8，测不出解码 —— 换一个样本")
	}

	root, err := BuildZipTreeFromBytes("p.zip", buildRawNameZip(t, raw, "hello"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 {
		t.Fatalf("子节点数 = %d，期望 1", len(root.Children))
	}
	child := root.Children[0]
	if child.Name != zh {
		t.Errorf("Name = %q，期望 %q", child.Name, zh)
	}
	if child.Path != zh {
		t.Errorf("Path = %q，期望 %q", child.Path, zh)
	}
	if child.RawPath != raw {
		t.Errorf("RawPath 应保留原始 GBK 字节\n得到: %q\n期望: %q", child.RawPath, raw)
	}
}

// TestBuildZipTreeGBKDirName 目录条目同理：Path 保留尾斜杠，RawPath 也是原始字节。
func TestBuildZipTreeGBKDirName(t *testing.T) {
	raw := string(util.EncodeString("服务说明/", util.CharsetGBK))
	if utf8.ValidString(raw) {
		t.Fatal("样本是合法 UTF-8，测不出解码")
	}
	root, err := BuildZipTreeFromBytes("p.zip", buildRawNameZip(t, raw, ""), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 {
		t.Fatalf("子节点数 = %d，期望 1", len(root.Children))
	}
	dir := root.Children[0]
	if dir.Name != "服务说明" {
		t.Errorf("Name = %q，期望 服务说明（目录名不带斜杠）", dir.Name)
	}
	if dir.Path != "服务说明/" {
		t.Errorf("Path = %q，期望保留尾斜杠", dir.Path)
	}
	if dir.RawPath != raw {
		t.Errorf("RawPath = %q，期望原始字节 %q", dir.RawPath, raw)
	}
}

// TestBuildZipTreeASCIIKeepsRawPathEmpty 纯 ASCII 名字不留原始字节。
//
// RawPath 的约定是"解码前后不同才非空"：ASCII 名字留空，写回时走 Path，
// 免得每条条目都带一份重复的字节、也免得给它们设 NonUTF8 标志。
func TestBuildZipTreeASCIIKeepsRawPathEmpty(t *testing.T) {
	root, err := BuildZipTreeFromBytes("p.zip",
		buildRawNameZip(t, "META-INF/MANIFEST.MF", "x"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 1 || len(root.Children[0].Children) != 1 {
		t.Fatalf("树形不对: %+v", root.Children)
	}
	if got := root.Children[0].Children[0].RawPath; got != "" {
		t.Errorf("ASCII 名字的 RawPath = %q，期望空串", got)
	}
}

// TestModTimeRealTimestampPreserved 真实时间戳应当原样返回，且不被哨兵判定误伤。
func TestModTimeRealTimestampPreserved(t *testing.T) {
	data := buildTestZip(t, []entrySpec{
		{name: "real2020.txt", when: time.Date(2020, 11, 9, 9, 25, 52, 0, time.Local)},
		{name: "real1980.txt", when: time.Date(1980, 5, 5, 22, 38, 20, 0, time.Local)},
	})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		got := modTime(f)
		want := f.Modified
		if !got.Equal(want) {
			t.Fatalf("%s: 真实时间戳应原样返回，期望 %v 实际 %v", f.Name, want, got)
		}
	}
}

// TestModTimeZeroTimestampFallsBackToNow 核心回归：零 DOS 时间戳的条目不能再被
// 解析成 1979-11-30 的假时间，必须回落到当前时间（与 zip.go 注释本意一致）。
func TestModTimeZeroTimestampFallsBackToNow(t *testing.T) {
	data := buildTestZip(t, []entrySpec{
		{name: "zero.txt"}, // 零时间戳
	})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("应有 1 个条目，实际 %d", len(zr.File))
	}
	got := modTime(zr.File[0])

	// 必须落在 MS-DOS 历元之后（即 time.Now() 兜底，而非 1979-11-30 哨兵）。
	epoch := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.After(epoch) {
		t.Fatalf("zero.txt 应回落到当前时间（晚于 %v），实际 %v", epoch, got)
	}
	// 兜底时间应当就是"现在"（合理的误差窗口内）。
	if d := time.Since(got); d < 0 || d > time.Minute {
		t.Fatalf("zero.txt 兜底时间应是当前时间，实际偏差 %v", d)
	}
}
