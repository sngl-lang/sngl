package cgogen

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// goImport is the package the marks are declared in.
const goImport = "sngl:language/go"

// Rewrite is src, a .sngl file, with each of marks (all of them in this
// file) written beside the `cnative` mark of the declaration it is for: a
// `go.native` already there replaced, so running it again changes nothing,
// and one nothing marks any longer taken out. The file imports
// sngl:language/go where it did not.
func Rewrite(src []byte, marks []Mark) ([]byte, error) {
	lines := strings.Split(string(src), "\n")
	alias := importAlias(lines)
	if alias == "" {
		alias = "go"
	}
	byLine := map[int]Mark{}
	for _, m := range marks {
		byLine[m.Pos.Line] = m
	}
	var out []string
	// Each declaration's marks are the lines above it that start `#[`;
	// they are written out when the declaration is reached. A declaration's
	// position is its first mark's line.
	var attrs []string
	first := 0
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#[") {
			if len(attrs) == 0 {
				first = i + 1
			}
			attrs = append(attrs, l)
			continue
		}
		if len(attrs) > 0 {
			attrs = rewriteAttrs(attrs, alias, byLine[first])
			out = append(out, attrs...)
			attrs = nil
		}
		out = append(out, l)
	}
	out = append(out, attrs...)
	if importAlias(lines) == "" && len(marks) > 0 {
		out = addImport(out, alias)
	}
	return []byte(strings.Join(out, "\n")), nil
}

// rewriteAttrs is a declaration's marks with the `go.native` m says, beside
// its `cnative` one, and without the one it had where m is zero.
func rewriteAttrs(attrs []string, alias string, m Mark) []string {
	var out []string
	cnative := -1
	// What the mark says beside the wrapper's name -- `schedules` on a timer
	// whose callback the loop runs -- is the declaration's, kept.
	flags := ""
	for _, a := range attrs {
		if strings.Contains(a, alias+".native(") && hasCNative(attrs) {
			if f := nativeFlags.FindStringSubmatch(a); f != nil {
				flags = f[1]
			}
			continue
		}
		if strings.Contains(a, "cnative(") {
			cnative = len(out)
		}
		out = append(out, a)
	}
	if m.Name == "" || cnative < 0 {
		return out
	}
	indent := out[cnative][:len(out[cnative])-len(strings.TrimLeft(out[cnative], " \t"))]
	mark := fmt.Sprintf("%s#[%s.native(%q, %q%s)]", indent, alias, m.Path, m.Name, flags)
	return slices.Insert(out, cnative+1, mark)
}

// nativeFlags reads what a `#[go.native(path, name, …)]` says after the name.
var nativeFlags = regexp.MustCompile(`\.native\("[^"]*",\s*"[^"]*"((?:,\s*\w+)*)\)\]`)

func hasCNative(attrs []string) bool {
	return slices.ContainsFunc(attrs, func(a string) bool { return strings.Contains(a, "cnative(") })
}

// importAlias is the name lines import sngl:language/go as, or "".
func importAlias(lines []string) string {
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 3 && f[0] == "import" && f[2] == fmt.Sprintf("%q", goImport) {
			return f[1]
		}
	}
	return ""
}

// addImport adds sngl:language/go as alias after the last import of lines.
func addImport(lines []string, alias string) []string {
	last := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "import ") {
			last = i
		}
	}
	return slices.Insert(lines, last+1, fmt.Sprintf("import %s %q", alias, goImport))
}

// sortMarks orders marks by file and line, each once: two copies of one
// package reached through two imports mark one declaration twice.
func sortMarks(marks []Mark) []Mark {
	slices.SortFunc(marks, func(a, b Mark) int {
		return cmp.Or(cmp.Compare(a.Pos.File, b.Pos.File), cmp.Compare(a.Pos.Line, b.Pos.Line))
	})
	return slices.CompactFunc(marks, func(a, b Mark) bool { return a.Pos.File == b.Pos.File && a.Pos.Line == b.Pos.Line })
}
