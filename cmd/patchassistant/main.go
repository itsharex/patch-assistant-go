// Command patchassistant 是 PatchAssistant2J 的 Go 版命令行入口。
//
// 当前阶段只落地了「不依赖 GUI」的地基：领域模型、工具层、配置、以及
// jar/zip 建树。GUI 与补丁匹配流程尚未移植，见 README 的进度表。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cn.cpoet.patch.assistant/internal/config"
	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/pack"
	"cn.cpoet.patch.assistant/internal/service"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

const usage = `PatchAssistant2J (Go) — 补丁助手命令行

用法:
  patchassistant list   <包路径>          以树形打印 jar/zip 内容
  patchassistant info   <包路径>          打印包内统计信息
  patchassistant readme <说明文件路径>    解析 README 升级指令并打印

  patchassistant patch  [选项] <应用包> <补丁包>
      按补丁包的 README 与开关做匹配，输出打过补丁的应用包。
      选项（必须写在位置参数【之前】）:
        -o <路径>        输出路径，默认为应用包自身（同目录同名，会覆盖原包）
        -path=<bool>     启用路径匹配，默认 true
        -name=<bool>     启用文件名匹配，默认 true
        -regex=<正则>    参与匹配的补丁文件名正则，默认取内置默认值
        -sign=<bool>     是否写入 META-INF/patch-up/*.sign，默认 true
        -user=<名字>     打补丁的人，写进签名，默认取系统用户名
        -readme=<名字>   补丁说明文件名，默认 README.txt
        -v               打印每一步进度

  patchassistant patchreadme <补丁包> <文本文件>
      把文本文件的内容写回补丁包的说明文件（没有则新建）。
      选项:
        -readme=<名字>   补丁说明文件名，默认 README.txt
        -v               打印每一步进度

说明:
  补丁匹配与保存已可用；GUI 与反编译尚未移植，见 README 的进度表。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	var err error
	switch cmd {
	case "list":
		err = cmdList(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	case "readme":
		err = cmdReadme(os.Args[2:])
	case "patch":
		err = cmdPatch(os.Args[2:])
	case "patchreadme":
		err = cmdPatchReadme(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

// newStep 构造进度回调：-v 时逐行打印，否则静默。
func newStep(verbose bool) service.Step {
	if !verbose {
		return nil
	}
	return func(msg string) { fmt.Println("  · " + msg) }
}

// openPack 打开包并建树，返回根节点与关闭函数。
func openPack(path string) (*tree.Node, func(), error) {
	if path == "" {
		return nil, nil, errors.New("缺少包路径参数")
	}
	ext := strings.ToLower(util.Ext(path))
	if ext != constant.ExtJar && ext != constant.ExtZip {
		return nil, nil, fmt.Errorf("仅支持 .jar / .zip，收到: %s", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}

	root, err := pack.BuildZipTree(filepath.Base(path), f, st.Size(), false)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	// 树是惰性的，底层句柄必须活到使用结束
	return root, func() { f.Close() }, nil
}

func cmdList(args []string) error {
	root, closeFn, err := openPack(first(args))
	if err != nil {
		return err
	}
	defer closeFn()

	printTree(root, "", true)
	return nil
}

func printTree(n *tree.Node, prefix string, isLast bool) {
	if n.Path != "" { // 根节点不打印自身
		marker := "├── "
		if isLast {
			marker = "└── "
		}
		suffix := ""
		if !n.Dir() {
			suffix = fmt.Sprintf("  (%s)", humanSize(n.Size))
		} else {
			suffix = "/"
		}
		fmt.Printf("%s%s%s%s\n", prefix, marker, n.Name, suffix)
	}

	childPrefix := prefix
	if n.Path != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, c := range n.Children {
		printTree(c, childPrefix, i == len(n.Children)-1)
	}
}

func cmdInfo(args []string) error {
	root, closeFn, err := openPack(first(args))
	if err != nil {
		return err
	}
	defer closeFn()

	files, dirs, classes, jars := 0, 0, 0, 0
	var totalSize int64
	var walk func(*tree.Node)
	walk = func(n *tree.Node) {
		for _, c := range n.Children {
			if c.Dir() {
				dirs++
			} else {
				files++
				totalSize += c.Size
				if strings.HasSuffix(c.Name, constant.DotClass) {
					classes++
				}
				if strings.HasSuffix(c.Name, constant.DotJar) {
					jars++
				}
			}
			walk(c)
		}
	}
	walk(root)

	fmt.Printf("包名    : %s\n", root.Name)
	fmt.Printf("文件数  : %d\n", files)
	fmt.Printf("目录数  : %d\n", dirs)
	fmt.Printf("class 数: %d\n", classes)
	fmt.Printf("内嵌 jar: %d\n", jars)
	fmt.Printf("解压大小: %s\n", humanSize(totalSize))
	return nil
}

func cmdReadme(args []string) error {
	path := first(args)
	if path == "" {
		return errors.New("缺少说明文件路径参数")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cs := util.DetectCharset(raw)
	text := util.DecodeString(raw, cs)

	fmt.Printf("字符集猜测: %s\n", cs)
	fmt.Println("--- 解析结果 ---")

	infos := service.ParseReadMe(text)
	for _, info := range infos {
		fmt.Printf("  [%-6s] path1=%-46s path2=%-40s path3=%-18s appNodePath=%s\n",
			service.ChangeTypeName(info.Type), info.Path1, info.Path2, info.Path3, info.AppNodePath())
	}
	fmt.Printf("共命中 %d 行\n", len(infos))
	return nil
}

// cmdPatch 按补丁包给应用包打补丁，输出新的应用包。
//
// 流程对齐 Java UI 里的 refreshPatchMappedNode + AppPackService.savePack：
// 先刷新 README（把补丁包里的说明读进 PatchSign），再刷新映射，最后写出。
// 有自定义根时逐个根跑一轮，否则只跑主根 —— 与 HomeRightTreeView 一致。
func cmdPatch(args []string) error {
	fs := flag.NewFlagSet("patch", flag.ContinueOnError)
	outPath := fs.String("o", "", "输出路径")
	withPath := fs.Bool("path", true, "启用路径匹配")
	withName := fs.Bool("name", true, "启用文件名匹配")
	regex := fs.String("regex", "", "参与匹配的补丁文件名正则")
	writeSign := fs.Bool("sign", true, "是否写入补丁签名")
	user := fs.String("user", "", "打补丁的人，写进签名")
	readme := fs.String("readme", "", "补丁说明文件名")
	verbose := fs.Bool("v", false, "打印每一步进度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("用法: patchassistant patch [选项] <应用包> <补丁包>")
	}
	if len(rest) > 2 {
		// Go 的 flag 遇到第一个非选项参数就停止解析，后面的 -x 会被当成
		// 位置参数【静默丢掉】。宁可报错也别让用户以为选项生效了。
		return fmt.Errorf("选项必须写在位置参数之前，多出来的参数: %v", rest[2:])
	}
	appPath, patchFilePath := rest[0], rest[1]

	conf := config.NewPatchConf()
	if util.IsBlank(*regex) {
		*regex = conf.PatchFileRegex
	}
	if util.IsBlank(*readme) {
		*readme = conf.ReadmeFile
	}
	if util.IsBlank(*user) {
		*user = conf.EffectiveUsername(util.GetUserName())
	}
	target := *outPath
	if util.IsBlank(target) {
		target = defaultPatchedPath(appPath)
	}

	st := newStep(*verbose)
	opts := service.PatchOptions{
		WithPath:       *withPath,
		WithName:       *withName,
		PatchFileRegex: *regex,
		PatchFileDiff:  conf.PatchFileDiff,
		LangCode:       config.New().Genera.Language,
		Step:           st,
	}

	appTreeInfo, err := service.GetAppTreeInfo(appPath)
	if err != nil {
		return fmt.Errorf("读取应用包失败: %w", err)
	}
	patchTreeInfo, err := service.GetPatchTreeNode(patchFilePath, st)
	if err != nil {
		return fmt.Errorf("读取补丁包失败: %w", err)
	}

	total := tree.NewTotalInfo()
	roots := patchTreeInfo.CustomRootInfoMap()
	if len(roots) == 0 {
		roots = []tree.CustomRootEntry{{Node: patchTreeInfo.RootNode(), Info: patchTreeInfo.RootInfo()}}
	}
	for _, entry := range roots {
		service.RefreshReadmeNode(patchTreeInfo, entry.Node, *readme, st)
		appTreeInfo.SetPatchDiffInfo("")
		service.RefreshMappedNode(total, appTreeInfo, patchTreeInfo, entry.Node, opts)
	}

	ctx := &service.WriteContext{
		AppTreeInfo:    appTreeInfo,
		PatchTreeInfo:  patchTreeInfo,
		Total:          total,
		WritePatchSign: *writeSign,
		Username:       *user,
		ReadmeFile:     *readme,
		Step:           st,
	}
	if err := service.SaveAppPack(ctx, target); err != nil {
		return fmt.Errorf("保存失败: %w", err)
	}

	fmt.Printf("输出包  : %s\n", target)
	fmt.Printf("新增 %d / 修改 %d / 删除 %d（手动删除 %d）\n",
		total.AddTotal(), total.ModTotal(), total.DelTotal(), total.ManualDelTotal())
	if diff := appTreeInfo.PatchDiffInfo(); !util.IsBlank(diff) {
		fmt.Println("--- 补丁比对提示 ---")
		fmt.Println(diff)
	}
	return nil
}

// cmdPatchReadme 把文本文件的内容写回补丁包的说明文件。
//
// 只处理补丁包【主根】的 README —— 自定义根的 README 需要在树里选中对应根，
// CLI 不做这层选择。逻辑对齐 Java ReadmeView.handleUpdateReadme。
func cmdPatchReadme(args []string) error {
	fs := flag.NewFlagSet("patchreadme", flag.ContinueOnError)
	readme := fs.String("readme", "", "补丁说明文件名")
	verbose := fs.Bool("v", false, "打印每一步进度")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return errors.New("用法: patchassistant patchreadme [选项] <补丁包> <文本文件>")
	}
	if len(rest) > 2 {
		return fmt.Errorf("选项必须写在位置参数之前，多出来的参数: %v", rest[2:])
	}
	patchFilePath, textPath := rest[0], rest[1]
	if util.IsBlank(*readme) {
		*readme = config.NewPatchConf().ReadmeFile
	}

	raw, err := os.ReadFile(textPath)
	if err != nil {
		return err
	}
	text := util.DecodeString(raw, util.DetectCharset(raw))

	st := newStep(*verbose)
	patchTreeInfo, err := service.GetPatchTreeNode(patchFilePath, st)
	if err != nil {
		return fmt.Errorf("读取补丁包失败: %w", err)
	}
	rootNode := patchTreeInfo.RootNode()
	service.RefreshReadmeNode(patchTreeInfo, rootNode, *readme, st)

	ctx := &service.WriteContext{
		PatchTreeInfo: patchTreeInfo,
		ReadmeFile:    *readme,
		Step:          st,
	}
	if err := service.UpdatePatchReadme(ctx, rootNode, text); err != nil {
		return err
	}
	fmt.Printf("已写回 %s 的 %s（%d 字节）\n", patchFilePath, *readme, len(text))
	return nil
}

// defaultPatchedPath 返回默认输出路径 —— **就是应用包自己的路径**。
//
// 与 GUI / Java 版口径一致：输出名不加任何后缀（原版 `setInitialFileName`
// 用的就是应用包根节点自己的名字）。这里曾自行拼过 "-patched"，已去掉。
//
// **注意：默认值与输入文件同名同目录，不带 -o 直接跑会原地覆盖应用包。**
// 这是与原版一致的行为，脚本里请显式传 -o。
func defaultPatchedPath(appPath string) string {
	return appPath
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	for _, u := range units {
		v /= unit
		if v < unit {
			return fmt.Sprintf("%.1f %s", v, u)
		}
	}
	return fmt.Sprintf("%.1f PB", v/unit)
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
