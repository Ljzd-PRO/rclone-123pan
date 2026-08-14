package _123pan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
)

const defaultCopyPollInterval = time.Second

// CopyRecoveryError reports only provider IDs created or changed by this Copy
// transaction. It never asks an operator to recover objects by a name pattern.
type CopyRecoveryError struct {
	StageDirID int64
	CopiedID   int64
	TaskID     int64
	Cause      error
}

func (e *CopyRecoveryError) Error() string {
	return fmt.Sprintf(
		"123Pan 服务端复制需要人工核验（staging 目录 ID %d，复制文件 ID %d，任务 ID %d）：%s",
		e.StageDirID,
		e.CopiedID,
		e.TaskID,
		scrubSecrets(e.Cause.Error()),
	)
}

func (e *CopyRecoveryError) Unwrap() error { return e.Cause }

func (f *Fs) nextStageName(prefix string) (string, error) {
	nameFn := f.stageName
	if nameFn == nil {
		nameFn = randomStageName
	}
	return nameFn(prefix)
}

func (f *Fs) copyPollDelay() time.Duration {
	if f.copyPollInterval > 0 {
		return f.copyPollInterval
	}
	return defaultCopyPollInterval
}

// createCopyStageLocked creates a fresh, empty directory below a parent which
// is already protected by the account-wide parent mutation lock.
func (f *Fs) createCopyStageLocked(ctx context.Context, parentID int64, leaf string) (*api.File, error) {
	if existing, found, err := f.findChild(ctx, parentID, leaf); err != nil {
		return nil, err
	} else if found {
		return nil, fmt.Errorf("refusing non-unique Copy staging path %q with existing ID %d", leaf, existing.FileID)
	}
	request := map[string]any{
		"driveId":      0,
		"etag":         "",
		"fileName":     f.opt.Enc.FromStandardName(leaf),
		"parentFileId": parentID,
		"size":         0,
		"type":         1,
	}
	var response api.UploadData
	requestErr := f.client.doNonIdempotent(ctx, http.MethodPost, api.UploadRequestPath, request, &response)
	created, found, verifyErr := f.findChild(ctx, parentID, leaf)
	if verifyErr != nil {
		return nil, errors.Join(requestErr, verifyErr)
	}
	if found && created.IsDir() && created.FileID > 0 {
		if response.FileID > 0 && response.FileID != created.FileID {
			return nil, fmt.Errorf("Copy staging mkdir returned ID %d but visible directory has ID %d", response.FileID, created.FileID)
		}
		return created, nil
	}
	if requestErr != nil {
		return nil, requestErr
	}
	return nil, errorsNewProtocol("Copy staging mkdir succeeded without one visible directory")
}

func validateCopySource(item *api.File, parentID int64) (string, error) {
	if item == nil || item.FileID <= 0 || item.IsDir() || item.Type != 0 || item.Size < 0 || parentID < 0 {
		return "", errorsNewProtocol("Copy source has an invalid identity")
	}
	md5 := normalizeMD5(item.ETag)
	if md5 == "" {
		return "", errorsNewProtocol("Copy source is missing a valid MD5 ETag")
	}
	return md5, nil
}

func copyFileMatches(item api.File, parentID, sourceID int64, serverName string, size int64, md5 string) bool {
	return item.FileID > 0 &&
		item.FileID != sourceID &&
		item.ParentFileID == parentID &&
		item.FileName == serverName &&
		item.Size == size &&
		normalizeMD5(item.ETag) == md5 &&
		!item.IsDir() && item.Type == 0
}

func (f *Fs) inspectCopiedFile(ctx context.Context, stageID, sourceID int64, serverName string, size int64, md5 string) (*api.File, bool, error) {
	files, err := f.listAll(ctx, stageID)
	if err != nil {
		return nil, false, err
	}
	if len(files) == 0 {
		return nil, false, nil
	}
	if len(files) != 1 || !copyFileMatches(files[0], stageID, sourceID, serverName, size, md5) {
		if len(files) == 1 {
			item := files[0]
			return &item, false, fmt.Errorf("Copy staging directory ID %d contains an object with mismatched identity", stageID)
		}
		return nil, false, fmt.Errorf("Copy staging directory ID %d contains an ambiguous object set", stageID)
	}
	item := files[0]
	return &item, true, nil
}

