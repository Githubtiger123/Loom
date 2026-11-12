package validator

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// IsBoundedJSONObject checks the original JSON before decoding into a struct or
// map can discard duplicate keys. Limits include object keys and delimiters.
func IsBoundedJSONObject(raw []byte, maxBytes, maxDepth, maxTokens int) bool {
	if len(raw) == 0 || len(raw) > maxBytes || maxDepth < 1 || maxTokens < 1 || !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	remaining := maxTokens
	next := func() (json.Token, error) {
		remaining--
		if remaining < 0 {
			return nil, io.ErrShortBuffer
		}
		return d.Token()
	}
	var value func(int, bool) bool
	value = func(depth int, objectOnly bool) bool {
		if depth > maxDepth {
			return false
		}
		token, err := next()
		if err != nil {
			return false
		}
		delim, composite := token.(json.Delim)
		if objectOnly && (!composite || delim != '{') {
			return false
		}
		if !composite {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := next()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return false
				}
				keys[name] = true
				if !value(depth+1, false) {
					return false
				}
			}
		case '[':
			for d.More() {
				if !value(depth+1, false) {
					return false
				}
			}
		default:
			return false
		}
		end, err := next()
		return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !value(1, true) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
