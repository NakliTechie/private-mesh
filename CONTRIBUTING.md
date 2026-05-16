# Contributing

This repo is the Phase 1 implementation of a locked spec set. Most contribution decisions are answered by [the specs](docs/specs/) and [the agent handoff](docs/specs/agent-handoff-fabric-v1.2.md); read those first.

## Working agreement

- **Milestone-gated.** Work proceeds M0 → M9 per the handoff's build order. Don't start a milestone until the previous one's gate artifacts are in place: code merged + smoke test + subdirectory README + commit referencing the milestone + dated paragraph in [STATUS.md](STATUS.md).
- **Spec-first.** Locked decisions (handoff §"What's locked vs what's the agent's call") cannot be changed without escalation. The wire protocol is non-negotiable; all 32 conformance tests must pass.
- **Dependencies named in specs are MUST.** Where a spec's "Dependencies" section names a library, use it. Substitutions for SHOULD/Recommended require a documented reason in STATUS.md before the swap.
- **Agent's call is the agent's call.** Library choices not in any spec, variable names, file layout within a file, error message wording, debug logs — pick and move on. Document the choice in a code comment.

## Pull requests

- One PR per milestone where reasonable. Larger milestones (M2, M8) may be split, but each PR must land in a working state.
- Subject line: Conventional Commits with the milestone tag. Examples: `chore: M0 — repo skeleton`, `feat(hub): M2 — protocol endpoints`, `test(sdk-go): M3 — conformance suite 32/32`.
- PR body: terse. What changed, what's the gate artifact, anything reviewer needs to know to validate. No marketing prose.

## Review

Bhai reviews. Expect short responses. See handoff §"Escalation protocol" for what to ask, what not to ask, and how Bhai typically replies.

## Hard NOTs

These are repeated from the handoff and are non-negotiable. PRs that add any of these will be reverted.

- No telemetry, error reporting, analytics, or auto-update without explicit user consent.
- No email anywhere. No Stripe or payments. No "sign in with X" flows. Identity is the FIF.
- No `localStorage` / `sessionStorage` in tools — use IndexedDB or OPFS.
- No bundlers in deployed consumer artifacts (single HTML, single deploy).
- No Service Workers with aggressive caching.
- No bundled pre-trained models in any binary.

## Style

- **Go.** Standard `gofmt`. Conventional Go layout (`pkg/`, `internal/` only when needed). Avoid framework imports where stdlib suffices.
- **JavaScript / TypeScript.** Prettier defaults. No bundler in consumer tool output. Web platform APIs first; small dependencies second; large frameworks only with strong justification.
- **Commits.** Conventional Commits (`feat:`, `fix:`, `chore:`, `docs:`, `test:`, `refactor:`). Reference the milestone in the subject where applicable.
- **Comments.** Default to none. Add one only when *why* is non-obvious.

## License

By contributing you agree your contributions are licensed under [Apache-2.0](LICENSE).
