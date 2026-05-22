// Package emailresend is the Bridge adapter for Resend.com — single
// operation: `send` an email. Side-effectful; the Grant MUST carry
// requires-human-approval for unbounded use or be scoped via only-domain
// (recipient domain). Spec: docs/specs/bridge-adapters-spec-001-v1.1.md.
package emailresend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "email-resend"
	adapterVersion = "1.0.0"
	defaultBaseURL = "https://api.resend.com"
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
		a.client = &http.Client{Timeout: 20 * time.Second}
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
			Name:        "send",
			Description: "Send an email via Resend. Required Grant caveats: requires-human-approval OR only-domain (recipient domain).",
			Params: []bridge.ParamSpec{
				{Name: "from", Type: "string", Required: true},
				{Name: "to", Type: "array", Required: true, Description: "list of recipient emails"},
				{Name: "subject", Type: "string", Required: true},
				{Name: "html", Type: "string"},
				{Name: "text", Type: "string"},
				{Name: "reply_to", Type: "string"},
			},
			SideEffects: true,
		},
	}
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	if req.Operation != "send" {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
	apiKey, err := bridge.ResolveCredential(req.Credentials, "api_key")
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	for _, k := range []string{"from", "to", "subject", "html", "text", "reply_to"} {
		if v, ok := req.Params[k]; ok {
			body[k] = v
		}
	}
	if body["from"] == nil || body["to"] == nil || body["subject"] == nil {
		return nil, fmt.Errorf("%w: from, to, subject are required", bridge.ErrMissingParam)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", bridge.ErrInvalidParam, err)
	}
	start := time.Now()
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/emails", bytes.NewReader(jsonBody))
	r.Header.Set("Authorization", "Bearer "+apiKey)
	r.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		// Resend supports Idempotency-Key natively.
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
		return nil, fmt.Errorf("%w: resend returned %d: %s", bridge.ErrUpstreamUnavailable, resp.StatusCode, string(raw))
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
