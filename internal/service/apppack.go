package service

import (
	"os"
	"path/filepath"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/i18n"
	"cn.cpoet.patch.assistant/internal/model"
	"cn.cpoet.patch.assistant/internal/pack"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// GetAppTreeInfo 从一个应用包（jar/zip）建树。
//
// 移植自 Java AppPackService.getTreeNode(File)。
//
// 保真提示：根节点是【文件节点】而不是目录节点 —— Java 用 `new FileNode()`
// 并把 file 指到那个 jar 上，所以 rootNode.isDir() 返回 false。
// 这一点会影响 GetNodePathByParent 的判断，别"顺手"改成目录节点。
func GetAppTreeInfo(filePath string) (*tree.AppTreeInfo, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	// 对应 Java `file.getPath()` —— Windows 下把 '/' 换成 '\'，
	// 这个值会进根节点 Path 与签名里的 originAppPath。
	filePath = util.ToPlatformPath(filePath)

	treeInfo := tree.NewAppTreeInfo()
	rootNode := tree.NewFile(filepath.Base(filePath), filePath, info.Size(), info.ModTime(),
		&tree.FileSource{Path: filePath}, false)
	rootNode.Type = constant.TreeNodeTypeRoot
	treeInfo.SetRootNode(rootNode)

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	hashInfo, err := util.GetHashInfo(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	rootNode.SetSize(int64(hashInfo.Length))
	rootNode.SetMd5(hashInfo.Md5)
	treeInfo.SetAppPackSign(&model.AppPackSign{
		Md5:  hashInfo.Md5,
		Sha1: hashInfo.Sha1,
		Path: filePath,
	})

	/* ⚠️ 这里**不能**用 BuildZipTreeFromReader（那是 `io.ReadAll`）：
	   它会把整个包读进内存并钉住整棵树的寿命。用户 2026-10-26 报障
	   "打开包之后占用 400 多内存" —— 测试包 1.jar 有 **143MB**，光那一份
	   []byte 就 143MB，加上 WebView2 自己的开销正好 400MB 上下。

	   BuildZipTreeFromPath 只读中央目录（几 KB），条目内容等预览 / 反编译 /
	   算 md5 时才读，而且是"每次读开一次文件、读完就关"——树既不占内存，
	   也不长期持有句柄（句柄问题见 pack.BuildZipTreeFromPath 的注释）。 */
	sub, err := pack.BuildZipTreeFromPath(filePath, info.Size(), false)
	if err != nil {
		return nil, err
	}
	for _, child := range sub.Children {
		rootNode.AddChild(child)
	}
	return treeInfo, nil
}

// CreatePatchDiffInfo 记录"补丁文件与应用文件其实没变化"的提示。
//
// 移植自 Java AppPackService.createPatchDiffInfo。这是给打补丁的人看的
// 风险提示，两种情形：
//
//  1. 两边内容 md5 相同 → 提示"是同一个文件"（说明补丁打上去等于没打）；
//  2. 应用文件的修改时间【不早于】补丁文件的修改时间 → 提示"应用比补丁新"
//     （说明应用侧可能已经被改过，再打这个补丁会覆盖掉别人的改动）。
//
// 注意第 2 条只在两个时间都非空时才判，且用的是 !isBefore（>=），
// 也就是时间【相等】也会提示。
func CreatePatchDiffInfo(appTreeInfo *tree.AppTreeInfo, appNode, patchNode *tree.Node, opts PatchOptions) {
	if !opts.PatchFileDiff || appTreeInfo == nil {
		return
	}
	appMd5 := appNode.GetMd5OrInit()
	patchMd5 := patchNode.GetMd5OrInit()
	// 任一侧没有 md5 就别比：**目录**的 GetMd5OrInit 恒为空（ConsumeBytes 对
	// 目录直接返回），两个空串相等会得出一条"是同一个文件"的假提示。整包套用
	// 时会走到每个目录上，不挡的话提示里会塞满目录名。
	if appMd5 == "" || patchMd5 == "" {
		return
	}
	if appMd5 == patchMd5 {
		appTreeInfo.AppendPatchDiffInfo(i18n.Tr(opts.LangCode, "app.service.app-pack.diff-file-tip.hash",
			appNode.Path, patchNode.Path))
		return
	}
	appModifyTime := appNode.ModifyTime()
	patchModifyTime := patchNode.ModifyTime()
	if !appModifyTime.IsZero() && !patchModifyTime.IsZero() && !appModifyTime.Before(patchModifyTime) {
		appTreeInfo.AppendPatchDiffInfo(i18n.Tr(opts.LangCode, "app.service.app-pack.diff-file-tip.time",
			appNode.Path, formatDateTime(appModifyTime),
			patchNode.Path, formatDateTime(patchModifyTime)))
	}
}

// formatDateTime 按 "yyyy-MM-dd HH:mm:ss" 格式化。
// 对应 Java DateUtil.formatDateTime。
func formatDateTime(t time.Time) string { return t.Format(dateTimeLayout) }

// dateTimeLayout 与 Java DateUtil.DATE_TIME_FORMAT 一致。
const dateTimeLayout = "2006-01-02 15:04:05"
