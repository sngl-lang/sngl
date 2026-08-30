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
		if len(p.Stmts) != 1 {
			t.Errorf("package owner Stmts = %d; want the package body", len(p.Stmts))
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
