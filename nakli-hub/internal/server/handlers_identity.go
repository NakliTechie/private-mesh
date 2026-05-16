package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

const pairingDefaultExpiry = 10 * time.Minute

// --- GET /fabric/v1/identity/principal ---

type identityPrincipalResp struct {
	PrincipalID   string `json:"principal_id"`
	PrincipalType string `json:"principal_type"`
	PublicKey     string `json:"public_key"` // base64 if known; empty if Hub has not seen this principal yet
}

func (s *Server) handleIdentityPrincipal(w http.ResponseWriter, r *http.Request) {
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "read"}); err != nil {
		return
	}
	g := grantFromCtx(r.Context())
	principalID := stripFabricSuffix(g.Identifier.IssuedByPrincipal)
	resp := identityPrincipalResp{PrincipalID: principalID, PrincipalType: pickPrincipalType(g.Caveats)}
	if p, err := s.store.GetPrincipal(r.Context(), principalID); err == nil {
		resp.PrincipalType = p.PrincipalType
		resp.PublicKey = base64.StdEncoding.EncodeToString(p.PublicKey)
	}
	writeSuccess(w, r, http.StatusOK, resp, FreshnessNow(s.now()))
}

// --- POST /fabric/v1/identity/pair/initiate ---

type pairInitiateReq struct {
	PairingMethod    string `json:"pairing_method"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type pairInitiateResp struct {
	PairingToken string    `json:"pairing_token"`
	RendezvousURL string   `json:"rendezvous_url"`
	ExpiresAt    time.Time `json:"expires_at"`
	QRPayload    string    `json:"qr_payload"`
	NumericCode  string    `json:"numeric_code"`
	MagicLink    string    `json:"magic_link"`
}

func (s *Server) handlePairInitiate(w http.ResponseWriter, r *http.Request) {
	var req pairInitiateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if err := s.checkAuth(w, r, scopeRequirement{Primitive: "identity", Operation: "pair"}); err != nil {
		return
	}
	g := grantFromCtx(r.Context())

	expiry := pairingDefaultExpiry
	if req.ExpiresInSeconds > 0 && req.ExpiresInSeconds < 24*3600 {
		expiry = time.Duration(req.ExpiresInSeconds) * time.Second
	}
	token := newULID()
	code, err := newNumericCode(6)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not generate numeric code", true)
		return
	}
	now := s.now()
	expiresAt := now.Add(expiry)
	if err := s.store.CreatePairingToken(r.Context(), storage.PairingToken{
		Token:                token,
		NumericCode:          code,
		InitiatedByPrincipal: stripFabricSuffix(g.Identifier.IssuedByPrincipal),
		InitiatedAt:          now,
		ExpiresAt:            expiresAt,
	}); err != nil {
		s.logger.Error("CreatePairingToken failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not create pairing token", true)
		return
	}
	rendezvous := r.Host + "/fabric/v1/identity/pair/complete"
	if r.TLS == nil {
		rendezvous = "http://" + rendezvous
	} else {
		rendezvous = "https://" + rendezvous
	}
	magic := rendezvous + "?token=" + token
	qrPayload, err := encodeQRPayload(token, rendezvous, expiresAt)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "qr payload encode failed", true)
		return
	}
	writeSuccess(w, r, http.StatusOK, pairInitiateResp{
		PairingToken:  token,
		RendezvousURL: rendezvous,
		ExpiresAt:     expiresAt,
		QRPayload:     qrPayload,
		NumericCode:   code,
		MagicLink:     magic,
	}, FreshnessNow(s.now()))
}

// --- POST /fabric/v1/identity/pair/complete ---

type pairCompleteReq struct {
	PairingToken        string `json:"pairing_token"`
	NewDevicePublicKey  string `json:"new_device_public_key"` // base64
	NewDeviceName       string `json:"new_device_name"`
}

type pairCompleteResp struct {
	DeviceID         string             `json:"device_id"`
	EnrollmentGrant  string             `json:"enrollment_grant"` // base64 macaroon
	TransportConfigs []map[string]any   `json:"transport_configs"`
}

// handlePairComplete is unauthenticated — the pairing_token is the auth.
func (s *Server) handlePairComplete(w http.ResponseWriter, r *http.Request) {
	var req pairCompleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "request body is not valid JSON", false)
		return
	}
	if req.PairingToken == "" || req.NewDevicePublicKey == "" || req.NewDeviceName == "" {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "pairing_token, new_device_public_key, new_device_name are required", false)
		return
	}
	pub, err := base64.StdEncoding.DecodeString(req.NewDevicePublicKey)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, ErrBadRequest, "new_device_public_key is not valid base64", false)
		return
	}
	pt, err := s.store.LookupPairingToken(r.Context(), req.PairingToken)
	if err != nil {
		writeError(w, r, http.StatusNotFound, ErrNotFound, "pairing token not found", false)
		return
	}
	now := s.now()
	if now.After(pt.ExpiresAt) {
		writeError(w, r, http.StatusUnauthorized, ErrGrantInvalid, "pairing token expired", false)
		return
	}
	if pt.CompletedAt != nil {
		writeError(w, r, http.StatusConflict, ErrConflict, "pairing token already used", false)
		return
	}

	deviceID := newULID()
	if err := s.store.UpsertPrincipal(r.Context(), storage.Principal{
		PrincipalID:       deviceID,
		PrincipalType:    "device",
		PublicKey:         pub,
		ParentPrincipalID: pt.InitiatedByPrincipal,
		DisplayName:       req.NewDeviceName,
		CreatedAt:         now,
	}); err != nil {
		s.logger.Error("UpsertPrincipal device failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "device enroll failed", true)
		return
	}
	if err := s.store.CompletePairing(r.Context(), req.PairingToken, deviceID); err != nil {
		s.logger.Error("CompletePairing failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "pairing completion failed", true)
		return
	}

	// Mint an initial Grant for the new device. Scope is identity:enroll with
	// a 10-minute lifetime so the new device can complete setup but not act
	// beyond that without a fresh Grant from the operator.
	grantID := newULID()
	enroll, err := grant.Mint(grant.MintSpec{
		RootKey:  s.hubID.MacaroonRootKey,
		Location: r.Host,
		Identifier: grant.Identifier{
			GrantID:           grantID,
			IssuedAt:          now,
			IssuedByPrincipal: s.hubID.HubID,
			IssuedByKeypair:   s.hubID.PublicKey,
			Scope: grant.Scope{
				Primitive:  "identity",
				Namespace:  "*",
				Operations: []string{"enroll"},
			},
		},
		Caveats: []string{
			"time < " + now.Add(10*time.Minute).UTC().Format(time.RFC3339Nano),
			"principal-type in [device]",
			"device-id == " + deviceID,
			"nondelegatable",
		},
	})
	if err != nil {
		s.logger.Error("Mint enrollment grant failed", "err", err)
		writeError(w, r, http.StatusInternalServerError, ErrUnavailable, "could not mint enrollment grant", true)
		return
	}
	transportConfigs := []map[string]any{
		{
			"type":       "hub",
			"url":        rendezvousBase(r),
			"public_key": base64.StdEncoding.EncodeToString(s.hubID.PublicKey),
			"preference": 1,
		},
	}
	writeSuccess(w, r, http.StatusOK, pairCompleteResp{
		DeviceID:         deviceID,
		EnrollmentGrant:  base64.StdEncoding.EncodeToString(enroll.Macaroon),
		TransportConfigs: transportConfigs,
	}, FreshnessNow(s.now()))
}

// rendezvousBase returns the public-facing URL of this Hub for use in
// transport configs handed to a newly paired device.
func rendezvousBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func encodeQRPayload(token, rendezvous string, expiresAt time.Time) (string, error) {
	payload := map[string]any{
		"fabric_pair":    "1.0",
		"pairing_token":  token,
		"rendezvous_url": rendezvous,
		"expires_at":     expiresAt.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// Base32 (Crockford-friendly via stdlib's base32 with padding stripped)
	// is more QR-tolerant than base64. We use std base32 to match the
	// fabric-spec §"QR payload format" which calls for base32.
	return strings.TrimRight(base32StdEnc.EncodeToString(b), "="), nil
}

var base32StdEnc = base32Lower()

// base32Lower returns a base32 encoder with a lowercase alphabet, just for
// readability in QR text. Wire compatibility is preserved at the
// encoding-vs-decoding step (both producer and consumer use this encoder).
func base32Lower() *baseEncoderShim { return &baseEncoderShim{} }

// baseEncoderShim wraps encoding/base32 std encoder. Defined as a local type
// to avoid importing the encoding/base32 package at the top level — keeps the
// imports section neat without making the file depend on stdlib pkg name
// collisions with our `base64` helpers elsewhere.
type baseEncoderShim struct{}

func (baseEncoderShim) EncodeToString(b []byte) string {
	return baseEncodeBase32(b)
}

// baseEncodeBase32 is implemented in a separate file to keep this one focused.
// See base32.go.

// newNumericCode returns a cryptographically-random n-digit numeric code as a
// zero-padded string.
func newNumericCode(n int) (string, error) {
	if n <= 0 || n > 9 {
		return "", fmt.Errorf("newNumericCode: n out of range")
	}
	var max int64 = 1
	for i := 0; i < n; i++ {
		max *= 10
	}
	v, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", n, v.Int64()), nil
}

// ulidNowString returns a fresh ULID; used as a fallback if newULID is unavailable.
func ulidNowString() string {
	id, _ := ulid.New(ulid.Now(), rand.Reader)
	return id.String()
}
