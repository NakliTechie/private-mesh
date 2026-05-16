// Package identity holds the Fabric Identity File (FIF) shape and the
// envelope parse/unlock/serialize machinery. Authoritative wire format is in
// docs/specs/fabric-spec-001-v1.0.md §"Fabric Identity File".
package identity

import "time"

// InnerFIFFormat is the format string carried by the inner FIF body.
const InnerFIFFormat = "fif-inner/1.0"

// PrincipalType identifies who holds an Identity.
type PrincipalType string

const (
	PrincipalHuman  PrincipalType = "human"
	PrincipalAgent  PrincipalType = "agent"
	PrincipalDevice PrincipalType = "device"
)

// Principal is the identity header carried inside the FIF.
type Principal struct {
	Type        PrincipalType `json:"type"`
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	CreatedAt   time.Time     `json:"created_at"`
}

// KeyAlgorithm is the named signing-key algorithm. v1.0 fabric uses ed25519 only.
type KeyAlgorithm string

const KeyAlgEd25519 KeyAlgorithm = "ed25519"

// KeyPair is a public + private signing keypair. Private keys are zeroed on Lock.
type KeyPair struct {
	Algorithm  KeyAlgorithm `json:"algorithm"`
	PublicKey  []byte       `json:"public_key"`
	PrivateKey []byte       `json:"private_key"`
}

// DeviceSubkey is a device-bound subkey enrolled on the holder's behalf.
type DeviceSubkey struct {
	DeviceID   string       `json:"device_id"`
	DeviceName string       `json:"device_name"`
	Algorithm  KeyAlgorithm `json:"algorithm"`
	PublicKey  []byte       `json:"public_key"`
	PrivateKey []byte       `json:"private_key"`
	EnrolledAt time.Time    `json:"enrolled_at"`
}

// AgentIdentity is an agent-bound keypair provisioned by the holder.
type AgentIdentity struct {
	AgentID       string       `json:"agent_id"`
	AgentName     string       `json:"agent_name"`
	Vendor        string       `json:"vendor"`
	Algorithm     KeyAlgorithm `json:"algorithm"`
	PublicKey     []byte       `json:"public_key"`
	PrivateKey    []byte       `json:"private_key"`
	ProvisionedAt time.Time    `json:"provisioned_at"`
	ExpiresAt     time.Time    `json:"expires_at,omitempty"`
}

// TransportConfig configures one transport the holder can reach.
// Hub and cf-worker carry URL + public_key; local-network carries service_name.
type TransportConfig struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	PublicKey   []byte `json:"public_key,omitempty"`
	Preference  int    `json:"preference"`
}

// HeldGrant is a Grant retained in the FIF for offline use.
type HeldGrant struct {
	GrantID      string    `json:"grant_id"`
	Macaroon     []byte    `json:"macaroon"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	IssuedBy     string    `json:"issued_by"`
	ScopeSummary string    `json:"scope_summary"`
}

// BridgeCredential is a BYOK secret for an external service, double-encrypted at rest.
type BridgeCredential struct {
	Service         string `json:"service"`
	CredentialType  string `json:"credential_type"`
	CredentialValue string `json:"credential_value"`
	ScopeSummary    string `json:"scope_summary"`
}

// StateCache is an opportunistic cache of recent stream heads.
type StateCache struct {
	VaultHeads   map[string]string `json:"vault_heads"`
	HistoryHeads map[string]string `json:"history_heads"`
}

// InnerFIF is the decrypted body of a FIF. Matches the JSON shape in fabric-spec.
type InnerFIF struct {
	Format            string             `json:"format"`
	Principal         Principal          `json:"principal"`
	RootKeypair       KeyPair            `json:"root_keypair"`
	DeviceSubkeys     []DeviceSubkey     `json:"device_subkeys"`
	AgentIdentities   []AgentIdentity    `json:"agent_identities"`
	Transports        []TransportConfig  `json:"transports"`
	GrantsHeld        []HeldGrant        `json:"grants_held"`
	BridgeCredentials []BridgeCredential `json:"bridge_credentials"`
	RecentStateCache  StateCache         `json:"recent_state_cache"`
}

// NewInnerFIF returns an InnerFIF with empty collections initialized so
// JSON serialization produces "[]" / "{}" instead of "null".
func NewInnerFIF(p Principal, root KeyPair) *InnerFIF {
	return &InnerFIF{
		Format:            InnerFIFFormat,
		Principal:         p,
		RootKeypair:       root,
		DeviceSubkeys:     []DeviceSubkey{},
		AgentIdentities:   []AgentIdentity{},
		Transports:        []TransportConfig{},
		GrantsHeld:        []HeldGrant{},
		BridgeCredentials: []BridgeCredential{},
		RecentStateCache: StateCache{
			VaultHeads:   map[string]string{},
			HistoryHeads: map[string]string{},
		},
	}
}
