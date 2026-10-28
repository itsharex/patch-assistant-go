package service

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/model"
	"cn.cpoet.patch.assistant/internal/tree"
	"cn.cpoet.patch.assistant/internal/util"
)

// tempFileExt 临时文件后缀，对应 Java FileTempUtil.TEMP_FILE_EXT。
const tempFileExt = ".temp"

// datePureLayout 与 Java DateUtil.DATE_PURE_FORMAT 一致。
const datePureLayout = "20060102"

// WriteContext 写出时的上下文，覆盖"打补丁写应用包"与"存补丁包"两条路径。
//
// 对应 Java 里散落在 AppContext / HomeContext / Configuration 中的那几项依赖，
// Go 版显式收成一个 struct，便于测试时替换。
type WriteContext struct {
	// AppTreeInfo 应用包信息
	AppTreeInfo *tree.AppTreeInfo
	// PatchTreeInfo 补丁包信息，可为 nil（不打补丁、只是重打包）
	PatchTreeInfo *tree.PatchTreeInfo
	// Total 统计信息，写进签名
	Total *tree.TotalInfo
	// WritePatchSign 是否写入 META-INF/patch-up/*.sign
	WritePatchSign bool
	// Username 打补丁的人，写进签名
	Username string
	// ReadmeFile 补丁说明文件名，空则用 constant.ReadmeFile。
	// 对应 Java Configuration.getPatch().getReadmeFile()。
	ReadmeFile string
	// TempDir 临时目录，空则用系统临时目录
	TempDir string
	// Step 进度回调
	Step Step
}

// appPackWriter 打补丁写出器。
// 移植自 Java AppPackWriteProcessor。
type appPackWriter struct {
	ctx *WriteContext

	delInfos []model.DelInfo
	addInfos []model.AddInfo
	modInfos []model.ModInfo
}

// SaveAppPack 生成打过补丁的应用包，写出到 outFile。
//
// 移植自 Java AppPackService.savePack → AppPackWriteProcessor.exec。
//
// 流程是"先写临时文件、成功后再覆盖目标文件"——这样中途失败不会毁掉
// 用户原有的包。Go 版保持同样的两段式。
func SaveAppPack(ctx *WriteContext, outFile string) error {
	w := &appPackWriter{ctx: ctx}
	transitFile := createTransitFile(outFile, ctx.TempDir)

	err := w.write(transitFile)
	if err != nil {
		_ = os.Remove(transitFile)
		return err
	}
	step(ctx.Step, "Write to:"+outFile)
	if err := copyFile(transitFile, outFile); err != nil {
		_ = os.Remove(transitFile)
		return fmt.Errorf("write file failed: %w", err)
	}
	step(ctx.Step, "Clean transit file")
	_ = os.Remove(transitFile)
	return nil
}

