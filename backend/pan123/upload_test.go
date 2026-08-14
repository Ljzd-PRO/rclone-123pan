package pan123

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
)

type countingReader struct {
	reader io.Reader
	read   atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read.Add(int64(n))
	return n, err
}

func TestPrepareSourceKnownMD5DoesNotRead(t *testing.T) {
	input := &countingReader{reader: strings.NewReader("hello")}
	src := object.NewStaticObjectInfo("x", time.Now(), 5, true, map[hash.Type]string{hash.MD5: "5D41402ABC4B2A76B9719D911017C592"}, nil)
	prepared, err := prepareSource(context.Background(), input, src, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if input.read.Load() != 0 || prepared.md5 != "5d41402abc4b2a76b9719d911017c592" || prepared.size != 5 {
		t.Fatalf("read=%d source=%#v", input.read.Load(), prepared)
	}
}

func TestPrepareSourceMemoryAndTemp(t *testing.T) {
	for _, tc := range []struct {
		name      string
		size      int64
		limit     int64
		wantSpool bool
	}{
		{name: "memory", size: 5, limit: 10},
		{name: "temp", size: 5, limit: 1, wantSpool: true},
		{name: "unknown", size: -1, limit: 10, wantSpool: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := object.NewStaticObjectInfo("x", time.Now(), tc.size, true, nil, nil)
			prepared, err := prepareSource(context.Background(), strings.NewReader("hello"), src, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			spool := tempSpoolName(prepared)
			got, err := io.ReadAll(prepared.reader)
			if err != nil || string(got) != "hello" {
				t.Fatalf("got %q, %v", got, err)
			}
			if prepared.size != 5 || prepared.md5 != "5d41402abc4b2a76b9719d911017c592" || (spool != "") != tc.wantSpool {
				t.Fatalf("unexpected source: %#v spool=%q", prepared, spool)
			}
			if err := prepared.cleanup(); err != nil {
				t.Fatal(err)
			}
			if spool != "" {
				if _, err := os.Stat(os.TempDir() + string(os.PathSeparator) + spool); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("spool still exists: %v", err)
				}
			}
		})
	}
}

func TestPrepareSourceRejectsShortAndLong(t *testing.T) {
	for _, tc := range []struct {
		size int64
		data string
	}{
		{size: 6, data: "hello"},
		{size: 4, data: "hello"},
	} {
		src := object.NewStaticObjectInfo("x", time.Now(), tc.size, true, nil, nil)
		if _, err := prepareSource(context.Background(), strings.NewReader(tc.data), src, 10); err == nil {
			t.Fatalf("accepted size=%d data=%q", tc.size, tc.data)
		}
	}
}

func TestRapidUploadReadsNoBodyAndVerifies(t *testing.T) {
	const sum = "5d41402abc4b2a76b9719d911017c592"
	var uploadCalls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/b/api" + api.UploadRequestPath:
			uploadCalls.Add(1)
			writeEnvelope(t, w, 0, api.UploadData{FileID: 9, Reuse: true})
		case "/b/api" + api.FileListPath:
			writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 1, InfoList: []api.File{{FileName: "x", FileID: 9, Size: 5, ETag: sum}}})
		default:
			http.NotFound(w, r)
		}
	})
	f := newListingTestFs(t, handler)
	f.opt.VerifyTimeout = 2
	input := &countingReader{reader: strings.NewReader("hello")}
	source := &preparedSource{reader: input, size: 5, md5: sum, cleanup: func() error { return nil }}
	obj, upload, err := f.rapidUpload(context.Background(), 0, "x", source, input)
	if err != nil {
		t.Fatal(err)
	}
	if obj == nil || !upload.Reuse || input.read.Load() != 0 || uploadCalls.Load() != 1 {
		t.Fatalf("obj=%#v upload=%#v read=%d calls=%d", obj, upload, input.read.Load(), uploadCalls.Load())
	}
}

func TestRapidUploadZeroIDCoordinatesByUniqueMetadata(t *testing.T) {
	const sum = "5d41402abc4b2a76b9719d911017c592"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/b/api" + api.UploadRequestPath:
			writeEnvelope(t, w, 0, api.UploadData{FileID: 0, Reuse: true, Key: "opaque-key"})
		case "/b/api" + api.FileListPath:
			writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 1, InfoList: []api.File{{FileName: "x", FileID: 11, Size: 5, ETag: sum}}})
		default:
			http.NotFound(w, r)
		}
	})
	f := newListingTestFs(t, handler)
	input := &countingReader{reader: strings.NewReader("hello")}
	source := &preparedSource{reader: input, size: 5, md5: sum, cleanup: func() error { return nil }}
	obj, upload, err := f.rapidUpload(context.Background(), 0, "x", source, input)
	if err != nil {
		t.Fatal(err)
	}
	if obj == nil || obj.id != 11 || upload.FileID != 0 || !upload.Reuse || input.read.Load() != 0 {
		t.Fatalf("obj=%#v upload=%#v read=%d", obj, upload, input.read.Load())
	}
}

func TestRapidUploadZeroIDRejectsAmbiguousTarget(t *testing.T) {
	const sum = "5d41402abc4b2a76b9719d911017c592"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b/api"+api.UploadRequestPath {
			writeEnvelope(t, w, 0, api.UploadData{FileID: 0, Reuse: true, Key: "opaque-key"})
			return
		}
		writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 2, InfoList: []api.File{
			{FileName: "x", FileID: 11, Size: 5, ETag: sum},
			{FileName: "x", FileID: 12, Size: 5, ETag: sum},
		}})
	})
	f := newListingTestFs(t, handler)
	_, _, err := f.rapidUpload(context.Background(), 0, "x", &preparedSource{size: 5, md5: sum}, bytes.NewReader(nil))
	if err == nil {
		t.Fatal("accepted ambiguous zero-ID rapid-upload target")
	}
}

