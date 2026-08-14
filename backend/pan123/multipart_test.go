package pan123

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
		parts, err := uploadPartCount(tc.size, uploadChunkSize)
		if err != nil {
			t.Fatal(err)
		}
		if parts != tc.parts || len(uploadBatches(parts)) != tc.batches {
			t.Fatalf("size=%d parts=%d batches=%d", tc.size, parts, len(uploadBatches(parts)))
		}
	}
	if _, err := uploadPartCount(maxUploadParts*uploadChunkSize+1, uploadChunkSize); err == nil {
		t.Fatal("accepted more than 10,000 parts")
	}
	if _, err := uploadPartCount(math.MaxInt64, uploadChunkSize); err == nil {
		t.Fatal("accepted MaxInt64 upload size after overflow-safe planning")
	}
	parts, err := uploadPartCount(160*1024*1024+1, largeUploadChunkSize)
	if err != nil || parts != 6 || len(uploadBatches(parts)) != 1 {
		t.Fatalf("32 MiB profile planned parts=%d batches=%d err=%v", parts, len(uploadBatches(parts)), err)
	}
	parts, err = uploadPartCount(10*largeUploadChunkSize+1, largeUploadChunkSize)
	if err != nil || parts != 11 || len(uploadBatches(parts)) != 2 {
		t.Fatalf("32 MiB second batch planned parts=%d batches=%d err=%v", parts, len(uploadBatches(parts)), err)
	}
}

func testPresignedUpload() api.UploadData {
	return api.UploadData{
		FileID: 7, Key: "key", Bucket: "bucket", UploadID: "upload", StorageNode: "node",
		SliceSize: strconv.FormatInt(uploadChunkSize, 10),
	}
}

