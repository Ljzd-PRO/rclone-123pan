package testserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBeforeAndAfterApplyFaults(t *testing.T) {
	for _, mode := range []FaultMode{FaultBeforeApply, FaultAfterApply} {
		var applied atomic.Int64
		server := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applied.Add(1)
			w.WriteHeader(http.StatusOK)
		}), Fault{Method: http.MethodPost, Path: "/x", Call: 1, Mode: mode})
		request, _ := http.NewRequest(http.MethodPost, "https://test/x", strings.NewReader("body"))
		_, err := server.Client().Do(request)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("mode=%d err=%v", mode, err)
		}
		wantApplied := int64(0)
		if mode == FaultAfterApply {
			wantApplied = 1
		}
		if applied.Load() != wantApplied {
			t.Fatalf("mode=%d applied=%d", mode, applied.Load())
		}
		records := server.Records()
		if len(records) != 1 || records[0].Length != 4 || records[0].MD5 != "841a2d689ad86bd1611447453c22c6fc" {
			t.Fatalf("records=%#v", records)
		}
	}
}

func TestBlockUntilCancellation(t *testing.T) {
	server := New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Fault{Method: http.MethodGet, Path: "/x", Call: 1, Mode: FaultBlockUntilCancel})
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://test/x", nil)
	done := make(chan error, 1)
	go func() {
		_, err := server.Client().Do(request)
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context canceled", err)
	}
}
