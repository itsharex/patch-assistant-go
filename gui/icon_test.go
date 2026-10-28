package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
)

// 树节点图标的测试。
//
// 判定规则本身没有 Java 侧的"黄金基准"可抄（原版直接返回 Image 对象），
// 所以这里是把 IConUtil.getPathByFileExt 与 TreeNodeUtil.getIconImage 两张表
// **逐条誊写**成期望值。改规则就得同时改这里和 gui/icon.go。

// TestIconByExt 钉住后缀 -> 图标的表。
func TestIconByExt(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"app.jar", iconFileJar},
		{"patch.zip", iconFileZip},
		{"Foo.class", iconFileClass},
		{"a.js", iconFileJS},
		{"index.html", iconFileHTML},
		{"app.yml", iconFileYml},
		{"app.yaml", iconFileYaml},
		{"application.properties", iconFileProps},
		{"logback-spring.xml", iconFileXML},
		{"说明.docx", iconFileDocx},
		{"说明.doc", iconFileDocx},
		{"汇报.pptx", iconFilePptx},
		{"汇报.ppt", iconFilePptx},
		{"台账.xlsx", iconFileXlsx},
		{"台账.xls", iconFileXlsx},
		// .sign 给的是**补丁**图标，不是通用文件图标
		{"20261012.sign", iconPatch},

		// 没有专属图标 -> 空串，调用方再落到目录/文件兜底
		{"README.txt", ""},
		{"classpath.idx", ""},
		{"MANIFEST.MF", ""},
		{"noext", ""},
		{"", ""},

		// **大小写敏感**：Java 的 String.endsWith 就是这样。
		// 别"顺手"改成 HasSuffixFold —— 那会与原版分叉。
		{"APP.JAR", ""},
		{"Foo.CLASS", ""},
		{"APP.YML", ""},
		{"README.TXT", ""},

		// 只认后缀，不认"名字里含不含"
		{"jar", ""},
		{"a.jarz", ""},
		{"x.class.bak", ""},
		{"yml", ""},

		// 多后缀：比的是整个名字的后缀，不是"最后一个点之后"
		{"a.b.js", iconFileJS},
		{".gitignore", ""},
	}
	for _, c := range cases {
		if got := iconByExt(c.name); got != c.want {
			t.Errorf("iconByExt(%q) = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// iconTestDir / iconTestFile 造节点的小工具。
func iconTestDir(name, path string, isPatch bool, typ constant.TreeNodeType, kids ...*tree.Node) *tree.Node {
	n := tree.NewDir(name, path, isPatch)
	n.Type = typ
	for _, k := range kids {
		n.AddChild(k)
	}
	return n
}

func iconTestFile(name, path string, isPatch bool, typ constant.TreeNodeType) *tree.Node {
	n := tree.NewFile(name, path, 1, time.Now(), nil, isPatch)
	n.Type = typ
	return n
}

// bootInf 造一个 Path 正好是 BOOT-INF/ 的目录 —— 目录条目的 Path 保留尾部斜杠
// （见 pack.buildFromZipReader 的保真注释），判定就靠这个。
func bootInf() *tree.Node {
	return iconTestDir("BOOT-INF", constant.BootInfPath, false, constant.TreeNodeTypeNone)
}

// TestIconName 钉住 getIconImage 的判定顺序。
func TestIconName(t *testing.T) {
	cases := []struct {
		desc string
		node *tree.Node
		want string
	}{
		{
			"应用根 + 有 BOOT-INF/ 直接子级 -> Spring Boot 图标",
			iconTestDir("demo-1.0.jar", "demo-1.0.jar", false, constant.TreeNodeTypeRoot,
				iconTestDir("META-INF", "META-INF/", false, constant.TreeNodeTypeNone), bootInf()),
			iconSpringBoot,
		},
		{
			"应用根 + 没有 BOOT-INF/ -> 按后缀给 jar 图标",
			iconTestDir("plain-1.0.jar", "plain-1.0.jar", false, constant.TreeNodeTypeRoot,
				iconTestDir("META-INF", "META-INF/", false, constant.TreeNodeTypeNone)),
			iconFileJar,
		},
		{
			"补丁根即使有 BOOT-INF/ 也不给 Spring Boot 图标（原版的 !isPatch）",
			iconTestDir("patch.zip", "patch.zip", true, constant.TreeNodeTypeRoot, bootInf()),
			iconFileZip,
		},
		{
			"BOOT-INF/ 必须是**直接**子级，孙子级不算",
			iconTestDir("demo-1.0.jar", "demo-1.0.jar", false, constant.TreeNodeTypeRoot,
				iconTestDir("outer", "outer/", false, constant.TreeNodeTypeNone, bootInf())),
			iconFileJar,
		},
		{
			"自定义根 -> file-mark，优先于后缀",
			iconTestFile("inner.jar", "inner.jar", true, constant.TreeNodeTypeCustomRoot),
			iconFileMark,
		},
		{
			"README 节点 -> readme.png，优先于后缀",
			iconTestFile("README.txt", "README.txt", true, constant.TreeNodeTypeReadme),
			iconReadme,
		},
		{
			"META-INF/patch-up/ -> patch-up.png，优先于目录图标",
			iconTestDir("patch-up", constant.PatchUpPath, false, constant.TreeNodeTypeNone),
			iconPatchUp,
		},
		{
			"普通目录 -> directory.png",
			iconTestDir("BOOT-INF", constant.BootInfPath, false, constant.TreeNodeTypeNone),
			iconDirectory,
		},
		{
			"有专属后缀的文件",
			iconTestFile("Foo.class", "com/example/Foo.class", true, constant.TreeNodeTypeMod),
			iconFileClass,
		},
		{
			".sign 文件 -> 补丁图标",
			iconTestFile("20261012.sign", "META-INF/patch-up/20261012.sign", true, constant.TreeNodeTypeNone),
			iconPatch,
		},
		{
			"没有专属后缀的普通文件 -> file.png",
			iconTestFile("classpath.idx", "classpath.idx", false, constant.TreeNodeTypeNone),
			iconFile,
		},
		{
			"后缀大小写敏感：Foo.JAR 落到兜底 file.png",
			iconTestFile("Foo.JAR", "Foo.JAR", false, constant.TreeNodeTypeNone),
			iconFile,
		},
		{
			"没有后缀的普通文件 -> file.png",
			iconTestFile("noext", "noext", false, constant.TreeNodeTypeNone),
			iconFile,
		},
	}
	for _, c := range cases {
		if got := iconName(c.node); got != c.want {
			t.Errorf("%s：iconName = %q，期望 %q", c.desc, got, c.want)
		}
	}

	if got := iconName(nil); got != "" {
		t.Errorf("iconName(nil) = %q，期望空串", got)
	}
}

// iconConsts 是全部图标常量 + 各自的用途说明。
//
// 提成包级变量是为了让 TestIconFilesExist 与 TestIconsEmbedded 共用一份 ——
// 这两条查的是同一张表的不同侧面（磁盘上有没有 / 二进制里有没有），
// 各存一份迟早会走样。
var iconConsts = map[string]string{
	iconDirectory:  "目录兜底",
	iconFile:       "文件兜底",
	iconFileJar:    ".jar",
	iconFileZip:    ".zip",
	iconFileClass:  ".class",
	iconFileJS:     ".js",
	iconFileHTML:   ".html",
	iconFileYml:    ".yml",
	iconFileYaml:   ".yaml",
	iconFileProps:  ".properties",
	iconFileXML:    ".xml",
	iconFileDocx:   ".doc / .docx",
	iconFilePptx:   ".ppt / .pptx",
	iconFileXlsx:   ".xls / .xlsx",
	iconPatch:      ".sign",
	iconFileMark:   "自定义根",
	iconReadme:     "README 节点",
	iconPatchUp:    "META-INF/patch-up/",
	iconSpringBoot: "Spring Boot 胖包根",
	iconSearch:     "工具栏搜索按钮（index.html 引用）",
	iconTime:       "搜索历史那一行的时钟",
}

// TestIconFilesExist 校验所有图标路径都真的有文件，且目录里没有多余文件。
//
// 这条比"名字对不对"更值钱：图标是 21 张 PNG，写错一个字母
// （`file-yaml` 写成 `file-yml`）既不会编译报错、也不会让别的测试变红，
// 界面上只会安静地缺一张图 —— 只有这条能抓住。
func TestIconFilesExist(t *testing.T) {
	for p, why := range iconConsts {
		if _, err := os.Stat(filepath.Join("frontend", "dist", p)); err != nil {
			t.Errorf("图标 %s（%s 用的）不存在：%v", p, why, err)
		}
	}

	// 反向：目录里不该有本包用不到的图 —— 多出来的多半是拷错了，
	// 而且每多一张就多一份进二进制的体积。
	entries, err := os.ReadDir(filepath.Join("frontend", "dist", "icons"))
	if err != nil {
		t.Fatalf("读 icons 目录失败: %v", err)
	}
	for _, e := range entries {
		if _, ok := iconConsts[iconDir+e.Name()]; !ok {
			t.Errorf("icons/%s 没有任何图标常量引用它（多余的文件）", e.Name())
		}
	}
	if len(entries) != len(iconConsts) {
		t.Errorf("icons 目录里 %d 个文件，常量表里 %d 个", len(entries), len(iconConsts))
	}
}

// pngMagic PNG 文件头，用来确认读到的是图而不是别的什么。
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// TestIconsEmbedded 证明图标**真的编进了二进制**。
//
// 上面那条 TestIconFilesExist 查的是工作目录（os.Stat）—— 就算 embed 指令
// 写错（漏掉 `all:` 前缀、dist 被挪到 embed 根之外），它照样全绿，
// 而用户打开程序会看到一列破图。这条读的是 embed.FS，也就是 wails build
// 时真正写进 exe 的那一份，路径与 gui/main.go 的 `//go:embed all:frontend/dist`
// 对齐。
func TestIconsEmbedded(t *testing.T) {
	// embed 里的路径是仓库相对路径（带 frontend/dist 前缀），
	// 而前端引用的是 `icons/xxx.png` —— asset server 会把前缀剥掉。
	// 前缀写错这里就会红，这正是这条测试存在的意义。
	const embedRoot = "frontend/dist/"

	for p, why := range iconConsts {
		b, err := assets.ReadFile(embedRoot + p)
		if err != nil {
			t.Errorf("图标 %s（%s 用的）没进二进制：%v", p, why, err)
			continue
		}
		if len(b) < len(pngMagic) {
			t.Errorf("图标 %s（%s 用的）在二进制里只有 %d 字节，不像 PNG", p, why, len(b))
			continue
		}
		if !bytes.HasPrefix(b, pngMagic) {
			t.Errorf("图标 %s（%s 用的）不是 PNG（文件头 % x）", p, why, b[:len(pngMagic)])
		}
	}

	// 反向：二进制里的 icons 目录内容应与常量表一一对应。
	entries, err := fs.ReadDir(assets, embedRoot+"icons")
	if err != nil {
		t.Fatalf("二进制里读不到 icons 目录（embed 路径 %q 不对？）: %v", embedRoot+"icons", err)
	}
	if len(entries) != len(iconConsts) {
		t.Errorf("二进制里 icons 有 %d 个文件，常量表里 %d 个", len(entries), len(iconConsts))
	}
}

// TestIconMatchesOriginalPNG 确认拷过来的图与原版逐字节一致。
//
// 为什么值得一条测试：图标是"原样搬运"的资产，一旦有人为了"优化体积"
// 重新压一遍 PNG，观感就会与原版有出入，而这一点在代码 review 里看不出来。
// 原版资源不在本仓库时（例如只拿到 patch-assistant-go）跳过。
func TestIconMatchesOriginalPNG(t *testing.T) {
	orig := filepath.Join("..", "..", "src-java", "src", "main", "resources", "icons")
	if _, err := os.Stat(orig); err != nil {
		t.Skipf("找不到原版图标目录，跳过：%v", err)
	}

	entries, err := os.ReadDir(filepath.Join("frontend", "dist", "icons"))
	if err != nil {
		t.Fatalf("读 icons 目录失败: %v", err)
	}
	for _, e := range entries {
		mine, err := os.ReadFile(filepath.Join("frontend", "dist", "icons", e.Name()))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", e.Name(), err)
		}
		theirs, err := os.ReadFile(filepath.Join(orig, e.Name()))
		if err != nil {
			t.Errorf("原版没有 icons/%s：%v", e.Name(), err)
			continue
		}
		if string(mine) != string(theirs) {
			t.Errorf("icons/%s 与原版不一致（%d 字节 vs %d 字节）—— 别重新压图",
				e.Name(), len(mine), len(theirs))
		}
	}
}
