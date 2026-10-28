// Package util 对应 Java 侧 cn.cpoet.patch.assistant.util。
//
// 只放与 UI 无关的纯工具，UI 相关的（AlterUtil/FXUtil/UIUtil/IConUtil/ImageUtil）
// 在 Go 版里会被目标 GUI 框架替换掉，不在此包移植。
//
// 本项目是 Java 版 PatchAssistant2J 的 Go 移植，原作者 CPoet（Apache-2.0，见 LICENSE）。
package util

import "strings"

// IsEmpty 判断字符串是否为空。
// 对应 Java StringUtil.isEmpty。
func IsEmpty(s string) bool { return len(s) == 0 }

// IsBlank 判断字符串是否为"空白串"。
//
// 保真提示：Java 侧用的是 Character.isSpaceChar，不是 isWhitespace。
// 二者差别很大 —— isSpaceChar 只认 Unicode 的 Zs/Zl/Zp 类空白，
// 因此 '\t' '\n' '\r' '\v' '\f' 都【不算】空白。
// 也就是说 Java 的 isBlank("\r\n") == false，移植时必须照搬，
// 否则 README 解析、路径判空的行为会和原版分叉。
func IsBlank(s string) bool {
	if len(s) == 0 {
		return true
	}
	for _, r := range s {
		if !isSpaceChar(r) {
			return false
		}
	}
	return true
}

// isSpaceChar 对齐 Java Character.isSpaceChar（Unicode 类别 Zs、Zl、Zp）。
func isSpaceChar(r rune) bool {
	switch {
	case r == 0x20, r == 0xA0, r == 0x1680,
		r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F,
		r == 0x3000:
		return true
	}
	return false
}

// LastIndexOfAndCount 返回字符 c 最后一次出现的下标与出现总次数。
//
// 对应 Java StringUtil.lastIndexOfAndCount。注意 Java 版按 char（UTF-16 码元）
// 遍历，这里按 byte 遍历，对 '$' 这类 ASCII 字符结果一致 ——
// 该方法在源码里只用于找 '$'，故安全。
func LastIndexOfAndCount(s string, c byte) (index int, count int) {
	index = -1
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			index = i
			count++
		}
	}
	return index, count
}

// IsEmptySlice 泛型集合判空，对应 Java CollectionUtil.isEmpty(Collection)。
func IsEmptySlice[T any](s []T) bool { return len(s) == 0 }

// IsNotEmptySlice 泛型集合判非空。
func IsNotEmptySlice[T any](s []T) bool { return len(s) > 0 }

// IsEmptyMap 泛型 Map 判空，对应 Java CollectionUtil.isEmpty(Map)。
func IsEmptyMap[K comparable, V any](m map[K]V) bool { return len(m) == 0 }

// IsNotEmptyMap 泛型 Map 判非空。
func IsNotEmptyMap[K comparable, V any](m map[K]V) bool { return len(m) > 0 }

// ToLowerASCII 仅折叠 ASCII 大小写，用于排序键，避免 Unicode 折叠带来的差异。
func ToLowerASCII(s string) string { return strings.ToLower(s) }
