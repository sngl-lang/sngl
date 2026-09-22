package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passWindowNesting reports a window inside another window.
//
// Every backend already refuses one: html, bubbletea and android each panic
// with "unexpected nested Window", and fyne and gtk4 fall into irwalk's
// generic "unhandled ir.Stmt" -- so the invariant was asserted five times and
// reported nowhere. A window is an application shell surface and there is
// nothing for a nested one to be.
//
// Reported here rather than by the checker because the source need not nest
// them to produce one: a component declaring a window is an ordinary component
// until the inliner splices its body into a window's, and a slot carries it the
// same way. After inlining the shape is the shape, whichever route made it.
//
// Always on, and not a capability: it answers for every target.
var passWindowNesting = pass{
	name:    "WindowNesting",
	enabled: func(Caps) bool { return true },
	apply:   lowerWindowNesting,
}

func lowerWindowNesting(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	// pkg.Windows explicitly: ir.Walk descends into a package-level window's
	// body but does not visit the window itself -- only one reached as a
	// statement is stepped -- so the outer window of a program whose windows
	// are at the root was in nobody's list and the nesting under it went
	// unreported. It was a statement in `component main`'s body before.
	outer := append([]*ir.Window{}, pkg.Windows...)
	seen := make(map[*ir.Window]bool, len(outer))
	for _, w := range outer {
		seen[w] = true
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if w, ok := n.(*ir.NodeInst); ok && ir.IsWindowNode(w) && !seen[w] {
			seen[w] = true
			outer = append(outer, w)
		}
		return nil
	})
	for _, w := range outer {
		for _, inner := range w.Children {
			var found *ir.Window
			_ = ir.Walk(inner, func(n ir.Node) error {
				if nested, ok := n.(*ir.NodeInst); ok && ir.IsWindowNode(nested) && found == nil {
					found = nested
				}
				return nil
			})
			if found != nil {
				return fmt.Errorf("%s: a window cannot be nested inside another window", windowPos(found))
			}
		}
	}
	// Every window's body has just been searched and none held a window, which
	// is the question ir.Owners walks each of them to answer. Nothing after
	// this pass constructs or moves a window -- the inliner, which splices one
	// into the body that instantiated its component, runs just before -- so the
	// answer holds for the rest of the build.
	pkg.WindowsFlat = true
	return nil
}

func windowPos(w *ir.Window) string {
	if p := ir.StmtPos(w); p.IsValid() {
		return p.String()
	}
	return "window"
}
