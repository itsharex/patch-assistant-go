// Package tree 对应 Java 侧 cn.cpoet.patch.assistant.control.tree.node。
//
// Java 里 TreeNode 是抽象类，派生出 CompressNode / FileNode / FileDirectNode /
// VirtualNode / MappedNode。这些子类的差异只有两点：是否目录、内容从哪来。
// Go 里用单一 struct + 可插拔的 dataSource 表达，避免为两个差异造五个类型。
//
// 刻意不移植的部分：treeItem 字段（javafx.scene.control.TreeItem）属于 UI 层，
// 由目标 GUI 框架自行维护。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package tree

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
)

// SizeUnknown 表示内容大小尚未计算，对齐 Java 里 size 初始值 -1。
const SizeUnknown int64 = -1

// DataSource 抽象节点内容的来源。
//
// CompressNode 的实现从内存里的 zip 数据读，FileNode 从磁盘读 ——
// 对应 Java 里各子类覆写的 consumeBytes/consumeInputStream。
type DataSource interface {
	// Open 返回内容读取器，调用方负责 Close。
	Open() (io.ReadCloser, error)
}

// FileSource 把磁盘文件作为节点内容来源。
//
// 对应 Java FileNode 的 `file` 字段（读取走 FileInputStream）。
// 补丁目录模式下的节点、以及"写出到临时文件后再挂回节点"的场景都用它。
type FileSource struct {
	// Path 磁盘文件路径
	Path string
}

// Open 打开文件。每次调用新开句柄，与 Java FileNode.consumeInputStream 一致。
func (f *FileSource) Open() (io.ReadCloser, error) { return os.Open(f.Path) }

// FilePath 返回磁盘文件路径，供 Node.FilePath 通过接口识别。
func (f *FileSource) FilePath() string { return f.Path }

// MemorySource 把一段内存字节作为节点内容来源。
//
// 这是"编辑包内文件内容"的落点：用户改完内容后把节点的 source 换成它，
// 之后 ConsumeBytes（以及依赖它的 md5/size 计算、写包路径）就自动读到新内容，
// 不需要在写出侧加任何分支。见 Node.SetContent。
//
// 刻意不支持 Write：内容一旦替换就是不可变的，改内容走 SetContent 换一个新实例，
// 免得出现"读了一半被别人改了"这种时序问题。
type MemorySource struct {
	// Data 内容字节。构造后不要再改它。
	Data []byte
}

// Open 每次返回一个从头开始的新读取器。
//
// 必须是"新读取器"而不是共享一个：md5 计算与写包会各读一遍。
func (m *MemorySource) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.Data)), nil
}

// Kind 节点种类，对应 Java 侧 TreeNode 的五个具体子类。
//
// Java 用继承表达"内容从哪来"和"是不是真实文件"，Go 用单一 struct + 一个
// 枚举字段。判断节点种类时用 Kind 而不是类型断言。
type Kind int

const (
	// KindFile 对应 FileNode：内容来自磁盘文件。
	KindFile Kind = iota
	// KindFileDirect 对应 FileDirectNode：用户直接拖进来的文件，非补丁树成员。
	// 行为与 KindFile 完全一致，单独列出只为对齐 Java 的类型体系。
	KindFileDirect
	// KindCompress 对应 CompressNode：内容来自压缩包内的条目。
	KindCompress
	// KindVirtual 对应 VirtualNode：为容纳补丁内容而临时虚拟出来的目录。
	KindVirtual
	// KindMapped 对应 MappedNode：应用侧本不存在的文件，虚拟绑定到补丁节点。
	KindMapped
)

// KindNames 调试用名称。
var KindNames = [...]string{"FileNode", "FileDirectNode", "CompressNode", "VirtualNode", "MappedNode"}

func (k Kind) String() string {
	if k < 0 || int(k) >= len(KindNames) {
		return "UnknownNode"
	}
	return KindNames[k]
}

