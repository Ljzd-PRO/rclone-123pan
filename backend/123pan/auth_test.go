package _123pan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fserrors"
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
	writeEnvelopeMessage(t, w, code, "", data)
}

func writeEnvelopeMessage(t *testing.T, w http.ResponseWriter, code int, message string, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message, "data": data}); err != nil {
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

func TestAuthenticationChallengeStopsBeforeRelogin(t *testing.T) {
	var logins atomic.Int64
	client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case api.SignInPath:
			logins.Add(1)
			writeEnvelope(t, w, 200, map[string]any{"token": "unexpected"})
		case "/b/api" + api.UserInfoPath:
			writeEnvelopeMessage(t, w, 401, "当前账号存在安全风险，请使用短信验证码或者微信进行登录。token=challenge-secret", nil)
		default:
			http.NotFound(w, r)
		}
	}), "13800138000", "old-token")

	err := client.do(context.Background(), http.MethodGet, api.UserInfoPath, nil, &api.UserInfoData{})
	if err == nil {
		t.Fatal("expected authentication challenge")
	}
	if !fserrors.IsFatalError(err) {
		t.Fatalf("challenge must be fatal, got %v", err)
	}
	var challenge *AuthenticationChallengeError
	if !errors.As(err, &challenge) {
		t.Fatalf("missing typed challenge in %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 401 {
		t.Fatalf("missing underlying API error in %v", err)
	}
	if logins.Load() != 0 {
		t.Fatalf("challenge triggered %d password logins", logins.Load())
	}
	message := err.Error()
	for _, want := range []string{"https://www.123pan.com/", "rclone config reconnect <remote>:"} {
		if !strings.Contains(message, want) {
			t.Fatalf("challenge guidance %q missing from %q", want, message)
		}
	}
	if strings.Contains(message, "challenge-secret") {
		t.Fatalf("challenge error leaked a secret: %q", message)
	}
	reconnectErr := reconnectChallengeError("my remote", err)
	if !fserrors.IsFatalError(reconnectErr) || !strings.Contains(reconnectErr.Error(), `rclone config reconnect "my remote:"`) {
		t.Fatalf("reconnect guidance is not shell-safe or lost fatal status: %v", reconnectErr)
	}
}

func TestAuthenticationChallengeMarkers(t *testing.T) {
	for _, message := range []string{
		"请完成验证码后重试",
		"Untrusted device: SMS verification required",
		"Please use WeChat login",
		"Account security risk",
	} {
		err := markAuthenticationChallenge(&APIError{HTTPStatus: http.StatusForbidden, Code: 403, Message: message})
		if !isAuthenticationChallengeError(err) || !fserrors.IsFatalError(err) {
			t.Fatalf("message %q was not classified as a fatal challenge: %v", message, err)
		}
	}
	ordinary := markAuthenticationChallenge(&APIError{HTTPStatus: http.StatusForbidden, Code: 403, Message: "password is incorrect"})
	if isAuthenticationChallengeError(ordinary) || fserrors.IsFatalError(ordinary) {
		t.Fatalf("ordinary authentication failure was misclassified: %v", ordinary)
	}
}

func reconnectTestConfig(token string) configmap.Simple {
	return configmap.Simple{
		"user":             "person@example.com",
		"pass":             obscure.MustObscure("password-value"),
		"access_token":     token,
		"platform":         "web",
		"api_min_interval": "0s",
	}
}

func TestReconnectAuthenticationCommitsValidatedToken(t *testing.T) {
	var logins atomic.Int64
	var validations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case api.SignInPath:
			logins.Add(1)
			writeEnvelope(t, w, 200, map[string]any{"token": "validated-token"})
		case "/b/api" + api.UserInfoPath:
			validations.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer validated-token" {
				t.Errorf("unexpected authorization %q", got)
			}
			writeEnvelope(t, w, 0, map[string]any{"UID": 42})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config := reconnectTestConfig("old-token")
	if err := reconnectAuthentication(context.Background(), config, server.URL, server.URL+"/b/api", server.Client()); err != nil {
		t.Fatal(err)
	}
	if config["access_token"] != "validated-token" {
		t.Fatalf("validated token was not committed: %#v", config)
	}
	if logins.Load() != 1 || validations.Load() != 1 {
		t.Fatalf("got %d logins and %d validations", logins.Load(), validations.Load())
	}
}

func TestReconnectAuthenticationPreservesTokenOnChallenge(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.SignInPath {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		writeEnvelopeMessage(t, w, 403, "当前账号存在安全风险，请使用短信验证码登录", nil)
	}))
	defer server.Close()

	config := reconnectTestConfig("known-good-token")
	err := reconnectAuthentication(context.Background(), config, server.URL, server.URL+"/b/api", server.Client())
	if err == nil || !isAuthenticationChallengeError(err) || !fserrors.IsFatalError(err) {
		t.Fatalf("expected fatal challenge, got %v", err)
	}
	if config["access_token"] != "known-good-token" {
		t.Fatalf("challenge replaced persisted token: %#v", config)
	}
	if calls.Load() != 1 {
		t.Fatalf("challenge was retried %d times by the pacer", calls.Load())
	}
}

func TestReconnectAuthenticationPreservesTokenOnValidationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case api.SignInPath:
			writeEnvelope(t, w, 200, map[string]any{"token": "unvalidated-token"})
		case "/b/api" + api.UserInfoPath:
			writeEnvelopeMessage(t, w, 403, "account is disabled", nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	config := reconnectTestConfig("old-token")
	err := reconnectAuthentication(context.Background(), config, server.URL, server.URL+"/b/api", server.Client())
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if config["access_token"] != "old-token" {
		t.Fatalf("unvalidated token was committed: %#v", config)
	}
}

func TestConfigRejectsUnexpectedState(t *testing.T) {
	out, err := Config(context.Background(), "remote", configmap.Simple{}, fs.ConfigIn{State: "unexpected"})
	if err == nil || out != nil || !strings.Contains(err.Error(), "unsupported 123Pan config state") {
		t.Fatalf("unexpected Config result out=%#v err=%v", out, err)
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

func TestIdempotentRequestRetriesTransientHTTPStatuses(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int64
			client, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					writeEnvelope(t, w, status, nil)
					return
				}
				writeEnvelope(t, w, 0, map[string]any{"UID": 42})
			}), "person@example.com", "token")
			var user api.UserInfoData
			if err := client.do(context.Background(), http.MethodGet, api.UserInfoPath, nil, &user); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatalf("HTTP %d 后调用次数 = %d，预期 2", status, calls.Load())
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("17"); got != 17*time.Second {
		t.Fatalf("seconds Retry-After parsed as %v", got)
	}
	if got := parseRetryAfter("invalid"); got != 0 {
		t.Fatalf("invalid Retry-After parsed as %v", got)
	}
}
