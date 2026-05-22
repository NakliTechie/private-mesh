// Package config holds the nakli-hub configuration. Spec: hub-spec-001-v1.1.md
// §Configuration. Phase 2a uses JSON (stdlib only); a TOML pass may follow.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the full Hub configuration. Defaults match the spec.
type Config struct {
	Hub struct {
		ID       string `json:"id"`        // ULID; populated on first run
		Listen   string `json:"listen"`    // bind address (default 127.0.0.1:7842)
		DataDir  string `json:"data_dir"`  // writable directory
		LogLevel string `json:"log_level"` // silent | error | warn | info | debug
		Identity struct {
			KeypairFile string `json:"keypair_file"` // file under DataDir
		} `json:"identity"`
	} `json:"hub"`
	Storage struct {
		SQLiteDB           string `json:"sqlite_db"`             // file under DataDir
		BlobsDir           string `json:"blobs_dir"`             // dir under DataDir
		MaxEventSizeBytes  int64  `json:"max_event_size_bytes"`  // 1 MiB per spec
		MaxBlobCount       int64  `json:"max_blob_count"`        // operator tunable
		FsyncWrites        bool   `json:"fsync_writes"`          // durable; false for tests
	} `json:"storage"`
	Idempotency struct {
		RetentionSeconds int64 `json:"retention_seconds"` // ≥ 86400 per spec
		MaxKeysPerGrant  int64 `json:"max_keys_per_grant"`
	} `json:"idempotency"`
	Auth struct {
		// StrictCaveatBinding, when true, makes `agent-id ==`, `device-id ==`,
		// and `principal-type in [...]` caveats FAIL if the corresponding
		// X-Fabric-Agent-Id / X-Fabric-Device-Id / X-Fabric-Principal-Type
		// header is absent. The default (false) preserves prior behavior in
		// which an absent header was treated as a Hub-trusted assertion —
		// this is the documented security gap that bypasses the binding
		// caveats. Operators should set this to true once their consumer
		// fleet (crate, crate-agent, etc.) sends the binding headers
		// unconditionally on every authenticated call.
		StrictCaveatBinding bool `json:"strict_caveat_binding"`

		// StrictSyncPushAttribution, when true, makes /sync/push reject
		// events whose `appended_by_principal` does not match the sender's
		// authenticated grant principal. Without this, a peer holding a
		// delegated sync:push grant can push events claiming any
		// `appended_by_principal` and the receiving Hub records the
		// forgery verbatim — corrupting the audit trail. Default false
		// preserves multi-master federation flows where Hub A forwards
		// events authored by Hub A's own principals to Hub B. Operators
		// running single-author topologies should enable this. A full
		// ed25519-signature-per-event mitigation is tracked in
		// plan/pending.md as a deferred protocol-level follow-up.
		StrictSyncPushAttribution bool `json:"strict_sync_push_attribution"`
	} `json:"auth"`
	Health struct {
		FreshnessBudgetSeconds int64 `json:"freshness_budget_seconds"`
	} `json:"health"`
}

// Default returns a Config populated with the spec defaults.
func Default() *Config {
	c := &Config{}
	c.Hub.Listen = "127.0.0.1:7842"
	c.Hub.LogLevel = "info"
	c.Hub.Identity.KeypairFile = "hub-identity.json"
	c.Storage.SQLiteDB = "fabric.db"
	c.Storage.BlobsDir = "blobs"
	c.Storage.MaxEventSizeBytes = 1 << 20 // 1 MiB
	c.Storage.MaxBlobCount = 10_000_000
	c.Storage.FsyncWrites = true
	c.Idempotency.RetentionSeconds = 86400
	c.Idempotency.MaxKeysPerGrant = 100000
	c.Health.FreshnessBudgetSeconds = 86400
	return c
}

// Load reads a Config from path. Returns Default() if path does not exist.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("config.Load: %w", err)
	}
	c := Default()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config.Load: parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes c to path with pretty JSON. Caller is responsible for dir creation.
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config.Save: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o640); err != nil {
		return fmt.Errorf("config.Save: write %s: %w", path, err)
	}
	return nil
}

// Validate enforces baseline invariants.
func (c *Config) Validate() error {
	if c.Hub.Listen == "" {
		return fmt.Errorf("config: hub.listen is required")
	}
	if c.Hub.DataDir == "" {
		return fmt.Errorf("config: hub.data_dir is required")
	}
	if c.Storage.MaxEventSizeBytes <= 0 {
		return fmt.Errorf("config: storage.max_event_size_bytes must be > 0")
	}
	switch c.Hub.LogLevel {
	case "silent", "error", "warn", "info", "debug":
	default:
		return fmt.Errorf("config: hub.log_level %q is not silent/error/warn/info/debug", c.Hub.LogLevel)
	}
	return nil
}

// HubIdentityPath returns the absolute path to the Hub's identity file.
func (c *Config) HubIdentityPath() string {
	return filepath.Join(c.Hub.DataDir, c.Hub.Identity.KeypairFile)
}

// SQLitePath returns the absolute path to the SQLite database.
func (c *Config) SQLitePath() string {
	return filepath.Join(c.Hub.DataDir, c.Storage.SQLiteDB)
}

// BlobsPath returns the absolute path to the blobs directory.
func (c *Config) BlobsPath() string {
	return filepath.Join(c.Hub.DataDir, c.Storage.BlobsDir)
}

// PendingPath returns the absolute path to the pending Bridge ops directory.
func (c *Config) PendingPath() string {
	return filepath.Join(c.Hub.DataDir, "pending")
}

// LogPath returns the absolute path to the logs directory.
func (c *Config) LogPath() string {
	return filepath.Join(c.Hub.DataDir, "logs")
}

// NormalizeDataDir resolves DataDir to an absolute path.
func (c *Config) NormalizeDataDir() error {
	if strings.HasPrefix(c.Hub.DataDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		c.Hub.DataDir = filepath.Join(home, c.Hub.DataDir[2:])
	}
	abs, err := filepath.Abs(c.Hub.DataDir)
	if err != nil {
		return err
	}
	c.Hub.DataDir = abs
	return nil
}
