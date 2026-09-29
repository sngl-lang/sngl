package gtk4

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

type irAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irBind
	computeds []irComputed
	gc        *golang.GoIRContext
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
	return info.dt
}

type compilation struct {
	gen      *Generator
	ctx      *codegen.CodegenCtx
	info     *irAnalysis
	cfg      Config
	registry *gir.TypeRegistry
	// shared is what the translators of one emitted file accumulate; it is
	// reset per emitIRMode attempt.
	shared  *emitShared
	wrapped bool // emitIRMode(true): target pkg/go/gtk4rt instead of inline cgo
	// disableWrapped forces the inline-cgo path: an external cgo harness
	// (agent-mode test build, Snapshot/BatchSnapshot) calls Model.BuildUI and
	// expects its `*C.GtkApplication`/`*C.GtkWidget` signature, which wrapped
	// mode replaces with gtk4rt.Handle.
	disableWrapped bool
}

func (c *compilation) BuildMutationModel(req *codegen.Request, _ *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("gtk4: unsupported lang %q", req.Lang.LanguageIdentifier())
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("gtk4: %w", err)
	}
	c.cfg = c.cfg.withDefaults()

	// The generator is the only thing that resolves a registry, so the choice
	// here matches the one made during type-check.
	reg, girErr := c.gen.useGIR(c.cfg.GIRPath)
	if girErr != nil {
		return nil, fmt.Errorf("gtk4: %w", girErr)
	}
	c.registry = reg

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
	var prune []string
	for _, name := range golang.PayloadPruneCandidates(c.ctx.Pkg, codegen.TriggerPayloads(c.ctx.Pkg)) {
		if !bytes.Contains(callbacksSrc, []byte(name)) {
			prune = append(prune, name)
		}
	}
	modelSrc = []byte(golang.PruneStructDecls(string(modelSrc), prune))

	// The templates already render `package main` and the import block, so
	// PackageName is left empty; the FileEmitter still owns the header, the
	// gofmt pass and source-map emission.
	for _, pair := range []struct {
		name    string
		content []byte
	}{
		{"model.go", modelSrc},
		{"callbacks.go", callbacksSrc},
	} {
		e := req.Lang.NewFileEmitter(sink, codegen.FileOptions{
			Name:     pair.name,
			Source:   req.Source,
			Platform: "gtk4",
			Maps:     req.Maps,
		})
		if _, err := e.Write(pair.content); err != nil {
			e.Close()
			return err
		}
		if err := e.Close(); err != nil {
			return err
		}
	}
	return nil
}

func mainBodyStmts(ctx *codegen.CodegenCtx) []ir.Stmt {
	if wins := ctx.Windows(); len(wins) > 0 && len(wins[0].Body) > 0 {
		// The package body is not emitted, so its first settles run once the
		// entry window's widgets exist.
		return append(slices.Clip(wins[0].Body), ctx.Pkg.RootMounts()...)
	}
	if main := ctx.RootDecl(); main != nil {
		return main.Body
	}
	return nil
}

// mainComponentLocalRefs is passNodeEscape's non-escaping widget-ref set for
// the scope mainBodyStmts emits: a harness-isolated root component's body, and
// nothing for a window's.
//
// Nil for the entry scope, and deliberately: locals are for a *recursive*
// render method, where a frame must not clobber the temp of the frame that
// called it. BuildUI has no frames, and everything emitted beside it may name
// a ref it created -- only some of those sites go through a qualifier that
// knows about locals.
func mainComponentLocalRefs(ctx *codegen.CodegenCtx) map[string]bool {
	if wins := ctx.Windows(); len(wins) > 0 && len(wins[0].Body) > 0 {
		return nil
	}
	if main := ctx.RootDecl(); main != nil {
		return main.LocalRefs
	}
	return nil
}

func analyzeIR(ctx *codegen.CodegenCtx) *irAnalysis {
	// Set before any body is translated, since it decides how a call to one of
	// these renders, and before ScopedExprCtx clones it. See
	// golang.ModelFreeFuncs -- a user type's method is emitted free, and one
	// calling a top-level func has no Model to reach a Model method through.
	ctx.ExprCtx.FreeFuncs = golang.ModelFreeFuncs(ctx.Pkg)
	ctx.ExprCtx.ModelParamFuncs = golang.ModelParamFuncs(ctx.Pkg)
	exprCtx := ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = gtk4IRAlertFunc
	// gtk4 builds a non-inlinable component as a record, so a CreateComponent
	// renders as a call to that record's ctor.
	gc.InstanceRecords = true
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		gc:             gc,
		dt:             ctx.Deps,
	}

	pkg := ctx.Pkg
	for _, path := range golang.BaseImports(pkg) {
		gc.RequireImport(path)
	}
	// Alert.* lowers to fmt.Fprintf(os.Stderr, ...); see gtk4IRAlertFunc.
	if info.NeedsToast {
		gc.RequireImport("fmt")
		gc.RequireImport("os")
	}

	// Inlining folded every component but a harness root into its caller, so
	// iterating every component's vars would re-add the originals and collide
	// their synthesized __root/__slot scratch fields.
	for _, tv := range ctx.ModelState() {
		// nil for a binding no declaration made: a window's route parameters,
		// which the slot population declares and the request fills. The
		// special cases below are all things a body or a pass declared, so
		// they are asked only where there is a declaration to ask.
		v := tv.Var()
		if v != nil && v.Synthesized {
			if v.Name == "__root" {
				// The __root sentinel is initialized lazily inside BuildUI:
				// cgo calls aren't valid in struct init.
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      "*C.GtkBox",
					init:        "nil",
					noAccessors: true,
				})
				continue
			}
			// NoContext's `__ctx_<name>` vars get a field of their declared
			// type, not the slot-var []*C.GtkWidget fallback below.
			if strings.HasPrefix(v.Name, "__ctx_") {
				ctxGC := gc
				if tv.Comp != nil {
					ctxGC = golang.NewIRContext(ctx.ExprCtx.ForComponent(tv.Comp))
				}
				ctxGoType := golang.VarGoType(v)
				if strings.HasPrefix(ctxGoType, "time.") {
					gc.RequireImport("time")
				}
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      ctxGoType,
					init:        irVarInit(v, ctxGC),
					noAccessors: true,
				})
				continue
			}
			// __slot<N> vars hold widget refs for reactive if/for teardown.
			//
			// Named rather than taken as the shape of every synthesized var:
			// an instance registry (`__instN_live`) is a synthesized list too,
			// and calling it a list of widgets typed the field as
			// []*C.GtkWidget while the render assigned []*CardInstance to it.
			if ir.IsSlotVarName(v.Name) {
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      "[]*C.GtkWidget",
					init:        "nil",
					noAccessors: true,
				}, irBind{
					name:        codegen.SlotAnchorField(v.Name),
					goType:      "*C.GtkWidget",
					init:        "nil",
					noAccessors: true,
				})
				continue
			}
		}
		varGC := gc
		if tv.Comp != nil {
			varGC = golang.NewIRContext(ctx.ExprCtx.ForComponent(tv.Comp))
		}
		goType := golang.BindGoType(tv.Type(), tv.Init())
		initVal := golang.LowerBindInit(tv.Type(), tv.Init(), varGC)
		if strings.HasPrefix(goType, "time.") {
			gc.RequireImport("time")
		}
		info.binds = append(info.binds, irBind{
			name:   tv.Name(),
			goType: goType,
			init:   initVal,
			// Consts skip getter/setter: the field name would collide with
			// the accessor (APP_NAME field + APP_NAME() method). So does any
			// name Go cannot export -- every `__`-prefixed one, which is
			// every name a lowering pass synthesized, and nothing outside the
			// program reads one.
			noAccessors: tv.IsConst() || golang.ExportName(tv.Name()) == tv.Name(),
		})
	}

	allFuncs := ctx.AllFuncs()
	for _, f := range allFuncs {
		if instanceOwnsFunc(ctx.Pkg, f) {
			continue
		}
		if codegen.IsComputed(f) {
			info.computeds = append(info.computeds, irComputed{
				name:   f.Name,
				goType: golang.FuncReturnGoType(f),
				fn:     f,
			})
		}
	}

	return info
}

