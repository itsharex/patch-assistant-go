package service

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件的夹具复用 patch_e2e_test.go 的 writeZip / innerJarBytes。

func md5Of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// zipOf 把 路径->内容 的 map 写成一个临时 zip，返回路径。
// dirs 里的名字按目录条目写入（isDir）—— 用来钉住"目录条目不参与比对"。
func zipOf(t *testing.T, files map[string]string, dirs []string) string {
	t.Helper()
	entries := make([]zipEntry, 0, len(files)+len(dirs))
	for _, d := range dirs {
		entries = append(entries, zipEntry{name: d, isDir: true})
	}
	for name, body := range files {
		entries = append(entries, zipEntry{name: name, body: body})
	}
	return writeZip(t, t.TempDir(), "test.jar", entries)
}

// 按条目名读内容（"点开看某个文件差在哪"走的那条路）。
func TestReadPackEntry(t *testing.T) {
	a := zipOf(t, map[string]string{
		"a.txt":                "hello",
		"com/foo/Bar.class":    "bar-bytes",
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\n",
	}, []string{"com/", "com/foo/"})

	// 读得到：内容逐字节一致
	e, err := ReadPackEntry(a, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !e.Found || string(e.Bytes) != "hello" {
		t.Fatalf("Found=%v Bytes=%q，期望 hello", e.Found, e.Bytes)
	}
	if len(e.Inners) != 0 {
		t.Fatalf("非 .class 不该带内部类，实际 %d 个", len(e.Inners))
	}

	// 读不到不是错误：Found=false（另一侧可能也没有，由调用方判）
	e, err = ReadPackEntry(a, "nope.txt")
	if err != nil {
		t.Fatal(err)
	}
	if e.Found || e.Bytes != nil {
		t.Fatalf("不存在的条目应当 Found=false，实际 Found=%v Bytes=%q", e.Found, e.Bytes)
	}

	// 目录条目不算文件 —— 与 readPackEntries 同一口径
	e, err = ReadPackEntry(a, "com/foo/")
	if err != nil {
		t.Fatal(err)
	}
	if e.Found {
		t.Fatal("目录条目不该被当成文件读出内容")
	}

	// 非 zip：报错文案要与比对那条路一致（前端会原样 toast 出来）
	notZip := filepath.Join(t.TempDir(), "x.jar")
	if err := os.WriteFile(notZip, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadPackEntry(notZip, "a.txt")
	if err == nil || !strings.Contains(err.Error(), "不是有效的 jar/zip") {
		t.Fatalf("err=%v，期望\"不是有效的 jar/zip\"", err)
	}
}

// .class 要把**同目录**的内部类一起读出来（反编译要用），
// 且不能把同目录的其它类、别的目录的同名内部类带进来。
//
// 这条口径对应 gui/content.go 的 classInners（树那边内部类是主类的子节点），
// 两边说的都是"反编译时内部类必须和主类一起喂给 fernflower"。
func TestReadPackEntry_ClassInners(t *testing.T) {
	a := zipOf(t, map[string]string{
		"com/foo/Bar.class":       "main",
		"com/foo/Bar$Inner.class": "inner-1",
		"com/foo/Bar$Deep.class":  "inner-2",
		"com/foo/Baz.class":       "sibling",
		"com/foo/BarExtra.class":  "prefix-but-not-inner",
		"com/other/Bar$X.class":   "other-dir",
		"com/foo/Bar.txt":         "not-a-class",
	}, nil)

	e, err := ReadPackEntry(a, "com/foo/Bar.class")
	if err != nil {
		t.Fatal(err)
	}
	if string(e.Bytes) != "main" {
		t.Fatalf("主类字节=%q，期望 main", e.Bytes)
	}
	var got []string
	for _, b := range e.Inners {
		got = append(got, string(b))
	}
	// 按条目名排序：Bar$Deep 在 Bar$Inner 之前
	if strings.Join(got, ",") != "inner-2,inner-1" {
		t.Fatalf("内部类=%v，期望只有同目录的 Bar$Deep / Bar$Inner（按名字排序）", got)
	}
}

func TestComparePacks_Identical(t *testing.T) {
	files := map[string]string{
		"a.txt":                "hello",
		"com/foo/Bar.class":    "class-bytes",
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\n",
	}
	a := zipOf(t, files, nil)
	b := zipOf(t, files, nil)

	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Same != 3 || res.TotalA != 3 || res.TotalB != 3 {
		t.Fatalf("Same=%d TotalA=%d TotalB=%d，期望都是 3", res.Same, res.TotalA, res.TotalB)
	}
	if res.DiffCount() != 0 {
		t.Fatalf("DiffCount=%d，期望 0", res.DiffCount())
	}
	if !strings.Contains(res.Text, "完全一致") {
		t.Fatalf("报告里应有\"完全一致\"，实际：\n%s", res.Text)
	}
	// 路径/文件名要回显
	if res.NameA != "test.jar" || res.NameB != "test.jar" {
		t.Fatalf("NameA=%q NameB=%q", res.NameA, res.NameB)
	}
	if res.PathA != a || res.PathB != b {
		t.Fatalf("PathA=%q PathB=%q", res.PathA, res.PathB)
	}
}

func TestComparePacks_ChangedAddedRemoved(t *testing.T) {
	a := zipOf(t, map[string]string{
		"keep.txt":          "same",
		"com/foo/Bar.class": "old-bytes",
		"com/foo/Baz.class": "baz",
		"old/Removed.class": "gone",
	}, nil)
	b := zipOf(t, map[string]string{
		"keep.txt":          "same",             // 相同
		"com/foo/Bar.class": "new-bytes-longer", // 内容不同
		"com/foo/Baz.class": "baz",              // 相同
		"new/Added.class":   "fresh",            // 仅 B 有
		"new/Added2.class":  "fresh2",           // 仅 B 有
	}, nil)

	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Same != 2 {
		t.Fatalf("Same=%d，期望 2", res.Same)
	}
	if len(res.Changed) != 1 || res.Changed[0].Path != "com/foo/Bar.class" {
		t.Fatalf("Changed=%+v，期望只有 com/foo/Bar.class", res.Changed)
	}
	c := res.Changed[0]
	if c.A.Md5 != md5Of("old-bytes") || c.B.Md5 != md5Of("new-bytes-longer") {
		t.Fatalf("MD5 不对：A=%s B=%s", c.A.Md5, c.B.Md5)
	}
	if c.A.Size != int64(len("old-bytes")) || c.B.Size != int64(len("new-bytes-longer")) {
		t.Fatalf("大小不对：A=%d B=%d", c.A.Size, c.B.Size)
	}
	if len(res.Removed) != 1 || res.Removed[0].Path != "old/Removed.class" {
		t.Fatalf("Removed=%+v，期望只有 old/Removed.class", res.Removed)
	}
	if len(res.Added) != 2 || res.Added[0].Path != "new/Added.class" || res.Added[1].Path != "new/Added2.class" {
		t.Fatalf("Added=%+v，期望按路径排序的两条", res.Added)
	}
	if res.TotalA != 4 || res.TotalB != 5 {
		t.Fatalf("TotalA=%d TotalB=%d，期望 4/5", res.TotalA, res.TotalB)
	}
	// 报告文本：汇总行 + 三个分组的关键内容都要在
	for _, want := range []string{
		"内容不同 1 · 仅 A 有 1 · 仅 B 有 2 · 相同 2",
		"com/foo/Bar.class", md5Of("old-bytes"), md5Of("new-bytes-longer"),
		"old/Removed.class", "new/Added2.class",
	} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("报告缺少 %q，实际：\n%s", want, res.Text)
		}
	}
}

