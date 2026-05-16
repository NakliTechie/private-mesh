# Fabric SDK (Go) Specification

**Document:** `fabric-sdk-go-spec-001-v1.1.md`
**Status:** v1.1 draft, normative
**Supersedes:** `fabric-sdk-go-spec-001-v1.0.md` — adds explicit dependency choices (macaroon library, crypto libraries, SQLite driver, HTTP routing, ULID library) per the reuse audit.
**Companion to:** `fabric-spec-001-v1.0.md` (the protocol)
**Audience:** Implementers of `fabric-sdk-go`; consumers writing Go binaries, daemons, the Hub, the reference CLI, and Go-based agents.

The Go SDK serves Go consumers: the `nakli-hub` binary itself (which uses the SDK for client-side operations), the `nakli-cli` reference CLI, the `nakli-llm-server` daemon (Phase 2), and any third-party Go agent or service that consumes the Fabric.

**Idiomatic Go.** Standard `context.Context` for cancellation. Errors as values. `io.Reader`/`io.Writer` for streams. Standard library first; minimal dependencies. No reflection-heavy magic.

---

## Scope

This document specifies:
- Package layout (`github.com/naklitechie/fabric-sdk-go`)
- Public API surface
- FIF management
- Grant management
- Primitive client methods
- Failure-model hooks (queue, freshness, health)
- Idempotency handling
- Embedding considerations (the Hub uses it; the CLI uses it; agents use it)

