package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

// componentSnapshotPath returns the absolute filesystem path to
// snapshots/example_<Name>_html.png relative to the source .sngl file,
// if it exists.
func componentSnapshotPath(sourceURI, componentName string) (string, bool) {
	srcPath := uriToPath(sourceURI)
	if srcPath == "" {
		return "", false
	}
	dir := filepath.Dir(srcPath)
	candidate := filepath.Join(dir, "snapshots", "example_"+componentName+"_html.png")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}

func (s *Server) handleHover(id json.RawMessage, params json.RawMessage) {
	var p HoverParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}

	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, nil)
		return
	}

	line := p.Position.Line + 1
	col := p.Position.Character + 1

	pkg, _ := s.checkForPreview(fs)

	opts := lspcore.HoverOptions{
		ComponentImageURL: func(name string) (string, bool) {
			path, ok := componentSnapshotPath(p.TextDocument.URI, name)
			if !ok {
				return "", false
			}
			return s.preview.RegisterAsset(path), true
		},
		StdlibSymbol: func(name string) (string, bool) {
			return lookupStdlibSymbol(pkg, name)
		},
		ComponentProp: func(componentName, propName string) (string, bool) {
			return lookupComponentProp(pkg, componentName, propName)
		},
	}
	info := lspcore.HoverAt(fs.Content, fs.Doc, line, col, opts)
	if info == "" {
		s.sendResult(id, nil)
		return
	}

	s.sendResult(id, Hover{
		Contents: MarkupContent{
			Kind:  "markdown",
			Value: info,
		},
	})
}
