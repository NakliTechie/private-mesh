// Unit C — Hub-side conformance for the CRATE-PAIR endpoints. Covers the
// matrix in plan/Unit-C-notes.md: intent + cancel + redeem + capability
// refresh + revoke, plus the negative cases (bad version, missing secret,
// replay, unknown, cancelled, expired, refresh-after-revoke).

package server_test

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

func freshSecret(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := cryptorand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func freshBucketID(t *testing.T) string {
	t.Helper()
	id, err := ulid.New(ulid.Now(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func freshPubkey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(pub)
}

// freshIntentPayload returns a well-formed payload with `now` as `issued_at`
// and now+15min as `expires_at`. Callers mutate fields to exercise edge cases.
func freshIntentPayload(t *testing.T) map[string]any {
	t.Helper()
	now := time.Now().Unix()
	return map[string]any{
		"v":                  1,
		"type":               "crate.pairing.token",
		"secret":             freshSecret(t),
		"transport_endpoint": "https://transport.example.com",
		"transport_type":     "hub",
		"bucket_id":          freshBucketID(t),
		"identity_pubkey":    freshPubkey(t),
		"issued_at":          now,
		"expires_at":         now + 900,
	}
}

// mintIdentityGrant produces a Grant for the browser's intent calls. Scope
// "identity" / "pair" matches the checkAuth requirement in handleIntent.
func (h *hubFixture) mintIdentityGrant(t *testing.T) string {
	t.Helper()
	return h.mintGrantWithScope(t, grant.Primitive("identity"), "*", []string{"pair"}, nil)
}

// --- Intent endpoint ---------------------------------------------------

func TestCratePairing_IntentValid(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", status, body)
	}
}

func TestCratePairing_IntentRejectsWrongVersion(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	payload["v"] = 99
	status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusUpgradeRequired {
		t.Fatalf("status: got %d, want 426; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "protocol_version" {
		t.Errorf("error.code: got %q, want protocol_version", ee.Error.Code)
	}
}

func TestCratePairing_IntentRejectsMissingSecret(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	delete(payload, "secret")
	status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "bad_request" {
		t.Errorf("error.code: got %q, want bad_request", ee.Error.Code)
	}
}

func TestCratePairing_IntentRejectsWrongType(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	payload["type"] = "not.crate.pairing.token"
	status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", status, body)
	}
}

func TestCratePairing_IntentRejectsExpiredPayload(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	past := time.Now().Add(-time.Hour).Unix()
	payload["issued_at"] = past - 900
	payload["expires_at"] = past
	status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	})
	if status != http.StatusGone {
		t.Fatalf("status: got %d, want 410; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "token_expired" {
		t.Errorf("error.code: got %q, want token_expired", ee.Error.Code)
	}
}

func TestCratePairing_IntentRequiresAuth(t *testing.T) {
	h := newHubFixture(t)
	status, body := h.do(t, "POST", "/v1/pairing/intent", freshIntentPayload(t), nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401; body=%s", status, body)
	}
}

// --- Redeem endpoint ---------------------------------------------------

func TestCratePairing_RedeemFreshToken(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	secret := payload["secret"].(string)
	if status, body := h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{
		"X-Fabric-Grant": g,
	}); status != http.StatusCreated {
		t.Fatalf("intent status: got %d; body=%s", status, body)
	}

	daemonPubkey := freshPubkey(t)
	redeemBody := map[string]any{
		"v":             1,
		"secret":        secret,
		"daemon_pubkey": daemonPubkey,
		"daemon_fingerprint": map[string]any{
			"platform": "darwin", "arch": "arm64", "hostname": "test", "agent_version": "0.0.0-m1",
		},
	}
	status, body := h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil)
	if status != http.StatusOK {
		t.Fatalf("redeem status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var out struct {
		V               int    `json:"v"`
		Capability      string `json:"capability"`
		BucketReference string `json:"bucket_reference"`
		TransportPubkey string `json:"transport_pubkey"`
		ExpiresAt       int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal redeem data: %v; body=%s", err, body)
	}
	if out.V != 1 || out.Capability == "" || out.BucketReference == "" || out.TransportPubkey == "" {
		t.Errorf("redeem response missing fields: %+v", out)
	}
	if out.ExpiresAt <= time.Now().Unix() {
		t.Errorf("expires_at should be in the future: got %d", out.ExpiresAt)
	}

	// Verify the capability is a valid macaroon signed by the Hub.
	macBytes, err := base64.StdEncoding.DecodeString(out.Capability)
	if err != nil {
		t.Fatalf("capability is not base64: %v", err)
	}
	if err := grant.VerifySignature(macBytes, h.id.MacaroonRootKey, grant.AlwaysSatisfied); err != nil {
		t.Errorf("capability signature invalid: %v", err)
	}
	parsed, err := grant.Parse(macBytes)
	if err != nil {
		t.Fatalf("capability parse: %v", err)
	}
	if string(parsed.Identifier.Scope.Primitive) != "sync" {
		t.Errorf("scope.primitive: got %q, want sync", parsed.Identifier.Scope.Primitive)
	}
	if parsed.Identifier.Scope.Namespace == "" {
		t.Errorf("scope.namespace empty")
	}
	// Verify caveats include device-id == daemonPubkey
	foundDevice := false
	for _, c := range parsed.Caveats {
		if strings.HasPrefix(strings.TrimSpace(c), "device-id == "+daemonPubkey) {
			foundDevice = true
			break
		}
	}
	if !foundDevice {
		t.Errorf("device-id caveat missing for %s; got %v", daemonPubkey, parsed.Caveats)
	}
}

