# PatchAssistant2J (Go)

`PatchAssistant2J` 的 Go 移植版。原项目是 JavaFX 桌面应用，Java 源码见同级目录 `../src-java`。

> **当前阶段：第三阶段（GUI）进行中**
> 阶段 1、2 已完成 —— 不依赖 GUI 的全部逻辑（领域模型、工具层、配置、jar/zip 建树、
> README 指令解析、补丁双模式匹配、应用包重打包与补丁签名、补丁包 README 写回）
> 均已落地，另有命令行入口可直接打补丁。
> 阶段 3 用 **Wails v2** 起了 GUI：主页双树 + 工具栏 + 设置弹窗已跑通，
> 支持把文件拖进窗口、两棵树的右键菜单（应用树 8 项 / 补丁树 13 项）、
> 底部「补丁信息」面板显示 README 正文与比对警告、**双击文件打开内容预览**
> （语法着色 / 双栏差异高亮 / 双栏滚动同步 / 差异模式 / 编码切换），
> **双击 `.class` 会反编译出 Java 源码**（**自带 Java 运行时，机器上不用装 Java**；同会话内重复打开同一 class 走内存缓存，不会每次都付 JVM 冷启动），
> **在补丁树点文件可在应用树里展开并定位到对应项**，
> **右键「编辑内容」可改包内文件（文本 / class 源码 / 十六进制，只改内存）**，
> 改完 `.class` 的源码后还可**「编译为 .class」写回包**（javac 同样来自内置运行时），
> **右键「保存源文件」把 `.class` 反编译后导出成 `.java`**，
> **搜索有三层入口**（树上打字即搜 / `Ctrl+F` 跨两棵树全局搜索 / 预览面板内
> `Ctrl+F` 查找与 `Ctrl+G` 跳转行列），预览面板的几何**一律夹在视口内**
> （拖不出去、抓得回来），
> 树的每一行带原版那套文件类型图标（目录 / jar / class / xml / README …），
> **文件行右侧显示修改时间 / 大小 / MD5**（用于逐项对比文件信息），
> **工具栏「对比」可逐文件比两个 jar/zip、也可比两个普通文件**（新功能，原版没有）：
> 两个包 → 汇总（内容不同 / 仅 A 有 / 仅 B 有 / 相同）+ 分组明细（路径 + 两侧大小与 MD5）；
> 两个普通文件 → 一行汇总（相同 / 不同）+ 两侧大小与 MD5，不同时点「查看差异」并排看；
> 报告可拷贝 / 保存；两个文件槽**也能直接把文件或包拖进去**；弹窗**可按住标题栏拖动、
> 可拖右下角缩放、也可一键最大化**（明细长的时候够看）；
> 包对比的明细里**每一行都能点开**看这个文件在 A / B 两侧的内容（两侧都有就并排对比，
> 只有一侧就单栏；`.class` 先反编译 —— 与预览面板同一套渲染与操作）；
> 按内容比 MD5，嵌套 jar（如 `BOOT-INF/lib/*.jar`）默认按普通文件算、**不预先递归展开**，
> 但明细里"内容不同"的嵌套包**可以点进去**再按条目比一次（面包屑 + 返回逐层钻取），
> 界面为亮色主题；设置弹窗按原版 `ConfigView` 分「搜索配置 / 补丁配置」两组，
> 已补「开启补丁文件比对」「保留历史条数」两项（主题 / 语言 / 保存结果到源目录 /
> 加载最后操作的补丁 暂未做，原因见 [MIGRATION.md](MIGRATION.md) §3.9）。READ ME 编辑器等屏待补。
> 完整进度与计划见 [MIGRATION.md](MIGRATION.md)；
> **Java↔Go 的标识符命名对照**（哪些沿用、哪些改过）见 [NAMING_AUDIT.md](NAMING_AUDIT.md)。

---

## 快速开始

```bash
go build -o pa ./cmd/patchassistant

# 以树形打印 jar/zip 内容
./pa list /path/to/app.jar

# 打印包内统计
./pa info /path/to/app.jar

# 解析补丁说明文件里的升级指令
./pa readme /path/to/README.txt

# 按补丁包给应用包打补丁（默认输出就是应用包自身，会覆盖 —— 建议显式 -o）
./pa patch -o /path/to/out.jar /path/to/app.jar /path/to/patch.zip

# 把文本文件写回补丁包的说明文件
./pa patchreadme /path/to/patch.zip /path/to/new-README.txt
```

`patch` 的选项（**必须写在位置参数之前**）：

| 选项 | 默认 | 作用 |
|---|---|---|
| `-o <路径>` | 应用包自身（同名同目录，**会覆盖**） | 输出路径 |
| `-path=<bool>` | `true` | 启用路径匹配 |
| `-name=<bool>` | `true` | 启用文件名匹配 |
| `-regex=<正则>` | `(.*\.class)\|(.*\.html)\|(.*\.js)\|(.*\.css)` | 参与匹配的补丁文件名正则 |
| `-sign=<bool>` | `true` | 是否写 `META-INF/patch-up/<yyyyMMdd>NN.sign` |
| `-user=<名字>` | 系统用户名 | 写进签名的操作人员 |
| `-readme=<名字>` | `README.txt` | 补丁说明文件名 |
| `-v` | 关 | 打印每一步进度 |

