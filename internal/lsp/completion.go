package lsp

import (
	"encoding/json"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
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
	ctx := completionContext(fs.Content, line, col)

	var items []CompletionItem

	switch ctx {
	case ctxTopLevel:
		items = topLevelKeywords()
	case ctxComponent:
		items = componentKeywords()
	case ctxPropValue, ctxExpression:
		items = expressionCompletions(fs)
	case ctxVisualNode:
		items = componentNameCompletions(fs)
	case ctxStyleProp:
		items = stylePropCompletions()
	case ctxEventHandler:
		items = eventCompletions(fs)
	default:
		// Provide all completions as fallback
		items = allCompletions(fs)
	}

	s.sendResult(id, CompletionList{
		IsIncomplete: false,
		Items:        items,
	})
}

type completionCtx int

const (
	ctxUnknown completionCtx = iota
	ctxTopLevel
	ctxComponent
	ctxVisualNode
	ctxPropValue
	ctxExpression
	ctxStyleProp
	ctxEventHandler
)

func completionContext(content string, line, col int) completionCtx {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return ctxTopLevel
	}
	l := strings.TrimSpace(lines[line-1])

	// Simple heuristics based on line content
	if strings.HasPrefix(l, "@") {
		return ctxEventHandler
	}
	if strings.Contains(l, "style={") || strings.Contains(l, "style=") {
		return ctxStyleProp
	}

	// Count brace depth to determine context
	braceDepth := 0
	for i := 0; i < line-1 && i < len(lines); i++ {
		braceDepth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
	}

	if braceDepth == 0 {
		return ctxTopLevel
	}
	if braceDepth == 1 {
		return ctxComponent
	}
	if braceDepth >= 2 {
		return ctxVisualNode
	}
	return ctxUnknown
}

func topLevelKeywords() []CompletionItem {
	kws := []string{"import", "output", "struct", "enum", "unit", "const", "style", "styles", "component"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	return items
}

func componentKeywords() []CompletionItem {
	kws := []string{"param", "var", "const", "computed", "prop", "event", "children", "if", "for"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	// Add stdlib component names for visual nodes
	items = append(items, stdlibComponentItems()...)
	return items
}

func componentNameCompletions(fs *fileState) []CompletionItem {
	items := stdlibComponentItems()
	if fs.Doc != nil {
		for _, c := range fs.Doc.Components {
			items = append(items, CompletionItem{
				Label:  c.Name,
				Kind:   CIKClass,
				Detail: "user component",
			})
		}
	}
	return items
}

func expressionCompletions(fs *fileState) []CompletionItem {
	var items []CompletionItem
	// Keywords
	for _, kw := range []string{"true", "false", "null"} {
		items = append(items, CompletionItem{Label: kw, Kind: CIKKeyword})
	}
	// Variables and computeds from the doc
	if fs.Doc != nil {
		for _, d := range fs.Doc.Data {
			items = append(items, CompletionItem{Label: d.Name, Kind: CIKVariable, Detail: d.Init.TypeHint})
		}
		for _, c := range fs.Doc.Computeds {
			items = append(items, CompletionItem{Label: c.Name, Kind: CIKVariable, Detail: "computed"})
		}
		for _, c := range fs.Doc.Consts {
			items = append(items, CompletionItem{Label: c.Name, Kind: CIKConstant, Detail: "const"})
		}
		for _, s := range fs.Doc.Structs {
			items = append(items, CompletionItem{Label: s.Name, Kind: CIKStruct})
		}
		for _, e := range fs.Doc.Enums {
			items = append(items, CompletionItem{Label: e.Name, Kind: CIKEnum})
		}
	}
	return items
}

func eventCompletions(fs *fileState) []CompletionItem {
	registry, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	var items []CompletionItem
	seen := map[string]bool{}
	for _, schema := range registry {
		for name := range schema.Events {
			if !seen[name] {
				seen[name] = true
				items = append(items, CompletionItem{
					Label:      "@" + name,
					Kind:       CIKEvent,
					InsertText: "@" + name + "={ }",
				})
			}
		}
	}
	return items
}

func stylePropCompletions() []CompletionItem {
	_, styleProps, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(styleProps))
	for name, sp := range styleProps {
		detail := sp.Type.String()
		if len(sp.Enum) > 0 {
			detail += " (" + strings.Join(sp.Enum, "|") + ")"
		}
		items = append(items, CompletionItem{
			Label:      name,
			Kind:       CIKProperty,
			Detail:     detail,
			InsertText: name + "=",
		})
	}
	return items
}

func stdlibComponentItems() []CompletionItem {
	registry, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(registry))
	for name := range registry {
		items = append(items, CompletionItem{
			Label:  name,
			Kind:   CIKClass,
			Detail: "stdlib component",
		})
	}
	return items
}

func allCompletions(fs *fileState) []CompletionItem {
	var items []CompletionItem

	// All SNGL keywords
	for kw := range snglparser.Keywords() {
		items = append(items, CompletionItem{Label: kw, Kind: CIKKeyword})
	}

	// Stdlib components
	items = append(items, stdlibComponentItems()...)

	// Doc items
	items = append(items, expressionCompletions(fs)...)

	return items
}
