// AWS Signature Version 4 — server-side port of crate/lib/sigv4.js (the
// browser-side signer). Used by the Hub to sign R2 / Hetzner / B2 / AWS-S3
// requests on the daemon's behalf — the daemon holds a capability for the
// bucket; the Hub holds the sig-v4 creds and signs.
//
// Reference: https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
//
// Implementation notes:
//   - HEAD / GET use empty-body SHA-256 as payload hash (matches the browser
//     port; Hetzner-compatible — Hetzner rejects UNSIGNED-PAYLOAD for HEAD).
//   - PUT streams MAY use UNSIGNED-PAYLOAD to avoid buffering large bodies.
//     SignRequestStreaming takes the payload-hash explicitly.
//   - All headers in `signedHeaders` are bound — so `host`, `x-amz-date`,
//     and `x-amz-content-sha256` are always included in the canonical request.
//
// Cross-checked against the browser port (crate/lib/sigv4.js); if Hetzner
// works in the browser, the Go port must produce byte-identical signatures
// for the same inputs. The unit-test fixtures pin this.
package crate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	algorithm = "AWS4-HMAC-SHA256"

	// emptyBodySHA256 is the hex SHA-256 of the empty string. Used as the
	// payload hash for HEAD / GET requests with no body.
	emptyBodySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// UnsignedPayload is the literal AWS sig-v4 sentinel for "don't include
	// the payload in the signature." Suitable for streamed PUTs against R2;
	// not suitable for Hetzner HEADs (Hetzner is stricter — empty-body hash
	// instead).
	UnsignedPayload = "UNSIGNED-PAYLOAD"
)

// SignOpts is the input to SignRequest. The Method / URL / Region / AccessKey /
// SecretKey fields are required; Date defaults to time.Now() when zero.
type SignOpts struct {
	// Method is the HTTP verb (HEAD / GET / PUT / DELETE).
	Method string

	// URL is the full request URL including query string. Path is canonicalised
	// per RFC 3986 unreserved-character rules; query is sorted alphabetically.
	URL string

	// Headers are caller-supplied headers (e.g. Content-Type for PUT). Names
	// are lower-cased before signing. SignRequest adds Host, X-Amz-Date, and
	// X-Amz-Content-Sha256 — caller-supplied versions of those are overwritten.
	Headers http.Header

	// PayloadHash is the hex SHA-256 of the request body. Pass empty string
	// for HEAD / GET (the function fills in emptyBodySHA256). For streamed
	// PUTs against R2, pass UnsignedPayload.
	PayloadHash string

	// Region is the sig-v4 region. For R2 always "auto"; for Hetzner the
	// datacenter (e.g. "nbg1"); for AWS the actual region.
	Region string

	// Service is the sig-v4 service. For S3-API providers always "s3".
	Service string

	// AccessKey + SecretKey are the bucket creds (already decrypted from the
	// crate_buckets row when this is called).
	AccessKey string
	SecretKey string

	// Date pins the signing timestamp — exposed for deterministic unit tests.
	// Zero value = time.Now().UTC().
	Date time.Time
}

// SignRequest applies sig-v4 to opts and returns the headers map ready to be
// merged onto an outbound http.Request. The returned headers include
// Host, X-Amz-Date, X-Amz-Content-Sha256, Authorization plus any caller-supplied
// headers (which are also part of the signed headers).
func SignRequest(opts SignOpts) (http.Header, error) {
	if opts.URL == "" {
		return nil, errors.New("crate/sigv4: URL is required")
	}
	if opts.Region == "" {
		return nil, errors.New("crate/sigv4: Region is required")
	}
	if opts.AccessKey == "" || opts.SecretKey == "" {
		return nil, errors.New("crate/sigv4: AccessKey + SecretKey required")
	}
	method := strings.ToUpper(opts.Method)
	if method == "" {
		method = http.MethodGet
	}
	service := opts.Service
	if service == "" {
		service = "s3"
	}
	date := opts.Date
	if date.IsZero() {
		date = time.Now().UTC()
	} else {
		date = date.UTC()
	}

	u, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("crate/sigv4: parse URL: %w", err)
	}

	payloadHash := opts.PayloadHash
	if payloadHash == "" {
		payloadHash = emptyBodySHA256
	}

	amzDate := date.Format("20060102T150405Z")
	dateStamp := amzDate[:8]

	// Merge caller headers + the three we always add. Lower-case names.
	merged := http.Header{}
	if opts.Headers != nil {
		for k, vv := range opts.Headers {
			lk := strings.ToLower(k)
			for _, v := range vv {
				merged.Add(lk, v)
			}
		}
	}
	// Always-added: overwrite, don't append (so callers can't sneak a
	// conflicting host into the signed request).
	merged.Set("host", u.Host)
	merged.Set("x-amz-date", amzDate)
	merged.Set("x-amz-content-sha256", payloadHash)

	canonHeaders, signedHeaders := canonicalHeaders(merged)

	canonRequest := strings.Join([]string{
		method,
		canonicalPath(u.Path),
		canonicalQuery(u.Query()),
		canonHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + opts.Region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonRequest)),
	}, "\n")

	kDate := hmacBytes([]byte("AWS4"+opts.SecretKey), []byte(dateStamp))
	kRegion := hmacBytes(kDate, []byte(opts.Region))
	kService := hmacBytes(kRegion, []byte(service))
	kSigning := hmacBytes(kService, []byte("aws4_request"))

	signature := hex.EncodeToString(hmacBytes(kSigning, []byte(stringToSign)))

	auth := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, opts.AccessKey, scope, signedHeaders, signature)

	// Return headers in canonical (lower-case) form. Caller copies them onto
	// the outbound http.Request via req.Header.Set(canonicalKey, value)
	// or by iterating.
	out := http.Header{}
	for k, vv := range merged {
		for _, v := range vv {
			out.Add(k, v)
		}
	}
	out.Set("Authorization", auth)
	return out, nil
}

