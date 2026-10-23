package lsp

import (
	"encoding/json"
	"slices"
	"strings"

	"duckfam.us/sngl/internal/lspcore"
)

// semanticTypes is the token-type legend. A type a name's declaration
// decides, and nothing a tree-sitter grammar can say: keywords, strings and
// comments are left to the editor's syntax highlighting, which an LSP token
// would cover with a coarser group. `component` is not a standard type; the
// Neovim plugin links it.
var semanticTypes = []string{
	lspcore.KindVariable:      "variable",
	lspcore.KindConstant:      "variable",
	lspcore.KindParameter:     "parameter",
	lspcore.KindFunction:      "function",
	lspcore.KindMethod:        "method",
	lspcore.KindComponent:     "component",
	lspcore.KindType:          "type",
	lspcore.KindTypeParameter: "typeParameter",
	lspcore.KindField:         "property",
	lspcore.KindEnumMember:    "enumMember",
	lspcore.KindEvent:         "event",
	lspcore.KindNamespace:     "namespace",
	lspcore.KindElement:       "variable",
}

var semanticModifiers = []string{"declaration", "readonly", "defaultLibrary"}

const (
	modDeclaration = 1 << iota
	modReadonly
	modDefaultLibrary
)

// semanticLegend is semanticTypes without its repeats, which the legend
// lists once.
var semanticLegend = func() []string {
	var out []string
	for _, t := range semanticTypes {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}()

func tokenType(k lspcore.SymbolKind) uint32 {
	return uint32(slices.Index(semanticLegend, semanticTypes[k]))
}

// semanticTokens is the delta-encoded tokens of every name file writes, as
// ix classifies them, in the UTF-16 columns an LSP position counts.
func semanticTokens(content string, ix *lspcore.Index, file string) []uint32 {
	data := []uint32{}
	if ix == nil {
		return data
	}
	lines := strings.Split(content, "\n")
	prevLine, prevCol := 0, 0
	var last *lspcore.Ref
	for _, r := range ix.InFile(file) {
		// A shorthand field, `{a}`, has two Refs at one position, and tokens
		// may not overlap.
		if last != nil && last.Pos == r.Pos {
			continue
		}
		last = &r
		// The editor's syntax highlighting draws `#id` as one token, which a
		// token on the name alone would split.
		if r.Pos.Line-1 >= len(lines) || r.Kind == lspcore.KindElement && r.IsDecl {
			continue
		}
		line := lines[r.Pos.Line-1]
		col := lspcore.UTF16Column(line, r.Pos.Column-1)
		length := lspcore.UTF16Column(line, r.Pos.Column-1+r.Len) - col
		var mods uint32
		if r.IsDecl {
			mods |= modDeclaration
		}
		if r.Kind == lspcore.KindConstant {
			mods |= modReadonly
		}
		if ix.Library[r.Decl] {
			mods |= modDefaultLibrary
		}
		l := r.Pos.Line - 1
		deltaCol := col
		if l == prevLine {
			deltaCol = col - prevCol
		}
		data = append(data, uint32(l-prevLine), uint32(deltaCol), uint32(length), tokenType(r.Kind), mods)
		prevLine, prevCol = l, col
	}
	return data
}

func (s *Server) handleSemanticTokensFull(id json.RawMessage, params json.RawMessage) {
	var p SemanticTokensParams
	if err := json.Unmarshal(params, &p); err != nil {
		s.sendError(id, -32602, "invalid params")
		return
	}
	fs := s.ws.get(p.TextDocument.URI)
	if fs == nil {
		s.sendResult(id, SemanticTokens{Data: []uint32{}})
		return
	}
	s.sendResult(id, SemanticTokens{Data: semanticTokens(fs.Content, fs.Index, uriToPath(fs.URI))})
}
