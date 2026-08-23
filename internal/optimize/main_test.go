package optimize

import (
	"os"
	"testing"
)

// TestMain shuts down any resident compile-time evaluator the tests started.
// Nothing else does it for a test binary, and each evaluator leaves a linked
// binary behind in the temp directory.
func TestMain(m *testing.M) {
	code := m.Run()
	CloseEvaluators()
	os.Exit(code)
}
