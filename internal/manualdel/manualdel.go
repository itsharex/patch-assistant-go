// Package manualdel 持久化用户在应用树上"删除 / 标记删除"的清单。
//
// 背景 ——
//
//	Go 版对齐 Java 原版的 HomeLeftTreeView.handleManualDel：删除是「从内存
//	里把节点摘掉」、标记删除是「把节点状态设成 DEL」。两条都只是改了本次会话
//	的内存视图，**关掉重开就丢了**，因为打开应用包时整棵树会从 jar 重新解析。
//
//	Java 原版也是这样，这是上游行为。但用户日常用起来会撞到：
//	  "我把 bootstrap.yml 删了，关掉再开它还在"。
//
//	这个包提供一个**会话级以外的兜底**：把删除清单写进配置文件目录，
//	下次打开同一个 jar 时自动重应用。
//
// 设计要点 ——
//
//   - **按 jar 路径分文件**，目录里每个 json 文件对应一份应用包。
//     用路径的 SHA-1 前 16 字节做文件名（hex 编码 32 字符），这样：
//       - 路径含中文 / 空格也不影响文件名；
//       - 同路径不管绝对还是相对（在 IDE 里有时是相对），都能命中。
//   - **内容是相对路径列表**（jar 内的 "/" 分隔路径）。开包后用
//     findTreeNodeByPath 按 Path 找节点再处理，**不要用 Name** ——
//     jar 内可能有多个同名文件（如 META-INF/、根目录下各一个 README.txt）。
//   - **两类动作都记**：物理删除（manualDelTotal）与标记删除（delTotal）
//     都进同一份文件，按 Action 字段标记。重应用区分得严格，不然下次再开
//     用户可能看着菜单里的"删除"是灰的（节点已被标 DEL，不是 NONE）。
//   - **写是 fail-soft**：写文件失败不能打断主流程（GUI 不能因为落盘失败
//     而拒绝执行删除动作），记一条 step 就够了。
//   - **读是不存在的就当没记录**：新装的机器、jar 第一次打开、文件被人
//     删了 —— 都正常返回零条。
package manualdel

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"cn.cpoet.patch.assistant/internal/util"
)

// Action 表示一条记录的种类。
//
// 注意命名沿用 internal/constant.TreeNodeType 的字符串值（"manualDel" / "del"），
// 这样下游应用阶段判 type 时不需要翻译。
const (
	ActionPendingDelete = "manualDel" // 物理删除（RemoveNodeChild）
	ActionMarkDelete   = "del"      // 标记删除（CountAndSetNodeType DEL）
)

// Entry 是单条记录。Path 是 jar 内的 "/" 分隔相对路径。
type Entry struct {
	// Path 应用树里的完整路径（与 tree.Node.Path 一致）。
	Path string `json:"path"`
	// Action "manualDel" 或 "del"。
	Action string `json:"action"`
}

// store 是磁盘上的文件内容。Version 留给将来 schema 升级用。
type store struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Store 是面向调用方的句柄，无锁。文件位置 / 路径哈希都在构造时算好。
type Store struct {
	dir string // 配置目录下专属于本包的子目录（保证存在）

	mu sync.Mutex // 串行化一个 Store 实例的并发读写（同一时间通常只有 GUI 主线程）
}

