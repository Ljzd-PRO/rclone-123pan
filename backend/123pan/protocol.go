package _123pan

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
)

const (
	webOrigin        = "https://yun.123pan.cn"
	protocolPlatform = "web"
	protocolVersion  = "3"
)

var digitTable = [...]byte{'a', 'd', 'e', 'f', 'g', 'h', 'l', 'm', 'y', 'i', 'j', 'n', 'o', 'p', 'k', 'q', 'r', 's', 't', 'u', 'b', 'c', 'v', 'w', 's', 'z'}

// signer has injected time and randomness so the private protocol can be
// regression-tested without weakening production randomness.
type signer struct {
	now    func() time.Time
	random func() (uint32, error)
}

func newSigner() signer {
	return signer{
		now: time.Now,
		random: func() (uint32, error) {
			var b [4]byte
			if _, err := rand.Read(b[:]); err != nil {
				return 0, err
			}
			return binary.LittleEndian.Uint32(b[:]) % 10_000_001, nil
		},
	}
}

func (s signer) sign(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse API URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return "", errors.New("API URL must be absolute")
	}
	r, err := s.random()
	if err != nil {
		return "", fmt.Errorf("generate signature nonce: %w", err)
	}
	now := s.now()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	digits := now.In(time.FixedZone("UTC+8", 8*60*60)).Format("200601021504")
	mapped := make([]byte, len(digits))
	for i := range digits {
		if digits[i] < '0' || digits[i] > '9' {
			return "", errors.New("unexpected time digit")
		}
		mapped[i] = digitTable[digits[i]-'0']
	}
	timeSign := strconv.FormatUint(uint64(crc32.ChecksumIEEE(mapped)), 10)
	nonce := strconv.FormatUint(uint64(r), 10)
	material := strings.Join([]string{timestamp, nonce, u.Path, protocolPlatform, protocolVersion, timeSign}, "|")
	value := strings.Join([]string{timestamp, nonce, strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(material))), 10)}, "-")
	q := u.Query()
	q.Add(timeSign, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// APIError preserves actionable protocol fields without retaining request or
// response secrets.
type APIError struct {
	HTTPStatus int
	Code       int
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	parts := []string{"123Pan API error"}
	if e.HTTPStatus != 0 {
		parts = append(parts, "HTTP "+strconv.Itoa(e.HTTPStatus))
	}
	parts = append(parts, "code "+strconv.Itoa(e.Code))
	if message := scrubSecrets(e.Message); message != "" {
		parts = append(parts, message)
	}
	if e.RequestID != "" {
		parts = append(parts, "request "+scrubSecrets(e.RequestID))
	}
	return strings.Join(parts, ": ")
}

func decodeEnvelope(body []byte, httpStatus, wantCode int, out any) error {
	var envelope api.RawEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode 123Pan response: %w", err)
	}
	if envelope.Code == nil {
		return &APIError{HTTPStatus: httpStatus, Code: -1, Message: "response is missing required code", RequestID: envelope.RequestID}
	}
	if httpStatus < http.StatusOK || httpStatus >= http.StatusMultipleChoices || *envelope.Code != wantCode {
		return &APIError{HTTPStatus: httpStatus, Code: *envelope.Code, Message: envelope.Message, RequestID: envelope.RequestID}
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode 123Pan response data: %w", err)
	}
	return nil
}

var (
	bearerRE = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)
	secretRE = regexp.MustCompile(`(?i)(access_token|token|authorization|cookie|password|pass|signature|sign|auth-key)=([^\s&]+)`)
	urlRE    = regexp.MustCompile(`https?://[^\s]+`)
)

func scrubSecrets(input string) string {
	input = bearerRE.ReplaceAllString(input, "Bearer REDACTED")
	input = secretRE.ReplaceAllString(input, "$1=REDACTED")
	return urlRE.ReplaceAllStringFunc(input, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery == "" {
			return raw
		}
		u.RawQuery = "REDACTED"
		return u.String()
	})
}
