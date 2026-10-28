package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"cn.cpoet.patch.assistant/internal/config"
	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/i18n"
	"cn.cpoet.patch.assistant/internal/manualdel"
	"cn.cpoet.patch.assistant/internal/pack"
	"cn.cpoet.patch.assistant/internal/service"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// App 是暴露给前端的绑定对象。
//
// 它只做三件事：持有当前打开的两个包、把内部结构翻译成前端能吃的 JSON、
// 按与 CLI 完全相同的顺序调用 service 包。**业务逻辑一律不进这里** ——
// 匹配/保存的规则都在 internal/service 里，GUI 与 CLI 共用同一套，
// 这样黄金基准（`_extract/golden/run.sh`）验证过的行为对两者同时成立。
type App struct {
	ctx context.Context

	appPath       string
	patchPath     string
	appTreeInfo   *tree.AppTreeInfo
	patchTreeInfo *tree.PatchTreeInfo

	// total 是【整个会话复用】的统计对象，**不要每次匹配都新建**。
	//
	// 这一点是踩出来的：Java 的 HomeContext 里 `final TotalInfo totalInfo`
	// 全程复用一个实例，只在应用树刷新时调 cleanTotal 清零
	// （HomeLeftTreeView 的 APP_TREE_REFRESHING 处理器）。
	// 早期版本每次 refresh 都 NewTotalInfo，结果"删除"计数会丢：
	// 被 README 的 `-` 指令标成 DEL 的应用节点没有补丁侧对应物，
	// 清理阶段遍历补丁树时够不到它，而 MappedDelNodeWithType 开头又是
	// "已是 DEL 就直接返回" —— 于是新计数器上没人给它加 1。
	// 复用同一个实例就不会有这个空档。
	total *tree.TotalInfo

	// settings 与 PatchConf 同构，是"设置"面板的状态。
	settings config.PatchConf

	// search 是"全局搜索"的历史记录。
	//
	// 对应 Java Configuration.search（SearchConf）。原版落盘 application.xml，
	// Go 版 GUI 没有配置文件（其它设置项同样只活在会话内），所以这里只存内存 ——
	// 见 MIGRATION.md。HistoryLimit 由 config.NewSearchConf() 给默认 30。
	search config.SearchConf

	// focusTree 聚焦哪棵树，对应 Java HomeContext.focusTreeStatus。
	//
	// 原版把它持久化进 Configuration；GUI 这边设置项目前都还是会话级的
	// （见 ApplySettings 的说明），所以这里也只在会话内生效。
	focusTree int

	// showMappedLinks 是设置里的「总是显示匹配连线」。
	//
	// 关掉之后连线跟随「开启补丁文件比对」开关（见 linksEnabled）——
	// 比对关着的时候补丁树那侧本来就没有"改动"语义，连出来只是挡视线。
	// 与其它设置项一样只活在会话里，不落盘。
	showMappedLinks bool

	// lang 界面语言代码，取 Configuration.Genera.Language（默认跟随系统）。
	//
	// 存成字段而不是每次去 config.New() 取：patchMenu 是**逐节点**调用的，
	// 每次新建一份 Configuration 纯属浪费。将来加了语言设置项，改这里即可。
	lang string

	// progress 收集最近若干条进度，供界面底部显示。
	progress []string

	// contentDiffModel 内容预览的"差异显示模式"开关。
	//
	// 对应 Java Configuration.content.diffModel（默认 false = 双栏）。
	// 原版把它持久化进配置文件，GUI 这边的设置项都还是会话级的
	// （见 ApplySettings 的说明），所以这里也只在会话内保持 ——
	// 一旦在预览对话框里切过，后续打开的文件都沿用这个模式。
	contentDiffModel bool

	// compareA / compareB 是最近一次「包对比」用的那两个包。
	//
	// 为什么要记在 Go 侧、而不是让前端把路径传回来：对比结果里每一行都能
	// "点开看内容"（见 OpenCompareContent），而"读哪个包"必须与**产出这份
	// 结果的那次对比**一致。前端传路径的话，用户在结果出来后改了槽里的
	// 路径，点开看到的就会是另一个包的内容 —— 规则留在这一侧就不会错位。
	compareA string
	compareB string

	// openExternal 把磁盘路径交给系统默认程序打开。
	//
	// 对应 Java OSUtil.openFile（ContentView.openWithSystem 用的那个）。
	// 抽成字段纯粹是为了可测：单测里换成桩，否则跑测试会真的弹出
	// 记事本/看图器，污染开发机。生产路径见 NewApp。
	openExternal func(path string) error

	// decompileCache 是反编译结果的【会话级】内存缓存。
	//
	// 背景：Go 版把 fernflower jar 内嵌，每次 Decompile 都要重新释放 + 起 JVM，
	// 本机实测首次冷启动约 1 秒（internal/decompile 的 decompileTimeout=60s）。
	// 用户双击同一个 class 反复预览时，每次都付这 1 秒就不值了 ——
	// 命中缓存直接返回字符串，省掉 JVM 冷启动。
	//
	// 关键诚实点：这只是"重复打开同一个 class"变快，**首次打开那个 class 的
	// ~1 秒冷启动无法消除**（JVM 本身就这么多）。不要对外宣称"反编译变快了"。
	//
	// key = 节点路径 + 主类与所有内部类字节的 sha256：只有"同一份字节"才复用，
	// 重打包导致字节变了会自动失效；不同 jar 里同路径但字节不同的 class 也会
	// 自然 miss（key 含字节哈希）。打开新包无需手动清缓存 —— 内容变了 key 就变。
	decompileCache map[string]string
	decompileMu    sync.Mutex

	// manualDelStore 用户对应用包做的"删除 / 标记删除"持久化。
	//
	// 原版语义上删除只活在内存里，关掉重开就丢（Java 也是这样）。
	// 这里加一层薄薄的兜底：把每次操作追加到配置目录的一个 JSON 文件，
	// 下次打开同一个 jar 时自动重应用。让"删除 bootstrap.yml"这种直觉动作
	// 能跨会话保持。
	//
	// 留 nil 表示 GUI 未启用此能力（单元测试可以塞 nil 不影响）。
	manualDelStore *manualdel.Store

	// manualDelPendingNow 是"本包还有删除记录吗"的缓存，供右键菜单判断
	// 「恢复原状」要不要出现。
	//
	// 为什么不直接在 appMenu 里读记录：菜单是**逐节点**生成的，一个几千节点的
	// 包就是几千次文件读取。所以在 state() 开头算一次放这儿，菜单直接用。
	// 会改记录的只有删除 / 标删 / 撤销，它们都以 a.state() 收尾，缓存不会过期。
	manualDelPendingNow bool

	// startArgs 是命令行传进来的启动目标（双击 .jar / 带参数启动）。
	//
	// 解析在 NewApp 里做；**实际加载延迟到第一次 GetState 才做**。
	// 原因：Wails v2 的 OnStartup 与前端 JS 加载是并发起步的，前端 init()
	// 调 GetState 的时机可能早于把包打开完，那时 GetState 拿到空状态、
	// 前端 apply 成"未打开"且不再重拉。把加载用 startupOnce 收口，所有调用方
	//（OnStartup 与 GetState）都会**阻塞到加载完成**再返回，竞态彻底消除。
	// 对应 Java 的 AppContext.initArgs + StartUpInfo。
	startArgs startupTargets

	// startupOnce 保证"打开启动包"只做一次，且调用方会阻塞到加载完成。
	// 早期版本用 bool 早置位，导致"加载还没完、GetState 已被放行返回空树"
	// 的竞态——见 §五 第 49→50 项。sync.Once.Do 的语义正是"做且仅做一次、
	// 并发调用方都等到做完"，天然消除该竞态。
	startupOnce sync.Once
}

// NewApp 创建绑定对象，设置项取 PatchConf 的文档默认值。
//
// 启动参数在这里解析（而不是在 main 里）：解析逻辑跟"谁调用它"无关，
// App 自己知道就够了，main.go 也能保持"只做 Wails 装配"。
// 跑测试时 os.Args 全是 `-test.*` 开关，会被 parseStartupArgs 忽略。
//
// **跑测试时不要走这条构造** —— 它会初始化真实的持久化层（%APPDATA%），
// 单测一旦调用 OpenAppPack / DeleteAppNode 就会读写用户机器上的删除记录，
// 把别的用例带崩（真实踩过：调试时写进去一条，TestSaveContentFlowsIntoPatchOutput
// 立刻红）。测试一律用 newTestApp(t)，它把 store 指到 t.TempDir()。
func NewApp() *App {
	a := &App{
		settings:     config.NewPatchConf(),
		search:       config.NewSearchConf(),
		total:        tree.NewTotalInfo(),
		focusTree:    focusTreeAll,
		lang:         config.New().Genera.Language,
		openExternal: openPathWithOS,
		startArgs:    parseStartupArgs(os.Args[1:]),
		// 连线默认开着 —— 它是"这个补丁改了哪儿"最直接的表达，
		// 藏起来反而没人知道有这个功能。设置里可以改成跟随比对开关。
		showMappedLinks: true,
	}
	// 持久化层 —— 定位配置目录失败时不阻断 GUI，记一笔就好。
	if s, err := manualdel.NewStore(constant.AppName); err != nil {
		fmt.Fprintf(os.Stderr, "manualdel 初始化失败（不影响主功能）: %v\n", err)
	} else {
		a.manualDelStore = s
	}
	return a
}

// langCode 返回当前界面语言，用于取文案。
func (a *App) langCode() string { return a.lang }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 启动目标的实际加载收口到 GetState（见 ensureStartup 的说明）：前端
	// init() 第一次 GetState 会同步把它打开完再返回，避免与 OnStartup 竞态。
	// 这里仍走一次 ensureStartup，保证即便有别的入口先碰 GetState 也只加载一次。
	a.ensureStartup()
}

// ---------------------------------------------------------------------------
// 传给前端的视图结构
// ---------------------------------------------------------------------------

// NodeView 是树节点传到前端的形态。
type NodeView struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Dir      bool        `json:"dir"`
	Size     int64       `json:"size"`
	Type     string      `json:"type"`
	ModTime  string      `json:"modTime"`
	Md5      string      `json:"md5,omitempty"`
	Children []*NodeView `json:"children,omitempty"`

	// Compress 表示这是个压缩包节点（.jar/.zip），**即使现在还没有子级**，
	// 它也是可以展开的。
	//
	// 为什么必须单独给这个标记：嵌套压缩包是【惰性】展开的（对应原版双击
	// 节点时的 buildChildrenWithCompress），建树和匹配阶段都只展开"用得上"
	// 的那些。所以 `BOOT-INF/lib/xxx.jar` 这类节点在 JSON 里往往一个子级
	// 都没有 —— 前端如果按"有 children 才画三角"来判断，它就成了叶子，
	// 用户永远打不开它，藏在里面的文件也就永远看不到（包括补丁命中的那些）。
	Compress bool `json:"compress,omitempty"`

	// Icon 文件名左侧那张图的相对路径（如 `icons/file-class.png`），
	// 由后端按原版 TreeNodeUtil.getIconImage 的规则算好 —— 见 icon.go。
	// 前端直接 `img.src = node.icon`，一行判定逻辑都不在 JS 里。
	Icon string `json:"icon,omitempty"`

	// Mapped 表示这个节点在另一棵树里有对应项（匹配阶段建立了 MappedNode 绑定）。
	// 前端用它决定"这一行点了要不要去另一棵树定位"—— 见 LocateMapped。
	Mapped bool `json:"mapped,omitempty"`

	// Edited 表示这个节点的内容被用户在本会话里改过（见 tree.Node.SetContent）。
	//
	// 前端在名字后面加一个"已修改"小标记。刻意**不**复用 Type 来表达：
	// Type 是补丁匹配的语义（ADD/MOD/DEL），改内容跟"这个文件在补丁里的角色"
	// 是两回事，混用会让匹配结果被编辑操作污染。
	Edited bool `json:"edited,omitempty"`

	// Menu 是右键菜单的完整内容（两棵树都会填）。
	Menu []*MenuItemView `json:"menu,omitempty"`
}

// MenuItemView 是右键菜单里的一项。
//
// **整份菜单都由后端算好**，前端只做两件事：按顺序渲染、按 ID 分发动作。
//
// 为什么不像最早那样只给几个 bool：原版每个菜单项的显示条件都要看
// 【父级】【祖先链】【两个包是否打开】【自定义根的数量】【该根下有没有
// README】—— 前端拿不到这些（树是懒渲染的，父级可能还没建 DOM）。
// 而且文案本身也会变（主根显示"重新加载"、自定义根显示"刷新信息"），
// 单选类还带勾选态。这些只有后端算得了，规则放在 Go 里也才好写单测。
type MenuItemView struct {
	// ID 动作标识，前端据此分发；见 menuAction* 常量。
	ID string `json:"id"`
	// Label 已按当前语言翻译好的文案。
	Label string `json:"label"`
	// Radio 为真表示这是单选类菜单项（有勾选态）。
	Radio bool `json:"radio,omitempty"`
	// Checked 单选类是否处于选中态。
	Checked bool `json:"checked,omitempty"`
	// Disabled 置灰但仍在菜单里显示（对应 JavaFX 的 setDisable）。
	Disabled bool `json:"disabled,omitempty"`
	// Children 子菜单项，目前只有「复制」用到。
	Children []*MenuItemView `json:"children,omitempty"`
}

// 菜单动作标识。前端按这些字符串分发，别改字面量。
const (
	menuReload         = "reload"
	menuCancelMapped   = "cancelMapped"
	menuMarkRoot       = "markRoot"
	menuMarkReadme     = "markReadme"
	menuSaveFile       = "saveFile"
	menuSaveSourceFile = "saveSourceFile"
	menuCopy           = "copy"
	menuCopyName       = "copyName"
	menuCopyPath       = "copyPath"
	menuCopyRelative   = "copyRelativePath"
	menuCopyMd5        = "copyMd5"
	menuViewSign       = "viewSign"
	menuViewNodeMapped = "viewNodeMapped"
	menuOpenInExplorer = "openInExplorer"
	menuClosePatch     = "closePatch"
	menuFocusPatchTree = "focusPatchTree"

	// 应用树专有
	menuCloseAppPack = "closeAppPack"
	menuFocusAppTree = "focusAppTree"
	// menuDelete「删除」：把节点从应用树上物理摘掉，统计记 MANUAL_DEL。
	// menuMarkDelete「标记删除」：节点留在树上，只把状态设成 DEL。
	// 两者语义不同，别混 —— 见 appMenu 的说明与 MIGRATION.md。
	menuDelete     = "delete"
	menuMarkDelete = "markDelete"
	// menuResetManualDel「恢复原状」：清掉本包的删除记录、按原包重新加载。
	// 这是「删除」与「标记删除」唯一的反向操作 —— 物理删掉的节点已经从树上
	// 摘掉了，右键都找不到它，只能从这里整体撤销。
	menuResetManualDel = "resetManualDel"

	// 两棵树共有的新增项（原版没有）
	menuEditContent = "editContent"
)

// 聚焦树状态，对应 Java FocusTreeStatusConst。
const (
	focusTreeAll   = 0
	focusTreeApp   = 1
	focusTreePatch = 2
)

// 变更标记，对应 Java ChangeTypeEnum 的 code。
//
// 注意 MOD 是 '!' 不是 '*' —— 界面配色里那个黄色是另一回事，
// 文本标记沿用原版（AbsNodeMappedView 的绑定信息文本用它）。
const (
	changeCodeAdd = "+"
	changeCodeMod = "!"
	changeCodeDel = "-"
)

// PackStats 是单个包的统计。
type PackStats struct {
	Files     int   `json:"files"`
	Dirs      int   `json:"dirs"`
	Classes   int   `json:"classes"`
	Jars      int   `json:"jars"`
	TotalSize int64 `json:"totalSize"`
}

// PatchTotal 是补丁匹配的统计。
type PatchTotal struct {
	Add       int `json:"add"`
	Mod       int `json:"mod"`
	Del       int `json:"del"`
	ManualDel int `json:"manualDel"`
}

// State 是一次操作后回给前端的完整状态。
type State struct {
	AppPath    string     `json:"appPath"`
	PatchPath  string     `json:"patchPath"`
	AppTree    *NodeView  `json:"appTree"`
	PatchTree  *NodeView  `json:"patchTree"`
	AppStats   PackStats  `json:"appStats"`
	PatchStats PackStats  `json:"patchStats"`
	Total      PatchTotal `json:"total"`
	Settings   Settings   `json:"settings"`
	DiffInfo   string     `json:"diffInfo"`
	// ReadmeText 是补丁包说明文件的正文（多个自定义根时按根拼接）。
	//
	// 对应 Java PatchTreeInfo.getReadmeText()。原版把它显示在首页底部的
	// 「补丁信息」面板里（HomeView.updatePatchInfo）—— 光识别出 README
	// 不显示出来，用户是看不到的。
	ReadmeText string   `json:"readmeText"`
	Progress   []string `json:"progress"`
	// Matched 表示两个包都已打开、匹配流程已跑过。
	Matched bool `json:"matched"`
	// FocusTree 0 都显示 / 1 只看应用树 / 2 只看补丁树。
	FocusTree int `json:"focusTree"`
	// ManualDelList 是"临时补丁"里标记删除的文件清单。
	//
	// 为什么单开一个字段而不复用应用树的 DEL 节点：用户在应用树上标删一
	// 批文件后，希望在补丁树那侧一眼看到"这次要删哪些"——原版没有这个
	// 概念（它的删除补丁来自补丁包里的真实文件 + README 的 `-` 指令），
	// 这是 Go 版为"只开一个 jar 就想删文件"的用法加的。
	//
	// 每个元素带 Path 与 Name，前端渲染成列表，每行可点（定位到应用树节点）。
	ManualDelList []ManualDelItem `json:"manualDelList"`

	// ManualDelPending 表示磁盘上还留着本包的「删除 / 标记删除」记录 ——
	// 也就是"关掉重开还会被重放"的那份清单非空。
	//
	// 前端拿它决定「恢复原状」要不要出现：没有记录时点了也没意义。
	// 注意它与 ManualDelList 不等价 —— 物理删除（「删除」菜单）的节点已经
	// 从树上摘掉了，不在 ManualDelList 里，但记录还在、重开还会被重删。
	ManualDelPending bool `json:"manualDelPending,omitempty"`

	// MappedLinks 是"匹配连线"的两端清单 —— 前端在两棵树之间画曲线用。
	//
	// 由后端算好而不是前端自己配对：配对规则（只连文件、要用结构位置而不是
	// 节点 Path 定位）和 MappedNode 的语义都在 Go 侧，放前端会让同一件事
	// 有两份实现。设置里关掉连线时这里是空的。
	MappedLinks []MappedLink `json:"mappedLinks,omitempty"`
}

// MappedLink 是一条匹配连线：应用树上的一行 ←→ 补丁树上的一行。
//
// 两端用 **dom 路径** 而不是节点 Path 标识，原因是嵌套 jar 里的条目
// Path 不带 jar 前缀（上游行为，见 MIGRATION.md §五）——
// 两个嵌套 jar 里都有 `META-INF/MANIFEST.MF` 时按 Path 连会连到别家去。
// dom 路径是"从根往下第几个子节点"的链（`r`、`r/0/3`），结构上唯一，
// 前端渲染时也能原样算出来（见 app.js 的 makeRow）。
type MappedLink struct {
	// AppDom 应用树那一端的 dom 路径。
	AppDom string `json:"appDom"`
	// PatchDom 补丁树那一端的 dom 路径。
	PatchDom string `json:"patchDom"`
}

