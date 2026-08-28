package lower_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A component with no visual body still holds things worth keeping. Testing
// emptiness by Body alone dropped the node, and with it the state and the
// timer, on every platform with no diagnostic -- so the build succeeded and
// the program did nothing.
func TestAComponentWithNoBodyButStateOrTimersSurvives(t *testing.T) {
	for _, tc := range []struct {
		what string
		fill func(*ir.Component)
	}{
		{"a timer", func(c *ir.Component) { c.Timers = []*ir.Timer{{}} }},
		{"a var", func(c *ir.Component) { c.Vars = []*ir.Var{{Name: "n", Type: ir.TypInt}} }},
		{"a func", func(c *ir.Component) { c.Funcs = []*ir.Func{{Name: "f"}} }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			held := &ir.Component{Name: "Held"}
			tc.fill(held)
			main := &ir.Component{Name: "main"}
			inst := &ir.NodeInst{Name: "Held", Component: held}
			main.Body = []ir.Stmt{inst}
			pkg := &ir.Package{Components: []*ir.Component{held, main}, Symbols: ir.NewSymbolTable()}

			if err := lower.Lower(pkg, lower.Caps{}, lower.Options{Platform: "html"}); err != nil {
				t.Fatalf("lower: %v", err)
			}
			if len(main.Body) == 0 {
				t.Fatalf("the node was dropped, taking %s with it", tc.what)
			}
		})
	}

	// The control: a component holding nothing at all still goes, which is
	// what the emptiness test is for.
	empty := &ir.Component{Name: "Empty"}
	main := &ir.Component{Name: "main"}
	main.Body = []ir.Stmt{&ir.NodeInst{Name: "Empty", Component: empty}}
	pkg := &ir.Package{Components: []*ir.Component{empty, main}, Symbols: ir.NewSymbolTable()}
	if err := lower.Lower(pkg, lower.Caps{}, lower.Options{Platform: "html"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(main.Body) != 0 {
		t.Errorf("a component declaring nothing at all was kept: %#v", main.Body)
	}
}