// emitIR emits model.go + callbacks.go. It attempts wrapped mode (targeting
// pkg/go/gtk4rt, no cgo) and falls back to inline cgo if any cgo survives, so
// a program is never half-wrapped.
func (c *compilation) emitIR() (modelSrc []byte, callbacksSrc []byte, err error) {
	if !c.disableWrapped {
		if m, cb, werr := c.emitIRMode(true); werr == nil && !bytesUseCgo(m) && !bytesUseCgo(cb) {
			return m, cb, nil
		}
	}
	return c.emitIRMode(false)
}

// widgetFieldSink types a registered Model widget field: gtk4rt.Handle in
// wrapped mode, the per-widget cgo pointer type otherwise.
func (c *compilation) widgetFieldSink(fields *[]widgetField) func(name, cType string) {
	return func(name, cType string) {
		// Through widgetFieldGoType like the other three sinks: it is what
		// knows that a component instance's handle arrives already spelled as
		// Go and is not a widget in either mode. Deciding the type here
		// instead declared `m.__n0 gtk4rt.Handle` for a field the build
		// assigns a *TreeViewInstance to.
		*fields = append(*fields, widgetField{name: name, goType: widgetFieldGoType(cType, c.wrapped)})
	}
}

func (c *compilation) emitIRMode(wrapped bool) (modelSrc []byte, callbacksSrc []byte, err error) {
	c.wrapped = wrapped
	c.shared = &emitShared{markup: collectMarkup(c.ctx.Pkg)}
	exprCtx := c.ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = gtk4IRAlertFunc
	gc.InstanceRecords = true

	// vc carries the accumulators the phases after the walk read back: the
	// test invokers the main body's translator records as it connects signals.
	var buildBuf strings.Builder
	vc := &viewContext{
		gc:         gc,
		registry:   c.registry,
		ctx:        c.ctx,
		buf:        &buildBuf,
		indent:     1,
		depTracker: c.info.depTracker(),
	}

	// Shared into every translator so OnCreateNode builds the GtkDrawingArea +
	// cairo trampoline and OnDefault wires reactive redraws.
	c.shared.canvasByID, c.shared.canvasByNode = canvasutil.Collect(c.ctx.Canvases)
	hasCanvas := len(c.shared.canvasByID) > 0
	// The draw funcs are codegen's own and are in no func list, so they are
	// emitted from the drawings rather than fished out of the loop below --
	// which is what the `canvasByFunc[fn] != nil` arm there used to do.
	emitCanvasDrawFuncs := func(b *strings.Builder) {
		all := c.ctx.Canvases.All()
		for i := range all {
			// A drawing inside a component with a record of its own is that
			// record's; emitComponentInstance emits it with the instance
			// receiver.
			if isInstanceComponent(all[i].Owner) {
				continue
			}
			emitIRCanvasDraw(b, &all[i], gc, c.registry, c.shared)
		}
	}

	var widgetFields []widgetField
	// lower.passPlatformExtensionBody is always on, so every platform override
	// is already resolved to its body here.
	bodyStmts := mainBodyStmts(c.ctx)
	var topLevelRefs []string
	var topLevelCType map[string]string
	if len(bodyStmts) > 0 {
		tr := newGtk4Translator(gc, c.widgetFieldSink(&widgetFields)).
			withPkg(c.ctx.Pkg).withRegistry(c.registry).withShared(c.shared).
			withLocalRefs(mainComponentLocalRefs(c.ctx)).withWrapped(c.wrapped).
			withInvokerSink(func(inv gtkEventInvoker) {
				vc.eventInvokers = append(vc.eventInvokers, inv)
			})
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

	allFuncs := c.ctx.AllFuncs()
	componentFuncs := gtk4ComponentFuncs(c.ctx.Pkg)
	stateFuncs := golang.ModelStateFuncs(c.ctx.Pkg)
	var funcBuf strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || codegen.IsComputed(fn) {
			continue
		}
		// An instance component carries its own funcs as methods on its record;
		// emitComponentInstance writes those.
		if instanceOwnsFunc(c.ctx.Pkg, fn) {
			continue
		}
		// golang.LiftsToFreeFunc is the one answer the call site uses too.
		if fn.Receiver != "" && golang.LiftsToFreeFunc(c.ctx.Pkg, fn.Receiver) {
			emitGTK4TypeMethod(&funcBuf, fn, gc)
			continue
		}
		// stateFuncs is the set ModelFreeFuncs kept from the call sites; see
		// its doc for what a package var costs a free function.
		if fn.Receiver == "" && !componentFuncs[fn] && !stateFuncs[fn] && fn.LoweredFromTag == "" && !fn.Synthesized {
			emitGTK4FreeFunc(&funcBuf, fn, gc)
			continue
		}
		if fn.Synthesized {
			emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
			continue
		}
		if fn.LoweredFromTag != "" {
			emitIRPromotedHandler(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
			continue
		}
		emitGTK4Func(&funcBuf, fn, gc, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
	}
	emitCanvasDrawFuncs(&funcBuf)

	createTargets := collectCreateComponentTargets(c.ctx.Pkg)
	for _, cc := range c.ctx.NonRootComponents() {
		// A component the build renders as a live instance gets a record of
		// its own; its widget fields and its state stay off the Model, which
		// is the whole point. See emitComponentInstance.
		if isInstanceComponent(cc.Component) {
			emitComponentInstance(&funcBuf, cc, gc, c.ctx.Pkg, c.registry, c.shared, c.wrapped, c.ctx.Canvases.All())
			continue
		}
		if createTargets[cc.Component] {
			emitIRComponentMethod(&funcBuf, cc, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
		}
	}

	// The Model struct needs the __root field whenever emitBuildUI will emit
	// the synthetic wrapper; needsRootWrapper mirrors its conditions.
	if needsRootWrapper(&buildBuf, topLevelRefs, topLevelCType) {
		hasRoot := false
		for _, wf := range widgetFields {
			if wf.name == "__root" {
				hasRoot = true
				break
			}
		}
		if !hasRoot {
			rootType := "*C.GtkBox"
			if c.wrapped {
				rootType = gtk4rtHandleType
			}
			widgetFields = append(widgetFields, widgetField{name: "__root", goType: rootType})
		}
	}

	// The entry window's `#id` names the GtkWindow BuildUI creates, which is
	// what `open` and `close` reach it through.
	winID := entryWindowID(c.ctx)
	if winID != "" && c.wrapped {
		widgetFields = append(widgetFields, widgetField{name: winID, goType: gtk4rtHandleType})
	}

	// Pre-rendered so gc.RequireImport calls from EvalExpr land before
	// newTemplateData samples gc.Imports().
	var buildUIBuf strings.Builder
	emitBuildUI(&buildUIBuf, &buildBuf, topLevelRefs, topLevelCType, gc, c.wrapped, windowTitleGo(c.ctx, gc), widgetFieldNames(widgetFields), winID)
	// emitEventInvokers emits raw unsafe.Pointer strings; register the import
	// structurally rather than by scanning the output.
	if len(vc.eventInvokers) > 0 && !c.wrapped {
		gc.RequireImport("unsafe")
	}
	var eventInvokersBuf strings.Builder
	emitEventInvokers(&eventInvokersBuf, vc.eventInvokers, c.wrapped)

	// The drawing-area trampoline registration emits an `unsafe.Pointer` cast.
	if hasCanvas {
		gc.RequireImport("unsafe")
		// The emitted CanvasStyle struct decl references snglcolor.Color.
		gc.RequireImport(canvasutil.ColorImportPath)
	}

	td, err := c.newTemplateData(widgetFields, funcBuf.String(), gc)
	if err != nil {
		return nil, nil, err
	}
	if hasCanvas {
		td.LangHelpers += canvasStdlibDeclsExcluding(td.Structs)
		td.HasCanvas = true
	}
	if c.ctx != nil && c.ctx.Pkg != nil && c.ctx.Pkg.UsesErrorHandling {
		td.LangHelpers += golang.ErrorEventDecl
	}

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

	modelBuf.WriteString(buildUIBuf.String())
	modelBuf.WriteString(eventInvokersBuf.String())
	// main() goes in callbacks.go, not model.go: cgo //export directives can't
	// coexist with the model.go preamble's static defs.
	if c.cfg.Main {
		if err := emitGTK4Main(&callbacksBuf, c.cfg, c.wrapped, c.ctx.Pkg); err != nil {
			c.shared.errs = append(c.shared.errs, err)
		}
	}

	if len(c.shared.errs) > 0 {
		return nil, nil, errors.Join(c.shared.errs...)
	}
	modelSrc = []byte(modelBuf.String())
	return modelSrc, []byte(callbacksBuf.String()), nil
}

func (c *compilation) newTemplateData(widgetFields []widgetField, functionCode string, gc *golang.GoIRContext) (templateData, error) {
	td := templateData{
		Package:         c.cfg.Package,
		Main:            c.cfg.Main,
		FunctionCode:    functionCode,
		Imports:         map[string]bool{},
		NeedsBoolToInt:  c.shared.boolToInt,
		NeedsGObjectSet: c.shared.gObjectSet,
		NeedsSlotAnchor: c.shared.slotAnchor,
		Wrapped:         c.wrapped,
	}
	if c.wrapped {
		// Widget fields are gtk4rt.Handle; ensure the import is present even if
		// (unusually) no gtk4rt call was emitted into this file.
		td.Imports[gtk4rtPkg] = true
	}
	// Lang-tracked helpers + their imports.
	helpers := golang.HelpersNeeded(c.ctx.Pkg)
	for _, imp := range helpers.Imports() {
		td.Imports[imp] = true
	}
	td.LangHelpers = helpers.Emit() + golang.EmitMergeFuncs(c.ctx.Pkg.MergeStructs)

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

	// passReactivity already injected reactive widget updates into handler
	// bodies as ir.Assign, so setters need no extra dispatch.
	for _, bind := range c.info.binds {
		goType := bind.goType
		if c.wrapped {
			// analyzeIR runs before the wrapped/cgo choice, so its synthesized
			// widget binds carry cgo pointer types.
			switch {
			case goType == "[]*C.GtkWidget":
				goType = "[]" + gtk4rtHandleType
			case strings.HasPrefix(goType, "*C."):
				goType = gtk4rtHandleType
			}
		}
		td.Binds = append(td.Binds, bindData{
			Name:        bind.name,
			GoType:      goType,
			InitVal:     bind.init,
			Getter:      golang.ExportName(bind.name),
			NoAccessors: bind.noAccessors,
		})
	}

	// A block-bodied computed renders its whole statement list, so a non-string
	// return type does not get a bogus `return ""`.
	for _, comp := range c.info.computeds {
		var body string
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = "\treturn " + gc.EvalExpr(ret.Value)
			}
		}
		if body == "" && comp.fn != nil {
			var lines []string
			for _, stmt := range comp.fn.Block {
				for _, line := range gc.EvalStmt(stmt) {
					lines = append(lines, "\t"+line)
				}
			}
			body = strings.Join(lines, "\n")
		}
		if body == "" {
			body = "\treturn " + golang.ZeroValueGo(comp.goType)
		}
		td.Computeds = append(td.Computeds, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	// __root/__slot<N> are already binds, and the BuildUI walk can register the
	// same element ref twice; dedupe so the Model struct declares each once.
	seenField := make(map[string]bool, len(c.info.binds)+len(widgetFields))
	for _, bind := range c.info.binds {
		seenField[bind.name] = true
	}
	for _, wf := range widgetFields {
		if seenField[wf.name] {
			continue
		}
		seenField[wf.name] = true
		td.WidgetFields = append(td.WidgetFields, widgetFieldData{
			Name:   wf.name,
			GoType: wf.goType,
		})
	}

	// Sampled LAST: an import can first be required while rendering a computed
	// body above (e.g. "fmt" via string interpolation).
	for _, p := range c.info.gc.Imports() {
		td.Imports[p] = true
	}
	for _, p := range gc.Imports() {
		td.Imports[p] = true
	}

	return td, nil
}

// emitIRSlotFunc emits a lowering-synthesized Func as a Model method. Only a
// __renderSlot<N> takes the host container; every other synthesized func -- an
// effect's settle halves, the focus-order navigation -- takes the parameters it
// declares, which is none.
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool) {
	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}).withPkg(pkg).withRegistry(reg).withShared(shared).withLocalRefs(fn.LocalRefs).withWrapped(wrapped)
	tr.collectTagComponents(fn.Block)
	bodyStmts := codegen.WalkLowered(context.Background(), fn.Block, tr)

	params := fn.Params
	if fn.SlotRender {
		// The slot body references the reactivity pass's param name, so
		// renaming it here would leave those refs dangling.
		params = []*ir.Param{{Name: "parent", Type: slotParentType(wrapped)}}
	}
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   params,
		Return:   ir.TypVoid,
		Block:    bodyStmts,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// gtk4PromotedHandlerSig declares how a SNGL @event handler lowers into Go.
// The GCallback trampoline carries no args, so the Go handler has no event
// param and each `e.<field>` becomes a widget-getter call on the source node.
type gtk4PromotedHandlerSig struct {
	// EventVar is the SNGL event param name being replaced (e.g. "e").
	// Empty when the SNGL event has no value param (click, change-on-button).
	EventVar string
	// Field is the SNGL event field accessed (e.g. "value"). Empty when
	// no rewrite is needed.
	Field string
	// CType is the GTK widget C type the handler is attached to, and selects
	// the getter. Empty when no getter is needed.
	CType string
}

// gtk4HandlerSig keys on (LoweredFromTag, LoweredFromEvent). A tag may be a
// SNGL stdlib alias or a GIR class name.
func gtk4HandlerSig(tag, event string) gtk4PromotedHandlerSig {
	switch tag {
	case "input", "entry", "GtkEntry":
		// "changed" is the GTK signal name (post-wrapper inline); "input"
		// and "change" are the SNGL stdlib event names.
		if event == "input" || event == "change" || event == "changed" || event == "activate" {
			return gtk4PromotedHandlerSig{EventVar: "event", Field: "value", CType: "GtkEntry"}
		}
	case "checkbox", "GtkCheckButton":
		// "toggled" is the GTK signal; "change" is the SNGL event name.
		if event == "change" || event == "toggled" {
			return gtk4PromotedHandlerSig{EventVar: "event", Field: "checked", CType: "GtkCheckButton"}
		}
	case "toggle", "GtkSwitch":
		if event == "change" || event == "notifyActive" {
			return gtk4PromotedHandlerSig{EventVar: "event", Field: "checked", CType: "GtkSwitch"}
		}
	}
	return gtk4PromotedHandlerSig{}
}

// gtk4EventGetterExpr reads `e.<field>` directly from the source widget of
// cType, replacing the two-way-bind assignment when a node-attached handler is
// promoted to a top-level Func the trampoline calls with no args.
func gtk4EventGetterExpr(cType, nodeID string, wrapped bool) ir.Expr {
	if wrapped {
		return rtEventGetterExpr(cType, codegen.ModelFieldRef(nodeID))
	}
	return cgoEventGetterExpr(cType, &ir.Ident{Name: nodeID, IsElementRef: true, Synthesized: true})
}

func cgoEventGetterExpr(cType string, widgetRef ir.Expr) ir.Expr {
	switch cType {
	case "GtkEntry":
		// In GTK4 the text accessor moved to GtkEditable.
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
		return cgoGBoolean("gtk_check_button_get_active", cType, widgetRef)
	case "GtkSwitch":
		return cgoGBoolean("gtk_switch_get_active", cType, widgetRef)
	}
	return nil
}

// cgoGBoolean calls a getter returning gboolean, which cgo types as C.int: Go
// converts no integer to bool, so the read is a comparison.
func cgoGBoolean(getter, cType string, widgetRef ir.Expr) ir.Expr {
	call := &ir.Call{
		Type:     ir.TypInt,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc(getter),
		Args:     []ir.CallArg{{Value: &ir.Conversion{Type: ir.NativePointerOf(cType), Operand: widgetRef}}},
	}
	return &ir.Binary{Op: ast.BinNeq, Left: call, Right: &ir.Literal{Type: ir.TypInt, Value: "0"}, Type: ir.TypBool}
}

// collectNodeCTypes returns a node-id → GTK C type map from every
// `lower.CreateNode` in the package. A promoted handler needs it to resolve
// element refs created in a sibling Func.
func collectNodeCTypes(pkg *ir.Package) map[string]string {
	out := map[string]string{}
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if call, ok := n.Init.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "CreateNode" && len(call.Args) >= 1 {
					if lit, ok := call.Args[0].Value.(*ir.Literal); ok && lit.Type == ir.TypString {
						tag := lit.Value
						// After passInlinePure every tag here is a GIR-resolved
						// native widget name.
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
			case *ir.NodeInst:
				walk(n.Children)
			case *ir.SlotInst, *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
				*ir.Break, *ir.Continue:
				// No CreateNode call to harvest.
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
	for _, w := range ir.AllWindows(pkg) {
		walk(w.Children)
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			walk(fn.Block)
		}
	}
	return out
}

// collectCreateComponentTargets returns the components instantiated via a
// `lower.CreateComponent` intrinsic — the non-inlinable ones that need a
// generated render<Comp> method.
func collectCreateComponentTargets(pkg *ir.Package) map[*ir.Component]bool {
	out := map[*ir.Component]bool{}
	if pkg == nil {
		return out
	}
	var walk func([]ir.Stmt)
	record := func(call *ir.Call) {
		if call == nil || call.Func == nil || call.Func.Intrinsic != "CreateComponent" || len(call.Args) == 0 {
			return
		}
		if id, ok := call.Args[0].Value.(*ir.Ident); ok {
			if comp, ok := id.Sym.(*ir.Component); ok {
				out[comp] = true
			}
		}
	}
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if call, ok := n.Init.(*ir.Call); ok {
					record(call)
				}
			case *ir.CallStmt:
				record(n.Call)
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.NodeInst:
				walk(n.Children)
			}
		}
	}
	for _, comp := range pkg.Components {
		walk(comp.Body)
		for _, fn := range comp.Funcs {
			if fn != nil {
				walk(fn.Block)
			}
		}
	}
	for _, w := range ir.AllWindows(pkg) {
		walk(w.Children)
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			walk(fn.Block)
		}
	}
	return out
}

