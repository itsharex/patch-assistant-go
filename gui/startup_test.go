package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseStartupArgs 覆盖命令行 → 启动目标的翻译规则。
//
// 规则来自 Java 的 AppContext.initArgs + StartUpInfo.init()，
// 外加 Go 版补的"裸路径"兼容（见 parseStartupArgs 的注释）。
func TestParseStartupArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantApp   string
		wantPatch string
	}{
		{"空参数", nil, "", ""},
		{"target 指向 jar 归应用包", []string{"--target=C:\\a\\app.jar"}, "C:\\a\\app.jar", ""},
		{"target 指向目录归补丁包", []string{"--target=D:\\patch"}, "", "D:\\patch"},
		{"patch 指向 zip", []string{"--patch=D:\\p.zip"}, "", "D:\\p.zip"},
		{"target 与 patch 各就各位", []string{"--target=a.jar", "--patch=p.zip"}, "a.jar", "p.zip"},
		{"patch 优先于 target 的补丁分支", []string{"--target=D:\\dir", "--patch=p.zip"}, "", "p.zip"},
		{"裸 jar 路径归应用包", []string{"C:\\a\\app.jar"}, "C:\\a\\app.jar", ""},
		{"裸目录路径归补丁包", []string{"D:\\patch"}, "", "D:\\patch"},
		{"显式 target 优先于裸路径（裸路径接不回去且不存在，整段丢弃）", []string{"--target=x.jar", "y.jar"}, "", ""},
		{"裸 jar 加裸目录：接不回去且都不存在，整段丢弃", []string{"a.jar", "D:\\patch"}, "", ""},
		{"区分大小写：.JAR 不算应用包", []string{"--target=APP.JAR"}, "", "APP.JAR"},
		{"带引号的 --target=（Windows 双击的真实形态）", []string{`--target="D:\some dir\a.jar"`}, `D:\some dir\a.jar`, ""},
		{"带引号的裸路径", []string{`"D:\some dir\a.jar"`}, `D:\some dir\a.jar`, ""},
		{"带引号的 --patch=", []string{`--patch="D:\p dir"`}, "", `D:\p dir`},
		{"不成对的引号不动它", []string{`--target="half`}, "", `"half`},
		{"--dev 有意忽略", []string{"--dev"}, "", ""},
		{"认不出的参数忽略", []string{"--nope=1", "-x"}, "", ""},
		{"无值的 --target= 忽略", []string{"--target="}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseStartupArgs(c.args)
			if got.appPack != c.wantApp || got.patchPack != c.wantPatch {
				t.Fatalf("parseStartupArgs(%q) = {app:%q patch:%q}，期望 {app:%q patch:%q}",
					c.args, got.appPack, got.patchPack, c.wantApp, c.wantPatch)
			}
		})
	}
}

// TestIsJarPathIsCaseSensitive 钉住"区分大小写"这条口径。
//
// 原版用的是 String.endsWith(".jar")，`APP.JAR` 不算应用包；
// 拖放侧的 acceptAsAppPack 也是同一口径。改成 EqualFold 会与两者都不一致。
func TestIsJarPathIsCaseSensitive(t *testing.T) {
	for _, p := range []string{"a.jar", "C:\\x\\a.jar"} {
		if !isJarPath(p) {
			t.Errorf("isJarPath(%q) = false，期望 true", p)
		}
	}
	for _, p := range []string{"a.JAR", "a.Jar", "a.zip", "a", ""} {
		if isJarPath(p) {
			t.Errorf("isJarPath(%q) = true，期望 false", p)
		}
	}
}

