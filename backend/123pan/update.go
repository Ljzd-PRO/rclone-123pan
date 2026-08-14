package _123pan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
)

type lockEntry struct {
	mu   sync.Mutex
	refs int
}

type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

// accountLockRegistry makes path locks effective across separately configured
// remotes which resolve to the same 123Pan account in this process.
var accountLockRegistry sync.Map

func newKeyedLocks() *keyedLocks { return &keyedLocks{locks: make(map[string]*lockEntry)} }

func locksForUID(uid int64) *keyedLocks {
	if uid <= 0 {
		return newKeyedLocks()
	}
	locks, _ := accountLockRegistry.LoadOrStore(uid, newKeyedLocks())
	return locks.(*keyedLocks)
}

func parentMutationLockKey(parentID int64) string {
	return fmt.Sprintf("parent:%d", parentID)
}

func objectPathLockKey(parentID int64, serverName string) string {
	return fmt.Sprintf("path:%d:%s", parentID, serverName)
}

func (k *keyedLocks) lock(keys ...string) func() {
	keys = append([]string(nil), keys...)
	sort.Strings(keys)
	unique := keys[:0]
	for _, key := range keys {
		if len(unique) == 0 || unique[len(unique)-1] != key {
			unique = append(unique, key)
		}
	}
	keys = unique
	entries := make([]*lockEntry, 0, len(keys))
	k.mu.Lock()
	for _, key := range keys {
		entry := k.locks[key]
		if entry == nil {
			entry = new(lockEntry)
			k.locks[key] = entry
		}
		entry.refs++
		entries = append(entries, entry)
	}
	k.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
	}
	return func() {
		for i := len(entries) - 1; i >= 0; i-- {
			entries[i].mu.Unlock()
		}
		k.mu.Lock()
		for i, key := range keys {
			entries[i].refs--
			if entries[i].refs == 0 {
				delete(k.locks, key)
			}
		}
		k.mu.Unlock()
	}
}

func randomStageName(prefix string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate %s name: %w", prefix, err)
	}
	return "rclone-123pan-" + prefix + "-" + hex.EncodeToString(id[:]), nil
}

// RecoveryError identifies only the exact objects created or renamed by this
// update; operators can recover them without matching broad name patterns.
type RecoveryError struct {
	StageID  int64
	BackupID int64
	Cause    error
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("123Pan update rollback incomplete (stage ID %d, backup ID %d): %s", e.StageID, e.BackupID, scrubSecrets(e.Cause.Error()))
}

func (e *RecoveryError) Unwrap() error { return e.Cause }

func (f *Fs) fileByID(ctx context.Context, parentID, fileID int64) (*api.File, bool, error) {
	files, err := f.listAll(ctx, parentID)
	if err != nil {
		return nil, false, err
	}
	for i := range files {
		if files[i].FileID == fileID {
			copy := files[i]
			return &copy, true, nil
		}
	}
	return nil, false, nil
}

func (f *Fs) renameByID(ctx context.Context, parentID, fileID int64, expectedOld, newName string) error {
	if fileID <= 0 {
		return errorsNewProtocol("refusing to rename invalid object ID")
	}
	request := map[string]any{"driveId": 0, "fileId": fileID, "fileName": f.opt.Enc.FromStandardName(newName)}
	err := f.client.doNonIdempotent(ctx, http.MethodPost, api.RenamePath, request, nil)
	item, found, verifyErr := f.fileByID(ctx, parentID, fileID)
	if verifyErr != nil {
		if err != nil {
			return fmt.Errorf("rename response ambiguous: %w; verify: %v", err, verifyErr)
		}
		return verifyErr
	}
	if found && item.FileName == f.opt.Enc.FromStandardName(newName) {
		return nil
	}
	if err != nil {
		return err
	}
	if !found {
		return errorsNewProtocol("renamed object ID disappeared")
	}
	return fmt.Errorf("rename postcondition failed for ID %d: expected %q from %q, got %q", fileID, newName, expectedOld, item.FileName)
}

