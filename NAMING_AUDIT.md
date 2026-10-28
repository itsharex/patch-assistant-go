# Java → Go 标识符命名对照审计

> **目的**：确认 Go 移植版里的"内部名字 / 标识"是否沿用 Java 端，方便 Java 开发者
> 对照阅读、维护其知识权益。
>
> **口径**：本文档**只做取证与登记**，不含任何回改。是否回改由看过清单后再定。
>
> **取证方法**：对两侧源码做机械提取（常量声明 / 方法签名 / XML 注解 / CSS 选择器 /
> 文件名），逐项比对，不靠记忆。数据来源：Java 侧 148 个 `.java` + 全部资源文件；
> Go 侧 73 个 `.go`（含测试）+ 前端产物。
>
> **分类**：
> - **A 类** 逐字沿用 —— 字符级完全一致
> - **B 类** 大小写 / 风格转换 —— 字母完全相同，只是命名约定不同
> - **C 类** 已改名 —— 语义等价但名字不同
> - **D 类** Go 侧新增 —— Java 没有对应物
> - **E 类** Java 有、Go 未以同名单元移植 —— 由标准库 / Wails / 前端 / 外挂工具承担

---

## 0. 结论摘要

| 类别 | 规模 | 判断 |
|---|---|---|
| A 逐字沿用 | 大 | **所有"跨语言边界"的标识全部逐字沿用**：i18n 键名、配置 XML 元素名、图标文件名、语法高亮 CSS 类名、枚举值字面量、调试用类型名 |
| B 风格转换 | 大 | 常量 `UPPER_SNAKE` → `UpperCamel`；方法 `camelCase` → `PascalCase`。**字母一字不差，纯约定差异** |
| C 已改名 | 中 | 集中在常量前缀（`ZIP` → `ExtZip`）、枚举类型名（`ChangeTypeEnum` → `ChangeType`）、少数方法（`getName` → `Name`） |
| D Go 新增 | 中 | 差异视图、jar 对比、反编译、javac、前端渲染等**原版没有的能力** |
| E 未移植 | 大 | 整个 JavaFX 视图层（`view/*`、`control/*`）、`fernflower/*`、若干 UI 工具类 |

**一句话**：**凡是"要跨进程 / 跨文件 / 跨语言"的标识，一律逐字沿用**（所以配置可互读、
i18n 资源可原样复用、图标可原样拷贝）；**凡是"只在某一侧内部使用"的标识，按各语言
惯例重写**。唯一需要留意的是常量与枚举——它们的**字母本身**有若干处被改过，
Java 开发者按原名去 Go 里搜会搜不到（见 §3）。

**另有两条与"知识权益"直接相关、需要单独决策的发现**（详见 §1.9 与 §8.4）：

1. **作者信息在常量层逐字沿用**（`AppAuthorName = "CPoet"` / `AppAuthorEmail` /
   `AppAuthorURL` 值与 Java 一字不差），**但在 Go 版里没有被"露出来"** ——
   没有关于页、Go exe 里搜不到 `CPoet`。**已补**：各包包注释加了出处声明，
   `gui/wails.json` 的 `comments` 与 exe 版本信息的 `Comments` 字段写出处。
2. **Go 侧原本没有 LICENSE**（Java 侧是 Apache-2.0 + `Copyright [2025] [CPoet]`）。
   **已补**：`patch-assistant-go/LICENSE` 与 `src-java/LICENSE` 逐字节一致。
   另外 `go.mod` 的 module 名已从 `patchassistant` 改为 `cn.cpoet.patch.assistant`
   （对齐 Java 的 `groupId`）。

> **执行状态汇总**（2026-10-15）：§8.4 中的 **LICENSE / wails.json 版权 / 源码出处注释 /
> go.mod 模块名前缀** 四项**已执行**；**前端「关于」页 + 工具栏按钮未做**（会动前端与
> 走查断言，代价最大）。

---

## 1. A 类 —— 逐字沿用（字符级完全一致）

### 1.1 i18n 键名（Java 143 个 / Go 144 个）

Java `src-java/src/main/resources/messages/i18n_{en-US,zh-CN}.properties`
↔ Go `patch-assistant-go/internal/i18n/messages/i18n_{en-US,zh-CN}.properties`

| | Java | Go |
|---|---|---|
| 键总数（每语言） | **143** | **144** |
| Java 有 Go 无 | — | **0 个**（Java 的 143 个键 Go 全部保留） |
| Go 有 Java 无 | — | **1 个**：`app.view.tree.edit-content`（Go 新增"编辑内容"功能） |

**Java 的 143 个键全部逐字保留，一个没删、一个没改名。** Go 只**多**了一个键。

键名分布（前缀 → 个数）：`app.view.*` 是大头，另有 `app.alter.*`（4）、
`app.common.*`（4）、`app.control.code.*`（6）、`app.language.*`（2）、`app.name`、
`app.service.app-pack.diff-file-tip.*`（2）。

完整键名清单可脚本导出（见文末「审计可复现步骤」）。
**校验**：两侧键名集合做差集，`Java - Go = ∅`、`Go - Java = {app.view.tree.edit-content}`。

资源文件本身是从原版 exe 二进制里**原样提取**的，用 `go:embed` 打包。

> **取证教训（第一版审计写错过）**：键名里含连字符（`app.view.about.author-main-page`、
> `app.view.config.genera-lang.en-US`、`app.view.config.search-save-history-limit` …）。
> 用 `^([\w.]+)\s*=` 提取会**在连字符处截断而整条漏掉**，第一版因此误报成"各 24 个键"。
> 正确正则要带 `\-`：`^\s*([A-Za-z][\w.\-]*)\s*=`。
>
> 另外：Java 的 `ConfigView` 里拼了 `app.view.config.genera-lang.<code>` /
> `genera-theme.<code>` 这类**动态拼接**的键 —— 这两个"父键"在 `.properties` 里**是有的**，
> 且**逐字保留**在 Go 的 properties 里；只是 Go 侧没有代码去拼接使用它们（见 §5）。

### 1.2 配置文件 XML 元素名（含原版拼写错误）

Java `core/Configuration.java` 的 `@JacksonXmlProperty` 字段名
↔ Go `internal/config/config.go` 的 `xml:"..."` 标签。

**逐字沿用，包括原版的拼写错误 `patchSighHeight`（少一个 `n`）**：

