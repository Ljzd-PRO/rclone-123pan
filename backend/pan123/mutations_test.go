package pan123

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
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
	t                  *testing.T
	mu                 sync.Mutex
	nodes              map[int64]mutationNode
	nextID             int64
	mkdirAfter         bool
	listCalls          map[int64]int
	copyMode           int
	copyStatuses       []int
	copyTaskCalls      int
	copyStartCalls     int
	copyLoseStart      bool
	copyNoMaterialize  bool
	copyCorrupt        bool
	lastCopyRequest    api.CopyRequest
	pendingCopyRequest *api.CopyRequest
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
		copyMode:  2,
	}
}

func (s *mutationStore) materializeCopy(request api.CopyRequest) {
	if s.copyNoMaterialize || len(request.FileList) != 1 {
		return
	}
	source, found := s.nodes[request.FileList[0].FileID]
	target, targetFound := s.nodes[request.TargetFileID]
	if !found || !targetFound || !target.file.IsDir() {
		return
	}
	for _, node := range s.nodes {
		if node.parent == request.TargetFileID && node.file.FileName == source.file.FileName {
			return
		}
	}
	id := s.nextID
	s.nextID++
	file := source.file
	file.FileID = id
	file.ParentFileID = request.TargetFileID
	if s.copyCorrupt {
		file.ETag = "00000000000000000000000000000000"
	}
	s.nodes[id] = mutationNode{file: file, parent: request.TargetFileID}
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
					file := node.file
					file.ParentFileID = parent
					files = append(files, file)
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
		case "/b/api" + api.CopyStartPath:
			s.copyStartCalls++
			var request api.CopyRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				s.t.Error(err)
			}
			s.lastCopyRequest = request
			if s.copyMode == 2 {
				s.materializeCopy(request)
			} else {
				copy := request
				s.pendingCopyRequest = &copy
			}
			if s.copyLoseStart {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 500, "message": "lost Copy response"})
				return
			}
			writeEnvelope(s.t, w, 0, map[string]any{"mode": s.copyMode, "taskId": 77})
		case "/b/api" + api.CopyTaskPath:
			if r.URL.Query().Get("taskId") != "77" {
				s.t.Errorf("unexpected Copy task ID %q", r.URL.Query().Get("taskId"))
			}
			status := 2
			if len(s.copyStatuses) != 0 {
				index := min(s.copyTaskCalls, len(s.copyStatuses)-1)
				status = s.copyStatuses[index]
			}
			s.copyTaskCalls++
			if status == 2 && s.pendingCopyRequest != nil {
				s.materializeCopy(*s.pendingCopyRequest)
				s.pendingCopyRequest = nil
			}
			writeEnvelope(s.t, w, 0, map[string]any{"status": status, "reason": "mock task failure"})
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
	var stageCounter int
	f := &Fs{
		name:   "test",
		opt:    Options{Enc: defaultEncoding, RootFolderID: "0", VerifyTimeout: fs.Duration(time.Second)},
		client: client,
		uid:    42,
		locks:  newKeyedLocks(),
		stageName: func(prefix string) (string, error) {
			stageCounter++
			return fmt.Sprintf("rclone-123pan-%s-fixed-%d", prefix, stageCounter), nil
		},
		copyPollInterval: time.Millisecond,
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

func TestConcurrentMkdirAcrossRemotesCreatesOneID(t *testing.T) {
	store := newMutationStore(t)
	first := newMutationFs(t, store)
	second := newMutationFs(t, store)
	const uid = int64(902100000101)
	first.uid, second.uid = uid, uid
	first.locks, second.locks = locksForUID(uid), locksForUID(uid)

	ids := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, remote := range []*Fs{first, second} {
		wg.Add(1)
		go func(f *Fs) {
			defer wg.Done()
			id, err := f.CreateDir(context.Background(), "0", "same")
			ids <- id
			errs <- err
		}(remote)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var expected string
	for id := range ids {
		if expected == "" {
			expected = id
		} else if id != expected {
			t.Fatalf("concurrent mkdir returned IDs %q and %q", expected, id)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, node := range store.nodes {
		if node.parent == 0 && node.file.FileName == "same" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent mkdir created %d server objects", count)
	}
}

func TestWriteParentFreshWalkRejectsStaleDircacheID(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	if err := f.verifyDirectoryPathID(context.Background(), "dir", 10); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	delete(store.nodes, 10)
	store.mu.Unlock()
	if err := f.verifyDirectoryPathID(context.Background(), "dir", 10); err == nil {
		t.Fatal("accepted a deleted parent directory ID")
	}
}

func TestResolveRootAllowsMissingDirectoryForMkdir(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	f.root = "new-root"
	f.dirCache = dircache.New(f.root, "0", f)
	if err := f.resolveRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.Mkdir(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	item, found, err := f.findChild(context.Background(), 0, "new-root")
	if err != nil || !found || !item.IsDir() {
		t.Fatalf("missing root was not created: item=%#v found=%v err=%v", item, found, err)
	}
}

func TestResolveRootReturnsIsFileAndParentFs(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	f.root = "file"
	f.dirCache = dircache.New(f.root, "0", f)
	if err := f.resolveRoot(context.Background()); err != fs.ErrorIsFile {
		t.Fatalf("got %v, want ErrorIsFile", err)
	}
	if f.root != "" {
		t.Fatalf("file root was not adjusted to parent: %q", f.root)
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

func TestRmdirCanRemoveCommandRootBelowConfiguredRoot(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	f.root = "dir"
	f.dirCache = dircache.New(f.root, "0", f)
	if err := f.resolveRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.Rmdir(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, found := store.get(10); found {
		t.Fatal("command root below configured root was not removed")
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

func TestMoveModelRandomSequence(t *testing.T) {
	store := newMutationStore(t)
	f := newMutationFs(t, store)
	current := fs.Object(newObject(f, "file", 0, api.File{
		FileName: "file", FileID: 1, Size: 1, ETag: "9dd4e461268c8034f5c8564e155c67a6",
	}))
	random := rand.New(rand.NewSource(123))
	for step := range 250 {
		name := fmt.Sprintf("model-%03d", step)
		parent := int64(0)
		remote := name
		if random.Intn(2) == 1 {
			parent = 10
			remote = "dir/" + name
		}
		moved, err := f.Move(context.Background(), current, remote)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		current = moved
		node, found := store.get(1)
		ider, hasID := moved.(fs.IDer)
		if !found || !hasID || node.parent != parent || node.file.FileName != name || ider.ID() != "1" || moved.Remote() != remote {
			t.Fatalf("step %d: node=%#v found=%t hasID=%t remote=%q", step, node, found, hasID, moved.Remote())
		}
	}
	if err := current.Remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found := store.get(1); found {
		t.Fatal("模型末尾 Remove 后对象仍存在")
	}
	if err := current.Remove(context.Background()); err != nil {
		t.Fatalf("模型末尾幂等 Remove: %v", err)
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

func TestDirMoveWithRootedCommandFsesVerifiesParentsFromConfiguredRoot(t *testing.T) {
	store := newMutationStore(t)
	store.nodes[20] = mutationNode{file: api.File{FileName: "parent", FileID: 20, Type: 1}, parent: 0}

	source := newMutationFs(t, store)
	source.root = "dir"
	source.dirCache = dircache.New(source.root, "0", source)
	if err := source.resolveRoot(context.Background()); err != nil {
		t.Fatal(err)
	}

	destination := newMutationFs(t, store)
	destination.root = "parent/nested"
	destination.dirCache = dircache.New(destination.root, "0", destination)
	if err := destination.resolveRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := destination.DirMove(context.Background(), source, "", ""); err != nil {
		t.Fatal(err)
	}
	node, found := store.get(10)
	if !found || node.parent != 20 || node.file.FileName != "nested" {
		t.Fatalf("unexpected rooted directory move result %#v", node)
	}
}
