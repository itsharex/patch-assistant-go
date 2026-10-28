package tree

import (
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/util"
)

// NodeFilter 节点过滤器。返回 true 表示"跳过这个节点"。
// 对应 Java 里到处传的 Predicate<TreeNode>（语义是 exclude）。
type NodeFilter func(*Node) bool

// RemoveNodeChild 从父节点中摘除指定节点。
//
// 对应 Java TreeNodeUtil.removeNodeChild。Java 版还会同步摘掉对应的
// JavaFX TreeItem，那部分属 UI 层，Go 侧由目标框架自己处理。
func RemoveNodeChild(node *Node) {
	if node == nil || node.Parent == nil {
		return
	}
	node.Parent.RemoveChild(node)
}

// DeepCleanMappedNode 递归清理节点上的绑定关系与状态。
//
// 移植自 Java TreeNodeUtil.deepCleanMappedNode，这段逻辑是整套匹配流程里
// 最绕的一块，三条分支必须一起看：
//
//  1. 若 node 是补丁侧的根/自定义根：先递归清子级，然后 —— 注意 —— 只有当它
//     自己不是 ROOT 类型时才清自己。也就是说补丁包主根永远不会被清，
//     而自定义根会被清掉（顺便把它的 customRootInfo 摘掉，见 PatchPackService）。
//  2. 其余情况：先拿 appNode（补丁节点取它的 MappedNode，应用节点取自己），
//     递归清 node 的整棵子树，再"向上回溯"——只要父级是 VirtualNode 且
//     已经空了，就继续往上清，直到遇到非 Virtual 或非空节点。
//  3. 回溯用 while 而非递归，且每轮都重新取 Parent，因为 cleanMappedNode
//     可能把 VirtualNode 从父节点上摘掉，链路会变。
//
// 保真提示：Java 的 `node.isPatch() && isRootNode(node)` 里 isRootNode 判的是
// Type 是不是 ROOT/CUSTOM_ROOT，不是"有没有 parent"。别改成后者。
func DeepCleanMappedNode(total *TotalInfo, node *Node, filter NodeFilter) {
	if node == nil {
		return
	}
	if node.IsPatch && IsRootNode(node) {
		for _, child := range node.Children {
			DeepCleanMappedNode(total, child, filter)
		}
		if node.Type != constant.TreeNodeTypeRoot {
			deepCleanMappedNode1(total, node, filter)
		}
		return
	}

	appNode := node
	if node.IsPatch {
		appNode = node.MappedNode
	}
	deepCleanMappedNode0(total, node, filter)

	for appNode != nil {
		parent := appNode.Parent
		if parent == nil || parent.Kind != KindVirtual || len(parent.Children) != 0 {
			break
		}
		deepCleanMappedNode0(total, parent, filter)
		appNode = parent
	}
}

// deepCleanMappedNode0 后序递归：先清所有子级，再清自己。
func deepCleanMappedNode0(total *TotalInfo, node *Node, filter NodeFilter) {
	if len(node.Children) > 0 {
		// 拷贝一份再遍历，因为清理过程可能改动 Children（Java 注释里
		// 写的是"避免并发异常"，实际是避免 ConcurrentModificationException）
		for _, child := range append([]*Node(nil), node.Children...) {
			deepCleanMappedNode0(total, child, filter)
		}
	}
	deepCleanMappedNode1(total, node, filter)
}

// deepCleanMappedNode1 清理单个节点：先减计数，再断开双向绑定。
//
// 计数只对非目录节点减 —— 与 Java 一致（统计的是文件数，不是节点数）。
func deepCleanMappedNode1(total *TotalInfo, node *Node, filter NodeFilter) {
	if !node.Dir() {
		total.DecrTotal(node.Type)
	}
	cleanMappedNode(node.MappedNode, filter)
	cleanMappedNode(node, filter)
}

// cleanMappedNode 断开单个节点：状态归 NONE、解绑；若是应用侧的
// VirtualNode/MappedNode，还要把自己从父节点上摘掉。
//
// 对应 Java TreeNodeUtil.cleanMappedNode(node, filter)。注意 filter 为 true
// 时表示"跳过"，所以条件是 `node == nil || (filter != nil && filter(node))`。
func cleanMappedNode(node *Node, filter NodeFilter) {
	if node == nil || (filter != nil && filter(node)) {
		return
	}
	node.Type = constant.TreeNodeTypeNone
	node.MappedNode = nil
	if !node.IsPatch && (node.Kind == KindVirtual || node.Kind == KindMapped) {
		RemoveNodeChild(node)
	}
}

// BindMappedNode 建立双向绑定并设置双方状态，不碰统计。
// 对应 Java TreeNodeUtil.mappedNode(node1, node2, status)。
func BindMappedNode(node1, node2 *Node, status constant.TreeNodeType) {
	node1.SetMappedNode(node2)
	node2.SetMappedNode(node1)
	node1.Type = status
	node2.Type = status
}

// MappedNode 建立双向绑定、设置状态并累加统计。
// 对应 Java TreeNodeUtil.mappedNode(totalInfo, node1, node2, status)。
func MappedNode(total *TotalInfo, node1, node2 *Node, status constant.TreeNodeType) {
	BindMappedNode(node1, node2, status)
	if !node1.Dir() {
		total.IncrTotal(status)
	}
}