| XML 元素名 | Java 字段 | Go 字段 |
|---|---|---|
| `homeWidth` / `homeHeight` | `Double homeWidth` | `float64 HomeWidth` |
| `configWidth` / `configHeight` | `Double configWidth` | `float64 ConfigWidth` |
| `searchWidth` / `searchHeight` | `Double searchWidth` | `float64 SearchWidth` |
| `contentWidth` / `contentHeight` | `Double contentWidth` | `float64 ContentWidth` |
| `nodeMappedWidth` / `nodeMappedHeight` | `Double nodeMappedWidth` | `float64 NodeMappedWidth` |
| `readmeWidth` / `readmeHeight` | `Double readmeWidth` | `float64 ReadmeWidth` |
| `patchSignWidth` | `Double patchSignWidth` | `float64 PatchSignWidth` |
| **`patchSighHeight`** | `Double patchSighHeight` ← 原版拼错 | `float64 PatchSighHeight` ← 照抄 |
| `isSelectedLinked` | `Boolean isSelectedLinked` | `bool IsSelectedLinked` |
| `isOnlyChanges` | `Boolean isOnlyChanges` | `bool IsOnlyChanges` |
| `isShowFileDetail` | `Boolean isShowFileDetail` | `bool IsShowFileDetail` |
| `showPatchInfo` | `Boolean showPatchInfo` | `bool ShowPatchInfo` |
| `focusTreeStatus` | （`AppContext`） | `int FocusTreeStatus` |
| `lastAppPackPath` / `lastPatchPackPath` / `lastSavePackPath` / `lastJarDecompilePath` | 同名字段 | 同名字段 |
| `genera` / `content` / `search` / `patch` | 四个子配置对象 | 四个子配置对象 |
| `language` / `theme` | `GeneraConf` | `GeneraConf` |
| `diffModel` | `ContentConf` | `ContentConf` |
| `username` / `readmeFile` / `pathMatch` / `fileNameMatch` / `patchFileRegex` / `writePatchSign` / `patchFileDiff` / `loadLastPatch` / `saveToSourceDir` | `PatchConf` | `PatchConf` |
| `historyLimit` / `history` / `history>history` | `SearchConf` | `SearchConf` |
| `path` / `name` / `searchTime` / `target` | `SearchItem` / `SearchHisItem` | 同名 |

**这条最关键**：因为元素名逐字沿用，**同一份 `application.xml` 可以被 Java 版与
Go 版互读**（刻意保持，方便两侧共存过渡）。

### 1.3 图标文件名（21 个，逐字节一致）

Java `src-java/src/main/resources/icons/`（21 个 PNG）
↔ Go `gui/frontend/dist/icons/`（21 个 PNG）

`ls` 逐项比对结果：**文件名集合完全相同，无差异**。

```
directory.png   file-docx.png   file-jar.png    file-mark.png     file-pptx.png
file-class.png  file-html.png   file-js.png     file-properties.png
file-xlsx.png   file-xml.png    file-yaml.png   file-yml.png      file-zip.png
file.png        patch-up.png    patch.png       readme.png        search.png
spring-boot.png time.png
```

图标是**原版那 21 张原样拷过来**的（64×64，逐字节校验过），没有重画。
Java 的 `IConConst.*` 常量名（`DIRECTORY` / `FILE_JAR` / …）**未沿用**，
Go 侧换成 `iconDirectory` / `iconFileJar` / …（见 §3.1），但**文件名本身一字不改**。

> 路径写法有一处小差异：Java 常量带前导斜杠（`"/icons/file-jar.png"`），
> Go 是相对路径（`"icons/file-jar.png"`）——因为 Go 侧由前端 `img.src` 解析，
> 相对 `index.html` 定位。

### 1.4 语法高亮 CSS 类名（7 种语言，全部逐字沿用）

Java `css/{java,javascript,css,html,sql,xml,yaml}-style.css`
↔ Go `internal/content/highlight.go` + `gui/frontend/dist/app.css`

**token 类名一字不改**，只是容器前缀从 `.code-area` 换成 `.lang-*`：

| 语言 | 逐字沿用的类名 |
|---|---|
| java / javascript | `.keyword` `.semicolon` `.paren` `.bracket` `.brace` `.string` `.comment` |
| css | `.keyword` `.property` `.punctuation` `.selector` `.terminator` `.value` `.comment` |
| html | `.html-tag` `.html-attribute` `.html-string` `.html-comment` `.html-doctype` |
| sql | `.keyword` `.number` `.string` `.comment` |
| xml | `.xml-tag` `.xml-attribute` `.xml-string` `.xml-comment` |
| yaml | `.yaml-key` `.yaml-keyword` `.yaml-string` `.yaml-number` `.yaml-comment` `.yaml-alias` `.yaml-anchor` |

**只有容器前缀与两个 JavaFX 专有选择器变了**：

| Java | Go | 原因 |
|---|---|---|
| `.code-area .keyword` | `.lang-java .keyword` | 一个 CodeArea 换成一个 `<code>` 容器 |
| `.code-area .lineno` | `.code .ln` | 行号列的元素名 |
| `.code-area .paragraph-box:has-caret` | — | RichTextFX 专有，浏览器没有对应物 |

### 1.5 枚举值字面量

| Java | Go | 值 |
|---|---|---|
| `ChangeTypeEnum.NONE/ADD/MOD/DEL/IGNORE` | `constant.ChangeNone/ChangeAdd/ChangeMod/ChangeDel/ChangeIgnore` | `""` `"+"` `"!"` `"-"` `"?"` |
| `I18NEnum.EN_US/ZH_CN` | `constant.I18NEnUS/I18NZhCN` | `"en-US"` `"zh-CN"` |
| `ThemeEnum.LIGHT/DARK` | `constant.ThemeLight/ThemeDark` | `"light"` `"dark"` |
| `I18NEnum` 的 `name` | 同 | `"英文(默认)"` / `"简体中文"` |
| `ThemeEnum` 的 `name` | 同 | `"明亮主题"` / `"暗夜主题"` |

> `ChangeTypeEnum.IGNORE = "?"` 的语义注释（"Ignore this patch file"）也一并保留。

### 1.6 调试用类型名（`String()` 的返回值）

**Go 把 Java 的 5 个节点类压成一个 `tree.Node`，但把 Java 的类名原样搬进了调试名表**：

```go
// Go internal/tree/node.go
var KindNames = [...]string{"FileNode", "FileDirectNode", "CompressNode", "VirtualNode", "MappedNode"}
```

对应 Java 的 `control/tree/node/{FileNode, FileDirectNode, CompressNode, VirtualNode, MappedNode}.java`。

同理 `constant.TreeNodeTypeNames` 与 Java `TreeNodeType` 的枚举常量名**逐字一致**：

```go
var TreeNodeTypeNames = [...]string{"NONE", "ADD", "MOD", "DEL", "MANUAL_DEL", "README", "ROOT", "CUSTOM_ROOT"}
```

> 但 `TreeNodeType` 的**序号刻意不同**（Go 是 `NONE,ADD,MOD,DEL,MANUAL_DEL,README,ROOT,CUSTOM_ROOT`；
> Java 是 `ADD,DEL,MOD,MANUAL_DEL,README,ROOT,CUSTOM_ROOT,NONE`）。这是**已登记的刻意偏差**：
> `TreeNodeType` 只存在于内存、从不参与序列化，序号不影响输出保真度。见 `enum.go` 注释。

### 1.7 类型名（Java 类名 = Go 导出类型名，字符级一致）

| Java 类 | Go 类型 | 位置 |
|---|---|---|
| `model.PatchSign` | `model.PatchSign` | `internal/model/model.go` |
| `model.PatchUpSign` | `model.PatchUpSign` | 同上 |
| `model.AppPackSign` | `model.AppPackSign` | 同上 |
| `model.AddInfo` / `DelInfo` / `ModInfo` / `HashInfo` | 同名 | 同上 |
| `model.Application` | `model.Application` | 同上 |
| `control.tree.PatchRootInfo` | `tree.PatchRootInfo` | `internal/tree/info.go` |
| `control.tree.PatchTreeInfo` | `tree.PatchTreeInfo` | 同上 |
| `control.tree.AppTreeInfo` | `tree.AppTreeInfo` | 同上 |
| `control.tree.TotalInfo` | `tree.TotalInfo` | `internal/tree/total.go` |
| `model.ReadMePathInfo` | `tree.ReadMePathInfo` | `internal/tree/readme_path.go` |
| `constant.TreeNodeType` | `constant.TreeNodeType` | `internal/constant/enum.go` |
| `service.PatchMatchProcessor` | `service.PatchMatchProcessor` | `internal/service/match.go` |
| `core.Configuration` | `config.Configuration` | `internal/config/config.go` |
| `core.GeneraConf` / `ContentConf` / `PatchConf` / `SearchConf` | 同名 | 同上 |
| `core.SearchItem` / `SearchHisItem` | 同名 | 同上 |
| `model.Application` 的字段名 | 同名字段 | 同上 |

