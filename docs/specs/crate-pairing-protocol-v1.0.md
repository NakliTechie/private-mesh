# Crate Pairing Protocol v1.0

**Status:** Spec
**Scope:** Cross-surface contract. Binding on both `NakliTechie/crate` (browser) and `NakliTechie/crate-agent` (daemon).
**Position in fabric:** Sits above `fabric-spec-001` (Sync primitive). Defines how a non-browser client obtains long-lived access to a Crate's transport without ever seeing bucket credentials.
**Licence:** This document is licensed CC BY-SA 4.0.

---

## Purpose

The Crate browser holds an Identity, bucket credentials, and an encryption passphrase. A daemon (crate-agent) needs *some* of these to sync files, but should not have *all* of them. Specifically:

- Daemon MUST have: a way to authenticate to a transport on behalf of the user's Identity
- Daemon MUST have: the user's encryption passphrase (or a key derived from it), to encrypt files locally before upload and decrypt on download
- Daemon MUST NOT have: bucket access keys / secret keys
- Daemon MUST NOT have: full Identity private key (it gets a delegated capability instead)

The pairing protocol bridges this gap. The browser issues a one-shot pairing token. The daemon redeems it against the transport. The transport returns a long-lived capability scoped to the user's Identity that lets the daemon participate in sync without ever touching bucket credentials.

## Parties

Three:

1. **Browser** — the existing Crate surface at `crate.naklitechie.com` (or a self-hosted equivalent). Holds bucket credentials, Identity private key, encryption passphrase.
2. **Daemon** — the crate-agent binary running on the user's machine. After pairing: holds Identity-delegated capability, passphrase-derived encryption key.
3. **Transport** — a `nakli-cf-worker` (Cloudflare Worker) or `nakli-hub` (self-hosted Go binary) that the user has either deployed themselves (BYOK / self-hosted), or which NakliTechie operates (managed-bucket tier, v1.1+). The transport holds bucket credentials and brokers between client surfaces and R2.

The transport is the security boundary. Compromising the daemon binary on disk gives an attacker the user's daemon-capability but not bucket credentials. Compromising the transport gives bucket access but not the encryption passphrase, so contents remain inaccessible.

## Wire format: the pairing token

The token is opaque to the user — they copy it from browser to daemon as one string. Internally it has structure.

### Encoding

```
CRATE-PAIR-{base64url(JSON payload)}
```

- Literal prefix `CRATE-PAIR-` to make tokens human-recognisable (helps with troubleshooting)
- `base64url` is RFC 4648 base64 with URL-safe alphabet, no padding
- The payload is UTF-8 JSON

### Payload schema

```json
{
  "v": 1,
  "type": "crate.pairing.token",
  "secret": "base64url:32-bytes-random",
  "transport_endpoint": "https://my-account.workers.dev",
  "transport_type": "cf-worker",
  "bucket_id": "01H...",
  "identity_pubkey": "base64url:ed25519-pubkey",
  "issued_at": 1747572345,
  "expires_at": 1747573245
}
```

Field semantics:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `v` | int | yes | Protocol version. Must be `1` for v1.0. |
| `type` | string | yes | Always `"crate.pairing.token"`. Future tokens of other shapes may exist; daemon rejects unrecognised types. |
| `secret` | string | yes | 32 cryptographically random bytes, base64url. The redemption secret. |
| `transport_endpoint` | string | yes | HTTPS URL where the daemon redeems the token. Must include scheme. No path component. |
| `transport_type` | string | yes | One of `cf-worker`, `hub`, `managed`. Daemon uses for diagnostic messages. |
| `bucket_id` | string | yes | Opaque identifier the transport uses to find the right bucket. NOT a bucket name or AWS reference — an internal ID assigned by the transport at bucket-provision time. |
| `identity_pubkey` | string | yes | The user's Identity public key, base64url-encoded. Daemon uses to verify subsequent transport messages claiming this Identity. |
| `issued_at` | int | yes | Unix timestamp (seconds) when browser issued the token. |
| `expires_at` | int | yes | Unix timestamp (seconds) after which the token MUST NOT be redeemed. v1.0: issuance + 900 seconds (15 minutes). |