// --- canonicalisation helpers -----------------------------------------------

// canonicalPath URI-encodes each path segment per AWS sig-v4 rules.
// RFC 3986 unreserved characters (A–Z a–z 0–9 - _ . ~) pass through; slashes
// between segments are preserved.
func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = uriEncodeSegment(s)
	}
	return strings.Join(segs, "/")
}

// canonicalQuery sorts query params alphabetically by name (then value) and
// URI-encodes both sides.
func canonicalQuery(q url.Values) string {
	type kv struct{ k, v string }
	pairs := make([]kv, 0, len(q))
	for k, vv := range q {
		ek := uriEncodeSegment(k)
		if len(vv) == 0 {
			pairs = append(pairs, kv{ek, ""})
			continue
		}
		for _, v := range vv {
			pairs = append(pairs, kv{ek, uriEncodeSegment(v)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders returns the canonical-headers string ("name:value\n"...,
// sorted by lower-case name) and the signed-headers list ("name;name;...").
//
// Per sig-v4: header values are trimmed and runs of whitespace collapsed.
func canonicalHeaders(h http.Header) (canonical, signed string) {
	type kv struct{ k, v string }
	pairs := make([]kv, 0, len(h))
	for k, vv := range h {
		lk := strings.ToLower(k)
		// Per sig-v4, multi-valued headers are joined with comma. Trim + collapse.
		joined := strings.Join(vv, ",")
		pairs = append(pairs, kv{lk, collapseWS(joined)})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k < pairs[j].k })
	var cb, sb strings.Builder
	for i, p := range pairs {
		cb.WriteString(p.k)
		cb.WriteByte(':')
		cb.WriteString(p.v)
		cb.WriteByte('\n')
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(p.k)
	}
	return cb.String(), sb.String()
}

// uriEncodeSegment percent-encodes a single path-or-query segment per AWS
// sig-v4 rules: A–Z a–z 0–9 - _ . ~ pass through, everything else encoded.
func uriEncodeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// collapseWS trims and collapses runs of whitespace per sig-v4's
// canonical-header-value rules.
func collapseWS(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteByte(c)
		inSpace = false
	}
	return b.String()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacBytes(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// --- endpoint URL builders -------------------------------------------------

// EndpointR2 returns the path-style R2 endpoint URL for a bucket.
// Format: https://{accountId}.r2.cloudflarestorage.com/{bucket}/
func EndpointR2(accountID, bucket string) string {
	return "https://" + accountID + ".r2.cloudflarestorage.com/" + url.PathEscape(bucket) + "/"
}

// EndpointHetzner returns the virtual-host Hetzner Object Storage endpoint URL.
// Format: https://{bucket}.{datacenter}.your-objectstorage.com/
// Datacenters: nbg1, fsn1, hel1.
func EndpointHetzner(datacenter, bucket string) string {
	return "https://" + url.PathEscape(bucket) + "." + datacenter + ".your-objectstorage.com/"
}

// EndpointB2 returns the virtual-host B2 S3-compatible endpoint URL.
// Format: https://{bucket}.s3.{region}.backblazeb2.com/
func EndpointB2(region, bucket string) string {
	return "https://" + url.PathEscape(bucket) + ".s3." + region + ".backblazeb2.com/"
}

// EndpointAWS returns the virtual-host AWS S3 endpoint URL.
// Format: https://{bucket}.s3.{region}.amazonaws.com/
func EndpointAWS(region, bucket string) string {
	return "https://" + url.PathEscape(bucket) + ".s3." + region + ".amazonaws.com/"
}

// EndpointForProvider returns the canonical bucket-base endpoint URL for a
// given provider. Returns ("", false) for unknown providers.
//
// accountID is honoured only for "r2"; for the others, it is ignored.
func EndpointForProvider(provider, accountID, region, bucket string) (string, bool) {
	switch strings.ToLower(provider) {
	case "r2":
		return EndpointR2(accountID, bucket), true
	case "hetzner":
		return EndpointHetzner(region, bucket), true
	case "b2":
		return EndpointB2(region, bucket), true
	case "aws-s3", "aws":
		return EndpointAWS(region, bucket), true
	default:
		return "", false
	}
}
