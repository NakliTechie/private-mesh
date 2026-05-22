package server

import (
	"fmt"
	"strings"
	"time"
)

// CaveatContext is the runtime context against which first-party caveats are
// evaluated. M3 adds request-side fields used to enforce the caveats parsed-
// but-not-enforced in Phase 2b (`rate`, `max-amount`, `only-domain`,
// `requires-human-approval`, and the structural side of `discharge-from`).
type CaveatContext struct {
	Now                 time.Time
	Operation           string
	Namespace           string
	Primitive           string
	HasIdempotencyKey   bool
	IsDelegationRequest bool

	// GrantID is the macaroon's grant_id; required for keyed rate-bucket lookup.
	GrantID string
	// RequesterPrincipalType is the principal type the authenticator attached
	// to the request (matched against `principal-type in [...]`). "" means the
	// caveat is treated as a Hub-trusted assertion.
	RequesterPrincipalType string
	// RequesterAgentID / RequesterDeviceID are similar pass-throughs for the
	// matching caveats.
	RequesterAgentID  string
	RequesterDeviceID string

	// IsBridgeCall flips the bridge-only caveats on (max-amount, only-domain,
	// requires-human-approval, idempotency-required for bridge per the spec's
	// implicit rule).
	IsBridgeCall bool
	// BridgeDomain is the call target's domain. Required for `only-domain`.
	BridgeDomain string
	// BridgeAmount / BridgeCurrency carry the financial side-effect details.
	// Required for `max-amount`. Currency comparison is case-insensitive.
	BridgeAmount   int64
	BridgeCurrency string

	// Server is the live Server, used to look up rate buckets and discharges.
	// nil during /grant/verify hypothetical checks.
	Server *Server

	// DischargeIDs is the set of third-party caveat ids the caller supplied
	// via X-Fabric-Discharge. discharge-from is satisfied if its caveat id is
	// in this set AND the macaroon-library third-party verification succeeded
	// earlier in the auth path.
	DischargeIDs map[string]struct{}

	// StrictBinding mirrors cfg.Auth.StrictCaveatBinding. When true, the
	// agent-id / device-id / principal-type caveats FAIL on an empty
	// requester value (header absent) instead of returning nil. The default
	// (false) keeps backward-compatible behavior so consumers can be
	// updated before the strict check is flipped on.
	StrictBinding bool
}

// CaveatRejection is the outcome of EvaluateCaveats: nil-error + outcome.
type CaveatRejectionKind int

const (
	// KindUnmet is the default rejection (HTTP 403 caveat_unmet).
	KindUnmet CaveatRejectionKind = iota
	// KindRateLimited indicates the rate caveat was exceeded (HTTP 429).
	KindRateLimited
	// KindHumanApproval indicates a `requires-human-approval` caveat must
	// short-circuit with HTTP 202 + pending-id semantics.
	KindHumanApproval
)

// EvaluateCaveats checks every first-party caveat. Returns nil on full
// satisfaction, or a *CaveatError naming the first unmet caveat.
func EvaluateCaveats(caveats []string, ctx CaveatContext) error {
	for _, c := range caveats {
		if err := evaluateOne(strings.TrimSpace(c), ctx); err != nil {
			return err
		}
	}
	return nil
}

