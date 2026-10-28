package service

import (
	"cn.cpoet.patch.assistant/internal/pack"
	"cn.cpoet.patch.assistant/internal/tree"
)

// BuildChildrenWithCompress 惰性展开一个压缩包节点的子级。
//
// 移植自 Java BasePackService.buildChildrenWithCompress(TreeNode, boolean)。
// 返回 false 表示"无需展开"—— 因为子级已经建好了。这一点很关键：
// 调用方（PatchMatchProcessor）靠它判断"是这次展开的、还是早就有的"，
// 从而决定匹配失败时要不要把 Children 置回 nil 回滚。
//
// 与 Java 的差异：Java 的 CompressNode 内容存在临时文件里（建树时就落盘了），
// Go 版是惰性读取（zipSource 直接读原压缩包）。所以这里是从节点的
// DataSource 现读一份 zip 数据再展开，语义一致但不会产生临时文件。
//
// 注意：这里用 BuildZipTreeFromReader（会把嵌套 jar 读进内存）。对一个
// fat-jar 里的 BOOT-INF/lib/*.jar，单个通常在几 MB 量级，可接受；
// 顶层 jar 不走这条路径（见 GetPatchTreeNode / GetAppTreeInfo）。
func BuildChildrenWithCompress(node *tree.Node, isPatch bool) bool {
	if node.Children != nil {
		return false
	}
	src := node.Source()
	if src == nil {
		return false
	}
	rc, err := src.Open()
	if err != nil {
		return false
	}
	defer rc.Close()

	sub, err := pack.BuildZipTreeFromReader(node.Name, rc, isPatch)
	if err != nil {
		return false
	}
	// 把 sub 的孩子搬到本节点下。这里不把 sub 本身当子节点 ——
	// Java 版也是把条目直接挂在 jar 节点下面，sub 只是一个临时容器。
	for _, child := range sub.Children {
		node.AddChild(child)
	}
	return true
}

// HandleInnerClassUnder 在指定父节点下归位内部类。
// 转发到 pack 包，供"补丁目录"建树时复用同一套逻辑。
func HandleInnerClassUnder(parent *tree.Node, innerClasses []*tree.Node) {
	pack.HandleInnerClassUnder(parent, innerClasses)
}

// readNodeBytes 读取节点全部内容。
// 对应 Java TreeNodeUtil.readNodeBytes。
func readNodeBytes(node *tree.Node) ([]byte, error) {
	if node == nil {
		return nil, nil
	}
	var out []byte
	err := node.ConsumeBytes(func(buf []byte) error {
		out = append(out, buf...)
		return nil
	})
	return out, err
}

// readNodeBytesOrEmpty 读取节点内容，失败时返回空切片。
//
// 与 Java 的差异：Java 读失败会抛 AppException 一路炸到上层（refreshReadmeNode
// 没有捕获）。Go 版显式忽略错误返回空内容，结果是 patchSign.readme 保持为空。
// 属刻意的健壮性改进，已在 MIGRATION.md 登记。
func readNodeBytesOrEmpty(node *tree.Node) []byte {
	b, err := readNodeBytes(node)
	if err != nil {
		return nil
	}
	return b
}
