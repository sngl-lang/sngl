package fyne

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

type irAnalysis struct {
	*codegen.CommonAnalysis
	binds      []irBind
	externs    []irExtern
	computeds  []irComputed
	dataEvents map[string][]*ir.EventHandler
	gc         *golang.GoIRContext
}

type irBind struct {
	name        string
	goType      string
	init        ir.Expr             // nil → rendered as "nil" (or ZeroValueGo) at template-build time
	varRef      *ir.Var             // set for real state vars → init rendered via golang.LowerVarInit (applies the var's declared type, e.g. string→date parse helpers)
	initGC      *golang.GoIRContext // optional: per-component GC for init rendering (nil → use top-level)
	noAccessors bool                // skip getter/setter generation (e.g. synthesized slot vars)
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

func analyzeIR(ctx *codegen.CodegenCtx) *irAnalysis {
	// Set before any body is translated, since it decides how a call to one of
	// these renders, and before ScopedExprCtx clones it. See
	// golang.ModelFreeFuncs.
	ctx.ExprCtx.FreeFuncs = golang.ModelFreeFuncs(ctx.Pkg)
	ctx.ExprCtx.ModelParamFuncs = golang.ModelParamFuncs(ctx.Pkg)
	exprCtx := ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		dataEvents:     make(map[string][]*ir.EventHandler),
		gc:             gc,
	}

	pkg := ctx.Pkg
	for _, path := range golang.BaseImports(pkg) {
		gc.RequireImport(path)
	}

	// NoInlineComponents inlined every component but a harness root into the
	// body instantiating it, so there are no remaining child-component vars to
	// collect.
	for _, ov := range ctx.ModelState() {
		// nil for a binding no declaration made: a window's route parameters,
		// which the slot population declares and the request fills. None of
		// the special cases below can be one -- a const, a synthesized var and
		// a slot ref are all things a body or a pass declared -- so they are
		// asked only where there is a declaration to ask.
		v := ov.Var()
		if v != nil && v.IsConst {
			// Consts skip getter/setter: the field name would collide with
			// the accessor (APP_NAME field + APP_NAME() method).
			info.binds = append(info.binds, irBind{
				name:        v.Name,
				goType:      golang.VarGoType(v),
				init:        v.Init,
				noAccessors: true,
			})
			continue
		}
		if v != nil && v.Synthesized {
			if v.Name == "__root" {
				// The __root sentinel is built through a native call so that
				// rendering this init registers the container import.
				initCall := &ir.Call{
					Type:     ir.TypDyn,
					Receiver: &ir.Ident{Name: "container"},
					Func:     &ir.Func{Foreign: ir.Foreign{Path: "fyne.io/fyne/v2/container", Name: "container.NewVBox"}},
				}
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      "*fyne.Container",
					init:        initCall,
					noAccessors: true,
				})
				continue
			}
			// NoContext's `__ctx_<name>` vars get a plain field of their
			// declared type, so a call reading one sees a concrete type.
			if strings.HasPrefix(v.Name, "__ctx_") {
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      golang.VarGoType(v),
					init:        v.Init,
					noAccessors: true,
				})
				continue
			}
			// __slot<N> vars hold widget refs for the renderSlot teardown loop.
			// noAccessors because ExportName("__slot0") is unchanged, so an
			// accessor would collide with the field.
			//
			// Named rather than taken as the shape of every synthesized var:
			// an instance registry (`__instN_live`) is a synthesized list too,
			// and calling it a list of widgets typed the field as
			// []fyne.CanvasObject while the render assigned []*CardInstance to
			// it.
			if ir.IsSlotVarName(v.Name) {
				info.binds = append(info.binds, irBind{
					name:        v.Name,
					goType:      "[]fyne.CanvasObject",
					init:        &ir.Literal{Type: ir.TypNull},
					noAccessors: true,
				}, irBind{
					name:        codegen.SlotAnchorField(v.Name),
					goType:      "fyne.CanvasObject",
					init:        &ir.Literal{Type: ir.TypNull},
					noAccessors: true,
				})
				continue
			}
		}
		goType := golang.BindGoType(ov.Type(), ov.Init())
		if strings.HasPrefix(goType, "time.") {
			gc.RequireImport("time")
		}
		info.binds = append(info.binds, irBind{
			name:   ov.Name(),
			goType: goType,
			init:   ov.Init(),
			varRef: v,
			// A name Go cannot export gets no accessor, because the accessor
			// would be spelled the same as the field and not compile. That is
			// every `__`-prefixed name, which is every name a lowering pass
			// synthesized -- and nothing outside the program reads one, so
			// there is no accessor to want. The `__slot<N>` case above is this
			// rule, written before there was a second var it applied to.
			noAccessors: golang.ExportName(ov.Name()) == ov.Name(),
		})
		// A parameter carries no @change: nothing in the page assigns it.
		if v != nil && len(v.Handlers) > 0 {
			info.dataEvents[v.Name] = v.Handlers
		}
	}

	allFuncs := ctx.AllFuncs()
	for _, f := range allFuncs {
		if codegen.IsComputed(f) {
			info.computeds = append(info.computeds, irComputed{
				name:   f.Name,
				goType: golang.FuncReturnGoType(f),
				fn:     f,
			})
		}
	}

	if info.NeedsToast {
		gc.RequireImport("time")
	}

	return info
}

