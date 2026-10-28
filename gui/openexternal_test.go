package main

import (
	"os/exec"
	"testing"

	"cn.cpoet.patch.assistant/internal/util"
)

// 打开包里的 .xlsx / .pdf 这类"没有解析器"的文件时，Go 版会闪一个 cmd 黑框。
// 修法落在这两条上，分开测：
//
//  1. 命令构造 —— Windows 上必须是 `cmd /c start "" <路径>`，那个空参数是
//     窗口标题占位，少了它带引号的路径会被 start 当成标题、文件打不开。
//  2. 去掉控制台 —— hideWindow 必须传 Win32 的 CREATE_NO_WINDOW。
//
// 为什么不用 Go 的 SysProcAttr.HideWindow（已实测，见 hidewindow_windows.go 的注释）：
// 它映射到 STARTUPINFO 的 SW_HIDE，控制台**照样会分配**，只是窗口建出来时隐藏。
// 实测 GUI 父进程 spawn `cmd /c <控制台子进程>`、让子进程自报状态：
//
//	不加标志            -> HAS-CONSOLE, visible=true   （黑框，= 修复前）
//	HideWindow: true    -> HAS-CONSOLE, visible=false
//	CREATE_NO_WINDOW    -> NO-CONSOLE
//
// 原版 Java 走 Desktop.open()，由 JVM 交给 ShellExecute，从来不会弹控制台 ——
// 所以这是移植引入的缺陷，不是刻意偏差。

// createNoWindowBit 是 Win32 的 CREATE_NO_WINDOW（0x08000000）。
// 测试里不能直接引用 hidewindow_windows.go 里的 createNoWindow 常量 ——
// 那个文件带 //go:build windows，非 Windows 平台上编译不到，会连带把测试编译失败。
const createNoWindowBit = 0x08000000

// TestOpenCommandForWindowsUsesStartWithTitlePlaceholder Windows 分支必须用
// `cmd /c start "" <路径>`，且**空标题占位一个都不能少**。
func TestOpenCommandForWindowsUsesStartWithTitlePlaceholder(t *testing.T) {
	if !util.IsWindows() {
		t.Skip("只在 Windows 上有意义")
	}
	const path = `C:\Users\test\一个 带空格 的表.xlsx`
	cmd := openCommandFor(path)

	want := []string{"/c", "start", "", path}
	if len(cmd.Args) != 1+len(want) {
		t.Fatalf("参数个数 = %d（%q），期望 %d", len(cmd.Args), cmd.Args, 1+len(want))
	}
	for i, w := range want {
		if cmd.Args[i+1] != w {
			t.Errorf("第 %d 个参数 = %q，期望 %q", i+1, cmd.Args[i+1], w)
		}
	}
}

// TestHideWindowRequestsNoConsole 打开外部文件前必须让子进程不带控制台。
func TestHideWindowRequestsNoConsole(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "start", "", "whatever")
	hideWindow(cmd)

	if !util.IsWindows() {
		// 非 Windows 上 hideWindow 是空实现，SysProcAttr 应保持 nil。
		if cmd.SysProcAttr != nil {
			t.Errorf("非 Windows 上 SysProcAttr 应为 nil，实际 = %+v", cmd.SysProcAttr)
		}
		return
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr 为 nil —— 没有去掉控制台，cmd 黑框会闪")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindowBit == 0 {
		t.Errorf("CreationFlags = %#x，必须含 CREATE_NO_WINDOW(%#x) —— "+
			"只设 HideWindow 的话控制台照样分配，黑框还是会闪",
			cmd.SysProcAttr.CreationFlags, createNoWindowBit)
	}
}
