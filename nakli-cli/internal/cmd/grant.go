package cmd

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/fifio"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newGrantCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "grant",
		Short: "Mint, inspect, verify, list, or revoke Grants (macaroons).",
	}
	c.AddCommand(
		newGrantMintCmd(),
		newGrantInspectCmd(),
		newGrantVerifyCmd(),
		newGrantRevokeCmd(),
		newGrantListCmd(),
	)
	return c
}

// hubIdentityFile is the minimal shape we care about from hub-identity.json.
type hubIdentityFile struct {
	HubID           string `json:"hub_id"`
	PublicKey       []byte `json:"public_key"`
	MacaroonRootKey []byte `json:"macaroon_root_key"`
}

// readHubIdentity opens hub_data_dir/hub-identity.json and parses out the
// macaroon root key. Used by local-mint and conformance paths.
func readHubIdentity(hubDataDir string) (*hubIdentityFile, error) {
	if hubDataDir == "" {
		return nil, fmt.Errorf("transport has no hub_data_dir; local mint requires it (or pass --hub-data-dir)")
	}
	path := filepath.Join(hubDataDir, "hub-identity.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("readHubIdentity: %w", err)
	}
	var hi hubIdentityFile
	if err := json.Unmarshal(b, &hi); err != nil {
		return nil, fmt.Errorf("readHubIdentity: parse %s: %w", path, err)
	}
	if len(hi.MacaroonRootKey) == 0 {
		return nil, fmt.Errorf("readHubIdentity: macaroon_root_key missing from %s", path)
	}
	return &hi, nil
}

