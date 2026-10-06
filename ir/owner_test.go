package ir

import "testing"

// Owners is the answer to "which declarations can own state", and the whole
// point of having one is that a consumer cannot get a different one. So this
// asserts both kinds are reported, each carrying its own vars and its own
// body.
func TestOwnersReportsEveryKind(t *testing.T) {
	pkgVar := &Var{Name: "pkgVar"}
	pkgConst := &Var{Name: "pkgConst", IsConst: true}
	compVar := &Var{Name: "compVar"}

	comp := &Component{Name: "counter", Vars: []*Var{compVar}, Body: []Stmt{&Return{}}}
	pkg := &Package{
		Vars:       []*Var{pkgVar},
		Consts:     []*Var{pkgConst},
		Body:       []Stmt{&Return{}},
		Components: []*Component{comp},
	}

	got := Owners(pkg)
	if len(got) != 2 {
		t.Fatalf("Owners returned %d owners; want 2 (package, component)", len(got))
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
}

func TestOwnersOfNilPackage(t *testing.T) {
	if got := Owners(nil); got != nil {
		t.Errorf("Owners(nil) = %v; want nil", got)
	}
}

// Vars reads as a snapshot and writes through AddVars, which is the half a
// consumer gets wrong silently: appending to Owner.Vars reaches a copy.
func TestOwnerAddVarsReachesTheDeclaration(t *testing.T) {
	comp := &Component{Name: "counter"}
	pkg := &Package{Components: []*Component{comp}}

	for _, o := range Owners(pkg) {
		o.AddVars(&Var{Name: "added"})
		// The snapshot stays empty, which is the trap AddVars exists for.
		if len(o.Vars) != 0 {
			t.Errorf("owner %q: AddVars wrote into the snapshot", o.Name())
		}
	}

	if len(comp.Vars) != 1 || comp.Vars[0].Name != "added" {
		t.Errorf("component vars = %v; want the one added var", comp.Vars)
	}
	if len(pkg.Vars) != 1 {
		t.Errorf("package vars = %d; want the one added var", len(pkg.Vars))
	}
}
