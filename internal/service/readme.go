// Package service 对应 Java 侧 cn.cpoet.patch.assistant.service。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package service

import (
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// ParseReadMe 解析补丁说明文件，返回其中的升级指令列表（纯文本解析，不查树）。
//
// 移植自 ReadMeFileService.getPathInfos(String, TreeNode, TreeNode) 的
// 文本扫描部分。逐行 find()，命中即产出一条记录，并记录该行在原文中的
// 起止下标（用于 UI 高亮对应行）。
//
// 与 Java 版一致的行为细节（这些不是 bug，是必须照搬的既有行为）：
//
//  1. 用 find() 而不是 matches()，所以一行里任意位置命中即可，
//     行首的无关文字会被忽略（例如 "说明：+foo.class" 也能命中）。
//  2. 变更类型前缀必须【紧贴】路径，中间不能有空格。因为前缀后的
//     字符类不含空白，正则回溯时会把前缀整个丢掉：
//     "+ foo.class"  → type=NONE, path1="foo.class"（前缀被吞）
//     "+foo.class"   → type=ADD,  path1="foo.class"（正确）
//     "- foo.class"  → type=NONE, path1="-", path2="foo.class"
//     最后一种会算出 ".../foo.class/-" 这种畸形路径。
//     该结论已用原版 Java 正则实跑核对，见 readme_test.go。
//  3. 三段路径的语义由 ReadMePathInfo.AppNodePath() 决定，见该方法的注释。
//
// 需要"查不到就丢弃"的完整语义时用 PathInfosOfText —— 那才是 Java 版
// getPathInfos 的真实行为。
func ParseReadMe(text string) []tree.ReadMePathInfo {
	var result []tree.ReadMePathInfo

	offset := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		offset += len(line) + 1

		m := util.ReadMePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		result = append(result, tree.ReadMePathInfo{
			Type:       constant.ChangeTypeOfCode(m[1]),
			Path1:      m[2],
			Path2:      m[4],
			Path3:      m[6],
			StartIndex: offset - len(line) - 1,
			EndIndex:   offset,
		})
	}
	return result
}

// PathInfos 取补丁根的说明文件内容并解析成路径信息列表。
//
// 移植自 Java ReadMeFileService.getPathInfos(PatchTreeInfo, TreeNode)。
// 根信息不存在、或 readme 内容为空时返回 nil。
//
// 注意空判定走 util.IsBlank（isSpaceChar 语义，不认 \t\n\r）——
// 一份只有制表符和换行的 README 在 Java 版里算"非空"，会被送去解析。
func PathInfos(patchTreeInfo *tree.PatchTreeInfo, patchRootNode *tree.Node) []tree.ReadMePathInfo {
	return PathInfosWithApp(patchTreeInfo, patchRootNode, nil)
}

// PathInfosWithApp 取说明文件内容并解析，appRootNode 用于解析 DEL 指令。
//
// 移植自 Java ReadMeFileService.getPathInfos(PatchTreeInfo, TreeNode, TreeNode)。
func PathInfosWithApp(patchTreeInfo *tree.PatchTreeInfo, patchRootNode, appRootNode *tree.Node) []tree.ReadMePathInfo {
	if patchTreeInfo == nil {
		return nil
	}
	rootInfo := patchTreeInfo.RootInfoByNode(patchRootNode)
	if rootInfo == nil || rootInfo.PatchSign == nil {
		return nil
	}
	readmeText := rootInfo.PatchSign.Readme
	if util.IsBlank(readmeText) {
		return nil
	}
	return PathInfosOfText(readmeText, patchRootNode, appRootNode)
}

