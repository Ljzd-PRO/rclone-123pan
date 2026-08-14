package pan123

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
)

func mutationSource(f *Fs) *Object {
	return newObject(f, "file", 0, api.File{
		FileName:     "file",
		FileID:       1,
		ParentFileID: 0,
		Size:         1,
		ETag:         "9dd4e461268c8034f5c8564e155c67a6",
		Type:         0,
	})
}

func TestCopyImmediateSameParentRenameUsesProviderBodylessCopy(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	result, err := f.Copy(context.Background(), mutationSource(f), "copy-of-file")
	if err != nil {
		t.Fatal(err)
	}
	ider, ok := result.(fs.IDer)
	if !ok || ider.ID() == "1" || result.Remote() != "copy-of-file" {
		t.Fatalf("unexpected Copy result %#v", result)
	}
	resultID, _ := parseID(ider.ID(), false)
	node, found := store.get(resultID)
	if !found || node.parent != 0 || node.file.FileName != "copy-of-file" || normalizeMD5(node.file.ETag) != "9dd4e461268c8034f5c8564e155c67a6" {
		t.Fatalf("unexpected copied node %#v", node)
	}
	if _, found := store.get(1); !found {
		t.Fatal("server-side Copy changed or removed the source ID")
	}
	store.mu.Lock()
	request := store.lastCopyRequest
	startCalls := store.copyStartCalls
	_, stageStillExists := store.nodes[request.TargetFileID]
	store.mu.Unlock()
	if startCalls != 1 || len(request.FileList) != 1 {
		t.Fatalf("Copy start calls=%d request=%#v", startCalls, request)
	}
	got := request.FileList[0]
	if got.FileID != 1 || got.ParentFileID != 0 || got.FileName != "file" || got.Size != 1 || got.ETag != "9dd4e461268c8034f5c8564e155c67a6" || got.Type != 0 || got.DriveID != 0 {
		t.Fatalf("unexpected provider Copy source identity %#v", got)
	}
	if request.TargetFileID <= 0 || stageStillExists {
		t.Fatalf("staging directory ID=%d stillExists=%t", request.TargetFileID, stageStillExists)
	}
}

func TestCopyAsyncPollsOfficialStatusesAndCopiesAcrossParents(t *testing.T) {
	store := newMutationStore(t)
	store.copyMode = 0
	store.copyStatuses = []int{0, 1, 2}
	f := newMutationFs(t, store)
	result, err := f.Copy(context.Background(), mutationSource(f), "dir/async-copy")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parseID(result.(fs.IDer).ID(), false)
	node, found := store.get(id)
	if !found || node.parent != 10 || node.file.FileName != "async-copy" {
		t.Fatalf("unexpected asynchronous Copy result %#v", node)
	}
	store.mu.Lock()
	taskCalls := store.copyTaskCalls
	targetStage := store.lastCopyRequest.TargetFileID
	stageNode, stageFound := store.nodes[targetStage]
	store.mu.Unlock()
	if taskCalls != 3 {
		t.Fatalf("Copy task polled %d times, want 3", taskCalls)
	}
	if stageFound || stageNode.parent != 0 {
		t.Fatalf("unexpected staging state found=%t node=%#v", stageFound, stageNode)
	}
}

func TestCopyCoordinatesLostStartResponseWithoutReplaying(t *testing.T) {
	store := newMutationStore(t)
	store.copyLoseStart = true
	f := newMutationFs(t, store)
	result, err := f.Copy(context.Background(), mutationSource(f), "coordinated-copy")
	if err != nil {
		t.Fatal(err)
	}
	if result.Remote() != "coordinated-copy" {
		t.Fatalf("unexpected result %q", result.Remote())
	}
	store.mu.Lock()
	calls := store.copyStartCalls
	store.mu.Unlock()
	if calls != 1 {
		t.Fatalf("ambiguous non-idempotent Copy was called %d times", calls)
	}
}

func TestCopyReplacesExistingTargetThroughBackupTransaction(t *testing.T) {
	store := newMutationStore(t)
	store.nodes[2] = mutationNode{file: api.File{
		FileName: "target", FileID: 2, Size: 1, ETag: "415290769594460e2e485922904f345d",
	}, parent: 0}
	f := newMutationFs(t, store)
	result, err := f.Copy(context.Background(), mutationSource(f), "target")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := parseID(result.(fs.IDer).ID(), false)
	if id == 1 || id == 2 {
		t.Fatalf("replacement reused source or old target ID %d", id)
	}
	if _, found := store.get(2); found {
		t.Fatal("verified old target backup was not moved to the recycle bin")
	}
	if _, found := store.get(1); !found {
		t.Fatal("replacement Copy removed its source")
	}
	node, found := store.get(id)
	if !found || node.file.FileName != "target" || normalizeMD5(node.file.ETag) != "9dd4e461268c8034f5c8564e155c67a6" {
		t.Fatalf("unexpected replacement node %#v", node)
	}
}