func TestCratePairing_RedeemReplayConflicts(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	secret := payload["secret"].(string)
	_, _ = h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{"X-Fabric-Grant": g})

	redeemBody := map[string]any{
		"v": 1, "secret": secret, "daemon_pubkey": freshPubkey(t),
		"daemon_fingerprint": map[string]any{},
	}
	if status, _ := h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil); status != http.StatusOK {
		t.Fatalf("first redeem: got %d, want 200", status)
	}
	status, body := h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil)
	if status != http.StatusConflict {
		t.Fatalf("replay status: got %d, want 409; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "token_already_redeemed" {
		t.Errorf("error.code: got %q, want token_already_redeemed", ee.Error.Code)
	}
}

func TestCratePairing_RedeemUnknownSecret(t *testing.T) {
	h := newHubFixture(t)
	redeemBody := map[string]any{
		"v": 1, "secret": freshSecret(t), "daemon_pubkey": freshPubkey(t),
		"daemon_fingerprint": map[string]any{},
	}
	status, body := h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil)
	if status != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "token_not_found" {
		t.Errorf("error.code: got %q, want token_not_found", ee.Error.Code)
	}
}

func TestCratePairing_RedeemExpiredToken(t *testing.T) {
	h := newHubFixture(t)

	// Seed the row directly so we can set a past expires_at without the intent
	// endpoint rejecting it as 410. Tests the redeem-side expiry path.
	secret := freshSecret(t)
	past := time.Now().Add(-time.Hour).UTC()
	pastIssued := past.Add(-15 * time.Minute)
	if err := h.store.CreateCratePairingToken(t.Context(), storage.CratePairingToken{
		Secret:            secret,
		PayloadJSON:       `{"seeded":"expired-test"}`,
		BucketID:          freshBucketID(t),
		IdentityPubkey:    freshPubkey(t),
		TransportEndpoint: "https://example.com",
		TransportType:     "hub",
		IssuedAt:          pastIssued,
		ExpiresAt:         past,
	}); err != nil {
		t.Fatalf("seed expired token: %v", err)
	}
	redeemBody := map[string]any{
		"v": 1, "secret": secret, "daemon_pubkey": freshPubkey(t),
		"daemon_fingerprint": map[string]any{},
	}
	status, body := h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil)
	if status != http.StatusGone {
		t.Fatalf("status: got %d, want 410; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "token_expired" {
		t.Errorf("error.code: got %q, want token_expired", ee.Error.Code)
	}
}

