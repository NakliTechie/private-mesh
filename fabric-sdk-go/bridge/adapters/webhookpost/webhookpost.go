// Package webhookpost is the generic-HTTP-POST Bridge adapter. The Grant
// MUST carry only-domain or requires-human-approval — the spec calls this
// out as the "everything fits if you're willing to assemble" adapter. The
// Hub's caveat machinery enforces those; the adapter itself just translates
// {url, headers, body} → an outbound POST.
package webhookpost

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
	adapterName    = "webhook-post"
	adapterVersion = "1.0.0"
)

type Adapter struct {
	client *http.Client
}

func New() *Adapter { return &Adapter{} }

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
			Name:        "post",
			Description: "POST a JSON body to the configured URL. Required Grant caveats: only-domain (or requires-human-approval).",
			Params: []bridge.ParamSpec{
				{Name: "url", Type: "string", Required: true},
				{Name: "headers", Type: "object", Description: "Extra request headers (e.g., Authorization)"},
				{Name: "body", Type: "object", Description: "JSON-serializable payload"},
			},
			SideEffects: true,
		},
	}
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	if req.Operation != "post" {
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
	target, err := bridge.ResolveParam[string](req.Params, "url", true, "")
	if err != nil {
		return nil, err
	}
	bodyJSON := []byte("{}")
	if v, ok := req.Params["body"]; ok && v != nil {
		bodyJSON, err = json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%w: body marshal: %v", bridge.ErrInvalidParam, err)
		}
	}
	start := time.Now()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		r.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}
	if headers, ok := req.Params["headers"].(map[string]any); ok {
		for k, v := range headers {
			if s, ok := v.(string); ok {
				r.Header.Set(k, s)
			}
		}
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
	result := map[string]any{
		"status": resp.StatusCode,
		"headers": flattenHeaders(resp.Header),
	}
	// Attempt to parse JSON; surface text otherwise.
	if json.Valid(raw) {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		result["body"] = parsed
	} else if len(raw) > 0 {
		result["body_text"] = string(raw)
	}
	if resp.StatusCode >= 400 {
		return &bridge.CallResponse{
			Result: result,
			Metrics: bridge.CallMetrics{
				DurationMs: int(time.Since(start).Milliseconds()) + 1,
				BytesIn:    len(raw),
				BytesOut:   len(bodyJSON),
			},
		}, fmt.Errorf("%w: %s returned %d", bridge.ErrUpstreamUnavailable, target, resp.StatusCode)
	}
	return &bridge.CallResponse{
		Result: result,
		Metrics: bridge.CallMetrics{
			DurationMs: int(time.Since(start).Milliseconds()) + 1,
			BytesIn:    len(raw),
			BytesOut:   len(bodyJSON),
		},
	}, nil
}

func flattenHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
