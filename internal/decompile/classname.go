package decompile

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MainClassName 从 class 字节里读出这个类自身的名字（形如 `cn/cpoet/Foo`）。
//
// 为什么要自己解析：fernflower 输出文件的文件名取自**字节里的类名**，
// 而我们交给它的临时文件名是随便起的（`c0.class`）。不先把类名读出来，
// 就不知道该去输出目录里读回哪个 `.java`。
//
// 只解析到 this_class 为止，不做完整校验 —— 常量池里的字符串、方法体一概不碰。
// 这也是为什么这个函数能保持很短：class 文件格式里 `this_class` 的位置在
// 常量池之后固定偏移两处（access_flags、this_class）。
func MainClassName(b []byte) (string, error) {
	if len(b) < 10 {
		return "", errors.New("文件太短，不是有效的 class")
	}
	if b[0] != 0xCA || b[1] != 0xFE || b[2] != 0xBA || b[3] != 0xBE {
		return "", errors.New("magic 不是 CAFEBABE，不是 class 文件")
	}

	pos := 8 // 跳过 magic(4) + minor(2) + major(2)
	cpCount := int(binary.BigEndian.Uint16(b[pos:]))
	pos += 2

	// utf8[i] 是索引 i 处的 Utf8 字符串；classIdx[i] 是索引 i 处 CONSTANT_Class
	// 指向的 name_index。别的常量类型只需要知道它们占多少字节，好把游标推过去。
	utf8 := make([]string, cpCount)
	classIdx := make([]int, cpCount)

	for i := 1; i < cpCount; i++ {
		if pos >= len(b) {
			return "", errors.New("常量池被截断")
		}
		tag := b[pos]
		pos++

		switch tag {
		case 1: // CONSTANT_Utf8
			if pos+2 > len(b) {
				return "", errors.New("Utf8 常量被截断")
			}
			n := int(binary.BigEndian.Uint16(b[pos:]))
			pos += 2
			if pos+n > len(b) {
				return "", errors.New("Utf8 常量长度越界")
			}
			utf8[i] = string(b[pos : pos+n])
			pos += n

		case 7: // CONSTANT_Class
			if pos+2 > len(b) {
				return "", errors.New("Class 常量被截断")
			}
			classIdx[i] = int(binary.BigEndian.Uint16(b[pos:]))
			pos += 2

		case 3, 4: // Integer, Float
			pos += 4
		case 5, 6: // Long, Double —— 占**两个**常量池槽位
			pos += 8
			i++
		case 8, 16, 19, 20: // String, MethodType, Module, Package
			pos += 2
		case 15: // MethodHandle
			pos += 3
		case 9, 10, 11, 12, 17, 18: // Fieldref, Methodref, InterfaceMethodref,
			//                              NameAndType, Dynamic, InvokeDynamic
			pos += 4

		default:
			return "", fmt.Errorf("未知的常量池 tag %d（索引 %d）", tag, i)
		}
	}

	// 常量池之后：access_flags(2) 然后才是 this_class(2)
	pos += 2
	if pos+2 > len(b) {
		return "", errors.New("缺少 this_class")
	}
	thisIdx := int(binary.BigEndian.Uint16(b[pos:]))
	if thisIdx <= 0 || thisIdx >= cpCount {
		return "", fmt.Errorf("this_class 索引 %d 越界", thisIdx)
	}
	nameIdx := classIdx[thisIdx]
	if nameIdx <= 0 || nameIdx >= cpCount {
		return "", fmt.Errorf("类名索引 %d 越界", nameIdx)
	}
	name := utf8[nameIdx]
	if name == "" {
		return "", errors.New("类名为空")
	}
	return name, nil
}
