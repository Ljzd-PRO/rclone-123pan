//go:build pan123live

package pan123_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/ljzd/rclone-123pan/backend/pan123"
	"github.com/ljzd/rclone-123pan/internal/livetest"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fstest/fstests"
)

func verifyLiveSentinels(t *testing.T, manifest *livetest.Manifest) {
	t.Helper()
	ctx := context.Background()
	anchor, err := fs.NewFs(ctx, manifest.AnchorRemote)
	if err != nil {
		t.Fatalf("打开 sentinel anchor remote: %v", err)
	}
	err = manifest.VerifySentinels(ctx, func(ctx context.Context, expected livetest.Object) (livetest.Object, error) {
		object, err := anchor.NewObject(ctx, expected.Name)
		if err != nil {
			return livetest.Object{}, err
		}
		ider, ok := object.(fs.IDer)
		if !ok {
			return livetest.Object{}, fmt.Errorf("sentinel %q 没有 IDer", expected.Name)
		}
		id, err := strconv.ParseInt(ider.ID(), 10, 64)
		if err != nil {
			return livetest.Object{}, err
		}
		parent, ok := object.(fs.ParentIDer)
		if !ok {
			return livetest.Object{}, fmt.Errorf("sentinel %q 没有 ParentIDer", expected.Name)
		}
		parentID, err := strconv.ParseInt(parent.ParentID(), 10, 64)
		if err != nil {
			return livetest.Object{}, err
		}
		sum, err := object.Hash(ctx, hash.MD5)
		if err != nil {
			return livetest.Object{}, err
		}
		return livetest.Object{
			Kind:     livetest.KindFile,
			ID:       id,
			ParentID: parentID,
			Name:     expected.Name,
			Size:     object.Size(),
			MD5:      sum,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestIntegration runs only after all destructive live-test guard variables
// are supplied. Unit and fault tests never require a real account.
func TestIntegration(t *testing.T) {
	if os.Getenv("RCLONE_123_RUN_LIVE") != "1" {
		t.Skip("仅在经过授权的专用空账号上设置 RCLONE_123_RUN_LIVE=1")
	}
	if os.Getenv("RCLONE_123_LIVE_ACK") != "DEDICATED_EMPTY_ACCOUNT" {
		t.Fatal("完整 fstests 只允许专用空账号，必须显式设置 RCLONE_123_LIVE_ACK=DEDICATED_EMPTY_ACCOUNT")
	}
	root := os.Getenv("RCLONE_123_LIVE_ROOT_ID")
	if root == "" || root == "0" {
		t.Fatal("RCLONE_123_LIVE_ROOT_ID 必须为固定的非零测试根")
	}
	manifest, err := livetest.Load(os.Getenv("RCLONE_123_LIVE_MANIFEST"))
	if err != nil {
		t.Fatalf("加载 live manifest: %v", err)
	}
	if manifest.Mode != livetest.ModeDedicatedContract {
		t.Fatalf("完整 fstests 要求 %q manifest", livetest.ModeDedicatedContract)
	}
	if strconv.FormatInt(manifest.WorkRootID, 10) != root {
		t.Fatalf("manifest work root ID %d 与环境变量 %s 不一致", manifest.WorkRootID, root)
	}
	verifyLiveSentinels(t, manifest)
	defer verifyLiveSentinels(t, manifest)
	fstests.Run(t, &fstests.Opt{
		RemoteName: "Test123Pan:",
		NilObject:  (*pan123.Object)(nil),
	})
}
