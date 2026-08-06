# Bridge — Git Egress

*Required milestone on `nakli-local-bridge`. Not a park wall, not a deferral.*

**Status:** required. Rig C2 builds against `FakeTransport` as its **test seam**;
this project is what makes remotes real. Both ship in v0.1.
**Repo:** `nakli-local-bridge` — existing. **Extend the bridge; do not create a new
broker.** The L1 Ollama path and this are the same artifact.

---

## 1. The problem

`isomorphic-git` speaks git-over-HTTP. A browser tab cannot reach an arbitrary git
host directly: no CORS headers on git endpoints, and the smart-HTTP protocol is not
designed for cross-origin fetch. Every browser git tool solves this with a proxy,
and the usual answer is somebody else's hosted CORS proxy — which would route the
user's code through a stranger's server. That is precisely the thing this portfolio
exists to refuse.

---

## 2. The three paths, ranked

| # | Path | Reach | Sovereignty | Verdict |
|---|---|---|---|---|
| 1 | **Bridge proxy** — the user's own `nakli-local-bridge` binary relays git-over-HTTP | Any host: GitHub, GitLab, Gitea, self-hosted, SSH-over-HTTPS | Full. The relay is the user's own machine | **Primary.** Build this |
| 2 | **GitHub REST API** — contents, refs, and trees endpoints, CORS-enabled, no proxy | GitHub only, and a subset of operations | Full. Direct to GitHub with the user's token | **Secondary.** Zero-install fallback |
| 3 | Hosted CORS proxy | Any host | **None** — the user's code crosses a third party | **Refused.** Not built, not offered |

Detection, not configuration: if the bridge is present, path 1. If not and the
remote is GitHub, path 2. Otherwise, one honest line explaining what would unlock
push, and the elevation offer. Local work never depends on any of this.

---

## 3. Scope

**In:** clone, fetch, push (operator to any ref; agent to `agent/*` only), ls-remote —
over HTTPS, to any host, through the bridge.
GitHub-API path covering clone, fetch, and single-branch push for the no-bridge case.

**Out of v0.1:** SSH transport (agent forwarding is a different animal), git LFS,
submodules, shallow-clone tuning beyond a depth parameter.

---

## 4. Actors and the boundary

| Actor | Authority | Trust boundary |
|---|---|---|
| Operator | Initiates every remote operation | Inside |
| Bridge daemon (user's machine) | Relays bytes; **stores nothing, logs nothing** | Inside — it is the user's own process |
| Agent session | **May push to `agent/<goalId>-<n>` branches only.** Never a default or protected ref, never with force | Inside, branch-scoped |
| Remote host | Receives what is pushed | **Outside.** The one boundary crossing in the whole system |

The boundary notice fires once, plainly, before the first push: pushing sends this
code to the named remote; nothing else here leaves the machine. Flag, do not block.

---

## 5. Credentials

- Tokens live in **Vault**, never in `localStorage`, the op-log, a run artifact, a
  goal record, terminal scrollback, the Kiln namespace, or a URL.
- The bridge receives a token per-request, scoped to one operation. It does not
  persist credentials to disk.
- Redact token-shaped strings by pattern before any log, artifact, or serialised
  scrollback write.
- The kernel never sees a token. `rig.git.push` is not exposed to Python at all.

---

## 6. Chunks

| Chunk | Content | Checkpoint (machine-checkable) |
|---|---|---|
| **B0** | Bridge git-proxy endpoint: streaming relay for smart-HTTP, origin allowlist, no persistence, `429`/`Retry-After` honoured on its own pollers | Clone a pinned public repo at a pinned SHA through the bridge → head matches expected. Byte-count of relayed bytes matches upstream. Bridge process holds no repo data after completion, asserted |
| **B1** | `Transport` implementation in Rig against the bridge; detection and silent fallback | With the bridge present: clone, fetch, push, ls-remote all succeed against a local test remote. With it absent: the same calls degrade to the honest message; **no local operation is affected**, asserted |
| **B2** | GitHub API transport for the no-bridge case | Clone, fetch, and single-branch push against a fixture repo succeed with a scoped token; operations outside the subset report the limitation by name, never fail opaquely |
| **B3** | Push safety: branch-scoping, protected-ref refusal, boundary notice, force-push refusal, revocation | An agent push to a default or protected ref is rejected **at the Transport layer**, by test — not by prompt. An agent push to `agent/*` succeeds and appears in the op-log with session identity, branch, and ref. `--force` unavailable to every actor. Notice fires exactly once. Operator revocation of agent push takes effect immediately, asserted |

`FakeTransport` remains permanently as the test seam — every checkpoint above also
re-runs Rig's C2 checkpoint through the real transport, unchanged.

---

## 7. Hard rules

1. **Do not build or offer a hosted CORS proxy.** Not as a convenience, not as a
   default, not behind a flag.
2. **Do not create a new broker.** Extend `nakli-local-bridge`.
3. **Do not persist credentials or repo content in the bridge.**
4. **Do not allow any push to a default or protected ref from an agent session,** and
   do not implement force-push for any actor. Branch-scoping is enforced in the
   Transport layer, never by instruction.
5. **Do not let the absence of the bridge break local git.** Everything except
   remote operations works with no bridge, no network, and no account.
6. **Do not implement force-push in v0.1.**

---

## 8. Open decisions

| # | Decision | Default |
|---|---|---|
| **B-D1** | Bridge endpoint shape — a generic HTTP relay with an allowlist, or a git-aware endpoint that parses smart-HTTP | **Git-aware.** A generic relay on a local daemon is an open proxy on the user's machine; scoping it to git is the safer artifact |
| **B-D2** | Origin allowlist default | **Deny by default**, operator adds hosts. GitHub pre-listed, unchecked |
