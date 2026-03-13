package compiler

import (
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
)

// Config controls code generation.
type Config struct {
	Package      string // Go package name (default: "ui")
	ScaleFactor  int    // pixels per terminal cell (default: 8)
	GenerateMain bool   // emit a main() function for standalone apps
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		c.Package = "ui"
	}
	if c.ScaleFactor == 0 {
		c.ScaleFactor = 8
	}
	return c
}

// Compile generates a Go source file from a checked SNGL document.
func Compile(doc *ast.Document, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	info := analyze(doc)
	src := emit(info, doc, cfg)
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("generated code formatting error: %w\n%s", err, src)
	}
	return formatted, nil
}

// analysis results

type bindInfo struct {
	name    string
	goType  string
	initVal string // Go expression for default value
}

type computedInfo struct {
	name   string
	goType string
	body   string // Go expression
}

type inputInfo struct {
	fieldName   string // e.g., "input0"
	bindTarget  string // bind field being synced, e.g., "Name"
	placeholder string
}

type analysisResult struct {
	binds          []bindInfo
	computeds      []computedInfo
	inputs         []inputInfo
	focusables     []string // ordered: "input0", "button0", etc.
	components     []*ast.Component
	modelFields    map[string]bool // all bind/computed names (fields)
	computedFields map[string]bool // computed names (methods, not struct fields)
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
	}

	// Binds
	for _, b := range doc.Binds {
		goType := inferGoType(b.Init)
		initVal := literalToGo(b.Init)
		info.binds = append(info.binds, bindInfo{
			name:    b.Name,
			goType:  goType,
			initVal: initVal,
		})
		info.modelFields[b.Name] = true
	}

	// Computeds
	for _, c := range doc.Computeds {
		goType := inferGoType(c.Expr)
		if goType == "any" && c.Expr.AST != nil {
			goType = celOutputTypeToGo(c.Expr.AST.OutputType())
		}
		info.computeds = append(info.computeds, computedInfo{
			name:   c.Name,
			goType: goType,
		})
		info.modelFields[c.Name] = true
		info.computedFields[c.Name] = true
	}

	// Components
	info.components = doc.Components

	// Walk visual tree to find inputs and buttons
	if doc.App != nil {
		focusIdx := 0
		for _, child := range doc.App.Children {
			focusIdx = walkForFocusables(child, info, focusIdx)
		}
	}

	return info
}

