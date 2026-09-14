package ir

import "testing"

// Owners is the answer to "which declarations can own state", and the whole
// point of having one is that a consumer cannot get a different one. So this
// asserts the three kinds are all reported, each carrying its own vars and its
// own body -- a window missing from this list is exactly the shape of #133.
func TestOwnersReportsEveryKind(t *testing.T) {
	pkgVar := &Var{Name: "pkgVar"}
	pkgConst := &Var{Name: "pkgConst", IsConst: true}
	compVar := &Var{Name: "compVar"}
	winVar := &Var{Name: "winVar"}

	comp := &Component{Name: "counter", Vars: []*Var{compVar}, Body: []Stmt{&Return{}}}
	win := &Window{Name: "home", Vars: []*Var{winVar}, Body: []Stmt{&Return{}}}
	pkg := &Package{
		Vars:       []*Var{pkgVar},
		Consts:     []*Var{pkgConst},
		Body:       []Stmt{&Return{}},
		Components: []*Component{comp},
		Windows:    []*Window{win},
	}

	got := Owners(pkg)
	if len(got) != 3 {
		t.Fatalf("Owners returned %d owners; want 3 (package, component, window)", len(got))
	}

	if p := got[0]; !p.IsPackage() || p.Name() != "" {
		t.Errorf("owner 0 = %q (IsPackage=%v); want the package", p.Name(), p.IsPackage())
	} else {
		if len(p.Vars) != 1 || p.Vars[0] != pkgVar {
			t.Errorf("package owner Vars = %v; want just pkgVar", p.Vars)
		}
		// Consts stay apart from Vars: consumers disagree about whether a
		// const is a model field, and merging them here would decide for them.
		if len(p.Consts) != 1 || p.Consts[0] != pkgConst {
			t.Errorf("package owner Consts = %v; want just pkgConst", p.Consts)
		}
		if len(p.Stmts()) != 1 {
			t.Errorf("package owner Stmts = %d; want the package body", len(p.Stmts()))
		}
	}

	if c := got[1]; c.Comp != comp || c.IsPackage() || c.Name() != "counter" {
		t.Errorf("owner 1 = %q; want the component", c.Name())
	} else if len(c.Vars) != 1 || c.Vars[0] != compVar {
		t.Errorf("component owner Vars = %v; want just compVar", c.Vars)
	}

	if w := got[2]; w.Win != win || w.IsPackage() || w.Name() != "home" {
		t.Errorf("owner 2 = %q; want the window", w.Name())
	} else if len(w.Vars) != 1 || w.Vars[0] != winVar {
		t.Errorf("window owner Vars = %v; want just winVar", w.Vars)
	}
}

func TestOwnersOfNilPackage(t *testing.T) {
	if got := Owners(nil); got != nil {
		t.Errorf("Owners(nil) = %v; want nil", got)
	}
}

// A window is a statement wherever the root tree reaches, and the package body
// is one of those places: `if ship { window #a {} }` at the root of a file
// leaves the window there until passRootWindow lifts it, and every consumer
// that runs before that pass -- the checker's four -- asks Owners.
func TestOwnersFindsWindowsInAnyBody(t *testing.T) {
	inPkg := &Window{Name: "fromPkgBody"}
	inComp := &Window{Name: "fromCompBody"}
	inWin := &Window{Name: "fromWindowBody"}
	listed := &Window{Name: "listed", Body: []Stmt{inWin}}

	pkg := &Package{
		Body:       []Stmt{&If{Body: []Stmt{inPkg}}},
		Components: []*Component{{Name: "root", Body: []Stmt{inComp}}},
		Windows:    []*Window{listed},
	}

	found := map[string]bool{}
	for _, o := range Owners(pkg) {
		if o.Win != nil {
			found[o.Win.Name] = true
		}
	}
	for _, want := range []string{"listed", "fromPkgBody", "fromCompBody", "fromWindowBody"} {
		if !found[want] {
			t.Errorf("Owners did not report window %q", want)
		}
	}
}

// A window's @error is an imperative block it owns, and it reaches a consumer
// the same way its funcs and timers do -- collected by hand beside the
// enumeration, it was the half of blocks.go's window arm that went missing.
func TestOwnersCarriesTheWindowErrorHandler(t *testing.T) {
	h := &EventHandler{Name: "error", Func: &Func{}}
	pkg := &Package{Windows: []*Window{{Name: "home", ErrorHandler: h}}}

	got := Owners(pkg)
	if len(got) != 2 {
		t.Fatalf("Owners returned %d owners; want 2", len(got))
	}
	if len(got[1].Handlers) != 1 || got[1].Handlers[0] != h {
		t.Errorf("window owner Handlers = %v; want the @error handler", got[1].Handlers)
	}
	if len(got[0].Handlers) != 0 {
		t.Errorf("package owner Handlers = %v; want none", got[0].Handlers)
	}
}
