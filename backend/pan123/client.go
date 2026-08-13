package pan123

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ljzd/rclone-123pan/backend/pan123/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
)

const maxControlResponse = 4 << 20

var retryHTTPStatuses = []int{http.StatusRequestTimeout, http.StatusTooManyRequests, 500, 501, 502, 503, 504, 505, 506, 507, 508, 510, 511}

type endpointGate struct {
	mu       sync.Mutex
	last     map[string]time.Time
	interval time.Duration
}

func (g *endpointGate) wait(ctx context.Context, endpoint string) error {
	if g.interval <= 0 {
		return nil
	}
	g.mu.Lock()
	now := time.Now()
	slot := g.last[endpoint].Add(g.interval)
	if slot.Before(now) {
		slot = now
	}
	g.last[endpoint] = slot
	g.mu.Unlock()
	wait := time.Until(slot)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return nil
}

type apiClient struct {
	loginRoot string
	apiRoot   string
	loginSrv  *rest.Client
	apiSrv    *rest.Client
	pacer     *fs.Pacer
	signer    signer
	gate      endpointGate
	config    configmap.Mapper
	user      string
	password  string
	platform  string

	tokenMu   sync.RWMutex
	token     string
	refreshMu sync.Mutex
}

func newAPIClient(ctx context.Context, loginRoot, apiRoot string, config configmap.Mapper, user, password, platform, token string, interval time.Duration) *apiClient {
	return newAPIClientWithHTTP(ctx, loginRoot, apiRoot, config, user, password, platform, token, interval, fshttp.NewClient(ctx))
}

func newAPIClientWithHTTP(ctx context.Context, loginRoot, apiRoot string, config configmap.Mapper, user, password, platform, token string, interval time.Duration, httpClient *http.Client) *apiClient {
	return &apiClient{
		loginRoot: loginRoot,
		apiRoot:   apiRoot,
		loginSrv:  rest.NewClient(httpClient).SetRoot(loginRoot),
		apiSrv:    rest.NewClient(httpClient).SetRoot(apiRoot),
		pacer: fs.NewPacer(ctx, pacer.NewDefault(
			pacer.MinSleep(100*time.Millisecond),
			pacer.MaxSleep(10*time.Second),
			pacer.DecayConstant(2),
		)),
		signer:   newSigner(),
		gate:     endpointGate{last: make(map[string]time.Time), interval: interval},
		config:   config,
		user:     user,
		password: password,
		platform: platform,
		token:    token,
	}
}

func (c *apiClient) getToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *apiClient) setToken(token string) {
	c.tokenMu.Lock()
	c.token = token
	c.tokenMu.Unlock()
}

func (c *apiClient) login(ctx context.Context, badToken string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if current := c.getToken(); current != badToken && current != "" {
		return nil
	}
	body := map[string]any{"password": c.password}
	if strings.Contains(c.user, "@") {
		body["mail"] = c.user
		body["type"] = 2
	} else {
		body["passport"] = c.user
		body["remember"] = true
	}
	var data api.LoginData
	if err := c.call(ctx, c.loginSrv, http.MethodPost, api.SignInPath, body, 200, "", false, true, &data); err != nil {
		return err
	}
	if data.Token == "" {
		return &APIError{HTTPStatus: http.StatusOK, Code: 200, Message: "login returned an empty token"}
	}
	c.setToken(data.Token)
	c.config.Set("access_token", data.Token)
	return nil
}

func (c *apiClient) do(ctx context.Context, method, path string, request, response any) error {
	badToken := c.getToken()
	err := c.call(ctx, c.apiSrv, method, path, request, 0, badToken, true, true, response)
	var apiErr *APIError
	if err == nil || !errorsAsAPI(err, &apiErr) || apiErr.Code != 401 {
		return err
	}
	if err := c.login(ctx, badToken); err != nil {
		return err
	}
	return c.call(ctx, c.apiSrv, method, path, request, 0, c.getToken(), true, true, response)
}

// doNonIdempotent never blindly retries an ambiguous transport or HTTP
// failure. A 401 is safe to refresh once because authentication failure means
// the operation was rejected before application.
func (c *apiClient) doNonIdempotent(ctx context.Context, method, path string, request, response any) error {
	badToken := c.getToken()
	err := c.call(ctx, c.apiSrv, method, path, request, 0, badToken, true, false, response)
	var apiErr *APIError
	if err == nil || !errorsAsAPI(err, &apiErr) || apiErr.Code != 401 {
		return err
	}
	if err := c.login(ctx, badToken); err != nil {
		return err
	}
	return c.call(ctx, c.apiSrv, method, path, request, 0, c.getToken(), true, false, response)
}

func errorsAsAPI(err error, target **APIError) bool {
	for err != nil {
		if typed, ok := err.(*APIError); ok {
			*target = typed
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (c *apiClient) call(ctx context.Context, srv *rest.Client, method, path string, request any, wantCode int, token string, signed, allowRetry bool, response any) error {
	var payload []byte
	var err error
	if request != nil {
		payload, err = json.Marshal(request)
		if err != nil {
			return fmt.Errorf("encode 123Pan request: %w", err)
		}
	}
	var finalErr error
	call := c.pacer.Call
	if !allowRetry {
		call = c.pacer.CallNoRetry
	}
	err = call(func() (bool, error) {
		endpoint := path
		if query := strings.IndexByte(endpoint, '?'); query >= 0 {
			endpoint = endpoint[:query]
		}
		if err := c.gate.wait(ctx, endpoint); err != nil {
			return false, err
		}
		rootURL := ""
		if signed {
			rootURL, err = c.signer.sign(c.apiRoot + path)
			if err != nil {
				return false, err
			}
		}
		opts := rest.Opts{
			Method:       method,
			Path:         path,
			RootURL:      rootURL,
			IgnoreStatus: true,
			ExtraHeaders: map[string]string{
				"Origin":      webOrigin,
				"Referer":     webOrigin + "/",
				"User-Agent":  "rclone-123/alpha",
				"Platform":    c.platform,
				"App-Version": protocolVersion,
			},
		}
		if signed {
			opts.Path = ""
			opts.ExtraHeaders["Authorization"] = "Bearer " + token
		}
		if payload != nil {
			opts.Body = bytes.NewReader(payload)
			opts.ContentType = "application/json"
			length := int64(len(payload))
			opts.ContentLength = &length
		}
		resp, callErr := srv.Call(ctx, &opts)
		if callErr != nil {
			finalErr = callErr
			if fserrors.ContextError(ctx, &finalErr) {
				return false, finalErr
			}
			return fserrors.ShouldRetry(callErr), callErr
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxControlResponse+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			finalErr = readErr
			return fserrors.ShouldRetry(readErr), readErr
		}
		if closeErr != nil {
			finalErr = closeErr
			return fserrors.ShouldRetry(closeErr), closeErr
		}
		if len(body) > maxControlResponse {
			finalErr = errorsNewProtocol("control response exceeds 4 MiB")
			return false, finalErr
		}
		finalErr = decodeEnvelope(body, resp.StatusCode, wantCode, response)
		if fserrors.ShouldRetryHTTP(resp, retryHTTPStatuses) {
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > 0 {
				finalErr = pacer.RetryAfterError(finalErr, retryAfter)
			}
			return true, finalErr
		}
		return false, finalErr
	})
	if err != nil {
		return err
	}
	return finalErr
}

func errorsNewProtocol(message string) error { return fmt.Errorf("123Pan protocol error: %s", message) }

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(time.Until(when), 0)
	}
	return 0
}
