package tree

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTempFile 造一个磁盘文件当节点内容来源。
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}
	return p
}

// TestSetContentReplacesBytes 钉住"改完内容能读出新字节"。
func TestSetContentReplacesBytes(t *testing.T) {
	p := writeTempFile(t, "a.txt", "old")
	n := NewFile("a.txt", "a.txt", 3, time.Now(), &FileSource{Path: p}, false)

	if got, _ := n.ContentBytes(); string(got) != "old" {
		t.Fatalf("改之前应读到 old，实际 %q", got)
	}

	n.SetContent([]byte("new content"))

	got, err := n.ContentBytes()
	if err != nil {
		t.Fatalf("读内容失败: %v", err)
	}
	if string(got) != "new content" {
		t.Fatalf("改之后应读到 new content，实际 %q", got)
	}
}

// TestSetContentResetsSizeAndMd5Cache 钉住缓存必须被清掉。
//
// 这是最容易漏、后果最严重的一条：Md5/Size 是"读一遍顺手填"的缓存，
// 不清的话写包时条目头里会带着【旧大小】—— 直接把 zip 写坏。
func TestSetContentResetsSizeAndMd5Cache(t *testing.T) {
	p := writeTempFile(t, "a.txt", "old")
	n := NewFile("a.txt", "a.txt", 3, time.Now(), &FileSource{Path: p}, false)

	oldMd5 := n.GetMd5OrInit() // 先读一遍，把缓存填满
	if oldMd5 == "" {
		t.Fatal("前置条件不成立：md5 没算出来")
	}
	if n.Size != 3 {
		t.Fatalf("前置条件不成立：size=%d，期望 3", n.Size)
	}

	n.SetContent([]byte("new content"))

	// 换内容之后，两个缓存必须回到"未计算"状态
	if n.Size != SizeUnknown {
		t.Errorf("SetContent 后 Size 应重置为 SizeUnknown，实际 %d", n.Size)
	}
	if n.Md5 != "" {
		t.Errorf("SetContent 后 Md5 应清空，实际 %q", n.Md5)
	}

	// 重新读一遍，应当算出新值
	newMd5 := n.GetMd5OrInit()
	if newMd5 == oldMd5 {
		t.Error("md5 没有随内容更新 —— 写包时会写出旧条目的校验值")
	}
	if n.Size != int64(len("new content")) {
		t.Errorf("size 没有随内容更新：%d，期望 %d", n.Size, len("new content"))
	}
}

// TestSetContentCopiesData 钉住"外部改动切片不会影响节点内容"。
//
// 不复制的话调用方复用同一个 buffer 就会把节点内容一起改掉，
// 这种 bug 只在大文件分块读时才会冒出来，很难查。
func TestSetContentCopiesData(t *testing.T) {
	p := writeTempFile(t, "a.txt", "old")
	n := NewFile("a.txt", "a.txt", 3, time.Now(), &FileSource{Path: p}, false)

	buf := []byte("hello")
	n.SetContent(buf)
	buf[0] = 'X' // 调用方复用了这块内存

	got, _ := n.ContentBytes()
	if string(got) != "hello" {
		t.Fatalf("SetContent 应复制数据，实际读到 %q", got)
	}
}

// TestEditedFlag 钉住"已修改"标记的开关。
func TestEditedFlag(t *testing.T) {
	p := writeTempFile(t, "a.txt", "old")
	n := NewFile("a.txt", "a.txt", 3, time.Now(), &FileSource{Path: p}, false)

	if n.Edited() {
		t.Fatal("新节点不应带已修改标记")
	}
	n.SetContent([]byte("x"))
	if !n.Edited() {
		t.Fatal("SetContent 之后应带已修改标记")
	}
	n.SetEdited(false)
	if n.Edited() {
		t.Fatal("SetEdited(false) 之后标记应被清掉")
	}
}

// TestMemorySourceReopenable 钉住"同一个来源可以反复打开"。
//
// md5 计算与写包会各读一遍，如果 Open 返回共享的同一个 reader，
// 第二次读会读到空 —— 表现是"改完内容写出去是个空文件"。
func TestMemorySourceReopenable(t *testing.T) {
	src := &MemorySource{Data: []byte("abc")}
	for i := 0; i < 3; i++ {
		rc, err := src.Open()
		if err != nil {
			t.Fatalf("第 %d 次 Open 失败: %v", i+1, err)
		}
		buf := make([]byte, 8)
		n, _ := rc.Read(buf)
		rc.Close()
		if string(buf[:n]) != "abc" {
			t.Fatalf("第 %d 次读到 %q，期望 abc", i+1, buf[:n])
		}
	}
}
