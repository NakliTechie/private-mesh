package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/httpc"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show CLI + transports + Hub health.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			w := output.For(cmd)
			payload := map[string]any{
				"cli": map[string]any{
					"default_fif":       cfg.CLI.DefaultFIF,
					"default_transport": cfg.CLI.DefaultTransport,
				},
				"transports": []map[string]any{},
			}
			transports := payload["transports"].([]map[string]any)

			w.Humanf("CLI:\n  default_fif:        %s\n  default_transport:  %s\n\nTransports:\n",
				cfg.CLI.DefaultFIF, cfg.CLI.DefaultTransport)
			if len(cfg.Transports) == 0 {
				w.Humanln("  (none configured)")
			}
			for _, t := range cfg.Transports {
				entry := map[string]any{
					"tag":  t.Tag,
					"type": t.Type,
					"url":  t.URL,
				}
				if t.Type == "hub" && t.URL != "" {
					c := httpc.New(t.URL)
					resp, err := c.Do("GET", "/fabric/v1/health", nil, nil)
					if err != nil {
						entry["reachable"] = false
						entry["error"] = err.Error()
						w.Humanf("  %-12s %-14s %-44s UNREACHABLE (%s)\n", t.Tag, t.Type, t.URL, err.Error())
					} else {
						entry["reachable"] = resp.Status == 200
						entry["status"] = resp.Status
						var hd map[string]any
						_ = json.Unmarshal(resp.Data, &hd)
						entry["health"] = hd
						mark := "OK"
						if resp.Status != 200 {
							mark = "UNHEALTHY"
						}
						w.Humanf("  %-12s %-14s %-44s %s\n", t.Tag, t.Type, t.URL, mark)
					}
				} else {
					w.Humanf("  %-12s %-14s %-44s (not probed)\n", t.Tag, t.Type, t.URL)
				}
				transports = append(transports, entry)
			}
			payload["transports"] = transports
			w.SetData(payload)
			return w.Finish()
		},
	}
}
