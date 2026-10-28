package service

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// 本文件移植 Java PatchPackService 里"把编辑后的 README 写回补丁包"这一段
// （原文件 484-649 行）：createReadmeNode / updatePatchReadme(两个重载) /
// updatePatchPackWithZip / writePatchPackWithZip。
//
// 这是阶段二闭环的最后一块 —— 前面 match.go 算出"哪些文件要改"，
// writer.go 把改动写进【应用包】，而这里负责把改动写进【补丁包】。

// UpdatePatchReadme 把编辑过的 README 文本写回补丁包。
//
// 移植自 Java PatchPackService.updatePatchReadme(ProgressContext, PatchTreeView,
// TreeNode, String, Consumer<Boolean>)。参数对应关系：
//
//	patchTree.getTreeInfo()                    → ctx.PatchTreeInfo
//	Configuration.getPatch().getReadmeFile()   → ctx.ReadmeFile
//	AppContext.getTempDir()                    → ctx.TempDir
//	callback.accept(TRUE/FALSE)                → 返回 error（nil 即成功）
//
// 其余三块属 UI 层，Go 版不做：UIUtil.runNotUI / runUI 的线程切换、
// patchTree.refresh() 的重绘、以及"新建 README 时往树控件里补一个 TreeItem"。
// 目标 GUI 框架自己处理，见 tree/node.go 顶部关于 treeItem 的说明。
//
// 两条路径：
//
//   - README 节点是压缩包条目（KindCompress）→ 文本先落到临时文件并挂到节点上，
//     再把整个补丁包重写一遍（嵌套压缩包会逐级重写），最后把最外层结果
//     覆盖回补丁包原路径；
//   - README 节点是普通磁盘文件（补丁目录模式）→ 直接写回磁盘。
//
// 失败时按上游的规则回滚（见 rollbackReadmeNode / rollbackPackNodes）。
func UpdatePatchReadme(ctx *WriteContext, rootNode *tree.Node, text string) error {
	if ctx == nil || ctx.PatchTreeInfo == nil {
		return errors.New("patch tree info is nil")
	}
	if rootNode == nil {
		return errors.New("root node is nil")
	}
	patchRootInfo := ctx.PatchTreeInfo.RootInfoByNode(rootNode)
	if patchRootInfo == nil {
		return fmt.Errorf("no patch root info bound to node: %s", rootNode.Name)
	}

	readmeNode := patchRootInfo.ReadmeNode
	var oldReadmeNode *tree.Node
	if readmeNode != nil {
		oldReadmeNode = readmeNode.Clone()
	} else {
		readmeNode = createReadmeNode(ctx.Step, rootNode, readmeFileNameOf(ctx))
	}

	if err := writeReadmeText(ctx, readmeNode, rootNode, text); err != nil {
		rollbackReadmeNode(rootNode, readmeNode, oldReadmeNode)
		step(ctx.Step, err.Error())
		return err
	}

	patchRootInfo.ReadmeNode = readmeNode
	if patchRootInfo.PatchSign != nil {
		patchRootInfo.PatchSign.Readme = text
	}
	ctx.PatchTreeInfo.UpdateReadmeText()
	return nil
}

// writeReadmeText 把文本落到 README 节点上。
//
// 移植自 Java updatePatchReadme 里 try 块的前半段。压缩包条目走"临时文件 +
// 重写补丁包"，普通文件直接写磁盘 —— 分流的依据是节点种类，不是文件名后缀。
//
// 与 Java 的差异：Java 用 `text.getBytes()`（平台默认字符集，老 JDK 在中文
// Windows 上是 GBK），Go 固定写 UTF-8。读取侧靠字符集探测（CharsetUtil /
// util.DetectCharset）自动识别，所以往返一致；已在 MIGRATION.md 登记。
func writeReadmeText(ctx *WriteContext, readmeNode, rootNode *tree.Node, text string) error {
	step(ctx.Step, "Update readme text to file")
	content := []byte(text)

	if readmeNode.Kind == tree.KindCompress {
		file := filepath.Join(tempDirOf(ctx.TempDir), uniqueFileName(readmeNode.Name))
		if err := os.WriteFile(file, content, 0o644); err != nil {
			return fmt.Errorf("write readme temp file failed: %w", err)
		}
		readmeNode.SetFileSource(file)
		now := time.Now()
		readmeNode.SetModifyTime(now)
		readmeNode.SetAccessTime(now)
		readmeNode.SetMd5(util.Md5(content))
		readmeNode.SetSize(int64(len(content)))
		return updatePatchPack(ctx, rootNode)
	}

	readmeNode.SetMd5(util.Md5(content))
	readmeNode.SetSize(int64(len(content)))
	if readmeNode.FilePath() == "" {
		readmeNode.SetFileSource(readmeNode.Path)
	}
	if err := os.WriteFile(readmeNode.FilePath(), content, 0o644); err != nil {
		return fmt.Errorf("write readme file failed: %w", err)
	}
	return nil
}

