package validator

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strings"
)

// HasLosslessJSONIntegerLiterals checks integer literals before protobuf Value
// converts them to float64. Callers must first enforce JSON size/depth bounds.
// Decimal/exponent values keep the normal JSON double semantics.
func HasLosslessJSONIntegerLiterals(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		number, ok := token.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			continue
		}
		value, err := number.Int64()
		if err != nil {
			return false
		}
		exact := big.NewInt(value)
		rounded, _ := new(big.Float).SetFloat64(float64(value)).Int(nil)
		if exact.Cmp(rounded) != 0 {
			return false
		}
	}
}
