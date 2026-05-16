# Distributed FIF Envelopes Specification

**Document:** `fif-envelopes-spec-001-v1.x.md`
**Status:** v1.x draft (post-v1.0 ship; v1.0 reserves envelope_type strings)
**Companion to:** `fabric-spec-001-v1.0.md` (FIF format section)
**Audience:** Implementers of post-v1.0 FIF envelope types; users considering distributed key custody.

The Fabric Identity File (FIF) is the user's root of trust. v1.0 supports a single envelope type: `passphrase-only`. Lose the passphrase, lose access. This spec extends the FIF envelope model with three additional types that distribute trust across multiple custodians:

- `shamir-shares` — threshold secret sharing across N shares (K required)
- `device-quorum` — threshold across N device subkeys (K required)
- `social-recovery` — shares held by trusted humans with explicit recovery flow

The v1.0 protocol RESERVED these envelope_type strings; v1.x adds them. The outer envelope envelope-header structure is unchanged; only the `envelope_type` and `envelope_params` fields differ.

---

## Scope

This document specifies:
- Each envelope type's cryptographic construction
- The protocol/SDK operations for setup, unlock, and recovery
- Migration paths between envelope types
- UX considerations (the cryptography is hard; the UX is harder)
- Threat models and failure modes
- Conformance test vectors

Out of scope:
- v1.0 `passphrase-only` (already specified in `fabric-spec-001-v1.0.md`)
- Hardware-backed key storage (TPM, Secure Enclave, YubiKey) — separate concern; can compose with these envelopes via `device-quorum`
- Quantum-resistant variants — when post-quantum primitives are standardized, all envelope types will rotate; not specified here

---

## Shared structure

All distributed envelopes use the same outer shape (per `fabric-spec-001-v1.0.md`):

```
fabric-identity-file := envelope-header || envelope-body || envelope-mac
```

Where:
- `envelope-header` is JSON containing `envelope_type`, `envelope_params`, and version info
- `envelope-body` is the encrypted inner FIF (XChaCha20-Poly1305)
- `envelope-mac` is the AEAD authentication tag

What differs across envelope types is **how the encryption key is derived**:

- `passphrase-only`: KDF(passphrase + salt) → key
- `shamir-shares`: combine K-of-N shares → recovered secret → KDF → key
- `device-quorum`: combine signatures from K-of-N devices → derived secret → key
- `social-recovery`: combine K-of-N share blobs (each held by a recoverer) → secret → key

The inner FIF format is identical across envelope types. The unlock operation produces the same in-memory structure regardless of how the key was reconstructed.

---

## Envelope type: `shamir-shares`

### Cryptographic model

Classical Shamir Secret Sharing. The FIF encryption key is split into N shares using a polynomial of degree K-1 over GF(2^256). Any K shares reconstruct the secret; K-1 or fewer reveal nothing.

```
envelope_params = {
  "scheme": "shamir/v1",
  "threshold_k": 3,
  "total_n": 5,
  "share_format": "base32-with-crc",
  "kdf": "argon2id",
  "kdf_params": { ... },
  "salt": "<base64>",
  "share_metadata": [
    { "share_index": 1, "hint": "Phone backup", "created_at": "..." },
    { "share_index": 2, "hint": "Safe deposit box", "created_at": "..." },
    { "share_index": 3, "hint": "Spouse", "created_at": "..." },
    { "share_index": 4, "hint": "Office colleague", "created_at": "..." },
    { "share_index": 5, "hint": "Cloud backup, encrypted", "created_at": "..." }
  ]
}
```

The shares themselves are NOT stored in the envelope (that would defeat the purpose). They are distributed out-of-band at setup time. The envelope only contains share metadata (which exists, hints to help the user recover).

### Setup flow