// createReadmeNode 在补丁根下新建一个说明文件节点。
//
// 移植自 Java PatchPackService.createReadmeNode。两个容易看漏的点：
//
//   - 节点种类取决于两个判断的【或】：`rootNode instanceof CompressNode`
//     或 `isCompressNode(rootNode)`（后者按文件名后缀）。前者在 Go 里就是
//     Kind == KindCompress，后者是 tree.IsCompressNode。两者用不同标准，
//     照搬。
//   - Path 的算法同样按【后缀】判断：压缩包根用裸文件名（它将成为包内
//     条目名），其余情况拼 rootNode.Path + 文件名（补丁目录模式下
//     rootNode.Path 是磁盘绝对路径，于是拼出来的也是绝对路径）。
func createReadmeNode(st Step, rootNode *tree.Node, readmeFileName string) *tree.Node {
	step(st, "Create a readme file to : "+rootNode.Name)

	kind := tree.KindFile
	if rootNode.Kind == tree.KindCompress || tree.IsCompressNode(rootNode) {
		kind = tree.KindCompress
	}
	readmeNode := tree.NewBareFile(kind)
	readmeNode.Name = readmeFileName
	if tree.IsCompressNode(rootNode) {
		readmeNode.Path = readmeFileName
	} else {
		readmeNode.Path = util.JoinPath(rootNode.Path, readmeFileName)
	}
	readmeNode.IsPatch = true
	readmeNode.Type = constant.TreeNodeTypeReadme
	readmeNode.Parent = rootNode
	rootNode.AddChild(readmeNode)
	return readmeNode
}

// readmeFileNameOf 取配置里的 README 文件名，空则回落默认值。
// 对应 Java `Configuration.getInstance().getPatch().getReadmeFile()` 的那两行兜底。
func readmeFileNameOf(ctx *WriteContext) string {
	if util.IsBlank(ctx.ReadmeFile) {
		return constant.ReadmeFile
	}
	return ctx.ReadmeFile
}

// rollbackReadmeNode 写回失败时恢复 README 节点。
//
// 移植自 Java updatePatchReadme 的 catch 分支。两种情形：
//
//   - 原本没有 README 节点（这次刚新建的）→ 直接从父节点上摘掉；
//   - 原本有 → 恢复文件引用；压缩包节点还要额外恢复 md5/大小/时间。
//
// 保真提示：普通文件节点（非压缩包）只恢复文件引用，不恢复 md5/大小 ——
// Java 里那几行恢复被写在 `if (readmeNode instanceof CompressNode)` 之内。
// 这是上游写法，照搬。
func rollbackReadmeNode(rootNode, readmeNode, oldReadmeNode *tree.Node) {
	if oldReadmeNode == nil {
		rootNode.RemoveChild(readmeNode)
		return
	}
	setNodeFile(readmeNode, oldReadmeNode.FilePath())
	if readmeNode.Kind == tree.KindCompress {
		readmeNode.SetMd5(oldReadmeNode.Md5)
		readmeNode.SetSize(oldReadmeNode.Size)
		readmeNode.SetModifyTime(oldReadmeNode.ModifyTime())
		readmeNode.SetAccessTime(oldReadmeNode.AccessTime())
	}
}

