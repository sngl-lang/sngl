package lsp

import (
	"encoding/json"
)

// handleCodeAction returns the available code actions for the given range.
// Today: "Preview in browser" when the document contains at least one
// window. The action carries the sngl.openPreview command so the editor
// follows up with workspace/executeCommand.
func (s *Server) handleCodeAction(id json.RawMessage, params json.RawMessage) {
	var p CodeActionParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}

	actions := []CodeAction{}

	fs := s.ws.get(p.TextDocument.URI)
	if fs != nil && fs.Doc != nil {
		if pkg, err := s.checkForPreview(fs); err == nil && pkg != nil && len(pkg.Windows) > 0 {
			actions = append(actions, CodeAction{
				Title: "Preview in browser",
				Kind:  CodeActionKindSource,
				Command: &Command{
					Title:   "Preview in browser",
					Command: "sngl.openPreview",
					Arguments: []any{
						map[string]any{
							"uri": p.TextDocument.URI,
							"position": map[string]any{
								"line":      p.Range.Start.Line,
								"character": p.Range.Start.Character,
							},
						},
					},
				},
			})
		}
	}

	s.sendResult(id, actions)
}
