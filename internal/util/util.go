// Package util 通用小工具（随机 id / hex 编码）。
package util

import (
	"crypto/rand"
	"encoding/hex"
)

// RandHex n 字节 CSPRNG → 2n 长度小写 hex
func RandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// IsHex 校验纯十六进制字符串
func IsHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
