# nakli-cli

Reference CLI for the NakliTechie Private Mesh. Demonstrates every protocol primitive and serves as the operator/developer tool.

**Status:** alpha (M0 skeleton)

## Quick start

```sh
./smoke.sh
```

Real CLI lands in M4. Once built:

```sh
nakli-cli init
nakli-cli vault append --stream notes --data @note.txt
nakli-cli conformance --target http://localhost:7842
```

## Build

TBD at M4. Built on top of the Go SDK.

## Test

```sh
./smoke.sh   # M0: prints OK
```

## Configuration

Config file plus env vars. Defaults documented at M4. JSON output mode (`--json`) for every command, intended for piping.

## Operational notes

- Installable via `curl|bash`
- Shell completions (bash, zsh, fish)
- No telemetry, no analytics, no auto-update

## Security notes

The CLI holds FIF unlock material in memory only during command execution. It NEVER persists passphrases or unwrapped keys. Macaroons it mints inherit the operator's authority and are scoped to the requested attenuation.

## Roadmap

- M4: full CLI per [cli-spec-001-v1.1.md](../docs/specs/cli-spec-001-v1.1.md)
- M9: `curl|bash` installer, signed releases

## License

Apache-2.0 (see [../LICENSE](../LICENSE)).
