package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// findCallStmt walks a block and returns the first CallStmt whose resolved
// function has the given name (or intrinsic ID).
func findCallStmt(stmts []ir.Stmt, funcName string) *ir.Call {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.CallStmt:
			if x.Call != nil && x.Call.Func != nil {
				fn := x.Call.Func
				if fn.Name == funcName || fn.Intrinsic == funcName {
					return x.Call
				}
				// Allow looking up error.raise by the sentinel "error.raise"
				// even though the stdlib wrapper lacks Intrinsic set.
				if funcName == "error.raise" && fn.Receiver == "error" && fn.Name == "raise" {
					return x.Call
				}
			}
		case *ir.If:
			if c := findCallStmt(x.Body, funcName); c != nil {
				return c
			}
			if c := findCallStmt(x.Else, funcName); c != nil {
				return c
			}
		case *ir.For:
			if c := findCallStmt(x.Body, funcName); c != nil {
				return c
			}
		}
	}
	return nil
}

// firstClickHandlerBlock finds the first @click handler body under the given
// visual-tree statements, descending through NodeInst children and
// ErrorBoundary children.
func firstClickHandlerBlock(stmts []ir.Stmt) []ir.Stmt {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.NodeInst:
			for i := range x.Handlers {
				if x.Handlers[i].Name == "click" && x.Handlers[i].Func != nil {
					return x.Handlers[i].Func.Block
				}
			}
			if b := firstClickHandlerBlock(x.Children); b != nil {
				return b
			}
		case *ir.ErrorBoundary:
			if b := firstClickHandlerBlock(x.Children); b != nil {
				return b
			}
		case *ir.If:
			if b := firstClickHandlerBlock(x.Body); b != nil {
				return b
			}
			if b := firstClickHandlerBlock(x.Else); b != nil {
				return b
			}
		case *ir.For:
			if b := firstClickHandlerBlock(x.Body); b != nil {
				return b
			}
		}
	}
	return nil
}

func TestErrorRaisePropagates(t *testing.T) {
	pkg := parse(t, `
window("App") {
    button(text="Go", @click {
        error.raise("boom", "")
    })
}
`)
	if len(pkg.Windows) != 1 {
		t.Fatalf("want 1 window, got %d", len(pkg.Windows))
	}
	block := firstClickHandlerBlock(pkg.Windows[0].Body)
	if block == nil {
		t.Fatal("no @click handler found")
	}
	call := findCallStmt(block, "error.raise")
	if call == nil {
		t.Fatal("no ErrorRaise call found in handler")
	}
	if call.ErrorMode != ir.ErrorPropagateNative {
		t.Errorf("ErrorRaise with no handler: mode = %v, want ErrorPropagateNative", call.ErrorMode)
	}
}

func TestErrorRaiseCaughtByBoundary(t *testing.T) {
	pkg := parse(t, `
window("App") {
    errorBoundary(@error(e) { })  {
        button(text="Go", @click {
            error.raise("boom", "")
        })
    }
}
`)
	eb := pkg.Windows[0].Body[0].(*ir.ErrorBoundary)
	if eb.Handler == nil {
		t.Fatal("boundary has no handler")
	}
	block := firstClickHandlerBlock(eb.Children)
	if block == nil {
		t.Fatal("no @click handler found")
	}
	call := findCallStmt(block, "error.raise")
	if call == nil {
		t.Fatal("ErrorRaise not found")
	}
	if call.ErrorMode != ir.ErrorInvokeAndTerminate {
		t.Errorf("mode = %v, want ErrorInvokeAndTerminate", call.ErrorMode)
	}
	if call.ResolvedHandler != eb.Handler {
		t.Error("resolved handler != boundary handler")
	}
}

func TestErrorRaiseCaughtByWindow(t *testing.T) {
	pkg := parse(t, `
window("App", @error(e) { }) {
    button(text="Go", @click {
        error.raise("boom", "")
    })
}
`)
	w := pkg.Windows[0]
	if w.ErrorHandler == nil {
		t.Fatal("window missing @error handler")
	}
	block := firstClickHandlerBlock(w.Body)
	call := findCallStmt(block, "error.raise")
	if call == nil {
		t.Fatal("ErrorRaise not found")
	}
	if call.ErrorMode != ir.ErrorInvokeAndTerminate {
		t.Errorf("mode = %v, want ErrorInvokeAndTerminate", call.ErrorMode)
	}
	if call.ResolvedHandler != w.ErrorHandler {
		t.Error("resolved handler != window handler")
	}
}

func TestErrorBoundaryInnerWins(t *testing.T) {
	pkg := parse(t, `
window("App", @error(e) { }) {
    errorBoundary(@error(e) { }) {
        button(text="Go", @click {
            error.raise("boom", "")
        })
    }
}
`)
	eb := pkg.Windows[0].Body[0].(*ir.ErrorBoundary)
	block := firstClickHandlerBlock(eb.Children)
	call := findCallStmt(block, "error.raise")
	if call.ResolvedHandler != eb.Handler {
		t.Error("inner boundary should win over window handler")
	}
}

func TestPerCallErrorHandlerAbsorbs(t *testing.T) {
	pkg := parse(t, `
func risky() {
    error.raise("x", "")
}

window("App") {
    button(text="Go", @click {
        risky(@error(e) {})
    })
}
`)
	// risky() should be CanError.
	var risky *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "risky" {
			risky = f
		}
	}
	if risky == nil || !risky.CanError {
		t.Fatalf("risky should be CanError; got %+v", risky)
	}
	block := firstClickHandlerBlock(pkg.Windows[0].Body)
	call := findCallStmt(block, "risky")
	if call == nil {
		t.Fatal("risky call not found")
	}
	if call.ErrorHandler == nil {
		t.Fatal("per-call @error not attached")
	}
	if call.ErrorMode != ir.ErrorPerCall {
		t.Errorf("mode = %v, want ErrorPerCall", call.ErrorMode)
	}
}

func TestTransitiveCanError(t *testing.T) {
	pkg := parse(t, `
func a() {
    b()
}

func b() {
    c()
}

func c() {
    error.raise("x", "")
}
`)
	get := func(name string) *ir.Func {
		for _, f := range pkg.Funcs {
			if f.Name == name {
				return f
			}
		}
		return nil
	}
	for _, n := range []string{"a", "b", "c"} {
		f := get(n)
		if f == nil || !f.CanError {
			t.Errorf("%s should be CanError", n)
		}
	}
}

func TestBubbleInsideFunc(t *testing.T) {
	pkg := parse(t, `
func risky() {
    error.raise("x", "")
}

func caller() {
    risky()
}
`)
	var caller *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "caller" {
			caller = f
		}
	}
	if caller == nil || !caller.CanError {
		t.Fatal("caller should be CanError")
	}
	call := findCallStmt(caller.Block, "risky")
	if call == nil {
		t.Fatal("no risky() call in caller")
	}
	if call.ErrorMode != ir.ErrorBubble {
		t.Errorf("mode = %v, want ErrorBubble", call.ErrorMode)
	}
}
