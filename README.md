# PatchAssistant2J（Go）

> 面向 Java jar 应用的**增量补丁工具**：按补丁包里的 `README.txt` 升级指令自动匹配、替换包内文件，
> 一键重打包并写入补丁签名；内置反编译器与精简 JDK，`class` 源码看完能直接改、改完能编回去。
> 单文件 exe，**目标机器不用装 Java**。

[![Go 1.25](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Wails v2](https://img.shields.io/badge/Wails-v2-DF0000)](https://wails.io)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/Platform-Windows-0078D4?logo=windows&logoColor=white)](#已知限制与路线图)

## 目录

- [项目简介](#项目简介)
- [项目来源](#项目来源)
- [功能特性](#功能特性)
- [快速开始](#快速开始)
- [补丁包约定](#补丁包约定)
- [目录结构](#目录结构)
- [设计取舍](#设计取舍)
- [测试与质量](#测试与质量)
- [已知限制与路线图](#已知限制与路线图)
- [文档导航](#文档导航)
- [致谢](#致谢)
- [许可证](#许可证)

## 项目简介

做 Java 应用（尤其是 Spring Boot 打出的 fat jar）的增量补丁时，传统做法是用 WinRAR 一类压缩工具
逐个查找、替换包内文件 —— 慢、容易漏，改完还得手动写补丁签名、写清变更说明。

`PatchAssistant2J` 把这条链路做成一个工具：

- **两侧树形浏览**：左边「应用包」、右边「补丁包」，嵌套 jar/zip 任意层级展开，文件行直接显示
  修改时间 / 大小 / MD5；
- **自动匹配**：读补丁包里的 `README.txt` 升级指令，按全路径 / 文件名双模式匹配，结果直接标在树上
  （新增 / 修改 / 删除），还支持手动指定根节点、手动标记删除；
- **重打包 + 签名**：一键写出打过补丁的应用包，并追加 `META-INF/patch-up/<yyyyMMdd>NN.sign`
  记录操作人、时间与变更统计；
- **看得见改了什么**：双击文件即预览，双栏差异高亮、滚动同步、`▲/▼` 跳到差异处，
  `.class` 会自动反编译成 Java 源码；
- **改得动**：文本 / 十六进制 / class 源码三种编辑形态，改的是内存、产物即生效；
  改完 class 源码还能用内置 javac 直接**编译回 `.class`**；
- **比得了**：工具栏「对比」可逐文件比两个 jar/zip（可钻进嵌套包），也可比两个普通文件。

图形界面用 **Wails v2**（WebView2 + Go 后端，前端是手写 HTML/CSS/JS），
业务逻辑另外提供了**命令行入口**，便于在流水线/脚本里批处理。

## 项目来源

本项目是 **[c-poet/patch-assistant-to-java](https://github.com/c-poet/patch-assistant-to-java)**
（`PatchAssistant4J`「补丁助手」，JavaFX + GluonFX 桌面版，作者 **CPoet**）的**延伸与优化版本**，
由 **张小强** 发起并主导 Go 重写。

| 维度 | Java 原版（上游） | 本项目（Go 版） |
|---|---|---|
| 运行时 | JavaFX + GluonFX 打包，依赖 JVM | Wails + WebView2，**单文件 exe，内置精简 JDK** |
| 建树 | 每个条目解压落盘再算 MD5 | 惰性读取 zip 条目，MD5 按需计算，不落盘 |
| 命令行 | 无 | `list` / `info` / `readme` / `patch` / `patchreadme` |
| 新增能力 | — | 包内文件编辑、class 源码**编译回包**、整包/单文件对比、双栏差异标记与跳转、树上即时搜索、跨树定位 |
| 体积 | — | exe ≈ 40 MB（其中内嵌运行时 27.5 MB） |

> **行为以原版为准。** 凡与 Java 版不一致的地方都当 bug 处理，不"顺手修好"：
> 逐条偏差与理由记录在 [MIGRATION.md](MIGRATION.md)，Java↔Go 标识符对照见
> [NAMING_AUDIT.md](NAMING_AUDIT.md)。Go 侧刻意新增的能力会显式标注「原版没有」。

## 功能特性

| 模块 | 能力 |
|---|---|
| 包浏览 | jar / zip / fat-jar 逐层展开，识别 Spring Boot `BOOT-INF` 布局；沿用原版 21 张文件类型图标；文件行显示修改时间 / 大小 / MD5 |
| 补丁匹配 | README 升级指令解析（`+` / `!` / `-` / `?`）、路径 + 文件名双模式、自定义根节点与说明文件、匹配结果树上高亮、手动删除项 |
| 打补丁 | 重打包应用包、写 `META-INF/patch-up/*.sign` 签名、补丁包 README 写回、比对异常提示 |
| 内容预览 | 语法着色、双栏差异（行底色 + `+/-/~` + 词级染色）、双栏滚动同步、差异处 `▲/▼` 跳转、编码切换（GBK / UTF-8）、面板内 `Ctrl+F` 查找、`Ctrl+G` 跳转行列 |
| 反编译 | 内嵌 Fernflower（与原版同一份 jar），双击 `.class` 直出 Java 源码；可「保存源文件」导出 `.java` |
| 编辑 | 文本 / 十六进制 / class 源码三种形态，只改内存、随补丁产物生效；源码可「编译为 `.class`」写回 |
| 搜索 | 树上打字即搜、`Ctrl+F` 跨两棵树全局搜索（`*` 通配、路径匹配，能钻进未展开的嵌套 jar）、预览面板内查找 |
| 对比 | 两个 jar/zip 逐文件对比（汇总 + 分组明细 + 嵌套包钻取）、两个普通文件对比并并排看差异、报告可复制 / 保存 |
| 交互 | 拖文件进窗口、无边框窗口、可拖动 / 缩放 / 最大化的浮动面板、亮色现代工具风主题 |

每个菜单项、每个手势、每条与上游的差异，都在 **[docs/FEATURES.md](docs/FEATURES.md)** 里逐条写明。

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

### 构建 GUI 版

GUI 用 [Wails v2](https://wails.io)，需要先安装 Wails CLI（`go install github.com/wailsapp/wails/v2/cmd/wails@latest`）：

```bash
cd gui
wails build        # 产出 gui/build/bin/PatchAssistant2J.exe
wails dev          # 开发模式：前端热重载
```

`gui/wails.json` 里 `frontend:install` / `frontend:build` 留空，所以 `wails build` **不跑 npm**，
直接服务 `gui/frontend/dist/` 下的手写静态文件（`index.html` / `app.css` / `app.js` 本身就是源码，
不要当构建产物删掉）。开发环境的坑（Go 模块代理、Wails 版本锁定）见
[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#环境与工具链的几个坑)。

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

## 补丁包约定

打补丁需要两个输入：**应用包**（`.jar`）和**补丁包**（`.zip` / `.jar` / 目录）。

1. **升级指令写在补丁包根目录的 `README.txt` 里**，一行一条，前缀符号**紧贴**路径：

   ```text
   +app/BOOT-INF/classes/com/demo/NewService.class
   !app/BOOT-INF/classes/com/demo/OldService.class
   -app/BOOT-INF/classes/com/demo/Removed.class
   ?app/BOOT-INF/classes/com/demo/Ignore.class
   ```

   | 前缀 | 含义 | 说明 |
   |---|---|---|
   | `+` | 新增 | 从补丁包取文件写进应用包 |
   | `!` | 修改 | 用补丁包里的同名文件替换 |
   | `-` | 删除 | 从应用包里删掉（应用包里不存在时该行被丢弃） |
   | `?` | 忽略 | 显式跳过 |

   > 解析规则照抄原版：一行里任意位置命中即可，所以前面可以有说明文字；
   > 但**前缀与路径之间不能有空格** —— `+ foo.class` 会被解析成「无前缀」（NONE），
   > 路径还可能带上一段残字符。想看解析结果：`pa readme README.txt`。

2. **说明文件必须在补丁包根目录下**才有机会被自动识别（`README.txt` / 大小写变体）。
   套了一层目录（`mypatch/README.txt`）、`./README.txt`、或改成 `.md` 都认不出来 ——
   这时在补丁树上右键「标记根节点」/「标记 README」手动指定。完整表格见
   [docs/FEATURES.md](docs/FEATURES.md#补丁说明文件readme的识别)。

3. **匹配模式**可以只用路径、只用文件名，或两者都开：

   | 模式 | 怎么找 | CLI |
   |---|---|---|
   | 路径匹配 | 按 README 里的相对路径在应用包里定位 | `-path=true`（默认） |
   | 文件名匹配 | 在应用包里找同名文件 | `-name=true`（默认） |
   | 后缀过滤 | 限定参与文件名匹配的后缀 | `-regex=<正则>` |

4. **输出**默认就是应用包自身（同名同目录，**会覆盖**）—— 与原版保存行为一致，脚本里请显式传 `-o`。
   写出时会追加一次签名：`META-INF/patch-up/<yyyyMMdd>NN.sign`，内容含操作人、时间与变更统计。

5. **典型流程**（GUI）：打开应用包 → 打开补丁包 → 树上看增/改/删标记 → （可选）双击比对内容、
   右键改内容 → 「执行打补丁」保存。CLI 一条命令等价：

   ```bash
   pa patch -o out.jar -v /path/to/app.jar /path/to/patch.zip
   ```

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

## 测试与质量

```bash
go vet ./...                 # 静态检查
gofmt -l .                   # 列出未格式化文件
go test -count=1 ./...       # 全量测试（不吃缓存）
go test ./gui/ -v            # GUI 绑定层
```

几条与"普通单元测试"不同的地方：

- **黄金基准来自 Java 原版**，不是自己写的期望值。`internal/service/testdata/readme_golden.tsv`
  是用原版 Java 跑同一份夹具得到的输出；打补丁 / 改 README 两条写出链路也有对跑基准。
  因此这些测试回答的是"Go 与 Java 行为是否一致"。
- **特征测试（characterization test）**：`patchsave_test.go` 里有两条用例专门钉住上游的 bug
  （回滚漏写 `i++`、未展开的嵌套 jar 被写空）。哪天有人修掉，用例会变红 —— 那时再决定
  是继续对齐还是有意修正。
- **前端另有一层无头浏览器走查**：注入后端替身，在真实 Chrome 里加载真实的 `app.js` / `app.css`，
  模拟点击 / 右键 / 拖拽 / 滚动，逐条断言输出 PASS/FAIL。它守的是"数据渲染对、手势接对"，
  这一层 Go 测试覆盖不到。
- **变异验证**：改坏实现确认对应用例真的会红，再还原。记录见
  [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#变异验证记录回归防线自检)。

完整策略、走查清单与踩坑记录见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md#测试策略)。

## 已知限制与路线图

- **平台**：主要在 Windows 10/11 上开发与验证（WebView2 + 无边框窗口）。代码里有 macOS/Linux 分支，
  但未做完整实测。
- **设置尚未落盘**：主题 / 语言 / 保存结果到源目录 / 加载最后操作的补丁等设置项还是界面占位，
  搜索历史也只活在本次会话（原版写在 `application.xml`）。
- **部分菜单项仍在补齐**：`Ctrl+S` 快捷键、README 编辑器（创建说明文件）、应用包的「保存源文件」等，
  后端能力多数已就绪，缺的是接线。逐项状态见 [MIGRATION.md](MIGRATION.md) §四、§五。
- **不要指望逐字节复现原版的 zip**：Java 用 JDK 的 zlib `Deflater`、Go 用纯 Go `flate`，压缩流本就不同；
  判定口径是**语义等价**（条目名、顺序、解压后内容一致），差异清单见 MIGRATION.md §3.10。
- **exe 体积换独立性**：内嵌 jlink 裁出的 JDK 21（`java.base` + `jdk.compiler`）让反编译 / 编译
  不需要本机 Java，代价是 exe ≈ 40 MB。设 `PATCH_ASSISTANT_NO_BUNDLED_JAVA=1` 可改用本机
  `JAVA_HOME` / `PATH` 里的 JDK（要求 11+）。

## 文档导航

| 文档 | 内容 |
|---|---|
| [docs/FEATURES.md](docs/FEATURES.md) | 功能详解：树与预览、编辑器、编译、反编译、搜索、右键菜单、拖放、主题 |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | 本地开发、构建、环境坑、测试策略、前端走查、变异验证记录 |
| [MIGRATION.md](MIGRATION.md) | 移植总账：进度、架构决策、与 Java 版的逐条偏差、未完成清单 |
| [NAMING_AUDIT.md](NAMING_AUDIT.md) | Java ↔ Go 标识符对照（哪些沿用、哪些改过） |
| [docs/GITHUB_ABOUT.md](docs/GITHUB_ABOUT.md) | 仓库 About（简介 / Topics）文案，维护用 |
| [LICENSE](LICENSE) | Apache-2.0 |

## 致谢

- **[CPoet](https://github.com/c-poet)** —— Java 原版 `PatchAssistant4J` 的作者。本项目的
  交互、行为口径与大量实现细节都来自原版，Apache-2.0 授权见 [LICENSE](LICENSE)。
- **张小强** —— 本项目的发起人，Go 重写与后续增强由他提出并推动。
- 上游依赖的生态：**GluonFX**、**Fernflower**、**RichTextFX**、**java-diff-utils**、**iconfont**。
- 内置运行时来自 **Microsoft Build of OpenJDK**（GPLv2 + Classpath Exception）；
  GUI 框架为 **[Wails](https://wails.io)**（MIT）。

## 许可证

[Apache License 2.0](LICENSE) · Copyright [2025] [CPoet]

本仓库是原项目的衍生作品，遵循同一许可证。二次分发请保留版权与许可声明。
