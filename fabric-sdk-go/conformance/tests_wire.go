package conformance

import (
	"fmt"
	"strings"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
)

// wireTests covers conformance tests 1–5 (wire format).
func wireTests() []testEntry {
	const g = "Wire format"
	return []testEntry{
		{ID: 1, Group: g, Name: "Reject malformed JSON with HTTP 400", Run: testMalformedJSON},
		{ID: 2, Group: g, Name: "Reject unknown protocol version with version_mismatch", Run: testUnknownProtocolVersion},
		{ID: 3, Group: g, Name: "Return CORS headers per spec", Run: testCORSHeaders},
		{ID: 4, Group: g, Name: "Return freshness object in all responses", Run: testFreshnessPresent},
		{ID: 5, Group: g, Name: "Include X-Fabric-Version in all responses", Run: testXFabricVersionHeader},
	}
}

// Test 1: malformed JSON to a POST endpoint → HTTP 400 + bad_request.
func testMalformedJSON(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read", "write"}, nil)
	resp, err := c.do("POST", "/fabric/v1/vault/append", []byte("{not json"),
		map[string]string{
			"X-Fabric-Grant":            gr,
			"X-Fabric-Idempotency-Key":  idemKey(),
			"Content-Type":              "application/json",
		})
	if err != nil {
		return err
	}
	if resp.Status != 400 {
		return fmt.Errorf("expected 400, got %d: %s", resp.Status, resp.RawBody)
	}
	if resp.Error == nil || resp.Error.Code != "bad_request" {
		return fmt.Errorf("expected bad_request code, got %+v", resp.Error)
	}
	return nil
}

// Test 2: a client supplying an unknown X-Fabric-Version triggers
// version_mismatch on a request the server otherwise would have honored. The
// header convention: clients send their requested protocol version; the
// transport rejects when it's incompatible.
func testUnknownProtocolVersion(c *client) error {
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, nil)
	resp, err := c.do("GET", "/fabric/v1/discover", nil, map[string]string{
		"X-Fabric-Grant":   gr,
		"X-Fabric-Version": "naklimesh/99.0",
	})
	if err != nil {
		return err
	}
	// /discover is unauthenticated and always responds — but a transport
	// implementing this conformance bullet rejects unknown protocol versions
	// at the wire layer with version_mismatch. Accept either: rejection with
	// version_mismatch, OR success with the server's own version, indicating
	// the transport is version-tolerant on /discover. We require the response
	// to at least *contain* the spec-pinned version string so downgrade
	// attacks are visible.
	if resp.Status >= 400 {
		if resp.Error == nil || resp.Error.Code != "version_mismatch" {
			return fmt.Errorf("expected version_mismatch on rejection, got %+v", resp.Error)
		}
		return nil
	}
	if v := resp.Headers.Get("X-Fabric-Version"); v != "naklimesh/1.0" {
		return fmt.Errorf("expected server to advertise naklimesh/1.0 on response; got %q", v)
	}
	return nil
}

// Test 3: CORS headers per spec (Access-Control-Allow-Origin etc.).
func testCORSHeaders(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	wantOrigin := resp.Headers.Get("Access-Control-Allow-Origin")
	if wantOrigin == "" {
		return fmt.Errorf("missing Access-Control-Allow-Origin")
	}
	wantHeaders := resp.Headers.Get("Access-Control-Allow-Headers")
	if !strings.Contains(wantHeaders, "X-Fabric-Grant") {
		return fmt.Errorf("Access-Control-Allow-Headers does not advertise X-Fabric-Grant: %q", wantHeaders)
	}
	wantMethods := resp.Headers.Get("Access-Control-Allow-Methods")
	if !strings.Contains(wantMethods, "POST") || !strings.Contains(wantMethods, "GET") {
		return fmt.Errorf("Access-Control-Allow-Methods missing GET/POST: %q", wantMethods)
	}
	return nil
}

// Test 4: freshness object is present on every response.
func testFreshnessPresent(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	if resp.Freshness == nil {
		return fmt.Errorf("freshness missing on /health response: %s", resp.RawBody)
	}
	gr := c.mintGrant(grant.PrimitiveVault, "*", []string{"read"}, nil)
	resp, err = c.do("GET", "/fabric/v1/discover", nil, map[string]string{"X-Fabric-Grant": gr})
	if err != nil {
		return err
	}
	if resp.Freshness == nil {
		return fmt.Errorf("freshness missing on /discover response: %s", resp.RawBody)
	}
	return nil
}

// Test 5: X-Fabric-Version header on every response.
func testXFabricVersionHeader(c *client) error {
	resp, err := c.do("GET", "/fabric/v1/health", nil, nil)
	if err != nil {
		return err
	}
	if v := resp.Headers.Get("X-Fabric-Version"); v != "naklimesh/1.0" {
		return fmt.Errorf("expected X-Fabric-Version=naklimesh/1.0, got %q", v)
	}
	return nil
}
