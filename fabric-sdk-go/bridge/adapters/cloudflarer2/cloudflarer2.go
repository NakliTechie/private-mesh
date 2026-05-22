// Package cloudflarer2 is the Bridge adapter for Cloudflare R2 (S3-compatible
// object storage). Operations: put-object, get-object, list-objects,
// delete-object. Uses an inline AWS SigV4 implementation (sigv4.go) — the
// aws-sdk-go-v2 would add ~3 MB to consumers' binaries for what is, in this
// adapter, a handful of HTTP calls per second at most.
//
// Credentials: { access_key_id, secret_access_key, account_id }.
package cloudflarer2

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/bridge"
)

const (
	adapterName    = "cloudflare-r2"
	adapterVersion = "1.0.0"
	region         = "auto"
	service        = "s3"
)

type Adapter struct {
	client      *http.Client
	endpointTpl string  // "%s.r2.cloudflarestorage.com" in prod; test override sets a full URL via WithEndpointURL.
	endpointURL string  // when set, overrides endpointTpl (used by httptest fixtures).
	now         func() time.Time
}

func New() *Adapter {
	return &Adapter{
		endpointTpl: "https://%s.r2.cloudflarestorage.com",
		now:         time.Now,
	}
}

// WithEndpointURL pins a full URL (scheme://host) overriding the per-account
// template. Used by tests against httptest.
func (a *Adapter) WithEndpointURL(u string) *Adapter { a.endpointURL = u; return a }

func (a *Adapter) Init(opts bridge.AdapterInitOptions) error {
	if opts.HTTPClient != nil {
		a.client = opts.HTTPClient
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	if a.now == nil {
		a.now = time.Now
	}
	return nil
}
func (a *Adapter) Close() error    { return nil }
func (a *Adapter) Name() string    { return adapterName }
func (a *Adapter) Version() string { return adapterVersion }

func (a *Adapter) Operations() []bridge.OperationSpec {
	return []bridge.OperationSpec{
		{
			Name:        "put-object",
			Description: "PutObject — uploads bytes (base64-encoded in params.body_b64) to bucket/key.",
			Params: []bridge.ParamSpec{
				{Name: "bucket", Type: "string", Required: true},
				{Name: "key", Type: "string", Required: true},
				{Name: "body_b64", Type: "string", Required: true},
				{Name: "content_type", Type: "string"},
			},
			SideEffects: true,
		},
		{
			Name:        "get-object",
			Description: "GetObject — returns the body as base64 (result.body_b64).",
			Params: []bridge.ParamSpec{
				{Name: "bucket", Type: "string", Required: true},
				{Name: "key", Type: "string", Required: true},
			},
			SideEffects: false,
		},
		{
			Name:        "list-objects",
			Description: "ListObjectsV2 — returns the parsed XML response.",
			Params: []bridge.ParamSpec{
				{Name: "bucket", Type: "string", Required: true},
				{Name: "prefix", Type: "string"},
				{Name: "max_keys", Type: "integer", Default: 1000},
			},
			SideEffects: false,
		},
		{
			Name:        "delete-object",
			Description: "DeleteObject — removes bucket/key.",
			Params: []bridge.ParamSpec{
				{Name: "bucket", Type: "string", Required: true},
				{Name: "key", Type: "string", Required: true},
			},
			SideEffects: true,
		},
	}
}

func (a *Adapter) Call(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	if a.client == nil {
		_ = a.Init(bridge.AdapterInitOptions{})
	}
	switch req.Operation {
	case "put-object":
		return a.putObject(ctx, req)
	case "get-object":
		return a.getObject(ctx, req)
	case "list-objects":
		return a.listObjects(ctx, req)
	case "delete-object":
		return a.deleteObject(ctx, req)
	default:
		return nil, fmt.Errorf("%w: %s", bridge.ErrUnknownOperation, req.Operation)
	}
}

func (a *Adapter) endpoint(creds map[string]string) (string, error) {
	if a.endpointURL != "" {
		return strings.TrimRight(a.endpointURL, "/"), nil
	}
	acct, err := bridge.ResolveCredential(creds, "account_id")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(a.endpointTpl, acct), nil
}

func (a *Adapter) putObject(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	bucket, _ := bridge.ResolveParam[string](req.Params, "bucket", true, "")
	key, _ := bridge.ResolveParam[string](req.Params, "key", true, "")
	b64, _ := bridge.ResolveParam[string](req.Params, "body_b64", true, "")
	if bucket == "" || key == "" || b64 == "" {
		return nil, fmt.Errorf("%w: bucket, key, body_b64 required", bridge.ErrMissingParam)
	}
	body, err := decodeB64(b64)
	if err != nil {
		return nil, fmt.Errorf("%w: body_b64 not valid base64: %v", bridge.ErrInvalidParam, err)
	}
	contentType, _ := bridge.ResolveParam[string](req.Params, "content_type", false, "application/octet-stream")
	r, err := a.newRequest(ctx, http.MethodPut, bucket, key, "", body, req.Credentials)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", contentType)
	return a.do(r, len(body))
}

func (a *Adapter) getObject(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	bucket, _ := bridge.ResolveParam[string](req.Params, "bucket", true, "")
	key, _ := bridge.ResolveParam[string](req.Params, "key", true, "")
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("%w: bucket, key required", bridge.ErrMissingParam)
	}
	r, err := a.newRequest(ctx, http.MethodGet, bucket, key, "", nil, req.Credentials)
	if err != nil {
		return nil, err
	}
	return a.do(r, 0)
}

