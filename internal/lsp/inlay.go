package lsp

import (
	"encoding/json"

	"git.duckfam.us/jonathan/sngl/internal/lspcore"
)

func (s *Server) handleInlayHint(id json.RawMessage, params json.RawMessage) {
	var p InlayHintParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, []InlayHint{})
		return
	}
	coreRng := lspcore.Range{
		Start: lspcore.Position{Line: p.Range.Start.Line, Character: p.Range.Start.Character},
		End:   lspcore.Position{Line: p.Range.End.Line, Character: p.Range.End.Character},
	}
	results := lspcore.ComputeInlayHints(fs.Content, fs.Doc, coreRng)
	hints := make([]InlayHint, 0, len(results))
	for _, r := range results {
		hints = append(hints, InlayHint{
			Position:    Position{Line: r.Position.Line, Character: r.Position.Character},
			Label:       r.Label,
			Kind:        InlayHintKindType,
			PaddingLeft: r.PaddingLeft,
		})
	}
	s.sendResult(id, hints)
}
