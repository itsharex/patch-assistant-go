# 开发与测试

本文件面向要改这份代码的人：怎么跑、怎么构建、测试怎么组织、哪些坑已经踩过。
产品能力与交互见 [FEATURES.md](FEATURES.md)，移植总账见 [MIGRATION.md](../MIGRATION.md)。

> 下文出现的 `../src-java`（Java 原版源码）与 `_extract/`（黄金基准、走查脚本、变异脚本等
> 开发工具）是开发机上的同级目录，**不随本仓库分发**，相关步骤只在本机对照时可用。

## 本地开发运行

### 跑 Go 版

不需要先 build，`go run` 直接跑源码：

```bash
go run ./cmd/patchassistant list   ../src-java/lib/fernflower-1.3.0.jar
go run ./cmd/patchassistant info   ../src-java/lib/fernflower-1.3.0.jar
go run ./cmd/patchassistant readme internal/service/testdata/README.txt
```

`list` 打印包内树形结构（目录在前、同级按名不区分大小写、内部类嵌在外部类下），
`info` 出统计，`readme` 解析补丁说明里的升级指令，
`patch` 走完整的"匹配 → 改包 → 写签名"链路（加 `-v` 可看到每一步）。

常用组合：

```bash
go build -o pa.exe ./cmd/patchassistant   # 出可执行文件，反复调用更快
go vet ./...                              # 静态检查
go test -count=1 ./...                    # 强制重跑，不吃缓存
gofmt -l .                                # 列出未格式化的文件
```

### 跑 GUI（Wails v2）

```bash
cd gui
wails build                 # 产出 gui/build/bin/PatchAssistant2J.exe
wails dev                   # 开发模式：热重载前端
```

## 环境与工具链的几个坑

两个环境上的坑，都已实测：

1. **本机 `proxy.golang.org` 不通。** 首次 `go mod tidy` / `wails build`
   会卡在拉依赖。临时切镜像即可，不必改全局配置：

   ```bash
   export GOPROXY="https://goproxy.cn,direct" GOSUMDB="sum.golang.google.cn"
   ```

2. **`go mod tidy` 会把 Wails 拉到 v2.16.0，与本机 CLI 的 2.15.0 不匹配。**
   出现版本告警时降回来：

   ```bash
   go get github.com/wailsapp/wails/v2@v2.15.0
   ```

`gui/wails.json` 里 `frontend:install` / `frontend:build` 三个命令都是空的，
所以 `wails build` 不跑 npm，直接服务 `gui/frontend/dist/` 的静态文件。
因此前端**不 `import`** Wails 生成的 `frontend/wailsjs/...` 绑定，
而是用运行时注入的 `window.go.main.App.<Method>` 调后端。

GUI 绑定层的测试与 CLI 共用黄金基准夹具：

```bash
go test ./gui/ -v
```

---

## 对照跑 Java 原版（用于核对行为）

改 Go 实现时，最可靠的验证方式是让 Java 原版跑同一个输入，两边比。启动方式：

```bash
cd ../src-java
"D:/soft/runtime/maven/apache-maven-3.9.12/bin/mvn.cmd" -B javafx:run
```

两个必踩的坑，都已实测确认：

1. **Git Bash 里要用 `mvn.cmd`，不能用 `mvn`。**
   PATH 里的 `mvn` 是 bash 脚本，在这个环境下会报
   `找不到或无法加载主类 org.codehaus.plexus.classworlds.launcher.Launcher`。
   直接给 `mvn.cmd` 的全路径即可。

2. **必须保证 `APPDATA` 环境变量存在。**
   `OSUtil.getAppConfigPath()` 走的是 `Paths.get(System.getenv("APPDATA"))`，
   一旦 `APPDATA` 为空就会 `Paths.get(null)` 抛 NPE。更麻烦的是这个 NPE 会
   **盖掉真正的启动异常** —— `ExceptionUtil.writeErrorFile` 自己也要读配置目录，
   于是它抛的 NPE 成了最终看到的那个，根因被吃掉。
   如果 shell 里没有这个变量，手动补上：

   ```bash
   APPDATA='C:\Users\<你的用户名>\AppData\Roaming' \
     "D:/soft/runtime/maven/apache-maven-3.9.12/bin/mvn.cmd" -B javafx:run
   ```

启动参数（`AppContext.initArgs` 只认 `--key=value` 形式）：

| 参数 | 作用 |
|---|---|
| `--target=<路径>` | 以 `.jar` 结尾 → 作为应用包；否则 → 作为补丁包/补丁目录 |
| `--patch=<路径>` | 指定补丁包或补丁目录 |
| `--dev=<任意非空值>` | 开发模式：异常除写 `error.log` 外，额外打到 stderr |

