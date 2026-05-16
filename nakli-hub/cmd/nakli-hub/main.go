// Command nakli-hub is the NakliTechie Private Mesh Hub binary. Phase 2a
// implements `init`, `serve`, and `version`. Other subcommands from
// hub-spec-001-v1.1.md (status, backup, restore, conformance, upgrade) land in
// Phase 2b/c.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/NakliTechie/private-mesh/nakli-hub/internal/backup"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/hubid"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/server"
	"github.com/NakliTechie/private-mesh/nakli-hub/internal/storage"
)

// BinaryVersion is the runtime version string. Set via -ldflags at build time
// for releases; defaults to a meaningful pre-release tag during development.
var BinaryVersion = "0.1.0-alpha.0"

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		os.Exit(runInit(os.Args[2:]))
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "backup":
		os.Exit(runBackup(os.Args[2:]))
	case "restore":
		os.Exit(runRestore(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "conformance":
		os.Exit(runConformance(os.Args[2:]))
	case "version":
		fmt.Printf("nakli-hub %s (protocol %s)\n", BinaryVersion, server.ProtocolVersion)
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `nakli-hub — NakliTechie Private Mesh Hub

Usage:
  nakli-hub init        --data-dir PATH [--config PATH]
  nakli-hub serve       --config PATH  [--data-dir PATH] [--listen ADDR]
  nakli-hub backup      --config PATH   --output PATH
  nakli-hub restore     --input  PATH  [--data-dir PATH] [--force]
  nakli-hub status      --config PATH  [--listen ADDR]
  nakli-hub conformance [--target URL]
  nakli-hub version`)
}

// --- init ---

func runInit(args []string) int {
	fs := flag.NewFlagSet("nakli-hub init", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "writable data directory (required)")
	configPath := fs.String("config", "", "path to write config.json (default: <data-dir>/config.json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dataDir == "" {
		fmt.Fprintln(os.Stderr, "nakli-hub init: --data-dir is required")
		fs.PrintDefaults()
		return 2
	}
	cfg := config.Default()
	cfg.Hub.DataDir = *dataDir
	if err := cfg.NormalizeDataDir(); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init:", err)
		return 1
	}
	if err := os.MkdirAll(cfg.Hub.DataDir, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: mkdir data_dir:", err)
		return 1
	}
	if err := os.MkdirAll(cfg.LogPath(), 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: mkdir logs:", err)
		return 1
	}
	if err := os.MkdirAll(cfg.PendingPath(), 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: mkdir pending:", err)
		return 1
	}

	id, err := hubid.Generate(func() string { return time.Now().UTC().Format(time.RFC3339Nano) })
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: generate identity:", err)
		return 1
	}
	cfg.Hub.ID = id.HubID
	if err := id.Save(cfg.HubIdentityPath()); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: save identity:", err)
		return 1
	}

	// Open the DB so migrations run on first init. Closing is fine; serve
	// will re-open.
	store, err := storage.Open(cfg.SQLitePath(), cfg.BlobsPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: storage.Open:", err)
		return 1
	}
	if err := store.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: storage.Close:", err)
		return 1
	}

	if *configPath == "" {
		*configPath = filepath.Join(cfg.Hub.DataDir, "config.json")
	}
	if err := cfg.Save(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub init: save config:", err)
		return 1
	}

	fmt.Printf("Initialized nakli-hub data_dir at %s\n", cfg.Hub.DataDir)
	fmt.Printf("  hub_id:          %s\n", id.HubID)
	fmt.Printf("  identity file:   %s\n", cfg.HubIdentityPath())
	fmt.Printf("  config file:     %s\n", *configPath)
	fmt.Printf("  sqlite db:       %s\n", cfg.SQLitePath())
	fmt.Println("Next: nakli-hub serve --config", *configPath)
	return 0
}

// --- serve ---

func runServe(args []string) int {
	fs := flag.NewFlagSet("nakli-hub serve", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.json")
	dataDirFlag := fs.String("data-dir", "", "override hub.data_dir from config")
	listenFlag := fs.String("listen", "", "override hub.listen from config")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "nakli-hub serve: --config is required")
		fs.PrintDefaults()
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub serve: load config:", err)
		return 1
	}
	if *dataDirFlag != "" {
		cfg.Hub.DataDir = *dataDirFlag
	}
	if err := cfg.NormalizeDataDir(); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub serve:", err)
		return 1
	}
	if *listenFlag != "" {
		cfg.Hub.Listen = *listenFlag
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub serve:", err)
		return 1
	}

	logger := newLogger(cfg.Hub.LogLevel)

	id, err := hubid.Load(cfg.HubIdentityPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "nakli-hub serve: hub-identity.json not found; run `nakli-hub init` first")
			return 1
		}
		fmt.Fprintln(os.Stderr, "nakli-hub serve: load identity:", err)
		return 1
	}

	store, err := storage.Open(cfg.SQLitePath(), cfg.BlobsPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub serve: storage.Open:", err)
		return 1
	}
	defer store.Close()

	srv := server.New(cfg, store, id, logger, BinaryVersion)
	httpSrv := &http.Server{
		Addr:              cfg.Hub.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		logger.Info("shutting down", "hub_id", id.HubID)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", cfg.Hub.Listen, "hub_id", id.HubID, "version", BinaryVersion)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "nakli-hub serve: ListenAndServe:", err)
		return 1
	}
	logger.Info("stopped", "hub_id", id.HubID)
	return 0
}

