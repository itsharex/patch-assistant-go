//go:build !windows

package decompile

import "os/exec"

// exeExt 非 Windows 平台没有可执行文件后缀。
const exeExt = ""

// hideWindow 非 Windows 平台无需处理（没有会弹控制台的问题）。
func hideWindow(cmd *exec.Cmd) {}
