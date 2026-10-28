package service

import (
	"regexp"
	"strconv"
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// PatchOptions 补丁匹配开关，对应 Java PatchConf 里的 pathMatch /
// fileNameMatch / patchFileRegex 三项。
type PatchOptions struct {
	// WithPath 开启路径匹配
	WithPath bool
	// WithName 开启文件名匹配
	WithName bool
	// PatchFileRegex 参与匹配的补丁文件名正则。
	// 空白表示不限制；默认值见 config.PatchConf 的默认配置。
	PatchFileRegex string
	// PatchFileDiff 是否开启"补丁文件比对"提示
	PatchFileDiff bool
	// LangCode 语言码，用于取提示文案
	LangCode string
	// Step 进度回调。放在这里是为了让它跟着"运行参数"一起传递，
	// 免得每个内部函数都要多带一个参数。
	Step Step
}

// PatchMatchProcessor 补丁匹配处理器。
//
// 移植自 Java PatchMatchProcessor。它做两件事，顺序固定：
//
//  1. 【路径匹配】把补丁树的子级建成"名称 → 节点"索引，然后从应用树根开始
//     逐层下探，同名（jar 为前缀）即绑定。一个补丁节点只会被消费一次
//     （匹配成功就从索引里删掉）。
//  2. 【文件名匹配】把补丁树里所有"还没被绑过、状态为 NONE、且名字过得了
//     补丁正则"的文件收成一张全局表，然后遍历应用树，按名字直接捞。
//
// 两轮都会消费同一份 nameMapping，所以顺序不能换。
type PatchMatchProcessor struct {
	total       *tree.TotalInfo
	appTreeInfo *tree.AppTreeInfo
	opts        PatchOptions
	step        Step

	appRootNode   *tree.Node
	patchRootNode *tree.Node

	// filterPattern 补丁文件名正则，惰性编译。
	// 对应 Java 里那个 volatile static FILTER_FILE_PATTERN 缓存。
	filterPattern    *regexp.Regexp
	filterPatternSrc string
}

// NewPatchMatchProcessor 创建匹配处理器。
func NewPatchMatchProcessor(total *tree.TotalInfo, appTreeInfo *tree.AppTreeInfo, opts PatchOptions) *PatchMatchProcessor {
	return &PatchMatchProcessor{total: total, appTreeInfo: appTreeInfo, opts: opts, step: opts.Step}
}

// SetAppRootNode 设置应用包根节点。
func (p *PatchMatchProcessor) SetAppRootNode(n *tree.Node) { p.appRootNode = n }

// SetPatchRootNode 设置补丁包根节点。
func (p *PatchMatchProcessor) SetPatchRootNode(n *tree.Node) { p.patchRootNode = n }

// isAcceptPatchFile 判断文件名是否通过补丁正则过滤。
//
// 移植自 Java PatchMatchProcessor.isAcceptPatchFile。三点行为：
//
//   - 正则为空/空白 → 全部放行；
//   - 正则变化时重新编译（Java 里用双重检查锁缓存，Go 侧单线程调用，
//     直接比较源码串即可）；
//   - 用 matches() 而不是 find()，即必须【整串匹配】。
//     默认正则 "(.*\.class)|(.*\.html)|(.*\.js)|(.*\.css)" 依赖这个语义。
func (p *PatchMatchProcessor) isAcceptPatchFile(name string) bool {
	pattern := p.opts.PatchFileRegex
	if util.IsBlank(pattern) {
		p.filterPattern = nil
		return true
	}
	if p.filterPattern == nil || p.filterPatternSrc != pattern {
		re, err := regexp.Compile(pattern)
		if err != nil {
			// Java 版会在 Pattern.compile 处直接抛 PatternSyntaxException。
			// Go 侧选择"放行全部"以免因为一条配置错误就整轮匹配失败，
			// 属刻意的健壮性改进，已在 MIGRATION.md 登记。
			p.filterPattern = nil
			p.filterPatternSrc = pattern
			return true
		}
		p.filterPattern = re
		p.filterPatternSrc = pattern
	}
	return p.filterPattern.MatchString(name)
}

// Exec 执行一轮匹配。
//
// 移植自 Java PatchMatchProcessor.exec()。
func (p *PatchMatchProcessor) Exec() {
	if !p.checkRootNode() {
		return
	}

	nameMapping := make(map[string]*tree.Node)
	if p.opts.WithName {
		for _, node := range p.patchRootNode.Children {
			if node.Dir() || node.MappedNode != nil {
				continue
			}
			if node.Type != constant.TreeNodeTypeNone {
				continue
			}
			if !p.isAcceptPatchFile(node.Name) {
				continue
			}
			if _, exists := nameMapping[node.Name]; !exists {
				nameMapping[node.Name] = node
			}
		}
		step(p.step, "number of nodes matched by name "+strconv.Itoa(len(nameMapping)))
	}

	var patchNodes []*tree.Node
	if p.opts.WithPath {
		patchNodes = p.patchRootNode.Children
	}
	p.match(p.appRootNode.Children, patchNodes, nameMapping)
}

// checkRootNode 两端根节点都必须有子级才值得匹配。
func (p *PatchMatchProcessor) checkRootNode() bool {
	return p.appRootNode != nil && len(p.appRootNode.Children) > 0 &&
		p.patchRootNode != nil && len(p.patchRootNode.Children) > 0
}

// match 在应用节点列表上跑一轮匹配。
//
// 移植自 Java PatchMatchProcessor.match(List, List, Map)。patchNodes 为空
// 表示跳过路径匹配，只做文件名匹配。
func (p *PatchMatchProcessor) match(appNodes, patchNodes []*tree.Node, nameMapping map[string]*tree.Node) bool {
	if len(appNodes) == 0 {
		return false
	}
	patchNodeMapping := p.createPatchNodeMapping(patchNodes)
	flag := false
	for _, appNode := range appNodes {
		if p.matchNode(appNode, patchNodeMapping, nameMapping) {
			flag = true
		} else {
			step(p.step, "not matched "+appNode.Path)
		}
	}
	return flag
}

// createPatchNodeMapping 把补丁节点列表建成"名称 → 节点"索引，并过滤掉
// 不通过补丁正则的条目。返回的 map 会被 matchNode 逐步消费。
//
// 移植自 Java PatchMatchProcessor.createPatchNodeMapping。注意 Java 在
// patchNodes 为空时返回的是 Collections.emptyMap()（不可变），
// Go 侧返回 nil 即可 —— 调用方有 len>0 守卫，不会误写。
func (p *PatchMatchProcessor) createPatchNodeMapping(patchNodes []*tree.Node) map[string]*tree.Node {
	if len(patchNodes) == 0 {
		return nil
	}
	mapping := make(map[string]*tree.Node, len(patchNodes))
	for _, node := range patchNodes {
		if !p.isAcceptPatchFile(node.Name) {
			continue
		}
		if _, exists := mapping[node.Name]; !exists {
			mapping[node.Name] = node
		}
	}
	return mapping
}

// matchNode 判断单个应用节点是否能匹配上补丁节点。
//
// 移植自 Java PatchMatchProcessor.match(TreeNode, Map, Map)。四条分支：
//
//  1. 已绑定过 → 直接算成功（幂等）；
//  2. 有路径索引 → 先按路径找补丁节点，命中后：
//     - 应用节点是目录 → 递归匹配两边子级；特别地，
//     "BOOT-INF/lib/" 目录会走多线程版本（见下方说明）；
//     - 应用节点是 .jar 而补丁节点不是 .jar → 展开 jar 再匹配子级，
//     匹配不上就把 Children 置回 nil 回滚（避免留下无意义的展开结果）；
//     - 其余 → 直接绑定；
//  3. 路径没命中 → 退化成按文件名匹配。
//
// 【与 Java 的差异（刻意）】Java 在 "BOOT-INF/lib/" 目录上会开 5 个线程
// 并发匹配。但那段代码本身是有竞态的：多个线程共享同一个 HashMap
// （patchNodeMapping / nameMapping）并同时 put/remove，没有加锁，
// 结果依赖线程调度顺序。Go 版改为【顺序执行同一批节点】——
// 处理到的节点集合完全相同，结果确定，且不再有数据竞争。
// 代价：当同一个 jar 有多个"互为前缀"的补丁候选时，选中的是哪一个
// 可能与 Java 某次运行的结果不同（Java 那次本身也不稳定）。
func (p *PatchMatchProcessor) matchNode(appNode *tree.Node, patchNodeMapping, nameMapping map[string]*tree.Node) bool {
	if appNode.MappedNode != nil {
		return true
	}
	if len(patchNodeMapping) > 0 {
		matchPatchNode := p.findPatchNode(appNode, patchNodeMapping)
		if matchPatchNode != nil {
			if appNode.Dir() {
				return p.match(appNode.Children, matchPatchNode.Children, nameMapping)
			}
			if strings.HasSuffix(appNode.Name, constant.DotJar) && !strings.HasSuffix(matchPatchNode.Name, constant.DotJar) {
				if appNode.Children != nil {
					return p.match(appNode.Children, matchPatchNode.Children, nameMapping)
				}
				if !BuildChildrenWithCompress(appNode, false) {
					return false
				}
				if p.match(appNode.Children, matchPatchNode.Children, nameMapping) {
					return true
				}
				appNode.Children = nil
				return false
			}
			p.mappedNode(appNode, matchPatchNode)
			return true
		}
	}
	return p.matchWithName(appNode, nameMapping)
}

// matchWithName 按文件名匹配。
//
// 移植自 Java PatchMatchProcessor.matchWithName。与应用节点同名（或 jar 前缀
// 命中）的补丁节点会被从 nameMapping 里【摘除】—— 所以一个补丁节点
// 只能匹配一个应用节点，先到先得。
//
// 应用节点是 jar 时同样会展开下探：这就是"只按文件名打补丁"能深入
// fat-jar 内部的原因。
func (p *PatchMatchProcessor) matchWithName(appNode *tree.Node, nameMapping map[string]*tree.Node) bool {
	if len(nameMapping) == 0 {
		return false
	}
	if appNode.Dir() {
		return p.match(appNode.Children, nil, nameMapping)
	}
	patchNode, ok := nameMapping[appNode.Name]
	if ok {
		delete(nameMapping, appNode.Name)
		p.mappedNode(appNode, patchNode)
		return true
	}
	if !strings.HasSuffix(appNode.Name, constant.DotJar) {
		return false
	}
	if appNode.Children != nil {
		return p.match(appNode.Children, nil, nameMapping)
	}
	if !BuildChildrenWithCompress(appNode, false) {
		return false
	}
	if p.match(appNode.Children, nil, nameMapping) {
		return true
	}
	appNode.Children = nil
	return false
}

// findPatchNode 在补丁索引里找应用节点对应的补丁节点，找到即摘除。
//
// 移植自 Java PatchMatchProcessor.findPatchNode：
//   - 应用节点是 .jar 时，遍历索引的【所有键】做前缀匹配，第一个命中的胜出；
//   - 否则按名称全等查找。
//
// 保真提示：Java 遍历的是 HashMap.keySet()，顺序不确定；Go 的 map 遍历
// 顺序同样不确定。也就是说"多个补丁名都能前缀命中同一个 jar"时，
// 选哪个是未定义的 —— 这是上游就有的不确定性，不是移植引入的。
func (p *PatchMatchProcessor) findPatchNode(appNode *tree.Node, patchNodeMapping map[string]*tree.Node) *tree.Node {
	if strings.HasSuffix(appNode.Name, constant.DotJar) {
		for patchName := range patchNodeMapping {
			if MatchPatchName(appNode, patchName) {
				node := patchNodeMapping[patchName]
				delete(patchNodeMapping, patchName)
				return node
			}
		}
	}
	node, ok := patchNodeMapping[appNode.Name]
	if !ok {
		return nil
	}
	delete(patchNodeMapping, appNode.Name)
	return node
}

// mappedNode 完成一次成功匹配的绑定动作。
//
// 移植自 Java PatchMatchProcessor.mappedNode。注意它调的是
// mappedModNodeWithType —— 也就是说路径/文件名匹配命中的一律按"修改"
// 处理，不会按"新增"。补丁树里"新增"的语义只由 README 指令触发。
func (p *PatchMatchProcessor) mappedNode(appNode, patchNode *tree.Node) {
	step(p.step, "successful match "+appNode.Path)
	MappedModNodeWithType(p.total, p.appTreeInfo, appNode, patchNode, p.opts)
}