// Node 树节点。
type Node struct {
	// Name 节点名称（含后缀）
	Name string
	// Path 节点在包内的完整路径，统一以 '/' 分隔
	Path string
	// RawPath 条目名在 zip 里的**原始字节**（以 string 承载，可能不是合法 UTF-8）。
	//
	// 只有「Path 是从非 UTF-8 的条目名解码而来」时才非空 —— 典型是中文 Windows
	// 上打包工具写的 GBK 名字（没有 0x800 标志位）。Path 存解码后的 UTF-8 供
	// 界面显示与匹配，RawPath 留原始字节供重打包时原样写回：不这么做的话，
	// 一次「改个文件再打补丁」就会把包里那条名字从 GBK 变成 UTF-8，
	// 下游仍按 GBK 解名的工具立刻看到乱码。
	//
	// 新建 / 用户拖进来的节点恒为空（它没有"原始字节"这回事）。
	RawPath string
	// IsPatch 是否属于补丁树
	IsPatch bool
	// Parent 父节点
	Parent *Node
	// Children 子节点，保持"目录在前、同级按名不区分大小写"的排序
	Children []*Node
	// MappedNode 匹配到的对端节点（应用↔补丁）
	MappedNode *Node
	// Md5 内容 md5，惰性计算
	Md5 string
	// Size 内容大小，SizeUnknown 表示未计算
	Size int64
	// Expanded 展开状态，用于重载后保留 UI 状态
	Expanded bool
	// Type 节点在补丁流程中的状态
	Type constant.TreeNodeType

	// WholeReplace 用户标记的"整体替换项"。
	//
	// 标记后这个节点被当成一个**原子文件**：它的子级不再逐个按名字去应用树
	// 里找，而是它自己整体替换应用树里的某一项。补丁包写出时也原样写字节、
	// 不重打包（`writePatchPackWithZip` 对压缩包节点本来就不递归）。
	//
	// 为什么需要这个标记：交付方常把"几个服务各自的完整包"塞进一个外层 zip
	// （`20261029_account-server_xxx.zip` 这种），而内层包的 README 在第二层，
	// `RefreshReadmeNode` 只扫根下、扫不到 —— 逐文件匹配必然乱。这种情况下
	// 用户真正要的语义就是"整个包换掉"，不是"把里面的 class 挑出来"。
	//
	// 刻意不复用 Type：Type 表达的是补丁变更类型（ADD/MOD/DEL），而"整体替换"
	// 是匹配/写出方式的开关，两者正交 —— 同一个节点既可能按名字命中 MOD，
	// 也可能作为新增项整体放进去。
	WholeReplace bool

	// Kind 节点种类
	Kind Kind
	// Comment 压缩条目的注释，重打包时要原样写回
	Comment string
	// Crc 压缩条目的 CRC32，-1 表示未知。对齐 Java CompressNode.crc 初值。
	Crc int64
	// Extra 压缩条目的扩展字段，重打包时要原样写回
	Extra []byte

	// dir 是否目录
	dir bool
	// createTime / accessTime / modifyTime 时间戳
	createTime time.Time
	accessTime time.Time
	modifyTime time.Time
	// source 内容来源，目录为 nil
	source DataSource
	// edited 内容是否被用户在本会话里改过（见 SetContent）。
	//
	// 刻意**不复用** Type（TreeNodeType）来表达这件事：Type 是补丁匹配的语义
	// （ADD/MOD/DEL…），改内容跟"这个文件在补丁里的角色"是两回事，混用会让
	// 匹配结果被编辑操作污染。
	edited bool
	// zipOffset 记录该条目在压缩包中的位置，供 zip 随机读优化
	zipOffset int64
}

// NewDir 创建目录节点。
func NewDir(name, path string, isPatch bool) *Node {
	now := time.Now()
	return &Node{
		Name: name, Path: path, IsPatch: isPatch,
		dir: true, Size: SizeUnknown, Crc: -1,
		createTime: now, accessTime: now, modifyTime: now,
	}
}

// NewFile 创建文件节点。
func NewFile(name, path string, size int64, modifyTime time.Time, src DataSource, isPatch bool) *Node {
	return &Node{
		Name: name, Path: path, IsPatch: isPatch,
		dir: false, Size: size, modifyTime: modifyTime,
		createTime: modifyTime, accessTime: modifyTime,
		source: src, Crc: -1,
	}
}

