package codegen

import "testing"

func TestImportSpecComparable(t *testing.T) {
	// Used as map key in CodeWriter; must be comparable.
	a := ImportSpec{Path: "fmt", Kind: ImportNative}
	b := ImportSpec{Path: "fmt", Kind: ImportNative}
	m := map[ImportSpec]struct{}{a: {}}
	if _, ok := m[b]; !ok {
		t.Fatal("equal specs should hash to same key")
	}
}

func TestImportKindString(t *testing.T) {
	cases := []struct {
		k    ImportKind
		want string
	}{
		{ImportNative, "native"},
		{ImportStdlibRuntime, "stdlib-runtime"},
		{ImportCgo, "cgo"},
		{ImportEsModule, "esmodule"},
		{ImportWasmExtern, "wasm-extern"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("ImportKind(%d).String() = %q want %q", c.k, got, c.want)
		}
	}
}