Tokens are not signed — they don't need to be. The `secret` is the authenticator; possession of the secret IS the authorisation to redeem. The transport verifies the secret against its own record.

### Size

A typical token is ~300-400 characters after base64url encoding. Fits in a single line, displayable in a terminal, copy-pasteable on mobile.

## Phase 1: Browser issues the token

Trigger: user clicks "Pair an agent" in browser Crate Settings → Devices.

Steps:

1. **Browser generates `secret`.** 32 bytes from `crypto.getRandomValues()`. Encoded base64url.

2. **Browser constructs the payload** with all required fields. `issued_at` = `Math.floor(Date.now() / 1000)`. `expires_at` = `issued_at + 900`.

3. **Browser POSTs the payload to the transport**, authenticated by the user's Identity. The transport stores the payload keyed by `secret`, with TTL = `expires_at - now`.

   Request:
   ```
   POST {transport_endpoint}/v1/pairing/intent
   Authorization: Identity {signed-request-from-browser}
   Content-Type: application/json
   
   { ...payload... }
   ```
   
   Response: `201 Created`, empty body. Or an error per "Error codes" below.

4. **Browser encodes the token** as `CRATE-PAIR-{base64url(JSON)}` and displays to the user with a copy button, a QR code (containing the same string), and a countdown showing time until expiry.

The browser MUST display:
- The token
- The expiry time ("Valid for 15:00")
- A clear "Cancel" button that revokes the pairing intent (POST `/v1/pairing/intent/cancel` with the secret)
- A clear "Generate new token" option after expiry

The browser MUST NOT:
- Auto-renew tokens silently (each token must be deliberately requested by the user)
- Display the token in any log or persistent storage
- Cache the token in localStorage / IndexedDB

## Phase 2: User transfers the token

Out of band. User copies the `CRATE-PAIR-...` string from the browser and pastes it into the terminal where the daemon is prompting.

The protocol does not specify the transfer mechanism. QR code, copy-paste, manual typing — all valid. The token's expiry bounds the window for transfer.

## Phase 3: Daemon redeems the token

Trigger: user runs `crate-agent pair`, daemon prompts `Paste pairing token:`, user pastes.

Steps:

1. **Daemon decodes the token.**
   - Strip `CRATE-PAIR-` prefix. If absent → error `E_TOKEN_FORMAT`.
   - Base64url-decode the remainder. If decoding fails → error `E_TOKEN_FORMAT`.
   - JSON-parse the decoded bytes. If parsing fails → error `E_TOKEN_FORMAT`.
   - Validate schema: required fields present, types correct, `v == 1`, `type == "crate.pairing.token"`. If invalid → error `E_TOKEN_FORMAT`.
   - Check `expires_at > now`. If not → error `E_TOKEN_EXPIRED`. (Daemon shows when it expired and suggests generating a new one.)

2. **Daemon generates its own ephemeral keypair.** Ed25519 keypair. This becomes the daemon's Identity for transport authentication. Private key is held in memory until step 5.

3. **Daemon prompts for the folder passphrase.** Used in step 4 and persisted (as a derived key) for file encryption operations.

4. **Daemon POSTs to the transport's redeem endpoint:**

   ```
   POST {transport_endpoint}/v1/pairing/redeem
   Content-Type: application/json
   
   {
     "v": 1,
     "secret": "{from token}",
     "daemon_pubkey": "{daemon's ephemeral ed25519 pubkey, base64url}",
     "daemon_fingerprint": {
       "platform": "darwin",
       "arch": "arm64",
       "hostname": "user-laptop",
       "agent_version": "1.0.0"
     }
   }
   ```
   
   Note: passphrase is NOT sent to the transport. Passphrase only goes to the local key derivation; transport never sees it.

