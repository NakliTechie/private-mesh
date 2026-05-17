package cmd

import (
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

// newVersionCmd implements `nakli-cli version`.
func newVersionCmd(binaryVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print binary, protocol, SDK, and Go versions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := output.For(cmd)
			out.SetData(map[string]any{
				"binary":   binaryVersion,
				"sdk":      "fabric-sdk-go (workspace)",
				"protocol": "naklimesh/1.0",
				"go":       runtime.Version(),
			})
			out.Humanf("nakli-cli %s\n", binaryVersion)
			out.Humanf("  fabric-sdk-go: workspace\n")
			out.Humanf("  Protocol:      naklimesh/1.0\n")
			out.Humanf("  Go:            %s\n", runtime.Version())
			return out.Finish()
		},
	}
}

// newGenerateULIDCmd implements `nakli-cli generate-ulid`.
func newGenerateULIDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate-ulid",
		Short: "Emit a fresh ULID. Useful for scripting.",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := ulid.New(ulid.Timestamp(time.Now()), cryptorand.Reader)
			if err != nil {
				return fmt.Errorf("generate-ulid: %w", err)
			}
			out := output.For(cmd)
			out.SetData(map[string]any{"ulid": id.String()})
			out.Humanln(id.String())
			return out.Finish()
		},
	}
	return cmd
}

// newGenerateHubIdentityCmd implements `nakli-cli generate-hub-identity`.
// Emits a JSON-shaped hub-identity file suitable for `nakli-hub serve`. The
// output format matches nakli-hub's internal/hubid/Save() so the file is a
// drop-in replacement.
func newGenerateHubIdentityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate-hub-identity",
		Short: "Emit a Hub identity (Ed25519 + macaroon root key) as JSON.",
		Long: `Generates a fresh Hub identity and writes it to stdout as JSON.
The file is drop-in compatible with nakli-hub's hub-identity.json — useful for
pre-provisioning hosts where you don't want to invoke nakli-hub init.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
			if err != nil {
				return fmt.Errorf("generate-hub-identity: keygen: %w", err)
			}
			macKey := make([]byte, 32)
			if _, err := cryptorand.Read(macKey); err != nil {
				return fmt.Errorf("generate-hub-identity: rand: %w", err)
			}
			hubID, err := ulid.New(ulid.Timestamp(time.Now()), cryptorand.Reader)
			if err != nil {
				return fmt.Errorf("generate-hub-identity: ulid: %w", err)
			}
			data := map[string]any{
				"hub_id":            hubID.String(),
				"public_key":        base64.StdEncoding.EncodeToString(pub),
				"private_key":       base64.StdEncoding.EncodeToString(priv),
				"macaroon_root_key": base64.StdEncoding.EncodeToString(macKey),
				"created_at":        time.Now().UTC().Format(time.RFC3339Nano),
			}
			out := output.For(cmd)
			if out.IsJSON() {
				out.SetData(data)
				return out.Finish()
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(data)
		},
	}
}
