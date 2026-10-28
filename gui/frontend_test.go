package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 前端资源在 dist 里原样嵌入 exe（wails.json 的 frontend:build 是空串，
// `wails build` 只打印 "No Build command. Skipping."），所以 dist 里的文件
// 就是运行时真正跑的代码 —— 没有编译期检查兜底，只能靠测试盯。

func frontendPath(name string) string {
	return filepath.Join("frontend", "dist", name)
}

func readFrontend(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(frontendPath(name))
	if err != nil {
		t.Fatalf("读不到 %s: %v", name, err)
	}
	return string(b)
}

// TestAppJSIsSyntacticallyValid 用 node --check 给 app.js 做语法校验。
//
// 为什么值得单开一条：app.js 是**唯一**的前端源文件，它一旦有语法错误，
// 浏览器会整份不执行 —— 页面照常渲染（HTML/CSS 是好的），但所有事件绑定
// 全没了：工具栏按钮没反应、右键菜单不出来、`initDrop()` 没跑于是
// `window.runtime.OnFileDrop` 没注册，把 jar 拖进窗口就落到浏览器默认
// 行为上、变成"下载"。
//
// 这种"看起来正常、其实全哑"的故障极难定位 —— 2026-10-20 真实踩过：
// `renderTempPatch()` 收尾之后多留了一段重复尾巴，整份 app.js 解析失败，
// 用户第一反应就是"拖进去让我下载"。
//
// 机器上没有 node 就跳过：这条只做语法体检，不该给项目引入构建依赖。
func TestAppJSIsSyntacticallyValid(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("没有 node，跳过 app.js 语法校验")
	}
	out, err := exec.Command(node, "--check", frontendPath("app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("app.js 语法错误（node --check 失败）：\n%s", out)
	}
}

// TestIndexHTMLReferencesAppJS 确认页面确实加载了 app.js。
//
// 有语法错误的 app.js 只是"不执行"，而**漏掉 script 标签**是同一个症状的
// 另一种成因（页面好看、什么都没反应）。两种都在这条测试的射程内。
func TestIndexHTMLReferencesAppJS(t *testing.T) {
	html := readFrontend(t, "index.html")
	if !strings.Contains(html, `src="app.js"`) {
		t.Error("index.html 里没有 <script src=\"app.js\">：页面会渲染但完全没有交互")
	}
	if n := strings.Count(html, `src="app.js"`); n > 1 {
		t.Errorf("index.html 里 app.js 被引用了 %d 次：重复加载会让 init() 跑两遍", n)
	}
}

// TestAppJSIDSexist 确认 `$('id')` 引用的元素真的存在。
//
// `$` 就是 getElementById，取不到返回 null —— 后面 `.addEventListener`
// 立刻抛 TypeError，而这个异常会**中断整个 init()**：在它之后绑定的
// 功能（包括拖放）全部失效，界面却看不出任何异常。
//
// 有一批元素是运行时才建的（查找栏、跳转行列弹窗、内容预览窗口的
// 十六进制提示），所以除了 index.html 也接受 app.js 里的动态创建
// （`id="x"` 模板串 / `el.id = 'x'`）。
func TestAppJSIDSexist(t *testing.T) {
	js := readFrontend(t, "app.js")
	html := readFrontend(t, "index.html")

	ref := regexp.MustCompile(`\$\('([^']+)'\)`)
	var missing []string
	seen := map[string]bool{}
	for _, m := range ref.FindAllStringSubmatch(js, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		if strings.Contains(html, `id="`+id+`"`) {
			continue
		}
		// 运行时创建：模板串里的 id="x"，或 xx.id = 'x'
		if strings.Contains(js, `id="`+id+`"`) || strings.Contains(js, `.id = '`+id+`'`) {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		t.Errorf("app.js 引用了不存在的元素 id（会在 init() 里抛 TypeError，拖放等功能随之失效）: %v", missing)
	}
}

// TestPaneDeclaresDropTarget 盯住拖放的落点标记。
//
// Wails 的落点判定是「光标下最深那个元素的 `--wails-drop-target` 计算值
// 必须等于 drop」（见 v2 的 draganddrop.js checkStyleDropTarget）。自定义
// 属性会继承，所以只要 .pane 上有这一行，面板里的树行也算落点。
//
// 把它删掉不会有任何报错 —— 拖到面板上就是"什么都不发生"。所以钉一下。
func TestPaneDeclaresDropTarget(t *testing.T) {
	css := readFrontend(t, "app.css")
	if !strings.Contains(css, "--wails-drop-target: drop") {
		t.Error("app.css 里没有 `--wails-drop-target: drop`：Wails 不会把面板当成拖放落点")
	}
	// 高亮规则必须挂在 .pane 上。写成裸的 .wails-drop-target-active 会让
	// 面板里每一行都亮（Wails 会把 class 加到落点及其所有带该属性的祖先）。
	if !strings.Contains(css, ".pane.wails-drop-target-active") {
		t.Error("app.css 里没有 `.pane.wails-drop-target-active`：拖入时的高亮会写到每一行上")
	}
}
