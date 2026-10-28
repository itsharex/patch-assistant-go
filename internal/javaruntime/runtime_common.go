// Package javaruntime 的跨平台部分：内置运行时的"要不要用"开关。
//
// 单独放一个没有 build tag 的文件，是因为常量与 Disabled 两个平台都要用，
// 而 Windows 那份是 `//go:build windows`、非 Windows 那份是 `//go:build !windows`，
// 常量写在任一份里都会让另一份编译失败。
package javaruntime

import (
	"errors"
	"os"
	"strings"
)

// envNoBundled 是"跳过内置运行时"的开关名。
const envNoBundled = "PATCH_ASSISTANT_NO_BUNDLED_JAVA"

// ErrDisabled 表示调用方要求跳过内置运行时（见 Disabled）。
var ErrDisabled = errors.New("已按要求跳过内置 Java 运行时")

// Disabled 报告是否要求跳过内置运行时 —— 环境变量 PATCH_ASSISTANT_NO_BUNDLED_JAVA
// 非空即生效。
//
// 留这个逃生口是为了两件事：① 想用自己装的特定 JDK（例如要验证"JDK 8 上也能编
// Java 8 的类"这条分支）；② 内置运行时解压不出来时，先绕开它排查是不是运行时
// 本身的问题。设了它之后，查找顺序退回 JAVA_HOME → PATH。
func Disabled() bool {
	return strings.TrimSpace(os.Getenv(envNoBundled)) != ""
}
