package lsp

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/parser"
)

// siblings parses the other .sngl files in filename's directory: the package
// a build of that directory checks it beside. An open buffer is read in place
// of its file on disk, so an unsaved edit is what its siblings see. A sibling
// that does not parse is left out and named in broken. over replaces a
// file's text, by path, ahead of both.
func (s *Server) siblings(filename string, over map[string]string) (docs []*ast.Document, broken []string) {
	dir := filepath.Dir(filename)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	open := map[string]string{}
	for _, fs := range s.ws.all() {
		open[uriToPath(fs.URI)] = fs.Content
	}
	maps.Copy(open, over)
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sngl") || path == filename {
			continue
		}
		var src []byte
		if content, ok := open[path]; ok {
			src = []byte(content)
		} else if src, err = os.ReadFile(path); err != nil {
			broken = append(broken, e.Name())
			continue
		}
		doc, err := parser.ParseShared(path, src)
		if err != nil {
			broken = append(broken, e.Name())
			continue
		}
		docs = append(docs, doc)
	}
	return docs, broken
}

// withSiblings is doc merged with its package's other files, as a build reads
// the directory, into a new document: doc itself is shared and never written.
func (s *Server) withSiblings(doc *ast.Document, filename string) *ast.Document {
	sibs, _ := s.siblings(filename, nil)
	if len(sibs) == 0 {
		return doc
	}
	merged := &ast.Document{Stmts: slices.Clone(doc.Stmts), BlankLines: doc.BlankLines}
	for _, sib := range sibs {
		build.MergeInto(merged, sib)
	}
	return merged
}

// republishSiblings rechecks every other open file in uri's directory, whose
// diagnostics may have changed with uri's content.
func (s *Server) republishSiblings(uri string) {
	dir := filepath.Dir(uriToPath(uri))
	for _, fs := range s.ws.all() {
		if fs.URI == uri || filepath.Dir(uriToPath(fs.URI)) != dir {
			continue
		}
		s.publishDiagnostics(fs.URI, s.analyze(fs))
	}
}
