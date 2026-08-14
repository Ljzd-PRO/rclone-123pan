package _123pan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
)

func handlerHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})}
}

func newDownloadTestObject(t *testing.T, downloadURL string, dataHandler http.Handler) (*Object, *atomic.Int64) {
	t.Helper()
	var infoCalls atomic.Int64
	controlHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/b/api"+api.DownloadInfoPath {
			t.Errorf("unexpected control path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		infoCalls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["fileId"] != float64(7) || body["etag"] == "" || body["fileName"] != "data.bin" {
			t.Errorf("incomplete download metadata: %#v", body)
		}
		writeEnvelope(t, w, 0, api.DownloadInfoData{DownloadURL: downloadURL})
	})
	client, _ := testClient(t, controlHandler, "person@example.com", "token")
	f := &Fs{client: client, downloadClient: handlerHTTPClient(dataHandler)}
	item := api.File{FileName: "data.bin", FileID: 7, Size: 10, ETag: "781e5e245d69b566979b86e28d23f2c7", S3KeyFlag: "key"}
	return newObject(f, "data.bin", 0, item), &infoCalls
}

func TestDownloadRangesAndHeaderIsolation(t *testing.T) {
	const content = "0123456789"
	encoded := base64.StdEncoding.EncodeToString([]byte("https://resolver.test/start"))
	dataHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("control credential leaked to %s", r.URL.Host)
		}
		switch r.URL.Host {
		case "resolver.test":
			if r.Header.Get("Referer") != webOrigin+"/" {
				t.Errorf("resolver referer = %q", r.Header.Get("Referer"))
			}
			w.Header().Set("Location", "https://cdn.test/file")
			w.WriteHeader(http.StatusFound)
		case "cdn.test":
			if r.Header.Get("Referer") != "https://resolver.test/" {
				t.Errorf("CDN referer = %q", r.Header.Get("Referer"))
			}
			start, end := int64(0), int64(len(content)-1)
			if rawRange := r.Header.Get("Range"); rawRange != "" {
				parsed, err := fs.ParseRangeOption(rawRange)
				if err != nil {
					t.Error(err)
					return
				}
				start, end = parsed.Start, parsed.End
				w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/10")
				w.WriteHeader(http.StatusPartialContent)
			}
			_, _ = io.WriteString(w, content[start:end+1])
		default:
			t.Errorf("unexpected data host %q", r.URL.Host)
			http.NotFound(w, r)
		}
	})
	for _, tc := range []struct {
		name    string
		options []fs.OpenOption
		want    string
	}{
		{name: "full", want: content},
		{name: "range", options: []fs.OpenOption{&fs.RangeOption{Start: 2, End: 5}}, want: "2345"},
		{name: "seek", options: []fs.OpenOption{&fs.SeekOption{Offset: 4}}, want: "456789"},
		{name: "suffix", options: []fs.OpenOption{&fs.RangeOption{Start: -1, End: 3}}, want: "789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := newDownloadTestObject(t, "https://wrapper.test/?params="+encoded, dataHandler)
			reader, err := o.Open(context.Background(), tc.options...)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			got, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownloadJSONRedirect(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "resolver.test" {
			writeEnvelope(t, w, 0, map[string]any{"redirect_url": "https://cdn.test/file"})
			return
		}
		_, _ = io.WriteString(w, "0123456789")
	})
	o, _ := newDownloadTestObject(t, "https://resolver.test/start", handler)
	reader, err := o.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(got) != "0123456789" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDownloadRefreshesOne403(t *testing.T) {
	var finalCalls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "resolver.test" {
			w.Header().Set("Location", "https://cdn.test/file")
			w.WriteHeader(http.StatusFound)
			return
		}
		if finalCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "0123456789")
	})
	o, infoCalls := newDownloadTestObject(t, "https://resolver.test/start", handler)
	reader, err := o.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(got) != "0123456789" {
		t.Fatalf("got %q, %v", got, err)
	}
	if infoCalls.Load() != 2 || finalCalls.Load() != 2 {
		t.Fatalf("info calls=%d final calls=%d", infoCalls.Load(), finalCalls.Load())
	}
}

func TestDownloadRejectsInvalidBase64AndShortBody(t *testing.T) {
	t.Run("base64", func(t *testing.T) {
		var dataCalls atomic.Int64
		o, _ := newDownloadTestObject(t, "https://wrapper.test/?params=%21%21%21", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dataCalls.Add(1)
		}))
		if _, err := o.Open(context.Background()); err == nil || !strings.Contains(err.Error(), "base64") {
			t.Fatalf("got %v", err)
		}
		if dataCalls.Load() != 0 {
			t.Fatal("data plane was contacted after invalid base64")
		}
	})
	t.Run("short", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host == "resolver.test" {
				w.Header().Set("Location", "https://cdn.test/file")
				w.WriteHeader(http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "short")
		})
		o, _ := newDownloadTestObject(t, "https://resolver.test/start", handler)
		reader, err := o.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(reader)
		_ = reader.Close()
		if err == nil {
			t.Fatal("accepted a short download body")
		}
	})
}

func TestZeroByteDownloadDoesNotContactNetwork(t *testing.T) {
	var calls atomic.Int64
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }), "person@example.com", "token")
	o := newObject(&Fs{client: client}, "empty", 0, api.File{FileName: "empty", FileID: 1, Size: 0})
	reader, err := o.Open(context.Background(), &fs.RangeOption{Start: 0, End: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || len(got) != 0 || calls.Load() != 0 {
		t.Fatalf("got %q, err=%v, calls=%d", got, err, calls.Load())
	}
}