// --- Cancel endpoint ---------------------------------------------------

func TestCratePairing_CancelThenRedeem(t *testing.T) {
	h := newHubFixture(t)
	g := h.mintIdentityGrant(t)

	payload := freshIntentPayload(t)
	secret := payload["secret"].(string)
	_, _ = h.do(t, "POST", "/v1/pairing/intent", payload, map[string]string{"X-Fabric-Grant": g})

	status, body := h.do(t, "POST", "/v1/pairing/intent/cancel",
		map[string]any{"secret": secret}, map[string]string{"X-Fabric-Grant": g})
	if status != http.StatusNoContent {
		t.Fatalf("cancel status: got %d, want 204; body=%s", status, body)
	}

	redeemBody := map[string]any{
		"v": 1, "secret": secret, "daemon_pubkey": freshPubkey(t),
		"daemon_fingerprint": map[string]any{},
	}
	status, body = h.do(t, "POST", "/v1/pairing/redeem", redeemBody, nil)
	if status != http.StatusNotFound {
		t.Fatalf("redeem-after-cancel status: got %d, want 404; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "token_cancelled" {
		t.Errorf("error.code: got %q, want token_cancelled", ee.Error.Code)
	}
}

// --- Capability refresh + revoke ---------------------------------------

// mintCapabilityForDaemon produces a sync-scope macaroon as if it had come
// from /v1/pairing/redeem, so we can exercise refresh + revoke without going
// through the full pairing flow. The real flow records the capability via
// RememberGrant; mirror that here so ownership-aware paths (revoke) work.
func (h *hubFixture) mintCapabilityForDaemon(t *testing.T, daemonPubkey string) (string, string) {
	t.Helper()
	now := time.Now().UTC()
	gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
	expires := now.Add(365 * 24 * time.Hour)
	caveats := []string{
		"time < " + expires.Format(time.RFC3339Nano),
		"device-id == " + daemonPubkey,
		"operation in [read, write]",
	}
	out, err := grant.Mint(grant.MintSpec{
		RootKey:  h.id.MacaroonRootKey,
		Location: h.ts.URL,
		Identifier: grant.Identifier{
			GrantID:           gid.String(),
			IssuedAt:          now,
			IssuedByPrincipal: h.id.HubID,
			IssuedByKeypair:   h.id.PublicKey,
			Scope: grant.Scope{
				Primitive:  grant.Primitive("sync"),
				Namespace:  "test-bucket",
				Operations: []string{"read", "write"},
			},
		},
		Caveats: caveats,
	})
	if err != nil {
		t.Fatalf("mint capability: %v", err)
	}
	// Mirror the production flow: every minted capability gets a
	// grants_known row so revoke paths can verify ownership.
	scopeJSON, _ := json.Marshal(map[string]any{"primitive": "sync", "namespace": "test-bucket", "operations": []string{"read", "write"}})
	caveatsJSON, _ := json.Marshal(caveats)
	if err := h.store.RememberGrant(context.Background(), storage.KnownGrant{
		GrantID:            gid.String(),
		IssuedByPrincipal:  h.id.HubID,
		RecipientPrincipal: daemonPubkey,
		ScopeJSON:          string(scopeJSON),
		CaveatsJSON:        string(caveatsJSON),
		IssuedAt:           now,
		ExpiresAt:          expires,
	}); err != nil {
		t.Fatalf("RememberGrant: %v", err)
	}
	return gid.String(), base64.StdEncoding.EncodeToString(out.Macaroon)
}

func TestCratePairing_CapabilityRefresh(t *testing.T) {
	h := newHubFixture(t)
	daemonPubkey := freshPubkey(t)
	_, cap := h.mintCapabilityForDaemon(t, daemonPubkey)

	status, body := h.do(t, "POST", "/v1/capability/refresh", nil, map[string]string{
		"X-Fabric-Grant": cap,
	})
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", status, body)
	}
	var env successEnv
	_ = json.Unmarshal(body, &env)
	var out struct {
		V          int    `json:"v"`
		Capability string `json:"capability"`
		ExpiresAt  int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("unmarshal refresh data: %v", err)
	}
	if out.Capability == cap {
		t.Errorf("refresh should mint a NEW capability, not echo the old one")
	}
	if out.ExpiresAt <= time.Now().Unix() {
		t.Errorf("refreshed expires_at must be in future: got %d", out.ExpiresAt)
	}
	// Confirm the device-id caveat is preserved.
	macBytes, _ := base64.StdEncoding.DecodeString(out.Capability)
	parsed, err := grant.Parse(macBytes)
	if err != nil {
		t.Fatalf("parse refreshed cap: %v", err)
	}
	foundDevice := false
	for _, c := range parsed.Caveats {
		if strings.HasPrefix(strings.TrimSpace(c), "device-id == "+daemonPubkey) {
			foundDevice = true
		}
	}
	if !foundDevice {
		t.Errorf("refreshed cap dropped device-id caveat; got %v", parsed.Caveats)
	}
}

