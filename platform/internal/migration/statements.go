package migration

import (
	"fmt"
	"strings"
)

// splitStatements handles ordinary MySQL migrations, not client DELIMITER
// commands or stored programs. Comments and quoted values may contain ';'.
// Executable comments are rejected rather than silently discarded.
func splitStatements(source string) ([]string, error) {
	var statements []string
	var statement strings.Builder
	var quote byte
	flush := func() {
		if sql := strings.TrimSpace(statement.String()); sql != "" {
			statements = append(statements, sql)
		}
		statement.Reset()
	}
	for i := 0; i < len(source); i++ {
		ch := source[i]
		if quote != 0 {
			statement.WriteByte(ch)
			if ch == '\\' && quote != '`' && i+1 < len(source) {
				i++
				statement.WriteByte(source[i])
				continue
			}
			if ch == quote {
				if i+1 < len(source) && source[i+1] == quote {
					i++
					statement.WriteByte(source[i])
				} else {
					quote = 0
				}
			}
			continue
		}
		switch {
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
			statement.WriteByte(ch)
		case ch == '#' || (ch == '-' && i+1 < len(source) && source[i+1] == '-' &&
			(i+2 == len(source) || source[i+2] <= ' ')):
			// MySQL -- comments require whitespace/control after the second '-'.
			for i < len(source) && source[i] != '\n' && source[i] != '\r' {
				i++
			}
			statement.WriteByte('\n')
		case ch == '/' && i+1 < len(source) && source[i+1] == '*':
			if i+2 < len(source) && (source[i+2] == '!' || source[i+2] == '+') {
				return nil, fmt.Errorf("不支持可执行 SQL 注释或优化器提示")
			}
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("SQL 块注释未闭合")
			}
			i += end + 3
			statement.WriteByte(' ')
		case ch == ';':
			flush()
		default:
			statement.WriteByte(ch)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("SQL 引号未闭合")
	}
	flush()
	return statements, nil
}
