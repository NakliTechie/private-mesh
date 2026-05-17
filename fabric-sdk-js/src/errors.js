// Typed error hierarchy mirroring the protocol error codes
// (fabric-spec-001-v1.0.md §Wire format). Spec table: §"Error types".

export class FabricError extends Error {
  constructor(message, { code = 'fabric_error', retryable = false, cause } = {}) {
    super(message);
    this.name = 'FabricError';
    this.code = code;
    this.retryable = retryable;
    if (cause) this.cause = cause;
  }
}

export class FIFFormatError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'fif_format', ...opts });
    this.name = 'FIFFormatError';
  }
}

export class FIFAuthenticationError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'fif_auth', ...opts });
    this.name = 'FIFAuthenticationError';
  }
}

export class FIFEnvelopeUnsupportedError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'fif_envelope_unsupported', ...opts });
    this.name = 'FIFEnvelopeUnsupportedError';
  }
}

export class IdentityLockedError extends FabricError {
  constructor(message = 'identity is locked; call unlockFIF or createFIF first', opts = {}) {
    super(message, { code: 'identity_locked', ...opts });
    this.name = 'IdentityLockedError';
  }
}

export class GrantInvalidError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'grant_invalid', ...opts });
    this.name = 'GrantInvalidError';
  }
}

export class GrantMissingError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'grant_missing', ...opts });
    this.name = 'GrantMissingError';
  }
}

export class GrantRevokedError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'grant_revoked', ...opts });
    this.name = 'GrantRevokedError';
  }
}

export class ScopeDeniedError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'scope_denied', ...opts });
    this.name = 'ScopeDeniedError';
  }
}

export class CaveatUnmetError extends FabricError {
  constructor(message, { unmetCaveats = [], ...opts } = {}) {
    super(message, { code: 'caveat_unmet', ...opts });
    this.name = 'CaveatUnmetError';
    this.unmetCaveats = unmetCaveats;
  }
}

export class IdempotencyConflictError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'idempotency_conflict', ...opts });
    this.name = 'IdempotencyConflictError';
  }
}

export class VaultDecryptionError extends FabricError {
  constructor(message, { eventId = '', ...opts } = {}) {
    super(message, { code: 'vault_decryption', ...opts });
    this.name = 'VaultDecryptionError';
    this.eventId = eventId;
  }
}

export class SyncConflictError extends FabricError {
  constructor(message, { conflict, ...opts } = {}) {
    super(message, { code: 'conflict', ...opts });
    this.name = 'SyncConflictError';
    this.conflict = conflict;
  }
}

export class TransportUnavailableError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'unavailable', retryable: true, ...opts });
    this.name = 'TransportUnavailableError';
  }
}

export class HumanApprovalRequiredError extends FabricError {
  constructor(message, { pendingOperationId, ...opts } = {}) {
    super(message, { code: 'human_approval_required', ...opts });
    this.name = 'HumanApprovalRequiredError';
    this.pendingOperationId = pendingOperationId;
  }
}

export class VersionMismatchError extends FabricError {
  constructor(message, opts = {}) {
    super(message, { code: 'version_mismatch', ...opts });
    this.name = 'VersionMismatchError';
  }
}

// fromEnvelope turns a Hub error envelope into the right FabricError subclass.
export function fromEnvelope(envelope, httpStatus) {
  const code = envelope?.error?.code ?? 'fabric_error';
  const message = envelope?.error?.message ?? `Hub returned status ${httpStatus}`;
  const retryable = envelope?.error?.retryable ?? false;
  switch (code) {
    case 'grant_invalid':         return new GrantInvalidError(message, { retryable });
    case 'grant_missing':         return new GrantMissingError(message, { retryable });
    case 'grant_revoked':         return new GrantRevokedError(message, { retryable });
    case 'scope_denied':          return new ScopeDeniedError(message, { retryable });
    case 'caveat_unmet':          return new CaveatUnmetError(message, { retryable });
    case 'idempotency_conflict':  return new IdempotencyConflictError(message, { retryable });
    case 'unavailable':           return new TransportUnavailableError(message, { retryable });
    case 'version_mismatch':      return new VersionMismatchError(message, { retryable });
    case 'human_approval_required':
      return new HumanApprovalRequiredError(message, {
        retryable,
        pendingOperationId: envelope?.data?.pending_id,
      });
    default:
      return new FabricError(message, { code, retryable });
  }
}
