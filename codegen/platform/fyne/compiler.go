package fyne

import (
	"fmt"
	"go/format"
	"maps"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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

// DepFields implements codegen.Dependent.
func (u widgetUpdater) DepFields() map[string]bool { return u.deps }

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
	goImports      map[string]bool // native Go import paths from Resolved fields
	needsTime      bool
	needsURL       bool
	needsCanvas    bool
	needsToast     bool
	dt             *codegen.DepTracker // shared dependency tracker
}

// depTracker returns the shared DepTracker, creating it if needed.
func (info *analysisResult) depTracker() *codegen.DepTracker {
	if info.dt == nil {
		info.dt = codegen.NewDepTracker(info.modelFields, info.computedFields, info.computedDeps)
	}
	return info.dt
}

func analyze(doc *ast.Document) *analysisResult {
	info := &analysisResult{
		modelFields:    make(map[string]bool),
		computedFields: make(map[string]bool),
		computedDeps:   make(map[string]map[string]bool),
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
			// Build computed dependency map
			if fn.Body.SNGL != nil {
				info.computedDeps[fn.Name] = codegen.ExtractDeps(fn.Body.SNGL, info.modelFields)
			}
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

	// --- Phase 2: Pre-render dynamic parts needed by template ---

	// Pre-render computed bodies
	var computedDatas []computedData
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
		computedDatas = append(computedDatas, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	// Pre-render user-defined functions
	var funcBuf strings.Builder
	for _, fn := range doc.Functions {
		if fn.IsStdlib {
			continue
		}
		emitGoFunc(&funcBuf, fn, ec)
	}

	// Pre-render timer bodies
	var timerDatas []timerData
	for _, t := range info.timers {
		var bodyBuf strings.Builder
		stmts := ec.translateMutation(t.body)
		for _, s := range stmts {
			fmt.Fprintf(&bodyBuf, "\t\t\t\t\t%s\n", s)
		}
		var updBuf strings.Builder
		mutated := codegen.MutatedFields(t.body)
		affected := codegen.FindAffected(info.depTracker(), updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				fmt.Fprintf(&updBuf, "\t\t\t\t\tm.%s()\n", u.name)
			}
		} else {
			updBuf.WriteString("\t\t\t\t\tm.doRefresh()\n")
		}
		timerDatas = append(timerDatas, timerData{
			Index:            t.index,
			IntervalMs:       t.intervalMs,
			ActiveVar:        t.activeVar,
			Body:             bodyBuf.String(),
			AffectedUpdaters: updBuf.String(),
		})
	}

	// --- Phase 3: Build templateData and render template ---
	td := newTemplateData(info, cfg, updaters, widgetFields, funcBuf.String())
	td.Computeds = computedDatas
	td.Timers = timerDatas

	tmplFiles := codegen.RenderTemplates(templateFS, "templates", td)

	var b strings.Builder
	if len(tmplFiles) > 0 {
		tmplFiles[0].WriteTo(&b)
	}

	// --- Phase 4: Append dynamic code (BuildUI, updaters, components, main) ---

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
	hasSlot := comp.ChildrenType != ""
	if hasSlot {
		params = append(params, "slotContent fyne.CanvasObject")
	}

	fmt.Fprintf(b, "func (m *Model) %s(%s) fyne.CanvasObject {\n", methodName, strings.Join(params, ", "))

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
		ec:         ec,
		buf:        &strings.Builder{},
		indent:     1,
		components: allComponents,
		slotVar:    slotVar,
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
	b.WriteString("\tw.Resize(fyne.NewSize(480, 640))\n")
	b.WriteString("\tw.SetContent(m.BuildUI())\n")
	if len(info.timers) > 0 {
		b.WriteString("\tm.StartTimers()\n")
	}
	b.WriteString("\tw.ShowAndRun()\n")
	if len(info.timers) > 0 {
		b.WriteString("\tm.StopTimers()\n")
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}

// --- Dependency tracking ---

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
	case *ast.ParenExpr:
		return snglNodeGoType(n.Inner)
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