5. **Transport verifies and responds.**

   Transport-side logic:
   - Look up the pairing intent by `secret`. If not found → `404` with `E_TOKEN_NOT_FOUND`.
   - Check if already redeemed. If yes → `409` with `E_TOKEN_ALREADY_REDEEMED`.
   - Check expiry. If expired → `410` with `E_TOKEN_EXPIRED`.
   - Mark redeemed (atomic — must prevent race conditions where two daemons redeem the same token simultaneously).
   - Issue a long-lived `daemon_capability`:
     - JWT-shaped, signed by the transport's key (or whatever the transport's standard capability format is — defined by `fabric-spec-001`)
     - Claims: `sub` = user's Identity pubkey, `aud` = transport endpoint, `act` = daemon_pubkey, `iat` = now, `exp` = now + 1 year (capability TTL is independent of pairing token TTL)
     - Scope: `crate:read crate:write` on this user's bucket
   - Notify the browser (via Sync primitive event) that a daemon has paired, including daemon fingerprint
   
   Response: `200 OK`
   ```json
   {
     "v": 1,
     "capability": "{JWT or transport-native capability token}",
     "bucket_reference": "{opaque bucket ID, same as in token}",
     "transport_pubkey": "{base64url ed25519 pubkey of transport}",
     "expires_at": 1779108345
   }
   ```

6. **Daemon receives the capability, writes config.**
   - Derives master encryption key from passphrase: `PBKDF2-SHA256(passphrase, salt, 600_000 iterations)` — same as browser. Salt is fetched from the bucket's `.crate/crate.json` (via the transport, using the freshly-issued capability) so daemon and browser derive the same key.
   - Encrypts the daemon's ephemeral private key + the capability under the master key.
   - Writes config TOML to `~/.config/nakli/crate-agent.toml` (or platform equivalent).
   - Writes identity key file to `~/.config/nakli/identity.key`, mode `0600`.
   - Verifies setup with a transport ping (authenticated by the capability).
   - Runs `crate-agent doctor` automatically as final sanity check.
   - Prints success summary and exits.

The daemon MUST clear the passphrase from memory after deriving the master key. The master key is held only for the duration of the daemon's runtime.

## Capability lifecycle

The capability issued in Phase 3 is the daemon's long-lived credential. Key properties:

- **TTL:** 1 year from issuance, MAY be shorter at transport's discretion.
- **Renewal:** Daemon SHOULD refresh proactively when 80% of TTL has elapsed by calling `/v1/capability/refresh` with the current capability. Transport returns a fresh one. No new pairing token required.
- **Revocation:** User can revoke from browser Crate at any time via Settings → Devices → Revoke. Browser sends `DELETE /v1/capability/{capability_id}` authenticated by user's Identity. Transport invalidates immediately. Next daemon API call returns `401 E_CAPABILITY_REVOKED`. Daemon logs warning, halts sync, requires re-pairing.
- **Audit trail:** Transport logs every capability use with daemon fingerprint. Browser Settings → Devices shows last-seen time and recent activity per paired device.

## Notification to other devices

When pairing succeeds (Phase 3 step 5), the transport emits a Sync primitive event of type `crate.device.paired`:

```json
{
  "event_type": "crate.device.paired",
  "ts": 1747572345,
  "device": {
    "kind": "daemon",
    "fingerprint": { "platform": "darwin", "arch": "arm64", "hostname": "user-laptop", "agent_version": "1.0.0" },
    "capability_id": "01H..."
  }
}
```

Other devices (browser tabs, other paired daemons) receive this via their own Sync subscription. Browser surfaces it as a notification: "New device paired: crate-agent on user-laptop (macOS arm64). Revoke if you don't recognise this."

This is the user's primary security signal. Browser MUST make this visible — non-dismissable banner until acknowledged, with a one-click revoke button.

## Error codes

Errors are returned as HTTP responses with a JSON body:

```json
{ "error": "E_TOKEN_EXPIRED", "message": "Pairing token expired 47 seconds ago. Generate a new one from your browser." }
```