```
nakli-cli identity envelope-set shamir --threshold 3 --total 5

Setup will generate 5 shares; any 3 will recover your identity.

Where should each share go?
  Share 1: [Phone backup]
  Share 2: [Safe deposit box]
  Share 3: [Spouse]
  Share 4: [Office colleague]
  Share 5: [Cloud backup, encrypted with separate passphrase]

[Generating shares...]

Share 1 (Phone backup):
  Index: 1
  Value: SHAMIR-NK1-AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-...
  Write this to: ./share-1-phone.txt
  Hand to: yourself / save to phone

Share 2 (Safe deposit box):
  Index: 2
  Value: SHAMIR-NK1-XXXX-YYYY-...
  Write this to: ./share-2-safedep.txt
  Hand to: yourself / print and store in safe deposit

... (etc)

Recovery: any 3 of the 5 shares will reconstruct your identity.
Loss of up to 2 shares: still recoverable.
Loss of 3 or more shares: identity is unrecoverable.

PRINT THIS PAGE AND STORE WITH YOUR LEGAL DOCUMENTS:
  Threshold: 3 of 5
  Share locations: [hints]
  Recovery instructions: ...
```

The CLI generates the shares, displays them once, never stores them. The user is responsible for distributing them.

### Unlock flow

```
nakli-cli identity unlock --envelope shamir

Recovery requires 3 of 5 shares.

Enter share 1 (or path to file):
  > /Users/bhai/share-1-phone.txt
  ✓ Share 1 read (index 1)

Enter share 2:
  > SHAMIR-NK1-AAAA-...
  ✓ Share 2 read (index 4)

Enter share 3:
  > SHAMIR-NK1-XXXX-...
  ✓ Share 3 read (index 5)

[Reconstructing secret...]
[Decrypting FIF...]
✓ Identity unlocked.
```

Shares can be entered in any order. The CLI infers the share index from the share format itself.

### Failure modes

- **K-1 or fewer shares available:** unrecoverable. The CLI surfaces this clearly.
- **One share is corrupted:** the share has a CRC; corruption is detected; the share is rejected; user must provide another.
- **One share is wrong/forged:** if the forged share is incorporated, Shamir produces a wrong secret; FIF decryption fails (AEAD MAC mismatch); CLI reports "wrong shares" and the user retries with different shares.
- **N+1 share generation attempt** (user wants to add a custodian): re-run the setup; old shares become invalid; user re-distributes. There is no "add one share" operation; threshold schemes don't generally support this without complexity. We accept this trade-off.

### Threat model

