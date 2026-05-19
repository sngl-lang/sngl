package lsp

import "encoding/json"

func (s *Server) handleInitialize(id json.RawMessage, params json.RawMessage) {
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
}
