package gtk4

import (
	"context"
	"fmt"
	"go/format"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irAnalysis holds the gtk4-specific analyzed state.
type irAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irBind
	computeds []irComputed
	goImports map[string]bool
	dt        *codegen.DepTracker
}

type irBind struct {
	name        string
	goType      string
	init        string
	noAccessors bool // skip getter/setter generation (synthesized __slot/__root)
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

// compilation holds per-request build state flowing between
// BuildMutationModel and EmitFromMutation.
type compilation struct {
	gen      *Generator
	ctx      *codegen.CodegenCtx
	info     *irAnalysis
	cfg      Config
	registry *gir.TypeRegistry
}

func (c *compilation) BuildMutationModel(req *codegen.Request, _ *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("gtk4: unsupported lang %q", req.Lang.LanguageIdentifier())
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("gtk4: %w", err)
	}
	c.cfg = c.cfg.withDefaults()

	// Load GIR registry. An explicit --opt gir= always overrides the
	// generator's cached (autodetected) registry so tests with inline GIR
	// fixtures work deterministically regardless of the host system.
	if c.cfg.GIRPath != "" {
		reg, perr := gir.ParseGIR(c.cfg.GIRPath)
		if perr == nil {
			c.registry = reg
			if c.gen != nil {
				c.gen.registry = reg
			}
		}
	} else if c.gen != nil && c.gen.registry != nil {
		c.registry = c.gen.registry
	} else {
		path, err := resolveGIRPath(c.cfg.GIRPath)
		if err == nil {
			reg, perr := gir.ParseGIR(path)
			if perr == nil {
				c.registry = reg
				if c.gen != nil {
					c.gen.registry = reg
				}
			}
		}
	}

	c.ctx = codegen.NewCodegenCtx(req, "gtk4")
	c.info = analyzeIR(c.ctx)

	stmts := mainBodyStmts(c.ctx)
	return c.ctx.BuildMutation(stmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request, sink codegen.Sink) error {
	modelSrc, callbacksSrc, err := c.emitIR()
	if err != nil {
		return err
	}

	modelFormatted, err := format.Source(modelSrc)
	if err != nil {
		return fmt.Errorf("gtk4 model.go formatting error: %w\n%s", err, modelSrc)
	}
	callbacksFormatted, err := format.Source(callbacksSrc)
	if err != nil {
		return fmt.Errorf("gtk4 callbacks.go formatting error: %w\n%s", err, callbacksSrc)
	}

	opts := codegen.WriterOptions{Source: req.Source, Maps: req.Maps}
	for _, pair := range []struct {
		name    string
		content []byte
	}{
		{"model.go", modelFormatted},
		{"callbacks.go", callbacksFormatted},
	} {
		w := codegen.OpenCodeFile(sink, pair.name, req.Lang, opts)
		if _, err := w.Write(pair.content); err != nil {
			w.Close()
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
	}
	return nil
}

// flattenPlatformFilters expands `platform <target> { ... }` blocks: when
// target matches the wanted platform, the body's statements are inlined;
// non-matching blocks are dropped. Non-filter statements pass through.
func flattenPlatformFilters(stmts []ir.Stmt, platform string) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		if pf, ok := s.(*ir.PlatformFilter); ok {
			if pf.Platform == platform {
				out = append(out, flattenPlatformFilters(pf.Body, platform)...)
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// mainBodyStmts returns the body statements to render: prefer the first
// window's body, otherwise fall back to the main component body.
func mainBodyStmts(ctx *codegen.CodegenCtx) []ir.Stmt {
	if wins := ctx.Windows(); len(wins) > 0 && len(wins[0].Body) > 0 {
		return wins[0].Body
	}
	if main := ctx.MainComponent(); main != nil {
		return main.Body
	}
	return nil
}

// analyzeIR collects gtk4-specific binds, computeds, and Go imports.
func analyzeIR(ctx *codegen.CodegenCtx) *irAnalysis {
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		goImports:      make(map[string]bool),
	}

	pkg := ctx.Pkg
	for _, imp := range pkg.Imports {
		if imp.Native == nil || imp.Native.ImportPath == "" {
			continue
		}
		if strings.HasPrefix(imp.Native.ImportPath, "c://") {
			continue
		}
		info.goImports[imp.Native.ImportPath] = true
	}
	if golang.PackageUsesI18n(pkg) {
		info.goImports[golang.SnglI18nImportPath] = true
	}
	// Alert.* calls lower to fmt.Fprintf(os.Stderr, ...) (see
	// gtk4IRAlertFunc) — pull in fmt + os when the package uses them.
	if info.NeedsToast {
		info.goImports["fmt"] = true
		info.goImports["os"] = true
	}

	// Build a GoIRContext so irVarInit can evaluate i18n.tr init calls.
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = gtk4IRAlertFunc

	// Collect vars from package + every component (mirrors fyne /
	// bubbletea). Non-main components with state would otherwise have
	// `m.<var>` references in render code with no declared Model field.
	type taggedVar struct {
		v    *ir.Var
		comp *ir.Component
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
		if v.Synthesized {
			if v.Name == "__root" {
				// Plan C's __root sentinel: stable *C.GtkBox that
				// BuildUI populates and returns. Initialized lazily
				// inside BuildUI (cgo calls aren't valid in struct init).
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      "*C.GtkBox",
					init:        "nil",
					noAccessors: true,
				})
				continue
			}
			// Plan A's __slot<N> vars hold widget refs for reactive
			// if/for teardown. Emit as []*C.GtkWidget so the renderSlot
			// loop (range, gtk_widget_unparent each, nil the slice)
			// compiles.
			info.binds = append(info.binds, irBind{
				name:        v.Name,
				goType:      "[]*C.GtkWidget",
				init:        "nil",
				noAccessors: true,
			})
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
	}

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

	return info
}

// emitIR generates the Go source for both model.go and callbacks.go.
func (c *compilation) emitIR() (modelSrc []byte, callbacksSrc []byte, err error) {
	exprCtx := c.ctx.ExprCtx
	if main := c.ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = gtk4IRAlertFunc

	// --- Phase 1: Render BuildUI body into a buffer ---
	// vc is still constructed so eventInvokers / nodeBindings / etc.
	// emitted by later phases keep their (currently empty) accumulators
	// — those phases run off WalkLowered output too in subsequent work,
	// but for now they need the receiver.
	var buildBuf strings.Builder
	vc := &viewContext{
		gc:         gc,
		registry:   c.registry,
		ctx:        c.ctx,
		buf:        &buildBuf,
		indent:     1,
		depTracker: c.info.depTracker(),
	}

	var widgetFields []widgetField
	bodyStmts := flattenPlatformFilters(mainBodyStmts(c.ctx), "gtk4")
	var topLevelRefs []string
	var topLevelCType map[string]string
	if len(bodyStmts) > 0 {
		tr := newGtk4Translator(gc, func(name, cType string) {
			widgetFields = append(widgetFields, widgetField{name: name, goType: "*C." + cType})
		}).withPkg(c.ctx.Pkg)
		tr.collectTagComponents(bodyStmts)
		body := codegen.WalkLowered(context.Background(), bodyStmts, tr)
		for _, stmt := range body {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(&buildBuf, "\t%s\n", line)
			}
		}
		topLevelRefs = tr.topLevel
		topLevelCType = tr.idCTypes
	}

	// --- Phase 2: User functions (non-computed, non-test, non-method) ---
	allFuncs := c.ctx.Pkg.Funcs
	if main := c.ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	var funcBuf strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		if fn.Synthesized {
			emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg)
			continue
		}
		if fn.LoweredFromTag != "" {
			emitIRPromotedHandler(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg)
			continue
		}
		emitGTK4Func(&funcBuf, fn, gc)
	}

	// Detect fmt usage in body/funcs (interpolation lowers to fmt.Sprint).
	if strings.Contains(buildBuf.String(), "fmt.") || strings.Contains(funcBuf.String(), "fmt.") {
		c.info.goImports["fmt"] = true
	}

	// If emitBuildUI will emit the synthetic __root wrapper, ensure
	// the field exists on the Model struct. The wrapper path triggers
	// when there is body content and the sole top-level isn't a window
	// class — see emitBuildUI for the matching conditions.
	if needsRootWrapper(&buildBuf, topLevelRefs, topLevelCType) {
		hasRoot := false
		for _, wf := range widgetFields {
			if wf.name == "__root" {
				hasRoot = true
				break
			}
		}
		if !hasRoot {
			widgetFields = append(widgetFields, widgetField{name: "__root", goType: "*C.GtkBox"})
		}
	}

	// --- Phase 3: Build template data ---
	td, err := c.newTemplateData(widgetFields, funcBuf.String(), gc)
	if err != nil {
		return nil, nil, err
	}

	// --- Phase 4: Render templates ---
	tmplFiles := codegen.RenderTemplates(templateFS, "templates", td)
	var modelBuf, callbacksBuf strings.Builder
	for _, f := range tmplFiles {
		switch f.Name {
		case "model.go":
			f.WriteTo(&modelBuf)
		case "callbacks.go":
			f.WriteTo(&callbacksBuf)
		}
	}

	// --- Phase 5: Append dynamic code to model.go ---
	emitBuildUI(&modelBuf, &buildBuf, topLevelRefs, topLevelCType, gc)
	emitEventInvokers(&modelBuf, vc.eventInvokers)
	// --- Phase 6: Append main() to callbacks.go (NOT model.go — cgo //export
	// directives can't coexist with the model.go preamble's static defs). ---
	if c.cfg.Main {
		emitGTK4Main(&callbacksBuf, c.cfg)
	}

	modelSrc = []byte(modelBuf.String())
	return modelSrc, []byte(callbacksBuf.String()), nil
}

