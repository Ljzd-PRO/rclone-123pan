package pan123

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
)

func (f *Fs) ensureLocks() {
	if f.locks == nil {
		f.locks = locksForUID(f.uid)
	}
}

// CreateDir creates exactly one directory and coordinates an ambiguous
// response by listing the parent. dircache serializes callers of this method.
func (f *Fs) CreateDir(ctx context.Context, pathID, leaf string) (string, error) {
	parentID, err := parseID(pathID, true)
	if err != nil {
		return "", err
	}
	f.ensureLocks()
	unlock := f.locks.lock(
		parentMutationLockKey(parentID),
		objectPathLockKey(parentID, f.opt.Enc.FromStandardName(leaf)),
	)
	defer unlock()
	if existing, found, err := f.findChild(ctx, parentID, leaf); err != nil {
		return "", err
	} else if found {
		if !existing.IsDir() {
			return "", fs.ErrorIsFile
		}
		return strconv.FormatInt(existing.FileID, 10), nil
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
		return "", errors.Join(requestErr, verifyErr)
	}
	if found && created.IsDir() && created.FileID > 0 {
		if response.FileID > 0 && response.FileID != created.FileID {
			return "", fmt.Errorf("mkdir returned ID %d but visible directory has ID %d", response.FileID, created.FileID)
		}
		return strconv.FormatInt(created.FileID, 10), nil
	}
	if requestErr != nil {
		return "", requestErr
	}
	return "", errorsNewProtocol("mkdir succeeded without one visible directory")
}

// Mkdir creates a directory path without changing an existing directory.
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	_, err := f.dirCache.FindDir(ctx, dir, true)
	return err
}

// Remove moves the exact current object ID to the recycle bin.
func (o *Object) Remove(ctx context.Context) error {
	o.fs.ensureLocks()
	unlock := o.fs.locks.lock(
		parentMutationLockKey(o.parentID),
		objectPathLockKey(o.parentID, o.name),
	)
	defer unlock()
	parentRemote, _ := dircache.SplitPath(o.remote)
	if err := o.fs.verifyDirectoryPathID(ctx, parentRemote, o.parentID); err != nil {
		return err
	}
	current, found, err := o.fs.fileByID(ctx, o.parentID, o.id)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if current.FileName != o.name || current.IsDir() {
		return fmt.Errorf("refusing to remove stale object ID %d: expected file %q, got %q type %d", o.id, o.name, current.FileName, current.Type)
	}
	return o.fs.trashExact(ctx, o.parentID, *current)
}

// Rmdir removes only a non-root directory proven empty by two complete lists.
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	if strings.Trim(dir, "/") == "" {
		return errors.New("refusing to remove the logical 123Pan root")
	}
	dirIDString, err := f.dirCache.FindDir(ctx, dir, false)
	if err != nil {
		return err
	}
	dirID, err := parseID(dirIDString, false)
	if err != nil {
		return err
	}
	leaf, parentIDString, err := f.dirCache.FindPath(ctx, dir, false)
	if err != nil {
		return err
	}
	parentID, err := parseID(parentIDString, true)
	if err != nil {
		return err
	}
	f.ensureLocks()
	unlock := f.locks.lock(
		parentMutationLockKey(dirID),
		parentMutationLockKey(parentID),
		objectPathLockKey(parentID, f.opt.Enc.FromStandardName(leaf)),
	)
	defer unlock()
	parentRemote, _ := dircache.SplitPath(dir)
	if err := f.verifyDirectoryPathID(ctx, parentRemote, parentID); err != nil {
		return err
	}
	for range 2 {
		children, err := f.listAll(ctx, dirID)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fs.ErrorDirectoryNotEmpty
		}
	}
	current, found, err := f.findChild(ctx, parentID, leaf)
	if err != nil {
		return err
	}
	if !found {
		f.dirCache.FlushDir(dir)
		return nil
	}
	if current.FileID != dirID || !current.IsDir() {
		return fmt.Errorf("refusing stale Rmdir: expected directory ID %d, got ID %d type %d", dirID, current.FileID, current.Type)
	}
	if err := f.trashExact(ctx, parentID, *current); err != nil {
		return err
	}
	f.dirCache.FlushDir(dir)
	return nil
}

func (f *Fs) moveByID(ctx context.Context, fileID, sourceParent, targetParent int64) (*api.File, error) {
	if fileID <= 0 || sourceParent < 0 || targetParent < 0 {
		return nil, errorsNewProtocol("invalid move identity")
	}
	request := map[string]any{
		"fileIdList":   []map[string]any{{"FileId": fileID}},
		"parentFileId": targetParent,
	}
	requestErr := f.client.doNonIdempotent(ctx, http.MethodPost, api.MovePath, request, nil)
	if target, found, verifyErr := f.fileByID(ctx, targetParent, fileID); verifyErr == nil && found {
		return target, nil
	} else if verifyErr != nil && requestErr == nil {
		return nil, verifyErr
	}
	if source, found, verifyErr := f.fileByID(ctx, sourceParent, fileID); verifyErr != nil {
		return nil, errors.Join(requestErr, verifyErr)
	} else if found {
		if requestErr != nil {
			return nil, requestErr
		}
		return nil, fmt.Errorf("move postcondition failed: ID %d remains in parent %d as %q", fileID, sourceParent, source.FileName)
	}
	return nil, fmt.Errorf("move response ambiguous: ID %d is in neither verified parent: %w", fileID, requestErr)
}

