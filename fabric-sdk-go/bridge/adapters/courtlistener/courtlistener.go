// Package courtlistener is the v1.0 Bridge adapter for CourtListener's free
// REST API (US court records, opinions, dockets). Operations: search,
// get-opinion, get-docket. Authoritative: docs/specs/bridge-adapters-spec-001-v1.1.md.
package courtlistener

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "courtlistener"
	adapterVersion = "1.0.0"
	defaultBaseURL = "https://www.courtlistener.com"
)

// Adapter is the courtlistener Bridge adapter. Construct with New() (which
// wires the default HTTP client + base URL), or WithBaseURL for tests.
type Adapter struct {
	client  *http.Client
	baseURL string
}

// New returns the adapter pointing at the public CourtListener API.
func New() *Adapter {
	return &Adapter{baseURL: defaultBaseURL}
}

// WithBaseURL overrides the upstream URL — tests inject httptest.Server.URL.
func (a *Adapter) WithBaseURL(u string) *Adapter { a.baseURL = u; return a }

// Init implements bridge.AdapterWithLifecycle so the Registry can hand us a
// shared HTTP client (configured with the appropriate transport).
func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
	if opts.HTTPClient != nil {
		a.client = opts.HTTPClient
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 15 * time.Second}
	}
	if a.baseURL == "" {
		a.baseURL = defaultBaseURL
	}
	return nil
}

// Close is a no-op; nothing persistent.
func (a *Adapter) Close() error { return nil }

func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
	return []bridge.OperationSpec{
		{
			Name:        "search",
			Description: "Search opinions / dockets.",
			Params: []bridge.ParamSpec{
				{Name: "q", Type: "string", Required: true, Description: "Query string"},
				{Name: "limit", Type: "integer", Default: 20},
				{Name: "type", Type: "string", Description: "o (opinion) | r (docket); default o"},
			},
			SideEffects: false,
			Estimable:   true,
		},
		{
			Name:        "get-opinion",
			Description: "Fetch a single opinion by id.",
			Params: []bridge.ParamSpec{
				{Name: "id", Type: "integer", Required: true},
			},
			SideEffects: false,
		},
		{
			Name:        "get-docket",
			Description: "Fetch a single docket by id.",
			Params: []bridge.ParamSpec{
				{Name: "id", Type: "integer", Required: true},
			},
			SideEffects: false,
		},
	}
}

// Call dispatches by operation name.
func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	switch req.Operation {
	case "search":
		return a.search(ctx, req)
	case "get-opinion":
		return a.getOpinion(ctx, req)
	case "get-docket":
		return a.getDocket(ctx, req)
	default:
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
}

func (a *Adapter) search(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	q, err := bridge.ResolveParam[string](req.Params, "q", true, "")
	if err != nil {
		return nil, err
	}
	limit, _ := bridge.ResolveParam[float64](req.Params, "limit", false, 20)
	if limit <= 0 {
		limit = 20
	}
	typ, _ := bridge.ResolveParam[string](req.Params, "type", false, "o")
	if typ == "" {
		typ = "o"
	}
	u, _ := url.Parse(a.baseURL + "/api/rest/v3/search/")
	qs := u.Query()
	qs.Set("q", q)
	qs.Set("type", typ)
	qs.Set("page_size", strconv.Itoa(int(limit)))
	u.RawQuery = qs.Encode()
	return a.doJSON(ctx, http.MethodGet, u.String(), nil, req.Credentials)
}

func (a *Adapter) getOpinion(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	id, err := bridge.ResolveParam[float64](req.Params, "id", true, 0)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/rest/v3/opinions/%d/", a.baseURL, int(id))
	return a.doJSON(ctx, http.MethodGet, u, nil, req.Credentials)
}

func (a *Adapter) getDocket(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	id, err := bridge.ResolveParam[float64](req.Params, "id", true, 0)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/rest/v3/dockets/%d/", a.baseURL, int(id))
	return a.doJSON(ctx, http.MethodGet, u, nil, req.Credentials)
}

func (a *Adapter) doJSON(ctx context.Context, method, u string, body io.Reader, creds map[string]string) (*bridge.CallResponse, error) {
	start := time.Now()
	r, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/json")
	// CourtListener supports anonymous reads; an API token is optional and
	// goes in the Authorization header per their docs.
	if tok := creds["api_key"]; tok != "" {
		r.Header.Set("Authorization", "Token "+tok)
	}
	resp, err := a.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()
	raw, readErr := bridge.ReadBodyCapped(resp.Body, bridge.DefaultResponseLimitBytes)
	if readErr != nil {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: %s returned %d: %s", bridge.ErrUpstreamUnavailable, u, resp.StatusCode, truncate(string(raw), 256))
	}
	var data map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("%w: parse JSON: %v", bridge.ErrUpstreamUnavailable, err)
		}
	}
	return &bridge.CallResponse{
		Result: data,
		Metrics: bridge.CallMetrics{
			DurationMs: int(time.Since(start).Milliseconds()) + 1,
			BytesIn:    len(raw),
			BytesOut:   contentLength(body),
		},
	}, nil
}

func contentLength(r io.Reader) int {
	if r == nil {
		return 0
	}
	type lengther interface{ Len() int }
	if l, ok := r.(lengther); ok {
		return l.Len()
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
