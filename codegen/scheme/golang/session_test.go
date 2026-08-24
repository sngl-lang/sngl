package golang

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// writeGoModule lays down a self-contained module whose package p declares
// Value() with the given return type, and returns the module directory.
func writeGoModule(t *testing.T, returnType, returnExpr string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/edited\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoPackage(t, dir, returnType, returnExpr)
	return dir
}

func writeGoPackage(t *testing.T, dir, returnType, returnExpr string) {
	t.Helper()
	src := "package p\n\n// Value is the declaration under test.\nfunc Value() " + returnType + " { return " + returnExpr + " }\n"
	if err := os.WriteFile(filepath.Join(dir, "p", "p.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func valueReturnKind(t *testing.T, ni *ir.NativeImport) ir.TypeKind {
	t.Helper()
	for _, f := range ni.Funcs {
		if f.Name == "Value" {
			if f.Return == nil {
				t.Fatal("Value has no return type")
			}
			return f.Return.Kind
		}
	}
	t.Fatal("no func Value in resolved import")
	return ir.TypeKind(0)
}

// A session is one compilation's view of the tree: it loads a package once,
// and a later compilation gets its own session and sees the file as it is now.
// The process-global memo this replaced pinned the first load forever, so
// `sngl preview` and `sngl doc --browse` type-checked every reload against
// signatures from the first compile.
func TestGoImporterSessionScopesToOneCompilation(t *testing.T) {
	const uri = "go://example.com/edited/p"
	dir := writeGoModule(t, "string", `""`)

	first := (&GoImporter{}).NewSession()
	ni, err := first.Resolve(uri, dir)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if got := valueReturnKind(t, ni); got != ir.TypeString {
		t.Fatalf("Value returns %v, want string", got)
	}

	writeGoPackage(t, dir, "int", "0")

	// Within the compilation the snapshot holds: one load, reused.
	ni, err = first.Resolve(uri, dir)
	if err != nil {
		t.Fatalf("second resolve in same session: %v", err)
	}
	if got := valueReturnKind(t, ni); got != ir.TypeString {
		t.Errorf("same session saw %v after the edit, want the loaded string", got)
	}
	if n := len(first.(*GoImporter).loaded.pkgs); n != 1 {
		t.Errorf("session loaded %d packages, want 1", n)
	}

	// A later compilation is a new session, and sees the file as it now is.
	second := (&GoImporter{}).NewSession()
	ni, err = second.Resolve(uri, dir)
	if err != nil {
		t.Fatalf("resolve in second session: %v", err)
	}
	if got := valueReturnKind(t, ni); got != ir.TypeInt {
		t.Errorf("new session saw %v, want int — the edit is invisible", got)
	}
}

// The registered importer is a process-wide singleton, so it must remember
// nothing at all.
func TestGoImporterSingletonCachesNothing(t *testing.T) {
	const uri = "go://example.com/edited/p"
	dir := writeGoModule(t, "string", `""`)

	singleton := &GoImporter{}
	if _, err := singleton.Resolve(uri, dir); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if singleton.loaded != nil {
		t.Fatal("the registered importer holds loaded packages")
	}

	writeGoPackage(t, dir, "int", "0")
	ni, err := singleton.Resolve(uri, dir)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got := valueReturnKind(t, ni); got != ir.TypeInt {
		t.Errorf("singleton saw %v after the edit, want int", got)
	}
}

// A failed load is not remembered: the file may be mid-edit, and the next
// resolve must get the chance to succeed.
func TestGoImporterSessionDoesNotCacheFailure(t *testing.T) {
	const uri = "go://example.com/edited/p"
	dir := writeGoModule(t, "string", `""`)
	if err := os.WriteFile(filepath.Join(dir, "p", "p.go"), []byte("package p\n\nfunc Value() string { return\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := (&GoImporter{}).NewSession()
	if _, err := sess.Resolve(uri, dir); err == nil {
		t.Fatal("resolve of a broken package succeeded")
	}
	if n := len(sess.(*GoImporter).loaded.pkgs); n != 0 {
		t.Fatalf("session cached %d packages from a failed load", n)
	}

	writeGoPackage(t, dir, "int", "0")
	ni, err := sess.Resolve(uri, dir)
	if err != nil {
		t.Fatalf("resolve after the fix: %v", err)
	}
	if got := valueReturnKind(t, ni); got != ir.TypeInt {
		t.Errorf("Value returns %v, want int", got)
	}
}