func walkForFocusables(vn *ast.VisualNode, info *analysisResult, idx int) int {
	switch vn.Component {
	case "input":
		fieldName := fmt.Sprintf("input%d", len(info.inputs))
		placeholder := ""
		if v, ok := vn.Props["placeholder"]; ok {
			if s, ok := v.Literal.(string); ok {
				placeholder = s
			}
		}
		bindTarget := ""
		// Extract bind target from on:input event: set(field, event.value)
		if inputEvt, ok := vn.Events["input"]; ok {
			if inputEvt.AST != nil {
				native := inputEvt.AST.NativeRep()
				expr := native.Expr()
				if expr.Kind() == celast.CallKind {
					call := expr.AsCall()
					if call.FunctionName() == "set" && len(call.Args()) >= 1 {
						firstArg := call.Args()[0]
						if firstArg.Kind() == celast.IdentKind {
							bindTarget = exportName(firstArg.AsIdent())
						}
					}
				}
			}
		}
		info.inputs = append(info.inputs, inputInfo{
			fieldName:   fieldName,
			bindTarget:  bindTarget,
			placeholder: placeholder,
		})
		info.focusables = append(info.focusables, fieldName)
		idx++
	case "button":
		btnCount := 0
		for _, f := range info.focusables {
			if strings.HasPrefix(f, "button") {
				btnCount++
			}
		}
		info.focusables = append(info.focusables, fmt.Sprintf("button%d", btnCount))
		idx++
	}

	for _, child := range vn.Children {
		idx = walkForFocusables(child, info, idx)
	}
	return idx
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
	var b strings.Builder

	ec := &exprContext{
		modelFields:    info.modelFields,
		computedFields: info.computedFields,
		localVars:      make(map[string]bool),
	}

	// Package
	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)

	// Imports
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n")
	if cfg.GenerateMain {
		b.WriteString("\t\"os\"\n")
	}
	b.WriteString("\t\"strings\"\n")
	b.WriteString("\n")
	b.WriteString("\ttea \"charm.land/bubbletea/v2\"\n")
	b.WriteString("\t\"charm.land/lipgloss/v2\"\n")
	if len(info.inputs) > 0 {
		b.WriteString("\t\"charm.land/bubbles/v2/textinput\"\n")
	}
	b.WriteString(")\n\n")

	// Suppress unused import warnings
	b.WriteString("var _ = fmt.Sprint\n")
	b.WriteString("var _ = strings.Join\n\n")

	// Ternary helper
	b.WriteString("func ternary[T any](cond bool, a, b T) T {\n")
	b.WriteString("\tif cond {\n\t\treturn a\n\t}\n\treturn b\n}\n\n")

	// Model struct
	b.WriteString("// Model is the Bubble Tea model for this SNGL UI.\n")
	b.WriteString("type Model struct {\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t%s %s\n", exportName(bind.name), bind.goType)
	}
	if len(info.binds) > 0 {
		b.WriteString("\n")
	}
	for _, inp := range info.inputs {
		fmt.Fprintf(&b, "\t%s textinput.Model\n", inp.fieldName)
	}
	if len(info.inputs) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("\tfocus int\n")
	b.WriteString("\twidth, height int\n")
	b.WriteString("}\n\n")

	// New()
	b.WriteString("// New creates a Model with default bind values.\n")
	b.WriteString("func New() Model {\n")
	b.WriteString("\tm := Model{\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t\t%s: %s,\n", exportName(bind.name), bind.initVal)
	}
	b.WriteString("\t}\n")
	// Initialize inputs
	for i, inp := range info.inputs {
		fmt.Fprintf(&b, "\tm.%s = textinput.New()\n", inp.fieldName)
		if inp.placeholder != "" {
			fmt.Fprintf(&b, "\tm.%s.Placeholder = %q\n", inp.fieldName, inp.placeholder)
		}
		if inp.bindTarget != "" {
			fmt.Fprintf(&b, "\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
		}
		if i == 0 {
			fmt.Fprintf(&b, "\tm.%s.Focus()\n", inp.fieldName)
		}
	}
	b.WriteString("\treturn m\n")
	b.WriteString("}\n\n")

	// Computed methods
	for _, comp := range info.computeds {
		// Build the expression body from the CEL AST
		body := ""
		for _, c := range doc.Computeds {
			if c.Name == comp.name {
				if c.Expr.AST != nil {
					native := c.Expr.AST.NativeRep()
					ec.nativeAST = native
					body = ec.translateExpr(native.Expr())
				} else if c.Expr.Literal != nil {
					body = literalToGo(c.Expr)
				}
				break
			}
		}
		fmt.Fprintf(&b, "func (m Model) %s() %s {\n", comp.name, comp.goType)
		fmt.Fprintf(&b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}

	// Init()
	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.inputs) > 0 {
		b.WriteString("\treturn textinput.Blink\n")
	} else {
		b.WriteString("\treturn nil\n")
	}
	b.WriteString("}\n\n")

	// Update()
	emitUpdate(&b, info, doc, ec, cfg)

	// View()
	emitView(&b, info, doc, ec, cfg)

	// User component render methods
	for _, comp := range info.components {
		emitComponentMethod(&b, comp, info.components, ec, cfg)
	}

	// main() for standalone apps
	if cfg.GenerateMain {
		b.WriteString("func main() {\n")
		b.WriteString("\tp := tea.NewProgram(New())\n")
		b.WriteString("\tif _, err := p.Run(); err != nil {\n")
		b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"error: %v\\n\", err)\n")
		b.WriteString("\t\tos.Exit(1)\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n")
	}

	return []byte(b.String())
}

