package bubbletea

import "testing"

// TestUnkeyedTeardownLoopBindsNoOrdinal compiles the Go emitted for
// testdata/effect_tree_lifetime.sngl.
//
// tearFunc builds `for <prefix>_ti, _ = live` and then, for a position with no
// key, drops the one statement that read the ordinal -- leaving Go with an
// index nothing mentions, which is a compile error rather than a warning. Only
// the unkeyed branch-lifetime effect trips it: effect_teardown_order and
// effect_in_loop index their key out of the live list on the next line, so the
// variable is read there.
func TestUnkeyedTeardownLoopBindsNoOrdinal(t *testing.T) {
	model := compileBubbletea(t, fixtureSource(t, "effect_tree_lifetime.sngl"))
	buildGeneratedGo(t, "bt-teardown-", model)
}
