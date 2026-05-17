package cloudflarer2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// signV4 attaches an AWS SigV4 Authorization header to req. The body must
// already be set on req and req.ContentLength populated; the SHA-256 of the
// body is provided separately (so streaming-body callers can supply
// "UNSIGNED-PAYLOAD" if they choose to).
//
// region / service are "auto" / "s3" for Cloudflare R2.
func signV4(req *http.Request, accessKey, secretKey, region, service, payloadSHA256, nowAMZDate string) {
	// 1. Canonical request.
	if req.Header.Get("host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}
	req.Header.Set("x-amz-content-sha256", payloadSHA256)
	req.Header.Set("x-amz-date", nowAMZDate)

	signedHeaders, canonicalHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadSHA256,
	}, "\n")

	// 2. String to sign.
	scope := strings.Join([]string{nowAMZDate[:8], region, service, "aws4_request"}, "/")
	hashedCanonicalRequest := sha256Hex([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		nowAMZDate,
		scope,
		hashedCanonicalRequest,
	}, "\n")

	// 3. Derive signing key.
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(nowAMZDate[:8]))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	// 4. Sign.
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	// 5. Authorization header.
	authz := "AWS4-HMAC-SHA256 " +
		"Credential=" + accessKey + "/" + scope + ", " +
		"SignedHeaders=" + signedHeaders + ", " +
		"Signature=" + signature
	req.Header.Set("Authorization", authz)
}

func canonicalURI(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}
	// AWS canonicalizes by percent-encoding each path segment exactly once,
	// preserving slashes. net/url already gives us EscapedPath() which is
	// close enough for the operations we use (put/get/delete/list object).
	return u.EscapedPath()
}

func canonicalQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	v := u.Query()
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		vs := v[k]
		sort.Strings(vs)
		for _, val := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(val))
		}
	}
	return strings.Join(parts, "&")
}

func canonicalHeaders(req *http.Request) (signed, canonical string) {
	// Include host + every x-amz-* header.
	pairs := map[string]string{
		"host": req.URL.Host,
	}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" || strings.HasPrefix(lk, "x-amz-") || lk == "content-type" {
			pairs[lk] = strings.TrimSpace(vs[0])
		}
	}
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonicalParts := []string{}
	for _, k := range keys {
		canonicalParts = append(canonicalParts, k+":"+pairs[k]+"\n")
	}
	return strings.Join(keys, ";"), strings.Join(canonicalParts, "")
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
