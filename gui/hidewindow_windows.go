//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// createNoWindow 是 Win32 的 CREATE_NO_WINDOW 创建标志（0x08000000）。
//
// 为什么不用 Go 的 SysProcAttr.HideWindow：那个映射到的是 STARTUPINFO 的
// STARTF_USESHOWWINDOW + SW_HIDE —— **控制台照样会分配**，只是窗口建出来时
// 是隐藏的。实测（GUI 父进程 spawn `cmd /c` 一个控制台子进程，让子进程自己
// 报告 GetConsoleWindow()）：
//
//	HideWindow: true      -> HAS-CONSOLE  (hwnd 非 0)
//	CreationFlags: 0x08000000 -> NO-CONSOLE (GetConsoleWindow() == 0)
//
// 打开包里的 .xlsx 时闪的 cmd 黑框，正是"控制台被分配了"这件事本身 ——
// 所以必须用 CREATE_NO_WINDOW 把控制台整个去掉，而不是把窗口藏起来。
const createNoWindow = 0x08000000

// hideWindow 让子进程不弹控制台窗口。
//
// 这条不能省：本程序是 GUI（Wails），自己没有控制台。在 Windows 上 exec 一个
// **控制台子系统**的程序时，系统会为它新分配一个控制台 —— 用户会看到黑框闪一下。
//
// 具体场景：openPathWithOS 走 `cmd /c start "" <路径>` 打开包里的 .xlsx / .pdf 等
// 文件。cmd.exe 是控制台程序，于是每打开一个这种文件就闪一次黑框。原版 Java 走
// Desktop.open()，由 JVM 交给 ShellExecute，从来不会弹控制台 —— 所以这是移植引入
// 的缺陷，不是刻意偏差。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