type presignedHarness struct {
	t              *testing.T
	urlCalls       atomic.Int64
	listCalls      atomic.Int64
	putCalls       atomic.Int64
	complete       atomic.Int64
	oldFlow        atomic.Int64
	forbidOne      atomic.Bool
	listParts      json.RawMessage
	completeFile   *api.File
	completeStatus int
	chunkSize      int64
	mu             sync.Mutex
	parts          map[int64][]byte
	block          <-chan struct{}
	started        chan<- int64
	active         atomic.Int64
	maxActive      atomic.Int64
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
			for part := body.Start; part < body.End; part++ {
				urls[strconv.FormatInt(part, 10)] = fmt.Sprintf("https://upload.test/part/%d?generation=%d&secret=redacted", part, generation)
			}
			writeEnvelope(h.t, w, 0, api.PresignedURLsData{PresignedURLs: urls})
		case "/b/api" + api.S3ListUploadPartsPath:
			var body api.UploadPartsRequest
			if err := decodeRequestJSON(r, &body); err != nil {
				h.t.Error(err)
			}
			if body.Bucket != "bucket" || body.Key != "key" || body.UploadID != "upload" || body.StorageNode != "node" {
				h.t.Errorf("invalid list parts request: %#v", body)
			}
			h.listCalls.Add(1)
			parts := h.listParts
			if len(parts) == 0 {
				parts = json.RawMessage("null")
			}
			writeEnvelope(h.t, w, 0, api.UploadPartsData{Parts: parts})
		case "/b/api" + api.UploadCompleteV2Path:
			var body api.UploadCompleteV2Request
			if err := decodeRequestJSON(r, &body); err != nil {
				h.t.Error(err)
			}
			chunkSize := h.chunkSize
			if chunkSize == 0 {
				chunkSize = uploadChunkSize
			}
			if body.Bucket != "bucket" || body.Key != "key" || body.UploadID != "upload" || body.StorageNode != "node" || body.FileID != 7 || body.IsMultipart != (body.FileSize > chunkSize) {
				h.t.Errorf("invalid v2 completion request: %#v", body)
			}
			h.complete.Add(1)
			if h.completeStatus != 0 {
				http.Error(w, "completion response lost", h.completeStatus)
				return
			}
			h.mu.Lock()
			var joined []byte
			for part := int64(1); ; part++ {
				data, ok := h.parts[part]
				if !ok {
					break
				}
				joined = append(joined, data...)
			}
			h.mu.Unlock()
			digest := md5.Sum(joined)
			fileInfo := api.File{
				FileID: body.FileID,
				Size:   body.FileSize,
				ETag:   hex.EncodeToString(digest[:]),
			}
			if h.completeFile != nil {
				fileInfo = *h.completeFile
			}
			writeEnvelope(h.t, w, 0, api.UploadCompleteData{FileInfo: fileInfo})
		case "/b/api/file/s3_complete_multipart_upload", "/b/api" + api.UploadCompletePath:
			h.oldFlow.Add(1)
			h.t.Errorf("unsupported legacy presigned completion path %q", r.URL.Path)
			http.NotFound(w, r)
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
		if r.Header.Get("Referer") != webOrigin+"/" {
			h.t.Errorf("presigned upload referer = %q", r.Header.Get("Referer"))
		}
		h.putCalls.Add(1)
		active := h.active.Add(1)
		defer h.active.Add(-1)
		for current := h.maxActive.Load(); active > current && !h.maxActive.CompareAndSwap(current, active); current = h.maxActive.Load() {
		}
		if h.started != nil {
			h.started <- active
		}
		if h.block != nil {
			<-h.block
		}
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

func TestPresignedReadsNoMoreThanActivePayloads(t *testing.T) {
	release := make(chan struct{})
	started := make(chan int64, 3)
	h := &presignedHarness{t: t, parts: make(map[int64][]byte), block: release, started: started}
	f := newPresignedTestFs(t, h)
	f.opt.UploadConcurrency = 2
	data := bytes.Repeat([]byte{'x'}, int(2*uploadChunkSize+1))
	digest := md5.Sum(data)
	reader := &countingReader{reader: bytes.NewReader(data)}
	source := &preparedSource{reader: reader, size: int64(len(data)), md5: hex.EncodeToString(digest[:])}
	done := make(chan error, 1)
	go func() {
		_, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source)
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("upload stopped before filling concurrency slots: %v", err)
		case <-time.After(time.Second):
			t.Fatal("upload did not fill concurrency slots")
		}
	}
	// Give the producer an opportunity to over-read while both PUTs are held.
	time.Sleep(20 * time.Millisecond)
	if got, want := reader.read.Load(), int64(2*uploadChunkSize); got != want {
		t.Fatalf("source read %d bytes with two active slots, want %d", got, want)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := h.maxActive.Load(); got != 2 {
		t.Fatalf("maximum active data PUTs = %d, want 2", got)
	}
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
			upload := testPresignedUpload()
			completed, err := f.uploadPresigned(context.Background(), upload, source)
			if err != nil {
				t.Fatal(err)
			}
			parts, _ := uploadPartCount(size, uploadChunkSize)
			wantList := int64(0)
			if parts > 1 {
				wantList = 1
			}
			if completed == nil || h.listCalls.Load() != wantList || h.complete.Load() != 1 || h.oldFlow.Load() != 0 || int64(len(h.parts)) != parts {
				t.Fatalf("completed=%#v list=%d complete=%d old=%d parts=%d want=%d", completed, h.listCalls.Load(), h.complete.Load(), h.oldFlow.Load(), len(h.parts), parts)
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

func TestPresignedUses32MiBServerSlice(t *testing.T) {
	h := &presignedHarness{t: t, parts: make(map[int64][]byte), chunkSize: largeUploadChunkSize}
	f := newPresignedTestFs(t, h)
	data := bytes.Repeat([]byte{'z'}, int(largeUploadChunkSize+1))
	digest := md5.Sum(data)
	upload := testPresignedUpload()
	upload.SliceSize = strconv.FormatInt(largeUploadChunkSize, 10)
	completed, err := f.uploadPresigned(context.Background(), upload, &preparedSource{
		reader: bytes.NewReader(data), size: int64(len(data)), md5: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed == nil || h.listCalls.Load() != 1 || h.complete.Load() != 1 || len(h.parts) != 2 {
		t.Fatalf("completed=%#v list=%d complete=%d parts=%d", completed, h.listCalls.Load(), h.complete.Load(), len(h.parts))
	}
	if got := int64(len(h.parts[1])); got != largeUploadChunkSize {
		t.Fatalf("first part size=%d, want %d", got, largeUploadChunkSize)
	}
	if got := len(h.parts[2]); got != 1 {
		t.Fatalf("last part size=%d, want 1", got)
	}
}

func TestPresigned403RefreshesBatch(t *testing.T) {
	h := &presignedHarness{t: t, parts: make(map[int64][]byte)}
	h.forbidOne.Store(true)
	f := newPresignedTestFs(t, h)
	digest := md5.Sum([]byte("x"))
	source := &preparedSource{reader: strings.NewReader("x"), size: 1, md5: hex.EncodeToString(digest[:])}
	if _, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source); err != nil {
		t.Fatal(err)
	}
	if h.urlCalls.Load() != 2 || h.putCalls.Load() != 2 || h.complete.Load() != 1 || h.oldFlow.Load() != 0 {
		t.Fatalf("urls=%d puts=%d complete=%d old=%d", h.urlCalls.Load(), h.putCalls.Load(), h.complete.Load(), h.oldFlow.Load())
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
			_, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source)
			if err == nil || h.complete.Load() != 0 || h.oldFlow.Load() != 0 {
				t.Fatalf("err=%v complete=%d old=%d", err, h.complete.Load(), h.oldFlow.Load())
			}
		})
	}
}

