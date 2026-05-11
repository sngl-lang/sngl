// Package testharness holds shared scaffolding for per-platform SNGL test
// runners: test grouping and single-component promotion.
package testharness

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