// ManualDelList entry kinds.
// ManualDelItem 是"临时补丁"里的一条标记删除记录。
type ManualDelItem struct {
	// Path 包内完整路径，与 tree.Node.Path 一致，用于定位。
	Path string `json:"path"`
	// Name 文件名（含后缀），用于展示。
	Name string `json:"name"`
	// Size 文件大小，展示用。
	Size int64 `json:"size"`
}

// Settings 是设置面板的形态，字段名与前端一致。
//
// 对应原版 view/config/ConfigView 的三段配置。原版一共 10 项，这里只有 8 项 ——
// 缺的「主题」「语言」「保存结果到源目录」「加载最后操作的补丁」四项：
// 前两个 Go 版没有第二种可选值（只有亮色 / 中文），后两个后端功能本身还没实现
// （`config.PatchConf` 里只有字段和默认值，全项目零使用点），摆上开关就是假的。
// 详见 MIGRATION.md。
type Settings struct {
	WithPath       bool   `json:"withPath"`
	WithName       bool   `json:"withName"`
	PatchFileRegex string `json:"patchFileRegex"`
	WritePatchSign bool   `json:"writePatchSign"`
	// PatchFileDiff 对应原版「开启补丁文件比对」。关掉后不再往「补丁信息」
	// 里追加"两边是同一个文件 / 应用比补丁新"那两条风险提示
	// （internal/service/apppack.go 的 CreatePatchDiffInfo）。
	PatchFileDiff bool `json:"patchFileDiff"`
	// HistoryLimit 对应原版「保留历史条数」，全局搜索历史的裁剪上限。
	HistoryLimit int    `json:"historyLimit"`
	Username     string `json:"username"`
	ReadmeFile   string `json:"readmeFile"`
	// ShowMappedLinks 对应设置里的「总是显示匹配连线」。
	// 关掉时连线跟随 PatchFileDiff —— 见 App.showMappedLinks 的说明。
	ShowMappedLinks bool `json:"showMappedLinks"`
}

// PatchResult 是一次"打补丁"的结果。
type PatchResult struct {
	Output   string     `json:"output"`
	Total    PatchTotal `json:"total"`
	DiffInfo string     `json:"diffInfo"`
}

// ---------------------------------------------------------------------------
// 设置
// ---------------------------------------------------------------------------

// GetSettings 返回当前设置。
func (a *App) GetSettings() Settings { return a.settingsView() }

// ApplySettings 更新设置。只影响后续操作，不改动已打开的两个包 ——
// 与 Java 版一致：改完设置需要重新触发一次匹配才能看到效果。
func (a *App) ApplySettings(s Settings) Settings {
	a.settings.PathMatch = s.WithPath
	a.settings.FileNameMatch = s.WithName
	a.settings.WritePatchSign = s.WritePatchSign
	a.settings.PatchFileDiff = s.PatchFileDiff
	a.settings.Username = s.Username
	a.settings.ReadmeFile = s.ReadmeFile
	// 连线开关是纯界面项，改完立刻反映到下一次取状态上（不需要重新匹配）。
	a.showMappedLinks = s.ShowMappedLinks
	if !util.IsBlank(s.PatchFileRegex) {
		a.settings.PatchFileRegex = s.PatchFileRegex
	}
	// 历史条数 <= 0 时回落到默认 30，与 SearchConf.AddHistory 自己的兜底
	// 保持一致 —— 这样"界面上显示的数"和"实际生效的数"永远是同一个。
	//
	// （原版这里是 `history.size() > historyLimit`，Integer 为 null 时拆箱
	// 直接 NPE，所以 Go 版选择兜住而不是照抄这个崩溃点。）
	if s.HistoryLimit > 0 {
		a.search.HistoryLimit = s.HistoryLimit
	} else {
		a.search.HistoryLimit = 30
	}
	return a.settingsView()
}

func (a *App) settingsView() Settings {
	return Settings{
		WithPath:       a.settings.PathMatch,
		WithName:       a.settings.FileNameMatch,
		PatchFileRegex: a.settings.PatchFileRegex,
		WritePatchSign: a.settings.WritePatchSign,
		PatchFileDiff:  a.settings.PatchFileDiff,
		HistoryLimit:   a.search.HistoryLimit,
		Username:       a.settings.EffectiveUsername(util.GetUserName()),
		ReadmeFile:     a.settings.ReadmeFile,

		ShowMappedLinks: a.showMappedLinks,
	}
}

// ---------------------------------------------------------------------------
// 打开包
// ---------------------------------------------------------------------------

// PickAppPack / PickPatchPack / PickSavePath 弹系统文件对话框。
//
// 放在 Go 侧而不是前端，是因为 WebView 的 <input type=file> 拿不到真实
// 文件系统路径（只给内容），而这个工具必须拿到路径去建树。
func (a *App) PickAppPack() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择应用包",
		Filters: []runtime.FileFilter{
			{DisplayName: "Java 包 (*.jar;*.zip)", Pattern: "*.jar;*.zip"},
		},
	})
}

// PickPatchPack 选择补丁包。
func (a *App) PickPatchPack() (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择补丁包",
		Filters: []runtime.FileFilter{
			{DisplayName: "补丁包 (*.zip;*.jar)", Pattern: "*.zip;*.jar"},
		},
	})
}

// PickSavePath 选择输出路径。
func (a *App) PickSavePath(defaultName string) (string, error) {
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存打过补丁的应用包",
		DefaultFilename: defaultName,
		Filters: []runtime.FileFilter{
			{DisplayName: "Java 包 (*.jar)", Pattern: "*.jar"},
		},
	})
}

// OpenAppPack 打开应用包并建树。
//
// 换应用包时清零统计 —— 对应 Java HomeLeftTreeView 里
// `APP_TREE_REFRESHING → totalInfo::cleanTotal` 那个处理器。
// 不清的话上一份包的计数会带到这一份上。
func (a *App) OpenAppPack(path string) (*State, error) {
	info, err := service.GetAppTreeInfo(path)
	if err != nil {
		return nil, fmt.Errorf("读取应用包失败: %w", err)
	}
	a.appTreeInfo = info
	a.appPath = path
	a.total.CleanTotal()
	// 上次的删除/标删记录在这里重应用 —— 这就是"关掉重开还在"的落点。
	// 放在 refresh() 之前：新树刚建好、补丁还没匹配，节点状态干净，
	// 正是复现删除动作的时机。
	a.reapplyManualDel()
	return a.refresh()
}

// OpenPatchPack 打开补丁包并建树。
//
// **换包前要先把上一份补丁留下的映射清干净** —— 对应 Java
// refreshPatchTree 开头那句 `cleanPatchMappedNode(oldTreeInfo.getRootNode())`。
// 不清的话：旧补丁在应用树上留下的 VirtualNode/MappedNode 会一直挂着，
// 而统计对象是整个会话复用的（见 App.total），新补丁再映射一遍，
// 计数就会叠加。用「重新加载」刷新同一个包时这条同样是必需的。
func (a *App) OpenPatchPack(path string) (*State, error) {
	if a.patchTreeInfo != nil {
		if root := a.patchTreeInfo.RootNode(); root != nil {
			service.CleanMappedNode(a.total, root, false)
		}
	}
	info, err := service.GetPatchTreeNode(path, a.step())
	if err != nil {
		return nil, fmt.Errorf("读取补丁包失败: %w", err)
	}
	a.patchTreeInfo = info
	a.patchPath = path
	return a.refresh()
}

// OpenPack 按扩展名自动判断打开的是应用包还是补丁包 —— 对应界面上的
// "打开"按钮：用户不必先想清楚这个文件属于哪一边。
//
// 判断规则：一侧还空着就填那一侧；两侧都空时按扩展名（.jar → 应用包，
// 其余 → 补丁包）；两侧都满了就覆盖应用包一侧。
func (a *App) OpenPack(path string) (*State, error) {
	if a.appTreeInfo == nil && a.patchTreeInfo == nil {
		if strings.EqualFold(util.Ext(path), constant.ExtJar) {
			return a.OpenAppPack(path)
		}
		return a.OpenPatchPack(path)
	}
	if a.appTreeInfo == nil {
		return a.OpenAppPack(path)
	}
	if a.patchTreeInfo == nil {
		return a.OpenPatchPack(path)
	}
	return a.OpenAppPack(path)
}

// ---------------------------------------------------------------------------
// 拖放
// ---------------------------------------------------------------------------

// DropSide 标识拖入落在了哪一侧面板。
type DropSide string

const (
	DropSideApp   DropSide = "app"
	DropSidePatch DropSide = "patch"
)

// acceptAsAppPack 判断左侧（应用包）是否接受这次拖入。
//
// 逐字对齐 Java HomeLeftTreeView.onDragOver：
//
//	if (files.size() == 1 && files.get(0).getName().endsWith(FileExtConst.DOT_JAR))
//
// 注意**区分大小写** —— Java 用的是 String.endsWith，`APP.JAR` 不算。
// 这是上游行为，不要"顺手"改成大小写不敏感。
func acceptAsAppPack(paths []string) bool {
	return len(paths) == 1 && strings.HasSuffix(paths[0], constant.DotJar)
}

// acceptAsPatchPack 判断右侧（补丁包）是否接受这次拖入。
//
// 对齐 Java HomeRightTreeView.onDragOver：
//
//	files.size() == 1 && (files.get(0).isDirectory()
//	                      || FileDecompressor.isCompressFile(files.get(0).getName()))
//
// isCompressFile 的判定就是后缀 `.jar` / `.zip`（见 FileDecompressor.doGetInstance），
// 同样区分大小写。
func acceptAsPatchPack(paths []string) bool {
	if len(paths) != 1 {
		return false
	}
	if isDirPath(paths[0]) {
		return true
	}
	return strings.HasSuffix(paths[0], constant.DotJar) ||
		strings.HasSuffix(paths[0], constant.DotZip)
}

// isPackExt 判断路径是不是"整包"后缀（.jar / .zip）。
//
// 拖放路由用：落在文件行上时，只有"非整包"的普通文件才走"替换节点内容"，
// .jar / .zip 哪怕落在行上也按"打开包"处理（拖到具体文件上换一个 jar 进来
// 没有意义，用户多半是想打开它）。
func isPackExt(p string) bool {
	return strings.HasSuffix(p, constant.DotJar) || strings.HasSuffix(p, constant.DotZip)
}

// isDirPath 对应 Java 的 File.isDirectory()：路径不存在或读不到就是 false。
func isDirPath(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// HandleFileDrop 处理"把文件拖进窗口"。
//
// side 由前端做几何命中判定（document.elementFromPoint 找落点所在的面板），
// Go 侧只负责**是否接受**与**往哪边打开** —— 判定规则留在 Go 里，可测。
//
// 为什么不做成 Go 侧的 runtime.OnFileDrop：那样只能拿到落点坐标 x/y，
// 还得自己换算成"落在哪个面板"，等于把布局知识搬进后端。让前端用浏览器的
// 命中测试回答"落在哪一侧"，后端只回答"这一侧收不收"，职责更清。
//
// 它把一次拖放分流成四种语义（前三/四条里除"打开包"外都是 Go 版新增）：
//
//  1. **应用侧**、落在**具体行**上 → 按"包内文件"处理，**哪怕拖进来的是 .jar / .zip**：
//     目标目录 = 落在目录行上就是那一行、落在文件行上是它的**父目录**
//     （原版 `appNode.isDir() ? appNode : appNode.getParent()`），
//     然后在那个目录里**按名字**找同名子项：命中 → 覆盖它的内容；没命中 → 新增。
//  2. 拖的是**普通文件**、落在**补丁侧**（行上或空白处）→ **按文件名去应用树里找
//     同名文件**：找到 1 个直接替换；找到多个返回候选让用户选；一个都没有就回
//     matchNone。这就是"我忘了路径、但手里有这个文件"的场景 —— 不需要用户记得
//     它在哪个目录，工具按文件名反查。
//  3. 拖的是**普通文件**、落在**应用侧空白处** → addOrReplaceChild：加到根目录里。
//  4. 拖的是整包（.jar / .zip / 目录）→ HandleFileDrop（原 Java 的"打开包"语义）。
//     注意这条在补丁侧**优先于**落点：补丁树上拖整包永远是"打开补丁包"。
//
// **为什么应用侧的"落在行上"要排在"整包"之前**：这是原版的分工 ——
// 行上的拖放归 FileTreeCell（`TreeNodeFileMatchProcessor`，jar 与普通文件一视同仁，
// 按名字匹配/新增），只有空白处才归 HomeLeftTreeView（`refreshAppTree`，打开整包）。
// Go 版早期把 `isPackExt` 提到最前面，于是"把 jar 拖到 BOOT-INF/lib 上"会**换掉整个
// 应用包**而不是把它加进去 —— 用户 2026-10-26 报的"左侧 jar 包不支持其他 jar 包直接
// 拖入，应该和文件一样"就是这件事。顺序反过来之后两边都对：想加包内文件就拖到行上，
// 想换应用包就拖到空白处。
//
// **落在文件行上也是"按名字匹配"，不是"就地替换这一行"**：原版
// `TreeNodeMatchProcessor.exec` 一律先算 `tarAppNode`（文件行取父目录）再 `match`，
// 而 `match` 是遍历该目录的子项逐个 `matchPatchName`。所以把 `B.jar` 拖到 `A.jar`
// 那一行，结果是在 `A.jar` 所在的目录里找 `B.jar`：找到就覆盖 B，没有就新增 B ——
// **A 一点都不动**。早期 Go 版在这里直接 `ReplaceNodeContent(rowPath)`，
// 会把这行节点的内容换成 B 的、名字还留着 A，属于静默改错文件。
//
// 落点由前端用 elementFromPoint 判定，把 rowPath 与 rowIsDir 一起传过来：
// rowPath 为空表示落在空白处；rowIsDir 表示落在目录行上。
// 这样前端只管几何，收不收 / 加还是换 / 要不要挑候选 的规则全在 Go 侧、可单测。
func (a *App) HandleDrop(side string, rowPath string, rowIsDir bool, paths []string) (*DropResult, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("没有拿到文件路径")
	}
	src := paths[0]

	// ① **应用侧** + 落在具体行上 → 按"包内文件"处理（与普通文件完全同一条路）。
	//
	// 只限应用侧：原版里补丁树的单元格**根本不接拖放**
	// （FileTreeCell.onDragOver 有一句 `getTreeView() != appTree` 就 return），
	// 落在补丁树上会一路冒泡到 HomeRightTreeView，整包照样是"打开补丁包"。
	// 所以这里不能用"落在行上"一刀切，否则拖个 jar 到补丁树上会变成"往补丁树里
	// 塞一个文件"。
	//
	// 目录也不走这条：一次拖一个目录到目录行上，原版是 `matchWithDir`（把目录里的
	// 子项逐个并进去），Go 版没实现那一套，保持原来的"按整包/目录处理"。
	if side == treeSideApp && !a.isRootRow(side, rowPath) && !isDirPath(src) {
		// 目标目录：目录行就是它自己，文件行退回它的**父目录**
		// （原版 `appNode.isDir() ? appNode : appNode.getParent()`）。
		//
		// 也就是说落在文件行上**不会**"就地替换这一行"，而是到它所在目录里
		// 按名字匹配 —— 把 B.jar 拖到 A.jar 行上，结果是覆盖/新增 B，A 不动。
		// 详见函数头那段说明。
		dir := rowPath
		if !rowIsDir {
			dir = parentLoc(rowPath)
		}
		return a.addDropFilesInto(side, dir, paths)
	}

	// ② 落在空白处 + 整包 / 目录 → 按"打开包"处理。
	if isPackExt(src) || isDirPath(src) {
		s, err := a.HandleFileDrop(side, paths)
		if err != nil {
			return nil, err
		}
		return &DropResult{Action: dropActionOpen, State: s}, nil
	}

	// ③ 普通文件 + 补丁侧空白处 → 按文件名反查应用树（"忘了路径"的入口）。
	// **一次拖多个要每个都查**：用户就是"手里一堆文件、不知道都在哪"才走这条路，
	// 只认第一个等于默默把其余的丢掉（用户当场发现：拖两个只出现一个）。
	if side == treeSidePatch {
		return a.matchLooseFilesIntoApp(paths)
	}

	// ④ 普通文件 + 应用侧空白处 → 加到根目录（拖几个就逐个加）。
	return a.addDropFilesInto(side, "", paths)
}

// isRootRow 判断 rowPath 是不是"根行"（那一行显示的就是包本身）。
//
// 前端会把根行也当普通行传过来（`renderTree` 渲染的就是 `state.appTree` 这个
// 根节点），而根节点的 Path 是**磁盘路径**（`D:\...\app.jar`），不是包内路径 ——
// 直接拿去 resolveNode 只会得到"树里找不到节点"。语义上根行就是整棵树，所以
// 按"空白处"处理：拖整包上来 = 换这个包，拖普通文件 = 加到根目录。
//
// rowPath 为空也算根（前端在空白处传的就是空串）。
func (a *App) isRootRow(side, rowPath string) bool {
	if rowPath == "" {
		return true
	}
	root, _, err := a.treeRoot(side)
	return err == nil && root != nil && root.Path == rowPath
}

// DropPatchNode 把补丁树上的节点拖到应用树的某个落点上（补丁树 → 应用树）。
//
// 对应原版 FileTreeCell 的 dragInfo 分支（TreeNodeTreeMatchProcessor）。
// 方向是**单向**的：原版 onDragOver 里 `dragInfo.getOriginTree() == getTreeView()`
// 直接 return，同树内拖动被明确禁止；`getTreeView() != appTree` 又要求落点
// 必须是应用树。所以只有"补丁 → 应用"这一条路。
//
// 与 HandleDrop 的分工：那个处理"从资源管理器拖文件进来"（原生通道，带真实
// 磁盘路径），这个处理"窗口内从补丁树拖节点"（HTML5 通道，带的是包内路径）。
// 两者最后都落到"按名字匹配进某个目录"这套规则上。
func (a *App) DropPatchNode(appRowPath string, appRowIsDir bool, patchPaths []string) (*State, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包")
	}
	if a.patchTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开补丁包")
	}
	if len(patchPaths) == 0 {
		return nil, fmt.Errorf("没有拿到要拖动的补丁节点")
	}

	// 落点：根行/空白处都算"整棵树的根"，其余按定位串解析。
	target := a.appTreeInfo.RootNode()
	if !a.isRootRow(treeSideApp, appRowPath) {
		n, err := a.resolveNode(treeSideApp, appRowPath)
		if err != nil {
			return nil, err
		}
		if appRowIsDir && !n.Dir() {
			return nil, fmt.Errorf("目标不是目录: %s", appRowPath)
		}
		target = n
	}

	nodes := make([]*tree.Node, 0, len(patchPaths))
	for _, p := range patchPaths {
		// 根行：那一行显示的就是补丁包自己（补丁包本身是个 .jar 时，树上
		// **唯一**那一行就是它）。语义是"把整包套到应用树上"，所以直接把根
		// 节点交下去，由 ApplyPatchNodesToApp 去并它的顶层条目。
		if a.isRootRow(treeSidePatch, p) {
			nodes = append(nodes, a.patchTreeInfo.RootNode())
			continue
		}
		n, err := a.resolveNode(treeSidePatch, p)
		if err != nil {
			return nil, err
		}
		// 说明文件是"补丁说明书"，不是要打进包里的内容；原版同样把它挡在
		// 拖拽之外（dragInfo.isHasReadmeNode() 时整次拖放被拒）。
		if n.Type == constant.TreeNodeTypeReadme {
			return nil, fmt.Errorf("说明文件不能拖进应用树: %s", n.Name)
		}
		nodes = append(nodes, n)
	}

	service.ApplyPatchNodesToApp(a.total, a.appTreeInfo, target, nodes, a.patchOptions(a.step()))
	return a.state(), nil
}

