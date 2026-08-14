// Command livemanifest validates the non-secret safety manifest before a live
// test process is allowed to start.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/ljzd/rclone-123pan/internal/livetest"
)

func main() {
	path := flag.String("file", "", "live manifest JSON 路径")
	root := flag.String("root-id", "", "预期的非零 work root ID")
	mode := flag.String("mode", "", "预期模式：isolated 或 dedicated-contract")
	flag.Parse()
	if *path == "" || *root == "" || *mode == "" {
		fmt.Fprintln(os.Stderr, "必须提供 -file、-root-id 和 -mode")
		os.Exit(2)
	}
	rootID, err := strconv.ParseInt(*root, 10, 64)
	if err != nil || rootID <= 0 {
		fmt.Fprintln(os.Stderr, "root ID 必须为正整数")
		os.Exit(2)
	}
	manifest, err := livetest.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if manifest.WorkRootID != rootID {
		fmt.Fprintf(os.Stderr, "manifest work root ID 为 %d，不是 %d\n", manifest.WorkRootID, rootID)
		os.Exit(1)
	}
	if string(manifest.Mode) != *mode {
		fmt.Fprintf(os.Stderr, "manifest 模式为 %s，不是 %s\n", manifest.Mode, *mode)
		os.Exit(1)
	}
	fmt.Printf("live manifest 已验证：session=%s files=%d/%d directories=%d/%d payload=%d/%d\n",
		manifest.Session,
		manifest.Usage.Files, manifest.Limits.MaxFiles,
		manifest.Usage.Directories, manifest.Limits.MaxDirectories,
		manifest.Usage.Payload, manifest.Limits.MaxPayload,
	)
}
