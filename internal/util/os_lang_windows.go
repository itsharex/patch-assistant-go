//go:build windows

package util

import "syscall"

var procGetUserDefaultUILanguage = syscall.NewLazyDLL("kernel32.dll").
	NewProc("GetUserDefaultUILanguage")

// winLangEnglish 是 Windows 的 primary language id：0x09 = 英语。
// 参见 MAKELANGID / LANG_ENGLISH。
const winLangEnglish = 0x09

// osLangIsEnglish 报告系统 UI 语言是不是英语。
//
// 只回答这一个问题，因为原版 I18nUtil.getSysLanguage 也只区分这一件事
// （英语 → en-US，其余 → zh-CN）。
//
// 用 GetUserDefaultUILanguage 而不是读环境变量：从资源管理器启动的 GUI
// 进程通常没有任何 LANG/LC_ALL，只有系统 API 靠得住。
// LANGID 的低 10 位是 primary language id，对应 Java
// `Locale.getDefault().getLanguage()` 拿到的语言子标签。
func osLangIsEnglish() bool {
	r, _, _ := procGetUserDefaultUILanguage.Call()
	return uint16(r)&0x03FF == winLangEnglish
}