// addDropFilesInto 把一批文件逐个"按名字加进 / 覆盖"某个目录。
//
// dirPath 为空表示根目录。每次拖放的"代表文件"取 paths[0]（提示文案、md5 用），
// 但**每个文件都会真的被加进去** —— 早期只处理 paths[0]，拖两个只出现一个。
//
// 动作：拖**一个**文件时如实区分"覆盖 / 新增 / 内容相同"（前端提示文案不一样）；
// 拖多个时统一报 add 并用 Count 说话 —— 混着覆盖和新增时逐条播报反而更乱，
// 但"一个都没改"（全同名同内容）仍然报 same，否则会说"已加入 N 个文件"却
// 什么都没加。
func (a *App) addDropFilesInto(side, dirPath string, paths []string) (*DropResult, error) {
	var out *addOutcome
	// 一次拖放内同一个文件的 md5 只读一遍：判断"内容是否相同"和给右侧面板填
	// SrcMd5 都要它，而胖包动辄上百 MB，算两遍就是两倍 IO。
	memo := md5Memo{}
	allSame := true
	for _, p := range paths {
		o, err := a.addOrReplaceChild(side, dirPath, dropFileName(p), p, memo)
		if err != nil {
			return nil, err
		}
		if !o.Same {
			allSame = false
		}
		out = o
	}
	src := paths[0]
	action := dropActionAdd
	switch {
	case len(paths) == 1 && out.Same:
		action = dropActionSame
	case len(paths) == 1 && out.Replaced:
		action = dropActionReplace
	case allSame:
		action = dropActionSame
	}
	return &DropResult{Action: action, State: out.State, Path: out.Loc,
		FileName: dropFileName(src), SrcPath: src, SrcSize: fileSize(src),
		SrcMd5: memo.of(src), Count: len(paths)}, nil
}

// md5Memo 记住一次拖放里算过的 md5 —— 同一个文件只读一遍。
type md5Memo map[string]string

func (m md5Memo) of(p string) string {
	if s, ok := m[p]; ok {
		return s
	}
	s := fileMd5(p)
	m[p] = s
	return s
}

// sameContent 判断节点**当前内容**与 srcMd5 是否完全一致。
//
// 任一侧拿不到 md5 就返回 false —— 宁可多替换一次，也不能因为"算不出来"
// 就把用户手里的文件丢掉。
func sameContent(n *tree.Node, srcMd5 string) bool {
	if n == nil || n.Dir() || srcMd5 == "" {
		return false
	}
	have := n.GetMd5OrInit()
	return have != "" && have == srcMd5
}

// 拖放结果的动作。前端按它决定弹什么提示 / 要不要让用户挑候选。
const (
	// dropActionOpen 打开了一个包。
	dropActionOpen = "open"
	// dropActionReplace 替换了某个节点的内容（Path 是那个节点）。
	dropActionReplace = "replace"
	// dropActionAdd 新增了某个节点（Path 是新增后的路径）。
	dropActionAdd = "add"
	// dropActionSame 同名且内容完全一致，**什么都没做**（Path 是那个同名节点）。
	//
	// 为什么单列一个动作而不是报 replace：替换成同一份内容在语义上是空操作，
	// 但 ReplaceNodeContent 会无条件 SetEdited(true) —— 界面上打出「已修改」
	// 星号、md5 缓存也被清空重算，用户以为改了，实际字节一个没变，保存出来
	// 也看不出差别。假改动比"少一个提示"更难受，所以这里直接跳过。
	dropActionSame = "same"
	// dropActionMatchMany 找到多个同名文件，需要前端让用户挑一个。
	dropActionMatchMany = "matchMany"
	// dropActionMatchNone 应用包里没有同名文件。
	dropActionMatchNone = "matchNone"
)

// MatchCandidate 是"按文件名反查"命中的一个应用侧文件。
type MatchCandidate struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Md5 是这一项**当前内容**的 md5，给前端在替换前和拖入文件的 srcMd5 比一眼。
	//
	// 为什么后端不直接比：这一步的语义是"让用户看过再决定"，比较结果要能显示在
	// 清单上（用户还没点，就该看到"这一处内容一样、不用换"）。两侧 md5 都是现成的
	// —— 候选行本来就显示 md5（`toView` 里 `GetMd5OrInit`），拖入那条带着 srcMd5。
	// 占位节点（树上同名、包里没有）留空：它压根没有内容可比。
	Md5 string `json:"md5,omitempty"`
}

// DropMatch 是"拖进来的某一个文件"的匹配结果。
//
// 为什么按文件分组而不是摊平成一个大列表：一次拖五个文件时，用户需要知道
// **哪一个**没找到、**哪一个**有 3 处同名 —— 摊平之后这一层信息就没了；
// 而且替换时必须用"那一行所属的那个文件"的 srcPath，不能全用第一个。
type DropMatch struct {
	// FileName 拖入的文件名。
	FileName string `json:"fileName"`
	// SrcPath 这个文件的磁盘路径（前端选中它名下某一行后，用它去替换）。
	SrcPath string `json:"srcPath"`
	// SrcSize 这个文件的字节数（拿不到时为 0）。
	SrcSize int64 `json:"srcSize,omitempty"`
	// SrcMd5 这个文件的 md5（拿不到时空串）。
	//
	// 为什么拖个外部文件也要给 md5：包内每个文件行都并排显示着"修改时间 /
	// 大小 / MD5"（对应原版 FileTreeCell 那一行 `dateTime + sizeReadability +
	// md5`），用户早就习惯拿 MD5 判断"这份内容对不对"。右侧「拖入的文件」条
	// 原先只有名字和体积，于是拖完没法跟左边目标文件比对 —— 得靠文件名猜，
	// 而文件名恰恰是最不可靠的线索。补上这一列，拖进来当场就能和左边那一行
	// 的 MD5 对上眼。
	SrcMd5 string `json:"srcMd5,omitempty"`
	// Candidates 包里**真实存在**的同名文件，可以替换。
	Candidates []MatchCandidate `json:"candidates,omitempty"`
	// Placeholders 树上同名、但**包里没有**的占位节点（见下面 DropResult 的说明）。
	Placeholders []MatchCandidate `json:"placeholders,omitempty"`
	// ScanNote 这个文件没找到时，到底查了多少东西。
	ScanNote string `json:"scanNote,omitempty"`
}

// DropResult 是一次拖放的结果。
//
// 为什么不再直接回 State：有些拖放需要**先问一句**（同名文件找到多个时让用户挑），
// 这时候还没有新状态可回。用 Action 把"做了什么 / 还要做什么"说清楚，
// 前端不必自己猜。
type DropResult struct {
	Action string `json:"action"`
	// State 已发生的状态变化；需要用户挑候选时为空。
	State *State `json:"state,omitempty"`
	// Path 被替换 / 新增的节点路径（Action 为 replace / add 时有值）。
	Path string `json:"path,omitempty"`
	// FileName 拖入的第一个文件名（右侧「拖入的文件」条要显示）。
	FileName string `json:"fileName,omitempty"`
	// SrcPath 拖入的第一个文件的磁盘路径。
	SrcPath string `json:"srcPath,omitempty"`
	// SrcSize 拖入的第一个文件的字节数（拿不到时为 0）。
	// 前端把它和目标文件的大小并排显示 —— "拖入 150 KB / 目标 147 KB"
	// 是判断"版本对不对"最快的线索，比看文件名可靠。
	SrcSize int64 `json:"srcSize,omitempty"`
	// SrcMd5 拖入的第一个文件的 md5（拿不到时为空）。
	// 与 SrcSize 同一个用途、更可靠：MD5 相同就是同一份内容。
	SrcMd5 string `json:"srcMd5,omitempty"`
	// Count 这次实际处理的文件数（replace 恒为 1，add 可能多个）。
	Count int `json:"count,omitempty"`
	// Items 每个拖入文件的匹配结果（Action 为 matchMany / matchNone 时有值），
	// 顺序与拖入顺序一致。
	//
	// 这里承载原先摊平的 Candidates / Placeholders / ScanNote。之所以改成按文件
	// 分组，是因为一次拖多个时那三个字段只能表达"第一个文件"的情况 ——
	// 用户拖两个、界面只显示一个，剩下那个被默默吃掉（真实报障）。
	Items []DropMatch `json:"items,omitempty"`
}

// FindAppCandidates 在应用包里按文件名找文件（大小写不敏感）。
//
// 用途是"忘了路径"：用户手里有一个 Foo.class，但不知道它在 jar 的哪个目录。
// 按文件名反查，把命中的路径列出来让他选。
//
// **会往嵌套 jar 里搜。** Spring Boot 胖包里绝大多数业务类在
// `BOOT-INF/lib/*.jar` 里，那些 jar 默认是折叠的；不进去搜就等于搜不到
// （第一版就踩了这个坑：用户拖一个明明在包里的 class 却提示"没有同名文件"）。
//
// 搜的方式是**只读地**把 jar 读一遍、看条目名，**不把它挂到树上** ——
// 一旦给 jar 节点建了子级，写包时就会认为"这个 jar 内部改过"而重打包它
// （见 writer 的 writeTreeNode2PackWithJar：有子级就走临时文件重打包）。
// 为了搜个名字把所有 jar 都重打一遍，代价太大。真正替换时（resolveNode）
// 才展开**命中的那一个**。
func (a *App) FindAppCandidates(name string) ([]MatchCandidate, error) {
	res, err := a.findAppCandidates(name)
	return res.cands, err
}

// findResult 是一次反查的全部产出。
type findResult struct {
	// cands 包里**真实存在**的同名文件，可以替换。
	cands []MatchCandidate
	// placeholders 树上同名、但**包里没有**的占位节点（见下）。
	placeholders []MatchCandidate
	// note 扫了多少东西（只在 cands 为空时有意义）。
	note string
}

// findAppCandidates 是两遍式搜索，第二遍的统计量一并返回（用于"没找到"时如实汇报）。
//
// **为什么要两遍。** 内存树只反映"打开那一刻 + 用户展开过的部分"：折叠着没展开
// 的目录、被裁剪过的子树，都不在里面。拿它当全集去回答"包里有没有这个文件"，
// 就会给出假否定 —— 而这正是这个功能唯一的输出。
//
// 所以：
//
//	第一遍  走内存树。**用户改过的内容只存在于这里**（拖进来的文件、替换过
//	        的内容磁盘上没有），所以必须查；但它不碰磁盘，很快。
//	        没展开的嵌套 jar 一律跳过，交给第二遍 —— 同一个 jar 不读两次。
//	第二遍  直接从磁盘上的原包扫一遍（只看中央目录，不解压），把所有层级的
//	        条目名过一遍；嵌套 jar（含 jar 套 jar）再进去扫。
//
// 定位串仍是 `BOOT-INF/lib/x.jar!/com/foo/Bar.class`，两遍产出的串一致，
// 靠 seen 去重 —— 同一处命中只会出现一次，且内存树的那份优先（内容更新）。
//
// **还有第三种"树上看得见、包里没有"的东西：MappedNode 占位节点。**
// 补丁 README 里写了目标路径，而应用包里那个路径不存在、指令又是 ADD 时，
// 匹配阶段会沿路径造一串 VirtualNode 目录 + 一个 MappedNode 文件（见
// service.doMatchMappedNodeChildrenWithReadme）。它在树上有名字、有大小、
// 还连着映射线，看起来和真文件一模一样 —— 但它**不是包里的文件**，
// 是"这次补丁要新增的文件"。用户照着树去找、却搜不到，就是这么来的
// （真实报障：CallMethodInvokerAbstract.class）。
// 所以搜不到时把这类节点单独挑出来，让提示能说清"它是什么"。
func (a *App) findAppCandidates(name string) (findResult, error) {
	m, err := a.findAppCandidatesFor([]string{name})
	if err != nil {
		return findResult{}, err
	}
	return m[strings.ToLower(strings.TrimSpace(name))], nil
}

// findAppCandidatesFor 一次回答**多个**文件名的反查。
//
// 为什么不是"循环调 findAppCandidates"：第二遍是扫整个磁盘上的包（几百 MB、
// 上千个嵌套 jar，单次约 0.4 秒），拖 5 个文件就扫 5 遍纯属浪费 —— 而扫一遍
// 时把 5 个名字一起比对，代价和查 1 个几乎一样。返回值的 key 是小写文件名。
func (a *App) findAppCandidatesFor(names []string) (map[string]findResult, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包，先打开要打补丁的 jar")
	}
	// 文件名比对本来就大小写不敏感（Windows 习惯），统一按小写收口。
	want := make(map[string]bool, len(names))
	for _, n := range names {
		if util.IsBlank(n) {
			continue
		}
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("文件名为空")
	}

	out := make(map[string]findResult, len(want))
	seen := make(map[string]map[string]bool, len(want)) // name -> loc -> 已收
	add := func(name, loc string, n *tree.Node) {
		if loc == "" {
			return
		}
		k := strings.ToLower(name)
		m := seen[k]
		if m == nil {
			m = make(map[string]bool, 4)
			seen[k] = m
		}
		if m[loc] {
			return
		}
		m[loc] = true
		r := out[k]
		r.cands = append(r.cands, MatchCandidate{Path: loc, Name: n.Name, Size: n.Size,
			Md5: n.GetMd5OrInit()})
		out[k] = r
	}

	// —— 第一遍：内存树（不碰磁盘）——
	// covered 记下"整棵子树都已经在内存树里走完了"的 jar，第二遍就不用再读它们。
	// 注意要**递归地**确认覆盖：一个展开过的 jar 里若还夹着没展开的嵌套 jar，
	// 那它就不算覆盖完 —— 否则第二遍会跳过它，那个内层 jar 就谁也查不到。
	covered := make(map[string]bool, 8)
	var walkMem func(n *tree.Node, prefix string) bool
	walkMem = func(n *tree.Node, prefix string) bool {
		if n == nil {
			return true
		}
		// KindMapped 要排除：它是"应用侧本不存在的文件，虚拟绑定到补丁节点"，
		// 长得和真文件一样（有名字、有大小、还连着映射线），但包里没有它。
		// 把它当候选会给出一个"替换了就写进新包"的假象，其实是凭空多出一个
		// 包里从来没有的文件 —— 用户以为是替换，实际是新增。这类节点单独
		// 由 findMappedPlaceholders 收，只用来解释"为什么树上看得见"。
		if !n.Dir() && !isCompressNode(n) && n.Kind != tree.KindMapped &&
			want[strings.ToLower(n.Name)] {
			add(n.Name, joinLoc(prefix, n.Path), n)
		}
		if isCompressNode(n) {
			if n.Children == nil {
				return false // 没展开：这一块内存树里没有，交给第二遍
			}
			loc := joinLoc(prefix, n.Path)
			full := true
			for _, c := range n.Children {
				if !walkMem(c, loc) {
					full = false
				}
			}
			if full {
				covered[loc] = true
			}
			return full
		}
		full := true
		for _, c := range n.Children {
			if !walkMem(c, prefix) {
				full = false
			}
		}
		return full
	}
	// 从根的直接子级开始走：根节点本身是"整个包"这一层（它的 Name/Path 是
	// 磁盘上的 jar 路径），把它当嵌套 jar 会给所有命中都套上 `xxx.jar!/` 前缀。
	for _, c := range a.appTreeInfo.RootNode().Children {
		walkMem(c, "")
	}

	// —— 第二遍：磁盘上的原包 ——
	// **无论第一遍有没有结果都要跑。** 第一遍只看得到"已加载"的部分，
	// 少一个候选和"一个都没有"一样糟：清单少一项，用户就会以为只有那一处。
	st := a.scanAppArchive(want, covered, add)
	note := fmt.Sprintf("已检查 %d 个文件、%d 个嵌套 jar", st.files, st.jars)
	if st.bad > 0 {
		// 用"压缩包"而不是"jar"：实测踩到过一个叫 *.zip 的普通资源文件，
		// 按扩展名会被当压缩包试着打开、然后失败。说成"jar 打不开"会吓人。
		note += fmt.Sprintf("（%d 个压缩包打不开，已跳过）", st.bad)
	}

	// 一个真文件都没找到时，再查一遍"占位节点"：它们长得和真文件一样，
	// 但只是补丁按 README 造出来的目标位（包里原本没有这个文件）。
	// 有真命中就不必查了 —— 那时候占位节点的存在与否不影响用户操作。
	for k := range want {
		r := out[k]
		r.note = note
		if len(r.cands) == 0 {
			r.placeholders = a.findMappedPlaceholders(k)
		}
		out[k] = r
	}
	return out, nil
}

