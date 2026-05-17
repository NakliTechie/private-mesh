package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/httpc"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newTransportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "transport",
		Short: "Manage transports (Hub, Cloudflare Worker, Local Network).",
	}
	c.AddCommand(
		newTransportAddCmd(),
		newTransportListCmd(),
		newTransportRemoveCmd(),
		newTransportPingCmd(),
	)
	return c
}

func newTransportAddCmd() *cobra.Command {
	var tag, ttype, url, hubDataDir string
	var preference int
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a transport to the CLI config.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tag == "" || ttype == "" {
				return fmt.Errorf("--tag and --type are required")
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cfgPath = config.DefaultConfigPath()
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			// Replace existing entry with the same tag.
			replaced := false
			for i := range cfg.Transports {
				if cfg.Transports[i].Tag == tag {
					cfg.Transports[i] = config.Transport{
						Tag: tag, Type: ttype, URL: url,
						Preference: preference, HubDataDir: hubDataDir,
					}
					replaced = true
					break
				}
			}
			if !replaced {
				cfg.Transports = append(cfg.Transports, config.Transport{
					Tag: tag, Type: ttype, URL: url,
					Preference: preference, HubDataDir: hubDataDir,
				})
			}
			if err := cfg.Save(cfgPath); err != nil {
				return err
			}
			out := output.For(cmd)
			out.SetData(map[string]any{"tag": tag, "type": ttype, "url": url, "preference": preference, "config_path": cfgPath, "replaced": replaced})
			if replaced {
				out.Humanf("Updated transport %q (config %s).\n", tag, cfgPath)
			} else {
				out.Humanf("Added transport %q (config %s).\n", tag, cfgPath)
			}
			return out.Finish()
		},
	}
	c.Flags().StringVar(&tag, "tag", "", "Short tag for the transport (required)")
	c.Flags().StringVar(&ttype, "type", "", "Transport type: hub | cf-worker | local-network (required)")
	c.Flags().StringVar(&url, "url", "", "Transport URL (for hub / cf-worker)")
	c.Flags().IntVar(&preference, "preference", 1, "Preference order; lower = preferred")
	c.Flags().StringVar(&hubDataDir, "hub-data-dir", "", "On-disk path of the Hub's data dir, when running on the same host (enables `conformance`)")
	return c
}

func newTransportListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured transports.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			sort.SliceStable(cfg.Transports, func(i, j int) bool {
				return cfg.Transports[i].Preference < cfg.Transports[j].Preference
			})
			out := output.For(cmd)
			out.SetData(cfg.Transports)
			if len(cfg.Transports) == 0 {
				out.Humanln("No transports configured. Use `nakli-cli transport add` to register one.")
				return out.Finish()
			}
			out.Humanf("%-12s %-14s %-44s %s\n", "TAG", "TYPE", "URL", "PREF")
			for _, t := range cfg.Transports {
				out.Humanf("%-12s %-14s %-44s %d\n", t.Tag, t.Type, t.URL, t.Preference)
			}
			return out.Finish()
		},
	}
}

func newTransportRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <tag>",
		Short: "Remove a transport from the CLI config.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			if cfgPath == "" {
				cfgPath = config.DefaultConfigPath()
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			before := len(cfg.Transports)
			filtered := cfg.Transports[:0]
			for _, t := range cfg.Transports {
				if t.Tag != args[0] {
					filtered = append(filtered, t)
				}
			}
			cfg.Transports = filtered
			if len(cfg.Transports) == before {
				return fmt.Errorf("transport %q not found", args[0])
			}
			if err := cfg.Save(cfgPath); err != nil {
				return err
			}
			out := output.For(cmd)
			out.SetData(map[string]any{"removed": args[0], "config_path": cfgPath})
			out.Humanf("Removed transport %q.\n", args[0])
			return out.Finish()
		},
	}
}

func newTransportPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping <tag>",
		Short: "Ping a transport (GET /fabric/v1/health).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			t := cfg.TransportByTag(args[0])
			if t == nil {
				return fmt.Errorf("transport %q not found", args[0])
			}
			if t.URL == "" {
				return fmt.Errorf("transport %q has no URL", args[0])
			}
			c := httpc.New(t.URL)
			resp, err := c.Do("GET", "/fabric/v1/health", nil, nil)
			if err != nil {
				return err
			}
			if resp.Status != 200 {
				return resp.Errorf()
			}
			out := output.For(cmd)
			out.SetData(map[string]any{"tag": t.Tag, "url": t.URL, "status": resp.Status, "health": resp.Data})
			out.Humanf("%s (%s)\n  Reachable:   yes (status %d)\n", t.Tag, t.URL, resp.Status)
			return out.Finish()
		},
	}
}
