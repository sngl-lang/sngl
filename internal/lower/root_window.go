package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passRootWindow moves the package's own body into an implied root window.
//
// A program's top level is what it shows when it opens; the windows it
// declares are the ones it navigates to from there. Both may be written, so
// the root is not "the window when there are no others" -- it is first among
// them.
//
// Making it a real ir.Window rather than teaching every consumer about
// ir.Package.Body is the whole point. Two dozen lowering passes and five
// platforms already walk pkg.Windows; a second shape carrying a body is a
// second thing each of them has to know, and every bug in #133 and #135 came
// from exactly that. After this pass a package body is not a special case
// anywhere downstream -- it is a window with no declaration behind it, which
// is a shape codegen already had from a lone main component.
//
// The body's state stays in pkg.Vars, where the checker put it and where every
// consumer already reads it: the window owns the body, the package owns the
// state, and ir.Owners reports both.
var passRootWindow = pass{
	name:    "RootWindow",
	enabled: func(Caps) bool { return true },
	apply:   applyRootWindow,
}

func applyRootWindow(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil || len(pkg.Body) == 0 {
		return nil
	}
	root := &ir.Window{Body: pkg.Body, Checked: true}
	pkg.Windows = append([]*ir.Window{root}, pkg.Windows...)
	pkg.Body = nil
	return nil
}
