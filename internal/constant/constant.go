// Package constant 汇集全局常量，对应 Java 侧 cn.cpoet.patch.assistant.constant。
//
// Java 用 interface 承载常量（隐式 public static final），Go 用 const 块，
// 命名保持一一对应，便于与 Java 源码对照。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package constant

// AppConst 应用常量。
const (
	AppName             = "PatchAssistant2J"
	AppProjectName      = "patch-assistant-to-java"
	AppLockName         = AppName + ".lock"
	AppAuthorName       = "CPoet"
	AppAuthorEmail      = "llzero54@foxmail.com"
	AppAuthorURL        = "http://www.cpoet.cn"
	ConfigFileName      = "application.xml"
	ApplicationInfoName = AppName + ".xml"
	StyleFileName       = "/css/theme.css"
	StyleDarkFileName   = "/css/theme-dark.css"
	ReadmeFile          = "README.txt"
	PatchUpDir          = "patch-up"
	ErrorLogFile        = "error.log"
)

// FileExt 常用文件后缀。
const (
	ExtZip   = "zip"
	ExtJar   = "jar"
	ExtClass = "class"
	ExtJava  = "java"
	ExtTxt   = "txt"
	ExtSign  = "sign"

	DotZip   = "." + ExtZip
	DotJar   = "." + ExtJar
	DotClass = "." + ExtClass
	DotJava  = "." + ExtJava
	DotTxt   = "." + ExtTxt
	DotSign  = "." + ExtSign
)

// JarInfoConst jar 包内固定路径。
const (
	MetaInfo     = "META-INF"
	MetaInfoDir  = MetaInfo + "/"
	ManifestFile = "MANIFEST.MF"
	ManifestPath = MetaInfoDir + ManifestFile
	PatchUpPath  = MetaInfoDir + PatchUpDir + "/"
	SourceSrc    = "src"
	SourceLib    = "lib"
	MvnPomXml    = "pom.xml"
	MvnPomProps  = "pom.properties"
)

// SpringConst Spring Boot fat-jar 内部布局。
const (
	BootInfPath = "BOOT-INF/"
	ClassesPath = BootInfPath + "classes/"
	LibPath     = BootInfPath + "lib/"
)

// ParamNameConst 命令行参数名。
const (
	ParamPatch  = "patch"
	ParamTarget = "target"
	ParamDev    = "dev"
)

// OSExplorerConst 各平台文件管理器命令。
const (
	ExplorerWindows  = "explorer"
	ExplorerMacOS    = "open"
	ExplorerGnome    = "files"
	ExplorerNautilus = "nautilus"
	ExplorerKDE      = "dolphin"
)

// CharsetConst 字符集名。
const (
	CharsetUTF8 = "UTF-8"
	CharsetGBK  = "GBK"
)

// LoadStatusConst 加载状态位（Java 侧为 int 位掩码）。
const (
	LoadNotLoaded     = 0
	LoadAppTreeLoad   = 1
	LoadPatchTreeLoad = 1 << 1
)
