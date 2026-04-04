package bubbletea

import (
	"fmt"
	"go/format"
	"maps"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// Config controls code generation.
type Config struct {
	Package      string // Go package name (default: "ui")
	ScaleFactor  int    // pixels per terminal cell (default: 8)
	GenerateMain bool   // emit a main() function for standalone apps
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		if c.GenerateMain {
			c.Package = "main"
		} else {
			c.Package = "ui"
		}
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

type timerInfo struct {
	index      int
	intervalMs int
	activeVar  string
	body       ast.Node
}

type externInfo struct {
	name       string
	goType     string
	isFunc     bool
	paramTypes []string
	returnType string
}

type analysisResult struct {
	binds          []bindInfo
	externs        []externInfo
	computeds      []computedInfo
	inputs         []inputInfo
	focusables     []string // ordered: "input0", "button0", etc.
	forCursors     []forLoopCursor
	timers         []timerInfo
	goImports      map[string]bool // native Go import paths from Resolved fields
	components     []*ast.Component
	structs        []*ast.StructDef
	modelFields    map[string]bool   // all bind/computed names (fields)
	computedFields map[string]bool   // computed names (methods, not struct fields)
	externFuncs    map[string]bool   // extern function names
	triggers       map[string]string // data field name → trigger func name
	needsTime      bool              // emit "time" import
	needsToast     bool              // emit toast queue infrastructure
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
		externFuncs:    make(map[string]bool),
		triggers:       make(map[string]string),
		goImports:      make(map[string]bool),
	}

	// Data fields
	for _, d := range doc.Data {
		if d.Resolved != nil && d.Resolved.NativePkg != "" {
			info.goImports[d.Resolved.NativePkg] = true
		}
		if d.Extern || d.IsFunc {
			// Extern functions and variables → model fields set by host
			ext := externInfo{
				name:   d.Name,
				isFunc: d.IsFunc,
			}
			if d.IsFunc {
				ext.paramTypes = d.ParamTypes
				ext.returnType = d.ReturnType
				ext.goType = externFuncGoType(d.ParamTypes, d.ReturnType)
				info.externFuncs[d.Name] = true
			} else if d.Resolved != nil && d.Resolved.NativeType != "" {
				ext.goType = d.Resolved.NativeType
			} else {
				ext.goType = typeHintToGo(d.Init.TypeHint)
			}
			info.externs = append(info.externs, ext)
			info.modelFields[d.Name] = true
			continue
		}
		goType := inferGoType(d.Init)
		if d.Resolved != nil && d.Resolved.NativeType != "" {
			goType = d.Resolved.NativeType
		}
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

	// Computed functions (zero-arg expression-form)
	for _, fn := range doc.Functions {
		if fn.Body.SNGL != nil && len(fn.Params) == 0 && !fn.IsStdlib {
			goType := inferGoType(fn.Body)
			if goType == "any" && fn.Body.SNGL != nil {
				goType = snglNodeGoType(fn.Body.SNGL)
			}
			info.computeds = append(info.computeds, computedInfo{
				name:   fn.Name,
				goType: goType,
			})
			info.modelFields[fn.Name] = true
			info.computedFields[fn.Name] = true
		}
	}

	// Timers
	for i, t := range doc.Timers {
		ms := intervalToMs(t.Interval)
		info.timers = append(info.timers, timerInfo{
			index:      i,
			intervalMs: ms,
			activeVar:  t.Active,
			body:       t.Body,
		})
		if ms > 0 {
			info.needsTime = true
		}
	}

	// Components and structs
	info.components = doc.AllComponents()
	info.structs = doc.Structs

	// Collect Go imports from struct fields with Resolved info
	for _, sd := range doc.Structs {
		for _, f := range sd.Fields {
			if f.Resolved != nil && f.Resolved.NativePkg != "" {
				info.goImports[f.Resolved.NativePkg] = true
			}
		}
	}

	// Walk visual tree to find inputs and buttons
	if doc.App != nil {
		focusIdx := 0
		for _, child := range doc.App.Children {
			focusIdx = walkForFocusables(child, info, focusIdx)
		}
	}

	// Detect Alert.toast/info/warn/error calls in event handlers, timers, and functions
	if !info.needsToast {
		info.needsToast = astUsesAlert(doc)
		if info.needsToast {
			info.needsTime = true
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
	if len(info.goImports) > 0 {
		sorted := make([]string, 0, len(info.goImports))
		for pkg := range info.goImports {
			sorted = append(sorted, pkg)
		}
		slices.Sort(sorted)
		for _, pkg := range sorted {
			fmt.Fprintf(&b, "\t%q\n", pkg)
		}
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
			goType := typeHintToGo(f.Type)
			if f.Resolved != nil && f.Resolved.NativeType != "" {
				goType = f.Resolved.NativeType
			}
			fmt.Fprintf(&b, "\t%s %s\n", exportName(f.Name), goType)
		}
		b.WriteString("}\n\n")
	}

	// Timer tick message types
	for _, t := range info.timers {
		fmt.Fprintf(&b, "type timerTickMsg%d struct{}\n", t.index)
	}
	if len(info.timers) > 0 {
		b.WriteString("\n")
	}

	// Toast infrastructure
	if info.needsToast {
		b.WriteString("type snglToast struct {\n\tmessage string\n\tvariant string\n}\n\n")
		b.WriteString("type toastDismissMsg struct{}\n\n")
	}

	// Model struct
	b.WriteString("// Model is the Bubble Tea model for this SNGL UI.\n")
	b.WriteString("type Model struct {\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t%s %s\n", bind.name, bind.goType)
	}
	// Extern fields (set by host before Init)
	for _, ext := range info.externs {
		fmt.Fprintf(&b, "\t%s %s // extern\n", exportName(ext.name), ext.goType)
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
	if info.needsToast {
		b.WriteString("\ttoasts []snglToast\n")
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

	// Computed methods (zero-arg expression-form functions)
	for _, comp := range info.computeds {
		body := ""
		for _, fn := range doc.Functions {
			if fn.Name == comp.name && fn.Body.SNGL != nil && len(fn.Params) == 0 {
				if fn.Body.SNGL != nil {
					body = ec.translateExpr(fn.Body.SNGL)
				} else if fn.Body.Literal != nil {
					body = literalToGo(fn.Body)
				}
				break
			}
		}
		fmt.Fprintf(&b, "func (m Model) %s() %s {\n", comp.name, comp.goType)
		fmt.Fprintf(&b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}

	// User-defined functions (skip stdlib — codegens use native calls)
	for _, fn := range doc.Functions {
		if fn.IsStdlib {
			continue
		}
		emitGoFunc(&b, fn, ec)
	}

	// Getters, setters, Msg/Cmd types, trigger registration
	emitGettersSetters(&b, info)

	// Init()
	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.timers) > 0 {
		b.WriteString("\tvar cmds []tea.Cmd\n")
		if len(info.inputs) > 0 {
			b.WriteString("\tcmds = append(cmds, textinput.Blink)\n")
		}
		for _, t := range info.timers {
			fmt.Fprintf(&b, "\tif m.%s {\n", t.activeVar)
			fmt.Fprintf(&b, "\t\tcmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))\n", t.intervalMs, t.index)
			b.WriteString("\t}\n")
		}
		b.WriteString("\treturn tea.Batch(cmds...)\n")
	} else if len(info.inputs) > 0 {
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

func emitGoFunc(b *strings.Builder, fn *ast.FuncDef, ec *exprContext) {
	// Build param list
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		goType := "any"
		switch p.Type {
		case "int":
			goType = "int"
		case "float":
			goType = "float64"
		case "bool":
			goType = "bool"
		case "string":
			goType = "string"
		}
		params[i] = p.Name + " " + goType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.ReturnType != "" {
		switch fn.ReturnType {
		case "int":
			retType = "int"
		case "float":
			retType = "float64"
		case "bool":
			retType = "bool"
		case "string":
			retType = "string"
		default:
			retType = "any"
		}
	}

	// Type-attached functions are standalone (no receiver)
	isTypeMethod := strings.Contains(fn.Name, ".")
	goName := exportName(fn.Name)
	if typeName, methodName, ok := ast.SplitMethodName(fn.Name); ok {
		goName = exportName(typeName) + exportName(methodName)
	}

	// Void functions use pointer receiver (mutation)
	receiver := "m Model"
	if fn.ReturnType == "" {
		receiver = "m *Model"
	}

	if fn.Body.SNGL != nil {
		// Add params as local vars for translation
		for _, p := range fn.Params {
			ec.localVars[p.Name] = true
		}
		body := ec.translateExpr(fn.Body.SNGL)
		for _, p := range fn.Params {
			delete(ec.localVars, p.Name)
		}
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	} else if fn.Block != nil {
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		for _, p := range fn.Params {
			ec.localVars[p.Name] = true
		}
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				ec.localVars[s.Name] = true
				val := ec.translateExpr(s.Init)
				fmt.Fprintf(b, "\t%s := %s\n", s.Name, val)
			default:
				stmts := ec.translateMutation(stmt)
				for _, line := range stmts {
					fmt.Fprintf(b, "\t%s\n", line)
				}
			}
		}
		if fn.Block.Return != nil {
			ret := ec.translateExpr(fn.Block.Return)
			fmt.Fprintf(b, "\treturn %s\n", ret)
		}
		// Clean up local vars
		for _, p := range fn.Params {
			delete(ec.localVars, p.Name)
		}
		for _, stmt := range fn.Block.Stmts {
			if s, ok := stmt.(*ast.VarStmt); ok {
				delete(ec.localVars, s.Name)
			}
		}
		b.WriteString("}\n\n")
	}
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

	// Timer tick messages
	for _, t := range info.timers {
		fmt.Fprintf(b, "\tcase timerTickMsg%d:\n", t.index)
		fmt.Fprintf(b, "\t\tif m.%s {\n", t.activeVar)
		// Emit body mutations
		stmts := ec.translateMutation(t.body)
		for _, s := range stmts {
			fmt.Fprintf(b, "\t\t\t%s\n", s)
		}
		// Re-schedule
		fmt.Fprintf(b, "\t\t\tif m.%s {\n", t.activeVar)
		fmt.Fprintf(b, "\t\t\t\tcmd = tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} })\n", t.intervalMs, t.index)
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
	}

	// Toast dismiss
	if info.needsToast {
		b.WriteString("\tcase toastDismissMsg:\n")
		b.WriteString("\t\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\t\tm.toasts = m.toasts[1:]\n")
		b.WriteString("\t\t\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\t\t\tcmd = tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} })\n")
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
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

	// Schedule toast dismiss if a toast was added
	if info.needsToast {
		b.WriteString("\tif len(m.toasts) > 0 && cmd == nil {\n")
		b.WriteString("\t\tcmd = tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} })\n")
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
							mutatedFields := codegen.MutatedFields(changeEvt.SNGL)
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
						mutatedFields := codegen.MutatedFields(clickEvt.SNGL)
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

	// Toast overlay
	if info.needsToast {
		b.WriteString("\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\tt := m.toasts[0]\n")
		b.WriteString("\t\tvar bg string\n")
		b.WriteString("\t\tswitch t.variant {\n")
		b.WriteString("\t\tcase \"success\": bg = \"#2e7d32\"\n")
		b.WriteString("\t\tcase \"error\": bg = \"#c62828\"\n")
		b.WriteString("\t\tcase \"warn\", \"warning\": bg = \"#f57f17\"\n")
		b.WriteString("\t\tdefault: bg = \"#1565c0\"\n")
		b.WriteString("\t\t}\n")
		b.WriteString("\t\ttoastStyle := lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(\"#ffffff\"))\n")
		b.WriteString("\t\tcontent = lipgloss.JoinVertical(lipgloss.Left, content, toastStyle.Render(t.message))\n")
		b.WriteString("\t}\n")
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
	// If component accepts children, add a slotContent parameter
	hasSlot := comp.ChildrenType != ""
	if hasSlot {
		params = append(params, "slotContent string")
	}

	fmt.Fprintf(b, "func (m Model) %s(%s) string {\n", methodName, strings.Join(params, ", "))

	// Add params as local vars
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, ec.localVars)
	for _, p := range comp.Params {
		ec.localVars[p.Name] = true
	}

	var slotVar string
	if hasSlot {
		slotVar = "slotContent"
	}

	vc := &viewContext{
		ec:          ec,
		scaleFactor: cfg.ScaleFactor,
		buf:         &strings.Builder{},
		indent:      1,
		focusIndex:  0,
		components:  allComponents,
		inComponent: true,
		slotVar:     slotVar,
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

// astUsesAlert returns true if the document contains any Alert.toast/info/warn/error calls.
func astUsesAlert(doc *ast.Document) bool {
	// Check event handlers in visual nodes
	if doc.App != nil {
		if slices.ContainsFunc(doc.App.Children, nodeUsesAlert) {
			return true
		}
	}
	// Check timer bodies
	for _, t := range doc.Timers {
		if exprNodeUsesAlert(t.Body) {
			return true
		}
	}
	// Check function blocks
	for _, fn := range doc.Functions {
		if fn.Block != nil {
			if slices.ContainsFunc(fn.Block.Stmts, exprNodeUsesAlert) {
				return true
			}
		}
	}
	return false
}

func nodeUsesAlert(vn *ast.VisualNode) bool {
	for _, evt := range vn.Events {
		if evt.SNGL != nil && exprNodeUsesAlert(evt.SNGL) {
			return true
		}
	}
	return slices.ContainsFunc(vn.Children, nodeUsesAlert)
}

func exprNodeUsesAlert(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.MethodExpr:
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok && ident.Name == "Alert" {
			return true
		}
	case *ast.StmtBlock:
		if slices.ContainsFunc(e.Stmts, exprNodeUsesAlert) {
			return true
		}
	case *ast.CallStmt:
		return exprNodeUsesAlert(e.Call)
	}
	return false
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
		case ast.BinDiv:
			return "float64"
		default:
			lt := snglNodeGoType(n.Left)
			rt := snglNodeGoType(n.Right)
			if lt == "float64" || rt == "float64" {
				return "float64"
			}
			return lt
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
	case *ast.InterpolationExpr:
		return "string"
	case *ast.MethodExpr:
		// String methods return string
		switch n.Method {
		case "upper", "lower", "trim", "replace", "substring":
			return "string"
		case "length", "indexOf":
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

func intervalToMs(expr ast.Expr) int {
	if expr.SNGL == nil {
		return 0
	}
	lit, ok := expr.SNGL.(*ast.LiteralExpr)
	if !ok || lit.Kind != ast.LiteralUnit {
		return 0
	}
	ul, ok := lit.Value.(ast.UnitLiteral)
	if !ok {
		return 0
	}
	num := 0.0
	fmt.Sscanf(ul.Number, "%f", &num)
	switch ul.Suffix {
	case "ms":
		return int(num)
	case "s":
		return int(num * 1000)
	case "m":
		return int(num * 60000)
	case "h":
		return int(num * 3600000)
	}
	return int(num)
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
		return "[]" + typeHintToGo(hint[2:])
	}
	if strings.HasPrefix(hint, "list:") {
		return "[]" + typeHintToGo(hint[5:])
	}
	if strings.HasPrefix(hint, "option:") {
		return "*" + typeHintToGo(hint[7:])
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
		// Package-qualified type (e.g., "ast.File") — pass through
		if strings.Contains(hint, ".") && !strings.ContainsAny(hint, ":~<>") {
			return hint
		}
		// User-defined struct types are simple identifiers; anything
		// containing special chars (func:, unit:, etc.) is unknown.
		if !strings.ContainsAny(hint, ":~<>") && hint != "" {
			return exportName(hint)
		}
		return "any"
	}
}

// externFuncGoType builds a Go function type from SNGL param/return types.
// e.g., ["string", "int"] + "bool" → "func(string, int) bool"
func externFuncGoType(paramTypes []string, returnType string) string {
	params := make([]string, len(paramTypes))
	for i, p := range paramTypes {
		params[i] = typeHintToGo(p)
	}
	sig := "func(" + strings.Join(params, ", ") + ")"
	if returnType != "" {
		sig += " " + typeHintToGo(returnType)
	}
	return sig
}

func checkerTypeToGo(t checker.Type) string {
	switch t {
	case checker.Int:
		return "int"
	case checker.Float:
		return "float64"
	case checker.Bool:
		return "bool"
	case checker.String, checker.Color,
		checker.URL, checker.Email, checker.UUID,
		checker.Regex, checker.Base64, checker.IPV4,
		checker.IPV6, checker.Hostname, checker.IDNEmail,
		checker.IDNHostname, checker.IRL, checker.IRLReference,
		checker.URLReference, checker.URLTemplate,
		checker.Currency, checker.Country2, checker.Country3,
		checker.CountrySubdivision, checker.Decimal:
		return "string"
	case checker.Date, checker.Time, checker.DateTime:
		return "time.Time"
	case checker.Duration:
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
	// Option types: wrap concrete literals with address-of via inline func
	if strings.HasPrefix(expr.TypeHint, "option:") {
		if expr.Literal == nil && expr.SNGL == nil {
			return "nil"
		}
		inner := expr
		inner.TypeHint = expr.TypeHint[7:]
		val := literalToGo(inner)
		if val == "nil" {
			return "nil"
		}
		goType := typeHintToGo(inner.TypeHint)
		return fmt.Sprintf("func() *%s { v := %s; return &v }()", goType, val)
	}
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
		// Foreign struct types use zero-value constructor, not nil
		if expr.Resolved != nil && expr.Resolved.NativeType != "" {
			return expr.Resolved.NativeType + "{}"
		}
		return "nil"
	}
	return `""`
}