// write 把整棵树写成一个新的 zip。
// 移植自 Java AppPackWriteProcessor.write。
func (w *appPackWriter) write(file string) error {
	out, err := os.Create(file)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	if err := w.doWrite(w.ctx.AppTreeInfo.RootNode(), zw); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

// doWrite 遍历应用树根的直接子级，按需先插入补丁签名。
//
// 移植自 Java AppPackWriteProcessor.doWrite。两处细节：
//
//   - 补丁签名必须在 "META-INF/" 目录条目【之前】写进去，所以这里是在
//     遇到 META-INF 目录时先调 writePatchSign、再写这个目录本身；
//   - 若整棵树里根本没有 META-INF 目录，签名会作为【末尾追加】的条目写入
//     （此时 writePatchSign 会自己补出 META-INF/ 目录条目）。
func (w *appPackWriter) doWrite(rootNode *tree.Node, zw *zip.Writer) error {
	step(w.ctx.Step, "Start write")
	isWritePathSign := false
	if len(rootNode.Children) > 0 {
		step(w.ctx.Step, "Application pack name:"+rootNode.Name)
		for _, child := range rootNode.Children {
			if constant.MetaInfoDir == child.Path && w.isWritePatchSign() {
				if err := w.writePatchSign(zw, child); err != nil {
					return err
				}
				isWritePathSign = true
			}
			if err := w.writeTreeNode2Pack(zw, child); err != nil {
				return err
			}
		}
	}
	if !isWritePathSign && w.isWritePatchSign() {
		if err := w.writePatchSign(zw, nil); err != nil {
			return err
		}
	}
	step(w.ctx.Step, "Write finished")
	return nil
}

// writePatchSign 写入一个补丁签名条目。
//
// 移植自 Java AppPackWriteProcessor.writePatchSign。文件名规则是
// `<yyyyMMdd><两位序号>.sign`，序号从 01 开始，取当天【尚未占用】的最小值。
//
// 保真提示：序号解析用的是 `name.substring(0, len - ".sign".length()).substring(8)`，
// 解析失败一律记作 0。也就是说文件名长度不够或不是数字时，会往集合里塞一个 0
// —— 而 0 永远不会与 targetNo 相等，所以不会影响占位判断。
func (w *appPackWriter) writePatchSign(zw *zip.Writer, metaInfoNode *tree.Node) error {
	targetNo := 1
	var patchSignNode *tree.Node
	fileName := time.Now().Format(datePureLayout)

	if metaInfoNode == nil {
		if err := writeDirEntry(zw, constant.MetaInfoDir); err != nil {
			return err
		}
	} else if len(metaInfoNode.Children) > 0 {
		for _, child := range metaInfoNode.Children {
			if constant.PatchUpPath == child.Path {
				patchSignNode = child
				break
			}
		}
	}

	if patchSignNode == nil {
		if err := writeDirEntry(zw, constant.PatchUpPath); err != nil {
			return err
		}
	} else if len(patchSignNode.Children) > 0 {
		existsNoSet := make(map[int]struct{})
		for _, child := range patchSignNode.Children {
			if !strings.HasPrefix(child.Name, fileName) {
				continue
			}
			no := 0
			trimmed := strings.TrimSuffix(child.Name, constant.DotSign)
			if len(trimmed) > len(fileName) {
				if v, err := strconv.Atoi(trimmed[len(fileName):]); err == nil {
					no = v
				}
			}
			existsNoSet[no] = struct{}{}
		}
		if len(existsNoSet) > 0 {
			for {
				if _, taken := existsNoSet[targetNo]; !taken {
					break
				}
				targetNo++
			}
		}
	}

	no := strconv.Itoa(targetNo)
	if targetNo <= 9 {
		no = "0" + no
	}
	name := fileName + no + constant.DotSign
	entryName := util.JoinPath(constant.PatchUpPath, name)

	content, err := w.createPatchSignContent()
	if err != nil {
		return err
	}
	return writeFileEntry(zw, entryName, content)
}

// createPatchSignContent 组装并序列化补丁签名。
//
// 移植自 Java AppPackWriteProcessor.createPatchSignContent。
//
// 保真点：签名的基础信息来自【主根】的 PatchSign（不是自定义根），
// 而自定义根只会以 `signs` 数组的形式附在后面。
// 另外注意 PatchUpSign.of 是逐字段拷贝（code/name/md5/sha1/readme/path），
// 所以 patch-up.sign 里会带上主根的 readme 全文。
func (w *appPackWriter) createPatchSignContent() ([]byte, error) {
	patchUpSign := &model.PatchUpSign{}
	if w.ctx.PatchTreeInfo != nil && w.ctx.PatchTreeInfo.RootInfo() != nil &&
		w.ctx.PatchTreeInfo.RootInfo().PatchSign != nil {
		patchUpSign = model.NewPatchUpSignOf(*w.ctx.PatchTreeInfo.RootInfo().PatchSign)
	}

	total := w.ctx.Total
	if total == nil {
		total = tree.NewTotalInfo()
	}
	addTotal, modTotal, delTotal, manualDelTotal := total.AddTotal(), total.ModTotal(), total.DelTotal(), total.ManualDelTotal()
	patchUpSign.AddTotal = &addTotal
	patchUpSign.ModTotal = &modTotal
	patchUpSign.DelTotal = &delTotal
	patchUpSign.ManualDelTotal = &manualDelTotal

	now := time.Now()
	patchUpSign.OperTime = model.NewJavaDate(now)
	patchUpSign.OperUser = w.ctx.Username

	if w.ctx.AppTreeInfo != nil {
		if sign := w.ctx.AppTreeInfo.AppPackSign(); sign != nil {
			patchUpSign.OriginAppMd5 = sign.Md5
			patchUpSign.OriginAppSha1 = sign.Sha1
			patchUpSign.OriginAppPath = sign.Path
		}
		if root := w.ctx.AppTreeInfo.RootNode(); root != nil {
			size := root.GetSizeOrInit()
			patchUpSign.OriginAppSize = &size
		}
	}

	patchUpSign.DelInfos = w.delInfos
	patchUpSign.AddInfos = w.addInfos
	patchUpSign.ModInfos = w.modInfos

	if w.ctx.PatchTreeInfo != nil {
		customRoots := w.ctx.PatchTreeInfo.CustomRootInfoMap()
		if len(customRoots) > 0 {
			signs := make([]model.PatchSign, 0, len(customRoots))
			for _, entry := range customRoots {
				if entry.Info != nil && entry.Info.PatchSign != nil {
					signs = append(signs, *entry.Info.PatchSign)
				}
			}
			patchUpSign.Signs = signs
		}
	}

	return util.WriteJSONAsBytes(patchUpSign)
}

// writeTreeNode2Pack 把单个节点写进新的 zip。
//
// 移植自 Java AppPackWriteProcessor.writeTreeNode2Pack。分支很多，逐个说明：
//
//   - DEL 状态：不写进新包；文件节点顺手记一条 delInfo（目录不记）。
//   - .jar 文件：走 writeTreeNode2PackWithJar（见下），并按 ADD/MOD 记信息。
//   - 未绑定（MappedNode == nil）：原样写出自己的内容。
//   - 已绑定：写出【补丁侧节点】的内容，但用【应用节点】的路径 ——
//     这就是"打补丁"的本质；同时把修改时间改成补丁节点的时间。
//
// 保真提示：已绑定分支里判断是否写内容用的是 `!zipEntry.isDirectory()`，
// 而 zipEntry 是由【应用节点】的路径生成的，所以判断依据是应用节点的
// 目录属性。这与前面的 `node.isDir()` 是同一个值，无差异。
func (w *appPackWriter) writeTreeNode2Pack(zw *zip.Writer, node *tree.Node) error {
	if node.Type == constant.TreeNodeTypeDel {
		if !node.Dir() {
			w.addDelInfo(node)
		}
		step(w.ctx.Step, "Delete:"+node.Name)
		return nil
	}

	if !node.Dir() && strings.HasSuffix(node.Name, constant.DotJar) {
		step(w.ctx.Step, "Write:"+node.Name)
		if err := w.writeTreeNode2PackWithJar(zw, node); err != nil {
			return err
		}
		if node.MappedNode != nil {
			if node.Type == constant.TreeNodeTypeAdd {
				w.addAddInfo(node, node.MappedNode)
			} else {
				w.addModInfo(node, node.MappedNode)
			}
		}
		return nil
	}

	if node.MappedNode == nil {
		if err := writeNodeEntry(zw, node, node, node.ModifyTime(), w.ctx.Step); err != nil {
			return err
		}
	} else {
		mappedNode := node.MappedNode
		if err := writeNodeEntry(zw, node, mappedNode, mappedNode.ModifyTime(), w.ctx.Step); err != nil {
			return err
		}
		if !node.Dir() {
			if node.Type == constant.TreeNodeTypeAdd {
				w.addAddInfo(node, mappedNode)
			} else {
				w.addModInfo(node, mappedNode)
			}
		}
	}

	for _, child := range node.Children {
		if err := w.writeTreeNode2Pack(zw, child); err != nil {
			return err
		}
	}
	return nil
}

// writeTreeNode2PackWithJar 写出一个 jar 节点。
//
// 移植自 Java AppPackWriteProcessor.writeTreeNode2PackWithJar，分三种：
//
//   - **整个 jar 被换掉**（节点自己绑到了补丁侧的某个 jar）→ 直接写补丁侧的内容；
//   - 子级为空（没展开过、也没改过内部内容）→ 直接把原 jar 字节写出去；
//   - 有子级（内部被打过补丁）→ 先把子级重打包成一个临时 jar，
//     再把临时 jar 作为一个 STORED 条目写出去。
//
// 【与原版的偏差，修的是上游的真 bug】原版只看 children，**完全不看
// MappedNode** —— 于是"把一个 jar 整个换进 BOOT-INF/lib"这条操作写出来还是
// 旧 jar：children 为空时写自己的字节，展开过时又拿旧子级重打包回去。
// README 里对 jar 写 `!路径` 同样无效（上游一样静默丢掉）。
// 为什么不能靠 children 判"要不要重打包"：树上的 jar 只要被展开过就有
// children（那是显示用的），这时重打包会把旧内容拼回去，新 jar 还是丢。
// 所以判据必须是**节点自己有没有绑定**：绑了就是整体替换，没绑才看 children
// （内部逐条打补丁的那条路，jar 节点本身是不绑的，见
// service.doMatchMappedNodeWithReadme 的下探分支）。
//
// 保真提示：临时 jar 那条路径会把条目的修改时间设成【当前时间】而不是
// 原 jar 的时间，且会显式设置 size 与 crc —— 因为 STORED 条目必须
// 预先知道这两项。这是上游行为，照搬。
func (w *appPackWriter) writeTreeNode2PackWithJar(zw *zip.Writer, node *tree.Node) error {
	if node.MappedNode != nil {
		src := node.MappedNode
		return writeNodeEntry(zw, node, src, src.ModifyTime(), w.ctx.Step)
	}
	if len(node.Children) == 0 {
		return writeNodeEntry(zw, node, node, node.ModifyTime(), w.ctx.Step)
	}

	tempFile, err := w.getTempFileWithJarNode(node)
	if err != nil {
		return err
	}
	defer os.Remove(tempFile)

	data, err := os.ReadFile(tempFile)
	if err != nil {
		return err
	}
	hdr := newEntryHeader(node, node.ModifyTime())
	hdr.Method = zip.Store
	hdr.CRC32 = crc32.ChecksumIEEE(data)
	hdr.UncompressedSize64 = uint64(len(data))
	hdr.CompressedSize64 = uint64(len(data))
	hdr.Modified = time.Now()
	return writeRawEntry(zw, hdr, data)
}

// getTempFileWithJarNode 把 jar 节点的子级重打包成临时 jar，返回其路径。
// 移植自 Java AppPackWriteProcessor.getTempFileWithJarNode。
func (w *appPackWriter) getTempFileWithJarNode(jarNode *tree.Node) (string, error) {
	path := filepath.Join(tempDirOf(w.ctx.TempDir), uniqueFileName(jarNode.Name))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	for _, child := range jarNode.Children {
		if err := w.writeTreeNode2Pack(zw, child); err != nil {
			_ = zw.Close()
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func (w *appPackWriter) addDelInfo(appNode *tree.Node) {
	if !w.isWritePatchSign() {
		return
	}
	w.delInfos = append(w.delInfos, model.DelInfo{
		AppPath:       appNode.Path,
		AppMd5:        appNode.GetMd5OrInit(),
		AppCreateTime: formatDateTime(appNode.ModifyTime()),
	})
}

func (w *appPackWriter) addAddInfo(appNode, patchNode *tree.Node) {
	if !w.isWritePatchSign() {
		return
	}
	w.addInfos = append(w.addInfos, model.AddInfo{
		AppPath:         appNode.Path,
		PatchPath:       patchNode.Path,
		PatchMd5:        patchNode.GetMd5OrInit(),
		PatchCreateTime: formatDateTime(patchNode.ModifyTime()),
	})
}

func (w *appPackWriter) addModInfo(appNode, patchNode *tree.Node) {
	if !w.isWritePatchSign() {
		return
	}
	w.modInfos = append(w.modInfos, model.ModInfo{
		AppPath:         appNode.Path,
		AppMd5:          appNode.GetMd5OrInit(),
		PatchPath:       patchNode.Path,
		PatchMd5:        patchNode.GetMd5OrInit(),
		AppCreateTime:   formatDateTime(appNode.ModifyTime()),
		PatchCreateTime: formatDateTime(patchNode.ModifyTime()),
	})
}

func (w *appPackWriter) isWritePatchSign() bool { return w.ctx.WritePatchSign }

// ---------------------------------------------------------------------------
// zip 写出辅助
// ---------------------------------------------------------------------------

// newEntryHeader 依据节点构造 zip 条目头。
//
// 移植自 Java AppPackWriteProcessor.getNewEntryWithZipEntry。对齐点：
//   - 目录条目的名字带尾部 '/'；
//   - 名字以 .jar 结尾的条目用 STORED（不压缩）—— 这是 Spring Boot
//     fat-jar 的约定，别改成 DEFLATE；
//   - 其余条目用 DEFLATE（Java ZipOutputStream 的默认 method）。
//
// 【刻意不调 SetMode】早期版本对目录条目调了 hdr.SetMode(os.ModeDir|0o755)，
// 黄金基准比对时暴露出这会同时污染两个字段：
//   - external attributes：Java 写 0（ZipOutputStream 不设 Unix 权限位），
//     SetMode 会写成 0x41ed0010（Unix 0o40755）；
//   - version made by：Java 写 20（0x0014，MS-DOS），SetMode 会把高字节改成
//     0x03（Unix）→ 788（0x0314）。
//
// 解压时这两项决定目录权限，属于用户可见行为，所以对齐 Java（写 0）。
//
// 【无法对齐的一项】目录条目的压缩方式：Java 的 `new ZipEntry("xxx/")` 用
// 默认的 DEFLATED，写出的压缩后大小是 2 字节的空 deflate 流（黄金基准实测：
// method=8 csize=2 size=0 crc=0）。但 Go 的 archive/zip 会强制把任何以 '/'
// 结尾的条目改成 Store：
//
//	if strings.HasSuffix(fh.Name, "/") {
//	    // Set the compression method to Store to ensure data length is truly zero
//	    fh.Method = Store
//	    fh.Flags &^= 0x8
//	    ... 尺寸清零 ...
//	}
//
// 这是标准库的硬限制（见 $GOROOT/src/archive/zip/writer.go:343），不是我们
// 的选择。CreateRaw 虽然不强制 Store，但对以 '/' 结尾的名字会返回 dirWriter{}
// 丢弃写入 —— 那 2 字节的 deflate 流补不进去，硬写会让后续本地头错位、
// 整个包损坏。影响仅限目录条目的 method/csize 两个字段，条目名、顺序、内容
// 都不受影响。
//
// 与 Java 的另一处差异：Java 对 CompressNode 会写 creationTime / accessTime
// （0x5455 扩展时间戳 extra，LOC 13 字节 / CD 5 字节），Go 的 archive/zip 只
// 暴露一个 Modified，且 LOC 与 CD 共用同一个 Extra 字段，写不出「LOC 长、CD 短」
// 这种不对称布局。详见 MIGRATION.md 的差异表。
// entryNameOf 返回条目写进包时该用的名字，以及"这个名字不是 UTF-8"的标志。
//
// 【为什么需要它】打开包时把 GBK 条目名解码成了 UTF-8 存进 Node.Path（否则
// 界面全是乱码，见 util.DecodeZipEntryName），但**写回时不能拿 Path 当条目名**
// —— 那样包里那条名字会从 GBK 悄悄变成 UTF-8，下游仍按 GBK 解名的工具立刻
// 看到乱码。Node.RawPath 存着原始字节，只在"解码前后不同"时才非空；
// 新建节点（拖进来的文件、补丁新增的文件）恒为空，走 Path 那一路。
//
// 返回的 nonUTF8 必须传给 zip.FileHeader.NonUTF8：不设的话 archive/zip 会按
// "合法 UTF-8 且需要多字节"去加 0x800 标志，读回来就被当成 UTF-8 解 ——
// 等于把 GBK 字节冒充成 UTF-8，比乱码更难查。
func entryNameOf(node *tree.Node) (name string, nonUTF8 bool) {
	name = node.Path
	if node.RawPath != "" {
		name = node.RawPath
		nonUTF8 = true
	}
	if node.Dir() {
		// 幂等：RawPath 里的目录名本就带尾 '/'，这里只是补上缺的那一个
		// （占位目录的 RawPath 来自 GetDirPath，不带斜杠）。
		name = util.PerfectDirPath(name)
	}
	return name, nonUTF8
}

func newEntryHeader(node *tree.Node, modified time.Time) *zip.FileHeader {
	name, nonUTF8 := entryNameOf(node)
	method := zip.Deflate
	if node.Dir() {
		method = zip.Store
	}
	hdr := &zip.FileHeader{
		Name:     name,
		Method:   method,
		Comment:  node.Comment,
		Extra:    node.Extra,
		Modified: modified,
		NonUTF8:  nonUTF8,
	}
	if node.Dir() {
		return hdr
	}
	if strings.HasSuffix(node.Name, constant.DotJar) {
		hdr.Method = zip.Store
	}
	return hdr
}

// storedVersion 是 STORED 条目的 version made by / version needed to extract。
//
// Java 的 ZipOutputStream.getVersion() 按压缩方式取值：STORED → 10，
// DEFLATED → 20，并且 CEN 的「made by」和「needed」用同一个值。
// Go 的 CreateHeader 一律写 20，CreateRaw 则完全不碰这两个字段，
// 所以走 CreateRaw 的 STORED 分支要自己设成 10。
const storedVersion = 10

// msDosTime 把本地时间编码成 MS-DOS 日期/时间两个 16 位字段。
//
// 与 archive/zip 内部的 timeToMsDosTime 同构。之所以自己实现，是因为需要
// 【只设 DOS 时间、不设 Modified】：CreateHeader 一旦发现 Modified 非零，
// 除了写 DOS 时间还会追加 0x5455 扩展时间戳 extra 字段；而 Java 对这类
// 「裸 new ZipEntry」建出来的条目（META-INF/、META-INF/patch-up/、*.sign）
// 是不带 extra 的，只有 putNextEntry 填进去的当前 DOS 时间。
func msDosTime(t time.Time) (dosDate, dosTime uint16) {
	dosDate = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	dosTime = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return
}

// writeNodeEntry 写出一个节点：条目头用 headerNode，内容取自 contentNode。
//
// headerNode 与 contentNode 分离，是为了支持"用应用节点的路径、
// 写补丁节点的内容"这个补丁语义。
func writeNodeEntry(zw *zip.Writer, headerNode, contentNode *tree.Node, modified time.Time, st Step) error {
	hdr := newEntryHeader(headerNode, modified)
	if hdr.Method == zip.Store && !headerNode.Dir() {
		// STORED 条目必须预先知道大小与 crc
		data, err := readNodeAll(contentNode)
		if err != nil {
			return err
		}
		hdr.CRC32 = crc32.ChecksumIEEE(data)
		hdr.UncompressedSize64 = uint64(len(data))
		hdr.CompressedSize64 = uint64(len(data))
		if headerNode.Size == tree.SizeUnknown {
			headerNode.SetSize(int64(len(data)))
		}
		return writeRawEntry(zw, hdr, data)
	}

	entryWriter, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if headerNode.Dir() {
		return nil
	}
	step(st, "Write:"+headerNode.Name)
	return contentNode.ConsumeBytes(func(buf []byte) error {
		_, werr := entryWriter.Write(buf)
		return werr
	})
}

// writeRawEntry 写出一个内容已知的 STORED 条目（不压缩，数据原样写入）。
//
// 用 CreateRaw 而不是 CreateHeader，是为了对齐 Java 的 STORED 写法：
//   - CreateHeader 会无条件置 data descriptor 位（fh.Flags |= 0x8），
//     并把 crc/size 留到条目末尾的数据描述符里再写；
//   - Java 对 STORED 是【尺寸已知】的（调用方先 setSize/setCrc），
//     所以直接写进本地头、不产生数据描述符。
//
// CreateRaw 不置该位，且在无数据描述符时把 crc/csize/usize 写进本地头
// （见 $GOROOT/src/archive/zip/writer.go 的 writeHeader），正好对上。
// 它也不碰 ReaderVersion/CreatorVersion，所以这里显式设成 Java 的 STORED 版本号。
//
// 注意 Go 的 zip.Writer 不需要（也无法）手动关闭单个条目 ——
// CreateHeader/CreateRaw 会自动关掉上一个条目，Writer.Close 关掉最后一个。
// 这与 Java ZipOutputStream 的 putNextEntry/closeEntry 语义一致。
func writeRawEntry(zw *zip.Writer, hdr *zip.FileHeader, data []byte) error {
	hdr.ReaderVersion = storedVersion
	hdr.CreatorVersion = storedVersion

	// CreateRaw 与 CreateHeader 还有一处不同：它【不会】把 Modified 转成
	// MS-DOS 日期/时间，也不会追加 0x5455 扩展时间戳。只设 Modified 的话
	// 写出来的 DOS 时间是 0（工具里显示成 1980-00-00）。这里手动转换一次，
	// 转完把 Modified 清掉，避免它被当成"未设置"以外的含义。
	if !hdr.Modified.IsZero() {
		hdr.ModifiedDate, hdr.ModifiedTime = msDosTime(hdr.Modified)
		hdr.Modified = time.Time{}
	}

	entryWriter, err := zw.CreateRaw(hdr)
	if err != nil {
		return err
	}
	_, err = entryWriter.Write(data)
	return err
}

// writeDirEntry 写出一个【新建的】目录条目。
//
// 对应 Java `zipOut.putNextEntry(new ZipEntry("xxx/"))`：
//   - 压缩方式：Java 用 ZipEntry 默认的 DEFLATED，但 Go 的 archive/zip 会
//     强制把以 '/' 结尾的条目改成 Store，这里显式写 Store 只是让代码与实际
//     行为一致（详见 newEntryHeader）；
//   - 时间戳：Java 的 putNextEntry 会给未设时间的条目填【当前时间】，
//     且不带 extra 字段。所以这里只设 DOS 时间、不设 Modified。
//     早期版本两个都没设，写出的是 0（显示成 1980-00-00）。
//
// 不调 SetMode，理由见 newEntryHeader。
func writeDirEntry(zw *zip.Writer, name string) error {
	dosDate, dosTime := msDosTime(time.Now())
	hdr := &zip.FileHeader{
		Name:         name,
		Method:       zip.Store,
		ModifiedDate: dosDate,
		ModifiedTime: dosTime,
	}
	_, err := zw.CreateHeader(hdr)
	return err
}

// writeFileEntry 写出一个【新建的】普通文件条目（DEFLATE）。
//
// 时间戳处理同 writeDirEntry：Java 那边是裸 new ZipEntry，putNextEntry
// 填当前 DOS 时间、不带 extra。当前唯一调用方是补丁签名 *.sign。
func writeFileEntry(zw *zip.Writer, name string, data []byte) error {
	dosDate, dosTime := msDosTime(time.Now())
	hdr := &zip.FileHeader{
		Name:         name,
		Method:       zip.Deflate,
		ModifiedDate: dosDate,
		ModifiedTime: dosTime,
	}
	entryWriter, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = entryWriter.Write(data)
	return err
}

// readNodeAll 把节点内容全部读进内存。
func readNodeAll(node *tree.Node) ([]byte, error) {
	var out []byte
	err := node.ConsumeBytes(func(buf []byte) error {
		out = append(out, buf...)
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------------------
// 文件系统辅助
// ---------------------------------------------------------------------------

// createTransitFile 生成中转文件路径：与目标同目录、后缀加 .temp。
// 对应 Java AppPackWriteProcessor.createTransitFile。
//
// 保真提示：Java 版在 `new File(parent, name + ext)` 抛异常时会回落到
// 系统临时目录。Go 版在父目录为空（相对路径无目录部分）时直接用当前目录，
// 不引入这个回落分支 —— 行为差异仅在极端输入下出现。
func createTransitFile(file, tempDir string) string {
	dir := filepath.Dir(file)
	if dir == "" || dir == "." {
		if tempDir != "" {
			return filepath.Join(tempDir, filepath.Base(file)+tempFileExt)
		}
		dir = "."
	}
	return filepath.Join(dir, filepath.Base(file)+tempFileExt)
}

// tempDirOf 返回用于临时文件的目录。
func tempDirOf(dir string) string {
	if dir == "" {
		return os.TempDir()
	}
	return dir
}

// uniqueFileName 生成不会重名的文件名。
//
// 对应 Java FileNameUtil.uniqueFileName：`<主名>_<32位随机>.<扩展名>`。
func uniqueFileName(name string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return util.Name(name) + "_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	base := util.Name(name) + "_" + hex.EncodeToString(buf)
	if ext := util.Ext(name); ext != "" {
		return base + string(util.ExtSeparator) + ext
	}
	return base
}

// copyFile 复制文件内容。
// 对应 Java FileUtil.copyTo。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
