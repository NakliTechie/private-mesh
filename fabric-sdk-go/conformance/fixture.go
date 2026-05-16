package conformance

// PrepHooks describes the per-run setup the suite needs from the harness:
// inserting a retired principal (test 30) and configuring a bogus peer URL
// (test 26). The CLI and the in-process test both implement these against
// their own access path (direct SQLite for the CLI, Server methods for the
// in-process fixture).
//
// The package itself does not import any Hub internals — keeps fabric-sdk-go
// transport-agnostic.
type PrepHooks struct {
	// RetiredAgentID is the principal id the suite expects to be marked as
	// retired before tests run. Test 30 issues a request claiming this id
	// and asserts principal_retired.
	RetiredAgentID string
}

// DefaultPrep returns the values the in-tree harnesses pre-populate.
func DefaultPrep() PrepHooks {
	return PrepHooks{
		RetiredAgentID: "01J0RETIREDAGENT00000000001",
	}
}
