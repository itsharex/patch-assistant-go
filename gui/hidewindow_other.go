//go:build !windows

package main

import "os/exec"

// hideWindow 非 Windows 平台无需处理（没有"为子进程分配控制台"这回事）。
// 与 internal/decompile 的同名函数保持一致的形状，供 openPathWithOS 跨平台调用。
func hideWindow(cmd *exec.Cmd) {}