func (c *compilation) newTemplateData(widgetFields []widgetField, functionCode string, gc *golang.GoIRContext) (templateData, error) {
	td := templateData{
		Package:      c.cfg.Package,
		Main:         c.cfg.Main,
		FunctionCode: functionCode,
		Imports:      map[string]bool{},
	}
	for p := range c.info.goImports {
		td.Imports[p] = true
	}

	// Lang-tracked helpers + their imports.
	helpers := golang.HelpersNeeded(c.ctx.Pkg)
	for _, imp := range helpers.Imports() {
		td.Imports[imp] = true
	}
	td.LangHelpers = helpers.Emit()

	// Units (excluding the special-cased `duration`).
	td.UnitDecls = golang.EmitUnitTypeDecls(c.info.Units)

	// Structs
	for _, sd := range c.info.Structs {
		s := structData{Name: golang.ExportName(sd.Name)}
		for _, f := range sd.Fields {
			s.Fields = append(s.Fields, structFieldData{
				Name: golang.ExportName(f.Name),
				Type: golang.IRTypeToGo(f.Type),
			})
		}
		td.Structs = append(td.Structs, s)
	}

	// Binds: emit getter/setter for each. Reactive widget updates are
	// already injected into handler bodies as ir.Assign by passReactivity
	// and lowered through WalkLowered — setters don't need extra dispatch.
	for _, bind := range c.info.binds {
		td.Binds = append(td.Binds, bindData{
			Name:        bind.name,
			GoType:      bind.goType,
			InitVal:     bind.init,
			Getter:      golang.ExportName(bind.name),
			NoAccessors: bind.noAccessors,
		})
	}

	// Computeds
	for _, comp := range c.info.computeds {
		body := ""
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = gc.EvalExpr(ret.Value)
			}
		}
		if body == "" {
			body = `""`
		}
		td.Computeds = append(td.Computeds, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	// Widget fields
	for _, wf := range widgetFields {
		td.WidgetFields = append(td.WidgetFields, widgetFieldData{
			Name:   wf.name,
			GoType: wf.goType,
		})
	}

	return td, nil
}

