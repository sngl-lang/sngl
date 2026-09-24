package markdown

import (
	"fmt"
	"strings"

	snglast "git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// importDecl is one import a live fence wrote, as the line the generated
// package holds it under.
type importDecl struct {
	alias string
	path  string
}

func (i importDecl) line() string {
	if i.alias == "" {
		return fmt.Sprintf("import %q", i.path)
	}
	return fmt.Sprintf("import %s %q", i.alias, i.path)
}

// liveSource parses a live fence and splits it into the imports the generated
// package hoists and the source that is left.
//
// The imports are hoisted because a fence is spliced into a component body,
// and an import may only be written at the root of a file. They are kept **as
// written**: rewriting an alias means resolving every name under it, which is
// checking, and this importer emits source for the checker rather than doing
// its work. Redundant ones collapse -- the same alias for the same path,
// twice, is one import -- and a genuine conflict is left to reach the checker,
// where the generated package fails with the diagnostic a program writing two
// such imports gets.
//
// An `output` block is dropped. It names the targets a *build* has, and a
// document is imported into a program that has named its own.
func (e *emitter) liveSource(src, where string) ([]importDecl, string) {
	doc, err := parser.Parse(e.doc.name, []byte(src))
	if err != nil {
		e.fail("%s: %v", where, err)
		return nil, ""
	}
	var imports []importDecl
	kept := make([]snglast.Stmt, 0, len(doc.Stmts))
	for _, s := range doc.Stmts {
		switch s := s.(type) {
		case *snglast.Import:
			imports = append(imports, importDecl{alias: s.Alias, path: s.Path})
		case *snglast.VisualNode:
			if s.TargetName() == "output" {
				continue
			}
			kept = append(kept, s)
		default:
			kept = append(kept, s)
		}
	}
	rest := parser.Format(&snglast.Document{Stmts: kept, BlankLines: doc.BlankLines})
	return imports, strings.TrimRight(rest, "\n")
}

// addImports records a fence's imports on the package, collapsing an exact
// repeat of one already held.
func (d *docState) addImports(imports []importDecl) {
	for _, imp := range imports {
		if d.seenImports[imp] {
			continue
		}
		d.seenImports[imp] = true
		d.imports = append(d.imports, imp)
	}
}

// emitLines writes an already-formatted block of SNGL source at this
// emitter's indentation.
func (e *emitter) emitLines(src string) {
	for line := range strings.SplitSeq(src, "\n") {
		e.line(line)
	}
}