func emitUpdate(b *strings.Builder, info *analysisResult, doc *ast.Document, ec *exprContext, cfg Config) {
	b.WriteString("func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {\n")
	b.WriteString("\tvar cmd tea.Cmd\n")
	b.WriteString("\tswitch msg := msg.(type) {\n")

	// WindowSizeMsg
	b.WriteString("\tcase tea.WindowSizeMsg:\n")
	b.WriteString("\t\tm.width = msg.Width\n")
	b.WriteString("\t\tm.height = msg.Height\n")

	// KeyPressMsg
	b.WriteString("\tcase tea.KeyPressMsg:\n")
	b.WriteString("\t\tswitch {\n")
	b.WriteString("\t\tcase msg.Code == 'c' && msg.Mod == tea.ModCtrl:\n")
	b.WriteString("\t\t\treturn m, tea.Quit\n")

	if len(info.focusables) > 1 {
		nFocus := len(info.focusables)
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == 0:\n")
		fmt.Fprintf(b, "\t\t\tm.focus = (m.focus + 1) %% %d\n", nFocus)
		emitFocusSync(b, info)
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == tea.ModShift:\n")
		fmt.Fprintf(b, "\t\t\tm.focus = (m.focus - 1 + %d) %% %d\n", nFocus, nFocus)
		emitFocusSync(b, info)
	}

	// Enter key for buttons
	if doc.App != nil {
		buttonIdx := 0
		emitButtonHandlers(b, doc.App.Children, info, ec, &buttonIdx)
	}

	b.WriteString("\t\t}\n") // end switch

	b.WriteString("\t}\n") // end type switch

	// Forward messages to focused input
	for i, inp := range info.inputs {
		fmt.Fprintf(b, "\tif m.focus == %d {\n", i)
		fmt.Fprintf(b, "\t\tm.%s, cmd = m.%s.Update(msg)\n", inp.fieldName, inp.fieldName)
		if inp.bindTarget != "" {
			fmt.Fprintf(b, "\t\tm.%s = m.%s.Value()\n", inp.bindTarget, inp.fieldName)
		}
		b.WriteString("\t}\n")
	}

	b.WriteString("\treturn m, cmd\n")
	b.WriteString("}\n\n")
}

func emitFocusSync(b *strings.Builder, info *analysisResult) {
	for i, inp := range info.inputs {
		fmt.Fprintf(b, "\t\t\tif m.focus == %d {\n", i)
		fmt.Fprintf(b, "\t\t\t\tm.%s.Focus()\n", inp.fieldName)
		b.WriteString("\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\tm.%s.Blur()\n", inp.fieldName)
		b.WriteString("\t\t\t}\n")
	}
}

