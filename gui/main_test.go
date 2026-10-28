package main

import (
	"strings"
	"testing"

	"cn.cpoet.patch.assistant/internal/constant"
)

// TestWindowTitleCarriesAuthor 钉住原生窗口标题栏的格式。
//
// 对齐原版 PatchAssistantApplication:41：
//
//	stage.setTitle(I18nUtil.t("app.name", AppConst.APP_NAME) + " By " + AppConst.APP_AUTHOR_NAME)
//
// 断言故意用**字面量**、而不是"再拿常量拼一遍"（那样恒真、等于没测）：
// 谁把 `" By "` 改成 `" - "`、或者把作者名换掉，这条就会红。
//
// 前端页面里那个同名的 `.brand` 不在 Go 测试的覆盖范围内，由前端走查的
// `brand-shows-author` 那条盯着。
func TestWindowTitleCarriesAuthor(t *testing.T) {
	const want = "PatchAssistant2J By CPoet"
	if windowTitle != want {
		t.Errorf("windowTitle = %q，期望 %q", windowTitle, want)
	}
}

// TestWindowTitleBuiltFromConstants 确认标题是**拼**出来的，不是写死的。
//
// 这条守的是"作者名会不会退化成死常量"：Go 编译器会消除未被引用的包级常量，
// 标题里若写死 "CPoet"、不引用 `constant.AppAuthorName`，那个常量就从二进制里
// 消失了 —— 作者名只在源码里存在，exe 里搜不到。
func TestWindowTitleBuiltFromConstants(t *testing.T) {
	if !strings.Contains(windowTitle, constant.AppAuthorName) {
		t.Errorf("windowTitle 里没有 constant.AppAuthorName（%q）：%q",
			constant.AppAuthorName, windowTitle)
	}
	if !strings.HasPrefix(windowTitle, constant.AppName) {
		t.Errorf("windowTitle 不是以 constant.AppName（%q）开头：%q",
			constant.AppName, windowTitle)
	}
}
