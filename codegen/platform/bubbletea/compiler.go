package bubbletea

import (
	"fmt"
	"go/format"
	"maps"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"github.com/google/cel-go/cel"
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

type forLoopCursor struct {
	cursorField string // model field name, e.g. "todosCursor"
	listField   string // model list field, e.g. "Todos"
	focusIdx    int    // focus index of this group
	indexVar    string // for-loop index variable, e.g. "index"
	iterVar     string // for-loop element variable, e.g. "item"
	changeExpr  *ast.Expr
}

type analysisResult struct {
	binds          []bindInfo
	computeds      []computedInfo
	inputs         []inputInfo
	focusables     []string // ordered: "input0", "button0", etc.
	forCursors     []forLoopCursor
	components     []*ast.Component
	structs        []*ast.StructDef
	modelFields    map[string]bool   // all bind/computed names (fields)
	computedFields map[string]bool   // computed names (methods, not struct fields)
	triggers       map[string]string // data field name → trigger func name
	needsTime      bool              // emit "time" import
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
		triggers:       make(map[string]string),
	}

	// Data fields (non-extern, non-func only)
	for _, d := range doc.Data {
		if d.Extern || d.IsFunc {
			continue
		}
		goType := inferGoType(d.Init)
		initVal := literalToGo(d.Init)
		if needsTimeType(d.Init.TypeHint) {
			info.needsTime = true
		}
		info.binds = append(info.binds, bindInfo{
			name:    d.Name,
			goType:  goType,
			initVal: initVal,
		})
		info.modelFields[d.Name] = true
		if d.Trigger != "" {
			info.triggers[d.Name] = d.Trigger
		}
	}

	// Computeds
	for _, c := range doc.Computeds {
		goType := inferGoType(c.Expr)
		if goType == "any" && c.Expr.AST != nil {
			goType = celOutputTypeToGo(c.Expr.AST.OutputType())
		} else if goType == "any" && c.Expr.SNGL != nil {
			goType = snglNodeGoType(c.Expr.SNGL)
		}
		info.computeds = append(info.computeds, computedInfo{
			name:   c.Name,
			goType: goType,
		})
		info.modelFields[c.Name] = true
		info.computedFields[c.Name] = true
	}

	// Components and structs
	info.components = doc.Components
	info.structs = doc.Structs

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
		// Extract bind target from on:input event: set(field, event.value) / field = event.value
		if inputEvt, ok := vn.Events["input"]; ok {
			bindTarget = extractAssignTarget(inputEvt.SNGL)
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
	case "checkbox":
		chkCount := 0
		for _, f := range info.focusables {
			if strings.HasPrefix(f, "checkbox") {
				chkCount++
			}
		}
		focusName := fmt.Sprintf("checkbox%d", chkCount)
		focusIdx := len(info.focusables)
		info.focusables = append(info.focusables, focusName)
		// For-looped checkboxes with on:change need a cursor
		if vn.For != nil && vn.For.IndexVar != "" {
			if changeEvt, ok := vn.Events["change"]; ok {
				listField := ""
				if ident, ok := vn.For.Iterable.SNGL.(*ast.IdentExpr); ok {
					listField = ident.Name
				}
				info.forCursors = append(info.forCursors, forLoopCursor{
					cursorField: listField + "Cursor",
					listField:   listField,
					focusIdx:    focusIdx,
					indexVar:    vn.For.IndexVar,
					iterVar:     vn.For.Variable,
					changeExpr:  &changeEvt,
				})
			}
		}
		idx++
	}

	for _, child := range vn.Children {
		idx = walkForFocusables(child, info, idx)
	}
	return idx
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
	var b strings.Builder

	structFields := make(map[string][]string)
	for _, sd := range info.structs {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		structFields[sd.Name] = fields
	}

	ec := &exprContext{
		modelFields:    info.modelFields,
		computedFields: info.computedFields,
		localVars:      make(map[string]bool),
		structNames:    structFields,
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
	if info.needsTime {
		b.WriteString("\t\"time\"\n")
	}
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

	// Time helper functions
	if info.needsTime {
		b.WriteString("func mustParseDuration(s string) time.Duration {\n")
		b.WriteString("\td, err := time.ParseDuration(s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn d\n}\n\n")

		b.WriteString("func mustParseDate(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(\"2006-01-02\", s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn t\n}\n\n")

		b.WriteString("func mustParseTime(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(\"15:04:05\", s)\n")
		b.WriteString("\tif err != nil {\n")
		b.WriteString("\t\tt, err = time.Parse(\"15:04\", s)\n")
		b.WriteString("\t\tif err != nil { panic(err) }\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn t\n}\n\n")

		b.WriteString("func mustParseDateTime(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(time.RFC3339, s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn t\n}\n\n")
	}

	// Struct types
	for _, sd := range info.structs {
		fmt.Fprintf(&b, "type %s struct {\n", exportName(sd.Name))
		for _, f := range sd.Fields {
			fmt.Fprintf(&b, "\t%s %s\n", exportName(f.Name), typeHintToGo(f.Type))
		}
		b.WriteString("}\n\n")
	}

	// Model struct
	b.WriteString("// Model is the Bubble Tea model for this SNGL UI.\n")
	b.WriteString("type Model struct {\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t%s %s\n", bind.name, bind.goType)
	}
	// Trigger callback fields
	for _, bind := range info.binds {
		if trigger, ok := info.triggers[bind.name]; ok {
			cbField := unexportName(trigger)
			fmt.Fprintf(&b, "\t%s func(%s)\n", cbField, bind.goType)
		}
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
	for _, fc := range info.forCursors {
		fmt.Fprintf(&b, "\t%s int\n", fc.cursorField)
	}
	if len(info.forCursors) > 0 {
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
		fmt.Fprintf(&b, "\t\t%s: %s,\n", bind.name, bind.initVal)
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
				if c.Expr.SNGL != nil {
					body = ec.translateExpr(c.Expr.SNGL)
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

	// Getters, setters, Msg/Cmd types, trigger registration
	emitGettersSetters(&b, info)

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

func emitGettersSetters(b *strings.Builder, info *analysisResult) {
	for _, bind := range info.binds {
		getter := exportName(bind.name)
		// Getter
		fmt.Fprintf(b, "func (m Model) %s() %s {\n", getter, bind.goType)
		fmt.Fprintf(b, "\treturn m.%s\n", bind.name)
		b.WriteString("}\n\n")

		// Setter
		fmt.Fprintf(b, "func (m Model) Set%s(v %s) Model {\n", getter, bind.goType)
		fmt.Fprintf(b, "\tm.%s = v\n", bind.name)
		// Sync bound inputs
		for _, inp := range info.inputs {
			if inp.bindTarget == bind.name && bind.goType == "string" {
				fmt.Fprintf(b, "\tm.%s.SetValue(m.%s)\n", inp.fieldName, bind.name)
			}
		}
		// Fire trigger callback
		if trigger, ok := info.triggers[bind.name]; ok {
			cbField := unexportName(trigger)
			fmt.Fprintf(b, "\tif m.%s != nil {\n", cbField)
			fmt.Fprintf(b, "\t\tm.%s(v)\n", cbField)
			b.WriteString("\t}\n")
		}
		b.WriteString("\treturn m\n")
		b.WriteString("}\n\n")

		// Msg type
		fmt.Fprintf(b, "type set%sMsg struct{ value %s }\n\n", getter, bind.goType)

		// Cmd function
		fmt.Fprintf(b, "func Set%sCmd(v %s) tea.Cmd {\n", getter, bind.goType)
		fmt.Fprintf(b, "\treturn func() tea.Msg { return set%sMsg{value: v} }\n", getter)
		b.WriteString("}\n\n")
	}

	// Trigger registration methods
	for _, bind := range info.binds {
		trigger, ok := info.triggers[bind.name]
		if !ok {
			continue
		}
		cbField := unexportName(trigger)
		fmt.Fprintf(b, "func (m Model) %s(fn func(%s)) Model {\n", trigger, bind.goType)
		fmt.Fprintf(b, "\tm.%s = fn\n", cbField)
		b.WriteString("\treturn m\n")
		b.WriteString("}\n\n")
	}
}

func emitUpdate(b *strings.Builder, info *analysisResult, doc *ast.Document, ec *exprContext, cfg Config) {
	b.WriteString("func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {\n")
	b.WriteString("\tvar cmd tea.Cmd\n")
	b.WriteString("\tswitch msg := msg.(type) {\n")

	// Set messages from Cmd functions
	for _, bind := range info.binds {
		getter := exportName(bind.name)
		fmt.Fprintf(b, "\tcase set%sMsg:\n", getter)
		fmt.Fprintf(b, "\t\tm = m.Set%s(msg.value)\n", getter)
	}

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
		checkboxIdx := 0
		emitButtonHandlers(b, doc.App.Children, info, ec, &buttonIdx, &checkboxIdx)
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

func emitButtonHandlers(b *strings.Builder, nodes []*ast.VisualNode, info *analysisResult, ec *exprContext, buttonIdx *int, checkboxIdx *int) {
	for _, vn := range nodes {
		if vn.Component == "checkbox" {
			if changeEvt, ok := vn.Events["change"]; ok {
				if changeEvt.SNGL != nil {
					focusIdx := -1
					for i, f := range info.focusables {
						if f == fmt.Sprintf("checkbox%d", *checkboxIdx) {
							focusIdx = i
							break
						}
					}
					if focusIdx >= 0 {
						var fc *forLoopCursor
						for i := range info.forCursors {
							if info.forCursors[i].focusIdx == focusIdx {
								fc = &info.forCursors[i]
								break
							}
						}
						if fc != nil {
							ec.localVars[fc.indexVar] = true
							stmts := ec.translateMutation(changeEvt.SNGL)
							delete(ec.localVars, fc.indexVar)
							fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
							fmt.Fprintf(b, "\t\t\tif m.%s < len(m.%s) {\n", fc.cursorField, fc.listField)
							fmt.Fprintf(b, "\t\t\t\t%s := m.%s\n", fc.indexVar, fc.cursorField)
							for _, stmt := range stmts {
								fmt.Fprintf(b, "\t\t\t\t%s\n", stmt)
							}
							fmt.Fprintf(b, "\t\t\t}\n")
							fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyUp && m.focus == %d:\n", focusIdx)
							fmt.Fprintf(b, "\t\t\tif m.%s > 0 { m.%s-- }\n", fc.cursorField, fc.cursorField)
							fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyDown && m.focus == %d:\n", focusIdx)
							fmt.Fprintf(b, "\t\t\tif m.%s < len(m.%s)-1 { m.%s++ }\n", fc.cursorField, fc.listField, fc.cursorField)
						} else {
							stmts := ec.translateMutation(changeEvt.SNGL)
							fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
							for _, stmt := range stmts {
								fmt.Fprintf(b, "\t\t\t%s\n", stmt)
							}
							mutatedFields := extractMutatedFields(changeEvt.SNGL)
							for _, inp := range info.inputs {
								if inp.bindTarget != "" && mutatedFields[inp.bindTarget] {
									fmt.Fprintf(b, "\t\t\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
								}
							}
						}
					}
				}
			}
			*checkboxIdx++
		}
		if vn.Component == "button" {
			if clickEvt, ok := vn.Events["click"]; ok {
				if clickEvt.SNGL != nil {
					focusIdx := -1
					for i, f := range info.focusables {
						if f == fmt.Sprintf("button%d", *buttonIdx) {
							focusIdx = i
							break
						}
					}
					if focusIdx >= 0 {
						stmts := ec.translateMutation(clickEvt.SNGL)
						fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
						for _, stmt := range stmts {
							fmt.Fprintf(b, "\t\t\t%s\n", stmt)
						}
						mutatedFields := extractMutatedFields(clickEvt.SNGL)
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
		emitButtonHandlers(b, vn.Children, info, ec, buttonIdx, checkboxIdx)
	}
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
		forCursors:  info.forCursors,
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
	maps.Copy(savedLocals, ec.localVars)
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

// extractAssignTarget extracts the target field name from an assignment SNGL node.
func extractAssignTarget(e ast.Node) string {
	switch n := e.(type) {
	case *ast.AssignStmt:
		if ident, ok := n.Target.(*ast.IdentExpr); ok {
			return ident.Name
		}
	case *ast.StmtBlock:
		if len(n.Stmts) > 0 {
			return extractAssignTarget(n.Stmts[0])
		}
	}
	return ""
}

// snglNodeGoType infers a Go type from a SNGL expression node.
func snglNodeGoType(e ast.Node) string {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		switch n.Kind {
		case ast.LiteralInt:
			return "int"
		case ast.LiteralFloat:
			return "float64"
		case ast.LiteralBool:
			return "bool"
		case ast.LiteralString:
			return "string"
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte, ast.BinAnd, ast.BinOr:
			return "bool"
		default:
			return snglNodeGoType(n.Left)
		}
	case *ast.UnaryExpr:
		if n.Op == ast.UnaryNot {
			return "bool"
		}
		return snglNodeGoType(n.Operand)
	case *ast.TernaryExpr:
		return snglNodeGoType(n.Then)
	case *ast.CallExpr:
		switch n.Func {
		case "string":
			return "string"
		case "int":
			return "int"
		case "float":
			return "float64"
		case "size":
			return "int"
		}
	}
	return "any"
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

func unexportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
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
	if strings.HasPrefix(hint, "[]") {
		return "[]" + exportName(hint[2:])
	}
	if strings.HasPrefix(hint, "list:") {
		return "[]" + exportName(hint[5:])
	}
	if strings.HasPrefix(hint, "enum:") {
		return "string"
	}
	switch hint {
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "string"
	case "date", "time", "dateTime":
		return "time.Time"
	case "duration":
		return "time.Duration"
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
	case t.IsEquivalentType(checker.ColorType),
		t.IsEquivalentType(checker.URLType),
		t.IsEquivalentType(checker.EmailType),
		t.IsEquivalentType(checker.UUIDType),
		t.IsEquivalentType(checker.RegexType),
		t.IsEquivalentType(checker.Base64Type),
		t.IsEquivalentType(checker.IPV4Type),
		t.IsEquivalentType(checker.IPV6Type),
		t.IsEquivalentType(checker.HostnameType),
		t.IsEquivalentType(checker.IDNEmailType),
		t.IsEquivalentType(checker.IDNHostnameType),
		t.IsEquivalentType(checker.IRLType),
		t.IsEquivalentType(checker.IRLReferenceType),
		t.IsEquivalentType(checker.URLReferenceType),
		t.IsEquivalentType(checker.URLTemplateType),
		t.IsEquivalentType(checker.CurrencyType),
		t.IsEquivalentType(checker.Country2Type),
		t.IsEquivalentType(checker.Country3Type),
		t.IsEquivalentType(checker.CountrySubdivisionType),
		t.IsEquivalentType(checker.DecimalType):
		return "string"
	case t.IsEquivalentType(checker.DateType),
		t.IsEquivalentType(checker.TimeType),
		t.IsEquivalentType(checker.DateTimeType):
		return "time.Time"
	case t.IsEquivalentType(checker.DurationType):
		return "time.Duration"
	default:
		return "any"
	}
}

func needsTimeType(hint string) bool {
	switch hint {
	case "date", "time", "dateTime", "duration":
		return true
	}
	return false
}

func literalToGo(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			switch expr.TypeHint {
			case "duration":
				return fmt.Sprintf("mustParseDuration(%q)", v)
			case "date":
				return fmt.Sprintf("mustParseDate(%q)", v)
			case "time":
				return fmt.Sprintf("mustParseTime(%q)", v)
			case "dateTime":
				return fmt.Sprintf("mustParseDateTime(%q)", v)
			default:
				return fmt.Sprintf("%q", v)
			}
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
	if expr.TypeHint != "" {
		return "nil"
	}
	return `""`
}
