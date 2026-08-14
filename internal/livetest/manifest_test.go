package livetest

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func digest(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validManifest() *Manifest {
	manifest := NewManifest(ModeIsolated, "rclone-test-0123456789abcdef", 7, 10, 11, "Test123PanAnchor:")
	manifest.Usage = Usage{Files: 2, Directories: 2, Payload: 2}
	manifest.Sentinels = []Object{
		{Kind: KindFile, ID: 20, ParentID: 10, Name: "guard-a", Size: 1, MD5: digest("a")},
		{Kind: KindFile, ID: 21, ParentID: 10, Name: "guard-b", Size: 1, MD5: digest("b")},
	}
	return manifest
}

func TestManifestRoundTripAndOwnerOnlyMode(t *testing.T) {
	manifest := validManifest()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := manifest.Save(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode is %04o", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session != manifest.Session || loaded.AnchorID != manifest.AnchorID {
		t.Fatalf("round trip changed manifest: %#v", loaded)
	}
}

func TestManifestRejectsQuotaWideningAndDuplicateIdentity(t *testing.T) {
	manifest := validManifest()
	manifest.Limits.MaxFiles = HardMaxFiles + 1
	if err := manifest.Validate(); err == nil {
		t.Fatal("accepted widened file quota")
	}
	manifest = validManifest()
	manifest.Objects = append(manifest.Objects, manifest.Sentinels[0])
	if err := manifest.Validate(); err == nil {
		t.Fatal("accepted duplicate sentinel identity")
	}
}

func TestManifestReservationsAreCumulative(t *testing.T) {
	manifest := validManifest()
	manifest.Limits.MaxFiles = 3
	manifest.Limits.MaxDirectories = 3
	manifest.Limits.MaxSingleFile = 4
	manifest.Limits.MaxPayload = 6
	if err := manifest.ReserveFile(4); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ReserveFile(0); err == nil {
		t.Fatal("file count quota was refunded or ignored")
	}
	if err := manifest.ReserveDirectory(); err != nil {
		t.Fatal(err)
	}
	if err := manifest.ReserveDirectory(); err == nil {
		t.Fatal("directory quota was refunded or ignored")
	}
}

func TestCleanupStateIsRecordedAndMonotonic(t *testing.T) {
	manifest := validManifest()
	object := Object{Kind: KindFile, ID: 30, ParentID: 11, Name: "created", Size: 1, MD5: digest("x")}
	if err := manifest.RecordObject(object); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Objects[0].Cleanup; got != CleanupActive {
		t.Fatalf("initial cleanup state = %q", got)
	}
	if err := manifest.MarkCleanup(30, CleanupTrashed); err != nil {
		t.Fatal(err)
	}
	if err := manifest.MarkCleanup(30, CleanupMissingConfirmed); err != nil {
		t.Fatal(err)
	}
	if err := manifest.MarkCleanup(30, CleanupTrashed); err == nil {
		t.Fatal("cleanup state moved backwards")
	}
	if err := manifest.MarkCleanup(20, CleanupTrashed); err == nil {
		t.Fatal("sentinel was accepted as a cleanup target")
	}
}

func TestManifestRejectsCleanedSentinel(t *testing.T) {
	manifest := validManifest()
	manifest.Sentinels[0].Cleanup = CleanupMissingConfirmed
	if err := manifest.Validate(); err == nil {
		t.Fatal("accepted a cleaned sentinel")
	}
}

func TestVerifySentinelsFailsClosed(t *testing.T) {
	manifest := validManifest()
	err := manifest.VerifySentinels(context.Background(), func(_ context.Context, expected Object) (Object, error) {
		if expected.ID == 21 {
			expected.Size++
		}
		return expected, nil
	})
	if err == nil {
		t.Fatal("accepted a changed sentinel")
	}
}

func TestLoadRejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	manifest := validManifest()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := manifest.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("accepted group/world-readable manifest")
	}
}