// findMappedPlaceholders 找出树上"同名但包里没有"的占位节点。
//
// 只认 KindMapped 且非目录：那是"应用侧本不存在的文件，虚拟绑定到补丁节点"
// （见 tree.Kind 的注释）。中间的 VirtualNode 是目录，用户不会拿文件去对。
func (a *App) findMappedPlaceholders(name string) []MatchCandidate {
	var out []MatchCandidate
	seen := make(map[string]bool, 2)
	var walk func(n *tree.Node)
	walk = func(n *tree.Node) {
		if n == nil {
			return
		}
		if !n.Dir() && n.Kind == tree.KindMapped && strings.EqualFold(n.Name, name) {
			if !seen[n.Path] {
				seen[n.Path] = true
				out = append(out, MatchCandidate{Path: n.Path, Name: n.Name, Size: n.Size})
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if a.appTreeInfo != nil {
		walk(a.appTreeInfo.RootNode())
	}
	return out
}

// scanStats 是第二遍"到底看了多少东西"的统计，用于把"没找到"讲清楚。
type scanStats struct {
	files int // 扫过的文件条目（含嵌套 jar 内部）
	jars  int // 读成功的嵌套 jar
	bad   int // 打不开的嵌套 jar（损坏 / 加密 / zip64 不兼容）
}

// maxNestedJarDepth 限制"jar 套 jar"往下钻的层数。
// 正常胖包只有一层（BOOT-INF/lib/*.jar），留三层是为了兜住
// "业务 jar 里再塞 lib/*.jar"这种打包方式，同时防病态包无限递归。
const maxNestedJarDepth = 3

// scanAppArchive 直接扫磁盘上的应用包，不看内存树。
//
// 用 pack.BuildZipTree 而不是 BuildZipTreeFromReader：前者只读中央目录
// （惰性、不解压），后者会把整个包 io.ReadAll 进内存 —— 对几百 MB 的
// 胖包是不可接受的。嵌套 jar 只能整个解出来（zip 的随机访问要求 ReaderAt），
// 但一次只持有一个，用完即弃。
func (a *App) scanAppArchive(want map[string]bool, covered map[string]bool, add func(string, string, *tree.Node)) scanStats {
	var st scanStats
	if util.IsBlank(a.appPath) {
		return st
	}
	f, err := os.Open(a.appPath)
	if err != nil {
		return st
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return st
	}
	root, err := pack.BuildZipTree(a.appPath, f, fi.Size(), false)
	if err != nil {
		return st
	}

	var walk func(n *tree.Node, prefix string, depth int)
	walk = func(n *tree.Node, prefix string, depth int) {
		if n == nil {
			return
		}
		if !n.Dir() && !isCompressNode(n) {
			st.files++
			if want[strings.ToLower(n.Name)] {
				add(n.Name, joinLoc(prefix, n.Path), n)
			}
		}
		if isCompressNode(n) {
			loc := joinLoc(prefix, n.Path)
			if covered[loc] || depth >= maxNestedJarDepth {
				return // 第一遍已经查过 / 钻得太深
			}
			sub, err := readZipTree(n)
			if err != nil || sub == nil {
				st.bad++
				return
			}
			st.jars++
			for _, c := range sub.Children {
				walk(c, loc, depth+1)
			}
			return
		}
		for _, c := range n.Children {
			walk(c, prefix, depth)
		}
	}
	for _, c := range root.Children {
		walk(c, "", 0)
	}
	return st
}

// readZipTree 只读地把一个压缩包节点解成临时树（不挂到原树上）。
//
// 与 service.BuildChildrenWithCompress 的区别：那个会**改**节点（挂子级），
// 这里只借它的解码逻辑看一眼条目名。用完即弃。
func readZipTree(n *tree.Node) (*tree.Node, error) {
	src := n.Source()
	if src == nil {
		return nil, fmt.Errorf("%s 没有内容来源", n.Name)
	}
	rc, err := src.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return pack.BuildZipTreeFromReader(n.Name, rc, n.IsPatch)
}

// joinLoc 拼出"嵌套定位串"：`BOOT-INF/lib/x.jar!/com/foo/Bar.class`。
//
// 为什么要单独造一个定位串：嵌套 jar 里的条目，Node.Path 是**相对**的
// （不带 jar 前缀），全树范围内会重名。用 `jar!/entry` 才能唯一指向一个条目。
// 前端只把它当字符串显示/回传，不解析。
func joinLoc(prefix, inner string) string {
	if prefix == "" {
		return inner
	}
	return prefix + "!/" + inner
}

// splitLoc 拆开嵌套定位串；不是定位串时 ok=false。
func splitLoc(loc string) (jarPath, inner string, ok bool) {
	if i := strings.Index(loc, "!/"); i >= 0 {
		return loc[:i], loc[i+2:], true
	}
	return "", "", false
}

// resolveNode 按"普通路径"或"嵌套定位串"找到节点。
//
// 嵌套定位串会**按需展开**那个 jar（只有它会被重打包，别的 jar 不受影响）。
func (a *App) resolveNode(side, loc string) (*tree.Node, error) {
	root, isPatch, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	return resolveIn(root, loc, isPatch)
}

// resolveIn 在一棵（子）树里解析定位串，**支持 jar 套 jar 的多级定位串**。
//
// 递归而不是只拆一层：搜索侧（scanAppArchive）能钻到
// `a.jar!/lib/b.jar!/com/foo/Bar.class`，替换侧就必须能解析同样的串 ——
// 否则会出现"清单里点得到、一点替换就报找不到节点"的半截功能。
// 正常胖包只有一层（BOOT-INF/lib/*.jar），多级是为了兜住
// "业务 jar 里再塞 lib/*.jar" 这种打包方式。
func resolveIn(root *tree.Node, loc string, isPatch bool) (*tree.Node, error) {
	jarPath, inner, ok := splitLoc(loc)
	if !ok {
		n := findTreeNodeByPathLoose(root, loc)
		if n == nil {
			return nil, fmt.Errorf("树里找不到节点: %s", loc)
		}
		return n, nil
	}
	jarNode := findTreeNodeByPath(root, jarPath)
	if jarNode == nil {
		return nil, fmt.Errorf("找不到嵌套 jar: %s", jarPath)
	}
	if jarNode.Children == nil {
		if !service.BuildChildrenWithCompress(jarNode, isPatch) {
			return nil, fmt.Errorf("展开 %s 失败（内容不是有效的压缩包）", jarNode.Name)
		}
	}
	// 注意：inner 可能还带着 `!/`，必须递归解析，不能直接按普通路径查。
	return resolveIn(jarNode, inner, isPatch)
}

// matchLooseFilesIntoApp 把补丁侧拖进来的文件按文件名反查应用树（每个都查）。
//
// 命中 0 个 → 回 matchNone；命中 ≥1 个 → 一律回候选让用户**先看一眼再决定**，
// 不自动替换。原因：拖放改的是包内容，同名文件在胖包里到处都是（多个模块都有
// common.js / application.properties），"悄悄替换了一个你以为对的文件"比多点
// 一下糟糕得多。
//
// 一次拖多个时**每个文件各出一条**（DropMatch）：用户需要知道哪个找到了、
// 哪个没找到。早先只处理 paths[0]，拖两个只认一个，用户当场就看出来了。
//
// 这里**不落临时目录**：直接把文件绑到命中节点上（SetFileSource），
// 打补丁时写出的就是它 —— 与"拖到具体文件行上替换"是同一条路径，
// 少一次"先拷到临时目录、再当补丁包读回来"的往返。
func (a *App) matchLooseFilesIntoApp(paths []string) (*DropResult, error) {
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, dropFileName(p))
	}
	// 一次扫描回答所有名字：磁盘那遍要读几百 MB 的包，按文件各扫一遍太亏。
	res, err := a.findAppCandidatesFor(names)
	if err != nil {
		return nil, err
	}

	items := make([]DropMatch, 0, len(paths))
	total := 0
	for _, src := range paths {
		name := dropFileName(src)
		r := res[strings.ToLower(strings.TrimSpace(name))]
		items = append(items, DropMatch{
			FileName:     name,
			SrcPath:      src,
			SrcSize:      fileSize(src), // 拿不到就算了，不影响匹配本身
			SrcMd5:       fileMd5(src),  // 同上：给右侧那条记录跟左边比对用
			Candidates:   r.cands,
			Placeholders: r.placeholders,
			ScanNote:     r.note,
		})
		total += len(r.cands)
	}

	action := dropActionMatchNone
	if total > 0 {
		action = dropActionMatchMany
	}
	// FileName / SrcPath / SrcSize / SrcMd5 描述"这次拖入"（右侧那条记录用第一个）；
	// 真正逐文件的信息在 Items 里。
	return &DropResult{
		Action:   action,
		FileName: dropFileName(paths[0]),
		SrcPath:  paths[0],
		SrcSize:  fileSize(paths[0]),
		SrcMd5:   fileMd5(paths[0]),
		Count:    len(paths),
		Items:    items,
	}, nil
}

// addOutcome 是一次"把文件加进目录"的结果。
//
// 需要 Loc 是因为"命中的节点"未必与拖入文件同名：原版的名字匹配对 .jar 是
// **前缀**匹配（见 service.MatchPatchName），拖 `foo.jar` 可能命中
// `foo-1.2.3.jar`。前端要按 Loc 去定位/高亮那一行，不能自己用
// `join(dir, 拖入的文件名)` 猜。
type addOutcome struct {
	State *State
	// Loc 命中或新增的节点在前端行上的定位串（嵌套 jar 里带 `jar!/` 前缀）。
	Loc string
	// Replaced 为 true 表示覆盖了已有节点的内容，false 表示新建了节点。
	Replaced bool
	// Same 为 true 表示同名且内容完全一致 —— 既没覆盖也没新建，什么都没做。
	Same bool
}

// addOrReplaceChild 把 src 这个文件加到 parentPath 目录下。
//
// parentPath 为空表示加到根；目录里已有同名子节点时**替换它**而不是加出
// 两个同路径条目（zip 里同路径重复会让解压结果不确定）。
//
// "同名"用的是 service.MatchPatchName —— 与原版 TreeNodeFileMatchProcessor.isMatch
// 同一套规则：.jar 前缀匹配、其余全等。早先这里是 `c.Name == name` 全等，
// 于是"拖一个短名字的 jar 覆盖长名字的"在原版能命中、在 Go 版会变成新增。
//
// 新节点用 KindFileDirect：语义就是"用户直接拖进来的文件"（见 tree.Kind 的注释），
// 与压缩包条目（KindCompress）区分开，但写出侧一视同仁 —— 写包遍历的是整棵树。
//
// memo 只在需要比 md5 时才用到（同名命中那一刻），新增走不到它，所以拖一个
// 全新文件不会有额外 IO。
func (a *App) addOrReplaceChild(side, parentPath, name, src string, memo md5Memo) (*addOutcome, error) {
	if util.IsBlank(name) {
		return nil, fmt.Errorf("拿不到拖入文件的文件名")
	}
	parent, isPatch, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	if parentPath != "" {
		// 用 resolveNode 而不是 nodeBySide：parentPath 可能是嵌套定位串
		// （`BOOT-INF/lib/x.jar!/com/foo/`）—— 那是前端行上的原样路径。
		// nodeBySide 是精确匹配，碰上带 `!/` 的串永远查不到，
		// 结果就是"拖到嵌套 jar 里的目录行新增"直接报树里找不到节点。
		parent, err = a.resolveNode(side, parentPath)
		if err != nil {
			return nil, err
		}
		// 只有指定了父目录时才检查是不是目录；根节点是个合成容器，
		// 它的 Dir() 未必为真，不能拿来当"不是目录"拒绝掉。
		if !parent.Dir() {
			return nil, fmt.Errorf("目标不是目录: %s", parentPath)
		}
	}

	// 同名子节点已存在 → 替换它的内容（对"拖到目录里覆盖旧版本"的直觉）。
	// 名字判定走 service.MatchPatchName，与原版同一套规则。
	for _, c := range parent.Children {
		if service.MatchPatchName(c, name) {
			// 子节点的 Path 是**相对所在 jar** 的（嵌套时不含 `jar!/` 前缀），
			// 直接拿去替换会按"树里第一条同名路径"命中，可能改到别的 jar 里
			// 那个同名文件上。这里补回前缀，让定位串唯一。
			loc := childLoc(parentPath, c.Path)
			// 同名 + 内容完全一致 → 什么都不做（见 dropActionSame 的说明）。
			// 只在这一刻算源文件 md5：新增路径根本走不到这儿，不会有额外 IO。
			if sameContent(c, memo.of(src)) {
				return &addOutcome{State: a.state(), Loc: loc, Same: true}, nil
			}
			st, err := a.ReplaceNodeContent(side, loc, src)
			if err != nil {
				return nil, err
			}
			return &addOutcome{State: st, Loc: loc, Replaced: true}, nil
		}
	}

	fi, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("读取拖入文件失败: %w", err)
	}
	// 新节点的 Path 要**和兄弟节点同一种写法**：嵌套 jar 里的节点是相对那个
	// jar 的（`gfmis/bgtex/factory/Keep.class`），不带 `jar!/` 前缀。
	// 之前这里直接用 parentPath（前端行上的定位串）拼，于是新节点的 Path 变成
	// `a.jar!/gfmis/...`，跟兄弟节点不是一个坐标系 —— 之后再按路径找它、
	// 替换它、或者重建树时对不上。
	childPath := name
	if parentPath != "" {
		childPath = util.JoinPath(parent.Path, name)
	}
	child := tree.NewFile(name, childPath, fi.Size(), time.Now(), nil, isPatch)
	child.Kind = tree.KindFileDirect
	child.SetFileSource(src)
	// 新加的节点也算"本会话里改过"：它不是原包里的条目，界面上要给个标记，
	// 也让用户知道它只存在于内存（重新加载就没了）。
	child.SetEdited(true)
	child.Parent = parent
	parent.Children = append(parent.Children, child)
	return &addOutcome{State: a.state(), Loc: childLoc(parentPath, childPath)}, nil
}

// childLoc 把"父目录（前端行上的路径，可能是嵌套定位串）"和"子节点相对路径"
// 拼成子节点的完整定位串。
//
// 取**最后一个** `!/`：三级嵌套（`a.jar!/lib/b.jar!/x/`）里，子节点的 Path 是
// 相对最内层 b.jar 的，前缀必须是 `a.jar!/lib/b.jar!/`。
func childLoc(parentLoc, childPath string) string {
	if i := strings.LastIndex(parentLoc, "!/"); i >= 0 {
		return parentLoc[:i+2] + childPath
	}
	return childPath
}

// parentLoc 取一个树节点定位串的**父目录**，保留 `jar!/` 前缀与结尾斜杠。
//
// 例：
//
//	`BOOT-INF/lib/x.jar!/com/foo/Bar.class` → `BOOT-INF/lib/x.jar!/com/foo/`
//	`BOOT-INF/classes/Foo.class`           → `BOOT-INF/classes/`
//	`Foo.class`                            → ``
//
// 用途：把一个文件拖到**文件行**上、而且一次拖了多个时，目标要退回它的父目录
// —— 一个文件节点装不下多个子项。根下的文件返回空串（空串就是"根目录"，
// 与 HandleDrop 里 rowPath 为空的口径一致）。
//
// 带尾斜杠的目录定位串（`a/b/`）**原样返回**：它本身就是"往这里加"，再往上退
// 一层反而错了。正常只被文件行调用，这条是兜底。
func parentLoc(loc string) string {
	head, tail := "", loc
	if i := strings.LastIndex(loc, "!/"); i >= 0 {
		head, tail = loc[:i+2], loc[i+2:]
	}
	j := strings.LastIndex(tail, "/")
	if j < 0 {
		return head
	}
	return head + tail[:j+1]
}

// dropFileName 取路径里的文件名（兼容 Windows 的 `\` 与 POSIX 的 `/`）。
func dropFileName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// fileSize 取磁盘文件的字节数，拿不到就返回 0。
//
// 只用于把体积显示给用户看（右侧「拖入的文件」条、候选清单里的"拖入 150 KB"），
// 所以取不到不算错 —— 文件刚被移走、权限不足都可能，不该因此让整个拖放失败。
func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
		return fi.Size()
	}
	return 0
}

// fileMd5 算磁盘文件的 md5（小写十六进制），拿不到就返回空串。
//
// 与 fileSize 同一个口径：取不到不算错，空串表示"不知道"，前端据此不显示
// 而不是显示一行假的 0。
//
// 为什么要单独算一遍而不是复用别处的结果：拖进来的文件此刻只是磁盘上一个
// 路径，包内节点那套 Md5 缓存（GetMd5OrInit）是给**包内**内容用的，两边
// 不是同一份字节。代价是把文件读一遍 —— 常见补丁文件都是 KB 级，读一遍
// 眼睛都来不及眨；真拖个几百 MB 的包进来，这一下也就是一秒上下，而且
// 拖放本来就走 busy 指示（见 app.js onFileDrop），用户看得到它在忙。
func fileMd5(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	s, err := util.Md5Stream(f)
	if err != nil {
		return ""
	}
	return s
}

// ReplaceNodeContent 用磁盘上的一个文件替换包内某个节点的内容。
//
// 对应"单文件替换"：选中应用包（或补丁包）里的某个文件，把外部文件拖到它上面，
// 它的内容就被换成拖进来的那个。实现上只换节点的内容来源（SetFileSource），
// 读内容的地方全都走 ConsumeBytes（见 service/writer.go），所以打补丁写出的
// 就是新内容 —— 与"编辑内容"（SetContent）是同一套机制，区别只是来源是磁盘文件。
//
// 与 SetContent 一样要把 Md5 / Size 缓存清掉、把 edited 置真：这两个缓存是
// "读一遍顺手填"的，不清的话 sign 里的校验值会拿旧内容算（详见 node.go 的
// SetContent 注释）。改动只存在于内存，重新加载 / 重新匹配会丢弃（和编辑内容一致）。
func (a *App) ReplaceNodeContent(side string, path string, srcPath string) (*State, error) {
	if util.IsBlank(path) {
		return nil, fmt.Errorf("没有指定要替换的节点")
	}
	if isDirPath(srcPath) {
		return nil, fmt.Errorf("拖入的是目录，无法替换单个文件")
	}
	// 用 resolveNode 而不是 nodeBySide：path 可能是嵌套定位串
	// （`BOOT-INF/lib/x.jar!/com/foo/Bar.class`），见 joinLoc。
	n, err := a.resolveNode(side, path)
	if err != nil {
		return nil, err
	}
	if n.Dir() {
		return nil, fmt.Errorf("目录不能替换内容: %s", n.Name)
	}
	n.SetFileSource(srcPath)
	n.Md5 = ""
	n.Size = tree.SizeUnknown
	n.SetEdited(true)
	return a.state(), nil
}

func (a *App) HandleFileDrop(side string, paths []string) (*State, error) {
	// Java 的 onDragDropped 是 `files.get(0)`，空列表会抛
	// IndexOutOfBoundsException —— 被全局异常处理器吃掉，界面无反应。
	// Go 里越界是 panic、会直接终止进程，所以这里显式挡掉。
	// 这是刻意的、必要的偏差，已登记在 MIGRATION.md §3.9。
	if len(paths) == 0 {
		return nil, fmt.Errorf("没有拿到文件路径")
	}

	switch DropSide(side) {
	case DropSideApp:
		// 对齐 Java：目标在 DRAG_OVER 期间没接受传输模式时，
		// DRAG_DROPPED 根本不会投递，所以非法拖入在原版是"静默无反应"。
		// 这里改成给一句明确的提示 —— 结果同样是"什么都没打开"，
		// 只是不再让人以为是程序卡了。同属 §3.9 的刻意偏差。
		if !acceptAsAppPack(paths) {
			return nil, fmt.Errorf("应用包只接受 1 个 .jar 文件")
		}
		return a.OpenAppPack(paths[0])

	case DropSidePatch:
		if !acceptAsPatchPack(paths) {
			return nil, fmt.Errorf("补丁包只接受 1 个目录，或 1 个 .jar / .zip 文件")
		}
		return a.OpenPatchPack(paths[0])

	default:
		return nil, fmt.Errorf("未知的拖放位置: %s", side)
	}
}

// ---------------------------------------------------------------------------
// 标记根节点 / 标记说明文件
// ---------------------------------------------------------------------------

// MarkRoot 把补丁树上的节点标记为自定义根，已是自定义根则取消标记。
//
// 移植自 Java HomeRightTreeView.listenMarkRootChange。
//
// 存在的意义：原版的 README 查找（RefreshReadmeNode）**只扫补丁根的直接
// 子节点**。所以"README 套在一层目录里"（`mypatch/README.txt`）或条目名带
// `./` 前缀（`./README.txt`，`zip -r x.zip .` 的产物）的包，自动流程认不出
// README —— 这在上游是同一个行为，不是移植偏差。原版给的解法就是让用户
// 把那一层目录手动标成根，于是 README 成了它的直接子节点。
func (a *App) MarkRoot(path string) (*State, error) {
	if a.patchTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开补丁包")
	}
	node := findPatchNodeByPath(a.patchTreeInfo.RootNode(), path)
	if node == nil {
		return nil, fmt.Errorf("补丁树里找不到节点: %s", path)
	}

	st := a.step()

	// 已是自定义根 -> 取消标记
	if node.Type == constant.TreeNodeTypeCustomRoot {
		a.patchTreeInfo.RemoveCustomRootInfo(node)
		service.CleanMappedNode(a.total, node, false)
		// 没有别的自定义根了，就把主根重新刷一遍
		if len(a.patchTreeInfo.CustomRootInfoMap()) == 0 {
			a.refreshRoot(a.patchTreeInfo.RootNode(), st, true)
		}
		return a.state(), nil
	}

	// 标记为自定义根：先把主根上的绑定清干净（只在还没有任何自定义根时做）
	if len(a.patchTreeInfo.CustomRootInfoMap()) == 0 {
		service.CleanMappedNode(a.total, a.patchTreeInfo.RootNode(), false)
	}
	node.Type = constant.TreeNodeTypeCustomRoot
	a.patchTreeInfo.AddCustomRootInfo(node, service.CreatePatchRootInfo(node))
	a.refreshRoot(node, st, true)

	return a.state(), nil
}

// MarkReadme 把节点标记为说明文件（README）。
//
// 移植自 Java HomeRightTreeView.handleMarkReadme：
//
//	patchRootInfo = treeInfo.getRootInfoByNode(selectedNode.getParent());
//	readmeNode = patchRootInfo.getReadmeNode();
//	if (readmeNode != null) readmeNode.setType(NONE);   // 旧的让位
//	selectedNode.setType(README);
//	refreshPatchMappedNode(true);
//
// 那句"旧的让位"不能省：RefreshReadmeNode 扫直接子节点时**碰到第一个
// Type == README 就 break**。若旧 README 在子节点顺序里更靠前，不把它降回
// NONE 就会一直被认成 README。
func (a *App) MarkReadme(path string) (*State, error) {
	if a.patchTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开补丁包")
	}
	node := findPatchNodeByPath(a.patchTreeInfo.RootNode(), path)
	if node == nil {
		return nil, fmt.Errorf("补丁树里找不到节点: %s", path)
	}

	rootInfo := a.patchTreeInfo.RootInfoByNode(node.Parent)
	if rootInfo == nil {
		// Java 这里会 NPE（getRootInfoByNode 返回 null 后直接解引用）。
		// 菜单项的显示条件已经挡掉了这种情况，走不到；真到了就给个明确错误，
		// 不要 panic 掉整个进程。
		return nil, fmt.Errorf("节点 %s 不在任何补丁根下", path)
	}
	if old := rootInfo.ReadmeNode; old != nil {
		old.Type = constant.TreeNodeTypeNone
	}
	node.Type = constant.TreeNodeTypeReadme

	a.refreshAllRoots(a.step())

	return a.state(), nil
}