func TestCratePairing_RevokeThenRefresh(t *testing.T) {
	h := newHubFixture(t)
	daemonPubkey := freshPubkey(t)
	gid, cap := h.mintCapabilityForDaemon(t, daemonPubkey)

	// The capability was issued by h.id.HubID; the ownership check now
	// requires the revoker to share that principal id. Mint the revGrant
	// on behalf of the Hub itself (the legitimate issuer).
	revGrant := h.mintGrantWithScopeAs(t, h.id.HubID, grant.Primitive("grant"), "*", []string{"revoke"}, nil)

	status, body := h.do(t, "DELETE", "/v1/capability/"+gid, nil, map[string]string{
		"X-Fabric-Grant": revGrant,
	})
	if status != http.StatusNoContent {
		t.Fatalf("revoke status: got %d, want 204; body=%s", status, body)
	}

	// Now the daemon's capability should be rejected by authMiddleware.
	status, body = h.do(t, "POST", "/v1/capability/refresh", nil, map[string]string{
		"X-Fabric-Grant": cap,
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("refresh-after-revoke status: got %d, want 401; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "grant_revoked" {
		t.Errorf("error.code: got %q, want grant_revoked", ee.Error.Code)
	}
}

// TestCapabilityRevoke_ThirdPartyDenied is the security regression for the
// audit finding: a principal holding any `grant:revoke` grant could
// previously revoke any capability in the Hub, regardless of whether they
// issued it or were the recipient. Now the request is rejected with 403
// scope_denied (or 404 not_found if the grant was never tracked).
func TestCapabilityRevoke_ThirdPartyDenied(t *testing.T) {
	h := newHubFixture(t)
	daemonPubkey := freshPubkey(t)
	gid, cap := h.mintCapabilityForDaemon(t, daemonPubkey)

	// revGrant is issued by an UNRELATED principal (default mint helper
	// generates a fresh random ulid). Holder has the right scope but no
	// stake in the target capability.
	revGrant := h.mintGrantWithScope(t, grant.Primitive("grant"), "*", []string{"revoke"}, nil)

	status, body := h.do(t, "DELETE", "/v1/capability/"+gid, nil, map[string]string{
		"X-Fabric-Grant": revGrant,
	})
	if status != http.StatusForbidden {
		t.Fatalf("third-party revoke: got %d, want 403; body=%s", status, body)
	}
	var ee errorEnv
	_ = json.Unmarshal(body, &ee)
	if ee.Error.Code != "scope_denied" {
		t.Errorf("error.code: got %q, want scope_denied", ee.Error.Code)
	}

	// And the daemon's capability must STILL work — the rejected revoke
	// should not leave any partial state behind.
	status, _ = h.do(t, "POST", "/v1/capability/refresh", nil, map[string]string{
		"X-Fabric-Grant": cap,
	})
	if status == http.StatusUnauthorized {
		t.Errorf("daemon capability was incorrectly revoked despite 403 on third-party revoke")
	}
}