func emitButtonHandlers(b *strings.Builder, nodes []*ast.VisualNode, info *analysisResult, ec *exprContext, buttonIdx *int) {
	for _, vn := range nodes {
		if vn.Component == "button" {
			if clickEvt, ok := vn.Events["click"]; ok {
				if clickEvt.AST != nil {
					// Find the focusable index for this button
					focusIdx := -1
					for i, f := range info.focusables {
						if f == fmt.Sprintf("button%d", *buttonIdx) {
							focusIdx = i
							break
						}
					}
					if focusIdx >= 0 {
						native := clickEvt.AST.NativeRep()
						ec.nativeAST = native
						stmts := ec.translateMutation(native.Expr())
						fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
						for _, stmt := range stmts {
							fmt.Fprintf(b, "\t\t\t%s\n", stmt)
						}
						// Sync inputs whose bind targets were mutated
						mutatedFields := extractMutatedFields(native.Expr())
						for _, inp := range info.inputs {
							if inp.bindTarget != "" && mutatedFields[inp.bindTarget] {
								fmt.Fprintf(b, "\t\t\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
							}
						}
					}
				}
			}
			*buttonIdx++
		}
		emitButtonHandlers(b, vn.Children, info, ec, buttonIdx)
	}
}

// extractMutatedFields returns the set of exported field names mutated by a CEL mutation expression.
func extractMutatedFields(e celast.Expr) map[string]bool {
	fields := make(map[string]bool)
	if e.Kind() == celast.ListKind {
		for _, el := range e.AsList().Elements() {
			for k, v := range extractMutatedFields(el) {
				fields[k] = v
			}
		}
		return fields
	}
	if e.Kind() == celast.CallKind {
		call := e.AsCall()
		args := call.Args()
		if len(args) >= 1 && args[0].Kind() == celast.IdentKind {
			fields[exportName(args[0].AsIdent())] = true
		}
	}
	return fields
}

func emitView(b *strings.Builder, info *analysisResult, doc *ast.Document, ec *exprContext, cfg Config) {
	b.WriteString("func (m Model) View() tea.View {\n")

	if doc.App == nil || len(doc.App.Children) == 0 {
		b.WriteString("\treturn tea.NewView(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	vc := &viewContext{
		ec:          ec,
		scaleFactor: cfg.ScaleFactor,
		buf:         &strings.Builder{},
		indent:      1,
		focusIndex:  0,
		components:  info.components,
	}

	// Render each top-level child
	if len(doc.App.Children) == 1 {
		vc.line("var content string")
		vc.renderNode(doc.App.Children[0], "content")
		b.WriteString(vc.buf.String())
	} else {
		vc.line("var parts []string")
		for i, child := range doc.App.Children {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`content := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
	}

	b.WriteString("\tv := tea.NewView(content)\n")
	b.WriteString("\tv.AltScreen = true\n")
	b.WriteString("\treturn v\n")
	b.WriteString("}\n\n")
}

func emitComponentMethod(b *strings.Builder, comp *ast.Component, allComponents []*ast.Component, ec *exprContext, cfg Config) {
	methodName := "render" + exportName(comp.Name)

	// Build parameter list
	var params []string
	for _, p := range comp.Params {
		goType := inferGoType(p.Default)
		params = append(params, p.Name+" "+goType)
	}

	fmt.Fprintf(b, "func (m Model) %s(%s) string {\n", methodName, strings.Join(params, ", "))

	// Add params as local vars
	savedLocals := make(map[string]bool)
	for k, v := range ec.localVars {
		savedLocals[k] = v
	}
	for _, p := range comp.Params {
		ec.localVars[p.Name] = true
	}

	vc := &viewContext{
		ec:          ec,
		scaleFactor: cfg.ScaleFactor,
		buf:         &strings.Builder{},
		indent:      1,
		focusIndex:  0,
		components:  allComponents,
		inComponent: true,
	}

	if len(comp.Body) == 1 {
		vc.line("var result string")
		vc.renderNode(comp.Body[0], "result")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []string")
		for i, child := range comp.Body {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderNode(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`result := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	}

	b.WriteString("}\n\n")

	// Restore local vars
	ec.localVars = savedLocals
}

// Helper functions

func exportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func inferGoType(expr ast.Expr) string {
	if expr.TypeHint != "" {
		return typeHintToGo(expr.TypeHint)
	}
	if expr.Literal != nil {
		switch expr.Literal.(type) {
		case int:
			return "int"
		case float64:
			return "float64"
		case bool:
			return "bool"
		case string:
			return "string"
		}
	}
	return "any"
}

func typeHintToGo(hint string) string {
	switch hint {
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string":
		return "string"
	default:
		return "any"
	}
}

func celOutputTypeToGo(t *cel.Type) string {
	switch {
	case t.IsEquivalentType(cel.IntType):
		return "int"
	case t.IsEquivalentType(cel.DoubleType):
		return "float64"
	case t.IsEquivalentType(cel.BoolType):
		return "bool"
	case t.IsEquivalentType(cel.StringType):
		return "string"
	default:
		return "any"
	}
}

func literalToGo(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return `""`
}