func evaluateOne(c string, ctx CaveatContext) error {
	switch {
	case strings.HasPrefix(c, "time < "):
		return checkTimeBefore(c[len("time < "):], ctx.Now)
	case strings.HasPrefix(c, "time > "):
		return checkTimeAfter(c[len("time > "):], ctx.Now)
	case strings.HasPrefix(c, "principal-type in "):
		return checkPrincipalTypeIn(c[len("principal-type in "):], ctx.RequesterPrincipalType, c, ctx.StrictBinding)
	case strings.HasPrefix(c, "agent-id == "):
		return checkEquals("agent-id", c[len("agent-id == "):], ctx.RequesterAgentID, c, ctx.StrictBinding)
	case strings.HasPrefix(c, "device-id == "):
		return checkEquals("device-id", c[len("device-id == "):], ctx.RequesterDeviceID, c, ctx.StrictBinding)
	case strings.HasPrefix(c, "operation in "):
		return checkOperationIn(c[len("operation in "):], ctx.Operation)
	case strings.HasPrefix(c, "namespace == "):
		want := strings.TrimSpace(c[len("namespace == "):])
		if ctx.Namespace != want {
			return caveatErr(c, "namespace does not match")
		}
	case c == "nondelegatable":
		if ctx.IsDelegationRequest {
			return caveatErr(c, "nondelegatable Grant cannot be used to mint a child Grant")
		}
	case c == "idempotency-required":
		if !ctx.HasIdempotencyKey {
			return caveatErr(c, "X-Fabric-Idempotency-Key required")
		}
	case strings.HasPrefix(c, "rate <= "):
		return checkRate(c[len("rate <= "):], ctx, c)
	case strings.HasPrefix(c, "max-amount <= "):
		return checkMaxAmount(c[len("max-amount <= "):], ctx, c)
	case strings.HasPrefix(c, "only-domain in "):
		return checkOnlyDomain(c[len("only-domain in "):], ctx, c)
	case c == "requires-human-approval":
		// Enforced as a short-circuit in the bridge handler; reaching this
		// branch in non-bridge contexts means the caveat does not apply.
		if ctx.IsBridgeCall {
			return &CaveatError{Caveat: c, Reason: "human approval required", Kind: KindHumanApproval}
		}
	case strings.HasPrefix(c, "discharge-from "):
		return checkDischargeFrom(c[len("discharge-from "):], ctx, c)
	default:
		return caveatErr(c, "unknown caveat")
	}
	return nil
}

func checkTimeBefore(s string, now time.Time) error {
	t, err := parseTimestamp(s)
	if err != nil {
		return caveatErr("time < "+s, fmt.Sprintf("bad timestamp: %v", err))
	}
	if !now.Before(t) {
		return caveatErr("time < "+s, "expired")
	}
	return nil
}

func checkTimeAfter(s string, now time.Time) error {
	t, err := parseTimestamp(s)
	if err != nil {
		return caveatErr("time > "+s, fmt.Sprintf("bad timestamp: %v", err))
	}
	if !now.After(t) {
		return caveatErr("time > "+s, "not yet valid")
	}
	return nil
}

func parseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func checkOperationIn(body, op string) error {
	allowed := parseListBracket(body)
	for _, a := range allowed {
		if a == op {
			return nil
		}
	}
	return caveatErr("operation in "+body, "operation not allowed by caveat")
}

func checkPrincipalTypeIn(body, requesterType, original string, strict bool) error {
	// In lax mode (the default while consumers are being updated), an absent
	// principal-type header is treated as a Hub-trusted assertion. In strict
	// mode the missing header IS the failure: it's how an attacker bypasses
	// the binding caveat. Operators flip cfg.Auth.StrictCaveatBinding once
	// every legitimate caller sends X-Fabric-Principal-Type.
	if requesterType == "" {
		if strict {
			return caveatErr(original, "principal-type binding requires X-Fabric-Principal-Type header")
		}
		return nil
	}
	allowed := parseListBracket(body)
	for _, a := range allowed {
		if a == requesterType {
			return nil
		}
	}
	return caveatErr(original, "principal-type not in allowed set")
}

func checkEquals(name, body, requesterValue, original string, strict bool) error {
	// See checkPrincipalTypeIn for the strict/lax rationale. The missing
	// header is the bypass; strict mode rejects it.
	if requesterValue == "" {
		if strict {
			return caveatErr(original, name+" binding requires X-Fabric-"+headerForCaveat(name)+" header")
		}
		return nil
	}
	want := strings.TrimSpace(body)
	if want != requesterValue {
		return caveatErr(original, name+" does not match")
	}
	return nil
}

// headerForCaveat returns the X-Fabric-* header name corresponding to a
// caveat keyword (used for human-readable error messages).
func headerForCaveat(name string) string {
	switch name {
	case "agent-id":
		return "Agent-Id"
	case "device-id":
		return "Device-Id"
	}
	return name
}

