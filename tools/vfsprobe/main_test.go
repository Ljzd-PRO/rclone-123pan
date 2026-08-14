package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.bin")
	if err := writeProbe(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(expected)) {
		t.Fatalf("探测文件大小 = %d，预期 %d", info.Size(), len(expected))
	}
}

func TestVerifyRejectsUnexpectedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.bin")
	if err := os.WriteFile(path, []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(path); err == nil {
		t.Fatal("错误内容应被拒绝")
	}
}