// emitIRSlotFunc emits a passReactivity-synthesized __renderSlot<N>
// Func as a Model method. The body is a mix of plain Go statements
// (for-teardown, Assign reset, If gate) and lower.* intrinsic calls.
// codegen.WalkLowered routes intrinsic shapes through gtk4Translator
// into ir.Stmt fragments; we then synthesize a new *ir.Func and feed
// it through gc.EmitFuncDef.
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package) {
	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: "*C." + cType})
	}).withPkg(pkg)
	tr.collectTagComponents(fn.Block)
	bodyStmts := codegen.WalkLowered(context.Background(), fn.Block, tr)

	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   []*ir.Param{{Name: "container", Type: ir.NativePointerOf("GtkBox")}},
		Return:   ir.TypVoid,
		Block:    bodyStmts,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// gtk4PromotedHandlerSig declares how a SNGL @event handler on a given
// tag/event lowers into Go for gtk4's signal-trampoline pattern. The
// SNGL handler may capture an event parameter (e.g. `@input(e)`) and
// reference fields like `e.value`. After lowering, the Go-side handler
// has no event param (the uniform GCallback trampoline carries no args);
// any `e.<field>` reference is replaced with a direct widget-getter call
// against the node the handler was attached to.
type gtk4PromotedHandlerSig struct {
	// EventVar is the SNGL event param name being replaced (e.g. "e").
	// Empty when the SNGL event has no value param (click, change-on-button).
	EventVar string
	// Field is the SNGL event field accessed (e.g. "value"). Empty when
	// no rewrite is needed.
	Field string
	// CType is the GTK widget C type the handler is attached to; informs
	// which getter to splice. Empty when no getter is needed.
	CType string
}