### 1.8 未导出函数 / 变量名（逐字沿用）

| Java | Go |
|---|---|
| `FileNameUtil.uniqueFileName` | `service.uniqueFileName`（未导出） |
| `FileNameUtil` 私有字段 `pathSeparatorPattern` | `util.PathSeparatorPattern` |
| `TreeNodeUtil.isSpringBootNode` | `gui.isSpringBootNode` |
| `Configuration` 的字段名 | `config.Configuration` 的 Go 字段名（`HomeWidth` 等，仅首字母大写） |

### 1.9 作者信息与出处（provenance）

> 单列一节，因为这是"维护 Java 端开发者知识权益"最直接相关的部分。

**常量值：逐字沿用，一字未改。**

| Java `AppConst` | 值 | Go `constant` | 值 | 是否相同 |
|---|---|---|---|---|
| `APP_AUTHOR_NAME` | `CPoet` | `AppAuthorName` | `CPoet` | ✅ |
| `APP_AUTHOR_EMAIL` | `llzero54@foxmail.com` | `AppAuthorEmail` | `llzero54@foxmail.com` | ✅ |
| `APP_AUTHOR_URL` | `http://www.cpoet.cn` | `AppAuthorURL` | `http://www.cpoet.cn` | ✅ |
| `APP_NAME` | `PatchAssistant2J` | `AppName` | `PatchAssistant2J` | ✅ |
| `APP_PROJECT_NAME` | `patch-assistant-to-java` | `AppProjectName` | `patch-assistant-to-java` | ✅ |

**但署名"有没有真正露出来"，两侧差别很大：**

| 载体 | Java | Go |
|---|---|---|
| 作者常量 | `APP_AUTHOR_*`，**被 `AboutView` 使用** | `AppAuthorName/Email/URL`，**全项目零使用点（死常量）** |
| 「关于」页 | `view/about/AboutView.java`：应用图标 + 名称/版本 + `Build #<buildTime> <artifactId>` + 描述 + `作者：CPoet` + `邮箱：llzero54@foxmail.com` + 三个超链接（作者首页 / 使用文档 / GitHub `github.com/c-poet/patch-assistant-to-java`） | **没有**。i18n 的 7 个 `app.view.about.*` 键**原样保留在 properties 里**，但**代码零引用**；工具栏也没有"关于"按钮（Java `HomeView` 第 89 行有 `aboutBtn`） |
| 源码注释 | 148 个 `.java` 里 **145 个**带 `@author CPoet` | **0 个 `@author`**。替代做法：约 11 处包注释写"对应 Java 侧 `cn.cpoet.patch.assistant.xxx`"（指向性出处，非署名） |
| 构建元数据 | `pom.xml`：`groupId=cn.cpoet.patch.assistant`、`artifactId=patch-assistant-to-java`、`version=1.4.7`、`<name>PatchAssistant2J`、GluonFX `<vendor>CPoet` | `wails.json`：`author.name = "CPoet"` ✅、`companyName/productName = PatchAssistant2J`、`productVersion = 1.4.7` ✅、**`comments = "Java 版 PatchAssistant2J 的 Go 移植"`**（明确出处）；`go.mod` module = `patchassistant`（**去掉了 `cn.cpoet.` 前缀**） |
| 许可证 | `src-java/LICENSE`：**Apache License 2.0**，`Copyright [2025] [CPoet]` | **无 LICENSE 文件、无任何许可声明** |
| 应用信息文件 | `PatchAssistant2J.xml`（`name/version/grouopId/artifactId/userName/buildTime`，Maven 过滤后打进包） | **未移植**（`AboutView` 靠它显示版本与构建号） |
| exe 版本信息资源 | **没有** `VS_VERSION_INFO`（GluonFX native-image 不写）→ 右键属性→详细信息为空 | **有**：`Comments = Java 版 PatchAssistant2J 的 Go 移植`、`CompanyName/ProductName/FileDescription = PatchAssistant2J`、`ProductVersion = 1.4.7`；但 `LegalCopyright = Copyright.........` ← **模板占位符没被替换**（`wails.json` 的 `info` 块缺 `copyright` 字段） |

**实测二进制对照**（直接扫 exe 字节）：

| 字符串 | Java exe（80 MB） | Go exe（12.98 MB） |
|---|---|---|
| `CPoet` | ✅ ASCII ×2 | ❌ **没有** |
| `llzero54` | ✅ ASCII ×1 | ❌ 没有 |
| `cpoet.cn` | ✅ ASCII ×2 | ❌ 没有 |
| `patch-assistant-to-java` | ✅ ASCII ×3 | ✅ ASCII ×44（来自 `constant.AppProjectName`、`patch-up` 目录名等） |
| `Apache` / `License` / `LICENSE` | ✅ ×9 / ×160 / ×100 | ❌ 没有 |
| `Java 版 … 的 Go 移植` | ❌ | ✅ UTF-16 ×1（PE 版本资源的 `Comments`） |

> **为什么 Go exe 里没有 `CPoet`**：`AppAuthorName` 是死常量 —— Go 编译器会把
> **未被引用的包级常量**从二进制里消除；而 `wails.json` 的 `author.name` 只进 Wails
> 自己的元数据，`gui/build/windows/info.json` 模板里**没有 Author 字段**，所以不落 PE 资源。
> Java exe 里的 `CPoet` 来自 `AppConst` 常量 + `LICENSE` 文本被 native-image 打进包里。

**结论**：作者信息在**常量层**逐字沿用（值一字未改），但在 **Go 版里没有被"露出来"** ——
没有关于页、没有 `@author`、没有许可证、exe 里也搜不到作者名。**唯一的显式出处声明**
是 `gui/wails.json` 的 `comments` 与 exe 版本信息的 `Comments` 字段。
详见 §8.4 的可选补救清单。

---

## 2. B 类 —— 大小写 / 风格转换（字母完全相同）

这一类的共同特征：**把 Java 名首字母大写、去掉下划线就是 Go 名**。
Java 开发者按"去掉下划线、首字母大写"就能搜到。

### 2.1 常量：`UPPER_SNAKE` → `UpperCamel`

> `internal/constant/constant.go` 的包注释写的是"命名保持一一对应，便于与 Java 源码对照"。
> 实测：**字母一一对应，但大小写与分隔符按 Go 惯例重写了**。

