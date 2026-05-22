// Package nasaimages is the Bridge adapter for NASA's image APIs:
// mars-photos and image-and-video library search.
// Spec: docs/specs/bridge-adapters-spec-001-v1.1.md.
package nasaimages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "nasa-images"
	adapterVersion = "1.0.0"
	defaultMars    = "https://api.nasa.gov"
	defaultImages  = "https://images-api.nasa.gov"
)

// Adapter is the nasa-images Bridge adapter.
type Adapter struct {
	client          *http.Client
	marsBaseURL     string
	imagesBaseURL   string
}

func New() *Adapter {
	return &Adapter{marsBaseURL: defaultMars, imagesBaseURL: defaultImages}
}

// WithBaseURLs overrides upstream URLs for tests.
func (a *Adapter) WithBaseURLs(mars, images string) *Adapter {
	a.marsBaseURL = mars
	a.imagesBaseURL = images
	return a
}

func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
	if opts.HTTPClient != nil {
		a.client = opts.HTTPClient
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 15 * time.Second}
	}
	return nil
}

func (a *Adapter) Close() error    { return nil }
func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
	return []bridge.OperationSpec{
		{
			Name:        "mars-photos",
			Description: "Mars rover photos for a sol or date.",
			Params: []bridge.ParamSpec{
				{Name: "rover", Type: "string", Required: true, Description: "curiosity | opportunity | spirit | perseverance"},
				{Name: "sol", Type: "integer", Description: "Martian solar day"},
				{Name: "earth_date", Type: "string", Description: "YYYY-MM-DD"},
				{Name: "page", Type: "integer", Default: 1},
			},
			SideEffects: false,
		},
		{
			Name:        "search-images",
			Description: "NASA Image and Video Library search.",
			Params: []bridge.ParamSpec{
				{Name: "q", Type: "string", Required: true},
				{Name: "page", Type: "integer", Default: 1},
				{Name: "media_type", Type: "string", Description: "image | video | audio"},
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
	case "mars-photos":
		return a.marsPhotos(ctx, req)
	case "search-images":
		return a.searchImages(ctx, req)
	default:
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
}

func (a *Adapter) marsPhotos(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	rover, err := bridge.ResolveParam[string](req.Params, "rover", true, "")
	if err != nil {
		return nil, err
	}
	apiKey := req.Credentials["api_key"]
	if apiKey == "" {
		apiKey = "DEMO_KEY"
	}
	u, _ := url.Parse(fmt.Sprintf("%s/mars-photos/api/v1/rovers/%s/photos", a.marsBaseURL, url.PathEscape(rover)))
	qs := u.Query()
	qs.Set("api_key", apiKey)
	if sol, ok := req.Params["sol"]; ok {
		if s, ok2 := sol.(float64); ok2 {
			qs.Set("sol", strconv.Itoa(int(s)))
		}
	}
	if ed, ok := req.Params["earth_date"].(string); ok && ed != "" {
		qs.Set("earth_date", ed)
	}
	if page, _ := bridge.ResolveParam[float64](req.Params, "page", false, 0); page > 0 {
		qs.Set("page", strconv.Itoa(int(page)))
	}
	u.RawQuery = qs.Encode()
	return a.doJSON(ctx, u.String())
}

func (a *Adapter) searchImages(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	q, err := bridge.ResolveParam[string](req.Params, "q", true, "")
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(a.imagesBaseURL + "/search")
	qs := u.Query()
	qs.Set("q", q)
	if mt, _ := bridge.ResolveParam[string](req.Params, "media_type", false, ""); mt != "" {
		qs.Set("media_type", mt)
	}
	if page, _ := bridge.ResolveParam[float64](req.Params, "page", false, 0); page > 0 {
		qs.Set("page", strconv.Itoa(int(page)))
	}
	u.RawQuery = qs.Encode()
	return a.doJSON(ctx, u.String())
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
	raw, readErr := bridge.ReadBodyCapped(resp.Body, bridge.DefaultResponseLimitBytes)
	if readErr != nil {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, readErr)
	}
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
