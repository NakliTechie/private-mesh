# Operation Anomaly Detection Specification

**Document:** `anomaly-detection-spec-001-v1.x.md`
**Status:** v1.x draft (post-v1.0; architecture supports it, implementation deferred)
**Companion to:** `fabric-spec-001-v1.0.md`, `hub-spec-001-v1.0.md`
**Audience:** Implementers of the anomaly detection engine; operators configuring it.

The Private Mesh records every operation in History or in operation logs. Per D-Agents, anomaly detection on agent operations is a v1.x feature the architecture supports: tight Grant scope, History query, structured provenance, bounded blast radius via macaroon caveats. This spec defines the detection engine: what patterns it watches for, how rules are written, how detections surface, and what actions can fire automatically.

The spec covers ALL principal anomalies (human, agent, device), not just agents. Anomalous human or device behavior matters too (compromised account, lost-but-still-active phone).

---

## Scope

This document specifies:
- The data model the engine consumes (already in the protocol)
- The rule catalogue: built-in detection patterns
- The rule definition language (lightweight; not a full DSL)
- Detection lifecycle (collection → evaluation → action)
- Action types: alert, throttle, revoke, queue-for-review, log-only
- The operator surface
- Integration with existing fabric primitives
- Conformance and testing

Out of scope:
- ML-based anomaly detection (deferred; rule-based first, learn what's needed before adding model complexity)
- Cross-fabric anomaly correlation (would require federation; deferred)
- Threat intelligence feeds (external services advising what's anomalous; out of scope for sovereign-first design)
- Forensic timeline reconstruction beyond what History already provides

---

## What "anomalous" means here

Operations the operator would want to be told about. Not "wrong" — wrong is for the Grant system. Anomaly is "this is technically authorized, but looks worth a second look."

Examples:
- An agent that normally writes 5 events/day suddenly writes 500
- A Grant that normally fires from one IP starts firing from a different one
- A Bridge call to a new destination domain that's never been seen before
- A History stream appended-to during hours nobody is awake
- An LLM call exhausting `max-cost` budget faster than usual
- A new device enrollment in a fabric that hasn't seen one in 6 months

Anomalies are signals, not verdicts. The engine surfaces them; the operator decides.

---

## Data model

The engine consumes data already in the fabric:

- **operation_log** (Hub's table; equivalent in Worker) — every authenticated request with principal, grant_id, endpoint, status, duration, timestamp
- **events table** — Vault and History events, with appended_by_principal, appended_by_grant_id, kind, namespace, stream_id, timestamp
- **bridge call log** — adapter, operation, params (or hashed params for privacy), result status, cost, timestamp
- **llm cost log** — route, tokens, cost, timestamp
- **principals, grants_known** — for context (when was this principal provisioned, what's its expected scope)

No new collection is needed. The engine is a consumer of existing observational data.

---

## The engine

### Architecture

The anomaly detection engine runs as part of the Hub (or as a sidecar binary in v1.x). It:

1. **Subscribes** to operation events as they're appended to the operation_log
2. **Maintains** rolling windows of per-principal, per-grant, per-namespace statistics
3. **Evaluates** rules against new events
4. **Emits** detection events when rules fire
5. **Optionally** takes action (revoke, throttle) per rule policy

```
┌──────────────────────────────────────────────────────┐
│ Hub                                                  │
│  ├── HTTP server / protocol endpoints                │
│  ├── SQLite operation_log                            │
│  ├── ... existing functionality                      │
│  │                                                   │
│  └── Anomaly engine (v1.x)                          │
│       ├── Rule loader (TOML files)                   │
│       ├── Rolling-window state                       │
│       ├── Rule evaluator                             │
│       ├── Action dispatcher                          │
│       └── Detection events → fabric.detections stream│
└──────────────────────────────────────────────────────┘
```

The engine is in-process for v1.x. A separate-binary version may come later if isolation is desired.

### Per-principal state

For each principal, the engine maintains:

```go
type PrincipalStats struct {
    PrincipalID         string
    Type                string  // human|agent|device
    
    // Rolling counters (windowed)
    OpsLastMinute       int
    OpsLastHour         int
    OpsLastDay          int
    OpsLast7Days        int
    
    // By-endpoint
    EndpointHistogram   map[string]int  // last day
    
    // By-Bridge-destination
    BridgeDomains       map[string]int  // last 30 days, ever
    
    // By-LLM-cost
    LLMCostCentsLastDay int
    LLMCostCentsLast30D int
    
    // Provenance
    SourceIPs           map[string]int  // last 30 days
    Devices             map[string]int  // operations grouped by device
    
    // Recency
    FirstSeen           time.Time
    LastSeen            time.Time
    
    // Failures
    FailedOpsLast24h    int
}
```

Maintained in SQLite (or in-memory with periodic flush). Reset on schedule (rolling windows).

### Detection events

When a rule fires, a detection event is appended to a History stream named `fabric.detections`:

```json
{
  "kind": "anomaly:detected",
  "payload": {
    "detection_id": "<ulid>",
    "rule_id": "agent-burst",
    "severity": "medium",
    "principal": "01HMXA...",
    "principal_type": "agent",
    "summary": "Agent claude-code-tax-prep writing at 30x normal rate",
    "evidence": {
      "ops_last_minute": 150,
      "baseline_ops_last_minute_p99": 5,
      "examples": ["op_id_1", "op_id_2", "op_id_3"]
    },
    "actions_taken": ["throttle"],
    "actions_recommended": ["review"]
  }
}
```

History streams have hash-chain integrity, so the detection log itself is tamper-evident.

---

## Rule catalogue (v1.x built-in)

Phase 1.x ships with a starter rule set. Operators can disable, tune thresholds, or add custom rules.

### Volume rules

**`burst-by-principal`** — principal X is suddenly doing N× their usual rate
- Severity: medium
- Default threshold: 10× rolling-30-day p99 for that principal, OR > 100 ops/minute
- Action: alert (default), throttle (opt-in)

**`new-endpoint-by-principal`** — principal X calling an endpoint they've never called before
- Severity: low (informational)
- Action: log-only (default), alert (opt-in)

### Bridge / external rules

**`new-bridge-domain`** — Bridge call to a domain never seen before, from any principal
- Severity: medium
- Action: alert

**`new-bridge-domain-by-principal`** — Bridge call to a domain this specific principal never used before
- Severity: low
- Action: log-only

**`cost-budget-pace`** — LLM or Bridge cost outpacing typical usage on track to exceed budget
- Severity: medium  
- Default: cost on track to exceed Grant's `max-amount` caveat within the window with > 6 hours remaining
- Action: alert, queue-for-review (opt-in)

### Source / device rules

**`new-source-ip`** — principal operating from an IP not seen in the last 30 days
- Severity: low (for humans on mobile/travel); medium (for agents)
- Action: alert if principal-type is agent or device

**`new-device-enrollment`** — a new device was enrolled in this fabric
- Severity: high (this is rare; should be noticed)
- Action: alert always (cannot be disabled in default config; explicit opt-out required)

**`agent-provisioned`** — a new agent was provisioned
- Severity: high
- Action: alert always

### Time rules

**`off-hours-activity`** — principal operating outside their usual hours
- Severity: low
- Default threshold: operations during hours when this principal has been historically idle (< 5% of operations in those hours)
- Action: log-only

### Behavioral rules

**`failure-burst`** — many failed operations in a short window (could indicate brute force, mis-configured agent, or scope-exploration)
- Severity: medium
- Default: > 20 failures in 5 minutes from one principal
- Action: alert, throttle (opt-in)

**`grant-scope-exploration`** — principal making many requests at the edge of their Grant's scope (lots of `scope_denied` errors)
- Severity: medium
- Default: > 10 scope_denied errors in 1 hour from one principal
- Action: alert

**`agent-after-retirement`** — operations attempted under a retired agent's keys
- Severity: high
- Action: alert always

### Identity rules

**`grant-minted`** — a new Grant was minted, especially with broad scope
- Severity: medium if scope is broad, low if narrow
- Action: alert if severity ≥ medium

**`agent-delegated-grant-mint`** — an agent (not a human) minted a Grant
- Severity: medium (this is allowed but should be visible)
- Action: alert

### History integrity rules

**`history-hash-chain-broken`** — a History stream's hash chain fails verification
- Severity: critical
- Action: alert always, queue-for-review (cannot be auto-resolved)

---

## Rule definition

Built-in rules ship with the engine. Custom rules are TOML:

```toml
# /etc/nakli-hub/anomaly-rules.d/my-rule.toml

[rule]
id = "my-bahi-night-write"
severity = "medium"
description = "Writes to bahi namespace between 2am-5am"

[rule.match]
namespace = "bahi"
operation = "vault:write"
time_window = { start = "02:00", end = "05:00", timezone = "Asia/Kolkata" }

[rule.threshold]
min_ops = 1   # any operation matching is enough

[rule.action]
on_fire = ["alert"]
cooldown_minutes = 60   # don't re-alert for an hour
```

The rule language is intentionally simple:
- A `match` clause filters events
- A `threshold` defines when to fire (e.g., `min_ops`, `min_distinct_destinations`, `min_failures`)
- An `action` lists what to do
- `cooldown` prevents alert spam

Power users wanting more expressiveness can implement custom detectors as Go plugins (post-v1.x; not in v1.x scope).

---

## Actions

When a rule fires, one or more actions execute:

### `alert`
Append a detection event to `fabric.detections` History. The operator's tools subscribe to this stream and surface alerts. Optional: integrate with external notification (webhook, email — but the fabric does not bundle these; operator configures via a Bridge call from a tool subscribing to the detection stream).

### `throttle`
Inject a transient `rate <= 1 per second` caveat-like check on the principal for a configurable duration. NOT done by modifying Grants (which are immutable); done by the transport's rate-limiting layer recognizing the throttle state.

Implementation: Hub maintains a `principal_throttles` table; on Grant verification, if the principal is throttled, additional checks apply.

The throttle is a temporary brake while a human reviews; it auto-expires (default 60 minutes) unless extended.

### `revoke`
Revoke the offending Grant via the standard `/fabric/v1/grant/revoke` endpoint. Strong action; off by default; opt-in per rule.

Example use: "If an agent's behavior suddenly looks compromised AND the agent's principal_type is agent AND the Grant's expiration is more than 24h away, revoke immediately."

### `queue-for-review`
Pause the principal's writes — queue them in the operation queue with a special "awaiting review" state. The operator can release or cancel via CLI.

Implementation: similar to `throttle` but stops operations entirely rather than slowing them.

### `log-only`
Emit the detection event but take no other action. Useful for low-severity rules where the operator just wants to know.

---

## Operator surface

### CLI

```
nakli-cli anomaly status
```

```
Anomaly detection: enabled
Rules: 15 built-in, 3 custom (loaded from /etc/nakli-hub/anomaly-rules.d/)

Recent detections (last 24h):
  TIME           SEV   RULE                       PRINCIPAL                 SUMMARY
  2026-05-15...  MED   burst-by-principal         agent claude-code-tax     30x normal rate
  2026-05-15...  HIGH  new-device-enrollment      —                         New device 'iPad-2' enrolled
  2026-05-15...  LOW   new-endpoint-by-principal  agent backup-runner       First /sync/peers call

Active throttles:
  PRINCIPAL              UNTIL                  RULE
  agent claude-code-tax  2026-05-15 17:32 UTC   burst-by-principal
```

```
nakli-cli anomaly detections --since 7d
nakli-cli anomaly detections --principal <id>
nakli-cli anomaly detection-detail <detection-id>
nakli-cli anomaly throttle release <principal-id>
nakli-cli anomaly rules list
nakli-cli anomaly rules disable <rule-id>
nakli-cli anomaly rules tune <rule-id> --threshold ...
```

### Tool integration

A tool can subscribe to `fabric.detections` (with appropriate Grant) and surface alerts to the user. Saanjha (shared list) could show a banner if its own writes triggered a detection. A dedicated "security center" tool could show all detections across the fabric.

For v1.x: the CLI is the primary surface. Dedicated UI tooling comes later.

---

## Configuration

```toml
# nakli-hub config additions

[anomaly]
enabled = true
rules_dir = "/etc/nakli-hub/anomaly-rules.d/"
detections_stream = "fabric.detections"

# Whether built-in rules are enabled by default
builtin_rules_enabled = true

# Default action overrides (per rule_id)
[anomaly.rule_overrides.burst-by-principal]
on_fire = ["alert", "throttle"]
throttle_minutes = 30

[anomaly.rule_overrides.new-device-enrollment]
on_fire = ["alert"]
# Can NOT be set to log-only (this is a force-alert rule by design)

# Stats retention
[anomaly.retention]
operation_stats_days = 90
detection_events_days = 365
```

### Force-alert rules

Some rules cannot be silenced. The operator can tune thresholds but the rule itself cannot be disabled:
- `new-device-enrollment`
- `agent-provisioned`
- `agent-after-retirement`
- `history-hash-chain-broken`
- `grant-minted` with broad scope (`*` namespace)

These are events the operator MUST know about. Allowing them to be silenced creates a stealth-mode hole.

The operator can configure WHERE the alert goes (which History stream, which webhook), but not whether it fires.

---

## Privacy and security

### What the engine sees
- Endpoint paths, principal IDs, grant IDs, timestamps, IPs, durations
- For Bridge calls: adapter name, operation name, destination domain (extracted by adapter; not full URL)
- For LLM calls: route, token counts, cost
- For Vault/History: namespace, stream_id, event_id, kind (NOT payload contents)

### What the engine does NOT see
- Event payload content (it's encrypted; the Hub doesn't have keys)
- Bridge call params content (adapter may pass to engine via hashed signature, not raw)
- LLM call content (only metrics)

### Where detections live
- `fabric.detections` History stream — encrypted like any History stream
- Accessible to consumers with the right Grant (typically operator only)

### Anti-tampering
- Detection events are History (hash-chained); tampering breaks the chain
- The Hub signs each detection event with its hub-identity key; consumers can verify
- Disabling anomaly detection requires editing config + restart; both are auditable

---

## Performance

The engine adds:
- One write to operation_stats per request (~1ms additional)
- Periodic rule evaluation (every N seconds; configurable; default 5s)
- One write to detection stream per fired rule (rare)

For typical loads (< 1000 ops/minute), overhead is negligible. For very high loads (> 10k ops/minute), the engine may need its own SQLite database (separate from the main fabric.db) — configurable.

---

## Testing

For unit tests of rules:
```go
// Test that burst-by-principal fires when ops exceed 10x baseline
func TestBurstByPrincipal(t *testing.T) {
    e := NewEngine(testConfig)
    // Establish baseline: 100 days of 5 ops/minute
    e.SimulateBaseline("principal-1", 100*24*60, 5)
    
    // Suddenly 100 ops/minute
    for i := 0; i < 100; i++ {
        e.OnOperation(newOp("principal-1"))
    }
    
    detections := e.Evaluate()
    require.Len(t, detections, 1)
    require.Equal(t, "burst-by-principal", detections[0].RuleID)
}
```

For integration tests: simulate operation streams against a Hub with the engine enabled; verify detection events appear.

---

## What we explicitly defer

- **ML-based detection.** The argument is straightforward: until we have rules running against real production data and a clear catalog of "things we missed," we don't know what model class to train. Premature ML adds complexity without proven value. Start simple.
- **Cross-fabric correlation.** Requires federation. Once federation is live, an operator may want "alert if any of my federated fabrics flags an agent as anomalous." v2 thought.
- **External threat feeds.** Bringing in third-party "this IP is bad" lists trades sovereignty for convenience. Out of scope for this design's values.
- **User-facing "security score" or dashboard.** Useful eventually; not v1.x. CLI suffices.
- **Auto-rotation of compromised Grants.** Combines anomaly detection with key rotation; both are individually complex; combining is post-v1.x.
- **Real-time stream of detections to a SIEM.** Some operators want this; the `fabric.detections` History stream can be subscribed to with a Bridge adapter writing to the SIEM; not built-in.

---

## Forward compatibility

The `fabric.detections` stream is a History stream (per the protocol). Adding new rule_ids over time is backward compatible — consumers handling detections should gracefully ignore unknown rule_ids.

The rule TOML format may evolve; we version it (`rule.version = "1"`) so the engine can warn on unsupported features without crashing.

---

## References

- Fabric protocol: `fabric-spec-001-v1.0.md`
- Hub spec: `hub-spec-001-v1.0.md` (operation_log table)
- D-Agents (anomaly detection on agent operations as v1.x feature)
- Hook 5 (graceful degradation surface)
- History primitive (where detection events live)
