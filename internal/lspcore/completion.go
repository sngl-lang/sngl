package lspcore

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// Complete returns completion items for the given position (1-based line and col).
func Complete(content string, doc *ast.Document, line, col int) []CompletionItem {
	ctx := CompletionContext(content, line, col)

	switch ctx {
	case CtxTopLevel:
		return TopLevelKeywords()
	case CtxComponent:
		return ComponentKeywords()
	case CtxPropValue, CtxExpression:
		return ExpressionCompletions(doc)
	case CtxVisualNode:
		return ComponentNameCompletions(doc)
	case CtxStyleProp:
		return StylePropCompletions()
	case CtxEventHandler:
		return EventCompletions()
	default:
		return AllCompletions(doc)
	}
}

type CompletionCtx int

const (
	CtxUnknown CompletionCtx = iota
	CtxTopLevel
	CtxComponent
	CtxVisualNode
	CtxPropValue
	CtxExpression
	CtxStyleProp
	CtxEventHandler
)

func CompletionContext(content string, line, col int) CompletionCtx {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return CtxTopLevel
	}
	l := strings.TrimSpace(lines[line-1])

	if strings.HasPrefix(l, "@") {
		return CtxEventHandler
	}
	if strings.Contains(l, "style={") || strings.Contains(l, "style=") {
		return CtxStyleProp
	}

	braceDepth := 0
	for i := 0; i < line-1 && i < len(lines); i++ {
		braceDepth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
	}

	if braceDepth == 0 {
		return CtxTopLevel
	}
	if braceDepth == 1 {
		return CtxComponent
	}
	if braceDepth >= 2 {
		return CtxVisualNode
	}
	return CtxUnknown
}

func TopLevelKeywords() []CompletionItem {
	kws := []string{"import", "output", "struct", "enum", "unit", "const", "style", "styles", "component"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	return items
}

func ComponentKeywords() []CompletionItem {
	kws := []string{"param", "var", "const", "computed", "prop", "event", "children", "if", "for"}
	items := make([]CompletionItem, len(kws))
	for i, kw := range kws {
		items[i] = CompletionItem{Label: kw, Kind: CIKKeyword}
	}
	items = append(items, StdlibComponentItems()...)
	return items
}

func ComponentNameCompletions(doc *ast.Document) []CompletionItem {
	items := StdlibComponentItems()
	if doc != nil {
		for _, c := range doc.Components {
			items = append(items, CompletionItem{
				Label:  c.Name,
				Kind:   CIKClass,
				Detail: "user component",
			})
		}
	}
	return items
}

func ExpressionCompletions(doc *ast.Document) []CompletionItem {
	var items []CompletionItem
	for _, kw := range []string{"true", "false", "null"} {
		items = append(items, CompletionItem{Label: kw, Kind: CIKKeyword})
	}
	if doc != nil {
		for _, d := range doc.Data {
			items = append(items, CompletionItem{Label: d.Name, Kind: CIKVariable, Detail: d.Init.TypeHint})
		}
		for _, c := range doc.Computeds {
			items = append(items, CompletionItem{Label: c.Name, Kind: CIKVariable, Detail: "computed"})
		}
		for _, c := range doc.Consts {
			items = append(items, CompletionItem{Label: c.Name, Kind: CIKConstant, Detail: "const"})
		}
		for _, s := range doc.Structs {
			items = append(items, CompletionItem{Label: s.Name, Kind: CIKStruct})
		}
		for _, e := range doc.Enums {
			items = append(items, CompletionItem{Label: e.Name, Kind: CIKEnum})
		}
	}
	return items
}

func EventCompletions() []CompletionItem {
	registry, _, _, _, err := checker.LoadStdlib()
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

func StylePropCompletions() []CompletionItem {
	_, styleProps, _, _, err := checker.LoadStdlib()
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

func StdlibComponentItems() []CompletionItem {
	registry, _, _, _, err := checker.LoadStdlib()
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

func AllCompletions(doc *ast.Document) []CompletionItem {
	var items []CompletionItem
	for kw := range snglparser.Keywords() {
		items = append(items, CompletionItem{Label: kw, Kind: CIKKeyword})
	}
	items = append(items, StdlibComponentItems()...)
	items = append(items, ExpressionCompletions(doc)...)
	return items
}
