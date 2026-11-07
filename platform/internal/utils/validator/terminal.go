package validator

func IsTerminalShell(value string) bool { return value == "/bin/sh" || value == "/bin/bash" }
func IsTerminalSize(width, height int) bool {
	return width >= 1 && width <= 1000 && height >= 1 && height <= 1000
}
