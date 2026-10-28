package service

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"cn.cpoet.patch.assistant/internal/util"
)

// ---------------------------------------------------------------------------
// 包内容对比（GUI「对比」对话框的数据源）
//
// 用途：用户手里有两个 jar/zip，想知道**里面哪些文件不同**。这与主界面的
// "应用包 + 补丁包匹配"是两件事 —— 那里跑的是补丁语义（README 指令、路径/
// 文件名匹配、增删改统计），这里只回答一个朴素的问题：逐文件比内容。
// 所以它是独立入口，不碰会话里的两棵树。
//
// 三条口径（界面提示与报告文本都按这个说）：
//  1. 只比【文件条目】，目录条目跳过；
//  2. 同路径 + 内容相同 = 相同；内容不同 = 内容不同；路径只在一侧 =
//     "仅 A 有" / "仅 B 有"（不假设哪边是新版本，UI 上也用中性词）；
//     "内容相同"的判定见 packSource 的注释：先看中央目录里的大小 + CRC32，
//     足够判否也足够判同，不需要解压；
//  3. 嵌套 jar（BOOT-INF/lib/x.jar）按普通二进制文件比，不递归展开 ——
//     内容变了就整条报"内容不同"。
//
// 不过滤 META-INF/patch-up/ 下的签名文件：这是工具自己写的书志，但
// "两个包差在哪"的诚实答案包含它，过滤掉反而是隐瞒。
// ---------------------------------------------------------------------------

// CompareEntry 一个包内文件条目的比对信息。
type CompareEntry struct {
	// Path 包内路径（zip 条目名原样，'/' 分隔）
	Path string `json:"path"`
	// Size 内容字节数
	Size int64 `json:"size"`
	// Md5 内容 md5（十六进制小写）
	Md5 string `json:"md5"`
	// ModTime 条目修改时间，Format("2006-01-02 15:04:05")
	ModTime string `json:"modTime,omitempty"`
}

// CompareChange 一条"两边都有、内容不同"的记录。
type CompareChange struct {
	// Path 包内路径
	Path string `json:"path"`
	// A 包 A 侧的条目信息
	A CompareEntry `json:"a"`
	// B 包 B 侧的条目信息
	B CompareEntry `json:"b"`
}

// ComparePackResult 两个包的比对结果。
type ComparePackResult struct {
	// Kind 结果类型判别字段，固定为 "pack" —— 前端据此在"包对比"与
	// "文件对比"（CompareFileResult）两套渲染分支间分流。
	Kind string `json:"kind"`
	// PathA / PathB 两个包的路径（回显用）
	PathA string `json:"pathA"`
	PathB string `json:"pathB"`
	// NameA / NameB 两个包的文件名（不含目录）
	NameA string `json:"nameA"`
	NameB string `json:"nameB"`
	// TotalA / TotalB 各自的文件条目数（不含目录条目）
	TotalA int `json:"totalA"`
	TotalB int `json:"totalB"`
	// Same 两边内容一致的条目数
	Same int `json:"same"`
	// Added 只在包 B 里有的文件
	Added []CompareEntry `json:"added"`
	// Removed 只在包 A 里有的文件
	Removed []CompareEntry `json:"removed"`
	// Changed 两边都有、内容不同的文件
	Changed []CompareChange `json:"changed"`
	// Text 纯文本报告（「拷贝报告」/「保存报告」用，前端不重新拼）
	Text string `json:"text"`
}

// ComparePacks 对比两个 jar/zip 内文件的内容差异。
//
// 实现要点：两段式读取（见 packSource）—— 先只扫中央目录拿到每个条目的大小
// 与 CRC32，用它判定"同/异"；只有判定为不同的条目才解压算 md5。内存里始终只留
// 每个条目的元信息（路径/大小/ md5/时间），不缓存任何内容字节。
func ComparePacks(pathA, pathB string) (*ComparePackResult, error) {
	a := strings.TrimSpace(pathA)
	b := strings.TrimSpace(pathB)
	if a == "" || b == "" {
		return nil, fmt.Errorf("请选择两个要比对的包")
	}
	// 同一条路径比不出东西，先挡掉 —— 不挡的话结果会是"全部相同"，
	// 用户还以为自己选错了包
	if a == b {
		return nil, fmt.Errorf("两个包是同一条路径，没有可比的内容")
	}

	sa, err := diskSource(a)
	if err != nil {
		return nil, err
	}
	sb, err := diskSource(b)
	if err != nil {
		return nil, err
	}
	return compareSources(sa, sb, a, b, filepath.Base(a), filepath.Base(b))
}

