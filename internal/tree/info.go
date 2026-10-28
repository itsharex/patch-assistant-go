package tree

import (
	"strings"

	"cn.cpoet.patch.assistant/internal/model"
	"cn.cpoet.patch.assistant/internal/util"
)

// PatchRootInfo 补丁根节点信息，对应 Java 的 PatchRootInfo。
//
// 一个补丁包里可以有多个"根"：主根 + 若干个用户标记的自定义根。
// 每个根各自持有一份签名信息和它自己的 README 节点。
type PatchRootInfo struct {
	// ReadmeNode 该根下的说明文件节点
	ReadmeNode *Node
	// PatchSign 该根的签名信息
	PatchSign *model.PatchSign
}

// NewPatchRootInfo 创建补丁根信息。
func NewPatchRootInfo() *PatchRootInfo {
	return &PatchRootInfo{PatchSign: &model.PatchSign{}}
}

// PatchTreeInfo 补丁包信息，对应 Java 的 PatchTreeInfo。
type PatchTreeInfo struct {
	rootNode *Node
	// rootInfo 主根（未标记自定义根时用这个）
	rootInfo *PatchRootInfo
	// customRootInfoMap 自定义根 → 根信息。
	//
	// Java 用 LinkedHashMap 保证遍历顺序（README 文本拼接依赖它）；
	// Go 的 map 无序，所以额外用 customRootOrder 记插入顺序。
	customRootInfoMap map[*Node]*PatchRootInfo
	customRootOrder   []*Node
	// readmeText 汇总后的说明文本
	readmeText string
}

// NewPatchTreeInfo 创建补丁包信息。
func NewPatchTreeInfo() *PatchTreeInfo { return &PatchTreeInfo{} }

// RootNode 根节点。
func (p *PatchTreeInfo) RootNode() *Node { return p.rootNode }

// SetRootNode 设置根节点。
func (p *PatchTreeInfo) SetRootNode(n *Node) { p.rootNode = n }

// RootInfo 主根信息。
func (p *PatchTreeInfo) RootInfo() *PatchRootInfo { return p.rootInfo }

// SetRootInfo 设置主根信息。
func (p *PatchTreeInfo) SetRootInfo(r *PatchRootInfo) { p.rootInfo = r }

// RootInfoByNode 按节点取根信息。
//
// 移植自 Java PatchTreeInfo.getRootInfoByNode：先比指针是不是主根，
// 不是再去自定义根表里查。注意 Java 用 `node == getRootNode()` 的引用比较，
// 这里保持一致（不能用 Path 之类的值比较替代）。
func (p *PatchTreeInfo) RootInfoByNode(node *Node) *PatchRootInfo {
	if node == p.rootNode {
		return p.rootInfo
	}
	if p.customRootInfoMap == nil {
		return nil
	}
	return p.customRootInfoMap[node]
}

// CustomRootInfoMap 自定义根绑定信息，按插入顺序返回。
func (p *PatchTreeInfo) CustomRootInfoMap() []CustomRootEntry {
	if len(p.customRootOrder) == 0 {
		return nil
	}
	out := make([]CustomRootEntry, 0, len(p.customRootOrder))
	for _, n := range p.customRootOrder {
		info, ok := p.customRootInfoMap[n]
		if !ok {
			continue
		}
		out = append(out, CustomRootEntry{Node: n, Info: info})
	}
	return out
}

// CustomRootEntry 自定义根的一条绑定记录。
type CustomRootEntry struct {
	Node *Node
	Info *PatchRootInfo
}

// AddCustomRootInfo 增加补丁根绑定信息，并刷新汇总文本。
func (p *PatchTreeInfo) AddCustomRootInfo(node *Node, info *PatchRootInfo) {
	if p.customRootInfoMap == nil {
		p.customRootInfoMap = make(map[*Node]*PatchRootInfo)
	}
	if _, exists := p.customRootInfoMap[node]; !exists {
		p.customRootOrder = append(p.customRootOrder, node)
	}
	p.customRootInfoMap[node] = info
	p.UpdateReadmeText()
}

