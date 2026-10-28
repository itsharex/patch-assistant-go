package tree

import (
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/util"
)

// ReadMePathInfo README 升级指令解析出的一条路径信息。
//
// 保真说明：Java 侧这个类在 cn.cpoet.patch.assistant.model 包下，Go 版挪到了
// tree 包 —— 因为它持有 *TreeNode 且路径语义完全依赖树结构，是 model → tree
// 这条依赖边的唯一来源。Java 允许包级循环依赖，Go 不允许，所以必须选一边。
// 放在 tree 里可以让依赖保持单向（tree → model），且包划分仍与 Java 的
// control.tree 一一对应。
type ReadMePathInfo struct {
	// Type 变更类型
	Type constant.ChangeType
	// Path1 第一段路径（补丁侧路径）
	Path1 string
	// Path2 第二段路径
	Path2 string
	// Path3 第三段路径
	Path3 string
	// AppNode 关联的应用节点
	AppNode *Node
	// PatchNode 关联的补丁节点
	PatchNode *Node
	// StartIndex 该指令在 README 文本中的起始下标
	StartIndex int
	// EndIndex 该指令在 README 文本中的结束下标
	EndIndex int
}

// AppNodePath 计算应用侧的目标路径。
//
// 移植自 Java ReadMePathInfo.getAppNodePath()，逻辑照搬：
// 优先 path2+path3，其次 path2，最后回落 path1；若拼接结果没有以
// path1 的文件名结尾，则再补上文件名。
//
// 注意 HasSuffix 用的是 fileName 而不是 path1 —— 所以 "a/b/Foo.class" 配
// path2="x/Foo.class" 时不会重复拼接，而 path2="x/Other.class" 会。
func (r *ReadMePathInfo) AppNodePath() string {
	fileName := util.FileName(r.Path1)
	switch {
	case r.Path3 != "":
		p := util.JoinPath(r.Path2, r.Path3)
		if !strings.HasSuffix(p, fileName) {
			p = util.JoinPath(p, fileName)
		}
		return p
	case r.Path2 != "":
		p := r.Path2
		if !strings.HasSuffix(p, fileName) {
			p = util.JoinPath(p, fileName)
		}
		return p
	default:
		return r.Path1
	}
}