- Adversary holding K-1 shares: learns nothing about the secret (Shamir's perfect-secrecy property)
- Adversary holding K shares: can recover (this is the intended threshold; design accordingly)
- Adversary intercepting shares in transit: this is why each share has its own encryption when stored; users should encrypt share files at rest (not specified by the protocol)
- Adversary compromising the setup machine: can capture the secret directly; setup is on a trusted device

---

## Envelope type: `device-quorum`

### Cryptographic model

The user's enrolled devices each hold a partial key. To unlock the FIF, K-of-N devices must each sign a fresh challenge with their device subkey; the combined signatures derive the FIF encryption key.

This is conceptually similar to Shamir but the "shares" are dynamically reconstructed from device subkey signatures, not stored as static blobs.

```
envelope_params = {
  "scheme": "device-quorum/v1",
  "threshold_k": 2,
  "total_n": 3,
  "devices": [
    { "device_id": "01HMX...", "device_pubkey": "<base64>", "name": "M4 Pro" },
    { "device_id": "01HMY...", "device_pubkey": "<base64>", "name": "iPad" },
    { "device_id": "01HMZ...", "device_pubkey": "<base64>", "name": "iPhone" }
  ],
  "challenge_salt": "<base64>",
  "kdf": "hkdf-sha256"
}
```

### Setup flow

After the user has multiple devices enrolled (per the v1.0 pairing flow), they can rotate the FIF to a device-quorum envelope:

```
nakli-cli identity envelope-set device-quorum --threshold 2

You have 3 enrolled devices:
  - 01HMX... (M4 Pro)
  - 01HMY... (iPad)
  - 01HMZ... (iPhone)

Setting threshold to 2 means: any 2 of these 3 devices must be present to unlock.

Confirm? [y/N]: y

[Each device must approve this rotation. Sending challenges...]

Waiting for approval:
  M4 Pro:   ✓ approved
  iPad:     ⌛ waiting...
  iPhone:   ⌛ waiting...

[After approvals from a quorum: re-encrypts FIF under new envelope]
✓ Envelope rotated.
```

The user, when running the CLI on each device, sees a notification (in the system tray, or in `nakli-cli` if interactive) asking "approve envelope rotation? this device will become a quorum member."

### Unlock flow

```
nakli-cli identity unlock --envelope device-quorum

Quorum required: 2 of 3 devices.

This device (M4 Pro): ✓ ready to sign

Other devices needed:
  - iPad
  - iPhone

Use one of:
  - Press the approval button on a device that has the notification
  - Run 'nakli-cli identity quorum-sign --challenge <code>' on the device

Code: 4823-1907

Waiting... (timeout 60s)

  iPad: ✓ signed
  
Have 2 of 2 minimum signatures. Unlocking...
✓ Identity unlocked.
```

Communication between devices for the signing: via mDNS on local network (when on same network), or via the Hub's signaling endpoint (when separated). The challenge value is short-lived; signatures bind to it.

### Key derivation

Once K signatures are collected:
```
derived_secret = HKDF-SHA256(
  ikm = concat(sorted_signatures),
  salt = challenge_salt,
  info = "fif-device-quorum-v1",
  L = 32
)
```

The FIF encryption key is `derived_secret`. The signatures are not stored; they're collected fresh each unlock.

### Failure modes

- **Insufficient devices online:** Operation queues for retry; user is told "need K devices, only have J." If the user can't get to K devices, the FIF cannot be unlocked on this device. They must use another envelope type (e.g., fall back to passphrase or Shamir if one was set up as a recovery envelope).
- **Lost device permanently:** the device's subkey is removed from the quorum via `identity quorum-replace --remove <device-id> --add <new-device-id>` which requires K-1 other devices to sign the rotation.
- **All devices lost:** unrecoverable unless a recovery envelope (Shamir, social) was set up in parallel.

### Composing with hardware-backed keys

Device subkeys CAN be hardware-backed (Secure Enclave, TPM, YubiKey). The cryptography is unchanged; the device just signs via its hardware key instead of a software-stored private key. This composes naturally.

---

## Envelope type: `social-recovery`

### Cryptographic model

Similar to Shamir but the shares are explicitly given to trusted humans ("recoverers"). The recovery flow is multi-step with delays and verification, to prevent social-engineering attacks.

```
envelope_params = {
  "scheme": "social-recovery/v1",
  "threshold_k": 3,
  "total_n": 5,
  "recoverers": [
    {
      "recoverer_id": "<ulid>",
      "name": "Brother",
      "pubkey": "<base64>",       // recoverer's device pubkey, used to encrypt their share
      "contact_hint": "Same phone number for 10 years"
    },
    ...
  ],
  "recovery_delay_hours": 72,        // wait period between initiating and unlocking
  "kdf": "argon2id",
  "kdf_params": { ... },
  "salt": "<base64>"
}
```

Each recoverer holds an encrypted share, encrypted to their device pubkey. To recover:
1. The user initiates a recovery on a new device
2. The new device's pubkey is published as the "intended new device"
3. K recoverers each verify (out-of-band) that this recovery is legitimate, then encrypt their share to the new device's pubkey
4. A waiting period elapses (`recovery_delay_hours`)
5. The new device decrypts the encrypted shares with its private key
6. Combines them via Shamir-style reconstruction
7. Unlocks the FIF

The waiting period gives the original FIF holder time to notice and cancel if the recovery is unauthorized.

### Setup flow

```
nakli-cli identity envelope-set social-recovery --threshold 3

Set up social recovery. K-of-N humans you trust can help you recover.

Threshold: 3
Total: ? (5)

For each recoverer, you need their fabric public key:
  Recoverer 1 name: Brother
    Pubkey or file path: ./brother-pubkey.txt
    Hint to identify: "Same phone number for 10 years"
    ✓ Recorded
  
  Recoverer 2 name: Father
    ...

  ...

Recovery delay: 72 hours (recommended).

Now sharing encrypted shares with each recoverer...

  Brother: ✓ share sent (he will receive a "social-recovery-share" event in his fabric)
  Father:  ✓ share sent
  ...

PRINT THIS PAGE:
  Recovery threshold: 3 of 5
  Recoverer names: Brother, Father, ...
  Recovery delay: 72 hours
  Recovery instructions: nakli-cli identity recover --envelope social-recovery
```

The recoverers don't need to do anything at setup time — they receive a "you have been given a recovery share" notification in their fabric (which their fabric tools surface) and the share is stored encrypted in their FIF.

### Recovery flow

```
# On a new device, install nakli-cli and run:
nakli-cli identity recover --envelope social-recovery

Initiating recovery for principal 01HMXK...

This device's pubkey will be: <base64>

Out of band, contact your recoverers:
  - Brother
  - Father
  - Mother
  - Uncle
  - Lawyer

Each must run:
  nakli-cli identity recovery-approve \
      --principal 01HMXK... \
      --to-device-pubkey <base64> \
      --reason "I verified this is genuinely Bhai"

A waiting period of 72 hours begins after the first approval.

Recovery cancelable until expiration: nakli-cli identity recovery-cancel <id>

Waiting...

  Brother:  ✓ approved at 2026-05-15 18:00:00 (delay until 2026-05-18 18:00:00)
  Father:   ✓ approved at 2026-05-15 19:00:00
  Mother:   ✓ approved at 2026-05-15 19:15:00
  Have 3 of 3 minimum. Recovery scheduled for 2026-05-18 18:00:00.

[After delay elapses and not cancelled]

Recovery proceeding. Decrypting shares from recoverers...
✓ Recovery complete. Identity unlocked.

IMPORTANT: Notify your original devices that recovery occurred. The previous FIF
remains valid; you may want to revoke its envelope and re-issue from this device.
```

### Cancellation

If the original FIF holder notices an unauthorized recovery (perhaps because they get a notification on a still-functioning device):

```
nakli-cli identity recovery-cancel <recovery-id>
```

Cancellation writes to a History stream all recoverers consult; the recovery aborts.

### Failure modes

- **Recoverer is unreachable:** the user must approach a different recoverer, or fail to reach threshold
- **Recoverer is dishonest** (approves an unauthorized recovery): they're acting as an adversary; the 72-hour delay is the defense — the legitimate user has time to notice and cancel
- **The original device is destroyed AND the user can't get K recoverers** (e.g., all in different countries, no contact): unrecoverable. This is the inherent risk of social recovery.

### Threat model

- Adversary with K-1 recoverer compromises: can't recover
- Adversary with K recoverer compromises: can recover, but the 72-hour delay gives the legitimate user time to notice and cancel (if they have any functioning device)
- Adversary with K recoverers AND control over the legitimate user's notification surface: can recover undetected. Mitigations: multi-channel notification (email, SMS, in-app to other devices); recommend recoverers be people the user trusts not to collude.

---

## Migration between envelope types

### Rotating envelopes

```
nakli-cli identity envelope-set <new-type> [options]
```

Process:
1. Unlock FIF under current envelope (whatever it is)
2. Generate new envelope params per the chosen type
3. Re-encrypt the inner FIF under the new envelope
4. Atomically write the new FIF (write to temp, fsync, rename)
5. (If applicable) distribute new shares / quorum approvals

Rotation does NOT change the root keypair; it changes how the root keypair is protected at rest. Devices, agents, Grants, transports — all unchanged.

### Recovery envelope as a parallel envelope

Optionally, a FIF can have multiple envelopes (concentric, not parallel) — but the simpler model is: pick one envelope type, and have a separate recovery envelope wrapping the same root.

For maximum safety:
- Primary envelope: `passphrase-only` (daily use, easy)
- Recovery envelope: `social-recovery` (in case of passphrase loss)

The recovery envelope is a separate FIF file with the same inner content, encrypted under the recovery envelope type. Stored separately. The CLI can manage both via:

```
nakli-cli identity recovery-envelope add social-recovery --threshold 3 --total 5
nakli-cli identity recovery-envelope list
nakli-cli identity recovery-envelope use social-recovery   # recover using this envelope
```

Two FIF files, one logical identity. Common pattern.

---

## UX considerations

### Onboarding

`passphrase-only` is the default at FIF creation. The user can rotate to a distributed envelope later. Most users will never bother. That's fine.

For those who do:
- The CLI walks them through carefully
- It prints recovery instructions to a printable page
- It refuses to proceed if the user doesn't acknowledge the trade-offs
- It encourages a recovery envelope alongside the primary

### Hints and labels

Each distributed envelope's params include human-readable hints/labels for shares, devices, or recoverers. These are NOT secret (they're in the FIF). They help the user remember where each share lives. Example:
- "Phone backup" — share on phone
- "Spouse" — share given to spouse
- "Brother in Bangalore" — recoverer
- "iPad" — device in quorum