// RemoveCustomRootInfo 移出根节点绑定信息，并刷新汇总文本。
func (p *PatchTreeInfo) RemoveCustomRootInfo(node *Node) {
	if p.customRootInfoMap != nil {
		delete(p.customRootInfoMap, node)
		for i, n := range p.customRootOrder {
			if n == node {
				p.customRootOrder = append(p.customRootOrder[:i], p.customRootOrder[i+1:]...)
				break
			}
		}
	}
	p.UpdateReadmeText()
}

// ReadmeText 汇总后的说明文本。
func (p *PatchTreeInfo) ReadmeText() string { return p.readmeText }

// UpdateReadmeText 重新生成汇总说明文本。
func (p *PatchTreeInfo) UpdateReadmeText() {
	p.readmeText = p.createReadmeText()
}

// createReadmeText 拼接各根的说明文本。
//
// 移植自 Java PatchTreeInfo.createReadmeText。格式是每个自定义根一段：
//
//	<根节点名>
//	---
//	<该根的 readme 正文>
//
// 段与段之间空一行。没有自定义根时直接返回主根的 readme（空则返回空串）。
//
// 保真提示：这里直接读 patchSign.Readme，用的是 Java StringUtil.isBlank 的语义
// （isSpaceChar，不认 \t\n\r），别换成 strings.TrimSpace。
func (p *PatchTreeInfo) createReadmeText() string {
	if len(p.customRootOrder) > 0 {
		var sb strings.Builder
		for _, node := range p.customRootOrder {
			info, ok := p.customRootInfoMap[node]
			if !ok || info == nil || info.PatchSign == nil {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(node.Name)
			sb.WriteString("\n---")
			if readme := info.PatchSign.Readme; !util.IsBlank(readme) {
				sb.WriteByte('\n')
				sb.WriteString(readme)
			}
		}
		return sb.String()
	}
	if p.rootInfo == nil || p.rootInfo.PatchSign == nil {
		return ""
	}
	readme := p.rootInfo.PatchSign.Readme
	if util.IsBlank(readme) {
		return ""
	}
	return readme
}

// AppTreeInfo 应用包信息，对应 Java 的 AppTreeInfo。
type AppTreeInfo struct {
	rootNode *Node
	// appPackSign 应用包签名信息
	appPackSign *model.AppPackSign
	// patchDiffInfo 补丁比较信息（多行文本）
	patchDiffInfo string
}

// NewAppTreeInfo 创建应用包信息。
func NewAppTreeInfo() *AppTreeInfo { return &AppTreeInfo{} }

// RootNode 根节点。
func (a *AppTreeInfo) RootNode() *Node { return a.rootNode }

// SetRootNode 设置根节点。
func (a *AppTreeInfo) SetRootNode(n *Node) { a.rootNode = n }

// AppPackSign 应用包签名信息。
func (a *AppTreeInfo) AppPackSign() *model.AppPackSign { return a.appPackSign }

// SetAppPackSign 设置应用包签名信息。
func (a *AppTreeInfo) SetAppPackSign(s *model.AppPackSign) { a.appPackSign = s }

// PatchDiffInfo 补丁比较信息。
func (a *AppTreeInfo) PatchDiffInfo() string { return a.patchDiffInfo }

// SetPatchDiffInfo 覆盖补丁比较信息。
// 对应 Java AppTreeInfo.setPatchDiffInfo —— 刷新匹配前会先置空。
func (a *AppTreeInfo) SetPatchDiffInfo(s string) { a.patchDiffInfo = s }

// AppendPatchDiffInfo 追加一行补丁比较信息。
//
// 对应 Java AppTreeInfo.appendPatchDiffInfo：已有内容非空时用 '\n' 连接。
// 这里的"空"判定同样走 isBlank（isSpaceChar 语义）。
func (a *AppTreeInfo) AppendPatchDiffInfo(s string) {
	if util.IsBlank(a.patchDiffInfo) {
		a.patchDiffInfo = s
		return
	}
	a.patchDiffInfo = a.patchDiffInfo + "\n" + s
}
