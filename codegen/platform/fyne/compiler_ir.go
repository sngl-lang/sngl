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
		if strings.HasPrefix(imp.Native.ImportPath, "c://") {
			continue // C imports handled via CgoPreamble, not regular Go imports
		}
		info.goImports[imp.Native.ImportPath] = true
	}

	// If any i18n stdlib calls are present, the generated code will call
	// i18n.GetTranslator() and must import the SNGL i18n runtime package.
	if golang.PackageUsesI18n(pkg) {
		info.goImports[golang.SnglI18nImportPath] = true
	}

	// Build GoIRContexts: one rooted at the main component (or package
	// scope when no main exists) for package-level vars, plus per-component
	// contexts for vars declared inside non-main components — so init
	// expressions that reference sibling vars (e.g. `greeting = $"Hello,
	// {nm}!"` reading `nm`) resolve through Component.Vars and emit `m.nm`
	// rather than a bare `nm` identifier.
	baseCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		baseCtx = baseCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(baseCtx)

	// Collect vars from package + every component. The render methods for
	// non-main components already reference state as `m.<var>` (see
	// renderIRComponentMethod's compGC.ForComponent), so those fields must
	// be declared on Model. Limiting to main left non-main components
	// referencing undeclared fields, breaking compile.
	type taggedVar struct {
		v    *ir.Var
		comp *ir.Component // nil for package-level
	}
	var allVars []taggedVar
	for _, v := range pkg.Vars {
		allVars = append(allVars, taggedVar{v: v})
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			allVars = append(allVars, taggedVar{v: v, comp: comp})
		}
	}
	for _, tv := range allVars {
		v := tv.v
		if v.IsConst {
			continue
		}
		// Skip synthesized __slot<N> Vars (passReactivity scratch state for
		// structural reactivity). fyne doesn't yet consume them — Plan B
		// will. Until then, ignore them so existing fyne builds stay green.
		if strings.HasPrefix(v.Name, "__slot") {
			continue
		}
		varGC := gc
		if tv.comp != nil {
			varGC = golang.NewIRContext(ctx.ExprCtx.ForComponent(tv.comp))
		}
		goType := irVarGoType(v)
		initVal := irVarInit(v, varGC)
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

func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config, lang codegen.LangTranslator) ([]byte, error) {
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

	// windowCodes holds pre-rendered per-window build methods for multi-window.
	var windowCodes []string
	var mainVC *irViewContext

	if len(wins) <= 1 {
		// Single-window: render wins[0] body into BuildUI buffer directly.
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
			mainVC = vc

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
	} else {
		// Multi-window: add navigation fields and pre-render each window into
		// its own buildWindowX() method. BuildUI and navigate() are emitted
		// later by emitIRMultiWindowCode.
		widgetFields = append(widgetFields, irWidgetField{"activeWindow", "string"})
		for _, w := range wins {
			widgetFields = append(widgetFields, irWidgetField{windowBoxField(w.Name), "*fyne.Container"})
		}

		for _, w := range wins {
			buildFn := windowBuildFunc(w.Name)
			var winBuf strings.Builder
			winVC := &irViewContext{
				gc:             gc,
				ctx:            ctx,
				buf:            &winBuf,
				indent:         1,
				info:           info,
				windowNames:    windowNames,
				labelCount:     endLabel,
				containerCount: endContainer,
			}
			var winCode strings.Builder
			fmt.Fprintf(&winCode, "func (m *Model) %s() fyne.CanvasObject {\n", buildFn)
			if len(w.Body) == 1 {
				winVC.line("var content fyne.CanvasObject")
				winVC.renderStmt(w.Body[0], "content")
				winVC.line("if content == nil { content = widget.NewLabel(\"\") }")
				winCode.WriteString(winBuf.String())
				winCode.WriteString("\treturn content\n")
			} else if len(w.Body) > 1 {
				winVC.line("var parts []fyne.CanvasObject")
				for i, child := range w.Body {
					childVar := fmt.Sprintf("part%d", i)
					winVC.line("var %s fyne.CanvasObject", childVar)
					winVC.renderStmt(child, childVar)
					winVC.line("if %s != nil { parts = append(parts, %s) }", childVar, childVar)
				}
				winCode.WriteString(winBuf.String())
				winCode.WriteString("\treturn container.NewVBox(parts...)\n")
			} else {
				winCode.WriteString("\treturn widget.NewLabel(\"\")\n")
			}
			winCode.WriteString("}\n\n")
			windowCodes = append(windowCodes, winCode.String())
			widgetFields = append(widgetFields, winVC.widgetFields...)
			updaters = append(updaters, winVC.updaters...)
			if blueprintImports == nil {
				blueprintImports = winVC.imports
			} else if winVC.imports != nil {
				maps.Copy(blueprintImports, winVC.imports)
			}
			endLabel = winVC.labelCount
			endContainer = winVC.containerCount
		}
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
		// Skip synthesized __renderSlot<N> Funcs emitted by passReactivity
		// for reactive if/for structural updates. fyne does not yet consume
		// these — Plan B will add proper translation. Until then, ignore
		// them so existing fyne tests stay green.
		if strings.HasPrefix(fn.Name, "__renderSlot") {
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
	td, err := newIRTemplateData(info, cfg, updaters, widgetFields, entrySync, blueprintImports, funcBuf.String(), gc, ctx, lang)
	if err != nil {
		return nil, err
	}
	td.Computeds = computedDatas
	td.Timers = timerDatas

	tmplFiles := codegen.RenderTemplates(templateFS, "templates", td)

	var b strings.Builder
	if len(tmplFiles) > 0 {
		tmplFiles[0].WriteTo(&b)
	}

	// --- Phase 4: Append dynamic code ---
	if len(wins) <= 1 {
		emitIRBuildUI(&b, info, &buildBuf, singleRoot)
	} else {
		emitIRMultiWindowCode(&b, wins, windowCodes)
	}
	emitIRUpdaters(&b, updaters)

	for _, code := range componentCodes {
		b.WriteString(code)
	}

	if cfg.Main {
		emitIRMain(&b, cfg, info)
	}

	// Resolve /*SNGLREACT:i*/ placeholders emitted by recordLateReactive
	// once every visual node's bindings have been recorded.
	src := b.String()
	if mainVC != nil {
		src = mainVC.resolveReactiveTokens(src)
	}
	return []byte(src), nil
}

func newIRTemplateData(info *irAnalysis, cfg Config, updaters []irWidgetUpdater, widgetFields []irWidgetField, entrySync []entrySyncRec, blueprintImports map[string]bool, functionCode string, gc *golang.GoIRContext, ctx *codegen.CodegenCtx, lang codegen.LangTranslator) (templateData, error) {
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

	// Units (excluding the special-cased `duration` which maps to
	// time.Duration). Single-base units become `type X float64`,
	// multi-base units become `type X struct { Base1, Base2 float64 }`.
	td.UnitDecls = golang.EmitUnitTypeDecls(info.Units)

	// Lang-tracked helpers + their imports.
	helpers := golang.HelpersNeeded(ctx.Pkg)
	for _, imp := range helpers.Imports() {
		td.Imports[imp] = true
	}
	td.LangHelpers = helpers.Emit()

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

	// Detect C native imports and emit the cgo preamble.
	var cNativeImports []*ir.NativeImport
	for _, imp := range ctx.Pkg.Imports {
		if imp.Native == nil {
			continue
		}
		for _, fn := range imp.Native.Funcs {
			if fn.NativePkg == "C" {
				cNativeImports = append(cNativeImports, imp.Native)
				break
			}
		}
	}
	if len(cNativeImports) > 0 {
		if cc, ok := lang.(codegen.CCompiler); ok {
			td.CgoPreamble = cc.EmitCHeader(cNativeImports)
		} else {
			return templateData{}, fmt.Errorf("platform fyne with lang %T does not support C imports", lang)
		}
	}

	return td, nil
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

	// Emit user funcs as lowercase methods on Model so they line up with
	// computed-method naming (template uses raw {{.Name}}). Tests then
	// uniformly invoke `c.<name>(...)` against the Model in the same Go
	// package without needing case translation.
	goName := fn.Name
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
	// fyne 2.7 lays out SetContent's tree against the *current* window
	// size, so set content before resizing — otherwise the canvas
	// reports its default 0×0 dimensions at first paint and the user
	// sees a blank window until they manually resize.
	b.WriteString("\tw.SetContent(m.BuildUI())\n")
	b.WriteString("\tw.Resize(fyne.NewSize(480, 640))\n")
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

func irVarInit(v *ir.Var, gc *golang.GoIRContext) string {
	return golang.LowerVarInit(v, gc)
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

// --- Multi-window helpers ---

// windowPascal converts a window Name (e.g. "app", "/foo", "window_L42") to
// a PascalCase identifier fragment for function/field names.
func windowPascal(name string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, name)
	parts := strings.FieldsFunc(clean, func(r rune) bool { return r == '_' })
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return b.String()
}

func windowBuildFunc(name string) string { return "buildWindow" + windowPascal(name) }
func windowBoxField(name string) string  { return "window" + windowPascal(name) + "Box" }

// emitIRMultiWindowCode emits BuildUI, navigate, and per-window build methods
// for a multi-window application. Called only when len(wins) > 1.
// widgetFields already includes the windowXxxBox fields added by emitIR.
func emitIRMultiWindowCode(b *strings.Builder, wins []*codegen.WindowCtx, windowCodes []string) {
	b.WriteString("// BuildUI creates the widget tree. Call once.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")
	b.WriteString("\tif m.activeWindow == \"\" {\n")
	fmt.Fprintf(b, "\t\tm.activeWindow = %q\n", wins[0].Name)
	b.WriteString("\t}\n")
	for _, w := range wins {
		fmt.Fprintf(b, "\tm.%s = container.NewStack(m.%s())\n", windowBoxField(w.Name), windowBuildFunc(w.Name))
	}
	for _, w := range wins {
		fmt.Fprintf(b, "\tif m.activeWindow != %q { m.%s.Hide() }\n", w.Name, windowBoxField(w.Name))
	}
	b.WriteString("\tvar windowObjects []fyne.CanvasObject\n")
	for _, w := range wins {
		fmt.Fprintf(b, "\twindowObjects = append(windowObjects, m.%s)\n", windowBoxField(w.Name))
	}
	b.WriteString("\treturn container.NewStack(windowObjects...)\n")
	b.WriteString("}\n\n")

	b.WriteString("func (m *Model) navigate(name string) {\n")
	b.WriteString("\tm.activeWindow = name\n")
	for _, w := range wins {
		fmt.Fprintf(b, "\tm.%s.Hide()\n", windowBoxField(w.Name))
	}
	b.WriteString("\tswitch name {\n")
	for _, w := range wins {
		fmt.Fprintf(b, "\tcase %q:\n\t\tm.%s.Show()\n", w.Name, windowBoxField(w.Name))
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	for _, code := range windowCodes {
		b.WriteString(code)
	}
}
