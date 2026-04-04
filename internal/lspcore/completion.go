package lspcore

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// Complete returns completion items for the given position (1-based line and col).
func Complete(content string, doc *ast.Document, line, col int) []CompletionItem {
	ctx := CompletionContext(content, line, col)

	switch ctx {
	case CtxTopLevel:
		return TopLevelKeywords()
	case CtxComponent:
		return ComponentKeywords()
	case CtxVisualNode:
		return ComponentNameCompletions(doc)
	case CtxStyleProp:
		return StylePropCompletions()
	case CtxEventHandler:
		return EventCompletions()
	case CtxOutputOpts:
		return OutputOptsCompletions(content, line)
	case CtxOutputTarget:
		return OutputTargetCompletions(content, line)
	default:
		return ExpressionCompletions(doc)
	}
}

type CompletionCtx int

const (
	CtxTopLevel CompletionCtx = iota
	CtxComponent
	CtxVisualNode
	CtxStyleProp
	CtxEventHandler
	CtxOutputOpts
	CtxOutputTarget
)

func CompletionContext(content string, line, col int) CompletionCtx {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return CtxTopLevel
	}
	l := strings.TrimSpace(lines[line-1])

	// Output line detection — must be before brace depth check since outputs are top-level
	if strings.HasPrefix(l, "output ") || l == "output" {
		// Check if cursor is inside parens
		raw := lines[line-1]
		prefix := raw[:min(col-1, len(raw))]
		if strings.Contains(prefix, "(") && !strings.Contains(prefix, ")") {
			return CtxOutputOpts
		}
		return CtxOutputTarget
	}

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
	return CtxVisualNode
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
	kws := []string{"var", "const", "prop", "children", "if", "for"}
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
				Label:         c.Name,
				Kind:          CIKClass,
				Detail:        "user component",
				Documentation: docForPos(doc, c.Pos),
			})
		}
		for _, c := range doc.ImportedComponents {
			items = append(items, CompletionItem{
				Label:         c.Name,
				Kind:          CIKClass,
				Detail:        "imported component",
				Documentation: docForPos(doc, c.Pos),
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
		for _, fn := range doc.Functions {
			if fn.Block == nil && len(fn.Params) == 0 && !fn.IsStdlib {
				items = append(items, CompletionItem{Label: fn.Name, Kind: CIKVariable, Detail: "func"})
			}
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
	registry, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	var items []CompletionItem
	seen := map[string]bool{}
	for _, schema := range registry {
		for name, etype := range schema.Events {
			if !seen[name] {
				seen[name] = true
				items = append(items, CompletionItem{
					Label:         "@" + name,
					Kind:          CIKEvent,
					InsertText:    "@" + name + "={ }",
					Documentation: "Payload type: " + etype,
				})
			}
		}
	}
	return items
}

func StylePropCompletions() []CompletionItem {
	_, styleProps, _, _, _, err := checker.LoadStdlib()
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
	registry, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(registry))
	for name, schema := range registry {
		items = append(items, CompletionItem{
			Label:         name,
			Kind:          CIKClass,
			Detail:        "stdlib component",
			Documentation: schema.Doc,
		})
	}
	return items
}

// OutputOptsCompletions returns completion items for output option keys
// inside the parenthesized options of an output declaration.
func OutputOptsCompletions(content string, line int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	l := strings.TrimSpace(lines[line-1])

	// Extract platform name: "output lang platform(...)" → platform is 2nd word after "output"
	platformName := extractOutputPlatform(l)
	if platformName == "" {
		return nil
	}

	plat := codegen.LookupPlatform(platformName)
	if plat == nil {
		return nil
	}
	ap, ok := plat.(codegen.APIProvider)
	if !ok {
		return nil
	}
	apiDoc := ap.API()
	if apiDoc == nil {
		return nil
	}

	var opts *ast.StructDef
	for _, s := range apiDoc.Structs {
		if s.Name == "Opts" {
			opts = s
			break
		}
	}
	if opts == nil {
		return nil
	}

	var items []CompletionItem
	for _, f := range opts.Fields {
		items = append(items, CompletionItem{
			Label:      f.Name,
			Kind:       CIKProperty,
			Detail:     f.Type,
			InsertText: f.Name + "=",
		})
	}
	return items
}

// OutputTargetCompletions returns completion items for lang/platform names
// on an output declaration line.
func OutputTargetCompletions(content string, line int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	l := strings.TrimSpace(lines[line-1])

	// Count words after "output" to determine position
	words := strings.Fields(l)
	// words[0] = "output", words[1] = lang (if present), words[2] = platform (if present)
	switch len(words) {
	case 1: // "output" — complete with lang names
		var items []CompletionItem
		for _, name := range codegen.Langs() {
			items = append(items, CompletionItem{Label: name, Kind: CIKKeyword, Detail: "language"})
		}
		return items
	case 2: // "output js" — complete with platforms supporting this lang
		lang := words[1]
		var items []CompletionItem
		for _, name := range codegen.PlatformsForLang(lang) {
			items = append(items, CompletionItem{Label: name, Kind: CIKKeyword, Detail: "platform"})
		}
		return items
	default:
		return nil
	}
}

// extractOutputPlatform extracts the platform name from an output line.
// e.g., "output js html(package=...)" → "html"
func extractOutputPlatform(line string) string {
	words := strings.Fields(line)
	if len(words) < 3 {
		return ""
	}
	// words[2] may have parens: "html(" or "html(package="
	plat := words[2]
	if idx := strings.IndexByte(plat, '('); idx >= 0 {
		plat = plat[:idx]
	}
	return plat
}
