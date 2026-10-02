package lower

import (
	"cmp"
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A target holding DocumentWindow writes its document from one window -- html's
// page -- and shows every other window inside it. Which window that is has to
// be one the document can always hold: the first, in source order, that no
// `if` over state and no `for` can take away. The optimizer has decided every
// `if` the build could, so an `if` still standing is one over state.
//
// The roots are the package's own windows and the package body, merged by
// where each is written, and a component instantiated there is reached
// through: a root component's windows are rendered where it is.

// documentWindow is that window, nil when the package renders none. A package
// whose every window may be absent has no document, and is refused at the
// first.
func documentWindow(pkg *ir.Package) (*ir.Window, error) {
	var found *ir.Window
	seen := map[*ir.Component]bool{}
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if found != nil {
				return
			}
			n, ok := s.(*ir.NodeInst)
			if !ok {
				continue
			}
			if ir.IsWindowNode(n) {
				found = n
				return
			}
			if c := n.Component; c != nil && !seen[c] {
				seen[c] = true
				walk(c.Body)
			}
		}
	}
	walk(packageRoots(pkg))
	// A harness renders its root component in place of the package body.
	if root := pkg.RootDecl(); root != nil && found == nil {
		walk(root.Body)
	}
	if found != nil {
		return found, nil
	}
	all := ir.AllWindows(pkg)
	if len(all) == 0 {
		return nil, nil
	}
	first := slices.MinFunc(all, func(a, b *ir.Window) int { return comparePos(ir.NodePos(a), ir.NodePos(b), nil) })
	return nil, fmt.Errorf("%s: html writes its document from a window that is always there, and every window here is under an `if` or a `for`", windowPos(first))
}

// packageRoots is the package's own windows and its body, in the order they
// are written. The checker keeps the first apart, so where a window stood
// among the body's statements is recovered from the positions; a file ranks
// by where its first statement appears, which is the order the files were
// checked in.
func packageRoots(pkg *ir.Package) []ir.Stmt {
	roots := make([]ir.Stmt, 0, len(pkg.Windows)+len(pkg.Body))
	for _, w := range pkg.Windows {
		roots = append(roots, w)
	}
	roots = append(roots, pkg.Body...)
	files := map[string]int{}
	for _, s := range roots {
		if f := rootPos(s).File; f != "" {
			if _, ok := files[f]; !ok {
				files[f] = len(files)
			}
		}
	}
	slices.SortStableFunc(roots, func(a, b ir.Stmt) int { return comparePos(rootPos(a), rootPos(b), files) })
	return roots
}

func rootPos(s ir.Stmt) ast.Pos {
	if n, ok := s.(*ir.NodeInst); ok {
		return ir.NodePos(n)
	}
	return ir.StmtPos(s)
}

// comparePos orders two positions; one with no position keeps its place.
func comparePos(a, b ast.Pos, files map[string]int) int {
	if !a.IsValid() || !b.IsValid() {
		return 0
	}
	if a.File != b.File {
		return cmp.Compare(files[a.File], files[b.File])
	}
	if c := cmp.Compare(a.Line, b.Line); c != 0 {
		return c
	}
	return cmp.Compare(a.Column, b.Column)
}

