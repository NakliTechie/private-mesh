package conformance

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// client wraps the http.Client + Hub identity needed to drive the conformance
// suite. It hands out signed Grants and parses success/error envelopes.
type client struct {
	target          string
	rootKey         []byte
	principalID     string
	pubKey          ed25519.PublicKey
	http            *http.Client
}

func newClient(cfg Config) *client {
	pub, _, _ := ed25519.GenerateKey(cryptorand.Reader)
	pid := cfg.PrincipalID
	if pid == "" {
		pid = newULID()
	}
	return &client{
		target:      strings.TrimRight(cfg.Target, "/"),
		rootKey:     cfg.MacaroonRootKey,
		principalID: pid,
		pubKey:      pub,
		http:        cfg.HTTPClient,
	}
}

// mintGrant produces a base64-encoded macaroon signed with the Hub's root key.
// caveats is empty by default; callers append their own constraints.
func (c *client) mintGrant(primitive grant.Primitive, namespace string, ops []string, caveats []string) string {
	now := time.Now().UTC()
	id := grant.Identifier{
		GrantID:           newULID(),
		IssuedAt:          now,
		IssuedByPrincipal: c.principalID,
		IssuedByKeypair:   c.pubKey,
		Scope: grant.Scope{
			Primitive:  primitive,
			Namespace:  namespace,
			Operations: ops,
		},
	}
	out, err := grant.Mint(grant.MintSpec{
		RootKey:    c.rootKey,
		Location:   c.target,
		Identifier: id,
		Caveats:    caveats,
	})
	if err != nil {
		panic("conformance: mintGrant failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(out.Macaroon)
}

// mintInvalidGrant produces a Grant signed with a *different* root key. Used
// for test 28 (signature forgery).
func (c *client) mintInvalidGrant(primitive grant.Primitive) string {
	bogus := make([]byte, 32)
	_, _ = cryptorand.Read(bogus)
	now := time.Now().UTC()
	id := grant.Identifier{
		GrantID:           newULID(),
		IssuedAt:          now,
		IssuedByPrincipal: c.principalID,
		IssuedByKeypair:   c.pubKey,
		Scope: grant.Scope{
			Primitive:  primitive,
			Namespace:  "*",
			Operations: []string{"read", "write"},
		},
	}
	out, _ := grant.Mint(grant.MintSpec{
		RootKey: bogus, Location: c.target, Identifier: id,
	})
	return base64.StdEncoding.EncodeToString(out.Macaroon)
}

// fabricResp mirrors the protocol's response envelope so callers can inspect
// without re-parsing.
type fabricResp struct {
	Status      int
	Headers     http.Header
	OK          bool             `json:"ok"`
	Data        json.RawMessage  `json:"data,omitempty"`
	Error       *fabricErrorBody `json:"error,omitempty"`
	Freshness   *freshness       `json:"freshness,omitempty"`
	RawBody     []byte
}

type fabricErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type freshness struct {
	AsOf         time.Time `json:"as_of"`
	PeersSynced  []string  `json:"peers_synced"`
	PeersMissing []string  `json:"peers_missing"`
	StalenessMs  int64     `json:"staleness_ms"`
}

// do issues a request and parses the envelope. Headers map is sparse — set
// only what the test needs; "Content-Type" is filled when body is non-nil.
func (c *client) do(method, path string, body any, headers map[string]string) (*fabricResp, error) {
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
				return nil, fmt.Errorf("do: marshal body: %w", err)
			}
			rdr = bytes.NewReader(j)
		}
	}
	req, err := http.NewRequest(method, c.target+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		if _, ok := headers["Content-Type"]; !ok {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	out := &fabricResp{
		Status:  resp.StatusCode,
		Headers: resp.Header.Clone(),
		RawBody: respBody,
	}
	if len(respBody) > 0 && strings.Contains(resp.Header.Get("Content-Type"), "json") {
		_ = json.Unmarshal(respBody, out)
	}
	return out, nil
}

// idemKey returns a fresh ULID suitable for X-Fabric-Idempotency-Key.
func idemKey() string { return newULID() }

// newULID returns a fresh ULID string.
func newULID() string {
	id, err := ulid.New(ulid.Timestamp(time.Now()), cryptorand.Reader)
	if err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	return id.String()
}

// b64 is a tiny helper so test code reads cleanly.
func b64(s []byte) string { return base64.StdEncoding.EncodeToString(s) }

// must converts an error into a panic with context, used only inside test
// setup where a non-test step is expected to succeed.
func must(err error, ctx string) {
	if err != nil {
		panic(ctx + ": " + err.Error())
	}
}
