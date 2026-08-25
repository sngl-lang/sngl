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
		// cgo imports come in via the c:// scheme (or are tagged
		// Foreign.Pkg=="C" on every Func). Skip either way: cgo enters
		// generated files via the cgo preamble, not the import block.
		if imp.AST != nil && strings.HasPrefix(imp.AST.Path, "c://") {
			continue
		}
		if strings.HasPrefix(imp.Native.ImportPath, "c://") {
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
	return out
}

// isCgoBundle reports whether every function on a NativeImport is tagged
// Foreign.Pkg=="C". Mirrors fyne's pre-existing detection: the c:// scheme
// is sometimes stripped before the import reaches codegen, so we look at
// the Funcs slice as a fallback.
func isCgoBundle(n *ir.NativeImport) bool {
	if n == nil || len(n.Funcs) == 0 {
		return false
	}
	for _, fn := range n.Funcs {
		if fn.Foreign.Pkg != "C" {
			return false
		}
	}
	return true
}
