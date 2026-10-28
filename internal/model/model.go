// Package model 对应 Java 侧 cn.cpoet.patch.assistant.model。
//
// Java 里这些是带 Jackson 注解的 POJO，Go 用 struct + tag 表达。
// 注意两处保真点：
//  1. Jackson 的 @JsonInclude(NON_EMPTY) 在 Go 里用 omitempty 近似；
//  2. PatchUpSign 继承 PatchSign，Go 用匿名嵌入表达，字段展开后与
//     Java 序列化结果一致。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package model

import (
	"encoding/json"
	"time"
)

// javaZone 是 Java 侧 @JsonFormat(timezone = "GMT+8") 对应的固定时区。
var javaZone = time.FixedZone("GMT+8", 8*60*60)

// JavaDate 按 Java 侧约定的格式序列化的时间。
//
// 保真提示：Java 的 PatchUpSign.operTime 是 java.util.Date（一个瞬时点），
// 注解 `@JsonFormat(pattern = "yyyy-MM-dd HH:mm:ss", timezone = "GMT+8")`
// 决定渲染方式 —— 所以无论 JVM 默认时区是什么，写出的签名里恒为 GMT+8 的
// "yyyy-MM-dd HH:mm:ss"。Go 的 time.Time 自带 MarshalJSON 会写成 RFC3339
// （带 T、带时区、带纳秒），与 Java 不一致，故这里自定义。
type JavaDate struct {
	time.Time
}

// NewJavaDate 包装一个时间点。
func NewJavaDate(t time.Time) *JavaDate { return &JavaDate{Time: t} }

// MarshalJSON 按 "yyyy-MM-dd HH:mm:ss" 渲染，时区固定 GMT+8。
func (d JavaDate) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.In(javaZone).Format("2006-01-02 15:04:05"))
}

// UnmarshalJSON 解析 Java 版写出的时间串，也兼容 RFC3339。
func (d *JavaDate) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano} {
		if t, err := time.ParseInLocation(layout, s, javaZone); err == nil {
			d.Time = t
			return nil
		}
	}
	// 解析不了就留零值，不阻断反序列化
	d.Time = time.Time{}
	return nil
}

// PatchSign 补丁签名。
type PatchSign struct {
	// Code 补丁编码
	Code string `json:"code,omitempty" xml:"code,omitempty"`
	// Name 补丁名称
	Name string `json:"name,omitempty" xml:"name,omitempty"`
	// Md5 补丁 md5 值
	Md5 string `json:"md5,omitempty" xml:"md5,omitempty"`
	// Sha1 补丁 sha1 值
	Sha1 string `json:"sha1,omitempty" xml:"sha1,omitempty"`
	// Path 补丁包所在路径
	Path string `json:"path,omitempty" xml:"path,omitempty"`
	// Readme 补丁 readme 文件内容
	Readme string `json:"readme,omitempty" xml:"readme,omitempty"`
}

// PatchUpSign 打补丁签名信息。
type PatchUpSign struct {
	PatchSign

	// ModTotal 修改统计
	ModTotal *int `json:"modTotal,omitempty" xml:"modTotal,omitempty"`
	// DelTotal 删除统计
	DelTotal *int `json:"delTotal,omitempty" xml:"delTotal,omitempty"`
	// AddTotal 增加统计
	AddTotal *int `json:"addTotal,omitempty" xml:"addTotal,omitempty"`
	// ManualDelTotal 手动删除统计
	ManualDelTotal *int `json:"manualDelTotal,omitempty" xml:"manualDelTotal,omitempty"`
	// OriginAppMd5 源应用包 md5 值
	OriginAppMd5 string `json:"originAppMd5,omitempty" xml:"originAppMd5,omitempty"`
	// OriginAppSha1 源应用包 sha1 值
	OriginAppSha1 string `json:"originAppSha1,omitempty" xml:"originAppSha1,omitempty"`
	// OriginAppSize 源应用包大小
	OriginAppSize *int64 `json:"originAppSize,omitempty" xml:"originAppSize,omitempty"`
	// OriginAppPath 源应用包路径
	OriginAppPath string `json:"originAppPath,omitempty" xml:"originAppPath,omitempty"`
	// OperTime 打补丁的时间（Java 侧格式 yyyy-MM-dd HH:mm:ss，时区 GMT+8）
	OperTime *JavaDate `json:"operTime,omitempty" xml:"operTime,omitempty"`
	// OperUser 打补丁的操作人员
	OperUser string `json:"operUser,omitempty" xml:"operUser,omitempty"`
	// Signs 内部补丁签名
	Signs []PatchSign `json:"signs,omitempty" xml:"signs>sign,omitempty"`
	// DelInfos 删除列表
	DelInfos []DelInfo `json:"delInfos,omitempty" xml:"delInfos>delInfo,omitempty"`
	// AddInfos 新增列表
	AddInfos []AddInfo `json:"addInfos,omitempty" xml:"addInfos>addInfo,omitempty"`
	// ModInfos 更新列表
	ModInfos []ModInfo `json:"modInfos,omitempty" xml:"modInfos>modInfo,omitempty"`
}

