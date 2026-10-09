package lsp

import (
	"encoding/json"

	"duckfam.us/sngl/internal/lspcore"
)

func (s *Server) handleCompletion(id json.RawMessage, params json.RawMessage) {
	var p CompletionParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}

	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil {
		s.sendResult(id, CompletionList{})
		return
	}

	line := p.Position.Line + 1
	col := p.Position.Character + 1
	coreItems := lspcore.Complete(fs.Content, fs.Doc, line, col)

	items := make([]CompletionItem, len(coreItems))
	for i, ci := range coreItems {
		items[i] = CompletionItem{
			Label:            ci.Label,
			Kind:             ci.Kind,
			Detail:           ci.Detail,
			InsertText:       ci.InsertText,
			InsertTextFormat: ci.InsertTextFormat,
		}
		if ci.Documentation != "" {
			items[i].Documentation = &MarkupContent{
				Kind:  "markdown",
				Value: ci.Documentation,
			}
		}
	}

	s.sendResult(id, CompletionList{
		IsIncomplete: false,
		Items:        items,
	})
}
