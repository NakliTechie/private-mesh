package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Result is the outcome of one conformance test.
type Result struct {
	ID         int    `json:"id"`
	Group      string `json:"group"`
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	Skipped    bool   `json:"skipped,omitempty"`
	Message    string `json:"message,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// Results aggregates per-test outcomes.
type Results struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Tests    []Result  `json:"tests"`
}

func (r *Results) add(x Result) { r.Tests = append(r.Tests, x) }

// AllPassed reports whether every test in the catalogue passed.
func (r *Results) AllPassed() bool {
	for _, t := range r.Tests {
		if !t.Passed {
			return false
		}
	}
	return len(r.Tests) > 0
}

// PassCount returns the number of passing tests.
func (r *Results) PassCount() int {
	n := 0
	for _, t := range r.Tests {
		if t.Passed {
			n++
		}
	}
	return n
}

// FailCount returns the number of failing tests.
func (r *Results) FailCount() int { return len(r.Tests) - r.PassCount() }

// PrintTable writes a human-readable summary to w.
func (r *Results) PrintTable(w io.Writer) {
	groups := map[string][]Result{}
	order := []string{}
	for _, t := range r.Tests {
		if _, ok := groups[t.Group]; !ok {
			order = append(order, t.Group)
		}
		groups[t.Group] = append(groups[t.Group], t)
	}
	for _, g := range order {
		fmt.Fprintf(w, "\n%s\n", g)
		fmt.Fprintf(w, "%s\n", strings.Repeat("-", len(g)))
		for _, t := range groups[g] {
			marker := "PASS"
			if !t.Passed {
				marker = "FAIL"
			}
			fmt.Fprintf(w, "  %s  %2d  %s (%dms)\n", marker, t.ID, t.Name, t.DurationMs)
			if !t.Passed && t.Message != "" {
				fmt.Fprintf(w, "       └─ %s\n", t.Message)
			}
		}
	}
	fmt.Fprintf(w, "\n%d/%d passing — %s\n", r.PassCount(), len(r.Tests), r.Finished.Sub(r.Started).Round(time.Millisecond))
}

// PrintJSON writes machine-readable output.
func (r *Results) PrintJSON(w io.Writer) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}

// printProgress emits one-line progress feedback during a Verbose run.
func printProgress(r Result) {
	mark := "ok"
	if !r.Passed {
		mark = "FAIL"
	}
	fmt.Fprintf(os.Stderr, "  [%s] %2d  %s\n", mark, r.ID, r.Name)
	if !r.Passed && r.Message != "" {
		fmt.Fprintf(os.Stderr, "        └─ %s\n", r.Message)
	}
}