| Java 常量 | Go 常量 | 所在 Java 文件 |
|---|---|---|
| `APP_NAME` | `AppName` | `AppConst` |
| `APP_PROJECT_NAME` | `AppProjectName` | 同上 |
| `APP_LOCK_NAME` | `AppLockName` | 同上 |
| `APP_AUTHOR_NAME` / `APP_AUTHOR_EMAIL` / `APP_AUTHOR_URL` | `AppAuthorName` / `AppAuthorEmail` / `AppAuthorURL` | 同上 |
| `CONFIG_FILE_NAME` | `ConfigFileName` | 同上 |
| `APPLICATION_INFO_NAME` | `ApplicationInfoName` | 同上 |
| `STYLE_FILE_NAME` / `STYLE_DARK_FILE_NAME` | `StyleFileName` / `StyleDarkFileName` | 同上 |
| `README_FILE` | `ReadmeFile` | 同上 |
| `PATCH_UP_DIR` | `PatchUpDir` | 同上 |
| `ERROR_LOG_FILE` | `ErrorLogFile` | 同上 |
| `DOT_ZIP` / `DOT_JAR` / `DOT_CLASS` / `DOT_JAVA` / `DOT_TXT` / `DOT_SIGN` | `DotZip` / `DotJar` / `DotClass` / `DotJava` / `DotTxt` / `DotSign` | `FileExtConst` |
| `META_INFO` | `MetaInfo` | `JarInfoConst` |
| `META_INFO_DIR` | `MetaInfoDir` | 同上 |
| `MANIFEST_FILE` / `MANIFEST_PATH` | `ManifestFile` / `ManifestPath` | 同上 |
| `PATCH_UP_PATH` | `PatchUpPath` | 同上 |
| `SOURCE_SRC` / `SOURCE_LIB` | `SourceSrc` / `SourceLib` | 同上 |
| `MVN_POM_XML` | `MvnPomXml` | 同上 |
| `BOOT_INF_PATH` | `BootInfPath` | `SpringConst` |
| `CLASSES_PATH` / `LIB_PATH` | `ClassesPath` / `LibPath` | 同上 |

### 2.2 方法：`camelCase` → `PascalCase`（导出）

| Java | Go | 类 |
|---|---|---|
| `FileNameUtil.getDirPath` | `util.GetDirPath` | `util/path.go` |
| `FileNameUtil.joinPath` | `util.JoinPath` | 同上 |
| `FileNameUtil.perfectDirPath` | `util.PerfectDirPath` | 同上 |
| `FileNameUtil.unPerfectDirPath` | `util.UnPerfectDirPath` | 同上 |
| `FileNameUtil.splitPath` | `util.SplitPath` | 同上 |
| `TextDiffUtil.diff2Str` | `util.Diff2Str` | `util/textdiff.go` |
| `TextDiffUtil.diffJoin` | `util.DiffJoin` | 同上 |
| `HashUtil.toHexStr` | `util.ToHexStr` | `util/hash.go` |
| `HashUtil.md5` / `sha1` | `util.Md5` / `util.Sha1` | 同上 |
| `HashUtil.getHashInfo` | `util.GetHashInfo` | 同上 |
| `StringUtil.isEmpty` / `isBlank` | `util.IsEmpty` / `util.IsBlank` | `util/string.go` |
| `StringUtil.lastIndexOfAndCount` | `util.LastIndexOfAndCount` | 同上 |
| `OSUtil.isWindows` / `isMacOS` / `isLinux` | `util.IsWindows` / `IsMacOS` / `IsLinux` | `util/os.go` |
| `OSUtil.getUserHome` / `getAppConfigPath` | `util.GetUserHome` / `GetAppConfigPath` | 同上 |
| `EnvUtil.getUserName` | `util.GetUserName` | 同上 |
| `TreeNodeUtil.removeNodeChild` | `tree.RemoveNodeChild` | `internal/tree/util.go` |
| `TreeNodeUtil.deepCleanMappedNode` | `tree.DeepCleanMappedNode` | 同上 |
| `TreeNodeUtil.mappedNode` | `tree.MappedNode` | 同上 |
| `TreeNodeUtil.countNodeType` | `tree.CountNodeType` | 同上 |
| `TreeNodeUtil.countAndSetNodeType` | `tree.CountAndSetNodeType` | 同上 |
| `TreeNodeUtil.findNodeByPath` | `tree.FindNodeByPath` | 同上 |
| `TreeNodeUtil.isNotUnderCustomRoot` | `tree.IsNotUnderCustomRoot` | 同上 |
| `TreeNodeUtil.getUnderRootNode` | `tree.GetUnderRootNode` | 同上 |
| `TreeNodeUtil.isRootNode` / `isCompressNode` | `tree.IsRootNode` / `IsCompressNode` | 同上 |
| `TreeNodeUtil.getNodePathByParent` | `tree.GetNodePathByParent` | 同上 |
| `PatchPackService.matchPatchName` | `service.MatchPatchName` | `internal/service/patch.go` |
| `PatchPackService.refreshReadmeNode` | `service.RefreshReadmeNode` | 同上 |
| `PatchPackService.cleanMappedNode` | `service.CleanMappedNode` | 同上 |
| `PatchPackService.refreshMappedNode` | `service.RefreshMappedNode` | 同上 |
| `PatchPackService.mappedDelNodeWithType` | `service.MappedDelNodeWithType` | 同上 |
| `PatchPackService.mappedAddNodeWithType` | `service.MappedAddNodeWithType` | 同上 |
| `PatchPackService.mappedModNodeWithType` | `service.MappedModNodeWithType` | 同上 |
| `PatchPackService.mappedInnerClassNode` | `service.MappedInnerClassNode` | 同上 |
| `PatchPackService.createPatchRootInfo` | `service.CreatePatchRootInfo` | 同上 |
| `PatchPackService.updatePatchReadme` | `service.UpdatePatchReadme` | `internal/service/patchsave.go` |
| `AppPackService.createPatchDiffInfo` | `service.CreatePatchDiffInfo` | `internal/service/apppack.go` |
| `BasePackService.buildChildrenWithCompress` | `service.BuildChildrenWithCompress` | `internal/service/base.go` |
| `PatchMatchProcessor.setAppRootNode` | `service.SetAppRootNode` | `internal/service/match.go` |
| `PatchMatchProcessor.setPatchRootNode` | `service.SetPatchRootNode` | 同上 |
| `PatchMatchProcessor.exec` | `service.Exec` | 同上 |
| `SearchConf.addHistory` | `config.AddHistory` | `internal/config/config.go` |

---

## 3. C 类 —— 已改名（语义等价，名字不同）

**这一节是审计的重点**：Java 开发者按原名字去 Go 里 grep 会**搜不到**。

### 3.1 常量（字母本身被改过）