// gtk4HandlerSig returns the promoted-handler descriptor for
// (LoweredFromTag, LoweredFromEvent). Tags here include both the SNGL
// stdlib aliases (input, button, checkbox, switch) and the GIR class
// names that hello-world / GIR-driven components emit.
func gtk4HandlerSig(tag, event string) gtk4PromotedHandlerSig {
	switch tag {
	case "input", "entry", "GtkEntry":
		// "changed" is the GTK signal name (post-wrapper inline); "input"
		// and "change" are the SNGL stdlib event names.
		if event == "input" || event == "change" || event == "changed" {
			return gtk4PromotedHandlerSig{EventVar: "event", Field: "value", CType: "GtkEntry"}
		}
	case "checkbox", "switch", "GtkCheckButton", "GtkSwitch":
		// "toggled" is the GTK signal; "change" is the SNGL event name.
		if event == "change" || event == "toggled" {
			return gtk4PromotedHandlerSig{EventVar: "event", Field: "value", CType: "GtkCheckButton"}
		}
	}
	return gtk4PromotedHandlerSig{}
}

// gtk4EventGetterExpr returns the IR expression that reads `e.<field>`
// directly from the source widget of cType. Used to replace the
// SNGL `var = e.<field>` two-way-bind assignment when promoting a
// node-attached handler into a top-level Func: the trampoline calls
// the handler with no args, so any reference to `e` must be replaced
// with a direct widget getter.
func gtk4EventGetterExpr(cType, nodeID string) ir.Expr {
	widgetRef := &ir.Ident{Name: nodeID, IsElementRef: true, Synthesized: true}
	switch cType {
	case "GtkEntry":
		// GTK4: GtkEntry implements GtkEditable; text accessor moved
		// from gtk_entry_get_text (GTK3) to gtk_editable_get_text.
		cast := &ir.Conversion{Type: ir.NativePointerOf("GtkEditable"), Operand: widgetRef}
		getText := &ir.Call{
			Type:     ir.TypDyn,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("gtk_editable_get_text"),
			Args:     []ir.CallArg{{Value: cast}},
		}
		return &ir.Call{
			Type:     ir.TypString,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("GoString"),
			Args:     []ir.CallArg{{Value: getText}},
		}
	case "GtkCheckButton":
		cast := &ir.Conversion{Type: ir.NativePointerOf("GtkCheckButton"), Operand: widgetRef}
		getActive := &ir.Call{
			Type:     ir.TypBool,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("gtk_check_button_get_active"),
			Args:     []ir.CallArg{{Value: cast}},
		}
		return &ir.Conversion{Type: ir.TypBool, Operand: getActive}
	}
	return nil
}

