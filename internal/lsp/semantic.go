package lsp

import "encoding/json"

// Semantic token type indices (must match SemanticTokensLegend order).
const (
	stType      = 0  // built-in types (int, float, bool, string, etc.)
	stClass     = 1  // component names
	stProperty  = 2  // prop names in prop lists
	stVariable  = 3  // variable references
	stKeyword   = 4  // keywords
	stNamespace = 5  // package names (html, bubbletea, etc.)
	stEnum      = 6  // enum values
	stEvent     = 7  // event names (@click, @change)
	stFunction  = 8  // top-level functions
	stMethod    = 9  // type-attached methods
	stParameter = 10 // function/component parameters
)

// SemanticTokenTypes returns the legend for the server capabilities.
func SemanticTokenTypes() []string {
	return []string{
		"type",      // 0
		"class",     // 1
		"property",  // 2
		"variable",  // 3
		"keyword",   // 4
		"namespace", // 5
		"enum",      // 6
		"event",     // 7
		"function",  // 8
		"method",    // 9
		"parameter", // 10
	}
}

func (s *Server) handleSemanticTokensFull(id json.RawMessage, params json.RawMessage) {
	var p SemanticTokensParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}

	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil || fs.Doc == nil {
		s.sendResult(id, SemanticTokens{Data: []uint32{}})
		return
	}

	// Best-effort type check. On error pkg is nil; the walker falls
	// back to keyword-only classification (so in-progress edits keep
	// at least their keyword highlights).
	pkg, _ := s.checkForPreview(fs)

	data := computeSemanticTokens(fs.Content, fs.Doc, pkg)
	s.sendResult(id, SemanticTokens{Data: data})
}
