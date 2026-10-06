package golang

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitCHeader implements codegen.CCompiler for the Go/cgo translator.
// Returns the cgo preamble comment block and `import "C"` line for the
// headers the program's c.link directives name, or "" when there are none.
func (t *Translator) EmitCHeader(links []*ir.CLink) string {
	if len(links) == 0 {
		return ""
	}

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
	b.WriteString("/*\n")
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
	b.WriteString("*/\nimport \"C\"\n")
	return b.String()
}