| Java 常量 | Go 常量 | 改动 | 所在 Java 文件 |
|---|---|---|---|
| `UTF_8` | `CharsetUTF8` | **加了 `Charset` 前缀**（Go 没有命名空间，`UTF8` 太泛） | `CharsetConst` |
| `GBK` | `CharsetGBK` | 同上 | 同上 |
| `ZIP` / `JAR` / `CLASS` / `JAVA` / `TXT` / `SIGN` | `ExtZip` / `ExtJar` / `ExtClass` / `ExtJava` / `ExtTxt` / `ExtSign` | **`Ext` 前缀替换了裸名** | `FileExtConst` |
| `MVN_POM_PROPERTIES` | `MvnPomProps` | **缩写成 `Props`** | `JarInfoConst` |
| `NOT_LOAD` | `LoadNotLoaded` | **整名重写** | `LoadStatusConst` |
| `APP_TREE_LOADING` | `LoadAppTreeLoad` | 整名重写（`Loading` → `Load`、加 `Load` 前缀） | 同上 |
| `PATCH_TREE_LOADING` | `LoadPatchTreeLoad` | 同上 | 同上 |
| `WINDOWS` | `ExplorerWindows` | **`Explorer` 前缀替换了裸名** | `OSExplorerConst` |
| `MACOS` | `ExplorerMacOS` | 同上 | 同上 |
| `LINUX_GNOME` | `ExplorerGnome` | 同上（去掉 `LINUX_`） | 同上 |
| `LINUX_NAUTILUS` | `ExplorerNautilus` | 同上 | 同上 |
| `LINUX_KDE` | `ExplorerKDE` | 同上 | 同上 |
| `PATCH` | `ParamPatch` | **`Param` 前缀替换了裸名** | `ParamNameConst` |
| `TARGET` | `ParamTarget` | 同上 | 同上 |
| `DEV` | `ParamDev` | 同上（**未解析**，Go 版无对应消费点，见下） | 同上 |
| `ALL` / `APP` / `PATCH` | `focusTreeAll` / `focusTreeApp` / `focusTreePatch` | 加了 `focusTree` 前缀，且**降为未导出**（只在 `gui` 包内用） | `FocusTreeStatusConst` |
| `APP_ICON` | （未移植，见 §5） | — | `IConConst` |
| `DIRECTORY` / `FILE` / `FILE_JAR` / … | `iconDirectory` / `iconFile` / `iconFileJar` / … | **`icon` 前缀替换了裸名**，且降为未导出 | `IConConst` |
| `FONT` / `CLOSE` / `ARROW_UP` / `ARROW_DOWN` | （未移植，改用内联 SVG，见 §5） | — | `IConFontConst` |
| `FONT_BOLD` / `COLOR_BLUE` / `COLOR_GREEN` / `COLOR_GRAY` | （未移植，改用 CSS，见 §5） | — | `StyleConst` |

> **注意 `IConConst`**：常量名从 `DIRECTORY` 变成 `iconDirectory`，但**文件名一字不改**
> （`directory.png`）。所以"图标文件名沿用"成立，"图标常量名沿用"不成立。

> **关于 `ParamNameConst` 三个常量（2026-10-15 更新）**：`ParamTarget` / `ParamPatch`
> 现在**有了真实引用点** —— `gui/startup.go` 的 `parseStartupArgs` 按它们读
> `--target=` / `--patch=` 参数，"双击 .jar 用本工具打开"这条链路全靠它们
> （见 MIGRATION.md 第 46 项）。只有 `ParamDev` 仍是零引用：原版只拿它决定
> 错误文件落盘位置（`ExceptionUtil:58`），而 Go 版没有 ExceptionUtil，所以
> **有意不解析**，不是漏了。

### 3.2 类型

| Java 类型 | Go 类型 | 改动 |
|---|---|---|
| `ChangeTypeEnum` | `constant.ChangeType` | 去掉 `Enum` 后缀 |
| `I18NEnum` | `constant.I18N` | 去掉 `Enum` 后缀 |
| `ThemeEnum` | `constant.Theme` | 去掉 `Enum` 后缀 |
| `control.tree.node.TreeNode` | `tree.Node` | 去掉 `Tree` 前缀 |
| `control.tree.node.VirtualNode` | `tree.Node`（`Kind=KindVirtual`） | **5 个类压成 1 个类型 + `Kind` 字段** |
| `control.tree.node.MappedNode` | `tree.Node`（`Kind=KindMapped`） | 同上 |
| `control.tree.node.FileNode` / `FileDirectNode` / `CompressNode` | `tree.Node`（`Kind=KindFile` / `KindFileDirect` / `KindCompress`） | 同上 |
| `control.tree.TreeInfo` | `tree.Node` 的根信息 | 未单独立类型 |
| `service.AppPackWriteProcessor` | `service.WriteContext` | 改名（Go 用"上下文"而非"处理器"） |
| `service.compress.LocalFileInfo` | — | 未移植 |
| `control.tree.FileTreeCellDragInfo` | `gui.DropSide` / `CompareDropFiles` | 改名 |
| `control.tree.FileTreeItem` | `gui.NodeView` | 改名（JavaFX `TreeItem` → 前端 DTO） |
| `control.tree.TreeNodeFileMatchProcessor` 等 3 个 Processor | — | 合并进 `service/match.go` |
| `control.tree.PatchMarkRootEvent` | — | 事件机制由 Wails 绑定调用替代 |
| `view.content.parser.ContentParser` / `TextContentParser` / `ClassContentParser` | `content.Render` / `RenderDiff` / `decompile.Decompile` | 改名 + 重组 |
| `view.content.facotry.*CodeAreaFactory`（8 个） | `content.ParserKind` + `content.ParserForExt` + `highlight.Tokenize` | **8 个工厂类压成 1 张扩展名表 + 1 个分词器** |
| `view.content.facotry.DiffLineNumberFactory` | `content.DiffNumbers` / `PlainNumbers` | 改名 |
| `core.AppContext` | （Wails `App`） | 改名 |
| `core.StartUpInfo` | `cmd/patchassistant` 的启动逻辑 | 改名 + 移位 |
| `core.TemporaryFileClear` | `service` 的临时目录管理 | 改名 |
| `fernflower.*`（5 个类） | `internal/decompile` + 外挂 CFR/JD | **整体替换**（见 §5） |
| `common.SortLinkedList` | — | 未移植 |
| `common.InputBufConsumer` / `InputStreamConsumer` | — | 未移植（Go 用 `io` 包） |
| `exception.AppException` | Go `error` | 语言机制替代 |

### 3.3 方法 / 函数

| Java | Go | 改动 |
|---|---|---|
| `FileNameUtil.getName` | `util.Name` | **去 `get` 前缀** |
| `FileNameUtil.getExt` | `util.Ext` | 去 `get` |
| `FileNameUtil.getFileName` | `util.FileName` | 去 `get` |
| `FileNameUtil.getPathSeparatorPattern` | `util.PathSeparatorPattern` | 去 `get`，**改成导出变量**（Java 侧对应的是私有字段名） |
| `TextDiffUtil.diff` | `util.DiffLines` | **改名**（`diff` 太泛） |
| `CharsetUtil.getCharset` | `util.DetectCharset` | 改名 |
| `CharsetUtil` 的解码/编码（原版内联在 `FileUtil.readFile`） | `util.DecodeString` / `EncodeString` | 抽成独立函数 |
| `I18nUtil.t` | `i18n.Tr` | 改名（`t` 太短） |
| `I18nUtil.getSysLanguage` | `util.SysLanguage` | 去 `get` |
| `JsonUtil.writeAsBytes` | `util.WriteJSONAsBytes` | 改名 |
| `JsonUtil.getJsonMapper` | `util.JacksonIndent` | 改名（Go 侧只保留 Jackson 缩进风格的复刻） |
| `IConUtil.getPathByFileExt` | `gui.iconByExt` | 改名 |
| `TreeNodeUtil.getIconImage` | `gui.iconName` | 改名 |
| `TreeNodeUtil.cleanMappedNode` | `tree.MappedNode` / `service.CleanMappedNode` | 拆成两处 |
| `TreeNodeUtil.buildNode` / `buildNodeChildren` / `buildChildNode` | `pack.BuildZipTree` / `BuildZipTreeFromReader` / `BuildZipTreeFromBytes` | 改名 + 移位到 `internal/pack` |
| `TreeNodeUtil.bindTreeNodeAndItem` | `tree.BindMappedNode` | 改名（`TreeItem` → `Node`） |
| `TreeNodeUtil.readNodeFile` / `readNodeBytes` | `tree.Node.Open` / `ContentBytes` | 改名（方法挂到 `Node` 上） |
| `PatchPackService.getTreeNode` | `service.GetPatchTreeNode` | **加 `Patch` 限定**（应用侧同名方法也要区分） |
| `AppPackService.getTreeNode` | `service.GetAppTreeInfo` | 改名（返回值就是 `AppTreeInfo`） |
| `AppPackService.savePack` | `service.SaveAppPack` | 改名 |
| `BasePackService.handleInnerClass` | `service.HandleInnerClassUnder` / `pack.HandleInnerClass` | 改名（`Under` 后缀区分"整棵树"与"单个节点"） |
| `ReadMeFileService.getPathInfos` | `service.PathInfos` / `PathInfosWithApp` / `PathInfosOfText` | **1 个重载方法拆成 3 个具名函数** |
| `SearchConf.getHistoryLimit` | `config.SearchConf.HistoryLimit` | 方法 → 字段 |
| `PatchConf.getUsername` | `config.EffectiveUsername` | 改名（"有效用户名"，含兜底） |
| `FileUtil.getSizeReadability` | （前端 `app.js` 里实现） | 移位到前端 |

