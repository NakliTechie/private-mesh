// Package openaicompatible is the Bridge adapter for OpenAI-compatible APIs
// (OpenRouter, Together, vLLM, LM Studio, etc.). Operations: chat-completions,
// completions, embeddings. The base URL is per-call so a single adapter can
// target any compatible provider.
package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "openai-compatible"
	adapterVersion = "1.0.0"
	defaultBaseURL = "https://api.openai.com"
)

type Adapter struct {
	client         *http.Client
	defaultBaseURL string
}

func New() *Adapter { return &Adapter{defaultBaseURL: defaultBaseURL} }

// WithDefaultBaseURL sets the fallback used when a call doesn't provide its
// own base_url param.
func (a *Adapter) WithDefaultBaseURL(u string) *Adapter { a.defaultBaseURL = u; return a }

func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
	if opts.HTTPClient != nil {
		a.client = opts.HTTPClient
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 120 * time.Second}
	}
	if a.defaultBaseURL == "" {
		a.defaultBaseURL = defaultBaseURL
	}
	return nil
}
func (a *Adapter) Close() error    { return nil }
func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
	commonAuth := []bridge.ParamSpec{
		{Name: "base_url", Type: "string", Description: "Override the default base URL"},
		{Name: "model", Type: "string", Required: true},
	}
	return []bridge.OperationSpec{
		{
			Name:        "chat-completions",
			Description: "POST /v1/chat/completions",
			Params: append([]bridge.ParamSpec{}, append(commonAuth, []bridge.ParamSpec{
				{Name: "messages", Type: "array", Required: true},
				{Name: "max_tokens", Type: "integer"},
				{Name: "temperature", Type: "number"},
				{Name: "stream", Type: "boolean", Default: false},
			}...)...),
		},
		{
			Name:        "completions",
			Description: "POST /v1/completions (legacy)",
			Params: append([]bridge.ParamSpec{}, append(commonAuth, []bridge.ParamSpec{
				{Name: "prompt", Type: "string", Required: true},
				{Name: "max_tokens", Type: "integer"},
			}...)...),
		},
		{
			Name:        "embeddings",
			Description: "POST /v1/embeddings",
			Params: append([]bridge.ParamSpec{}, append(commonAuth, []bridge.ParamSpec{
				{Name: "input", Type: "array", Required: true},
			}...)...),
		},
	}
}

// EffectiveHost implements bridge.AdapterEffectiveHost. The outbound
// destination is params.base_url when set, else the adapter's configured
// default. Returning the parsed Host lets the Hub enforce
// `only-domain in [...]` caveats against the real target rather than
// the caller-supplied req.Domain.
func (a *Adapter) EffectiveHost(params map[string]any) (string, error) {
	base := a.defaultBaseURL
	if v, ok := params["base_url"].(string); ok && v != "" {
		base = v
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("%w: base_url: %v", bridge.ErrInvalidParam, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: base_url has no host", bridge.ErrInvalidParam)
	}
	return u.Hostname(), nil
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	var endpoint string
	switch req.Operation {
	case "chat-completions":
		endpoint = "/v1/chat/completions"
	case "completions":
		endpoint = "/v1/completions"
	case "embeddings":
		endpoint = "/v1/embeddings"
	default:
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
	apiKey, err := bridge.ResolveCredential(req.Credentials, "api_key")
	if err != nil {
		return nil, err
	}
	base := a.defaultBaseURL
	if v, ok := req.Params["base_url"].(string); ok && v != "" {
		base = v
	}
	base = strings.TrimRight(base, "/")

	body := map[string]any{}
	for k, v := range req.Params {
		if k == "base_url" {
			continue
		}
		body[k] = v
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", bridge.ErrInvalidParam, err)
	}
	start := time.Now()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, bytes.NewReader(jsonBody))
	r.Header.Set("Authorization", "Bearer "+apiKey)
	r.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		r.Header.Set("Idempotency-Key", req.IdempotencyKey)
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
		return nil, fmt.Errorf("%w: upstream returned %d: %s", bridge.ErrUpstreamUnavailable, resp.StatusCode, string(raw))
	}
	var data map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &data)
	}
	return &bridge.CallResponse{
		Result: data,
		Metrics: bridge.CallMetrics{
			DurationMs: int(time.Since(start).Milliseconds()) + 1,
			BytesIn:    len(raw),
			BytesOut:   len(jsonBody),
		},
	}, nil
}