func newGrantMintCmd() *cobra.Command {
	var (
		recipient    string
		primitive    string
		namespace    string
		operations   []string
		expiresIn    time.Duration
		expiresAt    string
		rate         string
		maxAmount    string
		onlyDomains  []string
		requiresHuman bool
		nondel       bool
		parentPath   string
		outputPath   string
		hubDataDir   string
		extraCaveats []string
	)
	c := &cobra.Command{
		Use:   "mint",
		Short: "Mint a Grant signed with the Hub's macaroon root key.",
		Long: `Mints a fresh Grant macaroon. Reads the Hub's macaroon root key from the
hub-data-dir/hub-identity.json (either --hub-data-dir or the transport's
hub_data_dir from config). Output is base64-encoded macaroon bytes.

The bootstrap path (no parent Grant) is the M4-gate flow. Delegation
(--parent-grant) lands in M4.x and enforces narrowing rules.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if recipient == "" || primitive == "" {
				return fmt.Errorf("--recipient and --primitive are required")
			}
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
			dataDir := hubDataDir
			if dataDir == "" {
				dataDir = t.HubDataDir
			}
			hi, err := readHubIdentity(dataDir)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			expires := now.Add(30 * 24 * time.Hour)
			switch {
			case expiresAt != "":
				if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
					expires = parsed
				} else {
					return fmt.Errorf("--expires-at: %w", err)
				}
			case expiresIn > 0:
				expires = now.Add(expiresIn)
			}
			caveats := []string{"time < " + expires.UTC().Format(time.RFC3339Nano)}
			if rate != "" {
				cv, err := caveatFromRateSpec(rate)
				if err != nil {
					return err
				}
				caveats = append(caveats, cv)
			}
			if maxAmount != "" {
				cv, err := caveatFromAmountSpec(maxAmount)
				if err != nil {
					return err
				}
				caveats = append(caveats, cv)
			}
			if len(onlyDomains) > 0 {
				caveats = append(caveats, "only-domain in ["+strings.Join(onlyDomains, ", ")+"]")
			}
			if requiresHuman {
				caveats = append(caveats, "requires-human-approval")
			}
			if nondel {
				caveats = append(caveats, "nondelegatable")
			}
			caveats = append(caveats, extraCaveats...)

			gid, _ := ulid.New(ulid.Timestamp(now), cryptorand.Reader)
			pub, _, _ := ed25519.GenerateKey(cryptorand.Reader)
			id := grant.Identifier{
				GrantID:           gid.String(),
				IssuedAt:          now,
				IssuedByPrincipal: recipient,
				IssuedByKeypair:   pub,
				Scope: grant.Scope{
					Primitive:  grant.Primitive(primitive),
					Namespace:  namespace,
					Operations: operations,
				},
			}
			out, err := grant.Mint(grant.MintSpec{
				RootKey:    hi.MacaroonRootKey,
				Location:   t.URL,
				Identifier: id,
				Caveats:    caveats,
			})
			if err != nil {
				return fmt.Errorf("grant mint: %w", err)
			}
			macB64 := base64.StdEncoding.EncodeToString(out.Macaroon)

			if outputPath != "" {
				if err := os.WriteFile(outputPath, []byte(macB64), 0o600); err != nil {
					return fmt.Errorf("write %s: %w", outputPath, err)
				}
			}

			w := output.For(cmd)
			w.SetData(map[string]any{
				"grant_id":  gid.String(),
				"macaroon":  macB64,
				"recipient": recipient,
				"scope": map[string]any{
					"primitive": primitive, "namespace": namespace, "operations": operations,
				},
				"caveats":  caveats,
				"expires":  expires.Format(time.RFC3339),
				"output":   outputPath,
			})
			w.Humanf("Minted grant %s\n", gid.String())
			w.Humanf("  Recipient:  %s\n", recipient)
			w.Humanf("  Scope:      %s:%s (%s)\n", primitive, namespace, strings.Join(operations, ","))
			w.Humanf("  Expires:    %s\n", expires.Format(time.RFC3339))
			if len(caveats) > 1 {
				w.Humanf("  Caveats:    %s\n", strings.Join(caveats[1:], "; "))
			}
			if outputPath != "" {
				w.Humanf("  Macaroon:   %s (%d bytes b64)\n", outputPath, len(macB64))
			} else if !w.IsJSON() {
				w.Humanf("\n%s\n", macB64)
			}
			_ = parentPath // M4.x will use this for delegation paths
			return w.Finish()
		},
	}
	c.Flags().StringVar(&recipient, "recipient", "", "Recipient principal id (required)")
	c.Flags().StringVar(&primitive, "primitive", "", "Scope primitive (vault|history|grant|bridge|llm|identity|sync) (required)")
	c.Flags().StringVar(&namespace, "namespace", "*", "Scope namespace")
	c.Flags().StringSliceVar(&operations, "operations", []string{"read"}, "Comma-separated scope operations")
	c.Flags().DurationVar(&expiresIn, "expires-in", 0, "Expires after duration (e.g., 30m, 12h, 720h)")
	c.Flags().StringVar(&expiresAt, "expires-at", "", "Absolute expiry in RFC3339")
	c.Flags().StringVar(&rate, "rate", "", "Rate caveat (e.g., 1000/hour, 10/minute)")
	c.Flags().StringVar(&maxAmount, "max-amount", "", "Max-amount caveat for bridge calls (e.g., 1000USD)")
	c.Flags().StringSliceVar(&onlyDomains, "only-domain", nil, "Comma-separated only-domain list for bridge calls")
	c.Flags().BoolVar(&requiresHuman, "requires-human-approval", false, "Add the requires-human-approval caveat")
	c.Flags().BoolVar(&nondel, "nondelegatable", false, "Add the nondelegatable caveat")
	c.Flags().StringSliceVar(&extraCaveats, "caveat", nil, "Add a raw caveat string (repeatable)")
	c.Flags().StringVar(&parentPath, "parent-grant", "", "Path to a parent Grant (for delegation; M4.x)")
	c.Flags().StringVar(&outputPath, "output", "", "Write macaroon (base64) to file; empty = stdout")
	c.Flags().StringVar(&hubDataDir, "hub-data-dir", "", "Hub data dir override (otherwise read from transport config)")
	return c
}

func newGrantInspectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "inspect <path>",
		Short: "Print a Grant's structured content.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			mac, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
			if err != nil {
				return fmt.Errorf("base64 decode: %w", err)
			}
			g, err := grant.Parse(mac)
			if err != nil {
				return err
			}
			w := output.For(cmd)
			w.SetData(map[string]any{
				"grant_id":   g.Identifier.GrantID,
				"issued_at":  g.Identifier.IssuedAt,
				"issued_by":  g.Identifier.IssuedByPrincipal,
				"parent":     g.Identifier.ParentGrantID,
				"scope":      g.Identifier.Scope,
				"caveats":    g.Caveats,
			})
			w.Humanf("Grant %s\n", g.Identifier.GrantID)
			w.Humanf("  Issued at:    %s\n", g.Identifier.IssuedAt.Format(time.RFC3339))
			w.Humanf("  Issued by:    %s\n", g.Identifier.IssuedByPrincipal)
			if g.Identifier.ParentGrantID != "" {
				w.Humanf("  Parent grant: %s\n", g.Identifier.ParentGrantID)
			}
			w.Humanf("  Scope:        primitive=%s namespace=%s operations=%v\n",
				g.Identifier.Scope.Primitive, g.Identifier.Scope.Namespace, g.Identifier.Scope.Operations)
			w.Humanf("  Caveats:\n")
			for _, c := range g.Caveats {
				w.Humanf("    %s\n", c)
			}
			return w.Finish()
		},
	}
	return c
}

func newGrantVerifyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "verify <path>",
		Short: "Verify a Grant for a hypothetical operation against the Hub.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("grant verify: not yet implemented in the M4 gate; lands in M4.x (POSTs to /grant/verify)")
		},
	}
	return c
}

func newGrantRevokeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "revoke <grant_id>",
		Short: "Revoke a Grant (deferred to M4.x).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("grant revoke: not yet implemented in the M4 gate; lands in M4.x")
		},
	}
	return c
}

func newGrantListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Grants held in the FIF (deferred to M4.x).",
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = fifio.ErrFIFNotFound // suppress unused import while feature deferred
			return fmt.Errorf("grant list: not yet implemented in the M4 gate; lands in M4.x")
		},
	}
}

// caveatFromRateSpec converts "1000/hour" → "rate <= 1000 per hour".
func caveatFromRateSpec(spec string) (string, error) {
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("rate spec %q must be <int>/<window>", spec)
	}
	return "rate <= " + strings.TrimSpace(parts[0]) + " per " + strings.TrimSpace(parts[1]), nil
}

// caveatFromAmountSpec converts "1000USD" → "max-amount <= 1000 USD".
func caveatFromAmountSpec(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty max-amount")
	}
	// Find first non-digit.
	i := 0
	for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
		i++
	}
	if i == 0 || i == len(spec) {
		return "", fmt.Errorf("max-amount spec %q must be <int><currency>, e.g. 1000USD", spec)
	}
	return "max-amount <= " + spec[:i] + " " + strings.TrimSpace(spec[i:]), nil
}
