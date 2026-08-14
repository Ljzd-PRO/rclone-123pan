package _123pan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
)

const maxRedirectResponse = 1 << 20

var contentRangeRE = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+|\*)$`)

type exactReadCloser struct {
	inner     io.ReadCloser
	remaining int64
	checked   bool
}

func (r *exactReadCloser) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:r.remaining]
		}
		n, err := r.inner.Read(p)
		r.remaining -= int64(n)
		if err == io.EOF && r.remaining > 0 {
			return n, io.ErrUnexpectedEOF
		}
		return n, err
	}
	if r.checked {
		return 0, io.EOF
	}
	r.checked = true
	var one [1]byte
	n, err := r.inner.Read(one[:])
	if n > 0 {
		return 0, errorsNewProtocol("download response exceeded expected length")
	}
	if err == nil {
		return 0, errorsNewProtocol("download reader made no progress after expected length")
	}
	if err == io.EOF {
		return 0, io.EOF
	}
	return 0, err
}

func (r *exactReadCloser) Close() error { return r.inner.Close() }

func parseAbsoluteHTTPURL(raw, description string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid %s URL", description)
	}
	return u, nil
}

func unwrapDownloadURL(raw string) (*url.URL, error) {
	u, err := parseAbsoluteHTTPURL(raw, "download")
	if err != nil {
		return nil, err
	}
	encoded := u.Query().Get("params")
	if encoded == "" {
		return u, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errorsNewProtocol("download params is not valid base64")
	}
	return parseAbsoluteHTTPURL(string(decoded), "decoded download")
}

func (o *Object) requestDownloadURL(ctx context.Context) (*url.URL, error) {
	request := map[string]any{
		"driveId":   0,
		"etag":      o.metadata.ETag,
		"fileId":    o.id,
		"fileName":  o.metadata.FileName,
		"s3keyFlag": o.metadata.S3KeyFlag,
		"size":      o.size,
		"type":      o.metadata.Type,
	}
	var data api.DownloadInfoData
	if err := o.fs.client.do(ctx, http.MethodPost, api.DownloadInfoPath, request, &data); err != nil {
		return nil, err
	}
	if data.DownloadURL == "" {
		return nil, errorsNewProtocol("non-empty object has no download URL")
	}
	return unwrapDownloadURL(data.DownloadURL)
}

func (o *Object) resolveFinalURL(ctx context.Context, first *url.URL) (*url.URL, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, first.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Referer", webOrigin+"/")
	client := *o.fs.downloadClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	referer := first.Scheme + "://" + first.Host + "/"
	if response.StatusCode == http.StatusFound {
		location := response.Header.Get("Location")
		if location == "" {
			return nil, "", errorsNewProtocol("download redirect has no Location")
		}
		reference, err := url.Parse(location)
		if err != nil {
			return nil, "", errorsNewProtocol("download redirect has an invalid Location")
		}
		final := first.ResolveReference(reference)
		if _, err := parseAbsoluteHTTPURL(final.String(), "final download"); err != nil {
			return nil, "", err
		}
		return final, referer, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("resolve download URL: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRedirectResponse+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxRedirectResponse {
		return nil, "", errorsNewProtocol("download redirect response exceeds 1 MiB")
	}
	var redirect struct {
		Data struct {
			RedirectURL string `json:"redirect_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &redirect); err != nil {
		return nil, "", fmt.Errorf("decode download redirect: %w", err)
	}
	final, err := parseAbsoluteHTTPURL(redirect.Data.RedirectURL, "final download")
	if err != nil {
		return nil, "", err
	}
	return final, referer, nil
}

func expectedDownloadRange(options []fs.OpenOption, size int64) (rangeHeader string, start, end int64, ranged bool, err error) {
	start, end = 0, size-1
	for _, option := range options {
		key, value := option.Header()
		if !strings.EqualFold(key, "Range") || value == "" {
			continue
		}
		if ranged {
			return "", 0, 0, false, errorsNewProtocol("multiple download ranges are unsupported")
		}
		ranged = true
		rangeHeader = value
		parsed, parseErr := fs.ParseRangeOption(value)
		if parseErr != nil || parsed.Start < 0 || parsed.End < parsed.Start {
			return "", 0, 0, false, errorsNewProtocol("invalid normalized download range")
		}
		start, end = parsed.Start, parsed.End
	}
	return rangeHeader, start, end, ranged, nil
}

func validateDownloadResponse(response *http.Response, size, start, end int64, ranged bool) (int64, error) {
	expected := end - start + 1
	if expected < 0 {
		return 0, errorsNewProtocol("negative expected download length")
	}
	if ranged {
		if response.StatusCode != http.StatusPartialContent {
			return 0, fmt.Errorf("ranged download returned HTTP %d instead of 206", response.StatusCode)
		}
		match := contentRangeRE.FindStringSubmatch(response.Header.Get("Content-Range"))
		if match == nil {
			return 0, errorsNewProtocol("ranged download has invalid Content-Range")
		}
		gotStart, _ := strconv.ParseInt(match[1], 10, 64)
		gotEnd, _ := strconv.ParseInt(match[2], 10, 64)
		if gotStart != start || gotEnd != end {
			return 0, errorsNewProtocol("ranged download Content-Range does not match request")
		}
		if match[3] != "*" {
			total, parseErr := strconv.ParseInt(match[3], 10, 64)
			if parseErr != nil || total != size {
				return 0, errorsNewProtocol("ranged download Content-Range has wrong object size")
			}
		}
	} else if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download returned HTTP %d instead of 200", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != expected {
		return 0, errorsNewProtocol("download Content-Length does not match expected length")
	}
	return expected, nil
}

func (o *Object) openOnce(ctx context.Context, options []fs.OpenOption) (io.ReadCloser, int, error) {
	first, err := o.requestDownloadURL(ctx)
	if err != nil {
		return nil, 0, err
	}
	final, referer, err := o.resolveFinalURL(ctx, first)
	if err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, final.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Referer", referer)
	fs.OpenOptionAddHTTPHeaders(request.Header, options)
	response, err := o.fs.downloadClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	rangeHeader, start, end, ranged, err := expectedDownloadRange(options, o.size)
	_ = rangeHeader
	if err != nil {
		response.Body.Close()
		return nil, response.StatusCode, err
	}
	expected, err := validateDownloadResponse(response, o.size, start, end, ranged)
	if err != nil {
		response.Body.Close()
		return nil, response.StatusCode, err
	}
	return &exactReadCloser{inner: response.Body, remaining: expected}, response.StatusCode, nil
}

// Open retrieves a signed CDN URL and validates range and length semantics.
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	if o.size == 0 {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	normalized := append([]fs.OpenOption(nil), options...)
	fs.FixRangeOption(normalized, o.size)
	for attempt := 0; attempt < 2; attempt++ {
		reader, status, err := o.openOnce(ctx, normalized)
		if err == nil {
			return reader, nil
		}
		if status != http.StatusForbidden || attempt != 0 {
			return nil, err
		}
	}
	return nil, errors.New("unreachable download retry state")
}