Out of scope (same as JS SDK):
- Internal storage implementation
- CLI command surface (that's a separate spec)
- Transport implementation (transports use this SDK to be Fabric-compliant clients of other transports if needed; transport server implementation is in their respective specs)

---

## Dependencies

The SDK MUST use the following libraries. Where the spec says MUST, the agent uses that library; where it says MAY, the agent uses the named library unless there's a concrete documented reason not to. Spelling these out prevents the agent from reimplementing standard infrastructure.

### Required

- **`gopkg.in/macaroon.v2`** — macaroon serialization, signature chains, first-party and third-party caveats. The library is wire-compatible with libmacaroon C/Python and js-macaroon. v1.0 macaroons use `macaroon.V2`.
- **`gopkg.in/macaroon-bakery.v3`** (the `bakery` package) — higher-level mint/discharge/verify operations. Use this rather than rolling our own macaroon orchestration.
- **`golang.org/x/crypto/argon2`** — Argon2id for FIF passphrase KDF.
- **`golang.org/x/crypto/chacha20poly1305`** — XChaCha20-Poly1305 for payload and FIF body encryption (`chacha20poly1305.NewX`).
- **`golang.org/x/crypto/hkdf`** — HKDF-SHA256 for per-namespace key derivation.
- **`crypto/ed25519`** (stdlib) — root and device keypair signing/verification.
- **`github.com/oklog/ulid/v2`** — ULID generation for event IDs, idempotency keys, principal/agent/device IDs.

### Recommended (use unless there's a concrete reason not to)

- **`net/http`** with Go 1.22+ pattern matching for the SDK's transport-side HTTP handling. No external router framework needed; the standard library covers the routing precision we want.
- **`github.com/google/uuid`** — only if a non-ULID identifier is needed somewhere; ULID is preferred everywhere we have a choice.

### Out of scope for the SDK

- **`mattn/go-sqlite3`** — used by the Hub binary, NOT by the SDK. The SDK is transport-agnostic; storage is a Hub concern.
- **NetBird embed library** — used by `nakli-mesh` (Phase 2), NOT by the SDK directly.

### Forbidden

- Custom macaroon implementations. The library is mature, audited, and wire-compatible. Do not roll our own.
- Custom Argon2id, ChaCha20-Poly1305, HKDF, or Ed25519. Use stdlib + `golang.org/x/crypto`.
- Bundler/code generators for the macaroon caveat vocabulary. Caveats are first-party strings; we parse them in our `check` function passed to `macaroon.Verify`.

### Caveat vocabulary

The SDK defines our caveat vocabulary as parsed strings handed to `macaroon.Verify`'s `check` function. The library has no opinion on caveat content; that's what makes it suitable.

```go
// Example check function shape (illustrative, not authoritative)
func checkCaveat(caveat string) error {
    switch {
    case strings.HasPrefix(caveat, "time < "):
        return checkTimeBefore(caveat)
    case strings.HasPrefix(caveat, "rate <= "):
        return checkRate(caveat)
    case strings.HasPrefix(caveat, "max-amount <= "):
        return checkMaxAmount(caveat)
    // ... other caveats per fabric-spec-001-v1.0.md
    }
    return fmt.Errorf("unknown caveat: %s", caveat)
}
```

---

## Package layout

```
github.com/naklitechie/fabric-sdk-go/
├── fabric.go              # Top-level Client type
├── fabric_test.go
├── identity/
│   ├── identity.go        # Identity and Principal types
│   ├── fif.go             # FIF parse/encrypt/decrypt
│   ├── pairing.go         # Pairing flows
│   └── agents.go          # Agent provisioning/retirement
├── grant/
│   ├── grant.go           # Grant, Caveat types
│   ├── macaroon.go        # thin wrapper over gopkg.in/macaroon.v2 + bakery
│   ├── mint.go            # Minting and delegation
│   └── verify.go          # Local verification helpers
├── vault/
│   └── vault.go           # Vault client
├── history/
│   └── history.go         # History client with hash-chain
├── sync/
│   └── sync.go            # Sync client
├── llm/
│   └── llm.go             # LLM client
├── bridge/
│   └── bridge.go          # Bridge client
├── queue/
│   ├── queue.go           # Operation queue (interface)
│   ├── sqlite.go          # SQLite-backed implementation
│   └── memory.go          # In-memory implementation (testing)
├── transport/
│   ├── transport.go       # Transport interface
│   ├── http.go            # HTTP transport (the common case)
│   └── manager.go         # Multi-transport selection
├── conformance/
│   └── conformance.go     # Conformance test suite (used to test transports)
├── crypto/
│   ├── crypto.go          # Crypto primitives wrapper
│   └── kdf.go             # HKDF, Argon2id
├── events/
│   └── events.go          # Event bus
└── internal/
    └── ...                # Implementation details
```

External dependencies (minimal):
- `golang.org/x/crypto` — for `argon2`, `chacha20poly1305`, `hkdf`
- `github.com/oklog/ulid/v2` — ULID generation
- `crawshaw.io/sqlite` or `modernc.org/sqlite` — embedded SQLite for queue/cache
- `filippo.io/edwards25519` — Ed25519 (or stdlib `crypto/ed25519` when sufficient)

Total dep tree should be reviewable; no transitive sprawl.

---

## Top-level: package `fabric`

```go
package fabric

import (
    "context"
    "io"
)

// Client is the main entry point for the SDK.
type Client struct {
    // ... opaque
}

// Options configures a Client.
type Options struct {
    QueueStorage      QueueStorage
    QueueDB           string         // path to SQLite file; "" = memory
    Transports        []TransportConfig
    StalenessBudget   time.Duration  // default: 24h
    Logger            *slog.Logger
    HTTPClient        *http.Client   // optional; default uses sane settings
    Clock             Clock          // for testing
}

func New(opts Options) (*Client, error)

// Lifecycle
func (c *Client) UnlockFIF(ctx context.Context, fif io.Reader, passphrase string) (*identity.Identity, error)
func (c *Client) CreateFIF(ctx context.Context, passphrase, principalName string, w io.Writer) (*identity.Identity, error)
func (c *Client) Lock() error
func (c *Client) Close() error  // closes queue, transports, releases resources

// Accessors for primitive clients
func (c *Client) Identity() *identity.Client
func (c *Client) Grants() *grant.Client
func (c *Client) Vault() *vault.Client
func (c *Client) History() *history.Client
func (c *Client) Sync() *sync.Client
func (c *Client) LLM() *llm.Client
func (c *Client) Bridge() *bridge.Client

// Failure-model surface
func (c *Client) Queue() *queue.Client
func (c *Client) Freshness() *FreshnessSnapshot
func (c *Client) Health(ctx context.Context) (*HealthSnapshot, error)
func (c *Client) Events() *events.Bus
```

### Error model

```go
// All SDK errors satisfy error and Error.
type Error struct {
    Code      string  // matches protocol error codes
    Message   string
    Retryable bool
    Cause     error
}

func (e *Error) Error() string
func (e *Error) Unwrap() error
func (e *Error) Is(target error) bool

// Sentinel errors for code-level matching.
var (
    ErrGrantInvalid          = &Error{Code: "grant_invalid"}
    ErrGrantMissing          = &Error{Code: "grant_missing"}
    ErrGrantRevoked          = &Error{Code: "grant_revoked"}
    ErrScopeDenied           = &Error{Code: "scope_denied"}
    ErrCaveatUnmet           = &Error{Code: "caveat_unmet"}
    ErrIdempotencyConflict   = &Error{Code: "idempotency_conflict"}
    ErrConflict              = &Error{Code: "conflict"}
    ErrUnavailable           = &Error{Code: "unavailable"}
    ErrPartition             = &Error{Code: "partition"}
    ErrVersionMismatch       = &Error{Code: "version_mismatch"}
    ErrRateLimited           = &Error{Code: "rate_limited"}
    ErrHumanApprovalRequired = &Error{Code: "human_approval_required"}
    ErrFIFFormat             = &Error{Code: "fif_format"}
    ErrFIFAuth               = &Error{Code: "fif_auth"}
    ErrIdentityLocked        = &Error{Code: "identity_locked"}
)

// errors.Is(err, ErrGrantInvalid) works as expected.
```

---

## Package `identity`

```go
package identity

type Principal struct {
    ID          string
    Type        PrincipalType  // Human | Agent | Device
    PublicKey   []byte
    DisplayName string
    CreatedAt   time.Time
}

type Identity struct {
    Principal     *Principal
    RootKeypair   *KeyPair
    DeviceSubkeys []*DeviceSubkey
    Agents        []*AgentIdentity
    Transports    []*TransportConfig
    GrantsHeld    []*grant.Grant
    BridgeCreds   []*BridgeCredential
}

type Client struct {
    // ... opaque
}

func (c *Client) Current() *Identity  // current unlocked identity; nil if locked
func (c *Client) ProvisionAgent(ctx context.Context, spec AgentSpec) (*AgentIdentity, error)
func (c *Client) RetireAgent(ctx context.Context, agentID, reason string) error
func (c *Client) EnrollDevice(ctx context.Context, deviceName string) (*DeviceSubkey, error)

// Pairing
type PairingMethod int
const (
    PairingQR PairingMethod = iota
    PairingCode
    PairingLink
)

type PairingArtifacts struct {
    PairingToken string
    QRPayload    string  // base32, render to QR
    NumericCode  string
    MagicLink    string
    ExpiresAt    time.Time
}

func (c *Client) InitiatePair(ctx context.Context, method PairingMethod) (*PairingArtifacts, error)
func (c *Client) CompletePair(ctx context.Context, token, deviceName string) (*DeviceEnrollment, error)
```

### FIF parse/serialize

```go
// In identity/fif.go

type FIF struct {
    Format         string
    EnvelopeType   string
    EnvelopeParams map[string]any
    Inner          *InnerFIF  // nil until decrypted
}

func ParseFIF(r io.Reader) (*FIF, error)
func (f *FIF) Unlock(passphrase string) error  // populates Inner
func (f *FIF) Serialize(w io.Writer) error
func (f *FIF) RotateEnvelope(newType string, params map[string]any) error
```

The FIF type does NOT touch the network. It's purely in-memory parsing and crypto.

---

## Package `grant`

```go
package grant

type Grant struct {
    GrantID            string
    Macaroon           []byte  // wire-format bytes
    IssuedAt           time.Time
    ExpiresAt          time.Time
    IssuedByPrincipal  string
    ParentGrantID      string  // empty if root
    Scope              Scope
    Caveats            []Caveat
}

type Scope struct {
    Primitive  string  // "vault" | "history" | etc.
    Namespace  string  // or "*"
    Operations []string
}

type Caveat interface {
    CaveatType() string
    Marshal() ([]byte, error)
}

// Concrete caveat types
type TimeBefore struct { Time time.Time }
type TimeAfter struct { Time time.Time }
type PrincipalTypeIn struct { Types []string }
type AgentID struct { ID string }
type DeviceID struct { ID string }
type Operation struct { Ops []string }
type Namespace struct { Name string }
type Rate struct { Count int; Window string }
type MaxAmount struct { Amount int64; Currency string }
type OnlyDomain struct { Domains []string }
type RequiresHumanApproval struct{}
type Nondelegatable struct{}
type IdempotencyRequired struct{}
type DischargeFrom struct { VerifierURL string }

type Client struct { /* ... */ }

// Mint a Grant. If parentGrant is non-nil, the new Grant is a delegation
// of parentGrant (must be strictly narrower). If nil, this mints a root
// Grant (requires Identity with root key access).
type MintSpec struct {
    Recipient    string  // principal ID
    Scope        Scope
    Caveats      []Caveat
    ExpiresAt    time.Time
    ParentGrant  *Grant  // nil for root
}

func (c *Client) Mint(ctx context.Context, spec MintSpec) (*Grant, error)
func (c *Client) Verify(ctx context.Context, macaroon []byte, op HypotheticalOp) (*VerifyResult, error)
func (c *Client) Revoke(ctx context.Context, grantID, reason string) error
func (c *Client) List(ctx context.Context, filter Filter) ([]*Grant, error)

// HypotheticalOp is "what would this Grant let me do"
type HypotheticalOp struct {
    Primitive string
    Namespace string
    Operation string
    Context   map[string]any  // for caveats that depend on context (rate, amount, etc.)
}

type VerifyResult struct {
    WouldSucceed bool
    Reasons      []string
}
```

### Macaroon construction is internal

The SDK exposes high-level `Mint`. Macaroon byte construction happens internally per `fabric-spec-001-v1.0.md` Section "Capability tokens." Consumers do not write macaroons directly.

---

## Package `vault`

```go
package vault

type Event struct {
    EventID            string
    Kind               string
    Payload            []byte  // plaintext; SDK encrypts before send
    CausalDependencies []string
    VectorClock        map[string]int64
    AppendedAt         time.Time
    AppendedBy         string  // principal ID
}

type AppendSpec struct {
    Namespace      string
    StreamID       string
    Event          Event
    IdempotencyKey string  // optional; SDK generates if empty
}

type AppendResult struct {
    EventID        string
    SequenceNumber int64
}

type Client struct { /* ... */ }

func (c *Client) Append(ctx context.Context, spec AppendSpec) (*AppendResult, error)
func (c *Client) Read(ctx context.Context, namespace, streamID string, opts ReadOptions) ([]*Event, error)
func (c *Client) ListStreams(ctx context.Context, namespace string) ([]*StreamSummary, error)

// Subscribe returns a channel of events. Cancel via ctx.
func (c *Client) Subscribe(ctx context.Context, namespace, streamID string, sinceEventID string) (<-chan *Event, error)

type ReadOptions struct {
    SinceEventID string
    Limit        int  // default 100, max 1000
}
```

### Encryption is transparent

The Vault client encrypts `Event.Payload` before sending and decrypts on read. Plaintext never leaves the SDK boundary.

If decryption fails for one event in a Read response, that event's `Payload` is nil and `DecryptError` is populated; the call does not fail entirely.

### Causal metadata

The SDK tracks each device's vector clock in the queue's persistent store. On `Append`:
- Local clock increments
- `VectorClock` is set
- `CausalDependencies` is populated from local heads

Consumers can override by setting these fields explicitly (for replaying events from elsewhere).

---

## Package `history`

```go
package history

type Event struct {
    EventID           string
    Kind              string
    Payload           []byte
    PreviousEventHash []byte
    EventHash         []byte
    AppendedAt        time.Time
    AppendedBy        string
}

type Client struct { /* ... */ }

func (c *Client) Append(ctx context.Context, spec AppendSpec) (*AppendResult, error)
func (c *Client) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error)
func (c *Client) Verify(ctx context.Context, streamID string) (*VerifyResult, error)
```

The History client handles `PreviousEventHash` automatically: fetch head → compute hash → attempt append → on conflict, refetch head.

Verification walks the chain; for long streams the consumer may want to checkpoint and verify incrementally.

---

## Package `sync`

```go
package sync

type Client struct { /* ... */ }

type Status struct {
    Peers              []PeerStatus
    OverallFreshnessMs int64
}

type PeerStatus struct {
    PeerID             string
    LastSyncAt         time.Time
    FreshnessMs        int64
    PendingEventsOut   int
    PendingEventsIn    int
}

func (c *Client) Status(ctx context.Context) (*Status, error)
func (c *Client) ForcePull(ctx context.Context, peerID string) error  // empty peerID = all
func (c *Client) ForcePush(ctx context.Context, peerID string) error
func (c *Client) Peers(ctx context.Context) ([]*Peer, error)
```

Sync is primarily managed by the SDK transparently. Consumers usually don't call these; they just observe `Freshness()` snapshots.

---

## Package `llm`

```go
package llm

type Client struct { /* ... */ }

type CompletionSpec struct {
    Messages       []Message
    Capabilities   *Capabilities  // optional
    PreferredRoute string         // "local"|"browser-local"|"remote"|"auto"
    MaxCostCents   int            // 0 = no limit (subject to Grant caveats)
    IdempotencyKey string         // optional
}

type Message struct {
    Role    string  // "user"|"assistant"|"system"
    Content string
}

type Capabilities struct {
    MinContextWindow    int
    NeedsVision         bool
    NeedsFunctionCalling bool
}

type CompletionResult struct {
    Content    string
    RouteTaken string
    CostCents  int
    Tokens     TokenUsage
}

func (c *Client) Complete(ctx context.Context, spec CompletionSpec) (*CompletionResult, error)
func (c *Client) Routes(ctx context.Context) ([]*Route, error)
```

For Phase 2, the LLM client will integrate with `nakli-llm-server` on the anchor. v1.0 SDK supports remote BYOK routing only.

---

## Package `bridge`

```go
package bridge

type Client struct { /* ... */ }

type CallSpec struct {
    Adapter        string
    Operation      string
    Params         map[string]any
    DryRun         bool
    IdempotencyKey string  // optional; SDK generates
}

type CallResult struct {
    Status              string  // "completed"|"pending"
    Result              map[string]any
    PendingOperationID  string  // populated if Status="pending"
    WillExecuteIfApproved *CallSpec
}

func (c *Client) Call(ctx context.Context, spec CallSpec) (*CallResult, error)
func (c *Client) Approve(ctx context.Context, pendingOpID string, approve bool, reason string) (*CallResult, error)
func (c *Client) ListPending(ctx context.Context) ([]*PendingOperation, error)
func (c *Client) Adapters(ctx context.Context) ([]*Adapter, error)
```

---

## Package `queue`

```go
package queue

type Client struct { /* ... */ }

type Operation struct {
    ID             string
    Primitive      string
    Endpoint       string
    Payload        []byte
    Attempts       int
    LastAttemptAt  time.Time
    NextAttemptAt  time.Time
    LastError      string
    IdempotencyKey string
}

func (c *Client) Size() int
func (c *Client) Pending(ctx context.Context) ([]*Operation, error)
func (c *Client) Retry(ctx context.Context, operationID string) error
func (c *Client) Cancel(ctx context.Context, operationID string) error
func (c *Client) Clear(ctx context.Context, filter Filter) (int, error)
func (c *Client) Observe(handler func(Event)) func()  // returns unsubscribe

type Event struct {
    Type      EventType  // Enqueued|Attempt|Succeeded|FailedPermanent|FailedRetryable
    Operation *Operation
    Result    any
    Error     error
}
```

### Persistence

Default: SQLite-backed (single file specified by `Options.QueueDB`). On `Client.Close()`, queue is flushed. On `Client.New()`, queue is replayed (operations with `NextAttemptAt <= now` resume attempts).

Alternate: in-memory (for tests and ephemeral consumers).

---

## Package `transport`

```go
package transport

type Transport interface {
    ID() string
    Type() string  // "hub"|"cf-worker"|"local-network"|...
    Available(ctx context.Context) bool
    Do(ctx context.Context, req *Request) (*Response, error)
    Close() error
}

type Request struct {
    Method      string
    Endpoint    string  // e.g., "/fabric/v1/vault/append"
    Headers     http.Header
    Body        []byte
    Grant       *grant.Grant
    Idempotency string
}

type Response struct {
    Status     int
    Headers    http.Header
    Body       []byte
    Freshness  *Freshness
    ProtocolVersion string
}

// HTTPTransport is the common case: any transport speaking the HTTP protocol.
type HTTPTransport struct { /* ... */ }
func NewHTTPTransport(url string, opts HTTPOptions) (*HTTPTransport, error)

// Manager handles multi-transport selection with preference and fallback.
type Manager struct { /* ... */ }
func NewManager(transports []Transport, opts ManagerOptions) *Manager
func (m *Manager) Do(ctx context.Context, req *Request) (*Response, error)
func (m *Manager) Health(ctx context.Context) []TransportHealth
```

### Transport selection logic

`Manager.Do` tries transports in preference order:
1. Skip if `Available()` returned false recently (cached for 5s)
2. Attempt with per-transport timeout
3. On `ErrUnavailable` or timeout, try next
4. On all transports failing, return `ErrUnavailable` and the operation goes to the queue

---

## Package `events`

```go
package events

type Bus struct { /* ... */ }

type EventType string
const (
    Conflict          EventType = "conflict"
    DegradationChange EventType = "degradation-change"
    AgentRetired      EventType = "agent-retired"
    GrantRevoked      EventType = "grant-revoked"
    FIFRotationNeeded EventType = "fif-rotation-needed"
)

type Event struct {
    Type      EventType
    Timestamp time.Time
    Detail    any  // type depends on Type
}

func (b *Bus) On(eventType EventType, handler func(Event)) func()  // returns unsubscribe
func (b *Bus) Publish(event Event)
```

---

## Package `conformance`

The conformance suite is exported so that transport implementations and SDK forks can use it to verify themselves.

```go
package conformance

type SuiteOptions struct {
    TransportURL string
    Grant        []byte  // a Grant with scope to perform conformance ops
    SkipTests    []string
    Verbose      bool
}

type Result struct {
    Total      int
    Passed     int
    Failed     int
    Skipped    int
    Details    []TestResult
}

type TestResult struct {
    Name    string
    Passed  bool
    Reason  string
    Detail  string
}

func RunSuite(ctx context.Context, opts SuiteOptions) (*Result, error)

// Tests are numbered per the protocol spec's conformance section (32 tests in v1.0).
```

Used by the Hub, Cloudflare Worker, and Local Network transport teams as their CI gate; also by third-party transport authors.

---

## Concurrency

The Client is goroutine-safe. All public methods may be called concurrently. The queue serializes operations targeting the same stream (to preserve causal order); operations on different streams may proceed in parallel.

Subscribe streams use channels; the SDK closes the channel when `ctx` is cancelled. Consumers MUST drain or cancel; otherwise goroutines leak.

---

## Embedding patterns

### As a Hub server-side client

The Hub binary uses `fabric-sdk-go` to:
- Verify incoming macaroons (via `grant.Verify`)
- Maintain its own identity for signing freshness metadata
- Sync with peer Hubs (using Sync client against peer URLs)

### As the CLI

The CLI uses the SDK for every operation. The CLI is essentially a thin command-line wrapper over the SDK.

### As an agent runtime

A Go-based agent (or an agent that embeds a Go runtime) initializes:
```go
fab, err := fabric.New(fabric.Options{
    QueueDB: "/var/lib/agent/queue.db",
    Transports: []fabric.TransportConfig{
        { Type: "hub", URL: "https://hub.local" },
    },
})
defer fab.Close()

err = fab.UnlockFIF(ctx, agentFIFReader, "agent-passphrase")
// agent operations from here
```

---

## Compatibility

- Go 1.22+
- linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
- The SDK is tested on all listed platforms via CI

---

## Out of scope for v1.0

- gRPC transport (HTTP-only in v1.0; gRPC may come in v1.x)
- Built-in Prometheus metrics exporter (consumers add their own via Events)
- Distributed tracing integration (consumers add via the http.RoundTripper hook)
- Native macOS keychain integration for FIF storage (deferred)

---

## References

- Protocol spec: `fabric-spec-001-v1.0.md`
- libmacaroon-c (for wire format reference): https://github.com/rescrv/libmacaroons
- noble-curves (JS implementation reference for Ed25519): https://github.com/paulmillr/noble-curves
