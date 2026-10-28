// Package pack 对应 Java 侧 cn.cpoet.patch.assistant.service.BasePackService
// 及其 compress 子包，负责把 jar/zip 展开成树。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package pack

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// zipSource 把 *zip.File 适配成 tree.DataSource。
//
// 与 Java 的差异（有意为之，属改进）：Java 的 CompressNodeFactory.creteNodeFile
// 在【建树阶段】就把每个条目解压写到临时目录并算好 md5。对一个上百 MB 的
// fat-jar 来说，这意味着建树即全量落盘。Go 版改为持有 *zip.File 惰性读取，
// md5 由 tree.Node.GetMd5OrInit 按需计算。语义等价，但不再产生临时文件。
//
// 代价：树的有效期依赖 zip.Reader 的底层文件句柄不被关闭。
type zipSource struct{ f *zip.File }

func (z zipSource) Open() (io.ReadCloser, error) { return z.f.Open() }

// BuildZipTree 把一个 zip/jar 展开为树，返回根节点。
//
// 移植自 BasePackService.buildChildrenWithCompress。有两处上游行为需要说明：
//
//  1. 【死代码】Java 里有针对 "META-INF/MANIFEST.MF" 和 "META-INF/" 的两个特判
//     分支，但 node 的 Name 取的是 getFileName(entry.getName())，即只有文件名
//     （"MANIFEST.MF"），拿它去和含目录的 "META-INF/MANIFEST.MF" 比较永远不等，
//     所以那两个分支从不生效。这里按【实际生效】的通用路径实现，不复制死分支。
//     该结论来自静态分析，未做运行时验证。
//
//  2. 【字符集】Java 用 new ZipInputStream(in, GBK) 解码条目名，即假定中文名是
//     GBK 编码。Go 的 archive/zip 只在 UTF-8 标志位存在时按 UTF-8 解码，否则
//     原样返回字节 —— 直接当字符串用，GBK 名就会一路乱码到界面。这里统一走
//     util.DecodeZipEntryName 解码成 UTF-8 存进 Path/Name（显示与匹配用），
//     原始字节存进 Node.RawPath，重打包时原样写回。
func BuildZipTree(rootName string, r io.ReaderAt, size int64, isPatch bool) (*tree.Node, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	return buildFromZipReader(rootName, zr, isPatch)
}

// BuildZipTreeFromPath 按**路径**展开磁盘上的 zip/jar，返回根节点。
//
// 和 BuildZipTree 的区别只在"谁来持有 ReaderAt"：
//   - BuildZipTree 要调用方给一个已经打开的 io.ReaderAt，于是那个句柄的寿命
//     变成调用方（或树）的责任 —— 树活得久，句柄就得跟着活；
//   - 这个版本内部按需开文件，**每次随机读开一次、读完立刻关**，树上不挂
//     任何句柄，谁也不用负责关闭。
//
// 为什么需要它：应用包可能是几百 MB 的胖包，不能 io.ReadAll 进内存
// （用户 2026-10-26 报障"打开包之后占用 400 多内存"，那份 143MB 的
// []byte 就是元凶）；但让树长期持有 *os.File 又会引出"换包 / 退出 / 测试里
// 临时文件删不掉"的句柄归属问题（Windows 上 RemoveAll 会直接失败）。
// 每次读开一次把句柄寿命压到一次读之内，两个问题一起消掉。
//
// 代价：一次条目读取会多做几次 open/close（压缩流按块读）。实测读一个
// 条目是毫秒级，用户点开预览根本感觉不到；换来的是树不占内存也不占句柄。
func BuildZipTreeFromPath(path string, size int64, isPatch bool) (*tree.Node, error) {
	return BuildZipTree(filepath.Base(path), reopenReaderAt{path: path}, size, isPatch)
}

// reopenReaderAt 是一个"每次 ReadAt 都重新开一次文件"的 io.ReaderAt。
//
// 故意不缓存 *os.File：缓存就等于把句柄寿命又拉长回整棵树，绕回老问题。
type reopenReaderAt struct{ path string }