func (a *Adapter) listObjects(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	bucket, _ := bridge.ResolveParam[string](req.Params, "bucket", true, "")
	if bucket == "" {
		return nil, fmt.Errorf("%w: bucket required", bridge.ErrMissingParam)
	}
	prefix, _ := bridge.ResolveParam[string](req.Params, "prefix", false, "")
	maxKeys, _ := bridge.ResolveParam[float64](req.Params, "max_keys", false, 1000)
	q := url.Values{}
	q.Set("list-type", "2")
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	q.Set("max-keys", fmt.Sprintf("%d", int(maxKeys)))
	r, err := a.newRequest(ctx, http.MethodGet, bucket, "", q.Encode(), nil, req.Credentials)
	if err != nil {
		return nil, err
	}
	return a.do(r, 0)
}

func (a *Adapter) deleteObject(ctx context.Context, req *bridge.CallRequest) (*bridge.CallResponse, error) {
	bucket, _ := bridge.ResolveParam[string](req.Params, "bucket", true, "")
	key, _ := bridge.ResolveParam[string](req.Params, "key", true, "")
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("%w: bucket, key required", bridge.ErrMissingParam)
	}
	r, err := a.newRequest(ctx, http.MethodDelete, bucket, key, "", nil, req.Credentials)
	if err != nil {
		return nil, err
	}
	return a.do(r, 0)
}

func (a *Adapter) newRequest(ctx context.Context, method, bucket, key, rawQuery string, body []byte, creds map[string]string) (*http.Request, error) {
	ak, err := bridge.ResolveCredential(creds, "access_key_id")
	if err != nil {
		return nil, err
	}
	sk, err := bridge.ResolveCredential(creds, "secret_access_key")
	if err != nil {
		return nil, err
	}
	ep, err := a.endpoint(creds)
	if err != nil {
		return nil, err
	}
	path := "/" + bucket
	if key != "" {
		path += "/" + key
	}
	u := ep + path
	if rawQuery != "" {
		u += "?" + rawQuery
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	r, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		r.ContentLength = int64(len(body))
	}
	payloadSum := sha256Hex(body)
	signV4(r, ak, sk, region, service, payloadSum, a.now().UTC().Format("20060102T150405Z"))
	return r, nil
}

func (a *Adapter) do(r *http.Request, bytesOut int) (*bridge.CallResponse, error) {
	start := time.Now()
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
		return nil, fmt.Errorf("%w: R2 returned %d: %s", bridge.ErrUpstreamUnavailable, resp.StatusCode, string(raw))
	}
	result := map[string]any{
		"status":  resp.StatusCode,
		"headers": flattenHeaders(resp.Header),
	}
	if len(raw) > 0 {
		if strings.HasPrefix(strings.TrimSpace(string(raw)), "<") {
			var parsed map[string]any
			if obj, err := parseXMLToMap(raw); err == nil {
				parsed = obj
			}
			result["xml"] = parsed
			result["xml_raw"] = string(raw)
		} else {
			result["body_b64"] = encodeB64(raw)
		}
	}
	return &bridge.CallResponse{
		Result: result,
		Metrics: bridge.CallMetrics{
			DurationMs: int(time.Since(start).Milliseconds()) + 1,
			BytesIn:    len(raw),
			BytesOut:   bytesOut,
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

func decodeB64(s string) ([]byte, error) {
	// importing encoding/base64 once via an alias avoids polluting the API doc:
	return base64Std.DecodeString(s)
}
func encodeB64(b []byte) string { return base64Std.EncodeToString(b) }

// parseXMLToMap turns an XML document into a generic map. The R2 list-objects
// response is a small XML doc; consumers usually re-marshal the relevant
// fields. We use a generic generic-tree decoder.
func parseXMLToMap(b []byte) (map[string]any, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	type node struct {
		Name     string
		Attrs    map[string]string
		Children []any
	}
	var rootName string
	stack := []*node{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{Name: t.Name.Local, Attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.Attrs[a.Name.Local] = a.Value
			}
			if rootName == "" {
				rootName = n.Name
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" && len(stack) > 0 {
				stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, s)
			}
		}
	}
	if len(stack) > 0 {
		// Unbalanced — surface the partial tree anyway.
	}
	// Re-walk: find the root we already captured by re-decoding from index 0.
	dec2 := xml.NewDecoder(bytes.NewReader(b))
	root := decodeNode(dec2)
	if root == nil {
		return map[string]any{}, nil
	}
	return map[string]any{root.Name: nodeToValue(root)}, nil
}

type xmlNode struct {
	Name     string
	Text     string
	Children []*xmlNode
}

func decodeNode(d *xml.Decoder) *xmlNode {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil
		}
		if se, ok := tok.(xml.StartElement); ok {
			return decodeElement(d, se)
		}
	}
}

func decodeElement(d *xml.Decoder, se xml.StartElement) *xmlNode {
	n := &xmlNode{Name: se.Name.Local}
	for {
		tok, err := d.Token()
		if err != nil {
			return n
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child := decodeElement(d, t)
			n.Children = append(n.Children, child)
		case xml.EndElement:
			return n
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				if n.Text == "" {
					n.Text = s
				} else {
					n.Text += s
				}
			}
		}
	}
}

func nodeToValue(n *xmlNode) any {
	if len(n.Children) == 0 {
		return n.Text
	}
	out := map[string]any{}
	for _, c := range n.Children {
		v := nodeToValue(c)
		if existing, ok := out[c.Name]; ok {
			if list, ok := existing.([]any); ok {
				out[c.Name] = append(list, v)
			} else {
				out[c.Name] = []any{existing, v}
			}
		} else {
			out[c.Name] = v
		}
	}
	return out
}