跑测试：

```bash
go test ./...
```

### 让 .jar 双击用 GUI 版打开

Wails 只产出单文件 exe、不写注册表，所以**双击 `.jar` 不会自动用它**。
原版是靠 GluonFX 的 installer（.msi）注册的，便携版没有 installer。

```powershell
# 在 patch-assistant-go/ 下运行，或先把脚本拷到 exe 旁边
powershell -ExecutionPolicy Bypass -File register-jar-association.ps1
```

它写**两个** ProgId 到 `HKCU\Software\Classes`（只写用户级，不需要管理员权限）：

- `PatchAssistant2J.JAR` —— 自己的 ProgId，并列进 `.jar` 的「打开方式」候选；
- `Applications\PatchAssistant2J.exe` —— Windows「打开方式」**真正按 exe 文件名
  查的是这个键**。Go 与 Java 的 exe 同名，这个键此前指向 Java 版（只传裸路径），
  不一起改的话，列表里那个同名条目点下去还是启动 Java 版、jar 不加载。
  原值会备份进同目录的 `PatchAssistant2J.exe.assoc-backup.txt`，撤销时还原。

撤销用 `unregister-jar-association.ps1`。

⚠️ **Windows 10/11 不允许程序静默把自己设成默认打开方式**（`UserChoice` 带
哈希保护）。脚本只能让你在"打开方式"里能选到它，最后那一下得你手动来：

> 右键任意 `.jar` → 打开方式 → 选择其他应用 → 选 PatchAssistant2J
> → 勾选「始终使用此应用」

之后双击 `.jar` 会用 `--target=<路径>` 启动 GUI 并自动加载该包。
**路径里带空格也没关系**（`%1` 展开可能不带引号、会被按空格拆进 argv，
Go 版会按"接回去真实存在"还原）；带裸路径启动也认
（`PatchAssistant2J.exe <路径>`）。原版这两条都是"能启动但不加载"，
Go 版补上了，见 MIGRATION.md 第 46 项。

---

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

GUI 就是上面那套逻辑的图形外壳，不新增业务规则 —— 打开应用包 / 打开补丁包 /
重新匹配 / 执行打补丁，对应 CLI 的建树与 `patch`。

「执行打补丁」弹保存对话框时，**默认文件名就是应用包自己的名字**、默认目录就是
应用包所在目录（对齐原版 `HomeView.handleSaveAppPack` 里的
`setInitialFileName(根节点名)`，**不带 `-patched` 之类的后缀**）。直接确定会被系统
提示覆盖原包 —— 原版就是这个行为，想另存请改名或换目录。

界面用**亮色**主题，走"现代工具风"（2026-10-14 按用户要求美化过一版）：
工具栏最左是应用图标 + 分组竖线、面板头带主色小色条 + 路径药丸、树行 24px、
状态栏的增/改/删是小药丸、滚动条是"浮起来"的一条、按钮是"有厚度的小块"
（半透明渐变 + 顶部内高光 + 底部内暗影，按下换成内凹阴影，图标按钮悬停时
边框才浮现）。所有明暗相关的颜色集中在
`gui/frontend/dist/app.css` 顶部的 `:root` 里，要换主题改这一块就够；
唯一要手动同步的是 `gui/main.go` 的 `BackgroundColour`（窗口出现那一瞬的底色，
不跟着改会闪一下）。

**应用名后面带作者署名**：窗口的**真实标题**（任务栏 / Alt-Tab 用）是
`PatchAssistant2J By CPoet`，对齐原版 `PatchAssistantApplication:41` 的
`stage.setTitle(I18nUtil.t("app.name", APP_NAME) + " By " + APP_AUTHOR_NAME)`
（原版中文下是「补丁助手 By CPoet」；Go 版界面文案不走 i18n、用英文应用名）。
窗口是**无边框**的（`gui/main.go` 的 `Frameless: true`，见 MIGRATION §五 第 53 项），
界面上看不到系统标题栏 —— 原来那条标题栏会重复显示一遍应用名，已去掉；它的三个
窗口按钮（最小化 / 最大化 / 关闭）搬到了工具栏右端、紧跟「执行打补丁」。
工具栏**最左边只有一个应用图标、没有文字**（2026-10-16 按用户要求去掉品牌名，
见 MIGRATION §五 第 58 项）—— 应用名 + 署名不再在界面里重复，只在窗口标题栏出现。
那个图标就是**应用图标本身**（绿色菱形 logo，`gui/build/appicon.png`；
界面里用的是它的裁边副本 `frontend/dist/brand.png`，换图标时两处一起换）。
`gui/main.go` 的 `windowTitle` 常量必须拿 `constant.AppAuthorName` 拼、
不能写死字符串，否则那个常量会被 Go 编译器当作未引用常量消除掉
（守门测试 `TestWindowTitleCarriesAuthor` / `TestWindowTitleBuiltFromConstants`）。

