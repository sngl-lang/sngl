package ir

import (
	"strings"
	"testing"
)

func frozenPackage() (*Package, *StructDef) {
	p := &Package{Path: "sngl:frozen", Symbols: &SymbolTable{Root: &Scope{Symbols: map[string]Symbol{}, IsPackageRoot: true}}}
	sd := &StructDef{Name: "Thing", Pkg: p}
	p.Structs = append(p.Structs, sd)
	p.Symbols.Root.Symbols[sd.Name] = sd
	Freeze(p)
	return p, sd
}

func wantFrozenPanic(t *testing.T, what string, write func()) {
	t.Helper()
	defer func() {
		r := recover()
		if msg, _ := r.(string); !strings.Contains(msg, "frozen") {
			t.Errorf("%s: got %v, want a panic naming the frozen package", what, r)
		}
	}()
	write()
}

// A package a later check shares is written by none of the known writers.
func TestFrozenPackageRefusesWrites(t *testing.T) {
	p, sd := frozenPackage()
	wantFrozenPanic(t, "a new binding", func() {
		_ = p.Symbols.Root.Declare(&Var{Name: "fresh"})
	})
	wantFrozenPanic(t, "a method", func() {
		_ = AttachMethod(p.Symbols.Root, "Thing", &Func{Name: "m"})
	})
	wantFrozenPanic(t, "a library record", func() {
		NewBuiltins().RegisterStruct(&StructDef{Name: "list", Pkg: p, Builtin: BuiltinList})
	})
	wantFrozenPanic(t, "a specialization", func() {
		comp := &Component{Name: "c", Pkg: p, PlatformOverrides: map[string]Override{"x": {}}}
		specializeComp(comp, target{platform: "x"}, false)
	})

	// Re-binding what is already bound writes nothing, and a scope inside
	// the package is not the package's.
	if err := p.Symbols.Root.Declare(sd); err != nil {
		t.Errorf("re-declaring a bound symbol: %v", err)
	}
	_ = NewScope(p.Symbols.Root).Declare(&Var{Name: "local"})
}
