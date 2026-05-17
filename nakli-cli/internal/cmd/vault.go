package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/config"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/httpc"
	"github.com/NakliTechie/private-mesh/nakli-cli/internal/output"
)

func newVaultCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "vault",
		Short: "Append, read, subscribe, or list Vault streams.",
	}
	c.AddCommand(
		newVaultAppendCmd(),
		newVaultReadCmd(),
		newVaultStreamsCmd(),
		newVaultSubscribeCmd(),
	)
	return c
}

// loadGrant reads a Grant macaroon (base64) from --grant.
func loadGrant(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("--grant <path> is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read grant: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func newVaultAppendCmd() *cobra.Command {
	var (
		namespace   string
		streamID    string
		kind        string
		grantPath   string
		payloadFile string
		idemKey     string
	)
	c := &cobra.Command{
		Use:   "append",
		Short: "Append an event to a Vault stream.",
		Long: `Reads payload bytes from --payload-file or stdin and POSTs them to the Hub
as event.payload_ciphertext (base64-encoded). M4 does not encrypt bytes
client-side — encryption with per-namespace derived keys lands at M4.x. The
Hub stores opaque bytes either way.`,
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
			if namespace == "" || streamID == "" || kind == "" {
				return fmt.Errorf("--namespace, --stream-id, and --kind are required")
			}
			gr, err := loadGrant(grantPath)
			if err != nil {
				return err
			}
			payload, err := readPayload(payloadFile)
			if err != nil {
				return err
			}
			if idemKey == "" {
				idemKey = httpc.NewIdempotencyKey()
			}
			body := map[string]any{
				"namespace": namespace,
				"stream_id": streamID,
				"event": map[string]any{
					"kind":               kind,
					"payload_ciphertext": base64.StdEncoding.EncodeToString(payload),
				},
			}
			c := httpc.New(t.URL)
			resp, err := c.Do("POST", "/fabric/v1/vault/append", body,
				httpc.AuthHeaders(gr, idemKey))
			if err != nil {
				return err
			}
			if resp.Status != 200 {
				return resp.Errorf()
			}
			var data struct {
				EventID        string `json:"event_id"`
				SequenceNumber int64  `json:"sequence_number"`
			}
			_ = json.Unmarshal(resp.Data, &data)
			w := output.For(cmd)
			w.SetData(map[string]any{
				"event_id":        data.EventID,
				"sequence_number": data.SequenceNumber,
				"idempotency_key": idemKey,
			})
			w.Humanf("Appended event %s\n  Stream:        %s:%s\n  Sequence:      %d\n  Idempotency:   %s\n",
				data.EventID, namespace, streamID, data.SequenceNumber, idemKey)
			return w.Finish()
		},
	}
	c.Flags().StringVar(&namespace, "namespace", "", "Stream namespace (required)")
	c.Flags().StringVar(&streamID, "stream-id", "", "Stream id (required)")
	c.Flags().StringVar(&kind, "kind", "", "Event kind (required)")
	c.Flags().StringVar(&grantPath, "grant", "", "Path to base64 macaroon Grant (required)")
	c.Flags().StringVar(&payloadFile, "payload-file", "", "Read payload from file (default: stdin)")
	c.Flags().StringVar(&idemKey, "idempotency-key", "", "Idempotency key (default: fresh ULID)")
	return c
}

func readPayload(path string) ([]byte, error) {
	if path == "" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func newVaultReadCmd() *cobra.Command {
	var (
		namespace string
		streamID  string
		grantPath string
		since     string
		limit     int
	)
	c := &cobra.Command{
		Use:   "read",
		Short: "Read events from a Vault stream.",
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
			if namespace == "" || streamID == "" {
				return fmt.Errorf("--namespace and --stream-id are required")
			}
			gr, err := loadGrant(grantPath)
			if err != nil {
				return err
			}
			path := "/fabric/v1/vault/stream/" + namespace + "/" + streamID
			if since != "" || limit > 0 {
				q := []string{}
				if since != "" {
					q = append(q, "since="+since)
				}
				if limit > 0 {
					q = append(q, fmt.Sprintf("limit=%d", limit))
				}
				path += "?" + strings.Join(q, "&")
			}
			c := httpc.New(t.URL)
			resp, err := c.Do("GET", path, nil, httpc.AuthHeaders(gr, ""))
			if err != nil {
				return err
			}
			if resp.Status != 200 {
				return resp.Errorf()
			}
			w := output.For(cmd)
			if w.IsJSON() {
				w.SetData(json.RawMessage(resp.Data))
				return w.Finish()
			}
			// Human mode: pretty-print each event as one JSON line so the
			// payload's structure is preserved and scripting is possible.
			var read struct {
				Events []map[string]any `json:"events"`
				More   bool             `json:"more"`
			}
			if err := json.Unmarshal(resp.Data, &read); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, ev := range read.Events {
				if pb64, ok := ev["payload_ciphertext"].(string); ok {
					if decoded, err := base64.StdEncoding.DecodeString(pb64); err == nil {
						ev["payload"] = string(decoded)
					}
				}
				_ = enc.Encode(ev)
			}
			if read.More {
				w.Humanf("(more events available; pass --since=<last event_id>)\n")
			}
			return nil
		},
	}
	c.Flags().StringVar(&namespace, "namespace", "", "Stream namespace (required)")
	c.Flags().StringVar(&streamID, "stream-id", "", "Stream id (required)")
	c.Flags().StringVar(&grantPath, "grant", "", "Path to base64 macaroon Grant (required)")
	c.Flags().StringVar(&since, "since", "", "Read events after this event_id (exclusive)")
	c.Flags().IntVar(&limit, "limit", 0, "Max events to return (server-side cap)")
	return c
}

func newVaultStreamsCmd() *cobra.Command {
	var (
		namespace string
		grantPath string
	)
	c := &cobra.Command{
		Use:   "streams",
		Short: "List streams in a Vault namespace.",
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
			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}
			gr, err := loadGrant(grantPath)
			if err != nil {
				return err
			}
			c := httpc.New(t.URL)
			resp, err := c.Do("GET", "/fabric/v1/vault/streams/"+namespace, nil, httpc.AuthHeaders(gr, ""))
			if err != nil {
				return err
			}
			if resp.Status != 200 {
				return resp.Errorf()
			}
			w := output.For(cmd)
			w.SetData(json.RawMessage(resp.Data))
			if !w.IsJSON() {
				w.Humanf("%s\n", string(resp.Data))
			}
			return w.Finish()
		},
	}
	c.Flags().StringVar(&namespace, "namespace", "", "Stream namespace (required)")
	c.Flags().StringVar(&grantPath, "grant", "", "Path to base64 macaroon Grant (required)")
	return c
}

func newVaultSubscribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscribe",
		Short: "Stream events as they arrive (deferred to M4.x; SSE handler).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("vault subscribe: not yet implemented in the M4 gate; lands in M4.x (SSE)")
		},
	}
}