配合 `javafx-maven-plugin` 传参：

```bash
mvn.cmd -B javafx:run "-Djavafx.args=--target=app.jar --patch=patch.zip --dev=1"
```

---

## 测试策略

`internal/service/testdata/readme_golden.tsv` 是**用原版 Java 跑出来的黄金基准**，
不是我们自己写的期望值。产生方式：

1. 用 JDK 21 编译运行 `../_extract/ReadMeVerify.java`。该程序里的正则常量与
   `Pattern` 表达式逐字抄自 `FileNameUtil.FILE_NAME_REGEX` 与
   `ReadMeFileService.pattern`，`AppNodePath` 逻辑抄自 `ReadMePathInfo.getAppNodePath()`。
2. 让它读同一份 `testdata/README.txt`，输出 `type/path1/path2/path3/appNodePath`。
3. 固化输出为基准文件。

因此 `TestParseReadMeMatchesJavaGolden` 验证的是"Go 与 Java 是否同行为"，
而不是"Go 是否符合我的期望"。

**输出包也有黄金基准**，见 `../_extract/golden/`：用同一夹具驱动原版 Java 与 Go
版，对跑 `patch`（打补丁写出应用包）与 `patchreadme`（改写补丁包 README）两条
路径，比对条目名、顺序与解压后内容。一键复跑 `bash run.sh`。
注意**逐字节一致做不到**（Java 用 JDK 的 zlib `Deflater`、Go 用纯 Go `flate`，
压缩流本就不同），判定口径是语义等价；剩余字段级差异已逐条登记在
`MIGRATION.md` §3.10。

测试文件：

| 文件 | 覆盖 |
|---|---|
| `internal/service/readme_test.go` | README 指令解析（含 Java 黄金基准比对） |
| `internal/service/patch_e2e_test.go` | 匹配 → 保存应用包 → 签名的端到端链路 |
| `internal/service/patchsave_test.go` | README 写回补丁包（含两处上游 bug 的特征测试） |
| `internal/content/` | 语法高亮（204 条跨语言基准）、差异行号（132 条跨语言基准）、JavaFX 段落口径（探针实测值钉死）、解析器分派表、双栏差异标记与**词级差异**口径（`splitmarks_test.go`，15 条） |
| `internal/util/textdiff_deltas_test.go` | 差异算法（10 条）：**大文件只差一行不再退化成整篇替换**（含"改动落在第一行"）、**只剥公共前缀不剥后缀**（钉住 java-diff-utils 的选路）、下标平移、空输入、纯增纯删 |
| `internal/decompile/` | `.class` 反编译（8 个测试）：类名解析、非法输入拒绝、6 样本跨语言基准（中文常量、枚举、接口、泛型抽象类、内部类、41 KB 大类）、失败文案翻译 3 条、内置运行时优先 / 逃生口 / **清空 `PATH` 后仍能反编译** |
| `internal/javaruntime/` | 内置运行时（6 个测试）：解压产物齐全、路径稳定、版本目录名、zip slip 防护、逃生口开关 |
| `internal/util/charset_test.go` | 字符集（8 个测试）：GBK 解码、探测、UTF-8 容错，以及**编码侧的 4 个** —— GBK 编解码往返、UTF-8 分支、**emoji 保持 GB18030 不被写成 `?`**（钉住与 Java 的刻意偏差）、GB18030 覆盖全部 Unicode（说明为何不需要逐 rune 兜底） |
| `gui/app_test.go` | GUI 绑定层（**54** 个测试）：开包建树、变更状态传递、产物可重开、设置生效、重复匹配幂等、拖放落点与接受规则、README 识别范围与正文下发、补丁树右键菜单的 claim 条件与顺序、菜单各动作（重新加载/取消关联/保存文件/**保存源文件**/复制/签名/绑定信息/关闭补丁/聚焦，两棵树按 `side` 分派）、换包清理旧映射、**在另一棵树里定位**（正反向、嵌套 jar 内部、新增节点、四条拒绝路径、`Mapped` 标记）、**「保存源文件」的判据与默认文件名**（含"只挂补丁树"这条刻意未接）、**文件详情列：文件节点带 MD5、目录节点不带**（`TestNodeViewCarriesMd5`） |
| `gui/content_test.go` | 内容预览（15 个测试）：解析器分派、左右节点配对规则、差异模式、编码切换、双栏差异标记、目录与未知节点拒绝、`.class` 反编译与内部类 |
| `gui/icon_test.go` | 树节点图标（5 个测试）：后缀映射表（含大小写敏感）、`getIconImage` 的判定顺序、图标文件都存在且无多余、与原版 PNG 逐字节一致、**21 张图确实编进了二进制**（读 `embed.FS`，不是工作目录） |
| `gui/editor_test.go` | 编辑包内文件内容（**20** 个测试）：文本/二进制形态判定（按内容不按后缀）、**`.class` 默认给反编译源码**（另有「强制字节」「反编译失败降级到十六进制」「源码模式拒绝保存」三条）、保存后节点带 `edited` 标记、编码按 charset 写回、十六进制保存、目录与未知节点拒绝、**端到端"改内存 → `RunPatch` 产物真的变"**、重新加载丢弃改动、应用树菜单 claim 顺序、**「编译为 .class」能力标志 / 往返 / 报错 / 非 class 拒绝 / 内部类拒绝 / 集合校验** |
| `gui/search_test.go` | 全局搜索（**19** 个测试）：空关键词走历史、历史去重/头插/裁剪、两棵树与路径标签、根不参与匹配、大小写敏感、`*` 全匹配、路径式沿父子链匹配、**链条必须连续**、非法通配符返回错误、只开一棵树、**嵌套 jar 的现场解压 / 未命中回滚 / 已展开不重复解压**、去重规则、历史项与节点项不互撞 |
| `internal/tree/node_test.go` | `SetContent` 的语义（5 个测试）：替换字节、**清空 `Md5`/`Size` 缓存**、复制入参（防外部切片复用）、`edited` 标记、`MemorySource` 每次返回新读取器 |
| `../_extract/tools/probe_frontend/` | 前端走查（无头浏览器，314 条断言，见下） |

