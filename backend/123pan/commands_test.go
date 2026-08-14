package _123pan

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
)

type offlineStore struct {
	t     *testing.T
	mu    sync.Mutex
	tasks map[int64]api.OfflineTask
}

func (s *offlineStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/b/api" + api.OfflineResolvePath:
			var data api.OfflineResolveData
			data.List = append(data.List, struct {
				Result  int    `json:"result"`
				ID      int64  `json:"id"`
				ErrCode int    `json:"err_code"`
				ErrMsg  string `json:"err_msg"`
				Files   []struct {
					ID int64 `json:"id"`
				} `json:"files"`
			}{Result: 0, ID: 80, Files: []struct {
				ID int64 `json:"id"`
			}{{ID: 81}}})
			writeEnvelope(s.t, w, 0, data)
		case "/b/api" + api.OfflineSubmitPath:
			var request struct {
				UploadDir int64 `json:"upload_dir"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			if request.UploadDir != 0 {
				s.t.Errorf("upload dir = %d", request.UploadDir)
			}
			var data api.OfflineSubmitData
			data.TaskList = append(data.TaskList, struct {
				TaskID int64 `json:"task_id"`
				Result int   `json:"result"`
			}{TaskID: 99})
			s.tasks[99] = api.OfflineTask{TaskID: 99, Name: "queued", Status: 0}
			writeEnvelope(s.t, w, 0, data)
		case "/b/api" + api.OfflineTaskListPath:
			list := make([]api.OfflineTask, 0, len(s.tasks))
			for _, task := range s.tasks {
				list = append(list, task)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].TaskID < list[j].TaskID })
			writeEnvelope(s.t, w, 0, api.OfflineTaskListData{Total: int64(len(list)), List: list})
		case "/b/api" + api.OfflineTaskDeletePath:
			var request struct {
				IDs []int64 `json:"task_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			for _, id := range request.IDs {
				delete(s.tasks, id)
			}
			writeEnvelope(s.t, w, 0, nil)
		default:
			s.t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func newOfflineFs(t *testing.T, store *offlineStore) *Fs {
	t.Helper()
	client, _ := testClient(t, store.handler(), "person@example.com", "token")
	f := &Fs{client: client}
	f.dirCache = dircache.New("", "0", f)
	return f
}

func TestOfflineCommandsLifecycleAndUnknownStatus(t *testing.T) {
	store := &offlineStore{t: t, tasks: map[int64]api.OfflineTask{
		7: {TaskID: 7, Name: "mystery", Status: 91, Size: 10, Downloaded: 2, Progress: 20, Speed: 3, UploadName: "x"},
	}}
	f := newOfflineFs(t, store)
	added, err := f.Command(context.Background(), "offline-add", []string{"https://example.test/file"}, nil)
	if err != nil || added.(map[string]int64)["task_id"] != 99 {
		t.Fatalf("added=%#v err=%v", added, err)
	}
	statusesRaw, err := f.Command(context.Background(), "offline-status", []string{"7", "99"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	statuses := statusesRaw.([]OfflineStatus)
	if statuses[0].OriginalStatus != 91 || statuses[0].NormalizedStatus != "unknown" || statuses[1].NormalizedStatus != "downloading" {
		t.Fatalf("unexpected statuses %#v", statuses)
	}
	deleted, err := f.Command(context.Background(), "offline-delete", []string{"7", "99"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.(map[string][]int64)["deleted_task_ids"]) != 2 {
		t.Fatalf("unexpected delete result %#v", deleted)
	}
	if _, err := f.Command(context.Background(), "offline-status", []string{"7"}, nil); err == nil {
		t.Fatal("deleted task remained visible")
	}
}

func TestOfflineCommandValidation(t *testing.T) {
	store := &offlineStore{t: t, tasks: make(map[int64]api.OfflineTask)}
	f := newOfflineFs(t, store)
	for _, args := range [][]string{nil, {"0"}, {"-1"}, {"abc"}, {"1", "1"}} {
		if _, err := f.Command(context.Background(), "offline-status", args, nil); err == nil {
			t.Fatalf("accepted invalid IDs %#v", args)
		}
	}
	if _, err := f.Command(context.Background(), "offline-add", nil, nil); err == nil {
		t.Fatal("accepted missing URL")
	}
	if _, err := f.Command(context.Background(), "missing", nil, nil); err != fs.ErrorCommandNotFound {
		t.Fatalf("got %v, want command not found", err)
	}
}

func TestOfflineStatusMapping(t *testing.T) {
	want := map[int]string{0: "downloading", 1: "failed", 2: "complete", 3: "retrying", 99: "unknown"}
	for input, expected := range want {
		if got := normalizeOfflineStatus(input); got != expected {
			t.Fatalf("status %d = %q, want %q", input, got, expected)
		}
	}
}
