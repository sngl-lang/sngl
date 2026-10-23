package lsp

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/lspcore"
)

// source reads a file a position names: an open buffer before the file on
// disk, and a library file -- whose name is relative to lib.FS -- from the
// embedded library.
func (s *Server) source(file string) (string, bool) {
	if !filepath.IsAbs(file) {
		return lspcore.LibrarySource(file)
	}
	for _, fs := range s.ws.all() {
		if uriToPath(fs.URI) == file {
			return fs.Content, true
		}
	}
	b, err := os.ReadFile(file)
	return string(b), err == nil
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// libraryDir is where a library file is written for an editor to open, since
// the library is embedded in the binary.
func libraryDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "sngl", "lib")
}

// isLibraryCopy reports whether path is a library file written by
// libraryFile, which is the library's to check, not the editor's.
func isLibraryCopy(path string) bool {
	return strings.HasPrefix(path, libraryDir()+string(filepath.Separator))
}

// libraryFile writes the library file name, relative to lib.FS, where an
// editor can open it, read-only, and returns its path. A copy that differs
// from this binary's library is rewritten.
func libraryFile(name string) (string, bool) {
	text, ok := lspcore.LibrarySource(name)
	if !ok {
		return "", false
	}
	path := filepath.Join(libraryDir(), filepath.FromSlash(name))
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, []byte(text)) {
		return path, true
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", false
	}
	_ = os.Chmod(path, 0o644)
	if err := os.WriteFile(path, []byte(text), 0o444); err != nil {
		return "", false
	}
	_ = os.Chmod(path, 0o444)
	return path, true
}

// location is where a name of n runes at pos is written, as an editor
// addresses it.
func (s *Server) location(pos ast.Pos, n int) (Location, bool) {
	path := pos.File
	if !filepath.IsAbs(path) {
		p, ok := libraryFile(path)
		if !ok {
			return Location{}, false
		}
		path = p
	}
	text, _ := s.source(pos.File)
	lines := strings.Split(text, "\n")
	line := ""
	if pos.Line-1 < len(lines) {
		line = lines[pos.Line-1]
	}
	start := lspcore.UTF16Column(line, pos.Column-1)
	end := lspcore.UTF16Column(line, pos.Column-1+n)
	return Location{
		URI: pathToURI(path),
		Range: Range{
			Start: Position{Line: pos.Line - 1, Character: start},
			End:   Position{Line: pos.Line - 1, Character: end},
		},
	}, true
}

// refAt is the indexed name at an editor's position in fs.
func (s *Server) refAt(fs *fileState, p Position) *lspcore.Ref {
	if fs == nil || fs.Index == nil {
		return nil
	}
	lines := strings.Split(fs.Content, "\n")
	if p.Line < 0 || p.Line >= len(lines) {
		return nil
	}
	col := lspcore.RuneColumn(lines[p.Line], p.Character) + 1
	return fs.Index.At(uriToPath(fs.URI), p.Line+1, col)
}

func (s *Server) handleDefinition(id json.RawMessage, params json.RawMessage) {
	var p TextDocumentPositionParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	r := s.refAt(fs, p.Position)
	if r == nil {
		s.sendResult(id, nil)
		return
	}
	site, ok := fs.Index.Sites[r.Decl]
	if !ok {
		s.sendResult(id, nil)
		return
	}
	loc, ok := s.location(site.Pos, site.Len)
	if !ok {
		s.sendResult(id, nil)
		return
	}
	s.sendResult(id, loc)
}

func (s *Server) handleReferences(id json.RawMessage, params json.RawMessage) {
	var p ReferenceParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	r := s.refAt(fs, p.Position)
	if r == nil {
		s.sendResult(id, nil)
		return
	}
	locs := []Location{}
	for _, ref := range fs.Index.RefsTo(r.Decl) {
		if ref.IsDecl && !p.Context.IncludeDeclaration {
			continue
		}
		if loc, ok := s.location(ref.Pos, ref.Len); ok {
			locs = append(locs, loc)
		}
	}
	// A library declaration's name is not in the package's files.
	if site, ok := fs.Index.Sites[r.Decl]; ok && p.Context.IncludeDeclaration && fs.Index.Library[r.Decl] {
		if loc, ok := s.location(site.Pos, site.Len); ok {
			locs = append([]Location{loc}, locs...)
		}
	}
	s.sendResult(id, locs)
}
