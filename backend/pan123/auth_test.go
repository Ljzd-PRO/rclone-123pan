package pan123

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/lib/pacer"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testClient(t *testing.T, handler http.Handler, user, token string) (*apiClient, configmap.Simple) {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		if response.Body == nil {
			response.Body = io.NopCloser(strings.NewReader(""))
		}
		return response, nil
	})}
	config := configmap.Simple{"access_token": token}
	c := newAPIClientWithHTTP(context.Background(), "https://login.test", "https://api.test/b/api", config, user, "password-value", "web", token, 0, httpClient)
	c.signer = signer{now: time.Now, random: func() (uint32, error) { return 1, nil }}
	c.pacer.SetCalculator(&pacer.ZeroDelayCalculator{})
	return c, config
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, code int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": code, "data": data}); err != nil {
		t.Error(err)
	}
}

func TestLoginEmailAndPhoneBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		user string
		key  string
	}{
		{name: "email", user: "person@example.com", key: "mail"},
		{name: "phone", user: "13800138000", key: "passport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received map[string]any
			client, config := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != api.SignInPath {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				writeEnvelope(t, w, 200, map[string]any{"token": "fresh-token"})
			}), tc.user, "")
			if err := client.login(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			if received[tc.key] != tc.user || received["password"] != "password-value" {
				t.Fatalf("unexpected login body: %#v", received)
			}
			if config["access_token"] != "fresh-token" {
				t.Fatal("refreshed token was not persisted")
			}
			if tc.key == "mail" && received["type"] != float64(2) {
				t.Fatalf("email login type missing: %#v", received)
			}
			if tc.key == "passport" && received["remember"] != true {
				t.Fatalf("phone remember flag missing: %#v", received)
			}
		})
	}
}

func TestConcurrent401RefreshesOnce(t *testing.T) {
	var logins atomic.Int64
	var successful atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case api.SignInPath:
			logins.Add(1)
			writeEnvelope(t, w, 200, map[string]any{"token": "new-token"})
		case "/b/api" + api.UserInfoPath:
			if r.Header.Get("Authorization") != "Bearer new-token" {
				writeEnvelope(t, w, 401, nil)
				return
			}
			successful.Add(1)
			writeEnvelope(t, w, 0, map[string]any{"UID": 42})
		default:
			http.NotFound(w, r)
		}
	})
	client, config := testClient(t, handler, "person@example.com", "old-token")
	const workers = 64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var user api.UserInfoData
			errs <- client.do(context.Background(), http.MethodGet, api.UserInfoPath, nil, &user)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if logins.Load() != 1 {
		t.Fatalf("got %d login calls, want 1", logins.Load())
	}
	if successful.Load() != workers {
		t.Fatalf("got %d successful requests, want %d", successful.Load(), workers)
	}
	if config["access_token"] != "new-token" {
		t.Fatal("new token was not persisted")
	}
}

func TestSecond401IsNotRefreshedAgain(t *testing.T) {
	var logins atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == api.SignInPath {
			logins.Add(1)
			writeEnvelope(t, w, 200, map[string]any{"token": "still-bad"})
			return
		}
		writeEnvelope(t, w, 401, nil)
	})
	client, _ := testClient(t, handler, "13800138000", "bad")
	err := client.do(context.Background(), http.MethodGet, api.UserInfoPath, nil, &api.UserInfoData{})
	var apiErr *APIError
	if err == nil || !errorsAsAPI(err, &apiErr) || apiErr.Code != 401 {
		t.Fatalf("expected final 401, got %v", err)
	}
	if logins.Load() != 1 {
		t.Fatalf("got %d login calls, want 1", logins.Load())
	}
}

func TestLoginFailureDoesNotLeakPassword(t *testing.T) {
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(t, w, 403, map[string]any{"detail": "no"})
	}), "person@example.com", "")
	err := client.login(context.Background(), "")
	if err == nil {
		t.Fatal("expected login failure")
	}
	if strings.Contains(fmt.Sprint(err), "password-value") {
		t.Fatalf("password leaked in error: %v", err)
	}
}

func TestEndpointGateCancellation(t *testing.T) {
	gate := endpointGate{last: map[string]time.Time{"/x": time.Now()}, interval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.wait(ctx, "/x"); err != context.Canceled {
		t.Fatalf("got %v, want context canceled", err)
	}
}

func TestNonIdempotentRequestIsNotBlindlyRetried(t *testing.T) {
	var calls atomic.Int64
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		writeEnvelope(t, w, 500, nil)
	}), "person@example.com", "token")
	if err := client.doNonIdempotent(context.Background(), http.MethodPost, "/mutation", struct{}{}, nil); err == nil {
		t.Fatal("expected mutation error")
	}
	if calls.Load() != 1 {
		t.Fatalf("non-idempotent request was called %d times", calls.Load())
	}
}
