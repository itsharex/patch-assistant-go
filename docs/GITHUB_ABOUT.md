# GitHub 仓库 About 文案

在仓库首页右上角 **About → ⚙️ Edit** 里填写。三项分别是 Description / Website / Topics。

## Description（仓库简介，限 350 字符）

**推荐（中文，信息最全）**

```text
Java Jar 应用增量补丁工具（Go + Wails 重写版）：按补丁包 README 升级指令自动匹配、替换包内文件，重打包并写入补丁签名；内置 Fernflower 与精简 JDK，.class 可反编译、改完能编译回去。单文件 exe，目标机器无需安装 Java。基于 c-poet/patch-assistant-to-java 的优化分支，由张小强发起。
```

**备选（中英混排，便于检索）**

```text
Java Jar incremental patch tool (Go + Wails) — README-driven file matching, repack with patch signature, built-in decompiler & JDK so you can edit and recompile .class in place. Single exe, no JVM needed.
```

**超短（放不下时用）**

```text
Java Jar 增量补丁工具（Go + Wails 重写）：README 驱动匹配替换、重打包签名、内嵌反编译与 javac，单文件 exe 免装 Java。
```

## Website

可以留空，或填上游项目地址：

```text
https://github.com/c-poet/patch-assistant-to-java
```

## Topics（建议全选）

```text
java
jar
patch
patcher
incremental-updates
zip
decompiler
fernflower
spring-boot
go
golang
wails
desktop-app
windows
diff
cli
```

## 其他建议

- **Social preview**：在 `Settings → General → Social preview` 上传一张主界面截图（1280×640），
  比默认卡片更能说明这是个带 GUI 的工具。
- **Releases**：把 `gui/build/bin/PatchAssistant2J.exe` 作为 Release 附件上传，
  别提交进仓库（已在 `.gitignore` 里排除）。
- **仓库简介里的名字**：upstream 叫 `PatchAssistant4J`，本仓库源码里的应用名是 `PatchAssistant2J`
  （沿用 exe 名，为了不破坏 `.jar` 文件关联与已经发布的安装包）。简介里统一用
  `PatchAssistant2J（Go）` 更不容易混淆。
