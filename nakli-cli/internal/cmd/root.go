// Package cmd assembles the cobra command tree for nakli-cli. M4 lands the
// commands the agent handoff gate requires (init + vault append/read +
// conformance) plus the supporting structure for the rest of the spec.
package cmd

import (
	"github.com/spf13/cobra"
)

// NewRoot builds the top-level cobra command. binaryVersion is injected from
// main so the `version` subcommand can report it.
func NewRoot(binaryVersion string) *cobra.Command {
	root := &cobra.Command{
		Use:   "nakli-cli",
		Short: "Reference CLI for the NakliTechie Private Mesh.",
		Long: `nakli-cli is the operator/developer surface for the Fabric Protocol.

Spec: docs/specs/cli-spec-001-v1.1.md.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags every subcommand honors. The defaults match the spec
	// (cli-spec-001-v1.1.md §Conventions).
	root.PersistentFlags().String("config", "", "Config file path (defaults to $NAKLI_CONFIG or ~/.config/nakli-cli/config.toml)")
	root.PersistentFlags().String("fif", "", "FIF file path (overrides cli.default_fif)")
	root.PersistentFlags().Bool("passphrase-stdin", false, "Read passphrase from stdin (non-interactive)")
	root.PersistentFlags().String("transport", "", "Transport tag from config (overrides cli.default_transport)")
	root.PersistentFlags().Bool("json", false, "Emit machine-readable JSON on stdout")
	root.PersistentFlags().Bool("quiet", false, "Suppress non-essential output")
	root.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")
	root.PersistentFlags().Bool("no-color", false, "Disable ANSI colors")
	root.PersistentFlags().Duration("timeout", 30_000_000_000, "Per-request timeout (default 30s)")

	root.AddCommand(
		newVersionCmd(binaryVersion),
		newGenerateULIDCmd(),
		newGenerateHubIdentityCmd(),
		newInitCmd(),
		newIdentityCmd(),
		newTransportCmd(),
		newGrantCmd(),
		newVaultCmd(),
		newHistoryCmd(),
		newBridgeCmd(),
		newLLMCmd(),
		newQueueCmd(),
		newStatusCmd(),
		newConformanceCmd(),
		newBackupCmd(),
		newRestoreCmd(),
	)
	return root
}
