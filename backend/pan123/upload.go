package pan123

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/accounting"
	"github.com/rclone/rclone/fs/hash"
)

type preparedSource struct {
	reader  io.Reader
	size    int64
	md5     string
	cleanup func() error
}

func prepareSource(ctx context.Context, in io.Reader, src fs.ObjectInfo, memoryLimit int64) (*preparedSource, error) {
	if memoryLimit < 0 {
		return nil, errorsNewConfig("hash_memory_limit must not be negative")
	}
	size := src.Size()
	sum, hashErr := src.Hash(ctx, hash.MD5)
	sum = normalizeMD5(sum)
	if hashErr == nil && sum != "" && size >= 0 {
		return &preparedSource{reader: in, size: size, md5: sum, cleanup: func() error { return nil }}, nil
	}
	if hashErr != nil && !errors.Is(hashErr, hash.ErrUnsupported) {
		return nil, fmt.Errorf("read source MD5: %w", hashErr)
	}

	unwrapped, wrap := accounting.UnWrap(in)
	if size >= 0 && size <= memoryLimit {
		buffer := make([]byte, size)
		n, err := io.ReadFull(unwrapped, buffer)
		if err != nil {
			return nil, fmt.Errorf("cache source for MD5: %w", err)
		}
		var extra [1]byte
		if extraN, extraErr := unwrapped.Read(extra[:]); extraN != 0 || (extraErr != nil && extraErr != io.EOF) {
			return nil, fmt.Errorf("source size changed: declared %d bytes but more data followed", size)
		}
		if int64(n) != size {
			return nil, fmt.Errorf("source size changed: declared %d bytes, read %d", size, n)
		}
		digest := md5.Sum(buffer)
		return &preparedSource{
			reader:  wrap(bytes.NewReader(buffer)),
			size:    size,
			md5:     hex.EncodeToString(digest[:]),
			cleanup: func() error { return nil },
		}, nil
	}

	temp, err := os.CreateTemp("", "rclone-123pan-hash-*")
	if err != nil {
		return nil, fmt.Errorf("create hash spool in temp directory: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() error {
		closeErr := temp.Close()
		removeErr := os.Remove(tempName)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
		return closeErr
	}
	hasher := md5.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), unwrapped)
	if err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("spool source for MD5: %w", err)
	}
	if size >= 0 && written != size {
		_ = cleanup()
		return nil, fmt.Errorf("source size changed: declared %d bytes, read %d", size, written)
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		_ = cleanup()
		return nil, fmt.Errorf("rewind hash spool: %w", err)
	}
	return &preparedSource{
		reader:  wrap(temp),
		size:    written,
		md5:     hex.EncodeToString(hasher.Sum(nil)),
		cleanup: cleanup,
	}, nil
}

type uploadPostconditionError struct {
	FileID int64
	Reason string
}

func (e *uploadPostconditionError) Error() string {
	return fmt.Sprintf("123Pan upload postcondition failed for file ID %d: %s", e.FileID, e.Reason)
}

func (f *Fs) requestUpload(ctx context.Context, parentID int64, name string, source *preparedSource) (api.UploadData, error) {
	request := map[string]any{
		"driveId": 0,
		// 2 is the personal-web API's overwrite mode used by the fixed
		// OpenList reference. The backend has already serialized and freshly
		// checked this exact target before issuing the request.
		"duplicate": 2,
		"etag":      source.md5,
		"fileName":  f.opt.Enc.FromStandardName(name),
		// The personal web client and the fixed OpenList reference serialize
		// directory IDs as decimal strings. Sending a JSON number is accepted by
		// some endpoints but can allocate an upload which never becomes visible.
		"parentFileId": strconv.FormatInt(parentID, 10),
		"size":         source.size,
		"type":         0,
	}
	var upload api.UploadData
	// Upload requests allocate a server-side file/upload ID. A transport or
	// 5xx response is therefore ambiguous and must not be replayed blindly.
	if err := f.client.doNonIdempotent(ctx, http.MethodPost, api.UploadRequestPath, request, &upload); err != nil {
		return api.UploadData{}, err
	}
	credentialCount := 0
	for _, value := range []string{upload.AccessKeyID, upload.SecretAccessKey, upload.SessionToken} {
		if value != "" {
			credentialCount++
		}
	}
	fs.Debugf(f, "123Pan upload request response: file_id=%d reuse=%t key_present=%t upload_id_present=%t temporary_credentials=%d", upload.FileID, upload.Reuse, upload.Key != "", upload.UploadID != "", credentialCount)
	if upload.FileID <= 0 {
		return api.UploadData{}, errorsNewProtocol("upload request returned an invalid file ID")
	}
	if !upload.Reuse && upload.Key == "" {
		return api.UploadData{}, errorsNewProtocol("upload request returned neither reuse nor an upload key")
	}
	return upload, nil
}