---

## 4. D 类 —— Go 侧新增（Java 无对应）

这些是原版**没有的能力**，已在 `MIGRATION.md` §3.9 登记为刻意增强。

| Go 标识 | 位置 | 说明 |
|---|---|---|
| `content.SplitMarks` / `SplitBlock` / `InlineMark` / `ComputeSplitMarks` | `internal/content/splitmarks.go` | 双栏差异标记与差异块（原版双栏不做任何标记） |
| `content.Span` / `Seg` / `Line` / `Paragraphs` / `Render` / `RenderMarked` / `RenderDiff` / `DiffMarks` | `internal/content/render.go` | 服务端渲染高亮 HTML（原版用 RichTextFX 客户端渲染） |
| `content.PlainNumbers` / `DiffNumbers` | `internal/content/linenum.go` | 行号列 |
| `content.Tokenize` | `internal/content/highlight.go` | 语法分词器 |
| `content.ParserKind` / `ParserForExt` / `LangForExt` | `internal/content/supports.go` | 扩展名 → 解析器/语言 |
| `service.ComparePacks` / `CompareEntry` / `CompareChange` / `ComparePackResult` / `PackEntry` / `ReadPackEntry` / `DiffCount` | `internal/service/jarcompare.go` | **两个 jar 包对比**（原版没有） |
| `gui.OpenCompareContent` / `PickCompareJar` / `CopyCompareReport` / `SaveCompareReport` / `HandleCompareDrop` | `gui/jarcompare.go` | 对比界面绑定 |
| `internal/javac`（`FindJavac` / `Available` / `JavacMajor` / `Compile` / `Options` / `Result` / `ClassVersion` / `ReleaseFor` / `JavaVersionName`） | `internal/javac/` | **编译包内 `.java` 源码**（原版没有） |
| `decompile.FindJava` / `Available` / `Decompile` / `MainClassName` | `internal/decompile/` | 反编译（原版用内嵌 fernflower，Go 改用外挂 CFR/JD） |
| `gui.OpenEditor` / `SaveContent` / `SaveContentAs` / `CompileContent` | `gui/editor.go` | 编辑包内文件（原版没有） |
| `tree.DataSource` / `FileSource` / `MemorySource` / `Open` / `FilePath` / `ConsumeBytes` / `GetSizeOrInit` / `GetMd5OrInit` / `Clone` | `internal/tree/node.go` | 节点数据源抽象（原版直接读文件） |
| `model.JavaDate` / `NewJavaDate` / `MarshalJSON` / `UnmarshalJSON` | `internal/model/model.go` | Jackson 日期格式的复刻（保 XML 互读） |
| `util.WriteJSONAsBytes` / `JacksonIndent` | `util/json.go` | Jackson 缩进风格复刻 |
| `util.NormalizePath` / `ToPlatformPath` / `CompareNameIgnoreCase` | `util/path.go` | 路径工具（原版散在 `FileNameUtil` 里内联） |
| `util.ToLowerASCII` | `util/string.go` | ASCII 小写（Go `strings.ToLower` 行为不同，需精确控制） |
| `util.DiffLines` / `SplitDiffLines` / `Deltas` | `util/textdiff.go` | diff 的中间表示（原版直接出 unified diff 文本） |
| `constant.NormalizePath` | `internal/constant/enum.go` | 压缩包内路径归一化（`constant` 包内公用） |
| `i18n.Keys` | `internal/i18n/i18n.go` | 键名清单（走查用） |
| `gui.*` 全部绑定方法（`OpenAppPack` / `RunPatch` / `GetState` / …） | `gui/*.go` | Wails 绑定层，原版是 JavaFX 内部方法调用 |
| 前端 `app.js` / `app.css` / `index.html` 全部标识 | `gui/frontend/dist/` | 原版没有 Web 前端 |

---

## 5. E 类 —— Java 有、Go 未以同名单元移植

| Java | 去向 |
|---|---|
| `view/*`（全部，约 40 个类） | **前端 HTML/CSS/JS 重写**（`gui/frontend/dist/`） |
| `control/*`（全部，约 25 个类） | 同上 + `gui/*.go` 绑定层 |
| `fernflower/*`（5 个类） | 外挂 CFR/JD（见 `MIGRATION.md` §2.1） |
| `common/InputBufConsumer` / `InputStreamConsumer` | Go `io` 包 |
| `common/SortLinkedList` | Go `sort` 包 |
| `util.UIUtil` / `FXUtil` / `ImageUtil` / `AlterUtil` | JavaFX 专有，无对应 |
| `util.ExceptionUtil` | Go `error` 机制 |
| `util.FileTempUtil` | `os.CreateTemp` + `service` 临时目录管理 |
| `util.DateUtil` | Go `time` 包 |
| `util.CollectionUtil` | Go 语言特性（map 遍历 / `len`） |
| `util.XMLUtil` | Go `encoding/xml` |
| `util.UUIDUtil` | `crypto/rand`（`uniqueFileName` 内联使用） |
| `util.FileUtil` 的 `moveFile` / `copyTo` / `deleteFile` / `readBuf` / `writeFile` / `mkdir` / `openFolder` / `selectFile` / `getResource` | `os` / `io` 包 + `gui` 绑定（打开资源管理器 / 选文件） |
| `util.OSUtil` 的 `initHostServices` / `openUrl` / `openFile` / `getPid` / `execCommand` / `setSystemClipboard` | Wails runtime + `gui` 绑定 |
| `util.ImageUtil.loadImage` / `loadImageCache` | 前端 `<img src>`（后端只给路径） |
| `core.AppContext` 的 `initTheme` / `curTheme` / `updateTheme` | 前端 CSS（Go 版只有亮色一套） |
| `core.AppContext` 的 `syncConf2File` / `reload` / `getTempDir` | Go 版 GUI **没有配置文件**，设置只活在会话内（见 §3.9） |
| `constant.IConFontConst.FONT` / `CLOSE` / `ARROW_UP` / `ARROW_DOWN` | **内联 SVG**（`fonts/iconfont.ttf` 未移植） |
| `constant.StyleConst.FONT_BOLD` / `COLOR_BLUE` / `COLOR_GREEN` / `COLOR_GRAY` | CSS 变量（`app.css` 的 `:root`） |
| `constant.IConConst.APP_ICON`（`/icon.png`） | `gui/build/appicon.png` + `gui/build/windows/icon.ico`（文件名也改了） |
| `resources/fonts/iconfont.ttf` | 未移植 |
| `resources/css/theme-dark.css` | 未移植（Go 版只有亮色） |
| `resources/META-INF/` | 未移植 |
| `view/config/ConfigView` 里 `app.view.config.genera-lang.<code>` / `genera-theme.<code>` 这两个**动态拼接点** | 未移植。注意：两个"父键" `app.view.config.genera-lang` / `genera-theme` **在 properties 里是有的、且逐字保留**（Go 侧 properties 里也在），只是 Go 侧没有代码去拼 `<code>` 后缀使用它们（Go 版只有亮色 / 中文一套，没有第二个可选值） |

