package k8s

import (
	"crypto/sha256"
	"encoding/base32"
	"strings"
)

// identityDigest 保留原始身份的大小写和标点，生成 52 字符的合法标签值。
// 不用替换特殊字符的方式编码身份，避免不同登录名碰撞。
func identityDigest(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}