// emitIRPromotedHandler emits a node-attached event handler the lower pass
// promoted to a top-level Func. The trampoline calls it with no args, so the
// SNGL event param is dropped and `e.<field>` becomes a widget getter.
func emitIRPromotedHandler(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool) {
	sig := gtk4HandlerSig(fn.LoweredFromTag, fn.LoweredFromEvent)

	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}).withPkg(pkg).withRegistry(reg).withShared(shared).withLocalRefs(fn.LocalRefs).withWrapped(wrapped)
	// Pre-populated because OnPropAssign needs the C type for nodes created
	// in sibling slot Funcs, whose CreateNode sites are not in this Block.
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	tr.collectTagComponents(fn.Block)

	stmts := fn.Block
	var prelude []ir.Stmt
	nodeID := strings.TrimSuffix(fn.Name, "_"+fn.LoweredFromEvent+"_handler")
	cType := tr.idCTypes[nodeID]
	if cType == "" {
		cType = sig.CType
	}

	// Strip the synthesized leading `var = e.<field>` two-way bind and re-emit
	// as `m.<var> = <gettercall>`; the trampoline exposes no event param.
	if sig.EventVar != "" && sig.Field != "" && len(stmts) > 0 {
		if assign, ok := stmts[0].(*ir.Assign); ok {
			target, _ := assign.Target.(*ir.Ident)
			sel, _ := assign.Value.(*ir.Select)
			if target != nil && sel != nil {
				// The SNGL event param may be named anything, so key off the
				// field: the bare-ident operand IS the event param, never `m`.
				if op, _ := sel.Operand.(*ir.Ident); op != nil && op.Name != "m" && sel.Field == sig.Field {
					getter := gtk4EventGetterExpr(cType, nodeID, wrapped)
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

	if sig.CType != "" {
		stmts = substituteWidgetPayload(stmts, fn.Params, cType, gtk4EventGetterExpr(cType, nodeID, wrapped))
	}
	body := codegen.WalkLowered(context.Background(), stmts, tr)
	// Drop self-setter splices: writing the entry's text from inside its own
	// "changed" handler re-fires the signal and recurses.
	body = dropSelfSetterCalls(body, nodeID)
	// A handler the GTK trampoline calls takes no args, and OnAttachHandler
	// only connects a signal that answers to the event. An event no signal
	// answers to is a component's own -- a func-typed prop its instance calls
	// with the payload -- so its declared parameters stand. Dropping those
	// unconditionally left the payload ident undefined in the body that reads
	// it: `func (m *Model) __n0_done_handler() { m.got = v }`.
	params := fn.Params
	if tr.signalFor(nodeID, fn.LoweredFromEvent) != "" {
		params = nil
	}
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   params,
		Return:   ir.TypVoid,
		Block:    append(prelude, body...),
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// dropSelfSetterCalls filters out setter calls on selfNode so a two-way bound
// widget does not recurse into its own changed-signal handler.
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
	if call.Func.Foreign.Path != "C" || !strings.Contains(call.Func.Foreign.Name, "_set_") {
		return false
	}
	first := call.Args[0].Value
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

// gtk4ComponentFuncs is every func some component declares; what is left in
// pkg.Funcs is top level, with no component in scope to read.
func gtk4ComponentFuncs(pkg *ir.Package) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	if pkg == nil {
		return out
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			out[fn] = true
		}
	}
	// A window owns funcs the way a component does, and its state is in the
	// same Model -- so one of its funcs is a method too. This has to agree
	// with golang.ModelFreeFuncs, which is what told the call sites.
	return out
}

// emitGTK4FreeFunc emits a top-level func under its exported name, which is
// what ExprCtx.FreeFuncs told the call sites to expect.
func emitGTK4FreeFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	fnCopy := *fn
	fnCopy.Name = golang.ExportName(fn.Name)
	for _, line := range gc.EmitFuncDef(&fnCopy) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// emitGTK4TypeMethod emits a user type's method as the free
// `ReceiverMethod(this, …)` the Go translator names at every call site. The
// body touches no widget, so it goes through the plain renderer rather than
// the gtk4 translator.
func emitGTK4TypeMethod(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	for _, line := range gc.EmitTypeMethodDef(fn) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// emitGTK4Func emits a top-level user function as a method on *Model, routing
// the body through WalkLowered so a CanvasRedrawStmt becomes queue_draw.
func emitGTK4Func(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool) {
	if len(fn.Block) == 0 {
		return
	}
	tr := newGtk4Translator(gc, func(string, string) {}).withPkg(pkg).withRegistry(reg).withShared(shared).withWrapped(wrapped)
	tr.collectTagComponents(fn.Block)
	// Without the node C types OnPropAssign has no widget to write through and
	// a prop assignment would emit the model write but drop the setter. No
	// known program reaches this emitter with one; it is here so the three
	// emitters do not disagree about what their translator knows.
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	body := codegen.WalkLowered(context.Background(), fn.Block, tr)

	fnCopy := *fn
	// A component's own func names its component as the receiver
	// (passNoImplicitRecv). Inlining folded that component into main, so the
	// Model is the receiver it has here, and the synthetic first parameter is
	// that receiver rather than an argument.
	// EmitFuncDef drops the synthetic first parameter for a func with a
	// receiver, so the component receiver does not also arrive as an argument.
	fnCopy.Receiver = "Model"
	fnCopy.Block = body
	for _, line := range gc.EmitFuncDef(&fnCopy) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// buildRef spells a top-level widget ref the way the tree that created it did.
// A ref the translator did not put in the Model is a local `__nN` in
// buildWidgetTree, so parenting it as `m.__nN` names a field nothing declared
// -- which the generated Go then refused to compile. The fields are the
// authority rather than passNodeEscape's set, because the translator derives
// refs of its own (`__nN__el` for a component instance's root) that no
// lowering pass has heard of.
func buildRef(name string, fields map[string]bool) *ir.Ident {
	if !fields[name] {
		return &ir.Ident{Name: name}
	}
	return &ir.Ident{Name: name, IsElementRef: true, Synthesized: true}
}

// widgetFieldNames is the widget refs that became Model fields.
func widgetFieldNames(fields []widgetField) map[string]bool {
	out := make(map[string]bool, len(fields))
	for _, f := range fields {
		out[f.name] = true
	}
	return out
}

// needsRootWrapper mirrors the conditions inside emitBuildUI that trigger the
// synthetic m.__root wrapper, and must be kept in sync with them.
func needsRootWrapper(buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string) bool {
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		return false
	}
	if len(topLevelRefs) == 1 && isWindowClass(topLevelCType[topLevelRefs[0]]) {
		return false
	}
	return true
}

// isWindowClass reports whether cType is a top-level window widget, which is
// not wrapped in a synthetic m.__root.
func isWindowClass(cType string) bool {
	switch cType {
	case "GtkWindow", "GtkApplicationWindow", "GtkDialog":
		return true
	}
	return false
}

// windowTitleGo is the Go expression for the window's `title` prop, or "" when
// it declares none.
// entryWindowID is the `#id` of the window BuildUI builds, when the program
// opens or closes it, and "" otherwise.
func entryWindowID(ctx *codegen.CodegenCtx) string {
	if !codegen.OpensWindows(ctx.Pkg) {
		return ""
	}
	if wins := ctx.Windows(); len(wins) > 0 && wins[0].Window != nil {
		return wins[0].Window.ID
	}
	return ""
}

func windowTitleGo(ctx *codegen.CodegenCtx, gc *golang.GoIRContext) string {
	wins := ctx.Windows()
	if len(wins) == 0 {
		return ""
	}
	title := wins[0].Window.Prop(ir.WindowTitle)
	if title == nil {
		return ""
	}
	return gc.EvalExpr(title)
}

// emitBuildUI emits BuildUI(app *C.GtkApplication) *C.GtkWidget. Top-level
// widget refs not consumed by an AppendChild are parented into m.__root,
// except when the sole top-level ref is itself a window-class widget, which
// BuildUI returns directly.
func emitBuildUI(b *strings.Builder, buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string, gc *golang.GoIRContext, wrapped bool, title string, fields map[string]bool, winID string) {
	if wrapped {
		emitBuildUIWrapped(b, buildBuf, topLevelRefs, topLevelCType, title, fields, winID)
		return
	}
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		b.WriteString("func (m *Model) buildWidgetTree() {}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
		b.WriteString("\twin := C.gtk_application_window_new(app)\n")
		b.WriteString("\treturn win\n")
		b.WriteString("}\n\n")
		return
	}
	if len(topLevelRefs) == 1 && isWindowClass(topLevelCType[topLevelRefs[0]]) {
		ref := topLevelRefs[0]
		b.WriteString("func (m *Model) buildWidgetTree() {\n")
		b.WriteString(buildBuf.String())
		b.WriteString("}\n\n")
		b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
		b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
		b.WriteString("\tm.buildWidgetTree()\n")
		retIdent := buildRef(ref, fields)
		retExpr := &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: retIdent}
		fmt.Fprintf(b, "\treturn %s\n", gc.EvalExpr(retExpr))
		b.WriteString("}\n\n")
		return
	}
	// buildWidgetTree is idempotent (guarded by m.__root == nil) and BuildUI
	// makes a fresh window per call, so a second BuildUI does not re-parent an
	// already-parented widget.
	rootRef := &ir.Ident{Name: "__root", IsElementRef: true, Synthesized: true}
	rootCtorCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_box_new"),
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: "6"}},
		},
	}
	rootInit := &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: rootCtorCall}
	b.WriteString("func (m *Model) buildWidgetTree() {\n")
	b.WriteString("\tif m.__root != nil {\n\t\treturn\n\t}\n")
	fmt.Fprintf(b, "\tm.__root = %s\n", gc.EvalExpr(rootInit))
	b.WriteString(buildBuf.String())
	for _, ref := range topLevelRefs {
		childRef := buildRef(ref, fields)
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
	b.WriteString("}\n\n")
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
	b.WriteString("\tm.buildWidgetTree()\n")
	b.WriteString("\twin := C.gtk_application_window_new(app)\n")
	winRef := &ir.Ident{Name: "win"}
	setSizeCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc("gtk_window_set_default_size"),
		Args: []ir.CallArg{
			{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWindow"), Operand: winRef}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: "480"}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: "640"}},
		},
	}
	for _, line := range gc.EvalStmt(&ir.CallStmt{Call: setSizeCall}) {
		fmt.Fprintf(b, "\t%s\n", line)
	}
	// Not through gtk4rt: the cgo scaffold does not require that module, so
	// importing it does not build. The string is allocated once and not freed.
	if title != "" {
		fmt.Fprintf(b, "\tC.gtk_window_set_title((*C.GtkWindow)(unsafe.Pointer(win)), C.CString(%s))\n", title)
		gc.RequireImport("unsafe")
	}
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

// emitIRComponentMethod emits the `render<Comp>(props...)` Model method for a
// non-inlinable component the lower pass left as a CreateComponent intrinsic.
func emitIRComponentMethod(b *strings.Builder, cc *codegen.ComponentCtx, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool) {
	methodName := golang.ComponentRenderMethod(cc.Component.Name)

	compGC := gc.ForComponent(cc.Component)
	var params []*ir.Param
	for _, p := range cc.Props {
		params = append(params, &ir.Param{Name: p.Name, Type: p.Type})
		compGC = compGC.WithLocal(p.Name)
	}

	tr := newGtk4Translator(compGC, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}).withPkg(pkg).withRegistry(reg).withShared(shared).withLocalRefs(cc.Component.LocalRefs).withWrapped(wrapped)
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	tr.collectTagComponents(cc.Body)

	body := codegen.WalkLowered(context.Background(), cc.Body, tr)

	var bodyBuf strings.Builder
	for _, stmt := range body {
		for _, line := range compGC.EvalStmt(stmt) {
			fmt.Fprintf(&bodyBuf, "\t%s\n", line)
		}
	}

	var trailer string
	switch tops := tr.topLevel; {
	case len(tops) == 0 && wrapped:
		trailer = "\treturn gtk4rt.LabelNew(\"\")\n"
	case len(tops) == 0:
		trailer = "\treturn C.gtk_label_new(nil)\n"
	case len(tops) == 1 && wrapped:
		ref := tr.qualifyNodeExpr(&ir.Ident{Name: tops[0], IsElementRef: true, Synthesized: true})
		trailer = fmt.Sprintf("\treturn %s\n", compGC.EvalExpr(ref))
	case len(tops) == 1:
		ref := tr.qualifyNodeExpr(&ir.Ident{Name: tops[0], IsElementRef: true, Synthesized: true})
		retExpr := &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: ref}
		trailer = fmt.Sprintf("\treturn %s\n", compGC.EvalExpr(retExpr))
	case wrapped:
		var tb strings.Builder
		tb.WriteString("\t__box := gtk4rt.BoxNew(gtk4rt.OrientationVertical, 6)\n")
		for _, ref := range tops {
			childRef := tr.qualifyNodeExpr(&ir.Ident{Name: ref, IsElementRef: true, Synthesized: true})
			fmt.Fprintf(&tb, "\tgtk4rt.BoxAppend(__box, %s)\n", compGC.EvalExpr(childRef))
		}
		tb.WriteString("\treturn __box\n")
		trailer = tb.String()
	default:
		var tb strings.Builder
		boxCtorCall := &ir.Call{
			Type:     ir.TypDyn,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("gtk_box_new"),
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}},
				{Value: &ir.Literal{Type: ir.TypInt, Value: "6"}},
			},
		}
		boxInit := &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: boxCtorCall}
		fmt.Fprintf(&tb, "\t__box := %s\n", compGC.EvalExpr(boxInit))
		boxRef := &ir.Ident{Name: "__box"}
		for _, ref := range tops {
			childRef := tr.qualifyNodeExpr(&ir.Ident{Name: ref, IsElementRef: true, Synthesized: true})
			appendCall := &ir.Call{
				Type:     ir.TypVoid,
				Receiver: &ir.Ident{Name: "C"},
				Func:     nativeFunc("gtk_box_append"),
				Args: []ir.CallArg{
					{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkBox"), Operand: boxRef}},
					{Value: &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: childRef}},
				},
			}
			for _, line := range compGC.EvalStmt(&ir.CallStmt{Call: appendCall}) {
				fmt.Fprintf(&tb, "\t%s\n", line)
			}
		}
		retExpr := &ir.Conversion{Type: ir.NativePointerOf("GtkWidget"), Operand: boxRef}
		fmt.Fprintf(&tb, "\treturn %s\n", compGC.EvalExpr(retExpr))
		trailer = tb.String()
	}

	retType := "*C.GtkWidget"
	if wrapped {
		retType = gtk4rtHandleType
	}
	b.WriteString("func (m *Model) " + methodName + "(")
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name + " " + golang.IRTypeToGo(p.Type))
	}
	b.WriteString(") " + retType + " {\n")
	b.WriteString(bodyBuf.String())
	b.WriteString(trailer)
	b.WriteString("}\n\n")
}

