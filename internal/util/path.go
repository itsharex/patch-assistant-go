package util

import (
	"regexp"
	"strings"
)

// 统一分隔符。Java 侧 FileNameUtil 只认这两种，包内路径一律归一成 '/'。
const (
	Separator    = "/"
	SeparatorWin = "\\"
	ExtSeparator = '.'
)

// FileNameClass 是 Java FileNameUtil.FILE_NAME_REGEX 的等价字符类。
//
// Java 原文："[a-zA-Z-\\\\/.0-9\\u4e00-\\u9fa5_-]"，展开后是
// 字母、数字、'_'、'.'、'/'、'\' 以及 CJK 基本区。
// 注意 Java 的 "\u4e00" 转义在 Go 的 RE2 里必须写成 "\x{4e00}"，
// 直接照抄会编译报错。
const FileNameClass = `[a-zA-Z0-9_./\-\\\x{4e00}-\x{9fa5}]`

// FileNameRegex 单字符文件名类，供调用方自行拼接时复用。
var FileNameRegex = regexp.MustCompile(FileNameClass)

// PathSeparatorPattern 路径分隔符（'/' 与 '\'）。
var PathSeparatorPattern = regexp.MustCompile(`[/\\]`)

// ReadMePattern 是 README.txt 升级指令的解析正则，逐字对齐 Java：
//
//	"([!+-?]?)[\\\\/]?(CLASS+)(\\s+[\\\\/]?(CLASS+))?(\\s+(CLASS*))?"
//
// 保真提示：首段 "[!+-?]" 在 Java 里并不是"四个符号之一"，
// 因为 '+' 与 '?' 之间形成了字符区间 [+-?]，即 0x2B..0x3F
// （含 + - . / 0-9 : ; < = > ?）。Go 的 RE2 同样按区间解析，
// 所以这里照抄即可，行为与原版一致。
// 区间里的非四种符号最终会被 ChangeTypeOfCode 归为 NONE。
var ReadMePattern = regexp.MustCompile(
	`([!+-?]?)[\\/]?(` + FileNameClass + `+)(\s+[\\/]?(` + FileNameClass + `+))?(\s+(` + FileNameClass + `*))?`)

// Name 去掉扩展名返回主名。
// 对应 Java FileNameUtil.getName。
func Name(fileName string) string {
	i := strings.LastIndexByte(fileName, ExtSeparator)
	if i == -1 {
		return fileName
	}
	return fileName[:i]
}

// Ext 返回扩展名（不含点），无扩展名返回空串。
// 对应 Java FileNameUtil.getExt（Java 返回 null，Go 用空串表达）。
func Ext(fileName string) string {
	i := strings.LastIndexByte(fileName, ExtSeparator)
	if i == -1 {
		return ""
	}
	return fileName[i+1:]
}

// FileName 从路径中取文件名（含后缀）。
// 对应 Java FileNameUtil.getFileName。
func FileName(path string) string {
	if IsBlank(path) {
		return path
	}
	path = UnPerfectDirPath(path)
	path = strings.ReplaceAll(path, SeparatorWin, Separator)
	i := strings.LastIndex(path, Separator)
	if i == -1 {
		return path
	}
	return path[i+1:]
}

// GetDirPath 取所在目录，无分隔符时返回空串。
// 对应 Java FileNameUtil.getDirPath。
func GetDirPath(path string) string {
	if IsBlank(path) {
		return path
	}
	path = UnPerfectDirPath(path)
	path = strings.ReplaceAll(path, SeparatorWin, Separator)
	i := strings.LastIndex(path, Separator)
	if i == -1 {
		return ""
	}
	return path[:i]
}

// JoinPath 拼接两段路径，避免重复分隔符。
// 对应 Java FileNameUtil.joinPath（不做 Clean，保留原样）。
func JoinPath(path1, path2 string) string {
	if strings.HasSuffix(path1, Separator) {
		return path1 + strings.TrimPrefix(path2, Separator)
	}
	if strings.HasPrefix(path2, Separator) {
		return path1 + path2
	}
	return path1 + Separator + path2
}

// PerfectDirPath 保证路径以 '/' 结尾。
// 对应 Java FileNameUtil.perfectDirPath。
func PerfectDirPath(path string) string {
	if strings.HasSuffix(path, Separator) || strings.HasSuffix(path, SeparatorWin) {
		return path
	}
	return path + Separator
}

// UnPerfectDirPath 去掉路径尾部的分隔符。
// 对应 Java FileNameUtil.unPerfectDirPath。
func UnPerfectDirPath(path string) string {
	if strings.HasSuffix(path, Separator) || strings.HasSuffix(path, SeparatorWin) {
		return path[:len(path)-1]
	}
	return path
}

// SplitPath 按 '/' 或 '\' 切分路径。
// 对应 Java FileNameUtil.splitPath。
func SplitPath(path string) []string {
	return PathSeparatorPattern.Split(path, -1)
}

// NormalizePath 把 '\' 归一成 '/' 并去掉尾部斜杠，用于包内路径比对。
func NormalizePath(path string) string {
	if path == "" {
		return path
	}
	path = strings.ReplaceAll(path, SeparatorWin, Separator)
	return strings.TrimSuffix(path, Separator)
}

// ToPlatformPath 把路径分隔符统一成当前平台的形式。
//
// 对应 Java `java.io.File#getPath()` —— File 的构造函数会把路径里的分隔符
// 统一成 File.separatorChar，在 Windows 上就是把 '/' 换成 '\'。
//
// 为什么必须对齐：应用包/补丁包的根节点 Path 与写进补丁签名的
// originAppPath 都取自它。Go 若原样保留用户传入的 '/'，Windows 下写出的
// 签名会和 Java 版差一个反斜杠（黄金基准比对时暴露过）。
func ToPlatformPath(path string) string {
	if !IsWindows() {
		return path
	}
	return strings.ReplaceAll(path, Separator, SeparatorWin)
}

// CompareNameIgnoreCase 按大小写不敏感比较名称。
//
// 对应 Java String#compareToIgnoreCase。Java 是逐 char 折叠，
// 这里按 rune 折叠后比较；对 ASCII 与 CJK 文件名结果一致。
//
// 保真提示：这个方法原本是给"树同级排序"用的，但那个排序在 Java 里
// 【从未生效】—— TreeNode.getAndInitChildren 构造 SortLinkedList 时传了
// 比较器，而 SortLinkedList.add 被覆写成纯追加，比较器一次都没被调用
// （见 tree.Node.AddChild 的说明）。Go 侧已同步改成追加，所以本方法
// 当前没有调用方，保留是为了记录上游确实定义过这条规则。
func CompareNameIgnoreCase(a, b string) int {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	default:
		return 0
	}
}
