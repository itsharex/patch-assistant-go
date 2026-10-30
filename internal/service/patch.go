package service

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/model"
	"cn.cpoet.patch.assistant/internal/pack"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// Step 进度回调，对应 Java 的 ProgressContext.step(String)。
//
// Java 侧它同时负责刷新进度条文字和日志，Go 版把它抽成一个纯回调，
// 由 CLI / GUI 决定怎么呈现。传 nil 表示不关心进度。
type Step func(string)

func step(step Step, msg string) {
	if step != nil {
		step(msg)
	}
}

// MatchPatchName 判断应用节点与补丁名称是否匹配。
//
// 移植自 Java PatchPackService.matchPatchName。核心规则只有两条：
//
//   - 应用节点是 .jar → 【前缀匹配】。所以补丁名 "foo" 能匹配上
//     "foo-1.2.3.jar"，这就是"按文件名打补丁"能生效的原因。
//   - 其余 → 全等匹配。
//
// 保真提示：前缀匹配是单向的（只允许补丁名比应用名短），且没有校验边界，
// 补丁名 "fo" 同样能命中 "foo-1.2.3.jar"。这是上游行为，不要"顺手"加边界。
func MatchPatchName(appNode *tree.Node, patchName string) bool {
	if appNode == nil {
		return false
	}
	if strings.HasSuffix(appNode.Name, constant.DotJar) {
		return strings.HasPrefix(appNode.Name, patchName)
	}
	return appNode.Name == patchName
}

// RefreshReadmeNode 在补丁根下找说明文件并读取内容。
//
// 移植自 Java PatchPackService.refreshReadmeNode。挑选顺序（必须照搬）：
//
//  1. 先扫一遍找 Type == README 的节点，找到立刻 break；
//  2. 否则看名字是否等于配置的 readme 文件名（忽略大小写），命中就记下
//     —— 注意【不 break】，继续往后扫；
//  3. 若手上还没有候选、且当前节点以 ".txt" 结尾，也记下 —— 同样不 break。
//
// 也就是说 2/3 两条是"后者覆盖前者"的，同一个根下多个 .txt 时最终拿到的是
// 扫描顺序里的最后一个。这是上游既有行为，不是笔误。
func RefreshReadmeNode(patchTreeInfo *tree.PatchTreeInfo, rootNode *tree.Node, readmeFileName string, st Step) {
	step(st, "refresh readme file")
	rootInfo := patchTreeInfo.RootInfoByNode(rootNode)
	if rootInfo == nil {
		return
	}
	if util.IsBlank(readmeFileName) {
		readmeFileName = constant.ReadmeFile
	}
	step(st, "readme file name: "+readmeFileName)

	var readmeNode *tree.Node
	for _, child := range rootNode.Children {
		if child.Type == constant.TreeNodeTypeReadme {
			readmeNode = child
			break
		}
		if strings.EqualFold(readmeFileName, child.Name) {
			readmeNode = child
		} else if readmeNode == nil && strings.HasSuffix(child.Name, constant.DotTxt) {
			readmeNode = child
		}
	}

	if readmeNode == nil {
		step(st, "readme file not found")
		// 没有说明文件了 → 把旧状态清干净。
		//
		// 不清的话"取消标记说明文件"之后 PatchSign.Readme 还是上一次的内容：
		// 说明面板照旧显示旧文本，refreshMappedNodeWithReadme 还会按旧指令
		// 继续映射 —— 用户看到的是"取消了但没生效"。
		rootInfo.ReadmeNode = nil
		if rootInfo.PatchSign != nil {
			rootInfo.PatchSign.Readme = ""
		}
		patchTreeInfo.UpdateReadmeText()
		return
	}
	step(st, "target readme file: "+readmeNode.Name)
	readmeNode.Type = constant.TreeNodeTypeReadme
	rootInfo.ReadmeNode = readmeNode
	bytes := readNodeBytesOrEmpty(readmeNode)
	// 二进制内容不解码：把 zip/jar 的字节丢给 DecodeString 不会报错
	// （GB18030 的映射覆盖全部字节组合），只会解出一大段方块和生僻字，
	// 再被 ReadMePattern 匹配出一堆不存在的指令 —— 用户看到的就是"乱码"。
	// 菜单项已经挡掉了压缩包，这里是兜底：补丁包里本来就放了个二进制文件
	// 当 README 时同样不该解析。
	if len(bytes) > 0 && !util.IsBinary(bytes) {
		rootInfo.PatchSign.Readme = util.DecodeString(bytes, util.DetectCharset(bytes))
	}
	patchTreeInfo.UpdateReadmeText()
}

// CleanMappedNode 递归清空节点上绑定的信息。
// 对应 Java PatchPackService.cleanMappedNode(totalInfo, rootNode, excludeReadme)。
func CleanMappedNode(total *tree.TotalInfo, rootNode *tree.Node, excludeReadme bool) {
	CleanMappedNodeFiltered(total, rootNode, excludeReadme, nil)
}

// CleanMappedNodeFiltered 带过滤器的清空。
//
// 移植自 Java PatchPackService.cleanMappedNode(..., Predicate)。excludeReadme
// 为真时，会额外保护"直接挂在 rootNode 下的 README 节点"不被清掉 ——
// 注意判定用的是 `rootNode == node.getParent()` 的引用比较，
// 所以更深层的 README 节点不受保护。
func CleanMappedNodeFiltered(total *tree.TotalInfo, rootNode *tree.Node, excludeReadme bool, filter tree.NodeFilter) {
	if excludeReadme {
		tree.DeepCleanMappedNode(total, rootNode, func(node *tree.Node) bool {
			if rootNode == node.Parent && node.Type == constant.TreeNodeTypeReadme {
				return true
			}
			return filter != nil && filter(node)
		})
		return
	}
	tree.DeepCleanMappedNode(total, rootNode, filter)
}

