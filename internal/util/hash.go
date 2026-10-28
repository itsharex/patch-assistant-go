package util

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"hash"
	"io"
)

// ToHexStr 字节序列转小写十六进制串。
// 对应 Java HashUtil.toHexStr，输出格式与 Java 完全一致（小写、逐字节补零）。
func ToHexStr(b []byte) string { return hex.EncodeToString(b) }

// Md5 计算 md5 十六进制串。
// 对应 Java HashUtil.md5。
func Md5(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// Sha1 计算 sha1 十六进制串。
// 对应 Java HashUtil.sha1。
func Sha1(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// Md5Stream 流式计算 md5 十六进制串，不把整份内容读进内存。
//
// 与 Md5 的分工：Md5 给"内容已经在手里的字节片"用；本函数给"只有一条
// io.Reader（磁盘文件、zip 条目）"用。拖进来一个 500 MB 的包要算 md5 时，
// 走 Md5Stream 而不是先 ReadAll 再 Md5 —— 否则内存直接多出 500 MB。
func Md5Stream(r io.Reader) (string, error) {
	m := md5.New()
	if _, err := io.Copy(m, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(m.Sum(nil)), nil
}

// HashResult 内容哈希结果。
//
// 与 Java HashUtil.getHashInfo 返回的 HashInfo 字段一一对应。
// 刻意不直接返回 model.HashInfo —— 那会让 util 反向依赖 model，
// 而 model 需要用到 util 的路径函数，会形成导入环。
// 需要 model.HashInfo 的调用方在 service 层自行组装即可。
type HashResult struct {
	// Length 内容长度
	Length int
	// Md5 md5 十六进制串
	Md5 string
	// Sha1 sha1 十六进制串
	Sha1 string
}

// GetHashInfo 流式计算内容长度 + md5 + sha1。
//
// 对应 Java HashUtil.getHashInfo(InputStream)。Java 版用 MessageDigest
// 增量 update，这里同样用 hash.Hash 增量写入，避免整包读进内存 ——
// 补丁包动辄上百 MB，这一点必须保持。
func GetHashInfo(r io.Reader) (HashResult, error) {
	var res HashResult
	m := md5.New()
	s := sha1.New()
	w := io.MultiWriter(m, s)
	n, err := io.Copy(w, r)
	if err != nil {
		return res, err
	}
	res.Length = int(n)
	res.Md5 = hexSum(m)
	res.Sha1 = hexSum(s)
	return res, nil
}

func hexSum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }
