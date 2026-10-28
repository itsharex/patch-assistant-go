// Package config 对应 Java 侧 cn.cpoet.patch.assistant.core 里的配置类。
//
// Java 用 Jackson XML 把 Configuration 序列化成 application.xml，
// Go 用 encoding/xml。两者对"嵌套对象"的默认处理一致（都生成子元素），
// 因此配置文件可以互读 —— 这是刻意保持的，方便 Java 版与 Go 版共存过渡。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package config

import (
	"time"

	"cn.cpoet.patch.assistant/internal/constant"
	"cn.cpoet.patch.assistant/internal/util"
)

// DateTimeFormat 日期时间格式，对应 Java DateUtil.DATE_TIME_FORMAT。
const DateTimeFormat = "2006-01-02 15:04:05"

// SearchItem 搜索项。
type SearchItem struct {
	// Path 路径
	Path string `xml:"path,omitempty" json:"path,omitempty"`
	// Name 名称
	Name string `xml:"name,omitempty" json:"name,omitempty"`
}

// SearchHisItem 搜索历史项。
type SearchHisItem struct {
	SearchItem
	// SearchTime 搜索时间（Java 侧格式 yyyy-MM-dd HH:mm:ss，时区 UTC+8）
	SearchTime *time.Time `xml:"searchTime,omitempty" json:"searchTime,omitempty"`
	// Target 搜索目标
	Target string `xml:"target,omitempty" json:"target,omitempty"`
}

// GeneraConf 常规配置。
type GeneraConf struct {
	// Language 界面语言，默认跟随系统
	Language string `xml:"language,omitempty" json:"language,omitempty"`
	// Theme 主题，默认明亮
	Theme string `xml:"theme,omitempty" json:"theme,omitempty"`
}

// NewGeneraConf 返回带默认值的常规配置。
func NewGeneraConf() GeneraConf {
	return GeneraConf{
		Language: util.SysLanguage(),
		Theme:    constant.ThemeLight.Code,
	}
}

// ContentConf 内容查看页配置。
type ContentConf struct {
	// DiffModel 是否默认进入差异（对比）模式，默认关闭
	DiffModel bool `xml:"diffModel" json:"diffModel"`
}

// PatchConf 补丁配置。
type PatchConf struct {
	// Username 操作人员名称，为空时取系统用户名
	Username string `xml:"username,omitempty" json:"username,omitempty"`
	// ReadmeFile 补丁说明文件名
	ReadmeFile string `xml:"readmeFile,omitempty" json:"readmeFile,omitempty"`
	// PathMatch 是否启用全路径匹配
	PathMatch bool `xml:"pathMatch" json:"pathMatch"`
	// FileNameMatch 是否启用文件名匹配
	FileNameMatch bool `xml:"fileNameMatch" json:"fileNameMatch"`
	// PatchFileRegex 参与匹配的补丁文件正则
	PatchFileRegex string `xml:"patchFileRegex,omitempty" json:"patchFileRegex,omitempty"`
	// WritePatchSign 是否写入补丁签名
	WritePatchSign bool `xml:"writePatchSign" json:"writePatchSign"`
	// PatchFileDiff 补丁文件是否默认差异显示
	PatchFileDiff bool `xml:"patchFileDiff" json:"patchFileDiff"`
	// LoadLastPatch 是否自动加载上次的补丁包
	LoadLastPatch bool `xml:"loadLastPatch" json:"loadLastPatch"`
	// SaveToSourceDir 是否默认保存到源目录
	SaveToSourceDir bool `xml:"saveToSourceDir" json:"saveToSourceDir"`
}

// NewPatchConf 返回带默认值的补丁配置。
//
// 默认值逐项对齐 Java PatchConf 的字段初始化，改动前请先对照原文件。
func NewPatchConf() PatchConf {
	return PatchConf{
		ReadmeFile:      constant.ReadmeFile,
		PathMatch:       true,
		FileNameMatch:   true,
		PatchFileRegex:  `(.*\.class)|(.*\.html)|(.*\.js)|(.*\.css)`,
		WritePatchSign:  true,
		PatchFileDiff:   true,
		LoadLastPatch:   true,
		SaveToSourceDir: true,
	}
}

// EffectiveUsername 返回实际使用的操作人员名称。
// 对应 Java PatchConf.getUsername()：配置为空时回落系统用户名。
func (p PatchConf) EffectiveUsername(sysUserName string) string {
	if p.Username == "" {
		return sysUserName
	}
	return p.Username
}

// SearchConf 搜索配置。
type SearchConf struct {
	// HistoryLimit 历史记录上限
	HistoryLimit int `xml:"historyLimit" json:"historyLimit"`
	// History 搜索历史，最新的在最前
	History []SearchHisItem `xml:"history>history,omitempty" json:"history,omitempty"`
}

// NewSearchConf 返回带默认值的搜索配置。
func NewSearchConf() SearchConf { return SearchConf{HistoryLimit: 30} }