// --- backup ---

func runBackup(args []string) int {
	fs := flag.NewFlagSet("nakli-hub backup", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.json (required)")
	output := fs.String("output", "", "destination archive path (required; should end in .tar.gz)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "nakli-hub backup: --config and --output are required")
		fs.PrintDefaults()
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub backup: load config:", err)
		return 1
	}
	if err := cfg.NormalizeDataDir(); err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub backup:", err)
		return 1
	}
	manifest, err := backup.Create(backup.Inputs{
		DataDir:       cfg.Hub.DataDir,
		ConfigPath:    *configPath,
		IdentityPath:  cfg.HubIdentityPath(),
		SQLitePath:    cfg.SQLitePath(),
		BlobsRoot:     cfg.BlobsPath(),
		BinaryVersion: BinaryVersion,
	}, *output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub backup:", err)
		return 1
	}
	fmt.Printf("Wrote %s\n", *output)
	fmt.Printf("  hub_id:          %s\n", manifest.HubID)
	fmt.Printf("  created_at:      %s\n", manifest.CreatedAt.Format(time.RFC3339))
	fmt.Printf("  sqlite_bytes:    %d\n", manifest.SQLiteBytes)
	fmt.Printf("  blob_count:      %d\n", manifest.BlobCount)
	fmt.Printf("  binary_version:  %s\n", manifest.BinaryVersion)
	return 0
}

// --- restore ---

func runRestore(args []string) int {
	fs := flag.NewFlagSet("nakli-hub restore", flag.ContinueOnError)
	input := fs.String("input", "", "path to backup archive (required)")
	dataDir := fs.String("data-dir", "", "destination data directory (required)")
	force := fs.Bool("force", false, "allow restore into a non-empty directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *input == "" || *dataDir == "" {
		fmt.Fprintln(os.Stderr, "nakli-hub restore: --input and --data-dir are required")
		fs.PrintDefaults()
		return 2
	}
	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub restore:", err)
		return 1
	}
	res, err := backup.Extract(*input, abs, *force)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub restore:", err)
		return 1
	}
	fmt.Printf("Restored %s into %s\n", *input, abs)
	fmt.Printf("  hub_id:          %s\n", res.Manifest.HubID)
	fmt.Printf("  created_at:      %s\n", res.Manifest.CreatedAt.Format(time.RFC3339))
	fmt.Printf("  files_written:   %d\n", res.FilesWritten)
	fmt.Printf("  blobs_written:   %d (manifest said %d)\n", res.BlobsWritten, res.Manifest.BlobCount)
	fmt.Printf("  sqlite_bytes:    %d\n", res.Manifest.SQLiteBytes)
	fmt.Println("SQLite integrity check passed.")
	return 0
}

// --- status ---

// runStatus calls the local Hub's /fabric/v1/health endpoint and pretty-prints
// the response. Useful for shell scripts that need a quick health check.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("nakli-hub status", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.json")
	listen := fs.String("listen", "", "override the listen address read from config")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	addr := *listen
	if addr == "" {
		if *configPath == "" {
			fmt.Fprintln(os.Stderr, "nakli-hub status: either --config or --listen is required")
			fs.PrintDefaults()
			return 2
		}
		cfg, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nakli-hub status: load config:", err)
			return 1
		}
		addr = cfg.Hub.Listen
	}
	url := "http://" + addr + "/fabric/v1/health"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub status:", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub status: GET", url, ":", err)
		return 1
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nakli-hub status: read body:", err)
		return 1
	}
	// Pretty-print the JSON if it parses, otherwise fall back to raw output.
	var pretty bytes
	if pretty, err = prettyJSON(body); err == nil {
		fmt.Println(string(pretty))
	} else {
		fmt.Println(string(body))
	}
	if resp.StatusCode != 200 {
		fmt.Fprintln(os.Stderr, "nakli-hub status: HTTP", resp.StatusCode)
		return 1
	}
	return 0
}

// bytes is an alias used only inside runStatus for clarity.
type bytes = []byte

func prettyJSON(in []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(in, &v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, "", "  ")
}

// --- conformance (stub) ---

func runConformance(args []string) int {
	fs := flag.NewFlagSet("nakli-hub conformance", flag.ContinueOnError)
	target := fs.String("target", "http://127.0.0.1:7842", "transport URL to test")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Printf("nakli-hub conformance: target=%s\n", *target)
	fmt.Println("Not yet implemented. The 32-test suite lands at M3, under fabric-sdk-go/conformance/.")
	fmt.Println("At M3 this command will: run all 32 conformance tests and exit non-zero on any failure.")
	return 0
}

// newLogger returns a slog.Logger keyed off the spec's level names.
func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "silent":
		l = slog.LevelError + 4 // silence everything practical
	case "error":
		l = slog.LevelError
	case "warn":
		l = slog.LevelWarn
	case "debug":
		l = slog.LevelDebug
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
