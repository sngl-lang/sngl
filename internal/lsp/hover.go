package lsp

import (
	"encoding/json"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

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
	info := lspcore.HoverAt(fs.Content, fs.Doc, line, col, lspcore.HoverOptions{})
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
