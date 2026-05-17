// Command nakli-cli is the reference operator/developer CLI for the
// NakliTechie Private Mesh. Spec: docs/specs/cli-spec-001-v1.1.md. M4 wires
// the gate-critical commands; deferred commands print a `not yet implemented`
// note pointing at the milestone where they land.
package main

import (
	"fmt"
	"os"

	"github.com/NakliTechie/private-mesh/nakli-cli/internal/cmd"
)

// BinaryVersion is set via -ldflags at release time; defaults to a meaningful
// pre-release tag during development.
var BinaryVersion = "0.1.0-alpha.0"

func main() {
	if err := cmd.NewRoot(BinaryVersion).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
