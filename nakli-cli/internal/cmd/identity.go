package cmd

import (
	"encoding/base64"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/fifio"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newIdentityCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Manage Fabric Identity Files (FIFs), devices, and agents.",
	}
	c.AddCommand(
		newIdentityInitCmd(),
		newIdentityShowCmd(),
		newIdentityPairCmd(),
		newIdentityAgentsCmd(),
	)
	return c
}

func newIdentityInitCmd() *cobra.Command {
	var displayName, output_ string
	var envelope string
	c := &cobra.Command{
		Use:   "init",
		Short: "Generate a new FIF.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if displayName == "" || output_ == "" {
				return fmt.Errorf("--display-name and --output are required")
			}
			if envelope != "" && envelope != "passphrase-only" {
				return fmt.Errorf("only `passphrase-only` envelope is supported in v1.0")
			}
			stdin, _ := cmd.Flags().GetBool("passphrase-stdin")
			pw, err := fifio.PromptPassphrase(stdin, !stdin, "Passphrase")
			if err != nil {
				return err
			}
			pid, err := fifio.CreateRoot(output_, displayName, pw)
			if err != nil {
				return err
			}
			out := output.For(cmd)
			out.SetData(map[string]any{
				"principal_id": pid,
				"display_name": displayName,
				"fif_path":     output_,
			})
			out.Humanf("Created identity:\n  Principal ID:   %s\n  Display name:   %s\n  FIF written to: %s\n", pid, displayName, output_)
			out.Humanf("\nNext steps:\n  - nakli-cli transport add ... to register a transport\n  - nakli-cli identity pair to enroll additional devices\n")
			return out.Finish()
		},
	}
	c.Flags().StringVar(&displayName, "display-name", "", "Human-readable display name (required)")
	c.Flags().StringVar(&output_, "output", "", "Output path for the FIF (required)")
	c.Flags().StringVar(&envelope, "envelope", "passphrase-only", "Envelope type (only passphrase-only in v1.0)")
	return c
}

func newIdentityShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Display the current identity (loaded from FIF).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, fifPath, err := loadConfigAndFIFPath(cmd)
			if err != nil {
				return err
			}
			_ = cfg
			stdin, _ := cmd.Flags().GetBool("passphrase-stdin")
			pw, err := fifio.PromptPassphrase(stdin, false, "Passphrase")
			if err != nil {
				return err
			}
			fif, err := fifio.LoadAndUnlock(fifPath, pw)
			if err != nil {
				return err
			}
			defer fif.Lock()
			inner := fif.Inner

			out := output.For(cmd)
			out.SetData(map[string]any{
				"principal_id":    inner.Principal.ID,
				"display_name":    inner.Principal.DisplayName,
				"type":            string(inner.Principal.Type),
				"public_key_b64":  base64.StdEncoding.EncodeToString(inner.RootKeypair.PublicKey),
				"devices_count":   len(inner.DeviceSubkeys),
				"agents_count":    len(inner.AgentIdentities),
				"transports_count": len(inner.Transports),
				"grants_held":     len(inner.GrantsHeld),
			})
			out.Humanf("Principal:    %s\n", inner.Principal.ID)
			out.Humanf("Type:         %s\n", inner.Principal.Type)
			out.Humanf("Display name: %s\n", inner.Principal.DisplayName)
			out.Humanf("Devices:      %d\n", len(inner.DeviceSubkeys))
			out.Humanf("Agents:       %d\n", len(inner.AgentIdentities))
			out.Humanf("Grants held:  %d\n", len(inner.GrantsHeld))
			return out.Finish()
		},
	}
}

// newIdentityPairCmd: deferred — pairing needs Hub /identity/pair/initiate +
// polling. M4 ships an explicit "not yet" message pointing at M4.x.
func newIdentityPairCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "pair",
		Short: "Initiate device pairing (deferred to M4.x).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("identity pair: not yet implemented in the M4 gate; lands in M4.x")
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "complete",
		Short: "Complete a device pairing (deferred to M4.x).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("identity pair complete: not yet implemented in the M4 gate; lands in M4.x")
		},
	})
	return c
}

// newIdentityAgentsCmd: deferred for the same reason.
func newIdentityAgentsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "Manage provisioned agent identities (deferred to M4.x).",
	}
	c.AddCommand(
		&cobra.Command{
			Use: "list", Short: "List agents (deferred to M4.x).",
			RunE: func(cmd *cobra.Command, args []string) error {
				return fmt.Errorf("identity agents list: not yet implemented in the M4 gate; lands in M4.x")
			},
		},
		&cobra.Command{
			Use: "provision", Short: "Provision an agent (deferred to M4.x).",
			RunE: func(cmd *cobra.Command, args []string) error {
				return fmt.Errorf("identity agents provision: not yet implemented in the M4 gate; lands in M4.x")
			},
		},
		&cobra.Command{
			Use: "retire", Short: "Retire an agent (deferred to M4.x).",
			RunE: func(cmd *cobra.Command, args []string) error {
				return fmt.Errorf("identity agents retire: not yet implemented in the M4 gate; lands in M4.x")
			},
		},
	)
	return c
}

// loadConfigAndFIFPath is the standard preamble: read --config or env, then
// determine which FIF to use (cli.default_fif unless --fif overrides).
func loadConfigAndFIFPath(cmd *cobra.Command) (*config.Config, string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "", err
	}
	fif, _ := cmd.Flags().GetString("fif")
	if fif == "" {
		fif = cfg.CLI.DefaultFIF
	}
	if fif == "" {
		return nil, "", fmt.Errorf("FIF path is empty: set cli.default_fif in config or pass --fif")
	}
	return cfg, fif, nil
}
