package golang

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// BaseImports returns the Go import paths a generated file always needs
// based on the package's structural shape: every non-cgo native import
// declared in the SNGL source, plus the sngl-i18n runtime when the
// package contains i18n calls.
//
// Each platform's analyzeIR seeds its goImports map from this and then
// layers on platform-specific imports (time when timers exist, fmt+os
// when Alert.* is in use under that platform's AlertFunc, etc.).
//
// Resolves the orthogonality audit #6/#13 duplication: the pkg.Imports
// scan and the SnglI18nImportPath gate used to live in four places.
func BaseImports(pkg *ir.Package) []string {
	if pkg == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, imp := range pkg.Imports {
		if imp == nil || imp.Native == nil || imp.Native.ImportPath == "" {
			continue
		}
		if imp.AST != nil && strings.HasPrefix(imp.AST.Path, "c://") {
			continue
		}
		// Defensive: legacy callers ranged on Native.ImportPath; preserve
		// the c:// prefix skip here too in case AST is missing.
		if strings.HasPrefix(imp.Native.ImportPath, "c://") {
			continue
		}
		if seen[imp.Native.ImportPath] {
			continue
		}
		seen[imp.Native.ImportPath] = true
		out = append(out, imp.Native.ImportPath)
	}
	if PackageUsesI18n(pkg) && !seen[SnglI18nImportPath] {
		out = append(out, SnglI18nImportPath)
	}
	return out
}
