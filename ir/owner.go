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
	Timers []*Timer

	// Handlers is what the declaration itself subscribes to, which today is a
	// window's @error and nothing else -- a component catches with a boundary,
	// which is a statement in its body rather than a declaration on it.
	Handlers []*EventHandler

	// Body points at the declaration's own statement list rather than copying
	// it, so a pass that rewrites one writes through this instead of switching
	// on Comp/Win/package to find the field again. Never nil.
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
// the package, then each component, then each window -- including the windows
// a body renders, which are statements in that body rather than entries in
// pkg.Windows. A body is searched whether it belongs to the package, to a
// component or to a window already found, because passRootWindow has not
// necessarily run and a window is a statement anywhere the root tree reaches.
//
// It reports all of them, deduped by window pointer and by nothing else.
// Which subset a consumer wants is that consumer's question, and the answers
// genuinely differ -- a name-keyed set can hold only the root component's
// vars, because a child component's `count` is not the root model's `count`,
// while a pointer-keyed set wants every owner and cannot confuse two. What no
// consumer decides for itself is *which declarations are able to own state*.
func Owners(pkg *Package) []Owner {
	if pkg == nil {
		return nil
	}
	out := make([]Owner, 0, 1+len(pkg.Components)+len(pkg.Windows))
	out = append(out, Owner{Vars: pkg.Vars, Consts: pkg.Consts, Funcs: pkg.Funcs, Timers: pkg.Timers, Body: &pkg.Body})
	for _, c := range pkg.Components {
		out = append(out, Owner{Comp: c, Vars: c.Vars, Funcs: c.Funcs, Timers: c.Timers, Body: &c.Body})
	}
	seen := make(map[*Window]bool, len(pkg.Windows))
	addWin := func(w *Window) {
		if w == nil || seen[w] {
			return
		}
		seen[w] = true
		o := Owner{Win: w, Vars: w.Vars, Funcs: w.Funcs, Timers: w.Timers, Body: &w.Body}
		if w.ErrorHandler != nil {
			o.Handlers = []*EventHandler{w.ErrorHandler}
		}
		out = append(out, o)
	}
	for _, w := range pkg.Windows {
		addWin(w)
	}
	// A window a body renders is an *ir.Window statement in that body and
	// never reaches pkg.Windows. Each one found is itself searched, so the
	// walk is a worklist over `out` rather than a loop over a fixed list:
	// passWindowNesting rejects a window inside a window, but it runs late and
	// every consumer before it -- the checker's four included -- has to be able
	// to see the inner one to report on it.
	search := func(stmts []Stmt) {
		_ = WalkStmts(stmts, func(s Stmt) error {
			if w, ok := s.(*Window); ok {
				addWin(w)
			}
			return nil
		})
	}
	search(pkg.Body)
	for _, c := range pkg.Components {
		if c != nil {
			search(c.Body)
		}
	}
	for i := 0; i < len(out); i++ {
		if out[i].Win != nil {
			search(out[i].Win.Body)
		}
	}
	return out
}