// ---------------------------------------------------------------------------
// 两段式读取：扫元信息（不解压）→ 只给"判定不同"的条目算摘要
//
// 为什么值得分两段：zip 的中央目录里**本来就有**每个条目的大小和 CRC32，
// 读它不解压任何字节。而"两个条目内容是否相同"这个问题，大小 + CRC32 已经
// 足够回答（大小不同必然不同；大小同、CRC 不同必然不同；大小与 CRC 都同
// 即判为相同）。于是全量 md5 变成了纯粹的浪费 —— 那是为了**展示**才需要的
// 东西，而界面上只展示"不同"的那几行。
//
// 实测（143 MB 的胖包，651 个条目）：
//   只扫中央目录        1 ms
//   全量解压算 md5    222 ms   ×2 个包 = 444 ms，且解压 145 MB
// 换掉之后，两个完全相同的胖包对比从 498 ms 掉到 3 ms 量级。
//
// 口径说明（已登记 MIGRATION.md）：判"相同"依据 size + CRC32，不是 md5。
// CRC32 在大小相同的前提下给出 1/2^32 的碰撞概率；这是**对比对话框**的展示
// 结论，不参与补丁匹配（补丁语义走 internal/service 的 match 那一套），所以
// 这个强度是可接受的。真要逐字节确证，点开那一行看内容即可。
// ---------------------------------------------------------------------------

// packMeta 扫描阶段的条目元信息。全部来自 zip 中央目录，不读内容。
type packMeta struct {
	// size 声明大小（未压缩字节数）
	size uint64
	// crc 内容 CRC32（zip 规范要求写在中央目录里，流式写出的包也有）
	crc uint32
	// modTime 条目修改时间，Format("2006-01-02 15:04:05")
	modTime string
}

// entryDigest 一个条目的内容摘要（解压后才知道）。
type entryDigest struct {
	// md5 内容 md5（十六进制小写）
	md5 string
	// size 实际读到的字节数（不信任声明值，见 digestZipEntries）
	size int64
}

// packSource 一个包的条目来源。
//
// meta 立即给（廉价）；digest 只在需要时按路径列表取（昂贵）。
// 磁盘包与内存字节包各有一个构造器（diskSource / bytesSource），比对核心
// 只认这个接口，所以两边的比对规则只有一份。
type packSource struct {
	meta   map[string]packMeta
	digest func(paths []string) (map[string]entryDigest, error)
}