func TestCopyFailureLeavesOnlyExactRecoveryIDsAndPreservesSource(t *testing.T) {
	store := newMutationStore(t)
	store.copyMode = 0
	store.copyStatuses = []int{3}
	store.copyNoMaterialize = true
	f := newMutationFs(t, store)
	f.opt.VerifyTimeout = fs.Duration(15 * time.Millisecond)
	result, err := f.Copy(context.Background(), mutationSource(f), "failed-copy")
	if result != nil || err == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var recovery *CopyRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("expected CopyRecoveryError, got %T: %v", err, err)
	}
	if recovery.StageDirID <= 0 || recovery.CopiedID != 0 || recovery.TaskID != 77 {
		t.Fatalf("unexpected recovery identity %#v", recovery)
	}
	stage, found := store.get(recovery.StageDirID)
	if !found || !stage.file.IsDir() {
		t.Fatalf("ambiguous task staging directory was not retained: %#v", stage)
	}
	if _, found := store.get(1); !found {
		t.Fatal("failed Copy removed its source")
	}
	if _, found, findErr := f.findChild(context.Background(), 0, "failed-copy"); findErr != nil || found {
		t.Fatalf("failed Copy exposed a destination object: found=%t err=%v", found, findErr)
	}
}

func TestCopyRejectsMismatchedTerminalMetadata(t *testing.T) {
	store := newMutationStore(t)
	store.copyCorrupt = true
	f := newMutationFs(t, store)
	_, err := f.Copy(context.Background(), mutationSource(f), "corrupt-copy")
	var recovery *CopyRecoveryError
	if !errors.As(err, &recovery) || recovery.StageDirID <= 0 || recovery.CopiedID <= 0 {
		t.Fatalf("expected exact corrupt Copy recovery IDs, got %#v err=%v", recovery, err)
	}
	if _, found := store.get(1); !found {
		t.Fatal("metadata mismatch removed the source")
	}
}

func TestCopyRejectsIncompatibleAccountAndInvalidMD5(t *testing.T) {
	store := newMutationStore(t)
	destination := newMutationFs(t, store)
	other := newMutationFs(t, store)
	other.uid = destination.uid + 1
	if _, err := destination.Copy(context.Background(), mutationSource(other), "other-account"); !errors.Is(err, fs.ErrorCantCopy) {
		t.Fatalf("got %v, want ErrorCantCopy", err)
	}
	invalid := mutationSource(destination)
	invalid.metadata.ETag = "not-an-md5"
	invalid.md5 = ""
	store.mu.Lock()
	node := store.nodes[1]
	node.file.ETag = "not-an-md5"
	store.nodes[1] = node
	store.mu.Unlock()
	if _, err := destination.Copy(context.Background(), invalid, "invalid-md5"); err == nil {
		t.Fatal("Copy accepted a source without a valid MD5 ETag")
	}
	store.mu.Lock()
	calls := store.copyStartCalls
	store.mu.Unlock()
	if calls != 0 {
		t.Fatalf("invalid Copy reached provider endpoint %d times", calls)
	}
}

func TestRcloneOperationsCopyUsesServerSideCopyForCreateAndReplace(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	ctx, _ := fs.AddConfig(context.Background())
	source := mutationSource(f)

	created, err := operations.Copy(ctx, f, nil, "operations-copy", source)
	if err != nil {
		t.Fatal(err)
	}
	createdID, _ := parseID(created.(fs.IDer).ID(), false)
	if createdID == source.id {
		t.Fatal("operations.Copy did not create an independent provider object")
	}
	store.mu.Lock()
	firstStartCalls := store.copyStartCalls
	store.mu.Unlock()
	if firstStartCalls != 1 {
		t.Fatalf("operations.Copy called provider Copy %d times", firstStartCalls)
	}

	store.nodes[2] = mutationNode{file: api.File{
		FileName: "operations-target", FileID: 2, Size: 1, ETag: "415290769594460e2e485922904f345d",
	}, parent: 0}
	destination := newObject(f, "operations-target", 0, store.nodes[2].file)
	replaced, err := operations.Copy(ctx, f, destination, "ignored-by-rclone", source)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Remote() != "operations-target" || replaced.(fs.IDer).ID() == "2" {
		t.Fatalf("unexpected operations.Copy replacement %#v", replaced)
	}
	if _, found := store.get(2); found {
		t.Fatal("operations.Copy retained the old target ID after verified replacement")
	}
	if _, found := store.get(1); !found {
		t.Fatal("operations.Copy removed the source after replacement")
	}
}
