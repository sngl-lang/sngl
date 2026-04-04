package lspcore

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// Complete returns completion items for the given position (1-based line and col).
func Complete(content string, doc *ast.Document, line, col int) []CompletionItem {
	// Check for namespace dot completion first (e.g., "html.|")
	if items := NamespaceCompletions(content, doc, line, col); items != nil {
		return items
	}

	ctx := CompletionContext(content, line, col)

	switch ctx {
	case CtxTopLevel:
		return TopLevelKeywords()
	case CtxComponent:
		return ComponentKeywords()
	case CtxVisualNode:
		return ComponentNameCompletions(doc)
	case CtxPropList:
		return PropListCompletions(content, doc, line, col)
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
	CtxPropList // inside component(|)
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

	// Check if cursor is inside component prop parens: component_name(|)
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]
	if strings.Contains(l, "style={") || strings.Contains(prefix, "style=") {
		return CtxStyleProp
	}
	// Prop list: cursor is after an opening ( with no closing ) before it,
	// and NOT on an output line
	if strings.Contains(prefix, "(") && !strings.Contains(prefix, ")") {
		return CtxPropList
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
	kws := []string{"var", "const", "if", "for"}
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
				Label:            c.Name,
				Kind:             CIKClass,
				Detail:           "user component",
				Documentation:    docForPos(doc, c.Pos),
				InsertText:       c.Name + "($1)",
				InsertTextFormat: ITFSnippet,
			})
		}
		for _, c := range doc.ImportedComponents {
			items = append(items, CompletionItem{
				Label:            c.Name,
				Kind:             CIKClass,
				Detail:           "imported component",
				Documentation:    docForPos(doc, c.Pos),
				InsertText:       c.Name + "($1)",
				InsertTextFormat: ITFSnippet,
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
	registry, _, _, _, _, _, err := checker.LoadStdlib()
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
	_, styleProps, _, _, _, _, err := checker.LoadStdlib()
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
	registry, _, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(registry))
	for name, schema := range registry {
		items = append(items, CompletionItem{
			Label:            name,
			Kind:             CIKClass,
			Detail:           "stdlib component",
			Documentation:    schema.Doc,
			InsertText:       name + "($1)",
			InsertTextFormat: ITFSnippet,
		})
	}
	return items
}

// PropListCompletions returns props and events for the component whose
// prop list the cursor is inside.
func PropListCompletions(content string, doc *ast.Document, line, col int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]

	// Extract component name: find the identifier before the opening paren
	parenIdx := strings.LastIndex(prefix, "(")
	if parenIdx < 0 {
		return nil
	}
	before := strings.TrimRight(prefix[:parenIdx], " \t")
	// Component name is the last word (handle element refs like #id)
	compName := before
	if sp := strings.LastIndexAny(before, " \t{"); sp >= 0 {
		compName = before[sp+1:]
	}
	// Strip element ref prefix: "input #foo" → take text before #
	if hi := strings.LastIndex(compName, "#"); hi > 0 {
		compName = strings.TrimSpace(compName[:hi])
	} else if strings.HasPrefix(compName, "#") {
		// Just a ref, look further back
		if sp := strings.LastIndexAny(before[:len(before)-len(compName)], " \t{"); sp >= 0 {
			compName = strings.TrimSpace(before[sp+1 : len(before)-len(compName)])
		}
	}
	if compName == "" {
		return nil
	}

	// Look up schema for this component
	registry, _, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil
	}
	schema := registry[compName]

	// Also check user-defined components
	if schema == nil && doc != nil {
		for _, c := range doc.Components {
			if c.Name == compName {
				schema = buildUserComponentSchema(c)
				break
			}
		}
	}
	if schema == nil && doc != nil {
		for _, c := range doc.ImportedComponents {
			if c.Name == compName {
				schema = buildUserComponentSchema(c)
				break
			}
		}
	}
	if schema == nil {
		return nil
	}

	// Collect already-used prop names on this line to exclude them
	used := map[string]bool{}
	insideParens := prefix[parenIdx+1:]
	for _, part := range strings.Split(insideParens, ",") {
		part = strings.TrimSpace(part)
		if eq := strings.Index(part, "="); eq > 0 {
			name := strings.TrimSpace(part[:eq])
			name = strings.TrimPrefix(name, "@")
			name = strings.TrimPrefix(name, ":")
			used[name] = true
		}
	}

	var items []CompletionItem

	// Props
	for name, ps := range schema.Props {
		if used[name] {
			continue
		}
		detail := ps.Type.String()
		if len(ps.Enum) > 0 {
			detail += " (" + strings.Join(ps.Enum, "|") + ")"
		}
		items = append(items, CompletionItem{
			Label:            name,
			Kind:             CIKProperty,
			Detail:           detail,
			Documentation:    ps.Doc,
			InsertText:       name + "=$1",
			InsertTextFormat: ITFSnippet,
		})
	}

	// Events
	for name, etype := range schema.Events {
		if used[name] {
			continue
		}
		items = append(items, CompletionItem{
			Label:            "@" + name,
			Kind:             CIKEvent,
			Detail:           etype,
			InsertText:       "@" + name + "={ $1 }",
			InsertTextFormat: ITFSnippet,
		})
	}

	// Style prop
	if !used["style"] {
		items = append(items, CompletionItem{
			Label:            "style",
			Kind:             CIKProperty,
			Detail:           "inline styles",
			InsertText:       "style={$1}",
			InsertTextFormat: ITFSnippet,
		})
	}

	return items
}