// ---------------------------------------------------------------------------
// 补丁树右键菜单
// ---------------------------------------------------------------------------

// menuItem 造一个菜单项，文案按当前语言取。
func (a *App) menuItem(id, key string) *MenuItemView {
	return &MenuItemView{ID: id, Label: i18n.Tr(a.langCode(), key)}
}

// patchMenu 生成补丁树节点的右键菜单。
//
// 逐条对齐 Java HomeRightTreeView.buildPatchTreeContextMenu 的 claim
// 条件**和顺序**（菜单是按序渲染的，顺序错了观感就不对）。
//
// 有意跳过的两项（都依赖尚未移植的功能，见 MIGRATION.md §五）：
//
//	保存为项目    需要 JarDecompilerService
//	创建说明文件  需要 READ ME 编辑器那一屏（ReadmeView）
func (a *App) patchMenu(n, parent *tree.Node) []*MenuItemView {
	if n == nil || a.patchTreeInfo == nil {
		return nil
	}
	hasCustomRoot := len(a.patchTreeInfo.CustomRootInfoMap()) > 0
	items := make([]*MenuItemView, 0, 12)

	// 1. 主根 → 重新加载；自定义根 → 刷新信息
	switch n.Type {
	case constant.TreeNodeTypeRoot:
		items = append(items, a.menuItem(menuReload, "app.view.right-tree.reload"))
	case constant.TreeNodeTypeCustomRoot:
		items = append(items, a.menuItem(menuReload, "app.view.right-tree.refresh"))
	}

	// 2. 取消关联：只对已建立映射的节点显示
	if n.MappedNode != nil {
		items = append(items, a.menuItem(menuCancelMapped, "app.view.right-tree.cancel-binding"))
	}

	// 3. 标记根节点（单选，勾选态表示"当前已经是自定义根"）
	if canMarkRoot(n) {
		it := a.menuItem(menuMarkRoot, "app.view.right-tree.mark-root")
		it.Radio = true
		it.Checked = n.Type == constant.TreeNodeTypeCustomRoot
		items = append(items, it)
	}

	// 4. 标记说明文件
	if canMarkReadme(n, parent) {
		items = append(items, a.menuItem(menuMarkReadme, "app.view.right-tree.mark-readme"))
	}

	// 5. 编辑内容：**本轮新增，原版没有**（原版那段「编辑模式」菜单项被注释掉了，
	//    见 MIGRATION.md 的「编辑包内文件内容」）。非目录才有。
	if !n.Dir() {
		items = append(items, a.menuItem(menuEditContent, "app.view.tree.edit-content"))
	}

	// 6. 保存文件：任何非目录节点都能把内容导出去
	if !n.Dir() {
		items = append(items, a.menuItem(menuSaveFile, "app.view.right-tree.save-file"))
	}

	// 7. 保存源文件：只对 .class 显示 —— 导出的是反编译出来的 Java 源码
	//    （不是 class 字节）。对齐原版 claim 条件
	//    `!node.isDir() && node.getName().endsWith(DOT_CLASS)`。
	if isClassNode(n) {
		items = append(items, a.menuItem(menuSaveSourceFile, "app.view.right-tree.save-source-file"))
	}

	// 8. 复制（子菜单）。目录没有 md5 —— 原版是"显示但置灰"
	//    （copyMenu.setOnShowing 里 setDisable），不是隐藏。
	copyItem := a.menuItem(menuCopy, "app.common.copy")
	copyItem.Children = []*MenuItemView{
		a.menuItem(menuCopyName, "app.view.tree.copy-file-name"),
		a.menuItem(menuCopyPath, "app.view.tree.copy-file-path"),
		a.menuItem(menuCopyRelative, "app.view.tree.copy-file-relative-path"),
		a.menuItem(menuCopyMd5, "app.view.tree.copy-file-md5"),
	}
	copyItem.Children[3].Disabled = n.Dir()
	items = append(items, copyItem)

	// 9. 查看签名：只对根节点显示
	if tree.IsRootNode(n) {
		items = append(items, a.menuItem(menuViewSign, "app.view.right-tree.view-sign"))
	}

	// 10. 查看绑定信息：要先打开应用包；主根还额外要求"没有自定义根"
	if a.canViewNodeMapped(n, hasCustomRoot) {
		items = append(items, a.menuItem(menuViewNodeMapped, "app.view.right-tree.view-node-mapped"))
	}

	// 11. 资源管理器中查看：只对"磁盘上的普通文件"显示。
	//
	//    注意这里判的是**节点种类**（Java 的 `node instanceof CompressNode`），
	//    不是 `tree.IsCompressNode` 那个按后缀判的 —— 两者同名但完全不是一回事：
	//    压缩包里的 `README.txt` 后缀不像压缩包，可它是 CompressNode，没有磁盘
	//    路径，选不中。所以这一项实际只在**补丁目录模式**下出现。
	if isDiskFile(n) {
		items = append(items, a.menuItem(menuOpenInExplorer, "app.view.right-tree.open-in-explorer"))
	}

	// 12. 关闭补丁：只对主根显示
	if n.Type == constant.TreeNodeTypeRoot {
		items = append(items, a.menuItem(menuClosePatch, "app.view.right-tree.close-patch"))
	}

	// 13. 聚焦补丁（单选）。原版这一项**没有 claim**，永远显示。
	focus := a.menuItem(menuFocusPatchTree, "app.view.right-tree.focus-patch-tree")
	focus.Radio = true
	focus.Checked = a.focusTree&focusTreePatch != 0
	items = append(items, focus)

	return items
}

// appMenu 生成应用树节点的右键菜单。
//
// 原版 HomeLeftTreeView.buildAppTreeContextMenu 一共 **13 项**，本函数
// 逐条对齐原版的**顺序**与 **claim 条件**（菜单按序渲染，顺序错了观感就不对）：
//
//	创建目录        目录        —— 未做（需要建目录能力，见下）
//	重命名          单选节点    —— 未做
//	重新加载        节点是主根
//	取消关联        节点有映射
//	复制 ▸          单选节点（目录的 MD5 项置灰）
//	删除            单选节点、不是根、且状态为 NONE
//	标记删除        单选节点、状态为 DEL 或 NONE（单选切换项）
//	编辑内容        非目录      —— 本轮新增，原版没有
//	保存文件        非目录
//	保存源文件      非目录 + .class
//	资源管理器中查看  磁盘上的普通文件
//	关闭应用包      主根
//	聚焦应用树      无条件（原版这一项**没有 claim**，永远显示）
//
// 有意**未做**的 3 项（都是"改包结构"类操作，见 MIGRATION.md §四）：
//
//	创建目录 / 重命名        需要在建树与写出两侧同时支持
//	保存源文件（.class 反编译导出）后端能力已就绪，只是没接
//	保存为项目（.jar 整体导出）需要 JarDecompilerService
func (a *App) appMenu(n, parent *tree.Node) []*MenuItemView {
	if n == nil || a.appTreeInfo == nil {
		return nil
	}
	items := make([]*MenuItemView, 0, 11)

	// 3. 重新加载：原版只对主根显示（与补丁树不同，补丁树在自定义根上也有）
	if n.Type == constant.TreeNodeTypeRoot {
		items = append(items, a.menuItem(menuReload, "app.view.left-tree.reload"))
	}

	// 4. 取消关联：只对已建立映射的节点显示
	if n.MappedNode != nil {
		items = append(items, a.menuItem(menuCancelMapped, "app.view.left-tree.cancel-binding"))
	}

	// 5. 复制（子菜单）
	copyItem := a.menuItem(menuCopy, "app.common.copy")
	copyItem.Children = []*MenuItemView{
		a.menuItem(menuCopyName, "app.view.tree.copy-file-name"),
		a.menuItem(menuCopyPath, "app.view.tree.copy-file-path"),
		a.menuItem(menuCopyRelative, "app.view.tree.copy-file-relative-path"),
		a.menuItem(menuCopyMd5, "app.view.tree.copy-file-md5"),
	}
	copyItem.Children[3].Disabled = n.Dir()
	items = append(items, copyItem)

	// 6. 删除：对应 Java isAcceptManualDel —— 不是根、且状态必须是 NONE。
	//
	//    "状态必须是 NONE"这条很关键：已经被标删 / 标改 / 标增的节点，
	//    原版不给「删除」这一项（要改它得先「取消关联」把状态清回 NONE）。
	if canDeleteNode(n) {
		items = append(items, a.menuItem(menuDelete, "app.view.left-tree.delete"))
	}

	// 7. 标记删除：对应 Java claim —— 状态为 DEL 或 NONE，且是 RadioMenuItem
	//    （已经标删时呈勾选态，再点一次取消标记）。
	if canMarkDelNode(n) {
		it := a.menuItem(menuMarkDelete, "app.view.left-tree.mark-delete")
		it.Radio = true
		it.Checked = n.Type == constant.TreeNodeTypeDel
		items = append(items, it)
	}

	// 7.5 恢复原状：只在磁盘上还留着本包的删除记录时出现。
	//     物理删除的节点已经不在树上了，右键点不到它，只能整体撤销 ——
	//     这一项就是给那种情况准备的（见 ResetManualDel）。
	if a.manualDelPendingNow {
		items = append(items, a.menuItem(menuResetManualDel, "app.view.left-tree.reset-manual-del"))
	}

	// 8. 编辑内容：**本轮新增，原版没有**（见 MIGRATION.md 的「编辑包内文件内容」）
	if !n.Dir() {
		items = append(items, a.menuItem(menuEditContent, "app.view.tree.edit-content"))
	}

	// 9. 保存文件
	if !n.Dir() {
		items = append(items, a.menuItem(menuSaveFile, "app.view.left-tree.save-file"))
	}

	// 10. 资源管理器中查看：只对磁盘上的普通文件
	if isDiskFile(n) {
		items = append(items, a.menuItem(menuOpenInExplorer, "app.view.left-tree.open-in-explorer"))
	}

	// 11. 关闭应用包：只对主根显示
	if n.Type == constant.TreeNodeTypeRoot {
		items = append(items, a.menuItem(menuCloseAppPack, "app.view.left-tree.close-app-pack"))
	}

	// 12. 聚焦应用树（单选）。原版没有 claim，永远显示。
	focus := a.menuItem(menuFocusAppTree, "app.view.left-tree.focus-app-tree")
	focus.Radio = true
	focus.Checked = a.focusTree&focusTreeApp != 0
	items = append(items, focus)

	return items
}

// canDeleteNode 对应「删除」菜单项的 claim（Java isAcceptManualDel）。
//
// 原版是对**多选集合**求值，这里只有单选，右键一次只作用于一个节点，
// 所以直接判这一个节点即可 —— 语义与原版单选时完全一致。
func canDeleteNode(n *tree.Node) bool {
	return n != nil && n.Type != constant.TreeNodeTypeRoot && n.Type == constant.TreeNodeTypeNone
}

// canMarkDelNode 对应「标记删除」菜单项的 claim。
func canMarkDelNode(n *tree.Node) bool {
	if n == nil {
		return false
	}
	return n.Type == constant.TreeNodeTypeDel || n.Type == constant.TreeNodeTypeNone
}

// isDiskFile 判断节点是不是"磁盘上的普通文件"。
//
// 对应 Java 的 `node instanceof FileNode && !(node instanceof CompressNode)`。
// 这里判的是**节点种类**，与 tree.IsCompressNode 那个按文件名后缀判的
// （Java 的 TreeNodeUtil.isCompressNode）是两码事 —— 别混用。
// 目录和压缩包条目都没有可定位的磁盘路径（见 Node.FilePath）。
func isDiskFile(n *tree.Node) bool {
	return n != nil && !n.Dir() && (n.Kind == tree.KindFile || n.Kind == tree.KindFileDirect)
}

// canViewNodeMapped 对应「查看绑定信息」的 claim。
func (a *App) canViewNodeMapped(n *tree.Node, hasCustomRoot bool) bool {
	if a.appTreeInfo == nil {
		return false
	}
	switch n.Type {
	case constant.TreeNodeTypeCustomRoot:
		return true
	case constant.TreeNodeTypeRoot:
		return !hasCustomRoot
	}
	return false
}

// ---------------------------------------------------------------------------
// 右键菜单动作
// ---------------------------------------------------------------------------

// patchNode 取补丁树上指定路径的节点，取不到就报错。
func (a *App) patchNode(path string) (*tree.Node, error) {
	if a.patchTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开补丁包")
	}
	n := findPatchNodeByPath(a.patchTreeInfo.RootNode(), path)
	if n == nil {
		return nil, fmt.Errorf("补丁树里找不到节点: %s", path)
	}
	return n, nil
}

// ReloadOrRefresh 对应菜单里的「重新加载」/「刷新信息」。
//
// 移植自 Java handleReloadOrRefresh。两条分支要做的事不一样：
//
//	选中主根          → 重新读一遍补丁包（reloadPatchTree）
//	选中自定义根下的节点 → 只重跑映射，**不重读 README**
//
// 那个 false 是 refreshPatchMappedNode(isRefreshReadme=false, rootNode) 的第一
// 个参数。别顺手统一成"都重读"—— 原版刻意区分：刷新信息只是重新比对，
// 不重新解析说明文件。
//
// side 为 "app" 时走应用树那套：原版 HomeLeftTreeView.handleReload 就是
// "根文件还在就重读一遍应用包"，没有"刷新信息"这一说（应用树没有自定义根）。
func (a *App) ReloadOrRefresh(side, path string) (*State, error) {
	if side == treeSideApp {
		return a.reloadAppPack()
	}
	n, err := a.patchNode(path)
	if err != nil {
		return nil, err
	}
	root := tree.GetUnderRootNode(n)
	if root == nil {
		return nil, fmt.Errorf("节点 %s 不在任何补丁根下", path)
	}
	if root.Type == constant.TreeNodeTypeRoot {
		return a.reloadPatchPack()
	}

	a.progress = nil
	a.refreshRoot(root, a.step(), false)
	return a.state(), nil
}

// reloadPatchPack 重新读一遍当前补丁包。
//
// 对应 Java reloadPatchTree：**文件不存在就什么都不做**（原版那句
// `if (file.exists())`），不是报错。包被挪走/删掉时保持现状更不容易让人懵。
func (a *App) reloadPatchPack() (*State, error) {
	if util.IsBlank(a.patchPath) {
		return nil, fmt.Errorf("还没有打开补丁包")
	}
	if _, err := os.Stat(a.patchPath); err != nil {
		return a.state(), nil
	}
	return a.OpenPatchPack(a.patchPath)
}

// reloadAppPack 重新读一遍当前应用包。
//
// 对应 Java HomeLeftTreeView.handleReload 里的
// `if (rootNode.getFile().exists()) refreshAppTree(...)` —— 同样"文件没了就
// 什么都不做"。**注意这会重建整棵应用树，节点上未落盘的内容修改随之丢弃**
// （见 MIGRATION.md 的「编辑包内文件内容」）。
func (a *App) reloadAppPack() (*State, error) {
	if util.IsBlank(a.appPath) {
		return nil, fmt.Errorf("还没有打开应用包")
	}
	if _, err := os.Stat(a.appPath); err != nil {
		return a.state(), nil
	}
	return a.OpenAppPack(a.appPath)
}

// CancelMapped 对应「取消关联」：把这个节点（及其子树）的映射清干净。
//
// 移植自 Java handleCancelMapped → TreeNodeUtil.deepCleanMappedNode。
// 清完之后两棵树都只是重渲染，**不重跑匹配** —— 所以这里返回 state() 即可。
// 应用树和补丁树各有一个「取消关联」，实现是同一个（原版两边的 handler
// 也都是调 deepCleanMappedNode，只是传入的选中节点来自各自的树）。
func (a *App) CancelMapped(side, path string) (*State, error) {
	n, err := a.nodeBySide(side, path)
	if err != nil {
		return nil, err
	}
	tree.DeepCleanMappedNode(a.total, n, nil)
	return a.state(), nil
}

// DeleteAppNode 对应菜单里的「删除」。
//
// 移植自 Java HomeLeftTreeView.handleManualDel。**它不是"标删"，而是把节点
// 从应用树上物理摘掉** —— 三步顺序与原版逐字对齐：
//
//  1. deepCleanMappedNode   清干净子树里的映射与原有统计（顺便把状态清回 NONE）
//  2. removeNodeChild       从父节点的 children 里摘掉
//  3. countNodeType(MANUAL_DEL)  按"手动删除"口径累加统计
//
// **MANUAL_DEL 只是个统计口径，节点本身不会被设成这个类型**（设了也没用，
// 因为它已经被移出树了，写包时遍历根本走不到）。原版就是这样：MANUAL_DEL
// 与 DEL 是两个独立计数，分别写进签名里的 manualDelTotal / delTotal。
//
// 注意第 3 步在第 2 步**之后**：这时节点已经不在树上了，但仍要按它原来的
// 子树结构累加，所以 countNodeType 是拿被摘掉的节点当根来遍历的。
//
// 原版弹「确认删除？」确认框；那个框由前端负责（见 app.js 的 dispatch）。
func (a *App) DeleteAppNode(path string) (*State, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包")
	}
	n := findTreeNodeByPath(a.appTreeInfo.RootNode(), path)
	if n == nil {
		return nil, fmt.Errorf("应用树里找不到节点: %s", path)
	}
	// claim 条件在服务端复检一遍：菜单已经挡过一次，但这是公开方法，
	// 前端或将来的其它入口不该能绕过"根节点不能删 / 非 NONE 不能删"。
	if !canDeleteNode(n) {
		return nil, fmt.Errorf("节点 %s 不能删除（根节点或已被标记）", n.Name)
	}

	tree.DeepCleanMappedNode(a.total, n, nil)
	tree.RemoveNodeChild(n)
	tree.CountNodeType(a.total, n, constant.TreeNodeTypeManualDel)

	a.appendManualDel(a.appPath, path, manualdel.ActionPendingDelete)
	return a.state(), nil
}

// appendManualDel 把一次"删除 / 标记删除"动作追加进持久化层。
//
// jarPath 是**应用包**路径（决定记录存到哪个文件），nodePath 是**被删节点**
// 在包内的相对路径（决定记录内容）。这两个别混 —— 早期版本把 nodePath
// 也传了 jarPath，结果存进去的是 "D:/.../app.jar" 这种文件路径，
// 重应用时 findTreeNodeByPath 永远找不到节点，删除记录等于白写。
//
// fail-soft：落盘失败只在 step 里记一笔，不影响内存侧的删除结果。
func (a *App) appendManualDel(jarPath, nodePath, action string) {
	if a.manualDelStore == nil || jarPath == "" {
		return
	}
	entries, err := a.manualDelStore.Load(jarPath)
	if err != nil {
		a.step()("manualdel: 读取记录失败（继续，不落盘）: " + err.Error())
		return
	}
	entry := manualdel.Entry{Path: jarPathEntry(nodePath), Action: action}
	// 去重：同一 Path + Action 已存在就跳过（撤销再重做也不会写两条）。
	for _, e := range entries {
		if e.Path == entry.Path && e.Action == entry.Action {
			return
		}
	}
	a.manualDelStore.Save(jarPath, append(entries, entry), a.step())
}

