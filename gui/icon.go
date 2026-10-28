package main

import (
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 树节点图标：移植自 Java 的 TreeNodeUtil.getIconImage + IConUtil.getPathByFileExt。
//
// 图标就是**原版那 21 张 PNG 原样拷过来**的（`src-java/src/main/resources/icons/`，
// 64×64，逐字节校验过），放在 `frontend/dist/icons/` 下，靠
// `//go:embed all:frontend/dist` 进二进制。没有重画 —— 所以观感与截图完全一致。
//
// 为什么由**后端**决定用哪张图：判定规则不只看后缀，还要看节点类型
// （ROOT / CUSTOM_ROOT / README）、节点路径（`META-INF/patch-up/`）、以及
// "这个根是不是 Spring Boot 胖包"。这些前端都拿不到（树是懒渲染的，父级
// 可能还没建 DOM），而且规则放在 Go 里才好写单测。
//
// 原版是把 Image 对象直接塞给 ImageView；Go 这边只传一个相对路径，
// 前端 `img.src = node.icon` 就完事。

// iconDir 图标目录，相对 index.html。
const iconDir = "icons/"

// 图标路径。取值与 `src-java/src/main/resources/icons/` 下的文件名一一对应。
const (
	iconDirectory  = iconDir + "directory.png"
	iconFile       = iconDir + "file.png"
	iconFileJar    = iconDir + "file-jar.png"
	iconFileZip    = iconDir + "file-zip.png"
	iconFileClass  = iconDir + "file-class.png"
	iconFileJS     = iconDir + "file-js.png"
	iconFileHTML   = iconDir + "file-html.png"
	iconFileYml    = iconDir + "file-yml.png"
	iconFileYaml   = iconDir + "file-yaml.png"
	iconFileProps  = iconDir + "file-properties.png"
	iconFileXML    = iconDir + "file-xml.png"
	iconFileDocx   = iconDir + "file-docx.png"
	iconFilePptx   = iconDir + "file-pptx.png"
	iconFileXlsx   = iconDir + "file-xlsx.png"
	iconPatch      = iconDir + "patch.png"
	iconFileMark   = iconDir + "file-mark.png"
	iconReadme     = iconDir + "readme.png"
	iconPatchUp    = iconDir + "patch-up.png"
	iconSpringBoot = iconDir + "spring-boot.png"

	// 下面两张不是树节点图标，原版用在别处：
	//   iconSearch —— HomeView 工具栏那个"只有图标"的搜索按钮。原版用这张
	//                 search.png；Go 版 2026-10-16 起改用**内联 SVG**（细线条
	//                 放大镜，见 frontend/dist/index.html 与 MIGRATION §五 58），
	//                 所以 **Go 与前端都不再引用这张图**。留着它纯粹是为了让
	//                 TestIconFilesExist 的"目录里不许有多余文件"这条反向校验
	//                 仍然成立 —— 少了它，测试会以为这张图是拷错了。
	//   iconTime   —— 搜索历史那一行的时钟，由 SearchItemView.Icon 带出去。
	// 放进同一张表是为了让上面那条反向校验成立。
	iconSearch = iconDir + "search.png"
	iconTime   = iconDir + "time.png"
)

// iconByExt 按文件名后缀取专属图标，返回空串表示"没有专属图标"。
//
// 逐条照搬 IConUtil.getPathByFileExt，三个点必须照搬：
//
//   - 后缀匹配**区分大小写**。Java 的 `String.endsWith` 是大小写敏感的，
//     所以 `Foo.JAR` / `README.TXT` 拿不到专属图标，会落到兜底的 file.png。
//     这里用 strings.HasSuffix，行为一致 —— 别"顺手"改成 HasSuffixFold。
//   - `.sign` 给的是 **patch.png**（补丁图标），不是通用文件图标。
//   - `.doc/.docx`、`.ppt/.pptx`、`.xls/.xlsx` 是成对的，别只写一半。
//
// 另外注意 `.xml` 与 `.xlsx` 不冲突（"xlsx" 并不以 "xml" 结尾），
// 所以原版的判断顺序在这里不影响结果；保持原顺序只是为了方便对照。
func iconByExt(name string) string {
	switch {
	case strings.HasSuffix(name, constant.DotJar):
		return iconFileJar
	case strings.HasSuffix(name, constant.DotZip):
		return iconFileZip
	case strings.HasSuffix(name, constant.DotClass):
		return iconFileClass
	case strings.HasSuffix(name, ".js"):
		return iconFileJS
	case strings.HasSuffix(name, ".html"):
		return iconFileHTML
	case strings.HasSuffix(name, ".yml"):
		return iconFileYml
	case strings.HasSuffix(name, ".yaml"):
		return iconFileYaml
	case strings.HasSuffix(name, ".properties"):
		return iconFileProps
	case strings.HasSuffix(name, ".xml"):
		return iconFileXML
	case strings.HasSuffix(name, ".docx"), strings.HasSuffix(name, ".doc"):
		return iconFileDocx
	case strings.HasSuffix(name, ".pptx"), strings.HasSuffix(name, ".ppt"):
		return iconFilePptx
	case strings.HasSuffix(name, ".xlsx"), strings.HasSuffix(name, ".xls"):
		return iconFileXlsx
	case strings.HasSuffix(name, constant.DotSign):
		return iconPatch
	}
	return ""
}

// iconName 返回节点该用的图标路径，对应 Java TreeNodeUtil.getIconImage。
//
// **判定顺序不能改** —— 四条特例优先于后缀匹配：
//
//  1. 应用包的 ROOT 且直接子级里有 `BOOT-INF/` -> spring-boot.png
//     （Spring Boot 胖包。补丁根不给这个图标 —— 原版有 `!node.isPatch()`）
//  2. CUSTOM_ROOT（用户手动标记的根）          -> file-mark.png
//  3. README（说明文件）                       -> readme.png
//  4. 路径正好是 `META-INF/patch-up/`          -> patch-up.png
//  5. 有专属后缀图标                           -> 见 iconByExt
//  6. 目录                                     -> directory.png
//  7. 其余                                     -> file.png
func iconName(n *tree.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == constant.TreeNodeTypeRoot && !n.IsPatch && isSpringBootNode(n) {
		return iconSpringBoot
	}
	if n.Type == constant.TreeNodeTypeCustomRoot {
		return iconFileMark
	}
	if n.Type == constant.TreeNodeTypeReadme {
		return iconReadme
	}
	if n.Path == constant.PatchUpPath {
		return iconPatchUp
	}
	if ic := iconByExt(n.Name); ic != "" {
		return ic
	}
	if n.Dir() {
		return iconDirectory
	}
	return iconFile
}

// isSpringBootNode 对应 Java TreeNodeUtil.isSpringBootNode：
// 直接子级里有一个路径正好是 `BOOT-INF/` 就算 Spring Boot 胖包。
//
// 只看**直接**子级、且比的是**路径**不是名字 —— 别改成递归查找。
// 目录条目的 Path 保留尾部斜杠（见 pack.buildFromZipReader 的保真注释），
// 所以这里与 constant.BootInfPath 能对上。
func isSpringBootNode(n *tree.Node) bool {
	for _, c := range n.Children {
		if c.Path == constant.BootInfPath {
			return true
		}
	}
	return false
}
