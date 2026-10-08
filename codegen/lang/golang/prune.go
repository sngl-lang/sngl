package golang

import (
	"regexp"
	"strings"

	"duckfam.us/sngl/ir"
)

// PayloadPruneCandidates are the library structs the shake declared in pkg
// because a handler or a test names one as its payload, less those a test
// file constructs. Whether the emitted Go reads one is the emitter's answer:
// a fyne or gtk4 handler hands its host callback's own value to the body in
// place of `e.value`, and the ChangeEvent declared for it is read by nothing.
func PayloadPruneCandidates(pkg *ir.Package, testPayloads map[string]*ir.Type) []string {
	keep := map[string]bool{}
	for _, t := range testPayloads {
		if sd, ok := t.Decl.(*ir.StructDef); ok && sd != nil {
			keep[sd.Name] = true
		}
	}
	var out []string
	for _, sd := range pkg.Structs {
		if sd != nil && sd.Pkg != "" && !keep[sd.Name] {
			out = append(out, ExportName(sd.Name))
		}
	}
	return out
}

// PruneStructDecls removes each `type <name> struct { … }` of names that
// nothing else in src spells.
func PruneStructDecls(src string, names []string) string {
	for changed := true; changed; {
		changed = false
		for _, name := range names {
			if out, ok := dropDecl(src, "type "+name+" struct {", "\n}\n", name); ok {
				src, changed = out, true
			}
		}
	}
	return src
}

// dropDecl removes the declaration running from open to close when nothing
// outside it names name.
func dropDecl(src, open, close, name string) (string, bool) {
	start := strings.Index(src, open)
	if start < 0 || (start > 0 && src[start-1] != '\n') {
		return src, false
	}
	end := strings.Index(src[start:], close)
	if end < 0 {
		return src, false
	}
	end += start + len(close)
	for end < len(src) && src[end] == '\n' {
		end++
	}
	rest := src[:start] + src[end:]
	if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(rest) {
		return src, false
	}
	return rest, true
}