// jarPathEntry 把节点路径规整成记录用的相对路径（去前导斜杠）。
func jarPathEntry(nodePath string) string {
	return strings.TrimPrefix(nodePath, "/")
}

// reapplyManualDel 在打开应用包后，把持久化层里记录的删除重新应用到内存树。
//
// 这是"关掉重开还在"的关键：用户上次删了 bootstrap.yml，下次打开同一个 jar，
// 这里会把它从树上摘掉（或标删）。找不到的条目（jar 内容变了）会被忽略 ——
// 不报错、不阻塞打开流程。
func (a *App) reapplyManualDel() {
	if a.manualDelStore == nil || a.appPath == "" || a.appTreeInfo == nil {
		return
	}
	entries, err := a.manualDelStore.Load(a.appPath)
	if err != nil || len(entries) == 0 {
		return
	}
	d := manualdel.Split(entries, a.step())
	for _, e := range d.Manual {
		n := findTreeNodeByPath(a.appTreeInfo.RootNode(), e.Path)
		if n == nil {
			continue // jar 已经没有这个文件，忽略
		}
		tree.DeepCleanMappedNode(a.total, n, nil)
		tree.RemoveNodeChild(n)
		tree.CountNodeType(a.total, n, constant.TreeNodeTypeManualDel)
	}
	for _, e := range d.Marked {
		n := findTreeNodeByPath(a.appTreeInfo.RootNode(), e.Path)
		if n == nil {
			continue
		}
		tree.DeepCleanMappedNode(a.total, n, nil)
		if n.Type != constant.TreeNodeTypeDel {
			tree.CountAndSetNodeType(a.total, n, constant.TreeNodeTypeDel)
		}
	}
}

// MarkDeleteAppNode 对应菜单里的「标记删除」，是个切换操作。
//
// 移植自 Java HomeLeftTreeView.handleMarkDel：
//
//	已经是 DEL → deepCleanMappedNode 清掉，状态回到 NONE（取消标记）
//	否则       → 先 deepCleanMappedNode 清干净旧的，再 countAndSetNodeType(DEL)
//
// 与「删除」的本质差异：**节点留在树上**，只是状态变成 DEL；写出时
// writeTreeNode2Pack 看到 DEL 就跳过不写进新包，并记一条 delInfo
// （见 internal/service/writer.go）。所以标删是会进输出包的删除清单的，
// 而「删除」不是 —— 它只是把这文件从工作视图里拿走。
func (a *App) MarkDeleteAppNode(path string) (*State, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包")
	}
	n := findTreeNodeByPath(a.appTreeInfo.RootNode(), path)
	if n == nil {
		return nil, fmt.Errorf("应用树里找不到节点: %s", path)
	}
	if !canMarkDelNode(n) {
		return nil, fmt.Errorf("节点 %s 不能标记删除", n.Name)
	}

	// 先取状态再清理 —— DeepCleanMappedNode 会把 type 清回 NONE，
	// 如果在它之后判断，"已经是 DEL"分支永远走不到，取消标记就废了。
	wasDel := n.Type == constant.TreeNodeTypeDel

	tree.DeepCleanMappedNode(a.total, n, nil)
	if !wasDel {
		tree.CountAndSetNodeType(a.total, n, constant.TreeNodeTypeDel)
		a.appendManualDel(a.appPath, path, manualdel.ActionMarkDelete)
	} else {
		// 取消标记：把这条标删记录撤掉，否则下次打开同一个 jar 又标回来。
		a.removeManualDel(a.appPath, path, manualdel.ActionMarkDelete)
	}

	return a.state(), nil
}

// removeManualDel 从持久化层里删掉某条记录（取消标删 / 撤销删除用）。
//
// fail-soft：读不到就当没有，落盘失败只记 step，不影响内存侧。
// removeManualDel 从持久化层里删掉某条记录（取消标删时用）。
//
// jarPath 是应用包路径，nodePath 是节点在包内的相对路径。
// fail-soft：读不到就当没有，落盘失败只记 step，不影响内存侧。
func (a *App) removeManualDel(jarPath, nodePath, action string) {
	if a.manualDelStore == nil || jarPath == "" {
		return
	}
	entries, err := a.manualDelStore.Load(jarPath)
	if err != nil {
		return
	}
	target := jarPathEntry(nodePath)
	kept := entries[:0:0]
	for _, e := range entries {
		if e.Path == target && e.Action == action {
			continue
		}
		kept = append(kept, e)
	}
	a.manualDelStore.Save(jarPath, kept, a.step())
}

// manualDelPending 判断当前应用包是否还留着「删除 / 标记删除」记录。
//
// 读一次记录文件即可（通常就几行 JSON），比走一遍树便宜得多。
// 读失败当作没有 —— 这只是一个"要不要显示菜单项"的判断，不该因为它报错。
func (a *App) manualDelPending() bool {
	if a.manualDelStore == nil || a.appPath == "" {
		return false
	}
	entries, err := a.manualDelStore.Load(a.appPath)
	return err == nil && len(entries) > 0
}

// ResetManualDel 撤销对当前应用包做过的所有「删除 / 标记删除」。
//
// 这是"临时补丁"唯一的出口 —— 在此之前，标记删除只能一条一条在应用树上
// 反向点掉，而**物理删除（「删除」菜单）根本没有反向操作**：节点已经从树上
// 摘掉了，右键都找不到它，但记录还在，关掉重开照样重删一遍。用户报的
// "取消不掉"就是这个。
//
// 做法是"清记录 + 按原包重新解析"，而不是在树上逐条回滚：
//
//  1. 清掉磁盘上的记录 —— 否则 OpenAppPack 里的 reapplyManualDel 会
//     立刻把删掉的东西再删一遍，等于白重开。
//  2. OpenAppPack(原路径) 重新解析 jar —— 物理删掉的节点自然回来，
//     标删节点的 DEL 状态也随新树一起消失，统计（total）一并重算。
//  3. 里面会走到 refresh()，补丁树按新树重新匹配，连线/差异都跟着更新。
//
// 代价是要重读一遍 jar（几百 MB 的包要几秒），所以前端得给个确认框，
// 别做成随手就点的小按钮。
func (a *App) ResetManualDel() (*State, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包")
	}
	if util.IsBlank(a.appPath) {
		return nil, fmt.Errorf("应用包路径为空，无法重新加载")
	}
	path := a.appPath
	if a.manualDelStore != nil {
		if err := a.manualDelStore.Clear(path); err != nil {
			return nil, fmt.Errorf("清理删除记录失败: %w", err)
		}
	}
	return a.OpenAppPack(path)
}

// CopyNodeText 对应「复制」子菜单的四项，把文本写进系统剪贴板。
//
// 放后端做而不是前端 navigator.clipboard：WebView2 里剪贴板 API 需要
// 用户手势且可能被拒，而 Wails 的 runtime 通道没有这些限制。
//
// 两棵树都有这个子菜单（原版 HomeLeftTreeView / HomeRightTreeView 各建一份），
// 所以带 side。
func (a *App) CopyNodeText(side, path, kind string) error {
	n, err := a.nodeBySide(side, path)
	if err != nil {
		return err
	}
	var text string
	switch kind {
	case menuCopyName:
		text = n.Name
	case menuCopyPath:
		text = nodeDisplayPath(n)
	case menuCopyRelative:
		text = n.Path
	case menuCopyMd5:
		if n.Dir() {
			return fmt.Errorf("目录没有 MD5")
		}
		text = n.GetMd5OrInit()
	default:
		return fmt.Errorf("未知的复制类型: %s", kind)
	}
	return runtime.ClipboardSetText(a.ctx, text)
}

// SetWindowBackground 把窗口底色换成给定颜色，形如 "#f5f7fa"。
//
// 只为暗色主题服务：WebView 只覆盖客户区，缩放边缘与圆角那一圈露出的是
// **窗口**底色（gui/main.go 的 BackgroundColour）。切到暗色时不换，窗口
// 四周会留一圈浅色边，很扎眼。
//
// 颜色由前端从 CSS 变量 --bg 读出来再传过来，而不是 Go 侧写死两份常量 ——
// 这样"画布底色"只有 app.css 一个出处，换主题方案时不用记得再来改 Go。
// 启动那一瞬仍用 main.go 里的亮色值（那时前端还没跑起来，见那边的注释）。
func (a *App) SetWindowBackground(hex string) error {
	r, g, b, err := parseHexColour(hex)
	if err != nil {
		return err
	}
	runtime.WindowSetBackgroundColour(a.ctx, r, g, b, 255)
	return nil
}

// parseHexColour 解析 #rgb / #rrggbb（大小写、有没有 # 都行）。
func parseHexColour(s string) (r, g, b uint8, err error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("颜色格式不对: %q（应为 #rrggbb）", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("颜色解析失败 %q: %w", s, err)
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), nil
}

// nodeDisplayPath 拼出"从根之下开始"的显示路径。
//
// 逐字对齐 Java handleCopyPath 那个 do-while：
//
//	do { paths.add(node.getName()); }
//	while ((node = node.getParent()) != null && !ROOT.equals(node.getType()));
//
// 也就是**从自己往上收集到（不含）根节点**，再反转用 '/' 连接。
// 节点本身就是根时，结果就是根自己的名字。
func nodeDisplayPath(n *tree.Node) string {
	parts := []string{n.Name}
	for cur := n.Parent; cur != nil && cur.Type != constant.TreeNodeTypeRoot; cur = cur.Parent {
		parts = append(parts, cur.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, util.Separator)
}

// SaveNodeAs 对应「保存文件」：把节点的原始内容导出到用户选定的路径。
//
// 移植自 Java saveFile → doSaveFile(node, node::consumeBytes, ext)。
func (a *App) SaveNodeAs(side, path string) (*State, error) {
	n, err := a.nodeBySide(side, path)
	if err != nil {
		return nil, err
	}
	if n.Dir() {
		return nil, fmt.Errorf("目录不能保存为文件")
	}
	out, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存文件",
		DefaultFilename: defaultSaveName(n),
	})
	if err != nil {
		return nil, err
	}
	if util.IsBlank(out) {
		// 用户取消：原版直接 return，什么都不做
		return a.state(), nil
	}
	if err := writeNodeToFile(n, out); err != nil {
		return nil, err
	}
	return a.state(), nil
}

// defaultSaveName 算出保存对话框里的默认文件名。
//
// 沿用原版 doSaveFile 的算法：
//
//	String name = FileNameUtil.getName(FileNameUtil.getFileName(node.getName()));
//	// ext == null → name；否则 name + '.' + ext
//
// 由于 ext 取的就是节点自己的后缀，结果通常等于节点名本身
// （`Foo.class` → `Foo` + `.` + `class`）。
func defaultSaveName(n *tree.Node) string {
	base := util.Name(util.FileName(n.Name))
	ext := util.Ext(n.Name)
	if ext == "" {
		return base
	}
	return base + string(util.ExtSeparator) + ext
}

// writeNodeToFile 把节点内容原样写到磁盘。
//
// 抽出来是为了可测：SaveNodeAs 本身要弹系统对话框，单测跑不了，
// 而"内容有没有写对"才是真正要钉住的部分。
func writeNodeToFile(n *tree.Node, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	werr := n.ConsumeBytes(func(buf []byte) error {
		_, e := f.Write(buf)
		return e
	})
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("写出文件失败: %w", werr)
	}
	if cerr != nil {
		return fmt.Errorf("关闭文件失败: %w", cerr)
	}
	return nil
}

// SaveSourceNodeAs 对应「保存源文件」：把 .class 反编译成 Java 源码后导出。
//
// 移植自 Java HomeTreeView.saveSourceFile →
// `doSaveFile(node, consumer, FileExtConst.JAVA)`。
//
// 与原版的两点差异（见 MIGRATION.md §一）：
//
//  1. 原版先判 `name.endsWith(DOT_CLASS)`，不满足就**静默 return**（连错都不报）。
//     这里同样静默 —— 菜单项本身就只在 .class 上出现，走到这里说明是前端
//     传错了，不值得弹个错打断用户。
//  2. 原版写出用 `content.getBytes()`，即**平台默认编码**（原版 target 是
//     Java 11，中文 Windows 上是 GBK）。这里统一写 UTF-8：GBK 编不出的字符
//     会被写成 '?' 属于有损，UTF-8 无损，且与本工具内「另存为 .java」一致。
func (a *App) SaveSourceNodeAs(side, path string) (*State, error) {
	n, err := a.nodeBySide(side, path)
	if err != nil {
		return nil, err
	}
	if !isClassNode(n) {
		return a.state(), nil
	}
	// 反编译失败要**报错**，不能像编辑器那样降级 —— 原版 ClassContentParser.parse
	// 抛异常后由全局异常处理器接住，用户是能看见的。导出个空文件才是坑。
	data, err := sourceBytes(n)
	if err != nil {
		return nil, err
	}
	out, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存文件",
		DefaultFilename: sourceSaveName(n),
		Filters: []runtime.FileFilter{
			{
				DisplayName: "JAVA 文件",
				Pattern:     "*." + constant.ExtJava,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if util.IsBlank(out) {
		// 用户取消：原版 doSaveFile 里 `if (file == null) return;`，什么都不做
		return a.state(), nil
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return nil, fmt.Errorf("写出文件失败: %w", err)
	}
	return a.state(), nil
}

// isClassNode 判断节点是不是可以反编译的 .class。
//
// 抽出来是为了让「菜单项显不显示」和「动作做不做」用**同一个判据** ——
// 两处各写一遍 endsWith 迟早会漂移。
func isClassNode(n *tree.Node) bool {
	return n != nil && !n.Dir() && strings.HasSuffix(n.Name, constant.DotClass)
}

// sourceBytes 反编译节点并把源码编码成待写出的字节。
//
// 抽出来是为了可测：SaveSourceNodeAs 要弹系统对话框，单测跑不了，
// 而"反编译结果对不对、编码对不对"才是真正要钉住的部分。
func sourceBytes(n *tree.Node) ([]byte, error) {
	src, err := decompileNode(n)
	if err != nil {
		return nil, err
	}
	return []byte(src), nil
}

// sourceSaveName 算出「保存源文件」对话框里的默认文件名。
//
// 沿用原版 `doSaveFile(node, _, "java")` 的算法：
//
//	String name = FileNameUtil.getName(FileNameUtil.getFileName(node.getName()));
//	fileChooser.setInitialFileName(name + FileNameUtil.C_EXT_SEPARATOR + ext);
//
// 也就是把后缀换成 .java（`Foo.class` → `Foo.java`），而不是在后面再追加 ——
// 原版先 getName() 把原后缀去掉，这里靠 util.Name 做同一件事。
func sourceSaveName(n *tree.Node) string {
	return util.Name(util.FileName(n.Name)) + string(util.ExtSeparator) + constant.ExtJava
}

// PatchSignView 是「查看签名」弹窗的内容。
type PatchSignView struct {
	Name string `json:"name"`
	Md5  string `json:"md5"`
	Sha1 string `json:"sha1"`
}

// GetPatchSign 对应「查看签名」。
//
// 移植自 Java handleViewPatchSign → new PatchSignView(patchRootInfo.getPatchSign())。
// Java 那边 patchSign 为 null 会 NPE（被全局异常处理器吃掉），这里给个明确错误。
func (a *App) GetPatchSign(path string) (*PatchSignView, error) {
	n, err := a.patchNode(path)
	if err != nil {
		return nil, err
	}
	if !tree.IsRootNode(n) {
		return nil, fmt.Errorf("只有根节点才有签名信息")
	}
	info := a.patchTreeInfo.RootInfoByNode(n)
	if info == nil || info.PatchSign == nil {
		return nil, fmt.Errorf("该补丁包没有签名信息")
	}
	return &PatchSignView{
		Name: info.PatchSign.Name,
		Md5:  info.PatchSign.Md5,
		Sha1: info.PatchSign.Sha1,
	}, nil
}

// GetNodeMappedText 对应「查看绑定信息」，返回弹窗里那段文本。
//
// 移植自 Java AbsNodeMappedView.buildMappedInfo / buildDelInfo。
// 文本分两段，删除清单在前、映射清单在后，中间一个换行 —— 对应原版
// updateText(true) 里的 `delInfo + '\n' + mappedInfo`；某一段为空时不留空行。
func (a *App) GetNodeMappedText(path string) (string, error) {
	n, err := a.patchNode(path)
	if err != nil {
		return "", err
	}
	if !tree.IsRootNode(n) {
		return "", fmt.Errorf("只能查看根节点的绑定信息")
	}
	if a.appTreeInfo == nil {
		return "", fmt.Errorf("还没有打开应用包")
	}

	var sb strings.Builder
	buildDelInfo(&sb, a.appTreeInfo.RootNode())
	del := sb.String()
	sb.Reset()
	buildMappedInfo(&sb, n)
	mapped := sb.String()

	switch {
	case util.IsBlank(del):
		return mapped, nil
	case util.IsBlank(mapped):
		return del, nil
	}
	return del + "\n" + mapped, nil
}

// buildDelInfo 收集应用树里所有 DEL 节点，每个一行。
//
// 移植自 Java AbsNodeMappedView.buildDelInfo（含那两个重载）。
// 路径栈只在遇到"目录或压缩包"时压入，并且压缩包名字要去掉版本号 ——
// 这样 `lib/foo-1.0.0.jar` 里的类会显示成 `lib/foo/...`。
func buildDelInfo(sb *strings.Builder, root *tree.Node) {
	if root == nil || len(root.Children) == 0 {
		return
	}
	var stack []string
	for _, child := range root.Children {
		buildDelInfoNode(sb, child, &stack)
	}
}

func buildDelInfoNode(sb *strings.Builder, node *tree.Node, stack *[]string) {
	if node.Type == constant.TreeNodeTypeDel {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(changeCodeDel)
		if len(*stack) > 0 {
			sb.WriteString(strings.Join(*stack, util.Separator))
			sb.WriteString(util.Separator)
		}
		sb.WriteString(node.Name)
		return
	}
	if len(node.Children) == 0 {
		return
	}
	if node.Dir() || tree.IsCompressNode(node) {
		*stack = append(*stack, nameWithoutVersion(node.Name))
		for _, child := range node.Children {
			buildDelInfoNode(sb, child, stack)
		}
		*stack = (*stack)[:len(*stack)-1]
		return
	}
	for _, child := range node.Children {
		buildDelInfoNode(sb, child, stack)
	}
}

// buildMappedInfo 收集补丁树里已建立映射的节点，每行形如
// `<标记><包内路径>\t<对应的应用侧路径>`。
//
// 移植自 Java AbsNodeMappedView.buildMappedInfo（含两个重载）。
func buildMappedInfo(sb *strings.Builder, root *tree.Node) {
	if root == nil || len(root.Children) == 0 {
		return
	}
	var stack []string
	for _, child := range root.Children {
		buildMappedInfoNode(sb, child, &stack)
	}
}

func buildMappedInfoNode(sb *strings.Builder, node *tree.Node, stack *[]string) {
	if node.Dir() {
		if len(node.Children) == 0 {
			return
		}
		*stack = append(*stack, node.Name)
		for _, child := range node.Children {
			buildMappedInfoNode(sb, child, stack)
		}
		*stack = (*stack)[:len(*stack)-1]
		return
	}
	if node.MappedNode == nil {
		return
	}
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	if node.Type == constant.TreeNodeTypeAdd {
		sb.WriteString(changeCodeAdd)
	} else {
		sb.WriteString(changeCodeMod)
	}
	if len(*stack) > 0 {
		sb.WriteString(strings.Join(*stack, util.Separator))
		sb.WriteString(util.Separator)
	}
	sb.WriteString(node.Name)

	// 对应的应用侧路径：从映射到的那一头往上收集到（不含）根。
	// 第一段**保留原样**（可能带版本号的 jar 名），其余段去掉版本号 ——
	// 原版注释写的是"解决目标文件是 jar 文件导致版本号被移出问题"。
	var paths []string
	target := node.MappedNode
	paths = append(paths, target.Name)
	for cur := target.Parent; cur != nil && cur.Type != constant.TreeNodeTypeRoot; cur = cur.Parent {
		paths = append(paths, nameWithoutVersion(cur.Name))
	}
	if len(paths) > 0 {
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
		sb.WriteByte('\t')
		sb.WriteString(strings.Join(paths, util.Separator))
	}
}

// jarVerPattern 对应 Java AbsNodeMappedView 里的 `-[0-9.]+-.*`。
var jarVerPattern = regexp.MustCompile(`-[0-9.]+-.*`)

// nameWithoutVersion 去掉 jar 名里的版本段，其余名字原样返回。
// 对应 Java AbsNodeMappedView.getNameWithoutVersion。
func nameWithoutVersion(name string) string {
	if strings.HasSuffix(name, constant.DotJar) {
		return jarVerPattern.ReplaceAllString(name, "")
	}
	return name
}

// SaveMappedInfoAs 对应绑定信息弹窗里的「保存为文件」。
//
// 文本在这里**重新生成**而不是让前端回传：生成规则只有一份（就是
// GetNodeMappedText），前端不必也不该参与。
func (a *App) SaveMappedInfoAs(path string) error {
	text, err := a.GetNodeMappedText(path)
	if err != nil {
		return err
	}
	// 默认文件名沿用原版：设置里的说明文件名，为空则用 README.txt
	def := a.settings.ReadmeFile
	if util.IsBlank(def) {
		def = constant.ReadmeFile
	}
	out, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存为文件",
		DefaultFilename: def,
		Filters: []runtime.FileFilter{
			{DisplayName: "README FILE", Pattern: "*.txt"},
		},
	})
	if err != nil {
		return err
	}
	if util.IsBlank(out) {
		return nil // 用户取消
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return fmt.Errorf("写出文件失败: %w", err)
	}
	return nil
}