func TestRapidUploadZeroIDWithoutVisibleTargetFailsClosed(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b/api"+api.UploadRequestPath {
			writeEnvelope(t, w, 0, api.UploadData{FileID: 0, Reuse: true, Key: "opaque-key"})
			return
		}
		writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 0})
	})
	f := newListingTestFs(t, handler)
	_, _, err := f.rapidUpload(context.Background(), 0, "x", &preparedSource{size: 5, md5: "5d41402abc4b2a76b9719d911017c592"}, bytes.NewReader(nil))
	if err == nil {
		t.Fatal("accepted invisible zero-ID rapid-upload target")
	}
}

func TestUploadRequestRejectsFalseSuccess(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, ok := request["duplicate"]; ok {
			t.Fatalf("upload request contains obsolete duplicate field: %#v", request)
		}
		if request["parentFileId"] != float64(123) {
			t.Fatalf("parentFileId = %#v, want JSON number", request["parentFileId"])
		}
		if source, ok := request["RequestSource"]; !ok || source != nil {
			t.Fatalf("RequestSource = %#v, want explicit null", request["RequestSource"])
		}
		writeEnvelope(t, w, 0, api.UploadData{FileID: 9, Reuse: false, Key: ""})
	})
	f := newListingTestFs(t, handler)
	_, err := f.requestUpload(context.Background(), 123, "x", &preparedSource{size: 1, md5: "0cc175b9c0f1b6a831c399e269772661"})
	if err == nil {
		t.Fatal("accepted Reuse=false with empty Key")
	}
}

func TestUploadRequestDoesNotReplayAmbiguousFailure(t *testing.T) {
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "response lost", http.StatusInternalServerError)
	})
	f := newListingTestFs(t, handler)
	_, err := f.requestUpload(context.Background(), 0, "x", &preparedSource{size: 1, md5: "0cc175b9c0f1b6a831c399e269772661"})
	if err == nil {
		t.Fatal("accepted an ambiguous upload request failure")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upload request was replayed %d times", got)
	}
}

func TestUploadRequestRejectsIncompleteProfiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		data api.UploadData
	}{
		{
			name: "presigned missing context",
			data: api.UploadData{FileID: 9, Key: "key", SliceSize: "16777216"},
		},
		{
			name: "presigned wrong slice size",
			data: api.UploadData{FileID: 9, Key: "key", Bucket: "bucket", StorageNode: "node", UploadID: "upload", SliceSize: "1"},
		},
		{
			name: "partial legacy credentials",
			data: api.UploadData{FileID: 9, Key: "key", AccessKeyID: "access"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeEnvelope(t, w, 0, tc.data)
			})
			f := newListingTestFs(t, handler)
			_, err := f.requestUpload(context.Background(), 123, "x", &preparedSource{size: 1, md5: "0cc175b9c0f1b6a831c399e269772661"})
			if err == nil {
				t.Fatal("accepted incomplete upload profile")
			}
			if !strings.Contains(err.Error(), "file ID 9") {
				t.Fatalf("error omitted recovery file ID: %v", err)
			}
			if tc.name == "presigned wrong slice size" && !strings.Contains(err.Error(), `SliceSize "1"`) {
				t.Fatalf("error omitted non-secret SliceSize: %v", err)
			}
		})
	}
}

func TestRapidUploadRejectsFakeReuse(t *testing.T) {
	var lists atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/b/api"+api.UploadRequestPath {
			writeEnvelope(t, w, 0, api.UploadData{FileID: 9, Reuse: true})
			return
		}
		lists.Add(1)
		writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 1, InfoList: []api.File{{FileName: "x", FileID: 9, Size: 999, ETag: "5d41402abc4b2a76b9719d911017c592"}}})
	})
	f := newListingTestFs(t, handler)
	f.opt.VerifyTimeout = 1
	_, _, err := f.rapidUpload(context.Background(), 0, "x", &preparedSource{size: 5, md5: "5d41402abc4b2a76b9719d911017c592"}, bytes.NewReader(nil))
	if err == nil || lists.Load() == 0 {
		t.Fatalf("got err=%v lists=%d", err, lists.Load())
	}
}

func TestRapidUploadFallsBackWhenReuseHasUploadKeyButIsNotVisible(t *testing.T) {
	const sum = "5d41402abc4b2a76b9719d911017c592"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/b/api" + api.UploadRequestPath:
			writeEnvelope(t, w, 0, api.UploadData{
				FileID: 9, Reuse: true, Key: "upload-key", Bucket: "bucket",
				StorageNode: "node", UploadID: "upload", SliceSize: "16777216",
			})
		case "/b/api" + api.FileListPath:
			writeEnvelope(t, w, 0, api.FileListData{Next: "-1", Total: 0})
		default:
			http.NotFound(w, r)
		}
	})
	f := newListingTestFs(t, handler)
	source := &preparedSource{size: 5, md5: sum}
	obj, upload, err := f.rapidUpload(context.Background(), 0, "x", source, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if obj != nil {
		t.Fatal("unexpected rapid-upload object")
	}
	if !upload.Reuse || upload.Key != "upload-key" || upload.FileID != 9 {
		t.Fatalf("unexpected fallback upload data: %#v", upload)
	}
}