---

## 6. 前端标识符（CSS 类名 / DOM id / 资源文件名）

### 6.1 JavaFX style class → Go CSS 类名：**基本全部未沿用**

Java 的 JavaFX style class 是**平台语义**（`.tree-cell` 是 JavaFX 控件的皮肤类名），
Go 侧是浏览器 DOM，没有同名控件，所以整套重命名。**只有 `.code-area` 这个名字巧合保留**。

| Java JavaFX style class | Go CSS 类名 | 说明 |
|---|---|---|
| `.root` | `:root` / `body` | CSS 变量根 |
| `.home-view` | `body` + `.toolbar` + `.panes` + `.statusbar` | **一个类拆成多个** |
| `.tree-view` | `.tree` | |
| `.tree-cell` | `.row` | |
| `.tree-cell:selected .title` | `.row.selected .name` | |
| `.tree-cell:selected .detail` | `.row.selected .name-text` | |
| `.list-view` | `.search-list` | |
| `.list-cell` | `.search-row` | |
| `.list-cell:selected .title` / `.detail` | `.search-row.selected .s-name` / `.s-path` | |
| `.titled-pane` | `.set-group` + `.set-group-title` | |
| `.code-area` | `.code` | **唯一巧合保留的名字（后缀不同）** |
| `.code-area .lineno` | `.code .ln` | |
| `.code-area .add` / `.del` / `.mod` | `.cline.mark-add` / `.mark-del` / `.mark-mod` | **加 `mark-` 前缀** |
| `.code-area .ignore` | — | Go 的双栏差异不产生 ignore 行 |
| `.code-editor` | `.editbox` / `.contentwin` | |
| `.code-editor .search-box` | `.findbar` | |
| `.code-editor .search-box .text-field` | `.find-input` | |
| `.code-editor .search-box .button` | `.findbar .btn` | |
| `.code-editor .search-box .label` | `.find-count` | |
| `.code-editor .search-box .split-pane` | `.findbar` | |
| `.dialog-pane` | `.dialog` | |
| `.text-field` | `.field input` / `.search-input` / `.edit-text` | |
| `.check-box` | `.field-check input` | |
| `.radio-button` | `.ctx-check` | |
| `.menu-item` | `.ctxitem` | |
| `.split-pane` / `.split-pane-divider` | `.splitter-v` / `.splitter-h` | |
| `.scroll-pane` | 原生滚动条样式（`.tree::-webkit-scrollbar` 等） | |
| `.styled-text-area .selection` | `::selection` / `::highlight(find-hit)` | |
| `.button` | `.btn` / `.btn-primary` / `.btn-icon` | |
| `.label` | 无（直接元素） | |

### 6.2 Go 前端新增的类名（Java 无对应）

`.brand` `.contentwin*` `.cmp-*` `.ctxmenu` `.hex*` `.overlay` `.toast` `.twisty`
`.splitter` `.pane*` `.stat*` `.md5` `.modTime` `.size` `.info-*` `.fastsearch`
`.lang-*`（高亮容器前缀）`.cline` `.nav-current` 等。

### 6.3 资源文件名

| Java | Go | 状态 |
|---|---|---|
| `icons/*.png`（21 个） | `frontend/dist/icons/*.png` | **逐字沿用** |
| `icon.png` | `gui/build/appicon.png` | 改名 |
| `icon.ico` | `gui/build/windows/icon.ico` | 改名（且补成 6 层） |
| `messages/i18n_*.properties` | `internal/i18n/messages/i18n_*.properties` | **文件名逐字沿用**（移位） |
| `css/theme.css` | `frontend/dist/app.css` | 改名（且非逐行翻译） |
| `css/{java,javascript,css,html,sql,xml,yaml}-style.css` | 合并进 `frontend/dist/app.css` | **合并成一个文件**（类名保留） |
| `css/theme-dark.css` | — | 未移植 |
| `fonts/iconfont.ttf` | — | 未移植 |
| `PatchAssistant2J.xml` | — | 未移植（应用信息文件） |

---

## 7. 包结构对照

| Java 包 | Go 包 | 关系 |
|---|---|---|
| `cn.cpoet.patch.assistant.constant` | `internal/constant` | 一一对应 |
| `...core`（配置部分） | `internal/config` | 配置类迁到独立包 |
| `...core`（上下文 / 启动） | `gui` / `cmd/patchassistant` | 拆分 |
| `...model` | `internal/model` | 一一对应 |
| `...service` | `internal/service` | 一一对应 |
| `...service.compress` | `internal/pack` | 改名 |
| `...util` | `internal/util` + `internal/tree` + `gui` | **拆分到 3 处** |
| `...control.tree` + `control.tree.node` | `internal/tree` | 合并 |
| `...fernflower` | `internal/decompile` | 替换 |
| `...view.*` + `...control.*` | `gui` + `frontend/dist` | **重写为 Wails 绑定 + Web 前端** |
| `...exception` | — | Go `error` |
| `...common` | — | 标准库 |
| （无） | `internal/i18n` | **Go 新增** |
| （无） | `internal/content` | **Go 新增**（原版散在 `view.content`） |
| （无） | `internal/javac` | **Go 新增** |
| （无） | `internal/decompile` | 见上 |
| （无） | `cmd/patchassistant` | **Go 新增**（CLI 入口） |

---

## 8. 影响评估

### 8.1 会真正影响 Java 开发者对照阅读的（建议优先看）

1. **常量名**（§3.1）：`ZIP` → `ExtZip`、`WINDOWS` → `ExplorerWindows`、
   `PATCH` → `ParamPatch`、`DIRECTORY` → `iconDirectory`、`NOT_LOAD` → `LoadNotLoaded`、
   `UTF_8` → `CharsetUTF8`、`MVN_POM_PROPERTIES` → `MvnPomProps`。
   → **字母被改过，grep 不到。**
2. **枚举类型名**（§3.2）：`ChangeTypeEnum` → `ChangeType`、`I18NEnum` → `I18N`、
   `ThemeEnum` → `Theme`。
   → 去掉 `Enum` 后缀，**但枚举成员名（`ADD` / `MOD` / …）通过 `ChangeTypeCodes`
   与 `ChangeTypeAdd` 等保留**。
3. **节点类**（§3.2）：`TreeNode` / `FileNode` / `CompressNode` / `VirtualNode` / `MappedNode`
   → 统一成 `tree.Node` + `Kind` 字段。
   → 但**类名保留在 `KindNames` 里**，可作搜索锚点。
