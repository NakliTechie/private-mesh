// Package httpc is the CLI's HTTP client for talking to a Hub. Wraps
// fabric-sdk-go/grant for macaroon construction; the network layer is
// stdlib net/http until the SDK's transport package lands.
package httpc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Client is a thin Hub HTTP client.
type Client struct {
	target string
	http   *http.Client
}

// New returns a Client targeting the given Hub URL.
func New(target string) *Client {
	return &Client{
		target: strings.TrimRight(target, "/"),
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// WithTimeout overrides the default 30s request timeout.
func (c *Client) WithTimeout(d time.Duration) *Client {
	c.http.Timeout = d
	return c
}

// Target returns the configured Hub URL.
func (c *Client) Target() string { return c.target }

// Response is the parsed Hub response envelope.
type Response struct {
	Status   int
	Headers  http.Header
	OK       bool             `json:"ok"`
	Data     json.RawMessage  `json:"data,omitempty"`
	Error    *ResponseError   `json:"error,omitempty"`
	Raw      []byte
}

// ResponseError matches the Hub's error envelope.
type ResponseError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Do executes a request against the Hub and parses the envelope.
func (c *Client) Do(method, path string, body any, headers map[string]string) (*Response, error) {
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rdr = bytes.NewReader(b)
		case string:
			rdr = strings.NewReader(b)
		default:
			j, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("httpc.Do: marshal body: %w", err)
			}
			rdr = bytes.NewReader(j)
		}
	}
	req, err := http.NewRequest(method, c.target+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpc.Do %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := &Response{
		Status:  resp.StatusCode,
		Headers: resp.Header.Clone(),
		Raw:     raw,
	}
	if len(raw) > 0 && strings.Contains(resp.Header.Get("Content-Type"), "json") {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, fmt.Errorf("httpc.Do: unmarshal envelope: %w (body=%q)", err, string(raw))
		}
	}
	return out, nil
}

// AuthHeaders builds the standard request-time header map: X-Fabric-Grant
// always; X-Fabric-Idempotency-Key for state-changing operations.
func AuthHeaders(grantB64, idemKey string) map[string]string {
	h := map[string]string{"X-Fabric-Grant": grantB64}
	if idemKey != "" {
		h["X-Fabric-Idempotency-Key"] = idemKey
	}
	return h
}

// NewIdempotencyKey returns a fresh ULID-shaped idempotency key.
func NewIdempotencyKey() string {
	id, err := ulid.New(ulid.Now(), nil)
	if err != nil {
		return fmt.Sprintf("idem-%d", time.Now().UnixNano())
	}
	return id.String()
}

// Errorf wraps the response's structured error into a Go error suitable for
// printing.
func (r *Response) Errorf() error {
	if r.Error != nil {
		return fmt.Errorf("Hub error %s: %s (status %d)", r.Error.Code, r.Error.Message, r.Status)
	}
	if r.Status >= 400 {
		return fmt.Errorf("Hub returned status %d (body=%q)", r.Status, string(r.Raw))
	}
	return nil
}

// B64 is a convenience for base64-encoding bytes for header values.
func B64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// FromB64 decodes; returns an error pointing at the offending value.
func FromB64(s string) ([]byte, error) {
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return out, nil
}

// ErrNoData is returned when a caller expects a Data field but the response
// has none (e.g., an error envelope).
var ErrNoData = errors.New("httpc: response has no data field")