// NewStore 返回一个 Store。dirName 是配置目录下的子目录名，传空字符串
// 用默认的 "manual_deletes"。
//
// 调用方通常传内部用到的常量 AppName（与 util.GetAppConfigPath 的
// appName 同源），让多台机器 / 多用户之间互不干扰。
func NewStore(appName string) (*Store, error) {
	base, err := util.GetAppConfigPath(appName)
	if err != nil {
		return nil, fmt.Errorf("定位配置目录失败: %w", err)
	}
	sub := "manual_deletes"
	dir := filepath.Join(base, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败 %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// NewStoreAt 返回一个把记录写到指定目录的 Store。
//
// 目录**不必已存在**，Save 时由 os.WriteFile 保证不了，所以这里主动
// MkdirAll 一次，构造函数失败就早失败。仅测试 / 自定义位置用。
func NewStoreAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败 %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// pathToKey 把 jar 路径转成文件名。同一路径（按 string 比较）始终映射
// 同一文件，跨平台 / 跨大小写也稳定 —— hex 编码天然全小写。
func pathToKey(jarPath string) string {
	sum := sha1.Sum([]byte(jarPath))
	return hex.EncodeToString(sum[:8]) // 16 字节 = 32 hex 字符
}

// filePath 返回某 jar 对应的记录文件完整路径。
func (s *Store) filePath(jarPath string) string {
	return filepath.Join(s.dir, pathToKey(jarPath)+".json")
}

// Load 读取并解析某个 jar 的记录文件，**不存在视作零条**，绝不报错。
//
// 返回的 slice 是**已排序**的（按 Path），方便上层做差集 / 去重。
func (s *Store) Load(jarPath string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.filePath(jarPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("打开记录文件失败: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("读取记录文件失败: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var st store
	if err := json.Unmarshal(data, &st); err != nil {
		// 文件被外部改坏不能阻塞主流程：清空后当零条用，让用户重新操作。
		// 这是 fail-soft —— 删除记录没命中不会让人工选错。
		return nil, nil
	}
	sort.Slice(st.Entries, func(i, j int) bool {
		return st.Entries[i].Path < st.Entries[j].Path
	})
	return st.Entries, nil
}

// Save 原子地把整份 entries 写回磁盘。
//
// 写失败不返回 error 给上层 —— 调用方通常是 GUI 的一次操作，
// 不能因为落盘失败就回滚内存状态。返回一个内部 step 函数让 GUI
// 有机会把失败记到进度栏里。
func (s *Store) Save(jarPath string, entries []Entry, step func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 拷贝并排序 —— 上层传进来的 slice 顺序未必稳定。
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Action < sorted[j].Action
	})

	st := store{Version: 1, Entries: sorted}
	data, err := json.MarshalIndent(&st, "", "  ")
	if err != nil {
		if step != nil {
			step("manualdel: 序列化失败: " + err.Error())
		}
		return
	}

	// 原子写：先写 .tmp，再 rename 覆盖，避免半截文件被下一次启动读到。
	tmp := s.filePath(jarPath) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		if step != nil {
			step("manualdel: 写临时文件失败: " + err.Error())
		}
		return
	}
	if err := os.Rename(tmp, s.filePath(jarPath)); err != nil {
		if step != nil {
			step("manualdel: 落盘失败: " + err.Error())
		}
		_ = os.Remove(tmp)
	}
}

// Clear 删掉某个 jar 的记录文件（例如用户主动点了"恢复原状"）。
// 不存在的文件直接当作成功。
func (s *Store) Clear(jarPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.filePath(jarPath))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Diff 是 reapply 时按动作分组后的结果。便于上层直接调 tree.* 函数。
type Diff struct {
	// Manual 走"物理删除"路径的条目（CountNodeType(MANUAL_DEL) + RemoveNodeChild）。
	Manual []Entry
	// Marked 走"标记删除"路径的条目（CountAndSetNodeType(DEL)）。
	Marked []Entry
}

// Split 按 action 把 entries 分到两个组。两组各自内部已按 Path 排序。
//
// 不匹配的 action（损坏数据 / 旧版本字段）会被忽略并记到 step 里，
// 不能因为数据脏就整个 abort。
func Split(entries []Entry, step func(string)) Diff {
	var d Diff
	for _, e := range entries {
		e.Path = strings.TrimPrefix(e.Path, "/")
		switch e.Action {
		case ActionPendingDelete:
			d.Manual = append(d.Manual, e)
		case ActionMarkDelete:
			d.Marked = append(d.Marked, e)
		default:
			if step != nil {
				step("manualdel: 忽略未知条目 action=" + e.Action)
			}
		}
	}
	sort.Slice(d.Manual, func(i, j int) bool { return d.Manual[i].Path < d.Manual[j].Path })
	sort.Slice(d.Marked, func(i, j int) bool { return d.Marked[i].Path < d.Marked[j].Path })
	return d
}