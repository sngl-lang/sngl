package ir

import (
	"slices"
	"testing"
)

// A target package may restate an id another package declares, adding what
// a call needs on that target; the signature stays the declaring package's,
// whichever loads first, and a re-registration keeps what was added.
func TestTargetPackageRestatesAnIntrinsic(t *testing.T) {
	params := []*Param{{Name: "url"}}
	http := IntrinsicDef{Name: "http.get", Params: params, Pkg: "sngl:http", DeclaredAs: "get"}
	android := IntrinsicDef{Name: "http.get", Params: params, Pkg: "sngl:platform/android", DeclaredAs: "get", Permissions: []string{"android.permission.INTERNET"}}
	for _, order := range [][]IntrinsicDef{{http, android}, {android, http}, {http, android, http}} {
		b := NewBuiltins()
		for _, d := range order {
			b.RegisterIntrinsic(d)
		}
		got := b.LookupIntrinsic("http.get")
		if got.Pkg != "sngl:http" {
			t.Errorf("signature is %s's, want sngl:http's", got.Pkg)
		}
		if !slices.Equal(got.Permissions, []string{"android.permission.INTERNET"}) {
			t.Errorf("permissions %v, want android's", got.Permissions)
		}
	}
}

// Two packages that are not targets, or a restatement whose parameters do not
// match, still claim one id twice.
func TestIntrinsicDeclaredTwicePanics(t *testing.T) {
	for _, second := range []IntrinsicDef{
		{Name: "x.f", Params: []*Param{{Name: "a"}}, Pkg: "sngl:other", DeclaredAs: "f"},
		{Name: "x.f", Params: []*Param{{Name: "b"}}, Pkg: "sngl:platform/android", DeclaredAs: "f"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s declaring x.f again did not panic", second.Pkg)
				}
			}()
			b := NewBuiltins()
			b.RegisterIntrinsic(IntrinsicDef{Name: "x.f", Params: []*Param{{Name: "a"}}, Pkg: "sngl:x", DeclaredAs: "f"})
			b.RegisterIntrinsic(second)
		}()
	}
}
