package snglhost_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheProtocolCarriesNoCompiler is the reason this package is under pkg/.
//
// A generated worker imports it and is built inside the user's own module. If
// the protocol reached the checker, the IR, or a codegen package, every worker
// build would compile the compiler -- and any type crossing the wire could
// quietly become an IR pointer again, which is the thing the wire exists to
// prevent.
//
// internal/testrpc is allowed and deliberate: it is the codec the test driver
// already speaks, and pkg/go/testagent depends on it the same way.
func TestTheProtocolCarriesNoCompiler(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	const mod = "git.duckfam.us/jonathan/sngl/"
	allowed := map[string]bool{
		mod + "internal/testrpc": true,
		mod + "pkg/go/snglhost":  true,
	}
	var found int
	for dep := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(dep, mod) {
			continue
		}
		found++
		if !allowed[dep] {
			t.Errorf("the protocol depends on %s; a worker would build the compiler to render a window", dep)
		}
	}
	if found == 0 {
		t.Fatal("go list reported no first-party dependencies at all; this test is checking nothing")
	}
}
