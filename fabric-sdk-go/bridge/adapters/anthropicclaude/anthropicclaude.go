// Package anthropicclaude is the Bridge adapter for Anthropic's Messages
// API. Single operation: `messages`. Most tools should use the LLM
// primitive instead — this exists for tools that want raw API access
// (e.g., fine-grained prompt-caching control).
package anthropicclaude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "anthropic-claude"
	adapterVersion = "1.0.0"
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
)

type Adapter struct {
	client  *http.Client
	baseURL string
}

func New() *Adapter                              { return &Adapter{baseURL: defaultBaseURL} }
func (a *Adapter) WithBaseURL(u string) *Adapter { a.baseURL = u; return a }

func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
	if opts.HTTPClient != nil {
		a.client = opts.HTTPClient
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 120 * time.Second}
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
			Name:        "messages",
			Description: "POST /v1/messages — Anthropic Messages API.",
			Params: []bridge.ParamSpec{
				{Name: "model", Type: "string", Required: true},
				{Name: "messages", Type: "array", Required: true},
				{Name: "max_tokens", Type: "integer", Required: true},
				{Name: "system", Type: "string"},
				{Name: "temperature", Type: "number"},
				{Name: "stream", Type: "boolean", Default: false},
			},
			SideEffects: false, // billing only; idempotency-key supported but optional
		},
	}
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	if req.Operation != "messages" {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
	apiKey, err := bridge.ResolveCredential(req.Credentials, "api_key")
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	for _, k := range []string{"model", "messages", "max_tokens", "system", "temperature", "stream", "tools", "tool_choice"} {
		if v, ok := req.Params[k]; ok {
			body[k] = v
		}
	}
	if body["model"] == nil || body["messages"] == nil || body["max_tokens"] == nil {
		return nil, fmt.Errorf("%w: model, messages, max_tokens are required", bridge.ErrMissingParam)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", bridge.ErrInvalidParam, err)
	}
	start := time.Now()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(jsonBody))
	r.Header.Set("x-api-key", apiKey)
	r.Header.Set("anthropic-version", apiVersion)
	r.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		r.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}
	resp, err := a.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: anthropic returned %d: %s", bridge.ErrUpstreamUnavailable, resp.StatusCode, string(raw))
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
