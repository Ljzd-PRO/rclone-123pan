// Command rclone builds rclone with the out-of-tree 123Pan backend.
package main

import (
	_ "github.com/ljzd/rclone-123pan/backend/123pan"
	_ "github.com/rclone/rclone/backend/all"
	"github.com/rclone/rclone/cmd"
	_ "github.com/rclone/rclone/cmd/all"
	_ "github.com/rclone/rclone/lib/plugin"
)

func main() {
	cmd.Main()
}
