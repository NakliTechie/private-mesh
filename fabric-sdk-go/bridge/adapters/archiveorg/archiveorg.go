// Package archiveorg is the Bridge adapter for Internet Archive's public
// APIs (Wayback, item retrieval, advanced search). All operations are
// anonymous reads. Spec: docs/specs/bridge-adapters-spec-001-v1.1.md.
package archiveorg

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
	adapterName    = "archive-org"
	adapterVersion = "1.0.0"
	defaultBaseURL = "https://archive.org"
)

type Adapter struct {
	client  *http.Client
	baseURL string
}

func New() *Adapter                    { return &Adapter{baseURL: defaultBaseURL} }
func (a *Adapter) WithBaseURL(u string) *Adapter { a.baseURL = u; return a }

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
func (a *Adapter) Close() error    { return nil }
func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
	return []bridge.OperationSpec{
		{
			Name:        "wayback-get",
			Description: "Look up the closest Wayback snapshot for a URL.",
			Params: []bridge.ParamSpec{
				{Name: "url", Type: "string", Required: true},
				{Name: "timestamp", Type: "string", Description: "YYYYMMDD or YYYYMMDDhhmmss"},
			},
			SideEffects: false,
		},
		{
			Name:        "search",
			Description: "Advanced Search API (scoped to public items).",
			Params: []bridge.ParamSpec{
				{Name: "q", Type: "string", Required: true},
				{Name: "rows", Type: "integer", Default: 20},
				{Name: "page", Type: "integer", Default: 1},
			},
			SideEffects: false,
		},
		{
			Name:        "get-item",
			Description: "Fetch metadata for a single Archive item.",
			Params: []bridge.ParamSpec{
				{Name: "identifier", Type: "string", Required: true},
			},
			SideEffects: false,
		},
	}
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	switch req.Operation {
	case "wayback-get":
		return a.waybackGet(ctx, req)
	case "search":
		return a.search(ctx, req)
	case "get-item":
		return a.getItem(ctx, req)
	default:
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
}

func (a *Adapter) waybackGet(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	target, err := bridge.ResolveParam[string](req.Params, "url", true, "")
	if err != nil {
		return nil, err
	}
	timestamp, _ := bridge.ResolveParam[string](req.Params, "timestamp", false, "")
	u, _ := url.Parse(a.baseURL + "/wayback/available")
	qs := u.Query()
	qs.Set("url", target)
	if timestamp != "" {
		qs.Set("timestamp", timestamp)
	}
	u.RawQuery = qs.Encode()
	return a.doJSON(ctx, u.String())
}

func (a *Adapter) search(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	q, err := bridge.ResolveParam[string](req.Params, "q", true, "")
	if err != nil {
		return nil, err
	}
	rows, _ := bridge.ResolveParam[float64](req.Params, "rows", false, 20)
	if rows <= 0 {
		rows = 20
	}
	page, _ := bridge.ResolveParam[float64](req.Params, "page", false, 1)
	if page <= 0 {
		page = 1
	}
	u, _ := url.Parse(a.baseURL + "/advancedsearch.php")
	qs := u.Query()
	qs.Set("q", q)
	qs.Set("rows", strconv.Itoa(int(rows)))
	qs.Set("page", strconv.Itoa(int(page)))
	qs.Set("output", "json")
	u.RawQuery = qs.Encode()
	return a.doJSON(ctx, u.String())
}

func (a *Adapter) getItem(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	id, err := bridge.ResolveParam[string](req.Params, "identifier", true, "")
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/metadata/%s", a.baseURL, url.PathEscape(id))
	return a.doJSON(ctx, u)
}

func (a *Adapter) doJSON(ctx context.Context, u string) (*bridge.CallResponse, error) {
	start := time.Now()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	r.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: %s returned %d", bridge.ErrUpstreamUnavailable, u, resp.StatusCode)
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
		},
	}, nil
}
