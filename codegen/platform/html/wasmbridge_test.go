package html

import (
	"sync"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestContainsRef(t *testing.T) {
	cases := []struct {
		name string
		in   *ir.Type
		want bool
	}{
		{"nil", nil, false},
		{"string", ir.TypString, false},
		{"list-string", ir.ListOf(ir.TypString), false},
		{"ref-string", ir.RefOf(ir.TypString), true},
		{"list-ref", ir.ListOf(ir.RefOf(ir.TypInt)), true},
		{"option-list-ref", &ir.Type{Kind: ir.TypeOption, Elems: []*ir.Type{ir.ListOf(ir.RefOf(ir.TypBool))}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := containsRef(c.in); got != c.want {
				t.Errorf("containsRef(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestWASMTypeHint_RefRejected(t *testing.T) {
	if got := wasmTypeHint(ir.RefOf(ir.TypString)); got != "" {
		t.Errorf("ref<string> should produce empty hint (raw passthrough disabled), got %q", got)
	}
}

// fakeScheme exposes a hand-built ir.NativeImport so tests can drive
// collectWASMPackages without going through a real language toolchain.
type fakeScheme struct {
	name    string
	imports map[string]*ir.NativeImport
}

func (f *fakeScheme) Scheme() string { return f.name }
func (f *fakeScheme) Resolve(uri, _ string) (*ir.NativeImport, error) {
	return f.imports[uri], nil
}

var fakeSchemeOnce sync.Once

func registerFakeScheme(t *testing.T, ni *ir.NativeImport, uri string) {
	t.Helper()
	fakeSchemeOnce.Do(func() {
		codegen.RegisterScheme(&fakeScheme{
			name:    "wasmbridge-test",
			imports: map[string]*ir.NativeImport{uri: ni},
		})
	})
	// Subsequent tests in the same process reuse the registered scheme;
	// swap in the new payload so each test sees its own decls.
	si := codegen.LookupScheme("wasmbridge-test").(*fakeScheme)
	si.imports[uri] = ni
}

func TestCollectWASMPackages_SkipsRefSignatures(t *testing.T) {
	const uri = "ref-pkg"

	keep := &ir.Func{
		Name:   "Plain",
		Params: []*ir.Param{{Name: "s", Type: ir.TypString}},
		Return: ir.TypString,
		Purity: ir.PurityUnknown,
	}
	refParam := &ir.Func{
		Name:   "TakesRef",
		Params: []*ir.Param{{Name: "r", Type: ir.RefOf(ir.TypInt)}},
		Return: nil,
		Purity: ir.PurityUnknown,
	}
	refReturn := &ir.Func{
		Name:   "ReturnsRef",
		Return: ir.RefOf(ir.TypString),
		Purity: ir.PurityUnknown,
	}
	nestedRef := &ir.Func{
		Name:   "TakesListOfRef",
		Params: []*ir.Param{{Name: "rs", Type: ir.ListOf(ir.RefOf(ir.TypBool))}},
		Purity: ir.PurityUnknown,
	}

	ni := &ir.NativeImport{
		ImportPath: "example.com/refpkg",
		Funcs:      []*ir.Func{keep, refParam, refReturn, nestedRef},
	}
	registerFakeScheme(t, ni, uri)

	// All four funcs are forced client-side via html.frontend, so they are
	// WASM-eligible; the ref-bearing ones must still be filtered out. Wire
	// the call funcs' Foreign.Path to the import path so scanPlacement
	// recognizes them.
	frontendUse := func(name string) ir.Stmt {
		return &ir.CallStmt{Call: intrinsicCall("html.frontend",
			&ir.Call{Func: &ir.Func{Foreign: ir.Foreign{Path: "example.com/refpkg"}, Name: name}})}
	}
	pkg := &ir.Package{
		Imports: []*ir.Import{{
			AST:    &ast.Import{Path: "wasmbridge-test:" + uri},
			Path:   "wasmbridge-test:" + uri,
			Alias:  "rp",
			Native: ni,
		}},
		Components: []*ir.Component{{
			Name: "main",
			Body: []ir.Stmt{
				frontendUse("Plain"),
				frontendUse("TakesRef"),
				frontendUse("ReturnsRef"),
				frontendUse("TakesListOfRef"),
			},
		}},
	}

	got := collectWASMPackages(pkg, scanPlacement(pkg).frontend, nil, ".")
	if len(got) != 1 {
		t.Fatalf("expected 1 wasm package, got %d", len(got))
	}
	names := []string{}
	for _, f := range got[0].funcs {
		names = append(names, f.Name)
	}
	if len(names) != 1 || names[0] != "Plain" {
		t.Errorf("ref-bearing funcs leaked through bridge: got %v, want [Plain]", names)
	}
}

// TestFrontendWasmOptIn pins the Phase 5 contract: WASM is opt-in. A bare
// (default-backend) non-js: call ships NO WASM; only a call forced
// client-side via html.frontend pulls the package into the WASM bridge.
func TestFrontendWasmOptIn(t *testing.T) {
	const uri = "optin-pkg"

	persist := &ir.Func{
		Name:   "Persist",
		Params: []*ir.Param{{Name: "n", Type: ir.TypInt}},
		Return: ir.TypInt,
		Purity: ir.PurityUnknown,
	}
	ni := &ir.NativeImport{
		ImportPath: "example.com/optin",
		Funcs:      []*ir.Func{persist},
	}
	registerFakeScheme(t, ni, uri)

	imp := &ir.Import{
		AST:    &ast.Import{Path: "wasmbridge-test:" + uri},
		Path:   "wasmbridge-test:" + uri,
		Alias:  "op",
		Native: ni,
	}
	call := func() *ir.Call {
		return &ir.Call{Func: &ir.Func{Foreign: ir.Foreign{Path: "example.com/optin"}, Name: "Persist"},
			Args: []ir.CallArg{{Value: &ir.Literal{}}}}
	}

	t.Run("bare backend call ships no WASM", func(t *testing.T) {
		pkg := &ir.Package{
			Imports: []*ir.Import{imp},
			Components: []*ir.Component{{Name: "main", Body: []ir.Stmt{
				&ir.CallStmt{Call: call()},
			}}},
		}
		if got := collectWASMPackages(pkg, scanPlacement(pkg).frontend, nil, "."); len(got) != 0 {
			t.Fatalf("bare go: call must ship no WASM, got %d packages", len(got))
		}
	})

	t.Run("html.frontend call ships WASM", func(t *testing.T) {
		pkg := &ir.Package{
			Imports: []*ir.Import{imp},
			Components: []*ir.Component{{Name: "main", Body: []ir.Stmt{
				&ir.CallStmt{Call: intrinsicCall("html.frontend", call())},
			}}},
		}
		got := collectWASMPackages(pkg, scanPlacement(pkg).frontend, nil, ".")
		if len(got) != 1 || len(got[0].funcs) != 1 || got[0].funcs[0].Name != "Persist" {
			t.Fatalf("html.frontend(go_call) must ship the package's WASM, got %+v", got)
		}
	})
}
