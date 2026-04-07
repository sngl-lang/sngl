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
	changeExpr  *ast.Expr
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

	// Platform-specific data field analysis (Go types, init values, externs)
	for _, d := range doc.Data {
		if d.Resolved != nil && d.Resolved.NativePkg != "" {
			info.goImports[d.Resolved.NativePkg] = resolvePackageName(d.Resolved.NativePkg)
		}
		if d.Extern || d.IsFunc {
			ext := externInfo{
				name:   d.Name,
				isFunc: d.IsFunc,
			}
			if d.IsFunc {
				ext.paramTypes = d.ParamTypes
				ext.returnType = d.ReturnType
				ext.goType = externFuncGoType(d.ParamTypes, d.ReturnType)
			} else if d.Resolved != nil && d.Resolved.NativeType != "" {
				ext.goType = d.Resolved.NativeType
			} else {
				ext.goType = typeHintToGo(d.Init.TypeHint)
			}
			info.externs = append(info.externs, ext)
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
	}

	// Platform-specific computed function analysis (Go types)
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
		}
	}

	// Check timer intervals for time import
	for _, t := range common.Timers {
		if t.IntervalMs > 0 {
			info.needsTime = true
		}
	}

	// Collect Go imports from struct fields with Resolved info
	for _, sd := range doc.Structs {
		for _, f := range sd.Fields {
			if f.Resolved != nil && f.Resolved.NativePkg != "" {
				info.goImports[f.Resolved.NativePkg] = resolvePackageName(f.Resolved.NativePkg)
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

	// Toast needs time
	if common.NeedsToast {
		info.needsTime = true
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
			bindTarget = extractAssignTarget(inputEvt.Body.SNGL)
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
				body := changeEvt.Body
				info.forCursors = append(info.forCursors, forLoopCursor{
					cursorField: listField + "Cursor",
					listField:   listField,
					focusIdx:    focusIdx,
					indexVar:    vn.For.IndexVar,
					iterVar:     vn.For.Variable,
					changeExpr:  &body,
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
			goType := typeHintToGo(f.Type)
			if f.Resolved != nil && f.Resolved.NativeType != "" {
				goType = f.Resolved.NativeType
			}
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

	// Computed methods (zero-arg expression-form functions)
	for _, comp := range info.computeds {
		body := ""
		for _, fn := range doc.Functions {
			if fn.Name == comp.name && fn.Body.SNGL != nil && len(fn.Params) == 0 {
				if fn.Body.SNGL != nil {
					body = ec.TranslateExpr(fn.Body.SNGL)
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

	// User-defined functions (skip stdlib, test, and computed functions which
	// are already emitted as lowercase methods above)
	for _, fn := range doc.Functions {
		if fn.IsStdlib || fn.IsTest() {
			continue
		}
		// Skip computed functions — already emitted above
		if fn.Body.SNGL != nil && len(fn.Params) == 0 {
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
	for _, comp := range doc.Components {
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
			ec.LocalVars[p.Name] = true
		}
		body := ec.TranslateExpr(fn.Body.SNGL)
		for _, p := range fn.Params {
			delete(ec.LocalVars, p.Name)
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
			ec.LocalVars[p.Name] = true
		}
		for _, stmt := range fn.Block.Stmts {
			switch s := stmt.(type) {
			case *ast.VarStmt:
				ec.LocalVars[s.Name] = true
				val := ec.TranslateExpr(s.Init)
				fmt.Fprintf(b, "\t%s := %s\n", s.Name, val)
			default:
				stmts := ec.TranslateMutation(stmt)
				for _, line := range stmts {
					fmt.Fprintf(b, "\t%s\n", line)
				}
			}
		}
		if fn.Block.Return != nil {
			ret := ec.TranslateExpr(fn.Block.Return)
			fmt.Fprintf(b, "\treturn %s\n", ret)
		}
		// Clean up local vars
		for _, p := range fn.Params {
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
		for _, d := range doc.Data {
			if d.Name != bind.name {
				continue
			}
			for _, ev := range d.Events {
				if ev.Kind == "change" {
					stmts := ec.TranslateMutation(ev.Body)
					for _, s := range stmts {
						fmt.Fprintf(b, "\t%s\n", s)
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
		stmts := ec.TranslateMutation(t.Body)
		for _, s := range stmts {
			fmt.Fprintf(b, "\t\t\t%s\n", s)
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

func emitButtonHandlers(b *strings.Builder, nodes []*ast.VisualNode, info *analysisResult, ec *exprContext, buttonIdx *int, checkboxIdx *int) {
	for _, vn := range nodes {
		if vn.Component == "checkbox" {
			if changeEvt, ok := vn.Events["change"]; ok {
				if changeEvt.Body.SNGL != nil {
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
							ec.LocalVars[fc.indexVar] = true
							stmts := ec.TranslateMutation(changeEvt.Body.SNGL)
							delete(ec.LocalVars, fc.indexVar)
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
							stmts := ec.TranslateMutation(changeEvt.Body.SNGL)
							fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
							for _, stmt := range stmts {
								fmt.Fprintf(b, "\t\t\t%s\n", stmt)
							}
							mutatedFields := codegen.MutatedFields(changeEvt.Body.SNGL)
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
				if clickEvt.Body.SNGL != nil {
					focusIdx := -1
					for i, f := range info.focusables {
						if f == fmt.Sprintf("button%d", *buttonIdx) {
							focusIdx = i
							break
						}
					}
					if focusIdx >= 0 {
						stmts := ec.TranslateMutation(clickEvt.Body.SNGL)
						fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
						for _, stmt := range stmts {
							fmt.Fprintf(b, "\t\t\t%s\n", stmt)
						}
						mutatedFields := codegen.MutatedFields(clickEvt.Body.SNGL)
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
		doc:         doc,
		components:  info.Components,
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
	maps.Copy(savedLocals, ec.LocalVars)
	for _, p := range comp.Params {
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
	ec.LocalVars = savedLocals
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
