package lspcore

import (
	"regexp"
	"sort"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/ir"
)

// docScope is the library packages a document's imports bring into it.
// Completion offers what the file can write: a component from a package it has
// not imported would not compile, and one it imported under an alias is only
// spellable qualified.
type docScope struct {
	// dot holds the URIs whose declarations are unqualified here.
	dot []string
	// alias maps the identifier a qualified import bound to its URI.
	alias map[string]string
}

// builtinPkg is ambient: every file sees it without importing it.
const builtinPkg = "builtin"

// styleStruct is the type a `style=` prop takes, and the package that declares
// it. Its fields are the style vocabulary.
const (
	stylePkg  = "ui"
	styleType = "Style"
)

func scopeOf(content string, doc *ast.Document) docScope {
	s := docScope{dot: []string{builtinPkg}, alias: map[string]string{}}
	for _, imp := range docImports(content, doc) {
		// Only a library package has a schema to read; a directory or a
		// foreign-scheme import is resolved by the checker's own resolver,
		// which the editor does not run.
		scheme, uri := checker.ParseScheme(imp.Path)
		if scheme != "sngl" {
			continue
		}
		if imp.IsDot() {
			s.dot = append(s.dot, uri)
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = checker.NamespaceFromPath(imp.Path)
		}
		s.alias[alias] = uri
	}
	return s
}

// docImports is the file's imports, from the parse where there is one and from
// the text where there is not.
//
// An editor asks for completion mid-edit, when the buffer usually does not
// parse -- and Analyze hands back a document with no statements at all, not a
// partial one. The imports decide what is in scope, so reading them only off
// the AST loses them exactly when they are needed. They are line-oriented and
// sit at the top of the file, which is what makes scanning for them sound.
func docImports(content string, doc *ast.Document) []*ast.Import {
	var out []*ast.Import
	if doc != nil {
		for _, stmt := range doc.Stmts {
			if imp, ok := stmt.(*ast.Import); ok {
				out = append(out, imp)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, m := range importLine.FindAllStringSubmatch(content, -1) {
		out = append(out, &ast.Import{Alias: m[1], Path: m[2]})
	}
	return out
}

// An import line: the keyword, an optional alias (`.` or an identifier), and
// the quoted path. A `=>` replacement follows the path and does not change
// what the name binds to.
var importLine = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+(?:(\.|[A-Za-z_][A-Za-z0-9_]*)[ \t]+)?"([^"]+)"`)

// components offers every component the file can write unqualified.
func (s docScope) components() []CompletionItem {
	var items []CompletionItem
	for _, uri := range s.dot {
		for name, schema := range checker.PackageSchema(uri) {
			items = append(items, CompletionItem{
				Label:            name,
				Kind:             CIKClass,
				Detail:           "sngl:" + uri,
				Documentation:    FirstLine(schema.Doc),
				InsertText:       name + "($1)",
				InsertTextFormat: ITFSnippet,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}

// componentSchema resolves a component name as the file spells it: bare against
// the dot-imported packages, or `alias.name` against the one that alias names.
func (s docScope) componentSchema(name string) *checker.ComponentSchema {
	if alias, member, qualified := strings.Cut(name, "."); qualified {
		uri, ok := s.alias[alias]
		if !ok {
			return nil
		}
		return checker.PackageSchema(uri)[member]
	}
	for _, uri := range s.dot {
		if schema, ok := checker.PackageSchema(uri)[name]; ok {
			return schema
		}
	}
	return nil
}

// styleFields is the Style struct's fields, in declaration order: the order an
// author reads them in the source, which groups spacing with spacing and
// colour with colour where alphabetical would not.
func styleFields() []*ir.StructField {
	pkg := checker.LibPackage(stylePkg)
	if pkg == nil {
		return nil
	}
	for _, sd := range pkg.Structs {
		if sd.Name == styleType {
			return sd.Fields
		}
	}
	return nil
}

// pkgStmts flattens a package's files. A package is its declarations, and
// which file each sits in is not something a completion list should show.
func pkgStmts(docs []*ast.Document) []ast.Stmt {
	var out []ast.Stmt
	for _, d := range docs {
		if d != nil {
			out = append(out, d.Stmts...)
		}
	}
	return out
}

// FirstLine is a declaration's doc trimmed to its opening sentence, which is
// what a completion popup has room for.
func FirstLine(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	if i := strings.Index(doc, ". "); i > 0 {
		return doc[:i+1]
	}
	if i := strings.IndexByte(doc, '\n'); i > 0 {
		return doc[:i]
	}
	return doc
}