4. **8 个 CodeAreaFactory** → `content.ParserForExt` 一张表。
5. **`TreeNodeUtil` 的 `buildNode*`** → `pack.BuildZipTree*`（换了包）。
6. **前端 CSS 类名**（§6.1）：整套换了，只能靠语义对照。

### 8.2 不影响对照阅读的（无需回改）

- **B 类全部**：去掉下划线、首字母大写即可搜到，符合 Go 惯例，回改反而违反 `golint`。
- **A 类全部**：本来就是逐字沿用。
- 所有跨语言边界标识（i18n 键、XML 元素名、图标文件名、高亮类名、枚举值）：
  已经逐字沿用，**回改会破坏 Java/Go 共存与资源复用**。

### 8.3 可选的低成本回改（若确实要"更贴近 Java 原名"）

> 以下仅为**选项**，尚未执行。列出的是"回改代价小、收益明确"的几条。

| 现名 | 可改回 | 代价 |
|---|---|---|
| `constant.CharsetUTF8` / `CharsetGBK` | `UTF8` / `GBK`（或 `Charset_UTF8`） | 小，`CharsetConst` 语义已由包名表达 |
| `constant.ExtZip` / `ExtJar` / … | `Zip` / `Jar` / …（或保留 `Ext`） | 中，`Ext*` 与 `Dot*` 成对，改了要一起动 |
| `constant.LoadNotLoaded` / `LoadAppTreeLoad` / `LoadPatchTreeLoad` | `LoadStatusNotLoad` / `LoadStatusAppTreeLoading` / … | 小 |
| `constant.ExplorerWindows` / … | `OSExplorerWindows` / … | 小 |
| `constant.ParamPatch` / `ParamTarget` / `ParamDev` | `ParamNamePatch` / … | 小 |
| `constant.MvnPomProps` | `MvnPomProperties` | 小 |
| `constant.ChangeType` / `I18N` / `Theme` | `ChangeTypeEnum` / `I18NEnum` / `ThemeEnum` | **不建议**：Go 无 `enum`，带 `Enum` 后缀反而不地道 |
| `tree.Node` | `TreeNode` | **不建议**：`tree.TreeNode` 冗余 |
| `gui.iconDirectory` / `iconFile` / … | `iconConstDirectory` / … | 小，但 `icon` 前缀已足够表意 |

> **建议**：先看 §8.1 那 6 条（真正 grep 不到的），确认是否有对照需求；
> §8.3 的常量前缀改名收益有限（B 类"去下划线 + 首字母大写"已经能搜到），
> 改动会波及 `constant` 包所有引用点与若干测试断言。

### 8.4 作者署名与许可证的可选补救

> 按"维护 Java 端开发者知识权益"的诉求，这一组比 §8.3 的改名**更值得做**。
>
> **执行状态（2026-10-15）**：用户选定做了 ✅ **LICENSE**、✅ **wails.json 版权**、
> ✅ **源码出处注释**、✅ **go.mod 模块名前缀** 四项；**前端「关于」页 + 按钮暂未做**
> （代价最大，会动前端与走查断言，用户本轮未选）。

| 现状 | 补救 | 状态 | 备注 |
|---|---|---|---|
| **无 LICENSE** | 从 `src-java/LICENSE` 拷一份到 `patch-assistant-go/LICENSE` | ✅ **已做** | 逐字节一致（`diff` 为空）。Apache-2.0，`Copyright [2025] [CPoet]` |
| `wails.json` 无 `copyright` | `info` 块补 `"copyright": "Copyright [2025] [CPoet]"` | ✅ **已做** | 取值与 LICENSE 首行逐字一致；修掉了 exe 里 `LegalCopyright = Copyright.........` 那个未替换的占位符（已重建 exe 复验） |
| 源码无 `@author` | 在各包包注释里补出处声明 | ✅ **已做** | 13 个文件（11 个有包注释的包 + `gui/main.go` + `cmd/patchassistant/main.go`），统一一句：`本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。` |
| `go.mod` module 名无 `cn.cpoet` 前缀 | 改成 `cn.cpoet.patch.assistant`（对齐 Java `groupId`） | ✅ **已做** | 33 个 Go 文件 / 99 处 import 前缀同步改写；`go fmt`、`go vet`、`go build`、`go test ./...` 全绿 |
| `AppAuthorName/Email/URL` 是死常量 | 让它们被真正使用（即下面的"关于页"） | ⬜ 未做 | 随"关于页"一起做；或加 `TestAppAuthorConstantsMatchJava` 钉住值与 Java `AppConst` 一致 |
| 没有「关于」页 | 前端加 `#about-overlay`，对齐 Java `AboutView` | ⬜ 未做 | i18n 的 7 个 `app.view.about.*` 键**已逐字在 properties 里**，直接用即可；Java 的三个 URL 也现成（`https://www.cpoet.cn`、`.../post/projects/patch-assistant-to-java/v1.3.x`、`https://github.com/c-poet/patch-assistant-to-java`） |
| 工具栏无「关于」按钮 | 加 `#btn-about` | ⬜ 未做 | Java `HomeView` 有 `aboutBtn`；`app.view.home.about` 键已在 |
| exe 版本信息无作者名 | `wails.json` 的 `info` 块**没有 Author 字段**，只能借 `comments` / `companyName` | ⬜ 未做 | `Comments` 已写出处（"Java 版 PatchAssistant2J 的 Go 移植"）。**若想让 `CPoet` 出现在 exe 里，唯一办法是让它被代码引用**（即做"关于页"，或加一条引用这些常量的测试） |

> **剩余项**：前端「关于」页 + 工具栏按钮（连带激活 3 个死常量、让 `CPoet` 重新进入 exe）。
> 这一项要新增 HTML/JS/CSS、接线、同步走查断言（当前 `EXPECTED_TOTAL = 302`）与
> `MIGRATION.md` 登记 —— 属"新增能力"，与 §3.9 的双栏差异高亮同级。

---

## 附：审计可复现步骤

```bash
# 1. Java 常量名
python -c "..."   # 对 constant/*.java 抓 UPPER_SNAKE 声明

# 2. 两侧 i18n 键名比对（**注意连字符**，否则会漏键）
python -c "..."   # 正则用 ^\s*([A-Za-z][\w.\-]*)\s*=  后比集合

# 3. 图标文件名比对
diff <(ls src-java/src/main/resources/icons/ | sort) \
     <(ls patch-assistant-go/gui/frontend/dist/icons/ | sort)

# 4. CSS 选择器比对
grep -oE '\.[a-zA-Z][a-zA-Z0-9_-]*' src-java/src/main/resources/css/*.css | sort -u
grep -oE '^[^{]+\{' patch-assistant-go/gui/frontend/dist/app.css
# 注意：上面这条会漏掉逗号续行的多行选择器，核对类名要直接 grep 类名字面量

# 5. exe 里的署名 / 出处（PE 版本资源是 UTF-16LE）
python - <<'PY'
import io
b=io.open(r"patch-assistant-go/gui/build/bin/PatchAssistant2J.exe","rb").read()
print("VS_VERSION_INFO:", b.find(b'V\x00S\x00_\x00V\x00E\x00R\x00S\x00I\x00O\x00N\x00_\x00I\x00N\x00F\x00O\x00'))
for k in ["CPoet","llzero54","cpoet.cn","Apache","License"]:
    print(k, b.count(k.encode()))
PY
```
