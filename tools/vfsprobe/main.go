// vfsprobe 对显式指定的单个文件执行可重复的随机写和截断探测。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
)

var expected = []byte("0123WXYZ89ab")

func verify(path string) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("重新读取探测文件：%w", err)
	}
	if !bytes.Equal(got, expected) {
		return fmt.Errorf("探测文件内容不匹配：得到 %d 字节，预期 %d 字节", len(got), len(expected))
	}
	return nil
}

func writeProbe(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("创建探测文件：%w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	if _, err = f.Write([]byte("0123456789abcdef")); err != nil {
		return fmt.Errorf("写入初始内容：%w", err)
	}
	if _, err = f.Seek(4, 0); err != nil {
		return fmt.Errorf("定位随机写 offset：%w", err)
	}
	if _, err = f.Write([]byte("WXYZ")); err != nil {
		return fmt.Errorf("执行随机写：%w", err)
	}
	if err = f.Truncate(int64(len(expected))); err != nil {
		return fmt.Errorf("截断探测文件：%w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("同步探测文件：%w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("关闭探测文件：%w", err)
	}
	closed = true
	return verify(path)
}

func main() {
	path := flag.String("path", "", "要创建或核验的单个文件路径")
	verifyOnly := flag.Bool("verify-only", false, "只核验最终的 12 字节内容，不写文件")
	flag.Parse()
	if *path == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "必须只提供 -path")
		os.Exit(2)
	}
	var err error
	if *verifyOnly {
		err = verify(*path)
	} else {
		err = writeProbe(*path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