// 目录条目不能算进文件数、也不能变成"仅一侧有"。
func TestComparePacks_DirEntriesSkipped(t *testing.T) {
	a := zipOf(t, map[string]string{"d/x.txt": "1"}, []string{"d/"})
	b := zipOf(t, map[string]string{"d/x.txt": "1"}, nil) // B 没有显式目录条目
	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalA != 1 || res.TotalB != 1 || res.Same != 1 {
		t.Fatalf("TotalA=%d TotalB=%d Same=%d，期望都是 1（目录条目应被跳过）",
			res.TotalA, res.TotalB, res.Same)
	}
	if res.DiffCount() != 0 {
		t.Fatalf("DiffCount=%d，期望 0", res.DiffCount())
	}
}

// 嵌套 jar 按普通二进制文件比：整包字节不同就报"内容不同"，不递归展开。
func TestComparePacks_NestedJarAsBlob(t *testing.T) {
	oldBytes := innerJarBytes(t, map[string]string{"A.class": "old"})
	newBytes := innerJarBytes(t, map[string]string{"A.class": "new"})

	dir := t.TempDir()
	a := filepath.Join(dir, "app-old.jar")
	b := filepath.Join(dir, "app-new.jar")
	if err := os.WriteFile(a, oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, newBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	// 两个 jar 里都装着 A.class，但字节不同 → 报 1 条内容不同
	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Changed[0].Path != "A.class" {
		t.Fatalf("Changed=%+v，期望 A.class 一条", res.Changed)
	}

	// 同一份字节写两遍 → 判相同
	a2 := filepath.Join(t.TempDir(), "a2.jar")
	b2 := filepath.Join(t.TempDir(), "b2.jar")
	if err := os.WriteFile(a2, oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b2, oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := ComparePacks(a2, b2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Same != 1 || res2.DiffCount() != 0 {
		t.Fatalf("Same=%d DiffCount=%d，期望 1/0", res2.Same, res2.DiffCount())
	}
}

// 同路径选两次要拒绝 —— 不拒绝的话结果是"全部相同"，用户会以为选错包。
func TestComparePacks_SamePathRejected(t *testing.T) {
	a := zipOf(t, map[string]string{"x": "1"}, nil)
	if _, err := ComparePacks(a, a); err == nil {
		t.Fatal("同一条路径应报错")
	}
	if _, err := ComparePacks(a, "  "+a+"  "); err == nil {
		t.Fatal("trim 之后同一条路径也应报错")
	}
	if _, err := ComparePacks("", a); err == nil {
		t.Fatal("空路径应报错")
	}
}

// 非 zip 文件要给"不是有效的 jar/zip"而不是把 zip 库的原文抛出去。
func TestComparePacks_NotAZip(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "plain-a.jar")
	p2 := filepath.Join(dir, "plain-b.jar")
	if err := os.WriteFile(p1, []byte("not a zip at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("also not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ComparePacks(p1, p2)
	if err == nil || !strings.Contains(err.Error(), "不是有效的 jar/zip") {
		t.Fatalf("err=%v，期望提示\"不是有效的 jar/zip\"", err)
	}
}

// 时间戳不同但内容相同 → 仍然算相同（比对口径是内容，不是 mtime）。
func TestComparePacks_ModTimeIgnored(t *testing.T) {
	a := zipOf(t, map[string]string{"x.txt": "same"}, nil)
	b := zipOf(t, map[string]string{"x.txt": "same"}, nil)
	// 两个文件必然在不同时刻创建，天然覆盖"mtime 不同"
	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Same != 1 || res.DiffCount() != 0 {
		t.Fatalf("Same=%d DiffCount=%d，期望 1/0（mtime 不应影响判定）", res.Same, res.DiffCount())
	}
}

// 大小相同但内容不同 → 必须报"内容不同"。钉住"判定不只靠 size"。
func TestComparePacks_SameSizeDifferentContent(t *testing.T) {
	a := zipOf(t, map[string]string{"x.txt": "aaaa"}, nil)
	b := zipOf(t, map[string]string{"x.txt": "bbbb"}, nil)
	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Same != 0 {
		t.Fatalf("Changed=%d Same=%d，期望 1/0（同大小不同内容要算不同）",
			len(res.Changed), res.Same)
	}
	c := res.Changed[0]
	if c.A.Size != 4 || c.B.Size != 4 {
		t.Fatalf("A.Size=%d B.Size=%d，期望都是 4", c.A.Size, c.B.Size)
	}
	if c.A.Md5 != md5Of("aaaa") || c.B.Md5 != md5Of("bbbb") {
		t.Fatalf("展示用的 md5 不对：A=%s B=%s", c.A.Md5, c.B.Md5)
	}
}

// 判定"相同"的条目**不许被解压** —— 这是 143 MB 胖包上 498 ms → 3 ms 的全部
// 原因（见 jarcompare.go 里 packSource 的注释）。用"数据被改过、中央目录里的
// CRC 还是原内容"的条目来钉这件事：只要代码真的去读，Go 的 zip reader 会在
// 读完时报 ErrChecksum，对比直接失败。
//
// 反过来，落进"仅 A 有 / 仅 B 有 / 内容不同"的条目必须真的读（要算 md5 展示），
// 所以同一个坏条目一旦进了展示集合就应该报错 —— 那是对照组，证明本测试不是
// 空过。
func TestComparePacks_SameEntriesAreNotRead(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("0123456789abcdef", 64) // 1 KB，够长才有意义
	a := storedZip(t, dir, "a.jar", "blob.bin", body)
	b := storedZip(t, dir, "b.jar", "blob.bin", body)
	corruptEntryData(t, a, "blob.bin")
	corruptEntryData(t, b, "blob.bin")

	// 两侧中央目录的 size/CRC 都是原内容的，所以判为"相同"，一个字节都不读
	res, err := ComparePacks(a, b)
	if err != nil {
		t.Fatalf("相同条目不该被解压，却报错了：%v", err)
	}
	if res.Same != 1 || res.DiffCount() != 0 {
		t.Fatalf("Same=%d DiffCount=%d，期望 1/0", res.Same, res.DiffCount())
	}

	// 对照组：同一条坏条目落进"仅 A 有"（要展示 → 必须算 md5）→ 一定被读 → 报错
	other := storedZip(t, dir, "other.jar", "another.bin", body)
	if _, err := ComparePacks(a, other); err == nil {
		t.Fatal("需要展示的条目应当被解压，坏 CRC 应当报错（说明读路径没生效）")
	}
}

// storedZip 写一个**不压缩**的单条目 zip。不压缩是为了让"改坏内容"只动数据
// 字节、不破坏 deflate 流（压缩流里改一个字节通常直接解压失败，报的错就不是
// CRC 而是 flate 的，钉不住想钉的那件事）。
func storedZip(t *testing.T, dir, name, entry, body string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: entry, Method: zip.Store})
	if err != nil {
		t.Fatalf("创建 zip 条目 %s 失败: %v", entry, err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("写入 zip 条目 %s 失败: %v", entry, err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写出 %s 失败: %v", path, err)
	}
	return path
}

// corruptEntryData 把某个条目的第一个数据字节取反，**不动头部** ——
// 中央目录里记的 CRC 还是原内容的，所以任何真的去读它的代码都会撞 ErrChecksum。
func corruptEntryData(t *testing.T, zipPath, entry string) {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", zipPath, err)
	}
	off := int64(-1)
	for _, zf := range zr.File {
		if zf.Name == entry {
			off, err = zf.DataOffset()
			if err != nil {
				zr.Close()
				t.Fatalf("取 %s 的数据偏移失败: %v", entry, err)
			}
			break
		}
	}
	zr.Close()
	if off < 0 {
		t.Fatalf("%s 里找不到条目 %s", zipPath, entry)
	}
	raw, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[off] ^= 0xFF
	if err := os.WriteFile(zipPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// 单文件对比（CompareFiles）
// ---------------------------------------------------------------------------

// writeDiskFile 往临时目录写一个普通文件，返回路径。
func writeDiskFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompareFiles_Same(t *testing.T) {
	dir := t.TempDir()
	a := writeDiskFile(t, dir, "a.txt", "hello\n")
	b := writeDiskFile(t, dir, "b.txt", "hello\n")

	res, err := CompareFiles(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "file" {
		t.Fatalf("Kind=%q，期望 file", res.Kind)
	}
	if !res.Same {
		t.Fatal("同内容应判相同")
	}
	if res.Md5A != res.Md5B || res.Md5A != md5Of("hello\n") {
		t.Fatalf("md5 不对：A=%s B=%s 期望 %s", res.Md5A, res.Md5B, md5Of("hello\n"))
	}
	if res.SizeA != int64(len("hello\n")) || res.SizeB != int64(len("hello\n")) {
		t.Fatalf("大小不对：A=%d B=%d", res.SizeA, res.SizeB)
	}
	if res.NameA != "a.txt" || res.NameB != "b.txt" {
		t.Fatalf("NameA=%q NameB=%q", res.NameA, res.NameB)
	}
	if res.PathA != a || res.PathB != b {
		t.Fatalf("PathA=%q PathB=%q", res.PathA, res.PathB)
	}
	if !strings.Contains(res.Text, "完全一致") {
		t.Fatalf("报告里应有\"完全一致\"，实际：\n%s", res.Text)
	}
}

func TestCompareFiles_Different(t *testing.T) {
	dir := t.TempDir()
	a := writeDiskFile(t, dir, "a.txt", "v = 1;\n")
	b := writeDiskFile(t, dir, "b.txt", "v = 2;\n")

	res, err := CompareFiles(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Same {
		t.Fatal("不同内容不应判相同")
	}
	if res.Md5A != md5Of("v = 1;\n") || res.Md5B != md5Of("v = 2;\n") {
		t.Fatalf("md5 不对：A=%s B=%s", res.Md5A, res.Md5B)
	}
	// 报告：两侧路径 + 两个 md5 + "不同"结论都要在
	for _, want := range []string{a, b, res.Md5A, res.Md5B, "内容不同"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("报告缺少 %q，实际：\n%s", want, res.Text)
		}
	}
}

// 空路径 / 同一条路径要拒绝；目录要给"不接受目录"。
func TestCompareFiles_Rejects(t *testing.T) {
	dir := t.TempDir()
	a := writeDiskFile(t, dir, "a.txt", "x")

	if _, err := CompareFiles("", a); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := CompareFiles(a, ""); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := CompareFiles(a, a); err == nil {
		t.Fatal("同一条路径应报错")
	}
	if _, err := CompareFiles("  "+a+"  ", a); err == nil {
		t.Fatal("trim 之后同一条路径也应报错")
	}
	// 目录：明确报"不接受目录"，而不是把底层 io 错误抛出去
	if _, err := CompareFiles(dir, a); err == nil || !strings.Contains(err.Error(), "目录") {
		t.Fatalf("目录应报错，实际 %v", err)
	}
	// 不存在的文件：报错
	if _, err := CompareFiles(filepath.Join(dir, "nope.txt"), a); err == nil {
		t.Fatal("不存在的文件应报错")
	}
}

// ---------------------------------------------------------------------------
// 按字节对比嵌套包（ComparePackBytes）—— 与 ComparePacks 同一套规则
// ---------------------------------------------------------------------------

func TestComparePackBytes(t *testing.T) {
	a := innerJarBytes(t, map[string]string{"A.class": "old", "keep.txt": "k"})
	b := innerJarBytes(t, map[string]string{"A.class": "new", "keep.txt": "k"})

	res, err := ComparePackBytes("inner.jar", a, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "pack" {
		t.Fatalf("Kind=%q，期望 pack", res.Kind)
	}
	if len(res.Changed) != 1 || res.Changed[0].Path != "A.class" {
		t.Fatalf("Changed=%+v，期望只有 A.class", res.Changed)
	}
	if res.Same != 1 || res.TotalA != 2 || res.TotalB != 2 {
		t.Fatalf("Same=%d TotalA=%d TotalB=%d，期望 1/2/2", res.Same, res.TotalA, res.TotalB)
	}
	if res.NameA != "inner.jar" || res.NameB != "inner.jar" {
		t.Fatalf("NameA=%q NameB=%q", res.NameA, res.NameB)
	}
	// 报告文本照常给
	if !strings.Contains(res.Text, "A.class") {
		t.Fatalf("报告应含差异条目，实际：\n%s", res.Text)
	}
	// 非 zip 字节：报"不是有效的 jar/zip"而不是把 zip 库原文抛出去
	if _, err := ComparePackBytes("x.jar", []byte("not a zip"), b); err == nil {
		t.Fatal("非 zip 字节应报错")
	}
}

func TestReadPackEntryBytes(t *testing.T) {
	data := innerJarBytes(t, map[string]string{
		"a.txt":                "hello",
		"com/foo/Bar.class":    "bar",
		"com/foo/Bar$In.class": "in",
	})

	pe, err := ReadPackEntryBytes("inner.jar", data, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !pe.Found || string(pe.Bytes) != "hello" {
		t.Fatalf("Found=%v Bytes=%q，期望 hello", pe.Found, pe.Bytes)
	}

	// .class 条目要带上同目录的内部类（与 ReadPackEntry 同口径）
	pe, err = ReadPackEntryBytes("inner.jar", data, "com/foo/Bar.class")
	if err != nil {
		t.Fatal(err)
	}
	if !pe.Found || string(pe.Bytes) != "bar" || len(pe.Inners) != 1 || string(pe.Inners[0]) != "in" {
		t.Fatalf("class 条目=%+v，期望主类 bar + 内部类 in", pe)
	}

	// 不存在的条目：Found=false，不是错误
	pe, err = ReadPackEntryBytes("inner.jar", data, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if pe.Found {
		t.Fatal("不存在的条目应 Found=false")
	}
}
