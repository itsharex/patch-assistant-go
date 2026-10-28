package main

import (
	"os"
	"strings"

	"cn.cpoet.patch.assistant/internal/constant"
)

// startupTargets 是命令行解析出来的"启动时要打开什么"。
//
// 对应 Java 的 AppContext.initArgs（把 argv 解析成 `--key=value` 表）
// 加上 StartUpInfo（把表翻译成"应用包 / 补丁包"两个槽位）。
//
// 原版就是靠这两个类支持"双击 .jar 直接打开"的 —— GluonFX 打的 installer
// 注册出来的关联命令是 `"PatchAssistant2J.exe" "--target=%1"`，
// 详见 register-jar-association.bat 与 MIGRATION.md。
type startupTargets struct {
	// appPack 要当作【应用包】打开的 .jar 路径；没有则为空串。
	appPack string
	// patchPack 要当作【补丁包】打开的路径（.zip / .jar / 目录）；没有则为空串。
	patchPack string
}

// parseStartupArgs 把命令行参数翻译成启动目标。
//
// 格式对齐 Java AppContext.initArgs：只认 `--key=value`（原版正则
// `^--([a-zA-Z0-9_-]+)=(.+)$`），认不出来的参数**静默忽略**（原版也是 continue）。
// 原版的第三个参数 `--dev`（ParamNameConst.DEV）**有意不解析** —— 它只被
// ExceptionUtil:58 用来决定错误文件落盘位置，而 Go 版没有 ExceptionUtil
// （错误一律直接返回给调用方），解析了也无处可用。
//
// 槽位归属照搬 StartUpInfo.init()：
//
//	initAppPackFile → target 以 ".jar" 结尾时，当作应用包
//	initPatchFile   → `--patch=` 优先；否则 target 不以 ".jar" 结尾时，当作补丁包
//
// 注意 target 是**一个槽位分派两种用途**：`.jar` 归应用包，非 `.jar` 归补丁包。
// 这不是笔误 —— 原版拿 `--target=` 表示"用户想打开的那个东西"，由扩展名决定
// 它落在哪一侧。
func parseStartupArgs(args []string) startupTargets {
	var (
		t      startupTargets
		target string
		patch  string
	)

	tokens := make([]argToken, 0, len(args))
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue := strings.Cut(arg[2:], "=")
			if !hasValue {
				continue
			}
			tokens = append(tokens, argToken{key: name, value: unquote(value)})
		case strings.HasPrefix(arg, "-"):
			// 其它开关，忽略。
		default:
			// 裸路径 —— 原版没有这个分支，是 Go 版补的兼容，理由见下方注释。
			// 注意也要 unquote：手动关联注册的命令同样是 `"%1"`。
			tokens = append(tokens, argToken{value: unquote(arg)})
		}
	}
	// Windows 把带空格的路径按空格拆进 argv 时，`--target=` 的值和裸路径
	// 都会被切成好几段，先在这里接回去（见 mergeSplitTokens）。
	tokens = mergeSplitTokens(tokens)

	var bareJar, bareOther string
	for _, tk := range tokens {
		if tk.key == "" {
			// 裸路径：第一个 .jar 给应用包，第一个非 .jar 给补丁包。
			if isJarPath(tk.value) {
				if bareJar == "" {
					bareJar = tk.value
				}
			} else if bareOther == "" {
				bareOther = tk.value
			}
			continue
		}
		switch tk.key {
		case constant.ParamTarget:
			target = tk.value
		case constant.ParamPatch:
			patch = tk.value
		}
	}

	// 裸路径并入槽位，显式的 `--target=` / `--patch=` 优先。
	//
	// 为什么要认裸路径：Windows 的「打开方式 → 始终使用」生成的是
	// `Applications\<exe 名>` 这种 ProgId，它注册出来的命令里只能写裸 `%1`，
	// **给不出 `--target=`**；只有 installer 才会写带前缀的形式。原版只认
	// `--key=value`，于是"手动关联 → 双击 jar"会启动程序却不加载文件
	// （本机实测：`HKCR\Applications\PatchAssistant2J.exe` 那条就是这个坑）。
	// 认下裸路径，"手动关联"这条路才真的能用。
	//
	// 一次拖多个文件时：第一个 .jar 给应用包、第一个非 .jar 给补丁包。
	if target == "" {
		if bareJar != "" {
			target = bareJar
		} else {
			target = bareOther
		}
	}
	if patch == "" {
		patch = bareOther
	}

	// 按 StartUpInfo.init() 分派。
	if isJarPath(target) {
		t.appPack = target
	}
	if patch != "" {
		t.patchPack = patch
	} else if target != "" && !isJarPath(target) {
		t.patchPack = target
	}
	return t
}