// emitIR renders the model file body — no package clause or import block —
// with its Go import paths and cgo preamble, for the caller's FileEmitter.
// The aliases come back alongside because the file emitter is a context of its
// own: an alias forced during translation does not reach it.
func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config, lang codegen.LangTranslator) (string, []string, map[string]string, string, error) {
	exprCtx := ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	gc.AlertFunc = fyneIRAlertFunc
	// fyne builds a non-inlinable component as a record, so a CreateComponent
	// renders as a call to that record's ctor.
	gc.InstanceRecords = true

	gc.RequireImport("fyne.io/fyne/v2")

	var buildBuf strings.Builder
	var widgetFields []irWidgetField
	var eventInvokers []fyneEventInvoker
	var entrySync []entrySyncRec
	widgetImports := map[string]bool{}
	addWidgetImport := func(p string) { widgetImports[p] = true }
	// Every scope that walks a body reports here, so a prop this platform has
	// no setter for fails the build rather than going missing from the screen.
	// Deduped, because one prop written from several places is one mistake.
	var propErrs []error
	seenPropErr := map[string]bool{}
	failProp := func(err error) {
		if seenPropErr[err.Error()] {
			return
		}
		seenPropErr[err.Error()] = true
		propErrs = append(propErrs, err)
	}
	singleRoot := true
	var endLabel, endContainer int

	wins := ctx.Windows()
	windowNames := make(map[string]bool)
	for _, w := range wins {
		windowNames[w.Name] = true
	}

	var windowCodes []string

	// Package-wide because a promoted handler references nodes created in a
	// sibling slot Func, which per-Func discovery would not see.
	// AllFuncs already covers every window's funcs, component-declared ones
	// included; appending them here again emitted each twice.
	allFuncs := ctx.AllFuncs()
	allFuncs = append(allFuncs, promotedHandlersInNonMainComponents(ctx, allFuncs)...)
	nodeSpecs, err := collectNodes(ctx.Pkg, allFuncs)
	if err != nil {
		return "", nil, nil, "", err
	}
	// Before any translator runs: OnAppendChild wraps a styled child in the
	// override naming its theme, so the names have to exist by then.
	themes := assignThemes(nodeSpecs)

	// Shared into every translator so OnCreateNode builds the raster-backed
	// widget and OnDefault wires reactive redraws.
	canvasByID, canvasByNode := collectCanvases(ctx.Canvases)
	hasCanvas := len(canvasByID) > 0

	if len(wins) <= 1 {
		if len(wins) > 0 && len(wins[0].Body) > 0 {
			// The package body is not emitted here, so its first settles run
			// once the entry window's widgets exist.
			bodyStmts := append(slices.Clip(wins[0].Body), ctx.Pkg.RootMounts()...)
			tr := newFyneTranslator(gc, nodeSpecs, func(name, goType string) {
				widgetFields = append(widgetFields, irWidgetField{name: name, goType: goType})
			}, addWidgetImport, failProp).withLocalRefs(mainScopeLocalRefs(ctx)).
				withInvokerSink(func(inv fyneEventInvoker) {
					eventInvokers = append(eventInvokers, inv)
				})
			tr.canvasByID, tr.canvasByNode = canvasByID, canvasByNode
			body := codegen.WalkLowered(context.Background(), bodyStmts, tr)
			for _, stmt := range body {
				for _, line := range gc.EvalStmt(stmt) {
					fmt.Fprintf(&buildBuf, "\t%s\n", line)
				}
			}
			// topLevel holds the widget ids no AppendChild consumed — the
			// window roots.
			tops := tr.topLevel
			switch len(tops) {
			case 0:
				// A purely reactive body has no top-level widgets; a
				// synthesized __root bind then drives emitIRBuildUI to return
				// m.__root, and an empty label is the fallback.
				buildBuf.WriteString("\tcontent := fyne.CanvasObject(widget.NewLabel(\"\"))\n")
				gc.RequireImport("fyne.io/fyne/v2/widget")
			case 1:
				fmt.Fprintf(&buildBuf, "\tcontent := fyne.CanvasObject(%s)\n", gc.EvalExpr(topRef(tr, tops[0])))
			default:
				singleRoot = false
				gc.RequireImport("fyne.io/fyne/v2/container")
				buildBuf.WriteString("\tvar parts []fyne.CanvasObject\n")
				for _, ref := range tops {
					fmt.Fprintf(&buildBuf, "\tparts = append(parts, %s)\n", gc.EvalExpr(topRef(tr, ref)))
				}
			}
		}
	} else {
		gc.RequireImport("fyne.io/fyne/v2/container")
		widgetFields = append(widgetFields, irWidgetField{"activeWindow", "string"})
		for _, w := range wins {
			widgetFields = append(widgetFields, irWidgetField{windowBoxField(w.Name), "*fyne.Container"})
		}

		for i, w := range wins {
			buildFn := windowBuildFunc(w.Name)
			var winBuf strings.Builder
			tr := newFyneTranslator(gc, nodeSpecs, func(name, goType string) {
				widgetFields = append(widgetFields, irWidgetField{name: name, goType: goType})
			}, addWidgetImport, failProp).withLocalRefs(w.Window.LocalRefs)
			tr.canvasByID, tr.canvasByNode = canvasByID, canvasByNode
			winBody := w.Body
			if i == 0 {
				winBody = append(slices.Clip(winBody), ctx.Pkg.RootMounts()...)
			}
			body := codegen.WalkLowered(context.Background(), winBody, tr)
			for _, stmt := range body {
				for _, line := range gc.EvalStmt(stmt) {
					fmt.Fprintf(&winBuf, "\t%s\n", line)
				}
			}
			var winCode strings.Builder
			fmt.Fprintf(&winCode, "func (m *Model) %s() fyne.CanvasObject {\n", buildFn)
			winCode.WriteString(winBuf.String())
			tops := tr.topLevel
			switch len(tops) {
			case 0:
				winCode.WriteString("\treturn widget.NewLabel(\"\")\n")
				gc.RequireImport("fyne.io/fyne/v2/widget")
			case 1:
				fmt.Fprintf(&winCode, "\treturn %s\n", gc.EvalExpr(topRef(tr, tops[0])))
			default:
				gc.RequireImport("fyne.io/fyne/v2/container")
				winCode.WriteString("\treturn container.NewVBox(")
				for i, ref := range tops {
					if i > 0 {
						winCode.WriteString(", ")
					}
					winCode.WriteString(gc.EvalExpr(topRef(tr, ref)))
				}
				winCode.WriteString(")\n")
			}
			winCode.WriteString("}\n\n")
			windowCodes = append(windowCodes, winCode.String())
		}
	}

	// Sub-component widget fields are collected before the template data is
	// built, so the Model struct declares every field they reference.
	var componentCodes []string
	for _, cc := range ctx.NonRootComponents() {
		// A component the build renders as a live instance gets a record of
		// its own; its widget fields and its state stay off the Model, which
		// is the whole point. See emitComponentInstance.
		if isInstanceComponent(cc.Component) {
			var ib strings.Builder
			emitComponentInstance(&ib, cc, gc, nodeSpecs, addWidgetImport, canvasByID, canvasByNode, ctx.Canvases.All(), failProp)
			componentCodes = append(componentCodes, ib.String())
			continue
		}
		code, compFields, nextLabel, nextContainer := renderIRComponentMethod(
			cc, ctx, gc, info, windowNames, endLabel, endContainer, nodeSpecs, addWidgetImport, failProp,
		)
		componentCodes = append(componentCodes, code)
		widgetFields = append(widgetFields, compFields...)
		endLabel = nextLabel
		endContainer = nextContainer
	}

	var computedDatas []computedData
	for _, comp := range info.computeds {
		if instanceOwnsFunc(ctx.Pkg, comp.fn) {
			continue
		}
		var body string
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				body = "\treturn " + gc.EvalExpr(ret.Value)
			}
		}
		if body == "" && comp.fn != nil {
			// A block-bodied computed renders its whole statement list, so a
			// non-string return type does not degrade to `return ""`.
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
		computedDatas = append(computedDatas, computedData{
			Name:   comp.name,
			GoType: comp.goType,
			Body:   body,
		})
	}

	componentFuncs := fyneComponentFuncs(ctx.Pkg)
	stateFuncs := golang.ModelStateFuncs(ctx.Pkg)
	var funcBuf strings.Builder
	for _, fn := range allFuncs {
		if fn.IsTest || codegen.IsComputed(fn) {
			continue
		}
		if instanceOwnsFunc(ctx.Pkg, fn) {
			continue
		}
		// golang.LiftsToFreeFunc is the one answer the call site uses too.
		if fn.Receiver != "" && golang.LiftsToFreeFunc(ctx.Pkg, fn.Receiver) {
			emitIRFyneTypeMethod(&funcBuf, fn, gc)
			continue
		}
		// stateFuncs is the set ModelFreeFuncs kept from the call sites; see
		// its doc for what a package var costs a free function.
		if fn.Receiver == "" && !componentFuncs[fn] && !stateFuncs[fn] && fn.LoweredFromTag == "" && !fn.Synthesized {
			emitIRFyneFreeFunc(&funcBuf, fn, gc)
			continue
		}
		// A promoted node handler routes to emitIRPromotedHandler even when
		// Synthesized: the two-way-bind writeback handler is both.
		if fn.LoweredFromTag != "" {
			emitIRPromotedHandler(&funcBuf, fn, gc, &widgetFields, nodeSpecs, addWidgetImport, canvasByNode, failProp)
			continue
		}
		if fn.Synthesized {
			emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields, nodeSpecs, addWidgetImport, canvasByNode, failProp)
			continue
		}
		emitIRFyneModelMethod(&funcBuf, fn, gc, &widgetFields, nodeSpecs, addWidgetImport, canvasByNode, failProp)
	}

	// The draw routines are codegen's own and are in no func list, so they
	// come from the drawings rather than out of the loop below -- which is
	// what the `canvasByFunc[fn] != nil` arm there used to do, and what made
	// this platform need a name match to keep them off the generic path.
	// Their place among the emitted functions moved when they stopped being
	// entries in one; the order of Go declarations is inert.
	all := ctx.Canvases.All()
	for i := range all {
		// A drawing inside a component with a record of its own is that
		// record's: emitComponentInstance emits it with the instance receiver,
		// because the widget and the context it paints into are fields there.
		if isInstanceComponent(all[i].Owner) {
			continue
		}
		emitIRCanvasDraw(&funcBuf, &all[i], gc, canvasByNode, nodeSpecs, addWidgetImport, failProp)
	}

	// A var handler's body carries reactivity-injected widget updates like an
	// event handler's, so it goes through WalkLowered for the same reason. The
	// value being assigned is the setter's own `v`.
	varHandlerCode := map[string]string{}
	for name, handlers := range info.dataEvents {
		var hb strings.Builder
		for _, h := range handlers {
			if h.Name != "change" || h.Func == nil {
				continue
			}
			tr := newFyneTranslator(gc, nodeSpecs, func(name, goType string) {
				widgetFields = append(widgetFields, irWidgetField{name: name, goType: goType})
			}, addWidgetImport, failProp)
			tr.canvasByID, tr.canvasByNode = canvasByID, canvasByNode
			hgc := codegen.BindVarHandlerValue(gc, h, "v")
			for _, stmt := range codegen.WalkLowered(context.Background(), h.Func.Block, tr) {
				for _, line := range hgc.EvalStmt(stmt) {
					fmt.Fprintf(&hb, "\t%s\n", line)
				}
			}
		}
		varHandlerCode[name] = hb.String()
	}

	emitFyneEventInvokers(&funcBuf, eventInvokers)

	td, err := newIRTemplateData(info, cfg, widgetFields, entrySync, widgetImports, funcBuf.String(), varHandlerCode, gc, ctx, lang)
	if hasCanvas {
		// Skip any struct the user already declared, to avoid a duplicate
		// type decl.
		td.LangHelpers += canvasStdlibDeclsExcluding(td.Structs)
	}
	if ctx.Pkg.UsesErrorHandling {
		td.LangHelpers += golang.ErrorEventDecl
	}
	if decls := emitThemeDecls(themes); decls != "" {
		td.LangHelpers += decls
		for _, path := range themeImports(themes) {
			gc.RequireImport(path)
		}
	}
	if err != nil {
		return "", nil, nil, "", err
	}
	td.Computeds = computedDatas

	// Emitted directly rather than through a template, so every framework
	// reference registers its import through gc as it is written.
	// Before anything is written out: a prop with no setter would come back as
	// a page missing one value, which is the thing that has to be reported
	// rather than shipped.
	if len(propErrs) > 0 {
		return "", nil, nil, "", errors.Join(propErrs...)
	}

	var b strings.Builder
	emitFyneModel(&b, &td, gc)

	if len(wins) <= 1 {
		emitIRBuildUI(&b, info, &buildBuf, singleRoot, gc)
	} else {
		emitIRMultiWindowCode(&b, wins, windowCodes)
	}
	for _, code := range componentCodes {
		b.WriteString(code)
	}

	if cfg.Main {
		emitIRMain(&b, cfg, info, ctx.Pkg)
	}

	imports := make([]string, 0, len(td.Imports)+8)
	for p := range td.Imports {
		imports = append(imports, p)
	}
	imports = append(imports, gc.Imports()...)
	return b.String(), imports, assignedAliases(gc), td.CgoPreamble, nil
}