`patchsave_test.go` 里有两类**特征测试**（characterization test）：它们记录的是
**上游当前的行为**而不是"应该怎样"。`TestRollbackPackNodesReplicatesUpstreamBug`
断言"回滚时所有节点都被恢复成第 0 份快照" —— 这是 Java 漏写 `i++` 的结果；
`TestUpdatePatchReadmeNestedUnexpandedDropsContent` 断言"未展开的内层 jar 会被写空"。
哪天有人修掉这些，用例会变红 —— 那时再决定是保持与 Java 一致、还是有意修正。

### 前端走查（无头浏览器）

`../_extract/tools/probe_frontend/`，`bash run.sh` 一键复跑。

做法是注入一个后端替身顶掉 `window.go`，在真实 Chrome 里加载**真实的**
`app.js` / `app.css`，模拟点击 / 双击 / 右键 / Esc / 滚动 / 拖动分隔条，把 **314** 条断言输出成
`PROBE|<名字>|PASS/FAIL|<细节>`。

为什么单开一层：Go 测试能证明"数据算得对"，证明不了"前端把数据渲染对、
手势接对"。这一轮用户报的四个问题里有三个是**纯前端**问题 ——
"jar 包里没有高亮"、"lib 包里没有再次打开按钮"、"点名字不能展开"，
Go 侧测试全绿也照样复现不了。

断言按组统计：工具栏品牌标 1、**窗口按钮（无边框）2**、**树渲染与手势 15**、
**执行打补丁 1**、**文件详情 5**、文件类型图标 10、
**内容预览 54**、预览右键菜单 9、**双栏滚动同步 5**、**对比视图外观 7**、
**在另一棵树里定位 10**、class 反编译 5、**编辑内容 32**、**保存源文件 6**、
**编译为 .class 7**（含错误条 3 条）、**树上打字即搜 13**、**预览内查找 13**、
**面板内查找 / 跳转行列 15**、**全局搜索 24**、**包内容对比 57+7**、
**对比视图 Ctrl+F 路由 8**、
**可拖动分隔条 3**、设置弹窗 4、**走查自检 1**。

> 这一行必须与 `report.py` 的 `GROUPS` 逐组对齐，两边相加都要等于断言总数。
> 曾有一段时间 `GROUPS` 漏收了 16 条（需求 H / I / J 新增的那些），报告页只列出
> 122 条却报「138/138」—— **看起来全绿，实际有 16 条压根没进表**。
> 现在 `report.py` 开头有守门人：实跑里出现分组表没收录的断言、或分组表里有
> 走查没跑出来的断言，**直接报错退出**，不再靠人记得去核对。

