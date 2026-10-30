# Java → Go 移植方案与进度

原项目：`PatchAssistant2J` v1.4.7（JavaFX 19 + GluonFX），源码见 `../src-java`。
本文记录移植的技术决策、必须留意的上游行为，以及分阶段计划。

---

## 一、现状

### 已完成（第一阶段：地基）

| 模块 | 状态 | 说明 |
|---|---|---|
| `internal/constant` | ✅ | 全部常量与枚举 |
| `internal/model` | ✅ | 9 个模型类 |
| `internal/util` | ✅ | 字符串/路径/哈希/字符集/系统（非 UI 部分） |
| `internal/config` | ✅ | `Configuration` + 四个子配置 |
| `internal/tree` | ✅ | 树节点、同级顺序（**插入序，不排序** —— 见 §3.10）、惰性内容读取 |
| `internal/pack` | ✅ | zip/jar 展开成树（含内部类归位） |
| `internal/i18n` | ✅ | `go:embed` 打包 i18n 资源 |
| `internal/service` | ✅ | README 解析 + 补丁匹配 + 补丁包写回 + 应用包保存 |
| `internal/decompile` | ✅ | 外挂 JRE 调 Fernflower 反编译 `.class`（见 §2.1） |
| `gui` | 🚧 | 主页已跑通（阶段 3，见下）；设置/搜索/READ ME 编辑等屏待补 |

验证：`go build ./...`、`go vet ./...`、`go test ./...` 全绿。
建树结果已与 Python `zipfile` 交叉核对（226 文件 / 35 目录，逐项一致）。

**跨语言黄金基准已建立并跑通**（`../_extract/golden/run.sh`，见 §3.10、§六）。
两条写出路径（`patch` / `patchreadme`）与**原版 Java** 用同一夹具对跑，
条目名、顺序、解压后内容全部一致；剩余差异均为已登记的、无法消除的字段级差异。

### 阶段 2 已完成（补丁匹配与保存）

| 环节 | Go 位置 | 对应 Java |
|---|---|---|
| 双模式匹配 | `service/match.go` | `PatchMatchProcessor` |
| README 指令映射 | `service/patch.go` | `PatchPackService.refreshMappedNode*` |
| 补丁树 / 应用树建树 | `service/patch.go` / `service/apppack.go` | `PatchPackService.getTreeNode` / `AppPackService.getTreeNode` |
| 补丁差异提示 | `service/apppack.go` | `AppPackService.createPatchDiffInfo` |
| 应用包重打包 + 签名 | `service/writer.go` | `AppPackWriteProcessor` |
| 补丁包 README 写回 | `service/patchsave.go` | `PatchPackService.updatePatchReadme` |
| CLI | `cmd/patchassistant` | ——（原版只有 GUI） |

CLI 现有命令：`list` / `info` / `readme` / `patch` / `patchreadme`。
`patch` 已实跑验证：README 指令匹配 → 改包 → 写 `META-INF/patch-up/<yyyyMMdd>NN.sign`。

### 阶段 3 已开始（GUI 骨架，Wails v2）

选型按 §2.2 的建议走 **Wails v2.15.0 + WebView2**。

| 环节 | Go/前端位置 | 对应 Java |
|---|---|---|
| 应用入口与窗口 | `gui/main.go` | `PatchAssistantApplication` |
| 绑定层（状态 + 动作） | `gui/app.go` | `HomeContext` + `Home*View` 的处理器 |
| 界面 | `gui/frontend/dist/{index.html,app.css,app.js}` | `view/home/HomeLeftTreeView` 等 |
| 树节点图标 | `gui/icon.go` + `gui/frontend/dist/icons/*.png` | `TreeNodeUtil.getIconImage` + `IConUtil.getPathByFileExt`（图也是原版那套） |
| 绑定层测试 | `gui/app_test.go` | ——（原版无测试） |

已实现的交互：打开应用包 / 打开补丁包 / 重新匹配 / 设置 / 执行打补丁，
**把文件直接拖进窗口**（见下），双树展示（目录在前、同级按名不区分大小写、
内部类嵌在外部类下），变更状态着色（新增绿 / 修改黄 / 删除红删除线），
每行带原版那套文件类型图标（见下），**双击文件看内容预览**（双栏差异高亮 +
两栏滚动同步，见下），**在补丁树点文件定位到应用树里的对应项**（见下），
**右键「编辑内容」改包内文件**（文本 / class 源码 / 十六进制，只改内存，见下）。
两棵树都有一套右键菜单（应用树 8 项 / 补丁树 12 项，均由后端算好下发）。

#### 拖放：把文件拖进窗口

对齐原版 `HomeLeftTreeView` / `HomeRightTreeView` 的 `onDragOver`：

| 落点 | 接受条件 |
|---|---|
| 左侧（应用包） | 恰好 1 个文件，且后缀是 `.jar` |
| 右侧（补丁包） | 恰好 1 个文件，且是**目录**或后缀是 `.jar` / `.zip` |

两个容易忽略的点：**必须恰好 1 个**（拖两个进来是拒的，不是取第一个）；
**区分大小写**（Java 用的 `String.endsWith`，`.JAR` 不算）——
都由 `TestDropAcceptRulesMatchUpstream` 钉住。

**落到目录里撞上同名文件时会先比 md5**：内容完全相同就不替换、只提示一句
（右侧「拖入的文件」条标成灰色的「内容相同」，补丁侧清单的候选行也会提前挂上
同名标签）。原版是照换不误 —— 但换完会打上「已修改」星号，字节其实一个没变，
属于假改动。详见 §三偏差表。

实现上有三处值得记：

1. **拿不到路径，只能走 Wails 的原生通道。** 浏览器出于安全，HTML5 拖放
   只给 `File` 对象、不给真实路径，而建树必须拿到路径。所以用
   `options.DragAndDrop.EnableFileDrop = true`（Windows 侧才会把
   `file:drop:<x>:<y>` 消息交给 Go）+ 前端 `window.runtime.OnFileDrop`。

2. **职责划分：前端答"落在哪一侧"，后端答"这一侧收不收"。**
   `OnFileDrop` 只给落点坐标，把它换算成"落在哪个面板"是布局知识，
   留在前端（`document.elementFromPoint` 找 `[data-side]` 祖先）比搬到
   后端去猜坐标合理。而收不收的**规则**留在 Go（`HandleFileDrop`），
   因为它需要可测 —— 于是有了上面那张表对应的单元测试。

3. **落点用 Wails 的 CSS 约定标记**，不自己实现拖拽高亮：
   `.pane { --wails-drop-target: drop }`。自定义属性会继承，所以面板内的
   树行也算落点（Wails 判定落点取的是光标下**最深**那个元素的样式）。
   因此高亮规则必须写成 `.pane.wails-drop-target-active` —— 写成裸的
   `.wails-drop-target-active` 会让每一行都亮。

#### 窗口内拖放：补丁树 → 应用树

上面那节是"从资源管理器把文件拖进来"（Wails 原生通道）。**窗口内**还有一条：
把补丁树的一行拖到应用树的某一行上，对应原版 `FileTreeCell.onDragDropped` 里
`dragInfo != null` 的分支。方向是**单向**的 —— 原版 `onDragOver` 里
`dragInfo.getOriginTree() == getTreeView()`（同树内拖）和
`getTreeView() != appTree`（落点不是应用树）都直接 return。

规则与"往行上拖一个磁盘文件"完全同构，**落点只决定目标目录**：

| 落点 | 行为 |
|---|---|
| 目录行 | 把拖来的项按名字加进这个目录 |
| 文件行 | 退回它的**父目录**（不是替换那一行） |
| 包根行 | 加到包内顶层（根节点虽为文件节点，它的 `Children` 就是包内条目） |
| 拖来的是**目录**、落点也是**目录** | 把这个目录的**子项**逐个并进去（不是把目录本身塞进去） |
| 拖来的是**补丁包的根行** | 把这个包**按文件名**放进目标目录（有同名就换、没有就加）。它代表的是那个 `.jar`/`.zip` 文件本身，**不是**容器 —— 不会把包拆开铺进去。补丁包本身是个 `.jar` 时树上唯一那一行就是它，所以根行必须能拖 |
| 补丁侧的说明文件 | 拒绝（原版 `isHasMappedNode()` 之外的 `isHasReadmeNode()` 也是整次拒绝），前端干脆不给它 `draggable` |

起拖方式与别处不同：**要按住约 0.3 秒再拖**。树行同时承担"点选中 / 双击预览"，
一按就起拖的话手一抖就拖走。光标跟着门槛走 —— **平时是普通箭头**，按够了才变
张开的小手（`.row.drag-ready { cursor: grab }`），这就是"现在可以拖了"的唯一信号。
常驻 `grab` 的做法被否掉了：整行一直顶着抓手会变成常态视觉噪音，也和普通行的
点击手感不一致（用户 2026-10-28："正常情况下鼠标样式还是正常的，长按之后变成小手"）。
另外**不要**用 `:active` 变抓手 —— 一按下就变，会让人以为已经可以拖、一甩却被取消。

三处实现细节：

1. **走 HTML5，不走 Wails 原生通道。** 原生通道的 `draganddrop.js` 开头就判
   `types.includes("Files")`，内部拖动没有 `Files`、根本不触发 —— 两条通道
   天然不会打架。落点判定复用 `paneSideAt`（`elementFromPoint` 找 `[data-side]`）。

2. **定位串要带 `jar!/` 前缀。** 嵌套 jar 里节点的 `Path` 是**相对那个 jar**
   的（`com/xxl/.../AdminBiz.class`），全树范围可能重名（两个 jar 里都有
   `META-INF/MANIFEST.MF`）。前端 `locOf` 按"父行是不是压缩包"递归拼前缀，
   后端 `resolveIn` 递归解析，两边规则必须一致。

3. **已映射的节点可以改指到别处**（与上游的偏差，见 §三偏差表）：
   原版遇到 `isHasMappedNode()` 直接拒绝，这里改成先解绑旧位置再落到新位置。

#### 标记根节点 / 标记说明文件 —— README 识别范围的正解

**先说清一个容易误判成 bug 的行为**：`RefreshReadmeNode` 只扫
**补丁根的直接子节点**。所以下面这些包自动流程都认不出 README：

| zip 里的形态 | 自动识别 | 原因 |
|---|---|---|
| `README.txt`（在根下） | ✅ | —— |
| `readme.txt` / `README.TXT` | ✅ | 名字比对用 `equalsIgnoreCase` |
| `mypatch/README.txt`（套一层目录） | ❌ | README 成了 `mypatch` 的子节点 |
| `src/README.txt`（嵌套更深） | ❌ | 同上 |
| `./README.txt`（`zip -r x.zip .` 的产物） | ❌ | 会被建到名为 `.` 的目录节点下 |
| `README.md`（非 `.txt` 后缀且名字不匹配） | ❌ | 只认配置名 + `.txt` 后缀 |

**这些全是上游行为，不是移植偏差。** 已逐处比对 Java 的
`PatchPackService.refreshReadmeNode`、`BasePackService.getOrCreateParentNode`、
`FileNameUtil.getDirPath`、`ZipFileDecompressor`（确认它不规整条目名），
四处逻辑与 Go 侧一致。`TestReadmeOutsideRootIsNotRecognized` 是这条的
特征测试 —— 哪天有人改了识别范围，它会变红。

**原版的解法是手动指定**，对应两个右键菜单动作，现已移植：

| 动作 | Go 位置 | 对应 Java |
|---|---|---|
| 标记根节点（可切换） | `App.MarkRoot` | `HomeRightTreeView.listenMarkRootChange` |
| 标记说明文件 | `App.MarkReadme` | `HomeRightTreeView.handleMarkReadme` |

菜单项的**显示条件**照搬原版 `buildPatchTreeContextMenu` 的第 3、4 个 claim，
由 Go 侧算好（`canMarkRoot` / `canMarkReadme`）随 `NodeView` 下发：

- `canMarkRoot`：非根节点 && 有子节点 && （是目录或压缩包）&& 不在自定义根之下
- `canMarkReadme`：非目录 && 状态为 NONE && **父级是根节点**

最后一条是关键：**只有直接挂在根下的普通文件才允许标成说明文件**。
所以对"套了一层目录"的包，必须**先标根、再（可选地）标说明文件**，顺序不能反 ——
`TestMarkMenuFlagsFollowUpstreamConditions` 把这条钉住了
（`mypatch/README.txt` 的两个 flag 都是 false）。

两个实现要点：

1. **`MarkReadme` 要先把旧 README 降回 NONE。** `RefreshReadmeNode` 扫子节点时
   **碰到第一个 `Type == README` 就 break**，若旧的在子节点顺序里更靠前，
   不降级就会一直被认成 README。原版 `handleMarkReadme` 里那句
   `readmeNode.setType(NONE)` 就是干这个的。
2. **`refreshRoot` 里 `setPatchDiffInfo` 必须挡空。** 标记根节点时应用包完全
   可以还没打开，而 Java 那句 `appTreeInfo.setPatchDiffInfo(null)` 会 NPE
   （被全局异常处理器吃掉）；Go 里是 panic、会终止进程。属 §3.9 的必要偏差。

#### 配色：三套主题，集中在一处

2026-10-22 起有**三套**主题，用户在「设置 → 常规配置 → 主题」里切，选择存
`localStorage`（`patchassistant.theme`），重启后还在：

| 值 | 名字 | 画布 `--bg` | 观感 |
|---|---|---|---|
| `light` | 明亮主题 | `#f5f7fa` | 默认，原版 JavaFX 的亮色观感 |
| `graphite` | 石墨主题 | `#17191d` | 中性深灰，层次柔和，和 IDE 默认暗色一个路子 |
| `oled` | 纯黑主题 | `#000000` | 近黑面板，对比最烈，OLED 屏上不发光 |

三套**共用全部规则**，差别只在变量值：亮色在 `:root`，两套暗色在
`:root[data-theme="graphite"]` / `:root[data-theme="oled"]`。`<html>` 上的
`data-theme` 由 `index.html` `<head>` 里一段内联引导脚本在**样式表之前**设好
（否则暗色用户每次启动先白屏一闪），`app.js` 的 `initTheme()` 只负责给下拉填
当前值、挂 change，以及把窗口底色同步过去。

所有与明暗相关的颜色**只**出现在这几个块里，别处一律引用变量。要加第四套
主题，照抄一个 `:root[data-theme="…"]` 块改值即可。

窗口底色有两处要留神：
- `gui/main.go` 的 `BackgroundColour`（现为 `{245,247,250}`）决定窗口出现
  那一瞬的底色 —— 那时前端还没跑起来，谁也不知道用户上次选的是暗色，只能用
  亮色值。
- 之后由 `gui/app.go` 的 `SetWindowBackground` 把 CSS 的 `--bg` 读出来同步
  过去（WebView 只覆盖客户区，缩放边缘/圆角那一圈露的是**窗口**底色，暗色下
  不换会留一圈浅色边）。颜色由前端传而不是 Go 侧写死两份常量，为的是"画布
  底色"只有 `app.css` 一个出处。

两个变量的"方向"在亮暗之间是反的，换主题时容易踩：

| 变量 | 暗色 | 亮色 |
|---|---|---|
| `--accent-hi` | 更亮（`#7aabff`） | **更深**（`#1d4ed8`） |
| `--hover` | 提亮 | **加深** |

`--accent-hi` 同时被用作悬停态和需要更强对比的文字色，所以亮色下必须往深处走。
也因为这个，`--surface-3` 不再兼任悬停态（它只管浮层），悬停单独拆出 `--hover`。

保留的 3 处 `#fff` 是彩色底上的白字（如主按钮文字），明暗下都一样，故未抽成变量。

**语法高亮是唯一不能纯靠变量的地方**：原版那套照抄 JavaFX `*-style.css` 的
命名色（`purple`/`blue`/`darkgreen`/`firebrick`/`teal`）是给白底调的，深底上
要么糊要么刺眼，所以两套暗色各配了一套（纯黑用高饱和一档、石墨用 One-Dark
经典一档），按语言逐个覆写。`yaml-style.css` 那套本就是 VS Code Dark+ 的取值，
放在暗底上恰恰好，**原样保留**。

**2026-10-14 的"现代工具风"美化**。只动**窗口外观层**：工具栏、按钮、面板头、
树行、状态栏、补丁信息、弹窗、右键菜单、滚动条。三处**刻意没动**，因为它们被
别的东西锁着：

| 没动的部分 | 谁锁着 |
|---|---|
| 代码预览区（纯白底、行号 `#aeb3c2`、14px、语法高亮配色） | 走查 `diff-code-*` / `diff-lineno-*` 逐条按 RGB 钉死；对齐原版 `theme.css` |
| 双栏差异的行底色与 `＋/－/~` 符号 | 走查 `split-line-bg-*` / `split-glyph-*` 逐条按 RGB 钉死 |
| 树里"增/改/删"语义色（`--add` `--mod` `--del` 及其浅色底） | 走查 `split-glyph-color` / `split-line-bg-*` 钉着 `#1a7f37` / `#9a6700` / `#cf222e` 与三个浅底 |

`--text-mute` 也**没动** —— 它是"修改时间 / 大小 / MD5"那三列的颜色，用户明确
要求 MD5 列"完全不动"。

具体做了什么：

- 新增三个设计变量组：圆角 `--r-sm/md/lg`（4/6/8）、投影 `--shadow-sm/shadow/shadow-lg`
  三级、以及统一的间距节奏（工具栏 9px、面板头 8px、行高 24px、状态栏 34px）。
- **2026-10-22 补记：状态栏那道向上细影也收成了令牌 `--bar-shadow`。** 它原本
  把 `box-shadow: 0 -1px 2px rgba(16,24,40,.04)` 写死在 `.statusbar` 上，两套暗色
  主题都没翻 —— 深色界面上是一道发灰的脏边，亮色那种"轻轻抬起"的效果也丢了。
  之所以单独一个变量而不复用 `--shadow-sm`：那个是"控件四周一圈"，这里只往上一面，
  且强度弱得多。由 `.workbuddy-ai/mockups/verify_statusbar_shadow.py` 在三套主题下
  逐一量计算样式守着（`rgba(16,24,40,.04)` / `rgba(0,0,0,.45)` / `rgba(0,0,0,.5)`）。
- **工具栏**：品牌名前加一个小色块（纯文字品牌在一排按钮里像"第 0 个按钮"）；
  按钮从"灰底 + 描边"改成"白底 + 描边 + 贴边投影"；用**绝对定位的伪元素**在
  gap 里画两条分组竖线（不占按钮自身的背景，悬停时不会被染上）。
- **面板头**：标题前加 3px 主色小色条；路径做成小药丸（它是一段元信息，
  不该和标题抢同一层视觉）。
- **树行**：21px → 24px，间距与内边距同步放宽。
- **状态栏**：增/改/删做成小药丸，底色沿用差异行那套浅色（同一套语义色，
  看久了能形成条件反射）；药丸文字保持中性、只把**数字**染成状态色。
- **滚动条**：11px → 10px，滑块用 `background-clip: padding-box` + 透明边框
  做成"浮起来"的一条。
- **按钮的质感**（当天下午追加，用户："按钮这些增加一下质感"）。三类按钮
  （`.btn` / `.btn-primary` / `.btn-icon`）都改成"有厚度的一小块"，靠三样东西
  叠出来，缺一样就会重新变平：
  1. 底色上压一层**半透明**的垂直渐变（顶部白 → 底部透明）。用半透明叠加
     而不是写死两段色值，是为了让悬停**只改 `background-color` 就能平滑过渡**
     —— 渐变之间浏览器不做插值，直接换会是硬跳。
  2. 顶部 1px 内高光 + 底部 1px 内暗影，一上一下夹出厚度。
  3. 外投影，让按钮离开底纸。
  按下态把外投影换成**内凹**阴影（视觉上"陷进去"），比只换底色更能说明
  "这一下按到了"。主按钮是同一套手法，只是渐变改成提亮（深色底上叠白高光
  才看得出凸起）。**没有做拟物** —— 没有强高光、没有强投影。
  图标按钮静止态仍是透明的（一排图标全带常驻边框会很吵），悬停时边框才
  "浮现"；边框宽度一直是 1px、只是颜色透明，所以浮现时不会把邻居挤动。
  新增的变量：`--btn-face`（按钮静止底色）、`--btn-edge`（悬停描边）、
  `--accent-press`（主按钮按下），都在 `:root` 里。
- **顺手修了一个真 bug**：`.contentwin-err-head` 引用 `var(--sans)`，而
  `--sans` **从来没定义过** —— `font: 600 12px var(--sans)` 整条声明被判为无效
  而丢弃，编译错误标题于是悄悄退回继承字号。现在 `--sans: var(--ui)` 定义好了。

#### 应用图标（exe 与窗口）

原版只有两个图标文件：

| 文件 | 尺寸 | 用途 |
|---|---|---|
| `src-java/src/main/resources/icon.png` | 200×200 | `PatchAssistantApplication` 里 `stage.getIcons().add(ImageUtil.loadImage("/icon.png"))`，即**运行时窗口图标** |
| `src-java/src/main/resources/icon.ico` | **仅 64×64** | 打包进 exe |

实测原版 exe 的 PE 资源表（`_extract/tools/peicons.py`）：`RT_GROUP_ICON` 只有
**1 个条目、64×64、16936 字节**，与 `resources/icon.ico` 逐字节同尺寸 —— 也就是说
原版 exe 在"大图标"视图下本来就是被系统拉伸的。

Wails 这边要两个文件：

| 文件 | 作用 |
|---|---|
| `gui/build/windows/icon.ico` | **决定 exe 图标与窗口图标**。`pkg/commands/build/packager.go` 的 `compileResources` 把它 `LoadICO` 后写成 `PatchAssistant2J-res.syso` 嵌进二进制；Wails **没有**任何运行时 `SetIcon`/`WM_SETICON` 调用，窗口图标就是靠"窗口类无图标时回退到 exe 主图标"这条 Windows 规则来的 |
| `gui/build/appicon.png` | Windows 上**用不到** —— `generateIcoFile` 里是 `if !fs.FileExists(icoFile)` 才从它生成 ico。留着是给 macOS 的 icns、以及"ico 被删掉"兜底 |

**没有直接把原版那两个文件拷过去**，而是用 `_extract/tools/make_appicon.py`
从 200×200 的 PNG 重采样出 6 层（16 / 32 / 48 / 64 / 128 / 256）。理由：原版那份
64×64 在大图标视图下会被系统拉伸；用信息量更大的 200×200 做源，除了 256 层是
1.28 倍放大，其余全是**缩小**，质量严格优于原版。**图案没有重画**，配色与几何
都是原版那套（主色 `rgb(26,250,41)`，另有 alpha≈76 的半透明绿，背景透明）。

两个实现细节，都是踩过的：

- **重采样必须在预乘 alpha 上做**。原图 75.3% 是透明像素、9.6% 是 alpha≈76 的
  半透明绿；直接对非预乘的 RGB 滤波，透明像素的 `(0,0,0)` 会把边缘往黑里拉、
  出现一圈暗边。
- **ICO 每层存 PNG**（不是 32bpp BMP）。Wails 自己就是这么做的 ——
  `winicon.GenerateIcon` 每层 `png.Encode` —— 所以 `winres.LoadICO` 与 Windows
  都认。体积差一个数量级：BMP 版 370 KB，PNG 版 **17 KB**（原版单层 64×64 是
  16.9 KB，等于用同样的体积换来了 6 个尺寸）。

#### 补丁树右键菜单

原版 `HomeRightTreeView.buildPatchTreeContextMenu` 是一串 `MenuItemClaim`，
每项各自判断显不显示，加进 `ContextMenu` 的顺序就是渲染顺序。现已移植
**13 项**（按原版顺序，其中「编辑内容」是我们加的，原版没有）：

| 菜单项 | 显示条件 | 动作 |
|---|---|---|
| 重新加载 / 刷新信息 | 节点是主根 / 自定义根 | `ReloadOrRefresh` |
| 取消关联 | 节点已建立映射 | `CancelMapped` |
| 标记根节点（单选） | 非根、有子节点、（目录或压缩包）、不在自定义根下 | `MarkRoot` |
| 标记说明文件 | 非目录、状态为 NONE、**父级是根** | `MarkReadme` |
| 编辑内容 ★ | 非目录 | `OpenEditor`（见 §「编辑包内文件内容」） |
| 保存文件 | 非目录 | `SaveNodeAs` |
| 保存源文件 | 非目录且后缀是 `.class` | `SaveSourceNodeAs`（反编译导出 `.java`） |
| 复制 ▸ | 总是（目录的 MD5 项置灰） | `CopyNodeText` |
| 查看签名 | 根节点 | `GetPatchSign` |
| 查看绑定信息 | 根节点，且应用包已打开（主根还要求没有自定义根） | `GetNodeMappedText` |
| 资源管理器中查看 | 磁盘上的普通文件 | `OpenInExplorer` |
| 关闭补丁 | 主根 | `ClosePatchPack` |
| 聚焦补丁（单选） | 总是 | `SetFocusTree` |

★ = 刻意增强，原版没有（原版的"编辑模式"被作者注释掉了，见 §「编辑包内文件内容」）。

**有意跳过的两项**（见 §五）：
保存为项目（要 `JarDecompilerService`）、创建说明文件（要 READ ME 编辑器那一屏）。

**整份菜单由后端算好**，前端只按顺序渲染 + 按 ID 分发。这不是为了"架构好看"：
每个 claim 都要看父级、祖先链、两个包是否打开、自定义根的数量、该根下有没有
README，而树是懒渲染的、父级可能还没建 DOM —— 前端拿不到。文案本身也会变
（主根"重新加载" / 自定义根"刷新信息"），单选类还带勾选态。

两个实现要点：

1. **`isCompressNode` 与 `instanceof CompressNode` 不能混用。** 前者按后缀判、
   后者按节点种类判，两个 claim 用的不是同一个 —— 详见 §3.11 第一条。
2. **`ReloadOrRefresh` 的两条分支做的事不同。** 落点是主根就整包重读，
   落点是自定义根则只重跑映射、**不重读 README**。这条有专门的判别性测试
   （`TestReloadOrRefreshOnCustomRootSkipsReadme`）：改盘上的 README 内容，
   再确认「刷新信息」读到的还是旧指令、「重新匹配」才读到新内容。

**另一个上游怪癖（照搬了）**：`refreshPatchMappedNode(boolean)` 这个不带根
参数的重载，**一旦存在自定义根就不再刷新主根**。所以若把主根下的文件标成
说明文件、同时又存在别的自定义根，主根这一侧不会被重刷。Go 的
`refreshAllRoots` 保持同样行为。

**构建方式与一个必须知道的坑。** `gui/wails.json` 里
`frontend:install` / `frontend:build` / `frontend:dev:watcher` **三个都留空**，
所以 `wails build` 不跑 npm，直接服务 `gui/frontend/dist/` 下的静态文件。
代价是 Wails 生成的 `frontend/wailsjs/go/main/App.js` 绑定文件不在 `dist` 里、
取不到（会 404），因此前端一律用运行时注入的 `window.go.main.App.<Method>`
调用后端，不 `import` 生成物。

**`TotalInfo` 必须会话级复用 —— 这是本轮最值得记的一条。**
Java 侧 `HomeContext.totalInfo` 是 `final` 字段，全程只有一个实例，
只在应用树刷新时由 `APP_TREE_REFRESHING` 处理器调 `cleanTotal()` 清零。
Go 版最初每次匹配都 `tree.NewTotalInfo()` 造新计数器，结果"删除"计数会丢：
被 README 的 `-` 指令标成 DEL 的应用节点在补丁侧没有对应物，清理阶段遍历
补丁树时够不到它，而 `MappedDelNodeWithType` 开头又是"已是 DEL 就直接返回"、
不补计数 —— 于是新计数器上没人给它加 1。修法就是对齐 Java：
`App.total` 会话级复用、换应用包时 `CleanTotal()`。
`TestRematchIsStable` 是这条的回归测试。

**`RunPatch` 不重跑匹配**，这也是刻意的：原版点"保存/打补丁"只是把当前
匹配结果写出去，重新匹配由"重新匹配"按钮显式触发。早期版本图省事在保存前
又跑了一遍 `refresh`，写出的统计里"删除"就变成 0。

#### 补丁信息面板与 README 高亮

**背景：README 的识别一直是对的，缺的是"显示"。** 有反馈说"README 文件没有
读出来"，实测（造同构包跑 `OpenPatchPack`）确认 `RefreshReadmeNode` 工作正常：
节点 `Type` 变成 `README`、`PatchSign.Readme` 有内容、`ReadmeText()` 也有值。
真正的问题是原版有、我们漏做的那块 —— 首页底部的**「补丁信息」面板**
（`HomeView.buildBottomCentre` 的 TitledPane + `updatePatchInfo`）。
README 识别出来却没有任何地方显示，用户看到的自然就是"没读出来"。

面板内容按 `updatePatchInfo` 逐条对齐，最多两段、顺序固定：

| 两个包的状态 | 面板内容 |
|---|---|
| 都打开，警告空、说明有 | `说明：` + 换行 + README |
| 都打开，警告有、说明空 | `警告：` + 换行 + 比对提示 |
| 都打开，两段都有 | `警告：` + 比对提示 + **空行** + `说明：` + README |
| 只开应用包 | 有比对提示才显示 `警告：` 段 |
| 只开补丁包 | 有 README 才显示 `说明：` 段 |
| 都没开 | 留空（原版是 `setText(null)`） |

判空用的是 Java `StringUtil.isBlank` 的口径 —— 空串或**每个字符都是空格类
字符**才算空，`\n` / `\t` **不算**。所以一份内容全是换行的 README 在原版眼里
不算空、会被当正文显示。前端 `isBlank()` 照搬了这个口径，详见 §3.2。

面板常驻显示、不可折叠（原版 `setCollapsible(false)`），高度固定 150px，
正文区底色跟树区一样是 `--bg`（面板头才是 `--surface`），这样滚动截断时
看起来是一块独立的内容区，而不是和状态栏糊在一起。

**README 节点的高亮。** 原版靠 `TreeNodeUtil.getIconImage` 给它换
`/icons/readme.png` 图标来区分；当时我们的树没有图标列，所以改成三层叠加：
行底色 `--readme-soft` + 左侧色条 + 名字后面一个 `README` 小标签。
图标补上之后（见下）这三层**仍然保留** —— 16px 的图标夹在一堆深色文件名里
不够显眼。但那个 `README` 文字标签确实是**多出来的**（原版只有图标），
嫌冗余删掉 `makeRow` 里那一段即可，图标那侧不受影响。

顺带修掉一个上一轮留下的缺陷：`index.html` 里 `mapped-close` 这个 id 出现了
两次（弹窗右上角的 ✕ 和底部的「关闭」），`getElementById` 只认第一个，
所以底部那个按钮是死的。现在 ✕ 改叫 `mapped-x`，两个都绑上关闭。

#### 嵌套 jar 的展开：三个"看起来无关"的报障，同一个根因

用户反馈三件事：**① jar 包里没有高亮内容；② lib 包里没有"再次打开"的按钮；
③ 希望点名字就能展开，而不是非得点那个小三角。**

①②其实是**同一个**根因。嵌套压缩包是**惰性**展开的：`BOOT-INF/lib/*.jar`
只在"用得上"时才展开（对应原版 `buildChildrenWithCompress`），匹配下探失败
还会把 `appNode.Children = nil` 回滚。于是这类节点在 JSON 里**一个子级都没有**。

前端早先按"有 children 才画三角"来判断可展开，它就成了**死叶子**：
点不开、里面什么都看不到 —— 藏在里面的文件（包括补丁命中的那些高亮节点）
自然也就"没有高亮"、"没有再次打开按钮"。用户描述的是两个症状，
指向的是同一个断点。

修法分两层：

- **后端给标记**：`NodeView.Compress` —— 压缩包节点即使现在没有子级也算
  可展开。另加 `App.ExpandNode(treeKey, path)` 现场展开并回该节点的视图，
  对应原版双击时的 `buildChildrenWithCompress` + `buildNodeChildren`。
- **前端认标记**：`openable = kids.length > 0 || !!node.compress`。
  展开时若还没有子级就找后端要（三角先变成 `⋯`）。

③ 是纯手势问题。原版单击选中、双击打开，我们沿用双击打开内容预览，
所以单击展开必须**让位**：点名字后延后 200ms 执行，双击时把待执行的单击
取消掉。不取消的话，双击展开目录会"先展开再收起"。

顺带修掉一个真实漏洞：`makeRow` 早先在 `!openable` 时提前 `return`，
**叶子文件根本没绑双击** —— 而"打开内容预览"恰恰只对叶子文件有意义。
现在 `st` 对所有行都建（叶子给一个空操作的 `toggle`），双击一律绑上。

#### 树节点图标

用户拿原版截图来问"把这种样式图标加上"，于是把图标列补了回来。

**图标是原版那 21 张 PNG 原样拷过来的**（`src-java/src/main/resources/icons/`，
64×64，逐字节校验过），放在 `gui/frontend/dist/icons/`，靠
`//go:embed all:frontend/dist` 进二进制。没有重画 —— 所以观感与截图完全一致。

选哪张图**由后端算**（`gui/icon.go` 的 `iconName`，逐条对齐
`TreeNodeUtil.getIconImage` + `IConUtil.getPathByFileExt`），前端只做
`img.src = node.icon`。理由和内容预览那边一样：判定不只看后缀，还要看
节点类型、节点路径、以及"这个根是不是 Spring Boot 胖包"，前端拿不到；
规则放在 Go 里也才好写单测。

判定顺序**不能改**，四条特例优先于后缀匹配：

| 顺序 | 条件 | 图标 |
|---|---|---|
| 1 | 应用包的 ROOT 且直接子级里有 `BOOT-INF/` | `spring-boot.png` |
| 2 | `CUSTOM_ROOT`（用户手动标记的根） | `file-mark.png` |
| 3 | `README` 类型 | `readme.png` |
| 4 | 路径正好是 `META-INF/patch-up/` | `patch-up.png` |
| 5 | 有专属后缀图标 | 见 `iconByExt` |
| 6 | 目录 | `directory.png` |
| 7 | 其余 | `file.png` |

三个容易写错的地方：

- **后缀匹配区分大小写。** Java 的 `String.endsWith` 就是这样，所以
  `Foo.JAR` / `README.TXT` 拿不到专属图标，落到兜底的 `file.png`。
  别"顺手"改成大小写不敏感 —— 那会与原版分叉。
- **`.sign` 给的是 `patch.png`**（补丁图标），不是通用文件图标。
- **`.doc/.docx`、`.ppt/.pptx`、`.xls/.xlsx` 是成对的**，别只写一半。
  另外 `.txt` / `.idx` / `.MF` 这些**本来就没有**专属图标，落到 `file.png`
  是原版行为，不是漏了 —— 截图里 `README.txt` 显示成"信息"图标、
  `classpath.idx` 显示成普通文件图标，都是这个道理（前者是 **README 类型**
  给的特例图标，跟 `.txt` 后缀无关）。

第 1 条的 `BOOT-INF/` 判定比的是**路径**且必须是**直接**子级。目录条目的
Path 保留尾部斜杠（见 §2.5 的保真注释），所以这里能跟 `constant.BootInfPath`
对上；改成"递归找"或"比名字"都会与原版分叉。

顺带把目录名的尾斜杠去掉了（`META-INF/` → `META-INF`）。那个斜杠是
"没有图标列"时期的替代办法（`app.js` 里原有注释写着"既准确又省掉一个图标列"），
而原版靠图标区分目录与文件、名字里不带斜杠。

**仍未做**：工具栏按钮的图标只补了**搜索**那一个（原版工具栏就是"搜索（只有图标）
/ 配置 / 关于"），其余几个按钮（打开应用包 / 打开补丁包 / 重新匹配 / 设置 /
执行打补丁）在原版里没有对应物，图标得另选一套、风格未必对得上 —— 已与用户确认先不做。

补全局搜索那轮把 `icons/search.png`（工具栏搜索按钮）与 `icons/time.png`
（搜索历史那一行的时钟）也拷了进来，图标目录里因此是**恰好 21 张**，
多一张少一张都会被 `TestIconFilesExist` 抓住。这两张不是树节点图标，
但同样登记在 `gui/icon.go` 的常量表里 —— 否则那条"目录里不许有多余文件"的
反向校验会把它们当成拷错了。`iconTime` 由 `SearchItemView.Icon` 带出去，
`iconSearch` 只被 `index.html` 直接引用（Go 里不用，常量留着是为了那张表完整）。

"图到底进没进二进制"这件事单独有测试盯着：`TestIconsEmbedded` 读的是
`embed.FS` —— 也就是 `wails build` 时真正写进 exe 的那一份，而不是工作目录。
所以 embed 指令写错（漏 `all:` 前缀、`dist` 被挪出 embed 根）它会红，而
`TestIconFilesExist`（查磁盘）照样绿。两条查的是同一张表的两个侧面，缺一不可：
一个防"文件不在仓库里"，一个防"文件在仓库里但没被打包"。变异验证里把
`embedRoot` 从 `frontend/dist/` 改成 `dist/`，确实只有 `TestIconsEmbedded` 红 19 例。

构建结果（`wails build`，`gui/` 下执行）：`gui/build/bin/PatchAssistant2J.exe`
**12,872,192 字节（12.28 MiB）**。加图标那一步是 12,668,928 字节（12.08 MiB），
比更早的 12,621,824 字节（12.04 MiB）多 47,104 字节 —— 与当时那 19 张 PNG 合计
40,655 字节 + `app.js`/`app.css` 的改动 + embed 文件表相符；之后双栏滚动同步、
"在另一棵树里定位"、"编辑包内文件内容"（含 `.class` 源码形态）又共加了约 50 KB
（前端 `syncScroll` / `revealRow` / 编辑器模块 + 后端 `LocateMapped` / `editor.go`）；
「保存源文件」再加 3,584 字节（后端 `SaveSourceNodeAs` + 前端一个 `case` 分支）；
补 `-patched` 那轮之后是 12,726,272 字节（12.14 MiB）；搜索这一轮 **+145,920 字节**
（`gui/search.go` + 前端约 300 行搜索代码 + 两张新图标 4,108 字节 + `app.css`）。
（`du` 报的 81K 是 4K 块对齐后的虚高，实际数据 39.7 KB。）

#### 文件内容预览（`ContentView`）

对应原版一次 `new ContentView(node).showDialog(stage)`，是主页最后一块。
原版的对话框是 `Modality.NONE`（非模态）且可缩放，所以这里做成**可拖动、
可缩放的浮动面板，刻意不铺遮罩** —— 用户开着预览还能继续操作两棵树。

**标题栏上的「最大化」按钮**（`#content-max`）。原版那边是
`dialog.setResizable(true)` —— JavaFX 会给可缩放的对话框挂上**系统标题栏**的
最小化 / 最大化 / 关闭按钮，所以原版"有这个按钮"是框架给的，不是代码写的。
Go 侧的浮动面板没有系统标题栏，就在自己的标题栏上补一个，行为对齐：

| 状态 | 表现 |
|---|---|
| 普通 | 图标 `□`、提示「最大化」；可拖标题栏、右下角可缩放 |
| 最大化 | 图标 `❐`、提示「还原」；铺满主窗口（留 8px 边距）、`.maxed` 收掉缩放手柄、标题栏拖不动 |
| 还原 | 回到**最大化前的尺寸与位置**（不是默认尺寸） |

两个刻意的选择：

- **最大化状态不跨"打开"保留**。原版每次预览都是 `new Dialog()`，新开一个就是
  普通尺寸，所以 `hideContentWin` 里把 `contentMaxed` 清掉。
- **`rememberContentSize` 在最大化时直接 return**。不这么写的话，铺满窗口的尺寸
  会把"普通尺寸"覆盖掉 —— 下次打开就是一个铺满窗口、却没处于最大化状态的怪东西。
  这条由走查 `content-max-does-not-poison-remembered-size` 钉着（要在**最大化
  状态下关掉**才测得出来，见 §六）。

**面板几何一律夹进视口**（`clampContentGeom`，`CONTENT_GRAB_MIN = 72`）。

用户报过"打开的对比窗口在大窗口下拖出去就动不了了"。根因是**移植带来的结构性差异**，
不是原版的 bug：原版那个 `Dialog` 是**系统窗口**（有系统标题栏），拖到屏幕外还能靠
系统窗口菜单（Alt+Space）/ 任务栏把它拉回来；Go 版是应用内 `position: fixed` 浮层，
**没有系统标题栏**，标题栏一旦离开视口就再也没有抓手 —— 而位置又是被记住的，
关掉重开还是原地，等于永久卡死。实测（视口 1374×800）：向右下拖 +4000/+3000 后
面板停在 `left=4227, top=3090`，视口内离标题栏最近的点命中 `statusbar`，
**根本抓不到**。

所以几何统一在 `applyContentGeom` 里夹一次（默认几何 / 记住的几何 / 最大化 /
还原 / 拖动 / 视口变化全都汇到这一个出口，不会出现"某条路径忘了夹"）：

| 方向 | 夹取区间 | 理由 |
|---|---|---|
| 横向 | `left ∈ [72 - 宽, innerWidth - 72]` | 至少留 72px 面板在视口里，抓手不丢 |
| 纵向 | `top ∈ [0, innerHeight - 标题栏高]` | 标题栏**整条**必须可见（也不会跑到视口上方） |

`window.resize` 那条也一并夹：主窗口被改小后，原来记得的位置可能已经在视口外了
（不夹的话，把主窗口往小拖就能复现同一个"抓不回来"）。走查里
`content-drag-clamps-left-top` / `-right-bottom` / `-clamp-keeps-grabbable` /
`-clamp-survives-reopen` 四条钉着；其中"还抓得住吗"用 `elementFromPoint` 做命中
测试 —— **只量 left/top 证明不了这件事**，值可能看着在视口内却被别的层盖住。

新增 `internal/content` 包，把原版三个类的东西搬了过来：

| 原版 | Go | 说明 |
|---|---|---|
| `ContentSupports.getContentParser` | `ParserForExt` | 决定"能不能预览" |
| `ContentSupports.getCodeAreaFactory` | `LangForExt` | 决定"用什么语法着色" |
| `CodeAreaFactory.computeHighlighting` | `Tokenize` | 7 种语言的规则逐字抄自各 `*CodeAreaFactory` |
| `LineNumberFactory` / `DiffLineNumberFactory` | `PlainNumbers` / `DiffNumbers` | 行号口径 |

内容（文本、高亮片段、行号）**全部由后端算好**，前端只拼 DOM 和分发右键
菜单动作。高亮规则与行号口径都有跨语言黄金基准管着（见 §六），不能在 JS
里重写一份。双栏的差异标记（`ComputeSplitMarks`）同理 —— 后端给每行一个
`mark` 字段、给差异行一组行内字节区间（`inline`），前端只负责拼类名，
判定逻辑一行都不在 JS 里。

**两处不对称必须照搬，别"顺手补齐"**：

- `getContentParser(ext)`：`class` → `ClassContentParser`；
  `java/txt/yaml/yml/xml/properties/js/html/htm/css/sql/json/sign` →
  `TextContentParser`；其余 `null`（→ 交系统程序打开）。
- `getCodeAreaFactory(ext)`：`java/class`→Java、`js`→JavaScript、
  `html/htm`→Html、`css`→Css、`xml`→Xml、`yml/yaml`→Yaml、`sql`→SQL，
  其余一律 `DefaultCodeAreaFactory`（**不高亮**）。

两张表的扩展名集合**不一样**，所以 `txt` / `properties` / `json` / `sign`
是"能预览但不着色"。给 json 补上高亮看着更"合理"，但那是改行为，不是移植。

**段落口径与差异切分不是一回事。** 用探针（`_extract/tools/ParaProbe.java`）
实测 JavaFX `CodeArea`：只有 `\n`、`\r\n`、单独 `\r` 算换行；
`\u0085` / `\u2028` / `\u2029` / `\u000b` / `\u000c` **不算**；
**末尾空段落保留**（`"a\nb\n"` 是 3 段）；空串是 **1** 段。
而 `util.SplitDiffLines`（差异算法用）只认 `\n` / `\r\n` 且去尾部空行 ——
两者不能互相替换，所以 Go 侧是两套实现。

**`.class` 的处理**：走 Fernflower 反编译（见 §2.1 与 §一"`.class` 反编译"）。
反编译不可用（本机没装 Java）时走 `unsupported` 分支，把**原因**弹给用户，
而不是静默失败。

**一处刻意偏差**：差异（单栏）模式下切换字符编码，原版会**崩** ——
`ContentView.handleCharsetChange` 拿到的 node 是 `null`（那个 CodeArea 没绑
节点），走 else 分支去 `parse(null)` 直接 NPE。既然原版是坏的，这里选最有用的
行为：两侧一起按新编码重解。双栏模式下仍只改右键点中的那一栏，与原版一致。

#### 双栏差异高亮（刻意增强，原版**没有**）

原版的双栏模式（`diffModel = false`，**这是默认值**）在
`ContentView.dynamicCodeAreaWithDiffModel` 里只有两个并排的 `CodeArea`：

```java
crateCodeAreaPane(leftNode, leftContent);
crateCodeAreaPane(rightNode, rightContent);
new SplitPane(...)
```

**不做任何差异标记** —— 差异标记只出现在"差异模式"（单栏 unified diff）的
**行号列**上（`DiffLineNumberFactory`），文本本身也不着色。

用户要求"左边是被替换的文件、右边是替换文件，差异处要高亮"，故补上。
这是**新增能力，不是移植**，登记在此以免日后被当成原版行为照抄回去。

标记口径（`content.ComputeSplitMarks`）：

| 一个差异块 | 左栏（应用侧旧文件） | 右栏（补丁侧新文件） |
|---|---|---|
| 两边都有内容 | `mod` 改（琥珀） | `mod` 改（琥珀） |
| 只有左栏有 | `del` 删（红） | — |
| 只有右栏有 | — | `add` 增（绿） |

三档沿用树里那套状态名（`t-mod` / `t-add` / `t-del`）。

**呈现方式：行号列 ＋ / － / ~ 符号 + 整行一层浅色底 + 正文只染"真正变了的词"，
其余文字保留语法着色。** 三层各回答一个问题：

| 层 | 长什么样 | 回答什么 |
|---|---|---|
| 行号列符号 | `+` 增（绿）/ `-` 删（红）/ `~` 改（琥珀） | 这一行是增、删还是改 |
| 整行底色 | 增 `#dafbe1` / 删 `#ffebe9` / 改 `#fff8c5` | 差异在哪几行（扫一眼就看见） |
| 正文词级 | 只有变了的词染状态色 | 具体哪几个字变了 |

> 这条**返工了四次**，别再顺手改回去：
> 1. **第一版**：正文铺半透明底色 `rgba(…, .14)` + 行号栏叠一层色调。
>    用户截图反馈「把背景改成白色的，代码字体高亮就行了」。
> 2. **第二版**：去掉底色，改成**整行文字**染状态色
>    （`.cline.mark-mod > .lt, .cline.mark-mod > .lt * { color: var(--mod) }`）。
>    用户再次截图反馈「现在时全部内容高亮，我需要的是关键词高亮就行」——
>    整行染色把语法着色全盖掉了，一屏同一个颜色，反而看不出哪儿变了。
> 3. **第三版**：行号列保留色条（回答"差异在哪几行"），
>    正文**只染真正变了的词/片段**（回答"具体哪几个字变了"），其余 token
>    保留语法着色。
> 4. **第四版（当前）**：用户发来当前界面截图 + 一张参考图，要求
>    「在侧边需要处显示 + 或者 - 来区分是新增代码还是删除代码。并且显示这种高亮」。
>    经像素实测后确认口径：行号列左侧加 **＋ / － / ~** 符号，
>    差异行铺 **行级浅色底**（删浅红 / 增浅绿 / 改浅黄）。
>
> ⚠️ **第四版把第一版否掉的"行底色"又加回来了 —— 这是用户明确改主意，
> 不是把第一版的旧代码抄回来。别再拿第一版的结论把它删掉。**
>
> 这四次调整都是用户对这条**刻意增强**本身的调整，一并登记。

**关键区分：第三版同时修了一个真 bug，不是纯样式问题。**
用户说"全部内容高亮"时，根因是整个文件都被标成了差异块 —— 详见下面
「`maxMyersD` 闸门口径」一节。**这个 bug 与选哪种高亮方式无关，无论选哪种都得修。**

**行号列：符号 + 色条 + 底色三者都钉在 sticky 上。**

- **符号**：`::before` 的 `content` 给 `+` / `-` / `~`，颜色跟着状态走。
  两个必须记住的点：
  1. **每一行都要占住这一格** —— 先给所有行 `content: ' '`，再按 `mark-*`
     换成符号。少了占位符，带符号的行会比不带符号的行宽一个字符、行号左右错开。
     靠**特异性**（`.contentwin-body.split .cline.mark-* > .ln::before` 5 个类 >
     `.contentwin-body.split .code .ln::before` 4 个类）而非 `!important`。
  2. **只在双栏里加**（选择器前缀 `.contentwin-body.split`）。单栏（含差异模式）
     的行号列没有 `mark-*` 类，加进去会平白把行号列撑宽一个字符，与原版对不上。
- **色条**：`box-shadow: inset 3px 0 0 var(--mod)`（`.mark-del` / `.mark-add` 同理换色）。
- **底色**：整行 `.cline.mark-* { background: … }`；`.ln` 自己那层白底在标记行上
  必须**换成同一浅色**（`.cline.mark-* > .ln { background: … }`）—— 否则 sticky
  的行号列会把行底色挡住，横向滚动时还会露出底下的正文。
  这一条由走查 `split-lineno-bg-follows-line` 单独钉住。
- **无标记的行底色必须仍是透明的**（`split-line-bg-plain-white`）—— 守的是**减法**：
  只查"三种底色对不对"抓不住"底色偷偷铺满了整栏"。

**正文词级染色**：行内差异由 `content.ComputeSplitMarks` 的 `LeftInline` /
`RightInline` 给出，每个标记是一段字节区间 `{start, end}`。`applyInline` 把这些
区间贴到语法片段上 —— **在原有语法类名后面追加** ` mk-<mark>`，形如
`class="comment mk-mod"`：

- **叠加而不是替换**：语法类名保留，所以 `font-weight` 等属性继续生效，
  只有 `color` 被换掉。
- 染色选择器是 `.cline .lt .mk-mod { color: var(--mod) }`（`.mk-del` / `.mk-add` 同理）。
  特异性 3 个类，压过 `.lang-java .keyword` 的 2 个类 —— **不用 `!important`**。
  靠特异性而非强制覆盖，将来要收窄范围（比如只染标识符不染运算符）不必先拆一层。
- 未标记的行**不受影响**，语法着色照旧（`content-syntax-applied` 钉着）。
- 前端 `linesHtml` 的类名白名单要允许**两段**：
  `/^[a-z][a-z0-9-]*( [a-z][a-z0-9-]*)?$/`。原来只认单段，会把
  `"comment mk-mod"` 整个 `<span>` 丢掉（走查 `content-highlight` + 4 条
  `split-inline-*` 一起红）。

行内配对的口径：一个"改"块里左边第 k 行配右边第 k 行，逐行做词级差异；
块内多出来的行整行算变化。纯增 / 纯删的行整行算变化。

分词规则（`wordTokens`）：连续标识符字符（字母 / 数字 / `_` / `$`）算一个词，
连续空白算一个词，其余每个字符各算一个词。词级差异**复用 `util.Deltas`**
而不是另写一套算法 —— 这样行级与词级的选路才一致。

实现上只有一个坑：**标记的下标必须与渲染出来的行号一一对应**。所以
`content.ComputeSplitMarks` 收的是 `content.Paragraphs` 切出来的行（显示口径），
**不能**用 `util.SplitDiffLines`（差异算法口径，会去掉尾部空行）——
源码文件基本都以换行结尾，一错位就是整栏高亮漂移。

顺带把 `util.Delta` / `util.Deltas` 导出（原来是 `diffDelta` / `diffDeltas`）：
差异块现在有三个消费者（行级标记、词级差异、界面），界面也要用同一份。

#### `maxMyersD` 闸门口径（修的是真 bug）

用户第三版反馈里"全部内容高亮"的**真正根因**不在 CSS，在差异算法：

```go
// 错的口径（修复前）
maxD := n + m
if maxD > maxMyersD { return nil }
```

`n + m` 是两份文本的**总行数**，而它只是编辑距离的**上界**。两份 1000 行的文件
哪怕只差一行，`n + m = 2000` 照样撞闸 → `myersOps` 返回 `nil` → `Deltas`
退化成"整篇被替换" → 界面整个文件都被标成差异。

**用户场景**：`ElementValueSpecialServiceImpl` 反编译出来上千行，两版只差几行，
两版行数之和 > 2000 → 整篇标 `mod`。实测复现：两份 1200 行、只差中间 1 行的输入，
`Deltas` 返回**一个覆盖全部 1200 行的差异块**（对照：100 行的同类输入返回 1 行差异）。

**修复**：闸门改成按真实步数 `d` 判定，另加一条内存预算：

```go
// maxMyersD 是 Myers 前向搜索的**步数**上限（d = 当前已走到的编辑距离）。
const maxMyersD = 2000
// maxMyersCells 是 trace 的存储上限，单位是 int 个数（约 32 MB）。
const maxMyersCells = 4 << 20

for d := 0; d <= maxD; d++ {
    // 两个上限一起框内存：步数（d）与快照总长（d*width）。
    if d > maxMyersD || d*width > maxMyersCells {
        return nil
    }
    ...
}
```

两个上限**必须一起用**：只框步数框不住内存 —— 大文件即使步数没超，
每个快照本身也很长（`2*(n+m)+2`）。

**同时给 `Deltas` 加了"剥公共前缀"**，把大文件的起始搜索成本压下来：

- **剥前缀是安全的**：`d=0` 时那条"蛇"本来就沿着对角线一路吃掉公共前缀，
  之后搜索从 `(p, p)` 出发。剥掉前缀再搜，只是把整张坐标表平移了 `p` ——
  每一步的比较、回溯的选路都逐位对应，结果**严格等价**。
- **剥后缀是不安全的**：重复行会让最小编辑脚本不唯一，java-diff-utils 选哪一份
  取决于搜索起点。实测把后缀也剥掉后，132 条跨语言黄金语料**红 22 条**
  （`a=["", "}", "a", "}"]` vs `b=["", "x", "}"]` 这种"尾部有重复行"的用例选路会变）。
  所以只剥前缀，并加了 `TestDeltasDoesNotTrimCommonSuffix` 钉住。

#### 代码区与行号列的外观（对齐原版 theme.css）

**这里踩过一次坑，值得单独记一笔。** 原版代码区是 RichTextFX 的 `CodeArea`，
而 `CodeArea` 的实例初始化块里就 `getStyleClass().add("code-area")` —— 所以
应用自己的 `theme.css` 里那两条规则**是活的**：

```css
.code-area        { -fx-font-size: 14px; -fx-background-color: #ffffff; }
.code-area .lineno { -fx-font-size: 14px; -fx-text-fill: #aeb3c2;
                     -fx-background-color: #ffffff;
                     -fx-border-color: #ebecf0; -fx-border-width: 0 1px 0 0; }
```

而 `LineNumberFactory` **库内**还写死了一套默认值（`#666` 字、`#ddd` 底、
斜体 13px）。theme.css 把这几项整条覆盖掉了 —— **库默认值在这里根本不生效**。

本移植早期照着库默认值抄（`#ddd` 底 + `#666` 字），于是行号栏成了一块灰底，
和原版对不上。2026-10-13 用户拿原版截图来对比时才发现。**教训：原版外观要读
应用的 `theme.css`，不能只读第三方库的默认样式表。**

改后的口径（实测自原版截图，2 倍缩放）：

| 项 | 值 | 出处 |
|---|---|---|
| 代码区底色 | `#ffffff` | `theme.css .code-area` |
| 代码/行号字号 | 14px | `theme.css .code-area` / `.code-area .lineno` |
| 行高 | 17px | 字体行高 + `styled-text-area.css` 的 `.paragraph-text { -fx-line-spacing: 1 }` |
| 行号字色 | `#aeb3c2` | `theme.css .code-area .lineno` |
| 行号栏底色 | `#ffffff` | 同上 |
| 行号栏右边框 | 1px `#ebecf0` | 同上 |
| 行号栏宽 | ≈ `15px + 5ch` | 见下 |
| 正文起点 | ≈ `15px + 8ch` | 见下 |

两处"留白"是从截图里量出来的，**没有**对应的 CSS 声明，所以容易被人当成冗余
padding 清掉：

- **行号右侧多约 1ch + 4px**。原版 `CodeArea` 是 `StyleClassedTextArea`，
  `LineNumberFactory` 会再挂一个"折叠指示器"（内容是一个空格的 `Label`）在
  行号右边，外加 JavaFX 默认的 4px 图文间距。少了这段，行号会贴着竖线。
- **正文左缩进约 3ch**（实测竖线在 x≈111、正文从 x≈162 起）。来源未查明，
  按实测值对齐。

`.code .lt` 的 `padding` 与 `.code .ln` 的 `padding-right` 都有走查断言钉着
（`diff-lineno-fold-space` / `diff-code-inset`），改动会立刻变红。

#### 双栏滚动同步（刻意增强，原版**没有**）

原版双栏只是把两个 `VirtualizedScrollPane` 摆进 `SplitPane`，**两边各滚各的**。
用户要求"上下滑动应该左右同步"，故补上（同样是新增能力，不是移植）。

`app.js` 的 `syncScroll(a, b)` 只同步**纵向** `scrollTop`（用户说的就是"上下
滑动"；横向各栏按自己的行宽来更合理）。两个实现上的坑，都写在代码注释里：

1. **不能用锁（flag / `requestAnimationFrame`）来防回环。** 试过"程序化同步
   时上锁、rAF 解锁"：无头浏览器里 rAF 会被饿死（`--virtual-time-budget` 下
   不产帧），锁卡在 `true`，反向同步直接失效 —— 走查里表现为
   `split-scroll-sync-back` 假红。改成**不需要锁**：赋值成相同值不会再触发
   `scroll` 事件，回环天然终止。
2. **目标栏滚不到那么远时不要硬同步。** 两栏内容长度不一样时，浏览器会把
   目标栏夹到它自己的最大值，那一次 `scroll` 事件又会把源栏拽回来 ——
   结果长的那一栏永远滚不过短栏的底部。所以先比一下目标的极限，超了就不同步
   （短栏停在底部，长栏继续走）。

#### 在另一棵树里定位（刻意增强，原版**没有**）

用户要求"点击替换文件时，找出被替换的文件位置"（经确认指：**在补丁树点文件，
应用树里展开并定位过去**）。原版没有对应物 —— 补丁树右键菜单里那个
「聚焦应用树」是**显示模式开关**（`FocusTreeStatusConst` 的 ALL/APP/PATCH，
把另一个面板从 SplitPane 里摘掉），不是定位。

后端 `gui/app.go` 的 `LocateMapped(side, path)`：

- 顺着匹配阶段建立的 `MappedNode` **双向**绑定找对端节点
  （`internal/tree/util.go` 里成对调用 `setMappedNode`），两个方向都查得到。
  接口带 `side`，用户要的补丁→应用只是其中一个方向。
- 返回 `LocateResult{State, Side, Ancestors, Path}`。**祖先链必须由后端给**：
  前端只有一份懒渲染的 DOM，没展开的层级连行都没有，靠自己推不出下一跳。
- **不需要"展开祖先"这一步。** 映射存在本身就说明匹配阶段下探过那一支，
  沿途每个祖先的子级都已经建好了（`m.Parent` 指向 p，就意味着 p 的 Children
  里有 m）。所以后端只报路径，展开交给前端。
- 根那一行永远可见，**不进 `ancestors`**（进去的话前端会去展开一个多余的层级）。
- 没有对应项 / 路径不存在 / 对端树没打开，一律**明确报错**，不回空结果 ——
  空结果会让前端静默地什么都不做。

前端 `app.js`：

- 只有**补丁树里带 `mapped` 标记的文件行**才挂定位点击。`NodeView.Mapped`
  由后端给（`n.MappedNode != nil`）；不挂给目录（点目录的语义就是展开）。
- 单击有 **200ms 防抖**，与"点名字展开"同一个套路 —— 双击是打开内容预览，
  不防抖的话双击会先顺带触发一次定位。
- `revealRow` **沿 DOM 逐层往下走**，而不是按 path 查全局索引。原因是
  嵌套 jar 内部子节点的 Path **不带 jar 前缀**（上游行为，见 §五），
  胖包里两个 jar 各有一个 `org/` 是常态，Path 完全一样 —— 查索引会走到
  别的 jar 里去。走查里专门放了一个同路径的**诱饵 jar** 来钉这一点。
- 高亮是**短暂**的（3 秒）且不做持久选中：树会因为重新匹配整个换掉，
  持久高亮可能指到另一个节点上去。

#### 树节点文件详情列（修改时间 / 大小 / MD5）

文件行右侧显示 `modTime  size  md5` 三列。三处**与原版不一致**，按"刻意偏差"记下：

1. **不接 `Configuration.isShowFileDetail` 开关**。
   原版 `FileTreeCell.addFileDetail`（`src-java/.../FileTreeCell.java:208`）
   在渲染前先 `if (Boolean.TRUE.equals(Configuration.getInstance().getIsShowFileDetail()))`
   判一下；`HomeView` 顶部有个「文件详情」复选框（`HomeView.java:57`）控制它。
   Go 侧配置层有 `config.IsShowFileDetail bool \`xml:"isShowFileDetail,omitempty"\``
   （`internal/config/config.go:164`），但 `gui.Settings` 结构体（`gui/app.go:254`）
   没这个字段、设置面板里也没复选框。
   - Go 版**永远显示**三列。
   - 原版默认 `<isShowFileDetail/>`（空 boolean 元素 = false）**默认不显示**。
   - 用户当前在截图里是勾选状态看着 = 显示。所以**默认行为不同**：
     原版新装用户先看到的是没这三列、勾选才出现。
   - 严格保真需要把字段加进 `Settings`、设置面板加复选框、`ApplySettings` 落地、
     `addFileDetail` 改成响应设置。本次未做 —— 用户当前诉求是"显示"，先满足。

2. **`humanSize` 格式 `KB/MB/GB` 1 位小数**。
   原版 `FileUtil.getSizeReadability`（`FileUtil.java:79`）用
   `String.format("%.2f", cur)` + 单字母单位 `B/K/M/G`，所以
   `4.68K`、`138.66M` 这样。Go 侧前端 `humanSize`（`dist/app.js:86`）
   是 `KB/MB/GB` + 1 位小数（< 10 时 1 位、≥ 10 时 0 位），所以
   `4.7 KB`、`135.4 KB`。**这是预存在的偏差**，本轮没动 `humanSize`。
   走查里 mock 全是 < 1024 字节（40 B / 12 B / 710 B / 600 B / 88 B），
   走 B 支不暴露差异。真实文件会出现"格式对不上"的视觉差。

3. **整组右对齐**（`margin-left: auto` 在 `.modTime` 上）。
   原版 `HBox` 默认自然排列，detail 是 `"\t" + dateTime + "  " + size + "  " + md5`
   紧跟在 `title`（文件名）后面，没有右对齐。
   - 长文件名（如 `frs-server2-springcloud-3.6.0-SNAPSHOT.jar`）会把详情挤到
     行末或换行；
   - Go 树用 `flex` 行 + `width: 100%`，整组右对齐能保证详情列始终在固定位置
     可读；
   - 视觉上跟原版截图（`FileNameController.class  2026-10-12 16:10:14  41.88K …`）
     不一样。算**有意识的选择**而不是 bug，记在这。

4. ~~列宽紧张时"文件名优先、MD5 先让位"~~ —— **已被下一轮需求取代，仅作历史记录**。
   曾经给 `.name` 加过 `min-width: 100px`、让 `.md5` 先收缩并省略；但用户随后
   对着原版截图指出：原版是**不压缩 + 横向滚动**（TreeView 的 cell 按内容宽度
   排布，装不下就出横向滚动条）。两套方案互斥 —— 压进容器就没有滚动条，
   而用户要的正是后者。Go 版现已改为不压缩 + 横向滚动，详见 §五 第 24 项。
   - 这一轮唯一保留下来的：`.name` 是 flex 容器、纯文件名单独一层 `.name-text`、
     「已修改」星号 / README 标签挂在 `.name` **内部**、删除线写在 `.name-text` 上
     （原因见 §五 第 22 项的"文件名被挤压"补充）。

#### 编辑包内文件内容（刻意增强，原版**没有**）

右键文件 →「编辑内容」→ 改 → 保存。**原版没有这个能力**，是用户提的增强需求。

先说清楚"原版没有"这件事，因为很容易看走眼 —— i18n 里明明有 `app.view.content.edit-mode=编辑模式`
这行文案，看着像是做过：

- `CodeAreaFactory.getContextMenu` 里整个 `editModeItem` 被**注释掉了**
  （`}/* else { ... }*/` 那段，源码 110-115 行）；
- `CodeAreaFactory.create()` 无条件 `codeArea.setEditable(false)`（141 行）；
- `ContentView.handleEditMode(CodeArea)` 是**空方法**，体内只有一行被注释的
  `// codeArea.setEditable(!codeArea.isEditable());`。

也就是说作者把文案和菜单项都写好了，最后一步没接上。原版唯一能改包内内容的
路径是 **READ ME 编辑器**（`ReadmeView` → `PatchPackService.updatePatchReadme`），
只针对说明文件。另注意 `EditFileTreeCell` 那个类名有迷惑性 —— 它是**改文件名**
（重命名节点），不是改内容。

**三个设计决策**（已经过用户确认）：

1. **只改内存，不动磁盘上的原包；另提供「另存为」把内容导出到磁盘。**
   这样不会在用户没意识到的时候改坏他手上的包。
2. **所有文件都能编辑**（含二进制）—— 二进制走十六进制编辑器；
   `.class` 默认给反编译源码，需要改字节时切过去（见下）。
3. **两棵树都开放**（应用树 + 补丁树）。

**为什么这套改动可以压得很小：写包全走 `node.ConsumeBytes`。**
`service/writer.go`、`service/patchsave.go`、`service/base.go`、`service/patch.go`
读内容都经过它。所以只要把节点的 `source` 换掉（`tree.Node.SetContent`），
写出侧**不需要加任何分支**就自动用上新内容 —— 这是整个方案的地基。

六个容易写错的点：

- **`SetContent` 必须清掉 `Md5` 与 `Size`。** 两个字段都是"读一遍顺手填"的缓存，
  `GetMd5OrInit` / `GetSizeOrInit` 见到旧值就直接返回。不清的话旧值会流进
  patch-up sign（`DelInfo.AppMd5` / `AddInfo.PatchMd5` / `ModInfo.*Md5` /
  `OriginAppSize`），下游按 sign 校验就对不上。
  **但别把后果说大**：写出侧不会因此写坏 zip —— `writeNodeEntry` 对 STORED 条目
  是从实际数据重算 crc/大小，DEFLATE 条目走数据描述符，都不读 `n.Size`。
  变异验证时实测过：去掉这两行，只有针对性的单测
  `TestSetContentResetsSizeAndMd5Cache` 变红，端到端那条**不红**。
- **编码必须能指定，且不能一律 UTF-8。** 原文件是 GBK 的，改完就得按 GBK 写回，
  否则这个包从这一刻起整段乱码。为此补了与 `DecodeString` 对称的 `EncodeString`。
  这里有一处**刻意偏差**，见 §3.9。
- **二进制按内容判，不按后缀。** 口径：前 8 KB 出现 NUL 就算二进制（git / grep 的
  常用口径）。后缀是 `.txt` 的二进制文件现实中是存在的；反过来 UTF-16 文本会带
  NUL、会被归到十六进制那侧，这也是合理的（按文本编 UTF-16 本来就会写坏）。
- **`.class` 默认给反编译出来的源码，另有「改字节」入口切到十六进制；**
  **有 JDK 时还可「编译为 .class」写回包。**
  源码虽然"能预览"，但源码**不能按普通保存存回** `.class` —— 那里存的是字节，
  源码要经 `javac` 配合完整依赖重新编译才能变回 class。所以源码形态**没有「保存」**，
  只有「编译为 .class」（有 JDK 时）或「另存为 .java」，而且保存按钮是直接隐藏
  而不是置灰（露出来本身就是误导）。反编译不可用（本机没装 Java）时**不报错**，
  静默退回十六进制 —— 与 `OpenContent` 对 `ParserClass` 的处理同口径：
  前置条件缺失是降级，不是故障。
  （最初的版本是"`.class` 一律走十六进制"，用户看了实际界面后反馈"点编辑内容
  想改的是 Java 源码"，遂改成默认源码 + 保留改字节入口。）
- **十六进制编辑器必须分页。** 整段铺出来，一个 1 MB 文件就是 6.5 万个 `input`，
  界面直接卡死。`HEX_PAGE = 512`（32 行 × 16 列），前端只渲染当前页。
  字节整段 base64 传一次（翻页不再往返后端）。顺带一个坑：`bytesToBase64`
  不能一次性 `String.fromCharCode.apply(null, bytes)` —— 几十万个参数会爆栈，
  要按 `0x8000` 分块拼。
- **编辑是独立入口，不改动双击预览那条路。** 「编辑内容」走 `OpenEditor`，
  双击走 `OpenContent`（没有解析器的文件仍交系统程序打开）。这样"双击预览"的
  行为仍与原版一致。

两个界面上的取舍：

- **编辑态复用预览浮窗**，只在底部多一条按钮条。不另开一个浮窗，是因为那样要
  把拖动 / 缩放 / 记忆尺寸三块逻辑再写一遍，而同一时刻只会开一个。
- **"已修改"用名字后面一个星号，不用行底色。** 行底色已经被补丁状态
  （`t-add` / `t-mod` / `t-del`）占用了，那是"补丁对这个文件做了什么"，
  而这是"我改过它" —— 两个维度不能挤在一个通道里。

**应用树右键菜单**（原版 `HomeLeftTreeView.buildAppTreeContextMenu` 有 13 项）
一并落地了 8 项：重新加载、取消关联、复制 ▸、编辑内容、保存文件、
资源管理器中查看、关闭应用包、聚焦应用树 —— 顺序与各自的 claim 逐条对齐原版
（含"聚焦应用树"**没有 claim、永远显示**这条）。其余 5 项未做，理由见 §五。

#### `.class` 反编译（双击 class 看源码）

原版双击 `.class` 会弹出反编译后的 Java 源码。早先 Go 版这里只弹一句"未移植"，
用户实际用起来就是**双击 class 什么都看不到** —— 这是功能缺失，不是刻意偏差。
现已落地，做法与取舍见 §2.1。

四个容易写错的点：

- **内部类要跟着主类一起传。** 内部类在树里挂在外部类下，原版
  `ClassContentParser.parse` 会把所有子孙类的字节一并交给 fernflower。
  只喂主类的话，反编译出来的内部类是**空壳** —— 这个差异很隐蔽，
  已用基准钉住（`TestOpenContentClassPassesInnerClasses`）。
- **解析器只看左节点。** 原版 `contentParser.parse(rightNode)` 复用同一个 parser
  实例，所以左 `.java` 右 `.class` 时，右侧也按文本读，而不是去反编译。
  按"每个节点各自看自己的扩展名"实现就与原版不一致了。
- 反编译产出的源码走**与文本完全相同**的渲染/差异路径，高亮用 `LangJava`。
- 起一次 JVM 约 1 秒，所以前端双击 class 时**先把面板摆出来显示"正在反编译…"**，
  内容到位再替换 —— 否则用户对着没反应的界面会以为卡死。

**补丁树右键「保存源文件」已落地**（原版 `HomeTreeView.saveSourceFile`）：只对
`.class` 显示，把 class 反编译成 Java 源码后走"另存为"，默认文件名把后缀换成
`.java`（`Foo.class` → `Foo.java`），并带 `*.java` 过滤器。

三个对齐原版的点，以及一处**刻意偏差**：

- **claim 条件是 `!isDir() && name.endsWith(".class")`**，判据抽成了
  `isClassNode`，"菜单显不显示"和"动作做不做"共用同一个函数 ——
  各写一遍 `endsWith` 迟早会漂移。
- **非 `.class` 静默 return**，不报错。原版第一句就是
  `if (selectedItem == null || !name.endsWith(DOT_CLASS)) return;`，
  连错都不报。菜单项本来就只在 `.class` 上出现，走到那里说明前端传错了，
  弹个错只会打断用户。
- **反编译失败要报错**，不能像编辑器那样降级。原版
  `ClassContentParser.parse` 抛异常后由全局异常处理器接住，用户是能看见的；
  导出个空文件才是坑。（编辑器那边降级是对的 —— 那里至少还能按字节改。）
- **⚠️ 刻意偏差：导出编码固定 UTF-8。** 原版写的是
  `content.getBytes()`，即**平台默认编码**；原版 target 是 Java 11，
  在中文 Windows 上就是 GBK。GBK 编不出的字符（emoji 等）会被写成 `?`，
  属于**有损**；UTF-8 无损，且与本工具内「另存为 .java」一致。

**仍未做**：应用树的「保存源文件」。原版两棵树各注册了一份
（`HomeLeftTreeView` / `HomeRightTreeView`），本轮只接了补丁树 ——
后端 `SaveSourceNodeAs` 是按 `side` 参数写的，补上应用树只差一行菜单项。
`TestSaveSourceFileMenuOnlyOnPatchTree` 把"还没接"显式钉住了：真去接的时候
它会红，逼着一起改这里、`appMenu` 的注释和前端走查。

另：补丁树右键「保存为项目」（原版 `JarDecompilerService` 整体导出）仍未做，
理由见 §五。

#### 搜索：三层入口（需求 L）

用户问"对比一下 java 端，是不是缺少一个搜索的快捷键"。查下来**缺的不是一个
快捷键，是整套搜索入口** —— Go 版此前 `app.js` 里唯一的 keydown 处理只有
`Escape`。原版一共四处，用户确认"三个都补"：

| 原版入口 | 快捷键 | 实现位置 |
|---|---|---|
| 树上**直接打字即搜** | 无快捷键，打字触发 | `view/home/FastSearchControl.java` |
| **全局搜索对话框** | `Ctrl+F` / 工具栏搜索按钮 | `view/search/SearchView.java` |
| 代码编辑器内查找 | `Ctrl+F` | `control/code/SearchBox.java` |
| 代码编辑器跳转行列 | `Ctrl+G` | `control/code/GotoRowColDialog.java` |

（还有第五处 `Ctrl+S` → `handleSaveAppPack`，那是保存不是搜索，本轮没做，
仍留在 §四。）

**① 树上打字即搜**（`app.js` 的 `initFastSearch` / `fastSearch*`）。
原版把按键转发给一个**宽度随文本变化**的隐藏输入框（`TextDynamicWidthField`），
由它的 `textProperty` 驱动搜索；命中后按"离起点最近"的三段判据选一个目标并滚过去，
`Ctrl+Alt+↑/↓` 前后跳，`Esc` 取消。Go 版逐条对齐：

- 浮层输入框绝对定位在树上（`.fastsearch`），宽度用一个离屏 `span` 量文本宽度
  （原版用 JavaFX `Text.layoutBounds`，浏览器里等价的手段是量 DOM）；
- 树要能拿键盘焦点（`host.tabIndex = 0`），点一下树先 `focus()`；
- **同时支持 IME**：原版只吃 `keyTyped`，中文输入法的组合输入不产生单字符
  keydown，所以这里额外接了输入框的 `input` 事件；
- 遍历**只看已展开的行**（原版同样只走已建出来的 TreeItem）。

**② 全局搜索对话框**（`gui/search.go` + `app.js` 的全局搜索段）。
**搜索本身放在 Go 侧**，前端只负责画结果与跳转。理由是原版的遍历对象是内存里的树，
而且路径式搜索会为了"探进嵌套 jar 里找"而**现场解压**（`searchNodeWithPath` 里的
`buildChildrenWithCompress` 分支）—— 前端只有一份懒渲染的 DOM，既看不到全部节点
也没有解压能力。行为逐条照搬：

- 空关键词（口径是 Java `StringUtil.isBlank`）→ 展示**搜索历史**；
- 单关键词 → `name.contains(keyword)`（**区分大小写**），遍历两棵树；
- 含 `*` → `Pattern.compile(keyword.replace("*", ".*"))` + **全匹配**，
  即 `*.js` 是"以 .js 结尾"而不是"包含 .js"；
- 含 `/` 或 `\` → 按 `FileNameUtil.splitPath` 分段，沿父子链逐级匹配，
  中途遇到没展开的压缩包就现场解压；**这一支一个都没命中就把解压结果回滚**
  （原版 `node.setChildren(oldChildren)`，免得用户搜了个不存在的东西却发现
  树里凭空多了个展开的 jar）；
- 结果项的显示路径 = **根标签 + 各级祖先名**（`应用包:/com/example`），
  **不含节点自己** —— 原版 `String.join(SEPARATOR, paths)` 就是这个口径；
- 去重按 `(path, name)`（原版 `LinkedHashSet<SearchItem>`）；
- 单击**历史项**把关键词灌回搜索框（会连带重新搜一次）；单击节点项什么都不做；
  双击**节点项**在对应的树里选中并滚过去、写一条历史、关掉对话框；
  双击历史项什么都不做（原版如此，见 §3.9 的说明）；
- 每项都带回**祖先链**，前端双击时照着逐层展开 —— 前端算不出嵌套 jar 内部的
  路径，这一点与 `LocateMapped` 同一个理由。

**③ 面板内查找 + 跳转行列**（`app.js` 的 `initFindBar` / `find*` / `goto*`）。
原版挂在 `CodeEditor` 上，而 `NodeCodeEditor extends CodeEditor`，所以**预览面板
同样有这两个快捷键**。Go 版挂在 `#content-win` 上（焦点在面板内任意处都生效）：

- `Ctrl+F` 打开查找条（顶部一条：搜索框 + `n/共 m` 计数 + `↑`/`↓` + `✕`），
  打开时把当前选中的文本回填进去（跨行的选区不回填 —— 原版 `SearchBox` 同样只
  在单行选区时回填）；命中**不重叠**、从左到右；从 caret 往后挑第一个命中；
- **预览模式也能搜了**（需求 Q，此前 `findArea()` 只认 editor textarea、预览里
  `Ctrl+F` 静默失效）。现在 `renderContent()` 在 `#content-body` 里铺一个
  **hidden 的镜像 textarea**（`.edit-text.mirror-text`，内容为两栏纯文本、split
  用 `\n\n` 拼），`findArea()` 于是返回它 —— 查找条那套 `findHits`/`findCount`/
  `findStep` 的**全部数学逻辑零改动**；命中再靠 **CSS Custom Highlight API**
  （`CSS.highlights` + `::highlight(find-hit)` / `::highlight(find-active)`）画到
  只读渲染层上，**不改动 DOM**，从而保住反编译代码的语法着色（token span 的
  颜色还在）。命中跨行按行拆成多个 `Range`，双栏两栏都搜、各自染色；
  见 §五 第 40 项；
- `Ctrl+G` 打开「跳转行&列」，偏移与行:列双向联动，确定后滚到那一行并移动 caret；
- 两者都在 `renderContent()` / `renderEditor()` 开头先关掉（正文换掉了，查找条
  得跟着收）；
- **预览面板之外的 `Ctrl+F` 走全局搜索**。原版是"主窗口 `rootPane` / 内容对话框
  `CodeEditor` 各管各的"；这里靠事件冒泡的顺序等价实现：`#content-win` 的处理器
  会 `stopPropagation()`，所以焦点在预览面板里时事件到不了 `document`。

**两处原版真 bug，Go 版按语义修正**（不是"顺手改好"，是被走查逼出来的）：

1. `FastSearchControl.selectPre/selectNext` 用 `curIndex.getAndUpdate(...)` 的
   **返回值**取元素，而 `getAndUpdate` 返回的是**旧值** —— 两个方法因此取到同一个
   元素，`Ctrl+Alt+↑` 显示的其实是当前项的**下一项**。Go 版按语义做成
   "↑ 前一个 / ↓ 后一个"。
2. `GotoRowColDialog` 对非法输入直接把输入框内容**改写成 i18n 的「无效」两个字**
   （`invalidPos(field)` → `field.setText(I18nUtil.t("app.control.code.goto-invalid"))`），
   用户没法接着改。Go 版改成标红边框、**保留输入**（`.field input.invalid`）。

两条都登记在 §3.9。

**仍未做**：`Ctrl+S`（保存/打补丁，原版 `HomeView.handleSaveAppPack`）。
用户这轮要的是"三个搜索入口"，保存快捷键不在内 —— 留在 §四。

### 代码规模对照

原 Java 侧 148 个文件、14,625 行。按包拆分：

| Java 包 | 文件 | 行数 | Go 侧归属 |
|---|---:|---:|---|
| `view` | 35 | 4,400 | 🚧 主页已落地（`gui/`），其余屏待做 |
| `control` | 32 | 2,552 | 🚧 树的展示与状态着色已落地，其余待做 |
| `util` | 22 | 2,538 | ✅ 已移植非 UI 部分 |
| `service` | 14 | 2,154 | 🚧 部分 |
| `core` | 10 | 1,180 | ✅ 配置部分；`AppContext` 待做 |
| `model` | 9 | 819 | ✅ |
| `constant` | 15 | 631 | ✅ |
| `fernflower` | 5 | 186 | ✅ 桥接层等价实现见 §2.1（外挂同一份 jar，不重写） |
| `common` / `exception` | 4 | 89 | 待做 |

**结论：约 6,700 行（service + util + core + model + constant）是纯逻辑，可直译；
约 6,950 行（view + control）是 JavaFX 界面，必须重写。** 后者不是"翻译"，
是按新框架重新实现，工作量取决于框架选型。

---

## 二、关键决策

### 2.1 反编译 —— 已落地（方案 A：内置 Java 运行时 + 原版 fernflower jar）

原项目的"代码反编译"功能依赖 JetBrains 的 **Fernflower**（`lib/fernflower-1.3.0.jar`，
615 KB）。Java 侧那 186 行（`fernflower/` 包）只是桥接层，真正的反编译器是外部 jar。

**Go 生态没有成熟的 Java 字节码反编译器。** 当初评估的四条路线：

| 方案 | 做法 | 优点 | 代价 |
|---|---|---|---|
| **A. 外挂 JRE** ✅ 已采用 | Go 子进程调 `java -jar fernflower.jar` | 功能与原版一致，最快落地 | 每次反编译起一次 JVM（约 1 秒），但同会话内同一 class 重复打开会走内存缓存、不再起 JVM（见 §一「.class 反编译」与 §五 第 39 项）。**2026-10-16 起 java 不再要求用户自备 —— 改为内置**（见 §五 第 57 项） |
| **B. 降级为字节码解析** | 只解析 class 文件的常量池/方法签名，输出类似 `javap` 的结构 | 纯 Go、单文件 | 没有"源码"体验，与原版功能差距大 |
| **C. 自研反编译器** | 实现控制流重建 + 表达式恢复 | 纯 Go、功能完整 | 月级工作量，Fernflower 是多年积累的产物 |
| **D. 嵌入 libjvm** | cgo 调 JNI 启动 JVM | 功能一致 | 分发与构建复杂度高于 A |

**落地情况**（`internal/decompile`）：把**原项目那份 fernflower jar 原样嵌进二进制**
（`go:embed`），运行时释放到用户缓存目录执行。

**2026-10-16 补充：java 也内置了。** 原先调的是**本机** `java`，于是要求用户机器装
Java 11+（反编译）/ JDK（编译）—— 而原版是 GluonFX native-image、**自带 JVM**，
这属于移植带来的功能退化。现在改为内嵌一份 jlink 裁出来的运行时
（`java.base` + `jdk.compiler`，见 `internal/javaruntime` 与 §五 第 57 项），
查找顺序是 **内置 → `JAVA_HOME` → `PATH`**。

为什么选 A 而不选 B —— **保真**。用同一份 jar、同样的默认选项，反编译结果才可能与
原版逐字节一致；B 是"另做一个功能相似的替代品"，那已经不是移植了。

实现上有三个必须处理的点，都写在包里了：

1. jar 的释放与 sha256 校验（缓存在 `%LOCALAPPDATA%/PatchAssistant2J/`）；
2. **从 class 字节里解析类名** —— fernflower 的输出文件名取自**字节里的类名**，
   不是我们交给它的临时文件名。不解析就不知道该读回哪个 `.java`；
3. Windows 上 `HideWindow`，否则起 `java` 子进程会弹一个控制台黑框。

**已用跨语言基准验证**（`_extract/tools/`）：`decomp_consistency.sh` 把命令行方式与
原版 API（`SingleClassDecompiler`）的输出逐字节比对 —— 40 个单类样本、12 个干净重跑
样本、含内部类的样本、含中文的样本，**0 差异**。所以命令行方式可以放心用，
不必自己写 Java 驱动去调 API。

Go 侧另有 `internal/decompile` 的 6 样本基准（含中文常量、枚举、接口、泛型抽象类、
内部类、41 KB 大类），比对的是原版 API 产出的 `.java`。

**代价与降级**：内置之后，**用户机器不需要装任何 Java** —— 反编译与编译都从内置
运行时取。代价是 exe 从 12.45MB 涨到 **39.95MB**（内嵌 zip 27.5MB），首次使用要把
108 个文件解压到 `%LOCALAPPDATA%/PatchAssistant2J/runtime-<版本>/`（实测 **0.42 秒**，
之后复用）。

内置运行时解压失败（磁盘满、缓存目录不可写）时，`FindJava` 会退回本机 Java，
两边都没有才返回可判定的 `ErrNoJava`，界面把文案原样显示出来而不是静默失败。

**外部 Java 的版本要求是 11 或更高**（内嵌 fernflower jar 的 class 是 major 55）；
低于 11 时 JVM 在进 main 之前就退出，只留一句没信息量的 `A JNI error has occurred`
—— 所以 `decompileFailure` 会把 stderr 第二行的 `UnsupportedClassVersionError`
捞出来翻译成"本机 Java 版本过低"（真机撞过，见 §五 第 55 项）。内置的是 JDK 21，
所以这条只在使用 `PATCH_ASSISTANT_NO_BUNDLED_JAVA` 绕过内置时才可能遇到。

### 2.2 GUI 框架 —— 影响 6,950 行重写量

原界面需要的能力：多层树（应用树/补丁树，支持懒加载展开）、
代码编辑器（RichTextFX 等价物，带行号、语法着色）、
**文本差异高亮对比**（java-diff-utils）、拖拽、右键菜单、主题换肤
（当前用亮色，见 §一"配色"）。

| 方案 | 适配度 | 说明 |
|---|---|---|
| **Wails v2/v3** | 高 | Go 后端 + WebView 前端。树控件、代码编辑器（CodeMirror/Monaco）、diff 视图都有现成生态，是最省力的路线 |
| Fyne | 中 | 纯 Go、单文件。但树控件能力弱，代码编辑器与 diff 高亮要自己实现 |
| Gio | 低 | 更接近绘制层，开发量大 |

**建议 Wails**，主要因为"代码编辑器 + 语法着色 + 差异高亮"这一块，
Web 生态的成熟度远超任何 Go 原生 GUI 库。

> **已定：Wails v2.15.0**（阶段 3 已按此开工，见 §一"阶段 3 已开始"）。
> 选 v2 而非 v3 是因为本机 CLI 与运行时库都是 2.15.0，版本对齐最省事。
> 注意 `go mod tidy` 默认会拉到更新的 v2.16.0，需显式
> `go get github.com/wailsapp/wails/v2@v2.15.0` 降回与 CLI 一致。
> 另外本机 `proxy.golang.org` 不通，拉依赖要临时
> `export GOPROXY="https://goproxy.cn,direct" GOSUMDB="sum.golang.google.cn"`。

### 2.4 字符集探测与解码

**探测**：Java 用 `juniversalchardet`。Go 标准库无等价物，
`internal/util/charset.go` 是三层近似（BOM → UTF-8 校验 → 回落 GBK），
**精度低于 Java 版**（juniversalchardet 还能识别 Big5 / Shift_JIS /
windows-1252 等，本实现一律当 GBK）。要完全等价需接 chardet 类库。

**解码**：已接 `golang.org/x/text/encoding/simplifiedchinese`，GBK 分支用
**GB18030** 解码器 —— GB18030 是 GBK 的超集，对 GBK 合法的双字节序列两者映射
完全一致，所以解 GBK 不会解错，还顺带覆盖 4 字节序列和 juniversalchardet
偶尔报的 "GB18030"。

> 2026-10-10 之前这里是个 TODO 桩（GBK 分支按 UTF-8 透传 + 替换非法序列），
> 后果是**中文 Windows 上导出的补丁说明文件在底部「补丁信息」面板里整段变问号**。
> 已修，回归测试见 `internal/util/charset_test.go` 与
> `gui/app_test.go` 的 `TestReadmeTextDecodesGBK`。

**仍未做的**：UTF-16 带 BOM 的文本。`DetectCharset` 会认出 UTF-16 BOM，
但 `DecodeString` 统一按 UTF-8 处理（原注释里就标了"暂统一"）。实践中没遇到过
UTF-16 的补丁说明文件，先留着。

### 2.5 zip 条目名的字符集（必须处理，否则中文文件名乱码）

Java 版是 `new ZipInputStream(in, CharsetConst.GBK)` —— **假定条目名是 GBK**。
Go 的 `archive/zip` 只在条目带 UTF-8 标志位时按 UTF-8 解码，否则原样返回字节。

→ 需要在 `internal/pack/zip.go` 补 GBK 解码。**仍未做**，见 §五。

注意这一条与"写出的包要不要置 UTF-8 标志位（0x800）"**必须一起做**：
单独置位会让 GBK 名字被读取方按 UTF-8 解成乱码，详见 §3.10 的 B 类第三条。

---

## 三、上游行为清单（移植时必须照搬，不要"顺手修好"）

以下都是静态分析或实跑确认过的既有行为。其中若干项看起来像 bug，
但**照搬是刻意的** —— 改掉会让 Go 版与 Java 版产生行为分叉，
在过渡期两版并存时会造成难查的不一致。真要修，应作为独立议题与原项目同步。

### 3.1 README 升级指令：前缀必须紧贴路径

正则（`ReadMeFileService`）是：

```
([!+-?]?)[\\/]?(FILE_NAME_CLASS+)(\s+[\\/]?(FILE_NAME_CLASS+))?(\s+(FILE_NAME_CLASS*))?
```

而 `FILE_NAME_CLASS` 里**不含空白**。所以前缀后跟空格时正则回溯，前缀被整个丢弃：

| 写法 | 实际解析结果 |
|---|---|
| `+foo.class` | type=ADD, path1=`foo.class` ✅ |
| `!foo.class` | type=MOD, path1=`foo.class` ✅ |
| `-foo.class` | type=DEL, path1=`foo.class` ✅ |
| `?foo.class` | type=IGNORE, path1=`foo.class` ✅ |
| `+ foo.class` | type=NONE, path1=`foo.class` ← 前缀被吞 |
| `! foo.class` | type=NONE, path1=`foo.class` ← 前缀被吞 |
| `- foo.class` | type=NONE, path1=`-`, path2=`foo.class` ← 退化，且算出 `foo.class/-` 畸形路径 |

**已用原版 Java 正则实跑核对**（`../_extract/ReadMeVerify.java`），
并固化为回归测试 `TestParseReadMePrefixMustBeAttached`。

附带一个次要观察：首段字符类 `[!+-?]` 在 Java 里不是"四个符号之一"，
因为 `+` 与 `?` 之间形成区间 `[+-?]`（0x2B..0x3F），会一并匹配
`+ - . / 0-9 : ; < = > ?`。区间内的非四符号最终被 `ofCode` 归为 NONE，
所以实践中无副作用，但解释了为什么 `1 foo.class` 会解析成 path1=`1`。

### 3.2 `isBlank` 不认 `\t` `\n` `\r`

`StringUtil.isBlank` 用的是 `Character.isSpaceChar`（Unicode 的 Zs/Zl/Zp 类），
**不是** `isWhitespace`。因此 `\t`、`\n`、`\r`、`\v`、`\f` 都不算空白：

```java
StringUtil.isBlank("\r\n")  // == false
```

路径判空、README 解析里都会走到这个方法，Go 版已按原语义实现。

### 3.3 `META-INF/MANIFEST.MF` 的特判是死代码

`BasePackService.buildChildrenWithCompress` 里有两处特判：

```java
if (JarInfoConst.MANIFEST_PATH.equals(node.getName())) { ... }   // MANIFEST_PATH = "META-INF/MANIFEST.MF"
if (JarInfoConst.META_INFO_DIR.equals(node.getName())) { ... }   // META_INFO_DIR = "META-INF/"
```

但 `node.getName()` 取的是 `FileNameUtil.getFileName(entry.getName())`，即**只有文件名**
（`"MANIFEST.MF"`）。拿它去和含目录的字符串比较永远不等 → **两个分支从不生效**。

Go 版按实际生效的通用路径实现，未复制死分支。该结论来自静态分析，**未做运行时验证**。

### 3.4 `Application.groupId` 永远读不到值

模型字段是 `groupId`，但资源模板 `src/main/resources/PatchAssistant2J.xml` 里写的是
`<grouopId>`（多一个 o）。而 `XMLUtil` 关掉了 `FAIL_ON_UNKNOWN_PROPERTIES`，
于是这个拼写错误被静默忽略，`groupId` 始终为 null。

影响面很小：`Application` 只在 `AboutView`（关于页）被读取，所以表现为
**关于页的"组织"一栏是空的**。Go 版 `model.Application` 的 XML tag 沿用了
`grouopId` 以保持行为一致，并加了注释。

### 3.5 `ChangeType.NONE` 在匹配流程里按 `MOD` 处理

`PatchPackService.mappedNodeWithType`：

```java
case MOD:
case NONE:
default:
    mappedModNodeWithType(...);
```

即"没写前缀"的指令会被当作修改处理。结合 3.1 的结论 —— 写成 `+ foo.class`
（带空格）的意图是"新增"，实际会走修改分支；只有当应用树里找不到该路径时，
才会因为 `doMatchMappedNodeChildrenWithReadme` 接受 NONE 而回落成新增。

### 3.6 补丁签名写入位置

配置项"写入补丁签名"开启时，签名写入应用包内 `META-INF/patch-up.sign`。
（来源：1.3.x 说明文档配置表；当前源码 `AppConst.PATCH_UP_DIR = "patch-up"`、
`JarInfoConst.PATCH_UP_PATH = "META-INF/patch-up/"` 与之对应。）

### 3.7 1.3.x 有、1.4.7 已移除的功能

1.3.x 说明文档里存在、但当前源码 grep 不到的功能：

- **RAR 支持**：配置项"UnRar 路径"（默认 `C:\Program Files\WinRAR`），用于解压 rar5
- **Docker 镜像模式**：本机/远程 Docker、命令、目录、地址、用户名、密码等一整套配置

`FileExtConst` 现在只有 `zip`/`jar`/`class`/`java`/`txt`/`sign`，
`FileDecompressor.doGetInstance` 也只认 `.jar` / `.zip`。**移植时不需要实现这两块。**

### 3.8 补丁包 README 写回（`updatePatchReadme`）的行为

**分支依据是节点类型，不是文件名后缀。** `updatePatchReadme` 用
`readmeNode instanceof CompressNode` 决定"文本写进压缩包"还是"直接写磁盘"。
所以 Go 侧 zip 建树必须产出 `Kind == KindCompress` 的节点 ——
退化成 `KindFile` 会让 README 被写到**当前工作目录**。已由
`TestUpdatePatchReadmeExisting` 断言钉住。

**非 `.zip` 结尾的补丁包，节点名会被改成 `.zip`。** `updatePatchPackWithZip`
里 `if (!name.endsWith(".zip")) setName(getName(name) + ".zip")`。
所以叫 `patch.jar` 的补丁包编辑过 README 后，节点名会变成 `patch.zip`
（磁盘文件名不变，因为最后会 `copyTo` 回原路径）。这正是回滚逻辑存在的唯一理由。
已由 `TestUpdatePatchReadmeRenamesNonZipRoot` 固化。

**回滚逻辑漏了 `i++`（上游 bug，照搬）。** catch 分支里：

```java
i = 0;
for (FileNode node : rootNodes) {
    node.setName(oldRootNodes[i].getName());   // ← i 从未自增
    ...
}
```

所有节点都被恢复成 `oldRootNodes[0]` 的状态。正常场景下 `rootNodes` 只有一个
元素（补丁包本身就是压缩包），所以平时不显形；只有"README 挂在顶层嵌套 jar 上"
时才会暴露。已由 `TestRollbackPackNodesReplicatesUpstreamBug` 作为**特征测试**钉住 ——
哪天有人补上 `i++`，这条用例会红。

**重写补丁包不会先展开压缩包。** `writePatchPackWithZip` 只遍历节点的**现有子级**。
对一个"还没展开过"的内层 jar 写 README，重写出来的 jar 里就只剩 README 一个条目，
原有 class 全丢。这不是移植引入的（Java 是同一套写法），且真实使用中用户会先
在树里展开该节点。已由 `TestUpdatePatchReadmeNestedUnexpandedDropsContent` 固化。
**结论来自对 Java 源码的静态分析，未跑原版 Java 复现。**

**逐级重写实际是"一层"为主。** `updatePatchPack` 的向上收集循环要求祖先链上
**连续**都是 `KindCompress`，而补丁包根节点由 `getTreeNode` 建成文件节点
（`KindFile`），所以循环总是收到根节点就停 —— 只有"顶层嵌套 jar 被标为自定义根"
这一种情形会收集到两层。

### 3.9 刻意与 Java 不同的地方

> **命名（标识符）层面的 Java↔Go 对照**单独成篇：`NAMING_AUDIT.md`。
> 那份文档按「逐字沿用 / 大小写风格转换 / 已改名 / Go 新增 / 未移植」五类，
> 逐项列出每个 Java 原名 → Go 现名。**本节只登记行为差异，不重复命名清单** ——
> 改任何标识符前先过一遍那份清单，确认不是"跨语言边界标识"（i18n 键、配置 XML
> 元素名、图标文件名、高亮类名、枚举值那几类回改会破坏 Java/Go 共存与资源复用）。

以下差异是**有意为之**，改动前请确认不会破坏兼容性：

| 差异 | 位置 | 理由 |
|---|---|---|
| `BOOT-INF/lib/` 的并发匹配改顺序执行 | `service/match.go` | 上游用 5 线程共享无锁 HashMap，存在竞态；顺序执行结果确定 |
| 正则编译失败时放行全部 | `service/match.go` | 上游抛异常中断整轮匹配 |
| 读节点内容失败返回空而非抛出 | `service/base.go` | 上游会一路炸到调用方 |
| `GetNodePathByParent(nil, _)` 返回 `"/"` | `tree/util.go` | 上游 NPE |
| `updatePatchPack` 收集为空时返回 error | `service/patchsave.go` | 上游 `getLast()` 抛 `NoSuchElementException` |
| README 文本固定写 UTF-8 | `service/patchsave.go` | 上游 `text.getBytes()` 用平台默认字符集；读取侧有字符集探测，往返一致 |
| 编辑包内文件时按**用户指定/探测到的**字符集写回，不用平台默认 | `gui/editor.go` `contentBytes` / `util.EncodeString` | 上游 `PatchPackService.updatePatchReadme` 用 `text.getBytes()`（平台默认，中文 Windows 上是 GBK）。改包内任意文件不能这么干 —— 原文件是什么编码就得写回什么编码，否则整包乱码。所以编辑面板带编码选择器，默认取探测结果 |
| GBK 分支的**编码**用 GB18030 编码器，不是严格 GBK | `util.EncodeString` | Java 的 `Charset.forName("GBK")` 是严格 GBK，表示不了的字符（emoji、部分 CJK 扩展区）按 REPLACE 写成 `'?'`（0x3F）、内容静默丢失。这里用 GB18030（GBK 超集，覆盖全部 Unicode，穷举 U+0000..U+10FFFF 零失败）写成 4 字节序列。**理由**：解码侧本来就用 GB18030（为接受"juniversalchardet 报成 GB18030"的文件），编码侧若退回严格 GBK，"打开含 4 字节序列的 GB18030 文件 → 改一个字 → 保存"就会把那些字符全变成 `'?'` —— 我们自己刚接受的文件被自己写坏。**代价**：产出的包若被只认严格 GBK 的下游按 GBK 解码，4 字节序列会解不出来。由 `TestEncodeStringKeepsEmojiAsGB18030NotQuestionMark` 守着 |
| 拖入空文件列表时显式拒绝而非越界 | `gui/app.go` `HandleFileDrop` | 上游 `files.get(0)` 抛 `IndexOutOfBoundsException`（被全局异常处理器吃掉，界面无反应）；Go 里越界是 panic、会直接终止进程 |
| 非法拖入给明确提示而非静默忽略 | `gui/app.go` `HandleFileDrop` | 上游因 JavaFX 语义（见下）根本不投递 `DRAG_DROPPED`，表现为"拖了没反应"。这里改成一句说明，**结果同样是"什么都没打开"**，只是不再让人以为程序卡了 |
| 标记根节点时应用包为空不刷新 diffInfo | `gui/app.go` `refreshRoot` | 上游 `appTreeInfo.setPatchDiffInfo(null)` 会 NPE（被全局异常处理器吃掉）；Go 里是 panic、会终止进程。标记根节点时应用包可以还没打开 |
| 标记说明文件时父级不是根则报错而非 NPE | `gui/app.go` `MarkReadme` | 上游 `getRootInfoByNode` 返回 null 后直接解引用。菜单项的显示条件已挡掉这种情况，真到了就给明确错误而不是崩进程 |
| 节点没有磁盘路径时「资源管理器中查看」报错 | `gui/app.go` `OpenInExplorer` | 上游直接 `explorer /e,/select,<包内路径>`，路径不存在、表现为"没反应"。菜单的 claim 已经挡掉了压缩包条目，走不到；真到了给一句说明 |
| 补丁包没有签名时「查看签名」报错 | `gui/app.go` `GetPatchSign` | 上游 `patchSign.getName()` 对 null 解引用（被全局异常处理器吃掉，弹窗空白）。Go 里是 panic、会终止进程 |
| 绑定信息文本由后端重新生成，不让前端回传 | `gui/app.go` `SaveMappedInfoAs` / `CopyMappedInfo` | 生成规则只有一份（`GetNodeMappedText`）。前端已经拿到了文本，但让它回传等于把"内容从哪来"变成两个来源 |
| 双栏预览给差异行加高亮 | `content.ComputeSplitMarks` / `app.css` `.cline.mark-*` `.mk-*` | 原版双栏**不做任何标记**。用户要求"差异处高亮"，属新增能力。行号列 ＋/－/~ 符号 + 行级浅色底 + 词级染色。详见 §一「双栏差异高亮」 |
| 预览面板的「最大化」铺满**应用窗口**，不是整个屏幕 | `app.js` `toggleContentMax` | 原版那个 Dialog 是**独立系统窗口**，最大化后盖住整个桌面。Go 版是应用内的浮动面板、没有独立窗口，"最大化"只能是铺满应用窗口（留 8px 边距）。**按钮本身不是增强** —— 原版靠 JavaFX 给可缩放对话框挂的系统标题栏按钮，这里补了个同款 |
| 保存打补丁产物的默认文件名**不带后缀** | `gui/app.go` `defaultOutputPath` / `app.js` `runPatch` / `cmd/patchassistant` `defaultPatchedPath` | **这一条是"消掉一个曾经的偏差"，不是新增偏差。** 原版 `HomeView.handleSaveAppPack` 用 `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())` —— 初始名就是应用包自己的文件名、初始目录就是应用包所在目录，**不带任何后缀**。移植时自行拼成了 `<主名>-patched.<ext>`，用户要求去掉，现已对齐原版。**代价**：默认值与源文件同名同目录，在保存对话框里直接确定会被系统提示覆盖（原版就是这个行为）；CLI 不带 `-o` 会原地覆盖应用包，用法说明里已标注。由 `TestDefaultOutputPathHasNoSuffix`、`TestDefaultPatchedPathIsAppPackItself`、`runpatch-suggest-name-no-suffix` 三条守着 |
| 树上快速搜索的 ↑/↓ 按语义走 | `app.js` `fastSearchStep` | **修的是上游真 bug**（见 §一「搜索：三层入口」）：原版 `selectPre/selectNext` 用 `getAndUpdate` 的**旧值**取元素，两个方向取到同一个元素。Go 版做成"↑ 前一个 / ↓ 后一个" |
| 跳转行列对非法输入标红、保留输入 | `app.js` `gotoMark` | **修的是上游真 bug**：原版把输入框内容改写成 i18n 的「无效」两个字，用户没法接着改。这里改成红边框 + 保留原文 |
| 搜索关键词的正则编不出来时返回错误 | `gui/search.go` `createKeyword` | 关键词里既有 `*` 又有没配对的 `[` 之类时，原版抛 `PatternSyntaxException` 被后台线程吞掉、界面**悄悄什么都不发生**。这里把错误交回去，前端弹一条提示 —— 静默失败比报错难排查得多。由 `TestSearchInvalidWildcardRegexReturnsError` 守着 |
| 普通名字搜索**多了一层兜底**：0 命中时自动去扫嵌套 jar | `gui/search.go` `SearchNodesDeep` / `searchNestedJars` / `jarPathStack` + `app.js` `searchRun` / `searchDeepRun` / `searchRenderMessage` + `app.css` `.search-msg` | **这是相对原版新增的行为，经用户明确选择**（2026-10-21：用户搜 `EleCode.class` 0 结果，实测文件在 `BOOT-INF/lib/glb-util-3.0.1.jar!/gfmis/glb/annotation/`，原版单关键词搜索只遍历内存已加载节点、不下探未展开的嵌套 jar，所以也搜不到）。**不能简单做成"普通搜直接全探"**：该包 315 个嵌套 jar 全展开实测 **2.85s**，而普通搜是输入即搜（每敲一键一次）。所以：普通搜语义不变；0 命中且关键词不含 `/` `\` 时，前端停手 **500ms** 再调 `SearchNodesDeep`，把未展开的 jar 逐个展开再搜一次，命中**保留展开**（与 `searchWithPathAt` 命中分支同口径，前端双击要 GetState），未命中**整支回滚**。**即时搜索没加防抖**（走查断言 type 后 60ms 即查结果），只防抖这一层兜底。由 `TestSearchDeepFindsFileInsideNestedJar`、`TestSearchDeepRollsBackNestedJarOnMiss`、`TestSearchDeepThenInstantFindsIt`、`TestSearchDeepSkipsPathKeyword` 四条守着 |
| 搜索历史只存在会话内 | `gui/search.go` / `gui/app.go` 的 `App.search` | 原版把历史写进 `Configuration` 并落盘 `application.xml`。Go 版 GUI **没有配置文件**（设置项同样只活在会话内，见 §2.4 附近与 `ApplySettings` 的注释），所以历史也只存内存。`config.SearchConf` / `AddHistory` 已按原版实现好，接上配置文件时直接换来源即可 |
| 搜索对话框用固定尺寸、不落盘 | `app.js` `.dialog-search` | 原版是 720×300 的**可缩放**窗口，尺寸变化实时写回 `application.xml` 的 `searchWidth`/`searchHeight`。同上，Go 版没有配置文件；尺寸与其它对话框（设置 / 绑定信息 / 签名）保持一致地用 CSS 固定 |
| 搜索对话框底部加了一行操作提示 | `app.js` `index.html` `#search-overlay` | 原版没有这行字。"单击历史项回填、双击结果跳转"这套手势在界面上完全看不出来（原版只能靠试），加一行说明。**不影响任何行为** |
| **主题从"只有亮色"扩成三套**：明亮 / 石墨 / 纯黑 | `app.css` 的 `:root[data-theme="graphite"]` / `:root[data-theme="oled"]` + `app.js` 主题模块（`THEME_KEY` / `THEMES` / `currentTheme` / `applyTheme` / `initTheme`）+ `index.html` 的 `<head>` 引导脚本与「设置 → 常规配置 → 主题」下拉 + `gui/app.go` `SetWindowBackground` + `gui/main.go` `BackgroundColour` | 原版 `ThemeEnum` 只有 `LIGHT`/`DARK`，其 `theme-dark.css` 仅 22 行、只调 JavaFX 内置控件的 `-fx-base` 阶梯，**动不了**应用自己 `theme.css` 里写死的白底代码区 —— 那套暗色在 HTML/CSS 侧没有可搬的价值，但**配置结构**（`light`/`dark`、明亮/暗夜主题名）对齐了。Go 版原本只有一个主题，所以「常规配置」这组被砍掉；有了第二种可选值后该组与主题下拉一并补回（**语言仍不放**：界面文案中文写死，一个选项的框只会让人以为坏了）。用户 2026-10-21 看过三套预览（`.workbuddy-ai/mockups/make_theme_preview.py`）后先选纯黑，2026-10-22 要求把石墨也加上，最终三档：`light` / `graphite`（中性深灰）/ `oled`（纯黑，对比最烈、OLED 屏不发光）。**实现方式是"换令牌"而不是"再叠一张表"**：两套暗色与亮色共用全部规则，差别只在 `:root[data-theme=…]` 里的变量值。为此把历史上**写死在 `:root` 之外**的 6 类颜色提成了令牌（亮色取值与写死时一字不差，所以亮色渲染不变）：`.code`/行号栏的白底、`.btn` 的白色渐变高光与四层投影、`.dropfile-state-*` 三枚药丸、`.links` 的圆点与淡档、`var(--warn, …)`（`--warn` 从未定义过，等于写死）、以及 `.field select`。**语法高亮必须整套换**：原版那套照抄 JavaFX `*-style.css` 的命名色（`purple`/`blue`/`darkgreen`/`firebrick`/`teal`）是给白底调的，深底上不可读；暗色两套各配一套（纯黑用高饱和一档、石墨用 One-Dark 经典一档），`yaml-style.css` 那套本就是 VS Code Dark+ 取值，原样保留。**主题不经后端**：Go 版 GUI 没有配置文件，主题放 `localStorage`（`patchassistant.theme`），`index.html` 的 `<head>` 里内联一段引导脚本在样式表**之前**设好 `data-theme`，否则暗色用户每次启动先白屏一闪；窗口底色另由 `SetWindowBackground` 把 CSS 的 `--bg` 同步过去（WebView 只覆盖客户区，缩放边缘/圆角那一圈露的是窗口底色），启动那一瞬仍用 `main.go` 的亮色值（前端还没跑起来）。**亮色零回归的两种证明**：① 文本级 —— 把两个暗色块从新表剥掉后与改动前逐行 diff，每个被删的写死值都能在同名令牌里找到一字不差的值；② 计算样式级 —— `.workbuddy-ai/mockups/verify_themes.py` 用改动前的备份（`mockups/pre-theme-backup/`，刻意放在 `dist/` 外，否则会被 `wails build` 嵌进 exe）渲染同一页面，63 个选择器 × 按钮两态逐一比对计算样式，**唯一差异是新增的 `.field select`**（新元素，旧版没有）。走查套件 310/314 与基线完全一致（同样 4 条既有失败） |
| 「拖入的文件」条目显示 MD5 | `gui/app.go` `fileMd5` + `DropMatch.SrcMd5` / `DropResult.SrcMd5` + `app.js` `setDropFileMeta` / `renderDropFile` + `app.css` `.dropfile-md5` + `internal/util/hash.go` `Md5Stream` | 原版 `FileTreeCell` 给树上**每个**文件行都渲染 `日期 + 大小 + md5`（`FileDirectNode` 的注释就是"文件节点，直接由系统拖入"），拖进来的文件同样算 md5。Go 版树上那三列一开始就照做了，但右侧「拖入的文件」那条记录只有名字和体积 —— 用户 2026-10-22 报障："拖动了单个文件之后，右侧没有显示文件的md5值"。**为什么不能复用树上那套**：包内节点的 md5 走 `GetMd5OrInit()` 缓存，缓存的是**包内**那份字节；拖进来的文件此刻只是磁盘上一个路径，两边不是同一份内容，必须单独算。所以新增 `util.Md5Stream`（流式，不把整份读进内存 —— 拖个几百 MB 的包进来不能先 `ReadAll`）与 `gui/app.go` 的 `fileMd5`（拿不到就空串，混进个读不了的文件不能让整条链路报错），在 `HandleDrop` 的四条返回路径（替换 / 反查 / 新增 / 打开）和 `matchLooseFilesIntoApp` 的逐文件 `Items` 里都填上。**前端按路径对齐而不是按顺序对齐**：后端 `Items` 的顺序虽与拖入顺序一致，但按 `srcPath` 匹配更稳，将来乱序也不会错位。由 `TestFileMd5`、`TestDropLooseFilesReportSrcMd5`、`TestDropRowAndDirReportSrcMd5` 三条 Go 单测与 `dropfile-shows-md5` / `dropfile-md5-keeps-size` / `dropfile-clear-then-redrop` 三条走查守着 |
| 「单包视图」↔「双栏视图」切换，按钮文案跟着状态走 | `index.html` `#btn-single-view` + `app.js` `syncSingleViewBtn` + `app.css` `body.single-app` 那几条与 `.btn.active` | **原版没有这个按钮**（Java 侧无对应物，纯 Go 版新增）。作用是隐藏补丁面板 / 左右分隔条 / 上下分隔条 / 补丁信息，只留应用包这一侧占满整窗，给"把单个文件拖到具体应用文件上替换"一个更宽的落区；**有待替换结果时「补丁信息」仍然露出来**（`body.single-app.has-pending`）—— 单包视图正是"拖单文件进来替换"的主场，而待替换清单就住在那个面板里，一起藏掉的话拖完就什么都看不见了。切换只给 body 加 `.single-app`，布局知识全在 `app.css` 那几条规则里。**2026-10-22 修了两处**：① 按钮文案原本写死成「单包视图」，切过去之后还写着「单包视图」，用户报障"切换了但看不出来" —— 现在文案写的是"点一下会切到哪个视图"（双栏时「单包视图」、单包时「双栏视图」），`title` 也跟着翻；② app.js 早就在给按钮加 `.active`，但 `app.css` 里**从来没有 `.btn.active` 规则**，按钮在单包视图下和静止态长得一模一样，等于唯一的状态提示是假的 —— 补上规则（选中底色 `--row-sel` + 文字与描边 `--accent-press`，保留渐变高光以免从"陷进去"变成"贴平"）。由 `singleview-click-flips-label` / `singleview-active-has-style` / `singleview-title-follows` / `singleview-click-back-restores` 四条走查与 `.workbuddy-ai/mockups/verify_single_view_btn.py` 守着 |
| **行号列拆成独立一列**（性能修复） | `app.css` `.codepane` / `.code-gutter` / `.code` / `.gline` + `app.js` `linesHtml` / `makePane` / `syncGutter` | 用户 2026-10-23 报障："打开对比窗口时，上下滑动卡顿"。**根因量化过**：一行原本是 `.cline > .ln`，`.ln` 靠 `position: sticky; left: 0` 在横滚时钉住；而 sticky 的包含块正是滚动容器，于是**一万行就是一万个 sticky 元素**，每一帧滚动浏览器都要在主线程上把它们全部重排一遍。用 CDP 在真实时间里量（`.workbuddy-ai/mockups/probe_scroll_frames.js`，不靠 `--virtual-time-budget` —— 那个模式下 rAF 根本不产帧）：一万行平均一帧 **39.0ms、74% 的帧超时（≈24fps）**，而 JS 每帧只花 0.6ms，证明是渲染问题不是逻辑问题。八组 CSS 假设逐个排除后确认 `position: sticky` 是唯一显著因素（去掉它 39.0 → 18.8ms；`overflow: visible` 的对照组量出 16.7ms 是垂直同步的底线）。改法：一行拆成"不横滚的 `.code-gutter`（住 `.ln`）+ 横滚的 `.code`（住 `.lt`）"，一个 sticky 都不需要，纵向靠 `syncGutter` 把 `gutter.scrollTop = code.scrollTop`（赋成相同值不会再触发 `scroll`，不会回环）。**中途试过并否掉的**：`content-visibility: auto` 反而更慢（93.9ms，每帧都要给新露出来的行重新布局）、`contain: paint` 与 `overflow: clip` 无改善、把 syncGutter 整个去掉也无改善（说明瓶颈不在那次同步）。最终一万行 **16.8ms / p50 16.7ms / 掉帧 1%（219 帧里 2 帧）**，等于垂直同步的底线；3000 行与 5000 行都是 16.7ms、零掉帧。⚠️ **这个数会被机器负载带偏**：同一份代码在连续跑了一串 Chrome 之后量到过 20.8ms / 23%，隔一会儿再跑又回到 16.8ms / 1%（连跑三次都是）。要复现请先让机器静下来，别拿热机读数当回归基线。⚠️ 拆列**必须**给 `.code-gutter .ln` 补 `font-family: var(--mono)`：`.ln` 从 `.code` 的**子节点**变成了它的**兄弟**，继承链断了会一路落到 body 的 Segoe UI 上，而那是比例字体，后端"补空格到等宽"的行号 padding 全部失真（走查 `split-glyph-gutter-aligned` 当场红了：行号列宽出 43.86/37.81/39.63/41.5 四种） |
| 双栏差异配色换成 **IDEA 那套语义**：新增绿 / 删除**灰** / 更新**浅蓝** | `app.css` `:root` 与两块暗色主题里的 `--add` / `--mod` / `--del` 及 `-bg` / `--del-line`，新增的 `--danger` / `--danger-bg` / `--danger-line` / `--danger-hover` / `--danger-press` | 用户 2026-10-23："新增是绿色、删除是灰色、更新是浅蓝色。样式改成和 idea 一样的"。原来 `--del` 是红 `#cf222e`、`--mod` 是琥珀 `#9a6700`（亮色）/ `#d9a52c`（暗色）。改成 `--del` 中性灰 `#6e7781` / `--mod` 青蓝 `#1f6fb2`（暗色 `#98a2ae` / `#5aa9e6`），底色 `--del-bg #e3e5e8` / `--mod-bg #d9ecfa`；`--add` 那档用户没让动，保持原值。**关键的一处结构性调整**：`--del` 原先**兼着两个语义** —— 既是"这行被删了"，又是"点了不可逆"（`.btn-danger` 的底色、`.temppatch` 待删除面板、`.field input.invalid`）。灰掉之后删除按钮会变成灰按钮，警示就没了，所以把危险语义拆成独立的 `--danger` 一族（并顺手把 `.btn-danger:hover` 里硬编码的 `#b81f2b` 收成变量 —— 原来暗色主题下底色是 `#ff6b62`，一 hover 反而跳成深红）。`--warn` 也随之与 `--mod` 脱钩（警告必须是琥珀，更新是青蓝）。分界线写在 `:root` 的注释里：**描述状态用 `--del`，描述"点了会出事"用 `--danger`**。由 `.workbuddy-ai/mockups/verify_diff_palette.py` 三套主题 36 条断言（走查只跑默认亮色，暗色那组值以前没人量过）+ 走查 `split-line-bg-*` / `split-glyph-color` / `split-mark-bar-wider` / `split-inline-changed-word-color` 守着 |
| 差异导航排成**一颗胶囊** | `app.css` `.contentwin-nav` / `.contentwin-nav .btn-icon` / `.contentwin-nav-count` | 用户 2026-10-23："差异文件对比时，增加一个上下跳转按钮，只针对差异处内容……样式改成和 idea 一样的"。**按钮本身原版就有**（`#content-nav` / `-prev` / `-next` / `-count`，由后端 `content.SplitBlock` 驱动），这轮只改外观：IDEA 的差异导航是一条连在一起的控件（它那边竖着贴在编辑区右上角，这里标题栏是横向的，所以横向排），所以给外框加边框 + 全圆角 + 底色，里面两个按钮**去掉各自的边框和圆角**、靠一条 1px 分隔线分区；到头时置灰而不隐藏（位置稳定，用户不用在"能点/不能点"之间重新找按钮）。顺带补上一处**覆盖盲区**：走查以前只在「对比」弹窗那条链上验过导航，而 mock 的 `Foo.js` 预览数据**漏了 `blocks`** —— 真实后端 `renderContentView` 走 `content.RenderMarked` 时是带 `Blocks` 的，于是"树里双击进来的预览没有导航"这类 bug 会一路绿着溜过去。现在 mock 补上了 `blocks`（值由 Go 的 `content.ComputeSplitMarks` 实测得出，不是猜的），并加了 8 条走查断言，其中 `split-nav-absent-side-not-marked` 专门钉 `rightStart = -1` 的那一侧不许挂高亮 |
| 「当前差异块」用**一条连续的竖线**标，不再逐行画框 | `app.css` `.cline.nav-current` / `.gline.nav-current > .ln` / `.gline.mark-{add,del,mod}.nav-current > .ln` / `--nav-wash`（三套主题各一份） | 用户 2026-10-23 发了张 20 行差异块的截图："为啥每一行要这么框出来，太影响美观了"。旧实现是 `.nav-current { outline: 2px solid var(--accent); outline-offset: -2px }` —— 一个块有多少行就叠多少个框，看着像梯子，而且每行都被上下两条横线切断，正文读起来断断续续。改成 **IDEA 那种块级标记**：行号列**最右边**一条 3px 强调色竖线（`linear-gradient(to left, var(--accent) 0 3px, transparent 3px)`）贯穿整块 + 一层极淡的 `--nav-wash` 洗色，整块连成一片、中间不被任何横线打断。两条通道分工：最左 4px 色条仍是 `mark-*` 的**语义色**（增/删/改），最右 3px 是 `--accent` 的**位置色**（当前跳到哪）；放右边是因为左边已被语义色占着，再画会盖掉它。底色用 `background-image` 叠层而**不写 `background-color`** —— 差异行本身有 `mark-*` 底色，写 color 会整条替换掉、语义色就没了；有 `mark-*` 的行再把它的 4px 色条带到第三层（特异性 4 类 > 3 类，不用 `!important`）。⚠️ **这一改顺带挖出两个更严重的隐性 bug**：`.ln` 拆列后变成了 inline 盒，① 只有文字那么高 → 竖线渲染成 20 段虚线；② 丢了 `line-height` 继承 → 一路拿到 body 的 1.5 = **21px**，而正文 `.cline` 是 17px，**每行错开 4px**，40 行时行号列 `scrollHeight` 1260、正文 1020，**行号会指向错误的代码行**。修法是给 `.code-gutter .ln` 写死 `display: block` + `line-height: 17px`（`font-family` 那处见上一行的拆列条目，同一个继承链断裂的坑）。**为什么之前一直没发现**：mock 里的差异块最大只有 2 行，2 行错 4px 看不出来；为此新增 `.workbuddy-ai/mockups/shot_nav_block.py`，把 `OpenContent` 包一层返回"40 行文件、第 10~29 行全 mod"的载荷，再走真实双击 + 点「下一个差异」，20 行块才把问题顶出来。验证不再靠肉眼：`.workbuddy-ai/mockups/_measure_gutter.py` 直接量几何 —— 三套主题全部 `heights=[17]`、相邻行**间隙 maxGap=0**（所以竖线是一条连续的线而不是虚线）、`gutterScrollH == codeScrollH == 680`（不再漂移）、两栏各**正好 20 行** `nav-current`（整块标记，不是一行）、`.ln` 的 `background-image` 里确有 accent 的 `3px` 渐变 |
| **>64KB 的文件仍然高亮**（原版直接放弃） | `internal/content/highlight.go` `maxHighlightBytes` / `Tokenize` / `tokenizeRange` + `internal/content/highlight_golden_test.go` 的偏差分支 | 用户 2026-10-26："两个对比的，代码没有高亮"（截图是 `AccountSpecialService.class` 的双栏对比，两栏一片黑字）。**根因不是配色也不是 lang**：原版 `CodeAreaFactory.computeHighlighting` 里有 `if (text.getBytes().length > 64 * 1024) return null;`，超 64KB 就**整篇不高亮**；而 `.class` 反编译出来的 Spring service 动辄上千行 —— 实测 1200 行就是 **76,890 字节**，正好越线。所以症状是"这个文件没有高亮"，而不是"高亮功能坏了"。**排查过程值得记一笔**：先用 `go run` 打印 `RenderMarked` 的片段，确认后端确实吐了 `class:"keyword"`（lang 也是 `java`）；再用真 Chrome 渲染带 token 类的双栏载荷，量到 `.keyword` 的 computed color 是 `rgb(128,0,128)`（CSS/DOM 都没问题）；最后用 1200/2000/5000 行梯度一跑，`Tokenize` 从 1000 行（63,890 字节）的 11001 个区间**直接掉到 0**，这才锁定 64KB 闸门。**改法**：把 64KB 从"整段上限"降级成"**切块大小**" —— 超过就按**行边界**切块、逐块分词、把偏移加上块起点再拼起来，单次正则调用的规模仍被钉在 64KB 以内（避免大输入上正则退化）。切点退到最后一个换行**之后**，不会把一行或一个记号劈开；只有单行本身超 64KB（压缩过的 js/css）才硬切。**代价**：`/* ... */` 恰好跨块边界时认不出来（上下两半各自不匹配完整注释），块边界在换行处、注释块通常很短，实际罕见。**性能**（一次性、在 Wails 调用里算，不占 UI 线程）：1200 行 13ms / 5000 行 62ms / 10000 行 135ms / 20000 行 246ms，线性，且各语言关键字计数与切块前**逐行一致**（无漏词）。**黄金基准怎么处理**：`hl_ref.tsv` 是 Java 原版的真实输出，里面有一条 >64KB 的用例期望 `NULL`，现在会变成 `OK` —— 这不是移植错误，是刻意的增强，所以在 `highlight_golden_test.go` 里单列一个 `deviate` 分支（限定 `want==NULL && len(text)>maxHighlightBytes && got==OK`），不计入通过率，日志里单独报"另有 2 条刻意偏差"。改完 **202/202 通过 + 2 条偏差**。新增 `TestHighlightLargeFileKeepsKeywords` 钉住"大文件不只是非 nil，还得真的把 keyword/string/comment 认全"（1200 行 → 3600/1200/1200，且区间必须**连续覆盖全文**、不留空洞）。端到端另用 `.workbuddy-ai/mockups/_measure_highlight_big.py`：拿 Go 真实生成的 79,394 字节双栏载荷喂给前端，量到 3604 个 keyword 全为 `rgb(128,0,128)`、1200 个 string 为 `rgb(0,0,255)`、2400 个 comment 为 `rgb(95,158,160)` |
| 快速滚动时**行号列与正文列不再脱节** | `app.js` `syncScroll` / 新增 `fitGutterTail` / `refitGutters` | 用户 2026-10-26："按住上下快速滑动时，会短暂出现这个情况"（截图里右栏只剩一串行号、正文一片空白，行号与代码对不上）。**根因量出来了**：行号列（`.code-gutter`）与正文列（`.code`）是两个独立滚动容器，纵向靠 `scroll` 事件里的 `gutter.scrollTop = code.scrollTop` 同步；而**两栏之间**的同步又是"设对方的 `scrollTop`"，这会触发对方**下一帧**才派发的 `scroll` 事件，于是被镜像那一栏的行号列要等两跳才动 —— 每帧都慢一拍。用 CDP 在真实时间里量（`.workbuddy-ai/mockups/probe_sync_desync.js`，每帧滚 60px，180 帧）：两栏**正文**始终同步（`max-pane-diff = 0`），但被镜像那栏的**行号列每帧落后 120px**（≈7 行，`f1: code1=60 gut1=0`）—— 快速滑动时行号指的就是别的行。**改法**：在 `syncScroll` 里设完 `dst.scrollTop` 之后**当场**把它的行号列也推过去（`pullGutter(dst, gdst)`），不等它自己的事件。不回环：对方的事件下一帧到达时，`syncGutter` 看到两值已相等直接跳过，反向赋值也是同值、不再派发。改完 60/200/400/800px 四档步长、左右两栏分别驱动，`max-gutter-lag` 与 `max-pane-diff` **全部归零**。**顺带修掉一个只在文件末尾才现形的错位**：`.code` 是 `overflow:auto`，内容超宽时底部有 15px 横向滚动条，行号列是 `overflow:hidden` 没有它，于是正文能多滚 15px、行号列滚不到 —— 滚到底时行号比代码低一行。新增 `fitGutterTail` 把 `offsetHeight - clientHeight`（滚动条厚度，覆盖式滚动条为 0）补成行号列的 `padding-bottom`（加在内容之后，不挪动任何一行），渲染后推一帧量一次，窗口缩放时由已有的 `ResizeObserver` 重量（`refitGutters`）。**性能未回退**：`probe_scroll_frames.js 3000` 仍是 avg 16.7ms / p95 16.8ms / 掉帧 0%，每帧 JS 0.063ms |
| 包对比判"相同"改用**中央目录的大小 + CRC32**，只有判定不同的条目才解压算 md5 | `internal/service/jarcompare.go` 新增 `packMeta` / `entryDigest` / `packSource` / `compareSources` / `scanZipMeta` / `digestZipEntries` / `diskSource` / `bytesSource`，删掉 `compareEntryMaps` / `readEntriesFromZip` / `readPackEntries{,Bytes}`；测试见 `jarcompare_test.go` 的 `TestComparePacks_SameEntriesAreNotRead` / `TestComparePacks_SameSizeDifferentContent` | 用户 2026-10-26："对比窗口一出来，会有点卡顿"。**根因量出来了**：原实现 `readEntriesFromZip` 对**两个包的每个条目**都 `zf.Open()` + `io.Copy` 进 md5，143 MB 的胖包（651 条目）实测**中央目录 1ms / 全量 MD5 222ms**，两个包就是 444ms，端到端 `ComparePacks = 498ms`；而这 444ms 里 99% 是白干的 —— 那 651 个条目两侧**完全一致**，md5 一个都不会被显示。**改法**：zip 的中央目录里本来就有每个条目的大小和 CRC32（`zf.UncompressedSize64` / `zf.CRC32`），读它不解压任何字节，而"两个条目内容是否相同"这个问题它已经足够回答（大小不同必然不同；大小同、CRC 不同必然不同）。于是拆成两段：`scanZipMeta` 只扫中央目录拿到 `packMeta`，用它分桶判同异；`digestZipEntries` **只对"内容不同 / 仅 A 有 / 仅 B 有"这些要展示的条目**解压算 md5。`packSource` 把"元信息"和"按需摘要"包成同一个接口，磁盘包（`diskSource`）与嵌套 jar 的内存字节（`bytesSource`）各实现一份，比对规则仍只有 `compareSources` 一处。**实测**：两个相同的 143 MB 包 `ComparePacks` 498ms → **3ms**（其余两次 0ms / 10ms，堆 2 MB）；最坏情形（651 条全"仅 A 有"，必须全解压）470ms → 249ms，因为只读需要展示的那一侧。**口径变化要记住**：判"相同"的强度从 md5 降到 size+CRC32（同大小前提下 1/2^32 碰撞概率）。这个结论只用于**对比对话框的展示**，不参与补丁匹配（补丁语义走 match 那一套，与本文件无关），要逐字节确证点开那一行看内容即可 —— 所以是可接受的取舍。`Same` 条目不再有 md5（字段留空），`Size` 对"读过内容"的条目仍用**实际读到的字节数**、对未读的退回中央目录的声明值（后者只出现在不展示的"相同"条目上，界面与报告文本都不引用）。`TestComparePacks_SameEntriesAreNotRead` 用"数据被改过、中央目录 CRC 还是原内容"的 STORED 条目钉住"相同条目不读"（真读了会撞 `ErrChecksum`），并用对照组证明该断言不是空过 |
| **zip 条目名按 GBK 解码**（修乱码），重打包仍写回**原始字节** | `internal/util/charset.go` 新增 `DecodeZipEntryName` + `internal/tree/node.go` 新增 `RawPath` + `internal/pack/zip.go` + `internal/service/writer.go` 新增 `entryNameOf` / `patchsave.go` / `jarcompare.go` 新增 `zipEntryName` | 用户 2026-10-28 报障："打开这种时，文件名会出现乱码"。真因：那条 `服务配置说明.docx` 的条目名是 GBK 字节（`b7fe cef1 c5e4 d6c3 cbb5 c3f7`）且没设 0x800 标志位，而 Go 的 `archive/zip` 在缺标志位时**原样返回字节**，非法 UTF-8 经 JSON 到前端就成了 `?`。原版 Java 用 `new ZipInputStream(in, GBK)` 无条件按 GBK 解，所以那边没这个问题 —— 移植时漏了这一层。改法：`Path`/`Name` 存解码后的 UTF-8（显示与匹配用），原始字节存 `Node.RawPath`，写回时用 `RawPath` + `FileHeader.NonUTF8`（不设会让标准库给 GBK 字节打上 UTF-8 标志，比乱码更难查），输出包字节级不变 | `TestDecodeZipEntryNameGBK` / `KeepsUTF8`；`TestBuildZipTreeDecodesGBKEntryName` / `GBKDirName` / `ASCIIKeepsRawPathEmpty`；`TestNewEntryHeaderKeepsGBKNameBytes`（真写一遍再读回，验名字字节与 0x800 标志）、`TestNewEntryHeaderNewNodeWritesUTF8`、`TestEntryNameOfDirGetsTrailingSlash`；真实补丁包实测 `服务配置说明.docx` 正常显示、`raw=b7fecef1…` 保留 |
| 应用树上**拖 .jar / .zip 到某一行 = 当包内文件处理**（加进/替换那一行），只有落在空白处才"打开整包" | `gui/app.go` `HandleDrop`（把"落在行上"提到 `isPackExt` 之前，并限定 `side == treeSideApp`）+ 新增 `addDropFilesInto` / `parentLoc`；测试 `TestHandleDropJarOntoDirRowAddsFile` / `TestHandleDropJarOntoAppPaneOpens` / `TestHandleDropJarOntoPatchRowStillOpens` / `TestParentLoc`，原 `TestHandleDropRoutesToFileRow` 改断言 | 用户 2026-10-26："左侧 jar 包不支持其他 jar 包直接拖入，应该和文件一样"。**先确认不是没接线**：Wails 的 `OnFileDrop` 用 `useDropTarget=true`，靠 `elementFromPoint` 找落点再查 `--wails-drop-target`（`.pane` 上声明、子元素继承），探针在四个典型落点（面板正中 / 第一行 / 左下空白 / 标题栏）都拿到了 `HandleDrop:app:…`；后端 `isPackExt(src) → HandleFileDrop → OpenAppPack` 也通。**真正的分歧在分流顺序**：原版是**两层**处理器 —— 落在单元格上归 `FileTreeCell.onDragDropped`（`TreeNodeFileMatchProcessor`，`source == null` 时**任何** OS 文件都收，jar 与 `.class` 一视同仁，按 `matchPatchName` 在目标节点/父目录里找同名项，命中就 `mappedNode(MOD)`、没有就 `createAndGetAppNode(ADD)`），只有落在空白处才冒泡到 `HomeLeftTreeView.onDragDropped`（`refreshAppTree(files.get(0))`，打开整包）。Go 版早期把 `isPackExt` 放在最前面，于是"拖 jar 到 `BOOT-INF/lib` 上"会把**整个应用包换掉** —— 用户的感受就是"不支持把 jar 拖进来"。**改法**：把行优先分支提到整包判断之前，且**只对应用侧生效** —— 原版补丁树的单元格根本不接拖放（`FileTreeCell.onDragOver` 里 `getTreeView() != appTree` 就 return），整包落上去仍要走 `HomeRightTreeView` 的"打开补丁包"，一刀切会让补丁树开始接包内文件。目录也不走这条（原版对单个目录是 `matchWithDir` 把子项并进去，Go 版没实现，保持原样）。**顺带修掉两个边界**：① 文件行上拖多个时目标退回**父目录**（原版 `appNode.isDir() ? appNode : appNode.getParent()`，Go 版原来会把子项挂到一个文件节点下）—— 新增 `parentLoc`，要认 `jar!/` 前缀；② 抽出 `addDropFilesInto`，把"逐个加进去"的逻辑收一处。**界面**：`index.html` 两个 `.pane-head` 加 `title` 说明分工（落在头部 = 打开整包，落在行上 = 加进那一行），`app.js` 的落点语义注释与 `trackDropRow` 注释同步更新 —— 以前行会亮但松手是换掉整个包，提示是**错的**，现在对了。**另一个必须兜的边界**：前端把"根行"（那一行显示的就是包文件名）也当普通行传过来，而根节点的 `Path` 是**磁盘路径**（`D:\...\app.jar`）不是包内路径 —— 照普通行走会 `resolveNode` 失败、报"树里找不到节点"。新增 `App.isRootRow` 把它归到"空白处"：拖整包到根行 = 换这个包，拖普通文件 = 加到根目录（后者顺手修掉一个既有的报错）。`TestHandleDropOnRootRow` 钉住这两条 |
| 拖到**文件行**上 = 到它所在目录里**按名字**匹配（命中覆盖 / 没命中新增），**不是**就地替换那一行 | `gui/app.go` `HandleDrop`（文件行退回 `parentLoc`）+ 新增 `addOutcome` / 改 `addOrReplaceChild` 用 `service.MatchPatchName` | 原版 `TreeNodeMatchProcessor.exec` 一律先算 `tarAppNode = appNode.isDir() ? appNode : appNode.getParent()`，再在该目录里逐个 `matchPatchName`。Go 版早期在文件行上直接 `ReplaceNodeContent(rowPath)`，于是把 `B.jar` 拖到 `A.jar` 行上会**把 A 的内容换成 B 的、名字还留着 A** —— 静默改错文件。顺带把"同名"的判定从 `c.Name == name` 换成 `service.MatchPatchName`（`.jar` 前缀匹配、其余全等），与原版 `isMatch` 一致 | `TestHandleDropRoutesToFileRow`（名字不同 → 新增且原节点不动；名字相同 → 覆盖；拖 jar → 仍是包内文件）、`TestHandleDropJarOntoExistingLibJarReplaces`（`BOOT-INF/lib` 下已有同名 jar：落在 jar 行 / 落在 lib 目录行都**覆盖**、条目数不变） |
| **同名 + 内容 md5 相同 → 不替换**（原版照换不误） | `gui/app.go` 新增 `dropActionSame` / `md5Memo` / `sameContent`，`addOrReplaceChild` 加 md5 判断，`MatchCandidate` 加 `Md5`；`app.js` 的 `onFileDrop` / `pendingRow` / `pendingApply` 各加一条 same 分支 | 用户 2026-10-28："从外面拉入文件到对应位置时，如果存在同名的文件，则迅速做出 md5 比较，如果 md5 相同的话警示一下"。**先算成本**：拖入文件的 md5 本来每次拖放都在算（`DropResult.SrcMd5` 三处都填），目标节点的 md5 也是现成的（`toView` 里 `Md5: n.GetMd5OrInit()`，显示在树上的行都有缓存），所以增量只是一次字符串比较 —— 这也是判定"值得做"的前提。**做成"跳过"而不是"警示后仍替换"**：替换成同一份内容在语义上是空操作，但 `ReplaceNodeContent` 会无条件 `SetEdited(true)` 并清空 `Md5`，界面上打出「已修改」星号、用户以为改了，实际字节一个没变、保存出来也看不出差别 —— 假改动比"少一个提示"更难受。**三处入口都覆盖**：① 直接拖到目录行/空白处（`addOrReplaceChild`，一次拖多个时只有**全部**没改才报 same，否则会说"已加入 N 个文件"却什么都没加）；② 「补丁信息」清单的候选行（`MatchCandidate.Md5` 随候选下发，用户**点之前**就能看到「内容相同」标签）；③ 点「替换到选中项」时再拦一道（防止清单没刷新时误替换）。任一侧拿不到 md5 就照常替换 —— 宁可多换一次，也不能因为"算不出来"把用户手里的文件丢掉。**顺带**：`md5Memo` 让一次拖放里同一个文件只读一遍（胖包上百 MB，算两遍就是两倍 IO） | `TestHandleDropSameContentSkips`（内容相同 → `same` 且不打 Edited、md5 缓存保留；内容不同 → `replace`；名字不同 → `add`；一次拖多个全同 → `same`）、`TestFindAppCandidatesCarryMd5`（候选带着 md5 下发，前端才能自己比） |
| 树里同级节点按**目录在前 + 名字升序（忽略大小写）**显示 | `gui/app.go` `toView` 排序子级副本 + 新增 `lessForDisplay` | 用户 2026-10-28 报障："文件现在排序是乱的，能不能按照字母顺序排列"。乱的是**显示**：界面直接用了 zip 条目的**物理顺序**，而它由打包工具决定，`BOOT-INF/lib` 下就是一堆乱序。**原版也乱** —— Java 的 `SortLinkedList.add` 里有序插入那段被注释掉了（只剩 `super.add`），所以这是原版行为、不是移植漏的。修法只动**视图**：`toView` 复制一份子级再排，不碰 `n.Children` —— 树的顺序决定重打包时条目的写入顺序，动了会让输出包与原版分叉 | `TestTreeViewChildrenSortedForDisplay`（同一个包两头断言：视图 = `dir, Aa.jar, alpha.jar, beta.jar, Middle.jar, zeta.jar`；内部树仍是 zip 物理顺序 `zeta, alpha, Middle, beta, dir, Aa`）、`TestLessForDisplayTieBreaks`（目录优先、忽略大小写后同名用原名兜底）；真实 228 条 `BOOT-INF/lib` 实测从 `accessors-smart` 起严格升序 |
| **补丁树 → 应用树**的行拖放，且"已映射的节点"可以改指到别处 | `gui/frontend/dist/app.js` 新增 `parentRowOf` / `locOf` / `paneSideAt` / `initTreeDrag` / `DRAG_ARM_MS` + `makeRow` 里给补丁行挂 `draggable`；`gui/app.go` 新增 `DropPatchNode`；`internal/service/patch.go` 新增 `ApplyPatchNodesToApp` / `matchPatchChildrenInto` / `matchOnePatchNode` / `createAppNodeFromPatch` | 用户 2026-10-28 报障："在右侧打开这样文件后，右边的文件不能拖动放在右侧"。**先确认不是坏了**：前端两棵树**都**没有拖放源（`makeRow` 从不设 `draggable`，只有右侧「拖入的文件」条那几行能拖），后端也没有任何"树→树"的映射入口 —— 这条能力压根没移植，不是回归。方向按原版定：`FileTreeCell.onDragOver` 里 `dragInfo.getOriginTree() == getTreeView()` 与 `getTreeView() != appTree` 都直接 return，所以**只有**补丁树 → 应用树一条路，同树内拖动和"应用树 → 补丁树"都被上游明确禁止。规则照 `TreeNodeTreeMatchProcessor`：落点只决定目标目录（目录行是它自己、文件行退回父目录），文件身份仍由 `MatchPatchName` 按名字定（命中 → MOD，没命中 → 造 MappedNode/VirtualNode 按 ADD 绑定），单个目录落到目录行则把子项逐个并进去。**四处必须自己拿主意的地方**：① 原版 `isHasMappedNode()` 为真就**拒绝整次拖放**，而打开补丁包时自动匹配几乎把所有节点都映射过了 —— 照搬等于这条拖拽平时完全用不上，所以改成**先解绑旧位置再落到新位置**（统计不会重复计数）；② 落点可以是**包根行** —— 根节点是文件节点且 `Parent` 为 `nil`，早期 `tar = tar.Parent` 会把整次拖放静默丢掉，现在把根节点本身当容器，且新节点的 `Path` 不能拿根节点的**磁盘路径**去拼（`inheritRawPath` 同步加了例外）；③ **拖的来源也可以是根行**，但根行代表的是**那个 .jar/.zip 文件本身**，不是"容器" —— 拖到 `BOOT-INF/lib` 上是"把这个 jar 放进去"（有同名就换、没有就加），**不是**把包拆开铺进去（中途一度按"整包套用"实现，用户当场报"他把 jar 包分解放里面了"）；整包匹配上之后也**不再合并子项**（包里的东西已经整份在新 jar 里，再逐条 ADD 只会把统计算多、看着更像"被拆开"）；④ **要按住约 300ms 才起拖**（用户："太容易误触，改成长按才拖"）—— 不能靠"长按到了再把 `draggable` 设成 true"，Chrome 只在 `mousedown` 那一刻判定可拖（Blink 的 `mouse_down_may_start_drag_`），中途改对本次手势无效；正确做法是始终可拖、在 `dragstart` 里检查按够了没有，没够就 `preventDefault`（`dragstart` 只在真移动时派发，所以"按住不动 → 够时长 → 再移动"就是长按起拖）。通道用 HTML5 而不是 Wails 原生通道：原生那条开头就判 `types.includes("Files")`，内部拖动没有 `Files`、根本不触发 | `TestDropPatchNodeIntoAppTree`（目录行命中 → `mod` 且带 `mapped` 标记 / 没命中 → `add` / 包根行 → 加到包内顶层 / **拖整包根行 → 按文件名加进去、不拆开** / 说明文件被拒）；`TestApplyPatchFileMatchesExisting` / `CreatesWhenMissing` / `DirMergesChildren` / `OntoFileRowUsesParent` / `TwiceDoesNotDoubleCount` / `RemapsFromOldPlace` / `OntoRootAddsAtRoot` / `RootActsAsFile` / `NilTargetIsNoop` |
| **整个 jar 被换掉时写出补丁侧的内容**（原版只看 children，换了等于没换） | `internal/service/writer.go` `writeTreeNode2PackWithJar` | **修的是上游真 bug**。原版 `AppPackWriteProcessor.writeTreeNode2PackWithJar` 只看 `children`：为空就写自己的字节，有就重打包子级 —— **完全不看 `MappedNode`**。于是"把一个 jar 整个换进 `BOOT-INF/lib`"写出来还是旧 jar，README 里对 jar 写 `!路径` 也一样静默无效。判据必须是**节点自己有没有绑定**（绑了 = 整体替换），不能靠 children：树上的 jar 只要展开过就有 children（那是显示用的），这时重打包会把旧内容拼回去。由 `TestSaveAppPackWritesReplacedWholeJar` 钉住（保存后读回该条目，必须是补丁 jar 的字节、且是能解析的 zip） |
| `CreatePatchDiffInfo` 在任一侧 md5 为空时**不比** | `internal/service/apppack.go` | 目录的 `GetMd5OrInit` 恒为空（`ConsumeBytes` 对目录直接返回），两个空串相等会得出一条"是同一个文件"的假提示。上游照比不误 —— 因为它从没把整包套用过，走到目录上的机会极少；这里"拖整包根行"会把每个目录都走一遍，不挡的话提示里会塞满目录名。**这是修 bug 不是改语义**：拿两个空 md5 判"内容相同"本来就是无意义的 |
| 新增「整体替换项」标记（`tree.Node.WholeReplace`），原版没有 | `internal/tree/node.go`（字段）、`internal/service/patch.go`（`MatchWholeReplaceNodes` / `findWholeReplaceTarget` / `IsWholeReplaceName`）、`internal/service/match.go`（两处 `continue`）、`internal/service/patchsave.go`（不递归）、`gui/app.go`（`ToggleWholeReplace` + 菜单项 + `NodeView.WholeReplace`）、`app.js` / `app.css`（紫色「整体替换」徽标） | 用户 2026-10-29 报障：**一个 zip 里套着几个 zip**（交付方把每个服务的完整包塞进一个总包，形如 `20261029_account-server_存量账户区分变更和普通.zip`），"识别不到 readme 文件"。查证：`RefreshReadmeNode` 只扫补丁根的**直接子级**，内层包的 README 在第二层、永远扫不到；而内层包的命名（日期\_服务名\_变更描述）和应用里的真实文件名（`account-server.jar`）对不上，`MatchPatchName` 那套全等 / `.jar` 前缀匹配也无从下手 —— 逐文件匹配必然乱。解法按用户选的语义：把这些内层包标成**整体替换项**，它整个换掉应用里的对应项，**内部完全不参与逐文件匹配**。目标判定用"应用节点的主名被补丁名包含"（`20261029_account-server_…zip` 含 `account-server` → 命中 `account-server.jar`），多个候选取主名**最长**的（更具体），主名短于 3 字符直接排除（`a.jar` 那种会误命中一切）；认不出就保持未映射、不猜不新建，前端把徽标换成警示色「整体·未匹配」—— 这是用户唯一能发现"名字认不出来"的线索。写出侧：补丁包里压缩包节点本来就不递归（`IsCompressNode`），这里给 `.war`/`.ear` 也补上 `!child.WholeReplace` 的判断；应用包侧靠 `MappedNode` 写补丁字节（`writeTreeNode2PackWithJar` 与 `writeNodeEntry` 两条路都已通）。**通道是独立的**：`match.go` 的两处索引（`nameMapping` / `createPatchNodeMapping`）都跳过被标记节点，所以它既不会被常规匹配捡走，它的子级也不会被下探 | `TestWholeReplaceMatchesByContainedName`（按包含名命中、统计 2 mod）、`TestWholeReplaceChildrenDoNotParticipate`（子级名字与应用顶层项全等也不许被捡走）、`TestWholeReplaceUnmarkedDoesNothing`（不标记就零映射）、`TestWholeReplacePrefersLongestStem`（`account.jar` vs `account-server.jar` 取后者）、`TestWholeReplaceUnknownNameLeavesUnmapped`（认不出不新建）、`TestWholeReplaceTwoTargetsAreNotShared`（两个标记不抢同一目标）；`TestToggleWholeReplace`（标记 → MOD + 徽标 / 取消 → 归零）、`TestRunPatchWritesWholeReplaceBytes`（产物里那个条目真的是补丁字节） |
| **「标记说明文件」不再对压缩包开放**，并补了「取消说明文件标记」 | `gui/app.go`（`canMarkReadme` 加 `!service.IsArchiveName`、新增 `canUnmarkReadme` / `UnmarkReadme` / `menuUnmarkReadme`）、`internal/service/patch.go`（`RefreshReadmeNode` 加二进制拦截 + 扫不到 README 时清状态、抽出 `IsArchiveName`）、`internal/util/charset.go`（`IsBinary`）、`app.js`（分发 `unmarkReadme`） | 用户 2026-10-29 报障：右键内层 zip 出现了「标记说明文件」，点了之后**整片乱码**。两个 bug 叠在一起：① 原版 claim 只判了"非目录"，压缩包也能标 —— 而 README 是要当文本解码的，zip 的字节丢给 `DecodeString` **不会报错**（GB18030 的映射覆盖全部字节组合），只会解出一大段方块和生僻字，再被 `ReadMePattern` 匹配出一堆不存在的指令；② `RefreshReadmeNode` 在根下找不到 README 时**直接 return**，没清 `rootInfo.ReadmeNode` / `PatchSign.Readme` —— 于是"取消标记"之后说明面板还挂着旧文本、映射还按旧指令跑。修法：菜单项按 `IsArchiveName`（.jar/.war/.zip/.ear）挡掉压缩包；解码前用 `util.IsBinary`（前 8KB 有 NUL 即判二进制）兜一道，补丁包里本来就放了个二进制当 README 时同样不解析；`readmeNode == nil` 分支把两处状态一起清空。另外补了原版没有的**反向操作**：它靠"把另一个文件标成说明文件"让旧的自动让位，根下只有一个 .txt 或标错了压缩包时用户没有退路 —— 只能关掉包重开 | `TestPatchMenuClaimsFollowUpstream` 新增 `sub.zip` 用例（根下的压缩包：**没有** `markReadme`、**有** `wholeReplace`）、`README.txt` 用例改为断言出现 `unmarkReadme`；`TestUnmarkReadmeClearsState`（旧版误标的 zip 取消后：`ReadmeNode` 为 nil、`PatchSign.Readme` 为空、Type 回到 NONE，且二进制内容一开始就没被解码）；`TestIsBinary`（zip 头 / class 头判真，UTF-8 / GBK / ASCII 判假）、`TestIsBinaryOnlySniffsHead`（只看前 8KB） |




| 搜索按钮是"只有图标"的那个 | `index.html` `#btn-search` | 不是偏差 —— 原版 `HomeView.buildHeader` 里搜索按钮就是 `new Button()` + `ImageView(search.png)`，**没有文字**。这里照做（同一排的其它按钮是文字按钮，与原版工具栏的构成不同，见 §四「工具栏按钮图标」） |
| 预览面板的几何一律夹进视口 | `app.js` `clampContentGeom` / `applyContentGeom` | 原版是**系统窗口**，拖出屏幕还能靠系统窗口菜单 / 任务栏拉回来；Go 版是没有系统标题栏的应用内浮层，标题栏一旦离开视口就再也没有抓手，而位置又被记着 → 永久卡死（用户报的"拖出去就动不了了"）。所以横向至少留 72px、纵向保证标题栏整条可见。详见 §一「文件内容预览」 |
| 窗口外观走"现代工具风"（配色 / 圆角 / 投影 / 行高 24px / 按钮质感） | `app.css` 的 `:root` 与窗口外观层 | 原版用的是 JavaFX 默认亮色观感；Go 版从一开始就是自己的一套亮色主题（见 §一「配色」），这一轮按用户要求把它往现代桌面工具的方向推了一版：树行 21px → 24px、圆角 3/4px → 4/6/8px、新增三级投影、工具栏加分组竖线与品牌色块、状态栏的增改删做成药丸；当天下午又按"按钮这些增加一下质感"把三类按钮改成"有厚度的一小块"（半透明渐变 + 顶部内高光 + 底部内暗影 + 外投影，按下换内凹阴影，图标按钮悬停才浮现边框）。**代码预览区（白底 / 行号 `#aeb3c2` / 14px / 语法高亮）与双栏差异的行底色、`＋/－/~` 符号一律没动** —— 那几块是对齐原版 `theme.css` 的，有跨语言黄金基准与像素级走查锁着 |
| 应用图标用原版那套图案，但补成 6 层 | `gui/build/windows/icon.ico` / `gui/build/appicon.png` | 原版 exe 的 `RT_GROUP_ICON` 只有 **1 层 64×64**（实测 PE 资源表），大图标视图下会被系统拉伸。这里用原版 200×200 的 `icon.png` 重采样出 16/32/48/64/128/256 六层，除 256 层是 1.28 倍放大外全是缩小，质量优于原版。**图案没有重画**，生成脚本 `_extract/tools/make_appicon.py`。详见 §一「应用图标」 |
| 设置弹窗按原版分两组、补上「开启补丁文件比对」「保留历史条数」 | `gui/frontend/dist/{index.html,app.js,app.css}` 的 `#settings-overlay` / `gui/app.go` 的 `Settings` 与 `ApplySettings` | 原版 `ConfigView` 是「常规配置 / 搜索配置 / 补丁配置」三段（各一个不可折叠的 TitledPane）。Go 版只搬了**能真正生效**的项：补丁配置五项（路径/文件名匹配、写入补丁签名、正则、说明文件、操作人员）+ 搜索配置一项（保留历史条数），并补了原版「开启补丁文件比对」.**没有搬「常规配置」** —— 它下面只有主题与语言，而 Go 版只有亮色一套、界面文案是中文写死的，摆上去没有第二个可选值，空一组标题更怪。补的两项后端都是现成字段（`config.PatchConf.PatchFileDiff` 已驱动 `CreatePatchDiffInfo`、`config.SearchConf.HistoryLimit` 已被 `AddHistory` 使用），只差一个开关。另两项「保存结果到源目录」「加载最后操作的补丁」原版有、Go 版 `config.PatchConf` 里**只有字段和默认值、全项目零使用点**（功能未实现），按用户要求**只登记不实现**（见 §五第 36/37 项）。历史条数 ≤0 时回落 30（与 `SearchConf.AddHistory` 自己的兜底一致），不照抄原版 `historyLimit` 为 null 时拆箱 NPE 那个崩溃点。前端把弹窗结构从平铺改成两组标题（视觉语言与 `.pane-title` 的 3px 主色条一致），并给 `type=number` 去掉了 Chrome 的上下箭头（原版 `IntegerField` 是个带正则过滤的 `TextField`，也没有箭头）。走查新增 4 条钉这组改动（见 §六） |

> **界面层的偏差统一记在 §一「文件内容预览」与「编辑包内文件内容」**（含
> "差异模式切编码原版会崩"那条）。上表只重复登记两条 —— 双栏差异高亮与
> 编辑包内文件内容都是**新增能力**，比对截图时最容易被误判成"移植错了"。

**关于拖放的 JavaFX 语义**（决定了上面两条为什么这么写）：
JavaFX 的 `DragEvent` javadoc 明确 —— *"If this happens over a gesture target
that accepted previous `DRAG_OVER` events with a transfer mode supported by
gesture source, a `DRAG_DROPPED` event is sent to the gesture target."*
也就是说目标在 `DRAG_OVER` 期间没调 `acceptTransferModes`，`DRAG_DROPPED`
**根本不会投递**。所以原版 `onDragDropped` 里那句没有校验的 `files.get(0)`
实际上永远跑在"已经通过 `onDragOver` 校验"的前提下 —— 它是安全的，
但安全依赖于事件机制而不是它自己的代码。

### 3.10 跨语言黄金基准：输出包的字段级差异清单

用同一夹具分别驱动原版 Java（`../_extract/golden/GoldenHarness.java`）与 Go 版，
对两条写出路径逐字段比对。**条目名、顺序、解压后内容完全一致**；
下表是剩余的全部差异，分三类。

先明确一件事：**逐字节一致做不到，也不该作为目标。**
Java 的 `ZipOutputStream` 底层是 JDK 的 zlib `Deflater`，Go 的是纯 Go `flate`，
同一输入压缩出的字节流不同（实测 `MANIFEST.MF`：Java csize=27 / Go csize=31，
usize 都是 25）。算法不同，这不是 bug，也无法通过调参数消除。
所以判定口径是**语义等价**：条目集合、顺序、解压后内容一致。

**A. 已修复（曾真实分叉，黄金基准抓出来的）**

| 曾出现的分叉 | 根因 | 修复位置 |
|---|---|---|
| 同级节点被排序 | `SortLinkedList.add` 被覆写成纯追加（`addAndIndex` 注释掉了），构造时传的比较器**从未被调用**；Go 早期按"名字看着像排序"实现了排序插入 | `tree/node.go` 的 `AddChild` 改为追加，删掉 `SortLinkedList` 式排序 |
| `originAppPath` 少一个反斜杠 | Java 用 `File.getPath()`，Windows 下会把 `/` 归一成 `\` | `util.ToPlatformPath`，在 `GetAppTreeInfo` / `GetPatchTreeNode` 里调用 |
| 目录条目带 Unix 权限位 | 早期调了 `hdr.SetMode(os.ModeDir\|0o755)`，连带把 external attributes 写成 `0x41ed0010`、version made by 写成 `788`（Unix）；Java 两者都是 MS-DOS 默认值（`0` / `20`） | 去掉 `SetMode`（`service/writer.go` / `service/patchsave.go`） |
| STORED 条目多一个 data descriptor | `CreateHeader` 无条件置 `flags |= 0x8`；Java 对 STORED 是尺寸已知的，直接把 crc/size 写进本地头 | 改走 `CreateRaw`，并把 version 显式设成 Java 的 STORED 值 10 |
| 新建条目时间戳为 0 | Java 的 `putNextEntry` 会给未设时间的条目填**当前时间**；Go 两个都没设，写出 1980-00-00 | `writeDirEntry` / `writeFileEntry` 只设 DOS 时间、不设 `Modified`（设了会连带写出 extra） |
| 签名 JSON 差 15 字节 | Jackson `INDENT_OUTPUT` 用 `System.lineSeparator()`，Windows 下是 **CRLF**；Go 写的是 LF | `util/json.go` 按平台取行分隔符 |

**B. Go 标准库硬限制（改不动，已确认）**

| 差异 | Java | Go | 为什么改不动 |
|---|---|---|---|
| 目录条目压缩方式 | `DEFLATED`，csize=2（空 deflate 流） | `STORED`，csize=0 | `archive/zip` 的 `CreateHeader` 强制把以 `/` 结尾的条目改成 Store 并清零尺寸（`writer.go:343`）。`CreateRaw` 虽不强制，但对 `/` 结尾的名字返回 `dirWriter{}` 丢弃写入 —— 那 2 字节补不进去，硬写会让后续本地头错位、整个包损坏 |
| 0x5455 扩展时间戳的布局 | LOC 13 字节（flags=0x07，mtime+atime+ctime）/ CD 5 字节（flags=0x07，仅 mtime） | LOC 与 CD 同为 5 字节（flags=0x01，仅 mtime） | Go 的 `FileHeader` 只有**一个** `Extra` 字段，LOC 与 CD 共用，写不出这种不对称布局 |
| UTF-8 标志位（`0x800`） | 所有条目都置 | 仅当名字确实需要时才置 | Java 的 `ZipEntry` 默认按 UTF-8 编码名字并置位；Go 直接透传名字字节。**单独置位会出错**：若源包是 GBK 名字，Go 透传的是 GBK 字节，置了 UTF-8 位反而让读取方按 UTF-8 解码成乱码。要和 §2.5 的 GBK 处理一起做，不能只改这一处 |

**C. 不可复现（含"当前时刻"，两次运行本就不同）**

| 差异 | 说明 |
|---|---|
| 新建条目的 DOS 时间戳 / `operTime` | 都是"打补丁的那一刻"，两次运行必然不同。基准脚本里已归一化 |
| `creationTime` / `accessTime` | Java 的 `DateUtil.toFileTimeOrCur` 在时间缺失时**回落成当前时间**，所以这两个值本身不可复现 |

**D. 有意保留的差异（非 bug）**

| 差异 | 位置 | 理由 |
|---|---|---|
| 目录条目用 STORED | `service/writer.go` / `service/patchsave.go` | 同 B 类第一条，是 Go 标准库强制的，不是选择 |
| 内嵌 jar 重打包后字节不同 | `service/writer.go` | A/B 两类差异的**级联**：内层 jar 自己也有目录条目、也有 deflate 流。已确认内层 jar 的条目名/顺序/内容与 Java 一致 |

判读工具：`../_extract/golden/compare.py`（语义比对，会**递归**进内嵌压缩包按条目内容比，
不会因为内层 jar 字节不同就误报）、`zipdiff.py`（字段级 dump，用于定位上面这些差异）。
`run.sh` 会先跑一遍变异验证的前置检查 —— 见 §六。

### 3.11 补丁树右键菜单相关的上游行为

移植右键菜单时逐条比对原版 claim 条件发现的、容易"顺手改错"的地方：

| 行为 | 说明 |
|---|---|
| `isCompressNode` 与 `instanceof CompressNode` 是**两回事** | `TreeNodeUtil.isCompressNode(node)` 按**文件名后缀**判（`.jar` / `.zip`）；`node instanceof CompressNode` 判的是**节点种类**（内容来自压缩包）。前者用在「标记根节点」的 claim 里，后者用在「资源管理器中查看」的 claim 里。混用会让压缩包里的 `README.txt` 也出现「资源管理器中查看」，而它根本没有磁盘路径 |
| 「资源管理器中查看」实际只在补丁目录模式出现 | 上一条的推论：补丁包是 zip 时所有条目都是 `CompressNode`，claim 全不命中。只有补丁**目录**模式下才有磁盘上的真实文件 |
| 补丁根自己会显示「保存文件」和「资源管理器中查看」 | 根节点是 `new FileNode()`（`isDir()` 取自磁盘），zip 包下它既不是目录、也不是 `CompressNode`，两条 claim 都命中。看着别扭，但原版如此 |
| 「重新加载」与「刷新信息」是**同一个 ID、两种文案** | 主根显示 `reload`，自定义根显示 `refresh`。做的事也不同：主根重读整个包，自定义根只重跑映射、**不重读 README**（`refreshPatchMappedNode(false, rootNode)` 那个 `false`） |
| 「聚焦补丁」没有 claim | 原版这一项无条件加入菜单，只是勾选态跟着 `focusTreeStatus` 走 |
| 「复制MD5值」对目录是**置灰**而非隐藏 | `copyMenu.setOnShowing` 里 `setDisable`，菜单项还在 |
| 「关闭补丁」后应用树的增/改/删标记**不会被清** | `cleanMappedNode` 只清"有映射对端"的节点，而纯删除标记走 `mappedDelNodeWithType` → `countAndSetNodeType`，只改类型、不建映射，遍历够不到。原版 `handleClosePatch` 之后也没有别的清理路径。**这不是漏移植**，是照搬（由代码推得，未跑 Java 复现） |
| `getNameWithoutVersion` 的版本号正则要求"版本后还有一段" | 正则是 `-[0-9.]+-.*`，所以 `bar-2.3.4-SNAPSHOT.jar` → `bar`，而 `foo-1.0.0.jar` **原样保留**。直觉上"该把版本号去掉"，但原版就是这么写的 |
| 绑定信息文本里标记与路径之间**没有空格** | `sb.append(code)` 后直接 `append(路径)`，输出形如 `+BOOT-INF/classes/com/example/New.class\t...` |

---

## 四、未完成项

| 项 | 位置 | 说明 |
|---|---|---|
| GBK 解码 zip 条目名 | `internal/pack/zip.go` | 需补 GBK 解码；否则中文文件名乱码。要与"写出的包是否置 UTF-8 标志位"一起做（见 §2.5） |
| ~~GBK 解码文本内容~~ | ~~`internal/util/charset.go`~~ | **已做**（2026-10-10）：GBK 分支用 GB18030 解码器。之前是 TODO 桩，导致 GBK 的补丁说明文件整段变问号 |
| UTF-16 带 BOM 的文本解码 | `internal/util/charset.go` | 探测认得出 UTF-16 BOM，解码仍统一按 UTF-8 处理。实践中未遇到，先留着 |
| 字符集探测精度 | `internal/util/charset.go` | 当前为近似实现，与 juniversalchardet 有偏差（非中文场景会认错） |
| 系统语言探测的非 Windows 分支 | `internal/util/os_lang_other.go` | Windows 已改用 `GetUserDefaultUILanguage`（与 Java `Locale.getDefault()` 等价）；macOS / Linux 仍靠 `LANG` 等环境变量近似 |
| 内容读取的 zip 随机读优化 | `internal/tree` | `ZipOffset` 字段已预留 |
| `config.SearchHisItem.SearchTime` 的时间格式 | `internal/config/config.go` | 该字段 Java 侧同样是 `@JsonFormat("yyyy-MM-dd HH:mm:ss")`，Go 仍按 RFC3339 序列化。同一类问题已在 `model.JavaDate` 解决，配置侧尚未接入 |
| UTF-8 标志位与 GBK 解码 | `internal/pack/zip.go` / `service/writer.go` | 两者必须**一起**做：单独置 `0x800` 会让 GBK 名字被读成乱码。见 §3.10 的 B 类第三条 |
| 黄金基准纳入 CI | `_extract/golden/run.sh` | 脚本已可一键复跑，但尚未接进 CI（依赖 JDK + mvn 产物，接入成本待评估） |
| GUI 剩余屏 | `gui/frontend/` | 已落地主页双树 + 底部「补丁信息」面板 + 补丁树右键菜单 + 应用树右键菜单（8 项）+ 文件内容预览（含 `.class` 反编译）+ 编辑包内文件内容 + 搜索（三层入口）；待补设置、READ ME 编辑、补丁签名列表、关于 |
| 应用树右键菜单剩余 5 项 | `gui/app.go` | 新建目录、重命名、删除、标记删除、保存为项目 —— 原版 13 项里未落地的部分（已落地 8 项 + 我们新增的「编辑内容」）。理由见 §五第 18 条 |
| 应用树的「保存源文件」 | `gui/app.go` | 后端 `SaveSourceNodeAs(side, path)` 已就绪（补丁树已接），应用树只差一行菜单项。原版两棵树都注册了这一项 |
| `Ctrl+S` 保存 / 打补丁 | `gui/frontend/dist/app.js` | 原版 `HomeView.build` 里 `rootPane.setOnKeyPressed` 同时处理 `Ctrl+F`（已补）与 `Ctrl+S` → `handleSaveAppPack()`（带 `isLoading()` 守卫）。用户这轮要的是"三个搜索入口"，保存快捷键不在内 |
| GUI 真人点击走查 | `gui/build/bin/PatchAssistant2J.exe` | 已通过编译 + 程序化测试 + 资源嵌入校验 + 无头浏览器走查（237 条，含变异验证）；真人点击仍待做 |
| 工具栏按钮图标 | `gui/frontend/dist/index.html` | 搜索按钮的图标已补（原版那个按钮就是"只有图标"的）。2026-10-16 起该图标由 `icons/search.png` 换成**内联 SVG**（细线条放大镜，见 §五第 58 条）——`icons/search.png` 本身仍留在目录里（`gui/icon_test.go` 的常量表守门要求它与 `iconSearch` 一一对应）。其余按钮（打开应用包 / 打开补丁包 / 重新匹配 / 设置 / 执行打补丁）在原版没有对应物，图标要另选一套。已与用户确认先不做，见 §一「树节点图标」 |

**已完成**：原版 Java 黄金基准实跑 —— 见 §3.10。`patch` / `patchreadme` 两条
写出路径均已与 Java 对跑通过（条目名、顺序、解压后内容一致）。

---

## 五、分阶段计划

**阶段 1（已完成）地基** — 模型、工具、配置、建树、README 解析。

**阶段 2（已完成）补丁匹配与保存** — 无 GUI 依赖：

1. ✅ `PatchMatchProcessor` 的双模式匹配（路径匹配 + 文件名匹配）
2. ✅ `ReadMeFileService` 的树查找部分（`findAppNode` / `findPatchNode`）
3. ✅ 补丁签名写出（`META-INF/patch-up/<yyyyMMdd>NN.sign`）
4. ✅ 应用包保存（`AppPackWriteProcessor`，重打包 zip）。
   默认输出名与源包同名（对齐原版 `setInitialFileName(根节点名)`，**不带后缀**）；
   早期自行拼过 `-patched`，已去掉，详见偏差登记表
5. ✅ 补丁目录模式（非压缩包，直接读目录）
6. ✅ 补丁包 README 写回（`updatePatchReadme`）
7. ✅ CLI：`patch` / `patchreadme`

**阶段 3 GUI（进行中，选型已定 Wails v2）** — 详见 §一"阶段 3 已开始"：

1. ✅ Wails 骨架并入主模块（`gui/`，`wails build` 通过）
2. ✅ Go 绑定层：状态查询 + 六个动作 + 会话级 `TotalInfo`
3. ✅ 主页：工具栏 + 双树 + 变更着色 + 状态栏 + 设置弹窗
4. ✅ 把文件拖进窗口（左 `.jar` → 应用包；右 目录/`.jar`/`.zip` → 补丁包）
5. ✅ 补丁树右键菜单：标记根节点 / 标记说明文件（README 识别范围的正解）
6. ✅ 绑定层测试 15 个（复用黄金基准夹具）
7. ✅ 亮色主题（配色集中在 `app.css` 的 `:root`，换肤只改这一块 + `main.go` 的 `BackgroundColour`）
8. ✅ 补丁树右键菜单其余项：菜单整体数据化（后端算 `MenuItemView` 列表下发），
   落地 11 项中的 8 项新动作 —— 重新加载/刷新信息、取消关联、保存文件、复制
   （名称/路径/相对路径/MD5 四个子项）、查看签名、查看绑定信息、资源管理器中查看、
   关闭补丁、聚焦补丁；配套两个弹窗（签名 / 绑定信息）。
   绑定层测试增至 32 个。
9. ✅ 底部「补丁信息」面板（对齐 `HomeView.buildBottomCentre` / `updatePatchInfo`）：
   显示比对警告 + README 正文，两段的拼装规则与判空口径逐条对齐原版；
   同时给 README 节点加行底色 + 左色条 + `README` 标签（原版靠换图标，
   当时我们的树还没有图标列 —— 图标已在第 15 项补上，这三层保留）。绑定层测试增至 34 个。
10. ✅ 补丁树右键「保存源文件」：只对 `.class` 显示，反编译成 Java 源码后
    另存为 `.java`（默认文件名把后缀换掉）。非 `.class` 静默不做（对齐原版），
    反编译失败则报错（原版 `parse` 抛异常由全局处理器接住，用户看得见）。
    **刻意偏差**：导出编码固定 UTF-8，原版是 `getBytes()` 的平台默认编码
    （Java 11 + 中文 Windows = GBK，有损）。见 §一。
    仍未落地 2 项：保存为项目（`JarDecompilerService`）、创建说明文件
    （依赖 READ ME 编辑器）。
11. ✅ 嵌套 jar 的惰性展开 + 点名字展开/收起 + 叶子双击打开预览。
    用户报的"jar 包没有高亮 / lib 包没有再次打开按钮"是同一个根因，见 §一。
12. ✅ 文件内容预览（`ContentView`）：`internal/content` 包（解析器分派 + 7 种语言
    高亮 + 行号）+ 浮动面板（不铺遮罩，对齐原版 `Modality.NONE`）+ 差异模式 +
    编码切换 + 预览右键菜单。两个跨语言基准：高亮 204/204、差异行号 132/132。
13. ✅ `.class` 反编译（`internal/decompile`）：把原版 fernflower jar 嵌进二进制，
    外挂本机 JRE 执行；内部类随主类一起传；前端双击先显示"正在反编译…"。
    跨语言基准：命令行方式与原版 API 逐字节一致（52 个样本 + 内部类 + 中文），
    Go 侧另有 6 样本基准（见 §2.1）。
    `gui` 包测试 54 个（含子测试 74 个；内容预览 13 个，含 3 个反编译用例）。
14. ✅ 双栏预览的差异高亮（`content.ComputeSplitMarks` + `.cline.mark-*` / `.mk-*`）——
    **刻意增强，原版没有**（原版双栏不做任何标记），见 §一。
    呈现方式是"行号列 ＋/－/~ 符号 + 行级浅色底 + 正文只染真正变了的词"
    （**四次返工**才定下来：行底色 → 否掉 → 整行染色 → 否掉 → 只染色变了词 →
    用户改主意把行底色加回来）。
    实现过程中修掉一个真 bug：`myersOps` 的闸门错量成 `n+m`，导致大文件
    "只差几行"也退化成整篇替换（用户报的"全部内容高亮"根因）。
    `internal/util` 新增 `textdiff_deltas_test.go`（10 条，含
    `TestDeltasLargeFileWithOneChange` / `...ChangedAtVeryStart` /
    `TestDeltasDoesNotTrimCommonSuffix`）；`internal/content` 新增
    `splitmarks_test.go`（15 条，词级差异 + `applyInline` + `RenderMarked`）。
    原有 `content_test.go` 里的 `RenderMarked` 调用补 `nil` 参数。
15. ✅ 树节点图标（`gui/icon.go` + `frontend/dist/icons/`）：原版那 21 张 PNG
    原样嵌入二进制，选图规则逐条对齐 `TreeNodeUtil.getIconImage`；
    同时去掉目录名的尾斜杠。`gui` 新增 5 个（`TestIconByExt` / `TestIconName` /
    `TestIconFilesExist` / `TestIconMatchesOriginalPNG` / `TestIconsEmbedded`）。
    至此 `gui` 包共 **61** 个测试（含子测试 81 个）。
16. ✅ 双栏预览的**滚动同步**（`app.js` 的 `syncScroll`）—— **刻意增强，原版没有**
    （原版两个 `VirtualizedScrollPane` 各滚各的），见 §一。只同步纵向；
    不用锁防回环、带夹取保护。走查新增 5 条（`split-scroll-*`），
    另有 2 轮变异验证（去掉调用 → 2 条红；去掉夹取保护 → 1 条红）。
17. ✅ **在另一棵树里定位**（`LocateMapped` + `NodeView.Mapped` + `revealRow`）——
    **刻意增强，原版没有**（原版「聚焦应用树」是显示模式开关，不是定位），见 §一。
    补丁树里带映射的文件行单击 → 应用树展开祖先并滚到对应行、短暂高亮；
    200ms 防抖躲开双击；`revealRow` 沿 DOM 逐层下探（嵌套 jar 内部路径不带
    jar 前缀，按 path 查全局索引会串到别的 jar）。`gui` 新增 6 个测试
    （`TestLocateMapped*` 4 个 + `TestNodeViewCarriesMappedFlag` + 夹具），
    走查新增 10 条（`locate-*`，含同路径**诱饵 jar**），另有 3 轮前端变异
    + 2 轮 Go 变异。至此 `gui` 包共 **67** 个顶层测试。
18. ✅ 应用树右键菜单（原版 `HomeLeftTreeView.buildAppTreeContextMenu` 有 13 项）：
    落地 7 项 —— 重新加载、取消关联、复制 ▸、保存文件、资源管理器中查看、
    关闭应用包、聚焦应用树，顺序与各自 claim 逐条对齐（含"聚焦应用树"没有
    claim、永远显示）。配套 `reloadAppPack` / `CloseAppPack` 两个动作。
    未做的 6 项（新建目录 / 重命名 / 删除 / 标记删除 / 保存源文件 / 保存为项目）
    各有理由：前四项是**改包结构的写操作**，与本轮"只改内存、不碰磁盘原包"的
    约束冲突，要先想清楚语义（改的是内存树还是磁盘？）；保存源文件与保存为项目
    分别依赖 `.class` 反编译导出与 `JarDecompilerService`。
    ——其中「保存源文件」的**补丁树**那一份已在第 20 项落地，应用树这份只差
    一行菜单项（后端按 `side` 参数写的）。见 §四。
19. ✅ **编辑包内文件内容**（右键「编辑内容」）—— **刻意增强，原版没有**
    （原版的「编辑模式」菜单项被作者整段注释掉了），见 §一。
    后端：`tree.Node.SetContent` / `MemorySource` / `Edited` 标记、
    `util.EncodeString`、`gui/editor.go`（`OpenEditor` / `SaveContent` /
    `SaveContentAs`）；前端：两棵树共用的右键菜单 + 编辑器浮窗
    （文本 / **class 源码** / 十六进制分页 / 编码切换 / 保存 / 另存为 / 还原 /
    未保存提示）+ 树行「已修改」星号。
    `.class` 默认走**反编译源码**（可改、可另存为 `.java`，但**没有「保存」** ——
    源码回写 class 要 javac 与完整依赖），底部另有「改字节」入口切回十六进制；
    反编译不可用时静默降级到十六进制，不报错。
    测试：`internal/tree` 新增 5 个、`internal/util` 新增 4 个、`gui` 新增 15 个
    （含端到端"改内存 → 产物生效"，以及 class 源码模式 / 强制字节 / 降级 /
    拒绝保存四条），走查新增 31 条（`edit-*`）+ 1 条自检（`probe-count`）。
    至此 `gui` 包共 **81** 个顶层测试，走查 **94** 条。
20. ✅ 补丁树右键「保存源文件」：只对 `.class` 显示（判据抽成 `isClassNode`，
    菜单显不显示与动作做不做共用），反编译后另存为 `.java`。
    后端 `gui/app.go` 新增 `SaveSourceNodeAs` / `sourceBytes` / `sourceSaveName`；
    前端 `dispatch` 加 `saveSourceFile` 分支。
    **刻意偏差**：导出编码固定 UTF-8（原版 `content.getBytes()` 走平台默认编码，
    Java 11 + 中文 Windows 上是 GBK，emoji 会被写成 `?`）。见 §一。
    测试：`gui` 新增 5 个（菜单只挂补丁树 / 非 class 静默不做 / 默认文件名 /
    `isClassNode` 判据 / 反编译出的字节是源码而非 class）；走查新增 6 条
    （`savesrc-*`）。至此 `gui` 包共 **86** 个顶层测试，走查 **100** 条。
21. ✅ **源码编辑器「编译为 .class」** —— **刻意增强，原版没有**。
    用户改完反编译出的 Java 源码后，点「编译为 .class」用本机 JDK 重新编译，
    按原 class 版本号锁 `--release`（**JDK 8 上改用"不传版本参数"，见第 56 项**），
    用已打开的应用包 + BOOT-INF/lib 下所有 jar
    摊平做 classpath；编译成功写回节点（含内部类），失败把 javac 报错原样展示。
    内部类节点不能单独编译（源码本来就在外层类文件里）。
    后端：`internal/javac` 包（`Compile` / `ClassVersion` / `ReleaseFor`）、
    `gui/classpath.go`（胖包摊平）、`gui/editor.go`（`CompileContent` /
    `checkCompiledSet` / `compileAbility`）；前端：源码形态下显示编译按钮、
    dirty 时确认后调用、成功刷新树 + toast、失败展示报错。
    实测：mysql-connector-java 8.0.20（510 顶层类）抽 60 个做往返实验，
    **52 成功 = 87%**；失败原因全是反编译固有缺陷（局部变量名冲突、泛型重建不准、
    桥接方法缺失）。自造自包含样本 `RoundTrip.java`（版本 52）做守门验证。
    测试：`internal/javac` 9 个、`gui` 新增 6 个（ability 标志 / 往返 / 报错 /
    非 class 拒绝 / 内部类拒绝 / 集合校验）；走查新增 4 条
    （`edit-src-compile-*`）。至此 `gui` 包共 **92** 个顶层测试，走查 **104** 条。
22. ✅ **树节点文件详情列（修改时间 / 大小 / MD5）** —— 对齐原版界面的信息展示。
    用户要"详细对比文件信息"，所以文件行的右侧不再只有一个大小，而是三列：
    修改时间（`ModTime`）、大小（`Size`）、MD5（`Md5`）。
    - 后端：`NodeView` 新增 `Md5 string \`json:"md5,omitempty"\``，`toView()` 里填
      `n.GetMd5OrInit()`。`ModTime` / `Size` 原本就有，只是前端没用上 `ModTime`。
      `omitempty` 让目录节点不带该字段（树是按层懒加载的，不给目录算 MD5）。
    - 前端：`makeRow` 里文件行画 `.modTime` / `.size` / `.md5` 三个 `span`，
      靠 `.modTime { margin-left: auto; }` 整组右对齐；CSS 三列共用一条规则
      （11px 等宽字体、弱色、`flex: none` 不被名字挤变形）。
    - 一处顺带的口径变化：大小那列去掉了 `node.size > 0` 的前置条件，
      0 字节文件也显示 `0 B` —— 否则"这个空文件到底存不存在"看不出来。
    - **与原版不一致的地方另开 §一「树节点文件详情列」详记**（不接
      `isShowFileDetail` 开关、`humanSize` 格式、右对齐 vs 紧跟名字 三处）。
    - 测试：`gui` 新增 1 个（`TestNodeViewCarriesMd5`：文件节点有 MD5、
      目录节点没有）；走查新增 5 条（`tree-file-details` / `tree-file-modTime` /
      `tree-file-size` / `tree-file-md5` / `tree-dir-no-details`）。
      至此 `gui` 包共 **93** 个顶层测试，走查 **109** 条。
    **补充（UI 走查修正，沿用第 21 项「编译为 .class」）**：用户反馈两处界面问题，
    均为纯前端：① 源码底部提示文案过长，把底栏撑成两行、连带「改字节」按钮文字
    被拆行 —— 给 `.contentwin-foot .hint` 加 `flex:1 1 auto; min-width:0; overflow:hidden;
    text-overflow:ellipsis; white-space:nowrap`，并把 source 形态的引导句由
    「改完后点「编译为 .class」写回包，或「另存为 .java」导出」缩短为
    「改完用「编译为 .class」或「另存为 .java」」（按钮本身已摆在那里，无需在
    文案里重复），完整版挂到 `title` 上 hover 可见；② 编译失败只走底部会自动
    消失的 `toast`，javac 的多行诊断（含 `文件:行: 错误:` / `^` / `N 个错误`）看不清、
    不能复制 —— 在编辑器主体与底栏之间新增一条**常驻错误条**（红框、可滚动、
    可整段复制、右上角 ✕ 关闭，编译成功或手动关闭时清除），`CompileContent`
    失败时把原始报错填进去，`toast` 仍保留作即时红条提醒。`dist/index.html` 与
    走查用的 `probe_frontend/index.html` 两份都加了该元素（否则走查会
    `DRIVER-CRASH`）。走查新增 3 条（`edit-err-hidden-initial` / `edit-err-show-on-fail`
    / `edit-err-close`），并给 mock 后端加了 `CompileContent` 失败分支（源码含
    `/*FAIL_COMPILE*/` 哨兵时 reject 出 javac 风格报错）来驱动失败路径。
    变异验证：去掉 `setEditorError` 的报错填充 → `edit-err-show-on-fail` 红（1 条）。
    至此走查 **112** 条（GUI 顶层测试仍为 **93** 个）。
    **补充（modTime 兜底修复，对应需求 F「待替换的文件没有显示具体时间」）**：
    用户反馈补丁树（即"待替换的文件"）里大量文件看不到具体修改时间。根因在
    `internal/pack/zip.go` 的 `modTime()`：Go 的 `archive/zip` 在读取「零 DOS
    时间戳」条目时**不会**返回零值，而是把 MS-DOS 历元 1980-01-01 经月份归一化
    后解析成 `1979-11-30 00:00:00` 这类固定哨兵（本机实测如此，与 timezone 无关）。
    于是 `if !t.IsZero()` 判定为"有值"，注释里写的"缺失回落到当前时间"兜底**永不触发**，
    零时间戳条目被显示成一个毫无意义的固定日期，让人误以为"没显示具体时间"。
    修复：凡解析出的时间**早于 MS-DOS 历元（1980-01-01）**即视为缺失、回落
    `time.Now()`；真实时间戳（哪怕就是 1980 年）都晚于该阈值，原样保留。
    该改动对应用树 / 补丁树一并生效（两棵树都走 `modTime`），属纯后端修正，
    **不新增走查断言**（前端 `makeRow` 本来就按 `node.modTime` 渲染，后端补出真实值即自动显示）。
    `internal/pack` 新增 2 个单测守住：`TestModTimeRealTimestampPreserved`
    （真实时间戳原样返回、不被哨兵误伤）与 `TestModTimeZeroTimestampFallsBackToNow`
    （零时间戳必须落在 1980-01-01 之后、且就是当前时间）。
    **补充（映射节点的时间，对应"待替换的文件"的后续反馈「没有时间」）**：
    上一处修的是 zip 条目的哨兵；但用户真正点名的那个文件（应用树里、路径为
    `element-server2-com/grp/.../ElementValueSpecialServiceImpl.class`）根本不在
    应用包里 —— 它是**匹配阶段造出来的"映射节点"**（补丁里有、应用里没有的文件，
    见 `patch.go` 的 `doMatchMappedNodeChildrenWithReadme` / `addMappedNodeChildren`
    与 `NewMappedNode`）。真正的根因：Java 的 `MappedNode` **覆写**了
    `getModifyTime()`，直接返回被绑定（补丁侧）节点的时间；**Go 版漏了这个覆写**，
    `Node.ModifyTime()` 只返回自身字段 —— 而映射节点自身三个时间字段全是零值，
    于是"修改时间"那列是空的。修复：`Node.ModifyTime()` 对 `KindMapped` 转发到
    `MappedNode.ModifyTime()`（与 Java 覆写等价；Java 只覆写了 modify，create/access
    未覆写，故只转发 modify）。`internal/tree` 新增 3 个单测
    （`TestMappedNodeModifyTimeFollowsBoundNode` / `...WithoutBoundNode` /
    `TestNonMappedModifyTimeUnaffected`）；变异验证：退回 `return n.modifyTime`
    → `TestMappedNodeModifyTimeFollowsBoundNode` 红。纯后端修正，前端零改动、
    **不新增走查断言**。
    - **同处另有两个未移植的 `MappedNode` 覆写**（本轮**未改**，登记待办）：Java 的
      `MappedNode` 还覆写了 `isDir()`（转发 `mappedNode.isDir()`）与 `consumeBytes()`
      （转发 `mappedNode.consumeBytes()`），Go 版同样没转发。前者只在"映射节点包裹
      目录"时才有差异（README 新增指令的末段若本身是目录），后者会让"打开映射节点
      的内容"读到空。用户本轮只报"没有时间"，故未扩大范围。
    **补充（文件名被挤压，对应「左侧文件名都显示不全了」）**：
    用户反馈应用树里文件名被压成 `Ag…`、`Ele…` 这种只剩几个字符的样子。
    根因在 flex 的收缩分配：`.row` 里 `.name` 是**唯一**没设 `flex` 的项，
    而右侧 `.modTime` / `.size` / `.md5` 三列全是 `flex: none`（谁都不让位）；
    更要命的是 `.name` 带 `overflow: hidden`，这会让 `min-width: auto` 失效
    （规范：automatic minimum size 只在 `overflow: visible` 时生效），于是文件名
    能被一路压到 0 宽 —— 窄面板下实测正是 **0px**，32 字符的 MD5 反倒占满整行。
    （原版 JavaFX 的 `FileTreeCell` 把「文件名」和「时间/大小/MD5」放进同一个
    HBox，两个 Label 都能被压缩、按各自 minWidth 分配，不会出现"只有文件名挨压"。）
    修复两处：
    - `.name { min-width: 100px }` —— 给文件名一个下限，不再被压没；
    - `.md5 { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis }`
      —— 空间紧张时让**最长、最不具可读性**的 MD5 先收缩并省略，把宽度让出来
      （完整值挂 `title`，hover 可见）；时间/大小短且可读，保持 `flex: none`。
    - **顺带修掉自己引入的副作用**：`.name` 有了 `min-width` 后，挂在它**外面**的
      「已修改」星号 / README 标签会被推到 100px 处（实测短文件名时空 63px）。
      故把这两个标记挂进 `.name` 内部，并给纯文件名单独一层 `.name-text` 承担
      省略号（若把 ellipsis 留在 `.name` 上，长文件名会把星号一起截掉）。
      `.name` 因此变成 flex 容器，**又带出第二个坑**：flex 容器的 `text-decoration`
      不会传播进 flex 子项（实测子项拿到 `none`），所以 `.t-del` 的删除线必须
      改写到 `.name-text` 上，否则"已删除"的文件看着跟普通文件一样。
    - 走查新增 4 条：`tree-narrow-name-kept`（窄面板下文件名宽 ≥100px）、
      `tree-narrow-md5-yields`（MD5 被截断、先让位）、`edit-mark-hugs-name`
      （星号距文件名末尾 ≤10px）、`tree-del-line-through`（删除线真的画在
      `.name-text` 上）。前两条靠临时把 `#app-tree` 的 `max-width` 改窄造场景，
      第四条靠临时挂 `t-del` 类 —— 都只动内联样式/class，不动数据。
      变异验证：删掉 `min-width` → 文件名宽 0px、`tree-narrow-name-kept` 红；
      把 `.md5` 改回 `flex: none` → `tree-narrow-md5-yields` 红；星号挂回 `row`
      → 星号距名字 63px、`edit-mark-hugs-name` 红；去掉 `.name-text` 的删除线规则
      → `tree-del-line-through` 红。
    - 走查的 `rowIn` / `childRowIn` 相应改为按 `.name-text` 的文本匹配（原来直接读
      `.name` 的 `textContent`，会把星号也读进来 —— 挂上星号的行就再也按名字找不到了）。
    至此走查 **118** 条（GUI 顶层测试仍为 **93** 个）。
23. ✅ **工具栏「全部折叠」** —— 刻意增强，原版没有。
    左侧 jar 包树（特别是 Spring Boot 胖包）展开嵌套 jar + 目录后常常又长又深，
    用户没有一个动作能回到根视图，只能一层层点名字收起 —— 体验差。
    工具栏「重新匹配」之后新增一个 `全部折叠` 按钮：清空 `expanded` 状态（应用
    树 / 补丁树一并清，不分先后 —— 当前两棵树都按"treeKey:path"作 key，互不污染），
    然后 `apply(state)` 按当前数据重建两棵树；toast 报告清掉了多少条展开节点。
    不重跑匹配、不动 `state` 本身、不调后端，纯前端 O(N) 操作（清字典项）。
    - `dist/index.html` + 走查用的 `probe_frontend/index.html` 两份都加按钮
      （否则走查 `$('btn-collapse-all')` 为 null 会 DRIVER-CRASH）。
    - 走查新增 2 条（`collapse-all-pre-expanded` / `collapse-all-collapsed`）：
      在末尾测，前置条件由前面 `nested-jar-*` 那批已经展开 `BOOT-INF/lib` 提供。
    - 变异验证：把 `collapseAll` 里 `clearExpanded('app'); clearExpanded('patch');`
      改成 no-op → `collapse-all-collapsed` 红（应用树折叠后行数仍是 18 而不是 1），
      已自动还原。
    至此走查 **114** 条（GUI 顶层测试仍为 **93** 个）。
24. ✅ **树的横向滚动 + 可拖动分隔条**（对齐原版 TreeView / SplitPane）。
    用户对着原版截图提的：红框圈的是两棵树下方那两条**横向滚动条** ——
    原版 TreeView 的 cell 按内容自然宽度排布，装不下就出横向滚动条，左右滑一下
    就能看全；而 Go 版此前把行压进容器（见第 22 项"文件名被挤压"），内容永远
    不超出容器，**滚动条根本不出现**。同一轮还要求"内部边框可以拖动"，
    对应原版 HomeView 的两个 SplitPane。
    - **横向滚动**：`.row` 加 `width: max-content; min-width: 100%`，并把
      `.name` / `.name-text` 设为 `flex: none` —— 整行按内容宽度排布、不压缩，
      超出部分交给 `.tree`（本就是 `overflow: auto`）横向滚动。
      **上一轮那套"压缩 + 省略号"随之作废**：`.name` 的 `min-width: 100px`
      与 `.md5` 的收缩/省略规则都删了 —— 两套方案互斥（压进容器就没有滚动条），
      用户要的是后者。`min-width:100%` 保证内容比容器窄时行仍撑满（悬停底色
      不断），`width:max-content` 则让底色在横向滚动时也覆盖到溢出那截
      （少写它"不压缩"照样成立，但底色会断）。
    - **两个分隔条**：新增 `#split-v`（两棵树之间）与 `#split-h`（树区与
      「补丁信息」之间），对应原版 `HomeView` 里 `topPane` / `centrePane`
      两个 SplitPane 的 divider。拖动改内联样式（左右 flex-basis 百分比、
      下方像素高度），不落盘 —— 原版 divider position 也只活在会话里。
      原来 `.pane + .pane` 的左边框与 `.info-pane` 的上边框改由分隔条自己画
      （`::before`），悬停/拖动时变主色。聚焦模式（`body.focus-*`）下另一棵树的
      分隔条一起收掉，并把剩下那棵的 flex 还原（否则拖过分隔条后聚焦只剩半屏）。
    - `dist/index.html` 与走查用的 `probe_frontend/index.html` **两份都加**了
      这两个元素（少一份走查会 DRIVER-CRASH）。
    - 走查：上一轮的 `tree-narrow-name-kept` / `tree-narrow-md5-yields` 随方案
      作废，换成 4 条 —— `tree-narrow-name-not-squeezed`（文件名没被压缩）、
      `tree-narrow-md5-full`（MD5 完整不省略）、`tree-narrow-hscroll`（内容超出、
      出现横向滚动条）、`tree-row-fits-content`（行盒按内容宽度铺开、底色不断）；
      另加 3 条分隔条断言（`splitter-present` / `splitter-v-drags` /
      `splitter-h-drags`，用 mousedown+mousemove+mouseup 模拟拖动）。
      分隔条那三条**放在最后**：拖动会改布局，排前面会把别的断言带偏。
      变异验证：去掉 `.row` 的 `max-content` → `tree-row-fits-content` 红
      （行盒只有容器宽、子项溢出）；整体退回压缩方案 →
      `tree-narrow-name-not-squeezed` 红（文件名 clientWidth=0）+ 上条红；
      让分隔条不绑 `mousedown` → `splitter-v-drags`/`splitter-h-drags` 红。
    至此走查 **123** 条（GUI 顶层测试仍为 **93** 个）。
25. ✅ **内容预览的差异呈现返工**（需求 J，多轮）—— 这是对**刻意增强**本身的
    反复调整，不是移植，见 §一「双栏差异高亮」。
    - 第三版：行号列 3px 色条 + 正文只染真正变了的词；顺带修掉 `maxMyersD`
      闸门口径这个**真 bug**（用户"全部内容高亮"的根因，与选哪种高亮无关）。
      走查 **123 → 138** 条（新增 `split-inline-*` 8 条 + `split-mark-*` 等）。
    - 第四版：行号列 ＋ / － / ~ 符号 + 行级浅色底（删浅红 / 增浅绿 / 改浅黄）。
      走查 **138 → 147** 条（新增 `split-line-bg-*` 4 条 + `split-glyph-*` 6 条
      + `split-lineno-bg-follows-line` 1 条；同时删掉被取代的
      `split-mark-no-fill` / `split-mark-lineno-bar` 两条）。
      **行底色把第一版否掉的方案又加回来了 —— 用户明确改主意，别再删。**
    - 顺带修掉走查 fixture 的一处失真：`mock-backend.js` 的行号原来没按 Go 侧
      `content.PlainNumbers` 的口径右对齐补齐，导致新加的"行号列等宽"断言假红
      （详见 §六的变异列表）。
    - 这一轮的教训：`::before` 的断言有两个非直觉的坑（选择器要指到挂
      `::before` 的元素、`content` 返回带引号的字面量），详见 §六。
26. ✅ **预览面板的「最大化」按钮** —— 对齐原版，不是增强。
    原版 `ContentView.showDialog` 里 `dialog.setResizable(true)`，JavaFX 会给
    可缩放的对话框挂上系统标题栏的最大化按钮 —— 所以原版"有这个按钮"是框架给的。
    Go 侧的浮动面板没有系统标题栏，就在自己的标题栏上补一个
    （`#content-max`，图标 `□` / 还原态 `❐`），行为对齐：铺满应用窗口、
    最大化时拖不动、再点一次回到最大化前的尺寸与位置。
    走查 **147 → 161** 条（新增 14 条 `content-max-*` / `content-drag-works-after-restore`）。
    **这条的断言返工过两次**（都是"变异没变红"）：一次是最大化前没挪面板，
    "还原到默认尺寸"与"还原到原位置"结果重合；一次是"最大化 → 还原 → 关闭"
    里还原那步自己修好了尺寸，测不到 `rememberContentSize` 的守卫。
    详见 §六的变异列表。
27. ✅ **打补丁产物的默认文件名去掉 `-patched` 后缀** —— 对齐原版（不是增强）。
    原版 `HomeView.handleSaveAppPack` 用
    `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())` ——
    初始名就是应用包自己的文件名、初始目录就是应用包所在目录，**不带后缀**。
    移植时自行拼成了 `<主名>-patched.<ext>`，用户要求去掉。三处同步：
    `app.js` 的 `runPatch` 建议名、`gui/app.go` 的 `defaultOutputPath` 兜底、
    `cmd/patchassistant` 的 `defaultPatchedPath`。
    **代价**：默认值与源包同名同目录，在保存对话框里直接确定会被系统提示覆盖
    （原版就是这个行为）；CLI 不带 `-o` 会原地覆盖应用包，用法说明里已标注。
    走查 **161 → 162** 条（新增 `runpatch-suggest-name-no-suffix`，`report.py`
    为此新开一组「执行打补丁」）；Go 侧新增 `TestDefaultOutputPathHasNoSuffix`
    与 `TestDefaultPatchedPathIsAppPackItself`（后者新建了 `cmd/.../main_test.go`）。
    详见偏差登记表。
28. ✅ **搜索：三层入口**（需求 L）—— 对齐原版，其中两处修的是**上游真 bug**。
    用户问"是不是缺少一个搜索的快捷键"，查下来缺的是整套入口。三块：
    ① 树上打字即搜（`FastSearchControl`，13 条走查）；
    ② 全局搜索对话框（`SearchView`，后端 `gui/search.go` + 前端 24 条走查）；
    ③ 面板内查找 + 跳转行列（`SearchBox` / `GotoRowColDialog`，15 条走查）。
    详见 §一「搜索：三层入口」，偏差见 §3.9。
    - 走查 **162 → 214** 条（三组共 52 条）；`report.py` 补了三组说明与 11 条变异记录。
    - Go 侧新建 `gui/search_test.go`（**19** 个顶层测试）：空关键词走历史、
      历史去重/头插/裁剪、两棵树与路径标签、根不参与匹配、大小写敏感、
      `*` 全匹配、路径式沿父子链匹配、**链条必须连续**（兜底分支从第一级重启）、
      非法通配符返回错误、只开一棵树、**嵌套 jar 的现场解压 / 未命中回滚 /
      已展开不重复解压**、去重规则、历史与节点项不互撞。
      至此 `gui` 包共 **113** 个顶层测试。
    - 变异验证：前端 5 轮、Go 6 轮，全部至少 1 条红。**其中两轮第一遍没红**，
      都是"断言/用例写弱了"：`打开搜索弹窗时不清空搜索框` 只查了列表内容没查
      输入框；`路径式搜索的兜底分支接着 index 走` 的原有用例压根到不了那条分支。
      各自补强之后才抓住 —— 详见 §六的变异列表。
    - 顺带把 `icons/search.png` / `icons/time.png` 拷了进来（图标目录 19 → 21 张），
      `TestIconFilesExist` 的"不许有多余文件"这条反向校验要求它们也进常量表。
29. ✅ **预览面板不能拖出视口**（需求 M）—— 修的是**移植带来的结构性差异**。
    用户报"打开的对比窗口在大窗口下拖出去就动不了了"。原版那个 `Dialog` 是
    系统窗口，拖出屏幕还有系统窗口菜单 / 任务栏兜底；Go 版是没有系统标题栏的
    应用内浮层，标题栏离开视口就彻底没有抓手，而位置又被记着 → 永久卡死。
    - 实测复现（视口 1374×800）：右下拖 +4000/+3000 后面板停在 `4227,3090`，
      视口内离标题栏最近的点命中 `statusbar`，抓不到；关掉重开还是原地。
    - 修法：几何统一在 `applyContentGeom` 里夹一次（横向至少留 72px 面板、
      纵向标题栏整条可见），拖动 / 打开 / 还原 / 视口变化四条路径共用；
      `window.resize` 那条也一并夹（把主窗口往小拖能复现同一个问题）。
    - 走查 **214 → 219** 条（新增 5 条：`content-head-measurable`、
      `-drag-clamps-left-top`、`-drag-clamps-right-bottom`、
      `-drag-clamp-keeps-grabbable`、`-clamp-survives-reopen`）。
      其中"还抓得住吗"用 `elementFromPoint` 做命中测试 —— **只量 left/top
      证明不了这件事**（值看着在视口内、实际被别的层盖住）。
    - 变异验证：去掉夹取 → **4 条红**。**返工过一次**：第一版两次拖动位移等大，
      无夹取实现正好被抵消回视口内，只红 2～3 条；把最后一次位移放大
      （199999）并放到最后，才让"抓得住 / 重开"两条真的踩到。
    详见 §一「文件内容预览」与偏差登记表。
30. ✅ **界面美化（现代工具风）** —— 用户："把界面 ui 美化一下，现在太难看了"。
    只动**窗口外观层**（工具栏 / 面板头 / 树行 / 状态栏 / 补丁信息 / 弹窗 /
    右键菜单 / 滚动条），代码预览区与增改删语义色**刻意没动**（有像素级校验锁着，
    见 §一「配色」）。详见 §一「配色」。
    - 走查 **219 条仍全绿** —— 这一轮的价值就在这：走查里锁着 `--add`/`--mod`/
      `--del`、三个浅色底、代码区与行号栏的 RGB，改坏了会直接红。
    - 顺手修了一个真 bug：`var(--sans)` 从来没定义过，`.contentwin-err-head`
      的 `font: 600 12px var(--sans)` 整条被丢弃。
    - 顺手把 `probe_frontend/index.html` 改成**生成物**（`sync_index.py`）：
      实测发现手工抄的那份已经与 dist 漂了（少了 `<em>` 说明行等）。
    - 新增 `ui_shot.js` + `ui_shot.html`：把界面摆成"像真实使用"的样子截图，
      用于**看**外观（不跑断言）。写它的过程中踩到一个坑：`cloneNode` 不复制
      `__node`（那是 JS 属性不是属性节点），克隆出来的行没有右键菜单。
31. ✅ **应用图标换成原版那套** —— 用户："能不能应用图标也使用 java 的桌面应用图标"。
    原版 exe 的 `RT_GROUP_ICON` 只有 1 层 64×64（实测 PE 资源表），这里从原版
    200×200 的 `icon.png` 重采样出 6 层（16…256），**图案没重画**。
    详见 §一「应用图标」。
    - 验证一：从**成品 exe** 抠出 6 层 `RT_ICON`，逐层确认 PNG 签名有效、能解码、
      尺寸与源 ICO 一一对应（10364 / 3137 / 1356 / 1045 / 676 / 319 字节）。
    - 验证二：启动 exe 用 `PrintWindow` 截标题栏，确认**运行时窗口图标**
      确实变成了原版那个绿色菱形（见 §六「窗口图标实测」）。
    - 生成脚本 `_extract/tools/make_appicon.py`（手写 PNG 编解码 + Lanczos3，
      本机没有 PIL）。
32. ✅ **按钮质感** —— 用户："按钮这些增加一下质感"。
    三类按钮（`.btn` / `.btn-primary` / `.btn-icon`）都改成"有厚度的一小块"：
    半透明垂直渐变 + 顶部内高光 + 底部内暗影 + 外投影，按下换成内凹阴影，
    图标按钮悬停时边框才浮现。详见 §一「配色」。
    - 走查 **219 条仍全绿** —— 走查不量按钮外观（只有存在性与行为断言），
      所以这一轮的安全网是"截图肉眼看"，不是断言。
    - **无头浏览器触发不了 `:hover` / `:active`**，所以做了一个状态展示页：
      加载**真实的** `app.css`，再把 `:hover:not(:disabled)` /
      `:active:not(:disabled)` 机械改写成 `.sim-hover` / `.sim-active` 类名注入，
      这样"模拟态"用的就是产品里的真实规则、而不是手抄一份。截图逐状态确认。
33. ✅ **设置弹窗对齐 Java（分两组 + 补两项）** —— 用户："这是 java 端的设置页内容"（给了 Java 端设置页的构成）。原版 `ConfigView` 是「常规 / 搜索 / 补丁」三组，Go 版把弹窗从平铺改成「搜索配置 / 补丁配置」两组（**略去空着的「常规配置」** —— 它下面只有主题与语言，Go 版没有第二可选值），并补上「开启补丁文件比对」「保留历史条数」两项。这两项后端字段本来就有（`config.PatchConf.PatchFileDiff` / `config.SearchConf.HistoryLimit`），只差一个开关；另两项「保存结果到源目录」「加载最后操作的补丁」原版有、Go 版只有字段没实现，按用户要求**只登记不实现**（见 §3.9 与下两条）。详见 §3.9 偏差表。
    - 走查 **219 → 223** 条（新增 4 条：`settings-grouped` / `settings-new-fields-filled` /
      `settings-apply-sends-new-fields` / `settings-history-limit-falls-back`）。
      这组是**新增覆盖** —— 之前驱动里一行都没碰过设置弹窗，连那次"手写页漂了"
      都没暴露它，故一并补上。`report.py` 的 GROUPS 加了「设置弹窗（原版 ConfigView）」组。
    - 后端：新增 Go 单测 `TestApplySettingsNewFields`，钉住 PatchFileDiff 真落到
      `PatchOptions`、HistoryLimit 真裁剪历史、填 0 回落 30（共 54 → **55** 个 GUI 顶层测试）。
34. ⬜ 设置 / READ ME 编辑 / 补丁签名列表 / 关于
35. ⬜ 树内拖动节点做映射（原版 `TreeNodeTreeMatchProcessor`，与"拖文件进来"是两回事）
36. ⬜ 真人点击走查
37. ⬜ 设置弹窗补「保存结果到源目录」（原版有；Go 版 `config.PatchConf.SaveToSourceDir` 只有字段、功能未实现，见 §3.9）
38. ⬜ 设置弹窗补「加载最后操作的补丁」（同上，`config.PatchConf.LoadLastPatch` 只有字段、功能未实现，需顺带持久化上次路径）
39. ✅ **预览头部按钮等大模型化 + 反编译会话内缓存** —— 用户："双击文件之后，会出现这样的加载，稍微有点慢。并且右上角的放大按钮大小不太和 X 匹配"。两点诉求：
    - **按钮等大**：原版用 Unicode 字符 `□`(U+25A1) 与 `✕`(U+2715) 当最大化/关闭图标，Segoe UI 里渲染尺寸天然不对等（□ 是窄矩形、✕ 是交叉粗笔）；改成同 `viewBox`(0 0 12 12) + 同 `stroke-width`(1.4) 的 SVG（矩形 / 交叉线），最大化态再翻成"两个错位矩形"的还原图标。`app.js` 的 `syncMaxBtn` 改用 `innerHTML` 注入 SVG（不再用 `textContent` 设字符 —— 那会清掉 `<button>` 里的 `<svg>` 子节点）。
    - **反编译缓存**：`App` 加 `decompileCache map[string]string` + `sync.Mutex`；新增 `decompileClass`，以 `节点路径 + 主类与所有内部类字节 sha256` 为 key，命中直接返回字符串，省掉"重复打开同一个 class"的 JVM 冷启动。`content.go` / `editor.go` 两个反编译入口都改走它；并用一个 `decompileRun` 包级 seam（默认 = `decompile.Decompile`）让单测能数"真正起 JVM 的次数"。
    - **诚实边界**：缓存只让"重复打开同一 class"变快；首次打开那个 class 的 ~1 秒 JVM 冷启动无法消除（JVM 本身就这么多），不对外宣称"反编译变快了"。
    - 走查 **223 → 224** 条（新增 1 条 `content-buttons-equal-size`，直接量两个按钮的 `getBoundingClientRect` 钉死等大；实测 max=28×22 / x=28×22）。把原来只比存在性的 `content-max-btn-initial/-flips/-back` 从 `textContent==='□'/'❐'` 改成比 SVG 里 `<rect>` 个数（1=最大化、2=还原），顺带让"图标换成 SVG"这件事也被断言守住。
    - 后端：新增 Go 单测 `TestDecompileClassCaches`，断言同一 class 反编译两次只起 1 次 JVM、两次结果一致、缓存不无限膨胀。gui 包现共 **115** 个顶层测试（本轮 +1）。
    - 验证：`go vet` 干净；`go test ./gui/...` 全绿（51s）；走查 224/224 全绿；exe 重建 12,883,456 → **12,888,576** 字节。
40. ✅ **预览面板内 `Ctrl+F` 搜正文**（需求 Q）—— 用户："在这个窗口时，搜索内容时是有问题的。你看看。"（附的是文件内容预览窗口的截图）。
    此前 `findArea()` 只查 `.edit-text`（editor 的 textarea），而**预览面板是只读的 DOM 行渲染、根本没有 textarea**，所以预览里 `Ctrl+F` 被
    `if (!findArea()) return;` 静默吞掉 —— 工具栏那个"搜索"按钮又是搜**文件名/路径**的，不是搜文件内容。这是**已知未做功能**（本轮补上，不是行为分叉）：
    - **隐藏文本镜像**：`renderContent()` 在渲染完两栏后，往 `#content-body` 追加一个 hidden 的
      `<textarea class="edit-text mirror-text">`，内容为两栏纯文本（split 用 `\n\n` 拼）。
      `findArea()` 因此返回它 —— 现有 `findHits`/`findCount`/`findBtnState`/`findSelect`/`findUpdate`/`findStep`/`findOpen`/`findClose`
      **一行数学逻辑都没改**，编辑器模式（textarea 可见）与预览模式自动分流。
    - **高亮不改 DOM**：新增 `renderFindHighlights()` + `collectRanges` + `buildLineMap` + `pushPaneRanges` + `rangeForOffsets` + `scrollToActiveHit`
      用 **CSS Custom Highlight API**（`CSS.highlights` / `::highlight`）把命中画成黄带、当前命中画成更深的橙带。
      刻意不用"改 DOM 加 span"的路子 —— 那会破坏反编译代码原有的语法着色（token span）。命中跨行时按行拆 `Range`；
      split 模式下两栏各搜各染；`scrollIntoView({block:'nearest'})` 滚到当前命中（双栏 `syncScroll` 负责把另一栏镜像过去）。
    - **状态收尾**：`findClose()` 里 `CSS.highlights.delete('find-hit'/'find-active')` + `mirrorInfo = null`，
      编辑器模式走 `renderFindHighlights()` 里 `!ta.hidden` 的早退分支，纯属 no-op（原行为不变）。
    - **诚实边界**：`::highlight` 需要 Chromium 105+ / Edge 105+。原版就是内嵌 WebView2（Chromium），本版同样内嵌 WebView2，
      所以不是一个新增的硬性要求；不支持时搜索**功能仍可用**（命中计数、`n/m`、上下翻、滚动都在），只是不再有黄带。
    - 走查 **224 → 237** 条（新增 13 条 `preview-find-*`：镜像存在/隐藏/与两栏 DOM 逐字一致、Ctrl+F 真能打开查找条、
      `keep(n);` 两栏各一处计数 1/2、`find-hit`/`find-active` 注册与 Range 数、active 命中真落在 `.lt` 内、下一个/上一个、关闭后清除高亮）。
      其中 `preview-find-mirror-matches-dom` 是关键的**忠实度锚点**：镜像文本必须与渲染层纯文本逐字相同，否则命中偏移会错位。
    - Go 侧**零改动**（本轮纯前端）；`go test ./gui/...` 仍全绿。exe 重建 12,888,576 → **12,896,768** 字节。
    - 范围收窄（按用户报障只修这一处）：工具栏的「搜索」按钮（搜文件名/路径）不动、编辑器模式 `Ctrl+F` 行为不变。
41. ✅ **对比视图里 `Ctrl+F` 与编辑模式同步**（需求 R）—— 用户："两个文件对比时，搜索应该也和编辑的时候同步。现在还是这样的"（附截图：双栏对比里弹出的是**全局搜索**对话框）。
    根因是**焦点路由**，不是查找逻辑：面板内 `Ctrl+F` 的处理器挂在 `#content-win` 上、靠事件气泡到达；对比视图的代码栏是**只读 div、默认不可聚焦**，用户双击打开对比后没点代码区，焦点还在树上/body，`Ctrl+F` 一路气泡到 `document`，被 `initSearch` 的全局处理器接走 → 弹出按文件名搜的「搜索」对话框。编辑模式点进的是 textarea、焦点天然在面板里，所以一直是对的。
    - **代码栏可聚焦**：`makePane` 给 `.code` 加 `tabIndex = 0`，点击/Tab 都能把焦点带进面板；`.code:focus:not(:focus-visible) { outline: none }` —— 鼠标点出来的焦点不出框，键盘 Tab 过来的保留可见轮廓（与 `.fastsearch` 同口径）。
    - **打开时聚焦**：`renderContent` 渲染完把焦点带进第一栏。对齐原版 `ContentView` 是**模态**对话框、一打开焦点就在代码区里；延一拍（`setTimeout 0`）是因为 `renderContent` 常跑在 `showContentWin` 之前，面板还是 `display:none` 时同步 `focus()` 会静默失败（实测）。
    - **诚实边界**：`.code` 是 `overflow:auto` 的可滚动容器，按现行 HTML 规范本身就可聚焦（无头实测：无 `tabindex` 时 `focus()` 也生效、有 `tabindex` 时才可点击聚焦）—— 所以"点击代码栏后 `Ctrl+F`"在旧版其实也能走对，用户中招的是**打开后没点代码区直接按 `Ctrl+F`**（焦点停在树上）。`tabIndex` 的价值是明确化 + 跨引擎稳健 + 键盘可达。焦点在面板**外**（树/body）时 `Ctrl+F` 仍是全局搜索（原版 HomeView 行为），走查有反向对照断言钉住，没有被误伤。
    - **顺带修了一个真 bug**（新走查暴露）：`findClose()` 原来把 `mirrorInfo` 置 `null`，而它只有 `renderContent` 才会重新赋值 —— 于是**关掉查找条再按 `Ctrl+F` 重开，第二次只剩计数、没有高亮**。`mirrorInfo` 描述的是"当前正文渲染层"的结构、不是查找状态，改成跟着正文生命周期走：`renderContent` / `renderEditor` 换正文时各自维护，`findClose` 不再碰它。
    - 走查 **237 → 245** 条（新增 8 条 `route-*`：两栏可聚焦、打开后焦点在第一栏、**从代码栏** `Ctrl+F` 出查找条且不弹全局对话框、这条入口输入的关键词能数出命中、反向对照"焦点在面板外仍是全局搜索"、收尾关闭）。旧断言派发在 `#content-win` 上、绕过了真实焦点路径，所以一直没暴露这个问题 —— 新断言全部派发在**代码栏元素**上。
    - 变异验证 3 轮，全部按预期变红：去掉 `tabIndex` → 2 条红；去掉"打开时聚焦" → `route-focus-on-open` 红且复现 `activeElement=BODY` 的 bug 态；把 `mirrorInfo = null` 加回 `findClose` → 3 条 `preview-find-*` 高亮断言红。
    - Go 侧零改动；`go test ./gui/...` 仍全绿（45s）。exe 重建 12,896,768 → **12,899,328** 字节。
      留证截图 `route-r-highlight.png`（双栏 + 查找条 + 左右栏各一条命中，由 `t_route_shot.js` 复现）。

42. ✅ **对比两个 jar/zip 包里哪些文件不同**（**新功能**，原版没有）—— 用户："能否增加一个功能，用来支持一下状况。我不知道两个jar包里面有哪些文件是不同的，我把两个jar包上传之后，对比一下，然后打印出哪些文件不同处。你可以先规划一下，然后开始做"。
    原版要回答"两个包差在哪"，只能把一边当应用包、一边当补丁包走**补丁语义**（README 升级指令、路径/文件名匹配、增删改统计），而且右侧还要求包里有 README 才算得对。用户要的是更朴素的一件事：**逐文件比内容**。所以做成独立入口，不碰会话里的两棵树。
    - **分层**：`internal/service/jarcompare.go` 的 `ComparePacks(a, b)` 是唯一业务实现（与 CLI 同级共用，未来要加 `pa compare` 子命令直接复用）；`gui/jarcompare.go` 只做四件杂事 —— `PickCompareJar(role)`（选文件，role 决定对话框标题）、`ComparePacks`（直通）、`CopyCompareReport`（剪贴板，走 Go 侧 `runtime.ClipboardSetText`，与 `CopyMappedInfo` 同款，不依赖 WebView 的 `navigator.clipboard` 权限）、`SaveCompareReport`（保存对话框 + 落盘）。前端只渲染，报告文本不重拼。
    - **比对口径**（界面提示与报告文本都按这个说）：① 只比**文件条目**，目录条目跳过；② 同路径 + MD5 相同 = 相同，MD5 不同 = 内容不同，路径只在一侧 = "仅 A 有"/"仅 B 有"；③ **嵌套 jar 按普通二进制文件比，不递归展开**（`BOOT-INF/lib/x.jar` 内容变了就整条报"内容不同"，不进它内部逐条比）；④ **不过滤** `META-INF/patch-up/` 下的签名文件 —— 那是本工具写的书志，但"两个包差在哪"的诚实答案包含它，滤掉反而是隐瞒；⑤ 条目名**不做** `./` 之类的规整，与建树侧（`pack.BuildZipTree`）口径一致，免得出现"对比说相同、树上路径却不同"。
    - **不假设哪边是新版本**：界面与报告统一用中性词「包 A / 包 B / 仅 A 有 / 仅 B 有」，不写"新增/删除"—— 用户可能 A 放新包，说"删除"就是错的。状态栏那套增/改/删药膏是补丁语义的统计，两者不混用。
    - **流式读取**：`os.Open` 拿到的 `*os.File` 本身就是 `io.ReaderAt`，`zip.NewReader` 不必把整包读进内存；逐条目 `io.Copy` 进 md5 哈希，内存里只留每个条目的路径/大小/MD5/时间。大小用**实际读到的字节数**而不是 `UncompressedSize64`（元信息里声明的大小不可全信）。
    - **两个包的路径相同直接拒绝**（含 trim 后相同）：不拒绝的话结果是"全部相同"，用户会以为自己选错了包。非 zip 文件给"不是有效的 jar/zip"，不把 `archive/zip` 的原文抛给用户。
    - **保存返回 `(bool, error)`**：用户在保存对话框里取消时返回 `false`，前端据此区分"已保存 / 已取消保存"。`SaveMappedInfoAs` 那个老绑定取消也报"已保存"（前端一律 `runVoid`），**没有照抄这个毛病**；老绑定要不要一起修，留待用户发话（属既有行为，不在本次范围）。
    - **界面**：工具栏新增「对比」按钮（原版没有这个入口，故用文字按钮，不像搜索那样做图标按钮）→ 遮罩式弹窗（与设置/搜索同款）：两个文件槽（路径只读 + 「浏览…」）→「开始对比」→ 汇总药膏（内容不同 / 仅 A 有 / 仅 B 有 / 相同）+ 三个分组明细（路径一行、A/B 两侧一行，各带大小与 md5 前 12 位，完整 md5 在 `title` 与报告文本里）→「拷贝报告 / 保存报告」。两个包完全一致时出空状态提示、零明细行。
    - **拖包到弹窗上**（用户追加："这个能否也支持拖拽包到上面"）：两个文件槽就是拖放落点（`.cmp-slot` 挂 `--wails-drop-target: drop`，Wails 的 `useDropTarget=true` 只把落点算到挂了该属性的元素上；自定义属性会继承，所以槽里的输入框/按钮也算，落在它们上面同样收）。分工照搬主窗口那套：**前端只回答"落在哪个槽上"**（`compareDropTarget` 用 `document.elementFromPoint` 往上找 `data-slot`），**收不收、哪个是 A 哪个是 B 由 Go 侧 `HandleCompareDrop` 判**（可单测）。规则：只收 1~2 个 `.jar`/`.zip`，1 个填落点那个槽、**2 个按 A/B 顺序填（落点不重要）**；超过 2 个或非包文件明确报错，**不静默丢掉**。扩展名判定**不区分大小写** —— 与主窗口那两处刻意不同：那两处的大小写敏感是上游 Java `endsWith` 的保真点，而这是新增功能、没有上游要对齐，且文件对话框过滤器在 Windows 上本来就大小写不敏感。落点悬停时有虚线高亮（`.cmp-slot.wails-drop-target-active`，与 `.pane` 那套同款）。
      顺带补了一个**走查覆盖空白**：`mock-backend.js` 以前**没有提供 `window.runtime`**，于是 `initDrop()` 在走查里直接 return —— 拖入路径从来没被覆盖过。现在补上 `window.runtime.OnFileDrop`（记录回调与 `useDropTarget` 标志），走查可以按真实坐标喂 `(x, y, paths)`。
    - **弹窗可放大**（用户追加："这个窗口可以放大"）：明细动辄几百行，固定 780×540 不够看。两条路：**右下角拖拽缩放**（CSS `resize: both`，与内容预览面板同一套）+ **标题栏一键最大化**（`.dialog-compare.maxed` 铺满视口留 48px 边距，图标/提示语随状态翻，复用内容面板那对 `MAX_ICON`/`RESTORE_ICON`）。
      两个实现要点：① 最大化时**必须清掉内联宽高**再挂 `.maxed` —— 内联样式优先级高于样式表，不清掉的话用户拖出来的尺寸会顶住铺满规则（所以要把当时尺寸先存进 `cmpNormal`，还原/重开时写回）；② `.dialog-body` 默认的 `max-height: 60vh` 要放开（见 `.dialog-body-compare`），否则放大后正文区被卡住、下半截留空。
      尺寸只用一个变量记（`cmpNormal`）：拖出来的尺寸由浏览器写成内联样式、本来就一直挂在元素上，**关闭再打开自然保留**，不需要额外"记住"逻辑 —— 真正需要记的只有"最大化时被清掉的那一份"。最大化状态**不跨打开保留**（与内容预览面板一致：原版每次预览都是 `new Dialog()`）。
    - **弹窗可拖动**（用户追加："按住这个框拖动时，无法拖动"）：弹窗是被遮罩 flex **居中**的，原先没有任何位移机制 —— 按住它拖当然没反应。
      实现用 `transform: translate()` 表达偏移（相对"居中位置"），而不是像内容预览面板那样用 `left`/`top`：面板是 `position: fixed` 自己定位，而弹窗由遮罩居中，用 transform 就不必自己算基准坐标、也不会跟 flex 居中打架。
      三个要点：① **偏移量必须夹住**（横向至少留 72px、纵向整条标题栏可见，与面板同一个抓手口径）—— 弹窗没有系统标题栏，标题栏一旦离开视口就再也没有抓手，而偏移是记住的、重开还在原地，等于卡死（同 `CONTENT_GRAB_MIN` 的教训）；② **最大化时要清偏移**（内联 transform 会顶住 `.maxed` 的铺满规则，与"清内联宽高"同一个道理），还原时写回去；③ 标题栏上的按钮是"点"不是"拖"，`mousedown` 落在按钮上直接 return —— 否则用户想点最大化、弹窗却被顺手拖走了。
    - **点开看差异文件**（用户追加："能否增加一个功能，查看对比出来不同的文件"）：对比只回答"哪些文件不同"，用户接下来必然要问"差在哪"。做成**复用主界面的内容预览浮窗**（`#content-win`）来回答，而不是在对比弹窗里另画一块 —— 语法高亮、行号、差异标记、编码切换、面板内 Ctrl+F、最大化，这一整套只该有一份实现。
      - **浮窗盖在遮罩之上**（z-index 65 > 50，本来就成立），所以**不用关掉对比弹窗**就能连着翻看好几个文件，关掉浮窗就回到列表。
      - **来源记在 Go 侧**（`App.compareA` / `compareB`，由 `ComparePacks` 成功时写入）：读哪个包必须与**产出这份结果的那次对比**一致。让前端把路径传回来的话，用户在结果出来后改了槽里的路径，点开看到的就会是另一个包的内容。所以 `OpenCompareContent` 的参数里**只有条目路径**，没有 `side`（那是树预览的参数）。
      - **左右口径**：左栏 = 包 A、右栏 = 包 B；"仅 B 有"的条目（包 A 没有这个文件）左栏才落到包 B，且退化成单栏。栏标签写成 `<包名>!<条目名>` 而不是裸条目名 —— 两侧条目名一模一样，光看统一 diff 的 `---`/`+++` 分不出哪边是哪个包。
      - **渲染口径只留一处**：把树预览 `OpenContent` 里"两份文本怎么摆成单栏 / 差异模式 / 双栏（带行级差异标记）"那段抽成 `renderContentView`，两条路共用 —— 双栏标记的下标必须与渲染出来的行号一一对应，两边分开写迟早漂移。同理解码口径抽成 `decodeRaw`、反编译缓存抽成 `cachedDecompile`（树那边 key 是"节点路径 + 字节哈希"，对比那边是 `cmp:` 前缀 + 条目路径，命名空间分开）。
      - **`.class` 走反编译**（jar 对比里最常见的差异就是 class，不反编译这个功能基本没用）：`internal/service.ReadPackEntry` 读条目时把**同目录的内部类**（`Bar$*.class`）一并读出来 —— jar 里内部类是独立条目，只喂主类的话内部类是空壳，这与树那边"内部类是主类的子节点"是同一件事。结果按"条目路径 + 字节哈希"做会话内缓存，否则切一次差异模式就要重新起一次 JVM（本机冷启动约 1 秒）。
      - **没有解析器的类型**（图片、嵌套 jar…）给一句带两侧大小的说明，而不是照搬树预览的"交给系统程序打开"（对比场景要打开的是两个文件，用户问的是"差在哪"）。这里还**无条件收起浮窗**（树预览那条路只在 `slow` 时收）—— 用户是在列表里一个个点着看，浮窗里若留着上一个文件的内容，看起来就像"点开的其实是它"。
      - 浮窗里的编码切换 / 差异模式切换走 `reloadContent` 的**来源分流**：`fromCompare` 为真时调 `OpenCompareContent`，否则调 `OpenContent`。
    - **测试**：`internal/service/jarcompare_test.go` 9 条（完全一致 / 增删改混合 + 报告文本 / 目录条目跳过 / 嵌套 jar 按 blob / 同路径拒绝 / 非 zip / mtime 不同仍算相同 / **按条目名读内容（读到 / 读不到 / 目录条目 / 非 zip）** / **`.class` 的内部类收集只认同目录的 `Bar$*.class`**）；`gui/jarcompare_test.go` 10 条（直通绑定把中文错误原样冒出去、默认报告文件名拼接的 5 个用例含 Windows 路径与空名回落、`HandleCompareDrop` 的收与不收、`isComparePackPath` 的大小写与边界，以及 6 条 `OpenCompareContent`：**两侧都有→双栏且左 A 右 B、差异标记真的打上** / 仅一侧有→单栏且标题标明是哪个包（含"仅 B 有时左栏落到 B"）/ 差异模式与会话开关 / 没有解析器→unsupported 且说明带两侧大小 / 前置状态不对要报错 / **`.class` 带内部类反编译 + 会话缓存命中**）。
    - 走查 **245 → 263 → 270 → 276 → 295** 条（18 条 `compare-*` 主流程 + 7 条拖放 + 7 条放大与拖动 + 10 条点开查看：入口与打开、两个「浏览…」的 role 没传反、只选一个包时**只报错不发请求**、两个路径都发、汇总四个数、包名与文件数回显、三组标题带数且顺序固定、内容不同行两侧大小与 md5、仅一侧有时的 "—"、明细行数、拷贝解灰且带真报告文本、拷贝按钮反馈、保存带上两个包名、点遮罩关闭、**完全一致的空状态分支**；回调注册与 `useDropTarget=true`、**两个槽确实带 `--wails-drop-target: drop`**（CSS 契约）、拖到 A 槽只填 A、拖到 B 槽填 B、一次拖两个按 A/B 填、非包文件报错且槽位不动、**回归对照：关掉弹窗拖到应用树上仍走 `HandleFileDrop`**；最大化按钮存在、`resize=both`、**缩放手柄真的存在（`resize=both` 且 `overflow≠visible`）**、最大化铺满视口、最大化态图标翻成"还原"且手柄收掉、还原回普通尺寸、**拖出来的尺寸挺过"最大化→关闭→重开"**；**标题栏光标是 move、拖动真的位移、按在按钮上拖不动弹窗、往左上/右下猛拖都被夹住、最大化时拖不动、还原回到拖到的位置**；**结果行可点（提示行 + pointer 光标）、点行只发条目路径（不带 side）、`.class` 先出"正在反编译…"、两侧都有→双栏且标题带两个包名、浮窗盖在遮罩之上且对比弹窗没被关掉、切差异模式走 `OpenCompareContent`、仅一侧有→单栏、没有解析器→提示且不留下上一个文件的内容、点开期间对比弹窗一直开着**）。
    - 变异验证 13 轮，全部按预期变红：去掉"少选一个包"的守卫 → `compare-run-guards-missing-pack` 红（`ComparePacks` 真被发了）；把「仅 A 有」「仅 B 有」两组顺序对调 → `compare-groups` 红；拷贝时传空文本 → `compare-copy-sends-report` 红；`initDrop` 里去掉对比分支 → 4 条 `compare-drop-*` 红；去掉 `.cmp-slot` 的 `--wails-drop-target` → `compare-slots-are-drop-targets` 红；去掉 `.dialog-compare` 的 `resize: both` → 2 条红；最大化时不给弹窗挂 `.maxed` → 2 条红；最大化时不记普通尺寸 → `compare-size-survives-maximize` 红；**拖动不写位移 → 3 条 `compare-drag-*` 红；拖动不夹取 → 2 条夹取断言红；点行不取内容 → 6 条 `compare-open-*` 红；切差异模式走错来源（一律 `OpenContent`）→ `compare-open-menu-diffmode-routes` 红；不支持的类型不收起浮窗 → `compare-open-unsupported` 红；`.dialog-compare` 的 `overflow` 改回 `visible` → `compare-resize-handle-available` 红；最大化/还原不处理偏移 → `compare-max-drag-disabled` 红；Go 侧"不收集内部类" → 2 条单测红；"仅 B 有时左栏不落到 B" → `TestOpenCompareContent_OnlyOneSide` 红**。
    - **又一条被变异逼出来的教训**：拖动那几条断言第一版把"偏移恰好是 0"当成了前提（拖回中间之后才测最大化/还原），于是**"去掉 `toggleCompareMax` 里那次 `applyCompareOffset`"这个变异全绿** —— 偏移是 0 时，清不清、写不写都看不出来。修法是先拖到一个**非居中**的位置再测，并且让断言额外要求"位置确实偏离居中"（`Math.abs(left - 居中) > 20`），它就没法空过了。与上面"记尺寸"那条是同一个道理：**断言必须踩在"我们的代码真的在起作用"的那段路径上**。
    - 顺手加固了走查驱动：行/栏元素可能因实现被改坏而取不到，原先 `click(null)` 会直接抛出去、把后面所有断言连坐成一条 `DRIVER-CRASH`；现在这几处判空后再点，退化成干净的 FAIL（`compare-open-*` 那一组）。
    - **一条被变异逼出来的教训**：这条"记住尺寸"的断言第一版写成"设内联宽高 → 关闭 → 重开 → 还是那个尺寸"，**变异（把记尺寸的代码删掉）竟然没变红** —— 因为内联样式本来就挂在元素上，浏览器替我们保留了，测到的是浏览器而不是我们的代码。改成"设尺寸 → 最大化 → 关闭 → 重开"（走一遍真正会清内联宽高的路径）之后变异立刻变红。**写断言时要想清楚：这条路径里到底有没有我们的代码在起作用。**
    - **踩坑（值得记）**：走查用的 `probe_frontend/index.html` 是 `sync_index.py` 从 `dist/index.html` **生成**的，本轮先手工改了它、漏跑脚本 —— 结果 `#compare-overlay` 缺失，`compareClose()` 取 null 抛错**把全局 Esc 处理器整段打断**，连累 `content-win-esc` / `search-esc-closes` / `content-max-does-not-poison-remembered-size` 三条**无关**断言变红（它们排在对比段之前，所以不是顺序问题，是同一个处理器被炸）。修法两步：① 按项目约定跑 `python sync_index.py` 重新生成（生成物与 dist 只差"路径/标题/挂哪些脚本"三类，脚本会自校验）；② 给 `compareOpen` / `compareClose` / `initCompare` 加判空防护（对齐 `initSearch` / `dragSplitter` 的既有写法），HTML 与 app.js 版本不一致时不再带崩 `init()`。
    - Go 侧新增两个文件、改动零（既有逻辑一行未动）；`gofmt` 干净（`gui/app.go` 的字段对齐问题是上一轮遗留，未动）、`go vet` 干净、`go test ./...` 全绿。exe 重建 12,899,328 → 12,942,336 → **12,968,960** 字节（后两个数分别是"对比弹窗"与"拖动 + 点开查看"两次追加的产物；最后一次 +26,624 字节，来自前端拖动/点行代码、`.cmp-hint` 等样式与新增的 Go 侧 `OpenCompareContent`/`ReadPackEntry`）。留证截图 `compare-dialog.png`（普通态）与 `compare-dialog-maxed.png`（最大化态），由 `t_compare_shot.js` + `t_compare.html` 复现（加 `?maxed` 出最大化那张）；本轮再加一张 `compare-open-preview.png`（同一个脚本加 `?preview`：点第一行 `com/foo/Bar.class`，内容浮窗盖在对比遮罩之上、标题带两个包名、左右栏分别是 `int v = 1;` 与 `int v = 2;`，且底部「拷贝/保存/关闭」仍可见 —— 证明对比弹窗没被关掉）。

43. ✅ **双栏预览的差异导航 + 行号标记更醒目**（**原版没有**）—— 用户："现在所有的对比之后，差异处不太明显，得上下翻动才能查看不同处。能否增加啥功能更明显，比如在行数地方，有差异处用标识标注出来"。
    排查后发现"行号列标注差异"**本来就有**（`+`/`-`/`~` 符号 + 3px 色条 + 整行浅色底，见第 42 项那批 `split-*` 断言）—— 用户截图里那一段（1~32 行）恰好没有差异、差异在没滚到的位置，所以看着"啥标记都没有"。真正的痛点是**找不到**：行级标记只能回答"屏幕上这几行是不是差异"，回答不了"整份文件的差异都在哪"。于是先与用户确认方向（多选：差异导航 / 整文件概览条 / 现有标记更醒目 → 选定前两项），再动手 —— 避免把"标记不够显眼"误当成"压根没有标记"而重做一套。
    - **差异导航**（标题栏一对 `▲`/`▼` + 计数 `1/2`）：点一下把那一处滚进视口中部，**两栏各描一圈蓝框**；手动滚动时计数跟着走。
      - **差异块由后端给**：`internal/content/splitmarks.go` 新增 `SplitBlock`（mark + 左右各占的行范围）与 `SplitMarks.Blocks`，在 `ComputeSplitMarks` 的 delta 循环里顺手记 —— 同一趟遍历，不必再跑一次差异算法。"哪几行属于同一处差异"是差异算法的知识（一个"改"块里左第 k 行配右第 k 行），让前端扫 DOM 自己猜等于把这个口径复制一份，迟早漂移（与行内标记只算一次同一个理由）。行号 0 基；某侧没有内容时 `Start = -1` —— 写成 0 会让前端去高亮一行根本不存在的差异。
      - `gui/content.go` 的 `ContentView` 新增 `Blocks` 字段，`renderContentView` 的双栏分支透出。单栏与差异模式**没有**块：那两种形态的行号列本来就不带差异标记（对齐原版，加进去还会把行号列撑宽一个字符），没有可跳的目标 —— 前端据此整组隐藏导航。
      - 前端：`contentBlocks` / `contentNavIndex` 状态，`contentNavGo` / `markBlockCurrent` / `scrollToContentBlock` / `syncContentNavFromScroll`；按钮接线在 `initContentWin`（标题栏的拖动处理器本来就跳过 `button`，所以点按钮不会顺手把面板拖走）。
      - **只滚一栏**：两栏的 `scrollTop` 是同步的（见第 22 项），滚左栏、右栏自己跟过去；`block:'center'` 让那一处落在视口中部、上下都留出上下文。参考行优先取左栏（原文那一侧，打开时焦点也在它上面），只有右栏有的块（新增）才落到右栏。
      - **跳转后要短暂忽略滚动同步**：跳转本身会触发 `scroll` 事件，不理会的话计数会被"离视口顶部最近的那一块"改写、与刚点的那块对不上。用**时间戳**（`CONTENT_NAV_LOCK_MS`）而不是布尔标志 —— `scroll` 事件是**异步派发**的，同步置位、同步复位等于没加（与 `syncScroll` 里"不能用锁"是同一类坑，那边靠"赋相同值不再触发事件"绕开）。
      - **计数从 0 起**（`0/2` = "还没跳过任何一处"），不假装已经在第 1 处 —— 第 1 处可能在几千行之外、屏幕上根本看不到。到头置灰、**不绕回**（绕回会让"点了几下"与"文件里第几处"失去对应）。
      - 位置换算用**行高常量**（`CONTENT_LINE_H = 17`，与 `.code` 的 `line-height` 一致）而不是量 DOM：行高固定、正文 `white-space: pre` 不折行，`n × 行高` 是精确的；滚动事件里量几百个 `getBoundingClientRect` 会逼着浏览器反复重排。
    - **行号标记更醒目**（用户同时选的那一项）：`+`/`-`/`~` 符号**加粗 + 去掉斜体**（`.ln` 整体是斜体 —— 对齐原版 RichTextFX 的 `Font.font("monospace", ITALIC, 13)` —— 符号跟着斜，看着像正文的一部分而不是标记），色条 **3px → 4px**。只动字形与宽度，**不碰**行底色与词级着色：那套配色是用户四次返工定下来的（见 README 那段长注释与 `.contentwin-body.split` 上方的说明）。
      - 加粗有把行号列撑宽的风险（那就破坏了"每一行都占住这一格"），走查的 `split-glyph-gutter-aligned` 正好是它的护栏：`--mono` 是等宽字体栈，各字重的字符步进一致，实测宽度仍然只有一种（47.81px）。
    - **测试**：`internal/content/splitmarks_test.go` 新增 4 条（块的精确位置 `mod:1+1/1+1 del:3+1/-1+0` / "只有右栏有"时左栏必须是 -1 / 两份一样时零块 / **不变量：每个带行级标记的行都被恰好一个块覆盖**——前端高亮完全按块的行范围来，覆盖漏一行就是"跳到了却只高亮一半"，覆盖两遍就是两个块指向同一处）；`gui/jarcompare_test.go` 的 `TestOpenCompareContent_Split` 补块数与范围校验。
    - 走查 **295 → 301**（+2 条标记：符号加粗去斜体、色条 4px；+4 条导航：导航出现且计数 0/2 与"上一个"置灰、点"下一个"两栏都高亮且行下标正好是后端给的 4/5、再点一次跳到第 100 行并**真的滚过去**（`scrollTop` 0 → 1561）、单栏时整组隐藏）。
      - 为了让"跳转真的滚了"可测，`mock-backend.js` 的对比预览双栏从 2 行加长到 **120 行**、两处差异分在第 5~6 与第 100~101 行 —— 两行的假数据根本滚不动，断言就只能在"按钮点了没反应"和"真跳了"之间瞎猜。顺带补上 mock 一直缺的 `mark`：真实实现的双栏预览是带差异标记的（走 `content.RenderMarked`），mock 漏了属失真 —— 走查量的是"前端把 mark 落成 `mark-*` 类没有"，数据源头少了 mark 就永远量不到。
    - 变异验证 **+7 轮**（前端 5 + Go 2），全部按预期变红：去掉跳转后的高亮 → `compare-open-nav-highlight` 红；跳转不滚 → `compare-open-nav-next-jumps` 红；导航永不隐藏 → `compare-open-nav-hidden-single` 红；符号不加粗 → `split-glyph-bold-not-italic` 红；色条退回 3px → `split-mark-bar-wider` 红；mod 块的 mark 写成 del → `TestComputeSplitMarksBlocksPositions` 红；只有右栏的块左栏起点写成 0 → `TestComputeSplitMarksBlocksAddOnly` 红。
    - Go 侧新增 `SplitBlock`（`internal/content`）与 `ContentView.Blocks`（`gui`），既有逻辑未动；`gofmt` 干净（`gui/app.go` 的字段对齐是上一轮遗留，未动）、`go vet` 干净、`go test ./...` 全绿。exe 重建 12,968,960 → **12,981,760** 字节（+12,800，来自 `SplitBlock`/`Blocks`、前端导航与样式）。留证截图 `content-nav.png`（`t_compare_shot.js` 加 `?nav`：标题栏可见 `▲ 1/2 ▼`、第 5~6 行**两栏都被蓝框描住**、行号列加粗的 `~` 与 4px 色条、左右分别是 `int v = 1;` 与 `int v = 2;`）。

44. ✅ **Java↔Go 命名对照审计 + 作者署名/许可证补全** —— 用户："为了维护java端开发者的知识权益，内部名字等标识还得沿用java端的，你先整理出来，有没有改动过"；追问"作者信息这些没有改动吗，出处"；再指示"把代码内容也改一下"。
    - **产出（文档）**：新增 `NAMING_AUDIT.md`（599 → 685 行）。按五类逐项列 Java 原名 → Go 现名：**A 逐字沿用**（i18n 键名 143 个全保留 / 配置 XML 元素名含原版拼写错误 `patchSighHeight` / 图标文件名 21 个 / 语法高亮 CSS 类名 7 种语言 / 枚举值字面量 / `KindNames` / `TreeNodeTypeNames` / 16 个类型名）、**B 大小写风格转换**（常量 `UPPER_SNAKE`→`UpperCamel`、方法 `camelCase`→`PascalCase`）、**C 已改名**（`ZIP`→`ExtZip`、`WINDOWS`→`ExplorerWindows`、`PATCH`→`ParamPatch`、`DIRECTORY`→`iconDirectory`、`NOT_LOAD`→`LoadNotLoaded`、`UTF_8`→`CharsetUTF8`、`MVN_POM_PROPERTIES`→`MvnPomProps`、`ChangeTypeEnum`→`ChangeType`、`TreeNode`/`FileNode`/`CompressNode`/`VirtualNode`/`MappedNode` 5 类压成 `tree.Node`+`Kind`、`getName`→`Name`、`TextDiffUtil.diff`→`DiffLines`、8 个 `*CodeAreaFactory`→`ParserForExt` 一张表）、**D Go 新增**、**E 未移植**（整个 JavaFX 视图/控件层、`fernflower`、`UIUtil`/`FXUtil`/`ImageUtil`、`iconfont.ttf`、`theme-dark.css`）。结论一句话：**跨语言边界的标识一律逐字沿用**（所以 `application.xml` 可 Java/Go 互读、i18n 资源可原样复用、图标可原样拷贝），**只在某一侧内部用的标识按各语言惯例重写**。
    - **产出（代码/元数据，用户选定四项）**：
      - ✅ **`LICENSE`**：从 `src-java/LICENSE` 拷到 `patch-assistant-go/LICENSE`，逐字节一致（`diff` 为空）。Apache-2.0，`Copyright [2025] [CPoet]`。**这是本轮唯一带授权风险的一条** —— 移植版不带原许可证等于缺了原作者的授权。
      - ✅ **`gui/wails.json` 补 `copyright`**：`info` 块加 `"copyright": "Copyright [2025] [CPoet]"`（取值与 LICENSE 首行逐字一致）。**顺带修掉一个既有瑕疵**：`gui/build/windows/info.json` 的模板里有 `"LegalCopyright": "{{.Info.Copyright}}"`，但 `wails.json` 一直没有 `copyright` 字段，于是 exe 版本资源里留下的是**未替换的字面量 `Copyright.........`**（上一版 exe 实测如此）。补上后重建，`LegalCopyright` 正确显示为 `Copyright [2025] [CPoet]`，且 **`CPoet` 重新出现在 exe 里**（UTF-16 命中 1 次）。
      - ✅ **源码补出处注释**：13 个文件（11 个有包注释的包 + `gui/main.go` + `cmd/patchassistant/main.go`）在包注释末尾统一加一段：`本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。`。`gui/main.go` 原本没有包注释，顺带补了一个 `Package main` 的说明。Java 侧 148 个文件里 145 个带 `@author CPoet`，Go 侧此前 **0 个**。
      - ✅ **`go.mod` 模块名加 `cn.cpoet` 前缀**：`module patchassistant` → **`module cn.cpoet.patch.assistant`**（对齐 Java `pom.xml` 的 `<groupId>cn.cpoet.patch.assistant</groupId>`）。33 个 Go 文件、99 处 import 前缀同步改写（精确替换 + 命中次数断言 33/99 + 事后扫描零残留）。注意 `README.md` / `MIGRATION.md` 里的 `cmd/patchassistant` 是**目录路径**，不受影响、未改。
      - ⬜ **未做**：前端「关于」页 + 工具栏「关于」按钮（对齐 Java `AboutView`：图标 + 名称/版本 + 作者 + 邮箱 + 三个链接）。代价最大（新增 HTML/JS/CSS + 走查断言 + 登记），用户本轮未选。连带影响：`AppAuthorName` / `AppAuthorEmail` / `AppAuthorURL` 仍是**死常量**（Go 编译器会消除未被引用的包级常量，所以 exe 里搜不到 `llzero54` / `cpoet.cn`）；i18n 的 7 个 `app.view.about.*` 键**逐字在 properties 里但零引用**。详见 `NAMING_AUDIT.md` §8.4。
    - **审计过程中修正的两处自身错误**（由用户追问逼出来，已写进 `NAMING_AUDIT.md` §1.1 当反面教材）：
      1. **i18n 键数第一版写错**（"各 24 个" → 实际 **Java 143 / Go 144**）。原因：提取正则 `^([\w.]+)\s*=` **在连字符处截断**，把 `app.view.about.author-main-page`、`app.view.config.genera-lang.en-US`、`app.view.config.search-save-history-limit` 这类键**整条漏掉**。修正后的结论更有利：**Java 的 143 个键 Go 全部保留（差集 ∅）**，Go 只多 1 个 `app.view.tree.edit-content`。
      2. `app.view.config.genera-lang` / `genera-theme` 这两个"父键"**在原版 properties 里是有的**（上一版误记为"原版漏配的死键"），Go 侧也逐字保留，只是没有代码去拼 `<code>` 后缀使用。
    - **验证**：`go fmt` 干净（顺带把上一轮遗留的 `gui/app.go` 字段对齐问题修掉了 —— `go fmt ./...` 会写回，这轮执行后 `gofmt -l` 才真正为空）、`go vet ./...` 干净、`go build ./...` 通过、`go test ./...` 全绿（输出里的包路径已变成 `cn.cpoet.patch.assistant/...`）；前端走查 **301/301 全绿**（本轮没动前端，跑一遍确认模块改名未波及）；exe 重建 12,981,760 → **12,986,368** 字节（+4,608，来自版权字符串与模块路径变化），重建后扫 PE 版本资源确认 `LegalCopyright = Copyright [2025] [CPoet]`、`Comments = Java 版 PatchAssistant2J 的 Go 移植`、`ProductVersion = 1.4.7`。
    - **顺带发现（未改）**：Java 的 exe（GluonFX native-image）**根本没有 PE 版本资源**（`VS_VERSION_INFO` 不存在，右键属性→详细信息是空的），反倒是 Go 版这边有；Java exe 里内嵌 `CPoet`×2 / `llzero54`×1 / `cpoet.cn`×2（来自 `AppConst` 常量 + LICENSE 文本）。

45. ✅ **应用名后面补作者署名（`By CPoet`）** —— 用户："把 By CPoet 也新增到打包后页面的名称后面"（附 Java 端标题栏截图「补丁助手 By CPoet」）。
    - **来源**：原版 `PatchAssistantApplication:41` —— `stage.setTitle(I18nUtil.t("app.name", AppConst.APP_NAME) + " By " + AppConst.APP_AUTHOR_NAME)`。原版中文下 `app.name` = `补丁助手`，所以标题是「补丁助手 By CPoet」；英文下是「PatchAssistant2J By CPoet」。
    - **用户选定**：文本用 **`PatchAssistant2J By CPoet`**（不改名称本身、只加后缀）；改 **窗口标题栏 + 页面工具栏品牌名** 两处，**HTML `<title>` 不改**。
    - **改动**：
      - `gui/main.go`：新增 `const windowTitle = constant.AppName + " By " + constant.AppAuthorName`，`options.App` 的 `Title` 由写死的 `"PatchAssistant2J"` 改成 `windowTitle`。**必须走 `constant` 而不是写死字符串** —— 写死的话 `AppAuthorName` 就是死常量，会被 Go 编译器从二进制里消除，作者名只在源码里存在（第 44 项踩过这个坑）。
      - `gui/frontend/dist/index.html`：`<span class="brand">PatchAssistant2J</span>` → `PatchAssistant2J By CPoet`，并加注释说明这行的来源与"窗口标题在 `main.go`、两处要一起改"。
      - 两处都写了交叉引用注释，防止以后只改一处、窗口标题和页面品牌名对不上。
    - **测试**：新增 `gui/main_test.go` 两条 —— `TestWindowTitleCarriesAuthor`（断言**字面量** `"PatchAssistant2J By CPoet"`；用"再拿常量拼一遍"会恒真、等于没测）、`TestWindowTitleBuiltFromConstants`（断言标题里含 `constant.AppAuthorName`，守住"作者名别退化成死常量"）。走查新增 `brand-shows-author` 一条（前端那半边的对应断言）。
    - **走查 301 → 302**：新增分组「工具栏品牌名」1 条。同步改了 `driver.js` 的 `EXPECTED_TOTAL`、`report.py` 的 `GROUPS`、`README.md`（3 处）、本文件 §六的分组统计。
    - **验证**：`gofmt -l` 空、`go vet ./...` 干净、`go test ./...` 全绿（含新增 2 条）、前端走查 **302/302**；`sync_index.py` 重跑（`dist/index.html` 改了，走查页是生成物）；exe 重建 12,986,368 → **12,986,880** 字节（+512），重建后 `PatchAssistant2J By CPoet` 在 exe 里 ASCII 命中 2 次（`windowTitle` 常量 + 内嵌的前端 HTML）。

46. ✅ **支持「双击 .jar 用本工具打开」** —— 用户："现在打开jar包时，不会使用目前的这个工具默认打开，是不是没有安装加载在环境变量中的原因"。
    - **诊断结论：跟环境变量毫无关系。** `PATH` 只决定"命令行敲名字能不能找到 exe"；双击文件走的是**注册表文件关联**（`HKCU\...\FileExts\.jar\UserChoice` → ProgId → `shell\open\command`），两者互不相干。
    - **实机取证（用户机器）**：
      | 注册表项 | 值 |
      |---|---|
      | `.jar` UserChoice（生效） | `Applications\PatchAssistant2J.exe` → `"...\PatchAssistant2J.exe" "%1"`（**项目目录里的 Java 便携版**） |
      | `HKCR\PatchAssistant2J.JAR`（原版 installer 写入，被覆盖） | `"D:\soft\installers\...\PatchAssistant2J.exe" "--target=%1"` |
      | `HKCR\jar_auto_file` | WinRAR |
      - **顺带查出的真相**：UserChoice 那条传的是**裸 `%1`**（用户手动"打开方式"选的，Windows 只会生成 `Applications\<exe名>` 这种 ProgId、给不出 `--target=`），而原版 `AppContext.initArgs` 的正则 `^--([a-zA-Z0-9_-]+)=(.+)$` **只认 `--key=value`** —— 所以当时双击 jar 虽然能启动程序，**那个 jar 根本没被加载**。这就是用户现象的根因。
      - 原版的关联是 **GluonFX installer**（`packageType` 打的 .msi，装在 `D:\soft\installers\`）注册的，源码里没有任何写注册表的代码。
    - **Go 版的两个缺口**（都补了）：
      1. **不注册关联** —— Wails 只产单文件 exe、不写注册表。补两个脚本：`register-jar-association.ps1` / `unregister-jar-association.ps1`，写 `HKCU\Software\Classes\PatchAssistant2J.JAR` + 列进 `.jar\OpenWithProgids`。
      2. **不接启动参数** —— `gui/main.go` 完全不解析 `os.Args`，`constant.AppTarget`/`ParamPatch`/`ParamDev` 三个常量此前是**死常量**（零引用点）。新增 `gui/startup.go`。
    - **改动**：
      - 新增 `gui/startup.go`：`parseStartupArgs`（对齐 `AppContext.initArgs` + `StartUpInfo.init()` 的槽位分派）、`isJarPath`、`isExistingFile`/`isExistingPath`、`(*App).openStartupTargets`。
      - `gui/app.go`：`App` 加 `startArgs` 字段；`NewApp` 里解析 `os.Args[1:]`（跑测试时 `os.Args` 全是 `-test.*` 开关，会被忽略）；`startup` 里调 `openStartupTargets()` —— OnStartup 先于前端脚本执行，所以前端 `init` 的 `GetState` 直接拿到加载完的树。
    - **刻意偏差（登记）**：**Go 版额外认「裸路径」参数**，原版只认 `--key=value`。
      理由：Windows 的"打开方式 → 始终使用"只能生成裸 `%1` 形式的命令（本机那条 UserChoice 就是），不认裸路径的话"手动关联 → 双击"这条路依然不工作。槽位分派仍照原版：`.jar` → 应用包，其余 → 补丁包；**区分大小写**（原版 `String.endsWith`，`APP.JAR` 不算应用包）。
      另：`--dev`（`ParamNameConst.DEV`）**有意不解析** —— 原版只用它决定错误文件落盘位置（`ExceptionUtil:58`），Go 版没有 ExceptionUtil。
    - **Windows 的一个硬限制**：`UserChoice` 带哈希保护，**任何程序都无法静默把自己设成默认打开方式**。脚本只能把 Go exe 加进"打开方式"候选，最后那一下"始终使用"必须用户手动点。这一点写进了脚本的注释与本说明，不要在文档里承诺"自动设为默认"。
    - **🔴 第二轮修复：Windows 双击传进来的路径带引号（原版同款 bug，已修）**
      用户首次复现"双击 jar → 界面起来了但 jar 没加载"。加临时落盘日志（`%TEMP%\pa2j-startup.log`，用完删）抓到 argv 原形：

      ```
      os.Args = [exe, `--target="D:\...\_extract\golden\fixture\app.jar"`]
      parsed  = {appPack: ""  patchPack: `"D:\...\app.jar"`}     ← 落到了补丁包分支
      isExistingPath = false                                    ← 带引号，os.Stat 找不到
      ```

      根因：注册表命令 `"--target=%1"` 里的 `%1` 会被**连引号一起替换**（照抄自原版 installer 的写法），于是 `strings.HasSuffix(p, ".jar")` 判 false（结尾是 `"`），整条链路从第一步就断。
      **原版在这个位置也是坏的**：`AppContext.initArgs` 的正则 `(.+)` 原样捕获引号，`StartUpInfo` 的 `endsWith(".jar")` 与 `FileUtil.getExistsFile`（`new File(path).exists()`）双双不成立。installer 特意注册 `--target=%1` 说明作者**意图**是能打开的，只是实现漏了剥引号 —— 故按意图修，不算刻意偏差。
      修复：`startup.go` 新增 `unquote()`，对 `--target=` / `--patch=` 的值与裸路径三处统一剥成对引号。
    - **测试**：`gui/startup_test.go` 8 条 —— `TestParseStartupArgs`（19 个子用例，新增 4 条引号形态：带引号的 `--target=` / 裸路径 / `--patch=` / **不成对的引号不动它**）、`TestIsJarPathIsCaseSensitive`、`TestUnquote`（6 个子用例，含 `""` → 空、`"half` → 原样）、`TestOpenStartupTargetsOpensArgPacks`、`TestOpenStartupTargetsSkipsMissingPath`、`TestOpenStartupTargetsAcceptsDirectoryPatch`、`TestNewAppIgnoresTestFlags`。
      第三轮后累计 **10 条**：新增 `TestMergeSplitTokens`（6 个子用例）、`TestMergeSplitTokensFindsLongestJoin`、`TestMergeSplitTokensKeepsExistingRun`、`TestParseStartupArgsMergesSplitPath`，`TestUnquote` 增至 7 个子用例（新增双重引号）。
    - **变异验证 4 轮全红**：在原 3 轮基础上追加 —— 把 `unquote` 退化成直接 `return s`（即退回原版行为），`TestUnquote` + 3 条引号子用例同时红。
      第三轮追加 3 轮（累计 **7 轮**）：合并彻底失效（`if false && best > 0`）、去掉存在性判断（`if true`，会误并多个文件）、`unquote` 退回只剥一层 —— 分别打红合并 / 丢弃 / 引号断言。
    - **注册脚本实测**：两个 .ps1 都在本机跑通并读回验证 —— `command` = `"D:\...\PatchAssistant2J.exe" "--target=%1"`（引号与 `%1` 均正确）、`ProgId` 默认值 = `PatchAssistant2J`、`OpenWithProgids` 含 `PatchAssistant2J.JAR`；unregister 跑后 ProgId 消失。第三轮扩展为**同时写两个 ProgId**，原值备份进同目录的 `PatchAssistant2J.exe.assoc-backup.txt`（**UTF-8 无 BOM** —— 路径含中文，`-Encoding ASCII` 会把 `新建文件夹` 写成 `?????`，已踩过）；unregister 跑后 `Applications` 那条**还原成 Java 原值、中文路径完好**。先写过 `.bat` 版，但**本环境的 Bash/PowerShell 工具都不允许调起 `cmd.exe`，脚本无法实测**，故改用可直接验证的 `.ps1`（内容纯 ASCII，无中文路径编码风险）。
    - **验证总览**：`gofmt -l` 空、`go vet ./...` 干净、`go test ./...` 全绿。
      以**与双击完全一致的 argv**（`--target="D:\...\app.jar"`，含引号、中文路径）启动程序：进程稳定存活 15 秒、WebView2 正常、**无 panic**；再跑不带参数的启动作回归，确认不会误加载。exe 重建 12,986,880 → 12,991,488 → **12,994,432** 字节。
      **第四轮端到端**（临时探针落盘、用完删）：对**四种 argv 形态**各启动一次真实 exe —— 注册表命令 `%1` 带引号 / 不带引号（按空格拆散）+ 手动关联的裸 `%1` 带引号 / 不带引号，路径用 `C:\...\pa2j e2e (2)\app.jar`（带空格和括号，模拟本机项目路径）。四种形态解析结果都是 `appPack="...\app.jar"`，且 **`appTree=true`、`progress=[]`** —— 树真的加载出来了。exe 演进 12,986,880 → 12,991,488 → 12,994,432 → 12,994,560 → 12,995,584 → **12,996,096** 字节。
    - **顺带发现**：`--target` 在 exe 里 ASCII **0 次**、`target` 74 次 —— Go 编译器会拆常量字符串（`"--"` 是另一个常量）。**不要用"整串在不在二进制里"判断参数解析逻辑有没有进来**。
    - **🔴 第三轮修复：`%1` 不带引号展开时，路径会被按空格拆进 argv（本机真实路径必中）**
      用户再次复现："右键 jar → 用 PatchAssistant2J 打开 → 页面起来了，但里面没有我右键的 jar"。
      - **注册表侧根因**：Windows「打开方式」按 **exe 文件名**查 `HKCR\Applications\<exe名>` 这个 ProgId，**不看** `OpenWithProgids`。本机 `Applications\PatchAssistant2J.exe\shell\open\command` 指向的是**项目目录里的 Java 便携版**（`"...\patch-assistant-to-java\PatchAssistant2J.exe" "%1"`，现已不在盘上），而 Java 版只认 `--key=value`、不认裸路径 —— 点那个同名条目就是"启动界面但不加载文件"。
        名字冲突的根源是 **Go 与 Java 的 exe 同名**（都是 `PatchAssistant2J.exe`），Windows 的"打开方式"只会用 `Applications\<exe名>` 这个 ProgId。
        修复：`register-jar-association.ps1` 现在**同时写两个键**（`PatchAssistant2J.JAR` 与 `Applications\PatchAssistant2J.exe`），都指向 Go exe + `"--target=%1"`；原值备份进同目录的 `PatchAssistant2J.exe.assoc-backup.txt`（**UTF-8 无 BOM**，路径含中文，用 `-Encoding ASCII` 写过一次、把 `新建文件夹` 变成 `?????`，已踩过），unregister 负责还原/删除。
      - **解析侧根因**：`%1` 替换进来时**不一定带引号**（实测同一条关联两种形态都出现过），于是带空格的路径被按空格切成多个 argv。本机项目路径 `D:\Download\新建文件夹 (2)\...` 必中，而且**第一个碎片 `D:\Download\新建文件夹` 本身是个存在的目录** —— 原有代码把它当 `--target=` 的值收下 → 不是 .jar → 滑进补丁包槽 → **打开了一个错误的文件夹**，用户要的 jar 只剩碎片没人认。
        修复：`parseStartupArgs` 改为两段式（先切 token、再 `mergeSplitTokens` 还原），`mergeSplitTokens` 取**最长的"接回去真实存在"的接法**；接不回去且整段里有片段不存在时**整段丢弃**（连首片段），宁可不打开也不打开错东西；例外是"每一段自己都是存在的路径"（用户确实传了多个文件）。
        之所以看"最长存在接法"而不是逐段累加：目录名自己也可能被拆散（`D:\a` + `b\c` + `d\e.jar` → `D:\a b\c d\e.jar`），中间的 `D:\a b\c` 并不存在，逐段累加第一步就放弃。
        `unquote` 同步改成**循环剥到底**（命令行带引号、shell 又给 `%1` 补一层时会出现 `""...""` 双重包裹；Windows 文件名不允许含 `"`，剥到底不误伤合法路径）。

47. ✅ **对比包时把"补丁签名"从列表里藏起来，但报告里保留** —— 用户反馈："能不能再对比的时候，把这种签名的东西过滤掉，影响操作"。现场截图里"仅 A 有 2 / 仅 B 有 3"全部是 `META-INF/patch-up/<时间戳>.sign`（本工具每次打补丁写的），真正的差异（`BOOT-INF/lib/...jar`）被淹没在签名条目里。
    口径（刻意与原版不同，原版无此功能）：
      - **后端 `ComparePacks` / `jarcompare.go` 不动** —— 注释里写明"不过滤 META-INF/patch-up/ 下的签名文件，这是工具自己写的书志...过滤掉反而是隐瞒"。报告必须诚实。
      - **前端 `renderCompare` 按"界面隐藏、报告保留"过滤**：
        - 隐藏规则：`/^META-INF\/patch-up\/[^/]+\.sign$/i`（只认本工具的产物，jar 自带的 `META-INF/*.SF / *.RSA / *.DSA / *.EC` 不在范围内）。
        - 三组（`r.changed` / `r.removed` / `r.added`）各自按路径过滤一遍；汇总药膏与分组标题的数跟着过滤后的列表走（否则"仅 A 有 3"但列表只 1 行更困惑）。
        - `cmpState.text = r.text || ''` 原样保留，"拷贝报告/保存报告"出去的仍是后端完整文本。
        - 隐藏条目数 > 0 时弹一条小提示 `已隐藏 N 个 META-INF/patch-up/*.sign 签名条目（详见「拷贝报告 / 保存报告」）`，避免用户以为后端算少了。
      - 走查 305/305 绿；变异验证 3 轮全红：
        - `isCompareSignature` 永远返回 `false` → 7 条变红（含 `compare-signature-hidden` / `compare-signature-note-shown` / `compare-row-count` / `compare-summary-pills` / `compare-groups` / `compare-rerun-for-detail` / `compare-still-open-after-preview`）。
        - `if (sigN > 0)` 改成 `if (false)` → 仅 `compare-signature-note-shown` 变红。
        - `cmpState.text = r.text || ''` 改成 `cmpState.text = ''` → 仅 `compare-report-keeps-signatures` 变红。
      - **重建 exe**：`cd gui && wails build`（wails.json 的 `frontend:build` 为空，不重跑 npm，直接嵌入 `frontend/dist/` 现成文件）。产物 `gui/build/bin/PatchAssistant2J.exe` 由 12,996,096 → **12,999,168** 字节（+3,072，来自新增的 `isCompareSignature` 函数 + `.cmp-filtered-note` 样式 + 提示文案）。扫 PE 确认新 exe 含 `cmp-filtered-note`(2) / `已隐藏`(3) / `isCompareSignature`(2) 三处新增字符串，旧构建不含。
        - **注意之前的误判**：早前以为"前端改了 Wails 会自动反映、无需重 build"是错的 —— Wails 在 `wails build` 时才把 `frontend/dist` 通过 `//go:embed all:frontend/dist` 打进二进制，不构建不生效。用户要求"重新打包"后才补上。

48. ✅ **用户再次报"双击 jar 仍不加载、只有拖动能加载"—— 端到端证伪"解析/加载有 bug"，定位到"exe 或关联是旧的"**
    用户把 .jar 默认打开方式改成本程序后，双击仍只开界面、不加载 jar，但拖入能加载。这一条是 §五 第 44~46 项（文件关联 + 启动参数）**之后**的新一次复现，所以要重新证伪"是不是 Go 侧解析/加载又坏了"。
    - **排查手段**：在 `NewApp`（wails.Run 之前，`os.Executable` 同目录落 `pa2j-startup.log`）与 `openStartupTargets`（写盘放在 OpenAppPack 之后，若中途 panic 则只留 NewApp 那次写）两处加**临时落盘诊断**，重建 exe（`cd gui && wails build`）后用"与真实双击一致"的 argv 形态在**本机直接跑 exe**复现。
    - **决定性结果**（用项目路径里那个带空格+括号的 `test jar/app.jar`，按空格拆散的 argv 形态）：
      ```
      os.Args = [exe, "D:/Download/新建文件夹", "(2)/.../probe_frontend/test", "jar/app.jar"]
       appPack = "D:/Download/新建文件夹 (2)/.../test jar/app.jar"   appPackExist = true
       OpenAppPack OK  appPath="...test jar/app.jar"  appTree?=true  matched=false
      ```
      —— 关联命令、参数解析、文件存在性、OpenAppPack 加载树，**四环全绿**。又用临时单测 `GetAppTreeInfo` 验过该测试 jar 可建树（`root non-nil`），排除"jar 是坏产物"。
    - **前端渲染不构成差异**：`init()` 只调一次 `call('GetState').then(apply)`，`backend()` 在前端绑定就绪前轮询（≤10s），`apply(s)` 对 `s.appTree` 是否存在统一渲染（app.js:852）；拖放侧 `HandleFileDrop` 返回的 State 也是走同一个 `apply`。所以"启动加载"与"拖放加载"在渲染层是**同一条路径**，拖动能显示、启动不该不显示。
    - **结论**：**Go 侧启动链路没问题**。用户看到的现象只剩两种真实可能 —— ① 双击启动的是**旧构建**（双击之前那次修复的 exe 没重 build / 仍指向旧路径），或 ② 右键"打开方式"选中的同名条目 `Applications\PatchAssistant2J.exe` 仍指向**没接启动参数的旧 exe / Java 便携版**（第 44 项踩过的同名坑）。两种都不是代码 bug，靠"重 build + 用 `register-jar-association.ps1` 把两个 ProgId 都指到新 exe"解决。
    - **交付**：删掉两处临时诊断、还原 `NewApp` 与 `openStartupTargets` 原貌（并清掉 `strings/fmt/time/path/filepath` 的临时 import，仅 `app.go` 的 `time` 因 `formatTime` 仍保留）；保留 `diag-jar-assoc.ps1`（落 `jar-assoc-diag.txt` 把 `.jar` 的 UserChoice / 两个 ProgId 的 command 全 dump 出来，供用户在本机确认真实关联到底指向谁）。clean 重建 exe 12,999,168 字节，启动实测不再写任何 `pa2j-startup.log`。

49. ✅ **真凶落定：启动链路真正的问题是「前端 init 的 GetState 与 OnStartup 加载竞态」，不是旧构建/关联 —— 推翻第 48 项结论**
    用户提供 exe 同目录真实 `1.jar`（143MB）双击复现，界面仍显示"应用包 未打开 / 把 .jar 拖到这里"—— 证明第 48 项"前端渲染不构成差异、只剩旧构建/旧关联两种可能"的推断**错了**：那次只验了 Go 侧 `OpenAppPack OK appTree?=true`，没验"前端那次 `GetState` 看到没看到树"，把"两条路径都走 `apply`"偷换成了"渲染层无 bug"。
    - **根因**：Wails v2 的 `OnStartup` 与前端 JS 加载并发起步。`app.js:3517` 的 `init()` 只调**一次** `GetState().then(apply)`；`app.go:141` 的 `startup()` 调 `openStartupTargets()` 读 143MB jar 约 0.5s。前端 `init()` 在 `window.go` 注入后立即调 `GetState`，几乎必然早于加载完成 → `a.appTreeInfo` 仍 nil → `state()` 不填 `AppTree` → `apply` 渲染成"未打开"，且 `init` 里 `GetState` 只调一次、之后再不重拉，于是**永久**"未打开"。
    - **第 48 项错在哪**：说"拖放能显示、启动不该不显示"—— 这是把"两条路径都走 `apply`"偷换成了"两条路径时机一致"。拖放是用户**事后**触发（那时树已建好）；启动是树**还没建**就被前端取了一次空状态。
    - **修复（纯后端、前端零改动）**：把"打开启动包"从 `OnStartup` 收口到**第一次 `GetState`**，加 `startupApplied` 布尔防重入。
      - `App` 加字段 `startupApplied bool`；
      - `GetState()` 先 `a.ensureStartup()` 再 `state()`；
      - 新增 `ensureStartup()`：未加载则置位并 `openStartupTargets()`；
      - `startup(ctx)` 改调 `a.ensureStartup()`（保留 `a.ctx = ctx`）。
      这样无论 OnStartup 与前端谁先到，`GetState` 都**同步**等加载完再返回，竞态消除；前端 `app.js` 的 `apply` 逻辑完全不用改（它本来就会渲染非空 `s.appTree`）。
    - **守护测试**：`gui/startup_test.go` 新增 `TestGetStateTriggersStartupLoad`—— 直接 `GetState()`（不手动 `openStartupTargets`）、断言返回 `AppTree` 与 `appTreeInfo` 非空、`appPath` 正确，钉住"是 GetState 自己触发了加载"。
    - **验证**：`go test ./gui/` 全过（6.8s，含新测试）；`cd gui && wails build` 重建 exe 12,999,168 字节（时间戳 2026-10-16 09:35）。**注册表关联路径不变**（`PatchAssistant2J.exe` 原地重建），无需重跑 `register-jar-association.ps1`。
    - **下一步**：用户用新 exe 双击 `1.jar` 复验界面是否显示树。

50. ✅ **第 49 项中段方案有 race，落地改为 `sync.Once` —— 真机 `pa2j-diag.log` 取证 + 清理**
    第 49 项写完后用户双击 `1.jar` 仍"未打开"，证明用 `startupApplied bool` 早置位做"防重入"**不能消除**竞态：两条路并发时，`OnStartup` 先到 `ensureStartup` 把位置 `true` 再开始加载，`GetState` 看到位已 true 直接跳过、返回空树，**`GetState` 与"加载完成"之间仍有空档**。
    - **真机取证**：在 `NewApp` / `GetState` / `openStartupTargets` 三处加临时 `diagLog`（写 `pa2j-diag.log`，exec 同目录），用 `os.Executable` 定位。`wails build` 后用户双击 `1.jar`，日志时间线清晰显示：`openStartupTargets appPack=1.jar` → `GetState enter startupApplied=true appTreeInfoNil=true` → `GetState after appTreeInfoNil=true` → 之后 `openStartupTargets appPack OK appTreeInfoNil=false`。`NewApp` 那行还顺带验证了"`--target=` 已被正确解析成 `appPack`"（排除传参问题）。
    - **修正为 `sync.Once`**：这是消除该竞态的正确语义——"只做一次，且所有竞速的调用方都阻塞到这次做完再返回"。`App.startupApplied bool` 替换为 `startupOnce sync.Once`；`ensureStartup()` 改为 `a.startupOnce.Do(func(){ a.openStartupTargets() })`。`sync` 包原已导入（`decompileMu` 在用）。`refresh()` 不依赖 `a.ctx`（已核），所以 `GetState` 提前到 `OnStartup` 之前触发加载也安全。
    - **验证（用户真机）**：用 `sync.Once` 版 exe 双击 `1.jar` —— 界面正确显示应用包树（META-INF / org / BOOT-INF 等），状态栏"应用包 430 文件 / 19 目录 / 66 class"。"现在合适了"（用户原话）。
    - **清理**：删除三处 `diagLog` 调用、`diagLog` 助手函数、`path/filepath` import（为 `diagLog` 临时加的）；删除 `build/bin/pa2j-diag.log`；`go test ./gui/` 全过（5.9s，含 `TestGetStateTriggersStartupLoad`）；`wails build` clean 重建 exe 12,999,168 字节（2026-10-16 09:59，零诊断残留）。注册表关联路径不变，无需重注册。
    - **教训（写给以后）**："加载收口到 `GetState`"这类需要"等做完才放行"的链路，**用 `sync.Once`，别用 bool 早置位**——后者把"已开始"当成了"已完成"，留给并发方一个空档。要在真机抓这种时序问题，落盘诊断比推理快。

51. ✅ **「对比」支持对比普通文件（不限于 jar/zip 包）**（**新功能扩展**）—— 用户："现在对比页面不能对比文件，只能对比jar包，增加对比文件的功能"。
    第 42 项的「对比」原本只吃 `.jar`/`.zip`（后端按 zip 读、前端按扩展名拦）。用户要的是"两个普通文件也能比"。做成**同一入口按对象类型分流**：
    - **分流口径**：两个都是包（`.jar`/`.zip`，大小写不敏感）走 `ComparePacks`（逐条目比）；两个都是普通文件走 `CompareFiles`（整份内容比 md5）；**一包一文件直接拒绝**（包比"内部条目"、文件比"整份内容"，混着比无从下手）。分流在前端 `compareRun`（`isArchivePath`），后端两条绑定各自独立 —— 与项目一贯的"规则可单测"一致：包/文件的判定在 Go（`isComparePackPath`，被 `HandleCompareDrop` 的"混拖"检查复用），前端 `isArchivePath` 逐字对齐它。
    - **后端**：`internal/service/jarcompare.go` 新增 `CompareFiles(a,b)` + `CompareFileResult`（`kind:"file"` 判别字段；同时给 `ComparePackResult` 加 `kind:"pack"`）—— 与 `ComparePacks` 同样的两道前置检查（空路径 / 同路径拒绝），流式读文件算 md5、目录明确报"不接受目录"。`gui/jarcompare.go` 新增 `CompareFiles`（直通 + 记来源）与 `OpenFileCompare`（读磁盘两个文件，`.class` 反编译、其余按字符集解码，复用 `renderContentView` 出双栏/单栏差异）。
    - **界面**：对比弹窗槽位标签由「包 A / 包 B」改为「A / B」，标题「对比两个包」→「对比文件 / 包」，占位符与提示同步；`PickCompareJar` 过滤器加一项「所有文件」。
    - **文件对比结果**：没有"条目列表"可言，形态比包对比简单 —— 一行汇总（相同/不同）+ 两侧文件名/大小/md5；不同时给一个「查看差异」按钮，点开复用内容预览浮窗（`#content-win`）并排看两个文件（`compareOpenFiles`；`.class` 先出"正在反编译…"）。浮窗里的编码 / 差异模式切换走 `reloadContent` 的 `fromFile` 分支（回 `OpenFileCompare` 取数）。
    - **拖放放宽**：`HandleCompareDrop` 不再按扩展名拦（包与普通文件都收），只挡目录（目录不是"要比对的东西"）与"一次拖两个但不同类"（混拖）。`isComparePackPath` 保留并用于这条混拖检查。
    - **测试**：`internal/service/jarcompare_test.go` 新增 3 条（相同 / 不同 / 拒绝：空路径·同路径·目录·不存在）；`gui/jarcompare_test.go` 新增 `TestAppCompareFiles_Passthrough`、`TestOpenFileCompare_Split`、`TestOpenFileCompare_NoParser`、`TestOpenFileCompare_EmptyPaths`，并把 `TestHandleCompareDrop` 的"非包文件报错"改为"普通文件被接受 + 混拖拒绝 + 目录拒绝"。走查断言数**不变**（305）：`mock-backend.js` 的 `HandleCompareDrop` 替身同步为新规则、原 `compare-drop-rejects-non-pack` 改为 `compare-drop-accepts-non-pack`（拖入 .txt 被接受），`compare-run-guards-missing-pack` 的提示语期望改成"请先选择两个要比对的对象"；`report.py` 的 `GROUPS` 同步该键名。走查 305/305 绿。
    - Go 侧 `go build ./...` / `go test ./...` 全绿；`node --check app.js` 通过；`python sync_index.py` 已重跑（生成物与 dist 只差"路径/标题/挂哪些脚本"三类）。exe 重建 12,999,168 → **13,022,720** 字节（2026-10-16）。

52. ✅ **对比两个包时，可点进内容不同的嵌套 jar 看里面差异**（**新功能扩展**）—— 用户："对比两个包时，出现lib下jar包不同的，能不能也可以查看"（附截图：`BOOT-INF/lib/*.jar` 显示"内容不同"）。
    第 42/51 项的包对比把每个条目当"blob"比（条目级 md5），胖包里 `BOOT-INF/lib/*.jar` 只报"内容不同"，看不出里面到底哪几个 class / 资源变了。用户要的是**能钻进去看**。做成**按需展开**：
    - **口径（为什么不全量递归）**：顶层仍按条目级比，**不预展开**嵌套包 —— 一个 Spring Boot 胖包的 `BOOT-INF/lib` 下常有几十上百个 jar，全量递归展开会读爆内存、也会把界面刷成一片噪声。只在用户**点击**那个"内容不同"的嵌套包时，才按需读 A/B 两侧**同一路径链**的内层字节、再跑一次逐条目比。这是"展开一层、点一层"的 drill-down，不是"一次展开到底"。
    - **后端**：`internal/service/jarcompare.go` 把包对比的核心抽成 `compareEntryMaps(entriesA, entriesB, pathA, pathB, nameA, nameB)` —— `ComparePacks`（磁盘文件）与新增的 `ComparePackBytes(entryPath, a, b []byte)`（内存字节）**共用同一核心**。zip 读取三层抽象：`withPack`（磁盘）/ `withPackBytes`（`bytes.NewReader`）→ `readEntriesFromZip` / `entryFromZip`（共享核心）；`ReadPackEntry` 重构为 `withPack`+`entryFromZip`，新增 `ReadPackEntryBytes(name, data, entryPath)` 供逐层下钻读内层包里的条目。
    - **GUI 绑定**：`gui/jarcompare.go` 新增 `OpenNestedCompare(chain []string)` —— `readChainEntry(outerPath, chain)` 先 `ReadPackEntry` 读第一层、再逐层 `ReadPackEntryBytes` 读到最内层包，取到 A/B 两侧字节后 `service.ComparePackBytes(leaf, ba, bb)`；**单侧缺失（只在一侧存在的嵌套包）明确报错**而不是崩溃。`CompareContentOptions` 加 `Chain []string`（`omitempty`），`OpenCompareContent` 改用 `readContextEntry(pathA/B, opt.Chain, entry)` —— 按 chain 逐层读到最内层包再按名读条目。
    - **导航栈 + 面包屑**：前端 `cmpFrames`（每帧 `{chain, result}`）+ `cmpChain()` 取当前链；点嵌套包 `compareDrill(path)` 压栈（结果缓存、返回不重跑），「← 返回」`compareBack()` 弹栈；`updateCmpBreadcrumb()` 把 chain 渲染成面包屑（HTML `#cmp-crumb` + CSS `.cmp-crumb`/`.cmp-crumb-path`，flex + 省略号）。`renderCompare` 的行 `cmpRow` 按 `isArchivePath(c.path)` 分流：嵌套包点开 `compareDrill`、普通文件点开 `compareOpenFile`；`compareOpenFile` 带 `chain`（顶层不带字段，与后端 `omitempty` 一致，否则走查 `compare-open-sends-path` 会因多出空字段而红），`reloadContent` 的 `fromCmp` 分支仅在 chain 非空时带上。
    - **测试**：`internal/service/jarcompare_test.go` 新增 `TestComparePackBytes`、`TestReadPackEntryBytes`；`gui/jarcompare_test.go` 新增 `nestedPair` 助手、`TestOpenNestedCompare`、`TestOpenNestedCompare_Guards`、`TestOpenCompareContent_NestedChain`。`go test ./internal/service/ ./gui/` 全绿；`node --check app.js` 通过；走查 305/305 仍绿（**drill-down 路径未加走查断言**，靠 Go 单测覆盖 —— 无头走查的后端替身不含真实嵌套 zip 字节，加断言会引入脆弱的替身数据，收益不抵成本）。exe 重建 **13,022,720 → 13,035,008** 字节（2026-10-16）。

53. ✅ **无边框窗口：去掉原生标题栏，窗口按钮搬到工具栏右端**（**界面调整**）—— 用户："把红线画出来的去掉，把右侧三个按钮移动到执行补丁后面"（截图里红线框住的是**原生标题栏**）。
    **背景**：原生标题栏在窗口最上方显示「PatchAssistant2J By CPoet」，而工具栏的 `.brand` 已经有一份（对齐原版 `PatchAssistantApplication:41`），重复；用户要去掉标题栏、并把它的三个窗口按钮搬进工具栏。
    - **做法**：`gui/main.go` 加 `options.App.Frameless: true`。原 `Windows.Options` 注释里"用系统原生边框…省得自己实现拖动/缩放"那句随之作废，改写为"不透明 + 阴影圆角由 Wails 默认提供 + 边缘缩放由 Wails 的 enableResize 负责"。
    - **拖动 / 缩放的分工**（写清楚，免得以后误以为缩放也要自己实现）：
      - **拖动**：工具栏挂 CSS `--wails-draggable: drag`（Wails 运行时自己监听 mousedown/mousemove）；`.btn` / `.brand` / `.win-ctls` 写回 `no-drag` —— 否则"按住想点按钮"稍微一动就变成拖窗口。
      - **双击最大化**：Wails 不管，前端 `initWindowControls` 在 `.toolbar` 上补一条 dblclick（落在 button 上不算）。
      - **缩放不用自己写**：无边框模式下 Wails 保留 `WS_THICKFRAME`，并在 `navigationCompleted` 里 `window.wails.flags.enableResize = true`，前端运行时按边缘命中测试发 `resize:` 消息（wails v2 `internal/frontend/desktop/windows/frontend.go`）。**Aero 阴影 / Win11 圆角默认保留**（未设 `DisableFramelessWindowDecorations`）。
    - **窗口按钮**：`index.html` 工具栏 `#btn-run` 之后加 `.win-ctls`（最小化 / 最大化 / 关闭，顺序对齐 Windows），图标用**内联 SVG**（不再往 `icons/` 加位图）；`app.js` 的 `initWindowControls` 走 `window.runtime.WindowMinimise / WindowToggleMaximise / Quit`，最大化按钮的图标跟 `WindowIsMaximised()` 同步（`syncWinMaxIcon`，最大化时换成"还原"图标）。样式在 app.css 的 `.win-ctl`（关闭键悬停变红）。
    - **判空（本项目的老坑）**：走查里 `window.runtime` 是替身、**没有**这三个方法，所以逐个判空（`if (rt.X) rt.X()`）—— `init()` 里一处抛 undefined 会把后面所有初始化一起带崩，连累无关断言变红。
    - **测试**：走查新增 2 条（`win-ctls-after-run`：三个按钮在位且紧跟 `#btn-run`；`toolbar-draggable-css`：`.toolbar` 的 `--wails-draggable` = `drag`），断言总数 **305 → 307**；`driver.js` 的 `EXPECTED_TOTAL`、`report.py` 的 `GROUPS`（新增分组「窗口按钮（无边框）2」）、README、本文件 §六 四处同步。走查 307/307 绿。
      - 顺带修正 README 那一行**原本就存在的陈旧**：漏了「工具栏品牌名 1」、「包内容对比」还停在 54（合计 301 而实际 305），现按 `report.py` 实算统一改成 307。
    - 真正的拖动 / 最小化 / 关闭 / 缩放行为依赖 Wails 运行时，无头走查测不到（替身没有），只能靠 `go build` + 真机验证。

54. ✅ **工具栏品牌标换成绿色应用图标**（**界面调整**）—— 用户："这个图标换成绿色的那个"（截图红线框住工具栏最左边那个方块）。
    原来 `.brand::before` 是个 `--accent` 蓝方块（纯 CSS 色块，15px 圆角方）。用户要的"绿色的那个"是**应用图标**（`gui/build/appicon.png`，绿色菱形 logo —— 也是 exe / 任务栏图标），并且明确选了"**直接用绿色应用图标**"而不是"只把方块换成绿色"。
    - **做法**：把 `appicon.png` **按 alpha 边界裁边**再缩到 128²，存成 `gui/frontend/dist/brand.png`（原图 1024² 里 logo 只占中间 781²，不裁的话 16px 下菱形只有约 8px，太小）；`.brand::before` 改成 `background: url('brand.png') center / contain no-repeat`，去掉原来的蓝色 `box-shadow`，尺寸 15px → **16px**（菱形本身比同尺寸实心方块"轻"，+1px 补回视觉重量）。
    - **放 dist 根目录、不放 `icons/`**：`icons/` 是"原样搬运自原版 Java 资源"的专用目录，`gui/icon_test.go` 有两条守门测试（`icons` 目录内容与常量表一一对应、且与原版 `src-java/.../icons/` 逐字节一致）—— 第一次放进 `icons/` 就把它撞红了（`原版没有 icons/brand.png`）。这是移植版新增的资产、原版没有，所以放 dist 根。
    - **资源路径**：走查页的 `app.css` 也是从 dist 目录加载的（`sync_index.py` 当时只改写 `<link href="app.css">` 与 `src="icons/` —— 后者已在第 58 项移除，因为搜索图标改成了内联 SVG、`dist/index.html` 里不再有 `src="icons/`），CSS 里的 `url('brand.png')` 会自然解析到 dist 根，**不用改 `sync_index.py`**。
    - **两处副本要一起换**：`gui/build/appicon.png`（Wails 用，进 exe / 任务栏）与 `frontend/dist/brand.png`（界面用）。已在 app.css 注释里写明这层关系。
    - 走查断言**不变**（307）：`brand-shows-author` 只看 `.brand` 的文字，与图标无关（该断言名后来在第 58 项改成了 `brand-icon-only`，因为 `.brand` 的文字已被去掉）。`wails build` 重建 exe **13,040,640 → 13,047,808** 字节（2026-10-16）。

55. ✅ **修「双击 `.class` 反编译失败」的真机报错 —— 反编译器要 Java 11，不是 8**（**bug 修复 + 诊断改进**）
    用户在其他电脑上打开 jar、双击 `.class`，界面只弹一句：
    `反编译失败：Error: A JNI error has occurred, please check your installation and try again`。
    - **根因**：内嵌的 `fernflower-1.3.0.jar` 里 **225 个 class 全是 major 55（Java 11）**（实测 zip 头）。那台机器上是 Java 8，JVM 在**加载主类、进 `main` 之前**就退出 —— `A JNI error has occurred` 正是这个场景的固定开场白。真正的原因在 stderr **第二行**：`java.lang.UnsupportedClassVersionError: … (class file version 55.0), this version of the Java Runtime only recognizes class file versions up to 52.0`。
    - **为什么一直没暴露**：① `Decompile` 出错时只取 stderr 的**第一行**（`firstLine`），恰好把唯一有用的那行丢掉；② `ErrNoJava` 与 README 里的版本要求写的是"**JRE 8 或更高**"——**是错的**，而开发机上是 JDK 21，永远撞不到。
    - **修复**：`internal/decompile/decompile.go` 新增 `isJavaVersionError` / `versionErrorDetail` / `decompileFailure` —— 识别 `UnsupportedClassVersionError` 与 `A JNI error has occurred`，把 stderr 里那条 `UnsupportedClassVersionError` 捞出来，翻成
      "反编译失败：本机 Java 版本过低 —— 反编译器需要 Java 11 或更高。当前使用的 java：<路径>。请安装 Java 11+ 并让它排在 PATH 前面，或把 JAVA_HOME 指向 Java 11+ 的目录后重启本程序。（JVM 原话：…）"。
      `.toast` 是 `white-space: pre-wrap`，多行能正常显示；其它失败照旧只取第一行（不把整段堆栈倒给用户）。
    - **文案修正**：`ErrNoJava`、README（功能表 / 快速开始 / 使用说明三处）、MIGRATION §2.1 里的"JRE 8 / 需要 JRE"统一改成"**Java 11 或更高**"。
    - **测试**：`internal/decompile/decompile_test.go` 新增 `TestDecompileFailure_JavaTooOld`（钉住提示里必须出现"Java 11 或更高"/实际 java 路径/`UnsupportedClassVersionError`/本机版本上限/`JAVA_HOME`）、`_JNIWithoutDetail`、`_Generic`（普通失败仍只留第一行）。把 `decompileFailure` 拆成纯函数就是为了这几条能单测 —— 否则只能靠真机再撞一次。
    - **顺带踩到一条约定**：新加的 `brand.png` 一开始放进 `frontend/dist/icons/`，把 `gui/icon_test.go` 的 `TestIconMatchesOriginalPNG` 撞红了（"原版没有 icons/brand.png"）—— `icons/` 是"原样搬运自原版 Java 资源"的专用目录，移植版新增的资产要放 **dist 根目录**。已改成 `frontend/dist/brand.png` + CSS `url('brand.png')`（4x 截图确认渲染与移动前逐字节一致）。
    - `go vet` / `go test ./...` 全绿；走查 307/307 绿；`wails build` 重建 exe **13,047,808 → 13,052,416** 字节（2026-10-16）。
    - **刻意没做（可选后续）**：自动去找机器上更新的 JDK（注册表 / 常见安装目录），或随程序带一个 JRE。前者能救"装了 JDK 17 但 PATH 上排的是 8"的机器，后者要把体积 +40MB 左右。都先不动，等确认那台机器的实际情况。

56. ✅ **修「编译为 .class」在 JDK 8 上完全不可用 —— 两个各自独立的 bug**（**bug 修复**）
    用户："能否直接使用运行本机的jdk版本号，不用规定的版本号来编译"（前因：另一台机器只有 JDK 8）。
    排查时发现本机就装着 JDK 8（`D:\soft\runtime\JDK\jdk1.8.0_251`），于是直接实测，撞出**两个都足以让 JDK 8 编不了**的问题：
    - **① `--release` 是 JDK 9 才有的选项。** `Compile` 无条件拼 `--release N`，而 `--release` 由 **JEP 247 在 JDK 9 引入**；JDK 8 的 javac 直接报 `javac: 无效的标记: --release`（实测 `jdk1.8.0_251`），编译根本起不来。这与 `ErrNoJavac` 文案写的"JDK 8 或更高"**直接矛盾** —— 文案承诺了 8，代码在 8 上必然失败。
    - **② `JavacMajor()` 把 JDK 8 读成 1。** JDK 8 的 `javac -version` 输出是老式编号 `javac 1.8.0_251`，而解析代码取**第一段**（`1`）。后果比 ① 更难看：`Compile` 里"能不能编出原 class 版本"那道守卫判成 `8 > 1`，于是**拒绝编译 Java 8 的类**，还弹出一句"原 class 是 Java 8，但本机 JDK 只有 **1**"的胡话。
    - **修法（"自适应"，用户从三个方案里选的）**：新增纯函数 `versionArgs(release, javacMajor)` —— **javac ≥ 9 才传 `--release N`**；JDK 8 时**什么都不传**。为什么"不传"就是对的：JDK 8 只能产出 class 8，而 `ReleaseFor` 的下限是 8、上面那道守卫又保证了 `release ≤ javacMajor`，所以走到这里时 release 必然是 8 —— 正好等于 JDK 8 的默认产出。（不用 `-source`/`-target` 顶替：它们只对"用了新语法"报警告，**拦不住引用新版本的 API**，语义比 `--release` 弱。）
      同时把版本解析拆成 `parseJavacMajor`，**认两种编号**：`21.0.9 → 21`、`1.8.0_251 → 8`（主版本在第二段）。
    - **刻意没做（用户明确否掉的方向）**：**不**改成"一律不传版本参数、直接用本机默认版本"。那样在 JDK 21 上编一个原本给 Java 8 用的类会产出 class 65，装回 jar 后在 Java 8 运行时加载直接 `UnsupportedClassVersionError` —— 等于把 jar 弄坏。同理**保留**"原 class 版本高于本机 JDK 就提前拒绝"的守卫（降级编虽然运行时安全，但源码用了高版本 API 时 javac 会失败，宁可不做）。
    - **测试**：`internal/javac` 新增 `TestVersionArgs`（6 组，含 `{8, 8} → 不传`）、`TestParseJavacMajor`（10 组，含真机输出 `javac 1.8.0_251 → 8`）、`TestCompileWithoutReleaseUsesJavacDefault`（钉住 `release=0` 时产出 `javacMajor + 44`，与 `TestCompileHonorsRelease` 成对）。
    - **验证做到了"实测"而不是"推理"**：本机就有 JDK 8，所以直接把 `JAVA_HOME` 指向 `jdk1.8.0_251` 跑 `go test ./internal/javac/` —— 修复后**全绿**。再做**变异验证**：两处修复分别改回旧写法，各自精确变红（① `javac: 无效的标记: --release`；② `parseJavacMajor("javac 1.8.0_251") = 1` 与"本机 javac 1 的默认"），还原后 `diff -q` 确认内容一致。
    - **顺带发现（未修，属既有现象）**：JDK 8 环境下 `go test ./gui/` 有 **2 条既有失败**（`TestOpenEditorClassUsesSourceMode`、`TestCompileAbilityFlags`，都是 `模式 = "hex"，期望 "source"`）+ 1 条跳过（`TestCompileContentRoundTrip`）。原因是**反编译**要 Java 11（见第 55 项），而 `decompile.Available()` 只检查"java 在不在"、不检查版本，测试于是通过了前置判断、却在实际反编译时降级到十六进制。与本次改动无关（`gui/editor.go:160-167` 的源码模式只在 `decompileClass` 成功时才进入，完全不碰 `javac`）。
      **实际含义**：只装 JDK 8 的机器上反编译用不了 → 拿不到源码 → 这次修好的"编译"在那台机器上仍然走不到。真要用还是得装 Java 11+；本次修复真正救的是"`java` 是 11+、`javac` 却是 8"这类混装机器，以及把那条会吓人的错误信息纠正过来。

57. ✅ **内置 Java 运行时 —— 不再要求用户机器装 Java**（**功能补齐**，2026-10-16）
    用户："我觉得能否打成动态库，这样更方便一点"（前因：另一台机器只装 JDK 8，双击 `.class` 打不开）。
    - **先否掉字面理解**：JVM 不是单个 DLL。实测 JDK 21 里有 **88 个 DLL**，jlink 出来的 44MB 运行时也还是 **55 个 DLL + jimage 模块文件**的目录结构。"把 JRE 压成一个动态库"这条路不存在。
    - **查了原版怎么做的（关键证据）**：原版 `pom.xml` 依赖 `org.jetbrains:intellij-fernflower`，`fernflower/` 包下 5 个类直接 `new Fernflower(...)` + `engine.decompileContext()`（**当库用**，不是起子进程），打包用 **GluonFX native-image**，其 `META-INF/substrate/config/reflectionconfig.json` **完全没给 fernflower 做反射配置** —— 说明原版**自带 JVM**，反编译从不依赖用户机器上的 Java。Go 版移植时改成"外挂本机 JRE"，这是**功能退化**（§2.1 记着），本次补齐。
    - **顺带实测并否决了 GraalVM native-image 路线**（把 fernflower 编成原生 exe、彻底脱离 JVM）：GraalVM 21 + VS 2026 编译成功，产物 **24.58MB**、编译 49s、启动 757ms（java 版 847ms，只快 90ms）。**否决理由三条**：① **覆盖不了「编译为 .class」**（javac 无法 native-image，决定性）；② **输出与原版有边缘差异** —— JDK 21 与 GraalVM 的 java 输出完全一致（说明 fernflower 对 JVM 不敏感），但 native 稳定地不同：`Arrays.asList("A","B","A")` 变成 `Arrays.asList(new String[]{"A","B","A"})`（varargs 写法，语义等价），14 个真实业务 class 0 差异但差异真实存在；③ **非 ASCII 路径失败**（SubstrateVM 用 ANSI 命令行 API，连 Python 宽字符调用也失败）。构建链还要 GraalVM + MSVC。
    - **落地：内置 jlink 运行时**（`internal/javaruntime`）。
      - **模块选 `java.base,jdk.compiler`，不是纯 JRE** —— **JRE 里没有 javac**，只内置 JRE 的话「编译为 .class」仍然要用户装 JDK。带上 jdk.compiler 后 jlink 会自动生成 `bin/javac.exe`，两件事一起解决。实测 **44MB 目录 / 108 文件 / zip 后 27.49MB**。
      - **用 Microsoft Build of OpenJDK 21，不用本机的 Oracle JDK** —— 后者的 NFTC 许可只明确允许再分发"**未修改的** Program"，而 jlink 产物是裁剪派生的、条款模糊；这份运行时是要拷到别人电脑上的，不该赌。Microsoft 构建是 GPLv2+CE，可自由再分发。
      - 内嵌 zip（`go:embed`），首次使用解压到 `%LOCALAPPDATA%/PatchAssistant2J/runtime-<版本>/`。解压目录名**带版本号**，并写一个内容为 zip sha256 的 `.ready` 标记 —— 换了运行时（升级 JDK、改模块列表）标记就对不上，自动重解压，不会用着半新半旧的目录。解压走"临时目录 → 改名"，中途失败不留残缺目录。**实测首次解压 0.42 秒**。
      - **查找顺序改为「内置 → `JAVA_HOME` → `PATH`」**（`decompile.FindJava` 与 `javac.FindJavac` 必须一致，否则会出现"反编译用 JDK 8、编译用 JDK 21"的版本错配）。后两级是逃生口：内置解压失败时仍能靠本机 Java 干活。
      - **逃生口开关 `PATCH_ASSISTANT_NO_BUNDLED_JAVA`**：非空则跳过内置运行时。给"想用自己装的特定 JDK"的用户，也用来在单测里验证外部 JDK 分支。
      - 非 Windows 平台不内嵌（`runtime_other.go`），保持跨平台可编译。
    - **测试**：`internal/javaruntime` 6 条（解压产物齐全 / 路径稳定 / 版本目录名 / zip slip 防护 / 逃生口）；`internal/decompile` 与 `internal/javac` 各两条 —— **`TestFindJavaWorksWithoutAnyJavaOnMachine`**（清空 `JAVA_HOME` 与 `PATH` 仍拿到内置 java）、**`TestDecompileWorksWithoutAnyJavaOnMachine`**（清空后**真的反编译出源码**），以及两条逃生口回退测试。
    - `go vet` / `go test ./...` 全绿；走查 **307/307**（前端未改）；`wails build` 重建 exe **12.45MB → 39.95MB**（2026-10-16）。
    - **顺带**：第 56 项末尾那条"纯 JDK 8 机器上反编译用不了、于是编译也走不到"到此**不再成立** —— 内置运行时是 JDK 21，两条链路都不再看机器上的 Java 了。

58. ✅ **工具栏去掉品牌文字 + 放大镜换成细线条图标**（**界面调整**）—— 用户发了两张截图："先把这个图标后面的 `PatchAssistant2J By CPoet` 这个去掉不要名称了。并且把放大镜换成这样的图标"（第二张是要的样式：一根细线条的放大镜）。
    - **① 去掉品牌文字**：`gui/frontend/dist/index.html` 里 `<span class="brand">PatchAssistant2J By CPoet</span>` → **`<span class="brand"></span>`**（元素与 `.brand::before` 的 `brand.png` 图标都保留，只清空文字）。理由与第 54 项连起来看：工具栏已经用**应用图标本身**做品牌标，再跟一串应用名 + 署名就是重复；用户要去掉。
      - **应用名 + 署名没有消失** —— 仍在**窗口标题栏**（`gui/main.go` 的 `windowTitle` 常量 = `constant.AppName + " By " + constant.AppAuthorName`），任务栏 / Alt-Tab 看得到。这正是第 53 项"去掉原生标题栏"的补偿点：界面里不再重复，但窗口的真实标题仍在。
      - **`windowTitle` 那两处常量一个都不能动** —— 它必须拿 `constant.AppAuthorName` 拼，写死字符串会让那个常量变成未引用常量、被 Go 编译器消除，exe 里就搜不到作者名。守门测试照旧：`gui/main_test.go` 的 `TestWindowTitleCarriesAuthor` / `TestWindowTitleBuiltFromConstants`。
    - **② 放大镜换成细线条图标**：`#btn-search` 的 `<img src="icons/search.png" alt="搜索">` 换成**内联 SVG**（`viewBox="0 0 16 16"`、`fill="none"`、`stroke="currentColor"`、`stroke-width="1.5"`、一个 `<circle>` + 一条斜线 `<path>`）。原版那张 `icons/search.png` 是带十字准星的粗体放大镜，用户要的是细线条的。
      - **用内联 SVG 而不是再放一张 PNG**：① `stroke="currentColor"` 能跟着 `.btn-icon` 的 hover / disabled 一起变色；② `dist/icons/` 是"原样搬运自原版 Java 资源"的专用目录，`gui/icon_test.go` 两条守门要求它与常量表一一对应、且与原版逐字节一致 —— 不能往里塞新图（新资产要放 dist 根目录，见第 54 项）。
      - **`icons/search.png` 本身仍保留**（`gui/icon.go` 的 `iconSearch` 常量 + 常量表守门要求目录里必须有它），只是界面不再引用。
      - 按钮补了 `aria-label="搜索"`：原来那个可访问名来自 `<img alt="搜索">`，换成 `aria-hidden` 的 SVG 后要自己给。
      - `app.css` 的 `.btn-icon img` 规则补上 `svg`（`.btn-icon img, .btn-icon svg { display:block; width:16px; height:16px }`）—— 否则 SVG 会按 `viewBox` 撑开、跟原来那张 16×16 的 PNG 不等高。
    - **`sync_index.py` 的死规则**：它原本有一条 `src="icons/` → `src="<dist>/icons/` 的资源路径改写。搜索图标换成内联 SVG 后，`dist/index.html` 里已经**没有任何** `src="icons/` 引用（窗口按钮三个也是内联 SVG），规则无从匹配、直接让脚本报错退出。处理方式不是"删掉了事"——那会留坑：下次有人再塞一张 `<img src="icons/...">`，走查页会 404 且没人发现。改为**反向守门** `FORBIDDEN`：源文件里一旦又出现 `src="icons/` 就报错退出，要求要么恢复改写规则、要么把该图标也改成内联 SVG。
    - **测试**：走查断言**总数不变（307）**，改的是两条既有断言 ——
      - `brand-shows-author` → **`brand-icon-only`**：从"`.brand` 的文字等于 `PatchAssistant2J By CPoet`"改成"文字为空 **且** `::before` 的 `background-image` 仍是 `brand.png`"（只断言文字为空的话，把整个 `::before` 删掉也能过）。
      - `search-btn-present`：从"有 `img`"改成"**恰好一个子元素、且那个是 `svg`、且没有 `img`**"。语义仍是原版 `HomeView.buildHeader` 的"只有图标、无文字"。
      - `report.py` 的 `GROUPS`：分组名「工具栏品牌名」→「**工具栏品牌标**」、键名同步；README 的分组统计行、本文件 §六的分组统计一并同步。`driver.js` 的 `EXPECTED_TOTAL` 不变（307）。
    - **同步改的文档**：README「应用名后面带作者署名」一段（改写成"工具栏只有图标、署名只在窗口标题栏"）、README 分组统计行；本文件 §四「工具栏按钮图标」行、第 54 项里提到 `brand-shows-author` / `sync_index.py` 改写规则的句子。
    - `go vet` / `go test ./...` 全绿；走查 **307/307** 绿（`brand-icon-only` 与 `search-btn-present` 两条确认通过）；`wails build` 重建 exe **41,895,424 → 41,895,936** 字节（+512，2026-10-16）。

59. ✅ **修「打开包里的 .xlsx / .pdf 时弹 cmd 黑框」**（**bug 修复**）—— 用户发截图："我打开包里的这种文件时，会有 cmd 窗口弹出来"（截图是包内的 `.xlsx` 列表）。
    - **根因**：`gui/content.go` 的 `openPathWithOS` 在 Windows 上走 `exec.Command("cmd", "/c", "start", "", path)` 打开无解析器的文件，**一个创建标志都没设**。本程序是 GUI（Wails）、自己没有控制台，于是系统为 `cmd.exe` 新分配一个控制台 —— 那个控制台窗口就是用户看到的黑框。原版 Java 走 `HostServices.showDocument` / `Desktop.open()`，由 JVM 交给 ShellExecute，从来不会弹控制台 —— 所以这是移植引入的缺陷，不是刻意偏差。
    - **为什么不是 `SysProcAttr.HideWindow`**（已实测，别再改回去）：Go 的 `HideWindow` 映射到 `STARTUPINFO` 的 `STARTF_USESHOWWINDOW` + `SW_HIDE`，**控制台照样会分配**，只是窗口建出来时是隐藏的。实测办法是让被 spawn 的控制台子进程自报 `GetConsoleWindow()` 与 `IsWindowVisible`（内省，不受桌面可见性影响）：

      | `SysProcAttr` | 子进程控制台 | 控制台窗口 |
      |---|---|---|
      | 不设（= 修复前） | HAS-CONSOLE | **visible=true** ← 黑框 |
      | `HideWindow: true` | HAS-CONSOLE | visible=false |
      | `CreationFlags: CREATE_NO_WINDOW` | **NO-CONSOLE** | — |

      黑框的成因是"控制台被分配了"这件事本身，所以必须用 `CREATE_NO_WINDOW`（`0x08000000`）把控制台整个去掉。
    - **修复**：新增 `gui/hidewindow_windows.go` / `gui/hidewindow_other.go`（照 `internal/decompile` 的同名文件形状），Windows 侧 `hideWindow` 设 `SysProcAttr{CreationFlags: createNoWindow}`；`openPathWithOS` 在 `cmd.Start()` 之前调用它。顺带把命令构造拆成 `openCommandFor`，好让"用的是 `cmd /c start "" <路径>`"也能被断言。
    - **`internal/decompile` 与 `internal/javac` 里的同名函数仍是 `HideWindow: true`**（MIGRATION §2.1 第 3 点也这么记着）。按上表它**确实能藏住窗口**（visible=false），所以反编译 / 编译那边没被投诉过黑框 —— 但控制台还是被白分配了一份。要不要一并换成 `CREATE_NO_WINDOW` 是另一件事，**本轮没动**，等用户确认。
    - **测试**：`gui/openexternal_test.go` 新增两条 —— `TestOpenCommandForWindowsUsesStartWithTitlePlaceholder`（钉住 `cmd /c start "" <路径>` 那个空标题占位，少了它带引号的路径会被 `start` 当成标题、文件打不开）、`TestHideWindowRequestsNoConsole`（钉住 `CreationFlags` 必须含 `CREATE_NO_WINDOW` 位；测试里自己定义这个常量，不能引用 `hidewindow_windows.go` 的 —— 那个文件带 `//go:build windows`，非 Windows 平台编译不到会把测试一起编译失败）。
    - `go vet` / `gofmt` 干净；`go test ./...` 全绿；`wails build` 重建 exe **41,895,936 → 41,896,448** 字节（+512，2026-10-16）。

60. ✅ **修「两个文件点查看差异，进去是空白」**（**bug 修复**）—— 用户发两张截图："点击对比之后，点击查看差异，进去是空白的"（截图里两个 `.yaml`，浮窗标题 `application.yaml ↔ application.yaml`、导航计数 `0/24`，两栏全空）。
    - **根因**：`gui/frontend/dist/app.js` 的 `compareOpenFiles` 拿到后端返回的 `ContentView` 后写了 `v.left = left; v.right = right;`（left/right 是**两个文件的磁盘路径字符串**）。而 `ContentView` 序列化出来的两栏对象的 json 键**恰好也叫 `left` / `right`**（值是 `{path, lines}`）—— 于是两栏对象被这两个字符串整个盖掉。`makePane` 里 `pane.lines` 变成 undefined，`linesHtml` 返回空串，两栏空白。
    - **为什么症状是"标题在、计数在、就是没内容"**：被盖掉的只有 `left` / `right`，`title` 与 `blocks` 没被动 —— 所以标题照常显示、差异导航照常算出「0/24」，只有两栏是空的。这个"半截正常"的样子很容易让人以为后端没返回内容，其实后端好得很。
    - **后端侧已实测无罪**：用用户本机那两个真实 `.yaml` 跑 `OpenFileCompare`，返回 `Mode=split`、`Blocks=24`（与截图里的计数一致）、左栏 253 行、右栏 176 行、`yaml-key` / `yaml-comment` / `yaml-number` 着色段齐全。**问题 100% 在前端。**
    - **为什么会漏到用户手上**：走查（`_extract/tools/probe_frontend/`）此前**只覆盖了 `ComparePacks` / `OpenCompareContent` 那条链**（两个包），`mock-backend.js` 里根本没有 `CompareFiles` 和 `OpenFileCompare` 两个替身 —— 文件对比的「查看差异」一次都没被测过。这与 §五 51「支持两类对象」登记的能力不匹配：能力加了，走查没跟上。
    - **修复**：把前端自用的两个字段名从 `left` / `right` 改成 **`fileLeft` / `fileRight`**（`compareOpenFiles` 与 `reloadContent` 的 `fromFile` 分支共三处：加载态、成功态、切编码重取）。不动后端字段名 —— 那会牵动 `OpenCompareContent` 那条已经稳定的链。两处都写了注释说明"为什么不能叫 left/right"。
    - **测试（两条腿）**：
      - **走查补 7 条**（`compare-files-run-routes` / `-result-shows-diff-btn` / `-open-sends-paths` / `-open-both-panes-have-lines` / `-open-nav-count` / `-charset-reload-keeps-panes` / `-close-returns-to-list`），断言总数 **307 → 314**。关键的那条**数两侧的 `.cline` 行数**（各 60）—— 只断言"浮窗打开了"是不够的：空白时浮窗照样打得开、标题和差异计数也照样在，只有去数行才能把"空白"和"有内容"区分开。
      - **变异验证**：把修复改回 `v.left = left` 重跑，`compare-files-open-both-panes-have-lines` 与 `-charset-reload-keeps-panes` **同时变红**，失败信息正是"左栏行数=0 右栏行数=0"—— 证明这两条断言真能抓住这个 bug，不是摆设。还原后 314/314 复绿。
      - `mock-backend.js` 补齐 `CompareFiles` 与 `OpenFileCompare` 两个替身，行数据刻意做长（60 行）且两处差异隔得远（第 4~5、50~51 行）—— 两行的假数据滚不动，导航类断言就只能瞎猜（与 `cmpSplitSide` 同一个理由）。
    - 顺带清掉一个临时诊断文件 `gui/zz_diag_test.go`（用用户那两个真实文件复现用的，结论已写进本条）。
    - `go vet` / `gofmt` 干净；`go test ./...` 全绿；走查 **314/314** 绿（含变异验证）；`wails build` 重建 exe **41,896,448 → 41,896,960** 字节（+512，2026-10-19）。



---

## 六、验证手段

移植最容易出的问题不是"编译不过"，而是"编译过了但行为不一样"。
因此采用的验证方式：

1. **跨语言黄金基准**：用原版 Java 跑出结果，固化为测试基准，
   再用 Go 跑同一输入比对。
   - README 解析：`internal/service/testdata/readme_golden.tsv`
   - 输出包（`patch` / `patchreadme` 两条路径）：`../_extract/golden/`
     一键复跑 `bash run.sh`。夹具、Java 驱动、比对脚本都在这个目录里，
     判读口径与差异清单见 §3.10。
   - 文本差异（Myers 编辑脚本的选路顺序）：`internal/util/textdiff_golden_test.go`，
     132 条语料（含 120 条从极小字母表生成、必然大量重复行的随机用例）逐字节一致。
   - 语法高亮：`internal/content/highlight_golden_test.go`，204 条语料。
     比对用"样式类 + 片段文本"而不是长度，避开 Java UTF-16 码元 vs Go 字节的差异。
   - 差异行号：`internal/content/linenum_golden_test.go`，132 条。
   - `.class` 反编译：`internal/decompile/decompile_test.go`，6 个样本（中文常量、
     枚举、接口、泛型抽象类、内部类、41 KB 大类），基准由**原版 API** 产出。
     另有 `_extract/tools/decomp_consistency.sh` 专门验证"命令行方式与原版 API
     逐字节一致"（40 + 12 个单类样本、内部类、中文，0 差异）—— 这条是敢用
     方案 A（外挂 JRE）的依据。
2. **变异验证**：写完测试后，故意把实现改回"直觉写法"，确认测试会变红。
   - 已对 README 前缀规则做过，测试确实抓住了行为分叉。
   - 已对黄金基准的比对脚本做过：故意改内层 jar 里一个 class、改顶层一个 class、
     只调换两个条目顺序 —— 三种变异全部报 FAIL，证明比对脚本不瞎。
3. **第三方工具交叉核对**：建树结果与 Python `zipfile` 逐项对比。
4. **JRE 实跑**：本机有 JDK 21，原版正则/逻辑可直接跑，不必靠推理。
5. **前端走查（无头浏览器）**：`../_extract/tools/probe_frontend/`，`bash run.sh`
   一键复跑。做法是注入一个后端替身顶掉 `window.go`，在真实 Chrome 里加载
   **真实的**    `app.js` / `app.css`，模拟点击 / 双击 / 右键 / Esc / 滚动，
   把 **314** 条断言输出成 `PROBE|<名字>|PASS/FAIL|<细节>`。分组统计：工具栏品牌标 1、
   窗口按钮（无边框）2、树渲染与手势 15、
   执行打补丁 1、文件详情 5、文件类型图标 10、内容预览 54、预览右键菜单 9、双栏滚动同步 5、
   对比视图外观 7、在另一棵树里定位 10、class 反编译 5、编辑内容 32、保存源文件 6、
   编译为 .class 7（含错误条 3 条）、**树上打字即搜 13、预览内查找 13、面板内查找/跳转行列 15、
   全局搜索 24、包内容对比 57+7（含 3 条「界面隐藏/报告保留」签名过滤断言，以及第 60 项
   新增的 7 条「两个普通文件」对比断言）、对比视图 Ctrl+F 路由 8**、
   可拖动分隔条 3、设置弹窗 4、走查自检 1。
   （**这一行与 `report.py` 的 `GROUPS` 必须逐组对齐、两边相加都等于总数** ——
   曾漏收 16 条却仍报「全绿」，详见 README 的同一段说明。现在 `report.py` 开头
   加了守门人：实跑里出现分组表没收录的断言、或分组表里有走查没跑出来的断言，
   **直接报错退出**，不再静默漏收。）

   `probe_frontend/index.html` 是**生成物**，不是手写的：跑
   `python sync_index.py` 从 `gui/frontend/dist/index.html` 生成，只允许改
   三处（资源路径 / 标题 / 挂哪些脚本），改不出预期形状就报错退出。
   顺带生成 `ui_shot.html`（挂 `ui_shot.js`，把界面摆成"像真实使用"的样子，
   用 `--screenshot` 截图看外观，不跑断言）。
   **为什么固化成脚本**：这两份页面以前是**手工抄**的，实测已经漂了 ——
   走查页的设置弹窗少了三条 `<em>` 说明行、`#set-hint` 是空的、三个输入框少了
   `spellcheck`。走查照样全绿（驱动压根不碰设置弹窗），但它测的已经不是发出去
   的那份页面了。手工同步会烂，机械生成不会。

   **要看 `:hover` / `:active` 这类伪状态时**：无头 Chrome 触发不了伪类。
   做法是把 `app.css` 再取一份文本，用正则把 `:hover:not(:disabled)` /
   `:active:not(:disabled)` 机械改写成 `.sim-hover` / `.sim-active` 注入页面，
   给按钮挂上对应类名就能截到。关键点是改写的是**产品里的同一份规则**，
   不是手抄的副本 —— 手抄的会跟产品漂，那验证就等于没验。

   为什么单开一层：Go 测试能证明"数据算得对"，证明不了"前端把数据渲染对、
   手势接对" —— 而这一轮用户报的四个问题里有三个是**纯前端**问题，
   Go 测试全绿也照样复现不了。走查本身也做过变异验证（四十轮）：
   - 退回"有 children 才算可展开"→ 5 条红，且报出的正是用户截图里的现象
     （`class=row t-mod`、`twisty=""`、`ExpandNode 调用 0 次`）；
   - 去掉叶子行的双击绑定 → 5 条红（预览全打不开）；
   - 不显示 `.class` 的加载态 → 1 条红（`class-loading-shown`）；
   - `linesHtml` 不拼 `mark-*` 类名 → 18 条红（双栏高亮全丢：行级标记
     + 8 条 `split-inline-*` + 3 条 `split-line-bg-*` + 4 条 `split-glyph-*`
     + `split-lineno-bg-follows-line`）。行级类名是这一整套呈现的**唯一锚点**，
     少了它一次红 18 条。而 `split-glyph-blank-reserved` / `-gutter-aligned` /
     `split-line-bg-plain-white` 保持绿 —— 它们本来就是"没有标记行时"的性质，
     说明这几条**不能单独用来证明功能存在**；
   - **前端 `linesHtml` 的类名白名单只认单段**（`/^[a-z][a-z0-9-]*$/`）→ 5 条红
     （`content-highlight` + 4 条 `split-inline-*`）。第三版新增的守门人：
     行内标记的类名是 `"comment mk-mod"` **两段**，白名单只认一段会把整个
     `<span>` 丢掉 —— 丢掉之后语法着色反而"看起来正常"，只有 `split-inline-*`
     这些正向断言抓得住；
   - CSS 里把 `.cline.mark-del` 写成 `.cline.mark-deleted` → 3 条红
     （`split-line-bg-del` 底色透明 + `split-glyph-del` 符号退回占位空格
     + `split-lineno-bg-follows-line` 底色白、阴影 `none`）。这条正是它存在的
     理由：**类名对了不等于样式生效**，只查 DOM 类名的断言抓不到选择器写错。
     注意 `split-mark-covers-linenumber` 保持绿是对的 —— 类名是 JS 挂的，
     CSS 写错只影响"画出来没有"，不影响"类名在不在"；
   - **去掉行底色规则** → 3 条红（`split-line-bg-del` / `-add` / `-mod` 全退回
     `rgba(0,0,0,0)`）。这正是用户第四版点名要的"行级背景底色"；
   - **去掉符号占位规则**（`.contentwin-body.split .code .ln::before`）→ 2 条红
     （`split-glyph-blank-reserved` 占位符 `none` + `split-glyph-gutter-aligned`
     宽度裂成 `47.81/39.61` 两种）。**这条证明"占位符不是装饰、是布局的一部分"** ——
     单看符号本身完全正常，红的是行号被撑歪；
   - **把新增行的符号写成减号** → 1 条红（`split-glyph-add`：符号 `-`）。
     只有 `add` 那条红、`del` / `mod` 保持绿，说明三条断言各盯各的档，没有互相顶替；
   - **行号列底色不跟随整行**（删掉 `.cline.mark-del > .ln { background }`）→ 1 条红
     （`split-lineno-bg-follows-line`：行号栏底色回到 `rgb(255,255,255)`，色条还在）。
     **这是 sticky 行号列的专属陷阱**：只看行底色完全正常，横向滚动时才露馅；
   - **把行底色铺到无标记的行上** → 1 条红（`split-line-bg-plain-white`：底色成了
     `rgb(218,251,225)`）。守的是**减法** —— 只查"三种底色对不对"抓不住
     "底色偷偷铺满了整栏"。（上一版的同类守门断言是 `split-mark-no-fill`，
     那时用户要"背景全白"；这一版改要行底色，守门口径随之换成"无标记行必须透明"。）
   - **mock 的行号不右对齐补齐**（`padNums` 变成空操作）→ 2 条红
     （`split-glyph-gutter-aligned` 宽度裂成两种 + `content-linenumbers` 补齐的空格没了）。
     **这条抓的不是实现 bug 而是 fixture 失真**：真实后端 `content.PlainNumbers`
     给的是补空格右对齐的字符串，行号列因此天然等宽；mock 给裸数字的话 1 位数行
     和 2 位数行本来就差一个字符。加"行号列等宽"断言时先红的就是这条，
     当时先怀疑 CSS，写了个隔离页（`glyph_probe.html`）却证明 CSS 全对 ——
     **红的是 mock，不是产品代码**；
   - **删掉 `.cline .lt .mk-mod` 的颜色规则** → 1 条红
     （`split-inline-changed-word-color`：变了的词回到默认 `#1c2024`）；
   - **把整行染色加回去**（`.cline.mark-mod > .lt, … > .lt * { color: var(--mod) }`）
     → 2 条红（`split-inline-keeps-syntax`：未变的 `"// "` 从 green 变琥珀；
     `split-inline-no-whole-line-color`：带标记行与普通行 `.lt` 色不一致）。
     这条**直接复现用户第三版的报障** —— 证明走查守得住这个口径；
   - **`applyInline` 丢掉原语法类名**（改成直接赋 `mk-mod`）→ 2 条红
     （`split-inline-keeps-syntax-class`：`comment` 没了；
     `split-inline-keeps-syntax`：`color` / `font-weight` 一起丢）；
   - **`wordTokens` 退化成"整行一个词"** → 4 条红（`split-inline-changed-word-marked`
     / `-color` / `-keeps-syntax` / `-keeps-syntax-class`：区间覆盖整行，
     `"// "` 也被染成状态色）；
   - 前端把 `ico.src` 写死成 `icons/file.png` → 7 条红，而
     `icon-fallback-file` 保持绿（`file.png` 正是兜底值）—— 说明图标断言判的是
     "该是哪张图"，不是"有没有图"；
   - CSS 里把 `.icon` 的尺寸从 16px 改成 20px → 只有 `icon-size-16` 一条红；
   - **去掉 `syncScroll` 的调用** → 2 条红（`split-scroll-sync` 右栏停在 0、
     `split-scroll-sync-back` 左栏停在 120）；
   - **去掉滚动同步里的夹取保护** → 1 条红（`split-scroll-no-clamp-back`：
     短栏被夹到 137、长栏被拽回）—— 证明那个"看起来多余"的判断确有必要；
   - **不挂定位点击处理器** → 6 条红（定位那组几乎全丢）；
   - **去掉定位的 200ms 防抖** → 1 条红（`locate-deferred`）；
   - **把 `revealRow` 从"沿 DOM 逐层下探"改成"按 path 全树查"** → 1 条红，
     且报出的祖先链正是 `…|BOOT-INF/lib/decoy-lib.jar|org` —— 走查里那个
     同路径的**诱饵 jar** 起效了，证明"必须逐层下探"不是想当然；
   - **`HEX_PAGE` 改成 100000000** → `edit-hex-grid` / `edit-hex-paging` 2 条红，
     且**总数从 83 掉到 82**（`edit-hex-page2` 藏在 `if (next)` 里，条件不成立时
     整条不执行）—— 这次变异直接催生了 `probe-count`；
   - **去掉树行的「已修改」星号** → 1 条红（`edit-mark-in-tree`）；
   - **应用树不绑右键菜单（只留 `patch-tree`）** → `edit-app-ctxmenu` 红，
     且**总数从 83 掉到 74**（整段 9 条应用树编辑断言全部不执行）。**只有
     `probe-count` 能把这件事暴露出来** —— 剩下 74 条照样全绿，`SUMMARY`
     差点就报 PASS。
   - **前端把 `saveSourceFile` 的分发分支改名** → `savesrc-call` 红
     （新增调用 = `[]`），总分 99/100。`probe-count` 保持绿是对的 ——
     断言条数没变，只是调用没发出去；这种"菜单点了没反应"只有**正向**断言
     抓得住，而 `savesrc-not-savefile` 这种"不该调 X"的断言在
     "什么都没调"时会**空过**，正好说明正反两条要成对写。
   - **mock 的语言类名退回 `lang-js`** → 2 条红（`content-lang-javascript` +
     `content-syntax-applied`）。这条暴露的是**走查自身的洞**：Go 侧
     `LangForExt(".js")` 给的是 `javascript`，mock 却写 `js`，于是 `.lang-*` 那套
     语法着色规则**一条都匹配不上**，而 `content-highlight` 只数 `span.keyword`
     的个数 —— 数得出段数、却没有颜色落上去，**走查一直是"绿的假象"**。
     （第一版的"去掉 `> .lt *` 后代选择器"第一次跑时**没红**，正是被这个洞
     掩盖的 —— 顺带说明 `color` 不像 `background` 能叠，子元素自己声明了就不继承。）
   - **去掉行号栏的 3px 色条** → 1 条红（`split-lineno-bg-follows-line`：阴影 `none`）。
   - **去掉「最大化」按钮的点击绑定** → 7 条红（`content-max-fills-window` /
     `-bigger-than-before` / `-btn-flips` / `-resize-none` / `-drag-disabled` /
     `-restores` / `content-drag-works-after-restore`）。注意 `content-max-btn-present`
     与 `-btn-initial` 仍绿 —— **"按钮画出来了"和"按钮能用"是两件事**，
     只有正向的行为断言抓得住后者；
   - **最大化时忘了加 `.maxed` 类** → 1 条红（`content-max-resize-none`：
     面板铺满了却还挂着原生缩放手柄，拉一下就把"最大化"拉变形）；
   - **最大化时仍允许拖标题栏** → 1 条红（`content-max-drag-disabled`：
     面板被拖到 `-172,85`，已经不是"最大化"了）；
   - **「还原」时回到默认尺寸而不是最大化前的几何** → 2 条红
     （`content-max-restores` + `content-drag-works-after-restore` 的基准跟着错）。
     **这条第一次跑没变红**：断言里最大化前没挪过面板，"默认几何"恰好等于
     "最大化前的几何"，两种实现结果一样。补了一条 `content-max-pre-move`
     （先把面板挪走）才抓住 —— **变异"没变红"往往是"前提没造出来"，
     不一定是断言写弱了**，这一条和前面 `myersOps` 那次是同一类问题；
   - **`rememberContentSize` 不跳过最大化** → 1 条红
     （`content-max-does-not-poison-remembered-size`：在最大化状态下关掉、
     再打开仍是铺满窗口的尺寸）。**这条也返工过一次**：第一版写成
     "最大化 → 还原 → 关闭 → 重开"，变异没变红 —— 还原那一步自己就把尺寸
     写回普通值了，守卫有没有都一样。改成"最大化 → **直接关闭**"才真的踩到它。
   - **保存对话框的建议名加回 `-patched` 后缀** → 1 条红
     （`runpatch-suggest-name-no-suffix`：建议名成了 `app-patched.jar`）。
     原版 `HomeView.handleSaveAppPack` 用
     `setInitialFileName(appTree.getTreeInfo().getRootNode().getName())`，
     建议名就是应用包自己的文件名、初始目录就是应用包所在目录，
     **不带任何后缀** —— 移植时多加的 `-patched` 别再长回来。

   搜索那轮的十一轮（五轮前端 + 六轮 Go），**其中两轮第一遍没变红**，
   两条都记在这里，因为教训是同一类：

   - **双击搜索结果不写历史**（前端）→ 3 条红 + 驱动崩溃。历史没写进去之后，
     后面靠历史撑起来的两条断言跟着全塌，最后 `click(sRows()[0])` 拿到
     `undefined` 让整个驱动崩掉（`DRIVER-CRASH`）。**一条功能缺失引发连锁红**，
     正好说明断言之间是有依赖链的。
   - **双击结果不展开祖先**（前端，把 `it.ancestors` 换成 `[]`）→ 3 条红。
     埋在四层目录下的 `Target.js` 永远露不出来 —— 定位这条路整条断掉。
   - **打开搜索弹窗时不清空搜索框**（前端）→ **第一遍没红**。
     第一遍断言只查了"列表里是历史"，而输入框残留上次关键词时列表**照样**是历史
     （因为 `searchRun` 收到的仍是空串）—— 于是变异全绿。补上
     `sInp.value === ''` 才抓住。**变异没变红 = 断言写弱了。**
   - **单击历史项不回填关键词**（前端）→ 1 条红。
   - **结果行里名称与路径的顺序对调**（前端）→ 1 条红。只对调顺序、类名都还在，
     所以按 class 取元素的那些断言一条都不红 —— **光断言"元素找得到"抓不到
     "顺序错了"**，得断言整串类名。
   - **单关键词搜索不把节点名压进路径栈**（Go 侧）→ 1 条红（路径变成 `应用包:`）。
   - **`matchKeyword` 改成大小写不敏感**（Go 侧）→ 1 条红。
   - **路径式搜索的兜底分支接着 index 走、不归 0**（Go 侧）→ **第一遍没红**。
     原有用例命中的路径都走"匹配分支"，**压根到不了兜底分支**（兜底只在"该级没命中"
     时才走），而走到兜底时 index 往往正好是 0，两种写法等价。专门补了一条
     "链条必须连续"的用例（`a/x/b` 不匹配 `a/b`）才抓住。
     **变异没变红，说明用例没覆盖到那条分支** —— 与前面 `myersOps` 那次同类。
   - **解压后的回滚条件取反**（Go 侧）→ 3 条红。
   - **单关键词搜索连根节点一起匹配**（Go 侧）→ 2 条红。
   - **通配符不做全匹配、退回 contains 语义**（Go 侧）→ 1 条红。
   - **去掉预览面板的几何夹取**（前端）→ **4 条红**（`content-drag-clamps-left-top` /
     `-right-bottom` / `-clamp-keeps-grabbable` / `-clamp-survives-reopen`）。
     **返工过一次**：第一版两次拖动位移等大（都 99999），无夹取实现被抵消回视口内，
     只红 2～3 条 —— "抓得住""重开"两条白写了。把最后一次位移放大到 199999 并放到
     最后，无夹取时面板真的停在视口外，四条才全部踩到。
     **变异红得不够多，往往是"用例的取值让两种实现同解"**。

   四十七轮都还原干净、确认回到 219/219 全绿。**这也顺带证明：那几个报障确实是
   前端问题，改 Go 侧再多也没用。**（此段记录于夹取那一轮，走查基数当时是 219；
   后续设置弹窗那一轮把基数推到 223，见 §五第 33 项。）

   三个走查自身的坑（已实测）：**无头 + `--virtual-time-budget` 下 `scroll`
   事件会漏**（同一次运行里第一次同步赶上帧、第二次没赶上，表现为
   `split-scroll-sync-back` 假红），所以滚动断言里**显式派发**
   `new Event('scroll')`；**`content-body` 上有个捕获期的 `scroll` 监听会收起
   右键菜单**，所以滚动断言必须排在 `ctxmenu-*` / `diffmodel-*` 之后；
   **藏在条件分支里的断言，条件不成立时是整条不执行、而不是变红** —— 于是总数
   悄悄变小而剩下的全绿、`SUMMARY` 照样 PASS。所以末尾加了 `probe-count`
   比对实跑条数。这条不是想出来的，是上一条变异撞出来的。

   第四版还撞出**第四、第五个坑**，都跟 `::before` 有关：
   1. **`getComputedStyle` 的选择器必须指到挂 `::before` 的那个元素**。
      `::before` 挂在 `.ln` 上，断言里写 `glyph('.cline.mark-add')`（行 div）
      只会读到 `none` —— 一次假红 4 条，而 CSS 完全正常。
   2. **Chrome 返回的 `content` 是带引号的字符串字面量**（`"+"` 三个字符），
      不是裸的 `+`。手写期望值时极易少写一个收尾引号（`'"+'` 只有两个字符），
      于是断言永远不可能通过。现在的写法是**先把外层引号剥掉再比**
      （`c.replace(/^"|"$/g, '')`），期望值写成裸的 `+` / `-` / `~`。
      这两条都写进了 `driver.js` 的注释里 —— 它们不是"想清楚"就能避开的，
      是必须踩过一次才知道的。

   定位第 1 个坑的办法值得记：**写一个只含这段 CSS 的最小隔离页**
   （`glyph_probe.html`），在那里量到 `content="+" / "-" / "~" / " "`、
   4 种 `.ln` 宽度统一 `47.8125` —— 于是"CSS 是好的"被证实，
   剩下的只可能是走查脚本自己写错了。**当"实现看着没问题但断言红了"时，
   先用最小隔离页把实现和测试分开，比继续盯着断言猜要快得多。**

   变异验证有脚本：`bash mutate.sh <文件绝对路径> <python 替换脚本>` ——
   备份 → 注入 → 跑走查 → 列出变红断言 → 还原 → `diff -q` 校验还原。
   手写"改-跑-还原"容易忘了还原或看漏红了几条，所以固化成脚本。注意传**绝对路径**
   （脚本内部会先 `cd` 到自身目录）。

   Go 侧另外二十五轮变异（不在走查里）：双栏不给标记 → `TestOpenContentSplitMarks`
   红；`DiffMarks` 只标右栏不标左栏 → `TestDiffMarks` 三例 + 对齐用例红；
   **`myersOps` 的闸门退回 `maxD = n + m`** → 2 条红
   （`TestDeltasLargeFileChangedAtVeryStart` + `TestComputeSplitMarksLargeFileChangedAtVeryStart`）。
   第一次跑**没红**：改动落在文件中间时公共前缀把 `n+m` 压下去了，两种口径都能过 ——
   于是专门补了"改动落在**第一行**"的两条测试，再跑才抓住。**变异"没变红"是断言
   写弱了的信号**，不是实现没问题的证据；
   **`Deltas` 连公共后缀一起剥** → 2 条红（`TestDeltasDoesNotTrimCommonSuffix`
   + `TestDiffGoldenAgainstJava` 132 条里红 22 条）。重复行让最小编辑脚本不唯一，
   剥后缀会改变 java-diff-utils 的选路 —— 所以只剥前缀（等价平移），
   这条测试专门钉住"别再顺手把后缀也剥了"；
   **`wordTokens` 退化成"整行一个词"** → 4 条红（区间覆盖整行，等于回到整行染色）；
   **`applyInline` 在没有行级标记时也贴行内标记** → 1 条红
   （`TestRenderMarkedIgnoresInlineWithoutLineMark`）。第一版断言写成
   `sg.Class == "mk-mod"`，变异后类名变成 `mk-`（mark 为空）照样绿 ——
   改成 `strings.Contains(sg.Class, "mk-")` 才抓住；
   **`applyInline` 丢掉原语法类名** → 2 条红（`TestApplyInlineSplitsSegment` /
   `TestApplyInlineAcrossSegments`）；
   `iconName` 让后缀规则抢在四条特例之前 → `TestIconName` 两例红；
   图标常量写错一个字母（`file-class` → `file-clas`）→ `TestIconFilesExist`
   双向报错（文件不存在 + 原文件没人引用）；
   `TestIconsEmbedded` 里的 embed 前缀从 `frontend/dist/` 改成 `dist/` →
   **只有这一条**红 19 例，`TestIconFilesExist`（查工作目录）照旧全绿 ——
   恰好说明这两条查的是不同侧面，少哪条都会漏掉一种"图上不了界面"的失效模式；
   **祖先链不剔掉根** → `TestLocateMapped*` 3 条红（链多一层）；
   **`NodeView.Mapped` 漏传** → `TestNodeViewCarriesMappedFlag` 1 条红；
   **去掉 `SetContent` 里的 `Md5`/`Size` 缓存清空** → 只有
   `TestSetContentResetsSizeAndMd5Cache` 1 条红，**端到端那条不红** ——
   写出侧的大小是从实际数据算的（STORED 条目重算 crc/大小、DEFLATE 走数据
   描述符），压根不读 `n.Size`；真正被影响的是写进 patch-up sign 的 md5 /
   `OriginAppSize`。这类"后果不在最显眼处"的 bug 只能靠针对性单测守；
   **`looksBinary` 恒返回 `false`** → 2 条红（二进制被当文本打开、按文本存回）；
   **`EncodeString` 的 GBK 分支当 UTF-8 透传** → 3 条红（含
   `TestSaveContentHonorsCharset`），GBK 文件改一个字保存后整篇乱码；
   **编码器从 GB18030 退回严格 GBK**（即"顺手对齐原版"）→ 1 条红
   （`TestEncodeStringKeepsEmojiAsGB18030NotQuestionMark`），且 **`gui` 侧全绿**
   —— 说明这条刻意偏差光靠端到端抓不到，只有那条单测守着；
   **「保存源文件」菜单项不判 `.class`**（改成"非目录都给"）→ 6 条红
   （`TestPatchMenuClaimsFollowUpStream` 四例 + 主根那条 + 只挂补丁树那条），
   菜单是**逐节点**算的，claim 写松了就会到处冒出来；
   **默认文件名不换后缀**（`Foo.class.java`）→ `TestSourceSaveName` 1 条红；
   **`isClassNode` 不判目录** → `TestIsClassNode` 1 条红（原版 claim 里有
   `!isDir()`，少了它名字像 class 的目录也会长出一项点下去没用的菜单）；
   **`SaveSourceNodeAs` 不判 class** → `TestSaveSourceNodeAsIgnoresNonClass` 1 条红
   （原版这里是静默 return，不弹错打断用户）。
6. **窗口图标实测（GUI 实跑 + Win32 截图）**：`icon.ico` 编进 exe 只证明"字节进去了"，
   证明不了"窗口真的用它" —— Wails 源码里没有任何 `SetIcon` / `WM_SETICON`，
   窗口图标完全依赖"窗口类无图标时回退到 exe 主图标"这条 Windows 规则。
   所以直接启动 exe，用 ctypes 调 `PrintWindow`（`PW_RENDERFULLCONTENT`）把窗口
   渲染到内存 DC，再裁标题栏左上角放大看。两个踩过的坑：
   - **不能从屏幕 BitBlt 抓**。本机 DPI 是 200%，且非前台进程抢不到焦点
     （`SetForegroundWindow` 会被 Windows 拒绝），实测抓到的是**遮挡它的另一个
     程序**（一张 DBX Marketplace 的界面）。`PrintWindow` 不依赖窗口在屏幕上可见。
   - `ctypes` 的 `create_string_buffer` 按索引取返回的是 `bytes` 而不是 `int`，
     取像素必须走 `.raw`。
   另注：`PrintWindow` 对 WebView2 的**客户区**截出来是白的（内容在独立合成层），
   但标题栏是原生绘制的、能截到 —— 验证窗口图标只需要标题栏，够用。

**跑 Java 驱动的三个坑**（`run.sh` 已处理，手工跑时注意）：

- **`APPDATA` 必须非空。** `OSUtil.getAppConfigPath` 是
  `Paths.get(System.getenv("APPDATA"))`，Git Bash 里 `APPDATA` 常常为空 →
  `Paths.get(null)` 抛 NPE，而 `main` 抛异常后 JavaFX 的 toolkit 线程仍是非
  daemon 的，进程**不会退出**，看起来像卡死。`run.sh` 把 `APPDATA` 指到
  `out/appdata`，顺带避免读到用户真实配置导致基准不可复现。
- **必须 `export MSYS_NO_PATHCONV=1`。** 这条是 2026-10-10 定位出来的，
  比"加引号"更根本 —— `-cp` 本来就是**一整个**参数（`run.sh` 里一直是
  `java -cp "$CP"`，引号没漏），但 MSYS/Git Bash 会对参数做**路径转换**：
  这个参数里用 `;` 串了 28 条 classpath，首条是本项目的 `target/classes`，
  而工作区路径带空格（`新建文件夹 (2)`）—— 转换器把它按空格拆开、还把
  `D:/` 改写成 POSIX 形态。实测 JVM 收到的 `java.class.path` 被拆成 **55 段**
  （正常 28 段），只剩 `out\classes` 一条有效，JavaFX 全部
  `ClassNotFoundException`。

  症状极具误导性：Java 侧产物压根没生成，`patchreadme` 路径比对的其实是
  "夹具原样 vs Go 改过的包"，报出来是 **`README.txt 内容不一致`** ——
  长得像业务逻辑回归，实际是环境问题。**遇到基准突然变红，先看 Java 侧日志
  有没有异常，别先怀疑自己的改动。**

  诊断手法（可复用）：写个探针类打印 `System.getProperty("java.class.path")`
  的段数与前几段，默认环境 vs `MSYS_NO_PATHCONV=1` 各跑一次，一眼看出被拆。
- **classpath 也要整体加引号**（`java -cp "$CP"`）。这条单独不够，但省掉它
  会叠加 word splitting，排查时更难看清。

**夹具生成要显式给目录。** `make_fixture.py` 的 `out_dir` 默认是 `.`，
而 `run.sh` 读的是 `fixture/` 下的文件 —— 漏掉参数就会往 golden 根目录写一份
没人用的 `app.jar` / `patch.zip` / `readme.txt`，制造"生成了其实没生成"的假象
（2026-10-10 修）。
