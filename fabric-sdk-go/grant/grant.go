// Package grant implements the Grant data model and the macaroon serialization
// wrapper for the Private Mesh fabric. Authoritative wire format and caveat
// catalog are in docs/specs/fabric-spec-001-v1.0.md §"Capability tokens".
package grant

import "time"

// Primitive names the fabric primitive a Grant authorizes operations on.
type Primitive string

const (
	PrimitiveVault    Primitive = "vault"
	PrimitiveHistory  Primitive = "history"
	PrimitiveBridge   Primitive = "bridge"
	PrimitiveLLM      Primitive = "llm"
	PrimitiveSync     Primitive = "sync"
	PrimitiveIdentity Primitive = "identity"
	PrimitiveGrant    Primitive = "grant"
)

// Scope describes what a Grant authorizes.
type Scope struct {
	Primitive  Primitive `json:"primitive"`
	Namespace  string    `json:"namespace"`
	Operations []string  `json:"operations"`
}

// Identifier is the structured content carried inside a macaroon's "id" field.
// JSON-marshalled per fabric-spec-001-v1.0.md §"Macaroon structure".
type Identifier struct {
	GrantID           string    `json:"grant_id"`
	IssuedAt          time.Time `json:"issued_at"`
	IssuedByPrincipal string    `json:"issued_by_principal"`
	IssuedByKeypair   []byte    `json:"issued_by_keypair"`
	ParentGrantID     string    `json:"parent_grant_id,omitempty"`
	Scope             Scope     `json:"scope"`
}

// Grant is the high-level Grant the SDK exposes. The Macaroon field is the
// wire-format bytes that consumers send in X-Fabric-Grant.
type Grant struct {
	GrantID   string
	Macaroon  []byte
	Identifier Identifier
	Caveats   []string
}
