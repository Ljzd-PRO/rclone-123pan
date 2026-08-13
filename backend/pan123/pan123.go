// Package pan123 implements an experimental 123Pan personal account backend.
package pan123

import (
	"context"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
)

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "123pan",
		Prefix:      "pan123",
		Description: "123Pan personal account (experimental)",
		NewFs:       NewFs,
	})
}

// NewFs is the backend constructor. The implementation is added in later
// milestones; keeping the constructor explicit makes this scaffold buildable.
func NewFs(context.Context, string, string, configmap.Mapper) (fs.Fs, error) {
	return nil, fs.ErrorNotImplemented
}
