package golang

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitCHeader implements codegen.CCompiler for the Go/cgo translator.
// Returns the cgo preamble comment block and `import "C"` line for the
// headers the program's c.link directives name, or "" when there are none.
// <stdlib.h> is always among them, as in gtk4's preamble: lowering frees
// every `C.CString` with `C.free`.
func (t *Translator) EmitCHeader(links []*ir.CLink) string {
	if len(links) == 0 {
		return ""
	}
	if !slices.ContainsFunc(links, func(l *ir.CLink) bool { return l.Include == "stdlib.h" }) {
		links = append(slices.Clip(links), &ir.CLink{Include: "stdlib.h", System: true})
	}
	return "/*\n" + CPreambleLines(links) + "*/\nimport \"C\"\n"
}

// CPreambleLines is what the c.link directives put in a cgo preamble: the
// #cgo flags and the #include lines, one per line, for a target that writes a
// preamble of its own around them.
func CPreambleLines(links []*ir.CLink) string {
	// Each flag once, in the order the headers first name it: a linker
	// resolves a library against the ones after it, so pkg-config's order is
	// meaningful and sorting would break it.
	var cflags, ldflags []string
	for _, l := range links {
		for _, f := range l.CFlags {
			if !slices.Contains(cflags, f) {
				cflags = append(cflags, f)
			}
		}
		for _, f := range l.LDFlags {
			if !slices.Contains(ldflags, f) {
				ldflags = append(ldflags, f)
			}
		}
	}

	var b strings.Builder
	if len(cflags) > 0 {
		b.WriteString("#cgo CFLAGS: " + strings.Join(cflags, " ") + "\n")
	}
	if len(ldflags) > 0 {
		b.WriteString("#cgo LDFLAGS: " + strings.Join(ldflags, " ") + "\n")
	}
	var seen []string
	for _, l := range links {
		inc := `"` + l.Include + `"`
		if l.System {
			inc = "<" + l.Include + ">"
		}
		if slices.Contains(seen, inc) {
			continue
		}
		seen = append(seen, inc)
		b.WriteString("#include " + inc + "\n")
	}
	return b.String()
}
