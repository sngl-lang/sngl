// Package testharness holds shared scaffolding for per-platform SNGL test
// runners: dependency probes, test grouping, single-component promotion,
// and the canonical key-name set used by t.key / t.type primitives.
package testharness

// Available reports whether a platform's headless test runner can run on
// the current host. OK=true means the runner is callable; OK=false carries
// a human-readable Reason explaining what's missing.
type Available struct {
	OK     bool
	Reason string
}

// TestGroup pairs a target component name with the test FuncDefs whose
// second parameter selects that component. Tests with no component
// receiver land in the group keyed by the empty string.
type TestGroup struct {
	Component string
	Funcs     []TestFunc
}

// TestFunc carries enough identity to look up the IR func and report
// failures back to the runner. Concrete implementations resolve Func via
// pkg lookup at runtime.
type TestFunc struct {
	Name      string
	Component string
}
