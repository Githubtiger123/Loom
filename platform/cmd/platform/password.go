package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// 密码不通过 flag、进程参数或日志传递；交互终端隐藏输入，流水线可使用标准输入。
func readLocalPassword() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "输入新密码（不回显）：")
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("读取密码失败")
		}
		fmt.Fprint(os.Stderr, "再次输入：")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil || string(first) != string(second) {
			return "", fmt.Errorf("两次密码不一致或读取失败")
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(io.LimitReader(os.Stdin, 2048)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("读取密码失败")
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
