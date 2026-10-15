package ir

import "testing"

// Sharing declarations copies a lambda's body, which is the expression's own,
// and leaves what that body names shared.
func TestCloneSharingDeclsCopiesALambdaBody(t *testing.T) {
	v := &Var{Name: "x"}
	body := &Func{Block: []Stmt{&Return{Value: &Ident{Name: "x", Sym: v}}}}
	lam := &Lambda{Func: body}

	cp := CloneExprSharingDecls(lam).(*Lambda)
	if cp.Func == body {
		t.Fatal("the lambda's body is shared")
	}
	if got := cp.Func.Block[0].(*Return).Value.(*Ident).Sym; got != v {
		t.Errorf("the var the body names was copied")
	}
}

// A declaration's package maps to the package's copy where the package is
// being copied, and is shared where it is not.
func TestCloneMapsADeclarationsPackage(t *testing.T) {
	lib := &Package{Path: "sngl:lib"}
	p := &Package{}
	own := &Var{Name: "own", Pkg: p}
	foreign := &Var{Name: "foreign", Pkg: lib}
	p.Vars = []*Var{own, foreign}

	cp := ClonePackage(p)
	if cp.Vars[0].Pkg != cp {
		t.Error("a declaration of the copied package does not name the copy")
	}
	if cp.Vars[1].Pkg != lib {
		t.Error("a declaration of another package does not name that package")
	}
}

// A clone for one target keeps that target's override bodies and empties the
// rest, on both axes.
func TestCloneForATargetKeepsItsOverrides(t *testing.T) {
	body := func() Override { return Override{Vars: []*Var{{Name: "v"}}} }
	p := &Package{}
	p.Components = []*Component{{
		Name: "c", Pkg: p,
		PlatformOverrides: map[string]Override{"html": body(), "fyne": body()},
		LanguageOverrides: map[string]Override{"go": body(), "js": body()},
	}}

	c := ClonePackageFor(p, "fyne", "go").Components[0]
	for name, m := range map[string]map[string]Override{"fyne": c.PlatformOverrides, "go": c.LanguageOverrides} {
		for key, o := range m {
			if kept := len(o.Vars) > 0; kept != (key == name) {
				t.Errorf("override %q kept=%v", key, kept)
			}
		}
	}
}
