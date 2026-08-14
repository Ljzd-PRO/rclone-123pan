package _123pan

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignerGolden(t *testing.T) {
	now := time.Date(2025, time.December, 31, 16, 0, 0, 0, time.UTC)
	s := signer{now: func() time.Time { return now }, random: func() (uint32, error) { return 1234567, nil }}
	got, err := s.sign("https://api.123278.com/b/api/file/list/new?existing=yes")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.123278.com/b/api/file/list/new?"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("signed URL %q does not have prefix %q", got, want)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("existing") != "yes" {
		t.Fatal("existing query parameter was lost")
	}
	// The exact signature is a compatibility tripwire for UTC+8 day rollover.
	if got != "https://api.123278.com/b/api/file/list/new?2543441651=1767196800-1234567-3144878440&existing=yes" {
		t.Fatalf("signature changed: %s", got)
	}
}

func TestDecodeEnvelopeStrict(t *testing.T) {
	var out struct {
		Token string `json:"token"`
	}
	if err := decodeEnvelope([]byte(`{"code":200,"data":{"token":"ok"}}`), 200, 200, &out); err != nil {
		t.Fatal(err)
	}
	if out.Token != "ok" {
		t.Fatalf("unexpected token %q", out.Token)
	}
	for _, body := range []string{`{"data":{}}`, `{"code":0`, `{"code":401,"message":"expired"}`} {
		if err := decodeEnvelope([]byte(body), 200, 0, nil); err == nil {
			t.Fatalf("accepted invalid response %s", body)
		}
	}
	var apiErr *APIError
	err := decodeEnvelope([]byte(`{"code":401,"message":"expired","request_id":"req-1"}`), 200, 0, nil)
	if !errors.As(err, &apiErr) || apiErr.Code != 401 || apiErr.RequestID != "req-1" {
		t.Fatalf("typed error lost fields: %#v", err)
	}
}

func TestScrubSecrets(t *testing.T) {
	got := scrubSecrets("Bearer abc.def token=secret https://s3.example/x?signature=secret&part=1 password=hunter2")
	for _, secret := range []string{"abc.def", "secret", "hunter2", "part=1"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked in %q", secret, got)
		}
	}
}

func FuzzDecodeEnvelope(f *testing.F) {
	f.Add([]byte(`{"code":0,"data":{}}`))
	f.Add([]byte(`{"data":null}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_ = decodeEnvelope(body, 200, 0, nil)
	})
}