> `probe_frontend/index.html` 是**生成物**：跑 `python sync_index.py` 从
> `gui/frontend/dist/index.html` 生成（只允许改资源路径 / 标题 / 挂哪些脚本，
> 改不出预期形状就报错）。顺带生成 `ui_shot.html` —— 挂 `ui_shot.js`，
> 把界面摆成"像真实使用"的样子（展开树 + 填补丁信息），用 `--screenshot`
> 截图**看**外观，不跑断言。
> 以前这两份页面是手工抄的，实测已经与 dist 漂了（走查页的设置弹窗少了三条
> `<em>` 说明行）—— 走查照样全绿，但它测的已经不是发出去的那份页面了。

> 五个走查自身的坑，已实测：
> - **无头 + `--virtual-time-budget` 下 `scroll` 事件会漏。** 同一次运行里
>   第一次同步赶上了帧、第二次没赶上 —— 表现为 `split-scroll-sync-back` 假红。
>   所以滚动断言里**显式派发** `new Event('scroll')`：走的是同一条监听路径，
>   只是不赌浏览器何时产帧。
> - **`content-body` 上有个捕获期的 `scroll` 监听会收起右键菜单**
>   （`app.js` 里 `addEventListener('scroll', hideMenu, true)`）。所以滚动断言
>   必须排在 `ctxmenu-*` / `diffmodel-*` 之后，否则会把它们带红。
> - **藏在条件分支里的断言，条件不成立时是整条不执行、而不是变红。**
>   于是总数会悄悄变小，而剩下的全绿 —— `SUMMARY` 照样报 PASS。
>   所以末尾加了一条 `probe-count`：实跑条数对不上预期就是失败。
>   这条不是想出来的，是变异验证时撞出来的（见下）。
> - **`getComputedStyle` 的选择器必须指到挂 `::before` 的那个元素。**
>   双栏的 `+` / `-` / `~` 符号是 `.ln::before`，断言里写
>   `glyph('.cline.mark-add')`（行 div）只会读到 `none` —— 一次假红 4 条，
>   而 CSS 完全正常。写成 `.cline.mark-add > .ln` 才对。
> - **Chrome 返回的 `content` 是带引号的字符串字面量**（`"+"`，三个字符），
>   不是裸的 `+`。手写期望值时极易少写一个收尾引号（`'"+'` 只有两个字符），
>   于是断言**永远不可能通过**。现在统一**先剥掉外层引号再比**
>   （`c.replace(/^"|"$/g, '')`），期望值写成裸的 `+` / `-` / `~`。
>
> 后两个坑是靠**最小隔离页**定位的：写一个只含这段 CSS 的
> `glyph_probe.html`，在那里量到 `content="+" / "-" / "~" / " "`、
> 四种 `.ln` 宽度统一 `47.8125` —— "CSS 是好的"被证实，剩下的只可能是走查
> 脚本自己写错了。**当"实现看着没问题但断言红了"时，先用最小隔离页把实现和
> 测试分开，比继续盯着断言猜要快得多。**

## 变异验证记录（回归防线自检）

走查本身也做过变异验证（改坏实现确认会变红，再还原）：

- 退回"有 children 才算可展开" → 5 条红，报出的正是用户截图里的现象
  （`class=row t-mod`、`twisty=""`、`ExpandNode 调用 0 次`）；
- 去掉叶子行的双击绑定 → 5 条红（预览全打不开）；
- 不显示 `.class` 的加载态 → 1 条红（`class-loading-shown`）；
- `linesHtml` 不拼 `mark-*` 类名 → 18 条红（双栏高亮全丢：行级标记 + 8 条
  `split-inline-*` + 3 条 `split-line-bg-*` + 4 条 `split-glyph-*` +
  `split-lineno-bg-follows-line`）。行级类名是这一整套呈现的**唯一锚点**；
  而 `split-glyph-blank-reserved` / `-gutter-aligned` / `split-line-bg-plain-white`
  保持绿 —— 它们本来就是"没有标记行时"的性质，**不能单独用来证明功能存在**；
- **`linesHtml` 的类名白名单只认单段**（`/^[a-z][a-z0-9-]*$/`）→ 5 条红
  （`content-highlight` + 4 条 `split-inline-*`）。行内标记的类名是
  `"comment mk-mod"` **两段**，白名单只认一段会把整个 `<span>` 丢掉 ——
  丢掉之后语法着色反而"看起来正常"，只有 `split-inline-*` 这些正向断言抓得住；
- CSS 里把 `.cline.mark-del` 写成 `.cline.mark-deleted` → 3 条红
  （`split-line-bg-del` 底色透明 + `split-glyph-del` 符号退回占位空格 +
  `split-lineno-bg-follows-line` 底色白、阴影 `none`）。
  这条是特意加的：**类名对了不等于样式生效**，只查 DOM 类名的断言抓不到选择器写错。
  而 `split-mark-covers-linenumber` 保持绿是对的 —— 类名是 JS 挂的，
  CSS 写错只影响"画出来没有"，不影响"类名在不在"；