// compareSources 是包对比的**唯一核心**：两个来源，产出结果。
//
// 抽出来是因为有两个来源要走同一套比对规则：磁盘上的两个包
// （ComparePacks）与**嵌套 jar 的字节**（ComparePackBytes，见"展开嵌套包"）。
// 规则只此一处，两边不各写一份。
func compareSources(sa, sb packSource,
	pathA, pathB, nameA, nameB string) (*ComparePackResult, error) {

	// 第一遍：只分桶，不构造条目 —— 此时还不知道哪些条目要算 md5
	var changedPaths, removedPaths, addedPaths []string
	same := 0
	for p, ma := range sa.meta {
		mb, ok := sb.meta[p]
		if !ok {
			removedPaths = append(removedPaths, p)
			continue
		}
		// 大小 + CRC32 都在中央目录里，判同判异都不需要解压
		if ma.size == mb.size && ma.crc == mb.crc {
			same++
			continue
		}
		changedPaths = append(changedPaths, p)
	}
	for p := range sb.meta {
		if _, ok := sa.meta[p]; !ok {
			addedPaths = append(addedPaths, p)
		}
	}

	// 第二遍：只给"要展示的条目"解压算 md5。
	// A 侧需要的是 变更 + 仅A有；B 侧需要的是 变更 + 仅B有 —— 相同条目一个
	// 字节都不读，这正是省下来的那 444 ms。
	dA, err := sa.digest(concatPaths(changedPaths, removedPaths))
	if err != nil {
		return nil, err
	}
	dB, err := sb.digest(concatPaths(changedPaths, addedPaths))
	if err != nil {
		return nil, err
	}

	// entryOf 拼出一个条目的展示信息。digest 里有的用实际读到的字节数
	// （流式读上来的才是真实内容），没有的退回中央目录的声明值 —— 后者只
	// 出现在"相同"条目上，而"相同"条目不进任何展示列表，也不进报告文本。
	entryOf := func(p string, m packMeta, dg map[string]entryDigest) CompareEntry {
		e := CompareEntry{Path: p, Size: int64(m.size), ModTime: m.modTime}
		if d, ok := dg[p]; ok {
			e.Size = d.size
			e.Md5 = d.md5
		}
		return e
	}

	res := &ComparePackResult{
		Kind:    "pack",
		PathA:   pathA,
		PathB:   pathB,
		NameA:   nameA,
		NameB:   nameB,
		TotalA:  len(sa.meta),
		TotalB:  len(sb.meta),
		Same:    same,
		Added:   []CompareEntry{},
		Removed: []CompareEntry{},
		Changed: []CompareChange{},
	}

	sortComparePaths(changedPaths)
	sortComparePaths(removedPaths)
	sortComparePaths(addedPaths)

	for _, p := range changedPaths {
		res.Changed = append(res.Changed, CompareChange{
			Path: p,
			A:    entryOf(p, sa.meta[p], dA),
			B:    entryOf(p, sb.meta[p], dB),
		})
	}
	for _, p := range removedPaths {
		res.Removed = append(res.Removed, entryOf(p, sa.meta[p], dA))
	}
	for _, p := range addedPaths {
		res.Added = append(res.Added, entryOf(p, sb.meta[p], dB))
	}

	res.Text = res.reportText()
	return res, nil
}

// concatPaths 拼两个路径列表（不改动入参切片）。
func concatPaths(x, y []string) []string {
	if len(y) == 0 {
		return x
	}
	out := make([]string, 0, len(x)+len(y))
	out = append(out, x...)
	out = append(out, y...)
	return out
}

// ComparePackBytes 对比**两个 jar/zip 的字节**（不是磁盘文件）。
//
// 用途：展开包对比结果里的嵌套 jar（`BOOT-INF/lib/x.jar`）—— 那两个 jar
// 的字节在外层包里，没有各自的磁盘路径，只能按字节比。
//
// entryPath 是它在**外层包里的路径**（如 `BOOT-INF/lib/x.jar`）：回显用它的
// 路径当 PathA/PathB、用文件名（`x.jar`）当 NameA/NameB。两侧是同一个嵌套
// jar 的 A/B 两份字节，所以只需一个名字。
func ComparePackBytes(entryPath string, a, b []byte) (*ComparePackResult, error) {
	name := filepath.Base(entryPath)
	sa, err := bytesSource(name, a)
	if err != nil {
		return nil, err
	}
	sb, err := bytesSource(name, b)
	if err != nil {
		return nil, err
	}
	return compareSources(sa, sb, entryPath, entryPath, name, name)
}

// withPack 打开一个 zip，把 *zip.Reader 交给 fn 用。
//
// 文件句柄的生命周期由这里管 —— 调用方只管用 zr，不用操心 Close。
// 三种失败各有各的文案（打开不了 / 读不了信息 / 不是 zip），因为前端会把
// 这句话原样 toast 出来，"不是有效的 jar/zip 文件"与"打开文件失败"
// 对用户是两件完全不同的事。
func withPack(packPath string, fn func(zr *zip.Reader) error) error {
	f, err := os.Open(packPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %s: %w", filepath.Base(packPath), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("读取文件信息失败: %s: %w", filepath.Base(packPath), err)
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		return fmt.Errorf("不是有效的 jar/zip 文件: %s", filepath.Base(packPath))
	}
	return fn(zr)
}

