package pan123

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
)

func TestUploadPartPlanBoundaries(t *testing.T) {
	for _, tc := range []struct {
		size    int64
		parts   int64
		batches int
	}{
		{0, 1, 1},
		{1, 1, 1},
		{uploadChunkSize - 1, 1, 1},
		{uploadChunkSize, 1, 1},
		{uploadChunkSize + 1, 2, 1},
		{10 * uploadChunkSize, 10, 1},
		{10*uploadChunkSize + 1, 11, 2},
		{maxUploadParts * uploadChunkSize, maxUploadParts, 1000},
	} {
		parts, err := uploadPartCount(tc.size)
		if err != nil {
			t.Fatal(err)
		}
		if parts != tc.parts || len(uploadBatches(parts)) != tc.batches {
			t.Fatalf("size=%d parts=%d batches=%d", tc.size, parts, len(uploadBatches(parts)))
		}
	}
	if _, err := uploadPartCount(maxUploadParts*uploadChunkSize + 1); err == nil {
		t.Fatal("accepted more than 10,000 parts")
	}
}

type presignedHarness struct {
	t         *testing.T
	urlCalls  atomic.Int64
	putCalls  atomic.Int64
	complete  atomic.Int64
	forbidOne atomic.Bool
	mu        sync.Mutex
	parts     map[int64][]byte
}

func (h *presignedHarness) control() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/b/api" + api.SingleObjectAuthPath, "/b/api" + api.PresignedPartsPath:
			generation := h.urlCalls.Add(1)
			var body struct {
				Start int64 `json:"partNumberStart"`
				End   int64 `json:"partNumberEnd"`
			}
			if err := decodeRequestJSON(r, &body); err != nil {
				h.t.Error(err)
			}
			urls := make(map[string]string)
			for part := body.Start; part <= body.End; part++ {
				urls[strconv.FormatInt(part, 10)] = fmt.Sprintf("https://upload.test/part/%d?generation=%d&secret=redacted", part, generation)
			}
			writeEnvelope(h.t, w, 0, api.PresignedURLsData{PresignedURLs: urls})
		case "/b/api" + api.UploadCompleteV2Path:
			h.complete.Add(1)
			writeEnvelope(h.t, w, 0, nil)
		default:
			h.t.Errorf("unexpected control path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func decodeRequestJSON(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

func (h *presignedHarness) data() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			h.t.Error("Authorization leaked to presigned upload")
		}
		h.putCalls.Add(1)
		generation := r.URL.Query().Get("generation")
		if h.forbidOne.Load() && generation == "1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		part, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/part/"), 10, 64)
		if err != nil {
			h.t.Error(err)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			h.t.Error(err)
		}
		if int64(len(data)) != r.ContentLength {
			h.t.Errorf("part %d length=%d header=%d", part, len(data), r.ContentLength)
		}
		h.mu.Lock()
		h.parts[part] = append([]byte(nil), data...)
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func newPresignedTestFs(t *testing.T, harness *presignedHarness) *Fs {
	t.Helper()
	client, _ := testClient(t, harness.control(), "person@example.com", "token")
	return &Fs{
		client:         client,
		downloadClient: handlerHTTPClient(harness.data()),
		opt:            Options{UploadConcurrency: 3},
	}
}

func TestPresignedUploadBoundaries(t *testing.T) {
	for _, size := range []int64{0, 1, uploadChunkSize - 1, uploadChunkSize, uploadChunkSize + 1} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			h := &presignedHarness{t: t, parts: make(map[int64][]byte)}
			f := newPresignedTestFs(t, h)
			data := bytes.Repeat([]byte{'x'}, int(size))
			digest := md5.Sum(data)
			source := &preparedSource{reader: bytes.NewReader(data), size: size, md5: hex.EncodeToString(digest[:])}
			upload := api.UploadData{FileID: 7, Key: "key", Bucket: "bucket", UploadID: "upload", StorageNode: "node"}
			if err := f.uploadPresigned(context.Background(), upload, source); err != nil {
				t.Fatal(err)
			}
			parts, _ := uploadPartCount(size)
			if h.complete.Load() != 1 || int64(len(h.parts)) != parts {
				t.Fatalf("complete=%d parts=%d want=%d", h.complete.Load(), len(h.parts), parts)
			}
			var joined []byte
			for part := int64(1); part <= parts; part++ {
				joined = append(joined, h.parts[part]...)
			}
			if !bytes.Equal(joined, data) {
				t.Fatal("uploaded bytes changed")
			}
		})
	}
}

