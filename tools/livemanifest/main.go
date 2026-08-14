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
	action := flag.String("action", "validate", "操作：validate、reserve-file、reserve-directory、record、relocate 或 mark")
	kind := flag.String("kind", "", "record 的对象类型：file 或 directory")
	id := flag.Int64("id", 0, "record/mark 的正整数对象 ID")
	parentID := flag.Int64("parent-id", 0, "record 的正整数 parent ID")
	name := flag.String("name", "", "record 的对象名称")
	size := flag.Int64("size", -1, "reserve-file/record 的对象大小")
	md5 := flag.String("md5", "", "record 文件的 MD5")
	state := flag.String("state", "", "mark 的状态：trashed 或 missing_confirmed")
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
	save := false
	switch *action {
	case "validate":
	case "reserve-file":
		if err := manifest.ReserveFile(*size); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save = true
	case "reserve-directory":
		if err := manifest.ReserveDirectory(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save = true
	case "record":
		object := livetest.Object{
			Kind:     livetest.Kind(*kind),
			ID:       *id,
			ParentID: *parentID,
			Name:     *name,
			Size:     *size,
			MD5:      *md5,
		}
		if err := manifest.RecordObject(object); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save = true
	case "relocate":
		if err := manifest.RelocateObject(*id, *parentID, *name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save = true
	case "mark":
		if err := manifest.MarkCleanup(*id, livetest.CleanupState(*state)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		save = true
	default:
		fmt.Fprintf(os.Stderr, "未知 action %q\n", *action)
		os.Exit(2)
	}
	if save {
		if err := manifest.Save(*path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("live manifest 已验证：session=%s files=%d/%d directories=%d/%d payload=%d/%d\n",
		manifest.Session,
		manifest.Usage.Files, manifest.Limits.MaxFiles,
		manifest.Usage.Directories, manifest.Limits.MaxDirectories,
		manifest.Usage.Payload, manifest.Limits.MaxPayload,
	)
}
