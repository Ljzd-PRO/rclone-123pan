package pan123

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

// Object is an immutable metadata snapshot. Destructive methods revalidate it
// by ID before changing remote state.
type Object struct {
	fs       *Fs
	remote   string
	parentID int64
	id       int64
	name     string
	size     int64
	modTime  time.Time
	md5      string
	metadata api.File
}

func newObject(f *Fs, remote string, parentID int64, item api.File) *Object {
	return &Object{
		fs:       f,
		remote:   remote,
		parentID: parentID,
		id:       item.FileID,
		name:     item.FileName,
		size:     item.Size,
		modTime:  item.UpdateAt,
		md5:      normalizeMD5(item.ETag),
		metadata: item,
	}
}

func normalizeMD5(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func (o *Object) Fs() fs.Info                       { return o.fs }
func (o *Object) String() string                    { return o.remote }
func (o *Object) Remote() string                    { return o.remote }
func (o *Object) ModTime(context.Context) time.Time { return o.modTime }
func (o *Object) Size() int64                       { return o.size }
func (o *Object) Storable() bool                    { return true }
func (o *Object) ID() string                        { return strconv.FormatInt(o.id, 10) }

func (o *Object) Hash(_ context.Context, ty hash.Type) (string, error) {
	if ty != hash.MD5 {
		return "", hash.ErrUnsupported
	}
	return o.md5, nil
}

func (o *Object) SetModTime(context.Context, time.Time) error { return fs.ErrorCantSetModTime }

var _ fs.Object = (*Object)(nil)
var _ fs.IDer = (*Object)(nil)