// keepDocumentSurface answers what the document window says about being on
// screen. The document cannot leave the screen and is told of no close, so
// its `visible` and `@closed` do nothing there -- but `open` and `close` are
// the window's methods on every target and assign `visible`, so a call of one
// through the window's `#id` still writes it: the var `:visible` bound, or a
// cell beside the window holding what the call site said, which a read of
// `w.visible` names. The window itself is left with neither, since nothing it
// renders reads them.
func keepDocumentSurface(pkg *ir.Package, w *ir.Window) error {
	comp := w.Component
	var visible *ir.Prop
	for _, p := range comp.Props {
		if p.Name == "visible" {
			visible = p
		}
	}
	var target ir.Expr
	for _, b := range w.Bindings {
		if b.PropName == "visible" {
			target = b.Target
		}
	}
	w.Bindings = slices.DeleteFunc(w.Bindings, func(b ir.PropBinding) bool { return b.PropName == "visible" })
	w.Handlers = slices.DeleteFunc(w.Handlers, func(h ir.EventHandler) bool { return h.Name == "closed" })
	start := w.Prop("visible")
	w.Props = slices.DeleteFunc(w.Props, func(a ir.Arg) bool { return a.Name == "visible" })
	h := w.Handle
	if h == nil || visible == nil {
		return nil
	}
	var cell *ir.Var
	cellRef := func() ir.Expr {
		if target != nil {
			return ir.CloneExprSharingDecls(target)
		}
		if cell == nil {
			if start == nil {
				start = visible.Default
			}
			if start == nil {
				start = ir.DeclaredDefault(visible.Type)
			}
			cell = &ir.Var{Name: h.Name + "__visible", Type: visible.Type, Init: ir.CloneExprSharingDecls(start)}
			if owner := documentOwner(pkg, w); owner != nil {
				owner.Vars = append(owner.Vars, cell)
			} else {
				pkg.Vars = append(pkg.Vars, cell)
			}
		}
		return &ir.Ident{Name: cell.Name, Type: cell.Type, Sym: cell}
	}
	// The handle, or a select of it through an instance of the component
	// rendering the window -- a test's `c.main`.
	var through map[*ir.Component]map[string]*ir.Var
	if owner := documentOwner(pkg, w); owner != nil {
		through = map[*ir.Component]map[string]*ir.Var{owner: {h.Name: h}}
	}
	isHandle := func(e ir.Expr) bool { return handleThrough(e, through) == h }
	props := map[*ir.Param]bool{}
	for _, p := range comp.Props {
		if p.Sym != nil {
			props[p.Sym] = true
		}
	}
	methods := map[*ir.Func]bool{}
	for _, f := range comp.Funcs {
		methods[f] = true
	}
	var bad error
	// The body a method call stands for, the prop it writes named by what
	// holds it; any other prop it reads is what the window was given.
	inline := func(call *ir.Call) []ir.Stmt {
		body := deepCloneStmts(call.Func.Block)
		_ = ir.RewriteExprs(body, func(e ir.Expr) (ir.Expr, error) {
			id, ok := e.(*ir.Ident)
			if !ok {
				return e, nil
			}
			p, ok := id.Sym.(*ir.Param)
			if !ok || !props[p] {
				return e, nil
			}
			if p == visible.Sym {
				return cellRef(), nil
			}
			if v := w.Prop(p.Name); v != nil {
				return ir.CloneExprSharingDecls(v), nil
			}
			return e, nil
		})
		return body
	}
	err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.Select:
			if x.Field == "visible" && isHandle(x.Operand) {
				return cellRef(), nil
			}
		case *ir.CallStmt:
			call := x.Call
			if call == nil || !methods[call.Func] {
				return n, nil
			}
			if recv, _ := handleReceiver(call); recv != h {
				return n, nil
			}
			body := inline(call)
			if len(body) == 1 {
				return body[0], nil
			}
			return &ir.If{Cond: &ir.Literal{Type: ir.TypBool, Value: "true"}, Body: body}, nil
		case *ir.Call:
			if methods[x.Func] && bad == nil {
				if recv, _ := handleReceiver(x); recv == h {
					bad = fmt.Errorf("%s: `%s.%s()` is a method of html's document window, which answers one only as a statement", handleCallPos(x), h.Name, x.Func.Name)
				}
			}
		}
		return n, nil
	})
	if err != nil {
		return err
	}
	return bad
}

// documentOwner is the component whose own body renders w, nil for the
// package's.
func documentOwner(pkg *ir.Package, w *ir.Window) *ir.Component {
	for _, c := range pkg.Components {
		if c != nil && slices.ContainsFunc(c.Body, func(s ir.Stmt) bool { return s == ir.Stmt(w) }) {
			return c
		}
	}
	return nil
}
