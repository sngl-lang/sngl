package fyne

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irAnalysis is the IR-based replacement for analysisResult.
type irAnalysis struct {
	*codegen.CommonAnalysis
	binds      []irBind
	externs    []irExtern
	computeds  []irComputed
	dataEvents map[string][]*ir.EventHandler
	goImports  map[string]bool
	dt         *codegen.DepTracker
}

type irBind struct {
	name   string
	goType string
	init   string
}

type irExtern struct {
	name   string
	goType string
}

type irComputed struct {
	name   string
	goType string
	fn     *ir.Func
}

func (info *irAnalysis) depTracker() *codegen.DepTracker {
	if info.dt == nil {
		info.dt = info.CommonAnalysis.DepTracker()
	}
	return info.dt
}

func analyzeIR(ctx *codegen.CodegenCtx) *irAnalysis {
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		dataEvents:     make(map[string][]*ir.EventHandler),
		goImports:      make(map[string]bool),
	}

	pkg := ctx.Pkg

	// Collect Go imports from native (go://) imports so the generated Go file
	// includes them as real package imports.
	for _, imp := range pkg.Imports {
		if imp.Native == nil || imp.Native.ImportPath == "" {
			continue
		}
		info.goImports[imp.Native.ImportPath] = true
	}

	// Collect vars from package + main component
	allVars := pkg.Vars
	if main := ctx.MainComponent(); main != nil {
		allVars = append(allVars, main.Vars...)
	}
	for _, v := range allVars {
		if v.IsConst {
			continue
		}
		goType := irVarGoType(v)
		initVal := irVarInit(v)
		if strings.HasPrefix(goType, "time.") {
			info.goImports["time"] = true
		}
		info.binds = append(info.binds, irBind{
			name:   v.Name,
			goType: goType,
			init:   initVal,
		})
		if len(v.Handlers) > 0 {
			info.dataEvents[v.Name] = v.Handlers
		}
	}

	// Computed functions
	allFuncs := pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	for _, f := range allFuncs {
		if codegen.IsComputed(f) {
			info.computeds = append(info.computeds, irComputed{
				name:   f.Name,
				goType: irFuncReturnType(f),
				fn:     f,
			})
		}
	}

	// Timer analysis
	for _, t := range info.Timers {
		if t.IntervalMs > 0 {
			info.goImports["time"] = true
		}
	}

	if info.NeedsToast {
		info.goImports["time"] = true
	}

	return info
}