// collectNodeCTypes walks every component / window / func body looking
// for `LocalVar __nX = lower.CreateNode("tag")` pairs and returns a
// node-id → GTK C type map. The lower pass emits these inside
// __renderSlotN bodies and component bodies; promoted node-attached
// handlers need the map to resolve element refs in reactivity splices
// to their setter even though those handlers live in separate Funcs.
func collectNodeCTypes(pkg *ir.Package) map[string]string {
	out := map[string]string{}
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if call, ok := n.Init.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "CreateNode" && len(call.Args) >= 1 {
					if lit, ok := call.Args[0].Value.(*ir.Literal); ok && lit.Type == ir.TypString {
						tag := lit.Raw
						// After passInlinePure (Plan G), every tag landing here
						// is a GIR-resolved native widget name (GtkButton, ...).
						if strings.HasPrefix(tag, "Gtk") {
							out[n.Name] = tag
						}
					}
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.PlatformFilter:
				walk(n.Body)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.NodeInst:
				walk(n.Children)
			case *ir.Window:
				walk(n.Body)
			case *ir.SlotInst, *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle:
				// No CreateNode call to harvest.
			case *ir.ContextProvider:
				panic(fmt.Sprintf("gtk4.collectNodeCTypes: ContextProvider should be lowered: %#v", n))
			default:
				panic(fmt.Sprintf("gtk4.collectNodeCTypes: unhandled ir.Stmt %T", n))
			}
		}
	}
	if pkg == nil {
		return out
	}
	for _, comp := range pkg.Components {
		walk(comp.Body)
		for _, fn := range comp.Funcs {
			if fn != nil {
				walk(fn.Block)
			}
		}
	}
	for _, w := range pkg.Windows {
		walk(w.Body)
		for _, fn := range w.Funcs {
			if fn != nil {
				walk(fn.Block)
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			walk(fn.Block)
		}
	}
	return out
}