> 美化时**刻意没动**三块：代码预览区（纯白底 / 行号色 / 14px / 语法高亮配色）、
> 双栏差异的行底色与 `＋/－/~` 符号、树里"增/改/删"那套语义色。
> 它们是对齐原版 `theme.css` 的，走查按 RGB 逐条钉着（314 条里有一批就是量它们）。

**应用图标用的就是原版那套图案**（绿色菱形 + 四个孔 + 两片半透明扇形）。
原版只有 `src-java/src/main/resources/icon.png`（200×200，`stage.getIcons()`
用的窗口图标）与 `icon.ico`（**仅 64×64 单层** —— 实测原版 exe 的 PE 资源表里
`RT_GROUP_ICON` 就只有这一个条目）。这里从 200×200 的 PNG 重采样出 6 层
（16 / 32 / 48 / 64 / 128 / 256）写进 `gui/build/windows/icon.ico`：除 256 层是
1.28 倍放大外其余全是缩小，比原版那份 64×64 在大图标视图下被系统拉伸要清楚。
**图案没有重画**。生成脚本 `_extract/tools/make_appicon.py`（本机没有 PIL，
PNG 编解码是手写的，重采样用 Lanczos3 且在预乘 alpha 上做）。改图标后重跑它，
再 `wails build` 即可。

**也可以直接把文件拖进窗口**（对齐原版 `onDragOver` 的判定）：

| 拖到哪 | 接受什么 |
|---|---|
| 左侧「应用包」面板 | 1 个 `.jar` |
| 右侧「补丁包」面板 | 1 个目录，或 1 个 `.jar` / `.zip` |

判定**区分大小写**，且**必须恰好 1 个文件**（拖两个是拒的）—— 这是原版行为，
由 `gui/app_test.go` 的 `TestDropAcceptRulesMatchUpstream` 钉住。

> 实现上有个绕不开的点：浏览器出于安全，HTML5 拖放只给 `File` 对象、
> **不给真实路径**，而建树必须拿到路径。所以走 Wails 的原生拖放通道
> （`EnableFileDrop` + `window.runtime.OnFileDrop`），
> 详见 [MIGRATION.md](MIGRATION.md) 的「拖放：把文件拖进窗口」。

**补丁说明文件（README）必须在补丁包根目录下**，这是原版既有行为：
查找时只扫**补丁根的直接子节点**，所以下面这些形态自动认不出来 ——

| zip 里的形态 | 自动识别 |
|---|---|
| `README.txt` / `readme.txt` / `README.TXT`（在根下） | ✅ |
| `mypatch/README.txt`（套一层目录） | ❌ |
| `./README.txt`（`zip -r x.zip .` 的产物） | ❌ |
| `README.md`（非 `.txt` 且名字不匹配） | ❌ |

认不出来的话，**在补丁树上右键手动指定**（对齐原版）：

- **标记根节点** —— 把套的那层目录标成根，README 就成了它的直接子节点；
  再点一次取消标记。
- **标记说明文件** —— 直接把某个文件标成说明文件。

> 顺序有讲究：只有**直接挂在根下**的普通文件才能「标记说明文件」。
> 所以对套了目录的包，得**先标根**。详见
> [MIGRATION.md](MIGRATION.md) 的「标记根节点 / 标记说明文件」。

**被识别出来的那个说明文件在树上是高亮的** —— 行底色 + 左侧色条 + 名字后面
一个 `README` 小标签。原版只靠换成专门的 readme 图标来区分，那个图标我们现在
也有了（见下），所以这个文字标签是**多出来的**：留着是因为 16px 的图标夹在
长文件名里不够显眼。嫌冗余的话去掉那一段就行，图标那侧不受影响。

README 的正文显示在窗口**底部的「补丁信息」面板**里（对齐原版首页底部那块
TitledPane）。面板最多两段、顺序固定：比对有异常时先显示 `警告：` + 比对提示，
然后是 `说明：` + README 正文；只开了一侧包时只显示对应的那一段。
正文可以选中复制。

**补丁树右键菜单**整体对齐原版 `buildPatchTreeContextMenu`，共 13 项
（每项各自的显示条件、顺序、单选勾选态都与原版一致）：

| 菜单项 | 说明 |
|---|---|
| 重新加载 / 刷新信息 | 落在主根上重读整包；落在自定义根上只重跑映射（不重读 README） |
| 取消关联 | 清掉该节点及其子树的映射 |
| 标记为补丁根节点 | 单选；再点一次取消 |
| 标记为 README | 单选；仅目录模式下出现 |
| **编辑内容** ★ | 打开编辑器改包内文件内容（文本 / class 源码 / 十六进制），**只改内存** |
| 保存文件 | 把节点内容另存到磁盘 |
| 保存源文件 | 仅 `.class`；反编译成 Java 源码后另存为 `.java`（默认文件名把后缀换掉） |
| 复制 ▸ | 子菜单：名称 / 路径 / 相对路径 / 复制 MD5（目录时 MD5 置灰） |
| 查看签名 | 弹出补丁签名信息 |
| 查看绑定信息 | 弹出该节点与应用包对应节点的绑定明细 |
| 资源管理器中查看 | 定位到该节点在磁盘上的真实文件 |
| 关闭补丁 | 清掉映射并卸下补丁包 |
| 聚焦补丁 | 单选；隐藏应用包面板，只看补丁树 |

