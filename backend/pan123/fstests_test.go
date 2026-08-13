package pan123_test

import (
	"os"
	"testing"

	"github.com/ljzd/rclone-123pan/backend/pan123"
	"github.com/rclone/rclone/fstest/fstests"
)

// TestIntegration runs only after all destructive live-test guard variables
// are supplied. Unit and fault tests never require a real account.
func TestIntegration(t *testing.T) {
	if os.Getenv("RCLONE_123_RUN_LIVE") != "1" {
		t.Skip("set RCLONE_123_RUN_LIVE=1 only for a dedicated empty account")
	}
	if root := os.Getenv("RCLONE_123_LIVE_ROOT_ID"); root == "" || root == "0" {
		t.Fatal("RCLONE_123_LIVE_ROOT_ID must be a fixed non-zero test root")
	}
	if os.Getenv("RCLONE_123_LIVE_SENTINELS") == "" {
		t.Fatal("RCLONE_123_LIVE_SENTINELS must record immutable root-external sentinel IDs and hashes")
	}
	fstests.Run(t, &fstests.Opt{
		RemoteName: "Test123Pan:",
		NilObject:  (*pan123.Object)(nil),
	})
}
