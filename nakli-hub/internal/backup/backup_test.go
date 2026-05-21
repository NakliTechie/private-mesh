package backup_test

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/backup"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// makeHubData stands up a minimal Hub data dir, writes a couple of blobs, and
// returns the config so the test can call backup.Create against it.
func makeHubData(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Hub.DataDir = dir
	cfg.Storage.FsyncWrites = false
	if err := cfg.NormalizeDataDir(); err != nil {
		t.Fatal(err)
	}

	id, err := hubid.Generate(func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(cfg.HubIdentityPath()); err != nil {
		t.Fatal(err)
	}
	cfg.Hub.ID = id.HubID

	store, err := storage.Open(cfg.SQLitePath(), cfg.BlobsPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Seed a principal + a couple of blob-backed events to ensure the backup
	// captures both SQLite state and disk blobs.
	pub, _, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPrincipal(t.Context(), storage.Principal{
		PrincipalID:   "01JFXAMPLEPRINCIPALBACK01",
		PrincipalType: "human",
		PublicKey:     pub,
		DisplayName:   "Backup Tester",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ev := "01JFXAMPLEEVENT00000" + string('A'+rune(i))
		if _, err := store.WriteBlob("list", ev, []byte("payload-"+ev), false); err != nil {
			t.Fatalf("WriteBlob: %v", err)
		}
		if _, err := store.AppendEvent(t.Context(), storage.AppendInput{
			Namespace:           "list",
			StreamID:            "stream-backup",
			StreamType:          storage.StreamVault,
			Kind:                "test",
			PayloadCiphertext:   []byte("payload-" + ev),
			VectorClock:         "{}",
			AppendedByPrincipal: "01JFXAMPLEPRINCIPALBACK01",
			AppendedByGrantID:   "01JFXAMPLEGRANTBACK00001",
		}, store.BlobPath("list", ev), ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	configPath := filepath.Join(cfg.Hub.DataDir, "config.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatal(err)
	}
	// Close the store so VACUUM INTO doesn't fight with our handle.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	cfg := makeHubData(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	configPath := filepath.Join(cfg.Hub.DataDir, "config.json")

	manifest, err := backup.Create(backup.Inputs{
		DataDir:       cfg.Hub.DataDir,
		ConfigPath:    configPath,
		IdentityPath:  cfg.HubIdentityPath(),
		SQLitePath:    cfg.SQLitePath(),
		BlobsRoot:     cfg.BlobsPath(),
		BinaryVersion: "test",
	}, archive)
	if err != nil {
		t.Fatalf("backup.Create: %v", err)
	}
	if manifest.BlobCount < 3 {
		t.Errorf("manifest blob count: got %d, want >= 3", manifest.BlobCount)
	}
	if manifest.SQLiteBytes <= 0 {
		t.Errorf("manifest sqlite_bytes: got %d, want > 0", manifest.SQLiteBytes)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	res, err := backup.Extract(archive, dest, false)
	if err != nil {
		t.Fatalf("backup.Extract: %v", err)
	}
	if res.Manifest.HubID != manifest.HubID {
		t.Errorf("hub_id mismatch: got %s, want %s", res.Manifest.HubID, manifest.HubID)
	}
	if res.BlobsWritten != res.Manifest.BlobCount {
		t.Errorf("blobs_written %d, manifest %d", res.BlobsWritten, res.Manifest.BlobCount)
	}
	// Open the restored SQLite and confirm rows survived.
	store, err := storage.Open(filepath.Join(dest, "fabric.db"), filepath.Join(dest, "blobs"))
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer store.Close()
	events, _, err := store.ReadStream(t.Context(), "list", "stream-backup", storage.ReadOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ReadStream: %v", err)
	}
	if len(events) < 3 {
		t.Errorf("expected at least 3 events in restored stream, got %d", len(events))
	}
}

// TestBackupIncludesCrateBuckets confirms the crate_buckets table (added in
// schema migration v3) survives the VACUUM INTO snapshot — the backup is a
// whole-DB snapshot, but a regression test is cheap insurance against future
// migration-table changes that might accidentally drop tables.
func TestBackupIncludesCrateBuckets(t *testing.T) {
	cfg := makeHubData(t)

	// Reopen the store to seed a crate_buckets row, then close again so VACUUM
	// INTO doesn't fight us. (makeHubData already closes its handle.)
	store, err := storage.Open(cfg.SQLitePath(), cfg.BlobsPath())
	if err != nil {
		t.Fatalf("re-open store for seeding: %v", err)
	}
	const testBucketID = "01JFXAMPLEBUCKETBACK00001"
	if err := store.CreateCrateBucket(t.Context(), storage.CrateBucket{
		BucketID:              testBucketID,
		Provider:              "r2",
		AccountID:             "0123456789abcdef0123456789abcdef",
		Region:                "auto",
		BucketName:            "backup-test-bucket",
		EndpointURL:           "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com/backup-test-bucket",
		AccessKeyID:           "AKIA-TEST-KEY",
		SecretAccessKeySealed: []byte("sealed-secret-test-bytes"),
		Nonce:                 []byte("0123456789012345678901234"), // 24 bytes
		RegisteredByPrincipal: "01JFXAMPLEPRINCIPALBACK01",
	}); err != nil {
		t.Fatalf("CreateCrateBucket: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store before backup: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if _, err := backup.Create(backup.Inputs{
		DataDir:      cfg.Hub.DataDir,
		ConfigPath:   filepath.Join(cfg.Hub.DataDir, "config.json"),
		IdentityPath: cfg.HubIdentityPath(),
		SQLitePath:   cfg.SQLitePath(),
		BlobsRoot:    cfg.BlobsPath(),
	}, archive); err != nil {
		t.Fatalf("backup.Create: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "restored")
	if _, err := backup.Extract(archive, dest, false); err != nil {
		t.Fatalf("backup.Extract: %v", err)
	}

	restored, err := storage.Open(filepath.Join(dest, "fabric.db"), filepath.Join(dest, "blobs"))
	if err != nil {
		t.Fatalf("re-open restored store: %v", err)
	}
	defer restored.Close()
	got, err := restored.LookupCrateBucket(t.Context(), testBucketID)
	if err != nil {
		t.Fatalf("LookupCrateBucket on restored store: %v", err)
	}
	if got.BucketName != "backup-test-bucket" {
		t.Errorf("bucket_name: got %q, want %q", got.BucketName, "backup-test-bucket")
	}
	if got.AccessKeyID != "AKIA-TEST-KEY" {
		t.Errorf("access_key_id: got %q, want %q", got.AccessKeyID, "AKIA-TEST-KEY")
	}
	if string(got.SecretAccessKeySealed) != "sealed-secret-test-bytes" {
		t.Errorf("secret_access_key_sealed survived but bytes differ: got %q", got.SecretAccessKeySealed)
	}
	if string(got.Nonce) != "0123456789012345678901234" {
		t.Errorf("nonce: bytes differ: got %q", got.Nonce)
	}
	if got.RegisteredByPrincipal != "01JFXAMPLEPRINCIPALBACK01" {
		t.Errorf("registered_by_principal: got %q", got.RegisteredByPrincipal)
	}
}

func TestExtractRefusesNonEmptyDirWithoutForce(t *testing.T) {
	cfg := makeHubData(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if _, err := backup.Create(backup.Inputs{
		DataDir:      cfg.Hub.DataDir,
		ConfigPath:   filepath.Join(cfg.Hub.DataDir, "config.json"),
		IdentityPath: cfg.HubIdentityPath(),
		SQLitePath:   cfg.SQLitePath(),
		BlobsRoot:    cfg.BlobsPath(),
	}, archive); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	// Drop a file so dest is non-empty.
	if err := os.WriteFile(filepath.Join(dest, "leftover"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Extract(archive, dest, false); err == nil {
		t.Fatal("expected Extract to refuse non-empty dir without --force")
	}
	// With force=true it succeeds.
	if _, err := backup.Extract(archive, dest, true); err != nil {
		t.Fatalf("Extract --force: %v", err)
	}
}

func TestExtractRejectsCorruptArchive(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bogus.tar.gz")
	if err := os.WriteFile(archive, []byte("not a real gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Extract(archive, filepath.Join(dir, "out"), false); err == nil {
		t.Fatal("expected Extract to fail on garbage input")
	}
}

func TestManifestFormatString(t *testing.T) {
	if backup.ArchiveFormat == "" {
		t.Fatal("ArchiveFormat is empty")
	}
	// Ensure the format string is what readers will validate against; if it
	// ever changes we want the test to catch a silent break.
	if want := "nakli-hub-backup/1.0"; backup.ArchiveFormat != want {
		t.Errorf("ArchiveFormat: got %q, want %q", backup.ArchiveFormat, want)
	}
}

// Smoke that the manifest JSON has the fields we expect.
func TestManifestShape(t *testing.T) {
	m := backup.Manifest{
		Format:        backup.ArchiveFormat,
		CreatedAt:     time.Now(),
		HubID:         "x",
		BinaryVersion: "v",
		BlobCount:     1,
		SQLiteBytes:   2,
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"format"`, `"created_at"`, `"hub_id"`, `"binary_version"`, `"blob_count"`, `"sqlite_bytes"`} {
		if !contains(b, k) {
			t.Errorf("manifest JSON missing %s; got %s", k, b)
		}
	}
}

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