func newIRTemplateData(info *irAnalysis, cfg Config, widgetFields []irWidgetField, entrySync []entrySyncRec, widgetImports map[string]bool, functionCode string, varHandlerCode map[string]string, gc *golang.GoIRContext, ctx *codegen.CodegenCtx, lang codegen.LangTranslator) (templateData, error) {
	td := templateData{
		Package:      cfg.Package,
		Main:         cfg.Main,
		AppName:      cfg.AppName,
		NeedsToast:   info.NeedsToast,
		FunctionCode: functionCode,
	}

	// The structural imports only; the dynamic ones are recorded at their emit
	// sites and unioned in emitIR.
	td.Imports = map[string]bool{}
	if cfg.Main {
		td.Imports["os"] = true
		td.Imports["fyne.io/fyne/v2/app"] = true
	}
	for _, p := range info.gc.Imports() {
		td.Imports[p] = true
	}
	for p := range widgetImports {
		td.Imports[p] = true
	}

	// `duration` is special-cased to time.Duration.
	td.UnitDecls = golang.EmitUnitTypeDecls(info.Units)

	helpers := golang.HelpersNeeded(ctx.Pkg)
	for _, imp := range helpers.Imports() {
		td.Imports[imp] = true
	}
	td.LangHelpers = helpers.Emit() + golang.EmitMergeFuncs(ctx.Pkg.MergeStructs)

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

	for _, bind := range info.binds {
		getter := golang.ExportName(bind.name)
		initStr := "nil"
		if bind.init != nil {
			renderGC := bind.initGC
			if renderGC == nil {
				renderGC = gc
			}
			if bind.varRef != nil {
				// Routed through LowerVarInit so the declared type drives
				// temporal literal lowering: a `date` var initialized from a
				// string emits mustParseDate(...) rather than a bare string.
				initStr = golang.LowerVarInit(bind.varRef, renderGC)
			} else {
				initStr = renderGC.EvalExpr(bind.init)
			}
		} else if !bind.noAccessors {
			initStr = golang.ZeroValueGo(bind.goType)
		}
		bd := bindData{
			Name:        bind.name,
			GoType:      bind.goType,
			InitVal:     initStr,
			Getter:      getter,
			NoAccessors: bind.noAccessors,
		}

		var extra strings.Builder
		for _, sync := range entrySync {
			if sync.varName == bind.name && bind.goType == "string" {
				fmt.Fprintf(&extra, "\tm.%s%s(v)\n", sync.fieldName, sync.target)
			}
		}
		extra.WriteString(varHandlerCode[bind.name])
		bd.SetterExtra = extra.String()
		td.Binds = append(td.Binds, bd)
	}

	for _, ext := range info.externs {
		td.Externs = append(td.Externs, externData{
			Name:   golang.ExportName(ext.name),
			GoType: ext.goType,
		})
	}

	// One field per id, however many times the id was created: an unrolled loop
	// writes the same `__nN` per iteration and the sink is fed at each, which
	// undeduped is `__n5 redeclared` in the struct.
	seenField := make(map[string]bool, len(widgetFields))
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

	var cNativeImports []*ir.NativeImport
	for _, imp := range ctx.Pkg.Imports {
		if imp.Native == nil {
			continue
		}
		for _, fn := range imp.Native.Funcs {
			if fn.Foreign.Path == "C" {
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

// fyneComponentFuncs is every func some component declares; what is left in
// pkg.Funcs is top level, with no component in scope to read.
func fyneComponentFuncs(pkg *ir.Package) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	if pkg == nil {
		return out
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			out[fn] = true
		}
	}
	// A window owns funcs the way a component does and its state is in the
	// same Model, so one of its funcs is a Model method too.
	return out
}

// emitIRFyneTypeMethod emits a method on a user type as the free function its
// call sites name: `func GlyphRow(gl Glyph, row int) string`.
func emitIRFyneTypeMethod(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	for _, line := range gc.EmitTypeMethodDef(fn) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// emitIRFyneFreeFunc emits a top-level func under its exported name, which is
// what ExprCtx.FreeFuncs told the call sites to expect.
func emitIRFyneFreeFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
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

// emitIRFyneModelMethod emits a func the Model dispatches through — a
// component's own action, a top-level func that touches state, a clone the
// inliner hoisted — as a Model method. Its body goes through the same
// widget-aware translation a promoted handler's does, because it touches the
// same things: state, and the element refs that are Model fields. Emitted
// through the plain renderer instead, `__n0.Text = …` names a variable that
// does not exist, unqualified and untranslated, where the Spec's setter should
// have made it `m.__n0.SetText(…)`.
//
// Every Model method takes this path. It used to be component funcs alone,
// with a plain-renderer fallback beside it, and which of the two a func got
// was decided by an owner-membership test: a window's funcs were component-like
// and a package's were not. That was always too narrow -- passReactivity
// injects updaters into a top-level func's body as readily as into a
// component's -- and a window owning nothing widened the gap to every effect
// entry point, since those are the package's funcs now.
func emitIRFyneModelMethod(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField, nodeSpecs map[string]*fyneSpec, importSink func(string), canvasByNode map[*ir.NodeInst]*canvasMeta, failSink func(error)) {
	if len(fn.Block) == 0 {
		return
	}
	tr := newFyneTranslator(gc, nodeSpecs, func(name, goType string) {
		*widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
	}, importSink, failSink).withLocalRefs(fn.LocalRefs)
	tr.canvasByNode = canvasByNode

	params := fn.Params
	if len(params) > 0 && params[0].Receiver {
		params = params[1:]
	}
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   params,
		Return:   fn.Return,
		Block:    codegen.WalkLowered(context.Background(), fn.Block, tr),
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func emitIRBuildUI(b *strings.Builder, info *irAnalysis, buildBuf *strings.Builder, singleRoot bool, gc *golang.GoIRContext) {
	b.WriteString("// BuildUI creates the widget tree. Call once; widgets are updated selectively.\n")
	b.WriteString("func (m *Model) BuildUI() fyne.CanvasObject {\n")

	if buildBuf.Len() == 0 {
		b.WriteString("\treturn widget.NewLabel(\"\")\n")
		gc.RequireImport("fyne.io/fyne/v2/widget")
		b.WriteString("}\n\n")
		return
	}

	b.WriteString(buildBuf.String())

	hasRoot := false
	for _, bind := range info.binds {
		if bind.name == "__root" {
			hasRoot = true
			break
		}
	}

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
		// A synthesized __root is reused as the returned container, so slot
		// updaters operating on m.__root mutate the displayed tree.
		if hasRoot {
			b.WriteString("\tm.__root.Objects = parts\n")
			if info.NeedsToast {
				b.WriteString("\tm.toastLabel = widget.NewLabel(\"\")\n")
				b.WriteString("\tm.toastBox = container.NewVBox(m.toastLabel)\n")
				b.WriteString("\tm.toastBox.Hide()\n")
				b.WriteString("\treturn container.NewBorder(nil, m.toastBox, nil, nil, m.__root)\n")
			} else {
				b.WriteString("\treturn m.__root\n")
			}
		} else if info.NeedsToast {
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

// renderIRComponentMethod pre-renders one user-defined component. The counters
// continue from startLabel/startContainer so that m.fieldN names never collide
// with the main BuildUI's or an earlier component method's.
func renderIRComponentMethod(
	cc *codegen.ComponentCtx,
	ctx *codegen.CodegenCtx,
	gc *golang.GoIRContext,
	info *irAnalysis,
	windowNames map[string]bool,
	startLabel, startContainer int,
	specs map[string]*fyneSpec,
	importSink func(string),
	failSink func(error),
) (code string, fields []irWidgetField, nextLabel, nextContainer int) {
	methodName := golang.ComponentRenderMethod(cc.Component.Name)

	var params []*ir.Param
	for _, p := range cc.Props {
		params = append(params, &ir.Param{Name: p.Name, Type: p.Type})
	}
	hasSlot := cc.Component.ChildrenType != nil
	if hasSlot {
		params = append(params, &ir.Param{
			Name: "slotContent",
			Type: ir.NativeGoNamed("fyne.CanvasObject"),
		})
	}

	compGC := gc.ForComponent(cc.Component)
	for _, p := range cc.Props {
		compGC = compGC.WithLocal(p.Name)
	}

	var compFields []irWidgetField
	tr := newFyneTranslator(compGC, specs, func(name, goType string) {
		compFields = append(compFields, irWidgetField{name: name, goType: goType})
	}, importSink, failSink).withLocalRefs(cc.Component.LocalRefs)

	bodyStmts := codegen.WalkLowered(context.Background(), cc.Body, tr)

	tops := tr.topLevel
	var trailer string
	switch len(tops) {
	case 0:
		trailer = "\treturn widget.NewLabel(\"\")\n"
	case 1:
		trailer = fmt.Sprintf("\treturn %s\n", compGC.EvalExpr(topRef(tr, tops[0])))
	default:
		var tb strings.Builder
		tb.WriteString("\treturn container.NewVBox(")
		for i, ref := range tops {
			if i > 0 {
				tb.WriteString(", ")
			}
			tb.WriteString(compGC.EvalExpr(topRef(tr, ref)))
		}
		tb.WriteString(")\n")
		trailer = tb.String()
	}

	synthesized := &ir.Func{
		Name:     methodName,
		Receiver: "Model",
		Params:   params,
		Return:   ir.NativeGoNamed("fyne.CanvasObject"),
		Block:    bodyStmts,
	}
	lines := compGC.EmitFuncDef(synthesized)
	// EmitFuncDef emits "<sig> {", body lines, then "}", so the return-form
	// trailer goes in before the closing brace.
	var b strings.Builder
	for i, line := range lines {
		if i == len(lines)-1 {
			b.WriteString(trailer)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	return b.String(), compFields, startLabel, startContainer
}

func emitIRMain(b *strings.Builder, cfg Config, info *irAnalysis, pkg *ir.Package) {
	b.WriteString("func main() {\n")
	b.WriteString("\ta := app.New()\n")
	fmt.Fprintf(b, "\tw := a.NewWindow(%q)\n", cfg.AppName)
	b.WriteString("\tm := New()\n")
	// fyne 2.7 lays out SetContent's tree against the *current* window
	// size, so set content before resizing — otherwise the canvas
	// reports its default 0×0 dimensions at first paint and the user
	// sees a blank window until they manually resize.
	b.WriteString("\tw.SetContent(m.BuildUI())\n")
	if pkg != nil && pkg.RemoteSettle != nil {
		// After BuildUI, since the updaters write to widgets it creates, and
		// through DoAndWait because a settle arrives on the fetch's goroutine
		// and Fyne's widgets belong to the main one.
		fmt.Fprintf(b, "\tremote.Default.OnSettle(func() { fyne.DoAndWait(m.%s) })\n", pkg.RemoteSettle.Name)
	}
	b.WriteString("\tw.Resize(fyne.NewSize(480, 640))\n")
	b.WriteString("\tw.ShowAndRun()\n")
	if pkg != nil && pkg.Teardown != nil {
		// ShowAndRun returns when the window closes, which is the one exit
		// this can be reached from: a killed process runs nothing here, and an
		// effect's teardown is written knowing that.
		fmt.Fprintf(b, "\tm.%s()\n", pkg.Teardown.Name)
	}
	b.WriteString("\t_ = os.Stderr\n")
	b.WriteString("}\n")
}

// elementRef builds an ir.Ident for a widget field name. Its
// IsElementRef+Synthesized flags route through evalIdent's m.<name>
// qualification, so callers do not hand-emit the "m." prefix.
func elementRef(name string) *ir.Ident {
	return &ir.Ident{Name: name, IsElementRef: true, Synthesized: true}
}

// topRef renders a return-trailer reference to a top-level widget id: a
// bare local for non-escaping ids (declared as `__nN := ...` in the same
// scope), else the m.<name> element ref.
func topRef(tr *fyneTranslator, name string) ir.Expr {
	if tr.isLocalRef(name) {
		return localElementRef(name)
	}
	return elementRef(name)
}

// mainScopeLocalRefs returns the non-escaping widget-ref set passNodeEscape
// recorded for the scope the BuildUI emission walks: a harness-isolated root
// component's body, and nothing for a window's.
//
// Nil for the entry scope, and deliberately: locals are for a *recursive*
// render method, where a frame must not clobber the temp of the frame that
// called it. BuildUI has no frames, and everything emitted beside it may name
// a ref it created -- only some of those sites go through a qualifier that
// knows about locals.
func mainScopeLocalRefs(ctx *codegen.CodegenCtx) map[string]bool {
	if wins := ctx.Windows(); len(wins) > 0 && len(wins[0].Body) > 0 {
		return nil
	}
	if main := ctx.RootDecl(); main != nil {
		return main.LocalRefs
	}
	return nil
}

// extractEventBindTarget returns the variable the synthesized two-way bind
// writes, or "" when stmts does not begin with one.
//
// passPropBindings opens a bound node's handler with `<var> = <event>.<field>`.
// A Fyne callback receives the unwrapped value rather than the SNGL event
// struct, so the emitter replaces that statement with a read of the callback's
// own parameter — which means it has to be certain it is looking at that
// statement and not at the first line the user wrote.
//
// Recognising "an assignment" was not certain enough: a handler whose body
// merely began with one had that line replaced and silently dropped. The bind
// is identified by its whole shape instead — a plain assignment to a name,
// reading a field off one of the handler's own parameters.
func extractEventBindTarget(stmts []ir.Stmt, params []*ir.Param) string {
	if len(stmts) == 0 {
		return ""
	}
	assign, ok := stmts[0].(*ir.Assign)
	if !ok || assign.Op != ast.AssignSet {
		return ""
	}
	target, ok := assign.Target.(*ir.Ident)
	if !ok {
		return ""
	}
	sel, ok := assign.Value.(*ir.Select)
	if !ok || !isParamRef(sel.Operand, params) {
		return ""
	}
	return target.Name
}

// isParamRef reports whether e reads one of params. Sym is the reliable answer;
// the name is the fallback for an ident a pass rebuilt without re-resolving it.
func isParamRef(e ir.Expr, params []*ir.Param) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	for _, p := range params {
		if p == nil {
			continue
		}
		if id.Sym == ir.Symbol(p) || (id.Sym == nil && id.Name == p.Name) {
			return true
		}
	}
	return false
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

// windowPascal converts a window Name to a PascalCase identifier fragment.
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

// promotedHandlersInNonMainComponents returns the node-attached event handlers
// that lowering promoted to Funcs on a component other than the app root.
//
// ctx.AllFuncs() deliberately covers only pkg-level and main-component funcs,
// and a window's promoted handlers are collected from its own Funcs slice. A
// component that is neither has no other route into the emit loop, so its
// handlers would be attached (`w.OnTapped = m.h`) with no method of that name
// emitted. Only promoted handlers are added: a nested component *method* is
// registered in pkg.Funcs as well and is already emitted from there.
func promotedHandlersInNonMainComponents(ctx *codegen.CodegenCtx, have []*ir.Func) []*ir.Func {
	seen := make(map[*ir.Func]bool, len(have))
	for _, fn := range have {
		seen[fn] = true
	}
	var out []*ir.Func
	for _, cc := range ctx.NonRootComponents() {
		for _, fn := range cc.Component.Funcs {
			if fn == nil || fn.LoweredFromTag == "" || seen[fn] {
				continue
			}
			seen[fn] = true
			out = append(out, fn)
		}
	}
	return out
}

// collectNodes walks pkg and every Func looking for
// `LocalVar __nX = lower.CreateNode("tag")` and returns node-id → the widget
// Spec that instantiation carried.
//
// The map is keyed by node id, not by tag, because a tag now names one of the
// three primitives rather than a widget: every Label in the program lowers to
// `CreateNode("Widget")`, and what makes one a Label is the Spec record its
// own `spec` prop holds.
//
// It is built package-wide because a promoted node-attached handler, and the
// reactivity splices inside it, reference nodes created in a sibling Func.
//
// A node whose component is not a fyne primitive is absent, and the translator
// emits nothing for it. One that *is* a primitive but carries no decodable
// Spec is an error rather than an absence: skipping it would drop the widget
// and leave the AppendChild naming it behind — issue #120's shape, a
// successful build emitting Go that does not compile.
func collectNodes(pkg *ir.Package, funcs []*ir.Func) (map[string]*fyneSpec, error) {
	specs := map[string]*fyneSpec{}
	var firstErr error
	// A child is appended after every node in the tree has been created, so
	// the edges are recorded here and read back once the walk is done -- a
	// container's layout is decided by its children's flex, which is not known
	// when the container itself is reached.
	kids := map[string][]string{}
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for i, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if call, ok := n.Init.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "CreateNode" && len(call.Args) >= 1 {
					if lit, ok := call.Args[0].Value.(*ir.Literal); ok && lit.Type == ir.TypString {
						if err := harvestSpec(specs, n, lit.Value, stmts[i+1:]); err != nil && firstErr == nil {
							firstErr = err
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
			case *ir.CallStmt:
				if parent, child, ok := appendChildEdge(n); ok {
					kids[parent] = append(kids[parent], child)
				}
			case *ir.SlotInst, *ir.Assign, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
				*ir.Break, *ir.Continue:
				// No CreateNode call to harvest.
			default:
				panic(fmt.Sprintf("fyne.collectNodes: unhandled ir.Stmt %T", n))
			}
		}
	}
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		walk(fn.Block)
	}
	if pkg != nil {
		for _, comp := range pkg.Components {
			walk(comp.Body)
			// And its funcs. `funcs` above is what the Model emits, which is
			// not the same set: a component the build could not inline keeps
			// its slot renderer on itself, and the widgets that renderer
			// creates had no spec, so OnCreateNode emitted nothing and the
			// renderer referenced a variable no statement declared.
			for _, fn := range comp.Funcs {
				if fn != nil {
					walk(fn.Block)
				}
			}
		}
		for _, w := range ir.AllWindows(pkg) {
			walk(w.Children)
		}
	}
	for id, sp := range specs {
		sp.Children = kids[id]
	}
	return specs, firstErr
}

// appendChildEdge reads the parent and child of a `lower.AppendChild(p, c)`
// statement, when that is what this is.
func appendChildEdge(cs *ir.CallStmt) (parent, child string, ok bool) {
	call := cs.Call
	if call == nil || call.Func == nil || call.Func.Intrinsic != ir.NodeOpAppendChild || len(call.Args) < 2 {
		return "", "", false
	}
	parent = codegen.IdentBareName(call.Args[0].Value)
	child = codegen.IdentBareName(call.Args[1].Value)
	if parent == "" || child == "" {
		return "", "", false
	}
	return parent, child, true
}

// harvestSpec decodes the Spec for one created node. rest is what follows the
// CreateNode in the same statement list: lowering emits the node's prop
// assignments there and nowhere else, so it is both where the `spec` record is
// found and the only place a constructor-argument prop's expression is known
// to be in scope.
func harvestSpec(specs map[string]*fyneSpec, lv *ir.LocalVar, tag string, rest []ir.Stmt) error {
	var comp *ir.Component
	if lv.Type != nil && lv.Type.Kind == ir.TypeComponent {
		comp, _ = lv.Type.Decl.(*ir.Component)
	}
	if comp == nil || fynePrimitive(comp) == "" {
		if lv.CanvasNode == nil && codegen.DeclinesNode(comp) {
			return codegen.UnimplementedNode(comp, lv.NodeAST, tag, "fyne")
		}
		return nil
	}
	props := map[string]ir.Expr{}
	for _, s := range rest {
		asn, ok := s.(*ir.Assign)
		if !ok {
			break
		}
		sel, ok := asn.Target.(*ir.Select)
		if !ok {
			break
		}
		id, ok := sel.Operand.(*ir.Ident)
		if !ok || !id.IsElementRef || id.Name != lv.Name {
			break
		}
		props[sel.Field] = asn.Value
	}
	sp, err := specFromProps(tag, props)
	if err != nil {
		return err
	}
	specs[lv.Name] = sp
	return nil
}

// emitIRPromotedHandler emits a node-attached event handler that the
// lower pass promoted to a top-level Func. The Handler record for
// LoweredFromEvent on the node's Spec dictates the Go signature (e.g.
// fyne's Entry.OnChanged is `func(s string)`, not `func(e InputEvent)`).
// The first stmt of the handler body — the user's `var = e.<field>`
// two-way bind — is rewritten to `m.<var> = <param>`; subsequent
// stmts (reactive splices injected by passReactivity) flow through
// the same WalkLowered + translator pipeline as slot bodies so they
// pick up widget-setter rewrites via OnPropAssign.
//
// The node is found by LoweredFromNode rather than LoweredFromTag: a tag
// names one of three primitives, so it says which children contract the
// widget has and nothing about its callbacks.
func emitIRPromotedHandler(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField, nodeSpecs map[string]*fyneSpec, importSink func(string), canvasByNode map[*ir.NodeInst]*canvasMeta, failSink func(error)) {
	var binding fyneHandler
	if sp, ok := nodeSpecs[fn.LoweredFromNode]; ok {
		binding = sp.Handlers[fn.LoweredFromEvent]
	}

	// One parse, shared with the Spec decode that validated it -- two
	// readings of one signature is how a parameter got dropped from a
	// signature the decoder had just accepted.
	params := fn.Params
	if binding.Signature != "" {
		parsed, err := signatureParams("", fn.LoweredFromEvent, binding.Signature)
		if err != nil {
			// specFromProps rejected this already; reaching here means the two
			// disagree, which is a bug in this package rather than in the Spec.
			panic("fyne: " + err.Error())
		}
		params = parsed
	}

	tr := newFyneTranslator(gc, nodeSpecs, func(name, goType string) {
		*widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
	}, importSink, failSink).withLocalRefs(fn.LocalRefs)
	tr.canvasByNode = canvasByNode

	stmts := fn.Block

	var prelude []ir.Stmt
	if binding.Param != "" {
		// The synthesized `var = e.<field>` two-way bind, when the handler
		// opens with one, is re-emitted as a direct `m.<var> = <param>` since
		// the closure exposes the unwrapped fyne value. fn.Params is the SNGL
		// signature the bind was written against, not the Fyne one that
		// replaced it above.
		bindVar := extractEventBindTarget(stmts, fn.Params)
		if bindVar != "" {
			prelude = []ir.Stmt{&ir.Assign{
				Target: &ir.Ident{Name: bindVar},
				Op:     ast.AssignSet,
				Value:  &ir.Ident{Name: binding.Param},
			}}
			stmts = stmts[1:]
		}
		// Any other read of the payload's value is the callback's argument
		// too: the closure is handed the text, never an event struct.
		if len(fn.Params) > 0 {
			codegen.ReadEventField(stmts, fn.Params[0], "value", &ir.Ident{Name: binding.Param, Type: ir.TypString})
		}
	}

	body := codegen.WalkLowered(context.Background(), stmts, tr)
	if binding.Signature != "" {
		body = substitutePayload(body, fn.Params, binding.Param, params)
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

// emitIRSlotFunc emits a lowering-synthesized Func as a Model method. The
// body is a mix of plain Go statements (For teardown, Assign reset, If gate)
// and lower.* intrinsic calls. codegen.WalkLowered routes intrinsic shapes
// through fyneTranslator into ir.Stmt fragments; we then feed them through
// gc.EvalStmt at the source-emission boundary.
//
// Only a __renderSlot<N> takes the host container: its body was written
// against passReactivity's `parent`, which the translator rewrites to
// slotParentParam. Every other synthesized func -- an effect's settle halves, the
// focus-order navigation -- takes the parameters it declares, which is none.
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]irWidgetField, specs map[string]*fyneSpec, importSink func(string), canvasByNode map[*ir.NodeInst]*canvasMeta, failSink func(error)) {
	tr := newFyneTranslator(gc, specs, func(name, goType string) {
		*widgetFields = append(*widgetFields, irWidgetField{name: name, goType: goType})
	}, importSink, failSink).withLocalRefs(fn.LocalRefs)
	tr.canvasByNode = canvasByNode

	bodyStmts := codegen.WalkLowered(context.Background(), fn.Block, tr)

	params := fn.Params
	if fn.SlotRender {
		params = []*ir.Param{{Name: slotParentParam, Type: ir.NativeGoPointerOf("fyne.Container")}}
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

// assignedAliases is the alias every import must arrive under: whatever the
// translation context assigned as it registered them. Read from the context
// rather than from the Specs because the context is what de-conflicts two
// packages wanting one name, and because its import order is the walk order
// -- so the result is the same on every run of a build.
func assignedAliases(gc *golang.GoIRContext) map[string]string {
	out := map[string]string{}
	for _, p := range gc.Imports() {
		if a := gc.ForcedAlias(p); a != "" {
			out[p] = a
		}
	}
	return out
}