func (f *Fs) moveItem(ctx context.Context, item api.File, sourceParent, targetParent int64, sourceName, targetName string) (*api.File, error) {
	if sourceParent == targetParent && sourceName == targetName {
		return &item, nil
	}
	if conflict, found, err := f.findChild(ctx, targetParent, targetName); err != nil {
		return nil, err
	} else if found && conflict.FileID != item.FileID {
		return nil, fmt.Errorf("move target %q already exists with ID %d", targetName, conflict.FileID)
	}
	if sourceParent == targetParent {
		if err := f.renameByID(ctx, sourceParent, item.FileID, sourceName, targetName); err != nil {
			return nil, err
		}
		return f.fileByIDRequired(ctx, targetParent, item.FileID, targetName)
	}
	if sourceName == targetName {
		moved, err := f.moveByID(ctx, item.FileID, sourceParent, targetParent)
		if err != nil {
			return nil, err
		}
		if moved.FileName != f.opt.Enc.FromStandardName(targetName) {
			return nil, errorsNewProtocol("move changed object name unexpectedly")
		}
		return moved, nil
	}
	nameFn := f.stageName
	if nameFn == nil {
		nameFn = randomStageName
	}
	temporary, err := nameFn("move")
	if err != nil {
		return nil, err
	}
	if err := f.renameByID(ctx, sourceParent, item.FileID, sourceName, temporary); err != nil {
		return nil, err
	}
	if _, err := f.moveByID(ctx, item.FileID, sourceParent, targetParent); err != nil {
		rollback := f.renameByID(ctx, sourceParent, item.FileID, temporary, sourceName)
		if rollback != nil {
			return nil, &RecoveryError{StageID: item.FileID, Cause: errors.Join(err, rollback)}
		}
		return nil, err
	}
	if err := f.renameByID(ctx, targetParent, item.FileID, temporary, targetName); err != nil {
		_, moveBackErr := f.moveByID(ctx, item.FileID, targetParent, sourceParent)
		renameBackErr := f.renameByID(ctx, sourceParent, item.FileID, temporary, sourceName)
		if moveBackErr != nil || renameBackErr != nil {
			return nil, &RecoveryError{StageID: item.FileID, Cause: errors.Join(err, moveBackErr, renameBackErr)}
		}
		return nil, err
	}
	return f.fileByIDRequired(ctx, targetParent, item.FileID, targetName)
}

func (f *Fs) fileByIDRequired(ctx context.Context, parentID, fileID int64, standardName string) (*api.File, error) {
	item, found, err := f.fileByID(ctx, parentID, fileID)
	if err != nil {
		return nil, err
	}
	if !found || item.FileName != f.opt.Enc.FromStandardName(standardName) {
		return nil, fmt.Errorf("object ID %d did not reach parent %d with name %q", fileID, parentID, standardName)
	}
	return item, nil
}

// Move performs an ID-verified server-side move within one account.
func (f *Fs) Move(ctx context.Context, src fs.Object, remote string) (fs.Object, error) {
	source, ok := src.(*Object)
	if !ok || source.fs.uid != f.uid {
		return nil, fs.ErrorCantMove
	}
	if source.remote == remote && source.fs == f {
		return source, nil
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
	moved, err := f.moveItem(ctx, *current, source.parentID, targetParent, source.fs.opt.Enc.ToStandardName(current.FileName), targetLeaf)
	if err != nil {
		return nil, err
	}
	return newObject(f, remote, targetParent, *moved), nil
}

// DirMove performs a safe server-side directory move within one account.
func (f *Fs) DirMove(ctx context.Context, src fs.Fs, srcRemote, dstRemote string) error {
	source, ok := src.(*Fs)
	if !ok || source.uid != f.uid {
		return fs.ErrorCantDirMove
	}
	sourcePath := strings.Trim(path.Join(source.root, srcRemote), "/")
	targetPath := strings.Trim(path.Join(f.root, dstRemote), "/")
	if sourcePath == "" || targetPath == "" || targetPath == sourcePath || strings.HasPrefix(targetPath, sourcePath+"/") {
		return fmt.Errorf("refusing invalid directory move from %q to %q", sourcePath, targetPath)
	}
	sourceIDString, sourceParentString, sourceLeaf, targetParentString, targetLeaf, err := f.dirCache.DirMove(ctx, source.dirCache, source.root, srcRemote, f.root, dstRemote)
	if err != nil {
		return err
	}
	sourceID, err := parseID(sourceIDString, false)
	if err != nil {
		return err
	}
	sourceParent, err := parseID(sourceParentString, true)
	if err != nil {
		return err
	}
	targetParent, err := parseID(targetParentString, true)
	if err != nil {
		return err
	}
	f.ensureLocks()
	unlock := f.locks.lock(
		parentMutationLockKey(sourceParent),
		parentMutationLockKey(targetParent),
		objectPathLockKey(sourceParent, source.opt.Enc.FromStandardName(sourceLeaf)),
		objectPathLockKey(targetParent, f.opt.Enc.FromStandardName(targetLeaf)),
	)
	defer unlock()
	sourceDir, _ := dircache.SplitPath(srcRemote)
	if err := source.verifyDirectoryPathID(ctx, sourceDir, sourceParent); err != nil {
		return err
	}
	targetDir, _ := dircache.SplitPath(dstRemote)
	if err := f.verifyDirectoryPathID(ctx, targetDir, targetParent); err != nil {
		return err
	}
	item, found, err := source.fileByID(ctx, sourceParent, sourceID)
	if err != nil {
		return err
	}
	if !found || !item.IsDir() || item.FileName != source.opt.Enc.FromStandardName(sourceLeaf) {
		return fmt.Errorf("source directory identity changed for ID %d", sourceID)
	}
	if _, err := f.moveItem(ctx, *item, sourceParent, targetParent, sourceLeaf, targetLeaf); err != nil {
		return err
	}
	source.dirCache.FlushDir(srcRemote)
	f.dirCache.FlushDir(dstRemote)
	return nil
}

var _ dircache.DirCacher = (*Fs)(nil)