| Code | HTTP | Meaning | User-facing recovery |
|---|---|---|---|
| `E_TOKEN_FORMAT` | 400 | Token couldn't be decoded or doesn't match schema | "Token looks malformed. Make sure you copied the full string including the `CRATE-PAIR-` prefix." |
| `E_TOKEN_EXPIRED` | 410 | Token's `expires_at` has passed | "Token expired. Generate a new one from your browser." |
| `E_TOKEN_NOT_FOUND` | 404 | Transport has no pairing intent matching the secret | "Token isn't recognised. Was it generated against a different transport?" |
| `E_TOKEN_ALREADY_REDEEMED` | 409 | Token was redeemed previously | "Token already used. Tokens are single-use. Generate a new one." |
| `E_TRANSPORT_UNREACHABLE` | n/a (network error) | Daemon couldn't reach `transport_endpoint` | "Couldn't reach the transport. Check the URL and your network connection." |
| `E_CAPABILITY_REVOKED` | 401 | Daemon's capability was revoked by user | "Access was revoked from another device. Re-pair to continue." |
| `E_CAPABILITY_EXPIRED` | 401 | Capability TTL elapsed without refresh | "Capability expired. Re-pair to continue." (Should not happen if daemon refreshes properly.) |
| `E_PROTOCOL_VERSION` | 426 | Token's `v` field is unsupported by this daemon | "Pairing protocol version mismatch. Update crate-agent." |

Daemons MUST print the user-facing recovery message on stderr, not just the code. Browsers MUST surface revocations as in-app notifications, not silent failures.

## Security properties

### What an attacker can do if they steal a token mid-transit (before redemption)

