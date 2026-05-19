package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/ir"
)

// checkForPreview type-checks the workspace file and returns the IR
// package. Unlike analyze (which collects diagnostics for publishing),
// this short-circuits on any check error so the preview server can fall
// back to its last good render.
func (s *Server) checkForPreview(fs *fileState) (*ir.Package, error) {
	dir := filepath.Dir(uriToPath(fs.URI))
	pkg, diags := sngl.Check(fs.Doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil, fmt.Errorf("%s", d.Error())
		}
	}
	return pkg, nil
}

func (s *Server) handleInitialize(id json.RawMessage, params json.RawMessage) {
	s.preview.SetRenderer(func(uri, windowName string) ([]byte, error) {
		fs := s.ws.get(uri)
		if fs == nil || fs.Doc == nil {
			return nil, fmt.Errorf("document not open: %s", uri)
		}
		pkg, err := s.checkForPreview(fs)
		if err != nil {
			if stale, ok := s.cache.get(uri, windowName); ok {
				return appendErrorBanner(stale, err.Error()), nil
			}
			return nil, err
		}
		body, rerr := renderDocAsHTML(pkg, windowName)
		if rerr != nil {
			if stale, ok := s.cache.get(uri, windowName); ok {
				return appendErrorBanner(stale, rerr.Error()), nil
			}
			return nil, rerr
		}
		s.cache.set(uri, windowName, body)
		return body, nil
	})
	if err := s.preview.Start(); err != nil {
		s.log.Printf("preview server: %v", err)
		// Continue without preview — hover degrades silently
	}

	result := InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: 1, // Full
			HoverProvider:    true,
			CompletionProvider: &CompletionOptions{
				TriggerCharacters: []string{".", "@", "("},
			},
			SemanticTokensProvider: &SemanticTokensOptions{
				Legend: SemanticTokensLegend{
					TokenTypes:     SemanticTokenTypes(),
					TokenModifiers: []string{},
				},
				Full: true,
			},
			ColorProvider:     true,
			InlayHintProvider: true,
		},
		ServerInfo: &ServerInfo{
			Name:    "sngl-lsp",
			Version: "0.1.0",
		},
	}
	s.sendResult(id, result)
	if port := s.preview.Port(); port > 0 {
		s.notify("sngl/previewReady", map[string]any{
			"port": port,
			"url":  fmt.Sprintf("http://127.0.0.1:%d", port),
		})
	}
}

func (s *Server) handleDidOpen(params json.RawMessage) {
	var p DidOpenTextDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.log.Printf("didOpen unmarshal: %v", err)
		return
	}
	fs := s.ws.open(p.TextDocument.URI, p.TextDocument.Text, p.TextDocument.Version)
	diags := s.analyze(fs)
	s.publishDiagnostics(p.TextDocument.URI, diags)
}

func (s *Server) handleDidChange(params json.RawMessage) {
	var p DidChangeTextDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.log.Printf("didChange unmarshal: %v", err)
		return
	}
	if len(p.ContentChanges) == 0 {
		return
	}
	// Full sync: last change has the full text
	text := p.ContentChanges[len(p.ContentChanges)-1].Text
	fs := s.ws.update(p.TextDocument.URI, text, p.TextDocument.Version)
	diags := s.analyze(fs)
	s.publishDiagnostics(p.TextDocument.URI, diags)
	s.scheduleReload()
}

func (s *Server) handleDidClose(params json.RawMessage) {
	var p DidCloseTextDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	s.publishDiagnostics(p.TextDocument.URI, nil)
	s.ws.close(p.TextDocument.URI)
}

func (s *Server) handleDidSave(params json.RawMessage) {
	var p DidSaveTextDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil {
		return
	}
	diags := s.analyze(fs)
	s.publishDiagnostics(p.TextDocument.URI, diags)
	s.scheduleReload()
}
