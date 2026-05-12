package testharness

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// CollectConditionalIDs walks pkg's windows and components for user-set
// #ids on visual nodes that appear inside `if` or `for` blocks. Platform
// test runners pass the result as the testlower MethodFields set so
// `c.<id>` lowers to `c.<id>()` (a nilable / list-typed reader method)
// instead of a raw field access (which would name-collide or be
// undefined).
//
// Synthetic ids assigned by NoReactivity (`__nN`) are skipped — they
// aren't reachable from test syntax.
func CollectConditionalIDs(pkg *ir.Package) map[string]bool {
	if pkg == nil {
		return nil
	}
	out := make(map[string]bool)
	for _, w := range pkg.Windows {
		walkConditional(w.Body, 0, out)
	}
	for _, c := range pkg.Components {
		walkConditional(c.Body, 0, out)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func walkConditional(stmts []ir.Stmt, depth int, out map[string]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if depth > 0 && n.ID != "" && !strings.HasPrefix(n.ID, "__n") {
				out[n.ID] = true
			}
			walkConditional(n.Children, depth, out)
		case *ir.If:
			walkConditional(n.Body, depth+1, out)
			walkConditional(n.Else, depth+1, out)
		case *ir.For:
			walkConditional(n.Body, depth+1, out)
			walkConditional(n.Else, depth+1, out)
		case *ir.PlatformFilter:
			walkConditional(n.Body, depth, out)
		case *ir.SlotInst:
			walkConditional(n.Children, depth, out)
		case *ir.ErrorBoundary:
			walkConditional(n.Children, depth, out)
		case *ir.Window:
			walkConditional(n.Body, depth, out)
		}
	}
}