// NewCompressDir 创建压缩包内的目录条目节点。
// 对应 Java CompressNodeZipFactory 里 entry.isDirectory() 的分支。
func NewCompressDir(name, path string, createTime, modifyTime, accessTime time.Time, isPatch bool) *Node {
	return &Node{
		Name: name, Path: path, IsPatch: isPatch, Kind: KindCompress,
		dir: true, Size: SizeUnknown, Crc: -1,
		createTime: createTime, accessTime: accessTime, modifyTime: modifyTime,
	}
}

// NewCompressFile 创建压缩包内的文件条目节点。
func NewCompressFile(name, path string, size, crc int64, createTime, modifyTime, accessTime time.Time, src DataSource, isPatch bool) *Node {
	return &Node{
		Name: name, Path: path, IsPatch: isPatch, Kind: KindCompress,
		dir: false, Size: size, Crc: crc,
		createTime: createTime, accessTime: accessTime, modifyTime: modifyTime,
		source: src,
	}
}

// NewBareFile 创建一个"裸"文件节点：没有内容来源、大小未知。
//
// 对应 Java 里 `new FileNode()` / `new CompressNode()` —— 两者都是先建空壳，
// 随后逐个 set 字段。当前用途是承载一个即将落盘的说明文件（README）：
// 先建节点、再把临时文件挂上去，最后写进补丁包。
//
// kind 传 KindCompress 对应 CompressNode，传 KindFile 对应 FileNode。
func NewBareFile(kind Kind) *Node {
	return &Node{Kind: kind, Crc: -1, Size: SizeUnknown}
}

// NewVirtualDir 创建虚拟目录节点。
//
// 对应 Java VirtualNode：补丁 README 指向的中间层目录在应用包里并不存在时，
// 用它来"占位"，好让新文件有地方挂。
func NewVirtualDir(name string, modifyTime time.Time) *Node {
	return &Node{
		Name: name, Kind: KindVirtual,
		dir: true, Size: SizeUnknown, Crc: -1,
		createTime: modifyTime, accessTime: modifyTime, modifyTime: modifyTime,
	}
}

// NewMappedNode 创建虚拟绑定节点，绑定到补丁侧节点 m。
//
// 对应 Java `new MappedNode(m)`。Java 的 MappedNode.setMappedNode 会立刻把
// m 的 name/md5/size 复制过来（其中 md5/size 走 getXxxOrInit，会触发一次读取），
// 这里保持一致。
func NewMappedNode(m *Node) *Node {
	n := &Node{Kind: KindMapped, Crc: -1, Size: SizeUnknown}
	n.SetMappedNode(m)
	return n
}

// Dir 是否目录。对应 Java TreeNode.isDir()。
func (n *Node) Dir() bool { return n.dir }

// SetDir 设置是否目录。
func (n *Node) SetDir(v bool) { n.dir = v }

// ModifyTime 最后修改时间。对应 Java TreeNode.getModifyTime()。
//
// 保真点（别漏）：Java 的 MappedNode **覆写**了 getModifyTime()，直接返回被绑定
// 节点的时间（见 MappedNode.java）。Go 没有多态，所以在基类方法里按 KindMapped
// 显式转发 —— 否则"补丁里有、应用里没有"的文件（映射节点）自己那三个时间字段
// 全是零值，界面"修改时间"那一列就是空的。Java 只覆写了 getModifyTime，
// create/access 两个时间没有覆写，所以这里也只转发 modify。
func (n *Node) ModifyTime() time.Time {
	if n.Kind == KindMapped && n.MappedNode != nil {
		return n.MappedNode.ModifyTime()
	}
	return n.modifyTime
}

// CreateTime 创建时间。
func (n *Node) CreateTime() time.Time { return n.createTime }

// AccessTime 访问时间。
func (n *Node) AccessTime() time.Time { return n.accessTime }

// SetModifyTime 设置修改时间。
func (n *Node) SetModifyTime(t time.Time) { n.modifyTime = t }

// SetCreateTime 设置创建时间。
func (n *Node) SetCreateTime(t time.Time) { n.createTime = t }

// SetAccessTime 设置访问时间。
func (n *Node) SetAccessTime(t time.Time) { n.accessTime = t }

// SetMd5 直接写入 md5，跳过内容读取。
func (n *Node) SetMd5(v string) { n.Md5 = v }

