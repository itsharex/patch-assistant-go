// Package i18n 对应 Java 侧 cn.cpoet.patch.assistant.util.I18nUtil
// 以及 src/main/resources/messages/ 下的两个资源包。
//
// 资源文件是【从 exe 二进制中原样提取】的，与仓库源码逐字节一致
// （见工作区根目录的《源码获取与Go移植评估.md》第三节）。用 go:embed
// 打包而不是转成 Go 代码，好处是资源仍是"数据"，将来改文案不用碰代码 ——
// 这也更贴近 Java 的 ResourceBundle 模型。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package i18n

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"cn.cpoet.patch.assistant/internal/constant"
)

//go:embed messages/*.properties
var messageFS embed.FS

// bundle 一种语言的全部文案。
type bundle struct {
	code     string
	messages map[string]string
}

var (
	once     sync.Once
	bundles  map[string]*bundle
	fallback = constant.I18NEnUS.Code
)

func load() {
	bundles = make(map[string]*bundle, len(constant.I18NValues))
	for _, lang := range constant.I18NValues {
		data, err := messageFS.ReadFile("messages/i18n_" + lang.Code + ".properties")
		if err != nil {
			continue
		}
		bundles[lang.Code] = &bundle{code: lang.Code, messages: parseProperties(string(data))}
	}
}

// parseProperties 解析 .properties 文本。
//
// 只实现本项目用到的子集：`key=value` 单行、'#'/'!' 开头的注释、
// 空行忽略、行尾 \r 去掉。Java 的 ResourceBundle 还支持续行符 '\'、
// 转义序列、':' 分隔符 —— 本项目 143 条文案里都没有用到，
// 所以不做，需要时再补（资源文件里有 `\n` 两字符的字面量，
// 那是【文案内容】而非转义，Java 侧同样按字面量输出，故此处也不处理）。
func parseProperties(text string) map[string]string {
	m := make(map[string]string, 160)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i <= 0 {
			continue
		}
		m[line[:i]] = line[i+1:]
	}
	return m
}

// Tr 按当前语言取文案并填充参数。
//
// 对应 Java I18nUtil.tr(key, args...)。行为对齐：
//   - key 缺失时返回 key 本身（Java 的 ResourceBundle 会返回 "!key!"，
//     这里选择更易读的 key，属刻意的差异，已在 MIGRATION.md 登记）；
//   - 参数用 fmt.Sprintf 的 %s 占位填充，与 Java String.format 一致。
//
// langCode 传空串时用默认语言（en-US）。
func Tr(langCode, key string, args ...any) string {
	once.Do(load)
	b := bundles[langCode]
	if b == nil {
		b = bundles[fallback]
	}
	format := key
	if b != nil {
		if v, ok := b.messages[key]; ok {
			format = v
		}
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// Keys 返回指定语言的全部 key（测试用）。
func Keys(langCode string) []string {
	once.Do(load)
	b := bundles[langCode]
	if b == nil {
		return nil
	}
	keys := make([]string, 0, len(b.messages))
	for k := range b.messages {
		keys = append(keys, k)
	}
	return keys
}
