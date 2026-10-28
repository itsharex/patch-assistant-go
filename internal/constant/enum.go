package constant

import "strings"

// ChangeType 变更类型，对应 Java 的 ChangeTypeEnum。
//
// 注意 Code 是 README.txt 升级指令里的前缀符号，语义与显示无关，
// 不要用 String() 去做解析。
type ChangeType int

const (
	ChangeNone   ChangeType = iota
	ChangeAdd               // "+"
	ChangeMod               // "!"
	ChangeDel               // "-"
	ChangeIgnore            // "?"
)

// ChangeTypeCodes 与 Java 的枚举顺序严格一致。
var ChangeTypeCodes = [...]string{"", "+", "!", "-", "?"}

// Code 返回变更类型对应的指令符号。
func (c ChangeType) Code() string {
	if c < 0 || int(c) >= len(ChangeTypeCodes) {
		return ""
	}
	return ChangeTypeCodes[c]
}

// ChangeTypeOfCode 按指令符号反查变更类型，未知符号回落 ChangeNone。
// 对应 Java ChangeTypeEnum.ofCode。
func ChangeTypeOfCode(code string) ChangeType {
	for i, v := range ChangeTypeCodes {
		if v == code {
			return ChangeType(i)
		}
	}
	return ChangeNone
}

// I18N 语言项，对应 Java 的 I18NEnum。
type I18N struct {
	Code string
	Name string
}

// 支持的语言。
var (
	I18NEnUS = I18N{Code: "en-US", Name: "英文(默认)"}
	I18NZhCN = I18N{Code: "zh-CN", Name: "简体中文"}
)

// I18NValues 枚举全集，顺序与 Java 一致。
var I18NValues = []I18N{I18NEnUS, I18NZhCN}

// I18NOfCode 按语言码查找，未命中回落 en-US。
func I18NOfCode(code string) I18N {
	for _, v := range I18NValues {
		if v.Code == code {
			return v
		}
	}
	return I18NEnUS
}

// Theme 主题，对应 Java 的 ThemeEnum。
type Theme struct {
	Code string
	Name string
}

// 支持的明暗主题。
var (
	ThemeLight = Theme{Code: "light", Name: "明亮主题"}
	ThemeDark  = Theme{Code: "dark", Name: "暗夜主题"}
)

// ThemeValues 枚举全集，顺序与 Java 一致。
var ThemeValues = []Theme{ThemeLight, ThemeDark}

// ThemeOfCode 按主题码查找。
//
// 与 Java 行为一致：未命中返回 nil 而不是回落默认值 —— Java 侧这里
// 返回 null，调用方需要自己处理。
func ThemeOfCode(code string) *Theme {
	for i, v := range ThemeValues {
		if v.Code == code {
			return &ThemeValues[i]
		}
	}
	return nil
}

// TreeNodeType 节点类型，对应 Java 的 TreeNodeType。
//
// Java 侧用它标记节点在补丁流程中的状态，PatchMatchProcessor 里
// 会筛出 NONE 的节点参与按名匹配。
//
// 保真提示：Java 的声明顺序是 ADD, DEL, MOD, MANUAL_DEL, README, ROOT,
// CUSTOM_ROOT, NONE，与这里的编号不同。这是刻意的 —— TreeNodeType 只存在于
// 内存中，从不参与序列化（写进 META-INF/patch-up.sign 的是 ChangeType），
// 所以序号不影响输出保真度，而沿用阶段一的编号可以避免无谓的改动。
type TreeNodeType int

const (
	TreeNodeTypeNone TreeNodeType = iota
	TreeNodeTypeAdd
	TreeNodeTypeMod
	TreeNodeTypeDel
	// TreeNodeTypeManualDel 用户在界面上手动标记删除
	TreeNodeTypeManualDel
	// TreeNodeTypeReadme 说明文件节点
	TreeNodeTypeReadme
	// TreeNodeTypeRoot 补丁包/应用包的根节点
	TreeNodeTypeRoot
	// TreeNodeTypeCustomRoot 用户标记的自定义根节点
	TreeNodeTypeCustomRoot
)

// TreeNodeTypeNames 调试用名称，与 Java 枚举常量名一致。
var TreeNodeTypeNames = [...]string{"NONE", "ADD", "MOD", "DEL", "MANUAL_DEL", "README", "ROOT", "CUSTOM_ROOT"}

func (t TreeNodeType) String() string {
	if t < 0 || int(t) >= len(TreeNodeTypeNames) {
		return "UNKNOWN"
	}
	return TreeNodeTypeNames[t]
}

// NormalizePath 把 Windows 反斜杠统一成 '/'，并去掉尾部斜杠。
//
// 对应 Java FileNameUtil.unPerfectDirPath + replace('\\', '/') 的组合用法，
// 在压缩包内路径比对时到处都会用到，所以放在 constant 包里公用。
func NormalizePath(path string) string {
	if path == "" {
		return path
	}
	path = strings.ReplaceAll(path, "\\", "/")
	return strings.TrimSuffix(path, "/")
}