★ = 新增能力，**原版没有**（原版的「编辑模式」被作者整段注释掉了）。

**应用树右键菜单**同样对齐原版 `buildAppTreeContextMenu`（原版 13 项），
落地 8 项：重新加载、取消关联、复制 ▸、编辑内容 ★、保存文件、
资源管理器中查看、关闭应用包、聚焦应用树。未落地 5 项：新建目录、重命名、
删除、标记删除、保存为项目 —— 其中**保存源文件**只差一行菜单项
（后端 `SaveSourceNodeAs(side, path)` 已就绪，补丁树已接）。

还有 2 项补丁树菜单未落地：**保存为项目**（依赖 `JarDecompilerService`）、
**创建说明文件**（依赖 READ ME 编辑器）。

> 菜单是**后端算好下发**的，不是前端拼的 —— 原版每项的显示条件要看父级、
> 祖先链、两个包是否都打开、自定义根数量等，而树是懒渲染的，前端可能还拿不到
> 父节点。详见 [MIGRATION.md](MIGRATION.md) 的「补丁树右键菜单」。

#### 展开树与内容预览

**点名字就能展开/收起**，不必非得点那个小三角 —— 原版只有三角能点，
但那个三角只有 10px 宽，实际很难点中。

**每一行都带文件类型图标**，用的就是原版那套 PNG
（`src-java/src/main/resources/icons/` 里的 21 张 64×64，逐字节拷过来嵌进二进制）：
目录、`.jar` / `.zip`、`.class`、`.js` / `.html`、`.xml` / `.yml` / `.properties`、
Office 三件套、`.sign`，外加四个特例 —— Spring Boot 胖包的根、自定义根、
README、`META-INF/patch-up/`；另外两张（搜索按钮、搜索历史的时钟）不在树上，
见 `gui/icon.go` 的常量注释。目录名因此不再带尾斜杠（原版靠图标区分目录）。

> 选哪张图是**后端**算的（`gui/icon.go`），前端只负责贴上去 —— 判定不只看
> 后缀，还要看节点类型和路径。两个容易被误判成 bug 的点：**后缀匹配区分大小写**
> （`Foo.JAR` 拿不到 jar 图标，落到兜底的普通文件图标，原版就是这样）；
> `.txt` / `.idx` / `.MF` 这些**本来就没有**专属图标，落到普通文件图标是
> 原版行为，不是漏了。详见 [MIGRATION.md](MIGRATION.md) 的「树节点图标」。

**双击**的行为对齐原版 `HomeLeftTreeView` / `HomeRightTreeView`：

| 双击什么 | 行为 |
|---|---|
| 文件（`.java` / `.js` / `.html` / `.css` / `.xml` / `.yml` / `.sql` …） | 打开内容预览面板 |
| `.class` | 反编译成 Java 源码再预览（自带 Java 运行时，见下） |
| 嵌套 `.jar` / `.zip` | 展开它（原版 `buildChildrenWithCompress`） |
| 目录 | 展开它 |
| 无解析器的类型 | 交系统默认程序打开（原版 `openWithSystem`） |

嵌套压缩包是**惰性**展开的：`BOOT-INF/lib/*.jar` 只在"用得上"时才展开，
所以这类节点在数据里一个子级都没有。前端不能按"有 children 才画三角"来判断，
否则它会变成**死叶子** —— 点不开、里面的东西（包括补丁命中的高亮节点）
永远看不到。所以后端专门给了个 `compress` 标记。

**单击补丁树里带映射的文件行，会在应用树里展开并定位到对应项** ——
沿途没展开的目录逐层展开，目标那一行滚进视野并短暂高亮（3 秒）。
这是**原版没有的**（原版补丁树右键的「聚焦应用树」是显示模式开关，不是定位），
属刻意增强，见 [MIGRATION.md](MIGRATION.md) 的「在另一棵树里定位」。

> 几个实现上的要点：
> - 对应关系由**后端**顺着匹配阶段的 `MappedNode` 绑定算（双向都能查），
>   前端只负责展开与滚动 —— 懒渲染的 DOM 里没展开的层级连行都没有。
> - 只挂在**文件行**上，不挂目录（点目录的语义就是展开）；不带映射的行点了没反应。
> - 单击有 **200ms 防抖**，与"点名字展开"同一套路 —— 双击是打开内容预览，
>   不防抖的话双击会顺带触发一次定位。
> - 定位时**沿 DOM 逐层往下走**，而不是按路径查全局索引：嵌套 jar 内部子节点的
>   路径**不带 jar 前缀**（上游行为），胖包里两个 jar 各有一个 `org/` 是常态，
>   按路径查会走到别的 jar 里去。

