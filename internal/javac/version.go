package javac

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// class 文件头：magic(4) + minor(2) + major(2)。
const classHeaderLen = 8

// ClassVersion 读 class 文件的 major version。
//
// 布局（JVM 规范 §4.1）：前 4 字节是魔数 CAFEBABE，接着 2 字节 minor，
// 再 2 字节 major —— 都是大端。major 就是大家说的"class 版本号"：
// 52 = Java 8、53 = Java 9 …… 61 = Java 17、65 = Java 21。
func ClassVersion(b []byte) (int, error) {
	if len(b) < classHeaderLen {
		return 0, fmt.Errorf("字节数只有 %d，不足一个 class 文件头", len(b))
	}
	if b[0] != 0xCA || b[1] != 0xFE || b[2] != 0xBA || b[3] != 0xBE {
		return 0, errors.New("magic 不是 CAFEBABE，不是 class 文件")
	}
	return int(binary.BigEndian.Uint16(b[6:8])), nil
}

// ReleaseFor 把 class 的 major version 映射成"要编成的目标 Java 版本号"。
//
// 注意这个值**不一定会变成 `--release` 参数**：JDK 8 的 javac 不认 `--release`，
// 那种情况下改用"不传版本参数"（见 javac.go 的 versionArgs）。本函数只负责
// "目标是哪个版本"这层语义。
//
//	45 → 1.1，46 → 2，…，52 → 8，53 → 9，…，61 → 17，65 → 21
//
// 也就是 major - 44。两个例外：
//
//   - **低于 8 一律抬到 8。** Java 7 及更早的类（major ≤ 51）现在基本见不到，
//     而 JDK 21 的 `--release` 最低只认 8（7 在 JDK 20 已被移除）。抬到 8 是
//     安全的：版本更高的 class 在更高的运行时上照样能加载。
//   - **魔数不对时返回 0**，表示"不指定版本，交给 javac 默认"。
//     调用方拿到 0 应当先判 ClassVersion 的错误，别把 0 当合法值用。
func ReleaseFor(major int) int {
	r := major - 44
	if r < 8 {
		return 8
	}
	return r
}

// JavaVersionName 把 major version 说成人话，用于错误提示。
func JavaVersionName(major int) string {
	if major <= 45 {
		return "1.1"
	}
	return fmt.Sprintf("%d", major-44)
}
