package pan123

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/dircache"
	"github.com/rclone/rclone/lib/encoder"
)

func newListingTestFs(t *testing.T, handler http.Handler) *Fs {
	t.Helper()
	client, _ := testClient(t, handler, "person@example.com", "token")
	f := &Fs{name: "test", opt: Options{Enc: defaultEncoding}, client: client, uid: 42}
	f.dirCache = dircache.New("", "0", f)
	f.features = (&fs.Features{CanHaveEmptyDirectories: true, PartialUploads: true}).Fill(context.Background(), f)
	return f
}

func pagedListHandler(t *testing.T, count int) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/b/api"+api.FileListPath {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("orderBy") != "file_id" || r.URL.Query().Get("orderDirection") != "desc" {
			t.Errorf("listing contract query changed: %v", r.URL.Query())
		}
		page, err := strconv.Atoi(r.URL.Query().Get("Page"))
		if err != nil || page < 1 {
			t.Errorf("invalid page: %v", r.URL.Query())
			return
		}
		start := (page - 1) * int(listPageSize)
		end := min(start+int(listPageSize), count)
		items := make([]api.File, 0, max(end-start, 0))
		for i := start; i < end; i++ {
			items = append(items, api.File{FileName: fmt.Sprintf("file-%05d", i), FileID: int64(i + 1), Size: int64(i)})
		}
		next := "-1"
		if end < count {
			next = strconv.Itoa(page + 1)
		}
		writeEnvelope(t, w, 0, api.FileListData{Next: next, Total: int64(count), InfoList: items})
	})
}

func TestListBoundaryMatrix(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100, 101, 199, 200, 201, 1001, 10000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			f := newListingTestFs(t, pagedListHandler(t, count))
			files, err := f.listAll(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != count {
				t.Fatalf("got %d files, want %d", len(files), count)
			}
		})
	}
}

func TestListDiscardsInconsistentRounds(t *testing.T) {
	var rounds atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("Page"))
		if page == 1 {
			rounds.Add(1)
		}
		total := int64(101)
		if rounds.Load() < 3 && page == 2 {
			total = 102
		}
		start := (page - 1) * 100
		end := min(start+100, 101)
		items := make([]api.File, 0, end-start)
		for i := start; i < end; i++ {
			items = append(items, api.File{FileName: fmt.Sprintf("f-%d", i), FileID: int64(i + 1)})
		}
		next := "2"
		if page == 2 {
			next = "-1"
		}
		writeEnvelope(t, w, 0, api.FileListData{Next: next, Total: total, InfoList: items})
	})
	f := newListingTestFs(t, handler)
	files, err := f.listAll(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 101 || rounds.Load() != 3 {
		t.Fatalf("got files=%d rounds=%d", len(files), rounds.Load())
	}
}

func TestListRejectsDuplicateIDAndName(t *testing.T) {
	for _, items := range [][]api.File{
		{{FileName: "a", FileID: 1}, {FileName: "b", FileID: 1}},
		{{FileName: "same", FileID: 1}, {FileName: "same", FileID: 2}},
	} {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 2, InfoList: items})
		})
		f := newListingTestFs(t, handler)
		if _, err := f.listAll(context.Background(), 0); err == nil {
			t.Fatalf("accepted ambiguous items %#v", items)
		}
	}
}

func TestListRejectsUnsafeDecodedLeaves(t *testing.T) {
	// rclone's Standard encoder safely represents dot, slash, and NUL names;
	// only an empty leaf remains capable of collapsing to its parent path.
	for _, name := range []string{""} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			serverName := defaultEncoding.FromStandardName(name)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 1, InfoList: []api.File{{FileName: serverName, FileID: 1}}})
			})
			f := newListingTestFs(t, handler)
			if _, err := f.listAll(context.Background(), 0); err == nil {
				t.Fatalf("accepted unsafe server leaf %q", name)
			}
		})
	}
}

func TestListRejectsUnknownObjectType(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 1, InfoList: []api.File{{FileName: "unknown", FileID: 1, Type: 7}}})
	})
	f := newListingTestFs(t, handler)
	if _, err := f.listAll(context.Background(), 0); err == nil {
		t.Fatal("accepted an unknown object type")
	}
}

func TestListRejectsStalledNextAndPartialTermination(t *testing.T) {
	for _, mode := range []string{"stalled", "partial"} {
		t.Run(mode, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, _ := strconv.Atoi(r.URL.Query().Get("Page"))
				next := "same"
				if mode == "partial" {
					next = "-1"
				}
				writeEnvelope(t, w, 0, api.FileListData{Next: next, Total: 2, InfoList: []api.File{{FileName: fmt.Sprintf("x-%d", page), FileID: int64(page)}}})
			})
			f := newListingTestFs(t, handler)
			if _, err := f.listAll(context.Background(), 0); err == nil {
				t.Fatal("accepted inconsistent listing")
			}
		})
	}
}

func TestObjectMD5Validation(t *testing.T) {
	const upper = "D41D8CD98F00B204E9800998ECF8427E"
	o := newObject(&Fs{}, "x", 0, api.File{FileID: 1, ETag: upper})
	got, err := o.Hash(context.Background(), hash.MD5)
	if err != nil || got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, invalid := range []string{"", "short", "z41d8cd98f00b204e9800998ecf8427e"} {
		if got := normalizeMD5(invalid); got != "" {
			t.Fatalf("accepted invalid ETag %q as %q", invalid, got)
		}
	}
	if _, err := o.Hash(context.Background(), hash.SHA1); err != hash.ErrUnsupported {
		t.Fatalf("got %v, want unsupported hash", err)
	}
	if err := o.SetModTime(context.Background(), time.Now()); err != fs.ErrorCantSetModTime {
		t.Fatalf("got %v, want ErrorCantSetModTime", err)
	}
}

func TestEncodingCollisionFailsClosed(t *testing.T) {
	items := []api.File{
		{FileName: "x\\y", FileID: 1},
		{FileName: "x／y", FileID: 2},
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 2, InfoList: items})
	})
	f := newListingTestFs(t, handler)
	f.opt.Enc = encoder.EncodeBackSlash
	if f.opt.Enc.ToStandardName(items[0].FileName) == f.opt.Enc.ToStandardName(items[1].FileName) {
		if _, err := f.listAll(context.Background(), 0); err == nil {
			t.Fatal("accepted an encoding collision")
		}
	}
}

func TestAboutAndUserInfo(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, 0, api.UserInfoData{UID: 42, Nickname: "tester", SpaceUsed: 25, SpacePermanent: 100, SpaceTemp: 50, FileCount: 7})
	})
	f := newListingTestFs(t, handler)
	usage, err := f.About(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if *usage.Total != 150 || *usage.Used != 25 || *usage.Free != 125 || *usage.Objects != 7 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	info, err := f.UserInfo(context.Background())
	if err != nil || info["uid"] != "42" || info["nickname"] != "tester" {
		t.Fatalf("unexpected user info: %#v, %v", info, err)
	}
}