预览面板**不铺遮罩**：原版的对话框是 `Modality.NONE`（非模态）且可缩放，
所以这里做成可拖动、可缩放的浮动面板，开着预览还能继续操作两棵树。
标题是 `左节点路径 <- 右节点路径`；两侧都有内容时是**双栏**对照，
右键可以切成**单栏差异模式**（行号列显示 `...` 与算出来的差异行号）。

标题栏右侧有**「最大化」按钮**（`□`，点一下铺满窗口、再点回到原样，图标变 `❐`）。
原版那边是 `dialog.setResizable(true)` —— JavaFX 会给可缩放的对话框挂上系统标题栏的
最大化按钮，所以这是**对齐原版**而不是新增功能。最大化时标题栏拖不动、缩放手柄收掉，
与系统窗口一致；**在最大化状态下关掉再打开会回到普通尺寸**（最大化不污染记忆尺寸）。

面板**拖不出视口**：横向至少留 72px、纵向标题栏整条必须可见。原版那个对话框是
**系统窗口**，拖到屏幕外还能靠系统窗口菜单 / 任务栏拉回来；这个浮层没有系统标题栏，
标题栏一旦离开视口就再也没有抓手（而位置又是记着的，关掉重开还是原地）。
主窗口被改小时也会重新夹一次。

**双栏里差异处会高亮** —— 左栏是应用包里被替换的旧文件、右栏是补丁里的新文件，
两侧各自标出自己这一侧的差异行：

| 颜色 | 含义 |
|---|---|
| 琥珀 | 这一块两边都有、内容改了 |
| 红（只在左栏） | 旧文件里有、新文件里没有 |
| 绿（只在右栏） | 新文件里新增的 |

行号列左侧一个 **`+` / `-` / `~` 符号**（一眼看出这行是增、删还是改；**加粗、
不随行号斜体**，比正文更抓眼），再加一道 **4px 同色竖条**（横向滚动时也能认出行号）；
**整行铺一层浅色底**（增浅绿 / 删浅红 / 改浅黄，扫一眼就知道差异在哪几行）；
**正文里只有真正变了的那几个词/片段染状态色**，其余文字保留语法着色 ——
既看得出"差异在哪几行"，也看得出"具体哪几个字变了"，不会把整屏染成同一个颜色。
纯新增 / 纯删除的行整行都是变化，整行染色。

> 呈现方式**返工过四次**，别再顺手改回去：
> 1. 先铺半透明底色 —— 用户要"背景改成白色"；
> 2. 改成整行文字染状态色 —— 用户又说"全部内容高亮，我需要的是关键词高亮就行"；
> 3. 定为"行号列色条 + 只染真正变了的词"；
> 4. 用户改主意，要"侧边显示 `+` / `-` 区分新增还是删除，并且显示这种高亮"
>    —— 于是加上符号与**行级底色**。
>    **第 4 步把第 1 步否掉的行底色又加回来了，这是用户明确改主意，不是旧代码回潮。**
> 用户第三次反馈时还牵出一个**真 bug**：大文件（两版行数之和 > 2000）即使只差
> 几行也会被整篇标成差异 —— 根因是 Myers 搜索的闸门错量成了 `n + m`，
> 详见 [MIGRATION.md](MIGRATION.md) 的「`maxMyersD` 闸门口径」。

> **这一点是原版没有的。** 原版双栏只摆两个代码区、不做任何标记
> （差异标记只出现在"差异模式"的单栏行号列上），所以这是**刻意的增强**，
> 已登记在 [MIGRATION.md](MIGRATION.md) 的偏差清单里。

**双栏的两栏上下滚动是同步的** —— 在任意一栏滚动，另一栏跟着走到同一位置。
只同步纵向（用户要的就是"上下滑动左右同步"），横向各栏按自己的行宽来。

> 同样是**原版没有的**（原版两个 `VirtualizedScrollPane` 各滚各的）。
> 两个实现上的坑：**不能用锁防回环**（无头浏览器里 `requestAnimationFrame`
> 会被饿死、锁卡住，改成"赋相同值不再触发事件、回环天然终止"）；
> **目标栏滚不到那么远时不硬同步**（否则长栏永远滚不过短栏底部）。

**双栏里能直接跳到下一处差异** —— 标题栏上有一对 `▲` / `▼` 与计数
（`1/2`）：点一下就把那一处滚进视口中部，并且**两栏各描一圈蓝框**
（一处差异在两栏里各有一个视角，只描一侧会让人以为另一边没变）。
手动滚动时计数也跟着走（取离视口顶部最近的那一块）。

