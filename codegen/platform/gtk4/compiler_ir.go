package gtk4

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/canvasutil"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/ir"
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
	model, callbacks, err := c.emitIR()
	if err != nil {
		return err
	}
	var prune []string
	for _, name := range golang.PayloadPruneCandidates(c.ctx.Pkg, codegen.TriggerArgs(c.ctx.Pkg)) {
		if !strings.Contains(callbacks.body, name) {
			prune = append(prune, name)
		}
	}
	if len(prune) > 0 {
		model.body = golang.PruneStructDecls(model.body, prune)
	}
	if err := model.write(sink, "model.go", c.cfg.Package, req); err != nil {
		return err
	}
	return callbacks.write(sink, "callbacks.go", c.cfg.Package, req)
}

// mainBodyStmts is what buildWidgetTree builds: the package body, whose
// parent is the application, or the body of a component a harness isolated as
// the whole program.
func mainBodyStmts(ctx *codegen.CodegenCtx) []ir.Stmt {
	if main := ctx.RootDecl(); main != nil {
		return main.Stmts
	}
	return ctx.Pkg.Body
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
		// nil for a binding no declaration made: a document's route parameters,
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
				init := irVarInit(v, ctxGC)
				// Written in a context of its own, as a component's var is
				// below: its imports are the file's -- the i18n runtime a
				// locale's default names, which a test build rooted at the
				// component wrote with none.
				for _, p := range ctxGC.Imports() {
					gc.RequireImport(p)
				}
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      ctxGoType,
					init:        init,
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
		// A component's var is written in a context of its own, whose imports
		// are the file's all the same.
		for _, p := range varGC.Imports() {
			gc.RequireImport(p)
		}
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
func (c *compilation) emitIR() (model, callbacks goFile, err error) {
	if !c.disableWrapped {
		m, cb, werr := c.emitIRMode(true)
		if werr == nil && !cgoRefRE.MatchString(m.body) && !cgoRefRE.MatchString(cb.body) {
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

func (c *compilation) emitIRMode(wrapped bool) (model, callbacks goFile, err error) {
	c.wrapped = wrapped
	c.shared = &emitShared{markup: collectMarkup(c.ctx.Pkg), toplevels: collectToplevels(c.ctx.Pkg), images: collectImages(c.ctx.Pkg), scrolls: collectScrolls(c.ctx.Pkg), toplevelIcons: map[string]string{}, toplevelTitles: map[string]ir.Expr{}}
	if !wrapped {
		if err := refuseCgoVisibleWrites(c.ctx.Pkg, c.shared.toplevels); err != nil {
			c.shared.fail(err)
		}
	}
	exprCtx := c.ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = gtk4IRAlertFunc
	gc.InstanceRecords = true
	gc.InstanceReads = codegen.InstanceReads(c.ctx.Pkg)

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
	var topLevelRefs, appChildren []string
	var topLevelCType map[string]string
	// The ids a test drives one copy of, whose copies the Model lists.
	indexed := ir.IndexedRefIDs(c.ctx.Pkg)
	presentIDs, keyedPresent := codegen.NullTestedRefs(c.ctx.Pkg)
	recordInvoker := func(inv gtkEventInvoker) { vc.eventInvokers = append(vc.eventInvokers, inv) }
	if len(bodyStmts) > 0 {
		tr := newGtk4Translator(gc, c.widgetFieldSink(&widgetFields)).
			withPkg(c.ctx.Pkg).withRegistry(c.registry).withShared(c.shared).
			withLocalRefs(mainComponentLocalRefs(c.ctx)).withWrapped(c.wrapped).
			withInvokerSink(recordInvoker).withIndexed(indexed, presentIDs, nil)
		tr.building = true
		tr.collectTagComponents(bodyStmts)
		body := codegen.WalkLowered(context.Background(), bodyStmts, tr)
		for _, stmt := range body {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(&buildBuf, "\t%s\n", line)
			}
		}
		topLevelRefs = tr.topLevel
		topLevelCType = tr.idCTypes
		appChildren = tr.appChildren
	}
	// The root is the application unless a harness isolated a component: the
	// package body is then what is built, and each node in it was attached to
	// the application as it was.
	appRoot := c.ctx.RootDecl() == nil

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
		if fn.IsMethod() && golang.LiftsToFreeFunc(c.ctx.Pkg, fn) {
			emitGTK4TypeMethod(&funcBuf, fn, gc)
			continue
		}
		// stateFuncs is the set ModelFreeFuncs kept from the call sites; see
		// its doc for what a package var costs a free function.
		if !fn.IsMethod() && !componentFuncs[fn] && !stateFuncs[fn] && fn.Lowered.Tag == "" && !fn.Synthesized {
			emitGTK4FreeFunc(&funcBuf, fn, gc)
			continue
		}
		if fn.Synthesized {
			emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped, indexed, presentIDs, recordInvoker)
			continue
		}
		if fn.Lowered.Tag != "" {
			emitIRPromotedHandler(&funcBuf, fn, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
			continue
		}
		emitGTK4Func(&funcBuf, fn, gc, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
	}
	emitCanvasDrawFuncs(&funcBuf)

	presenceFields := map[string]string{}
	createTargets := collectCreateComponentTargets(c.ctx.Pkg)
	for _, cc := range c.ctx.NonRootComponents() {
		// A component the build renders as a live instance gets a record of
		// its own; its widget fields and its state stay off the Model, which
		// is the whole point. See emitComponentInstance.
		if isInstanceComponent(cc.Component) {
			emitComponentInstance(&funcBuf, cc, gc, c.ctx.Pkg, c.registry, c.shared, c.wrapped, c.ctx.Canvases.All(),
				recordInvoker,
				func(f widgetField) { widgetFields = appendWidgetFieldOnce(widgetFields, f) },
				indexed, presentIDs, func(id, field string) { presenceFields[id] = field })
			continue
		}
		if createTargets[cc.Component] {
			emitIRComponentMethod(&funcBuf, cc, gc, &widgetFields, c.ctx.Pkg, c.registry, c.shared, c.wrapped)
		}
	}

	// The inline-cgo scaffold has no runtime to attach a window to, so it puts
	// the first one's box in the window BuildUI makes, as it did the one
	// window it could build before a window was a node.
	cgoTitle, cgoIcon := "", ""
	if appRoot && !c.wrapped {
		topLevelRefs = append(slices.Clip(topLevelRefs), appChildren...)
		if len(appChildren) > 0 {
			if t := c.shared.toplevelTitles[appChildren[0]]; t != nil {
				cgoTitle = gc.EvalExpr(t)
			}
			cgoIcon = c.shared.toplevelIcons[appChildren[0]]
		}
	}
	// The Model struct needs the __root field whenever emitBuildUI will emit
	// the synthetic wrapper; needsRootWrapper mirrors its conditions.
	if appRoot && c.wrapped {
		widgetFields = appendWidgetFieldOnce(widgetFields, widgetField{name: "__built", goType: "bool"})
	} else if needsRootWrapper(&buildBuf, topLevelRefs, topLevelCType) {
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

	// Pre-rendered so gc.RequireImport calls from EvalExpr land before
	// newScaffoldData samples gc.Imports().
	var buildUIBuf strings.Builder
	if appRoot && c.wrapped {
		first := ""
		if len(appChildren) > 0 {
			first = appChildren[0]
		}
		emitBuildUIApp(&buildUIBuf, &buildBuf, first, widgetFieldNames(widgetFields))
	} else {
		emitBuildUI(&buildUIBuf, &buildBuf, topLevelRefs, topLevelCType, gc, c.wrapped, cgoTitle, cgoIcon, widgetFieldNames(widgetFields))
	}
	// emitEventInvokers emits raw unsafe.Pointer strings; register the import
	// structurally rather than by scanning the output.
	if len(vc.eventInvokers) > 0 && !c.wrapped {
		gc.RequireImport("unsafe")
	}
	var eventInvokersBuf strings.Builder
	emitEventInvokers(&eventInvokersBuf, vc.eventInvokers, c.wrapped)
	for _, inv := range vc.eventInvokers {
		// A calendar's invoker selects the day through model.go's helper.
		if inv.WidgetType == "GtkCalendar" {
			c.shared.calendarDate = true
		}
	}
	emitPresenceChecks(&eventInvokersBuf, c.ctx.Pkg, presentIDs, presenceFields, widgetFields)
	emitCopyRegistries(&eventInvokersBuf, widgetFields)
	if keyedInstances(c.ctx.Pkg) {
		emitUnlistInstance(&eventInvokersBuf, widgetFields, c.wrapped, len(codegen.InstanceRootIDs(c.ctx.Pkg)) > 0)
	}
	emitKeyedPresenceChecks(&eventInvokersBuf, keyedPresent, widgetFields)
	for _, h := range golang.InstanceHolds(gc.InstanceReads) {
		widgetFields = append(widgetFields, widgetField{name: h[0], goType: h[1]})
	}
	golang.EmitInstanceReads(&eventInvokersBuf, gc.InstanceReads, instanceMethodName, func(v *ir.Var) string { return instanceVarGoType(v, c.wrapped) })

	// The drawing-area trampoline registration emits an `unsafe.Pointer` cast.
	if hasCanvas {
		gc.RequireImport("unsafe")
		// The emitted Style struct decl references snglcolor.Color.
		gc.RequireImport(canvasutil.ColorImportPath)
	}

	td, err := c.newScaffoldData(widgetFields, funcBuf.String(), gc)
	if err != nil {
		return goFile{}, goFile{}, err
	}
	if hasCanvas {
		td.LangHelpers += canvasStdlibDeclsExcluding(td.Structs)
		td.HasCanvas = true
	}
	if c.ctx != nil && c.ctx.Pkg != nil && c.ctx.Pkg.UsesErrorHandling {
		td.LangHelpers += golang.ErrorEventDecl
	}
	if c.ctx != nil && c.ctx.Pkg != nil {
		td.LangHelpers += golang.PackageConstDecls(c.ctx.Pkg, gc)
	}

	var modelBuf, callbacksBuf strings.Builder
	writeModelHead(&modelBuf, td)
	writeCallbacksHead(&callbacksBuf, td)
	modelBuf.WriteString(buildUIBuf.String())
	modelBuf.WriteString(eventInvokersBuf.String())
	// main() goes in callbacks.go, not model.go: cgo //export directives can't
	// coexist with the model.go preamble's static defs.
	if c.cfg.Main {
		if err := emitGTK4Main(&callbacksBuf, c.cfg, c.wrapped, c.ctx.Pkg, appRoot && c.wrapped); err != nil {
			c.shared.errs = append(c.shared.errs, err)
		}
	}

	if len(c.shared.errs) > 0 {
		return goFile{}, goFile{}, errors.Join(c.shared.errs...)
	}
	model = goFile{cgoPreamble: modelPreamble(td), imports: td.Imports, body: modelBuf.String()}
	callbacks = goFile{cgoPreamble: callbacksPreamble(td), imports: callbacksImports(td), body: callbacksBuf.String()}
	return model, callbacks, nil
}

func (c *compilation) newScaffoldData(widgetFields []widgetField, functionCode string, gc *golang.GoIRContext) (scaffoldData, error) {
	td := scaffoldData{
		Package:           c.cfg.Package,
		Main:              c.cfg.Main,
		FunctionCode:      functionCode,
		NeedsBoolToInt:    c.shared.boolToInt,
		NeedsGObjectSet:   c.shared.gObjectSet,
		NeedsSlotAnchor:   c.shared.slotAnchor,
		NeedsCalendarDate: c.shared.calendarDate,
		NeedsDropDown:     c.shared.dropDown,
		Wrapped:           c.wrapped,
		CLinks:            golang.CPreambleLines(ir.ReachedCLinks(c.ctx.Pkg)),
	}
	if c.wrapped {
		// Widget fields are gtk4rt.Handle; ensure the import is present even if
		// (unusually) no gtk4rt call was emitted into this file.
		td.Imports = append(td.Imports, gtk4rtPkg)
	}
	// Lang-tracked helpers + their imports.
	helpers := golang.HelpersNeeded(c.ctx.Pkg)
	td.Imports = append(td.Imports, helpers.Imports()...)
	td.LangHelpers = helpers.Emit() + golang.EmitMergeFuncs(c.ctx.Pkg.MergeStructs)

	// Units (excluding the special-cased `duration`).
	td.UnitDecls = golang.EmitUnitTypeDecls(c.info.Units) + golang.EmitEnumTypeDecls(c.info.Enums)

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
	td.Imports = append(td.Imports, c.info.gc.Imports()...)
	td.Imports = append(td.Imports, gc.Imports()...)

	return td, nil
}

// emitIRSlotFunc emits a lowering-synthesized Func as a Model method. Only a
// __renderSlot<N> takes the host container; every other synthesized func -- an
// effect's settle halves, the focus-order navigation -- takes the parameters it
// declares, which is none.
//
// A widget a render func builds is one a test drives as much as one BuildUI
// builds, so the func records its invokers too; one of an id a test indexes
// is kept by its copy as it is built (withIndexed).
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool, indexed, present map[string]bool, invokerSink func(gtkEventInvoker)) {
	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}).withPkg(pkg).withRegistry(reg).withShared(shared).withLocalRefs(fn.Lowered.LocalRefs).withWrapped(wrapped).
		withInvokerSink(invokerSink).withIndexed(indexed, present, fn.Block)
	// A handler the slot builds may patch a node BuildUI or another slot
	// creates, which OnPropAssign can only write knowing its C type; without
	// it the write was dropped and the node went stale.
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	tr.collectTagComponents(fn.Block)
	block := readWidgets(fn.Block, widgetReader(wrapped, shared, modelNode(wrapped)))
	bodyStmts := codegen.WalkLowered(context.Background(), block, tr)

	params := fn.Params
	if fn.Lowered.SlotRender {
		// The slot body references the reactivity pass's param name, so
		// renaming it here would leave those refs dangling.
		params = []*ir.Param{{Name: "parent", Type: slotParentType(wrapped)}}
	}
	synthesized := &ir.Func{
		Name:    fn.Name,
		Lowered: ir.FuncLowering{HostReceiver: "Model"},
		Params:  params,
		Return:  ir.TypVoid,
		Block:   bodyStmts,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// widgetReads are the getters gtk4.sngl declares for an override to read what
// a control holds, by intrinsic id, and the widget each reads. A GTK signal
// hands its handler the widget it fired on and nothing else, so
// `@toggled(b) { checked = checkActive(b) }` is how the value a change
// reports reaches the program.
var widgetReads = map[string]string{
	"gtk4.checkActive":   "GtkCheckButton",
	"gtk4.switchActive":  "GtkSwitch",
	"gtk4.entryText":     "GtkEntry",
	"gtk4.calendarDate":  "GtkCalendar",
	"gtk4.dropDownLabel": "GtkDropDown",
}

// widgetWrites are the setters gtk4.sngl declares for an override to write
// what a control holds when it decides to, by intrinsic id, and the widget
// each writes.
var widgetWrites = map[string]string{
	"gtk4.setEntryText": "GtkEntry",
}

// widgetAccess spells widgetReads and widgetWrites calls for one scope.
type widgetAccess struct {
	read  func(cType string, ref ir.Expr) ir.Expr
	write func(cType string, ref, value ir.Expr) ir.Expr
}

// selfIsNode is a signal handler's body with its parameters read as the node
// the handler is attached to. A GTK signal hands its callback the instance it
// fired on, which is what an event declares as `self`; the trampoline passes
// the handler nothing, and the instance is the node it was connected on.
func selfIsNode(stmts []ir.Stmt, params []*ir.Param, node ir.Expr) []ir.Stmt {
	if len(params) == 0 {
		return stmts
	}
	isParam := func(id *ir.Ident) bool {
		return slices.ContainsFunc(params, func(p *ir.Param) bool {
			return p != nil && (id.Sym == ir.Symbol(p) || (id.Sym == nil && id.Name == p.Name))
		})
	}
	stmts = ir.CloneStmtsSharingDecls(stmts)
	_ = ir.RewriteExprs(stmts, func(e ir.Expr) (ir.Expr, error) {
		if id, ok := e.(*ir.Ident); ok && isParam(id) {
			return ir.CloneExprSharingDecls(node), ir.SkipDir
		}
		return e, nil
	})
	return stmts
}

// readWidgets rewrites each widgetReads and widgetWrites call in stmts to the
// getter or setter access answers for the widget it is handed. The statements
// are copied first: a body is the lowered IR's, shared with every other reader
// of it.
func readWidgets(stmts []ir.Stmt, access widgetAccess) []ir.Stmt {
	found := false
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && (widgetReads[c.Func.Intrinsic] != "" || widgetWrites[c.Func.Intrinsic] != "") {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	if !found {
		return stmts
	}
	stmts = ir.CloneStmtsSharingDecls(stmts)
	// A handler attached in these statements reads the node it is attached to
	// through its parameter, which signalLambda maps once the node is in scope;
	// read here, the parameter would be taken for a widget handed in as a value.
	attached := map[*ir.Param]struct{}{}
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if c, ok := e.(*ir.Call); ok && c.Func != nil && c.Func.Intrinsic == ir.NodeOpAttachHandler && len(c.Args) == 3 {
			if lam, ok := c.Args[2].Value.(*ir.Lambda); ok && lam.Func != nil {
				for _, p := range lam.Func.Params {
					attached[p] = struct{}{}
				}
			}
		}
		return nil
	})
	_ = ir.RewriteExprs(stmts, func(e ir.Expr) (ir.Expr, error) {
		c, ok := e.(*ir.Call)
		if !ok || c.Func == nil || len(c.Args) == 0 {
			return e, nil
		}
		if id, ok := c.Args[0].Value.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok {
				if _, ok := attached[p]; ok {
					return e, nil
				}
			}
		}
		if cType := widgetWrites[c.Func.Intrinsic]; cType != "" && len(c.Args) == 2 {
			if w := access.write(cType, c.Args[0].Value, c.Args[1].Value); w != nil {
				return w, ir.SkipDir
			}
			return e, nil
		}
		cType := widgetReads[c.Func.Intrinsic]
		if cType == "" || len(c.Args) != 1 {
			return e, nil
		}
		if g := access.read(cType, c.Args[0].Value); g != nil {
			return g, ir.SkipDir
		}
		return e, nil
	})
	return stmts
}

// widgetReader is readWidgets' read for one scope, which qualify spells a
// node's handle in. A widget that is no node of the scope arrived as a value
// -- a handler lifted out of a component built at run time hands the widget
// its signal fired on to the code that holds the handler's body -- and a
// widget value has no Go type of its own, so it is asserted to its field's.
// gtk4rt wraps no getter for some widgets, and the cgo one there is what makes
// the program fall back to inline cgo.
func widgetReader(wrapped bool, shared *emitShared, qualify func(name string) ir.Expr) widgetAccess {
	// What is not a name is a node already spelled: selfIsNode's, which is
	// the Model's field.
	spell := func(cType string, ref ir.Expr) ir.Expr {
		if id, ok := ref.(*ir.Ident); ok {
			if isNodeRef(id) {
				return qualify(strings.TrimPrefix(id.Name, "m."))
			}
			return &ir.Ident{Name: id.Name + ".(" + widgetFieldGoType(cType, wrapped) + ")"}
		}
		return ref
	}
	return widgetAccess{read: func(cType string, ref ir.Expr) ir.Expr {
		w := spell(cType, ref)
		if wrapped {
			if g := rtEventGetterExpr(cType, w); g != nil {
				return g
			}
		}
		return shared.cgoEventGetter(cType, w)
	}, write: func(cType string, ref, value ir.Expr) ir.Expr {
		w := spell(cType, ref)
		if wrapped {
			return rtEventSetterExpr(cType, w, value)
		}
		return cgoEventSetterExpr(cType, w, value)
	}}
}

// cgoEventSetterExpr is the inline-cgo write of widgetWrites, the twin of
// cgoEventGetterExpr.
func cgoEventSetterExpr(cType string, widgetRef, value ir.Expr) ir.Expr {
	switch cType {
	case "GtkEntry":
		return nativeCall("sngl_set_entry_text_quiet", ir.TypVoid, cgoCast("GtkEditable", widgetRef), cString(value))
	}
	return nil
}

// isNodeRef reports whether id names a node: one a lowering synthesized, or a
// program's `#id`.
func isNodeRef(id *ir.Ident) bool {
	if id.IsElementRef || strings.HasPrefix(id.Name, "__n") || strings.HasPrefix(id.Name, "m.") {
		return true
	}
	v, ok := id.Sym.(*ir.Var)
	return ok && v.NodeHandle
}

// modelNode spells a node of the Model, in the mode the file is emitted in.
func modelNode(wrapped bool) func(name string) ir.Expr {
	return func(name string) ir.Expr {
		if wrapped {
			return codegen.ModelFieldRef(name, gtk4rtHandleIRType())
		}
		return &ir.Ident{Name: name, IsElementRef: true, Synthesized: true}
	}
}

// cgoEventGetter is cgoEventGetterExpr, noting a helper model.go must
// emit for the getter it returns.
func (s *emitShared) cgoEventGetter(cType string, widgetRef ir.Expr) ir.Expr {
	g := cgoEventGetterExpr(cType, widgetRef)
	if g != nil && cType == "GtkCalendar" {
		s.calendarDate = true
	}
	return g
}

func cgoEventGetterExpr(cType string, widgetRef ir.Expr) ir.Expr {
	switch cType {
	case "GtkEntry":
		// In GTK4 the text accessor moved to GtkEditable.
		cast := &ir.Conversion{Type: ir.NativePointerOf("GtkEditable"), Operand: widgetRef}
		getText := &ir.Call{
			Type:     ir.NativePointerOf("char"),
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
	case "GtkCalendar":
		// The date is a *time.Time built from three getters on a GDateTime
		// that has to be released, which no one expression can say: the
		// helper is Go in model.go's head, emitted when this is reached.
		return &ir.Call{
			Type: ir.NativeGoPointerOf("time.Time"),
			Func: &ir.Func{Name: calendarDateHelper, Foreign: ir.Foreign{Name: calendarDateHelper}},
			Args: []ir.CallArg{{Value: &ir.Conversion{Type: ir.NativePointerOf(cType), Operand: widgetRef}}},
		}
	case "GtkDropDown":
		label := &ir.Call{
			Type:     ir.NativePointerOf("char"),
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("sngl_drop_down_label"),
			Args:     []ir.CallArg{{Value: &ir.Conversion{Type: ir.NativePointerOf(cType), Operand: widgetRef}}},
		}
		return &ir.Call{
			Type:     ir.TypString,
			Receiver: &ir.Ident{Name: "C"},
			Func:     nativeFunc("GoString"),
			Args:     []ir.CallArg{{Value: label}},
		}
	}
	return nil
}

// calendarDateHelper is model.go's Go function reading a GtkCalendar's
// selected day, and calendarSelectHelper the one selecting a day;
// emitShared.calendarDate says they are called.
const (
	calendarDateHelper   = "snglCalendarDate"
	calendarSelectHelper = "snglCalendarSelect"
)

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
// `lower.CreateNode` in the package. A promoted handler and a render slot's
// handlers need it to resolve element refs created in a sibling Func.
func collectNodeCTypes(pkg *ir.Package) map[string]string {
	out, _ := collectNodeTags(pkg)
	return out
}

// collectToplevels is every Toplevel id a `lower.CreateNode` in the package
// makes, whichever scope makes it: a slot at the root of the package body
// creates a window that a handler emitted before it shows and hides.
func collectToplevels(pkg *ir.Package) map[string]bool {
	_, prims := collectNodeTags(pkg)
	tops := map[string]bool{}
	for id, tag := range prims {
		if tag == toplevelTag {
			tops[id] = true
		}
	}
	return tops
}

// collectImages is every image primitive id a `lower.CreateNode` in the
// package makes, by its tag, for the reason collectToplevels is.
func collectImages(pkg *ir.Package) map[string]string {
	_, prims := collectNodeTags(pkg)
	out := map[string]string{}
	for id, tag := range prims {
		if imagePrimitive(tag) {
			out[id] = tag
		}
	}
	return out
}

// collectScrolls is every Scroll primitive id, for the reason
// collectToplevels is.
func collectScrolls(pkg *ir.Package) map[string]bool {
	_, prims := collectNodeTags(pkg)
	out := map[string]bool{}
	for id, tag := range prims {
		if tag == scrollTag {
			out[id] = true
		}
	}
	return out
}

func collectNodeTags(pkg *ir.Package) (map[string]string, map[string]string) {
	out := map[string]string{}
	prims := map[string]string{}
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
						if cType := primitiveCType(tag); cType != "" {
							out[n.Name] = cType
							prims[n.Name] = tag
						}
					}
				}
			case *ir.If, *ir.For, *ir.Match:
				for _, b := range ir.TransparentBlocks(n) {
					walk(*b)
				}
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
		return out, prims
	}
	for _, comp := range pkg.Components {
		walk(comp.Stmts)
		for _, fn := range comp.Funcs {
			if fn != nil {
				walk(fn.Block)
			}
		}
	}
	walk(pkg.Body)
	for _, fn := range pkg.Funcs {
		if fn != nil {
			walk(fn.Block)
		}
	}
	return out, prims
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
			case *ir.If, *ir.For, *ir.Match:
				for _, b := range ir.TransparentBlocks(n) {
					walk(*b)
				}
			case *ir.NodeInst:
				walk(n.Children)
			}
		}
	}
	for _, comp := range pkg.Components {
		walk(comp.Stmts)
		for _, fn := range comp.Funcs {
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

// emitIRPromotedHandler emits a node-attached event handler the lower pass
// promoted to a top-level Func. The trampoline calls it with no args, so the
// SNGL event param is dropped and `e.<field>` becomes a widget getter.
func emitIRPromotedHandler(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField, pkg *ir.Package, reg *gir.TypeRegistry, shared *emitShared, wrapped bool) {
	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: widgetFieldGoType(cType, wrapped)})
	}).withPkg(pkg).withRegistry(reg).withShared(shared).withLocalRefs(fn.Lowered.LocalRefs).withWrapped(wrapped)
	// Pre-populated because OnPropAssign needs the C type for nodes created
	// in sibling slot Funcs, whose CreateNode sites are not in this Block.
	maps.Copy(tr.idCTypes, collectNodeCTypes(pkg))
	tr.collectTagComponents(fn.Block)

	nodeID := strings.TrimSuffix(fn.Name, "_"+fn.Lowered.Event+"_handler")
	// A handler the GTK trampoline calls takes no args, and OnAttachHandler
	// only connects a signal that answers to the event. An event no signal
	// answers to is a component's own -- a func-typed prop its instance calls
	// with the payload -- so its declared parameters stand. Dropping those
	// unconditionally left the payload ident undefined in the body that reads
	// it: `func (m *Model) __n0_done_handler() { m.got = v }`.
	params := fn.Params
	stmts := fn.Block
	if tr.signalFor(nodeID, fn.Lowered.Event) != "" {
		params = nil
		stmts = selfIsNode(stmts, fn.Params, modelNode(wrapped)(nodeID))
	}
	stmts = readWidgets(stmts, widgetReader(wrapped, shared, modelNode(wrapped)))
	body := codegen.WalkLowered(context.Background(), stmts, tr)
	// Drop self-setter splices: writing the entry's text from inside its own
	// "changed" handler re-fires the signal and recurses.
	body = dropSelfSetterCalls(body, nodeID)
	synthesized := &ir.Func{
		Name:    fn.Name,
		Lowered: ir.FuncLowering{HostReceiver: "Model"},
		Params:  params,
		Return:  ir.TypVoid,
		Block:   body,
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
	fnCopy.Name = gc.FreeFuncName(fn.Name)
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
	body := codegen.WalkLowered(context.Background(), readWidgets(fn.Block, tr.widgetReader()), tr)

	fnCopy := *fn
	// A component's own func names its component as the receiver
	// (passNoImplicitRecv). Inlining folded that component into main, so the
	// Model is the receiver it has here, and the synthetic first parameter is
	// that receiver rather than an argument.
	// EmitFuncDef drops the synthetic first parameter for a func with a
	// receiver, so the component receiver does not also arrive as an argument.
	fnCopy.Receiver = nil
	fnCopy.Lowered.HostReceiver = "Model"
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

// emitBuildUI emits BuildUI(app *C.GtkApplication) *C.GtkWidget. Top-level
// widget refs not consumed by an AppendChild are parented into m.__root,
// except when the sole top-level ref is itself a window-class widget, which
// BuildUI returns directly.
func emitBuildUI(b *strings.Builder, buildBuf *strings.Builder, topLevelRefs []string, topLevelCType map[string]string, gc *golang.GoIRContext, wrapped bool, title, icon string, fields map[string]bool) {
	if wrapped {
		emitBuildUIWrapped(b, buildBuf, topLevelRefs, topLevelCType, title, fields)
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
	rootCtorCall := nativeCall("gtk_box_new", ir.NativePointerOf("GtkWidget"), cOrientationVertical(), intLit("6"))
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
	if icon != "" {
		setIcon := rtCall("WindowSetIcon", ir.TypVoid,
			&ir.Conversion{Type: gtk4rtHandleIRType(), Operand: cgoCast("", winRef)},
			&ir.Conversion{Type: gtk4rtHandleIRType(), Operand: cgoCast("", buildRef(icon, fields))})
		for _, line := range gc.EvalStmt(&ir.CallStmt{Call: setIcon}) {
			fmt.Fprintf(b, "\t%s\n", line)
		}
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

	body := codegen.WalkLowered(context.Background(), readWidgets(cc.Body, tr.widgetReader()), tr)

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
		boxCtorCall := nativeCall("gtk_box_new", ir.NativePointerOf("GtkWidget"), cOrientationVertical(), intLit("6"))
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
	keyed := map[string]bool{}
	for _, inv := range invokers {
		keyed[inv.IDLabel] = keyed[inv.IDLabel] || inv.List
	}
	for _, inv := range invokers {
		methodName := inv.IDLabel + golang.ExportName(inv.SnglEvent)
		if seen[methodName] {
			continue // duplicate id+event — keep the first
		}
		seen[methodName] = true
		if inv.WidgetType == "GtkDropDown" {
			fmt.Fprintf(b, "// %s selects the choice labelled v on the #%s widget; for tests.\n",
				methodName, inv.IDLabel)
		} else if inv.RtFire != "" {
			fmt.Fprintf(b, "// %s fires %q on the #%s widget; for tests.\n",
				methodName, inv.SnglEvent, inv.IDLabel)
		} else {
			fmt.Fprintf(b, "// %s fires the %q signal on the #%s widget; for tests.\n",
				methodName, inv.GTKSignal, inv.IDLabel)
		}
		emitEventInvoker(b, inv, methodName, "", "m."+inv.FieldName, wrapped)
		if !keyed[inv.IDLabel] {
			continue
		}
		fmt.Fprintf(b, "// %s fires it on the copy of #%s with the key; for tests.\n",
			codegen.InvokerAt(methodName), inv.IDLabel)
		emitEventInvoker(b, inv, codegen.InvokerAt(methodName), "key string", "m."+copiesField(inv.IDLabel)+"[key]", wrapped)
	}
}

// emitEventInvoker writes one invoker, firing the signal on the widget w
// spells. lead is a parameter ahead of the event's own, or "".
func emitEventInvoker(b *strings.Builder, inv gtkEventInvoker, methodName, lead, w string, wrapped bool) {
	// An entry's events carry its text, which the handler reads back off
	// the widget; the invoker takes the text and puts it there first.
	var valueParam, preFire string
	if inv.WidgetType == "GtkEntry" {
		valueParam = "v string"
		if wrapped {
			preFire = fmt.Sprintf("gtk4rt.EditableSetTextQuiet(%s, v)", w)
		} else {
			preFire = fmt.Sprintf("__v := C.CString(v)\n\tdefer C.free(unsafe.Pointer(__v))\n\tC.sngl_set_entry_text_quiet((*C.GtkEditable)(unsafe.Pointer(%s)), __v)", w)
		}
	}
	// A calendar's carries the day picked, which the invoker selects
	// before firing day-selected, as a click on the day does.
	if inv.WidgetType == "GtkCalendar" {
		valueParam = "v *time.Time"
		if wrapped {
			preFire = fmt.Sprintf("gtk4rt.CalendarSelectDate(%s, v)", w)
		} else {
			preFire = fmt.Sprintf("%s((*C.GtkCalendar)(unsafe.Pointer(%s)), v)", calendarSelectHelper, w)
		}
	}
	// A drop-down's carries the label chosen, which the invoker selects: the
	// selection notifies, as a click on the choice does, so nothing is fired
	// after it.
	if inv.WidgetType == "GtkDropDown" {
		valueParam = "v string"
		if wrapped {
			preFire = fmt.Sprintf("gtk4rt.DropDownSelect(%s, v)", w)
		} else {
			preFire = fmt.Sprintf("__v := C.CString(v)\n\tdefer C.free(unsafe.Pointer(__v))\n\tC.sngl_drop_down_select((*C.GtkDropDown)(unsafe.Pointer(%s)), __v)", w)
		}
	}
	params := func(p string) string {
		if lead == "" {
			return p
		}
		if p == "" {
			return lead
		}
		return lead + ", " + p
	}
	switch {
	case inv.State:
		fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", methodName, params("v bool"))
	case valueParam != "":
		fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", methodName, params(valueParam))
	default:
		fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", methodName, params(""))
	}
	if lead != "" {
		fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn\n\t}\n", w)
	}
	if inv.State {
		emitInvokerStateWrite(b, inv, w, wrapped)
	}
	if preFire != "" {
		fmt.Fprintf(b, "\t%s\n", preFire)
	}
	if inv.WidgetType == "GtkDropDown" {
		b.WriteString("}\n\n")
		return
	}
	switch prop, notify := strings.CutPrefix(inv.GTKSignal, "notify::"); {
	case inv.RtFire != "" && wrapped:
		fmt.Fprintf(b, "\tgtk4rt.%s(%s)\n", inv.RtFire, w)
	case inv.RtFire != "":
		fmt.Fprintf(b, "\tgtk4rt.%s(gtk4rt.Handle(unsafe.Pointer(%s)))\n", inv.RtFire, w)
	case inv.GTKSignal == "pressed":
		fmt.Fprintf(b, "\tC.sngl_emit_pressed(unsafe.Pointer(%s))\n", w)
	case notify:
		// g_signal_emit_by_name cannot be handed the GParamSpec a
		// notification carries; g_object_notify builds it.
		fmt.Fprintf(b, "\tprop := C.CString(%q)\n\tdefer C.free(unsafe.Pointer(prop))\n", prop)
		fmt.Fprintf(b, "\tC.g_object_notify((*C.GObject)(unsafe.Pointer(%s)), prop)\n", w)
	case wrapped:
		// gtk4rt.Emit is the same g_signal_emit_by_name, behind the
		// runtime this mode already links against.
		fmt.Fprintf(b, "\tgtk4rt.Emit(%s, %q)\n", w, inv.GTKSignal)
	default:
		fmt.Fprintf(b, "\tsig := C.CString(%q)\n", inv.GTKSignal)
		b.WriteString("\tdefer C.free(unsafe.Pointer(sig))\n")
		fmt.Fprintf(b, "\tC.sngl_emit(C.gpointer(unsafe.Pointer(%s)), sig)\n", w)
	}
	b.WriteString("}\n\n")
}

// emitInvokerStateWrite writes the state a test passed into the widget, which
// is what a user's flip does and what fires the signal. State the widget
// already holds fires nothing, so the invoker then falls through to firing it
// itself.
func emitInvokerStateWrite(b *strings.Builder, inv gtkEventInvoker, w string, wrapped bool) {
	setter := invokerStateSetter(inv.WidgetType, wrapped)
	if wrapped {
		fmt.Fprintf(b, "\tif gtk4rt.CheckButtonGetActive(%s) != v {\n", w)
		fmt.Fprintf(b, "\t\t%s(%s, v)\n\t\treturn\n\t}\n", setter, w)
		return
	}
	getter := strings.Replace(setter, "_set_", "_get_", 1)
	ptr := fmt.Sprintf("(*C.%s)(unsafe.Pointer(%s))", inv.WidgetType, w)
	fmt.Fprintf(b, "\tif (%s(%s) != 0) != v {\n", getter, ptr)
	b.WriteString("\t\ton := C.gboolean(0)\n\t\tif v {\n\t\t\ton = 1\n\t\t}\n")
	fmt.Fprintf(b, "\t\t%s(%s, on)\n\t\treturn\n\t}\n", setter, ptr)
}

// emitGTK4Main appends the GTK application bootstrap to callbacks.go, whose
// preamble is declarations only, so the //export snglActivate can coexist.
func emitGTK4Main(b *strings.Builder, cfg Config, wrapped bool, pkg *ir.Package, app bool) error {
	if app {
		callee := ""
		if pkg.Run != nil && len(pkg.Run.Block) > 0 {
			callee = golang.ModelCallee(pkg, pkg.Run, "m")
		}
		emitGTK4AppMain(b, callee, pkg.Run != nil, pkg.Teardown)
		return nil
	}
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