// SetSize 直接写入大小。
func (n *Node) SetSize(v int64) { n.Size = v }

// SetType 设置节点状态。
func (n *Node) SetType(t constant.TreeNodeType) { n.Type = t }

// SetSource 设置内容来源。
func (n *Node) SetSource(src DataSource) { n.source = src }

// Source 返回内容来源，目录节点为 nil。
func (n *Node) Source() DataSource { return n.source }

// SetContent 用一段内存字节替换节点内容，并把节点标记为"已修改"。
//
// 这是"编辑包内文件内容"的核心：只换 source，不动任何其它字段。
// 之所以够用，是因为读内容的地方全都走 ConsumeBytes（写包时是
// service/writer.go 与 service/patchsave.go），换掉 source 它们就自动读到新内容。
//
// 必须清掉 Md5 与 Size：两个字段都是"读一遍时顺手填"的缓存
// （GetMd5OrInit / GetSizeOrInit 见旧值就直接返回），不清的话会带着旧值
// 流进 patch-up sign —— DelInfo.AppMd5 / AddInfo.PatchMd5 / ModInfo.*Md5
// 以及 OriginAppSize 全是"读一遍记下来"的元数据（service/writer.go），
// 下游按 sign 校验时就会对不上。
//
// 注意别把后果说大：写出侧**不会**因此写坏 zip。service/writer.go 的
// writeNodeEntry 对 STORED 条目是从实际数据算 crc/大小（writeRawEntry 前
// 重算），对 DEFLATE 条目走数据描述符，都不读 n.Size。所以这条 bug 的表
// 现是"sign 里的校验值错了"，不是"包坏了"。
//
// 数据会复制一份，避免调用方之后复用同一个切片把内容改掉。
func (n *Node) SetContent(data []byte) {
	n.source = &MemorySource{Data: append([]byte(nil), data...)}
	n.Md5 = ""
	n.Size = SizeUnknown
	n.edited = true
}

// Edited 返回内容是否被本会话改过。
//
// 界面用它给节点加"已修改"标记；重新加载/重新匹配会重建节点，
// 标记自然消失（改过的内容也随之丢弃）。
func (n *Node) Edited() bool { return n.edited }

// SetEdited 直接设置"已修改"标记，供重新加载后恢复状态等场景使用。
func (n *Node) SetEdited(v bool) { n.edited = v }

