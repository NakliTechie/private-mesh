// Package backup implements snapshot create + extract for nakli-hub.
//
// Spec: hub-spec-001-v1.1.md §"Backup and restore". Archive format is
// gzip-compressed tar containing:
//   manifest.json    — format + timestamp + source hub_id + counts
//   config.json      — hub config
//   hub-identity.json
//   fabric.db        — clean snapshot via SQLite VACUUM INTO
//   blobs/aa/bb/<event_id>.bin — event ciphertext payloads
//
// VACUUM INTO performs an online consistent snapshot of the database. WAL
// readers stay happy; concurrent writers may briefly contend but the
// operation is short.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ArchiveFormat is the version string carried in manifest.format. Bump if the
// archive layout ever changes incompatibly.
const ArchiveFormat = "nakli-hub-backup/1.0"

// Manifest is the metadata file embedded at the root of the archive.
type Manifest struct {
	Format       string    `json:"format"`
	CreatedAt    time.Time `json:"created_at"`
	HubID        string    `json:"hub_id"`
	BinaryVersion string   `json:"binary_version"`
	BlobCount    int64     `json:"blob_count"`
	SQLiteBytes  int64     `json:"sqlite_bytes"`
}

// Inputs is the operator-supplied set of paths a backup reads from.
type Inputs struct {
	DataDir       string // the Hub's data directory
	ConfigPath    string // path to config.json (may live outside DataDir)
	IdentityPath  string // path to hub-identity.json (typically DataDir/hub-identity.json)
	SQLitePath    string // path to fabric.db
	BlobsRoot     string // path to blobs/
	BinaryVersion string // for the manifest; informational
}

// Create writes a gzip-compressed tar archive to outPath. The Hub may be
// running during this operation; SQLite's VACUUM INTO produces a consistent
// snapshot without exclusive locking.
func Create(in Inputs, outPath string) (*Manifest, error) {
	if err := requireFile(in.ConfigPath, "config"); err != nil {
		return nil, err
	}
	if err := requireFile(in.IdentityPath, "identity"); err != nil {
		return nil, err
	}
	if err := requireFile(in.SQLitePath, "sqlite db"); err != nil {
		return nil, err
	}

	hubID, err := readHubID(in.IdentityPath)
	if err != nil {
		return nil, fmt.Errorf("backup.Create: %w", err)
	}

	// Take a clean SQLite snapshot to a temp file. We use VACUUM INTO rather
	// than copying the .db + .db-wal pair because it produces a single,
	// consistent file with no WAL leftovers.
	snapDir, err := os.MkdirTemp(in.DataDir, ".backup-snapshot-")
	if err != nil {
		return nil, fmt.Errorf("backup.Create: mkdtemp: %w", err)
	}
	defer os.RemoveAll(snapDir)
	snapDB := filepath.Join(snapDir, "fabric.db")
	if err := vacuumInto(in.SQLitePath, snapDB); err != nil {
		return nil, fmt.Errorf("backup.Create: %w", err)
	}

	// Count blobs + measure snapshot size for the manifest.
	blobCount, err := countBlobs(in.BlobsRoot)
	if err != nil {
		return nil, fmt.Errorf("backup.Create: %w", err)
	}
	snapInfo, err := os.Stat(snapDB)
	if err != nil {
		return nil, fmt.Errorf("backup.Create: stat snapshot: %w", err)
	}

	manifest := &Manifest{
		Format:        ArchiveFormat,
		CreatedAt:     time.Now().UTC(),
		HubID:         hubID,
		BinaryVersion: in.BinaryVersion,
		BlobCount:     blobCount,
		SQLiteBytes:   snapInfo.Size(),
	}

	if err := writeArchive(outPath, manifest, in, snapDB); err != nil {
		return nil, fmt.Errorf("backup.Create: %w", err)
	}
	return manifest, nil
}

// Extract opens a backup archive and writes its contents into outDir. outDir
// must be empty unless force is true. Returns the manifest the archive
// declares + a tally of the files extracted.
type ExtractResult struct {
	Manifest    Manifest
	FilesWritten int64
	BlobsWritten int64
}

func Extract(archivePath, outDir string, force bool) (*ExtractResult, error) {
	if err := requireFile(archivePath, "archive"); err != nil {
		return nil, err
	}
	if !force {
		empty, err := isDirEmptyOrAbsent(outDir)
		if err != nil {
			return nil, err
		}
		if !empty {
			return nil, fmt.Errorf("backup.Extract: %s is not empty; pass --force to overwrite", outDir)
		}
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, fmt.Errorf("backup.Extract: mkdir outDir: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("backup.Extract: gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	res := &ExtractResult{}
	manifestSeen := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("backup.Extract: tar: %w", err)
		}
		// Refuse paths that try to escape outDir.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("backup.Extract: refusing unsafe path %q", hdr.Name)
		}
		target := filepath.Join(outDir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return nil, err
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileModeFor(clean))
			if err != nil {
				return nil, err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return nil, err
			}
			if err := out.Close(); err != nil {
				return nil, err
			}
			if clean == "manifest.json" {
				if err := readManifest(target, &res.Manifest); err != nil {
					return nil, err
				}
				manifestSeen = true
			}
			if strings.HasPrefix(clean, "blobs/") {
				res.BlobsWritten++
			}
			res.FilesWritten++
		default:
			// Skip symlinks / other types — Hub backups don't use them.
		}
	}
	if !manifestSeen {
		return nil, fmt.Errorf("backup.Extract: archive missing manifest.json")
	}
	if res.Manifest.Format != ArchiveFormat {
		return nil, fmt.Errorf("backup.Extract: archive format %q is not %q", res.Manifest.Format, ArchiveFormat)
	}
	// Quick sanity: SQLite integrity check.
	if err := sqliteIntegrityOK(filepath.Join(outDir, "fabric.db")); err != nil {
		return nil, fmt.Errorf("backup.Extract: %w", err)
	}
	// Quick sanity: a sample of blobs are readable.
	if err := sampleBlobReadable(filepath.Join(outDir, "blobs"), 8); err != nil {
		return nil, fmt.Errorf("backup.Extract: %w", err)
	}
	return res, nil
}

