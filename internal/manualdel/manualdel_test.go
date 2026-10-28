package manualdel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newStore 造一个指向临时目录的 Store。直接绕过 util.GetAppConfigPath，
// 让测试不去读用户真实的 %APPDATA%。
func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("造 Store 失败: %v", err)
	}
	return s
}

func absPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// TestSaveLoadRoundTrip 基本读写闭环：落盘后能读回同样的内容。
func TestSaveLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "realpay-server.jar")

	// 空包第一次读：零条，且**不能报错**。
	got, err := s.Load(jar)
	if err != nil {
		t.Fatalf("空包第一次读应无错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空包第一次读应零条，实际 %d", len(got))
	}

	want := []Entry{
		{Path: "BOOT-INF/classes/bootstrap.yml", Action: ActionPendingDelete},
		{Path: "BOOT-INF/classes/com/x/Foo.class", Action: ActionMarkDelete},
	}
	s.Save(jar, want, nil)

	got, err = s.Load(jar)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	// Load 保证按 Path 升序
	if len(got) != 2 {
		t.Fatalf("读回 %d 条，期望 2", len(got))
	}
	if got[0].Path != "BOOT-INF/classes/bootstrap.yml" {
		t.Errorf("排序错: 第一条 = %q", got[0].Path)
	}
	if got[0].Action != ActionPendingDelete {
		t.Errorf("action = %q，期望 %q", got[0].Action, ActionPendingDelete)
	}
}

// TestSaveOverwrites 第二次 Save 是覆盖语义（无追加），否则重做一次就多一条。
func TestSaveOverwrites(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "a.jar")

	s.Save(jar, []Entry{{Path: "a", Action: ActionPendingDelete}}, nil)
	s.Save(jar, []Entry{{Path: "b", Action: ActionPendingDelete}}, nil)

	got, _ := s.Load(jar)
	if len(got) != 1 || got[0].Path != "b" {
		t.Errorf("覆盖语义失效: %+v", got)
	}
}

// TestDifferentJarsAreIsolated 两个包的记录必须互不串味 ——
// 按路径 SHA-1 分文件就是为了这个。
func TestDifferentJarsAreIsolated(t *testing.T) {
	s := newStore(t)
	a := absPath(t, "a.jar")
	b := absPath(t, "b.jar")

	s.Save(a, []Entry{{Path: "only-a", Action: ActionPendingDelete}}, nil)
	s.Save(b, []Entry{{Path: "only-b", Action: ActionPendingDelete}}, nil)

	ga, _ := s.Load(a)
	gb, _ := s.Load(b)
	if len(ga) != 1 || ga[0].Path != "only-a" {
		t.Errorf("包 a 记录错: %+v", ga)
	}
	if len(gb) != 1 || gb[0].Path != "only-b" {
		t.Errorf("包 b 记录错: %+v", gb)
	}
}

// TestPathWithChineseCharsAndSpaces 路径含中文/空格是真实场景，
// 但文件名是 hex 编码，不能因此失败。
func TestPathWithChineseCharsAndSpaces(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "中文 目录 (2)/应用包.jar")

	s.Save(jar, []Entry{{Path: "内部/a.yml", Action: ActionMarkDelete}}, nil)

	got, err := s.Load(jar)
	if err != nil {
		t.Fatalf("中文路径读回失败: %v", err)
	}
	if len(got) != 1 || got[0].Path != "内部/a.yml" {
		t.Errorf("中文路径记录错: %+v", got)
	}
}

// TestClearRemovesFile 清空后 Load 应回到零条。
func TestClearRemovesFile(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "c.jar")

	s.Save(jar, []Entry{{Path: "x", Action: ActionPendingDelete}}, nil)
	if err := s.Clear(jar); err != nil {
		t.Fatalf("Clear 失败: %v", err)
	}
	// 不存在的文件再清一次也应算成功（幂等）
	if err := s.Clear(jar); err != nil {
		t.Fatalf("重复 Clear 应幂等: %v", err)
	}

	got, _ := s.Load(jar)
	if len(got) != 0 {
		t.Errorf("Clear 后应零条，实际 %+v", got)
	}
}

