package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/fifio"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newInitCmd() *cobra.Command {
	var (
		displayName  string
		fifPath      string
		hubURL       string
		hubDataDir   string
		nonInteractive bool
	)
	c := &cobra.Command{
		Use:   "init",
		Short: "First-run setup: generate a FIF and register a Hub transport.",
		Long: `Walks the operator through generating a FIF, registering a Hub transport,
and writing a CLI config (~/.config/nakli-cli/config.toml).

Non-interactive mode (--non-interactive) takes every input via flags; useful
for scripted setup or test fixtures.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cfgPath = config.DefaultConfigPath()
			}
			if _, err := os.Stat(cfgPath); err == nil {
				return fmt.Errorf("config already exists at %s; remove it or use --config <path> to write elsewhere", cfgPath)
			}
			reader := bufio.NewReader(os.Stdin)

			if !nonInteractive {
				cmd.PrintErrln("Welcome to nakli-cli. This wizard sets up a Fabric identity and a Hub transport.")
				cmd.PrintErrln()
			}
			if displayName == "" {
				if nonInteractive {
					return fmt.Errorf("--display-name is required in --non-interactive mode")
				}
				displayName = prompt(reader, "Display name (e.g. Bhai)")
				if displayName == "" {
					return fmt.Errorf("display name is required")
				}
			}
			if fifPath == "" {
				if nonInteractive {
					fifPath = filepath.Join(filepath.Dir(cfgPath), "identity.fif")
				} else {
					def := filepath.Join(filepath.Dir(cfgPath), "identity.fif")
					ans := prompt(reader, "FIF path [default "+def+"]")
					if ans == "" {
						fifPath = def
					} else {
						fifPath = ans
					}
				}
			}
			stdin, _ := cmd.Flags().GetBool("passphrase-stdin")
			pw, err := fifio.PromptPassphrase(stdin, !stdin, "Passphrase")
			if err != nil {
				return err
			}
			pid, err := fifio.CreateRoot(fifPath, displayName, pw)
			if err != nil {
				return err
			}

			if hubURL == "" {
				if nonInteractive {
					return fmt.Errorf("--hub-url is required in --non-interactive mode")
				}
				ans := prompt(reader, "Hub URL [default http://127.0.0.1:7842]")
				if ans == "" {
					hubURL = "http://127.0.0.1:7842"
				} else {
					hubURL = ans
				}
			}

			cfg := config.Default()
			cfg.CLI.DefaultFIF = fifPath
			cfg.CLI.DefaultTransport = "hub"
			cfg.Transports = []config.Transport{
				{Tag: "hub", Type: "hub", URL: hubURL, Preference: 1, HubDataDir: hubDataDir},
			}
			if err := cfg.Save(cfgPath); err != nil {
				return err
			}

			w := output.For(cmd)
			w.SetData(map[string]any{
				"principal_id": pid,
				"fif_path":     fifPath,
				"config_path":  cfgPath,
				"hub_url":      hubURL,
				"hub_data_dir": hubDataDir,
			})
			w.Humanf("\nSetup complete.\n  Principal ID:   %s\n  FIF written to: %s\n  Config:         %s\n  Hub:            %s\n",
				pid, fifPath, cfgPath, hubURL)
			if hubDataDir != "" {
				w.Humanf("  Hub data dir:   %s (enables `grant mint --hub-data-dir-free` and conformance)\n", hubDataDir)
			} else {
				w.Humanf("\n  Tip: add --hub-data-dir on the same host to enable `nakli-cli grant mint` and `nakli-cli conformance` without further flags.\n")
			}
			w.Humanf("\nTry:\n  nakli-cli status\n  nakli-cli vault append --help\n")
			return w.Finish()
		},
	}
	c.Flags().StringVar(&displayName, "display-name", "", "Display name for the FIF")
	c.Flags().StringVar(&fifPath, "fif", "", "Output FIF path (default: alongside the config)")
	c.Flags().StringVar(&hubURL, "hub-url", "", "Hub URL (default: prompt)")
	c.Flags().StringVar(&hubDataDir, "hub-data-dir", "", "Hub data dir on this host (enables conformance + local mint)")
	c.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Run without prompts (all values must come from flags)")
	return c
}

func prompt(r *bufio.Reader, q string) string {
	fmt.Fprint(os.Stderr, "> "+q+": ")
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}
