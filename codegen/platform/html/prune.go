package html

import (
	"strings"
)

// A page's script is written from the package, and the package holds every
// page's declarations: the factory for each page's runtime instances and each
// page's render slots and their accumulators. Written whole into each
// page, a site of N pages carried N pages' worth of them in every one, and its
// build time grew with N². So the declarations that belong to some page are
// marked as they are written, and pruneDecls keeps the ones this page names.
//
// By name, as linkSharedConsts links a shared const: what matters is whether
// the page's script can reach the binding, and a name the script never spells
// is one it cannot. A name that appears only inside a string is kept, which
// costs a declaration and never a reference to nothing.
const (
	declOpen  = "\x00<"
	declClose = "\x00>\x00"
)

// openDecl starts a declaration of name, which pruneDecls drops unless the
// rest of the script names it.
func openDecl(b *strings.Builder, name string) {
	b.WriteString(declOpen + name + "\x00")
}

func closeDecl(b *strings.Builder) {
	b.WriteString(declClose)
}

type scriptDecl struct {
	name, text string
}

// pruneDecls removes the marked declarations the rest of script does not name,
// to a fixed point: a kept declaration keeps whatever it names in turn.
func pruneDecls(script string) string {
	if !strings.Contains(script, declOpen) {
		return script
	}
	var (
		root  strings.Builder
		decls []scriptDecl
		order []int // -1 for a run of root text, else an index into decls
		texts []string
	)
	for {
		i := strings.Index(script, declOpen)
		if i < 0 {
			root.WriteString(script)
			texts, order = append(texts, script), append(order, -1)
			break
		}
		root.WriteString(script[:i])
		texts, order = append(texts, script[:i]), append(order, -1)
		rest := script[i+len(declOpen):]
		nameEnd := strings.IndexByte(rest, 0)
		end := strings.Index(rest, declClose)
		decls = append(decls, scriptDecl{name: rest[:nameEnd], text: rest[nameEnd+1 : end]})
		texts, order = append(texts, ""), append(order, len(decls)-1)
		script = rest[end+len(declClose):]
	}

	names := scriptIdents(root.String())
	kept := make([]bool, len(decls))
	for changed := true; changed; {
		changed = false
		for i, d := range decls {
			if !kept[i] && names[d.name] {
				kept[i] = true
				for n := range scriptIdents(d.text) {
					names[n] = true
				}
				changed = true
			}
		}
	}

	var out strings.Builder
	for i, at := range order {
		switch {
		case at < 0:
			out.WriteString(texts[i])
		case kept[at]:
			out.WriteString(decls[at].text)
		}
	}
	return out.String()
}