> 为什么需要：行号列的 `+`/`-`/`~` 只能回答"屏幕上这几行是不是差异"，
> 回答不了"整份文件的差异都在哪" —— 差异只有几处却散在几千行里时，
> 用户只能一路上下翻着找（用户原话："差异处不太明显，得上下翻动才能查看
> 不同处"）。**这一对按钮是原版没有的**，属刻意增强。
>
> 差异块由**后端**给（`content.SplitBlock`）："哪几行属于同一处差异"是差异
> 算法的知识（一个"改"块里左第 k 行配右第 k 行），让前端扫 DOM 自己猜
> 等于把这个口径复制一份，迟早漂移 —— 与行内标记只在 `ComputeSplitMarks`
> 里算一次是同一个理由。单栏与差异模式没有块，导航整组隐藏。

**预览里能搜正文**：`Ctrl+F`（焦点在预览面板里时）弹出一条查找条，
输关键词后命中被染成黄带、当前命中是更深的橙带，`↑`/`↓` 在命中间跳转，
`Esc` 关闭。原版的 `SearchBox` 挂在 `CodeEditor` 上，而
`NodeCodeEditor extends CodeEditor`，所以**预览面板本来就有这个快捷键**。

> 实现上走了一条"不改 DOM"的路子：预览是只读的行渲染（不是富文本区），
> 所以在正文底层铺一个**隐藏的文本镜像**让查找逻辑原样工作，命中再用
> **CSS Custom Highlight API**（`::highlight`）画在渲染层上 —— 这样
> 反编译代码的语法着色完全不受影响。见 [MIGRATION.md](MIGRATION.md) §五 第 40 项。

**双栏对比里同样能搜**：打开对比后面板自己把焦点带进代码栏（对齐原版
`ContentView` 是模态对话框、一打开焦点就在代码区），此时 `Ctrl+F` 就是
面板内查找、两栏各搜各染；焦点在树上 / 面板外时 `Ctrl+F` 仍是跨两棵树的
全局搜索。曾因代码栏不可聚焦，"打开对比直接按 `Ctrl+F`"会错弹成全局搜索
对话框，与编辑模式不同步 —— 见 [MIGRATION.md](MIGRATION.md) §五 第 41 项。
> 见 [MIGRATION.md](MIGRATION.md) 的「双栏滚动同步」。

预览里右键的菜单项：复制 / 全选 / 取消选择 / 字符编码（GBK、UTF-8）/
差异模式（只有存在可比对的另一侧时才出现）。

> 两个容易误判成 bug 的点：**高亮语言按左节点的扩展名选**；
> `txt` / `properties` / `json` / `sign` 是"能预览但不着色" ——
> 这是原版 `getContentParser` 与 `getCodeAreaFactory` 两张扩展名表不对称
> 的结果，不是漏做。详见 [MIGRATION.md](MIGRATION.md) 的「文件内容预览」。

#### 改包内文件内容（右键「编辑内容」）

**原版没有这个功能**（原版的「编辑模式」菜单项被作者整段注释掉了，连
`codeArea.setEditable(false)` 都还留着），这是新增能力。两棵树的文件行都开放。

| 打开什么 | 用什么编辑 |
|---|---|
| `.class` | **反编译出来的 Java 源码**（可改；底部有「改字节」切到十六进制） |
| 有文本解析器、且内容里没有 NUL | 文本编辑器（等宽字体 + 语法着色 + 编码选择器） |
| 其余（二进制、带 NUL 的） | 十六进制编辑器（每页 512 字节，可翻页，右侧 ASCII 列） |

四条要点：

1. **只改内存，不动磁盘上的原包。** 保存后节点内容变了、树上那一行出现 `*` 星号，
   之后「执行打补丁」写出的产物用的就是新内容；**重新加载 / 重新匹配会丢弃改动**。
   想落盘用编辑器底部的「另存为」。
2. **编码要选对。** 原文件是 GBK 的，改完必须按 GBK 写回，否则这个包从这一刻起
   整段变乱码。所以文本编辑器带编码选择器，默认取探测结果。
3. **二进制按内容判，不按后缀**（前 8 KB 有 NUL 就算二进制）。UTF-16 文本会带 NUL，
   因此也走十六进制 —— 按文本编 UTF-16 本来就会写坏。
4. **`.class` 默认给源码，但源码不能存回 class。** 那里存的是字节码，源码要经
   `javac` 配合完整依赖重新编译才能变回去 —— 工具内做不到。所以源码形态
   **没有「保存」**，只有「另存为 .java」；只想改几个字节（比如替换一段字符串
   常量）时用底部的「改字节」。反编译自带 Java 运行时，失败时自动退回十六进制。

> 编辑器复用预览那个浮动面板（可拖动、可缩放、记忆尺寸），只在底部多一条
> 按钮条（还原 / 另存为 / 保存；`.class` 源码形态是 还原 / 另存为 .java / 改字节，
> 没有「保存」）。有未保存改动时关掉会提示一次。
> 详见 [MIGRATION.md](MIGRATION.md) 的「编辑包内文件内容」。

#### 编译为 .class（源码编辑器）

改完反编译出的 Java 源码后，点底部的「**编译为 .class**」可把改动写回包内。

整条链路：

1. 读取原 `.class` 的版本号（字节 6–7），据此让 javac 编出**同一个版本**
   （JDK 9+ 用 `--release N`；JDK 8 上 `--release` 这个选项还不存在，改用"不传版本参数"
   —— 它默认产出就是 class 8，正好是需要的）；