// emitIRPromotedHandler emits a gtk4 node-attached event handler that
// the lower pass promoted to a top-level Func. The signal trampoline
// calls Go handlers with no args, so any SNGL `@input(e)` param is
// dropped; an `e.<field>` reference in the body's leading two-way-bind
// assignment is rewritten to a direct widget-getter call. Reactive
// splices that follow flow through codegen.WalkLowered into the gtk4
// translator so `__nN.value = expr` shapes get rewritten via
// OnPropAssign into `C.gtk_*_set_*(...)` calls.
func emitIRPromotedHandler(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package) {
	sig := gtk4HandlerSig(fn.LoweredFromTag, fn.LoweredFromEvent)

	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: "*C." + cType})
	}).withPkg(pkg)
	// Pre-populate idCTypes so OnPropAssign in reactivity splices finds
	// the C type for nodes created in sibling slot Funcs or the component
	// body — those CreateNode sites aren't in this handler's own Block.
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	tr.collectTagComponents(fn.Block)

	stmts := fn.Block
	var prelude []ir.Stmt

	// Strip the synthesized leading `var = e.<field>` two-way bind and
	// re-emit as `m.<var> = <gettercall>` since the trampoline exposes
	// no event param. Built as IR so the cgo cast goes through the
	// standard ir.Conversion → renderer path.
	if sig.EventVar != "" && sig.Field != "" && len(stmts) > 0 {
		if assign, ok := stmts[0].(*ir.Assign); ok {
			target, _ := assign.Target.(*ir.Ident)
			sel, _ := assign.Value.(*ir.Select)
			if target != nil && sel != nil {
				if op, _ := sel.Operand.(*ir.Ident); op != nil && op.Name == sig.EventVar && sel.Field == sig.Field {
					// nodeID = handler-name minus the "_<event>_handler" suffix.
					nodeID := strings.TrimSuffix(fn.Name, "_"+fn.LoweredFromEvent+"_handler")
					cType := tr.idCTypes[nodeID]
					if cType == "" {
						cType = sig.CType
					}
					getter := gtk4EventGetterExpr(cType, nodeID)
					if getter != nil {
						prelude = []ir.Stmt{&ir.Assign{
							Target: &ir.Ident{Name: target.Name},
							Op:     ast.AssignSet,
							Value:  getter,
						}}
						stmts = stmts[1:]
					}
				}
			}
		}
	}

	body := codegen.WalkLowered(context.Background(), stmts, tr)
	// Drop self-setter splices: writing the entry's text from inside
	// its own "changed" handler re-fires the signal and recurses.
	// nodeID = handler-name minus the "_<event>_handler" suffix.
	selfNode := strings.TrimSuffix(fn.Name, "_"+fn.LoweredFromEvent+"_handler")
	body = dropSelfSetterCalls(body, selfNode)
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   nil, // GTK trampoline calls handlers with no args.
		Return:   ir.TypVoid,
		Block:    append(prelude, body...),
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// dropSelfSetterCalls filters CallStmt's that invoke a GTK setter
// (first arg is a cgo cast wrapping m.<selfNode>) so that two-way
// bound widgets don't recurse into their own changed-signal handlers.
// Stmts other than self-targeting setter calls pass through unchanged.
func dropSelfSetterCalls(stmts []ir.Stmt, selfNode string) []ir.Stmt {
	if selfNode == "" {
		return stmts
	}
	out := stmts[:0:0]
	for _, s := range stmts {
		if cs, ok := s.(*ir.CallStmt); ok && cs != nil && isSetterOn(cs.Call, selfNode) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// isSetterOn reports whether a Call is a C.gtk_*_set_* invocation whose
// first argument resolves to m.<selfNode>.
func isSetterOn(call *ir.Call, selfNode string) bool {
	if call == nil || call.Func == nil || len(call.Args) == 0 {
		return false
	}
	if call.Func.NativePkg != "C" || !strings.Contains(call.Func.NativeName, "_set_") {
		return false
	}
	first := call.Args[0].Value
	// Unwrap one or more ir.Conversion layers (the cgo cast pattern).
	for {
		conv, ok := first.(*ir.Conversion)
		if !ok {
			break
		}
		first = conv.Operand
	}
	if sel, ok := first.(*ir.Select); ok {
		if op, ok := sel.Operand.(*ir.Ident); ok && op.Name == "m" && sel.Field == selfNode {
			return true
		}
	}
	return false
}

// emitGTK4Func emits a top-level user function as a method on *Model.
func emitGTK4Func(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	fnCopy := *fn
	if fnCopy.Receiver == "" {
		fnCopy.Receiver = "Model"
	}
	for _, line := range gc.EmitFuncDef(&fnCopy) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// needsRootWrapper mirrors the conditions inside emitBuildUI that
// trigger the synthetic m.__root *C.GtkBox wrapper emission. Kept in
// sync so the Model struct gets the matching field declared.
func needsRootWrapper(buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string) bool {
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		return false
	}
	if len(topLevelRefs) == 1 && isWindowClass(topLevelCType[topLevelRefs[0]]) {
		return false
	}
	return true
}

// isWindowClass reports whether cType is a top-level window widget
// that should not be wrapped in a synthetic m.__root.
func isWindowClass(cType string) bool {
	switch cType {
	case "GtkWindow", "GtkApplicationWindow", "GtkDialog":
		return true
	}
	return false
}

// emitBuildUI emits BuildUI(app *C.GtkApplication) *C.GtkWidget.
// With NoDeclarative on, the body buffer is a flat stream of intrinsic
// calls (CreateNode → m.<id> = ctor; AppendChild → gtk_box_append; etc.)
// translated by gtk4Translator. Top-level widget refs that weren't
// consumed by an AppendChild get parented into m.__root, which BuildUI
// initializes lazily and embeds in a GtkApplicationWindow.
//
// When the (single) top-level ref is itself a window-class widget,
// BuildUI returns it directly: the user explicitly placed a
// GtkApplicationWindow/GtkWindow at the root so there's no need for the
// synthetic m.__root wrapper or a freshly-constructed
// gtk_application_window_new.
func emitBuildUI(b *strings.Builder, buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string, gc *golang.GoIRContext) {
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		b.WriteString("\twin := C.gtk_application_window_new(app)\n")
		b.WriteString("\treturn win\n")
		b.WriteString("}\n\n")
		return
	}
	// Window-class passthrough: when the sole top-level is a window
	// widget (e.g. user wrote GtkApplicationWindow at the root), skip
	// the __root wrapper and return it directly.
	if len(topLevelRefs) == 1 && isWindowClass(topLevelCType[topLevelRefs[0]]) {
		ref := topLevelRefs[0]
		b.WriteString(buildBuf.String())
		retIdent := &ir.Ident{Name: ref, IsElementRef: true, Synthesized: true}
		retExpr := &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: retIdent}
		fmt.Fprintf(b, "\treturn %s\n", gc.EvalExpr(retExpr))
		b.WriteString("}\n\n")
		return
	}
	// Lazy-init __root — cgo calls aren't valid in field initializers,
	// so the binds-loop in New() puts nil there and BuildUI promotes it.
	rootRef := &ir.Ident{Name: "__root", IsElementRef: true, Synthesized: true}
	rootCtorCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_box_new"),
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}},
			{Value: &ir.Literal{Type: ir.TypInt, Raw: "6"}},
		},
	}
	rootInit := &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: rootCtorCall}
	b.WriteString("\tif m.__root == nil {\n")
	fmt.Fprintf(b, "\t\tm.__root = %s\n", gc.EvalExpr(rootInit))
	b.WriteString("\t}\n")
	b.WriteString(buildBuf.String())
	for _, ref := range topLevelRefs {
		childRef := &ir.Ident{Name: ref, IsElementRef: true, Synthesized: true}
		appendCall := &ir.Call{
			Type:     ir.TypVoid,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("gtk_box_append"),
			Args: []ir.CallArg{
				{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: rootRef}},
				{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: childRef}},
			},
		}
		for _, line := range gc.EvalStmt(&ir.CallStmt{Call: appendCall}) {
			fmt.Fprintf(b, "\t%s\n", line)
		}
	}
	b.WriteString("\twin := C.gtk_application_window_new(app)\n")
	b.WriteString("\tC.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), 480, 640)\n")
	winRef := &ir.Ident{Name: "win"}
	setChildCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_window_set_child"),
		Args: []ir.CallArg{
			{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWindow"), Operand: winRef}},
			{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: rootRef}},
		},
	}
	for _, line := range gc.EvalStmt(&ir.CallStmt{Call: setChildCall}) {
		fmt.Fprintf(b, "\t%s\n", line)
	}
	b.WriteString("\treturn win\n")
	b.WriteString("}\n\n")
}