func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config) []byte {
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = fyneIRAlertFunc

	// --- Phase 1: Render BuildUI into buffer, collecting widget fields + updaters ---
	var buildBuf strings.Builder
	var widgetFields []irWidgetField
	var updaters []irWidgetUpdater
	var entrySync []entrySyncRec
	var blueprintImports map[string]bool
	singleRoot := true
	var endLabel, endContainer int

	// Window name set for link-interception in multi-window apps.
	wins := ctx.Windows()
	windowNames := make(map[string]bool)
	for _, w := range wins {
		windowNames[w.Name] = true
	}

	if len(wins) > 0 && len(wins[0].Body) > 0 {
		bodyStmts := wins[0].Body
		vc := &irViewContext{
			gc:          gc,
			ctx:         ctx,
			buf:         &buildBuf,
			indent:      1,
			info:        info,
			windowNames: windowNames,
		}

		if len(bodyStmts) == 1 {
			vc.line("var content fyne.CanvasObject")
			vc.renderStmt(bodyStmts[0], "content")
			vc.line("if content == nil { content = widget.NewLabel(\"\") }")
		} else {
			singleRoot = false
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
		entrySync = vc.entrySync
		blueprintImports = vc.imports
		endLabel = vc.labelCount
		endContainer = vc.containerCount
	}

	// --- Phase 1b: Pre-render component methods with continued counters ---
	// Collect widget fields and updaters from sub-components BEFORE building
	// template data so the Model struct declares every field they reference.
	var componentCodes []string
	for _, cc := range ctx.NonMainComponents() {
		code, compFields, compUpdaters, nextLabel, nextContainer := renderIRComponentMethod(
			cc, ctx, gc, info, windowNames, endLabel, endContainer,
		)
		componentCodes = append(componentCodes, code)
		widgetFields = append(widgetFields, compFields...)
		updaters = append(updaters, compUpdaters...)
		endLabel = nextLabel
		endContainer = nextContainer
	}

	// --- Phase 2: Pre-render dynamic parts ---

	// Computed bodies
	var computedDatas []computedData
	for _, comp := range info.computeds {
		body := ""
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = gc.EvalExpr(ret.Value)
			}
		}
		if body == "" {
			body = `""`
		}
		computedDatas = append(computedDatas, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	// User functions
	allFuncs := ctx.Pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	var funcBuf strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		emitIRFyneFunc(&funcBuf, fn, gc)
	}

	// Timer bodies
	var timerDatas []timerData
	for _, t := range info.Timers {
		var bodyBuf strings.Builder
		mutated := make(map[string]bool)
		for _, stmt := range t.Body {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(&bodyBuf, "\t\t\t\t\t%s\n", line)
			}
			maps.Copy(mutated, codegen.MutatedFields(stmt))
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

	// --- Phase 3: Build template data and render ---
	td := newIRTemplateData(info, cfg, updaters, widgetFields, entrySync, blueprintImports, funcBuf.String(), gc, ctx)
	td.Computeds = computedDatas
	td.Timers = timerDatas

	tmplFiles := codegen.RenderTemplates(templateFS, "templates", td)

	var b strings.Builder
	if len(tmplFiles) > 0 {
		tmplFiles[0].WriteTo(&b)
	}

	// --- Phase 4: Append dynamic code ---
	emitIRBuildUI(&b, info, &buildBuf, singleRoot)
	emitIRUpdaters(&b, updaters)

	for _, code := range componentCodes {
		b.WriteString(code)
	}

	if cfg.Main {
		emitIRMain(&b, cfg, info)
	}

	return []byte(b.String())
}

func newIRTemplateData(info *irAnalysis, cfg Config, updaters []irWidgetUpdater, widgetFields []irWidgetField, entrySync []entrySyncRec, blueprintImports map[string]bool, functionCode string, gc *golang.GoIRContext, ctx *codegen.CodegenCtx) templateData {
	td := templateData{
		Package:      cfg.Package,
		Main:         cfg.Main,
		AppName:      cfg.AppName,
		NeedsToast:   info.NeedsToast,
		HasTimers:    len(info.Timers) > 0,
		FunctionCode: functionCode,
	}

	// Collect every import the generated code needs into one deduped set:
	// always-on imports, conditional Main additions, native go:// imports
	// from user code, and blueprint-declared imports collected by the
	// renderer. analyzeIR has already added "time" via goImports when any
	// time-typed var, timer, or toast is in scope.
	td.Imports = map[string]bool{
		"fmt":                       true,
		"fyne.io/fyne/v2":           true,
		"fyne.io/fyne/v2/widget":    true, // widget.NewLabel fallback
		"fyne.io/fyne/v2/container": true, // container.NewVBox multi-root + unknown fallback
	}
	if cfg.Main {
		td.Imports["os"] = true
		td.Imports["fyne.io/fyne/v2/app"] = true
	}
	for p := range info.goImports {
		td.Imports[p] = true
	}
	for p := range blueprintImports {
		td.Imports[p] = true
	}

	// Structs
	for _, sd := range info.Structs {
		s := structData{Name: golang.ExportName(sd.Name)}
		for _, f := range sd.Fields {
			s.Fields = append(s.Fields, structFieldData{
				Name: golang.ExportName(f.Name),
				Type: golang.IRTypeToGo(f.Type),
			})
		}
		td.Structs = append(td.Structs, s)
	}

	// Binds
	for _, bind := range info.binds {
		getter := golang.ExportName(bind.name)
		bd := bindData{
			Name:    bind.name,
			GoType:  bind.goType,
			InitVal: bind.init,
			Getter:  getter,
		}

		var extra strings.Builder
		// Entry sync — populated during render walk via vc.entrySync.
		for _, sync := range entrySync {
			if sync.varName == bind.name && bind.goType == "string" {
				fmt.Fprintf(&extra, "\tm.%s%s(v)\n", sync.fieldName, sync.target)
			}
		}
		// @change handlers
		if handlers, ok := info.dataEvents[bind.name]; ok {
			for _, h := range handlers {
				if h.Name == "change" && h.Func != nil {
					for _, stmt := range h.Func.Block {
						for _, line := range gc.EvalStmt(stmt) {
							fmt.Fprintf(&extra, "\t%s\n", line)
						}
					}
				}
			}
		}
		// Affected updaters
		mutated := map[string]bool{bind.name: true}
		affected := codegen.FindAffected(info.depTracker(), updaters, mutated)
		for _, u := range affected {
			fmt.Fprintf(&extra, "\tm.%s()\n", u.name)
		}
		bd.SetterExtra = extra.String()
		td.Binds = append(td.Binds, bd)
	}

	// Externs
	for _, ext := range info.externs {
		td.Externs = append(td.Externs, externData{
			Name:   golang.ExportName(ext.name),
			GoType: ext.goType,
		})
	}

	// Widget fields
	for _, wf := range widgetFields {
		td.WidgetFields = append(td.WidgetFields, widgetFieldData{
			Name:   wf.name,
			GoType: wf.goType,
		})
	}

	// Updater names
	for _, u := range updaters {
		td.UpdaterNames = append(td.UpdaterNames, u.name)
	}

	return td
}

func emitIRFyneFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		goType := golang.IRTypeToGo(p.Type)
		params[i] = p.Name + " " + goType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = golang.IRTypeToGo(fn.Return)
	}

	goName := golang.ExportName(fn.Name)
	receiver := "m *Model"

	localGC := gc
	for _, p := range fn.Params {
		localGC = localGC.WithLocal(p.Name)
	}

	if len(fn.Block) > 0 {
		fmt.Fprintf(b, "func (%s) %s(%s) %s {\n", receiver, goName, paramStr, retType)
		for _, stmt := range fn.Block {
			for _, line := range localGC.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t%s\n", line)
			}
		}
		b.WriteString("}\n\n")
	}
}

