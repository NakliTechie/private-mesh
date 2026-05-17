// Wrangler bindings and env vars. Mirrors wrangler.toml.

export interface Env {
  BLOBS: R2Bucket;
  STATE: KVNamespace;

  // vars
  PROTOCOL_VERSION: string;
  MAX_EVENT_SIZE_BYTES: string;
  IDEMPOTENCY_RETENTION_SECONDS: string;
  DISCHARGE_TTL_SECONDS: string;
  LOG_LEVEL: string;
  CONFORMANCE_MODE?: string;
  // PEER_URL is a comma-separated list of upstream peers used by /health to
  // surface the `degraded` flag (conformance test 26). Optional.
  PEER_URL?: string;

  // secrets — all base64
  HUB_ID: string;
  HUB_PUBLIC_KEY: string;
  HUB_PRIVATE_KEY: string;
  MACAROON_ROOT_KEY: string;
}

// Constants pinned to the protocol version.
export const PROTOCOL_VERSION = 'naklimesh/1.0';

// Bridge primitive support is intentionally bare on the Worker — the
// catalogue is hard-coded as the conformance-test noop adapter so the
// caveat-side of /bridge/call has a known dispatch target. Real adapters
// run on the Hub; the Worker forwards or 501s. See cf-worker-spec §Bridge.
export const NOOP_ADAPTER_NAME = 'conformance-test';

// CONFORMANCE_RETIRED_AGENT mirrors fabric-sdk-go/conformance.DefaultPrep().
// When CONFORMANCE_MODE=true, the Worker seeds this principal as retired at
// startup so conformance test 30 has a reproducible target.
export const CONFORMANCE_RETIRED_AGENT = '01J0RETIREDAGENT00000000001';
