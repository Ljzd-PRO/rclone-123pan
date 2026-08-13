// Package testserver provides a socket-free HTTP transport with deterministic
// fault injection for backend tests. It is never linked by production code.
package testserver

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// FaultMode selects where a fault occurs relative to handler application.
type FaultMode int

const (
	FaultBeforeApply FaultMode = iota + 1
	FaultAfterApply
	FaultBlockUntilCancel
)

// Fault applies to one one-based call of a method/path pair.
type Fault struct {
	Method string
	Path   string
	Call   int
	Mode   FaultMode
	Err    error
}

// Record captures non-secret request facts.
type Record struct {
	Method string
	Path   string
	Length int64
	MD5    string
}

// Server implements http.RoundTripper around an http.Handler.
type Server struct {
	Handler http.Handler

	mu      sync.Mutex
	faults  []Fault
	calls   map[string]int
	records []Record
}

// New creates a stateful transport for handler.
func New(handler http.Handler, faults ...Fault) *Server {
	return &Server{Handler: handler, faults: faults, calls: make(map[string]int)}
}

// Client returns an HTTP client backed by this transport.
func (s *Server) Client() *http.Client { return &http.Client{Transport: s} }

// Records returns a detached request snapshot.
func (s *Server) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Record(nil), s.records...)
}

func (s *Server) matchingFault(request *http.Request) *Fault {
	key := request.Method + " " + request.URL.Path
	s.calls[key]++
	for i := range s.faults {
		fault := &s.faults[i]
		if fault.Method == request.Method && fault.Path == request.URL.Path && fault.Call == s.calls[key] {
			return fault
		}
	}
	return nil
}

// RoundTrip applies a request to the handler without opening a network socket.
func (s *Server) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		_ = request.Body.Close()
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	digest := md5.Sum(body)
	s.mu.Lock()
	s.records = append(s.records, Record{Method: request.Method, Path: request.URL.Path, Length: int64(len(body)), MD5: hex.EncodeToString(digest[:])})
	fault := s.matchingFault(request)
	s.mu.Unlock()
	if fault != nil {
		switch fault.Mode {
		case FaultBeforeApply:
			return nil, faultError(fault)
		case FaultBlockUntilCancel:
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
	}
	recorder := httptest.NewRecorder()
	s.Handler.ServeHTTP(recorder, request)
	if fault != nil && fault.Mode == FaultAfterApply {
		return nil, faultError(fault)
	}
	return recorder.Result(), nil
}

func faultError(fault *Fault) error {
	if fault.Err != nil {
		return fault.Err
	}
	return io.ErrUnexpectedEOF
}

// WaitForCancellation is a handler helper for cancellation tests.
func WaitForCancellation(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

var _ http.RoundTripper = (*Server)(nil)
