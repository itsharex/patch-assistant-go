package util

import (
	"os"
	"path/filepath"
	"runtime"

	"cn.cpoet.patch.assistant/internal/constant"
)

// IsWindows 是否 Windows。
// 对应 Java OSUtil.isWindows。
func IsWindows() bool { return runtime.GOOS == "windows" }

// IsMacOS 是否 macOS。
// 对应 Java OSUtil.isMacOS。
func IsMacOS() bool { return runtime.GOOS == "darwin" }

// IsLinux 是否 Linux。
// 对应 Java OSUtil.isLinux。
func IsLinux() bool { return runtime.GOOS == "linux" }

// GetUserHome 用户主目录。
// 对应 Java OSUtil.getUserHome（Java 用 user.home 系统属性，这里用 os.UserHomeDir）。
func GetUserHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// GetUserName 当前用户名。
// 对应 Java EnvUtil.getUserName —— 优先读环境变量，读不到再回落系统。
func GetUserName() string {
	for _, k := range []string{"USERNAME", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// SysLanguage 返回系统语言代码（"en-US" / "zh-CN"）。
//
// 判定规则对齐 Java I18nUtil.getSysLanguage：
//
//	String language = Locale.getDefault().getLanguage();
//	if ("en".equals(language)) return EN_US;
//	return ZH_CN;
//
// **注意方向** —— 原版是"只有英语算英文，其余一律中文"，不是"是中文才给中文"。
// 所以法语系统在原版下也会拿到中文界面。别按直觉改成后者。
//
// 早期版本读的是 LC_ALL / LC_MESSAGES / LANG 环境变量，而且方向写反了
// （认不出中文就回落 en-US）。两个问题都会显形：Windows 上这三个变量通常
// 根本没设（从资源管理器启动的程序更是一个都没有），于是界面文案会变成英文。
// 现在语言子标签交给平台实现（Windows 走系统 UI 语言）。
func SysLanguage() string {
	if osLangIsEnglish() {
		return constant.I18NEnUS.Code
	}
	return constant.I18NZhCN.Code
}

// GetAppConfigPath 返回应用配置目录，必要时创建。
//
// 对应 Java OSUtil.getAppConfigPath(String)，目录规则照搬：
//
//	Windows : %APPDATA%/<appName>
//	macOS   : ~/Library/Application Support/<appName>
//	Linux   : $XDG_CONFIG_HOME/<appName> 或 ~/.config/<appName>
//
// 与 Java 版一致地保留"创建失败则回落用户主目录"的兜底分支。
func GetAppConfigPath(appName string) (string, error) {
	var base string
	switch {
	case IsWindows():
		base = os.Getenv("APPDATA")
	case IsMacOS():
		base = filepath.Join(GetUserHome(), "Library", "Application Support")
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); !IsBlank(xdg) {
			base = xdg
		} else {
			base = filepath.Join(GetUserHome(), ".config")
		}
	}

	appDir := filepath.Join(base, appName)
	if err := os.MkdirAll(appDir, 0o755); err == nil {
		return appDir, nil
	}

	// 兜底：用户主目录下同名目录
	fallback := filepath.Join(GetUserHome(), appName)
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", err
	}
	return fallback, nil
}
