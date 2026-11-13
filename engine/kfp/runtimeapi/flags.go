package runtimeapi

import (
	"fmt"
	"strings"
)

// FlagValues parses the official runtime flag envelope without interpreting IR.
// Duplicate flags are rejected; component commands after -- are not flags.
func FlagValues(args []string) (map[string]string, []string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			return values, args[i+1:], nil
		}
		if !strings.HasPrefix(args[i], "-") {
			return values, args[i:], nil
		}
		name, value, equals := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if name == "" {
			return nil, nil, fmt.Errorf("empty runtime flag")
		}
		if _, exists := values[name]; exists {
			return nil, nil, fmt.Errorf("duplicate runtime flag %s", name)
		}
		if !equals {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("missing runtime flag value")
			}
			i++
			value = args[i]
		}
		values[name] = value
	}
	return values, nil, nil
}