// buildUserComponentSchema creates a ComponentSchema from a user-defined component.
func buildUserComponentSchema(comp *ast.Component) *checker.ComponentSchema {
	schema := &checker.ComponentSchema{
		Props:  make(map[string]checker.PropSchema),
		Events: make(map[string]string),
	}
	for _, p := range comp.Params {
		schema.Props[p.Name] = checker.PropSchema{
			Type: checker.TypeFromHint(p.Default.TypeHint),
		}
	}
	for _, e := range comp.EventDecls {
		schema.Events[e.Name] = e.PayloadType
	}
	return schema
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
	src := plat.PkgSource()
	if src == "" {
		return nil
	}
	apiDoc, err := parseSngl(platformName+".sngl", src)
	if err != nil || apiDoc == nil {
		return nil
	}

	var opts *ast.StructDef
	for _, s := range apiDoc.Structs {
		if s.Name == "Options" {
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

func parseSngl(name, src string) (*ast.Document, error) {
	return parser.Parse(name, strings.NewReader(src))
}

// NamespaceCompletions returns completions for "namespace." (e.g., "html.").
// Returns nil if the cursor is not after a namespace dot.
func NamespaceCompletions(content string, doc *ast.Document, line, col int) []CompletionItem {
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	raw := lines[line-1]
	prefix := raw[:min(col-1, len(raw))]

	// Find "word." pattern at end of prefix
	dotIdx := strings.LastIndex(prefix, ".")
	if dotIdx < 0 {
		return nil
	}
	// Extract namespace name (word before the dot)
	before := prefix[:dotIdx]
	nsStart := len(before)
	for nsStart > 0 && isIdentChar(before[nsStart-1]) {
		nsStart--
	}
	nsName := before[nsStart:]
	if nsName == "" {
		return nil
	}

	// Check if this is a registered platform or language
	var pkgDoc *ast.Document
	if plat := codegen.LookupPlatform(nsName); plat != nil {
		src := plat.PkgSource()
		if src != "" {
			pkgDoc, _ = parseSngl(nsName+".sngl", src)
		}
	}
	if pkgDoc == nil {
		if lang := codegen.LookupLang(nsName); lang != nil {
			src := lang.PkgSource()
			if src != "" {
				pkgDoc, _ = parseSngl(nsName+".sngl", src)
			}
		}
	}

	// Also check imported namespaces in the document
	if pkgDoc == nil && doc != nil {
		for _, imp := range doc.Imports {
			if imp.Namespace == nsName {
				// Imported package — offer its components
				var items []CompletionItem
				for _, c := range doc.ImportedComponents {
					items = append(items, CompletionItem{
						Label:            c.Name,
						Kind:             CIKClass,
						Detail:           "imported component",
						InsertText:       c.Name + "($1)",
						InsertTextFormat: ITFSnippet,
					})
				}
				return items
			}
		}
	}

	if pkgDoc == nil {
		return nil
	}

	var items []CompletionItem

	// Components from the package (excluding sngl.X overrides)
	for _, c := range pkgDoc.Components {
		if strings.Contains(c.Name, ".") {
			continue // skip sngl.X overrides
		}
		items = append(items, CompletionItem{
			Label:            c.Name,
			Kind:             CIKClass,
			Detail:           nsName + " component",
			InsertText:       c.Name + "($1)",
			InsertTextFormat: ITFSnippet,
		})
	}

	// Structs
	for _, s := range pkgDoc.Structs {
		items = append(items, CompletionItem{
			Label:  s.Name,
			Kind:   CIKStruct,
			Detail: nsName + " struct",
		})
	}

	// Enums
	for _, e := range pkgDoc.Enums {
		items = append(items, CompletionItem{
			Label:  e.Name,
			Kind:   CIKEnum,
			Detail: nsName + " enum",
		})
	}

	return items
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || (ch >= '0' && ch <= '9')
}
