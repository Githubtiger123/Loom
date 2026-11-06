package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	cmd := flag.String("cmd", "api", "运行角色: api | sync | cron")
	flag.Parse()

	switch *cmd {
	case "api", "sync", "cron":
		fmt.Printf("loom starting, role=%s\n", *cmd)
	default:
		fmt.Fprintf(os.Stderr, "unknown cmd: %s\n", *cmd)
		os.Exit(1)
	}
}
