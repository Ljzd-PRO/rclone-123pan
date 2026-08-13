package pan123

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
)

type updateFault struct {
	path  string
	call  int
	after bool
}

type updateStore struct {
	t     *testing.T
	mu    sync.Mutex
	files map[int64]api.File
	calls map[string]int
	fault *updateFault
}

func newUpdateStore(t *testing.T, fault *updateFault) *updateStore {
	return &updateStore{
		t: t,
		files: map[int64]api.File{
			1: {FileName: "target", FileID: 1, Size: 3, ETag: "149603e6c03516362a8da23f624db945"},
		},
		calls: make(map[string]int),
		fault: fault,
	}
}

func (s *updateStore) shouldFault(path string) (before, after bool) {
	s.calls[path]++
	if s.fault == nil || s.fault.path != path || s.fault.call != s.calls[path] {
		return false, false
	}
	return !s.fault.after, s.fault.after
}

func (s *updateStore) fail(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "message": "injected response loss"})
}

func (s *updateStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		before, after := s.shouldFault(r.URL.Path)
		if before {
			s.fail(w)
			return
		}
		switch r.URL.Path {
		case "/b/api" + api.FileListPath:
			files := make([]api.File, 0, len(s.files))
			for _, file := range s.files {
				files = append(files, file)
			}
			sort.Slice(files, func(i, j int) bool { return files[i].FileID > files[j].FileID })
			writeEnvelope(s.t, w, 0, api.FileListData{Next: "-1", Total: int64(len(files)), InfoList: files})
		case "/b/api" + api.UploadRequestPath:
			var request struct {
				Name string `json:"fileName"`
				Size int64  `json:"size"`
				ETag string `json:"etag"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			s.files[2] = api.File{FileName: request.Name, FileID: 2, Size: request.Size, ETag: request.ETag}
			writeEnvelope(s.t, w, 0, api.UploadData{FileID: 2, Reuse: true})
		case "/b/api" + api.RenamePath:
			var request struct {
				FileID int64  `json:"fileId"`
				Name   string `json:"fileName"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			file, ok := s.files[request.FileID]
			if !ok {
				s.t.Errorf("rename missing ID %d", request.FileID)
			}
			file.FileName = request.Name
			s.files[request.FileID] = file
			if after {
				s.fail(w)
				return
			}
			writeEnvelope(s.t, w, 0, nil)
		case "/b/api" + api.TrashPath:
			var request struct {
				Files []api.File `json:"fileTrashInfoList"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			for _, requested := range request.Files {
				current, ok := s.files[requested.FileID]
				if ok && current.FileName == requested.FileName {
					delete(s.files, requested.FileID)
				}
			}
			if after {
				s.fail(w)
				return
			}
			writeEnvelope(s.t, w, 0, nil)
		default:
			s.t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func (s *updateStore) snapshot() map[int64]api.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[int64]api.File, len(s.files))
	for id, file := range s.files {
		result[id] = file
	}
	return result
}

func updateTestObject(t *testing.T, store *updateStore) *Object {
	t.Helper()
	f := newListingTestFs(t, store.handler())
	f.opt.VerifyTimeout = fs.Duration(time.Second)
	f.opt.HashMemoryLimit = 10
	f.locks = newKeyedLocks()
	names := []string{"rclone-123pan-stage-fixed", "rclone-123pan-backup-fixed"}
	f.stageName = func(prefix string) (string, error) {
		if len(names) == 0 {
			return "", fmt.Errorf("unexpected extra %s name", prefix)
		}
		name := names[0]
		names = names[1:]
		return name, nil
	}
	return newObject(f, "target", 0, api.File{FileName: "target", FileID: 1, Size: 3, ETag: "149603e6c03516362a8da23f624db945"})
}

func runUpdate(t *testing.T, store *updateStore) error {
	o := updateTestObject(t, store)
	src := object.NewStaticObjectInfo("target", time.Now(), 3, true, map[hash.Type]string{hash.MD5: "22af645d1859cb5ca6da0c484f1f37ea"}, nil)
	return o.Update(context.Background(), strings.NewReader("new"), src)
}

func assertUpdateFinal(t *testing.T, store *updateStore, newWins bool) {
	t.Helper()
	files := store.snapshot()
	if len(files) != 1 {
		t.Fatalf("unexpected recovery objects: %#v", files)
	}
	if newWins {
		if files[2].FileName != "target" || files[2].ETag != "22af645d1859cb5ca6da0c484f1f37ea" {
			t.Fatalf("new object not final: %#v", files)
		}
	} else if files[1].FileName != "target" || files[1].ETag != "149603e6c03516362a8da23f624db945" {
		t.Fatalf("old object not restored: %#v", files)
	}
}

func TestRecoverableUpdateHappyPath(t *testing.T) {
	store := newUpdateStore(t, nil)
	if err := runUpdate(t, store); err != nil {
		t.Fatal(err)
	}
	assertUpdateFinal(t, store, true)
}

func TestRecoverableUpdateCoordinatesAppliedResponseLoss(t *testing.T) {
	for _, fault := range []*updateFault{
		{path: "/b/api" + api.RenamePath, call: 1, after: true},
		{path: "/b/api" + api.RenamePath, call: 2, after: true},
		{path: "/b/api" + api.TrashPath, call: 1, after: true},
	} {
		t.Run(fmt.Sprintf("%s-%d", fault.path, fault.call), func(t *testing.T) {
			store := newUpdateStore(t, fault)
			if err := runUpdate(t, store); err != nil {
				t.Fatal(err)
			}
			assertUpdateFinal(t, store, true)
		})
	}
}

func TestRecoverableUpdateRollsBackBeforeApplyFailures(t *testing.T) {
	for _, fault := range []*updateFault{
		{path: "/b/api" + api.RenamePath, call: 1},
		{path: "/b/api" + api.RenamePath, call: 2},
		{path: "/b/api" + api.TrashPath, call: 1},
	} {
		t.Run(fmt.Sprintf("%s-%d", fault.path, fault.call), func(t *testing.T) {
			store := newUpdateStore(t, fault)
			if err := runUpdate(t, store); err == nil {
				t.Fatal("expected injected update error")
			}
			assertUpdateFinal(t, store, false)
		})
	}
}

func TestKeyedLocksStableOrder(t *testing.T) {
	locks := newKeyedLocks()
	first := locks.lock("b", "a")
	done := make(chan struct{})
	go func() {
		unlock := locks.lock("a", "b")
		unlock()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second lock entered before release")
	case <-time.After(10 * time.Millisecond):
	}
	first()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stable-order lock deadlocked")
	}
}