// emitEventInvokers emits one Model method per (#id, @event) pair. Each fires
// the real GTK signal, so the test runner drives `c.<id>.<event>()` through
// the trampoline and Go-callback bridge rather than re-running the handler.
func emitEventInvokers(b *strings.Builder, invokers []gtkEventInvoker, wrapped bool) {
	seen := map[string]bool{}
	for _, inv := range invokers {
		methodName := inv.IDLabel + golang.ExportName(inv.SnglEvent)
		if seen[methodName] {
			continue // duplicate id+event — keep the first
		}
		seen[methodName] = true
		fmt.Fprintf(b, "// %s fires the %q signal on the #%s widget; for tests.\n",
			methodName, inv.GTKSignal, inv.IDLabel)
		// An entry's events carry its text, which the handler reads back off
		// the widget; the invoker takes the payload's value and puts it there
		// first.
		var valueParam, preFire string
		if inv.WidgetType == "GtkEntry" {
			valueParam = "v string"
			if wrapped {
				preFire = fmt.Sprintf("gtk4rt.EditableSetTextQuiet(m.%s, v)", inv.FieldName)
			} else {
				preFire = fmt.Sprintf("__v := C.CString(v)\n\tdefer C.free(unsafe.Pointer(__v))\n\tC.sngl_set_entry_text_quiet((*C.GtkEditable)(unsafe.Pointer(m.%s)), __v)", inv.FieldName)
			}
		}
		switch {
		case inv.Payload != "":
			fmt.Fprintf(b, "func (m *Model) %s(e %s) {\n", methodName, inv.Payload)
			emitInvokerStateWrite(b, inv, wrapped)
		case valueParam != "":
			fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", methodName, valueParam)
		default:
			fmt.Fprintf(b, "func (m *Model) %s() {\n", methodName)
		}
		if preFire != "" {
			fmt.Fprintf(b, "\t%s\n", preFire)
		}
		switch prop, notify := strings.CutPrefix(inv.GTKSignal, "notify::"); {
		case inv.GTKSignal == "pressed":
			fmt.Fprintf(b, "\tC.sngl_emit_pressed(unsafe.Pointer(m.%s))\n", inv.FieldName)
		case notify:
			// g_signal_emit_by_name cannot be handed the GParamSpec a
			// notification carries; g_object_notify builds it.
			fmt.Fprintf(b, "\tprop := C.CString(%q)\n\tdefer C.free(unsafe.Pointer(prop))\n", prop)
			fmt.Fprintf(b, "\tC.g_object_notify((*C.GObject)(unsafe.Pointer(m.%s)), prop)\n", inv.FieldName)
		case wrapped:
			// gtk4rt.Emit is the same g_signal_emit_by_name, behind the
			// runtime this mode already links against.
			fmt.Fprintf(b, "\tgtk4rt.Emit(m.%s, %q)\n", inv.FieldName, inv.GTKSignal)
		default:
			fmt.Fprintf(b, "\tsig := C.CString(%q)\n", inv.GTKSignal)
			b.WriteString("\tdefer C.free(unsafe.Pointer(sig))\n")
			fmt.Fprintf(b, "\tC.sngl_emit(C.gpointer(unsafe.Pointer(m.%s)), sig)\n", inv.FieldName)
		}
		b.WriteString("}\n\n")
	}
}