// CopyMappedInfo 对应绑定信息弹窗里的「拷贝信息」。
func (a *App) CopyMappedInfo(path string) error {
	text, err := a.GetNodeMappedText(path)
	if err != nil {
		return err
	}
	return runtime.ClipboardSetText(a.ctx, text)
}

// OpenInExplorer 对应「资源管理器中查看」。
//
// 移植自 Java handleOpenInExplorer → FileUtil.openFolder / selectFile。
// Windows 下目录用 `explorer <path>`，文件用 `explorer /e,/select,<path>`
// （打开所在目录并选中该文件）。两棵树都有这一项。
func (a *App) OpenInExplorer(side, path string) error {
	n, err := a.nodeBySide(side, path)
	if err != nil {
		return err
	}
	diskPath := n.FilePath()
	if util.IsBlank(diskPath) {
		// 压缩包条目没有磁盘路径，选不中。菜单里已经挡掉了，
		// 走不到；真到了就给个明确错误，别静默什么都不做。
		return fmt.Errorf("节点 %s 不在磁盘上（压缩包条目），无法在资源管理器中定位", path)
	}
	args := []string{diskPath}
	if !n.Dir() {
		args = []string{"/e,/select," + diskPath}
	}
	cmd := exec.Command("explorer.exe", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开资源管理器失败: %w", err)
	}
	return nil
}

// ClosePatchPack 对应「关闭补丁」：把补丁包关掉、相关映射清干净。
//
// 移植自 Java handleClosePatch。注意清映射是**先于**置空 treeInfo 做的
// （cleanPatchMappedNode 要用旧的 rootNode）。
//
// 原版最后还会 setLastPatchPackPath(null) 落盘；GUI 这边的设置项目前都还是
// 会话级的（见 ApplySettings 的说明），没有配置文件可写，故略过。
func (a *App) ClosePatchPack() (*State, error) {
	if a.patchTreeInfo != nil {
		if root := a.patchTreeInfo.RootNode(); root != nil {
			service.CleanMappedNode(a.total, root, false)
		}
	}
	a.patchTreeInfo = nil
	a.patchPath = ""
	return a.state(), nil
}

// CloseAppPack 对应应用树的「关闭应用包」。
//
// 移植自 Java HomeLeftTreeView.handleCloseApp：清 treeInfo、清树、清统计。
// 原版最后还 setLastAppPackPath(null) 落盘，GUI 这边设置项是会话级的，略过
// （与 ClosePatchPack 同一个理由）。
//
// 顺序上**先清映射再置空**，和 ClosePatchPack 一致 —— 清映射要用旧的 rootNode。
func (a *App) CloseAppPack() (*State, error) {
	if a.appTreeInfo != nil {
		if root := a.appTreeInfo.RootNode(); root != nil {
			service.CleanMappedNode(a.total, root, false)
		}
	}
	a.appTreeInfo = nil
	a.appPath = ""
	// 对应原版 `APP_TREE_REFRESHING -> totalInfo::cleanTotal`，
	// 不清的话上一份包的计数会留在状态栏上。
	a.total.CleanTotal()
	return a.state(), nil
}

// PathTarget 是「按包内路径定位替换目标」的结果。
//
// 四种情形要分开表达，因为下一步动作完全不同：
//   - 正常文件 → 可以替换；
//   - Dir（目录）→ 能定位，但**不能**当替换目标；
//   - Placeholder（MappedNode）→ 树上看得见，但包里没有，替换等于新增；
//   - Missing → 路径根本不存在。
//
// 后三种都**不算错误**：用户手输路径打错字、粘了个目录进来都是常态，
// 回一个能解释的结果比抛错好。
type PathTarget struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Dir         bool   `json:"dir,omitempty"`
	Placeholder bool   `json:"placeholder,omitempty"`
	Missing     bool   `json:"missing,omitempty"`
}

// ResolveAppPath 按用户粘贴的包内路径定位替换目标。
//
// 用途：按文件名反查不到时的兜底 —— 名字对不上、但知道它在包里哪个位置。
// 也用于"包里有多个同名文件、想精确指定其中一个"。
//
// 容错做在这里而不是前端：用户从资源管理器 / 解压软件复制出来的路径常常带
// 引号、反斜杠、甚至带盘符和上级目录，直接拿去查必然查不到。
func (a *App) ResolveAppPath(raw string) (*PathTarget, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包，先打开要打补丁的 jar")
	}
	p := normalizeInsidePath(raw)
	if p == "" {
		return nil, fmt.Errorf("路径为空")
	}
	n, err := a.resolveNode(treeSideApp, p)
	if err != nil || n == nil {
		return &PathTarget{Path: p, Missing: true}, nil
	}
	if n.Dir() {
		// 目录：能定位，但替换的是文件，替换目录没有意义。
		// 单独标出来，前端才能把下一步说清楚 —— 最典型的就是用户右键
		// 「复制路径」复制了一个目录，以为能直接拿去指定替换目标。
		// 不回 Size：目录的 Size 是 -1（tree.SizeUnknown），前端照着显示会变成
		// "未知大小"，反而像出了错。
		return &PathTarget{Path: p, Name: n.Name, Dir: true}, nil
	}
	return &PathTarget{
		Path:        p, // 回原样的定位串：嵌套条目必须靠 `jar!/entry` 才能唯一指向
		Name:        n.Name,
		Size:        n.Size,
		Placeholder: n.Kind == tree.KindMapped,
	}, nil
}

// normalizeInsidePath 把"人复制来的路径"收拾成包内路径。
//
//   - 去掉首尾空白与成对的引号（资源管理器"复制为路径"自带）；
//   - 反斜杠统一成正斜杠；
//   - 去掉开头的 /；
//   - 带上级目录时（`D:/work/x/BOOT-INF/classes/Foo.class`，从解压软件里
//     复制常见），从 `BOOT-INF/` `WEB-INF/` `META-INF/` 这类包根锚点切一刀，
//     只留包内那一段。
//
// 不做更聪明的猜测（比如按文件名模糊匹配）：这里的原则是"用户给什么就查什么"，
// 猜错了反而会替换到别处去。
func normalizeInsidePath(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, `"`)
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ""
	}
	// 只在"前面还有别的东西"时才切，且取**最靠前**的锚点，
	// 这样 `x/BOOT-INF/classes/a/BOOT-INF/b.class` 不会被切错。
	best := -1
	for _, anchor := range []string{"BOOT-INF/", "WEB-INF/", "META-INF/"} {
		if i := strings.Index(p, "/"+anchor); i >= 0 && (best < 0 || i+1 < best) {
			best = i + 1
		}
	}
	if best > 0 {
		p = p[best:]
	}
	return p
}

// LocateNode 在指定树里定位一个节点，不要求它有映射对端。
//
// 与 LocateMapped 的区别就在这里：LocateMapped 是"在**另一棵树**里找这个
// 节点的对应项"，而这里是"就在这棵树上找到它、把祖先链摊开"。
//
// 为「临时补丁」清单加的 —— 那些节点是用户手动标删的，**根本没有映射**
// （标删不建绑定，见 MappedDelNodeWithType 的注释），走 LocateMapped
// 会直接报"没有对应项"。
func (a *App) LocateNode(side, path string) (*LocateResult, error) {
	srcRoot, _, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	// resolveNode 兼容嵌套定位串（`jar!/entry`）：命中嵌套 jar 里的条目时，
	// 会按需展开那个 jar，祖先链自然带上 jar 与中间目录，前端就能逐层展开。
	n, err := a.resolveNode(side, path)
	if err != nil {
		return nil, err
	}
	if n == srcRoot {
		// 根那一行永远可见，没有祖先要展开
		return &LocateResult{State: a.state(), Side: side, Path: n.Path}, nil
	}
	chain := ancestorChain(n, srcRoot)
	if chain == nil {
		return nil, fmt.Errorf("%s 不在当前树结构里，重新打开一次应用包", n.Name)
	}
	// 根那一行不用前端展开，别塞进 ancestors
	ancestors := make([]string, 0, len(chain))
	for _, anc := range chain {
		if anc == srcRoot {
			continue
		}
		ancestors = append(ancestors, anc.Path)
	}
	return &LocateResult{
		State:     a.state(),
		Side:      side,
		Ancestors: ancestors,
		Path:      n.Path,
	}, nil
}