func (f *Fs) verifyUpload(ctx context.Context, parentID int64, name string, fileID, size int64, sum string) (*api.File, error) {
	deadline := time.Now().Add(time.Duration(f.opt.VerifyTimeout))
	serverName := f.opt.Enc.FromStandardName(name)
	var last string
	for {
		files, err := f.listAll(ctx, parentID)
		if err == nil {
			for i := range files {
				item := &files[i]
				if item.FileID != fileID {
					continue
				}
				if item.FileName == serverName && item.Size == size && normalizeMD5(item.ETag) == sum && !item.IsDir() {
					return item, nil
				}
				last = fmt.Sprintf("metadata mismatch: name=%q size=%d md5=%q type=%d", item.FileName, item.Size, normalizeMD5(item.ETag), item.Type)
			}
			if last == "" {
				last = "file ID is not visible"
			}
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return nil, &uploadPostconditionError{FileID: fileID, Reason: scrubSecrets(last)}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// inspectUpload performs one complete, consistency-checked listing and reports
// whether the exact upload result is already visible. A visible ID with
// mismatched metadata is an identity conflict, not an ordinary "not yet"
// result, so callers must fail closed instead of uploading over it.
func (f *Fs) inspectUpload(ctx context.Context, parentID int64, name string, fileID, size int64, sum string) (*api.File, bool, error) {
	files, err := f.listAll(ctx, parentID)
	if err != nil {
		return nil, false, err
	}
	serverName := f.opt.Enc.FromStandardName(name)
	for i := range files {
		item := &files[i]
		if item.FileID != fileID {
			continue
		}
		if item.FileName != serverName || item.Size != size || normalizeMD5(item.ETag) != sum || item.IsDir() {
			return nil, false, &uploadPostconditionError{
				FileID: fileID,
				Reason: fmt.Sprintf("metadata mismatch: name=%q size=%d md5=%q type=%d", item.FileName, item.Size, normalizeMD5(item.ETag), item.Type),
			}
		}
		return item, true, nil
	}
	return nil, false, nil
}

func (f *Fs) rapidUpload(ctx context.Context, parentID int64, name string, source *preparedSource, input io.Reader) (*Object, api.UploadData, error) {
	upload, err := f.requestUpload(ctx, parentID, name, source)
	if err != nil {
		return nil, api.UploadData{}, err
	}
	if !upload.Reuse {
		return nil, upload, nil
	}
	item, visible, err := f.inspectUpload(ctx, parentID, name, upload.FileID, source.size, source.md5)
	if err != nil {
		return nil, upload, err
	}
	// The fixed OpenList reference treats Reuse=true as an unconditional
	// rapid-upload hit. The live personal API can now return Reuse=true together
	// with an upload key while the object is not visible. In that response shape
	// the key is the only verified path to completing the upload, so continue
	// with data transfer. Reuse without a key still has no upload path and must
	// wait for the exact object postcondition.
	if !visible && upload.Key != "" {
		fs.Infof(f, "123Pan reuse candidate ID %d is not visible; using the supplied upload key", upload.FileID)
		return nil, upload, nil
	}
	if !visible {
		item, err = f.verifyUpload(ctx, parentID, name, upload.FileID, source.size, source.md5)
		if err != nil {
			return nil, upload, err
		}
	}
	if _, acc := accounting.UnWrapAccounting(input); acc != nil {
		acc.ServerSideTransferStart()
		acc.ServerSideTransferEnd(source.size)
	}
	return newObject(f, name, parentID, *item), upload, nil
}

func tempSpoolName(source *preparedSource) string {
	file, ok := source.reader.(*os.File)
	if !ok {
		return ""
	}
	return filepath.Base(file.Name())
}

func (f *Fs) uploadNew(ctx context.Context, in io.Reader, src fs.ObjectInfo, parentID int64, name, remote string) (*Object, error) {
	prepared, err := prepareSource(ctx, in, src, int64(f.opt.HashMemoryLimit))
	if err != nil {
		return nil, err
	}
	defer prepared.cleanup()
	o, upload, err := f.rapidUpload(ctx, parentID, name, prepared, in)
	if err != nil {
		return nil, err
	}
	if o != nil {
		o.remote = remote
		return o, nil
	}
	if err := f.uploadData(ctx, upload, prepared); err != nil {
		var ambiguous *ambiguousCompleteError
		if errors.As(err, &ambiguous) {
			item, verifyErr := f.verifyUpload(ctx, parentID, name, upload.FileID, prepared.size, prepared.md5)
			if verifyErr == nil {
				return newObject(f, remote, parentID, *item), nil
			}
			return nil, fmt.Errorf("%w; postcondition check also failed: %v", err, verifyErr)
		}
		return nil, fmt.Errorf("upload data for file ID %d: %w", upload.FileID, err)
	}
	item, verifyErr := f.verifyUpload(ctx, parentID, name, upload.FileID, prepared.size, prepared.md5)
	if verifyErr != nil {
		return nil, verifyErr
	}
	return newObject(f, remote, parentID, *item), nil
}