- **去掉行底色规则** → 3 条红（`split-line-bg-del` / `-add` / `-mod` 全退回透明）。
  这正是用户这一轮点名要的"行级背景底色"；
- **去掉符号占位规则** → 2 条红（`split-glyph-blank-reserved` 占位符 `none` +
  `split-glyph-gutter-aligned` 宽度裂成两种）。**这条证明"占位符不是装饰、
  是布局的一部分"** —— 单看符号本身完全正常，红的是行号被撑歪；
- **把新增行的符号写成减号** → 1 条红（`split-glyph-add`）。只有 `add` 那条红、
  `del` / `mod` 保持绿，说明三条断言各盯各的档，没有互相顶替；
- **行号列底色不跟随整行** → 1 条红（`split-lineno-bg-follows-line`）。
  **这是 sticky 行号列的专属陷阱**：只看行底色完全正常，横向滚动时才露馅；
- **把行底色铺到无标记的行上** → 1 条红（`split-line-bg-plain-white`）。
  守的是**减法** —— 只查"三种底色对不对"抓不住"底色偷偷铺满了整栏"；
- **mock 的行号不右对齐补齐** → 2 条红（`split-glyph-gutter-aligned` +
  `content-linenumbers`）。**这条抓的不是实现 bug 而是 fixture 失真**：
  真实后端 `content.PlainNumbers` 给的是补空格右对齐的字符串，行号列因此天然等宽；
  mock 给裸数字的话 1 位数行和 2 位数行本来就差一个字符；
- **删掉 `.cline .lt .mk-mod` 的颜色规则** → 1 条红
  （`split-inline-changed-word-color`：变了的词回到默认 `#1c2024`）；
- **把整行染色加回去**（`.cline.mark-mod > .lt, … > .lt * { color: var(--mod) }`）
  → 2 条红（`split-inline-keeps-syntax`：未变的 `"// "` 从 green 变琥珀；
  `split-inline-no-whole-line-color`：带标记行与普通行 `.lt` 色不一致）。
  这条**直接复现用户第三次的报障**，证明走查守得住"只染变了的词"这个口径；
- **`applyInline` 丢掉原语法类名**（改成直接赋 `mk-mod`）→ 2 条红
  （`split-inline-keeps-syntax-class`：`comment` 没了；
  `split-inline-keeps-syntax`：`color` / `font-weight` 一起丢）；
- **`wordTokens` 退化成"整行一个词"** → 4 条红（`split-inline-changed-word-marked`
  / `-color` / `-keeps-syntax` / `-keeps-syntax-class`）；
- 前端把 `ico.src` 写死成 `icons/file.png` → 7 条红，而
  `icon-fallback-file` 保持绿（`file.png` 正是兜底值）—— 图标断言判的是
  "该是哪张图"，不是"有没有图"；
- CSS 里把 `.icon` 的尺寸从 16px 改成 20px → 只有 `icon-size-16` 一条红；
- **去掉 `syncScroll` 的调用** → 2 条红（`split-scroll-sync` 右栏停在 0、
  `split-scroll-sync-back` 左栏停在 120）；
- **去掉滚动同步里的夹取保护** → 1 条红（`split-scroll-no-clamp-back`：
  短栏被夹到 137、长栏被拽回）—— 证明那个"看起来多余"的判断确有必要；
- **不挂定位点击处理器** → 6 条红（定位那组几乎全丢）；
- **去掉定位的 200ms 防抖** → 1 条红（`locate-deferred`）；
- **把 `revealRow` 从"沿 DOM 逐层下探"改成"按 path 全树查"** → 1 条红，
  且报出的祖先链正是 `…|BOOT-INF/lib/decoy-lib.jar|org` —— **走查里那个
  同路径的诱饵 jar 起效了**，证明"必须逐层下探"不是想当然。
- **`HEX_PAGE` 改成 100000000** → `edit-hex-grid` / `edit-hex-paging` 2 条红，
  且**总数从 83 掉到 82** —— `edit-hex-page2` 藏在 `if (next)` 里，
  条件不成立时整条不执行。这次变异直接催生了 `probe-count`。
- **去掉树行的「已修改」星号** → 1 条红（`edit-mark-in-tree`）；
- **应用树不绑右键菜单（只留 `patch-tree`）** → `edit-app-ctxmenu` 红，
  且**总数从 83 掉到 74** —— 整段 9 条应用树编辑断言全部不执行。
  只有 `probe-count` 能把这件事暴露出来：剩下 74 条照样全绿，`SUMMARY`
  差点就报 PASS。