// collectManualDelList 扫一遍应用树，把所有被标成 DEL 的文件收集成清单。
//
// 这就是前端那个"临时补丁"列表的数据源 —— 用户在应用树上标删一批文件后，
// 这里按树的遍历顺序（目录在前、同级按名）给出一个稳定的列表，
// 打补丁时这些文件一个都不会进新包。
//
// 只收**文件**（node.Dir() 为假）：目录被标 DEL 只是写法上的跳过条件，
// 展示出来用户也没法对着一个目录点"定位"，反而杂。
func (a *App) collectManualDelList() []ManualDelItem {
	if a.appTreeInfo == nil {
		return nil
	}
	var out []ManualDelItem
	var walk func(*tree.Node)
	walk = func(n *tree.Node) {
		if n == nil {
			return
		}
		if !n.Dir() && n.Type == constant.TreeNodeTypeDel {
			out = append(out, ManualDelItem{
				Path: n.Path,
				Name: n.Name,
				Size: n.Size,
			})
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(a.appTreeInfo.RootNode())
	return out
}

// SetFocusTree 对应「聚焦补丁」：0 都显示 / 1 只看应用树 / 2 只看补丁树。
//
// 移植自 Java HomeView.focusTree —— 它把另一个面板从 SplitPane 里摘掉。
// 这里只存状态，具体隐藏由前端按这个值做。
func (a *App) SetFocusTree(status int) *State {
	if status != focusTreeApp && status != focusTreePatch {
		status = focusTreeAll
	}
	a.focusTree = status
	return a.state()
}

// findPatchNodeByPath 在补丁树里按 Path 找节点。
//
// Path 在包内唯一（zip 条目名唯一；补丁目录模式是绝对路径），所以拿它当标识。
// 前端只需要回传这个字符串，不必持有后端对象引用。
func findPatchNodeByPath(root *tree.Node, path string) *tree.Node {
	return findTreeNodeByPath(root, path)
}

// findTreeNodeByPath 在任意一棵树里按 Path 找节点（深度优先）。
func findTreeNodeByPath(root *tree.Node, path string) *tree.Node {
	var found *tree.Node
	var walk func(*tree.Node)
	walk = func(n *tree.Node) {
		if n == nil || found != nil {
			return
		}
		if n.Path == path {
			found = n
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return found
}

// findTreeNodeByPathLoose 在 findTreeNodeByPath 之外多容忍"目录尾斜杠的差异"。
//
// 树里目录的 Path 带不带尾部 '/'，取决于**zip 里有没有显式的目录条目**：
// 有条目就是原样的 `a/b/`（见 pack.buildFromZipReader 的保真点），
// 没条目时补建的占位目录则是不带斜杠的 `a/b`（getOrCreateParentNode）。
// 而右键「复制路径」拼的是节点**名字**（nodeDisplayPath），目录名天然不带斜杠。
//
// 于是"复制出来的路径再粘回去"会查不到 —— 用户看到的却是"包里没有这个路径"，
// 明明上一秒还在树上点着它。这是纯粹的表示差异，不是真的没有，必须容错，
// 而且两个方向都要容（粘来的带斜杠、树里不带，以及反过来）。
//
// 只用于**按路径解析**（resolveIn）；其他地方仍走精确匹配，
// 免得"差一个斜杠也算命中"把别处的判断悄悄放宽。
func findTreeNodeByPathLoose(root *tree.Node, path string) *tree.Node {
	if n := findTreeNodeByPath(root, path); n != nil {
		return n
	}
	if strings.HasSuffix(path, "/") {
		return findTreeNodeByPath(root, strings.TrimSuffix(path, "/"))
	}
	return findTreeNodeByPath(root, path+"/")
}

// ---------------------------------------------------------------------------
// 展开嵌套压缩包
// ---------------------------------------------------------------------------

// 树的标识。前端在调用需要"哪一棵树"的方法时回传这两个字符串。
const (
	treeSideApp   = "app"
	treeSidePatch = "patch"
)

// treeRoot 取指定那一侧的根节点，顺带告诉调用方它是应用树还是补丁树。
//
// 返回的 bool 就是 BuildChildrenWithCompress 的 isPatch 参数 —— 它决定
// 展开出来的子节点归哪一侧，不能猜。
func (a *App) treeRoot(side string) (*tree.Node, bool, error) {
	switch side {
	case treeSideApp:
		if a.appTreeInfo == nil {
			return nil, false, fmt.Errorf("还没有打开应用包")
		}
		return a.appTreeInfo.RootNode(), false, nil
	case treeSidePatch:
		if a.patchTreeInfo == nil {
			return nil, false, fmt.Errorf("还没有打开补丁包")
		}
		return a.patchTreeInfo.RootNode(), true, nil
	default:
		return nil, false, fmt.Errorf("未知的树: %s", side)
	}
}

// isCompressNode 判断节点是不是"可以被展开的压缩包"。
//
// 对应 Java TreeNodeUtil.isCompressNode：按**名字后缀**判 .jar/.zip。
// 这里多一条 `!n.Dir()`：目录条目名字恰好以 .jar 结尾时（zip 里能造出来）
// 原版会去调 buildChildrenWithCompress，而目录节点没有内容来源，
// 那一步只会静默失败。与其给前端一个点了没反应的三角，不如一开始就不给。
func isCompressNode(n *tree.Node) bool {
	return n != nil && !n.Dir() && tree.IsCompressName(n.Name)
}

// ExpandNode 展开一个嵌套压缩包节点，返回它展开后的视图。
//
// 移植自原版"双击节点"那一下：
//
//	应用树（HomeLeftTreeView.onMouseClicked）：名字以 .jar 结尾
//	    → AppPackService.buildChildrenWithCompress(node, false)
//	      + TreeNodeUtil.buildNodeChildren(item, node, OnlyChangeFilter)
//	补丁树（HomeRightTreeView.onMouseClicked）：TreeNodeUtil.isCompressNode(node)
//	    → PatchPackService.buildChildrenWithCompress(node, true)
//	      + TreeNodeUtil.buildNodeChildren(item, node)
//
// 两条路径的差别只有 isPatch 这个参数，所以这里合成一个方法。
//
// 两点必须照搬、别"顺手改好"：
//
//  1. **不重跑匹配**。原版展开只是把子级建出来，节点状态保持原样。
//     如果在这里补一次 RefreshMappedNode，那些"匹配时展开过、匹配失败又回滚"
//     的 jar 会突然冒出增/改标记 —— 与原版不一致。
//  2. **原版只看扩展名，不看内容**。一个内容其实是文本的 `foo.jar` 会被
//     原样交给 zip 解析器，失败后界面无反应（异常被全局处理器吃掉）。
//     这里把失败变成一条明确错误，属 §3.9 的刻意偏差。
//
// 返回的是该节点【整个子树】的视图，前端直接替换掉原来那一行下面的容器即可。
func (a *App) ExpandNode(side, path string) (*NodeView, error) {
	root, isPatch, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	n := findTreeNodeByPath(root, path)
	if n == nil {
		return nil, fmt.Errorf("树里找不到节点: %s", path)
	}
	if !isCompressNode(n) {
		return nil, fmt.Errorf("%s 不是压缩包，无法展开", n.Name)
	}
	// Children == nil 才需要展开。已经展开过的（比如匹配阶段下探进去并
	// 保留下来的 jar）直接返回现状 —— 对应 Java buildChildrenWithCompress
	// 里 `if (node.getChildren() != null) return false;` 的效果。
	if n.Children == nil {
		if !service.BuildChildrenWithCompress(n, isPatch) {
			return nil, fmt.Errorf("展开 %s 失败（内容不是有效的压缩包）", n.Name)
		}
	}
	return a.toView(n, n.Parent), nil
}

// ---------------------------------------------------------------------------
// 在另一棵树里定位
// ---------------------------------------------------------------------------

// LocateResult 是「在另一棵树里定位」的结果。
type LocateResult struct {
	// State 定位之后的最新完整状态 —— 沿途的祖先可能被现场展开过，树变了。
	State *State `json:"state"`

	// Side 目标节点所在的树（"app" / "patch"）。
	Side string `json:"side"`

	// Ancestors 目标节点的祖先路径，**从根的直接子级往下**排。
	// 前端按这个顺序依次展开，最后再滚到 Path 那一行。
	Ancestors []string `json:"ancestors"`

	// Path 目标节点自身的路径。
	Path string `json:"path"`
}

// LocateMapped 在另一棵树里找出这个节点的对应项，并把它沿途的祖先路径给出来。
//
// 对应关系来自匹配阶段建立的 MappedNode **双向**绑定（见 internal/tree/util.go
// 里成对调用的 setMappedNode），所以两个方向都查得到。用户要的是"在补丁树点
// 文件、应用树里定位"，接口仍做成带 side 的双向形式 —— 反过来用只是换个值。
//
// 为什么这件事得后端算，前端自己走不下去：判定"到底有没有对应项"要看
// MappedNode，前端拿不到。祖先链也得后端给 —— 前端只有一份懒渲染的 DOM，
// 没展开的层级连行都没有，靠自己推不出路径的下一跳。
//
// **这里不需要"展开祖先"这一步。** 映射存在本身就说明匹配阶段下探过那一支，
// 沿途每个祖先的子级都已经建好了：`m.Parent` 指向 p，就意味着 p 的 Children
// 里有 m。所以后端只管把祖先路径报出来，展开交给前端 —— 它那边是懒渲染的，
// 展开本来就是它的事（`st.toggle(true)`，子级已备好，同步完成）。
//
// 返回定位之后的最新状态，调用方整体重渲染即可，不必自己拼补丁。
func (a *App) LocateMapped(side, path string) (*LocateResult, error) {
	srcRoot, srcIsPatch, err := a.treeRoot(side)
	if err != nil {
		return nil, err
	}
	n := findTreeNodeByPath(srcRoot, path)
	if n == nil {
		return nil, fmt.Errorf("树里找不到节点: %s", path)
	}
	m := n.MappedNode
	if m == nil {
		return nil, fmt.Errorf("%s 在%s里没有对应项", n.Name, oppositeTreeName(srcIsPatch))
	}

	targetSide := treeSidePatch
	if srcIsPatch {
		targetSide = treeSideApp
	}
	targetRoot, _, err := a.treeRoot(targetSide)
	if err != nil {
		return nil, err
	}

	// 目标就是根：它那一行永远可见，没有祖先要展开。
	if m == targetRoot {
		return &LocateResult{State: a.state(), Side: targetSide, Path: m.Path}, nil
	}

	chain := ancestorChain(m, targetRoot)
	if chain == nil {
		// 匹配回滚用的是浅拷贝，绑定里的引用有极小概率已经不挂在树上。
		// 这种时候返回一条断掉的祖先链会让前端去展开不存在的行，
		// 不如明确报错、让用户重新匹配一次。
		return nil, fmt.Errorf("%s 的绑定关系已失效（不在%s上），重新匹配一下", m.Name, oppositeTreeName(srcIsPatch))
	}

	// 根那一行永远可见，不用前端展开，别塞进 ancestors。
	ancestors := make([]string, 0, len(chain))
	for _, anc := range chain {
		if anc == targetRoot {
			continue
		}
		ancestors = append(ancestors, anc.Path)
	}

	return &LocateResult{
		State:     a.state(),
		Side:      targetSide,
		Ancestors: ancestors,
		Path:      m.Path,
	}, nil
}

// ancestorChain 从 node 往上回溯到 root，返回 [root, ..., node.Parent]（浅 -> 深）。
// 回溯不到 root（绑定失效、节点已不在树上）返回 nil。
func ancestorChain(node, root *tree.Node) []*tree.Node {
	var chain []*tree.Node
	for p := node.Parent; p != nil; p = p.Parent {
		chain = append([]*tree.Node{p}, chain...)
	}
	if len(chain) == 0 || chain[0] != root {
		return nil
	}
	return chain
}

// oppositeTreeName 给错误信息用的树名：源在补丁树就说"应用树"，反之亦然。
func oppositeTreeName(srcIsPatch bool) string {
	if srcIsPatch {
		return "应用树"
	}
	return "补丁树"
}

// ---------------------------------------------------------------------------
// 匹配
// ---------------------------------------------------------------------------

// refresh 在两个包都已打开时跑一遍匹配流程，并回一个完整状态。
//
// 顺序严格对齐 CLI 的 cmdPatch（也就是 Java HomeContext 的那一套）：
// 对每个补丁根（自定义根优先，没有就用主根）依次
// refreshReadmeNode → 清空 diffInfo → refreshMappedNode。
func (a *App) refresh() (*State, error) {
	if a.appTreeInfo == nil || a.patchTreeInfo == nil {
		return a.state(), nil
	}

	a.progress = nil
	a.refreshAllRoots(a.step())

	return a.state(), nil
}

// refreshRoot 对单个补丁根跑一轮"读 README + 重新映射"。
//
// 对应 Java HomeRightTreeView.refreshPatchMappedNode(isRefreshReadme, rootNode)：
// 先按需 refreshReadmeNode，再 setPatchDiffInfo(null)，最后 refreshMappedNode。
//
// withReadme 为 false 时**跳过读 README**，只重跑映射 —— 菜单里的
// 「刷新信息」走的就是这条路（原版传的 false）。别统一成"都重读"。
//
// 应用包没打开时不炸：Java 那句 setPatchDiffInfo(null) 会 NPE（被全局异常
// 处理器吃掉，界面无反应），而 Go 里是 panic、会终止进程。标记根节点时应用包
// 完全可以还没打开，所以这里必须挡一下 —— 属 §3.9 的必要偏差。
func (a *App) refreshRoot(rootNode *tree.Node, st service.Step, withReadme bool) {
	if withReadme {
		service.RefreshReadmeNode(a.patchTreeInfo, rootNode, a.settings.ReadmeFile, st)
	}
	if a.appTreeInfo != nil {
		a.appTreeInfo.SetPatchDiffInfo("")
	}
	service.RefreshMappedNode(a.total, a.appTreeInfo, a.patchTreeInfo, rootNode, a.patchOptions(st))
}

// refreshAllRoots 对每个补丁根各跑一轮 —— 有自定义根就逐个刷，否则刷主根。
//
// 对应 Java refreshPatchMappedNode(boolean) 那个不带根参数的重载。
// 注意它的行为：**一旦存在自定义根，主根就不再被刷新了**。
// 这是上游既有行为，照搬。
func (a *App) refreshAllRoots(st service.Step) {
	roots := a.patchTreeInfo.CustomRootInfoMap()
	if len(roots) == 0 {
		a.refreshRoot(a.patchTreeInfo.RootNode(), st, true)
		return
	}
	for _, entry := range roots {
		a.refreshRoot(entry.Node, st, true)
	}
}

func (a *App) patchOptions(st service.Step) service.PatchOptions {
	return service.PatchOptions{
		WithPath:       a.settings.PathMatch,
		WithName:       a.settings.FileNameMatch,
		PatchFileRegex: a.settings.PatchFileRegex,
		PatchFileDiff:  a.settings.PatchFileDiff,
		LangCode:       config.New().Genera.Language,
		Step:           st,
	}
}

// step 返回一个把进度记进 a.progress 的回调，最多保留最近 300 条。
func (a *App) step() service.Step {
	return func(msg string) {
		a.progress = append(a.progress, msg)
		if len(a.progress) > 300 {
			a.progress = a.progress[len(a.progress)-300:]
		}
	}
}

// ---------------------------------------------------------------------------
// 打补丁
// ---------------------------------------------------------------------------

// RunPatch 写出打过补丁的应用包。
//
// **不在这里重跑匹配** —— 这是刻意的，与 Java 版一致：原版点"保存/打补丁"
// 只是把当前的匹配结果写出去，重新匹配由"重新匹配"按钮显式触发。
//
// 早期版本图省事在保存前又跑了一遍 refresh，结果写出的统计里"删除"变成 0。
// 原因是重复匹配并非幂等（详见 App.total 的说明），而且 Java 也不会这么做。
// 改设置后需要用户点一次"重新匹配"，界面上有对应提示。
//
// **没有补丁包时也允许执行** —— 这条是后来放开的：用户最常见的用法是
// "只打开一个 jar，标记删掉几个文件，然后输出新包"，此时并没有补丁包。
// 后端 SaveAppPack 本就支持 PatchTreeInfo=nil（Writer 的注释写的
// "不打补丁、只是重打包"），挡在 GUI 层属于自找的阻塞。
// 这种"纯重打包"模式下，应用树上被标 DEL 的节点依旧会被排除、照记 delInfos。
func (a *App) RunPatch(outPath string) (*PatchResult, error) {
	if a.appTreeInfo == nil {
		return nil, fmt.Errorf("还没有打开应用包")
	}

	target := outPath
	if util.IsBlank(target) {
		target = a.defaultOutputPath()
	}

	st := a.step()
	ctx := &service.WriteContext{
		AppTreeInfo:    a.appTreeInfo,
		PatchTreeInfo:  a.patchTreeInfo,
		Total:          a.total,
		WritePatchSign: a.settings.WritePatchSign,
		Username:       a.settings.EffectiveUsername(util.GetUserName()),
		ReadmeFile:     a.settings.ReadmeFile,
		Step:           st,
	}
	if err := service.SaveAppPack(ctx, target); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}

	// 产物落地后删掉本包的持久化记录：它此时已经"写实"到磁盘的 jar 里，
	// 下次打开不会再从树上重删一次（否则 manualDel / del 计数叠加，
	// 而新包本身已经没有那些文件了，重复删只会让统计虚高）。
	//
	// 输出到**别的路径**时同理 —— 用户接着打开新包时它上一次的删除
	// 已验证过；带着旧记录反而会让新包再被删一遍。清掉最干净。
	if a.manualDelStore != nil && a.appPath != "" {
		if err := a.manualDelStore.Clear(a.appPath); err != nil {
			a.step()("manualdel: 清理记录失败（不影响已保存的产物）: " + err.Error())
		}
	}

	return &PatchResult{
		Output:   target,
		Total:    a.state().Total,
		DiffInfo: a.appTreeInfo.PatchDiffInfo(),
	}, nil
}

// defaultOutputPath 返回默认输出路径 —— **就是应用包自己的路径**。
//
// 与 Java 版一致：原版 `HomeView.handleSaveAppPack` 用
// `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())`，
// 初始文件名就是应用包的文件名、初始目录就是应用包所在目录，**不带任何后缀**。
// 这里曾自行拼过 "-patched"，属于移植时多加的，已去掉。
//
// 只在调用方没给输出路径时兜底（GUI 正常路径由前端把建议名传给 PickSavePath）。
// 与源文件同名时由系统保存对话框提示覆盖 —— 原版就是这个行为。
func (a *App) defaultOutputPath() string {
	if util.IsBlank(a.appPath) {
		return ""
	}
	return a.appPath
}

// ---------------------------------------------------------------------------
// 状态组装
// ---------------------------------------------------------------------------

// state 把当前持有的东西翻译成给前端的 JSON。
//
// 统计一律取自 a.total（会话级唯一实例），不从参数传 —— 早期版本让调用方
// 传 total，结果传 nil 的那些调用点会静默地回一份全 0 的统计。
func (a *App) state() *State {
	// 菜单要按它决定「恢复原状」出不出现，逐节点判断太贵，先算一次（见字段注释）
	a.manualDelPendingNow = a.appTreeInfo != nil && a.manualDelPending()
	s := &State{
		AppPath:   a.appPath,
		PatchPath: a.patchPath,
		Settings:  a.settingsView(),
		Progress:  append([]string(nil), a.progress...),
		Total: PatchTotal{
			Add:       a.total.AddTotal(),
			Mod:       a.total.ModTotal(),
			Del:       a.total.DelTotal(),
			ManualDel: a.total.ManualDelTotal(),
		},
	}
	if a.appTreeInfo != nil {
		root := a.appTreeInfo.RootNode()
		s.AppTree = a.toView(root, nil)
		s.AppStats = packStats(root)
		s.DiffInfo = a.appTreeInfo.PatchDiffInfo()
		s.ManualDelList = a.collectManualDelList()
		s.ManualDelPending = a.manualDelPendingNow
	}
	if a.patchTreeInfo != nil {
		root := a.patchTreeInfo.RootNode()
		s.PatchTree = a.toView(root, nil)
		s.PatchStats = packStats(root)
		s.ReadmeText = a.patchTreeInfo.ReadmeText()
	}
	// 两侧都打开才算匹配过
	s.Matched = a.appTreeInfo != nil && a.patchTreeInfo != nil
	s.FocusTree = a.focusTree
	if a.linksEnabled() {
		s.MappedLinks = a.collectMappedLinks()
	}
	return s
}

// linksEnabled 决定这次状态里要不要带匹配连线。
//
// 设置里的「总是显示匹配连线」关掉之后，连线跟随「开启补丁文件比对」——
// 比对关着时补丁树那侧本来就不表达"改动"，画出来只是挡视线。
func (a *App) linksEnabled() bool {
	return a.showMappedLinks || a.settings.PatchFileDiff
}

// domRoot 是 dom 路径的起点（两棵树的根节点都用它，因为两棵树分开索引）。
const domRoot = "r"

// collectDomPaths 给一棵树里的每个节点算出 dom 路径。
//
// 只是"第几个子节点"的链，与展开状态无关 —— 前端按同一套规则算，
// 所以收起/展开不会让已有的连线错位。
func collectDomPaths(n *tree.Node, dom string, out map[*tree.Node]string) {
	if n == nil {
		return
	}
	out[n] = dom
	for i, c := range n.Children {
		collectDomPaths(c, dom+"/"+strconv.Itoa(i), out)
	}
}

// collectMappedLinks 收集所有"补丁文件 ←→ 应用文件"的配对。
//
// 只连文件、不连目录：目录的映射是匹配过程的中间产物（"这一片都在补丁里"），
// 连出来是一堆伞骨，没有信息量。目录对上时它下面的文件各自有线，已经够了。
func (a *App) collectMappedLinks() []MappedLink {
	if a.appTreeInfo == nil || a.patchTreeInfo == nil {
		return nil
	}
	appDom := make(map[*tree.Node]string, 1024)
	collectDomPaths(a.appTreeInfo.RootNode(), domRoot, appDom)

	var links []MappedLink
	var walk func(n *tree.Node, dom string)
	walk = func(n *tree.Node, dom string) {
		if n == nil {
			return
		}
		// 对端必须在应用树里（appDom 里有它），且两边都是文件 ——
		// 补丁里一个文件对到应用树一个目录上是不可能的，但防御一下：
		// 树是懒展开的，MappedNode 的构建依赖当时展开到哪儿。
		if n.MappedNode != nil && !n.Dir() && !n.MappedNode.Dir() {
			if ad, ok := appDom[n.MappedNode]; ok {
				links = append(links, MappedLink{AppDom: ad, PatchDom: dom})
			}
		}
		for i, c := range n.Children {
			walk(c, dom+"/"+strconv.Itoa(i))
		}
	}
	walk(a.patchTreeInfo.RootNode(), domRoot)
	return links
}

// GetState 返回当前状态（前端刷新时用）。
//
// 注意它**不跑匹配** —— 只把当前持有的树翻译成 JSON。改完设置想看到效果
// 要用 ReMatch。
func (a *App) GetState() *State {
	// 第一次取状态时把启动参数指定的包打开（与 OnStartup 共用 ensureStartup，
	// 用 startupOnce 保证阻塞到加载完成），保证前端拿到的是已加载的树而非空状态。
	a.ensureStartup()
	return a.state()
}

// ReMatch 重跑一遍匹配流程，对应界面上的"重新匹配"按钮。
//
// 之所以单独开一个方法而不是复用 GetState：匹配会改动应用树上的节点状态
// （ADD/MOD/DEL），是个有副作用的操作，不该藏在"取状态"里。
func (a *App) ReMatch() (*State, error) { return a.refresh() }

// canMarkRoot 对应原版补丁树右键菜单「标记根节点」的显示条件
// （HomeRightTreeView.buildPatchTreeContextMenu 的第 3 个 claim）。
func canMarkRoot(n *tree.Node) bool {
	return n.Type != constant.TreeNodeTypeRoot &&
		len(n.Children) > 0 &&
		(n.Dir() || tree.IsCompressNode(n)) &&
		tree.IsNotUnderCustomRoot(n)
}

// canMarkReadme 对应「标记说明文件」的显示条件（第 4 个 claim）。
//
// 条件里那句 `isRootNode(node.getParent())` 很关键：**只有直接挂在根下的
// 普通文件才允许标记**。所以对"README 套在一层目录里"的包，得先把它那层
// 目录标成根，这个菜单项才会出现 —— 顺序不能反。
func canMarkReadme(n *tree.Node, parent *tree.Node) bool {
	return !n.Dir() &&
		n.Type == constant.TreeNodeTypeNone &&
		tree.IsRootNode(parent)
}

// toView 把内部节点翻译成前端形态。
//
// 是 App 的方法而不是自由函数，因为补丁树要顺带把右键菜单算出来 ——
// 菜单的显示条件要用到"两个包是否打开""有没有自定义根""当前聚焦状态"，
// 这些都在 App 上。
func (a *App) toView(n *tree.Node, parent *tree.Node) *NodeView {
	if n == nil {
		return nil
	}
	v := &NodeView{
		Name:     n.Name,
		Path:     n.Path,
		Dir:      n.Dir(),
		Size:     n.Size,
		Type:     typeName(n.Type),
		ModTime:  formatTime(n.ModifyTime()),
		Md5:      n.GetMd5OrInit(),
		Compress: isCompressNode(n),
		Icon:     iconName(n),
		Mapped:   n.MappedNode != nil,
		Edited:   n.Edited(),
	}
	// 右键菜单两棵树都有。补丁树那套是完整移植的 11 项；
	// 应用树那套只做了本轮用得上的几项，其余登记在 MIGRATION.md §四。
	if n.IsPatch {
		v.Menu = a.patchMenu(n, parent)
	} else {
		v.Menu = a.appMenu(n, parent)
	}
	if len(n.Children) > 0 {
		// 显示顺序：目录在前，同级按名字（忽略大小写）。
		//
		// **只排视图，不动 n.Children** —— 树里的顺序是 zip 条目在包里的物理
		// 顺序，重打包时按它写回（见 pack.buildFromZipReader 末尾那段"不排序"）。
		// 之前界面显示的就是这个物理顺序，而它由打包工具决定，在 fat-jar 的
		// `BOOT-INF/lib` 下看就是一堆乱序（用户 2026-10-28 报障"文件排序是乱的"）。
		//
		// 顺带说明为什么原版也是乱的：Java 的 SortLinkedList.add 里那段有序插入
		// 被注释掉了，只剩纯追加 —— 所以"乱"是原版行为，不是移植漏了。
		kids := make([]*tree.Node, len(n.Children))
		copy(kids, n.Children)
		sort.SliceStable(kids, func(i, j int) bool { return lessForDisplay(kids[i], kids[j]) })
		v.Children = make([]*NodeView, 0, len(kids))
		for _, c := range kids {
			v.Children = append(v.Children, a.toView(c, n))
		}
	}
	return v
}

// lessForDisplay 是同级节点的**显示**顺序：目录在前，其余按名字（忽略大小写）。
//
// 只比名字不比路径：兄弟节点的目录前缀相同，比名字就够了，而且这样目录才能
// 整体排在文件前面（`BOOT-INF/classes`、`BOOT-INF/lib` 在 `xxx.jar` 之前）。
//
// 中文名按 UTF-8 字节序排（≈ Unicode 序），不是拼音序 —— 与"目录树里按名字
// 排"的直觉一致，也不依赖任何 collator。名字忽略大小写后仍相同（`Foo.jar`
// 与 `foo.jar`）时用原始名兜底，保证顺序稳定。
func lessForDisplay(x, y *tree.Node) bool {
	if x.Dir() != y.Dir() {
		return x.Dir()
	}
	lx, ly := strings.ToLower(x.Name), strings.ToLower(y.Name)
	if lx != ly {
		return lx < ly
	}
	return x.Name < y.Name
}

func packStats(root *tree.Node) PackStats {
	var s PackStats
	var walk func(*tree.Node)
	walk = func(n *tree.Node) {
		for _, c := range n.Children {
			if c.Dir() {
				s.Dirs++
			} else {
				s.Files++
				if c.Size > 0 {
					s.TotalSize += c.Size
				}
				switch {
				case hasSuffixFold(c.Name, constant.DotClass):
					s.Classes++
				case hasSuffixFold(c.Name, constant.DotJar):
					s.Jars++
				}
			}
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return s
}

// typeName 把节点状态翻成前端用的短字符串，前端据此上色。
func typeName(t constant.TreeNodeType) string {
	switch t {
	case constant.TreeNodeTypeAdd:
		return "add"
	case constant.TreeNodeTypeMod:
		return "mod"
	case constant.TreeNodeTypeDel:
		return "del"
	case constant.TreeNodeTypeManualDel:
		return "manualDel"
	case constant.TreeNodeTypeReadme:
		return "readme"
	case constant.TreeNodeTypeRoot:
		return "root"
	case constant.TreeNodeTypeCustomRoot:
		return "customRoot"
	default:
		return "none"
	}
}

func hasSuffixFold(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
