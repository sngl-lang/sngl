package bubbletea

import (
	"fmt"
	"go/format"
	"maps"
	"path"
	"sort"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"golang.org/x/tools/go/packages"
)

var pkgNameCache sync.Map // import path → package name

// resolvePackageName returns the Go package name for the given import path.
func resolvePackageName(importPath string) string {
	if name, ok := pkgNameCache.Load(importPath); ok {
		return name.(string)
	}
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedName}, importPath)
	if err == nil && len(pkgs) > 0 && pkgs[0].Name != "" {
		pkgNameCache.Store(importPath, pkgs[0].Name)
		return pkgs[0].Name
	}
	// Fallback to last path segment.
	name := path.Base(importPath)
	pkgNameCache.Store(importPath, name)
	return name
}

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
}

type externInfo struct {
	name       string
	goType     string
	isFunc     bool
	paramTypes []string
	returnType string
}

type analysisResult struct {
	*codegen.CommonAnalysis
	binds      []bindInfo
	externs    []externInfo
	computeds  []computedInfo
	inputs     []inputInfo
	focusables []string // ordered: "input0", "button0", etc.
	forCursors []forLoopCursor
	goImports  map[string]string // import path → namespace alias (only imports used by generated types)
	needsTime  bool              // emit "time" import
}

func analyze(doc *ast.Document) *analysisResult {
	common := codegen.AnalyzeCommon(doc)

	info := &analysisResult{
		CommonAnalysis: common,
		goImports:      make(map[string]string),
	}

	// Note: go:// imports are not added to goImports here because they may
	// not be referenced by the generated bubbletea code (e.g. when the View
	// is empty due to platform-specific bodies). They are added below only
	// when a data field or struct field has a Resolved type that needs them.

	// Platform-specific data field analysis (Go types, init values)
	for _, dv := range docVars(doc) {
		goType := inferGoType(dv.Init)
		hint := exprTypeHint(dv.Type)
		if hint != "" {
			goType = typeHintToGo(hint)
		}
		initVal := literalToGo(dv.Init)
		if needsTimeType(hint) {
			info.needsTime = true
		}
		info.binds = append(info.binds, bindInfo{
			name:    dv.Name,
			goType:  goType,
			initVal: initVal,
		})
	}

	// Platform-specific computed function analysis (Go types)
	for _, fn := range docFuncDefs(doc) {
		if isComputed(fn) {
			goType := inferGoType(fn.Body)
			if goType == "any" && fn.Body != nil {
				goType = snglNodeGoType(fn.Body)
			}
			info.computeds = append(info.computeds, computedInfo{
				name:   fn.Name,
				goType: goType,
			})
		}
	}

	// Check timer intervals for time import
	for _, t := range common.Timers {
		if t.IntervalMs > 0 {
			info.needsTime = true
		}
	}

	// Collect Go imports from struct fields (placeholder for future resolved info)
	_ = docStructDefs(doc)

	// Walk visual tree to find inputs and buttons
	bodyStmts := docBodyStmts(doc)
	if len(bodyStmts) > 0 {
		focusIdx := 0
		for _, stmt := range bodyStmts {
			focusIdx = walkStmtForFocusables(stmt, info, focusIdx)
		}
	}

	// Toast needs time
	if common.NeedsToast {
		info.needsTime = true
	}

	return info
}

// walkStmtForFocusables dispatches statement types for focusable scanning.
func walkStmtForFocusables(stmt ast.Stmt, info *analysisResult, idx int) int {
	switch s := stmt.(type) {
	case *ast.VisualNode:
		idx = walkForFocusables(s, info, idx)
	case *ast.IfStmt:
		for _, child := range s.Body.Stmts {
			idx = walkStmtForFocusables(child, info, idx)
		}
		for _, child := range s.Else.Stmts {
			idx = walkStmtForFocusables(child, info, idx)
		}
	case *ast.ForStmt:
		for _, child := range s.Body.Stmts {
			idx = walkStmtForFocusables(child, info, idx)
		}
	case *ast.PlatformStmt:
		for _, child := range s.Body.Stmts {
			idx = walkStmtForFocusables(child, info, idx)
		}
	}
	return idx
}