// emitInvokerStateWrite writes the payload's state into the widget, which is
// what a user's flip does and what fires the signal. State the widget already
// holds fires nothing, so the invoker then falls through to firing it itself.
func emitInvokerStateWrite(b *strings.Builder, inv gtkEventInvoker, wrapped bool) {
	setter := invokerStateSetter(inv.WidgetType, wrapped)
	if wrapped {
		fmt.Fprintf(b, "\tif gtk4rt.CheckButtonGetActive(m.%s) != e.%s {\n", inv.FieldName, inv.PayloadField)
		fmt.Fprintf(b, "\t\t%s(m.%s, e.%s)\n\t\treturn\n\t}\n", setter, inv.FieldName, inv.PayloadField)
		return
	}
	getter := strings.Replace(setter, "_set_", "_get_", 1)
	ptr := fmt.Sprintf("(*C.%s)(unsafe.Pointer(m.%s))", inv.WidgetType, inv.FieldName)
	fmt.Fprintf(b, "\tif (%s(%s) != 0) != e.%s {\n", getter, ptr, inv.PayloadField)
	fmt.Fprintf(b, "\t\tv := C.gboolean(0)\n\t\tif e.%s {\n\t\t\tv = 1\n\t\t}\n", inv.PayloadField)
	fmt.Fprintf(b, "\t\t%s(%s, v)\n\t\treturn\n\t}\n", setter, ptr)
}