- **前端不隐藏源码形态的「保存」** → 1 条红（`edit-src-no-save`）。那个按钮
  点了会被拦，但**露出来本身就是误导** —— 用户会以为能存回 class；
- **「改字节」不生效（`asBytes` 不往下传）** → 8 条红：`edit-src-bytes-switch`
  加整组 `edit-hex-*` 连锁失败（切不过去就没有网格）。`edit-hex-page2` 不在
  变红列表里 —— 它藏在 `if (next)` 里整条不执行，由 `probe-count`（实跑 91、
  预期 93）兜住。
- **把 `saveSourceFile` 的分发分支改名** → `savesrc-call` 红（新增调用 = `[]`），
  总分 99/100。`probe-count` 保持绿是对的 —— 断言条数没变，只是调用没发出去；
  这种"菜单点了没反应"只有**正向**断言抓得住，而 `savesrc-not-savefile` 这种
  "不该调 X"的断言在"什么都没调"时会**空过**，正好说明正反两条要成对写。
- **前端不画文件详情列（`if (!node.dir)` 前加 `false &&`）** → 4 条红
  （`tree-file-details` / `tree-file-modTime` / `tree-file-size` /
  `tree-file-md5`），总分 105/109。`tree-dir-no-details` 与 `probe-count`
  保持绿 —— 目录行本来就没有详情列，断言条数也没变，各自守的是不同的事。
- **前端把编译报错只走 toast、不填错误条（删 `setEditorError(errText(e))`）**
  → 1 条红（`edit-err-show-on-fail`）。`edit-err-close` 仍绿是对的 ——
  关闭按钮本来就只是把已是隐藏的条设为隐藏。
- **`collapseAll` 里的 `clearExpanded` 改成 no-op** → 1 条红
  （`collapse-all-collapsed`：点完按钮后应用树行数仍是 18 而非 1）。
  `collapse-all-pre-expanded` 仍绿是对的 —— 前置条件靠前面 `nested-jar-*` 段
  把 `BOOT-INF/lib` 展开后自然成立，测的是"该跑的都跑了"不是"折叠效果"。
- **去掉 `.row` 的 `width: max-content`** → 1 条红（`tree-row-fits-content`：
  行盒只剩容器宽、子项溢出，横向滚动时溢出那截没有悬停底色）。这条是
  变异验证时"没红"逼出来的 —— 第一版只断言了"不压缩"和"出现滚动条"，
  去掉 `max-content` 那两条照样绿（子项都是 `flex: none`），于是补了这条；
- **整体退回"压缩 + 省略号"方案**（行撑满容器、`.name` / `.name-text` 允许收缩）
  → 2 条红（`tree-narrow-name-not-squeezed`：文件名 `clientWidth=0`，
  以及 `tree-row-fits-content`）—— `clientWidth=0` 正是用户截图里
  文件名被挤没的现象；
- **让分隔条不绑 `mousedown`** → 2 条红（`splitter-v-drags` / `splitter-h-drags`：
  拖动前后尺寸纹丝不动）；
- **把「已修改」星号挂回 `row`（而不是 `.name` 里）** → 1 条红
  （`edit-mark-hugs-name`：星号距文件名末尾 **63px**）。这条守的是
  `.name` 加 `min-width` 之后带出来的副作用；
- **去掉 `.name-text` 上的删除线规则** → 1 条红（`tree-del-line-through`：
  `text-decoration` 为 `none`）。`.name` 变成 flex 容器后，它的
  `text-decoration` **不会**传播进 flex 子项 —— 这类"看着等价、实则不生效"
  的写法，只有真去量 `getComputedStyle` 才抓得住。
- **行号栏底色退回 RichTextFX 库默认的 `#ddd`** → 1 条红
  （`diff-lineno-white-bg`：`rgb(221, 221, 221)`）。这条守的是"到底抄了哪一份
  样式" —— 原版 `theme.css` 把库默认值整条覆盖成白底了，照库抄就错；
- **代码区字号退回 12px** → 1 条红（`diff-code-font-14`）；
- **去掉行号栏的右边框** → 1 条红（`diff-lineno-border`，报出
  `0px rgb(174, 179, 194)` —— 宽度没了、颜色还留着）；
- **把行号右内边距改成对称的 `0 5px`** → 1 条红（`diff-lineno-fold-space`：
  左右都成了 5px）。这条守的是原版**折叠指示器**占的那截留白：它没有颜色、
  没有类名、不影响任何文本，被当成冗余 padding 清掉时**别的东西一条都不会红**；
