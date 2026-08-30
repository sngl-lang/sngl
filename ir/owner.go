package ir

// Owner is a declaration that owns state: the vars it declares and the body
// that reads them. There are three kinds -- the package itself, a component,
// and a window -- and the point of naming them together is that nothing else
// about them differs at this level.
//
// Body's doc comment states the invariant they share: "a var belongs to the
// body that declares it, and a body swapped in without its vars reads names
// nothing declared." Owning the two together is what makes a declaration an
// owner. Asking each declaration separately, in each consumer, is what let a
// window be an owner nothing knew about -- a window-level `var` then reached
// no target at all, because six independent collections each named the package
// and the main component and stopped there (#133, #135).
type Owner struct {
	// Comp and Win name the declaration that owns this state. Both nil means
	// the package owns it.
	Comp *Component
	Win  *Window

	// Vars is the state this declaration declares. A component's or window's
	// consts live here too, marked IsConst, because that is where the checker
	// puts them; the package keeps its own in Consts, because that is where
	// the checker puts those. Consumers disagree about whether a const is a
	// model field, so the two stay apart rather than being merged here and
	// re-separated by everyone.
	Vars   []*Var
	Consts []*Var
	Funcs  []*Func
	Stmts  []Stmt
}

// IsPackage reports whether the package itself is the owner.
func (o Owner) IsPackage() bool { return o.Comp == nil && o.Win == nil }

// Name is what the owning declaration is called; "" for the package.
func (o Owner) Name() string {
	switch {
	case o.Comp != nil:
		return o.Comp.Name
	case o.Win != nil:
		return o.Win.Name
	}
	return ""
}

// Owners returns every declaration in pkg that owns state, outermost first:
// the package, then each component, then each window.
//
// It reports all of them. Which subset a consumer wants is that consumer's
// question, and the answers genuinely differ -- a name-keyed set can hold only
// the root component's vars, because a child component's `count` is not the
// root model's `count`, while a pointer-keyed set wants every owner and cannot
// confuse two. What no consumer should decide for itself is *which
// declarations are able to own state*, which is the part that was wrong.
func Owners(pkg *Package) []Owner {
	if pkg == nil {
		return nil
	}
	out := make([]Owner, 0, 1+len(pkg.Components)+len(pkg.Windows))
	out = append(out, Owner{Vars: pkg.Vars, Consts: pkg.Consts, Funcs: pkg.Funcs, Stmts: pkg.Body})
	for _, c := range pkg.Components {
		out = append(out, Owner{Comp: c, Vars: c.Vars, Funcs: c.Funcs, Stmts: c.Body})
	}
	for _, w := range pkg.Windows {
		out = append(out, Owner{Win: w, Vars: w.Vars, Funcs: w.Funcs, Stmts: w.Body})
	}
	return out
}