// ContentBytes 一次性读出节点全部内容。
//
// 与 ConsumeBytes 的关系：后者是流式回调（大文件友好），本方法是"全读进内存"
// 的便利包装。编辑/另存为这类场景需要完整字节，用它更省事。
func (n *Node) ContentBytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := n.ConsumeBytes(func(b []byte) error {
		_, err := buf.Write(b)
		return err
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SetFileSource 把节点内容来源换成磁盘文件。
// 对应 Java FileNode.setFile(File)。
func (n *Node) SetFileSource(path string) { n.source = &FileSource{Path: path} }

// FilePath 返回节点内容对应的【磁盘】文件路径，非文件来源返回空串。
//
// 对应 Java FileNode.getFile()，但有一处必须留意的差异：Java 的 FileNode
// 必定有 file（压缩包条目的内容是建树时落盘的临时文件），而 Go 里压缩包
// 条目走 zipSource 惰性读取，没有磁盘路径，于是返回空串。
//
// 需要判断"这个节点的内容在磁盘上还是压缩包里"时用本方法，
// 别误用 Path —— 那是包内路径（如 "BOOT-INF/classes/Foo.class"）。
func (n *Node) FilePath() string {
	if n == nil || n.source == nil {
		return ""
	}
	if fs, ok := n.source.(interface{ FilePath() string }); ok {
		return fs.FilePath()
	}
	return ""
}

// SetZipOffset 记录条目在压缩包内的偏移，便于后续随机读。
func (n *Node) SetZipOffset(off int64) { n.zipOffset = off }

// ZipOffset 返回条目在压缩包内的偏移。
func (n *Node) ZipOffset() int64 { return n.zipOffset }

// SetMappedNode 绑定对端节点。
//
// 对应 Java TreeNode.setMappedNode —— 注意 MappedNode 覆写了它：对一个
// KindMapped 节点调用时，会把源节点的名称/md5/大小一起复制过来。
// 其余种类只写字段，与 Java 基类行为一致。
func (n *Node) SetMappedNode(m *Node) {
	n.MappedNode = m
	if n.Kind != KindMapped || m == nil {
		return
	}
	n.Name = m.Name
	n.Md5 = m.GetMd5OrInit()
	n.Size = m.GetSizeOrInit()
}

// Clone 浅拷贝节点，用于"操作失败时回滚"的快照。
//
// 对应 Java TreeNode.clone()（Object.clone 的浅拷贝语义）：Children 切片头
// 被复制但底层数组共享，Parent/MappedNode 仍指向原对象。
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	return &c
}

// GetOrInitChildren 返回子节点切片，必要时初始化。
// 对应 Java TreeNode.getAndInitChildren。
func (n *Node) GetOrInitChildren() []*Node {
	if n.Children == nil {
		n.Children = make([]*Node, 0, 4)
	}
	return n.Children
}

// AddChild 把子节点【追加】到末尾，并回填 parent。
//
// 保真提示（重要，容易踩）：Java 的 getAndInitChildren 返回 SortLinkedList，
// 名字看着像"插入时排序"，但它的 add 被覆写成了纯追加：
//
//	@Override
//	public boolean add(T t) {
//	    // addAndIndex(t);          ← 排序插入的逻辑被注释掉了
//	    // return true;
//	    return super.add(t);
//	}
//
// 也就是说构造时传进去的那个比较器【从未被调用】，同级节点就是插入顺序。
// 这直接决定了重打包时 zip 条目的先后：Java 写出的顺序 = 建树时的插入顺序。
//
// 曾经这里实现成"按名排序插入"，与 Java 输出不一致（黄金基准比对时暴露）。
// 别改回排序 —— 真要排序，得先确认上游把 addAndIndex 打开了。
func (n *Node) AddChild(child *Node) {
	child.Parent = n
	n.Children = append(n.GetOrInitChildren(), child)
}

// RemoveChild 从子节点中摘除指定节点（不清理被摘节点的 parent）。
// 对应 Java 里 parent.getChildren().remove(node)。
func (n *Node) RemoveChild(child *Node) {
	for i, c := range n.Children {
		if c == child {
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			return
		}
	}
}

// ConsumeBytes 读取节点内容，逐块回调，顺带计算 md5 与大小。
//
// 对应 Java TreeNode.consumeBytes(InputBufConsumer)。Java 版用回调而不是
// 返回 []byte，是为了支持大文件流式处理；Go 里保留这个形态。
// 目录节点直接返回。
//
// 读完会填充 Md5 与 Size —— 对应 Java 版在 CompressNodeFactory 建树阶段
// 就把 md5 算好的效果，只是这里推迟到真正需要时才做。
func (n *Node) ConsumeBytes(fn func(buf []byte) error) error {
	if n.dir || n.source == nil {
		return nil
	}
	rc, err := n.source.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	digest := md5.New()
	buf := make([]byte, 32*1024)
	var total int64
	for {
		read, err := rc.Read(buf)
		if read > 0 {
			total += int64(read)
			digest.Write(buf[:read])
			if cbErr := fn(buf[:read]); cbErr != nil {
				return cbErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	n.Size = total
	n.Md5 = hex.EncodeToString(digest.Sum(nil))
	return nil
}

// GetSizeOrInit 返回大小，未计算则触发一次读取。
// 对应 Java TreeNode.getSizeOrInit。
func (n *Node) GetSizeOrInit() int64 {
	if n.Size != SizeUnknown {
		return n.Size
	}
	_ = n.ConsumeBytes(func([]byte) error { return nil })
	return n.Size
}

// GetMd5OrInit 返回 md5，未计算则触发一次读取。
// 对应 Java TreeNode.getMd5OrInit。
func (n *Node) GetMd5OrInit() string {
	if n.Md5 != "" {
		return n.Md5
	}
	_ = n.ConsumeBytes(func([]byte) error { return nil })
	return n.Md5
}

// FindChildByName 按名称查找直接子节点，未找到返回 nil。
func (n *Node) FindChildByName(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}
