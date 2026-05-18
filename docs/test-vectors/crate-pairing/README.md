# crate-pairing test vectors

Reference test vectors for the [Crate Pairing Protocol v1.0](../../specs/crate-pairing-protocol-v1.0.md). Both browser (`NakliTechie/crate`) and daemon (`NakliTechie/crate-agent`) implementations consume this file to verify their token-decoding and validation paths agree with the spec.

The protocol spec (§"Test vectors", line 325) **mandates** that this file live here:

> A `test-vectors.json` file MUST live in `NakliTechie/private-mesh/docs/test-vectors/crate-pairing/` with these and additional cases, so both browser and daemon implementations can verify behaviour.

## File shape

`test-vectors.json` is a top-level object:

```json
{
  "$schema_version": "1.0",
  "source_spec": "../../specs/crate-pairing-protocol-v1.0.md",
  "vectors": [ /* array of cases */ ]
}
```

Each entry in `vectors[]` has:

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | Stable case identifier (use as the test name) |
| `token` | string | The full token string a daemon would receive |
| `expected_error` | string \| null | Error code from the protocol's [error table](../../specs/crate-pairing-protocol-v1.0.md#error-codes), or `null` if the token is valid |
| `note` | string | Plain-English description of what this case exercises |

## Current cases

| Name | Expected error |
| --- | --- |
| `valid-v1` | _(none — token is valid)_ |
| `invalid-malformed-json` | `E_TOKEN_FORMAT` |
| `invalid-wrong-version` | `E_PROTOCOL_VERSION` |
| `invalid-expired` | `E_TOKEN_EXPIRED` |
| `invalid-missing-secret` | `E_TOKEN_FORMAT` |
| `invalid-wrong-type` | `E_TOKEN_FORMAT` |
| `invalid-bad-prefix` | `E_TOKEN_FORMAT` |

All tokens are synthetic — random `secret` value, example transport endpoint, no real cryptographic keys. Do not use any of these in production environments.

## Adding cases

When extending the protocol (per §"Forward compatibility"), append new entries — do not edit existing ones. Existing implementations must remain green against the historical cases.

## Licence

Documentation in this directory is CC BY-SA 4.0, like the rest of `docs/`. The `test-vectors.json` payloads are public-domain data.
