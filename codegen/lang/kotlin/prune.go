package kotlin

import (
	"regexp"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// PruneLibraryDataClasses removes the `data class` of each library struct in
// pkg that nothing else in src spells. The shake declares a payload struct for
// every handler that names one, and a Compose callback hands its handler the
// host's own value instead, so on android most of them are read by nothing.
func PruneLibraryDataClasses(src string, pkg *ir.Package) string {
	for changed := true; changed; {
		changed = false
		for _, sd := range pkg.Structs {
			if sd == nil || sd.Pkg == "" {
				continue
			}
			if out, ok := dropDecl(src, "data class "+exportName(sd.Name)+"(", "\n)\n", exportName(sd.Name)); ok {
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