// TestUnquote 钉住"剥 Windows 双击带进来的成对引号"。
//
// 这条是真修过的 bug：原版 installer 注册了 `"--target=%1"`，但
// `AppContext.initArgs` 的正则会把引号一起捕获，导致 `endsWith(".jar")`
// 与 `new File(path).exists()` 双双不成立 —— 原版装了 installer 也打不开 jar。
func TestUnquote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`"D:\a\b.jar"`, `D:\a\b.jar`},
		{`""`, ``},
		// shell 又补了一层引号：命令行带引号、%1 也被引号包裹
		{`""D:\a\b.jar""`, `D:\a\b.jar`},
		{`"half`, `"half`},
		{`half"`, `half"`},
		{`"`, `"`},
		{`D:\a\b.jar`, `D:\a\b.jar`},
		{"", ""},
	}
	for _, c := range cases {
		if got := unquote(c.in); got != c.want {
			t.Errorf("unquote(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestMergeSplitTokens 钉住"把被空格拆散的路径接回去，且只在真存在时才接"。
//
// 真实触发场景：Windows 经文件关联启动时，`%1` 展开**不一定带引号**（本机实测
// ShellExecute 走关联启动就是不带引号），路径含空格时 argv 会被切成多段。
// 本机项目路径 `D:\Download\新建文件夹 (2)\PatchAssistant2J1.3.0\...`
// 正好命中 —— 而且 `D:\Download\新建文件夹` 本身是个**存在的目录**，
// 不接回去的话 `--target=` 会拿到它、滑进补丁包槽，打开一个错误的文件夹，
// 用户要的 jar 只剩碎片没人认。
//
// 反向同样重要：拖多个文件进来时，接回去是一条**不存在**的路径，
// 必须原样返回，否则多个文件会被误并成一个。
func TestMergeSplitTokens(t *testing.T) {
	dir := t.TempDir()
	whole := filepath.Join(dir, "a b.jar")
	if err := os.WriteFile(whole, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, "a")

	cases := []struct {
		name string
		in   []argToken
		want []argToken
	}{
		{"空", nil, nil},
		{"没什么可接", []argToken{{key: "target", value: "a.jar"}}, []argToken{{key: "target", value: "a.jar"}}},
		{
			"裸路径被拆散且接回去存在",
			[]argToken{{value: head}, {value: "b.jar"}},
			[]argToken{{value: whole}},
		},
		{
			"--target= 的值被拆散且接回去存在",
			[]argToken{{key: "target", value: head}, {value: "b.jar"}},
			[]argToken{{key: "target", value: whole}},
		},
		{
			"接回去不存在就别硬接",
			[]argToken{{key: "target", value: "a.jar"}, {key: "patch", value: "b.zip"}},
			// patch 不是裸路径，本来就接不过来
			[]argToken{{key: "target", value: "a.jar"}, {key: "patch", value: "b.zip"}},
		},
		{
			"两个裸路径接不回去、且都不是存在的路径：整段丢弃",
			[]argToken{{value: "a.jar"}, {value: "b.zip"}},
			nil,
		},
		{
			"首段存在、后续片段不存在：整段丢弃（不把目录当补丁包打开）",
			[]argToken{{key: "target", value: head + `(x)`}, {value: `frag\a.jar`}},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeSplitTokens(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("mergeSplitTokens(%+v) = %+v，期望 %+v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("mergeSplitTokens(%+v) = %+v，期望 %+v", c.in, got, c.want)
				}
			}
		})
	}
}

// TestMergeSplitTokensFindsLongestJoin 钉住"最长的存在接法"。
//
// 为什么不能逐段累加：目录名自己也可能被拆散 ——
// `D:\a` + `b\c` + `d\e.jar` 合成 `D:\a b\c d\e.jar`，
// 而中间的 `D:\a b\c` 并不存在，逐段累加会在第一步就放弃。
func TestMergeSplitTokensFindsLongestJoin(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a b", "c d")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	whole := filepath.Join(sub, "e.jar")
	if err := os.WriteFile(whole, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 注意 `D:\a b\c` 这一整段并不存在，只有整条路径存在。
	in := []argToken{{key: "target", value: filepath.Join(dir, "a")}, {value: `b\c`}, {value: `d\e.jar`}}

	got := mergeSplitTokens(in)
	if len(got) != 1 || got[0].value != whole {
		t.Fatalf("mergeSplitTokens(%+v) = %+v，期望单个 token 值 %q", in, got, whole)
	}
}

// TestMergeSplitTokensKeepsExistingRun 钉住"每段自己都存在时不丢"。
//
// 例外条款的理由：argv 里一段连续的裸路径，绝大多数是 Windows 把一条带空格的
// 路径拆开了；但用户真的手敲多个文件时，每一段都是存在的路径，这时不能丢。
func TestMergeSplitTokensKeepsExistingRun(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "one.jar")
	b := filepath.Join(dir, "two.zip")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	in := []argToken{{key: "target", value: a}, {value: b}}
	got := mergeSplitTokens(in)
	if len(got) != 2 || got[0].value != a || got[1].value != b {
		t.Fatalf("mergeSplitTokens(%+v) = %+v，期望两段都保留", in, got)
	}

	// 完整链路：显式 target 给应用包，裸非 jar 路径按 StartUpInfo 归补丁包。
	st := parseStartupArgs([]string{"--target=" + a, b})
	if st.appPack != a {
		t.Fatalf("appPack = %q，期望 %q", st.appPack, a)
	}
	if st.patchPack != b {
		t.Fatalf("patchPack = %q，期望 %q", st.patchPack, b)
	}
}

// TestParseStartupArgsMergesSplitPath 走完整条链路：
// 被空格拆散的 jar 路径要能还原并落到应用包槽位上。
func TestParseStartupArgsMergesSplitPath(t *testing.T) {
	dir := t.TempDir()
	whole := filepath.Join(dir, "a b.jar")
	if err := os.WriteFile(whole, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		// `--target=` 的值被拆散（注册表 `"--target=%1"` 且 %1 不带引号展开）
		{"--target=" + filepath.Join(dir, "a"), "b.jar"},
		// 裸路径被拆散（手动关联的裸 `%1`）
		{filepath.Join(dir, "a"), "b.jar"},
	} {
		got := parseStartupArgs(args)
		if got.appPack != whole {
			t.Fatalf("parseStartupArgs(%q) appPack = %q，期望 %q", args, got.appPack, whole)
		}
		if got.patchPack != "" {
			t.Fatalf("parseStartupArgs(%q) patchPack = %q，期望空（不能滑到补丁包槽）", args, got.patchPack)
		}
	}
}

// TestOpenStartupTargetsOpensArgPacks 验证"带文件启动"真的把包打开了。
func TestOpenStartupTargetsOpensArgPacks(t *testing.T) {
	app := newAppZh()
	app.startArgs = startupTargets{
		appPack:   fixture(t, "app.jar"),
		patchPack: fixture(t, "patch.zip"),
	}
	app.openStartupTargets()

	if app.appTreeInfo == nil {
		t.Fatal("启动参数指定的应用包没有被打开")
	}
	if app.patchTreeInfo == nil {
		t.Fatal("启动参数指定的补丁包没有被打开")
	}
	if app.appPath != fixture(t, "app.jar") {
		t.Fatalf("appPath = %q，期望夹具路径", app.appPath)
	}
	if app.patchPath != fixture(t, "patch.zip") {
		t.Fatalf("patchPath = %q，期望夹具路径", app.patchPath)
	}
}

// TestOpenStartupTargetsSkipsMissingPath 钉住"路径不存在就静默跳过"。
//
// 对齐原版 FileUtil.getExistsFile 返回 null 的行为：那个槽位留空，
// 不报错、也不打开别的东西。
//
// 光断言"没打开"是不够的 —— 就算去掉存在性校验，OpenAppPack 自己也会因为
// 文件读不到而失败，树照样是空的，弱断言看不出差别（变异验证实测如此）。
// 真正的判别点是**有没有去尝试打开**：原版压根不尝试，所以不该留下任何进度。
func TestOpenStartupTargetsSkipsMissingPath(t *testing.T) {
	dir := t.TempDir()
	app := newAppZh()
	app.startArgs = startupTargets{
		appPack:   filepath.Join(dir, "nope.jar"),
		patchPack: filepath.Join(dir, "nope.zip"),
	}
	app.openStartupTargets()

	if app.appTreeInfo != nil || app.patchTreeInfo != nil {
		t.Fatal("路径不存在时不该打开任何包")
	}
	if app.appPath != "" || app.patchPath != "" {
		t.Fatalf("appPath/patchPath 都该为空，实际 %q / %q", app.appPath, app.patchPath)
	}
	if len(app.progress) != 0 {
		t.Fatalf("路径不存在时不该去尝试打开（原版 getExistsFile 返回 null 就跳过），"+
			"却留下了进度记录: %v", app.progress)
	}
}

// TestOpenStartupTargetsAcceptsDirectoryPatch 验证补丁包可以是目录。
//
// 对应原版 FileUtil.getExistsDirOrFile —— 补丁包既可以是 zip/jar，也可以是目录。
func TestOpenStartupTargetsAcceptsDirectoryPatch(t *testing.T) {
	dir := t.TempDir()
	app := newAppZh()
	app.startArgs = startupTargets{patchPack: dir}
	app.openStartupTargets()

	if app.patchTreeInfo == nil {
		t.Fatal("目录形态的补丁包没有被打开")
	}
}

// TestNewAppIgnoresTestFlags 钉住"跑测试时不会误把 -test.* 当成启动参数"。
//
// NewApp 里读的是 os.Args[1:]，而 go test 会塞进一堆 `-test.xxx` 开关；
// 它们以 `-` 开头，必须被 parseStartupArgs 全部忽略。
func TestNewAppIgnoresTestFlags(t *testing.T) {
	app := NewApp()
	if app.startArgs.appPack != "" || app.startArgs.patchPack != "" {
		t.Fatalf("测试进程的参数被误当成启动目标: %+v", app.startArgs)
	}
}

// TestGetStateTriggersStartupLoad 钉住"前端 init 第一次 GetState 会触发启动加载"。
//
// 这是修竞态的关键不变量：OnStartup 与前端 JS 加载并发起步，前端 init() 调
// GetState 的时机可能早于 OnStartup 把包打开完。把加载收口进 GetState（同步
// 阻塞直到打开完）后，无论谁先到，前端那次 GetState 拿到的都是已加载的树，
// 不会渲染成"未打开"。
//
// 注意：直接调 GetState，不手动调 openStartupTargets —— 要证明是 GetState 自己
// 触发了加载，而不是依赖 startup() 先跑。
func TestGetStateTriggersStartupLoad(t *testing.T) {
	app := newAppZh()
	app.startArgs = startupTargets{appPack: fixture(t, "app.jar")}

	s := app.GetState()
	if s.AppTree == nil {
		t.Fatal("GetState 没有触发启动参数加载，返回的 AppTree 为空")
	}
	if app.appTreeInfo == nil {
		t.Fatal("GetState 触发加载后 appTreeInfo 仍为空")
	}
	if app.appPath != fixture(t, "app.jar") {
		t.Fatalf("appPath = %q，期望夹具路径", app.appPath)
	}
}
