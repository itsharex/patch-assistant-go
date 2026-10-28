package main

import (
	"regexp"
	"strings"
	"time"

	"cn.cpoet.patch.assistant/internal/config"
	"cn.cpoet.patch.assistant/internal/i18n"
	"cn.cpoet.patch.assistant/internal/service"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// 全局搜索（对应 Java cn.cpoet.patch.assistant.view.search.SearchView）。
//
// 为什么这件事放后端：原版是在**内存里的树**上遍历的，而且路径式搜索
// （含 `/` 的关键词）会为了"探进嵌套 jar 里找"而**现场解压**那些 jar
// （SearchView.searchNodeWithPath 里的 buildChildrenWithCompress 分支）。
// 前端只有一份懒渲染的 DOM，没展开过的层级连行都没有 —— 它既看不到
// 树里的全部节点，也没有解压能力。所以搜索必须在这边做，前端只负责画结果。

// SearchItemView 是搜索对话框里的一行。
//
// 对应 Java 的 SearchItem / SearchHisItem / SearchNodeItem 三个类：
// 原版用继承区分"节点结果"和"历史记录"，这里用一个结构体 + History 标志，
// 因为前端只关心"画什么、点了做什么"。
type SearchItemView struct {
	// Name 名称：节点结果里是节点名；历史记录里是**当初搜索的关键词**。
	Name string `json:"name"`
	// Path 显示用的路径。节点结果是"根标签 + 各级祖先名"，历史记录固定
	// 是 "Search History"（原版 SearchListCell 里写死的字面量）。
	Path string `json:"path"`

	// Side / NodePath / Ancestors 只在节点结果上有值。
	//
	// Ancestors 是目标节点的祖先路径，**从根的直接子级往下**排 ——
	// 前端要按它逐层展开才能把那一行建出来（前端拿不到树，只拿得到
	// 懒渲染出来的 DOM）。这跟 LocateMapped 返回的祖先链是同一个形状。
	Side      string   `json:"side,omitempty"`
	NodePath  string   `json:"nodePath,omitempty"`
	Ancestors []string `json:"ancestors,omitempty"`

	// Icon 节点图标文件名（规则见 icon.go，逐条对齐原版 TreeNodeUtil.getIconImage）。
	Icon string `json:"icon,omitempty"`

	// History 标记这是历史记录项。
	History bool `json:"history,omitempty"`
	// SearchTime 历史记录的搜索时间，格式化后的字符串。
	SearchTime string `json:"searchTime,omitempty"`
	// Target 历史记录里记下的"当初双击的那个节点路径"。
	//
	// **原版只写不读**：SearchListCell 里 setTarget(node.getPath()) 写进去，
	// 但双击历史项时什么都不做（只有 SearchNodeItem 才响应双击）。
	// 这里照搬 —— 想改成"双击历史项跳回原节点"是增强，得先登记。
	Target string `json:"target,omitempty"`
}

// searchKeyword 一个待匹配的关键词。
//
// 对应 Java SearchView 里的私有内部类 SearchKeyword：关键词里出现 `*` 时
// 额外编译出一条正则，匹配时用正则的全匹配（matcher().matches()）代替 contains。
type searchKeyword struct {
	keyword string
	pattern *regexp.Regexp
}

// searchCollector 按插入顺序收集去重后的结果。
//
// 对应原版的 `Set<SearchItem> results = new LinkedHashSet<>()`：
// SearchItem 的 equals/hashCode 只看 (path, name)，所以同 (path,name)
// 只留第一条。LinkedHashSet 保证插入顺序，这里用 seen + 切片照搬。
type searchCollector struct {
	items []*SearchItemView
	seen  map[string]bool
}

func newSearchCollector() *searchCollector {
	return &searchCollector{seen: make(map[string]bool)}
}

func (c *searchCollector) add(v *SearchItemView) {
	key := v.Path + "\x00" + v.Name
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.items = append(c.items, v)
}

// SearchNodes 按关键词搜索两棵树里的节点。
//
// 对应 Java SearchView.searchNode(String)：关键词为空（按 StringUtil.isBlank
// 的口径）时返回**搜索历史**，否则遍历应用树与补丁树。**只搜已经加载进树的节点**
// —— 嵌套 jar 内部的文件在没展开前不在树里，普通搜看不见它们（与原版一致）。
// 想连嵌套 jar 一起搜用 SearchNodesDeep。
//
// 返回的每一项都自带祖先链，前端双击时直接照着展开即可 —— 不必再回后端
// 问一次"这个节点在哪一层"。
func (a *App) SearchNodes(keyword string) ([]*SearchItemView, error) {
	return a.searchNodes(keyword, false)
}

// SearchNodesDeep 是"兜底深度搜"：普通搜（SearchNodes）一条都没命中时，
// 额外把两棵树里**没展开的嵌套 jar** 现场展开再搜一遍。
//
// 为什么不直接让普通搜就兜底：普通搜是"输入即搜"，每敲一个键都会调一次；
// 这个包有 315 个嵌套 jar，全展开再搜实测约 2.8s，不防抖的话打一行文件名
// 就会触发十几次全量扫描。所以拆成两个入口：前端即时搜照旧（几十毫秒出结果），
// 空结果时**停手 500ms** 再调一次本方法 —— 一次停顿只扫一次，且只扫一次。
//
// 命中的那一支**保留展开**（前端双击结果时靠 GetState 拿到展开后的层级才能
// 跳过去，与 searchWithPathAt 的处理一致）；一条都没命中的整支**回滚**，免得
// 用户只是搜了个不存在的名字，树里却凭空多出一堆被展开的 jar。
func (a *App) SearchNodesDeep(keyword string) ([]*SearchItemView, error) {
	return a.searchNodes(keyword, true)
}

func (a *App) searchNodes(keyword string, allowDeep bool) ([]*SearchItemView, error) {
	if util.IsBlank(keyword) {
		return a.searchHistoryItems(), nil
	}

	c := newSearchCollector()
	paths := util.SplitPath(keyword)
	if len(paths) > 1 {
		kws, err := a.createKeywords(paths)
		if err != nil {
			return nil, err
		}
		if a.appTreeInfo != nil {
			a.searchWithPath(a.appTreeInfo.RootNode(), kws, c)
		}
		if a.patchTreeInfo != nil {
			a.searchWithPath(a.patchTreeInfo.RootNode(), kws, c)
		}
		// 路径式搜索自己就会沿途展开嵌套 jar（见 searchWithPathAt），不需要兜底。
		return c.items, nil
	}

	kw, err := a.createKeyword(keyword)
	if err != nil {
		return nil, err
	}
	if a.appTreeInfo != nil {
		a.searchSimple(a.appTreeInfo.RootNode(), kw, c)
	}
	if a.patchTreeInfo != nil {
		a.searchSimple(a.patchTreeInfo.RootNode(), kw, c)
	}
	if allowDeep && len(c.items) == 0 {
		a.searchNestedJars(kw, c)
	}
	return c.items, nil
}

// AddSearchHistory 记一条搜索历史。
//
// 对应 Java SearchListCell 双击节点结果时那段 runNotUI：名称取**搜索框里
// 当前的文本**（不是被点的那一项），路径写死 "Search History"，target 记下
// 被点节点的 path，时间取当下。
//
// 原版把历史写进 Configuration 并落盘 application.xml。Go 版 GUI 目前没有
// 配置文件（其它设置项同样只活在会话内），所以这里也只存在 App 上 ——
// 已在 MIGRATION.md 登记。
func (a *App) AddSearchHistory(keyword, target string) {
	now := time.Now()
	a.search.AddHistory(config.SearchHisItem{
		SearchItem: config.SearchItem{Path: "Search History", Name: keyword},
		SearchTime: &now,
		Target:     target,
	})
}

// searchHistoryItems 把会话内的搜索历史翻成结果项。
//
// 对应 Java SearchView.searchHistory：直接 addAll(history)，
// 最新的在最前（SearchConf.AddHistory 头插保证）。
func (a *App) searchHistoryItems() []*SearchItemView {
	out := make([]*SearchItemView, 0, len(a.search.History))
	for i := range a.search.History {
		h := a.search.History[i]
		item := &SearchItemView{
			Name:    h.Name,
			Path:    h.Path,
			History: true,
			Target:  h.Target,
		}
		if h.SearchTime != nil {
			item.SearchTime = formatTime(*h.SearchTime)
			// 原版 SearchListCell.addSearchHisInfo：有搜索时间才补上时间和时钟图标
			item.Icon = iconTime
		}
		out = append(out, item)
	}
	return out
}

// rootLabel 路径栈的第一个元素：根节点的显示标签。
//
// 对应 Java SearchView.createPathStack —— 注意它取的是**被搜那棵树根节点**
// 的 isPatch()，所以两个标签都是常量。
func (a *App) rootLabel(isPatch bool) string {
	if isPatch {
		return i18n.Tr(a.langCode(), "app.view.search.patch-pack")
	}
	return i18n.Tr(a.langCode(), "app.view.search.app-pack")
}

// ---------------------------------------------------------------------------
// 单关键词搜索
// ---------------------------------------------------------------------------

// searchSimple 从根的**子级**开始遍历。
//
// 对应 Java SearchView.searchNode(TreeNode, SearchKeyword, Set) 的顶层：
// 根节点自己**不参与匹配**，每个直接子级都拿一条新开的路径栈。
func (a *App) searchSimple(root *tree.Node, kw *searchKeyword, c *searchCollector) {
	if root == nil || root.Children == nil {
		return
	}
	for _, child := range root.Children {
		a.searchSimpleWalk(child, kw, []string{a.rootLabel(root.IsPatch)}, c)
	}
}

// searchSimpleWalk 对应 Java SearchView.searchNode(TreeNode, SearchKeyword,
// Stack, Set)：先看自己命不命中，再带着"把名字压进路径栈"往下走。
func (a *App) searchSimpleWalk(n *tree.Node, kw *searchKeyword, paths []string, c *searchCollector) {
	if matchKeyword(kw, n.Name) {
		c.add(nodeSearchItem(a, n, strings.Join(paths, util.Separator)))
	}
	if n.Children != nil {
		paths = append(paths, n.Name)
		for _, child := range n.Children {
			a.searchSimpleWalk(child, kw, paths, c)
		}
		paths = paths[:len(paths)-1]
	}
}

// ---------------------------------------------------------------------------
// 兜底深度搜：把没展开的嵌套 jar 展开后再搜一遍（SearchNodesDeep 专用）
// ---------------------------------------------------------------------------

// collectUnexpandedJars 收集 n 子树里所有"是嵌套压缩包但还没展开"的节点。
//
// 判据与 BuildChildrenWithCompress 开头一致：Children == nil 即视为未展开。
// 遇到未展开的 jar 就不再往下走（它里面本来还没有子节点）。
func collectUnexpandedJars(n *tree.Node) []*tree.Node {
	var out []*tree.Node
	var walk func(x *tree.Node)
	walk = func(x *tree.Node) {
		for _, c := range x.Children {
			if isCompressNode(c) && c.Children == nil {
				out = append(out, c)
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

// expandJarTree 展开 jarRoot 以及展开后露出的内层 jar，并记录每一层原来的
// Children（未展开时都是 nil），供调用方按"命中与否"整体回滚。
func (a *App) expandJarTree(jarRoot *tree.Node) ([]*tree.Node, [][]*tree.Node) {
	var nodes []*tree.Node
	var olds [][]*tree.Node
	queue := []*tree.Node{jarRoot}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.Children != nil || !isCompressNode(n) {
			continue // 已展开 / 不是嵌套包
		}
		old := n.Children
		if !service.BuildChildrenWithCompress(n, n.IsPatch) {
			continue // 打开失败（不是有效 zip 等）
		}
		nodes = append(nodes, n)
		olds = append(olds, old)
		queue = append(queue, collectUnexpandedJars(n)...) // 展开后露出的内层 jar
	}
	return nodes, olds
}

// jarPathStack 造兜底深度搜的初始路径栈：从树根一路到 jarRoot 的**显示名**链
//（含根标签与 jarRoot 自己）。少了这一段，兜底搜出来的显示路径就只有
// `应用包:/lib.jar/org`、不带 `BOOT-INF/lib/` 前缀，跟路径式搜索的口径不一致 ——
// 用户看不出这个 jar 到底挂在包的哪个位置。
func jarPathStack(a *App, root, jarRoot *tree.Node) []string {
	var rev []string
	for n := jarRoot; n != nil && n != root; n = n.Parent {
		rev = append(rev, n.Name)
	}
	rev = append(rev, a.rootLabel(root.IsPatch))
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// searchNestedJars 兜底：两棵树里每个"没展开的嵌套 jar"都展开、搜一遍。
//
// 命中的一支**保留展开** —— 前端双击结果时先 GetState 再 revealRow，那一跳要靠
// 展开后的层级才找得到行（searchWithPathAt 命中分支也是保留展开的，语义一致）。
// 一条都没命中的整支**回滚**（Children 还原成 nil），免得用户搜了个不存在的
// 名字，树里却凭空多出一堆被展开的 jar。
func (a *App) searchNestedJars(kw *searchKeyword, c *searchCollector) {
	var roots []*tree.Node
	if a.appTreeInfo != nil {
		roots = append(roots, a.appTreeInfo.RootNode())
	}
	if a.patchTreeInfo != nil {
		roots = append(roots, a.patchTreeInfo.RootNode())
	}
	for _, root := range roots {
		for _, jarRoot := range collectUnexpandedJars(root) {
			nodes, olds := a.expandJarTree(jarRoot)
			if len(nodes) == 0 {
				continue
			}
			sub := newSearchCollector()
			// 路径栈已经走到 jarRoot 自己，所以只从它的**子级**往下走
			// （照搬 searchSimple 的"从根的子级开始"；jarRoot 自己的名字是否
			// 命中，在第一遍普通搜里已经判过了 —— 走到兜底说明它没命中）
			stack := jarPathStack(a, root, jarRoot)
			for _, child := range jarRoot.Children {
				a.searchSimpleWalk(child, kw, stack, sub)
			}
			if len(sub.items) > 0 {
				for _, it := range sub.items {
					c.add(it)
				}
				continue // 命中 → 保留展开
			}
			for i := len(nodes) - 1; i >= 0; i-- { // 没命中 → 整支回滚（内层先还原）
				nodes[i].Children = olds[i]
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 路径式搜索（关键词里含 '/' 或 '\'）
// ---------------------------------------------------------------------------

// searchWithPath 路径式搜索的入口。
//
// 对应 Java SearchView.searchNodeWithPath(TreeNode, SearchKeyword[], Set)：
// 同样从根的子级开始。原版这里有个笔误 —— `node.getChildren().forEach(
// child -> searchNodeWithPath(node, ...))` 传的是**父节点** node 而不是 child；
// 但那个重载只用 node 取 isPatch()，而父子同树、isPatch 必然相同，
// 所以传父传子结果一致，这里直接传 child 写清楚。
func (a *App) searchWithPath(root *tree.Node, kws []*searchKeyword, c *searchCollector) {
	if root == nil || root.Children == nil {
		return
	}
	for _, child := range root.Children {
		a.searchWithPathAt(child, kws, 0, []string{a.rootLabel(root.IsPatch)}, c)
	}
}

// searchWithPathAt 从 index 这一级开始，在 n 这棵子树里找"沿父子链连续命中"的序列。
//
// 对应 Java SearchView.searchNodeWithPath(TreeNode, SearchKeyword[], int,
// Stack, Set)。三条分支逐条照搬：
//
//  1. 当前这一级命中了、且是最后一级 → 收下结果，返回 true；
//  2. 命中了但还有下一级 → 如果已经展开过（Children 非空）就往下走；
//     没展开但它是压缩包，就**现场解压**再往下走。走完若这一支一个都没中，
//     把解压出来的子级**回滚**（原版 node.setChildren(oldChildren)，而
//     oldChildren 此时必然是 null —— buildChildrenWithCompress 开头就是
//     "Children != null 直接返回 false"，所以能走到这儿说明它是 null）；
//  3. 兜底：对**所有**子级重启整条路径序列（index 归 0）。
//
// 第 3 条是原版的真实语义，别当成 bug 删掉：它意味着 `com/example/Foo.js`
// 在整个树里任何位置出现这条链都能命中，而不是只能从根往下数。
//
// 另：原版兜底分支把子级**遍历了两遍**（一遍 forEach 丢掉返回值，一遍
// collectChildrenMatchResult 收 flag），因为结果是 Set、幂等，两遍等价于
// 一遍，这里合成一遍。
func (a *App) searchWithPathAt(n *tree.Node, kws []*searchKeyword, index int, paths []string, c *searchCollector) bool {
	if index < len(kws) {
		if matchKeyword(kws[index], n.Name) {
			if index == len(kws)-1 {
				c.add(nodeSearchItem(a, n, strings.Join(paths, util.Separator)))
				return true
			}
			if len(n.Children) > 0 {
				paths = append(paths, n.Name)
				flag := a.walkChildrenWithPath(n.Children, kws, index+1, paths, c)
				paths = paths[:len(paths)-1]
				return flag
			}
			if isCompressNode(n) {
				old := n.Children
				if service.BuildChildrenWithCompress(n, n.IsPatch) && len(n.Children) > 0 {
					paths = append(paths, n.Name)
					flag := a.walkChildrenWithPath(n.Children, kws, index+1, paths, c)
					paths = paths[:len(paths)-1]
					if !flag {
						// 白解压了一场：这一支没有任何命中，把子级退回去，
						// 免得用户没搜到东西却发现树里凭空多了个展开的 jar。
						n.Children = old
					}
					return flag
				}
			}
		}
	}
	if n.Children != nil {
		paths = append(paths, n.Name)
		flag := a.walkChildrenWithPath(n.Children, kws, 0, paths, c)
		paths = paths[:len(paths)-1]
		return flag
	}
	return false
}

// walkChildrenWithPath 遍历一批子级，返回"有没有任意一个命中"。
// 对应 Java 的 collectChildrenMatchResult。
func (a *App) walkChildrenWithPath(children []*tree.Node, kws []*searchKeyword, index int, paths []string, c *searchCollector) bool {
	flag := false
	for _, child := range children {
		if a.searchWithPathAt(child, kws, index, paths, c) {
			flag = true
		}
	}
	return flag
}

// ---------------------------------------------------------------------------
// 关键词与结果
// ---------------------------------------------------------------------------

// createKeyword 对应 Java SearchView.createKeyword。
//
// 含 `*` 时把它换成 `.*` 编成正则，匹配改用**全匹配** —— 即 `*.js`
// 是"以 .js 结尾"，`a*b` 是"a 开头 b 结尾"。
//
// 与原版的一处刻意差异：正则编不出来时（关键词里既有 `*` 又有没配对的
// `[` 之类），Java 会抛 PatternSyntaxException，被 UIUtil.runNotUI 的线程
// 吞掉、界面**悄悄什么都不发生**。这里把错误交回去，前端弹一条提示 ——
// 静默失败比报错难排查得多。
func (a *App) createKeyword(keyword string) (*searchKeyword, error) {
	kw := &searchKeyword{keyword: keyword}
	if !strings.Contains(keyword, "*") {
		return kw, nil
	}
	pat, err := regexp.Compile("^(?:" + strings.ReplaceAll(keyword, "*", ".*") + ")$")
	if err != nil {
		return nil, err
	}
	kw.pattern = pat
	return kw, nil
}

func (a *App) createKeywords(keywords []string) ([]*searchKeyword, error) {
	out := make([]*searchKeyword, len(keywords))
	for i, k := range keywords {
		kw, err := a.createKeyword(k)
		if err != nil {
			return nil, err
		}
		out[i] = kw
	}
	return out, nil
}

// matchKeyword 对应 Java SearchView.matchKeyword：
// 有正则用正则全匹配，没有就用 contains（**区分大小写**）。
func matchKeyword(kw *searchKeyword, name string) bool {
	if kw.pattern != nil {
		return kw.pattern.MatchString(name)
	}
	return strings.Contains(name, kw.keyword)
}

// nodeSearchItem 把一个节点翻成结果项。
func nodeSearchItem(a *App, n *tree.Node, displayPath string) *SearchItemView {
	side := treeSideApp
	if n.IsPatch {
		side = treeSidePatch
	}
	return &SearchItemView{
		Name:      n.Name,
		Path:      displayPath,
		Side:      side,
		NodePath:  n.Path,
		Ancestors: ancestorPaths(n),
		Icon:      iconName(n),
	}
}

// ancestorPaths 取目标节点的祖先路径，从根的直接子级往下排。
//
// 沿 Parent 链上溯而不是靠 Path 字符串切分：嵌套 jar 内部子节点的 Path
// **不带 jar 前缀**（上游行为，见 MIGRATION.md §五），切字符串会得出错误的
// 层级。前端 revealRow 是沿 DOM 逐层找的，要的正是"每一层各自的 Path"。
func ancestorPaths(n *tree.Node) []string {
	var rev []string
	for p := n.Parent; p != nil && p.Parent != nil; p = p.Parent {
		rev = append(rev, p.Path)
	}
	if len(rev) == 0 {
		return nil
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