func emitIRBuildUI(b *strings.Builder, info *irAnalysis, buildBuf *strings.Builder, singleRoot bool) {
	b.WriteString("// BuildUI creates the widget tree. Call once; widgets are updated selectively.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")

	if buildBuf.Len() == 0 {
		b.WriteString("\treturn widget.NewLabel(\"\")\n")
		b.WriteString("}\n\n")
		return
	}

	b.WriteString(buildBuf.String())

	if singleRoot {
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

func emitIRUpdaters(b *strings.Builder, updaters []irWidgetUpdater) {
	for _, u := range updaters {
		fmt.Fprintf(b, "func (m *Model) %s() {\n", u.name)
		fmt.Fprintf(b, "\t%s\n", u.body)
		b.WriteString("}\n\n")
	}
}

// renderIRComponentMethod pre-renders one user-defined component to a string,
// returning the generated code, any widget fields it allocated, any updaters
// it registered, and the updated rolling counters. Counters continue from
// startLabel/startContainer so that m.fieldN names in component methods never
// collide with fields in the main BuildUI or earlier component methods.
func renderIRComponentMethod(
	cc *codegen.ComponentCtx,
	ctx *codegen.CodegenCtx,
	gc *golang.GoIRContext,
	info *irAnalysis,
	windowNames map[string]bool,
	startLabel, startContainer int,
) (code string, fields []irWidgetField, updaters []irWidgetUpdater, nextLabel, nextContainer int) {
	methodName := "render" + golang.ExportName(cc.Component.Name)

	var params []string
	for _, p := range cc.Props {
		goType := golang.IRTypeToGo(p.Type)
		params = append(params, p.Name+" "+goType)
	}
	hasSlot := cc.Component.ChildrenType != nil
	if hasSlot {
		params = append(params, "slotContent fyne.CanvasObject")
	}
	var slotVar string
	if hasSlot {
		slotVar = "slotContent"
	}

	compGC := gc.ForComponent(cc.Component)
	for _, p := range cc.Props {
		compGC = compGC.WithLocal(p.Name)
	}

	vc := &irViewContext{
		gc:             compGC,
		ctx:            ctx,
		buf:            &strings.Builder{},
		indent:         1,
		info:           info,
		slotVar:        slotVar,
		windowNames:    windowNames,
		labelCount:     startLabel,
		containerCount: startContainer,
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func (m *Model) %s(%s) fyne.CanvasObject {\n", methodName, strings.Join(params, ", "))

	if len(cc.Body) == 1 {
		vc.line("var result fyne.CanvasObject")
		vc.renderStmt(cc.Body[0], "result")
		vc.line("if result == nil { result = widget.NewLabel(\"\") }")
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn result\n")
	} else {
		vc.line("var parts []fyne.CanvasObject")
		for i, child := range cc.Body {
			childVar := fmt.Sprintf("part%d", i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderStmt(child, childVar)
			vc.line("if %s != nil { parts = append(parts, %s) }", childVar, childVar)
		}
		b.WriteString(vc.buf.String())
		b.WriteString("\treturn container.NewVBox(parts...)\n")
	}
	b.WriteString("}\n\n")

	return b.String(), vc.widgetFields, vc.updaters, vc.labelCount, vc.containerCount
}

func emitIRMain(b *strings.Builder, cfg Config, info *irAnalysis) {
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

// --- helpers ---

func irVarGoType(v *ir.Var) string {
	if v.Type != nil {
		return golang.IRTypeToGo(v.Type)
	}
	if v.Init != nil {
		if t := v.Init.ExprType(); t != nil {
			return golang.IRTypeToGo(t)
		}
	}
	return "any"
}

func irVarInit(v *ir.Var) string {
	if v.Init == nil {
		return golang.ZeroValueGo(golang.IRTypeToGo(v.Type))
	}

	varGoType := golang.IRTypeToGo(v.Type)
	if lit, ok := v.Init.(*ir.Literal); ok {
		switch varGoType {
		case "time.Duration":
			return fmt.Sprintf("mustParseDuration(%q)", lit.Raw)
		case "time.Time":
			if v.Type != nil {
				switch v.Type.Kind {
				case ir.TypeTime:
					return fmt.Sprintf("mustParseTime(%q)", lit.Raw)
				case ir.TypeDateTime:
					return fmt.Sprintf("mustParseDateTime(%q)", lit.Raw)
				}
			}
			return fmt.Sprintf("mustParseDate(%q)", lit.Raw)
		}
		litGoType := golang.IRTypeToGo(lit.Type)
		if litGoType == "time.Duration" {
			return fmt.Sprintf("mustParseDuration(%q)", lit.Raw)
		}
	}

	return golang.IRLiteralToGo(v.Init)
}

func irFuncReturnType(f *ir.Func) string {
	if f.Return != nil && f.Return.Kind != ir.TypeDyn {
		return golang.IRTypeToGo(f.Return)
	}
	if len(f.Block) == 1 {
		if ret, ok := f.Block[0].(*ir.Return); ok && ret.Value != nil {
			if t := ret.Value.ExprType(); t != nil {
				return golang.IRTypeToGo(t)
			}
		}
	}
	return ""
}

func extractIRAssignTarget(stmts []ir.Stmt) string {
	if len(stmts) == 0 {
		return ""
	}
	if assign, ok := stmts[0].(*ir.Assign); ok {
		if ident, ok := assign.Target.(*ir.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func fyneIRAlertFunc(gc *golang.GoIRContext, method string, args []ir.CallArg) []string {
	switch method {
	case "toast":
		msg := gc.EvalExpr(args[0].Value)
		variant := `"info"`
		if len(args) > 1 {
			variant = gc.EvalExpr(args[1].Value)
		}
		return []string{fmt.Sprintf("m.showToast(%s, %s)", msg, variant)}
	case "info", "warn", "error":
		msg := gc.EvalExpr(args[0].Value)
		return []string{fmt.Sprintf("m.showToast(%s, %q)", msg, method)}
	case "confirm":
		return []string{"// Alert.confirm not supported in Fyne"}
	}
	return []string{"// unsupported Alert." + method}
}
