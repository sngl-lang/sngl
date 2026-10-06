package lsp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"git.duckfam.us/jonathan/sngl/ir"
)

// handleExecuteCommand routes workspace/executeCommand to the appropriate
// handler. Today only sngl.openPreview is supported.
func (s *Server) handleExecuteCommand(id json.RawMessage, params json.RawMessage) {
	var p ExecuteCommandParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	switch p.Command {
	case "sngl.openPreview":
		s.cmdOpenPreview(id, p.Arguments)
	default:
		s.sendError(id, -32601, "unknown command: "+p.Command)
	}
}

// cmdOpenPreview parses {uri, position?} from args and returns
// {"url": "http://127.0.0.1:PORT/preview/<base64-uri>/<window>"}.
func (s *Server) cmdOpenPreview(id json.RawMessage, args []json.RawMessage) {
	if len(args) < 1 {
		s.sendError(id, -32602, "sngl.openPreview requires {uri, position?}")
		return
	}
	var first struct {
		URI      string   `json:"uri"`
		Position Position `json:"position"`
	}
	if err := json.Unmarshal(args[0], &first); err != nil {
		s.sendError(id, -32602, "sngl.openPreview: invalid args")
		return
	}
	fs := s.ws.get(first.URI)
	if fs == nil || fs.Doc == nil {
		s.sendError(id, -32603, "document not open: "+first.URI)
		return
	}
	pkg, err := s.checkForPreview(fs)
	if err != nil || pkg == nil || !pkg.IsProgram() {
		s.sendError(id, -32603, "the document renders nothing at the root of the file")
		return
	}
	// For now, the first node the package body renders. (TODO: the one
	// enclosing the position.)
	windowName := firstRootID(pkg)

	port := s.preview.Port()
	if port == 0 {
		s.sendError(id, -32603, "preview server not running")
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/preview/%s/%s",
		port, base64.RawURLEncoding.EncodeToString([]byte(first.URI)), windowName)
	s.sendResult(id, map[string]string{"url": url})
}

// firstRootID is the `#id` of the first node the package body renders, "" when
// it wrote none.
func firstRootID(pkg *ir.Package) string {
	for _, s := range pkg.Body {
		if n, ok := s.(*ir.NodeInst); ok {
			return n.ID
		}
	}
	return ""
}