// argToken 是一个还没判定归属的参数片段。
//
// key 为空串表示"裸路径"（没有 `--` 前缀），非空则是 `--key=value` 里的 key。
// value 一律已经过 unquote。
type argToken struct {
	key   string
	value string
}

// mergeSplitTokens 把被 Windows 按空格拆散的参数还原成完整路径。
//
// 什么时候会被拆散：注册表命令里的 `%1` 被替换进来时**不一定带引号**
// （本机实测：ShellExecute 走关联启动时不加引号），于是带空格的路径会被
// 切成多个 argv。本机项目路径就是
// `D:\Download\新建文件夹 (2)\PatchAssistant2J1.3.0\...` —— 带空格也带括号。
// 拆散的位置可能落在 `--target=` 的值中间，也可能落在裸路径中间，两种都接。
//
// 不修的后果很阴：`D:\Download\新建文件夹` 本身是个**存在的目录**，
// 于是 `--target=` 拿到它 → 不是 .jar → 落到补丁包槽 → 程序打开了一个错误的
// 文件夹，而用户要的 jar 只剩一个 `(2)\...\x.jar` 碎片躺在 argv 里没人认。
// 症状正是"页面打开了，但里面没有我右键的 jar"。
//
// 判定口径：**从当前位置往后，找出最长的"接回去真实存在"的一段**。
// 全程裸路径片段（不能把别人的 `--patch=` 吞进来），一段里只要有任何一种接法
// 存在就按最长的采纳。接不回去就原样不动 —— 拖多个文件进来时，接回去是
// 不存在的路径，绝不能因此把多个文件误并成一个。
//
// 之所以要看"最长的存在接法"而不是"逐段累加直到不存在"：目录名自己也可能被
// 拆散（`D:\a` + `b\c` + `d\e.jar` 合成 `D:\a b\c d\e.jar`），中间的
// `D:\a b\c` 并不存在，逐段累加会在第一步就放弃。
//
// **接不回去的整段会被丢掉**（连同首片段），而不是原样留下。道理：argv 里出现
// "一段连续的裸路径"只有两种来历 —— Windows 把一条带空格的路径拆开了（占绝大多
// 数，因为文件关联只会传一个 `%1`），或者用户手敲了多个路径（罕见）。接不回去说
// 明这条路径在磁盘上不存在，此时把片段当路径用只会打开错东西：本机实测
// `D:\Download\新建文件夹` 就是个**存在的目录**，原样留下会把它当补丁包打开，
// 而用户要的 jar 一个碎片也没进来 —— 正是"页面开了但里面没有我的文件"。
// 宁可不打开，也不打开一个用户没要的东西。
//
// 唯一的例外：整段里**每一段自己都是存在的路径**。那就不再是"拆散的半条路径"，
// 而是用户确实传了多个文件，逐个当路径用。
func mergeSplitTokens(tokens []argToken) []argToken {
	out := make([]argToken, 0, len(tokens))
	for i := 0; i < len(tokens); {
		cur := tokens[i]
		i++

		// 只有承载路径的 token 才可能被拆散；别的 `--key=value` 原样保留，
		// 也绝不让后面的裸路径片段并进它的值里。
		if cur.key != "" && cur.key != constant.ParamTarget && cur.key != constant.ParamPatch {
			out = append(out, cur)
			continue
		}

		// 紧跟其后的一串裸路径片段（它们只可能是被拆散的同一段路径）。
		runEnd := i
		for j := i; j < len(tokens) && tokens[j].key == ""; j++ {
			runEnd = j + 1
		}
		runLen := runEnd - i

		// 在这一串里找最长的"接回去真实存在"的前缀。
		best := 0
		for j := i; j < runEnd; j++ {
			if isExistingPath(joinValues(cur, tokens[i:j+1])) {
				best = j - i + 1
			}
		}

		if best > 0 {
			// 接回最好的那一种；还剩的片段留给后面单独当路径看
			// （真的有人在 argv 里传多个文件时，它们接不回去、各自成立）。
			cur.value = joinValues(cur, tokens[i:i+best])
			i += best
			out = append(out, cur)
			continue
		}
		if runLen > 0 && !runAllExist(cur, tokens[i:runEnd]) {
			// 一个片段都没接回去，且有片段压根不存在：整段是拼不回来的路径，
			// 连首片段一起丢掉（见上方注释）。
			i = runEnd
			continue
		}
		out = append(out, cur)
	}
	return out
}

// runAllExist 判断首片段与后续片段是否**每一段自己都是存在的路径**。
func runAllExist(cur argToken, rest []argToken) bool {
	if cur.value != "" && !isExistingPath(cur.value) {
		return false
	}
	for _, tk := range rest {
		if !isExistingPath(tk.value) {
			return false
		}
	}
	return true
}

