package _123pan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
)

var commandHelp = []fs.CommandHelp{
	{
		Name:  "offline-add",
		Short: "解析一个 URL 并添加为 123 网盘离线下载任务。",
		Long:  "remote 路径即保存目录。必须提供且只能提供一个 URL 参数；返回包含 task_id 的 JSON 对象。",
	},
	{
		Name:  "offline-status",
		Short: "返回一个或多个离线任务 ID 的稳定状态 JSON。",
		Long:  "每个参数都必须是正整数任务 ID。未知服务端状态会保留原值，并把 normalized_status 标记为 unknown。",
	},
	{
		Name:  "offline-delete",
		Short: "删除一个或多个离线任务 ID，并确认其消失。",
		Long:  "每个参数都必须是正整数。删除后会重新查询任务，只有全部指定 ID 均不可见时才报告成功。",
	},
}

var errOfflineTaskNotFound = errors.New("123Pan offline task not found")

// OfflineStatus is stable command/RC JSON independent of protocol DTO names.
type OfflineStatus struct {
	TaskID           int64   `json:"task_id"`
	Name             string  `json:"name"`
	OriginalStatus   int     `json:"original_status"`
	NormalizedStatus string  `json:"normalized_status"`
	Size             int64   `json:"size"`
	Downloaded       int64   `json:"downloaded"`
	Progress         float64 `json:"progress"`
	Speed            int64   `json:"speed"`
	UploadName       string  `json:"upload_name"`
}

func normalizeOfflineStatus(status int) string {
	switch status {
	case 0:
		return "downloading"
	case 1:
		return "failed"
	case 2:
		return "complete"
	case 3:
		return "retrying"
	default:
		return "unknown"
	}
}

func parseTaskIDs(args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, errors.New("at least one positive task ID is required")
	}
	ids := make([]int64, len(args))
	seen := make(map[int64]struct{}, len(args))
	for i, value := range args {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid offline task ID %q: must be a positive integer", value)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate offline task ID %d", id)
		}
		seen[id] = struct{}{}
		ids[i] = id
	}
	return ids, nil
}

func (f *Fs) offlineAdd(ctx context.Context, rawURL string) (int64, error) {
	var resolved api.OfflineResolveData
	if err := f.client.do(ctx, http.MethodPost, api.OfflineResolvePath, map[string]any{"urls": rawURL}, &resolved); err != nil {
		return 0, err
	}
	if len(resolved.List) != 1 {
		return 0, errorsNewProtocol("offline resolve did not return exactly one result")
	}
	result := resolved.List[0]
	if result.Result != 0 || result.ID <= 0 {
		message := result.ErrMsg
		if message == "" {
			message = fmt.Sprintf("resolve result %d error code %d", result.Result, result.ErrCode)
		}
		return 0, fmt.Errorf("offline resolve failed: %s", scrubSecrets(message))
	}
	fileIDs := make([]int64, 0, len(result.Files))
	for _, file := range result.Files {
		if file.ID <= 0 {
			return 0, errorsNewProtocol("offline resolve returned an invalid file ID")
		}
		fileIDs = append(fileIDs, file.ID)
	}
	if len(fileIDs) == 0 {
		return 0, errorsNewProtocol("offline resolve returned no files")
	}
	rootIDString, err := f.dirCache.RootID(ctx, false)
	if err != nil {
		return 0, err
	}
	rootID, err := parseID(rootIDString, true)
	if err != nil {
		return 0, err
	}
	request := map[string]any{
		"resource_list": []map[string]any{{"resource_id": result.ID, "select_file_id": fileIDs}},
		"upload_dir":    rootID,
	}
	var submitted api.OfflineSubmitData
	if err := f.client.doNonIdempotent(ctx, http.MethodPost, api.OfflineSubmitPath, request, &submitted); err != nil {
		return 0, err
	}
	if len(submitted.TaskList) != 1 || submitted.TaskList[0].Result != 0 || submitted.TaskList[0].TaskID <= 0 {
		return 0, errorsNewProtocol("offline submit did not return one successful positive task ID")
	}
	return submitted.TaskList[0].TaskID, nil
}

