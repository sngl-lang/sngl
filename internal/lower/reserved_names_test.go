package lower

import (
	"strings"
	"testing"
)

// A call naming its arguments names the parameter as it is spelled now: the
// fold after lowering binds arguments by name, and one left with the old name
// bound nothing, so the parameter read nil.
func TestReservedParamRenamesTheCallsArgument(t *testing.T) {
	pkg := checkForLower(t, `
const func twice(win int) => win * 2

const func four() => twice(win=2)
`)
	if err := reserveNames(pkg, Features{Reserved: []string{"win"}}, Options{}, false); err != nil {
		t.Fatal(err)
	}
	got := lowerDump(t, pkg)
	if !strings.Contains(got, "twice(win__r0=2)") {
		t.Errorf("the call still names the old parameter:\n%s", got)
	}
}
