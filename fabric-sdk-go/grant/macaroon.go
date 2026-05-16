package grant

import (
	"encoding/json"
	"errors"
	"fmt"

	"gopkg.in/macaroon.v2"
)

// MacaroonVersion is the libmacaroon wire version used by the fabric protocol.
// fabric-spec-001-v1.0.md §"Macaroon structure" pins this to v2.
const MacaroonVersion = macaroon.V2

// ErrSignatureInvalid is returned when macaroon HMAC verification fails.
var ErrSignatureInvalid = errors.New("grant: macaroon signature verification failed")

// MintSpec is the input to Mint.
type MintSpec struct {
	// RootKey is the HMAC secret known to the issuer and verifier. 32 bytes recommended.
	RootKey []byte
	// Location is bound into the macaroon. Use "*" for transport-agnostic Grants.
	Location string
	// Identifier carries the structured grant header that consumers can inspect.
	Identifier Identifier
	// Caveats is the ordered list of first-party caveat strings (per the spec's
	// caveat catalog, e.g. "time < 2026-06-15T18:00:00Z"). For M1 the wrapper
	// passes these through verbatim; semantic checking is the verifier's job.
	Caveats []string
}

// Mint constructs a macaroon from spec and returns the high-level Grant.
// The Grant's Macaroon field holds the libmacaroon v2 wire bytes.
func Mint(spec MintSpec) (*Grant, error) {
	idJSON, err := json.Marshal(spec.Identifier)
	if err != nil {
		return nil, fmt.Errorf("grant.Mint: marshal identifier: %w", err)
	}
	m, err := macaroon.New(spec.RootKey, idJSON, spec.Location, MacaroonVersion)
	if err != nil {
		return nil, fmt.Errorf("grant.Mint: macaroon.New: %w", err)
	}
	for _, c := range spec.Caveats {
		if err := m.AddFirstPartyCaveat([]byte(c)); err != nil {
			return nil, fmt.Errorf("grant.Mint: add caveat %q: %w", c, err)
		}
	}
	bin, err := m.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("grant.Mint: marshal: %w", err)
	}
	return &Grant{
		GrantID:    spec.Identifier.GrantID,
		Macaroon:   bin,
		Identifier: spec.Identifier,
		Caveats:    append([]string(nil), spec.Caveats...),
	}, nil
}

// Parse decodes wire-format macaroon bytes into a Grant, extracting the
// structured Identifier and the caveat list. Parse does NOT verify the
// signature; call VerifySignature for that.
func Parse(macBytes []byte) (*Grant, error) {
	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(macBytes); err != nil {
		return nil, fmt.Errorf("grant.Parse: unmarshal: %w", err)
	}
	var id Identifier
	if err := json.Unmarshal(m.Id(), &id); err != nil {
		return nil, fmt.Errorf("grant.Parse: identifier JSON: %w", err)
	}
	caveats := make([]string, 0, len(m.Caveats()))
	for _, c := range m.Caveats() {
		if c.VerificationId != nil {
			// Third-party caveats land here in a later milestone (M2/M3).
			caveats = append(caveats, "third-party:"+string(c.Id))
			continue
		}
		caveats = append(caveats, string(c.Id))
	}
	return &Grant{
		GrantID:    id.GrantID,
		Macaroon:   append([]byte(nil), macBytes...),
		Identifier: id,
		Caveats:    caveats,
	}, nil
}

// CheckFunc evaluates a first-party caveat string against application context.
// Return nil if satisfied; return a non-nil error to reject. M1 verification
// passes a no-op CheckFunc (signature only); M2+ wires in the caveat catalog.
type CheckFunc func(caveat string) error

// AlwaysSatisfied is a CheckFunc that accepts every first-party caveat. Useful
// for the M1 gate where we only test wire-level signature interop.
func AlwaysSatisfied(string) error { return nil }

// VerifySignature checks the HMAC chain over the macaroon given the issuer's
// root key. The check func evaluates first-party caveats; pass AlwaysSatisfied
// to verify only the signature chain.
func VerifySignature(macBytes, rootKey []byte, check CheckFunc) error {
	var m macaroon.Macaroon
	if err := m.UnmarshalBinary(macBytes); err != nil {
		return fmt.Errorf("grant.VerifySignature: unmarshal: %w", err)
	}
	if check == nil {
		check = AlwaysSatisfied
	}
	if err := m.Verify(rootKey, func(c string) error { return check(c) }, nil); err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	return nil
}