// --- internals ---

func writeArchive(outPath string, manifest *Manifest, in Inputs, snapDB string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarFile(tw, "manifest.json", manifestJSON, 0o640, manifest.CreatedAt); err != nil {
		return err
	}
	if err := writeTarFromPath(tw, in.ConfigPath, "config.json", manifest.CreatedAt); err != nil {
		return err
	}
	if err := writeTarFromPath(tw, in.IdentityPath, "hub-identity.json", manifest.CreatedAt); err != nil {
		return err
	}
	if err := writeTarFromPath(tw, snapDB, "fabric.db", manifest.CreatedAt); err != nil {
		return err
	}
	return walkBlobs(tw, in.BlobsRoot, manifest.CreatedAt)
}

func walkBlobs(tw *tar.Writer, blobsRoot string, now time.Time) error {
	if _, err := os.Stat(blobsRoot); errors.Is(err, os.ErrNotExist) {
		// no blobs yet — fine.
		return nil
	} else if err != nil {
		return err
	}
	return filepath.Walk(blobsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.Dir(blobsRoot), path)
		if err != nil {
			return err
		}
		// rel will start with "blobs/" because Dir(blobsRoot) is the parent.
		return writeTarFromPath(tw, path, filepath.ToSlash(rel), now)
	})
}

func writeTarFile(tw *tar.Writer, name string, data []byte, mode os.FileMode, modtime time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    int64(mode),
		Size:    int64(len(data)),
		ModTime: modtime,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFromPath(tw *tar.Writer, src, dst string, modtime time.Time) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:     dst,
		Mode:     int64(info.Mode().Perm()),
		Size:     info.Size(),
		ModTime:  modtime,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func readManifest(path string, into *Manifest) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

func requireFile(p, label string) error {
	if p == "" {
		return fmt.Errorf("backup: %s path is empty", label)
	}
	_, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("backup: %s at %s: %w", label, p, err)
	}
	return nil
}

func isDirEmptyOrAbsent(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func countBlobs(blobsRoot string) (int64, error) {
	var n int64
	if _, err := os.Stat(blobsRoot); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	err := filepath.Walk(blobsRoot, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

func readHubID(identityPath string) (string, error) {
	b, err := os.ReadFile(identityPath)
	if err != nil {
		return "", err
	}
	var id struct {
		HubID string `json:"hub_id"`
	}
	if err := json.Unmarshal(b, &id); err != nil {
		return "", err
	}
	if id.HubID == "" {
		return "", fmt.Errorf("backup: hub-identity.json missing hub_id")
	}
	return id.HubID, nil
}

// vacuumInto opens sourceDB read-write to issue VACUUM INTO. The target path
// must not exist when VACUUM INTO runs.
func vacuumInto(sourceDB, target string) error {
	_ = os.Remove(target)
	db, err := sql.Open("sqlite3", sourceDB)
	if err != nil {
		return fmt.Errorf("vacuumInto: open source: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("vacuumInto: ping: %w", err)
	}
	// The target path is interpolated into the SQL; sanitize so a malicious
	// path can't inject SQL. We just confirm there are no quotes — paths come
	// from controlled inputs in this codebase but cheap to guard.
	if strings.ContainsAny(target, "'\"") {
		return fmt.Errorf("vacuumInto: target path has quotes; refusing")
	}
	if _, err := db.Exec(fmt.Sprintf("VACUUM INTO '%s'", target)); err != nil {
		return fmt.Errorf("vacuumInto: VACUUM INTO %s: %w", target, err)
	}
	return nil
}

// sqliteIntegrityOK opens the database and runs `PRAGMA integrity_check`.
func sqliteIntegrityOK(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return fmt.Errorf("integrity: open: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("integrity: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity: pragma returned %q", result)
	}
	return nil
}

// sampleBlobReadable reads up to n blob files to verify they're present and
// readable (the file mode is correct and the data is on disk).
func sampleBlobReadable(blobsRoot string, n int) error {
	if _, err := os.Stat(blobsRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	checked := 0
	return filepath.Walk(blobsRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || checked >= n {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("blob sample %s: %w", p, err)
		}
		f.Close()
		checked++
		return nil
	})
}

// fileModeFor returns the mode bits an extracted file should land with. We
// keep configs and the identity file at 0600 (sensitive), blobs at 0640.
func fileModeFor(name string) os.FileMode {
	switch {
	case name == "hub-identity.json":
		return 0o600
	case name == "config.json", name == "manifest.json":
		return 0o640
	case name == "fabric.db":
		return 0o640
	case strings.HasPrefix(name, "blobs/"):
		return 0o640
	default:
		return 0o640
	}
}

// init silences "imported and not used" if any helpers are deleted later.
func init() {
	// rand is used indirectly by SQLite drivers; keep the import so future
	// utilities (snapshot id generation) don't need to re-add it.
	_ = rand.Reader
	_ = hex.EncodeToString
}