// updatePatchPack 把补丁包（可能多层嵌套）逐级重写。
//
// 移植自 Java PatchPackService.updatePatchReadme(ProgressContext, FileNode)。
//
// 为什么要"逐级"：README 可能被写在【嵌套压缩包】里 —— 比如补丁包
// patch.zip 里套着 lib/foo.jar，而 README 挂在 foo.jar 下。这时得先把
// foo.jar 重写一遍，再重写 patch.zip（重写 patch.zip 时读到的正是刚写好的
// foo.jar）。所以 rootNodes 是从【最内层】往外收集的，写也从内往外写。
//
// 收集规则（照搬 Java）：从 rootNode 起向上走，凡是"名字是压缩包后缀"的都
// 收进来；一旦遇到 Kind 不是 KindCompress 的节点就停。注意这两个判断用的
// 是【不同标准】—— 前者按文件名后缀、后者按节点种类，属上游写法。
//
// 与 Java 的差异：rootNodes 为空时 Java 会在 getLast() 抛
// NoSuchElementException，Go 返回 error。实际流程走不到这里（能进
// updatePatchPack 说明 README 是压缩包条目，其父节点必然也是压缩包）。
func updatePatchPack(ctx *WriteContext, rootNode *tree.Node) error {
	step(ctx.Step, "Write in the patch package")

	rootNodes := make([]*tree.Node, 0, 4)
	for node := rootNode; node != nil; {
		if tree.IsCompressNode(node) {
			rootNodes = append(rootNodes, node)
		}
		if node.Kind != tree.KindCompress {
			break
		}
		node = node.Parent
	}
	if len(rootNodes) == 0 {
		return fmt.Errorf("no compress node found above readme, root: %s", rootNode.Name)
	}

	oldRootNodes := make([]*tree.Node, len(rootNodes))
	for i, n := range rootNodes {
		oldRootNodes[i] = n.Clone()
	}

	for _, n := range rootNodes {
		if err := updatePatchPackWithZip(ctx, n); err != nil {
			rollbackPackNodes(rootNodes, oldRootNodes)
			return err
		}
		// 重新计算文件大小和 md5 值（Java 传一个空的 consumeBytes 回调来触发重算）
		_ = n.ConsumeBytes(func([]byte) error { return nil })
	}

	// 最外层的结果覆盖回补丁包原路径，并把节点的文件引用指回原路径
	last := rootNodes[len(rootNodes)-1]
	origin := oldRootNodes[len(oldRootNodes)-1]
	if err := copyFile(last.FilePath(), origin.FilePath()); err != nil {
		rollbackPackNodes(rootNodes, oldRootNodes)
		return err
	}
	setNodeFile(last, origin.FilePath())

	step(ctx.Step, "Write the patch package successfully")
	return nil
}

// rollbackPackNodes 回滚补丁包重写。
//
// 移植自 Java updatePatchReadme 里 catch 分支的回滚。
//
// 保真提示（上游 bug，照搬不改）：Java 原文是
//
//	i = 0;
//	for (FileNode node : rootNodes) {
//	    node.setName(oldRootNodes[i].getName());
//	    node.setFile(oldRootNodes[i].getFile());
//	    node.setMd5(oldRootNodes[i].getMd5());
//	    node.setSize(oldRootNodes[i].getSize());
//	}
//
// 循环体里【没有 i++】。也就是说每个节点都被回滚成 oldRootNodes[0] 的状态，
// 而不是各自对应的那一份。正常场景下 rootNodes 只有一个元素（补丁包本身
// 就是一个压缩包），所以这个 bug 平时不显形；只有"README 写在嵌套压缩包里"
// 时才会暴露。Go 版照搬，不"顺手修好"。
func rollbackPackNodes(rootNodes, oldRootNodes []*tree.Node) {
	if len(oldRootNodes) == 0 {
		return
	}
	origin := oldRootNodes[0]
	for _, node := range rootNodes {
		node.Name = origin.Name
		setNodeFile(node, origin.FilePath())
		node.SetMd5(origin.Md5)
		node.SetSize(origin.Size)
	}
}