func walkForFocusables(vn *ast.VisualNode, info *analysisResult, idx int) int {
	name := vnName(vn)
	switch name {
	case "input":
		fieldName := fmt.Sprintf("input%d", len(info.inputs))
		placeholder := ""
		if v := vnProp(vn, "placeholder"); v != nil {
			if s, ok := codegen.ExprLiteralString(v); ok {
				placeholder = s
			}
		}
		bindTarget := ""
		// Extract bind target from on:input event
		if inputEvt := vnEvent(vn, "input"); inputEvt != nil {
			if len(inputEvt.Body.Stmts) > 0 {
				bindTarget = extractAssignTarget(inputEvt.Body.Stmts[0])
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
		idx++
		_ = focusIdx // for-loop cursor logic would go here if needed
	}

	for _, child := range vnChildren(vn) {
		idx = walkStmtForFocusables(child, info, idx)
	}
	return idx
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
	var b strings.Builder

	structFields := make(map[string][]string)
	for _, sd := range info.Structs {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name)
		}
		structFields[sd.Name] = fields
	}

	ec := &exprContext{
		ModelFields:    info.ModelFields,
		ComputedFields: info.ComputedFields,
		LocalVars:      make(map[string]bool),
		StructNames:    structFields,
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
		var pkgs []string
		for pkg := range info.goImports {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		for _, pkg := range pkgs {
			ns := info.goImports[pkg]
			// Use the resolved namespace as an explicit alias so the
			// generated code's type references (e.g. docs.Component) resolve.
			fmt.Fprintf(&b, "\t%s %q\n", ns, pkg)
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
	b.WriteString("var _ = strings.Join\n")
	b.WriteString("var _ = lipgloss.NewStyle\n\n")

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
	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "type %s struct {\n", exportName(sd.Name))
		for _, f := range sd.Fields {
			goType := typeHintToGo(exprTypeHint(f.Type))
			fmt.Fprintf(&b, "\t%s %s\n", exportName(f.Name), goType)
		}
		b.WriteString("}\n\n")
	}

	// Timer tick message types
	for _, t := range info.Timers {
		fmt.Fprintf(&b, "type timerTickMsg%d struct{}\n", t.Index)
	}
	if len(info.Timers) > 0 {
		b.WriteString("\n")
	}

	// Toast infrastructure
	if info.NeedsToast {
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
	if info.NeedsToast {
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

	// SetTerminalSize allows callers to set terminal dimensions from outside the package.
	b.WriteString("// SetTerminalSize sets the terminal dimensions.\n")
	b.WriteString("func (m *Model) SetTerminalSize(w, h int) {\n")
	b.WriteString("\tm.width = w\n")
	b.WriteString("\tm.height = h\n")
	b.WriteString("}\n\n")

	// Computed methods (zero-arg expression-form functions)
	fns := docFuncDefs(doc)
	for _, comp := range info.computeds {
		body := ""
		for _, fn := range fns {
			if fn.Name == comp.name && isComputed(fn) {
				if fn.Body != nil {
					body = ec.TranslateExpr(fn.Body)
				}
				break
			}
		}
		fmt.Fprintf(&b, "func (m Model) %s() %s {\n", comp.name, comp.goType)
		fmt.Fprintf(&b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}

	// User-defined functions (skip methods, test, and computed functions which
	// are already emitted as lowercase methods above)
	for _, fn := range fns {
		if strings.Contains(fn.Name, ".") || fn.IsTest() {
			continue
		}
		// Skip computed functions — already emitted above
		if isComputed(fn) {
			continue
		}
		emitGoFunc(&b, fn, ec)
	}

	// Getters, setters, Msg/Cmd types
	emitGettersSetters(&b, info, doc, ec)

	// Init()
	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.Timers) > 0 {
		b.WriteString("\tvar cmds []tea.Cmd\n")
		if len(info.inputs) > 0 {
			b.WriteString("\tcmds = append(cmds, textinput.Blink)\n")
		}
		for _, t := range info.Timers {
			fmt.Fprintf(&b, "\tif m.%s {\n", t.ActiveVar)
			fmt.Fprintf(&b, "\t\tcmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))\n", t.IntervalMs, t.Index)
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

	// User component render methods (not stdlib overrides)
	for _, comp := range docComponents(doc) {
		emitComponentMethod(&b, comp, info.Components, ec, cfg)
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
	fnParams := fn.Params.Params
	params := make([]string, len(fnParams))
	for i, p := range fnParams {
		goType := "any"
		hint := exprTypeHint(p.Type)
		switch hint {
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

	retHint := funcReturnType(fn)
	retType := ""
	if retHint != "" {
		switch retHint {
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
	if fn.ReturnType == nil {
		receiver = "m *Model"
	}

	if fn.Body != nil {
		// Add params as local vars for translation
		for _, p := range fnParams {
			ec.LocalVars[p.Name] = true
		}
		body := ec.TranslateExpr(fn.Body)
		for _, p := range fnParams {
			delete(ec.LocalVars, p.Name)
		}
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	} else if len(fn.Block.Stmts) > 0 {
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		for _, p := range fnParams {
			ec.LocalVars[p.Name] = true
		}
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				ec.LocalVars[s.Name] = true
				val := ec.TranslateExpr(s.Init)
				fmt.Fprintf(b, "\t%s := %s\n", s.Name, val)
			case *ast.ReturnStmt:
				if s.Value != nil {
					ret := ec.TranslateExpr(s.Value)
					fmt.Fprintf(b, "\treturn %s\n", ret)
				} else {
					b.WriteString("\treturn\n")
				}
			default:
				stmts := ec.TranslateMutation(stmt)
				for _, line := range stmts {
					fmt.Fprintf(b, "\t%s\n", line)
				}
			}
		}
		// Clean up local vars
		for _, p := range fnParams {
			delete(ec.LocalVars, p.Name)
		}
		for _, stmt := range fn.Block.Stmts {
			if s, ok := stmt.(*ast.VarStmt); ok {
				delete(ec.LocalVars, s.Name)
			}
		}
		b.WriteString("}\n\n")
	}
}

func emitGettersSetters(b *strings.Builder, info *analysisResult, doc *ast.Document, ec *exprContext) {
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
		// Inline @change event bodies
		for _, dv := range docVars(doc) {
			if dv.Name != bind.name {
				continue
			}
			for _, ev := range dv.Handlers {
				if ev.Name == "change" {
					for _, stmt := range ev.Body.Stmts {
						stmts := ec.TranslateMutation(stmt)
						for _, s := range stmts {
							fmt.Fprintf(b, "\t%s\n", s)
						}
					}
				}
			}
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
	for _, t := range info.Timers {
		fmt.Fprintf(b, "\tcase timerTickMsg%d:\n", t.Index)
		fmt.Fprintf(b, "\t\tif m.%s {\n", t.ActiveVar)
		// Emit body mutations
		for _, bodyStmt := range t.Body.Stmts {
			translated := ec.TranslateMutation(bodyStmt)
			for _, s := range translated {
				fmt.Fprintf(b, "\t\t\t%s\n", s)
			}
		}
		// Re-schedule
		fmt.Fprintf(b, "\t\t\tif m.%s {\n", t.ActiveVar)
		fmt.Fprintf(b, "\t\t\t\tcmd = tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} })\n", t.IntervalMs, t.Index)
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
	}

	// Toast dismiss
	if info.NeedsToast {
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
	bodyStmts := docBodyStmts(doc)
	if len(bodyStmts) > 0 {
		buttonIdx := 0
		checkboxIdx := 0
		emitButtonHandlersStmts(b, bodyStmts, info, ec, &buttonIdx, &checkboxIdx)
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
	if info.NeedsToast {
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

// emitButtonHandlersStmts dispatches statement types for button/checkbox handlers.
func emitButtonHandlersStmts(b *strings.Builder, stmts []ast.Stmt, info *analysisResult, ec *exprContext, buttonIdx *int, checkboxIdx *int) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VisualNode:
			emitButtonHandlersVN(b, s, info, ec, buttonIdx, checkboxIdx)
		case *ast.IfStmt:
			emitButtonHandlersStmts(b, s.Body.Stmts, info, ec, buttonIdx, checkboxIdx)
			emitButtonHandlersStmts(b, s.Else.Stmts, info, ec, buttonIdx, checkboxIdx)
		case *ast.ForStmt:
			emitButtonHandlersStmts(b, s.Body.Stmts, info, ec, buttonIdx, checkboxIdx)
		case *ast.PlatformStmt:
			emitButtonHandlersStmts(b, s.Body.Stmts, info, ec, buttonIdx, checkboxIdx)
		}
	}
}

func emitButtonHandlersVN(b *strings.Builder, vn *ast.VisualNode, info *analysisResult, ec *exprContext, buttonIdx *int, checkboxIdx *int) {
	name := vnName(vn)
	events := vnEvents(vn)

	if name == "checkbox" {
		if changeEvt, ok := events["change"]; ok {
			if len(changeEvt.Body.Stmts) > 0 {
				focusIdx := -1
				for i, f := range info.focusables {
					if f == fmt.Sprintf("checkbox%d", *checkboxIdx) {
						focusIdx = i
						break
					}
				}
				if focusIdx >= 0 {
					// Translate the event body stmts
					var translated []string
					for _, stmt := range changeEvt.Body.Stmts {
						translated = append(translated, ec.TranslateMutation(stmt)...)
					}
					fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
					for _, stmt := range translated {
						fmt.Fprintf(b, "\t\t\t%s\n", stmt)
					}
					// Collect mutated fields from all body stmts
					mutatedFields := make(map[string]bool)
					for _, stmt := range changeEvt.Body.Stmts {
						maps.Copy(mutatedFields, codegen.MutatedFields(stmt))
					}
					for _, inp := range info.inputs {
						if inp.bindTarget != "" && mutatedFields[inp.bindTarget] {
							fmt.Fprintf(b, "\t\t\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
						}
					}
				}
			}
		}
		*checkboxIdx++
	}
	if name == "button" {
		if clickEvt, ok := events["click"]; ok {
			if len(clickEvt.Body.Stmts) > 0 {
				focusIdx := -1
				for i, f := range info.focusables {
					if f == fmt.Sprintf("button%d", *buttonIdx) {
						focusIdx = i
						break
					}
				}
				if focusIdx >= 0 {
					var translated []string
					for _, stmt := range clickEvt.Body.Stmts {
						translated = append(translated, ec.TranslateMutation(stmt)...)
					}
					fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
					for _, stmt := range translated {
						fmt.Fprintf(b, "\t\t\t%s\n", stmt)
					}
					mutatedFields := make(map[string]bool)
					for _, stmt := range clickEvt.Body.Stmts {
						maps.Copy(mutatedFields, codegen.MutatedFields(stmt))
					}
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
	emitButtonHandlersStmts(b, vnChildren(vn), info, ec, buttonIdx, checkboxIdx)
}

func emitView(b *strings.Builder, info *analysisResult, doc *ast.Document, ec *exprContext, cfg Config) {
	b.WriteString("func (m Model) View() tea.View {\n")

	bodyStmts := docBodyStmts(doc)
	if len(bodyStmts) == 0 {
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
		doc:         doc,
		components:  info.Components,
	}

	// Render each top-level child
	if len(bodyStmts) == 1 {
		vc.line("var content string")
		vc.renderStmt(bodyStmts[0], "content")
		b.WriteString(vc.buf.String())
	} else {
		vc.line("var parts []string")
		for i, child := range bodyStmts {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`content := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
	}

	// Toast overlay
	if info.NeedsToast {
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

func emitComponentMethod(b *strings.Builder, comp *ast.ComponentDecl, allComponents []*ast.ComponentDecl, ec *exprContext, cfg Config) {
	methodName := "render" + exportName(comp.Name)

	// Build parameter list
	cParams := compParams(comp)
	var params []string
	for _, p := range cParams {
		goType := inferGoType(p.Default)
		params = append(params, p.Name+" "+goType)
	}
	// If component accepts children, add a slotContent parameter
	hasSlot := compHasChildren(comp)
	if hasSlot {
		params = append(params, "slotContent string")
	}

	fmt.Fprintf(b, "func (m Model) %s(%s) string {\n", methodName, strings.Join(params, ", "))

	// Add params as local vars
	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, ec.LocalVars)
	for _, p := range cParams {
		ec.LocalVars[p.Name] = true
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

	bodyStmts := compBodyStmts(comp)
	if len(bodyStmts) == 1 {
		vc.line("var result string")
		vc.renderStmt(bodyStmts[0], "result")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []string")
		for i, child := range bodyStmts {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s string", childVar)
			vc.renderStmt(child, childVar)
			vc.line("parts = append(parts, %s)", childVar)
		}
		vc.line(`result := lipgloss.JoinVertical(lipgloss.Left, parts...)`)
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	}

	b.WriteString("}\n\n")

	// Restore local vars
	ec.LocalVars = savedLocals
}

// extractAssignTarget extracts the target field name from an assignment SNGL node.
func extractAssignTarget(e ast.Stmt) string {
	if n, ok := e.(*ast.AssignStmt); ok {
		if ident, ok := n.Target.(*ast.IdentExpr); ok {
			return ident.Name
		}
	}
	return ""
}