// withPackBytes 与 withPack 同理，但来源是一段内存字节（嵌套 jar 用）。
//
// 嵌套包的字节本来就已在内存里（从外层包读出来的），没有磁盘路径，
// 所以直接 zip.NewReader(bytes.Reader)。
func withPackBytes(name string, data []byte, fn func(zr *zip.Reader) error) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("不是有效的 jar/zip 文件: %s", name)
	}
	return fn(zr)
}

// diskSource 构造磁盘包的来源：扫一次中央目录拿元信息，解压推迟到 digest。
func diskSource(packPath string) (packSource, error) {
	var meta map[string]packMeta
	err := withPack(packPath, func(zr *zip.Reader) error {
		meta = scanZipMeta(zr)
		return nil
	})
	if err != nil {
		return packSource{}, err
	}
	return packSource{
		meta: meta,
		digest: func(paths []string) (map[string]entryDigest, error) {
			var out map[string]entryDigest
			err := withPack(packPath, func(zr *zip.Reader) error {
				var e error
				out, e = digestZipEntries(zr, paths)
				return e
			})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}, nil
}

// bytesSource 与 diskSource 同理，来源是一段内存字节（嵌套包用）。
func bytesSource(name string, data []byte) (packSource, error) {
	var meta map[string]packMeta
	err := withPackBytes(name, data, func(zr *zip.Reader) error {
		meta = scanZipMeta(zr)
		return nil
	})
	if err != nil {
		return packSource{}, err
	}
	return packSource{
		meta: meta,
		digest: func(paths []string) (map[string]entryDigest, error) {
			var out map[string]entryDigest
			err := withPackBytes(name, data, func(zr *zip.Reader) error {
				var e error
				out, e = digestZipEntries(zr, paths)
				return e
			})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}, nil
}

// zipEntryName 取条目的解码后名字（UTF-8）。
//
// 必须和建树侧（pack.BuildZipTree）用同一个解码口径，否则"树上叫
// `服务配置说明.docx`、对比里叫 `??????.docx`"两处对不上；更麻烦的是
// 按名回查条目（entryFromZip / classInnerFiles）拿的是树上的 Path，
// 和原始字节比会永远不相等，于是预览永远显示"这条不存在"。
func zipEntryName(zf *zip.File) string {
	return util.DecodeZipEntryName(zf.Name)
}

// scanZipMeta 从中央目录读出 路径 -> packMeta，**不解压任何条目**。
//
// 目录条目跳过（zip 规范里名字以 '/' 结尾，Go 的 FileInfo().IsDir()
// 就是判这个）；条目名保持原样不做 "./" 之类的规整 —— 与建树侧
// （pack.BuildZipTree）口径一致，避免"对比说相同、树上路径却不同"。
// "原样"指不做路径规整；编码则统一按 zipEntryName 解码（见其注释）。
func scanZipMeta(zr *zip.Reader) map[string]packMeta {
	out := make(map[string]packMeta, len(zr.File))
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		out[zipEntryName(zf)] = packMeta{
			size:    zf.UncompressedSize64,
			crc:     zf.CRC32,
			modTime: zf.Modified.Format("2006-01-02 15:04:05"),
		}
	}
	return out
}

// digestZipEntries 只对 paths 里列出的条目解压算 md5，其余条目一个字节不读。
//
// 用实际读到的字节数而不是 zf.UncompressedSize64：流式读上来的才是真实内容，
// 元信息里声明的大小不可全信。顺带一提，Go 的 zip reader 在读完时会校验
// CRC，所以这一步也是完整性校验（声明与内容不符会直接报错）。
func digestZipEntries(zr *zip.Reader, paths []string) (map[string]entryDigest, error) {
	out := make(map[string]entryDigest, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	want := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		want[p] = struct{}{}
	}
	for _, zf := range zr.File {
		name := zipEntryName(zf)
		if _, ok := want[name]; !ok {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("读取条目失败: %s: %w", name, err)
		}
		h := md5.New()
		n, err := io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("读取条目失败: %s: %w", name, err)
		}
		out[name] = entryDigest{md5: hex.EncodeToString(h.Sum(nil)), size: n}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 按条目名读内容（「点开看某个文件差在哪」用）
//
// 与上面的全包扫描是两件事：readPackEntries 要把每个条目都读一遍算 md5
// （比对时必须），而"查看某一条的内容"只需要那一条 —— 几百 MB 的胖包上
// 这个差别是几秒 vs 几十毫秒。所以单独给一个按名读的入口。
// ---------------------------------------------------------------------------

// PackEntry 是包内一个文件条目的原始内容。
type PackEntry struct {
	// Bytes 条目内容的原始字节
	Bytes []byte
	// Inners .class 的同目录内部类字节（其余类型为空）。
	//
	// 为什么要把内部类一起带上：jar 里的内部类是**独立条目**
	// （com/foo/Bar$Baz.class），而反编译要把内部类和主类一起喂给
	// fernflower 才能还原内部类的方法体 —— 只喂主类的话内部类是空壳。
	// 这与树预览那边"内部类是主类的子节点"是同一件事
	// （见 gui/content.go 的 classInners）。
	Inners [][]byte
	// Found 条目是否存在（"仅 A 有 / 仅 B 有"的那一侧就是 false）
	Found bool
}

// ReadPackEntry 读出一个包内文件条目的原始字节。
//
// 与 readPackEntries 同一套口径：条目名**原样匹配**（不做 "./" 规整，
// 不做大小写折叠），目录条目不参与（zip 规范里目录名以 '/' 结尾）。
func ReadPackEntry(packPath, entryPath string) (PackEntry, error) {
	var out PackEntry
	err := withPack(packPath, func(zr *zip.Reader) error {
		var e error
		out, e = entryFromZip(zr, entryPath)
		return e
	})
	if err != nil {
		return PackEntry{}, err
	}
	return out, nil
}

// ReadPackEntryBytes 与 ReadPackEntry 同理，来源是一段内存字节（嵌套包用）。
func ReadPackEntryBytes(name string, data []byte, entryPath string) (PackEntry, error) {
	var out PackEntry
	err := withPackBytes(name, data, func(zr *zip.Reader) error {
		var e error
		out, e = entryFromZip(zr, entryPath)
		return e
	})
	if err != nil {
		return PackEntry{}, err
	}
	return out, nil
}

// entryFromZip 从一个**已打开**的 zip 读出一个条目的原始字节（连同同目录内部类）。
func entryFromZip(zr *zip.Reader, entryPath string) (PackEntry, error) {
	out := PackEntry{}
	var main *zip.File
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || zipEntryName(zf) != entryPath {
			continue
		}
		main = zf
		break
	}
	if main == nil {
		return out, nil // 这个包里没有该条目 —— 不是错误（另一侧可能也没有，由调用方判）
	}
	b, err := readZipEntry(main)
	if err != nil {
		return PackEntry{}, err
	}
	out.Bytes = b
	out.Found = true
	for _, zf := range classInnerFiles(zr, entryPath) {
		ib, err := readZipEntry(zf)
		if err != nil {
			return PackEntry{}, err
		}
		out.Inners = append(out.Inners, ib)
	}
	return out, nil
}

// readZipEntry 读出一个条目的全部字节。
func readZipEntry(zf *zip.File) ([]byte, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, fmt.Errorf("读取条目失败: %s: %w", zf.Name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("读取条目失败: %s: %w", zf.Name, err)
	}
	return b, nil
}

// classInnerFiles 找同目录下的内部类条目，按名字排序（顺序稳定，便于断言）。
//
// 判定：同目录、扩展名是 `.class`、主名以 `<主类名>$` 开头。
// 扩展名**大小写敏感**（只认小写 .class），与 content.ParserForExt 一致 ——
// 那边也只认小写，`.CLASS` 走的是"没有解析器"那一支，这里跟着走同一条路。
func classInnerFiles(zr *zip.Reader, entryPath string) []*zip.File {
	dir, base := path.Split(entryPath) // path.Split 返回的 dir 带尾斜杠
	prefix := strings.TrimSuffix(base, ".class") + "$"
	var out []*zip.File
	for _, zf := range zr.File {
		name := zipEntryName(zf)
		if zf.FileInfo().IsDir() || name == entryPath {
			continue
		}
		d, b := path.Split(name)
		if d != dir || !strings.HasSuffix(b, ".class") {
			continue
		}
		if !strings.HasPrefix(strings.TrimSuffix(b, ".class"), prefix) {
			continue
		}
		out = append(out, zf)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lessPath 路径排序：先按小写比较，再按原始串（大小写不同时分出稳定次序）。
func lessPath(x, y string) bool {
	lx, ly := strings.ToLower(x), strings.ToLower(y)
	if lx != ly {
		return lx < ly
	}
	return x < y
}

func sortComparePaths(ps []string) {
	sort.Slice(ps, func(i, j int) bool { return lessPath(ps[i], ps[j]) })
}

// reportText 拼出纯文本报告。分组顺序固定：内容不同、仅 A 有、仅 B 有；
// 每组内按路径排序。数字用原样字节数（可 grep、可 diff）。
func (r *ComparePackResult) reportText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "包内容对比报告\n")
	fmt.Fprintf(&b, "A：%s（%d 个文件）\n", r.PathA, r.TotalA)
	fmt.Fprintf(&b, "B：%s（%d 个文件）\n", r.PathB, r.TotalB)
	fmt.Fprintf(&b, "汇总：内容不同 %d · 仅 A 有 %d · 仅 B 有 %d · 相同 %d\n",
		len(r.Changed), len(r.Removed), len(r.Added), r.Same)

	if len(r.Changed) > 0 {
		fmt.Fprintf(&b, "\n—— 内容不同（%d）——\n", len(r.Changed))
		for i, c := range r.Changed {
			fmt.Fprintf(&b, "[%d] %s\n", i+1, c.Path)
			fmt.Fprintf(&b, "    A：%d 字节  md5 %s（%s）\n", c.A.Size, c.A.Md5, c.A.ModTime)
			fmt.Fprintf(&b, "    B：%d 字节  md5 %s（%s）\n", c.B.Size, c.B.Md5, c.B.ModTime)
		}
	}
	if len(r.Removed) > 0 {
		fmt.Fprintf(&b, "\n—— 仅 A 有（%d）——\n", len(r.Removed))
		for i, e := range r.Removed {
			fmt.Fprintf(&b, "[%d] %s  %d 字节  md5 %s（%s）\n",
				i+1, e.Path, e.Size, e.Md5, e.ModTime)
		}
	}
	if len(r.Added) > 0 {
		fmt.Fprintf(&b, "\n—— 仅 B 有（%d）——\n", len(r.Added))
		for i, e := range r.Added {
			fmt.Fprintf(&b, "[%d] %s  %d 字节  md5 %s（%s）\n",
				i+1, e.Path, e.Size, e.Md5, e.ModTime)
		}
	}
	if len(r.Changed) == 0 && len(r.Removed) == 0 && len(r.Added) == 0 {
		fmt.Fprintf(&b, "\n两个包的文件内容完全一致。\n")
	}
	return b.String()
}

// DiffCount 不同的文件总数（内容不同 + 仅 A 有 + 仅 B 有）。
// 界面汇总与"为空则显示一致"都按这个数走。
func (r *ComparePackResult) DiffCount() int {
	return len(r.Changed) + len(r.Removed) + len(r.Added)
}

// ---------------------------------------------------------------------------
// 单文件对比（"两个普通文件内容差在哪"）
//
// 与上面的包对比是**同一套"只比内容"的口径**，只是对象从"包内条目"换成
// 磁盘上的两个文件：没有条目列表、没有"仅 A 有 / 仅 B 有"，只回答
// "两个文件是否相同"。所以结果形态不同（CompareFileResult），前端走另一条
// 渲染分支；要看具体差在哪，由前端用内容预览浮窗把两个文件并排比
// （见 gui.OpenFileCompare）。
// ---------------------------------------------------------------------------

// CompareFileResult 两个普通文件的比对结果。
type CompareFileResult struct {
	// Kind 结果类型判别字段，固定为 "file"（与 ComparePackResult 的 "pack" 对应）。
	Kind string `json:"kind"`
	// PathA / PathB 两个文件的路径（回显用）
	PathA string `json:"pathA"`
	PathB string `json:"pathB"`
	// NameA / NameB 两个文件的文件名（不含目录）
	NameA string `json:"nameA"`
	NameB string `json:"nameB"`
	// SizeA / SizeB 各自的字节数
	SizeA int64 `json:"sizeA"`
	SizeB int64 `json:"sizeB"`
	// Md5A / Md5B 各自内容的 md5（十六进制小写）
	Md5A string `json:"md5A"`
	Md5B string `json:"md5B"`
	// Same 两文件内容是否一致（md5 相等）
	Same bool `json:"same"`
	// Text 纯文本报告（「拷贝报告」/「保存报告」用，前端不重新拼）
	Text string `json:"text"`
}

// CompareFiles 对比两个普通文件的内容（md5）。
//
// 与 ComparePacks 同样的两道前置检查：空路径挡掉、同一条路径挡掉
// （同路径比出来必然"相同"，不挡会让用户以为选错了文件）。
func CompareFiles(pathA, pathB string) (*CompareFileResult, error) {
	a := strings.TrimSpace(pathA)
	b := strings.TrimSpace(pathB)
	if a == "" || b == "" {
		return nil, fmt.Errorf("请选择两个要比对的文件")
	}
	if a == b {
		return nil, fmt.Errorf("两个文件是同一条路径，没有可比的内容")
	}

	ma, err := fileMeta(a)
	if err != nil {
		return nil, err
	}
	mb, err := fileMeta(b)
	if err != nil {
		return nil, err
	}

	res := &CompareFileResult{
		Kind:  "file",
		PathA: a,
		PathB: b,
		NameA: filepath.Base(a),
		NameB: filepath.Base(b),
		SizeA: ma.Size,
		SizeB: mb.Size,
		Md5A:  ma.Md5,
		Md5B:  mb.Md5,
		Same:  ma.Md5 == mb.Md5,
	}
	res.Text = res.reportText()
	return res, nil
}

// fileMeta 读一个磁盘文件的元信息（字节数 + md5 + 修改时间）。
//
// 目录挡掉：os.Open 能打开目录、io.Copy 却会失败，与其抛底层错误，
// 不如在这里给一句"不接受目录"——前端会原样 toast 出来。
func fileMeta(p string) (CompareEntry, error) {
	f, err := os.Open(p)
	if err != nil {
		return CompareEntry{}, fmt.Errorf("打开文件失败: %s: %w", filepath.Base(p), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return CompareEntry{}, fmt.Errorf("读取文件信息失败: %s: %w", filepath.Base(p), err)
	}
	if fi.IsDir() {
		return CompareEntry{}, fmt.Errorf("不接受目录: %s", filepath.Base(p))
	}
	h := md5.New()
	// 用实际读到的字节数而不是 fi.Size()：流式读上来的才是真实内容
	n, err := io.Copy(h, f)
	if err != nil {
		return CompareEntry{}, fmt.Errorf("读取文件失败: %s: %w", filepath.Base(p), err)
	}
	return CompareEntry{
		Path:    p,
		Size:    n,
		Md5:     hex.EncodeToString(h.Sum(nil)),
		ModTime: fi.ModTime().Format("2006-01-02 15:04:05"),
	}, nil
}

// reportText 拼出单文件对比的纯文本报告。
func (r *CompareFileResult) reportText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "文件对比报告\n")
	fmt.Fprintf(&b, "A：%s（%d 字节）\n", r.PathA, r.SizeA)
	fmt.Fprintf(&b, "B：%s（%d 字节）\n", r.PathB, r.SizeB)
	fmt.Fprintf(&b, "A md5：%s\n", r.Md5A)
	fmt.Fprintf(&b, "B md5：%s\n", r.Md5B)
	if r.Same {
		fmt.Fprintf(&b, "\n两个文件内容完全一致。\n")
	} else {
		fmt.Fprintf(&b, "\n两个文件内容不同（md5 不一致）。\n")
	}
	return b.String()
}
