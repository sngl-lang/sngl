package fyne

import (
	"fmt"
	"go/format"
	"maps"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Config controls code generation.
type Config struct {
	Package      string // Go package name (default: "ui")
	GenerateMain bool   // emit a main() function for standalone apps
	AppName      string // application display name
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		if c.GenerateMain {
			c.Package = "main"
		} else {
			c.Package = "ui"
		}
	}
	if c.AppName == "" {
		c.AppName = "SNGL App"
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

// analysis types

type bindInfo struct {
	name    string
	goType  string
	initVal string
}

type computedInfo struct {
	name   string
	goType string
}

type entryInfo struct {
	fieldName   string // "entry0", "entry1"
	bindTarget  string // data field synced via OnChanged
	placeholder string
	multiLine   bool // textarea
	password    bool // type="password"
	rows        int  // textarea rows hint
}

type externInfo struct {
	name       string
	goType     string
	isFunc     bool
	paramTypes []string
	returnType string
}

type timerInfo struct {
	index      int
	intervalMs int
	activeVar  string
	body       ast.Node
}

// widgetUpdater tracks a function that updates one widget property.
type widgetUpdater struct {
	name string          // "updateLabel0"
	body string          // Go code: m.label0.SetText(fmt.Sprint(...))
	deps map[string]bool // which state fields this reads {"count": true}
}

// widgetField tracks a persistent widget stored on Model.
type widgetField struct {
	name   string // "label0"
	goType string // "*widget.Label"
}

type analysisResult struct {
	binds          []bindInfo
	externs        []externInfo
	computeds      []computedInfo
	entries        []entryInfo
	timers         []timerInfo
	components     []*ast.Component
	structs        []*ast.StructDef
	modelFields    map[string]bool
	computedFields map[string]bool
	computedDeps   map[string]map[string]bool // computed name → set of root state fields
	externFuncs    map[string]bool
	triggers       map[string]string
	needsTime      bool
	needsURL       bool
	needsCanvas    bool
	needsToast     bool
}

// expandDeps replaces computed field references with the root state fields they read.
func (info *analysisResult) expandDeps(deps map[string]bool) map[string]bool {
	result := make(map[string]bool)
	for d := range deps {
		result[d] = true
		if info.computedFields[d] {
			if compDeps, ok := info.computedDeps[d]; ok {
				for cd := range compDeps {
					result[cd] = true
				}
			}
		}
	}
	return result
}

// findAffectedUpdaters returns updaters whose deps intersect with mutated fields.
func (info *analysisResult) findAffectedUpdaters(updaters []widgetUpdater, mutated map[string]bool) []widgetUpdater {
	if len(mutated) == 0 {
		return nil
	}
	// Expand mutated fields through computed deps
	expanded := make(map[string]bool)
	for f := range mutated {
		expanded[f] = true
	}
	for compName, compDeps := range info.computedDeps {
		for dep := range compDeps {
			if mutated[dep] {
				expanded[compName] = true
			}
		}
	}

	var result []widgetUpdater
	for _, u := range updaters {
		for dep := range u.deps {
			if expanded[dep] {
				result = append(result, u)
				break
			}
		}
	}
	return result
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
		computedDeps:   make(map[string]map[string]bool),
		externFuncs:    make(map[string]bool),
		triggers:       make(map[string]string),
	}

	// Data fields
	for _, d := range doc.Data {
		if d.Extern || d.IsFunc {
			ext := externInfo{
				name:   d.Name,
				isFunc: d.IsFunc,
			}
			if d.IsFunc {
				ext.paramTypes = d.ParamTypes
				ext.returnType = d.ReturnType
				ext.goType = externFuncGoType(d.ParamTypes, d.ReturnType)
				info.externFuncs[d.Name] = true
			} else {
				ext.goType = typeHintToGo(d.Init.TypeHint)
			}
			info.externs = append(info.externs, ext)
			info.modelFields[d.Name] = true
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
		if goType == "any" && c.Expr.SNGL != nil {
			goType = snglNodeGoType(c.Expr.SNGL)
		}
		info.computeds = append(info.computeds, computedInfo{
			name:   c.Name,
			goType: goType,
		})
		info.modelFields[c.Name] = true
		info.computedFields[c.Name] = true
		// Build computed dependency map
		if c.Expr.SNGL != nil {
			info.computedDeps[c.Name] = extractDeps(c.Expr.SNGL, info.modelFields)
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

	// Walk visual tree to find entries
	if doc.App != nil {
		for _, child := range doc.App.Children {
			walkForEntries(child, info)
		}
	}

	// Check if we need url or canvas imports
	if doc.App != nil {
		walkForImports(doc.App.Children, info)
	}

	// Detect Alert.toast/info/warn/error calls
	if !info.needsToast {
		info.needsToast = astUsesAlert(doc)
		if info.needsToast {
			info.needsTime = true
		}
	}

	return info
}

func walkForEntries(vn *ast.VisualNode, info *analysisResult) {
	switch vn.Component {
	case "input":
		entry := entryInfo{
			fieldName: fmt.Sprintf("entry%d", len(info.entries)),
		}
		if v, ok := vn.Props["placeholder"]; ok {
			if s, ok := v.Literal.(string); ok {
				entry.placeholder = s
			}
		}
		if v, ok := vn.Props["type"]; ok {
			if s, ok := v.Literal.(string); ok && s == "password" {
				entry.password = true
			}
		}
		if inputEvt, ok := vn.Events["input"]; ok {
			entry.bindTarget = extractAssignTarget(inputEvt.SNGL)
		}
		info.entries = append(info.entries, entry)
	case "textarea":
		entry := entryInfo{
			fieldName: fmt.Sprintf("entry%d", len(info.entries)),
			multiLine: true,
		}
		if v, ok := vn.Props["placeholder"]; ok {
			if s, ok := v.Literal.(string); ok {
				entry.placeholder = s
			}
		}
		if v, ok := vn.Props["rows"]; ok {
			if n, ok := v.Literal.(int); ok {
				entry.rows = n
			}
		}
		if inputEvt, ok := vn.Events["input"]; ok {
			entry.bindTarget = extractAssignTarget(inputEvt.SNGL)
		}
		info.entries = append(info.entries, entry)
	}

	for _, child := range vn.Children {
		walkForEntries(child, info)
	}
}

func walkForImports(nodes []*ast.VisualNode, info *analysisResult) {
	for _, vn := range nodes {
		switch vn.Component {
		case "link":
			info.needsURL = true
		case "image":
			if _, ok := vn.Props["src"]; ok {
				info.needsCanvas = true
			}
		}
		walkForImports(vn.Children, info)
	}
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

	// --- Phase 1: Render BuildUI into a buffer, collecting widget fields + updaters ---
	var buildBuf strings.Builder
	var widgetFields []widgetField
	var updaters []widgetUpdater

	if doc.App != nil && len(doc.App.Children) > 0 {
		vc := &viewContext{
			ec:         ec,
			buf:        &buildBuf,
			indent:     1,
			components: info.components,
			info:       info,
		}

		if len(doc.App.Children) == 1 {
			vc.line("var content fyne.CanvasObject")
			vc.renderNode(doc.App.Children[0], "content")
			vc.line("if content == nil { content = widget.NewLabel(\"\") }")
		} else {
			vc.line("var parts []fyne.CanvasObject")
			for i, child := range doc.App.Children {
				childVar := fmt.Sprintf("part%d", i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderNode(child, childVar)
				vc.line("if %s != nil { parts = append(parts, %s) }", childVar, childVar)
			}
		}

		widgetFields = vc.widgetFields
		updaters = vc.updaters
	}

	// --- Phase 2: Emit the actual source ---

	// Package
	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)

	// Imports
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n")
	if cfg.GenerateMain {
		b.WriteString("\t\"os\"\n")
	}
	if info.needsURL {
		b.WriteString("\t\"net/url\"\n")
	}
	if info.needsTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n")
	b.WriteString("\t\"fyne.io/fyne/v2\"\n")
	if cfg.GenerateMain {
		b.WriteString("\t\"fyne.io/fyne/v2/app\"\n")
	}
	if info.needsCanvas {
		b.WriteString("\t\"fyne.io/fyne/v2/canvas\"\n")
	}
	b.WriteString("\t\"fyne.io/fyne/v2/container\"\n")
	b.WriteString("\t\"fyne.io/fyne/v2/layout\"\n")
	b.WriteString("\t\"fyne.io/fyne/v2/widget\"\n")
	b.WriteString(")\n\n")

	// Suppress unused import warnings
	b.WriteString("var _ = fmt.Sprint\n")
	b.WriteString("var _ fyne.CanvasObject\n")
	b.WriteString("var _ = container.NewVBox\n")
	b.WriteString("var _ = layout.NewSpacer\n")
	b.WriteString("var _ = widget.NewLabel\n\n")

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

	// Toast infrastructure
	if info.needsToast {
		b.WriteString("type snglToast struct {\n\tmessage string\n\tvariant string\n}\n\n")
	}

	// Model struct
	b.WriteString("// Model holds the state for this SNGL UI.\n")
	b.WriteString("type Model struct {\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t%s %s\n", bind.name, bind.goType)
	}
	// Extern fields
	for _, ext := range info.externs {
		fmt.Fprintf(&b, "\t%s %s // extern\n", exportName(ext.name), ext.goType)
	}
	// Trigger callbacks
	for _, bind := range info.binds {
		if trigger, ok := info.triggers[bind.name]; ok {
			cbField := unexportName(trigger)
			fmt.Fprintf(&b, "\t%s func(%s)\n", cbField, bind.goType)
		}
	}
	if len(info.binds) > 0 {
		b.WriteString("\n")
	}
	// Entry widget fields (persistent across rebuilds)
	for _, entry := range info.entries {
		fmt.Fprintf(&b, "\t%s *widget.Entry\n", entry.fieldName)
	}
	if len(info.entries) > 0 {
		b.WriteString("\n")
	}
	// Persistent widget fields collected during BuildUI rendering
	for _, wf := range widgetFields {
		fmt.Fprintf(&b, "\t%s %s\n", wf.name, wf.goType)
	}
	if len(widgetFields) > 0 {
		b.WriteString("\n")
	}
	// Timer ticker fields
	for _, t := range info.timers {
		fmt.Fprintf(&b, "\ttimer%dTicker *time.Ticker\n", t.index)
	}
	if info.needsToast {
		b.WriteString("\ttoasts []snglToast\n")
		b.WriteString("\ttoastLabel *widget.Label\n")
		b.WriteString("\ttoastBox *fyne.Container\n")
	}
	b.WriteString("}\n\n")

	// New()
	b.WriteString("// New creates a Model with default values.\n")
	b.WriteString("func New() *Model {\n")
	b.WriteString("\tm := &Model{\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t\t%s: %s,\n", bind.name, bind.initVal)
	}
	b.WriteString("\t}\n")

	// Initialize entries
	for _, entry := range info.entries {
		if entry.multiLine {
			fmt.Fprintf(&b, "\tm.%s = widget.NewMultiLineEntry()\n", entry.fieldName)
		} else if entry.password {
			fmt.Fprintf(&b, "\tm.%s = widget.NewPasswordEntry()\n", entry.fieldName)
		} else {
			fmt.Fprintf(&b, "\tm.%s = widget.NewEntry()\n", entry.fieldName)
		}
		if entry.placeholder != "" {
			fmt.Fprintf(&b, "\tm.%s.SetPlaceHolder(%q)\n", entry.fieldName, entry.placeholder)
		}
		if entry.bindTarget != "" {
			fmt.Fprintf(&b, "\tm.%s.SetText(fmt.Sprint(m.%s))\n", entry.fieldName, entry.bindTarget)
			fmt.Fprintf(&b, "\tm.%s.OnChanged = func(s string) {\n", entry.fieldName)
			fmt.Fprintf(&b, "\t\tm.%s = s\n", entry.bindTarget)
			// Call affected updaters instead of doRefresh
			entryMutated := map[string]bool{entry.bindTarget: true}
			affected := info.findAffectedUpdaters(updaters, entryMutated)
			if len(affected) > 0 {
				for _, u := range affected {
					fmt.Fprintf(&b, "\t\tm.%s()\n", u.name)
				}
			} else {
				b.WriteString("\t\tm.doRefresh()\n")
			}
			fmt.Fprintf(&b, "\t}\n")
		}
		if entry.rows > 0 {
			fmt.Fprintf(&b, "\tm.%s.SetMinRowsVisible(%d)\n", entry.fieldName, entry.rows)
		}
	}

	b.WriteString("\treturn m\n")
	b.WriteString("}\n\n")

	// doRefresh() — kept as fallback
	b.WriteString("func (m *Model) doRefresh() {\n")
	for _, u := range updaters {
		fmt.Fprintf(&b, "\tm.%s()\n", u.name)
	}
	b.WriteString("}\n\n")

	// Toast helper
	if info.needsToast {
		b.WriteString("func (m *Model) showToast(msg, variant string) {\n")
		b.WriteString("\tm.toasts = append(m.toasts, snglToast{msg, variant})\n")
		b.WriteString("\tm.updateToast()\n")
		b.WriteString("\ttime.AfterFunc(3*time.Second, func() {\n")
		b.WriteString("\t\tfyne.Do(func() {\n")
		b.WriteString("\t\t\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\t\t\tm.toasts = m.toasts[1:]\n")
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t\tm.updateToast()\n")
		b.WriteString("\t\t})\n")
		b.WriteString("\t})\n")
		b.WriteString("}\n\n")

		b.WriteString("func (m *Model) updateToast() {\n")
		b.WriteString("\tif m.toastLabel == nil { return }\n")
		b.WriteString("\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\tm.toastLabel.SetText(m.toasts[0].message)\n")
		b.WriteString("\t\tm.toastBox.Show()\n")
		b.WriteString("\t} else {\n")
		b.WriteString("\t\tm.toastBox.Hide()\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n\n")
	}

	// Computed methods
	for _, comp := range info.computeds {
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
		fmt.Fprintf(&b, "func (m *Model) %s() %s {\n", comp.name, comp.goType)
		fmt.Fprintf(&b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}

	// User-defined functions
	for _, fn := range doc.Functions {
		if fn.IsStdlib {
			continue
		}
		emitGoFunc(&b, fn, ec)
	}

	// Getters/setters — setters call affected updaters
	emitGettersSetters(&b, info, updaters)

	// StartTimers()
	if len(info.timers) > 0 {
		emitTimers(&b, info, ec, updaters)
	}

	// BuildUI() — creates widget tree once, returns root
	emitBuildUI(&b, info, doc, &buildBuf, updaters)

	// Updater methods
	emitUpdaters(&b, updaters)

	// User component render methods
	for _, comp := range info.components {
		emitComponentMethod(&b, comp, info.components, ec)
	}

	// main()
	if cfg.GenerateMain {
		emitMain(&b, cfg, info)
	}

	return []byte(b.String())
}

func emitGoFunc(b *strings.Builder, fn *ast.FuncDef, ec *exprContext) {
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

	isTypeMethod := strings.Contains(fn.Name, ".")
	goName := exportName(fn.Name)
	if typeName, methodName, ok := ast.SplitMethodName(fn.Name); ok {
		goName = exportName(typeName) + exportName(methodName)
	}

	receiver := "m *Model"

	if fn.Body.SNGL != nil {
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

func emitGettersSetters(b *strings.Builder, info *analysisResult, updaters []widgetUpdater) {
	for _, bind := range info.binds {
		getter := exportName(bind.name)

		// Getter
		fmt.Fprintf(b, "func (m *Model) %s() %s {\n", getter, bind.goType)
		fmt.Fprintf(b, "\treturn m.%s\n", bind.name)
		b.WriteString("}\n\n")

		// Setter — calls affected updaters instead of doRefresh
		fmt.Fprintf(b, "func (m *Model) Set%s(v %s) {\n", getter, bind.goType)
		fmt.Fprintf(b, "\tm.%s = v\n", bind.name)
		// Sync bound entries
		for _, entry := range info.entries {
			if entry.bindTarget == bind.name && bind.goType == "string" {
				fmt.Fprintf(b, "\tm.%s.SetText(v)\n", entry.fieldName)
			}
		}
		// Fire trigger callback
		if trigger, ok := info.triggers[bind.name]; ok {
			cbField := unexportName(trigger)
			fmt.Fprintf(b, "\tif m.%s != nil {\n", cbField)
			fmt.Fprintf(b, "\t\tm.%s(v)\n", cbField)
			b.WriteString("\t}\n")
		}
		// Call affected updaters
		mutated := map[string]bool{bind.name: true}
		affected := info.findAffectedUpdaters(updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				fmt.Fprintf(b, "\tm.%s()\n", u.name)
			}
		}
		b.WriteString("}\n\n")
	}

	// Trigger registration methods
	for _, bind := range info.binds {
		trigger, ok := info.triggers[bind.name]
		if !ok {
			continue
		}
		cbField := unexportName(trigger)
		fmt.Fprintf(b, "func (m *Model) %s(fn func(%s)) {\n", trigger, bind.goType)
		fmt.Fprintf(b, "\tm.%s = fn\n", cbField)
		b.WriteString("}\n\n")
	}
}

func emitTimers(b *strings.Builder, info *analysisResult, ec *exprContext, updaters []widgetUpdater) {
	b.WriteString("// StartTimers starts all active timers.\n")
	b.WriteString("func (m *Model) StartTimers() {\n")
	for _, t := range info.timers {
		fmt.Fprintf(b, "\tif m.%s {\n", t.activeVar)
		fmt.Fprintf(b, "\t\tm.timer%dTicker = time.NewTicker(%d * time.Millisecond)\n", t.index, t.intervalMs)
		fmt.Fprintf(b, "\t\tgo func() {\n")
		fmt.Fprintf(b, "\t\t\tfor range m.timer%dTicker.C {\n", t.index)
		b.WriteString("\t\t\t\tfyne.Do(func() {\n")
		stmts := ec.translateMutation(t.body)
		for _, s := range stmts {
			fmt.Fprintf(b, "\t\t\t\t\t%s\n", s)
		}
		// Call only affected updaters for this timer
		mutated := extractMutatedFields(t.body)
		affected := info.findAffectedUpdaters(updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				fmt.Fprintf(b, "\t\t\t\t\tm.%s()\n", u.name)
			}
		} else {
			b.WriteString("\t\t\t\t\tm.doRefresh()\n")
		}
		b.WriteString("\t\t\t\t})\n")
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}()\n")
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("// StopTimers stops all active timers.\n")
	b.WriteString("func (m *Model) StopTimers() {\n")
	for _, t := range info.timers {
		fmt.Fprintf(b, "\tif m.timer%dTicker != nil {\n", t.index)
		fmt.Fprintf(b, "\t\tm.timer%dTicker.Stop()\n", t.index)
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n\n")
}

func emitBuildUI(b *strings.Builder, info *analysisResult, doc *ast.Document, buildBuf *strings.Builder, updaters []widgetUpdater) {
	b.WriteString("// BuildUI creates the widget tree. Call once; widgets are updated selectively.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")

	if doc.App == nil || len(doc.App.Children) == 0 {
		b.WriteString("\treturn widget.NewLabel(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	// Write the buffered build code
	b.WriteString(buildBuf.String())

	// Return the root widget
	if len(doc.App.Children) == 1 {
		if info.needsToast {
			b.WriteString("\tm.toastLabel = widget.NewLabel(\"\")\n")
			b.WriteString("\tm.toastBox = container.NewVBox(m.toastLabel)\n")
			b.WriteString("\tm.toastBox.Hide()\n")
			b.WriteString("\treturn container.NewBorder(nil, m.toastBox, nil, nil, content)\n")
		} else {
			b.WriteString("\treturn content\n")
		}
	} else {
		if info.needsToast {
			b.WriteString("\tm.toastLabel = widget.NewLabel(\"\")\n")
			b.WriteString("\tm.toastBox = container.NewVBox(m.toastLabel)\n")
			b.WriteString("\tm.toastBox.Hide()\n")
			b.WriteString("\treturn container.NewBorder(nil, m.toastBox, nil, nil, container.NewVBox(parts...))\n")
		} else {
			b.WriteString("\treturn container.NewVBox(parts...)\n")
		}
	}

	b.WriteString("}\n\n")
}

func emitUpdaters(b *strings.Builder, updaters []widgetUpdater) {
	for _, u := range updaters {
		fmt.Fprintf(b, "func (m *Model) %s() {\n", u.name)
		fmt.Fprintf(b, "\t%s\n", u.body)
		b.WriteString("}\n\n")
	}
}

func emitComponentMethod(b *strings.Builder, comp *ast.Component, allComponents []*ast.Component, ec *exprContext) {
	methodName := "render" + exportName(comp.Name)

	var params []string
	for _, p := range comp.Params {
		goType := inferGoType(p.Default)
		params = append(params, p.Name+" "+goType)
	}

	fmt.Fprintf(b, "func (m *Model) %s(%s) fyne.CanvasObject {\n", methodName, strings.Join(params, ", "))

	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, ec.localVars)
	for _, p := range comp.Params {
		ec.localVars[p.Name] = true
	}

	vc := &viewContext{
		ec:         ec,
		buf:        &strings.Builder{},
		indent:     1,
		components: allComponents,
	}

	if len(comp.Body) == 1 {
		vc.line("var result fyne.CanvasObject")
		vc.renderNode(comp.Body[0], "result")
		vc.line("if result == nil { result = widget.NewLabel(\"\") }")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []fyne.CanvasObject")
		for i, child := range comp.Body {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderNode(child, childVar)
			vc.line("if %s != nil { parts = append(parts, %s) }", childVar, childVar)
		}
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn container.NewVBox(parts...)\n")
	}

	b.WriteString("}\n\n")
	ec.localVars = savedLocals
}

func emitMain(b *strings.Builder, cfg Config, info *analysisResult) {
	b.WriteString("func main() {\n")
	b.WriteString("\ta := app.New()\n")
	fmt.Fprintf(b, "\tw := a.NewWindow(%q)\n", cfg.AppName)
	b.WriteString("\tm := New()\n")
	b.WriteString("\tw.SetContent(m.BuildUI())\n")
	if len(info.timers) > 0 {
		b.WriteString("\tm.StartTimers()\n")
	}
	b.WriteString("\tw.Resize(fyne.NewSize(480, 640))\n")
	b.WriteString("\tw.ShowAndRun()\n")
	if len(info.timers) > 0 {
		b.WriteString("\tm.StopTimers()\n")
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}

// --- Dependency tracking ---

// extractDeps walks an AST node and returns all model field references.
func extractDeps(e ast.Node, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkDeps(e, modelFields, deps)
	return deps
}

// walkDeps is the recursive walker for dependency extraction.
func walkDeps(e ast.Node, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ast.SelectExpr:
		root := findRootIdent(n.Operand)
		if root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ast.BinaryExpr:
		walkDeps(n.Left, modelFields, deps)
		walkDeps(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkDeps(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkDeps(n.Cond, modelFields, deps)
		walkDeps(n.Then, modelFields, deps)
		walkDeps(n.Else, modelFields, deps)
	case *ast.CallExpr:
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.MethodExpr:
		walkDeps(n.Receiver, modelFields, deps)
		for _, arg := range n.Args {
			walkDeps(arg, modelFields, deps)
		}
	case *ast.IndexExpr:
		walkDeps(n.Operand, modelFields, deps)
		walkDeps(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkDeps(el, modelFields, deps)
		}
	case *ast.StructExpr:
		for _, field := range n.Fields {
			walkDeps(field.Value, modelFields, deps)
		}
	case *ast.InterpolationExpr:
		for _, part := range n.Parts {
			walkDeps(part, modelFields, deps)
		}
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			walkDeps(stmt, modelFields, deps)
		}
	case *ast.AssignStmt:
		walkDeps(n.Target, modelFields, deps)
		walkDeps(n.Value, modelFields, deps)
	case *ast.ToggleStmt:
		walkDeps(n.Target, modelFields, deps)
	}
}

func findRootIdent(e ast.Node) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findRootIdent(n.Operand)
	case *ast.IndexExpr:
		return findRootIdent(n.Operand)
	case *ast.MethodExpr:
		return findRootIdent(n.Receiver)
	}
	return ""
}

// extractMutatedFields returns set of field names mutated by a statement.
func extractMutatedFields(e ast.Node) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	switch n := e.(type) {
	case *ast.StmtBlock:
		for _, stmt := range n.Stmts {
			maps.Copy(fields, extractMutatedFields(stmt))
		}
	case *ast.AssignStmt:
		root := findMutationRoot(n.Target)
		if root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		root := findMutationRoot(n.Target)
		if root != "" {
			fields[root] = true
		}
	case *ast.CallStmt:
		maps.Copy(fields, extractMutatedFields(n.Call))
	case *ast.CallExpr:
		if len(n.Args) >= 1 {
			root := findMutationRoot(n.Args[0])
			if root != "" {
				fields[root] = true
			}
		}
	case *ast.MethodExpr:
		root := findMutationRoot(n.Receiver)
		if root != "" {
			fields[root] = true
		}
	}
	return fields
}

func findMutationRoot(e ast.Node) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findMutationRoot(n.Operand)
	case *ast.IndexExpr:
		return findMutationRoot(n.Operand)
	}
	return ""
}

// exprDeps extracts deps from an ast.Expr, expanding through computeds.
func (info *analysisResult) exprDeps(expr ast.Expr) map[string]bool {
	if expr.SNGL != nil {
		deps := extractDeps(expr.SNGL, info.modelFields)
		return info.expandDeps(deps)
	}
	return nil
}

// --- Helper functions ---

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

// astUsesAlert returns true if the document contains any Alert.toast/info/warn/error calls.
func astUsesAlert(doc *ast.Document) bool {
	if doc.App != nil {
		if slices.ContainsFunc(doc.App.Children, nodeUsesAlert) {
			return true
		}
	}
	for _, t := range doc.Timers {
		if exprNodeUsesAlert(t.Body) {
			return true
		}
	}
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

func needsTimeType(hint string) bool {
	switch hint {
	case "date", "time", "dateTime", "duration":
		return true
	}
	return false
}

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
		switch n.Method {
		case "upper", "lower", "trim", "replace", "substring":
			return "string"
		case "length", "indexOf":
			return "int"
		}
	}
	return "any"
}

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
