package main

import "testing"

// TestDefaultPatchedPathIsAppPackItself 钉住 CLI 的默认输出路径。
//
// 与 GUI / Java 版口径一致：默认输出名**不加任何后缀** —— 原版
// `HomeView.handleSaveAppPack` 里 `setInitialFileName` 用的就是应用包
// 根节点自己的名字。这里曾自行拼过 "-patched"，已去掉。
//
// 代价已经写进用法说明：默认值与输入同名同目录，**不带 -o 直接跑会原地
// 覆盖应用包**，所以脚本里应当显式传 -o。
func TestDefaultPatchedPathIsAppPackItself(t *testing.T) {
	cases := []string{
		"app.jar",
		"/tmp/demo/app.jar",
		`D:\demo\app.jar`,
		"no-ext",
	}
	for _, in := range cases {
		if got := defaultPatchedPath(in); got != in {
			t.Errorf("defaultPatchedPath(%q) = %q，期望原样返回（不带 -patched 后缀）", in, got)
		}
	}
}