2. 把已打开的应用包摊平做 classpath（Spring Boot 胖包会先把 `BOOT-INF/lib/*.jar`
   解压到缓存目录，因为 javac 读不了 jar 套 jar）；
3. 调 javac 编译（javac 从**内置运行时**取），产出按文件名写回对应树节点（含内部类）；
4. 校验产出集合与树上节点一一对应 —— 多出/少掉都拒绝替换；
5. 成功刷新树（出现「已修改」星号），失败把 javac 报错原样展示。

**不需要本机装 JDK** —— javac 来自内置的 Java 运行时（见下「反编译」一节）。

想用自己装的特定 JDK（例如要验证 JDK 8 上编 Java 8 的类）时，设环境变量
`PATCH_ASSISTANT_NO_BUNDLED_JAVA=1` 跳过内置运行时，查找退回 `JAVA_HOME` → `PATH`。
此时**要编的类是什么版本，就需要不低于那个版本的 JDK**：原 class 是 Java 17 而本机
只有 JDK 8 时，会直接拒绝并说明原因，而不是悄悄降级编出一个版本对不上的 class。

内部类节点不能单独编译（源码本来就在外层类文件里），打开外层类改完一起编译即可。

> 这是**原版没有的能力**。原版作者把「编辑模式」菜单整段注释掉了，
> 源码只能看不能改。我们在「保存源文件」基础上又往前了一步：
> 改完能编回去。实测 mysql-connector-java 8.0.20（510 顶层类）抽 60 个做往返，
> **52 成功 = 87%**；失败原因全是反编译固有缺陷（局部变量名冲突、泛型重建不准、
> 桥接方法缺失），不是编译链路的问题。

---

不想进编辑器、只想把某个类导出来看的话，在**补丁树**的 `.class` 上右键 →
「保存源文件」，会反编译成 Java 源码后另存为 `.java`（默认文件名把后缀换掉，
`Foo.class` → `Foo.java`）。非 `.class` 的节点上不出现这一项。

两点与原版对齐、一处刻意偏差：

- 反编译**失败要报错**，不静默导出空文件（原版 `parse` 抛异常也是用户看得见的）。
  这和编辑器那边"失败就退回十六进制"不同 —— 那里至少还能按字节改。
- 导出的编码固定 **UTF-8**。原版写的是 `content.getBytes()`，即平台默认编码
  （Java 11 + 中文 Windows 上是 GBK），编不出的字符会被写成 `?`，属于有损。
- 应用树那份**还没接**（原版两棵树都有）。后端 `SaveSourceNodeAs` 是按 `side`
  参数写的，补上只差一行菜单项 —— `TestSaveSourceFileMenuOnlyOnPatchTree`
  把这件事显式钉住了，真要接的时候它会提醒你同步文档。

**双击 `.class` 会反编译出 Java 源码**（对齐原版 `ClassContentParser`）。

反编译用的是原项目自带的那份 Fernflower（`fernflower-1.3.0.jar`，已嵌进二进制），
所以结果与原版**逐字节一致**。

**不需要本机装 Java** —— 程序内嵌了一份用 jlink 裁出来的 JDK 21 运行时
（`java.base` + `jdk.compiler`，Microsoft Build of OpenJDK，GPLv2+CE）。首次使用时解压到
缓存目录（实测 **0.42 秒**，之后复用），反编译与「编译为 .class」都从这里取 java / javac。
代价是 exe 体积 **39.95MB**（内嵌运行时 zip 27.5MB）。

查找顺序是 **内置运行时 → `JAVA_HOME` → `PATH`**。设 `PATCH_ASSISTANT_NO_BUNDLED_JAVA=1`
可以跳过内置运行时、改用本机装的 Java —— 此时**版本要求是 11 或更高**：
那份 jar 里的 class 是 major 55（Java 11），Java 8 的 JVM 在加载主类时就退出，
只留下一句没头没脑的
`Error: A JNI error has occurred, please check your installation and try again`。
所以本程序会把 JVM 真正那句话（`UnsupportedClassVersionError`，含本机 class 版本上限）
翻出来，并提示"本机 Java 版本过低"。

内部类（`Foo$Bar.class`）会跟着外部类一起反编译 —— 这也是原版行为，
只喂主类的话内部类会是空壳。

> 为什么不把 JVM 内嵌进来：原版是 Java 程序，本来就在 JVM 里跑；Go 版是独立
> 可执行文件，内嵌 JVM 得走 cgo + libjvm，分发和构建都要复杂一大截。
> 取舍过程见 [MIGRATION.md](MIGRATION.md) §2.1。

#### 搜索

三层入口，与原版一一对应：