func (r reopenReaderAt) ReadAt(p []byte, off int64) (int, error) {
	f, err := os.Open(r.path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(p, off)
}

// BuildZipTreeFromReader 从一个流式 Reader 建树。
//
// 用途是展开「包中包」—— 例如 fat-jar 里的 BOOT-INF/lib/foo.jar。
// Go 的 archive/zip 需要 io.ReaderAt，所以这里先把内容读进内存再交给
// BuildZipTreeFromBytes。对嵌套 jar 来说这是必要代价；顶层 jar 请直接用
// BuildZipTree（os.Open 得到的 *os.File 本身就是 ReaderAt，不占内存）。
func BuildZipTreeFromReader(rootName string, r io.Reader, isPatch bool) (*tree.Node, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return BuildZipTreeFromBytes(rootName, data, isPatch)
}

// BuildZipTreeFromBytes 从内存中的 zip 数据建树。
//
// 注意：返回的树里的节点持有对 data 的引用（zipSource 惰性读取），
// 所以 data 在整棵树的生命周期内都不能被复用/回收 —— Go 的 GC 会
// 通过 ReaderAt 引用自动保活，调用方无需额外处理。
func BuildZipTreeFromBytes(rootName string, data []byte, isPatch bool) (*tree.Node, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	return buildFromZipReader(rootName, zr, isPatch)
}

// buildFromZipReader 是三个入口共用的实现。
func buildFromZipReader(rootName string, zr *zip.Reader, isPatch bool) (*tree.Node, error) {
	root := tree.NewDir(rootName, "", isPatch)

	var classes, innerClasses []*tree.Node
	parentNodeMap := make(map[string]*tree.Node)

	for _, f := range zr.File {
		// 【字符集】条目名先解码再进树。中文 Windows 上打出来的包，名字多半是
		// GBK 且没设 UTF-8 标志位，Go 的 archive/zip 会原样返回字节 —— 直接当
		// 字符串用就是一路乱码到界面（用户 2026-10-28 报的
		// "打开这种时文件名会出现乱码"）。原版 Java 用
		// `new ZipInputStream(in, GBK)` 无条件按 GBK 解，所以那边没有这个问题。
		//
		// 解码只作用于 Path/Name（显示与匹配用）；原始字节记在 RawPath 里，
		// 重打包时按原样写回（见 service.newEntryHeader），保证输出包字节级不变。
		raw := f.Name
		name := util.DecodeZipEntryName(raw)
		isDir := f.FileInfo().IsDir()

		var node *tree.Node
		if isDir {
			// 目录条目的三个时间在 Go 里拿不到（zip.FileHeader 只暴露 Modified），
			// 统一用 Modified 填充 —— Java 侧读的是 ZipEntry 的
			// creationTime/lastAccessTime/lastModifiedTime 三个独立字段。
			//
			// 保真点：Path 用【原样的条目名】，也就是保留尾部 '/'。
			// Java 的 CompressNodeZipFactory 是 `setPath(entry.getName())`，
			// 而 AppPackWriteProcessor.doWrite 靠 `"META-INF/".equals(child.getPath())`
			// 判断"补丁签名该写在哪儿" —— 少了这个斜杠就永远判不中，
			// 签名会被追加到包末尾、并多写一个重复的 META-INF/ 目录条目。
			// 注意：补建的占位目录（getOrCreateParentNode）Path 不带斜杠，
			// 那是 Java 的 getDirPath 行为，两者不要混为一谈。
			t := modTime(f)
			node = tree.NewCompressDir(util.FileName(util.UnPerfectDirPath(name)), name, t, t, t, isPatch)
		} else {
			// 保真点：Kind 必须是 KindCompress。Java 的 CompressNodeZipFactory
			// 建出来的就是 CompressNode，而"要不要把 README 写进补丁包"
			// （PatchPackService.updatePatchReadme）正是按 instanceof CompressNode
			// 分流的 —— 这里若退化成 KindFile，README 会被写到当前工作目录去。
			//
			// 另两点对齐 CompressNodeZipFactory.create：
			//   - crc 取自条目（Java: entry.getCrc()），不是 -1；
			//   - extra 原样保留（Java: entry.getExtra()），重打包时要写回去；
			//   - comment 在 Java 里【没有】赋值，所以这里也不填。
			t := modTime(f)
			node = tree.NewCompressFile(util.FileName(name), name,
				int64(f.UncompressedSize64), int64(f.CRC32), t, t, t, zipSource{f}, isPatch)
			node.Extra = f.Extra
		}
		node.RawPath = rawPathIfDecoded(raw, name)

		// 内部类单独收集，最后统一挂到外部类下
		if !node.Dir() && strings.HasSuffix(node.Name, constant.DotClass) {
			if idx, _ := util.LastIndexOfAndCount(node.Name, '$'); idx != -1 {
				innerClasses = append(innerClasses, node)
				continue
			}
			classes = append(classes, node)
		}

		if parent := getOrCreateParentNode(root, parentNodeMap, node, isPatch); parent != nil {
			parent.AddChild(node)
		} else {
			root.AddChild(node)
		}
		if node.Dir() {
			parentNodeMap[util.UnPerfectDirPath(node.Path)] = node
		}
	}

	if len(classes) > 0 && len(innerClasses) > 0 {
		HandleInnerClass(classes, innerClasses)
	}
	// 不排序：Java 的 SortLinkedList.add 是纯追加（排序逻辑被注释掉了），
	// 同级顺序就是条目在 zip 里的出现顺序。排序会让重打包出的条目顺序与原版分叉。
	return root, nil
}

// getOrCreateParentNode 找到或补建节点的父目录。
//
// 移植自 BasePackService.getOrCreateParentNode。zip 不保证目录条目先于其内容
// 出现，所以当真正的目录条目后到时，要把先前补建的占位目录节点"合并"掉。
func getOrCreateParentNode(root *tree.Node, parentNodeMap map[string]*tree.Node, node *tree.Node, isPatch bool) *tree.Node {
	if node.Dir() {
		key := util.UnPerfectDirPath(node.Path)
		if exist, ok := parentNodeMap[key]; ok {
			if exist.Parent != nil {
				exist.Parent.RemoveChild(exist)
			}
			for _, child := range exist.Children {
				node.AddChild(child)
			}
			return exist.Parent
		}
	}

	parentPath := util.GetDirPath(node.Path)
	if util.IsBlank(parentPath) {
		return root
	}
	if p, ok := parentNodeMap[parentPath]; ok {
		return p
	}

	// 目录条目缺失，补建一个占位目录节点。
	//
	// 保真点：Java 这里建的是 `CompressNode` + `setDir(true)`（见
	// BasePackService.getOrCreateParentNode），不是 FileNode。两者在
	// "是不是压缩包条目"这一点上语义不同，而
	// PatchPackService.updatePatchReadme 的向上收集循环正是按
	// `instanceof CompressNode` 决定要不要继续往上走 —— 退化成普通目录节点
	// 会让循环提前停下。
	now := time.Now()
	placeholder := tree.NewCompressDir(util.FileName(parentPath), parentPath, now, now, now, isPatch)
	// 父目录若是从 GBK 名字解出来的，占位目录也要记住原始字节 —— 否则重打包时
	// 它会以 UTF-8 名字写进包，与同目录其它条目编码不一致。
	if node.RawPath != "" {
		placeholder.RawPath = util.GetDirPath(node.RawPath)
	}
	parent := getOrCreateParentNode(root, parentNodeMap, placeholder, isPatch)
	if parent != nil {
		parent.AddChild(placeholder)
	}
	parentNodeMap[parentPath] = placeholder
	return placeholder
}

// HandleInnerClass 把 Foo$Bar.class 这类内部类挂到 Foo.class 之下。
//
// 移植自 BasePackService.handleInnerClass。按 '$' 的层数逐层归位，
// 处理匿名内部类（Foo$1.class）和多层嵌套（Foo$Bar$Baz.class）。
//
// 参数 classes 是"外部类"候选集合，会被过滤出 .class 结尾的节点建索引；
// innerClasses 是待归位的内部类。归位不上的（找不到外部类）会被丢弃 ——
// 这是上游行为，不是遗漏。
func HandleInnerClass(classes, innerClasses []*tree.Node) {
	// 键必须用「去掉扩展名的完整路径」，对应 Java 的 FileNameUtil.getName。
	// 注意别错用 util.FileName（那是取 basename 且保留扩展名），
	// 否则键与查找值对不上，所有内部类都会被判为孤儿而丢弃。
	classMap := make(map[string]*tree.Node, len(classes))
	for _, n := range classes {
		if !strings.HasSuffix(n.Name, constant.DotClass) {
			continue
		}
		key := util.Name(n.Path)
		if _, exists := classMap[key]; !exists {
			classMap[key] = n
		}
	}
	handleInnerClassLevel(classMap, innerClasses, 1)
}

// HandleInnerClassUnder 在指定父节点下归位内部类。
//
// 移植自 BasePackService.handleInnerClass(TreeNode, List)。用于"补丁目录"
// 这种直接从磁盘目录建树的场景 —— 内部类就在父节点的子级里，需要先把它
// 们从候选集合中挑出来。
func HandleInnerClassUnder(parent *tree.Node, innerClasses []*tree.Node) {
	if parent == nil || len(parent.Children) == 0 {
		return
	}
	HandleInnerClass(parent.Children, innerClasses)
}

func handleInnerClassLevel(classMap map[string]*tree.Node, innerClasses []*tree.Node, level int) {
	var remaining []*tree.Node
	for _, inner := range innerClasses {
		index, count := util.LastIndexOfAndCount(inner.Path, '$')
		if count > level {
			remaining = append(remaining, inner)
			continue
		}
		if count == level {
			outer := classMap[inner.Path[:index]]
			if outer != nil {
				outer.AddChild(inner)
				classMap[util.Name(inner.Path)] = inner
			}
		}
	}
	if len(remaining) > 0 {
		handleInnerClassLevel(classMap, remaining, level+1)
	}
}

// rawPathIfDecoded 在名字被解码过时返回原始字节，否则返回空串。
//
// 空串是"没有原始字节"的哨兵（见 tree.Node.RawPath）：ASCII 名和本来就是
// UTF-8 的名字解码前后完全相同，没有留原始字节的必要，重打包时用 Path 即可。
func rawPathIfDecoded(raw, decoded string) string {
	if raw == decoded {
		return ""
	}
	return raw
}

// modTime 取条目的修改时间，缺失时回落到当前时间。
// 坑：Go 的 archive/zip 在读取「零 DOS 时间戳」的条目时【不会】返回零值，
// 而是把 MS-DOS 历元 1980-01-01 经月份归一化后解析成 1979-11-30 00:00:00
// 这类固定哨兵（本机实测正是这个值， timezone 无关——它落在 1980-01-01 之前）。
// 这种条目本质上"没有时间戳"，本应按注释本意回落到当前时间，否则补丁树里
// 大量文件会被显示成一个毫无意义的固定日期，让人误以为"没显示具体时间"。
//
// 所以这里显式识别这个哨兵：凡解析出的时间早于 MS-DOS 历元（1980-01-01）的，
// 都视为缺失、回落到 time.Now()。真实的时间戳（哪怕就是 1980 年）都晚于这个
// 阈值，不会被误判。
func modTime(f *zip.File) time.Time {
	t := f.Modified
	if t.IsZero() || t.Before(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return time.Now()
	}
	return t
}