// joinValues 把首片段和后续片段用空格接回一条路径（空片段跳过）。
func joinValues(first argToken, rest []argToken) string {
	parts := make([]string, 0, len(rest)+1)
	if first.value != "" {
		parts = append(parts, first.value)
	}
	for _, tk := range rest {
		if tk.value != "" {
			parts = append(parts, tk.value)
		}
	}
	return strings.Join(parts, " ")
}

// unquote 剥掉包裹引号（可能不止一层）。
//
// Windows 通过文件关联启动时，注册表命令里的 `%1` 会被**带着引号**替换进来，
// 于是 argv 收到的是 `--target="D:\some dir\a.jar"` —— 首尾各一个双引号。
// 不剥的话 `HasSuffix(p, ".jar")` 判成 false（结尾是 `"`），整条"双击打开"
// 链路就断了。
//
// **原版在这里是坏的**：`AppContext.initArgs` 的正则 `^--([a-zA-Z0-9_-]+)=(.+)$`
// 会把引号一起捕获，`endsWith(".jar")` 与 `FileUtil.getExistsFile`（就是
// `new File(path).exists()`）都不过 —— 所以原版装了 installer 也打不开。
// 但 installer 特意注册 `"--target=%1"` 本身就说明作者**意图**是能打开的，
// 这里按意图实现，不是刻意偏差。
//
// 剥到不再成对为止：命令行本身带了引号、shell 又给 `%1` 补了一层时，会出现
// `--target=""D:\a.jar""` 这种双重包裹。Windows 文件名不允许含 `"`，
// 所以"剥到底"不会误伤合法路径。
func unquote(s string) string {
	for len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// isJarPath 判断路径是否"以 .jar 结尾"。
//
// **区分大小写**：原版 StartUpInfo.initAppPackFile 用的是
// `targetPath.endsWith(FileExtConst.DOT_JAR)`，`APP.JAR` 不算应用包。
// 这与拖放侧的 acceptAsAppPack 口径一致（那里也是 strings.HasSuffix），
// 别改成 EqualFold。
func isJarPath(p string) bool {
	return strings.HasSuffix(p, constant.DotJar)
}

// isExistingFile 判断路径存在且是文件。
//
// 对应 Java FileUtil.getExistsFile：不存在就返回 null，调用方静默降级。
func isExistingFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// isExistingPath 判断路径存在（文件或目录都算）。
//
// 对应 Java FileUtil.getExistsDirOrFile —— 补丁包既可以是 zip/jar，
// 也可以是一个目录。
func isExistingPath(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// openStartupTargets 按启动参数把对应的包打开。
//
// 在 startup（Wails 的 OnStartup）里调用，时机与前端的 GetState 之间没有竞态：
// OnStartup 先于前端脚本执行，所以前端 init 拿到的就已经是加载完的树。
//
// **路径不存在就静默跳过** —— 原版 FileUtil.getExistsFile / getExistsDirOrFile
// 返回 null 时那个槽位留空、退回"上次打开的包"；Go 版没有这项持久化，
// 跳过就是不打开（行为等价：用户不会看到"莫名其妙打开了别的东西"）。
//
// 打开失败（文件损坏等）记一条进度。原版这种情况抛 AppException、
// 由 ExceptionUtil 写进错误文件；GUI 里给条可见的反馈更合理。
func (a *App) openStartupTargets() {
	note := a.step()
	if p := a.startArgs.appPack; p != "" && isExistingFile(p) {
		if _, err := a.OpenAppPack(p); err != nil {
			note("启动参数指定的应用包打开失败: " + err.Error())
		}
	}
	if p := a.startArgs.patchPack; p != "" && isExistingPath(p) {
		if _, err := a.OpenPatchPack(p); err != nil {
			note("启动参数指定的补丁包打开失败: " + err.Error())
		}
	}
}

// ensureStartup 保证"命令行指定的启动包"被打开过一次，且**所有调用方都阻塞到
// 加载完成再返回**。
//
// 收口到这里（而不是只在 OnStartup 里做）是为了消除与前端 init() 的竞态：
// Wails v2 的 OnStartup 和前端 JS 加载并发起步，前端 init() 调 GetState 可能早于
// 把包打开完——那时 GetState 拿到空状态、前端 apply 成"未打开"且不再重拉。
//
// 用 sync.Once 而不是"bool 早置位"：Once.Do 保证只做一次，且**并发/竞速的调用方
// 都会等到这次加载真正完成后才返回**，不会出现"加载还没完、GetState 已被放行
// 返回空树"的竞态。OnStartup 与 GetState 都走这里，谁先到谁加载、另一个等到做完。
func (a *App) ensureStartup() {
	a.startupOnce.Do(func() {
		a.openStartupTargets()
	})
}
