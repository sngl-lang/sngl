package ir

import ()

// Owner is a declaration that owns state: the vars it declares and the body
// that reads them. There are two kinds -- the package itself and a component.
// A window is neither: it is an ordinary node, and what its body declares
// belongs to whatever renders it.
//
// Body's doc comment states the invariant they share: "a var belongs to the
// body that declares it, and a body swapped in without its vars reads names
// nothing declared." Owning the two together is what makes a declaration an
// owner. Asking each declaration separately, in each consumer, is what let a
// window be an owner nothing knew about -- a window-level `var` then reached
// no target at all, because six independent collections each named the package
// and the root component and stopped there (#133, #135).
type Owner struct {
	// Comp names the component that owns this state. nil means the package
	// owns it.
	Comp *Component

	// Vars is the state this declaration declares. A component's consts live
	// here too, marked IsConst, because that is where the checker puts them;
	// the package keeps its own in Consts, because that is where the checker
	// puts those. Consumers disagree about whether a const is a model field,
	// so the two stay apart rather than being merged here and re-separated by
	// everyone.
	//
	// These three are *snapshots*, unlike Body: `o.Vars = append(o.Vars, v)`
	// writes to a copy and the declaration never sees it. AddVars and AddFuncs
	// are the writes that reach it, and they are the only two any pass has
	// needed. They cannot be pointers the way Body is -- a component has no
	// Consts field for one to point at.
	Vars   []*Var
	Consts []*Var
	Funcs  []*Func

	// Pkg is the package this owner came from. AddVars and AddFuncs need it to
	// reach pkg.Vars and pkg.Funcs for the owner that *is* the package, so an
	// Owner built by hand rather than by Owners has to set it.
	Pkg *Package

	// Body points at the declaration's own statement list rather than copying
	// it, so a pass that rewrites one writes through this instead of switching
	// on Comp to find the field again. Never nil.
	Body *[]Stmt
}

// Stmts is the body's statements. Read-only: rewriting one means assigning
// through Body.
func (o Owner) Stmts() []Stmt {
	if o.Body == nil {
		return nil
	}
	return *o.Body
}

// AddVars and AddFuncs append to the list the owning declaration actually
// holds.
//
// They exist because Vars and Funcs are snapshots: every consumer *reads* what
// an owner declares and only a lowering pass adds to it, so paying a pointer
// at each of the twenty read sites to serve three writers is the wrong trade.
// What is not the wrong trade is writing the Comp/package switch once, here,
// beside the enumeration that already names the two -- passEffect had it
// twice and passBoundaryFailed a third time.
func (o Owner) AddVars(vars ...*Var) {
	if len(vars) == 0 {
		return
	}
	switch {
	case o.Comp != nil:
		o.Comp.Vars = append(o.Comp.Vars, vars...)
	case o.Pkg != nil:
		o.Pkg.Vars = append(o.Pkg.Vars, vars...)
	}
}

func (o Owner) AddFuncs(funcs ...*Func) {
	if len(funcs) == 0 {
		return
	}
	switch {
	case o.Comp != nil:
		o.Comp.Funcs = append(o.Comp.Funcs, funcs...)
	case o.Pkg != nil:
		o.Pkg.Funcs = append(o.Pkg.Funcs, funcs...)
	}
}

// IsPackage reports whether the package itself is the owner.
func (o Owner) IsPackage() bool { return o.Comp == nil }

// Name is what the owning declaration is called; "" for the package.
func (o Owner) Name() string {
	if o.Comp != nil {
		return o.Comp.Name
	}
	return ""
}

// Owners returns every declaration in pkg that owns state, outermost first:
// the package, then each component.
//
// Which subset a consumer wants is that consumer's question, and the answers
// genuinely differ -- a name-keyed set can hold only the root component's
// vars, because a child component's `count` is not the root model's `count`,
// while a pointer-keyed set wants every owner and cannot confuse two. What no
// consumer decides for itself is *which declarations are able to own state*.
func Owners(pkg *Package) []Owner {
	if pkg == nil {
		return nil
	}
	out := make([]Owner, 0, 1+len(pkg.Components))
	out = append(out, Owner{Pkg: pkg, Vars: pkg.Vars, Consts: pkg.Consts, Funcs: pkg.Funcs, Body: &pkg.Body})
	for _, c := range pkg.Components {
		out = append(out, Owner{Pkg: pkg, Comp: c, Vars: c.Vars, Funcs: c.Funcs, Body: &c.Body})
	}
	return out
}

// AppParent is the parent a node at the root of the package body is attached
// to: the application. `lower.AppendChild(__app, n)` is what the package body
// says of each of its nodes, and what attaching to the application means is
// each platform's answer -- a toplevel created and put on screen on gtk4 and
// fyne. Nothing declares it: a translator recognises the name, as it
// recognises a slot's parent, and a platform that has no application to
// attach to treats the node as it treated an unparented root before.
const AppParent = "__app"

// IsAppParent reports whether e names the application a root node attaches
// to.
func IsAppParent(e Expr) bool {
	id, ok := e.(*Ident)
	return ok && id.Name == AppParent && id.Synthesized
}
