//go:build windows

package javac

import (
	"os/exec"
	"syscall"
)

// exeExt Windows 上可执行文件的后缀。
const exeExt = ".exe"

// hideWindow 让子进程不弹控制台窗口。
//
// 这条不能省：本程序是 GUI（Wails），启动 javac 子进程时 Windows 会为它
// 分配一个控制台，用户会看到黑框闪一下甚至一直挂着。
// 与 internal/decompile 里同名函数是一回事，只是分属两个包。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
