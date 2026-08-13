package pan123

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
)

const (
	listPageSize = int64(100)
	listAttempts = 3
	maxListPages = int64(1_000_000)
)

type consistencyError struct{ message string }

func (e *consistencyError) Error() string { return "123Pan inconsistent listing: " + e.message }

func validDecodedLeaf(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

func (f *Fs) listAll(ctx context.Context, parentID int64) ([]api.File, error) {
	if parentID < 0 {
		return nil, errorsNewProtocol("negative parent folder ID")
	}
	var last error
	for attempt := 1; attempt <= listAttempts; attempt++ {
		files, err := f.listOnce(ctx, parentID)
		if err == nil {
			return files, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("directory listing failed after %d complete attempts: %w", listAttempts, last)
}

func (f *Fs) listOnce(ctx context.Context, parentID int64) ([]api.File, error) {
	var (
		lockedTotal  int64 = -1
		page               = int64(1)
		previousNext string
		files        []api.File
	)
	seenIDs := make(map[int64]struct{})
	seenNames := make(map[string]int64)
	for {
		if page > maxListPages {
			return nil, &consistencyError{message: "page count exceeded safety limit"}
		}
		query := make(url.Values)
		query.Set("driveId", "0")
		query.Set("limit", strconv.FormatInt(listPageSize, 10))
		query.Set("next", "0")
		query.Set("orderBy", "file_id")
		query.Set("orderDirection", "desc")
		query.Set("parentFileId", strconv.FormatInt(parentID, 10))
		query.Set("trashed", "false")
		query.Set("SearchData", "")
		query.Set("Page", strconv.FormatInt(page, 10))
		query.Set("OnlyLookAbnormalFile", "0")
		query.Set("event", "homeListFile")
		query.Set("operateType", "4")
		query.Set("inDirectSpace", "false")
		var data api.FileListData
		if err := f.client.do(ctx, http.MethodGet, api.FileListPath+"?"+query.Encode(), nil, &data); err != nil {
			return nil, err
		}
		if data.Total < 0 {
			return nil, &consistencyError{message: "negative Total"}
		}
		if lockedTotal < 0 {
			lockedTotal = data.Total
			if pages := (lockedTotal + listPageSize - 1) / listPageSize; pages+1 > maxListPages {
				return nil, &consistencyError{message: "reported Total exceeds page safety limit"}
			}
		} else if data.Total != lockedTotal {
			return nil, &consistencyError{message: fmt.Sprintf("Total changed from %d to %d", lockedTotal, data.Total)}
		}
		if len(data.InfoList) == 0 && int64(len(files)) < lockedTotal {
			return nil, &consistencyError{message: fmt.Sprintf("empty page %d before Total", page)}
		}
		for _, item := range data.InfoList {
			if item.FileID <= 0 {
				return nil, &consistencyError{message: fmt.Sprintf("invalid object ID %d", item.FileID)}
			}
			if item.Size < 0 {
				return nil, &consistencyError{message: fmt.Sprintf("negative size for ID %d", item.FileID)}
			}
			if _, exists := seenIDs[item.FileID]; exists {
				return nil, &consistencyError{message: fmt.Sprintf("duplicate object ID %d", item.FileID)}
			}
			standardName := f.opt.Enc.ToStandardName(item.FileName)
			if !validDecodedLeaf(standardName) {
				return nil, &consistencyError{message: fmt.Sprintf("object ID %d decoded to unsafe leaf %q", item.FileID, standardName)}
			}
			if otherID, exists := seenNames[standardName]; exists {
				return nil, &consistencyError{message: fmt.Sprintf("path %q is ambiguous between IDs %d and %d", standardName, otherID, item.FileID)}
			}
			seenIDs[item.FileID] = struct{}{}
			seenNames[standardName] = item.FileID
			files = append(files, item)
		}
		if int64(len(files)) > lockedTotal {
			return nil, &consistencyError{message: fmt.Sprintf("received %d unique IDs for Total %d", len(files), lockedTotal)}
		}
		if data.Next == "-1" {
			if int64(len(files)) != lockedTotal {
				return nil, &consistencyError{message: fmt.Sprintf("terminated with %d unique IDs for Total %d", len(files), lockedTotal)}
			}
			return files, nil
		}
		if data.Next == "" {
			return nil, &consistencyError{message: "missing Next before termination"}
		}
		if data.Next == previousNext {
			return nil, &consistencyError{message: fmt.Sprintf("Next stalled at %q", data.Next)}
		}
		previousNext = data.Next
		page++
	}
}

func parseID(id string, allowRoot bool) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 0 || (!allowRoot && n == 0) {
		return 0, fmt.Errorf("invalid 123Pan object ID %q", id)
	}
	return n, nil
}

func (f *Fs) findChild(ctx context.Context, parentID int64, leaf string) (*api.File, bool, error) {
	files, err := f.listAll(ctx, parentID)
	if err != nil {
		return nil, false, err
	}
	var match *api.File
	for i := range files {
		if f.opt.Enc.ToStandardName(files[i].FileName) != leaf {
			continue
		}
		if match != nil {
			return nil, false, &consistencyError{message: fmt.Sprintf("path %q maps to IDs %d and %d", leaf, match.FileID, files[i].FileID)}
		}
		copy := files[i]
		match = &copy
	}
	return match, match != nil, nil
}

// FindLeaf implements dircache.DirCacher without creating anything.
func (f *Fs) FindLeaf(ctx context.Context, pathID, leaf string) (string, bool, error) {
	parentID, err := parseID(pathID, true)
	if err != nil {
		return "", false, err
	}
	item, found, err := f.findChild(ctx, parentID, leaf)
	if err != nil || !found {
		return "", false, err
	}
	if !item.IsDir() {
		return "", false, fs.ErrorIsFile
	}
	return strconv.FormatInt(item.FileID, 10), true, nil
}

// List returns a complete, consistency-checked directory.
func (f *Fs) List(ctx context.Context, dir string) (fs.DirEntries, error) {
	id, err := f.dirCache.FindDir(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	parentID, err := parseID(id, true)
	if err != nil {
		return nil, err
	}
	files, err := f.listAll(ctx, parentID)
	if err != nil {
		return nil, err
	}
	entries := make(fs.DirEntries, 0, len(files))
	for _, item := range files {
		remote := path.Join(dir, f.opt.Enc.ToStandardName(item.FileName))
		if item.IsDir() {
			f.dirCache.Put(remote, strconv.FormatInt(item.FileID, 10))
			entries = append(entries, fs.NewDir(remote, item.UpdateAt).SetID(strconv.FormatInt(item.FileID, 10)).SetParentID(id))
			continue
		}
		entries = append(entries, newObject(f, remote, parentID, item))
	}
	return entries, nil
}

// NewObject resolves an object using a fresh complete parent listing.
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	leaf, parent, err := f.dirCache.FindPath(ctx, remote, false)
	if err != nil {
		return nil, err
	}
	parentID, err := parseID(parent, true)
	if err != nil {
		return nil, err
	}
	item, found, err := f.findChild(ctx, parentID, leaf)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fs.ErrorObjectNotFound
	}
	if item.IsDir() {
		return nil, fs.ErrorIsDir
	}
	return newObject(f, remote, parentID, *item), nil
}

// DirCacheFlush drops all cached path-to-ID mappings.
func (f *Fs) DirCacheFlush() { f.dirCache.ResetRoot() }

// About reports account quota values.
func (f *Fs) About(ctx context.Context) (*fs.Usage, error) {
	var user api.UserInfoData
	if err := f.client.do(ctx, http.MethodGet, api.UserInfoPath, nil, &user); err != nil {
		return nil, err
	}
	total := user.SpacePermanent + user.SpaceTemp
	free := max(total-user.SpaceUsed, 0)
	return &fs.Usage{
		Total:   fs.NewUsageValue(total),
		Used:    fs.NewUsageValue(user.SpaceUsed),
		Free:    fs.NewUsageValue(free),
		Objects: fs.NewUsageValue(user.FileCount),
	}, nil
}

// UserInfo reports non-secret account metadata.
func (f *Fs) UserInfo(ctx context.Context) (map[string]string, error) {
	var user api.UserInfoData
	if err := f.client.do(ctx, http.MethodGet, api.UserInfoPath, nil, &user); err != nil {
		return nil, err
	}
	return map[string]string{
		"uid":      strconv.FormatInt(user.UID, 10),
		"nickname": user.Nickname,
	}, nil
}