// AddHistory 追加一条搜索历史，去重后置顶并裁剪到上限。
//
// 对应 Java SearchConf.addHistory：先按 name 去重，再头插，
// 超出上限时从尾部逐个删除。
func (s *SearchConf) AddHistory(item SearchHisItem) {
	if s.HistoryLimit <= 0 {
		s.HistoryLimit = 30
	}
	kept := s.History[:0:0]
	for _, h := range s.History {
		if h.Name != item.Name {
			kept = append(kept, h)
		}
	}
	s.History = append([]SearchHisItem{item}, kept...)
	if len(s.History) > s.HistoryLimit {
		s.History = s.History[:s.HistoryLimit]
	}
}

// Configuration 应用配置，对应 application.xml 的根元素。
type Configuration struct {
	// 主界面尺寸
	HomeWidth  float64 `xml:"homeWidth,omitempty" json:"homeWidth,omitempty"`
	HomeHeight float64 `xml:"homeHeight,omitempty" json:"homeHeight,omitempty"`
	// 配置窗口尺寸
	ConfigWidth  float64 `xml:"configWidth,omitempty" json:"configWidth,omitempty"`
	ConfigHeight float64 `xml:"configHeight,omitempty" json:"configHeight,omitempty"`
	// 搜索窗口尺寸
	SearchWidth  float64 `xml:"searchWidth,omitempty" json:"searchWidth,omitempty"`
	SearchHeight float64 `xml:"searchHeight,omitempty" json:"searchHeight,omitempty"`
	// 内容查看窗口尺寸
	ContentWidth  float64 `xml:"contentWidth,omitempty" json:"contentWidth,omitempty"`
	ContentHeight float64 `xml:"contentHeight,omitempty" json:"contentHeight,omitempty"`
	// 绑定信息窗口尺寸
	NodeMappedWidth  float64 `xml:"nodeMappedWidth,omitempty" json:"nodeMappedWidth,omitempty"`
	NodeMappedHeight float64 `xml:"nodeMappedHeight,omitempty" json:"nodeMappedHeight,omitempty"`
	// 说明信息窗口尺寸
	ReadmeWidth  float64 `xml:"readmeWidth,omitempty" json:"readmeWidth,omitempty"`
	ReadmeHeight float64 `xml:"readmeHeight,omitempty" json:"readmeHeight,omitempty"`
	// 补丁签名窗口尺寸
	PatchSignWidth float64 `xml:"patchSignWidth,omitempty" json:"patchSignWidth,omitempty"`
	// 保真提示：Java 字段名是 patchSighHeight（少了个 n），
	// XML 元素名沿用原拼写，避免读旧配置时丢值。
	PatchSighHeight float64 `xml:"patchSighHeight,omitempty" json:"patchSighHeight,omitempty"`

	// 界面选项
	IsSelectedLinked bool `xml:"isSelectedLinked,omitempty" json:"isSelectedLinked,omitempty"`
	IsOnlyChanges    bool `xml:"isOnlyChanges,omitempty" json:"isOnlyChanges,omitempty"`
	IsShowFileDetail bool `xml:"isShowFileDetail,omitempty" json:"isShowFileDetail,omitempty"`
	ShowPatchInfo    bool `xml:"showPatchInfo,omitempty" json:"showPatchInfo,omitempty"`
	FocusTreeStatus  int  `xml:"focusTreeStatus,omitempty" json:"focusTreeStatus,omitempty"`

	// 最近使用的路径
	LastAppPackPath      string `xml:"lastAppPackPath,omitempty" json:"lastAppPackPath,omitempty"`
	LastPatchPackPath    string `xml:"lastPatchPackPath,omitempty" json:"lastPatchPackPath,omitempty"`
	LastSavePackPath     string `xml:"lastSavePackPath,omitempty" json:"lastSavePackPath,omitempty"`
	LastJarDecompilePath string `xml:"lastJarDecompilePath,omitempty" json:"lastJarDecompilePath,omitempty"`

	// 子配置
	Genera  GeneraConf  `xml:"genera" json:"genera"`
	Content ContentConf `xml:"content" json:"content"`
	Search  SearchConf  `xml:"search" json:"search"`
	Patch   PatchConf   `xml:"patch" json:"patch"`
}

// New 返回带默认值的配置。
// 对应 Java new Configuration() —— Java 在字段上直接给了初值。
func New() *Configuration {
	return &Configuration{
		HomeWidth: 720, HomeHeight: 600,
		ConfigWidth: 720, ConfigHeight: 560,
		SearchWidth: 720, SearchHeight: 300,
		NodeMappedWidth: 720, NodeMappedHeight: 560,
		ReadmeWidth: 720, ReadmeHeight: 560,
		PatchSignWidth: 680, PatchSighHeight: 280,
		Genera:  NewGeneraConf(),
		Content: ContentConf{DiffModel: false},
		Search:  NewSearchConf(),
		Patch:   NewPatchConf(),
	}
}
