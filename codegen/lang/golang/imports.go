package golang

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// BaseImport pairs a Go import path with its SNGL-declared alias (empty
// for default-aliased imports). Returned by BaseImports.
type BaseImport struct {
	Path  string
	Alias string
}

// BaseImports returns the Go imports a generated file always needs
// based on the package's structural shape: every non-cgo native import
// declared in the SNGL source (preserving its alias), plus the sngl-i18n
// runtime when the package contains i18n calls.
//
// Each platform's analyzeIR seeds its imports collection from this and
// then layers on platform-specific imports (time when timers exist,
// fmt+os when Alert.* is in use under that platform's AlertFunc, etc.).
//
// Resolves the orthogonality audit #6/#13 duplication: the pkg.Imports
// scan and the SnglI18nImportPath gate used to live in four places.
func BaseImports(pkg *ir.Package) []BaseImport {
	if pkg == nil {
		return nil
	}
	var out []BaseImport
	seen := map[string]bool{}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.Native.ImportPath == "" {
			continue
		}
		// cgo imports come in via the c: scheme, or with every function
		// naming C as its package. Skip either way: cgo enters generated files
		// through the cgo preamble, not the import block.
		if imp.AST != nil && strings.HasPrefix(imp.AST.Path, "c:") {
			continue
		}
		if strings.HasPrefix(imp.Native.ImportPath, "c:") {
			continue
		}
		if isCgoBundle(imp.Native) {
			continue
		}
		if seen[imp.Native.ImportPath] {
			continue
		}
		seen[imp.Native.ImportPath] = true
		out = append(out, BaseImport{Path: imp.Native.ImportPath, Alias: imp.Alias})
	}
	if PackageUsesI18n(pkg) && !seen[SnglI18nImportPath] {
		out = append(out, BaseImport{Path: SnglI18nImportPath})
	}
	// A `#[go.native]` *type* is spelled by IRTypeToGo, which is a free
	// function with no context to register an import from -- and unlike a
	// native call, a type may be the only mention its package gets: a var of
	// one becomes a field, a getter and a setter with nothing calling the
	// constructor beside them, which is what fyne's `*time.Timer` schedule is.
	// Collected here so all three Go platforms get it from the one place
	// each already asks.
	for _, path := range foreignTypeImports(pkg) {
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, BaseImport{Path: path})
	}
	return out
}

// foreignTypeImports is the import path of every `#[go.native]` type a value in
// pkg is declared as.
//
// Read off the *values* rather than off pkg.Structs, because a platform
// package's declaration is not in a program's struct list at all -- fyne's
// `Schedule` reaches a program only as the type of the `handle` var its timer
// override declares.
//
// The values are an owner's vars and its funcs' signatures, which is what a
// platform override writes today. A local of such a type inside a func body, or
// a const of one, reaches the file without its import and IRTypeToGo spells the
// name anyway -- generated Go that does not compile. Nothing produces either
// yet, so this is the shape of the gap rather than a live bug: an ir.LocalVar
// arm is what adding one costs.
func foreignTypeImports(pkg *ir.Package) []string {
	var out []string
	seen := map[string]bool{}
	// Through the type arguments and through a struct's fields: a
	// `list<Schedule>` spells the element in the field it becomes, and a
	// program struct holding a native-typed field spells that type in the Go
	// struct the backend emits -- with nothing else in the file naming the
	// package, a field being no call.
	walked := map[*ir.StructDef]bool{}
	var add func(t *ir.Type)
	add = func(t *ir.Type) {
		if t == nil {
			return
		}
		for _, e := range t.Elems {
			add(e)
		}
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok {
			return
		}
		if sd.Foreign.Name == "" || sd.Foreign.Marked {
			// The program's own struct: its fields are emitted with it. The
			// guard is for a struct that reaches itself through a list.
			if !walked[sd] {
				walked[sd] = true
				for _, f := range sd.Fields {
					if f != nil {
						add(f.Type)
					}
				}
			}
			return
		}
		path := sd.Foreign.Path
		if path == "" || path == "C" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, o := range ir.Owners(pkg) {
		for _, v := range o.Vars {
			if v != nil {
				add(v.Type)
			}
		}
		for _, fn := range o.Funcs {
			if fn == nil {
				continue
			}
			add(fn.Return)
			for _, pm := range fn.Params {
				if pm != nil {
					add(pm.Type)
				}
			}
		}
	}
	return out
}

// isCgoBundle reports whether every function on a NativeImport names C as its
// package. Mirrors fyne's pre-existing detection: the c: scheme is sometimes
// stripped before the import reaches codegen, so the functions are the
// fallback evidence.
func isCgoBundle(n *ir.NativeImport) bool {
	if n == nil || len(n.Funcs) == 0 {
		return false
	}
	for _, fn := range n.Funcs {
		if fn.Foreign.Path != "C" {
			return false
		}
	}
	return true
}
