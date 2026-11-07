package validator

import (
	"math/big"
	"regexp"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
)

var quantityExponent = regexp.MustCompile(`[eE]([+-]?[0-9]+)$`)

// QuantityUnits 精确转换为数据库的整数单位，拒绝溢出和小于存储精度的值。
// CPU 使用 scale=1000（毫核），内存和容量使用 scale=1（字节）。
func QuantityUnits(value string, scale int64) (int64, bool) {
	if len(value) == 0 || len(value) > 64 || scale < 1 {
		return 0, false
	}
	// 限制科学计数法指数，避免 AsDec 为恶意巨大指数分配巨量内存。
	if match := quantityExponent.FindStringSubmatch(value); match != nil {
		exponent, err := strconv.Atoi(match[1])
		if err != nil || exponent < -18 || exponent > 18 {
			return 0, false
		}
	}
	q, err := resource.ParseQuantity(value)
	if err != nil || q.Sign() <= 0 {
		return 0, false
	}
	n, ok := new(big.Rat).SetString(q.AsDec().String())
	if !ok {
		return 0, false
	}
	// ParseQuantity 对过大的 BinarySI 值可能饱和到 MaxInt64；不能静默接受饱和值。
	if n.Cmp(new(big.Rat).SetInt64(1<<63-1)) >= 0 {
		return 0, false
	}
	n.Mul(n, new(big.Rat).SetInt64(scale))
	if !n.IsInt() || !n.Num().IsInt64() || n.Sign() <= 0 {
		return 0, false
	}
	return n.Num().Int64(), true
}
