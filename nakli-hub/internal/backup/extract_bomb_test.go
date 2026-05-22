// Internal-package test file so we can swap the (unexported) size cap
// vars without exposing them on the public API surface. Public tests
// live in backup_test.go.

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractRejectsPerEntryBomb is the regression for P2 #20: a tar
// entry whose decompressed size exceeds defaultMaxEntryBytes must be
// rejected with ErrEntryTooLarge. Without this, an attacker-supplied
// backup archive can fill the disk during nakli-hub restore.
func TestExtractRejectsPerEntryBomb(t *testing.T) {
	// Temporarily shrink the per-entry cap so the test doesn't need a
	// multi-GB fixture. Restore on return.
	orig := defaultMaxEntryBytes
	defaultMaxEntryBytes = 1024
	t.Cleanup(func() { defaultMaxEntryBytes = orig })

	archive := buildArchiveSingleFile(t, "blobs/x.bin", make([]byte, 2048)) // 2 KiB > 1 KiB cap
	outDir := t.TempDir() + "/out"
	_, err := Extract(archive, outDir, true)
	if !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("expected ErrEntryTooLarge, got %v", err)
	}
}

// TestExtractRejectsAggregateBomb covers the many-small-files case: no
// single entry exceeds the per-entry cap, but the total decompressed
// size exceeds defaultMaxAggregateBytes.
func TestExtractRejectsAggregateBomb(t *testing.T) {
	origEntry := defaultMaxEntryBytes
	origAgg := defaultMaxAggregateBytes
	defaultMaxEntryBytes = 1024
	defaultMaxAggregateBytes = 2048
	t.Cleanup(func() {
		defaultMaxEntryBytes = origEntry
		defaultMaxAggregateBytes = origAgg
	})

	archive := buildArchiveMultipleFiles(t, []archiveEntry{
		{name: "blobs/a.bin", data: make([]byte, 800)},
		{name: "blobs/b.bin", data: make([]byte, 800)},
		{name: "blobs/c.bin", data: make([]byte, 800)}, // aggregate now exceeds cap
	})
	outDir := t.TempDir() + "/out"
	_, err := Extract(archive, outDir, true)
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("expected ErrArchiveTooLarge, got %v", err)
	}
}

// TestExtractAcceptsWithinBudget: legitimate archive that fits under
// the caps decompresses cleanly.
func TestExtractAcceptsWithinBudget(t *testing.T) {
	origEntry := defaultMaxEntryBytes
	origAgg := defaultMaxAggregateBytes
	defaultMaxEntryBytes = 4096
	defaultMaxAggregateBytes = 8192
	t.Cleanup(func() {
		defaultMaxEntryBytes = origEntry
		defaultMaxAggregateBytes = origAgg
	})

	archive := buildArchiveSingleFile(t, "blobs/x.bin", make([]byte, 1024))
	outDir := t.TempDir() + "/out"
	_, err := Extract(archive, outDir, true)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// --- helpers ---

type archiveEntry struct {
	name string
	data []byte
}

func buildArchiveSingleFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	return buildArchiveMultipleFiles(t, []archiveEntry{{name: name, data: data}})
}

// buildArchiveMultipleFiles writes a gzip+tar archive to a temp file
// and returns the path. Includes a minimal manifest.json so Extract's
// readManifest pass doesn't error on the synthetic fixture.
func buildArchiveMultipleFiles(t *testing.T, entries []archiveEntry) string {
	t.Helper()
	manifestJSON := []byte(`{"format":"nakli-hub-backup/1.0","hub_id":"01JTESTHUB000000000000001","created_at":"2026-05-22T12:00:00Z","blobs_dir":"blobs","sqlite":"fabric.db"}`)
	full := append([]archiveEntry{{name: "manifest.json", data: manifestJSON}}, entries...)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range full {
		hdr := &tar.Header{
			Name:    e.name,
			Mode:    0o644,
			Size:    int64(len(e.data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
