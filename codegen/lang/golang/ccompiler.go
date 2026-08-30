package golang

import (
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitCHeader implements codegen.CCompiler for the Go/cgo translator.
// Returns the cgo preamble comment block and `import "C"` line.
// Returns empty string when imports is nil or empty.
func (t *Translator) EmitCHeader(imports []*ir.NativeImport) string {
	if len(imports) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("/*\n")

	// Collect linker and compiler flags from all imports (deduped, sorted).
	var ldflags, cflags []string
	seenLD := map[string]bool{}
	seenCF := map[string]bool{}
	for _, ni := range imports {
		for _, flag := range ni.LinkFlags {
			if strings.HasPrefix(flag, "-I") {
				if !seenCF[flag] {
					seenCF[flag] = true
					cflags = append(cflags, flag)
				}
			} else {
				if !seenLD[flag] {
					seenLD[flag] = true
					ldflags = append(ldflags, flag)
				}
			}
		}
	}
	sort.Strings(cflags)
	sort.Strings(ldflags)
	if len(cflags) > 0 {
		b.WriteString("#cgo CFLAGS: " + strings.Join(cflags, " ") + "\n")
	}
	if len(ldflags) > 0 {
		b.WriteString("#cgo LDFLAGS: " + strings.Join(ldflags, " ") + "\n")
	}

	// Emit #include for each import.
	for _, ni := range imports {
		headerPath := strings.TrimPrefix(ni.ImportPath, "c:")
		if after, ok := strings.CutPrefix(headerPath, "pkg:"); ok {
			libName := after
			b.WriteString("#include <" + inferPkgHeaderForEmit(libName) + ">\n")
		} else {
			b.WriteString("#include \"" + headerPath + "\"\n")
		}
	}

	b.WriteString("*/\nimport \"C\"\n")
	return b.String()
}

// inferPkgHeaderForEmit mirrors inferPkgHeader from codegen/scheme/c/importer.go.
// Kept here to avoid an import cycle (golang → scheme/c would be circular).
func inferPkgHeaderForEmit(libName string) string {
	base := libName
	if idx := strings.LastIndex(base, "-"); idx != -1 {
		base = base[:idx]
	}
	base = strings.TrimRight(base, "+")
	if len(base) == 0 {
		return libName + ".h"
	}
	base = strings.ToUpper(base[:1]) + base[1:]
	return base + "/" + base + ".h"
}
