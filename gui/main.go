// Package main 是 PatchAssistant2J 的 Go 版 GUI 入口（Wails v2）。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package main

import (
	"embed"

	"cn.cpoet.patch.assistant/internal/constant"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// windowTitle 是原生窗口标题栏的文本。
//
// 对齐原版 PatchAssistantApplication:41：
//
//	stage.setTitle(I18nUtil.t("app.name", AppConst.APP_NAME) + " By " + AppConst.APP_AUTHOR_NAME)
//
// 原版中文下 `app.name` = "补丁助手"，所以标题是「补丁助手 By CPoet」；Go 版界面
// 文案不走 i18n、用的是英文应用名，所以这里是「PatchAssistant2J By CPoet」。
//
// 拼的时候**必须走 constant**（而不是写死字符串）：这样 `AppAuthorName` 有真实
// 引用点、不会被 Go 的"未引用包级常量"消除掉，作者名才会真的进 exe。前端页面
// 里那个同名的 `.brand` 在 index.html，两处要一起改。
const windowTitle = constant.AppName + " By " + constant.AppAuthorName

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     windowTitle,
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 600,
		// 无边框窗口：原生标题栏会在窗口最上方再显示一遍「PatchAssistant2J By CPoet」，
		// 而工具栏（frontend/dist/index.html 的 .brand）已经有一份，重复。
		// 所以去掉原生标题栏，把它的三个窗口按钮（最小化/最大化/关闭）搬到工具栏右端
		// （.win-ctls，紧跟「执行打补丁」之后）。
		//
		// 代价与对策：
		//   - 拖动/双击最大化要自己挂 —— 工具栏挂 CSS `--wails-draggable: drag`，
		//     按钮挂 no-drag（见 app.css / app.js）。
		//   - **缩放不用自己做**：无边框模式下 Wails 保留 WS_THICKFRAME，并在
		//     navigationCompleted 里把 window.wails.flags.enableResize 置 true，
		//     前端运行时按边缘命中测试发 resize: 消息（见 wails v2 windows/frontend.go）。
		//   - Aero 阴影 / Win11 圆角默认保留（未设 DisableFramelessWindowDecorations）。
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 窗口底色：与前端 CSS 的 --bg 保持一致，避免窗口出现时闪一下。
		// 亮色时它就是 --bg 的值；切到暗色由前端调 App.SetWindowBackground
		// 换掉（那时前端已经跑起来）。启动那一瞬只能用这个值 —— 前端还没
		// 加载完，谁也不知道用户上次选的是暗色。
		BackgroundColour: &options.RGBA{R: 245, G: 247, B: 250, A: 1},
		// 拖放：EnableFileDrop 打开后，WebView2 才会把拖入文件的【真实路径】
		// 交给 Go（`file:drop:<x>:<y>` 消息）。浏览器自身的 HTML5 拖放只给
		// File 对象、不给路径，而建树必须拿到路径，所以这条必须开。
		// 落点判定用 CSS 属性 --wails-drop-target（默认值 "drop"），
		// 前端把它挂在两个面板上 —— 详见 app.css 与 app.js。
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Windows: &windows.Options{
			// 不透明窗口（避免透明窗口在部分显卡驱动下闪烁）。
			// 无边框窗口的阴影/圆角由 Wails 默认提供，这里不用管；
			// 边缘缩放由 Wails 的 enableResize 机制负责（见上面的 Frameless 注释）。
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
