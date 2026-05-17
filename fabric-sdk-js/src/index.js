// Public API for @naklitechie/fabric-sdk. Spec: fabric-sdk-js-spec-001-v1.1.md.

export { Fabric } from './fabric.js';
export { HubTransport, TransportManager, PROTOCOL_VERSION, newIdempotencyKey } from './transport.js';
export { EventBus } from './events.js';
export { FreshnessAPI } from './freshness.js';
export { HealthAPI } from './health.js';
export { GrantStore } from './grants.js';
export { VaultAPI } from './vault.js';
export { HistoryAPI } from './history.js';
export { SyncAPI, LLMAPI, BridgeAPI } from './stubs.js';
export { deriveVaultKey } from './keys.js';

// Errors
export {
  FabricError,
  FIFFormatError, FIFAuthenticationError, FIFEnvelopeUnsupportedError,
  IdentityLockedError,
  GrantInvalidError, GrantMissingError, GrantRevokedError,
  ScopeDeniedError, CaveatUnmetError,
  IdempotencyConflictError,
  VaultDecryptionError,
  SyncConflictError,
  TransportUnavailableError,
  HumanApprovalRequiredError,
  VersionMismatchError,
} from './errors.js';

// FIF helpers (re-exported for advanced consumers)
export {
  parseFIF,
  newFIF,
  newInnerFIF,
  ENVELOPE_PASSPHRASE_ONLY,
} from './identity/fif.js';

// Grant / macaroon primitives
export {
  mint as mintMacaroon,
  parse as parseMacaroon,
  verifySignature as verifyMacaroonSignature,
} from './grant/macaroon.js';

// Crypto primitives
export {
  seal, open,
  randomBytes, randomNonce, randomSalt,
  deriveKey, deriveKeyArgon2id,
  KEY_SIZE, NONCE_SIZE, TAG_SIZE, SALT_SIZE,
} from './crypto.js';

// Base64 helpers
export { bytesToBase64, base64ToBytes } from './util/base64.js';