// CountNodeType 递归累加统计，不改状态。
//
// 对应 Java TreeNodeUtil.countNodeType。递归条件值得注意：
// 只有「目录」或「名字以 .class 结尾」的节点才会统计其子级 ——
// 因为 class 的内部类是被挂成子节点的，而普通文件的子级不存在。
func CountNodeType(total *TotalInfo, node *Node, nodeType constant.TreeNodeType) {
	if !node.Dir() {
		total.IncrTotal(nodeType)
	}
	if (node.Dir() || strings.HasSuffix(node.Name, constant.DotClass)) && len(node.Children) > 0 {
		for _, child := range node.Children {
			CountNodeType(total, child, nodeType)
		}
	}
}

// CountAndSetNodeType 递归设置状态并累加统计。
// 对应 Java TreeNodeUtil.countAndSetNodeType。
func CountAndSetNodeType(total *TotalInfo, node *Node, nodeType constant.TreeNodeType) {
	node.Type = nodeType
	if !node.Dir() {
		total.IncrTotal(nodeType)
	}
	if (node.Dir() || strings.HasSuffix(node.Name, constant.DotClass)) && len(node.Children) > 0 {
		for _, child := range node.Children {
			CountAndSetNodeType(total, child, nodeType)
		}
	}
}

// IsRootNode 判断是否补丁包根或自定义根。
// 对应 Java TreeNodeUtil.isRootNode。
func IsRootNode(node *Node) bool {
	return node != nil &&
		(node.Type == constant.TreeNodeTypeRoot || node.Type == constant.TreeNodeTypeCustomRoot)
}

// GetUnderRootNode 向上找到所属的根节点（ROOT 或 CUSTOM_ROOT）。
// 对应 Java TreeNodeUtil.getUnderRootNode。
func GetUnderRootNode(node *Node) *Node {
	for node != nil &&
		node.Type != constant.TreeNodeTypeRoot &&
		node.Type != constant.TreeNodeTypeCustomRoot {
		node = node.Parent
	}
	return node
}

// IsNotUnderCustomRoot 判断节点是否不在任何自定义根之下。
// 对应 Java TreeNodeUtil.isNotUnderCustomRoot。
func IsNotUnderCustomRoot(node *Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == constant.TreeNodeTypeCustomRoot {
			return false
		}
	}
	return true
}

// IsCompressNode 判断是否受支持的压缩文件（.jar / .zip）。
//
// 对应 Java TreeNodeUtil.isCompressNode → FileDecompressor.isCompressFile。
// 保真提示：1.4.7 只认 jar 和 zip 两种，PDF 里提过的 rar 支持已经移除，
// 别照着文档把 rar 加回来。
func IsCompressNode(node *Node) bool {
	if node == nil {
		return false
	}
	return IsCompressName(node.Name)
}

// IsCompressName 判断文件名是否是受支持的压缩包。
func IsCompressName(name string) bool {
	return strings.HasSuffix(name, constant.DotJar) || strings.HasSuffix(name, constant.DotZip)
}

// FindNodeByPath 按路径逐级查找节点，未找到返回 nil。
//
// 移植自 Java TreeNodeUtil.findNodeByPath（默认谓词版，用名称全等比较）。
func FindNodeByPath(node *Node, path string) *Node {
	return FindNodeByPathWith(node, path, func(child *Node, name string) bool {
		return child.Name == name
	})
}

// FindNodeByPathWith 按路径逐级查找节点，用自定义谓词匹配每段名称。
//
// 移植自 Java TreeNodeUtil.findNodeByPath(node, path, predicate)。语义是
// "每一段都必须命中一个子节点"，任一段落空就整体失败。
func FindNodeByPathWith(node *Node, path string, predicate func(*Node, string) bool) *Node {
	paths := util.SplitPath(path)
	for i := 0; i < len(paths); i++ {
		if len(node.Children) == 0 {
			return nil
		}
		found := false
		for _, child := range node.Children {
			if predicate(child, paths[i]) {
				node = child
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return node
}

// GetNodePathByParent 根据父节点推算子节点路径。
//
// 移植自 Java TreeNodeUtil.getNodePathByParent，两条分支要留意：
//
//   - 父节点是目录：直接拼 parent.Path + node.Name。
//   - 父节点不是目录：说明这是个压缩包文件节点（如 foo.jar），此时路径
//     要看它的父级 —— 若父级本身又是压缩包，就返回 "/"（包内根）；
//     否则拼 祖父.Path + node.Name。
//
// 保真提示：Java 版在父节点为 nil 时会 NPE。Go 里返回 "/" 并让调用方
// 自己判断，不制造 panic —— 这是唯一一处刻意的行为差异，属健壮性改进。
func GetNodePathByParent(parentNode, node *Node) string {
	if parentNode == nil {
		return util.Separator
	}
	if parentNode.Dir() {
		return util.JoinPath(parentNode.Path, node.Name)
	}
	grand := parentNode.Parent
	if IsCompressNode(grand) {
		return util.Separator
	}
	if grand == nil {
		return util.Separator
	}
	return util.JoinPath(grand.Path, node.Name)
}