// emitGTK4Main appends the GTK application bootstrap to callbacks.go, whose
// preamble is declarations only, so the //export snglActivate can coexist.
func emitGTK4Main(b *strings.Builder, cfg Config, wrapped bool, pkg *ir.Package) error {
	if pkg != nil && pkg.Run != nil {
		if !wrapped {
			return errors.New("gtk4: @run needs the gtk4rt entry point, and this program's generated code calls into cgo directly")
		}
		callee := ""
		if len(pkg.Run.Block) > 0 {
			callee = golang.ModelCallee(pkg, pkg.Run, "m")
		}
		emitGTK4RunMainWrapped(b, callee)
		return nil
	}
	if wrapped {
		emitGTK4MainWrapped(b)
		return nil
	}
	teardown := pkg != nil && pkg.Teardown != nil
	if teardown {
		// The model is local to activate, and the exit this has to run at is
		// g_application_run returning in main. A package var is the only place
		// both can reach.
		b.WriteString("\nvar snglModel *Model\n")
	}
	b.WriteString("\n//export snglActivate\n")
	b.WriteString("func snglActivate(app *C.GtkApplication, _ C.gpointer) {\n")
	b.WriteString("\tm := New()\n")
	if teardown {
		// New() already answers a *Model here, unlike bubbletea's value model,
		// so the address-of this used to take made it a **Model.
		b.WriteString("\tsnglModel = m\n")
	}
	b.WriteString("\twin := m.BuildUI(app)\n")
	b.WriteString("\tC.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))\n")
	b.WriteString("}\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("\truntime.LockOSThread()\n")
	// G_APPLICATION_NON_UNIQUE avoids needing an app-id, which
	// gtk_application_new otherwise requires to be reverse-DNS-valid.
	b.WriteString("\tapp := C.gtk_application_new(nil, C.G_APPLICATION_NON_UNIQUE)\n")
	b.WriteString("\tC.g_signal_connect_data((C.gpointer)(unsafe.Pointer(app)),\n")
	b.WriteString("\t\tC.CString(\"activate\"),\n")
	b.WriteString("\t\tC.GCallback(C.snglActivate), nil, nil, 0)\n")
	b.WriteString("\tstatus := C.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil)\n")
	if teardown {
		b.WriteString("\tif snglModel != nil {\n")
		fmt.Fprintf(b, "\t\tsnglModel.%s()\n", pkg.Teardown.Name)
		b.WriteString("\t}\n")
	}
	b.WriteString("\tif status != 0 {\n")
	b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"gtk: application exited with status %d\\n\", status)\n")
	b.WriteString("\t\tos.Exit(int(status))\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return nil
}

func irVarInit(v *ir.Var, gc *golang.GoIRContext) string {
	return golang.LowerVarInit(v, gc)
}

// gtk4IRAlertFunc lowers Alert.* calls: the platform has no toast widget, so
// notifications print to stderr and Alert.confirm returns true.
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