- Redeem the token themselves and gain a daemon capability for the user's bucket
- Read and write files (subject to encryption — they don't have the passphrase)
- The user sees the pairing notification in their browser and can revoke immediately

Mitigation: tokens expire in 15 minutes. User SHOULD revoke the pairing intent (browser "Cancel" button) if the transfer is interrupted. User SHOULD watch for unexpected pairing notifications.

### What an attacker can do if they steal a daemon's binary + state from disk

- Use the embedded capability to call the transport (read/write encrypted blobs)
- Cannot decrypt anything without the passphrase (which isn't on disk)
- User can revoke the capability from any browser, invalidating the stolen state

This is the central security argument for the pairing-token model. Compare with a hypothetical alternative where the daemon held S3 credentials directly: stolen state → permanent direct R2 access until the user remembers to rotate keys. With pairing tokens, revocation is one click in the browser.

### What an attacker can do if they compromise the transport

- See all encrypted blobs flowing through (already encrypted; useless without passphrases)
- Issue capabilities for new daemons (but pairing intents originate from authenticated browser sessions, so transport can't unilaterally create them)
- Deny service (transport availability)

The transport is trusted to enforce capability scoping and to honour revocations. It is NOT trusted with data confidentiality — that's the encryption's job.

### What an attacker can do if they observe the passphrase being typed into the daemon

- Compute the master encryption key
- Read any file they can also fetch via a compromised transport or stolen bucket credentials

Mitigation: the passphrase is the weakest link, same as in the browser. Use a password manager. Don't paste passphrases into untrusted terminals.

## Test vectors

To facilitate cross-implementation testing, the following are well-formed tokens (NOT for production use):

### Valid v1 token

```
Secret: hPmI8VvL3RfMnXq0AjBgWtZpDnUrEkSc (random)
Payload:
{
  "v": 1,
  "type": "crate.pairing.token",
  "secret": "hPmI8VvL3RfMnXq0AjBgWtZpDnUrEkSc",
  "transport_endpoint": "https://transport.example.com",
  "transport_type": "cf-worker",
  "bucket_id": "01HXT9DSACJ5RZQXM",
  "identity_pubkey": "GjK8mNpQvR2WxZyA4BcD6EfH8JkLnMpQ",
  "issued_at": 1747572345,
  "expires_at": 1747573245
}
Token: CRATE-PAIR-eyJ2IjoxLCJ0eXBlIjoiY3JhdGUucGFpcmluZy50b2tlbiIsInNlY3JldCI6ImhQbUk4VnZMM1JmTW5YcTBBakJnV3RacERuVXJFa1NjIiwidHJhbnNwb3J0X2VuZHBvaW50IjoiaHR0cHM6Ly90cmFuc3BvcnQuZXhhbXBsZS5jb20iLCJ0cmFuc3BvcnRfdHlwZSI6ImNmLXdvcmtlciIsImJ1Y2tldF9pZCI6IjAxSFhUOURTQUNKNVJaUVhNIiwiaWRlbnRpdHlfcHVia2V5IjoiR2pLOG1OcFF2UjJXeFp5QTRCY0Q2RWZIOEprTG5NcFEiLCJpc3N1ZWRfYXQiOjE3NDc1NzIzNDUsImV4cGlyZXNfYXQiOjE3NDc1NzMyNDV9
```

### Invalid: malformed JSON

```
Token: CRATE-PAIR-aGVsbG8gd29ybGQ=
(decodes to "hello world", not JSON)
Expected daemon behaviour: error E_TOKEN_FORMAT
```

### Invalid: wrong version

```
Payload: { "v": 99, "type": "crate.pairing.token", ... }
Expected daemon behaviour: error E_PROTOCOL_VERSION
```

### Invalid: expired

```
Payload: { ..., "issued_at": 1747500000, "expires_at": 1747500900 }
(both timestamps in the past)
Expected daemon behaviour: error E_TOKEN_EXPIRED
```

A `test-vectors.json` file MUST live in `NakliTechie/private-mesh/docs/test-vectors/crate-pairing/` with these and additional cases, so both browser and daemon implementations can verify behaviour.

## Forward compatibility

This is v1 of the protocol. Future versions should:

- Bump `v` in the token payload (currently `1`)
- Daemons checking `v` MUST reject unknown versions with `E_PROTOCOL_VERSION`, not silently downgrade
- Browsers MAY issue v1 tokens for some time after v2 ships, for compatibility with old daemons
- The transport endpoint paths SHOULD remain stable (`/v1/pairing/intent`, `/v1/pairing/redeem`) and version bumps SHOULD use new path prefixes (`/v2/...`) rather than redefining behaviour at existing paths

Backward incompatible changes that justify v2:
- Adding required fields the daemon must understand
- Changing the authentication scheme for transport requests
- Changing the capability format in a way old daemons can't consume

Non-breaking changes that don't require version bump:
- Adding optional fields the daemon can ignore
- Adding new error codes (daemons should handle unknown codes gracefully — show the raw message)
- Tightening security checks on the transport side that don't change wire format

## What's NOT in v1

Deliberately omitted, deferred to later versions:

- Multi-folder pairing (one token paired to one Crate; daemon needs separate pairing per folder). v1.3 (multi-folder daemon).
- Capability-chain delegation (daemon delegates to another daemon). Not in roadmap.
- Out-of-band token transfer (BLE, NFC). v2+.
- Token rotation without re-pairing. v1.1 (capability refresh covers most of this).
- Mutual TLS as an alternative to JWT-style capabilities. v2+.

## References

- `fabric-spec-001-v1.0.md` in `NakliTechie/private-mesh` — the Sync primitive's wire protocol, which this protocol builds on
- `crate-vision-and-roadmap-v1.0.md` — product context
- `crate-browser-handoff-v1.0.md` — browser-side implementation requirements (Phase 1)
- `crate-daemon-handoff-v1.0.md` — daemon-side implementation requirements (Phase 3)
- RFC 4648 — base64url encoding
- RFC 7519 — JWT (one option for capability format; transport-native is also acceptable)
- Ed25519 / RFC 8032 — signature scheme for Identity keys

---

*This document is licensed CC BY-SA 4.0.*