func (f *Fs) listOfflineTasks(ctx context.Context) (map[int64]api.OfflineTask, error) {
	const pageSize = int64(100)
	result := make(map[int64]api.OfflineTask)
	var lockedTotal int64 = -1
	for page := int64(1); ; page++ {
		if page > maxListPages {
			return nil, errorsNewProtocol("offline task pagination exceeded safety limit")
		}
		var data api.OfflineTaskListData
		request := map[string]any{
			"current_page": page,
			"page_size":    pageSize,
			"status_arr":   []int{0, 1, 2, 3},
		}
		if err := f.client.do(ctx, http.MethodPost, api.OfflineTaskListPath, request, &data); err != nil {
			return nil, err
		}
		if data.Total < 0 {
			return nil, errorsNewProtocol("offline task list returned negative Total")
		}
		if lockedTotal < 0 {
			lockedTotal = data.Total
		} else if data.Total != lockedTotal {
			return nil, errorsNewProtocol("offline task Total changed during pagination")
		}
		for _, task := range data.List {
			if task.TaskID <= 0 {
				return nil, errorsNewProtocol("offline task list returned invalid task ID")
			}
			if _, exists := result[task.TaskID]; exists {
				return nil, fmt.Errorf("offline task ID %d appeared more than once", task.TaskID)
			}
			result[task.TaskID] = task
		}
		if int64(len(result)) > lockedTotal {
			return nil, errorsNewProtocol("offline task list exceeded Total")
		}
		if len(data.List) == 0 || page*pageSize >= lockedTotal {
			if int64(len(result)) != lockedTotal {
				return nil, fmt.Errorf("offline task list ended with %d unique IDs for Total %d", len(result), lockedTotal)
			}
			return result, nil
		}
	}
}

func (f *Fs) offlineStatuses(ctx context.Context, ids []int64) ([]OfflineStatus, error) {
	tasks, err := f.listOfflineTasks(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]OfflineStatus, 0, len(ids))
	for _, id := range ids {
		task, found := tasks[id]
		if !found {
			return nil, fmt.Errorf("%w: %d", errOfflineTaskNotFound, id)
		}
		result = append(result, OfflineStatus{
			TaskID:           task.TaskID,
			Name:             task.Name,
			OriginalStatus:   task.Status,
			NormalizedStatus: normalizeOfflineStatus(task.Status),
			Size:             task.Size,
			Downloaded:       task.Downloaded,
			Progress:         task.Progress,
			Speed:            task.Speed,
			UploadName:       task.UploadName,
		})
	}
	return result, nil
}

func (f *Fs) offlineDelete(ctx context.Context, ids []int64) error {
	requestErr := f.client.doNonIdempotent(ctx, http.MethodPost, api.OfflineTaskDeletePath, map[string]any{"task_ids": ids}, nil)
	tasks, verifyErr := f.listOfflineTasks(ctx)
	if verifyErr != nil {
		return errors.Join(requestErr, verifyErr)
	}
	for _, id := range ids {
		if _, found := tasks[id]; found {
			if requestErr != nil {
				return requestErr
			}
			return fmt.Errorf("offline task %d is still visible after delete", id)
		}
	}
	return nil
}

// Command exposes offline tasks through rclone backend and backend/command RC.
func (f *Fs) Command(ctx context.Context, name string, args []string, opts map[string]string) (any, error) {
	if len(opts) != 0 {
		return nil, fmt.Errorf("command %q does not accept options", name)
	}
	switch name {
	case "offline-add":
		if len(args) != 1 || args[0] == "" {
			return nil, errors.New("offline-add requires exactly one non-empty URL")
		}
		id, err := f.offlineAdd(ctx, args[0])
		if err != nil {
			return nil, err
		}
		return map[string]int64{"task_id": id}, nil
	case "offline-status":
		ids, err := parseTaskIDs(args)
		if err != nil {
			return nil, err
		}
		return f.offlineStatuses(ctx, ids)
	case "offline-delete":
		ids, err := parseTaskIDs(args)
		if err != nil {
			return nil, err
		}
		if err := f.offlineDelete(ctx, ids); err != nil {
			return nil, err
		}
		return map[string][]int64{"deleted_task_ids": ids}, nil
	default:
		return nil, fs.ErrorCommandNotFound
	}
}
