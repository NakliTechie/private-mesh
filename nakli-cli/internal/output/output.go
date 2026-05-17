// Package output implements the human-readable and JSON output modes the
// spec mandates (cli-spec-001-v1.1.md §Conventions/Output).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Writer is a thin wrapper that decides at the end of a command whether to
// emit human-readable lines or a JSON envelope. Commands populate the writer
// with data + free-text lines; Finish() emits whichever the caller asked for.
type Writer struct {
	jsonMode bool
	quiet    bool
	stdout   io.Writer
	stderr   io.Writer
	data     any
}

// For returns a Writer configured from the cobra command's persistent flags
// (--json, --quiet). It binds to os.Stdout/os.Stderr; tests can replace via
// the With* setters.
func For(cmd *cobra.Command) *Writer {
	w := &Writer{
		stdout: cmd.OutOrStdout(),
		stderr: cmd.ErrOrStderr(),
	}
	if v, err := cmd.Flags().GetBool("json"); err == nil {
		w.jsonMode = v
	}
	if v, err := cmd.Flags().GetBool("quiet"); err == nil {
		w.quiet = v
	}
	return w
}

// IsJSON reports whether JSON mode is on; some commands stream JSON lines
// (e.g., vault read) and want to suppress trailing human-readable hints.
func (w *Writer) IsJSON() bool { return w.jsonMode }

// IsQuiet reports whether --quiet was set.
func (w *Writer) IsQuiet() bool { return w.quiet }

// SetData records the structured payload that JSON mode emits.
func (w *Writer) SetData(d any) { w.data = d }

// Humanf writes a human-readable line; JSON mode swallows it.
func (w *Writer) Humanf(format string, args ...any) {
	if w.jsonMode || w.quiet {
		return
	}
	fmt.Fprintf(w.stdout, format, args...)
}

// Humanln writes a human-readable line; JSON mode swallows it.
func (w *Writer) Humanln(s string) {
	if w.jsonMode || w.quiet {
		return
	}
	fmt.Fprintln(w.stdout, s)
}

// Errorln writes to stderr regardless of mode.
func (w *Writer) Errorln(s string) { fmt.Fprintln(w.stderr, s) }

// Finish emits the JSON envelope when in JSON mode. Call from the command's
// RunE before returning nil.
func (w *Writer) Finish() error {
	if !w.jsonMode {
		return nil
	}
	enc := json.NewEncoder(w.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"ok":   true,
		"data": w.data,
	})
}

// FinishError emits the JSON error envelope and returns err so cobra prints
// nothing else. In human mode it returns err so cobra surfaces it.
func (w *Writer) FinishError(code, msg string) error {
	if w.jsonMode {
		enc := json.NewEncoder(w.stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"ok": false,
			"error": map[string]any{
				"code":    code,
				"message": msg,
			},
		})
		return ErrSilenced
	}
	return fmt.Errorf("%s: %s", code, msg)
}

// ErrSilenced is returned by FinishError in JSON mode so cobra doesn't print
// the error a second time. main treats this as exit code 1 silently.
var ErrSilenced = fmt.Errorf("nakli-cli: error already reported")

// IsTerminal reports whether stdout is a terminal. Used to decide on ANSI
// colors. M4 keeps colors off by default; lipgloss can be added later.
func (w *Writer) IsTerminal() bool {
	f, ok := w.stdout.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