// TestCorruptJsonIsIgnored 文件被外部改坏不能阻塞打开流程，
// 应 fail-soft 成零条。
func TestCorruptJsonIsIgnored(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "bad.jar")

	if err := os.WriteFile(s.filePath(jar), []byte("{不是合法 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(jar)
	if err != nil {
		t.Errorf("坏文件应静默忽略，不该报错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("坏文件应视作零条，实际 %+v", got)
	}
}

// TestWriteFailIsRecordedNotPanic 落盘失败必须记到 step 而不是 panic。
//
// 构造一个"改名目标位置被占成目录"的 Store —— Save 先写 tmp 再 rename，
// rename 到已存在的目录必然失败，Windows/Linux 统一起效。
// 比用只读权限可靠（NTFS 不认 POSIX 权限位）。
func TestWriteFailIsRecordedNotPanic(t *testing.T) {
	s := newStore(t)

	dir := t.TempDir()
	// 把目标 .json 的 tmp 位置预先占成目录，os.WriteFile 就会失败。
	// Windows / Linux 统一起效（"目标被占用"那条路上 rename 语义不一致）。
	jar := absPath(t, "x.jar")
	targetKey := pathToKey(jar)
	if err := os.MkdirAll(filepath.Join(dir, targetKey+".json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad, err := NewStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}

	var steps []string
	bad.Save(jar, []Entry{{Path: "x", Action: ActionPendingDelete}}, func(msg string) {
		steps = append(steps, msg)
	})
	if len(steps) == 0 {
		t.Error("写失败时应至少记一条 step")
	}

	// 好的 Store 不受影响
	good := absPath(t, "y.jar")
	s.Save(good, []Entry{{Path: "y", Action: ActionPendingDelete}}, nil)
	got, err := s.Load(good)
	if err != nil || len(got) != 1 {
		t.Errorf("好的 Store 应正常: %+v err=%v", got, err)
	}
}

// TestSplitIgnoresUnknownAction 无效 action 被忽略并记 step，
// 不能因为脏数据把整批 abort。
func TestSplitIgnoresUnknownAction(t *testing.T) {
	var steps []string
	d := Split([]Entry{
		{Path: "/a", Action: ActionPendingDelete},
		{Path: "b", Action: ActionMarkDelete},
		{Path: "c", Action: "火星文"},
	}, func(msg string) { steps = append(steps, msg) })

	if len(d.Manual) != 1 || d.Manual[0].Path != "a" {
		t.Errorf("Manual 分组错（注意前导斜杠应被去掉）: %+v", d.Manual)
	}
	if len(d.Marked) != 1 || d.Marked[0].Path != "b" {
		t.Errorf("Marked 分组错: %+v", d.Marked)
	}
	if len(steps) == 0 {
		t.Error("未知 action 应记一条 step")
	}
}

// TestFileFormatIsReadable 落盘的是通用 JSON，不引入私有格式。
// 这条防将来有人图省事换成 gob。
func TestFileFormatIsReadable(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "json.jar")
	s.Save(jar, []Entry{{Path: "p/q.yml", Action: ActionMarkDelete}}, nil)

	data, err := os.ReadFile(s.filePath(jar))
	if err != nil {
		t.Fatal(err)
	}
	var st store
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("落盘内容应是合法 JSON: %v", err)
	}
	if st.Version != 1 {
		t.Errorf("version = %d，期望 1", st.Version)
	}
	if len(st.Entries) != 1 || st.Entries[0].Path != "p/q.yml" {
		t.Errorf("entries = %+v", st.Entries)
	}
}

// TestNoTempFileLeftBehind 原子写成功时不能留下 .tmp。
func TestNoTempFileLeftBehind(t *testing.T) {
	s := newStore(t)
	jar := absPath(t, "clean.jar")
	s.Save(jar, []Entry{{Path: "a", Action: ActionPendingDelete}}, nil)

	if _, err := os.Stat(s.filePath(jar) + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("成功的写入不应留下 .tmp，err = %v", err)
	}
}