// RefreshMappedNode 刷新补丁文件映射信息（整套匹配流程的入口）。
//
// 移植自 Java PatchPackService.refreshMappedNode，三步走：
//  1. 先清掉历史映射（保留 README 节点）；
//  2. 按 README 里写的指令做一轮映射；
//  3. 再按「路径匹配 / 文件名匹配」开关做一轮映射。
//
// 第 1 步的过滤器在遇到 CUSTOM_ROOT 节点时会顺手把它从 patchTreeInfo 的
// 自定义根表里摘掉 —— 因为自定义根本身会被清成普通节点。
func RefreshMappedNode(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, patchTreeInfo *tree.PatchTreeInfo, rootNode *tree.Node, opts PatchOptions) {
	step(opts.Step, "clear history mapped info")
	CleanMappedNodeFiltered(total, rootNode, true, func(node *tree.Node) bool {
		if node == rootNode {
			return true
		}
		if node.Type == constant.TreeNodeTypeCustomRoot {
			patchTreeInfo.RemoveCustomRootInfo(node)
		}
		return false
	})

	if appTreeInfo == nil || patchTreeInfo == nil {
		return
	}
	refreshMappedNodeWithReadme(total, appTreeInfo, patchTreeInfo, rootNode, opts)
	RefreshMappedNodeWithPathOrName(total, appTreeInfo, patchTreeInfo, opts)
	// 整体替换项走独立通道，必须在常规匹配之后 —— 常规匹配的两处索引
	// （nameMapping / patchNodeMapping）都会跳过被标记的节点，所以这里
	// 不会和它抢目标。
	MatchWholeReplaceNodes(total, appTreeInfo, rootNode, opts)
}

// MatchWholeReplaceNodes 把用户标记的"整体替换项"映射到应用树。
//
// 与常规匹配是两条独立通道：被标记的节点不进 nameMapping / patchNodeMapping
// （见 match.go 的两处 continue），所以它和它的子级都不会被逐文件匹配碰到。
// 这正是"整体替换"要的语义 —— 内层包里的 class 不该被挑出来单独匹配。
//
// 目标判定见 findWholeReplaceTarget。找不到就什么都不做（节点保持未映射，
// 前端据此显示"未匹配"），不猜、不新建。
func MatchWholeReplaceNodes(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo,
	rootNode *tree.Node, opts PatchOptions) {
	if appTreeInfo == nil || rootNode == nil {
		return
	}
	step(opts.Step, "match whole replace nodes")

	var marked []*tree.Node
	walkTree(rootNode, func(n *tree.Node) {
		if n.WholeReplace && !n.Dir() {
			marked = append(marked, n)
		}
	})
	if len(marked) == 0 {
		return
	}

	appRoot := appTreeInfo.RootNode()
	used := make(map[*tree.Node]bool, len(marked))
	for _, patchNode := range marked {
		appNode := findWholeReplaceTarget(appRoot, patchNode, used)
		if appNode == nil {
			step(opts.Step, "whole replace target not found: "+patchNode.Name)
			continue
		}
		used[appNode] = true
		// 同一个补丁节点之前可能绑在别处（换过目标或取消重标），先解绑，
		// 否则它会同时挂在两个应用节点上、统计也会算两遍。
		if patchNode.MappedNode != nil {
			CleanMappedNode(total, patchNode.MappedNode, true)
		}
		step(opts.Step, "whole replace "+patchNode.Name+" -> "+appNode.Path)
		MappedModNodeWithType(total, appTreeInfo, appNode, patchNode, opts)
	}
}

// findWholeReplaceTarget 在应用树里给整体替换项找目标。
//
// 判定是"应用节点的**主名**被补丁名包含"，取主名最长的那个：
//
//	补丁 20261029_account-server_存量账户区分变更和普通.zip
//	应用 BOOT-INF/lib/account-server.jar   → 主名 account-server，命中
//
// 为什么不用 MatchPatchName 那套（全等 / jar 前缀）：交付包的命名习惯是
// "日期_服务名_变更描述"，和包内的真实文件名对不上，前缀匹配也无从下手。
// 反过来"应用名被补丁名包含"是稳定的 —— 服务名一定出现在补丁名里。
//
// 主名短于 3 个字符的候选直接排除：`a.jar` 这种主名几乎能被任何补丁名
// 包含，放进来只会误命中。
func findWholeReplaceTarget(appRoot, patchNode *tree.Node, used map[*tree.Node]bool) *tree.Node {
	if appRoot == nil || patchNode == nil {
		return nil
	}
	var best *tree.Node
	bestLen := 0
	walkTree(appRoot, func(n *tree.Node) {
		if n == appRoot || n.Dir() || used[n] {
			return
		}
		if !IsWholeReplaceName(n.Name) {
			return
		}
		stem := strings.TrimSuffix(n.Name, filepath.Ext(n.Name))
		if len(stem) < 3 || !strings.Contains(patchNode.Name, stem) {
			return
		}
		if len(stem) > bestLen {
			best, bestLen = n, len(stem)
		}
	})
	return best
}

