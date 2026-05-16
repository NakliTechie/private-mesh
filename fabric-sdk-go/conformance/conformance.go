// Package conformance implements the 32-test conformance suite from
// fabric-spec-001-v1.0.md §"Conformance". The suite drives a running transport
// (typically nakli-hub) over HTTP. The same RunAll function is invoked by:
//
//   - `nakli-hub conformance --target <url> --data-dir <dir>` for a black-box
//     run against a separately-running Hub.
//   - The package's own `go test` harness, which spins up an in-process Hub
//     fixture (see conformance_test.go).
//
// `nakli-cli conformance` will land at M4 wiring this same package.
package conformance

import (
	"net/http"
	"time"
)

// Config carries every input RunAll needs. Both fields named *Key are byte
// slices owned by the caller; the suite never serializes them.
type Config struct {
	// Target is the full URL of the Hub, e.g. "http://127.0.0.1:7842".
	Target string
	// MacaroonRootKey is the Hub's macaroon HMAC root key — needed to mint
	// Grants the Hub will accept. Read from hub-identity.json by callers.
	MacaroonRootKey []byte
	// HTTPClient lets callers inject a custom client (timeouts, cookies).
	// nil means a default client with a 10s timeout is used.
	HTTPClient *http.Client
	// PrincipalID is the issuer principal the synthetic Grants will name. A
	// fresh ULID is generated if empty.
	PrincipalID string
	// Verbose causes RunAll to print per-test progress to stderr. The CLI
	// passes true; `go test` leaves it false to avoid duplicating output.
	Verbose bool
}

// RunAll executes the full 32-test conformance suite against cfg.Target and
// returns the aggregate Results. RunAll never panics; per-test panics are
// trapped and reported as failures.
func RunAll(cfg Config) *Results {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	c := newClient(cfg)
	out := &Results{Started: time.Now().UTC()}
	for _, t := range allTests() {
		r := runOne(c, t)
		out.add(r)
		if cfg.Verbose {
			printProgress(r)
		}
	}
	out.Finished = time.Now().UTC()
	return out
}

// testEntry is a single numbered conformance test.
type testEntry struct {
	ID    int
	Group string
	Name  string
	Run   func(*client) error
}

// allTests returns the 32 tests in their authoritative order (spec lines
// 805–853). Adding tests outside this catalogue is a M9.x extension.
func allTests() []testEntry {
	out := []testEntry{}
	out = append(out, wireTests()...)
	out = append(out, grantTests()...)
	out = append(out, idempotencyTests()...)
	out = append(out, vaultHistoryTests()...)
	out = append(out, failureModelTests()...)
	out = append(out, adversarialTests()...)
	return out
}

func runOne(c *client, t testEntry) Result {
	r := Result{ID: t.ID, Group: t.Group, Name: t.Name}
	start := time.Now()
	defer func() {
		if x := recover(); x != nil {
			r.Passed = false
			r.Message = "panic: " + asString(x)
		}
		r.DurationMs = time.Since(start).Milliseconds()
	}()
	if err := t.Run(c); err != nil {
		r.Passed = false
		r.Message = err.Error()
		return r
	}
	r.Passed = true
	return r
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "(unknown panic value)"
}