| 想做什么 | 怎么用 | 原版对应物 |
|---|---|---|
| 在当前这棵树里快速跳 | **直接在树上打字**（不必先点哪里），`Ctrl+Alt+↑/↓` 换命中，`Esc` 取消 | `FastSearchControl` |
| 跨两棵树找节点 | **`Ctrl+F`** 或工具栏那个放大镜按钮 | `SearchView` |
| 在预览/编辑器里找一段文字 | **`Ctrl+F`**（焦点在预览面板里时） | `SearchBox` |
| 跳到第 N 行第 M 列 | **`Ctrl+G`**（焦点在预览面板里时） | `GotoRowColDialog` |

全局搜索的规则与原版一致：

- 输入即搜，**区分大小写**；
- 关键词里带 `*` 当通配符，且是**全匹配** —— `*.js` 是"以 `.js` 结尾"，
  不是"包含 `.js`"；
- 关键词里带 `/`（或 `\`）按**路径**找，沿父子链逐级匹配，
  `com/example/Foo.js` 就写全路径；中间遇到没展开的嵌套 jar 会**现场解压**进去找，
  一条都没命中就把解压结果收回去（不会在你搜完发现树里多了个展开的 jar）；
- 搜索框空着时，列表里是**搜索历史**：单击一条把关键词填回搜索框，
  双击结果跳到对应的树并展开沿途目录；
- 结果行右边是节点图标；历史行右边是时间 + 时钟图标。

> 搜索**本身在后端**（`gui/search.go`）跑，前端只画结果 —— 原版遍历的是内存里的树，
> 路径式搜索还要能解压嵌套 jar，前端只有一份懒渲染的 DOM，两样都做不到。
>
> 顺带修掉原版两个真 bug：树上快速搜索的 `Ctrl+Alt+↑` 在原版显示的其实是**下一项**
> （`getAndUpdate` 取的是旧值）；跳转行列对非法输入会把输入框内容**改写成「无效」
> 两个字**，用户没法接着改 —— 这里改成标红边框、保留输入。
> 详见 [MIGRATION.md](MIGRATION.md) 的「搜索：三层入口」。

搜索历史**只活在本次会话**里（原版写进 `application.xml`）。Go 版 GUI 目前没有
配置文件，其它设置项也一样 —— 接上配置文件时直接换数据来源即可。

`Ctrl+S`（保存 / 打补丁）**还没接**，见 [MIGRATION.md](MIGRATION.md) §四。

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

### 对照跑 Java 原版（用于核对行为）

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

## 目录结构

包名与 Java 侧一一对应，便于双向对照：

| Go 包 | 对应 Java 包 | 说明 |
|---|---|---|
| `internal/constant` | `cn.cpoet.patch.assistant.constant` | 常量与枚举 |
| `internal/model` | `...model` | 补丁签名、变更记录、README 路径信息 |
| `internal/util` | `...util` | 字符串/路径/哈希/字符集/系统工具（仅非 UI 部分） |
| `internal/config` | `...core`（配置部分） | `Configuration` 及四个子配置 |
| `internal/tree` | `...control.tree.node` | 树节点与同级顺序（插入序，**不排序**） |
| `internal/pack` | `...service.BasePackService` + `service.compress` | zip/jar 展开成树 |
| `internal/i18n` | `...util.I18nUtil` | `go:embed` 打包 i18n 资源 |
| `internal/service` | `...service` | 业务服务：README 解析、补丁匹配、应用包/补丁包写出 |
| `internal/decompile` | `...fernflower` | 调 Fernflower 反编译 `.class`（jar 内嵌，见 MIGRATION §2.1） |
| `internal/javac` | —（Go 侧新增） | 调 javac 把源码编回 `.class`（原版没有这个能力） |
| `internal/javaruntime` | —（Go 侧新增） | 内嵌最小 JDK 运行时（jlink：`java.base`+`jdk.compiler`），首次使用解压到缓存目录 |
| `gui` | `view` + `control` | Wails GUI：绑定层 `app.go`、搜索 `search.go`、内容/编辑器 `content.go` / `editor.go`、图标 `icon.go`、前端 `frontend/dist/` |
| `cmd/patchassistant` | `PatchAssistantApplication` | 命令行入口 |

---

## 设计取舍

**树节点用单一 struct 而不是五个类型。** Java 里 `TreeNode` 是抽象类，派生出
`CompressNode` / `FileNode` / `FileDirectNode` / `VirtualNode` / `MappedNode`，
但子类差异只有两点：是否目录、内容从哪来。Go 版用 `tree.Node` + 可插拔的
`tree.DataSource` 表达，少一层类型噪音。

**zip 内容惰性读取，不再落盘。** Java 的 `CompressNodeFactory.creteNodeFile`
在建树阶段就把每个条目解压写到临时目录并算好 md5 —— 对一个上百 MB 的 fat-jar
意味着建树即全量落盘。Go 版持有 `*zip.File` 惰性读取，md5 按需计算。
语义等价，且不再产生临时文件。

**保留上游的"怪行为"。** 凡是与 Java 版行为不一致的地方，都当成 bug 处理，
不"顺手修好"。例如 README 前缀必须紧贴路径、`isBlank` 不认 `\t\n\r` ——
这些都已写进注释和回归测试，防止后续重构时分叉。

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