// PathInfosOfText 解析说明文本，并为每条指令找到对应的树节点。
//
// 移植自 Java ReadMeFileService.getPathInfos(String, TreeNode, TreeNode)。
// 关键行为：
//
//   - DEL 指令：只有传了 appRootNode 才做查找；查不到就【丢弃该条】。
//     不传 appRootNode 时 DEL 指令会原样保留（UI 里查看补丁说明时走这条路）。
//   - 其余指令：在补丁树里找 patchNode，查不到同样丢弃。
//   - startIndex/endIndex 按行累计，用于把指令映射回原文行。
//
// 保真提示：Java 版这里用 BufferedReader.readLine() 逐行读，行分隔符被吃掉，
// 所以索引是"规范化后"的下标；Go 版按 '\n' 切分并去掉行尾 '\r'，索引算法
// 与 Java 一致（offset += line.length() + 1）。
func PathInfosOfText(readmeText string, patchRootNode, appRootNode *tree.Node) []tree.ReadMePathInfo {
	var pathInfos []tree.ReadMePathInfo

	offset := 0
	for _, line := range strings.Split(readmeText, "\n") {
		line = strings.TrimSuffix(line, "\r")
		offset += len(line) + 1

		m := util.ReadMePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		pathInfo := tree.ReadMePathInfo{
			Type:  constant.ChangeTypeOfCode(m[1]),
			Path1: m[2],
			Path2: m[4],
			Path3: m[6],
		}

		if pathInfo.Type == constant.ChangeDel {
			if appRootNode != nil {
				pathInfo.AppNode = FindAppNode(appRootNode, &pathInfo)
				if pathInfo.AppNode == nil {
					continue
				}
			}
		} else {
			pathInfo.PatchNode = FindPatchNode(patchRootNode, &pathInfo)
			if pathInfo.PatchNode == nil {
				continue
			}
		}

		pathInfo.StartIndex = offset - len(line) - 1
		pathInfo.EndIndex = offset
		pathInfos = append(pathInfos, pathInfo)
	}
	return pathInfos
}

// FindAppNode 在应用树里按 AppNodePath 找目标节点。
//
// 移植自 Java ReadMeFileService.findAppNode。路径每段都用 MatchPatchName 匹配 ——
// 也就是 jar 走前缀匹配、其余走全等，这一点很关键：README 里写
// "BOOT-INF/lib/foo" 也能命中 "BOOT-INF/lib/foo-1.0.0.jar"。
func FindAppNode(appRootNode *tree.Node, pathInfo *tree.ReadMePathInfo) *tree.Node {
	return tree.FindNodeByPathWith(appRootNode, pathInfo.AppNodePath(),
		func(child *tree.Node, name string) bool {
			return MatchPatchName(child, name)
		})
}

// FindPatchNode 在补丁树里找指令对应的补丁节点。
//
// 移植自 Java ReadMeFileService.findPatchNode，两级回落：
//  1. 先按 path1 整条路径查；
//  2. 查不到、且 path2 为空时，改用 path1 的【文件名】再查一次
//     （例如 README 写的是 "a/b/Foo.class" 而补丁里只有 "Foo.class"）。
//
// 保真提示：第二级只在 `!Objects.equals(fileName, path1)` 时才试，
// 即 path1 本身就是纯文件名时不会重复查一遍。
func FindPatchNode(patchRootNode *tree.Node, pathInfo *tree.ReadMePathInfo) *tree.Node {
	patchNode := tree.FindNodeByPath(patchRootNode, pathInfo.Path1)
	if patchNode == nil && pathInfo.Path2 == "" {
		fileName := util.FileName(pathInfo.Path1)
		if fileName != pathInfo.Path1 {
			patchNode = tree.FindNodeByPath(patchRootNode, fileName)
		}
	}
	return patchNode
}

// ChangeTypeName 返回变更类型的可读名称，用于日志与测试基准。
//
// 对应 Java 的枚举常量名（NONE/ADD/MOD/DEL/IGNORE），
// 刻意与 Java 命名一致，方便和 Java 版日志/基准逐行比对。
func ChangeTypeName(t constant.ChangeType) string {
	switch t {
	case constant.ChangeAdd:
		return "ADD"
	case constant.ChangeMod:
		return "MOD"
	case constant.ChangeDel:
		return "DEL"
	case constant.ChangeIgnore:
		return "IGNORE"
	default:
		return "NONE"
	}
}