// updatePatchPackWithZip 把一个节点重写成新的 zip 临时文件，并把该文件挂回节点。
//
// 移植自 Java PatchPackService.updatePatchPackWithZip。
//
// 保真提示：Java 会把【不以 ".zip" 结尾】的节点名改成 ".zip" —— 所以一个叫
// "patch.jar" 的补丁包，编辑过 README 之后节点名会变成 "patch.zip"
// （磁盘文件名不变，因为最后会 copyTo 回原路径）。这个改名正是上面
// rollbackPackNodes 存在的唯一理由：失败时要把它改回去。
func updatePatchPackWithZip(ctx *WriteContext, rootNode *tree.Node) error {
	step(ctx.Step, "Writing to a zip file:"+rootNode.Name)
	if !strings.HasSuffix(rootNode.Name, constant.DotZip) {
		rootNode.Name = util.Name(rootNode.Name) + constant.DotZip
	}

	file := filepath.Join(tempDirOf(ctx.TempDir), uniqueFileName(rootNode.Name))
	out, err := os.Create(file)
	if err != nil {
		return err
	}

	zw := zip.NewWriter(out)
	if err := writePatchPackWithZip(ctx, zw, rootNode); err != nil {
		_ = zw.Close()
		_ = out.Close()
		return err
	}
	rootNode.SetFileSource(file)
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

// writePatchPackWithZip 把 rootNode 的子级逐个写进 zip。
//
// 移植自 Java PatchPackService.writePatchPackWithZip。三处要点：
//
//   - 条目名取 child.Path，目录额外补尾部 '/'；
//   - 内容来自 child.ConsumeBytes —— 对压缩包条目就是它当前挂着的那个文件；
//   - 只有当 child 有子级【且】child 不是压缩包时才递归下探。压缩包节点不递归
//     是有意的：它的内容本身就是整个压缩包的字节，上面那句 ConsumeBytes
//     已经整体写出来了，再递归就会写出重复条目。
//
// 【前置条件 · 上游陷阱】本方法只遍历节点的【现有子级】，不会先去展开压缩包。
// 所以对一个"还没展开过"的内层 jar 写 README，重写出来的 jar 里就只剩 README
// 一个条目，原有的 class 全丢。这不是移植引入的 —— Java 的
// writePatchPackWithZip 是同一套写法（静态分析结论，未跑原版 Java 复现）。
// 真实使用中用户会先在树里展开该节点，所以平时不显形；对应测试见
// TestUpdatePatchReadmeNestedUnexpandedDropsContent。
//
// 与 Java 的差异（两条，均已在 MIGRATION.md 登记）：
//
//  1. 目录条目用 STORED，Java 用默认的 DEFLATED。这是 Go 的 archive/zip
//     强制的（它把任何以 '/' 结尾的条目改成 Store），不是我们的选择，
//     详见 writer.go 里 newEntryHeader 的说明；
//  2. Java 在 setTimeLocal 之后还调了 setCreationTime/setLastAccessTime，
//     写出的 0x5455 扩展时间戳带 atime/ctime（取【当前时间】）。Go 的
//     archive/zip 只暴露一个 Modified，且 LOC 与 CD 共用同一个 Extra 字段，
//     写不出「LOC 13 字节 / CD 5 字节」这种不对称布局。
//
// 不调 SetMode：Java 这边同样是裸 new ZipEntry，external attributes 与
// version made by 都按 MS-DOS 默认值写（0 与 20）。理由同 writer.go 的
// newEntryHeader —— SetMode 会把这两项改成 Unix 风格，解压时目录权限不同。
//
// 另外 Java 会把 CompressNode.comment 写进条目注释，但 zip 建树时 comment
// 从未被赋值（见 CompressNodeZipFactory），所以实际总是 null —— 这里同样写空。
func writePatchPackWithZip(ctx *WriteContext, zw *zip.Writer, rootNode *tree.Node) error {
	if len(rootNode.Children) == 0 {
		return nil
	}
	for _, child := range rootNode.Children {
		// 名字与编码走 writer.go 的同一套口径：GBK 条目名解码进 Path 供显示，
		// 写回时用 RawPath 的原始字节（见 entryNameOf 的说明）。
		name, nonUTF8 := entryNameOf(child)
		method := zip.Deflate
		if child.Dir() {
			method = zip.Store
		}
		hdr := &zip.FileHeader{
			Name:     name,
			Method:   method,
			Comment:  child.Comment,
			Extra:    child.Extra,
			Modified: child.ModifyTime(),
			NonUTF8:  nonUTF8,
		}

		entryWriter, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if !child.Dir() {
			step(ctx.Step, "Write to the file:"+child.Name)
			if err := child.ConsumeBytes(func(buf []byte) error {
				_, werr := entryWriter.Write(buf)
				return werr
			}); err != nil {
				return err
			}
		}

		if len(child.Children) > 0 && !tree.IsCompressNode(child) {
			if err := writePatchPackWithZip(ctx, zw, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// setNodeFile 设置节点的磁盘文件引用；路径为空时清空来源。
//
// 对应 Java 里 `node.setFile(file)` 允许传 null 的语义 —— 上游回滚代码
// 会把 oldReadmeNode.getFile()（可能为 null）直接塞回去。
func setNodeFile(node *tree.Node, path string) {
	if path == "" {
		node.SetSource(nil)
		return
	}
	node.SetFileSource(path)
}
