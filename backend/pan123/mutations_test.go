package pan123

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/dircache"
)

type mutationNode struct {
	file   api.File
	parent int64
}

type mutationStore struct {
	t          *testing.T
	mu         sync.Mutex
	nodes      map[int64]mutationNode
	nextID     int64
	mkdirAfter bool
	listCalls  map[int64]int
}

func newMutationStore(t *testing.T) *mutationStore {
	return &mutationStore{
		t: t,
		nodes: map[int64]mutationNode{
			1:  {file: api.File{FileName: "file", FileID: 1, Size: 1, ETag: "9dd4e461268c8034f5c8564e155c67a6"}, parent: 0},
			10: {file: api.File{FileName: "dir", FileID: 10, Type: 1}, parent: 0},
		},
		nextID:    100,
		listCalls: make(map[int64]int),
	}
}

func (s *mutationStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/b/api" + api.FileListPath:
			parent, _ := strconv.ParseInt(r.URL.Query().Get("parentFileId"), 10, 64)
			s.listCalls[parent]++
			files := make([]api.File, 0)
			for _, node := range s.nodes {
				if node.parent == parent {
					files = append(files, node.file)
				}
			}
			sort.Slice(files, func(i, j int) bool { return files[i].FileID > files[j].FileID })
			writeEnvelope(s.t, w, 0, api.FileListData{Next: "-1", Total: int64(len(files)), InfoList: files})
		case "/b/api" + api.UploadRequestPath:
			var request struct {
				Name   string `json:"fileName"`
				Parent int64  `json:"parentFileId"`
				Type   int    `json:"type"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			id := s.nextID
			s.nextID++
			s.nodes[id] = mutationNode{file: api.File{FileName: request.Name, FileID: id, Type: request.Type}, parent: request.Parent}
			if s.mkdirAfter {
				s.mkdirAfter = false
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "message": "lost mkdir response"})
				return
			}
			writeEnvelope(s.t, w, 0, api.UploadData{FileID: id})
		case "/b/api" + api.RenamePath:
			var request struct {
				ID   int64  `json:"fileId"`
				Name string `json:"fileName"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			node := s.nodes[request.ID]
			node.file.FileName = request.Name
			s.nodes[request.ID] = node
			writeEnvelope(s.t, w, 0, nil)
		case "/b/api" + api.MovePath:
			var request struct {
				Files []struct {
					ID int64 `json:"FileId"`
				} `json:"fileIdList"`
				Parent int64 `json:"parentFileId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			for _, file := range request.Files {
				node := s.nodes[file.ID]
				node.parent = request.Parent
				s.nodes[file.ID] = node
			}
			writeEnvelope(s.t, w, 0, nil)
		case "/b/api" + api.TrashPath:
			var request struct {
				Files []api.File `json:"fileTrashInfoList"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			for _, file := range request.Files {
				if node, ok := s.nodes[file.FileID]; ok && node.file.FileName == file.FileName {
					delete(s.nodes, file.FileID)
				}
			}
			writeEnvelope(s.t, w, 0, nil)
		default:
			s.t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func (s *mutationStore) get(id int64) (mutationNode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[id]
	return node, ok
}

func newMutationFs(t *testing.T, store *mutationStore) *Fs {
	t.Helper()
	client, _ := testClient(t, store.handler(), "person@example.com", "token")
	f := &Fs{
		name:      "test",
		opt:       Options{Enc: defaultEncoding, VerifyTimeout: fs.Duration(time.Second)},
		client:    client,
		uid:       42,
		locks:     newKeyedLocks(),
		stageName: func(string) (string, error) { return "rclone-123pan-move-fixed", nil },
	}
	f.dirCache = dircache.New("", "0", f)
	f.features = (&fs.Features{CanHaveEmptyDirectories: true}).Fill(context.Background(), f)
	return f
}

func TestMkdirCoordinatesLostResponseAndRmdirChecksTwice(t *testing.T) {
	store := newMutationStore(t)
	store.mkdirAfter = true
	f := newMutationFs(t, store)
	if err := f.Mkdir(context.Background(), "newdir"); err != nil {
		t.Fatal(err)
	}
	idString, err := f.dirCache.FindDir(context.Background(), "newdir", false)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(idString, 10, 64)
	before := store.listCalls[id]
	if err := f.Rmdir(context.Background(), "newdir"); err != nil {
		t.Fatal(err)
	}
	if _, found := store.get(id); found {
		t.Fatal("Rmdir did not trash the exact directory ID")
	}
	if store.listCalls[id]-before < 2 {
		t.Fatalf("empty directory was listed %d times", store.listCalls[id]-before)
	}
}

func TestRmdirRejectsRootAndNonEmpty(t *testing.T) {
	store := newMutationStore(t)
	store.nodes[11] = mutationNode{file: api.File{FileName: "child", FileID: 11}, parent: 10}
	f := newMutationFs(t, store)
	if err := f.Rmdir(context.Background(), ""); err == nil {
		t.Fatal("removed logical root")
	}
	if err := f.Rmdir(context.Background(), "dir"); err != fs.ErrorDirectoryNotEmpty {
		t.Fatalf("got %v, want directory not empty", err)
	}
}

func TestRemoveRejectsStaleIdentityAndIsIdempotent(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	stale := newObject(f, "file", 0, api.File{FileName: "wrong", FileID: 1})
	if err := stale.Remove(context.Background()); err == nil {
		t.Fatal("removed object with stale name")
	}
	valid := newObject(f, "file", 0, api.File{FileName: "file", FileID: 1, Size: 1})
	if err := valid.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := valid.Remove(context.Background()); err != nil {
		t.Fatalf("second remove not idempotent: %v", err)
	}
}

func TestMoveParentAndNameByID(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	source := newObject(f, "file", 0, api.File{FileName: "file", FileID: 1, Size: 1, ETag: "9dd4e461268c8034f5c8564e155c67a6"})
	moved, err := f.Move(context.Background(), source, "dir/renamed")
	if err != nil {
		t.Fatal(err)
	}
	node, found := store.get(1)
	if !found || node.parent != 10 || node.file.FileName != "renamed" || moved.Remote() != "dir/renamed" {
		t.Fatalf("unexpected moved node=%#v object=%#v", node, moved)
	}
}

func TestDirMoveAndSubtreeGuard(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	if err := f.DirMove(context.Background(), f, "dir", "dir/sub"); err == nil {
		t.Fatal("allowed moving directory into its subtree")
	}
	if err := f.DirMove(context.Background(), f, "dir", "renamed-dir"); err != nil {
		t.Fatal(err)
	}
	node, found := store.get(10)
	if !found || node.parent != 0 || node.file.FileName != "renamed-dir" {
		t.Fatalf("unexpected directory node %#v", node)
	}
}
