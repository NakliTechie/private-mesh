# STATUS

Running milestone log for Phase 1 of the NakliTechie Private Mesh. Most-recent entry on top. Each milestone gets one paragraph: date, what landed, gate artifact, what's next.

---

## 2026-05-16 — M1 protocol building blocks complete

Crypto + types layer landed in both SDKs. `fabric-sdk-go` exports `crypto/` (XChaCha20-Poly1305 AEAD via `chacha20poly1305.NewX`, HKDF-SHA256, Argon2id with the spec's defaults t=3 m=65536 p=4), `identity/` (FIF envelope parse/unlock/serialize with on-wire header bytes as AEAD AAD and `fif_envelope_unsupported` refusal for reserved envelope types — the v1.0 forward-compat hook), and `grant/` (macaroon mint/parse/verify wrapping `gopkg.in/macaroon.v2`). `fabric-sdk-js` ships the equivalent in `src/` using `@noble/ciphers` (xchacha20poly1305), `hash-wasm` (Argon2id), WebCrypto (HKDF), and the `macaroon` npm package. 30 Go tests + 29 JS tests across the three packages, plus cross-SDK round-trip — `scripts/m1-interop.sh` runs four phases (Go generates → JS generates → JS verifies Go → Go verifies JS) and prints `M1 interop: OK`. Decisions documented: Go module path is `github.com/NakliTechie/private-mesh/fabric-sdk-go` (monorepo subdir; spec's standalone path deferred to release-time vanity setup); JS package name is `@naklitechie/fabric-sdk` per spec. **Gate:** unit tests pass on both sides and cross-SDK FIF + macaroon interop is green. **Next:** M2 — Hub binary: SQLite schema, protocol HTTP handlers, macaroon middleware, idempotency middleware, operation log, pairing. Estimate: 3–5 sessions.

---

## 2026-05-16 — M0 skeleton complete

Monorepo scaffolded against the v1.2 agent handoff. Vision, decisions, and the full spec set committed under `docs/` (vision + decisions at `docs/`; all 17 specs and 2 surveys at `docs/specs/`; rendered specs HTML at `docs/specs-v1.html`; empty `docs/archive/` reserved for superseded specs). All 8 subdirectories created with placeholder `README.md` and an executable `smoke.sh` that prints `OK: <subdir> (M0 skeleton)`. Root meta files in place: `README.md`, `ARCHITECTURE.md` (with ASCII layer map), `CONTRIBUTING.md` (codifying the milestone-gate workflow and the hard NOTs from the handoff), `LICENSE` (Apache-2.0), `.gitignore`. Scripts wired up: `scripts/build-all.sh` (gate artifact), `scripts/test-conformance.sh` (M3 placeholder), `scripts/release.sh` (M9 placeholder). CI scaffolded under `.github/workflows/` — one per subdirectory plus a `build-all.yml` aggregate. **Gate artifact:** `./scripts/build-all.sh` runs all 8 subdirectory smoke tests and prints `build-all: OK (8 subdirectories)`. **Next:** M1 — protocol building blocks (crypto + types layer in `fabric-sdk-go` and `fabric-sdk-js`, with cross-SDK FIF and macaroon interop as the gate). Estimate: 2–3 sessions.

---