- **去掉正文左缩进（`.lt` 的 `3ch`）** → 1 条红（`diff-code-inset`：
  左内边距 0px）。
- **mock 的语言类名退回 `lang-js`** → 2 条红（`content-lang-javascript` +
  `content-syntax-applied`）。这条暴露的是**走查自身的洞**：Go 侧
  `LangForExt(".js")` 给的是 `javascript`，mock 却写 `js`，于是 `.lang-*` 那套
  语法着色规则**一条都匹配不上**，而 `content-highlight` 只数 `span.keyword`
  的个数 —— 数得出段数、却没有颜色落上去，**走查一直是"绿的假象"**。
  补的 `content-syntax-applied` 直接量 `getComputedStyle` 的颜色。
  （第一版"去掉 `> .lt *` 后代选择器"那次变异**没红**，正是被这个洞掩盖的 ——
  顺带说明 `color` 不像 `background` 能叠，子元素自己声明了就不继承。）
- **去掉行号栏的 3px 色条** → 1 条红（`split-lineno-bg-follows-line`：阴影 `none`）。
  行号列是 sticky 的，横向滚动到正文看不见的地方时，那条色条就是认出行在哪的唯一线索。
- **去掉「最大化」按钮的点击绑定** → 7 条红（`content-max-fills-window` /
  `-bigger-than-before` / `-btn-flips` / `-resize-none` / `-drag-disabled` /
  `-restores` / `content-drag-works-after-restore`）。而 `content-max-btn-present`
  与 `-btn-initial` 保持绿 —— **"按钮画出来了"和"按钮能用"是两件事**；
- **最大化时忘了加 `.maxed` 类** → 1 条红（`content-max-resize-none`：
  面板铺满了却还挂着原生缩放手柄）；
- **最大化时仍允许拖标题栏** → 1 条红（`content-max-drag-disabled`）；
- **「还原」时回到默认尺寸而不是最大化前的几何** → 2 条红
  （`content-max-restores` + `content-drag-works-after-restore` 的基准跟着错）。
  **这条第一次跑没变红**：断言里最大化前没挪过面板，"默认几何"恰好等于
  "最大化前的几何"。补了一条 `content-max-pre-move`（先把面板挪走）才抓住 ——
  **变异"没变红"往往是"前提没造出来"，不一定是断言写弱了**；
- **`rememberContentSize` 不跳过最大化** → 1 条红
  （`content-max-does-not-poison-remembered-size`）。**这条也返工过一次**：
  第一版写成"最大化 → 还原 → 关闭 → 重开"，变异没变红 —— 还原那一步自己就把
  尺寸写回普通值了。改成"最大化 → **直接关闭**"才真的踩到守卫。
- **去掉预览面板的几何夹取** → 4 条红（`content-drag-clamps-left-top` /
  `-right-bottom` / `-clamp-keeps-grabbable` / `-clamp-survives-reopen`）：
  面板被拖到 `100180/100150`（视口只有 754×480），标题栏压根抓不到。
  **这条也返工过一次**：第一版两次拖动位移等大（都 99999），无夹取实现正好被
  抵消回视口内，只红 2～3 条 —— "还抓得住吗""关掉重开"两条白写了。把最后一次
  位移放大到 199999 并放到最后才全部踩到。**变异红得不够多，往往是"用例的取值
  让两种实现同解"**。

> 变异验证有脚本：`bash mutate.sh <文件绝对路径> <python 替换脚本>` ——
> 备份 → 注入 → 跑走查 → 列出变红断言 → 还原 → `diff -q` 校验还原。
> 手写"改-跑-还原"容易忘了还原或看漏红了几条，所以固化成脚本。
> 注意传**绝对路径**（脚本内部会先 `cd` 到自身目录）。

Go 侧同样做过变异验证（改 `app.go` / `internal/*` 确认测试变红，再还原）：

- **`myersOps` 的闸门退回 `maxD = n + m`** → 2 条红
  （`TestDeltasLargeFileChangedAtVeryStart` +
  `TestComputeSplitMarksLargeFileChangedAtVeryStart`）。第一次跑**没红**：
  改动落在文件中间时公共前缀把 `n+m` 压下去了，两种口径都能过 —— 于是专门补了
  "改动落在**第一行**"的两条测试才抓住。**变异"没变红"是断言写弱了的信号**，
  不是实现没问题的证据；