// IsWholeReplaceName 整体替换的目标只能是"整包"类文件。
//
// 不用 tree.IsCompressName：那个只认 .jar/.zip，而交付包里被替换的经常是
// 部署用的 .war / .ear。两者用途不同，不能混 —— IsCompressName 还决定
// 写出时要不要递归子级，改它会影响打包行为。
func IsWholeReplaceName(name string) bool { return IsArchiveName(name) }

// IsArchiveName 判断文件名是不是"整包"类（.jar/.war/.zip/.ear）。
//
// 与 tree.IsCompressName 刻意分开：那个只认 .jar/.zip，因为它同时决定
// "写出时要不要递归子级"（改它会动到打包行为）；这里包含部署用的 .war/.ear，
// 只用于"这东西是个包、不是一份文档"这类判断 —— 比如能不能标成说明文件。
func IsArchiveName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jar", ".war", ".zip", ".ear":
		return true
	}
	return false
}

// walkTree 前序遍历（含自身）。
//
// 嵌套压缩包是惰性展开的，没展开的节点 Children 为空 —— 遍历到它就停，
// 不会凭空读 zip。对找目标来说这没问题：要找的是"包"本身，不是包里的东西。
func walkTree(node *tree.Node, fn func(*tree.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for _, c := range node.Children {
		walkTree(c, fn)
	}
}

// refreshMappedNodeWithReadme 按 README 指令做一轮映射。
// 移植自 Java PatchPackService.refreshMappedNodeWithReadme。
func refreshMappedNodeWithReadme(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, patchTreeInfo *tree.PatchTreeInfo, treeNode *tree.Node, opts PatchOptions) {
	step(opts.Step, "refresh mapped info by readme file")
	pathInfos := PathInfos(patchTreeInfo, treeNode)
	if len(pathInfos) == 0 {
		return
	}
	for i := range pathInfos {
		// IGNORE（"?" 前缀）表示"这条指令不参与匹配"，直接跳过
		if pathInfos[i].Type == constant.ChangeIgnore {
			continue
		}
		matchMappedNodeWithReadme(total, &pathInfos[i], appTreeInfo, opts)
	}
}

// matchMappedNodeWithReadme 把一条 README 指令映射到应用树。
// 移植自 Java PatchPackService.matchMappedNodeWithReadme。
func matchMappedNodeWithReadme(total *tree.TotalInfo, pathInfo *tree.ReadMePathInfo, appTreeInfo *tree.AppTreeInfo, opts PatchOptions) {
	appNodePath := pathInfo.AppNodePath()
	step(opts.Step, "app node path: "+appNodePath)
	paths := util.SplitPath(appNodePath)
	doMatchMappedNodeChildrenWithReadme(total, appTreeInfo, pathInfo, paths, 0, appTreeInfo.RootNode(), opts)
}

// doMatchMappedNodeChildrenWithReadme 在 appNode 的子级里找路径的第 index 段。
//
// 移植自 Java PatchPackService.doMatchMappedNodeChildrenWithReadme，这是
// README 映射里最容易读错的一段，两个要点：
//
//  1. 先遍历子级尝试递归匹配（doMatchMappedNodeWithReadme），任一子级成功
//     就整体返回 —— 所以同一层有多个同名候选时，先命中的赢。
//  2. 子级都没命中【且】指令是 ADD/NONE 时，才会"凭空造节点"：
//     沿剩余路径逐层建 VirtualNode 占位，最后一段建 MappedNode 并绑定补丁节点。
//     DEL/MOD 指令走到这里直接失败返回 false —— 因为它们要求目标必须已存在。
//
// 保真提示：造节点时用的是 `parent.getAndInitChildren().add(newAppNode)`，
// 而 getAndInitChildren 返回的 SortLinkedList 的 add 是【纯追加】
// （排序逻辑被注释掉了，见 tree.Node.AddChild），所以新节点落在同级末尾。
// Go 的 AddChild 同样是追加，等价。
func doMatchMappedNodeChildrenWithReadme(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, pathInfo *tree.ReadMePathInfo,
	paths []string, index int, appNode *tree.Node, opts PatchOptions) bool {
	if index >= len(paths) {
		return false
	}
	for _, child := range appNode.Children {
		if doMatchMappedNodeWithReadme(total, appTreeInfo, pathInfo, paths, index, child, opts) {
			return true
		}
	}
	if pathInfo.Type != constant.ChangeAdd && pathInfo.Type != constant.ChangeNone {
		return false
	}

	now := time.Now()
	parent := appNode
	var newAppNode *tree.Node
	for index < len(paths) {
		if index+1 == len(paths) {
			newAppNode = tree.NewMappedNode(pathInfo.PatchNode)
		} else {
			virtualNode := tree.NewVirtualDir(paths[index], now)
			newAppNode = virtualNode
		}
		newAppNode.Parent = parent
		newAppNode.Path = tree.GetNodePathByParent(parent, newAppNode)
		inheritRawPath(newAppNode, parent, pathInfo.PatchNode)
		parent.AddChild(newAppNode)
		parent = newAppNode
		index++
	}

	tree.MappedNode(total, newAppNode, pathInfo.PatchNode, constant.TreeNodeTypeAdd)
	addMappedNodeChildren(total, newAppNode, pathInfo.PatchNode)
	return true
}

// addMappedNodeChildren 把补丁节点的子级逐个映射到应用节点下。
//
// 移植自 Java PatchPackService.addMappedNodeChildren。应用侧缺失的子节点会
// 被"造"出来（文件造 MappedNode、目录造 VirtualNode），已存在的则直接复用。
//
// 保真提示：已存在子节点的索引是按【名称】建的，而补丁侧子节点的查找也用
// 名称，所以这里不像 README 路径匹配那样走 jar 前缀匹配 —— 补丁里叫
// "foo-1.0.0.jar"、应用里叫 "foo-2.0.0.jar" 时是匹配不上的。
// inheritRawPath 让"从补丁条目造出来的应用侧节点"继承条目名的原始字节。
//
// 场景：补丁里有一个中文名的新文件（GBK、无 UTF-8 标志位），应用包里原本没有。
// 这时应用侧会造一个 MappedNode / VirtualNode，路径由"父路径 + 名字"拼出来 ——
// 拼出来的 Path 是解码后的 UTF-8。若不管 RawPath，重打包就会把这条名字写成
// UTF-8，等于我们悄悄改掉了用户包里的名字编码。
//
// 只在源条目带原始字节（名字原本不是 UTF-8）时生效。父路径取父节点的原始
// 字节，没有就用 Path。唯一的例外是**包根节点**：它的 Path 是磁盘路径
// （`D:\x\app.jar`），拿来当前缀会拼出 `D:\x\app.jar\中文.docx` 这种"条目名"，
// 重打包时直接写坏 —— 根下条目的原始名就是它自己的原始字节。
func inheritRawPath(node, parent, src *tree.Node) {
	if src.RawPath == "" {
		return
	}
	raw := rawBaseName(src.RawPath)
	if parent.Dir() || parent.Parent != nil {
		base := parent.Path
		if parent.RawPath != "" {
			base = parent.RawPath
		}
		raw = util.JoinPath(base, raw)
	}
	// 拼出来和 Path 一样就说明没差（父路径本身是 ASCII），留空即可。
	if raw != node.Path {
		node.RawPath = raw
	}
}

// rawBaseName 取一个原始条目名的最后一段（去掉目录尾斜杠）。
//
// 按 '/' 切分对 GBK 字节是安全的：GBK 的尾字节范围是 0x40–0xFE，不含 0x2F。
func rawBaseName(rawPath string) string {
	s := strings.TrimSuffix(rawPath, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func addMappedNodeChildren(total *tree.TotalInfo, appNode, patchNode *tree.Node) {
	if len(patchNode.Children) == 0 {
		return
	}
	childAppNodeMap := make(map[string]*tree.Node, len(appNode.Children))
	for _, c := range appNode.Children {
		if _, exists := childAppNodeMap[c.Name]; !exists {
			childAppNodeMap[c.Name] = c
		}
	}
	for _, childPatchNode := range patchNode.Children {
		childAppNode := childAppNodeMap[childPatchNode.Name]
		if childAppNode == nil {
			if !childPatchNode.Dir() {
				childAppNode = tree.NewMappedNode(childPatchNode)
			} else {
				childAppNode = tree.NewVirtualDir(childPatchNode.Name, childPatchNode.ModifyTime())
			}
			childAppNode.Parent = appNode
			// 保真提示：Java 这里传的是 childPatchNode（不是 childAppNode）
			// 去算路径。两者 Name 相同，所以结果一致，但属上游的笔误式写法。
			childAppNode.Path = tree.GetNodePathByParent(appNode, childPatchNode)
			inheritRawPath(childAppNode, appNode, childPatchNode)
			appNode.AddChild(childAppNode)
		}
		tree.MappedNode(total, childAppNode, childPatchNode, constant.TreeNodeTypeAdd)
		addMappedNodeChildren(total, childAppNode, childPatchNode)
	}
}

// ApplyPatchNodesToApp 把补丁树上的节点"拖"到应用树的某个落点上。
//
// 移植自 Java TreeNodeTreeMatchProcessor（FileTreeCell.onDragDropped 里
// `dragInfo != null` 的那条分支）。规则与"往行上拖一个磁盘文件"同构：
//
//  1. 落点只决定**目标目录**：目录行是它自己，文件行退回父目录
//     （原版 `appNode.isDir() ? appNode : appNode.getParent()`）；
//  2. 在目标目录里按名字（MatchPatchName）找同名子项：
//     命中 → 标成修改（MOD）并绑定；没命中 → 造一个映射节点（ADD）；
//  3. 单个**目录**落到**目录行**上 → 把这个目录的**子项**逐个并进去
//     （原版 exec 里 `matchWithDir` 那条分支）。
//
// **补丁包的根行不算"目录"**：它代表的是那个 .jar / .zip 文件本身，拖过去的
// 意思就是"把这个包放进这个目录"，所以走第 2 条按名匹配（命中就换、没有就加）。
// 早期把它当容器去并子项，结果拖到 `BOOT-INF/lib` 上会把 jar 拆开铺进去。
//
// 落点也可以是**根行**：应用包根节点虽然是文件节点（对齐 Java 的 FileNode），
// 它的 Children 就是包内顶层条目，所以它自己就是容器 —— 不能再往上退 Parent
// （根节点的 Parent 是 nil，退上去等于整次拖放静默丢失）。
//
// 【与原版的一处偏差】原版 onDragOver 里 `dragInfo.isHasMappedNode()` 为真就
// **拒绝整次拖放** —— 也就是"已经被自动匹配过的节点拖不动"。而打开补丁包时
// 自动匹配几乎会把所有节点都映射一遍，等于这条拖拽平时根本用不上。这里改成
// **先解绑旧位置再落到新位置**（语义是"把这条补丁改指到别处"），
// 统计因此不会重复计数。已登记 MIGRATION.md。
func ApplyPatchNodesToApp(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo,
	appNode *tree.Node, patchNodes []*tree.Node, opts PatchOptions) {
	if appNode == nil || len(patchNodes) == 0 {
		return
	}
	// 目标目录：目录行就是它自己，文件行退回父目录（原版
	// `appNode.isDir() ? appNode : appNode.getParent()`）。
	// 唯一的例外是**包根节点** —— 它是文件节点但 Parent 为 nil，而它的
	// Children 就是包内顶层条目，所以它本身就该当容器用。
	tar := appNode
	if !tar.Dir() && tar.Parent != nil {
		tar = tar.Parent
	}

	if len(patchNodes) == 1 {
		// 单个**目录**落到**目录行** → 把它的子项逐个并进去（原版 matchWithDir）。
		//
		// 注意这里**只对目录**成立。补丁包的根行虽然也"是个容器"，但它代表的是
		// **一个文件**（那个 .jar / .zip）—— 拖它过去的意思是"把这个包本身放进
		// 这个目录"，跟拖一个磁盘上的 jar 进去完全一样，所以走下面的按名匹配。
		// 早期这里把根行也当成"整包套用"（并子项），于是拖到 BOOT-INF/lib 上会
		// 把 jar **拆开**铺进去，用户当场报了"他把 jar 包分解放进去了"。
		el := patchNodes[0]
		if el.Dir() && appNode.Dir() {
			matchPatchChildrenInto(total, appTreeInfo, tar, el, opts)
			return
		}
	}
	for _, el := range patchNodes {
		matchOnePatchNode(total, appTreeInfo, tar, el, opts)
	}
}

// matchPatchChildrenInto 把 patchNode 的子项逐个匹配进 appNode。
// 对应原版 TreeNodeMatchProcessor.matchWithDir（file 元素没有子级，等于空操作）。
func matchPatchChildrenInto(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo,
	appNode, patchNode *tree.Node, opts PatchOptions) {
	for _, c := range patchNode.Children {
		// 说明文件不是要打进包里的内容。单个节点拖过来时由 DropPatchNode 挡掉，
		// 但**拖一个目录**过来时会扫到它下面的东西 —— 补丁根下的 README 只有
		// 拖根行才会碰到，而拖目录时不会；这里留着是兜底：只要将来有任何路径
		// 把 README 带进合并，都不该在应用树里凭空造出一个 README.txt。
		if c.Type == constant.TreeNodeTypeReadme {
			continue
		}
		matchOnePatchNode(total, appTreeInfo, appNode, c, opts)
	}
}

// matchOnePatchNode 在 appNode 的子级里按名字找 el 的对应项：命中就改绑，否则新建。
// 对应原版 TreeNodeMatchProcessor.match。
func matchOnePatchNode(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo,
	appNode, el *tree.Node, opts PatchOptions) {
	if el == nil {
		return
	}
	// 这个补丁节点已经绑在别处 → 先解绑旧位置。不解的话同一个补丁节点会挂在
	// 两个应用节点上，统计里也会算两遍（原版是直接拒绝这次拖放，见函数头）。
	if el.MappedNode != nil {
		CleanMappedNode(total, el.MappedNode, true)
	}
	// 拖的是**整个补丁包**（根行）时不做子项合并：那是"把这个 jar 换进去"，
	// 包里的条目已经整份包含在新 jar 里了。再逐条 ADD 一遍既把统计算多，
	// 界面上也会让人以为 jar 被"拆开"了 —— 用户 2026-10-28 报的就是这个观感。
	// 写出时也走不到那些子节点：节点自己绑着，写的是整份字节。
	recurse := el.Parent != nil
	for _, child := range appNode.Children {
		if MatchPatchName(child, el.Name) {
			MappedModNodeWithType(total, appTreeInfo, child, el, opts)
			if recurse {
				matchPatchChildrenInto(total, appTreeInfo, child, el, opts)
			}
			return
		}
	}
	child := createAppNodeFromPatch(total, appNode, el)
	if recurse {
		matchPatchChildrenInto(total, appTreeInfo, child, el, opts)
	}
}

// createAppNodeFromPatch 在应用侧造一个对应补丁节点的节点：目录造 VirtualNode，
// 文件造 MappedNode 并立刻按 ADD 绑定。
//
// 对应原版 TreeNodeMatchProcessor.createAndGetAppNode。路径写法与
// addMappedNodeChildren 里那段保持一致（相对最近的目录祖先，目录不带尾斜杠）。
func createAppNodeFromPatch(total *tree.TotalInfo, appNode, el *tree.Node) *tree.Node {
	var child *tree.Node
	if el.Dir() {
		child = tree.NewVirtualDir(el.Name, el.ModifyTime())
	} else {
		child = tree.NewMappedNode(el)
	}
	child.Parent = appNode
	child.Path = tree.GetNodePathByParent(appNode, el)
	// 落点是包根节点时，GetNodePathByParent 只能返回一个分隔符 —— 它要找的是
	// "祖父"，而根节点没有祖父。根下条目的 Path 就是自己的名字（与
	// addOrReplaceChild 里 `parentPath == ""` 那段保持一致）。
	if appNode.Parent == nil && !appNode.Dir() {
		child.Path = el.Name
	}
	inheritRawPath(child, appNode, el)
	appNode.AddChild(child)
	if !el.Dir() {
		tree.MappedNode(total, child, el, constant.TreeNodeTypeAdd)
	}
	return child
}

// doMatchMappedNodeWithReadme 判断单个应用节点是否命中路径的第 index 段。
//
// 移植自 Java PatchPackService.doMatchMappedNodeWithReadme：
//   - 最后一段命中 → 按指令类型做映射；
//   - 还有后续段 → 递归下探；下探前若当前节点没有子级、但它是个 .jar，
//     会先展开这个 jar（这就是"补丁能打进 fat-jar 内部"的关键）。
func doMatchMappedNodeWithReadme(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, pathInfo *tree.ReadMePathInfo,
	paths []string, index int, appNode *tree.Node, opts PatchOptions) bool {
	if !MatchPatchName(appNode, paths[index]) {
		return false
	}
	if index == len(paths)-1 {
		mappedNodeWithType(total, appTreeInfo, pathInfo, appNode, opts)
		return true
	}
	if len(appNode.Children) > 0 {
		return doMatchMappedNodeChildrenWithReadme(total, appTreeInfo, pathInfo, paths, index+1, appNode, opts)
	}
	if strings.HasSuffix(appNode.Name, constant.DotJar) && BuildChildrenWithCompress(appNode, false) {
		return doMatchMappedNodeChildrenWithReadme(total, appTreeInfo, pathInfo, paths, index+1, appNode, opts)
	}
	return false
}

// mappedNodeWithType 按 README 指令的变更类型分派映射动作。
//
// 移植自 Java PatchPackService.mappedNodeWithType。注意 switch 的
// 默认分支把 MOD 和 NONE 一起当"修改"处理 —— NONE（无前缀）的指令
// 因此等价于 "!" 修改指令。这是上游行为，已在 MIGRATION.md 登记。
func mappedNodeWithType(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, pathInfo *tree.ReadMePathInfo, appNode *tree.Node, opts PatchOptions) {
	switch pathInfo.Type {
	case constant.ChangeDel:
		MappedDelNodeWithType(total, appNode)
	case constant.ChangeAdd:
		MappedAddNodeWithType(total, appNode, pathInfo.PatchNode)
	default: // MOD / NONE
		MappedModNodeWithType(total, appTreeInfo, appNode, pathInfo.PatchNode, opts)
	}
}

// MappedDelNodeWithType 把应用节点标记为删除。
//
// 移植自 Java PatchPackService.mappedDelNodeWithType：
//   - 已经是 DEL 就直接返回（幂等）；
//   - 若原状态不是 NONE，先清掉它已有的绑定（连带子级）；
//   - 只有原状态是 NONE 或 MOD 才真正改成 DEL —— 也就是说一个已经
//     被标成 ADD 的节点，收到 DEL 指令后不会被改成 DEL，而是保持 ADD
//     （但它的绑定已经被上面那步清掉了）。这是上游行为。
func MappedDelNodeWithType(total *tree.TotalInfo, appNode *tree.Node) {
	if appNode.Type == constant.TreeNodeTypeDel {
		return
	}
	oldType := appNode.Type
	if appNode.Type != constant.TreeNodeTypeNone {
		CleanMappedNode(total, appNode, true)
	}
	if oldType == constant.TreeNodeTypeNone || oldType == constant.TreeNodeTypeMod {
		tree.CountAndSetNodeType(total, appNode, constant.TreeNodeTypeDel)
	}
}

// MappedAddNodeWithType 把应用节点标记为新增。
//
// 移植自 Java PatchPackService.mappedAddNodeWithType。只有当前是 DEL
// 的节点才会被处理 —— 即"先被标删、又要求新增"时，撤销删除改为新增。
// 其余状态（NONE/MOD/ADD）一概不动，直接返回。
func MappedAddNodeWithType(total *tree.TotalInfo, appNode, patchNode *tree.Node) {
	if appNode.Type != constant.TreeNodeTypeDel {
		return
	}
	CleanMappedNode(total, appNode, true)
	tree.MappedNode(total, appNode, patchNode, constant.TreeNodeTypeAdd)
	addMappedNodeChildren(total, appNode, patchNode)
}

// MappedModNodeWithType 把应用节点标记为修改，并绑定补丁节点。
//
// 移植自 Java PatchPackService.mappedModNodeWithType：
//   - DEL 状态的节点直接跳过（已删除的不用改）；
//   - ADD 状态的节点：先只清自己（过滤器 `appNode == node` 把子级保护住），
//     再按 ADD 重新绑定 —— 相当于"刷新"这个新增节点的绑定关系；
//   - MOD 状态的节点：整体清空后重新绑定（保证绑定是干净的）；
//   - 最后统一做差异提示与内部类绑定。
func MappedModNodeWithType(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, appNode, patchNode *tree.Node, opts PatchOptions) {
	if appNode.Type == constant.TreeNodeTypeDel {
		return
	}
	if appNode.Type == constant.TreeNodeTypeAdd {
		CleanMappedNodeFiltered(total, appNode, true, func(node *tree.Node) bool {
			return appNode == node
		})
		tree.MappedNode(total, appNode, patchNode, constant.TreeNodeTypeAdd)
		addMappedNodeChildren(total, appNode, patchNode)
		return
	}
	if appNode.Type == constant.TreeNodeTypeMod {
		CleanMappedNode(total, appNode, true)
	}
	tree.MappedNode(total, appNode, patchNode, constant.TreeNodeTypeMod)
	CreatePatchDiffInfo(appTreeInfo, appNode, patchNode, opts)
	MappedInnerClassNode(total, appTreeInfo, appNode, patchNode, opts)
}

// RefreshMappedNodeWithPathOrName 按配置开关执行"路径匹配 / 文件名匹配"。
//
// 移植自 Java PatchPackService.refreshMappedNodeWithPathOrName。有自定义根时
// 每个自定义根各跑一轮（PatchMatchProcessor 每次只处理一个补丁根），
// 没有自定义根时才拿主根跑。
//
// 注意：PatchMatchProcessor 是有状态的（它缓存了 nameMapping 并会被消费掉），
// 所以每轮都必须新建一个实例 —— 别把 processor 提到循环外面复用。
func RefreshMappedNodeWithPathOrName(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, patchTreeInfo *tree.PatchTreeInfo, opts PatchOptions) {
	step(opts.Step, fmt.Sprintf("refresh mapped with name: %t, with path: %t", opts.WithName, opts.WithPath))
	if !opts.WithPath && !opts.WithName {
		return
	}
	customRoots := patchTreeInfo.CustomRootInfoMap()
	if len(customRoots) > 0 {
		for _, entry := range customRoots {
			processor := NewPatchMatchProcessor(total, appTreeInfo, opts)
			processor.SetAppRootNode(appTreeInfo.RootNode())
			processor.SetPatchRootNode(entry.Node)
			processor.Exec()
		}
		return
	}
	processor := NewPatchMatchProcessor(total, appTreeInfo, opts)
	processor.SetAppRootNode(appTreeInfo.RootNode())
	processor.SetPatchRootNode(patchTreeInfo.RootNode())
	processor.Exec()
}

// MappedInnerClassNode 绑定 class 的内部类子节点。
//
// 移植自 Java PatchPackService.mappedInnerClassNode。逻辑：
//
//   - 只处理两边都是 .class 的节点，且至少一边有子级；
//   - 补丁侧有、应用侧没有的内部类 → 造 MappedNode 并标 ADD；
//   - 两边都有 → 标 MOD、做差异提示，并【递归】处理更深层的内部类；
//   - 补丁侧遍历完后，应用侧还剩的内部类 → 标 DEL（补丁里已经没有它们了）。
//
// 保真提示：应用侧的内部类索引是「名称 → 节点」，所以只在本层比较名称，
// 不会跨层误配。而 DEL 分支用的是 countAndSetNodeType，会连带把子级
// 也标成 DEL。
func MappedInnerClassNode(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, appNode, patchNode *tree.Node, opts PatchOptions) {
	if !strings.HasSuffix(appNode.Name, constant.DotClass) || !strings.HasSuffix(patchNode.Name, constant.DotClass) {
		return
	}
	if len(appNode.Children) == 0 && len(patchNode.Children) == 0 {
		return
	}
	innerNodeMap := make(map[string]*tree.Node, len(appNode.Children))
	for _, c := range appNode.Children {
		if _, exists := innerNodeMap[c.Name]; !exists {
			innerNodeMap[c.Name] = c
		}
	}
	for _, patchInnerNode := range patchNode.Children {
		appInnerNode := innerNodeMap[patchInnerNode.Name]
		if appInnerNode == nil {
			appInnerNode = tree.NewMappedNode(patchInnerNode)
			appInnerNode.Parent = appNode
			appInnerNode.Path = util.JoinPath(util.GetDirPath(appNode.Path), patchInnerNode.Name)
			appNode.AddChild(appInnerNode)
			tree.MappedNode(total, appInnerNode, patchInnerNode, constant.TreeNodeTypeAdd)
			continue
		}
		tree.MappedNode(total, appInnerNode, patchInnerNode, constant.TreeNodeTypeMod)
		CreatePatchDiffInfo(appTreeInfo, appInnerNode, patchInnerNode, opts)
		MappedInnerClassNode(total, appTreeInfo, appInnerNode, patchInnerNode, opts)
		delete(innerNodeMap, patchInnerNode.Name)
	}
	for _, v := range innerNodeMap {
		tree.CountAndSetNodeType(total, v, constant.TreeNodeTypeDel)
	}
}

// ---------------------------------------------------------------------------
// 补丁树构建
// ---------------------------------------------------------------------------

// GetPatchTreeNode 从一个补丁包（zip/jar）或补丁目录（文件夹）建树。
//
// 移植自 Java PatchPackService.getTreeNode(File)。
//
// 保真提示（上游死代码）：Java 版在这里 new 了一个 PatchSign、填了 name/path，
// 再传给 doGetTreeNodeWithCompress 去填 md5/sha1，但这个对象【从未被挂到
// 树上】—— 随后 createPatchRootInfo 又新建了一个 PatchSign。结果是
// rootInfo.PatchSign.Path 永远是空的。Go 版照搬这个结果（不补 path），
// 但省掉了那个无意义的中间对象。
func GetPatchTreeNode(filePath string, st Step) (*tree.PatchTreeInfo, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	// 对应 Java `file.getPath()`（Windows 下 '/' → '\'）。补丁目录模式下
	// 这个值会成为根节点 Path，并参与 README 文件的落盘路径拼接。
	filePath = util.ToPlatformPath(filePath)

	treeInfo := tree.NewPatchTreeInfo()
	// 保真提示：根节点是【文件节点】还是目录节点，取决于补丁包本身的类型 ——
	// Java 用 `new FileNode()` 再 `setFile(file)`，isDir() 就取自磁盘。
	// 补丁包是 zip/jar 时根节点 isDir()==false，这一点会被
	// GetNodePathByParent 用到，别"顺手"统一成目录节点。
	var rootNode *tree.Node
	if info.IsDir() {
		rootNode = tree.NewDir(filepath.Base(filePath), filePath, true)
	} else {
		rootNode = tree.NewFile(filepath.Base(filePath), filePath, info.Size(), info.ModTime(),
			&tree.FileSource{Path: filePath}, true)
	}
	rootNode.Type = constant.TreeNodeTypeRoot

	if info.IsDir() {
		doGetTreeNodeWithDir(filePath, rootNode)
	} else {
		doGetTreeNodeWithCompress(filePath, rootNode)
	}

	treeInfo.SetRootNode(rootNode)
	treeInfo.SetRootInfo(CreatePatchRootInfo(rootNode))
	return treeInfo, nil
}

// doGetTreeNodeWithDir 从磁盘目录递归建树（"补丁目录"模式）。
//
// 移植自 Java PatchPackService.doGetTreeNodeWithDir。两处细节：
//   - 子节点的 path 用的是【绝对路径】（file.getPath()），不是相对路径；
//   - .class 且名字含 '$' 的文件不直接挂上，先收集起来，
//     本层扫完后统一用 HandleInnerClassUnder 归位到外部类下。
func doGetTreeNodeWithDir(dir string, parentNode *tree.Node) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var innerClasses []*tree.Node
	for _, entry := range entries {
		childPath := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		node := tree.NewDir(entry.Name(), childPath, true)
		if !info.IsDir() {
			node = tree.NewFile(entry.Name(), childPath, info.Size(), info.ModTime(), &tree.FileSource{Path: childPath}, true)
		}
		node.Parent = parentNode
		if entry.IsDir() {
			doGetTreeNodeWithDir(childPath, node)
			parentNode.AddChild(node)
			continue
		}
		if strings.HasSuffix(entry.Name(), constant.DotClass) && strings.Contains(entry.Name(), "$") {
			innerClasses = append(innerClasses, node)
			continue
		}
		parentNode.AddChild(node)
	}
	if len(innerClasses) > 0 {
		pack.HandleInnerClassUnder(parentNode, innerClasses)
	}
}

// doGetTreeNodeWithCompress 从压缩包建树，并回填根节点的 md5/size。
//
// 移植自 Java PatchPackService.doGetTreeNodeWithCompress。Java 版会把
// md5/sha1 同时写进 rootNode 和一个随后被丢弃的 PatchSign；Go 版只写
// rootNode（PatchSign 的 md5/sha1 由 CreatePatchRootInfo 重新算）。
func doGetTreeNodeWithCompress(filePath string, rootNode *tree.Node) {
	f, err := os.Open(filePath)
	if err != nil {
		return
	}
	fi, statErr := f.Stat()

	hashInfo, err := util.GetHashInfo(f)
	_ = f.Close()
	if err == nil {
		rootNode.SetSize(int64(hashInfo.Length))
		rootNode.SetMd5(hashInfo.Md5)
	}
	if statErr != nil {
		return
	}

	/* 与应用包同一条口径：走 BuildZipTreeFromPath（只读中央目录、条目
	   惰性读），不要 BuildZipTreeFromReader（io.ReadAll 整个包）。
	   补丁包通常不大，但"整个补丁打进内存"没有任何理由留着 ——
	   用户 2026-10-26 报的就是大包内存占用。 */
	sub, err := pack.BuildZipTreeFromPath(filePath, fi.Size(), true)
	if err != nil {
		return
	}
	for _, child := range sub.Children {
		rootNode.AddChild(child)
	}
}

// CreatePatchRootInfo 构造补丁根的签名信息。
//
// 移植自 Java PatchPackService.createPatchRootInfo。注意两点：
//   - 目录根不填 md5/sha1（直接跳过整个 if）；
//   - 非目录根用 `treeNode.getMd5()`（【不是】getMd5OrInit），
//     即依赖调用方事先把 md5 算好。md5 为空时会写出空字符串。
func CreatePatchRootInfo(treeNode *tree.Node) *tree.PatchRootInfo {
	patchSign := &model.PatchSign{Name: treeNode.Name}
	if !treeNode.Dir() {
		sha1Hex, err := sha1OfNode(treeNode)
		if err == nil {
			patchSign.Sha1 = sha1Hex
		}
		patchSign.Md5 = treeNode.Md5
	}
	rootInfo := tree.NewPatchRootInfo()
	rootInfo.PatchSign = patchSign
	return rootInfo
}

// sha1OfNode 流式计算节点内容的 sha1 十六进制串。
//
// 对应 Java `treeNode.consumeBytes(len, buf -> sha1Digest.update(buf, 0, len))`。
func sha1OfNode(node *tree.Node) (string, error) {
	d := sha1.New()
	err := node.ConsumeBytes(func(buf []byte) error {
		_, werr := d.Write(buf)
		return werr
	})
	if err != nil {
		return "", err
	}
	return util.ToHexStr(d.Sum(nil)), nil
}
