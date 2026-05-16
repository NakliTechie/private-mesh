package server

import (
	"fmt"
	"strings"
	"time"
)

// CaveatContext is the runtime context against which first-party caveats are
// evaluated. Phase 2b populates only the fields the Hub can derive from the
// request itself (server clock, requested operation, requested namespace,
// whether an idempotency key is present, whether this is a delegation mint).
//
// Caveats that constrain the bearer's identity (principal-type, agent-id,
// device-id) are accepted but treated as Hub-trusted assertions for v1.0:
// the macaroon's HMAC chain proves the issuer attested to the recipient's
// identity. M3 conformance wires a current-principal layer so these caveats
// can be cross-checked against an authenticated requester.
type CaveatContext struct {
	Now                 time.Time
	Operation           string
	Namespace           string
	Primitive           string
	HasIdempotencyKey   bool
	IsDelegationRequest bool
}

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
	case strings.HasPrefix(c, "principal-type in "),
		strings.HasPrefix(c, "agent-id == "),
		strings.HasPrefix(c, "device-id == "):
		// Hub-trusted in phase 2b: the issuer attested to these by signing
		// the macaroon. M3 cross-checks against an authenticated requester.
		return nil
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
	case strings.HasPrefix(c, "rate <= "),
		strings.HasPrefix(c, "max-amount <= "),
		strings.HasPrefix(c, "only-domain in "),
		c == "requires-human-approval",
		strings.HasPrefix(c, "discharge-from "):
		// Phase 2b parses but does not enforce these. M3 conformance wires
		// rate counters, BYOK amount checks, the bridge domain check, the
		// approval queue, and the discharge fetcher.
		return nil
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

// CaveatError is a typed error so callers can use errors.As.
type CaveatError struct {
	Caveat string
	Reason string
}

func (e *CaveatError) Error() string {
	return fmt.Sprintf("caveat unmet: %s: %s", e.Caveat, e.Reason)
}

func caveatErr(caveat, reason string) error { return &CaveatError{Caveat: caveat, Reason: reason} }