func waitTimer(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (f *Fs) waitCopiedFile(ctx context.Context, stageID, sourceID int64, serverName string, size int64, md5 string) (*api.File, error) {
	for {
		item, found, err := f.inspectCopiedFile(ctx, stageID, sourceID, serverName, size, md5)
		if err != nil {
			return item, err
		}
		if found {
			return item, nil
		}
		if err := waitTimer(ctx, f.copyPollDelay()); err != nil {
			return nil, err
		}
	}
}

func (f *Fs) waitCopyTask(ctx context.Context, taskID int64) error {
	if taskID <= 0 {
		return errorsNewProtocol("Copy task returned an invalid task ID")
	}
	path := api.CopyTaskPath + "?taskId=" + strconv.FormatInt(taskID, 10)
	for {
		var task api.CopyTaskData
		if err := f.client.do(ctx, http.MethodGet, path, nil, &task); err != nil {
			return err
		}
		if task.Status == nil {
			return errorsNewProtocol("Copy task response is missing status")
		}
		switch *task.Status {
		case 0, 1:
			if err := waitTimer(ctx, f.copyPollDelay()); err != nil {
				return err
			}
		case 2:
			return nil
		default:
			reason := task.Reason
			if reason == "" {
				reason = "provider returned a terminal failure without reason"
			}
			return fmt.Errorf("123Pan Copy task %d failed with status %d: %s", taskID, *task.Status, scrubSecrets(reason))
		}
	}
}

func sameObjectSnapshot(left, right *api.File) bool {
	return left != nil && right != nil &&
		left.FileID == right.FileID &&
		left.FileName == right.FileName &&
		left.Size == right.Size &&
		normalizeMD5(left.ETag) == normalizeMD5(right.ETag) &&
		left.Type == right.Type
}

func (f *Fs) verifyFinalCopy(ctx context.Context, parentID, fileID int64, leaf string, size int64, md5 string) (*api.File, error) {
	serverName := f.opt.Enc.FromStandardName(leaf)
	for {
		files, err := f.listAll(ctx, parentID)
		if err == nil {
			var result *api.File
			for i := range files {
				item := &files[i]
				if item.FileName == serverName && item.FileID != fileID {
					return nil, fmt.Errorf("Copy target %q is ambiguous with ID %d", leaf, item.FileID)
				}
				if item.FileID == fileID {
					if !copyFileMatches(*item, parentID, 0, serverName, size, md5) {
						return nil, fmt.Errorf("Copy target ID %d failed identity verification", fileID)
					}
					copy := *item
					result = &copy
				}
			}
			if result != nil {
				return result, nil
			}
		}
		if err := waitTimer(ctx, 250*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

func (f *Fs) removeCopyStage(ctx context.Context, parentID int64, stage api.File) error {
	if stage.FileID <= 0 || !stage.IsDir() {
		return errorsNewProtocol("refusing to remove invalid Copy staging directory")
	}
	for range 2 {
		children, err := f.listAll(ctx, stage.FileID)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fmt.Errorf("refusing to remove non-empty Copy staging directory ID %d", stage.FileID)
		}
	}
	current, found, err := f.fileByID(ctx, parentID, stage.FileID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if !current.IsDir() || current.FileName != stage.FileName {
		return fmt.Errorf("Copy staging directory ID %d changed identity", stage.FileID)
	}
	return f.trashExact(ctx, parentID, *current)
}

func (f *Fs) copyRecovery(stageID, copiedID, taskID int64, cause error) error {
	return &CopyRecoveryError{StageDirID: stageID, CopiedID: copiedID, TaskID: taskID, Cause: cause}
}

// Copy performs an ID-verified provider-side copy without opening the source
// body. The provider only preserves the source name and copies into a directory,
// so a unique staging directory plus existing server-side move primitives are
// used to implement arbitrary rclone destination paths safely.
func (f *Fs) Copy(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	source, ok := src.(*Object)
	if !ok || source.fs.uid != f.uid {
		return nil, fs.ErrorCantCopy
	}
	targetLeaf, targetParentString, err := f.dirCache.FindPath(ctx, remote, true)
	if err != nil {
		return nil, err
	}
	targetParent, err := parseID(targetParentString, true)
	if err != nil {
		return nil, err
	}
	f.ensureLocks()
	unlock := f.locks.lock(
		parentMutationLockKey(source.parentID),
		parentMutationLockKey(targetParent),
		objectPathLockKey(source.parentID, source.name),
		objectPathLockKey(targetParent, f.opt.Enc.FromStandardName(targetLeaf)),
	)
	defer unlock()

	sourceDir, _ := dircache.SplitPath(source.remote)
	if err := source.fs.verifyDirectoryPathID(ctx, sourceDir, source.parentID); err != nil {
		return nil, err
	}
	targetDir, _ := dircache.SplitPath(remote)
	if err := f.verifyDirectoryPathID(ctx, targetDir, targetParent); err != nil {
		return nil, err
	}
	current, err := source.refreshExact(ctx)
	if err != nil {
		return nil, err
	}
	md5, err := validateCopySource(current, source.parentID)
	if err != nil {
		return nil, err
	}
	existing, targetFound, err := f.findChild(ctx, targetParent, targetLeaf)
	if err != nil {
		return nil, err
	}
	if targetFound {
		if existing.IsDir() {
			return nil, fs.ErrorIsDir
		}
		if existing.FileID == current.FileID {
			return newObject(f, remote, targetParent, *current), nil
		}
	}

	stageName, err := f.nextStageName("copy-dir")
	if err != nil {
		return nil, err
	}
	stage, err := f.createCopyStageLocked(ctx, targetParent, stageName)
	if err != nil {
		return nil, fmt.Errorf("create Copy staging directory: %w", err)
	}

	timeout := time.Duration(f.opt.VerifyTimeout)
	copyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request := api.CopyRequest{
		FileList: []api.CopyFile{{
			FileID:       current.FileID,
			Size:         current.Size,
			ETag:         current.ETag,
			Type:         current.Type,
			ParentFileID: source.parentID,
			FileName:     current.FileName,
			DriveID:      0,
		}},
		TargetFileID: stage.FileID,
	}
	var start api.CopyStartData
	startErr := f.client.doNonIdempotent(copyCtx, http.MethodPost, api.CopyStartPath, request, &start)
	taskID := start.TaskID
	var taskErr error
	if startErr == nil {
		if start.Mode == nil {
			startErr = errorsNewProtocol("Copy start response is missing mode")
		} else if *start.Mode != 2 {
			if taskID <= 0 {
				startErr = errorsNewProtocol("asynchronous Copy response is missing a positive task ID")
			} else {
				taskErr = f.waitCopyTask(copyCtx, taskID)
			}
		}
	}

	copied, copiedErr := f.waitCopiedFile(copyCtx, stage.FileID, current.FileID, current.FileName, current.Size, md5)
	if copiedErr != nil {
		var copiedID int64
		if copied != nil {
			copiedID = copied.FileID
		}
		return nil, f.copyRecovery(stage.FileID, copiedID, taskID, errors.Join(startErr, taskErr, copiedErr))
	}
	if startErr != nil || taskErr != nil {
		fs.Debugf(f, "123Pan Copy coordinated an ambiguous response through copied object ID %d", copied.FileID)
	}

	latestTarget, latestFound, err := f.findChild(ctx, targetParent, targetLeaf)
	if err != nil {
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, err)
	}
	if latestFound != targetFound || (targetFound && !sameObjectSnapshot(existing, latestTarget)) {
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, errorsNewProtocol("Copy target changed while provider task was running"))
	}

	temporaryName, err := f.nextStageName("copy-file")
	if err != nil {
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, err)
	}
	moved, err := f.moveItem(ctx, *copied, stage.FileID, targetParent, f.opt.Enc.ToStandardName(copied.FileName), temporaryName)
	if err != nil {
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, fmt.Errorf("move copied object out of staging directory: %w", err))
	}

	var backupName string
	if targetFound {
		backupName, err = f.nextStageName("copy-backup")
		if err != nil {
			return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, err)
		}
		if conflict, found, findErr := f.findChild(ctx, targetParent, backupName); findErr != nil {
			return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, findErr)
		} else if found {
			return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, fmt.Errorf("Copy backup path conflicts with ID %d", conflict.FileID))
		}
		if err := f.renameByID(ctx, targetParent, existing.FileID, targetLeaf, backupName); err != nil {
			return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, fmt.Errorf("stage existing Copy target as backup: %w", err))
		}
	}

	if err := f.renameByID(ctx, targetParent, moved.FileID, temporaryName, targetLeaf); err != nil {
		var rollback error
		if targetFound {
			rollback = f.renameByID(ctx, targetParent, existing.FileID, backupName, targetLeaf)
		}
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, errors.Join(fmt.Errorf("promote copied object: %w", err), rollback))
	}
	final, err := f.verifyFinalCopy(copyCtx, targetParent, moved.FileID, targetLeaf, current.Size, md5)
	if err != nil {
		var rollback error
		if targetFound {
			moveAsideErr := f.renameByID(ctx, targetParent, moved.FileID, targetLeaf, temporaryName)
			restoreErr := f.renameByID(ctx, targetParent, existing.FileID, backupName, targetLeaf)
			rollback = errors.Join(moveAsideErr, restoreErr)
		}
		return nil, f.copyRecovery(stage.FileID, copied.FileID, taskID, errors.Join(err, rollback))
	}
	result := newObject(f, remote, targetParent, *final)
	if targetFound {
		backup := *existing
		backup.FileName = f.opt.Enc.FromStandardName(backupName)
		if err := f.trashExact(ctx, targetParent, backup); err != nil {
			return result, f.copyRecovery(stage.FileID, copied.FileID, taskID, fmt.Errorf("trash replaced Copy backup ID %d: %w", backup.FileID, err))
		}
	}
	if err := f.removeCopyStage(ctx, targetParent, *stage); err != nil {
		return result, f.copyRecovery(stage.FileID, copied.FileID, taskID, fmt.Errorf("remove empty Copy staging directory: %w", err))
	}
	return result, nil
}