func TestPresignedRejectsUnverifiedResumeParts(t *testing.T) {
	h := &presignedHarness{
		t:         t,
		parts:     make(map[int64][]byte),
		listParts: json.RawMessage(`[{"PartNumber":1}]`),
	}
	f := newPresignedTestFs(t, h)
	data := bytes.Repeat([]byte{'x'}, int(uploadChunkSize+1))
	digest := md5.Sum(data)
	source := &preparedSource{reader: bytes.NewReader(data), size: int64(len(data)), md5: hex.EncodeToString(digest[:])}
	_, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source)
	if err == nil {
		t.Fatal("accepted unverified multipart resume state")
	}
	if h.listCalls.Load() != 1 || h.urlCalls.Load() != 0 || h.putCalls.Load() != 0 || h.complete.Load() != 0 {
		t.Fatalf("list=%d urls=%d puts=%d complete=%d", h.listCalls.Load(), h.urlCalls.Load(), h.putCalls.Load(), h.complete.Load())
	}
}

func TestPresignedRequiresCompleteFileInfo(t *testing.T) {
	h := &presignedHarness{t: t, parts: make(map[int64][]byte), completeFile: &api.File{}}
	f := newPresignedTestFs(t, h)
	digest := md5.Sum([]byte("x"))
	source := &preparedSource{reader: strings.NewReader("x"), size: 1, md5: hex.EncodeToString(digest[:])}
	_, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source)
	if err == nil {
		t.Fatal("accepted upload_complete/v2 without a valid file_info mapping")
	}
	if h.complete.Load() != 1 || h.oldFlow.Load() != 0 {
		t.Fatalf("complete=%d old=%d", h.complete.Load(), h.oldFlow.Load())
	}
}

func TestPresignedAcceptsExplicitCompletionFileIDMapping(t *testing.T) {
	digest := md5.Sum([]byte("x"))
	sum := hex.EncodeToString(digest[:])
	h := &presignedHarness{
		t:     t,
		parts: make(map[int64][]byte),
		completeFile: &api.File{
			FileID: 99,
			Size:   1,
			ETag:   sum,
		},
	}
	f := newPresignedTestFs(t, h)
	completed, err := f.uploadPresigned(context.Background(), testPresignedUpload(), &preparedSource{reader: strings.NewReader("x"), size: 1, md5: sum})
	if err != nil {
		t.Fatal(err)
	}
	if completed == nil || completed.FileID != 99 {
		t.Fatalf("completion mapping = %#v, want file ID 99", completed)
	}
}

func TestPresignedCompletionIsNeverReplayed(t *testing.T) {
	h := &presignedHarness{t: t, parts: make(map[int64][]byte), completeStatus: http.StatusInternalServerError}
	f := newPresignedTestFs(t, h)
	digest := md5.Sum([]byte("x"))
	source := &preparedSource{reader: strings.NewReader("x"), size: 1, md5: hex.EncodeToString(digest[:])}
	_, err := f.uploadPresigned(context.Background(), testPresignedUpload(), source)
	var ambiguous *ambiguousCompleteError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("completion error = %v, want ambiguousCompleteError", err)
	}
	if h.complete.Load() != 1 || h.oldFlow.Load() != 0 {
		t.Fatalf("complete=%d old=%d", h.complete.Load(), h.oldFlow.Load())
	}
}

func TestPartialLegacyCredentialsRejected(t *testing.T) {
	f := &Fs{}
	_, err := f.uploadData(context.Background(), api.UploadData{AccessKeyID: "only-one"}, &preparedSource{})
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
