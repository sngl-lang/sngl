package fyne

import (
	"fmt"
	"go/format"
	"maps"
	"strings"

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
	*codegen.CommonAnalysis
	binds       []bindInfo
	externs     []externInfo
	computeds   []computedInfo
	entries     []entryInfo
	dataEvents  map[string][]ast.EventHandler
	goImports   map[string]bool // native Go import paths from Resolved fields
	needsTime   bool
	needsURL    bool
	needsCanvas bool
	dt          *codegen.DepTracker // shared dependency tracker
}

// depTracker returns the shared DepTracker, creating it if needed.
func (info *analysisResult) depTracker() *codegen.DepTracker {
	if info.dt == nil {
		info.dt = info.CommonAnalysis.DepTracker()
	}
	return info.dt
}

func analyze(doc *ast.Document) *analysisResult {
	common := codegen.AnalyzeCommon(doc)

	info := &analysisResult{
		CommonAnalysis: common,
		dataEvents:     make(map[string][]ast.EventHandler),
		goImports:      make(map[string]bool),
	}

	// Platform-specific data field analysis (types, init values)
	for _, d := range docVars(doc) {
		goType := inferGoType(d.Init)
		initVal := literalToGo(d.Init)
		typeHint := exprTypeHint(d.Type)
		if needsTimeType(typeHint) {
			info.needsTime = true
		}
		info.binds = append(info.binds, bindInfo{
			name:    d.Name,
			goType:  goType,
			initVal: initVal,
		})
		if len(d.Handlers) > 0 {
			info.dataEvents[d.Name] = d.Handlers
		}
	}

	// Platform-specific computed function analysis (Go types)
	for _, fn := range docFuncDefs(doc) {
		if isStdlibFunc(fn) {
			continue
		}
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			goType := snglNodeGoType(fn.Body)
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

	// Walk visual tree to find entries
	bodyStmts := docBodyStmts(doc)
	for _, stmt := range bodyStmts {
		if vn, ok := stmt.(*ast.VisualNode); ok {
			walkForEntries(vn, info)
		}
	}

	// Check if we need url or canvas imports
	walkStmtsForImports(bodyStmts, info)

	// Toast needs time
	if common.NeedsToast {
		info.needsTime = true
	}

	return info
}

func walkForEntries(vn *ast.VisualNode, info *analysisResult) {
	name := vnName(vn)
	props := vnProps(vn)
	events := vnEvents(vn)
	switch name {
	case "input":
		entry := entryInfo{
			fieldName: fmt.Sprintf("entry%d", len(info.entries)),
		}
		if v, ok := props["placeholder"]; ok {
			if s, ok := exprLiteralString(v); ok {
				entry.placeholder = s
			}
		}
		if v, ok := props["type"]; ok {
			if s, ok := exprLiteralString(v); ok && s == "password" {
				entry.password = true
			}
		}
		if inputEvt, ok := events["input"]; ok {
			entry.bindTarget = extractAssignTargetFromBlock(&inputEvt.Body)
		}
		info.entries = append(info.entries, entry)
	case "textarea":
		entry := entryInfo{
			fieldName: fmt.Sprintf("entry%d", len(info.entries)),
			multiLine: true,
		}
		if v, ok := props["placeholder"]; ok {
			if s, ok := exprLiteralString(v); ok {
				entry.placeholder = s
			}
		}
		if v, ok := props["rows"]; ok {
			if n, ok := exprLiteralInt(v); ok {
				entry.rows = n
			}
		}
		if inputEvt, ok := events["input"]; ok {
			entry.bindTarget = extractAssignTargetFromBlock(&inputEvt.Body)
		}
		info.entries = append(info.entries, entry)
	}

	for _, child := range vnChildNodes(vn) {
		walkForEntries(child, info)
	}
}

func walkStmtsForImports(stmts []ast.Stmt, info *analysisResult) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VisualNode:
			name := vnName(s)
			props := vnProps(s)
			switch name {
			case "link":
				info.needsURL = true
			case "image":
				if _, ok := props["src"]; ok {
					info.needsCanvas = true
				}
			}
			walkStmtsForImports(vnChildren(s), info)
		case *ast.IfStmt:
			walkStmtsForImports(s.Body.Stmts, info)
			walkStmtsForImports(s.Else.Stmts, info)
		case *ast.ForStmt:
			walkStmtsForImports(s.Body.Stmts, info)
			walkStmtsForImports(s.Else.Stmts, info)
		case *ast.PlatformStmt:
			walkStmtsForImports(s.Body.Stmts, info)
		}
	}
}

