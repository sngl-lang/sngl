package ir

import (
	"strconv"
	"strings"
)

// Owner is a declaration that owns state: the vars it declares and the body
// that reads them. There are two kinds -- the package itself and a component.
//
// A window is listed too, and owns nothing: it is a rendering root rather than
// a storage level, so its declarations belong to whatever contains it. What it
// still contributes is a Body every pass has to walk and the @error handler it
// subscribes to, which is why it is here at all. Both go when ir.Window does.
//
// Body's doc comment states the invariant they share: "a var belongs to the
// body that declares it, and a body swapped in without its vars reads names
// nothing declared." Owning the two together is what makes a declaration an
// owner. Asking each declaration separately, in each consumer, is what let a
// window be an owner nothing knew about -- a window-level `var` then reached
// no target at all, because six independent collections each named the package
// and the root component and stopped there (#133, #135).
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
	//
	// These four are *snapshots*, unlike Body: `o.Vars = append(o.Vars, v)`
	// writes to a copy and the declaration never sees it. AddVars and AddFuncs
	// are the writes that reach it, and they are the only two any pass has
	// needed. They cannot be pointers the way Body is -- a component has no
	// Consts field for one to point at.
	Vars   []*Var
	Consts []*Var
	Funcs  []*Func

	// Handlers is what the declaration itself subscribes to, which today is a
	// window's @error and its own events (`@close`) and nothing else -- a
	// component catches with a boundary,
	// which is a statement in its body rather than a declaration on it. A
	// snapshot, as the four above are.
	Handlers []*EventHandler

	// Pkg is the package this owner came from. AddVars and AddFuncs need it to
	// reach pkg.Vars and pkg.Funcs for the owner that *is* the package, so an
	// Owner built by hand rather than by Owners has to set it.
	Pkg *Package

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

// AddVars and AddFuncs append to the list the owning declaration actually
// holds.
//
// They exist because Vars and Funcs are snapshots: every consumer *reads* what
// an owner declares and only a lowering pass adds to it, so paying a pointer
// at each of the twenty read sites to serve three writers is the wrong trade.
// What is not the wrong trade is writing the Comp/Win/package switch once,
// here, beside the enumeration that already names the three -- passEffect had
// it twice and passBoundaryFailed a third time.
// A window falls through to the package, which is its container: `window` is
// root-only, so a declaration a window body makes belongs to the package
// unless a root-family component renders it, and by the time a pass adds one
// the lift has put every window on the package.
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
func (o Owner) IsPackage() bool { return o.Comp == nil && o.Win == nil }

// Name is what the owning declaration is called; "" for the package.
func (o Owner) Name() string {
	switch {
	case o.Comp != nil:
		return o.Comp.Name
	case o.Win != nil:
		// The window's `#id` and not `window`: what a consumer wants from an
		// owner's name is the thing the program called it.
		return o.Win.ID
	}
	return ""
}

