//go:build !windows

package util

import (
	"os"
	"strings"
)

// osLangIsEnglish 报告系统语言是不是英语（非 Windows 实现）。
//
// 只回答这一个问题，因为原版 I18nUtil.getSysLanguage 也只区分这一件事。
//
// 走环境变量近似 Java 的 Locale.getDefault().getLanguage()：
// 取 LC_ALL / LC_MESSAGES / LANG 里第一个有值的，截到 '_' 或 '.' 之前
// （`zh_CN.UTF-8` → `zh`），再看是不是 "en"。
// 都读不到时返回 false —— 也就是回落中文，与原版"非英语一律中文"的方向一致。
func osLangIsEnglish() bool {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		if i := strings.IndexAny(v, "_.@"); i >= 0 {
			v = v[:i]
		}
		return strings.EqualFold(v, "en")
	}
	return false
}