// emitEventInvokers emits one Model method per (#id, @event) pair the
// visual walk connected via sngl_connect. Each method fires the GTK
// signal so the platform's test runner can drive
// `c.<id>.@<event>()` syntax through the real signal trampoline +
// Go-callback bridge — catching wiring bugs (e.g. callback arity
// mismatches in sngl_cb) that handler-rerun shims would miss.
func emitEventInvokers(b *strings.Builder, invokers []gtkEventInvoker) {
	seen := map[string]bool{}
	for _, inv := range invokers {
		methodName := inv.IDLabel + golang.ExportName(inv.SnglEvent)
		if seen[methodName] {
			continue // duplicate id+event — keep the first
		}
		seen[methodName] = true
		fmt.Fprintf(b, "// %s fires the %q signal on the #%s widget; for tests.\n",
			methodName, inv.GTKSignal, inv.IDLabel)
		if inv.ValueParam != "" {
			fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", methodName, inv.ValueParam)
		} else {
			fmt.Fprintf(b, "func (m *Model) %s() {\n", methodName)
		}
		if inv.PreFire != "" {
			fmt.Fprintf(b, "\t%s\n", inv.PreFire)
		}
		fmt.Fprintf(b, "\tsig := C.CString(%q)\n", inv.GTKSignal)
		b.WriteString("\tdefer C.free(unsafe.Pointer(sig))\n")
		fmt.Fprintf(b, "\tC.sngl_emit(C.gpointer(unsafe.Pointer(m.%s)), sig)\n", inv.FieldName)
		b.WriteString("}\n\n")
	}
}