// NewPatchUpSignOf 由基础签名构造升级签名。
// 对应 Java PatchUpSign.of(PatchSign)。
func NewPatchUpSignOf(s PatchSign) *PatchUpSign {
	return &PatchUpSign{PatchSign: s}
}

// AppPackSign 应用包信息。
type AppPackSign struct {
	// Md5 应用包 md5 值
	Md5 string `json:"md5,omitempty" xml:"md5,omitempty"`
	// Sha1 应用包 sha1 值
	Sha1 string `json:"sha1,omitempty" xml:"sha1,omitempty"`
	// Path 应用包所在路径
	Path string `json:"path,omitempty" xml:"path,omitempty"`
}

// AddInfo 新增文件信息。
type AddInfo struct {
	// AppPath 应用文件路径
	AppPath string `json:"appPath,omitempty" xml:"appPath,omitempty"`
	// PatchPath 补丁文件路径
	PatchPath string `json:"patchPath,omitempty" xml:"patchPath,omitempty"`
	// PatchMd5 补丁文件 md5 值
	PatchMd5 string `json:"patchMd5,omitempty" xml:"patchMd5,omitempty"`
	// PatchCreateTime 文件创建时间
	PatchCreateTime string `json:"patchCreateTime,omitempty" xml:"patchCreateTime,omitempty"`
}

// DelInfo 删除文件信息。
type DelInfo struct {
	// AppPath 应用文件路径
	AppPath string `json:"appPath,omitempty" xml:"appPath,omitempty"`
	// AppMd5 应用文件 md5 值
	AppMd5 string `json:"appMd5,omitempty" xml:"appMd5,omitempty"`
	// AppCreateTime 文件创建时间
	AppCreateTime string `json:"appCreateTime,omitempty" xml:"appCreateTime,omitempty"`
}

// ModInfo 修改文件信息。
type ModInfo struct {
	// AppPath 应用文件路径
	AppPath string `json:"appPath,omitempty" xml:"appPath,omitempty"`
	// PatchPath 补丁文件路径
	PatchPath string `json:"patchPath,omitempty" xml:"patchPath,omitempty"`
	// AppMd5 应用文件 md5 值
	AppMd5 string `json:"appMd5,omitempty" xml:"appMd5,omitempty"`
	// PatchMd5 补丁文件 md5 值
	PatchMd5 string `json:"patchMd5,omitempty" xml:"patchMd5,omitempty"`
	// AppCreateTime 应用文件创建时间
	AppCreateTime string `json:"appCreateTime,omitempty" xml:"appCreateTime,omitempty"`
	// PatchCreateTime 补丁文件创建时间
	PatchCreateTime string `json:"patchCreateTime,omitempty" xml:"patchCreateTime,omitempty"`
}

// HashInfo 内容哈希信息。
type HashInfo struct {
	// Length 内容长度
	Length int `json:"length" xml:"length"`
	// Md5 md5 值
	Md5 string `json:"md5,omitempty" xml:"md5,omitempty"`
	// Sha1 sha1 值
	Sha1 string `json:"sha1,omitempty" xml:"sha1,omitempty"`
}

// Application 应用信息，对应根元素 <Application>。
//
// 保真提示：Java 侧字段名是 groupId，但 resources/PatchAssistant2J.xml 模板里
// 写成了 <grouopId>，而 XmlMapper 关掉了 FAIL_ON_UNKNOWN_PROPERTIES，
// 于是这个字段永远读不到值（About 页的"组织"一栏实际是空的）。
// 这里保持同样的 tag 行为，不擅自"修正"，以免和 Java 版输出不一致。
type Application struct {
	// Name 应用名称
	Name string `xml:"name,omitempty" json:"name,omitempty"`
	// Version 应用版本
	Version string `xml:"version,omitempty" json:"version,omitempty"`
	// GroupID 组织（见上方保真提示）
	GroupID string `xml:"grouopId,omitempty" json:"groupId,omitempty"`
	// ArtifactID 项目名称
	ArtifactID string `xml:"artifactId,omitempty" json:"artifactId,omitempty"`
	// UserName 编译用户名称
	UserName string `xml:"userName,omitempty" json:"userName,omitempty"`
	// BuildTime 编译时间
	BuildTime string `xml:"buildTime,omitempty" json:"buildTime,omitempty"`
}