func TestPresigned403RefreshesBatch(t *testing.T) {
	h := &presignedHarness{t: t, parts: make(map[int64][]byte)}
	h.forbidOne.Store(true)
	f := newPresignedTestFs(t, h)
	digest := md5.Sum([]byte("x"))
	source := &preparedSource{reader: strings.NewReader("x"), size: 1, md5: hex.EncodeToString(digest[:])}
	if err := f.uploadPresigned(context.Background(), api.UploadData{FileID: 7, Key: "key", Bucket: "bucket"}, source); err != nil {
		t.Fatal(err)
	}
	if h.urlCalls.Load() != 2 || h.putCalls.Load() != 2 || h.complete.Load() != 1 {
		t.Fatalf("urls=%d puts=%d complete=%d", h.urlCalls.Load(), h.putCalls.Load(), h.complete.Load())
	}
}

func TestPresignedDoesNotCompleteOnShortOrChangedSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		size int64
		sum  string
	}{
		{name: "short", data: "x", size: 2, sum: "9dd4e461268c8034f5c8564e155c67a6"},
		{name: "changed", data: "y", size: 1, sum: "9dd4e461268c8034f5c8564e155c67a6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &presignedHarness{t: t, parts: make(map[int64][]byte)}
			f := newPresignedTestFs(t, h)
			source := &preparedSource{reader: strings.NewReader(tc.data), size: tc.size, md5: tc.sum}
			err := f.uploadPresigned(context.Background(), api.UploadData{FileID: 7, Key: "key", Bucket: "bucket"}, source)
			if err == nil || h.complete.Load() != 0 {
				t.Fatalf("err=%v complete=%d", err, h.complete.Load())
			}
		})
	}
}

func TestPartialLegacyCredentialsRejected(t *testing.T) {
	f := &Fs{}
	err := f.uploadData(context.Background(), api.UploadData{AccessKeyID: "only-one"}, &preparedSource{})
	if err == nil {
		t.Fatal("accepted partial credentials")
	}
}

func TestLegacyS3UploadAndComplete(t *testing.T) {
	var complete atomic.Int64
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/b/api"+api.UploadCompletePath {
			t.Errorf("unexpected control path %q", r.URL.Path)
		}
		complete.Add(1)
		writeEnvelope(t, w, 0, nil)
	})
	var uploaded []byte
	data := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "legacy.test" || r.Method != http.MethodPut {
			t.Errorf("unexpected legacy request %s %s", r.Method, r.URL)
		}
		var err error
		uploaded, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get("X-Amz-Decoded-Content-Length") != "5" {
			t.Errorf("decoded content length = %q", r.Header.Get("X-Amz-Decoded-Content-Length"))
		}
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	})
	client, _ := testClient(t, control, "person@example.com", "token")
	f := &Fs{client: client, downloadClient: handlerHTTPClient(data), opt: Options{UploadConcurrency: 3}}
	digest := md5.Sum([]byte("hello"))
	source := &preparedSource{reader: strings.NewReader("hello"), size: 5, md5: hex.EncodeToString(digest[:])}
	upload := api.UploadData{
		AccessKeyID: "access", SecretAccessKey: "secret", SessionToken: "session",
		EndPoint: "https://legacy.test", Bucket: "bucket", Key: "key", FileID: 9,
	}
	if err := f.uploadLegacy(context.Background(), upload, source); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(uploaded, []byte("hello")) || complete.Load() != 1 {
		t.Fatalf("uploaded=%q complete=%d", uploaded, complete.Load())
	}
}