func (f *Fs) trashExact(ctx context.Context, parentID int64, item api.File) error {
	if item.FileID <= 0 {
		return errorsNewProtocol("refusing to trash invalid object ID")
	}
	request := map[string]any{
		"driveId":           0,
		"operation":         true,
		"fileTrashInfoList": []api.File{item},
	}
	err := f.client.doNonIdempotent(ctx, http.MethodPost, api.TrashPath, request, nil)
	current, found, verifyErr := f.fileByID(ctx, parentID, item.FileID)
	if verifyErr != nil {
		if err != nil {
			return fmt.Errorf("trash response ambiguous: %w; verify: %v", err, verifyErr)
		}
		return verifyErr
	}
	if !found {
		return nil
	}
	if current.FileName != item.FileName {
		return fmt.Errorf("refusing trash retry: ID %d name changed from %q to %q", item.FileID, item.FileName, current.FileName)
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("trash postcondition failed for ID %d", item.FileID)
}

func (o *Object) refreshExact(ctx context.Context) (*api.File, error) {
	item, found, err := o.fs.fileByID(ctx, o.parentID, o.id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fs.ErrorObjectNotFound
	}
	if item.FileName != o.name || item.IsDir() {
		return nil, fmt.Errorf("object identity changed for ID %d: expected file %q, got %q type %d", o.id, o.name, item.FileName, item.Type)
	}
	return item, nil
}

func (o *Object) rollbackUpdate(ctx context.Context, stage *Object, stageName string, backup *api.File, backupName, targetName string) error {
	// Establish that the old object still exists under one of the two exact
	// transaction names before moving the verified replacement out of the
	// target path. In particular, an ambiguous backup-trash response must never
	// turn a valid new target into an empty path.
	backupID := o.id
	if backup != nil {
		backupID = backup.FileID
	}
	old, found, err := o.fs.fileByID(ctx, o.parentID, backupID)
	if err != nil {
		return fmt.Errorf("cannot establish rollback source ID %d; replacement kept in place: %w", backupID, err)
	}
	if !found {
		return fmt.Errorf("rollback source ID %d is absent; replacement kept in place", backupID)
	}
	targetServerName := o.fs.opt.Enc.FromStandardName(targetName)
	backupServerName := o.fs.opt.Enc.FromStandardName(backupName)
	switch old.FileName {
	case targetServerName:
		// The old object is already restored. The staging object can only be
		// removed after this identity check.
	case backupServerName:
		currentStage, stageFound, stageErr := o.fs.fileByID(ctx, o.parentID, stage.id)
		if stageErr != nil {
			return fmt.Errorf("inspect replacement ID %d before rollback: %w", stage.id, stageErr)
		}
		if stageFound {
			switch currentStage.FileName {
			case targetServerName:
				if err := o.fs.renameByID(ctx, o.parentID, stage.id, targetName, stageName); err != nil {
					return fmt.Errorf("move replacement ID %d aside during rollback: %w", stage.id, err)
				}
			case o.fs.opt.Enc.FromStandardName(stageName):
				// Already staged; continue with restoring the old object.
			default:
				return fmt.Errorf("replacement ID %d has unexpected name %q; refusing rollback", stage.id, currentStage.FileName)
			}
		}
		if err := o.fs.renameByID(ctx, o.parentID, backupID, backupName, targetName); err != nil {
			return fmt.Errorf("restore rollback source ID %d: %w", backupID, err)
		}
	default:
		return fmt.Errorf("rollback source ID %d has unexpected name %q; replacement kept in place", backupID, old.FileName)
	}

	restored, restoredFound, err := o.fs.fileByID(ctx, o.parentID, backupID)
	if err != nil {
		return fmt.Errorf("verify restored target ID %d: %w", backupID, err)
	}
	if !restoredFound || restored.FileName != targetServerName {
		return fmt.Errorf("rollback source ID %d was not restored to target; replacement retained for recovery", backupID)
	}
	currentStage, stageFound, err := o.fs.fileByID(ctx, o.parentID, stage.id)
	if err != nil {
		return fmt.Errorf("inspect staging ID %d after target restore: %w", stage.id, err)
	}
	if !stageFound {
		return nil
	}
	if currentStage.FileName != o.fs.opt.Enc.FromStandardName(stageName) {
		return fmt.Errorf("staging ID %d has unexpected name %q after target restore", stage.id, currentStage.FileName)
	}
	return o.fs.trashExact(ctx, o.parentID, *currentStage)
}

func (o *Object) updateLocked(ctx context.Context, in io.Reader, src fs.ObjectInfo) error {
	parentRemote, targetName := dircache.SplitPath(o.remote)
	if err := o.fs.verifyDirectoryPathID(ctx, parentRemote, o.parentID); err != nil {
		return err
	}
	old, err := o.refreshExact(ctx)
	if err != nil {
		return err
	}
	nameFn := o.fs.stageName
	if nameFn == nil {
		nameFn = randomStageName
	}
	stageName, err := nameFn("stage")
	if err != nil {
		return err
	}
	backupName, err := nameFn("backup")
	if err != nil {
		return err
	}
	stage, err := o.fs.uploadNew(ctx, in, src, o.parentID, stageName, o.remote)
	if err != nil {
		return fmt.Errorf("upload replacement staging object: %w", err)
	}
	if err := o.fs.renameByID(ctx, o.parentID, old.FileID, old.FileName, backupName); err != nil {
		rollbackErr := o.rollbackUpdate(ctx, stage, stageName, nil, backupName, targetName)
		if rollbackErr != nil {
			return &RecoveryError{StageID: stage.id, BackupID: old.FileID, Cause: errors.Join(err, rollbackErr)}
		}
		return fmt.Errorf("rename old object to backup: %w", err)
	}
	backup := *old
	backup.FileName = o.fs.opt.Enc.FromStandardName(backupName)
	if err := o.fs.renameByID(ctx, o.parentID, stage.id, stageName, targetName); err != nil {
		rollbackErr := o.rollbackUpdate(ctx, stage, stageName, &backup, backupName, targetName)
		if rollbackErr != nil {
			return &RecoveryError{StageID: stage.id, BackupID: backup.FileID, Cause: errors.Join(err, rollbackErr)}
		}
		return fmt.Errorf("promote staging object: %w", err)
	}
	final, err := o.fs.verifyUpload(ctx, o.parentID, targetName, stage.id, stage.size, stage.md5)
	if err != nil {
		rollbackErr := o.rollbackUpdate(ctx, stage, stageName, &backup, backupName, targetName)
		if rollbackErr != nil {
			return &RecoveryError{StageID: stage.id, BackupID: backup.FileID, Cause: errors.Join(err, rollbackErr)}
		}
		return err
	}
	if err := o.fs.trashExact(ctx, o.parentID, backup); err != nil {
		rollbackErr := o.rollbackUpdate(ctx, stage, stageName, &backup, backupName, targetName)
		if rollbackErr != nil {
			return &RecoveryError{StageID: stage.id, BackupID: backup.FileID, Cause: errors.Join(err, rollbackErr)}
		}
		return fmt.Errorf("trash replaced backup after rollback: %w", err)
	}
	updated := newObject(o.fs, o.remote, o.parentID, *final)
	*o = *updated
	return nil
}

// Update replaces an object through a recoverable staging/backup transaction.
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, _ ...fs.OpenOption) error {
	o.fs.ensureLocks()
	unlock := o.fs.locks.lock(
		parentMutationLockKey(o.parentID),
		objectPathLockKey(o.parentID, o.name),
	)
	defer unlock()
	return o.updateLocked(ctx, in, src)
}