// emitGTK4Main appends the GTK application bootstrap to callbacks.go.
// Goes in callbacks.go (not model.go) so the //export snglActivate directive
// can coexist with the file's preamble (which has only declarations).
func emitGTK4Main(b *strings.Builder, cfg Config) {
	b.WriteString("\n//export snglActivate\n")
	b.WriteString("func snglActivate(app *C.GtkApplication, _ C.gpointer) {\n")
	b.WriteString("\tm := New()\n")
	b.WriteString("\twin := m.BuildUI(app)\n")
	b.WriteString("\tC.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))\n")
	b.WriteString("}\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("\truntime.LockOSThread()\n")
	// G_APPLICATION_NON_UNIQUE skips the single-instance enforcement
	// so we don't need to register an app-id (which gtk_application_new
	// otherwise requires to be non-NULL and reverse-DNS-valid).
	b.WriteString("\tapp := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)\n")
	b.WriteString("\tC.g_signal_connect_data((C.gpointer)(unsafe.Pointer(app)),\n")
	b.WriteString("\t\tC.CString(\"activate\"),\n")
	b.WriteString("\t\tC.GCallback(C.snglActivate), nil, nil, 0)\n")
	b.WriteString("\tstatus := C.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil)\n")
	b.WriteString("\tif status != 0 {\n")
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"gtk: application exited with status %d\\n\", status)\n")
	b.WriteString("\t\tos.Exit(int(status))\n")
	b.WriteString("\t}\n")
	b.WriteString("\t_ = fmt.Sprint\n")
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

// gtk4IRAlertFunc lowers Alert.* calls on gtk4. The platform has no
// dedicated toast widget yet, so notifications print to stderr; this
// keeps Alert.toast / info / warn / error usage compilable on gtk4
// without requiring a Model.toasts field. Alert.confirm returns true
// (no blocking dialog wired up).
func gtk4IRAlertFunc(gc *golang.GoIRContext, method string, args []ir.CallArg) []string {
	if len(args) == 0 {
		return []string{"// unsupported Alert." + method + " (no args)"}
	}
	msg := gc.EvalExpr(args[0].Value)
	switch method {
	case "toast":
		variant := `"info"`
		if len(args) > 1 {
			variant = gc.EvalExpr(args[1].Value)
		}
		return []string{fmt.Sprintf(`fmt.Fprintf(os.Stderr, "[%%s] %%s\n", %s, %s)`, variant, msg)}
	case "info", "warn", "error":
		return []string{fmt.Sprintf(`fmt.Fprintf(os.Stderr, "[%s] %%s\n", %s)`, method, msg)}
	case "confirm":
		return []string{"true"}
	}
	return []string{"// unsupported Alert." + method}
}