// checkRate enforces a token bucket of N tokens per window. Window units:
// `second`, `minute`, `hour`, `day`. Format: `rate <= N per <unit>`.
func checkRate(body string, ctx CaveatContext, original string) error {
	body = strings.TrimSpace(body)
	parts := strings.SplitN(body, " per ", 2)
	if len(parts) != 2 {
		return caveatErr(original, "rate caveat must be `rate <= N per <unit>`")
	}
	n, err := parsePositiveInt(parts[0])
	if err != nil {
		return caveatErr(original, "rate caveat N must be a positive integer")
	}
	window, err := parseWindow(parts[1])
	if err != nil {
		return caveatErr(original, "rate caveat unit must be second|minute|hour|day")
	}
	if ctx.Server == nil || ctx.GrantID == "" {
		// Hypothetical evaluation (e.g., /grant/verify) — accept without
		// consuming a token.
		return nil
	}
	if !ctx.Server.rateConsume(ctx.GrantID, n, window) {
		return &CaveatError{Caveat: original, Reason: "rate limit exceeded", Kind: KindRateLimited}
	}
	return nil
}

func parseWindow(s string) (time.Duration, error) {
	switch strings.TrimSpace(s) {
	case "second":
		return time.Second, nil
	case "minute":
		return time.Minute, nil
	case "hour":
		return time.Hour, nil
	case "day":
		return 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("unknown window %q", s)
}

func parsePositiveInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty integer")
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit in %q", s)
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("integer must be > 0")
	}
	return n, nil
}

// checkMaxAmount enforces `max-amount <= <integer> <currency>` on Bridge calls.
func checkMaxAmount(body string, ctx CaveatContext, original string) error {
	if !ctx.IsBridgeCall {
		return nil
	}
	parts := strings.Fields(strings.TrimSpace(body))
	if len(parts) != 2 {
		return caveatErr(original, "max-amount caveat must be `max-amount <= <int> <currency>`")
	}
	maxN, err := parsePositiveInt(parts[0])
	if err != nil {
		return caveatErr(original, "max-amount integer invalid")
	}
	if !strings.EqualFold(ctx.BridgeCurrency, parts[1]) {
		return caveatErr(original, "currency does not match caveat")
	}
	if ctx.BridgeAmount > int64(maxN) {
		return caveatErr(original, "request amount exceeds max-amount")
	}
	return nil
}

// checkOnlyDomain enforces `only-domain in [a, b, c]` on Bridge calls.
func checkOnlyDomain(body string, ctx CaveatContext, original string) error {
	if !ctx.IsBridgeCall {
		return nil
	}
	if ctx.BridgeDomain == "" {
		return caveatErr(original, "bridge call missing domain")
	}
	allowed := parseListBracket(body)
	d := strings.ToLower(strings.TrimSpace(ctx.BridgeDomain))
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), d) {
			return nil
		}
	}
	return caveatErr(original, "domain not allowed by caveat")
}

// checkDischargeFrom verifies the caller supplied a verified discharge macaroon
// whose id matches the third-party caveat id. The macaroon-library third-party
// verification happens upstream in the auth middleware; this function checks
// that the discharge actually arrived for this caveat.
func checkDischargeFrom(body string, ctx CaveatContext, original string) error {
	// `discharge-from <verifier-url>` — the third-party caveat id is the
	// verifier-url string, embedded in the macaroon. The macaroon library's
	// verifier already validated the discharge chain; we only confirm the
	// caller actually attached the matching discharge so the caveat isn't a
	// silent no-op when the discharge header is missing.
	want := strings.TrimSpace(body)
	if want == "" {
		return caveatErr(original, "discharge-from caveat missing verifier url")
	}
	if ctx.Server == nil {
		// Hypothetical evaluation; accept.
		return nil
	}
	if _, ok := ctx.DischargeIDs[want]; ok {
		return nil
	}
	if _, cached := ctx.Server.dischargeLookup(want); cached {
		return nil
	}
	return caveatErr(original, "missing discharge macaroon for "+want)
}

// parseListBracket converts "[a, b, c]" into []string{"a","b","c"}.
func parseListBracket(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	out := []string{}
	for _, item := range strings.Split(s, ",") {
		v := strings.TrimSpace(item)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// CaveatError is a typed error so callers can use errors.As and dispatch on Kind.
type CaveatError struct {
	Caveat string
	Reason string
	Kind   CaveatRejectionKind
}

func (e *CaveatError) Error() string {
	return fmt.Sprintf("caveat unmet: %s: %s", e.Caveat, e.Reason)
}

func caveatErr(caveat, reason string) error {
	return &CaveatError{Caveat: caveat, Reason: reason, Kind: KindUnmet}
}