func emit(info *analysisResult, doc *ast.Document, cfg Config) []byte {
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
		AlertFunc:      fyneAlertFunc,
	}

	// --- Phase 1: Render BuildUI into a buffer, collecting widget fields + updaters ---
	var buildBuf strings.Builder
	var widgetFields []widgetField
	var updaters []widgetUpdater

	bodyStmts := docBodyStmts(doc)
	if len(bodyStmts) > 0 {
		vc := &viewContext{
			ec:         ec,
			buf:        &buildBuf,
			indent:     1,
			doc:        doc,
			components: info.Components,
			info:       info,
		}

		if len(bodyStmts) == 1 {
			vc.line("var content fyne.CanvasObject")
			vc.renderStmt(bodyStmts[0], "content")
			vc.line("if content == nil { content = widget.NewLabel(\"\") }")
		} else {
			vc.line("var parts []fyne.CanvasObject")
			for i, child := range bodyStmts {
				childVar := fmt.Sprintf("part%d", i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderStmt(child, childVar)
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
		for _, fn := range docFuncDefs(doc) {
			if fn.Name == comp.name && fn.Body != nil && len(fn.Params.Params) == 0 {
				body = ec.TranslateExpr(fn.Body)
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
	for _, fn := range docFuncDefs(doc) {
		if isStdlibFunc(fn) {
			continue
		}
		// Skip computed functions — already emitted via template
		if fn.Body != nil && len(fn.Params.Params) == 0 {
			continue
		}
		emitGoFunc(&funcBuf, fn, ec)
	}

	// Pre-render timer bodies
	var timerDatas []timerData
	for _, t := range info.Timers {
		var bodyBuf strings.Builder
		mutated := make(map[string]bool)
		for _, bodyStmt := range t.Body.Stmts {
			stmts := ec.TranslateMutation(bodyStmt)
			for _, s := range stmts {
				fmt.Fprintf(&bodyBuf, "\t\t\t\t\t%s\n", s)
			}
			for k, v := range codegen.MutatedFields(bodyStmt) {
				mutated[k] = v
			}
		}
		var updBuf strings.Builder
		affected := codegen.FindAffected(info.depTracker(), updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				fmt.Fprintf(&updBuf, "\t\t\t\t\tm.%s()\n", u.name)
			}
		} else {
			updBuf.WriteString("\t\t\t\t\tm.doRefresh()\n")
		}
		timerDatas = append(timerDatas, timerData{
			Index:            t.Index,
			IntervalMs:       t.IntervalMs,
			ActiveVar:        t.ActiveVar,
			Body:             bodyBuf.String(),
			AffectedUpdaters: updBuf.String(),
		})
	}

	// --- Phase 3: Build templateData and render template ---
	td := newTemplateData(info, cfg, updaters, widgetFields, funcBuf.String(), ec, doc)
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

	// User component render methods (not stdlib overrides)
	for _, comp := range docComponents(doc) {
		emitComponentMethod(&b, comp, info.Components, ec)
	}

	// main()
	if cfg.GenerateMain {
		emitMain(&b, cfg, info)
	}

	return []byte(b.String())
}

func emitGoFunc(b *strings.Builder, fn *ast.FuncDef, ec *exprContext) {
	params := make([]string, len(fn.Params.Params))
	for i, p := range fn.Params.Params {
		goType := typeHintToGo(exprTypeHint(p.Type))
		if goType == "" {
			goType = "any"
		}
		params[i] = p.Name + " " + goType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	retTypeHint := funcReturnType(fn)
	if retTypeHint != "" {
		retType = typeHintToGo(retTypeHint)
		if retType == "" {
			retType = "any"
		}
	}

	isTypeMethod := strings.Contains(fn.Name, ".")
	goName := exportName(fn.Name)
	if typeName, methodName, ok := ast.SplitMethodName(fn.Name); ok {
		goName = exportName(typeName) + exportName(methodName)
	}

	receiver := "m *Model"

	if fn.Body != nil {
		for _, p := range fn.Params.Params {
			ec.LocalVars[p.Name] = true
		}
		body := ec.TranslateExpr(fn.Body)
		for _, p := range fn.Params.Params {
			delete(ec.LocalVars, p.Name)
		}
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		fmt.Fprintf(b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	} else if fn.Block.IsDefined() {
		if isTypeMethod {
			fmt.Fprintf(b, "func %s(%s) %s {\n", goName, paramStr, retType)
		} else {
			fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		}
		for _, p := range fn.Params.Params {
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
					fmt.Fprintf(b, "\treturn\n")
				}
			default:
				stmts := ec.TranslateMutation(stmt)
				for _, line := range stmts {
					fmt.Fprintf(b, "\t%s\n", line)
				}
			}
		}
		for _, p := range fn.Params.Params {
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

func emitBuildUI(b *strings.Builder, info *analysisResult, doc *ast.Document, buildBuf *strings.Builder, updaters []widgetUpdater) {
	b.WriteString("// BuildUI creates the widget tree. Call once; widgets are updated selectively.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")

	bodyStmts := docBodyStmts(doc)
	if len(bodyStmts) == 0 {
		b.WriteString("\treturn widget.NewLabel(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	// Write the buffered build code
	b.WriteString(buildBuf.String())

	// Return the root widget
	if len(bodyStmts) == 1 {
		if info.NeedsToast {
			b.WriteString("\tm.toastLabel = widget.NewLabel(\"\")\n")
			b.WriteString("\tm.toastBox = container.NewVBox(m.toastLabel)\n")
			b.WriteString("\tm.toastBox.Hide()\n")
			b.WriteString("\treturn container.NewBorder(nil, m.toastBox, nil, nil, content)\n")
		} else {
			b.WriteString("\treturn content\n")
		}
	} else {
		if info.NeedsToast {
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

func emitComponentMethod(b *strings.Builder, comp *ast.ComponentDecl, allComponents []*ast.ComponentDecl, ec *exprContext) {
	methodName := "render" + exportName(comp.Name)

	var params []string
	for _, p := range compParams(comp) {
		goType := inferGoType(p.Default)
		params = append(params, p.Name+" "+goType)
	}
	hasSlot := compHasChildren(comp)
	if hasSlot {
		params = append(params, "slotContent fyne.CanvasObject")
	}

	fmt.Fprintf(b, "func (m *Model) %s(%s) fyne.CanvasObject {\n", methodName, strings.Join(params, ", "))

	savedLocals := make(map[string]bool)
	maps.Copy(savedLocals, ec.LocalVars)
	for _, p := range compParams(comp) {
		ec.LocalVars[p.Name] = true
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

	body := compBodyStmts(comp)
	if len(body) == 1 {
		vc.line("var result fyne.CanvasObject")
		vc.renderStmt(body[0], "result")
		vc.line("if result == nil { result = widget.NewLabel(\"\") }")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []fyne.CanvasObject")
		for i, child := range body {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderStmt(child, childVar)
			vc.line("if %s != nil { parts = append(parts, %s) }", childVar, childVar)
		}
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn container.NewVBox(parts...)\n")
	}

	b.WriteString("}\n\n")
	ec.LocalVars = savedLocals
}

func emitMain(b *strings.Builder, cfg Config, info *analysisResult) {
	b.WriteString("func main() {\n")
	b.WriteString("\ta := app.New()\n")
	fmt.Fprintf(b, "\tw := a.NewWindow(%q)\n", cfg.AppName)
	b.WriteString("\tm := New()\n")
	b.WriteString("\tw.Resize(fyne.NewSize(480, 640))\n")
	b.WriteString("\tw.SetContent(m.BuildUI())\n")
	if len(info.Timers) > 0 {
		b.WriteString("\tm.StartTimers()\n")
	}
	b.WriteString("\tw.ShowAndRun()\n")
	if len(info.Timers) > 0 {
		b.WriteString("\tm.StopTimers()\n")
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}

// --- Dependency tracking ---

// --- Helper functions ---

func extractAssignTargetFromBlock(block *ast.StmtBlock) string {
	if block == nil || len(block.Stmts) == 0 {
		return ""
	}
	return extractAssignTargetStmt(block.Stmts[0])
}

func extractAssignTargetStmt(s ast.Stmt) string {
	switch n := s.(type) {
	case *ast.AssignStmt:
		if ident, ok := n.Target.(*ast.IdentExpr); ok {
			return ident.Name
		}
	}
	return ""
}
