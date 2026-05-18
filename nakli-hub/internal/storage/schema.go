// Package storage opens SQLite, applies migrations, and provides
// query/mutation helpers backed by raw database/sql + prepared statements.
//
// Schema authority: hub-spec-001-v1.1.md §"SQLite schema". Phase 2a brings
// up the tables required for the M2 gate (vault append + read) plus the
// forward-compat-relevant tables (principals, idempotency, operation_log).
// The history-specific columns are present so M2b's history endpoints don't
// need a schema bump.
package storage

import (
	"database/sql"
	"fmt"
)

// Migrations is the ordered list of forward-only schema versions. The Hub
// uses SQLite's `user_version` pragma to track applied migrations.
var Migrations = []string{
	// 1: initial schema
	`
CREATE TABLE hub_identity (
    id TEXT PRIMARY KEY,
    hub_id TEXT NOT NULL,
    public_key BLOB NOT NULL,
    private_key BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE principals (
    principal_id TEXT PRIMARY KEY,
    principal_type TEXT NOT NULL,
    public_key BLOB NOT NULL,
    parent_principal_id TEXT,
    display_name TEXT,
    created_at TEXT NOT NULL,
    retired_at TEXT,
    retirement_event_id TEXT
);
CREATE INDEX idx_principals_type ON principals(principal_type);
CREATE INDEX idx_principals_parent ON principals(parent_principal_id);

CREATE TABLE streams (
    stream_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    stream_type TEXT NOT NULL,
    created_at TEXT NOT NULL,
    head_event_id TEXT,
    head_event_hash BLOB,
    event_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, stream_id)
);
CREATE INDEX idx_streams_namespace ON streams(namespace);

CREATE TABLE events (
    event_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    stream_type TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    kind TEXT NOT NULL,
    blob_path TEXT NOT NULL,
    payload_size_bytes INTEGER NOT NULL,
    payload_metadata TEXT,
    causal_dependencies TEXT,
    vector_clock TEXT NOT NULL,
    previous_event_hash BLOB,
    event_hash BLOB,
    appended_at TEXT NOT NULL,
    appended_by_principal TEXT NOT NULL,
    appended_by_grant_id TEXT NOT NULL,
    appended_by_device TEXT,
    FOREIGN KEY (namespace, stream_id) REFERENCES streams(namespace, stream_id)
);
CREATE INDEX idx_events_stream ON events(namespace, stream_id, sequence_number);
CREATE INDEX idx_events_appended ON events(appended_at);
CREATE INDEX idx_events_principal ON events(appended_by_principal);

CREATE TABLE idempotency (
    key TEXT NOT NULL,
    grant_id TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    payload_hash BLOB NOT NULL,
    response_status INTEGER NOT NULL,
    response_body BLOB,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (key, grant_id)
);
CREATE INDEX idx_idempotency_expires ON idempotency(expires_at);

CREATE TABLE grants_known (
    grant_id TEXT PRIMARY KEY,
    issued_by_principal TEXT NOT NULL,
    recipient_principal TEXT NOT NULL,
    parent_grant_id TEXT,
    scope TEXT NOT NULL,
    caveats TEXT NOT NULL,
    issued_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    revocation_event_id TEXT
);
CREATE INDEX idx_grants_principal ON grants_known(recipient_principal);

CREATE TABLE peers (
    peer_id TEXT PRIMARY KEY,
    peer_type TEXT NOT NULL,
    url TEXT NOT NULL,
    public_key BLOB NOT NULL,
    last_sync_at TEXT,
    last_seen_at TEXT,
    sync_state TEXT
);

CREATE TABLE pending_bridge (
    pending_id TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL,
    adapter TEXT NOT NULL,
    operation TEXT NOT NULL,
    params TEXT NOT NULL,
    requested_by_principal TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    approve_by TEXT,
    approved_at TEXT,
    rejected_at TEXT,
    rejected_reason TEXT,
    executed_at TEXT,
    result TEXT
);

CREATE TABLE operation_log (
    op_id TEXT PRIMARY KEY,
    timestamp TEXT NOT NULL,
    grant_id TEXT,
    principal TEXT,
    endpoint TEXT NOT NULL,
    status INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    error_code TEXT
);
CREATE INDEX idx_oplog_ts ON operation_log(timestamp);

CREATE TABLE pairing_tokens (
    token TEXT PRIMARY KEY,
    numeric_code TEXT,
    initiated_by_principal TEXT NOT NULL,
    initiated_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    completed_at TEXT,
    completed_by_device TEXT,
    fif_integrity_commitment BLOB
);
CREATE INDEX idx_pairing_expires ON pairing_tokens(expires_at);
`,
	// 2: Unit C — CRATE-PAIR tokens (browser issues, daemon redeems).
	//
	// Separate from `pairing_tokens` (which is for device-enrollment per
	// fabric-spec-001's /identity/pair flow). CRATE-PAIR tokens carry full
	// crate-pairing-protocol-v1.0.md payloads and are looked up by `secret`.
	`
CREATE TABLE crate_pairing_tokens (
    secret TEXT PRIMARY KEY,
    payload_json TEXT NOT NULL,
    bucket_id TEXT NOT NULL,
    identity_pubkey TEXT NOT NULL,
    transport_endpoint TEXT NOT NULL,
    transport_type TEXT NOT NULL,
    issued_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    redeemed_at TEXT,
    redeemed_by_daemon_pubkey TEXT,
    daemon_fingerprint TEXT,
    issued_capability_id TEXT,
    cancelled_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_crate_pairing_expires ON crate_pairing_tokens(expires_at);
CREATE INDEX idx_crate_pairing_bucket ON crate_pairing_tokens(bucket_id);
`,
}

// Migrate brings the database to the latest schema version. Idempotent.
func Migrate(db *sql.DB) error {
	var current int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("storage.Migrate: read user_version: %w", err)
	}
	for v := current; v < len(Migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("storage.Migrate: begin v%d: %w", v+1, err)
		}
		if _, err := tx.Exec(Migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("storage.Migrate: apply v%d: %w", v+1, err)
		}
		// PRAGMA user_version cannot be parameterised, so build inline.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("storage.Migrate: bump user_version v%d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("storage.Migrate: commit v%d: %w", v+1, err)
		}
	}
	return nil
}