- **`Deltas` 连公共后缀一起剥** → 2 条红（`TestDeltasDoesNotTrimCommonSuffix`
  + `TestDiffGoldenAgainstJava` 132 条里红 22 条）。重复行让最小编辑脚本不唯一，
  剥后缀会改变 java-diff-utils 的选路 —— 所以只剥前缀（等价平移），这条测试专门
  钉住"别再顺手把后缀也剥了"；
- **`applyInline` 在没有行级标记时也贴行内标记** → 1 条红
  （`TestRenderMarkedIgnoresInlineWithoutLineMark`）。第一版断言写成
  `sg.Class == "mk-mod"`，变异后类名变成 `mk-`（mark 为空）照样绿 ——
  改成 `strings.Contains(sg.Class, "mk-")` 才抓住；
- **祖先链不剔掉根** → 3 条红（`TestLocateMapped*` 断言的链多一层）；
- **`NodeView.Mapped` 漏传** → 1 条红（`TestNodeViewCarriesMappedFlag`）。
- **`NodeView.Md5` 漏传（去掉 `Md5: n.GetMd5OrInit()`）** → 1 条红
  （`TestNodeViewCarriesMd5`：文件节点的 Md5 为空）。这类"字段没下发"的
  失效是**静默**的 —— 前端只是少显示一列，不报错。
- **去掉 `SetContent` 里的 `Md5` / `Size` 缓存清空** → 只有
  `TestSetContentResetsSizeAndMd5Cache` 1 条红，端到端那条**不红**。
  因为写出侧的大小是从实际数据算的（STORED 条目重算 crc/大小、DEFLATE 走数据
  描述符），压根不读 `n.Size`；真正被影响的是写进 patch-up sign 的 md5 /
  `OriginAppSize`。这类"后果不在最显眼处"的 bug 只能靠针对性单测守。
- **`looksBinary` 恒返回 `false`** → 2 条红（二进制文件被当文本打开、按文本存回）。
- **`EncodeString` 的 GBK 分支当 UTF-8 透传** → 3 条红（含
  `TestSaveContentHonorsCharset`）—— GBK 文件改一个字保存后整篇乱码。
- **编码器从 GB18030 退回严格 GBK**（即"顺手对齐原版"）→ 1 条红
  （`TestEncodeStringKeepsEmojiAsGB18030NotQuestionMark`），且 **`gui` 侧全绿**
  —— 说明这条刻意偏差光靠端到端抓不到，只有那条单测守着。
- **`.class` 不走源码分支**（去掉 `ParserClass` 那条判定）→ 1 条红
  （`TestOpenEditorClassUsesSourceMode`：模式 = `hex`）。这条正是"点编辑内容
  看到的是反编译源码"这个需求本身的守门人；
- **`SaveContent` 不拒 `source` 模式** → 1 条红
  （`TestSaveContentRejectsSourceMode`）。不拒的话源码文本会被当字节写进 class
  —— 当场看不出问题，要到运行时才炸，正是最该拦住的一类错；
- **前端隐藏「编译为 .class」按钮** → 4 条红（`edit-src-compile-btn` /
  `edit-src-compile-hint` / `edit-src-compile-no-dirty` / `edit-src-compile-call`）；
- **`CompileContent` 不锁 class 版本** → 1 条红（`TestCompileContentRoundTrip`：
  产出版本 = 65，期望 52。JDK 21 默认编出 65，原包跑在 Java 8 上直接炸）；
- **`CompileContent` 不校验产出集合** → 1 条红（`TestCheckCompiledSet`：
  多出/缺少内部类时应当拒绝替换）；
- **`compileAbility` 对内部类返回 `true`** → 2 条红
  （`TestCompileAbilityFlags` + `TestCompileContentRejectsInnerClass`）。
- **「保存源文件」菜单项不判 `.class`**（改成"非目录都给"）→ 6 条红
  （`TestPatchMenuClaimsFollowUpstream` 四例 + 主根那条 + "只挂补丁树"那条）。
  菜单是**逐节点**算的，claim 条件写松了就会到处冒出来；
- **默认文件名不换后缀**（出来是 `Foo.class.java`）→ `TestSourceSaveName` 1 条红。
  能存，但与原版不一致 —— 原版先 `getName()` 去掉原后缀再拼 `.java`；
- **`isClassNode` 不判目录** → `TestIsClassNode` 1 条红。原版 claim 里有
  `!isDir()`，少了它，名字像 class 的目录也会长出一项点下去没用的菜单；
- **`SaveSourceNodeAs` 不判 class** → `TestSaveSourceNodeAsIgnoresNonClass`
  1 条红。原版这里是**静默 return**，不弹错打断用户。