// Owners returns every declaration in pkg that owns state, outermost first:
// the package, then each component, then each window -- including the windows
// a body renders, which are statements in that body rather than entries in
// pkg.Windows. A body is searched whether it belongs to the package, to a
// component or to a window already found, because a window is a statement
// anywhere the root tree reaches and nothing lifts one out.
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
	out = append(out, Owner{Pkg: pkg, Vars: pkg.Vars, Consts: pkg.Consts, Funcs: pkg.Funcs, Body: &pkg.Body})
	for _, c := range pkg.Components {
		out = append(out, Owner{Pkg: pkg, Comp: c, Vars: c.Vars, Funcs: c.Funcs, Body: &c.Body})
	}
	seen := make(map[*Window]bool, len(pkg.Windows))
	addWin := func(w *Window) {
		if w == nil || seen[w] {
			return
		}
		seen[w] = true
		o := Owner{Pkg: pkg, Win: w, Body: &w.Children}
		o.Handlers = WindowHandlers(w)
		out = append(out, o)
	}
	for _, w := range pkg.Windows {
		addWin(w)
	}
	// A window a body renders is an *ir.Window statement in that body and
	// never reaches pkg.Windows. A found window is a leaf: its body is a
	// separate owner, searched in its own turn below, so descending here would
	// walk it a second time to reach what the worklist reaches anyway.
	search := func(stmts []Stmt) {
		_ = WalkStmts(stmts, func(s Stmt) error {
			if w, ok := s.(*NodeInst); ok && IsWindowNode(w) {
				addWin(w)
				return SkipDir
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
	// A worklist over `out` rather than a loop over a fixed list, because a
	// window found here may itself hold one. Writing a window directly inside
	// a window is a tree-membership error, but two routes through
	// #[tree.none] are not -- a treeless body that renders one, and a treeless
	// component's slot carrying one -- and passWindowNesting is the only guard
	// on those, in lowering. Every consumer that runs before it sees what it
	// will reject.
	//
	// Which is the whole of what this loop finds, so once that pass has run it
	// finds nothing and costs a walk of every window's body to say so. A
	// window's body is a page, so that is the package over again per call, and
	// the lowering asks 21 times after the pass and codegen more. Package.
	// WindowsFlat is the pass handing its answer forward.
	if !pkg.WindowsFlat {
		for i := 0; i < len(out); i++ {
			if out[i].Win != nil {
				search(out[i].Win.Children)
			}
		}
	}
	return out
}

// WindowHandles maps each window's `#id` binding to the window it declared.
//
// A window's id binds an *ir.Var like every other node's, so a reference to it
// carries the handle rather than the window -- which is what the two consumers
// that need the window back (the entry-window reference, and folding a prop
// read off the id) ask this for. Keyed by symbol and not by name, because two
// bodies may each write `#home` and mean two windows.
//
// A map rather than a lookup per reference: the fold asks for *every* node
// handle a program selects off, and the answer is nil for all the ordinary
// ones -- a button, a synthesized `__nN`. Walking the package to say so once
// per select is quadratic in a handle-heavy program, and the fold already runs
// several times over.
func WindowHandles(pkg *Package) map[*Var]*Window {
	if pkg == nil {
		return nil
	}
	var out map[*Var]*Window
	for _, o := range Owners(pkg) {
		if o.Win == nil || o.Win.Handle == nil {
			continue
		}
		if out == nil {
			out = map[*Var]*Window{}
		}
		out[o.Win.Handle] = o.Win
	}
	return out
}

// AllWindows is every window pkg holds, in the order Owners reports them: the
// ones registered at the root of a file, then the ones a body renders.
//
// A window is a statement wherever the root family reaches, so the field alone
// has never been the whole answer -- a `for` at the top of a file puts one in
// pkg.Body, and a component that names the root family puts one in its own.
// Both were lifted onto the field by a lowering pass; nothing lifts them now,
// so every consumer that wants the windows asks this.
func AllWindows(pkg *Package) []*Window {
	if pkg == nil {
		return nil
	}
	var out []*Window
	for _, o := range Owners(pkg) {
		if o.Win != nil {
			out = append(out, o.Win)
		}
	}
	return out
}

// WindowHandlers is every handler a window subscribes to: its @error, then its
// own events (`@close`), in that order so the temps a pass names off it keep
// their numbers. Nil for a window with none.
func WindowHandlers(w *Window) []*EventHandler {
	var out []*EventHandler
	if w.ErrorHandler != nil {
		out = append(out, w.ErrorHandler)
	}
	for i := range w.Handlers {
		out = append(out, &w.Handlers[i])
	}
	return out
}

// WindowRootName is the synthesized var a window's top-level render slots are
// parented to: `__root` for the first of ir.AllWindows, `__root<N>` for the
// Nth after it. One name per window, because a host that holds several
// windows puts each one's content in a box of its own, and a slot re-rendered
// into another window's box would move its widgets there.
func WindowRootName(pkg *Package, w *Window) string {
	for i, x := range AllWindows(pkg) {
		if x == w && i > 0 {
			return "__root" + strconv.Itoa(i)
		}
	}
	return "__root"
}

// IsRootSlotVarName reports whether name is a WindowRootName.
func IsRootSlotVarName(name string) bool {
	rest, ok := strings.CutPrefix(name, "__root")
	if !ok {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
