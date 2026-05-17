package cmd

// stubs.go collects every M4.x-deferred command group as no-op cobra commands
// that print a clear pointer at the milestone where they land. The intent is
// that the help tree mirrors the cli-spec from day one so operators (and
// agents) can `--help` their way through the full surface.

import (
	"fmt"

	"github.com/spf13/cobra"
)

func deferred(use, short, milestone string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short + " (deferred to " + milestone + ").",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("%s: not yet implemented in the M4 gate; lands in %s", use, milestone)
		},
	}
}

func newHistoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "history",
		Short: "Append, read, verify a hash-chained History stream.",
	}
	c.AddCommand(
		deferred("append", "Append an event to a History stream", "M4.x"),
		deferred("read", "Read events from a History stream", "M4.x"),
		deferred("verify", "Verify a History stream's hash chain", "M4.x"),
	)
	return c
}

func newBridgeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "bridge",
		Short: "Invoke / approve / inspect Bridge adapter calls.",
	}
	c.AddCommand(
		deferred("call", "Invoke a Bridge adapter", "M5.5 (adapter framework)"),
		deferred("approve", "Approve a pending Bridge call", "M5.5"),
		deferred("pending", "List pending Bridge calls", "M5.5"),
		deferred("adapters", "List configured Bridge adapters", "M5.5"),
	)
	return c
}

func newLLMCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "llm",
		Short: "Invoke LLM routes (no Hub proxying in v1.0).",
	}
	c.AddCommand(
		deferred("complete", "Issue an LLM completion request", "M5+ (SDK routing)"),
		deferred("routes", "List LLM routes", "M5+"),
	)
	return c
}

func newQueueCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "queue",
		Short: "Inspect / retry / cancel the SDK's persistent queue.",
	}
	c.AddCommand(
		deferred("list", "List queued operations", "M5 (SDK queue package)"),
		deferred("retry", "Retry a queued operation", "M5"),
		deferred("cancel", "Cancel a queued operation", "M5"),
		deferred("clear", "Bulk-cancel matching queued operations", "M5"),
	)
	return c
}

func newBackupCmd() *cobra.Command {
	return deferred("backup", "Back up the CLI's FIF + queue + config", "M4.x")
}

func newRestoreCmd() *cobra.Command {
	return deferred("restore", "Restore a CLI backup", "M4.x")
}