### Recovery instructions

The CLI generates a "recovery instructions" page meant to be printed and stored with legal documents. Includes:
- Principal ID
- Envelope type and threshold
- Locations of shares (per hints)
- Step-by-step recovery walkthrough
- Contact info for technical support (if applicable)
- A note about quantum-future migration (just so users aren't surprised in 10 years)

### Disaster simulation

The CLI offers `nakli-cli identity disaster-simulate`:
```
Simulate disaster recovery. (No actual changes; this is a dry run.)

Scenario: you've lost your main device.

What recovery options do you have?
  ✓ Social-recovery envelope (3 of 5 recoverers)
  ✓ Shamir backup (3 of 5 shares; you remember 2 share locations: phone, safe deposit)

What can you do?
  - Recover via social-recovery: contact 3 recoverers, wait 72 hours.
  - Recover via Shamir: locate at least one more share (you know 2 of 5 locations).

Do you have a printed recovery page? [y/N]
```

This is a peace-of-mind feature; runs entirely locally.

---

## Conformance and testing

Each envelope type ships with conformance test vectors:
- Known threshold + total + shares → known reconstruction
- Wrong shares → known failure
- Insufficient shares → known failure

The fabric-sdk-go and fabric-sdk-js both include the test vectors and verify their implementations match.

For social-recovery, the test vectors are limited (the human element is hard to test); the cryptographic core has vectors, the workflow has scripted integration tests.

---

## What stays the same as v1.0

- Inner FIF format
- Root keypair semantics
- Device subkey semantics
- Agent identity semantics
- Grant minting and verification
- Transport protocols
- Everything else

The envelope is just a different lock on the same box.

---

## What changes from v1.0 reservedness

In v1.0, `envelope_type` values `shamir-shares`, `device-quorum`, `social-recovery` were reserved. v1.0 readers MUST reject FIFs with these types ("envelope type not supported"). v1.x readers MUST support them.

A v1.x-created FIF using a distributed envelope IS NOT readable by v1.0 software. Users rotating must understand this: rotation breaks compatibility with v1.0 tools.

Mitigation: ship v1.x SDK upgrades to all consumer tools before users rotate envelopes. This is a coordinated migration; the CLI prompts during rotation: "Have you upgraded all your consumer tools to v1.x?"

---

## Out of scope

- Hardware Security Module integration (composes via device-quorum; not specified here)
- Multi-signature ECDSA / EdDSA aggregate signatures (would simplify device-quorum but adds dependency on niche libraries)
- Verifiable Secret Sharing (Feldman / Pedersen) — overkill for personal use; standard Shamir suffices
- Time-locked encryption (e.g., "FIF unlocks itself in 6 months if I don't sign in") — interesting but separate
- Recovery via biometrics — biometrics are not a secret; not suitable as a sole factor

---

## References

- Shamir, Adi (1979). "How to share a secret." Communications of the ACM.
- HKDF: RFC 5869
- Argon2id: RFC 9106
- XChaCha20-Poly1305: RFC 8439 + XChaCha extension
- v1.0 FIF format: `fabric-spec-001-v1.0.md` (envelope section)
- Decision D2 (FIF envelope, layered approach)
- Decision D-FIF (distributed FIF deferred to v1.x)
