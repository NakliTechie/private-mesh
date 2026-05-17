package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/conformance"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
)

func newConformanceCmd() *cobra.Command {
	var (
		target     string
		hubDataDir string
	)
	c := &cobra.Command{
		Use:   "conformance",
		Short: "Run the 32-test fabric conformance suite against a transport.",
		Long: `Drives the 32 conformance tests defined in fabric-spec-001-v1.0.md §Conformance
against the configured (or --target) Hub.

M4 mirrors nakli-hub conformance's approach: the runner needs read access to
the Hub's hub-identity.json (for the macaroon root key used to mint test
Grants). Provide --hub-data-dir explicitly, or set hub_data_dir on the
transport entry in your config. Black-box mode (--grant <path>) lands at M4.x.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			transTag, _ := cmd.Flags().GetString("transport")
			t, err := cfg.PreferredTransport(transTag)
			if err != nil {
				return err
			}
			tgt := target
			if tgt == "" {
				tgt = t.URL
			}
			if tgt == "" {
				return fmt.Errorf("--target is required (or set the transport's url)")
			}
			dataDir := hubDataDir
			if dataDir == "" {
				dataDir = t.HubDataDir
			}
			if dataDir == "" {
				return fmt.Errorf("--hub-data-dir is required (or set hub_data_dir on the transport)")
			}
			hi, err := readHubIdentity(dataDir)
			if err != nil {
				return err
			}
			if err := seedRetiredPrincipal(dataDir); err != nil {
				return fmt.Errorf("seed retired principal: %w", err)
			}
			cmd.PrintErrln("Running conformance suite: target", tgt)
			results := conformance.RunAll(conformance.Config{
				Target:          tgt,
				MacaroonRootKey: hi.MacaroonRootKey,
				Verbose:         true,
			})
			results.PrintTable(os.Stdout)
			if !results.AllPassed() {
				return fmt.Errorf("conformance suite did not pass: %d/%d", results.PassCount(), len(results.Tests))
			}
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", "", "Hub URL (overrides transport.url)")
	c.Flags().StringVar(&hubDataDir, "hub-data-dir", "", "Path to Hub data dir (overrides transport.hub_data_dir)")
	return c
}

// seedRetiredPrincipal opens the Hub's SQLite and inserts/marks the retired-
// agent principal that conformance test 30 expects. Mirrors what
// `nakli-hub conformance` does but talks to SQLite directly (the Hub's
// internal storage package isn't importable across modules).
func seedRetiredPrincipal(hubDataDir string) error {
	prep := conformance.DefaultPrep()
	dbPath := filepath.Join(hubDataDir, "fabric.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	dsn := dbPath + "?_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
        INSERT INTO principals (principal_id, principal_type, public_key, created_at)
        VALUES (?, 'agent', x'', ?)
        ON CONFLICT (principal_id) DO NOTHING`,
		prep.RetiredAgentID, now); err != nil {
		return fmt.Errorf("insert principal: %w", err)
	}
	if _, err := db.Exec(`
        UPDATE principals
        SET retired_at = ?, retirement_event_id = 'cli-conformance-setup'
        WHERE principal_id = ? AND retired_at IS NULL`,
		now, prep.RetiredAgentID); err != nil {
		return fmt.Errorf("retire principal: %w", err)
	}
	return nil
}
