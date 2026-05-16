package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store wraps the SQLite handle and the blobs root. It is the only place that
// knows the storage layout. Server-level code calls Store methods instead of
// reaching for the *sql.DB directly.
type Store struct {
	db        *sql.DB
	blobsRoot string
	now       func() time.Time
}

// Open returns a Store rooted at sqlitePath + blobsRoot. The directory hosting
// sqlitePath and blobsRoot must already exist.
func Open(sqlitePath, blobsRoot string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o750); err != nil {
		return nil, fmt.Errorf("storage.Open: mkdir db parent: %w", err)
	}
	if err := os.MkdirAll(blobsRoot, 0o750); err != nil {
		return nil, fmt.Errorf("storage.Open: mkdir blobs: %w", err)
	}
	dsn := sqlitePath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage.Open: %w", err)
	}
	// Verify connectivity early.
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage.Open: ping: %w", err)
	}
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, blobsRoot: blobsRoot, now: time.Now}, nil
}

// Close releases the SQLite handle.
func (s *Store) Close() error { return s.db.Close() }

// DB returns the underlying *sql.DB. Use sparingly; prefer typed methods.
func (s *Store) DB() *sql.DB { return s.db }

// BlobsRoot returns the absolute path to the blobs directory.
func (s *Store) BlobsRoot() string { return s.blobsRoot }

// WithClock overrides the clock used for default timestamps (testing).
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

// Now returns the Store's clock-bound current time.
func (s *Store) Now() time.Time { return s.now() }

// BlobPath returns the on-disk path where the ciphertext for (namespace, event_id)
// is stored. Path: <blobsRoot>/<aa>/<bb>/<event_id>.bin where aa/bb are the
// first two byte-pairs of SHA-256(event_id || namespace), matching the spec.
func (s *Store) BlobPath(namespace, eventID string) string {
	sum := sha256.Sum256([]byte(eventID + namespace))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(s.blobsRoot, h[:2], h[2:4], eventID+".bin")
}

// WriteBlob writes ciphertext to disk at the path implied by (namespace, event_id).
// Caller has already validated payload_size_bytes against max_event_size_bytes.
func (s *Store) WriteBlob(namespace, eventID string, ciphertext []byte, fsyncWrites bool) (string, error) {
	path := s.BlobPath(namespace, eventID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("storage.WriteBlob: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("storage.WriteBlob: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(ciphertext); err != nil {
		return "", fmt.Errorf("storage.WriteBlob: write: %w", err)
	}
	if fsyncWrites {
		if err := f.Sync(); err != nil {
			return "", fmt.Errorf("storage.WriteBlob: fsync: %w", err)
		}
	}
	return path, nil
}

// ReadBlob returns the ciphertext for (namespace, event_id) from disk.
func (s *Store) ReadBlob(namespace, eventID string) ([]byte, error) {
	return os.ReadFile(s.BlobPath(namespace, eventID))
}

// ErrNotFound is returned when a row lookup is empty.
var ErrNotFound = errors.New("storage: not found")

// wrapNotFound translates sql.ErrNoRows.
func wrapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// nowRFC3339 returns the store's current time as an RFC 3339 string.
func (s *Store) nowRFC3339() string { return s.now().UTC().Format(time.RFC3339Nano) }
