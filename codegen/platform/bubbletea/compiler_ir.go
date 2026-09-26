package bubbletea

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Config controls code generation. Field names mirror bubbletea.sngl options.
type Config struct {
	Package     string // Go package name (default: "ui")
	ScaleFactor int    // pixels per terminal cell (default: 8); not source-exposed
	Main        bool   // emit a main() function for standalone apps

	// Stdlib globals (lib/options.sngl).
	Name        string
	Icon        string
	Description string
	Version     string

	// Lang globals (codegen/lang/golang/golang.sngl).
	GoVersion  string // Go toolchain version emitted in `sngl run` go.mod (default: "1.23")
	GoModExtra string // Extra text appended to temp test-module go.mod (e.g. replace directive)

	// Internal (set by CLI, not exposed in .sngl).
	Lang string `option:"lang"` // language identifier used to select the executor
}

func (c Config) withDefaults() Config {
	if c.Package == "" {
		if c.Main {
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

// widgetInfo describes one inlined Widget blueprint primitive. Everything —
// field type, constructor, view/update methods, init cmd — comes from the
// node's `Model` record, never from the stdlib component name.
type widgetInfo struct {
	fieldName   string
	model       modelMeta
	binds       []widgetBind
	placeholder string // Go-quoted-ready raw string; "" = no placeholder setter
	focusExpr   string // Go expr that is true when this widget is focused; "" = no tracking
}

// widgetBind pairs a two-way bound model field with the user var it syncs to.
// target is the Model field name the user's bind handler assigns to (e.g. "name");
// get is the read-back getter (see emitBindReadBack); set is the write-back
// method appended to the field (e.g. ".SetValue"), or "" for a read-only bind
// (list-backed widgets, whose items come from a data prop not the bind target).
type widgetBind struct {
	target string
	get    string
	set    string
}

// bindReadBack builds the Go expression reading a widget's value out of model
// field `field`:
//
//	get = ".Value()"           → m.<field>.Value()
//	get = "tui.SelectedString" → tui.SelectedString(m.<field>)
//
// A "tui." get is the converter wrap, which centralizes a nil guard: a
// list-backed widget's SelectedItem() is nil on an empty list, so the bare
// method chain would panic.
func bindReadBack(field, get string) string {
	if strings.HasPrefix(get, "tui.") {
		return get + "(m." + field + ")"
	}
	return "m." + field + get
}

// widgetSyncGoTypes is the set of Go types a bubbles widget value may
// round-trip through; a bind whose target has any other type is not synced,
// because wiring it would emit type-mismatched Go. An int bind is read-only in
// practice: every push site is additionally gated on `set != ""`.
var widgetSyncGoTypes = map[string]struct{}{
	"string": {},
	"int":    {},
}

// bindTargetSyncs keeps the constructor seed, the Set* setter and the Update
// reverse-sync consistent, so an unsupported bind type never emits
// type-mismatched Go.
func bindTargetSyncs(binds []irBind, target string) bool {
	if target == "" {
		return false
	}
	for _, b := range binds {
		if b.name == target {
			_, ok := widgetSyncGoTypes[b.goType]
			return ok
		}
	}
	return false
}

func CompileIR(ctx *codegen.CodegenCtx, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	info := analyzeIR(ctx)
	body, imports := emitIR(info, ctx, cfg)
	body = golang.PruneStructDecls(body, golang.PayloadPruneCandidates(ctx.Pkg, codegen.TriggerPayloads(ctx.Pkg)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// The MemSink keeps this returning bytes, which android's go path and the
	// tests consume; the caller's emitter adds the generated-by header, so
	// Source is left empty.
	mem := codegen.NewMemSink()
	e := (&golang.Translator{}).NewFileEmitter(mem, codegen.FileOptions{
		Name:        "model.go",
		PackageName: cfg.Package,
	})
	// pkg/go/canvas default-aliases to "canvas", but the draw-func selectors
	// reference it as snglcanvas.
	type aliasImporter interface {
		RequireImportAs(path, alias string) string
	}
	for _, p := range imports {
		if p == snglCanvasImportPath {
			if ai, ok := e.(aliasImporter); ok {
				ai.RequireImportAs(p, snglCanvasAlias)
				continue
			}
		}
		e.RequireImport(p)
	}
	if _, err := e.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	out := mem.Files()["model.go"]
	if out == nil {
		return nil, fmt.Errorf("bubbletea: emitter produced no model.go")
	}
	return out, nil
}

type irAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irBind
	externs   []irExtern
	computeds []irComputed
	widgets   []widgetInfo
	overlays  []overlayInfo
	hasFocus  bool // true when __focusOrder pass injected __focusID/__focusNext/__focusPrev
	gc        *golang.GoIRContext
	// loopTimers are the schedules written under a loop (loop_timers.go).
	loopTimers []codegen.LoopTimer
}

// overlayInfo records one modal/drawer Overlay primitive for Update()'s
// focus-capture logic. closeVar is recovered only when the gate condition is a
// simple assignable var ident, and is "" for a compound one.
type overlayInfo struct {
	openExpr string
	closeVar string
	// closeCode closes an overlay a loop renders: the statement that clears
	// the gate of whichever copy is open.
	closeCode string
}

type irBind struct {
	name        string
	goType      string
	init        string // Go expression
	isConst     bool
	synthesized bool // pass-generated (e.g. NoContext hidden ctx Var); no getter/setter
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
	// these renders, and before ScopedExprCtx clones it. Clones inherit it:
	// ForComponent and WithLocal copy the map reference along.
	ctx.ExprCtx.FreeFuncs = golang.ModelFreeFuncs(ctx.Pkg)
	ctx.ExprCtx.ModelParamFuncs = golang.ModelParamFuncs(ctx.Pkg)
	exprCtx := ctx.ScopedExprCtx()
	gc := golang.NewIRContext(exprCtx)
	// This platform's hand-written receivers are `func (m Model)` -- Update
	// mutates the copy and hands it back -- so passing the Model to something
	// that takes a *Model needs its address here. Every body EmitFuncDef
	// writes clears it again, those being pointer receivers.
	gc.ModelIsValue = true
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		gc:             gc,
	}
	pkg := ctx.Pkg

	for _, path := range golang.BaseImports(pkg) {
		gc.RequireImport(path)
	}

	// NoInlineComponents inlined every non-main component into main, so there
	// are no remaining child-component vars to collect.
	for _, ov := range ctx.ModelState() {
		// A const is a Model field as well, so `c.<name>` and `m.<name>`
		// reach it; a top-level one also gets a file-scope `var` for the
		// free functions, which are not Model methods.
		goType := golang.BindGoType(ov.Type(), ov.Init())
		initVal := golang.LowerBindInit(ov.Type(), ov.Init(), gc)
		if strings.HasPrefix(goType, "time.") {
			gc.RequireImport("time")
		}
		info.binds = append(info.binds, irBind{
			name:        ov.Name(),
			goType:      goType,
			init:        initVal,
			isConst:     ov.IsConst(),
			synthesized: ov.Synthesized(),
		})
	}

	// AllFuncs is what dedupes the nested component methods registered in both
	// pkg.Funcs and main.Funcs, and it is also the only list that includes a
	// window's own funcs -- the __focusNext/__focusPrev passFocusOrder puts
	// there were called from Update and declared nowhere.
	allFuncs := ctx.AllFuncs()
	seenFn := make(map[*ir.Func]bool, len(allFuncs))
	for _, f := range allFuncs {
		if seenFn[f] {
			continue
		}
		seenFn[f] = true
		if codegen.IsComputed(f) {
			goType := golang.FuncReturnGoType(f)
			info.computeds = append(info.computeds, irComputed{
				name:   f.Name,
				goType: goType,
				fn:     f,
			})
		}
	}

	for _, t := range info.Timers {
		if t.IntervalMs > 0 {
			gc.RequireImport("time")
		}
	}
	for _, o := range ir.Owners(pkg) {
		info.loopTimers = append(info.loopTimers, codegen.CollectLoopTimers(o.Stmts())...)
	}
	if len(info.loopTimers) > 0 {
		gc.RequireImport("time")
	}

	// An empty __focused prop (passFocusOrder did not run) means focus
	// tracking is not active.
	wins := ctx.Windows()
	for _, win := range wins {
		codegen.WalkVisualTree(win.Body, func(n *ir.NodeInst, _ int) bool {
			if btIntrinsic(n) != "Widget" {
				return false
			}
			bp := extractBlueprint(n)
			if bp.Kind != bpWidget {
				return false
			}
			fieldName := ctx.Namer.Next("widget")
			placeholder := ""
			if s, ok := codegen.IRLiteralString(bp.Placeholder); ok {
				placeholder = s
			}
			// A `:value` bind lowers to a handler named after the prop, and an
			// explicit @input/@change handler carries the same write; take the
			// first whose body assigns to a var, prop-named handler first.
			var binds []widgetBind
			for _, bm := range bp.Binds {
				target := ""
				for _, hname := range []string{bm.Prop, "input", "change", "select"} {
					if h := codegen.NodeHandler(n, hname); h != nil && h.Func != nil {
						if t := extractIRAssignTarget(h.Func.Block); t != "" {
							target = t
							break
						}
					}
				}
				binds = append(binds, widgetBind{target: target, get: bm.Get, set: bm.Set})
			}
			if bp.Model.Pkg != "" {
				gc.RequireImport(bp.Model.Pkg)
			}
			// A no-token string passes through unchanged, so its output stays
			// byte-identical.
			model := bp.Model
			model.New = expandWidgetTemplate(gc, model.New, n, fieldName)
			model.View = expandWidgetTemplate(gc, model.View, n, fieldName)
			model.Update = expandWidgetTemplate(gc, model.Update, n, fieldName)
			model.Init = expandWidgetTemplate(gc, model.Init, n, fieldName)
			model.Resize = expandWidgetTemplate(gc, model.Resize, n, fieldName)
			// A model string may name a pkg/go/tui helper inline, without the
			// `|conv` token whose path would have required the import.
			for _, s := range []string{model.New, model.View, model.Update, model.Init, model.Resize} {
				if strings.Contains(s, "tui.") {
					gc.RequireImport(tuiImportPath)
					break
				}
			}
			info.widgets = append(info.widgets, widgetInfo{
				fieldName:   fieldName,
				model:       model,
				binds:       binds,
				placeholder: placeholder,
				focusExpr:   nodeStaticFocusExpr(n, gc),
			})
			return false
		})
	}

	for _, win := range wins {
		collectOverlays(win.Body, nil, gc, &info.overlays)
	}

	for _, b := range info.binds {
		if b.name == "__focusID" {
			info.hasFocus = true
			break
		}
	}

	if info.NeedsToast {
		gc.RequireImport("time")
	}

	return info
}

// emitIR renders the model file body — no package clause or import block —
// and the Go import paths it uses. Imports are recorded at their emit sites,
// some structurally here and the rest accumulated on gc and info.gc; the
// caller feeds them to a FileEmitter.
func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config) (string, []string) {
	var b strings.Builder
	gc := info.gc

	// Every model's Init/Update/View shells reference tea. "lipgloss" and "fmt"
	// are not structural — a view rendering only through a bubbles Widget emits
	// neither — so they are required at the end, gated on the rendered body.
	gc.RequireImport("charm.land/bubbletea/v2")
	if cfg.Main {
		gc.RequireImport("os")
		gc.RequireImport("fmt") // main() prints errors via fmt.Fprintf
	}
	helpers := golang.HelpersNeeded(ctx.Pkg)
	for _, imp := range helpers.Imports() {
		gc.RequireImport(imp)
	}
	b.WriteString(helpers.Emit())
	b.WriteString(golang.EmitMergeFuncs(ctx.Pkg.MergeStructs))

	b.WriteString(golang.EmitUnitTypeDecls(info.Units))

	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "type %s struct {\n", golang.ExportName(sd.Name))
		for _, f := range sd.Fields {
			goType := golang.IRTypeToGo(f.Type)
			fmt.Fprintf(&b, "\t%s %s\n", golang.ExportName(f.Name), goType)
		}
		b.WriteString("}\n\n")
	}

	// The canvas stdlib structs are read by the synthesized draw funcs but are
	// not carried on pkg.Structs for the Go path. Skip a name a user struct
	// already declares.
	if len(ctx.Canvases.All()) > 0 {
		b.WriteString(canvasStdlibDecls(info.Structs))
	}

	if ctx.Pkg.UsesErrorHandling {
		b.WriteString(golang.ErrorEventDecl)
	}

	// A top-level const gets a file-scope Go `var` so the free functions, which
	// are not Model methods, can name it. A component-level const lives only as
	// a Model field.
	if len(ctx.Pkg.Consts) > 0 {
		for _, c := range ctx.Pkg.Consts {
			init := golang.LowerVarInit(c, gc)
			goType := golang.IRTypeToGo(c.Type)
			fmt.Fprintf(&b, "var %s %s = %s\n", c.Name, goType, init)
		}
		b.WriteString("\n")
	}

	for _, t := range info.Timers {
		fmt.Fprintf(&b, "type timerTickMsg%d struct{}\n", t.Index)
	}
	if len(info.Timers) > 0 {
		b.WriteString("\n")
	}
	emitLoopTimerTypes(&b, info.loopTimers)

	if info.NeedsToast {
		b.WriteString("type snglToast struct {\n\tmessage string\n\tvariant string\n}\n\n")
		b.WriteString("type toastDismissMsg struct{}\n\n")
	}

	b.WriteString("// Model is the Bubble Tea model for this SNGL UI.\n")
	b.WriteString("type Model struct {\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t%s %s\n", bind.name, bind.goType)
	}
	for _, ext := range info.externs {
		fmt.Fprintf(&b, "\t%s %s // extern\n", golang.ExportName(ext.name), ext.goType)
	}
	if len(info.binds) > 0 {
		b.WriteString("\n")
	}
	for _, w := range info.widgets {
		fmt.Fprintf(&b, "\t%s %s\n", w.fieldName, w.model.Type)
	}
	if len(info.widgets) > 0 {
		b.WriteString("\n")
	}
	emitLoopTimerFields(&b, info.loopTimers)
	if info.NeedsToast {
		b.WriteString("\ttoasts []snglToast\n")
	}
	b.WriteString("\twidth, height int\n")
	b.WriteString("}\n\n")

	// Binds are initialized sequentially so a later init can read an earlier
	// field as `m.<name>`; a struct-literal init leaves `m` undefined.
	b.WriteString("// New creates a Model with default bind values.\n")
	b.WriteString("func New() Model {\n")
	b.WriteString("\tm := Model{}\n")
	emitLoopTimerInits(&b, info.loopTimers)
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\tm.%s = %s\n", bind.name, bind.init)
	}
	firstFocusable := true
	for _, w := range info.widgets {
		fmt.Fprintf(&b, "\tm.%s = %s\n", w.fieldName, w.model.New)
		if w.placeholder != "" {
			fmt.Fprintf(&b, "\tm.%s.Placeholder = %q\n", w.fieldName, w.placeholder)
		}
		for _, bd := range w.binds {
			if bindTargetSyncs(info.binds, bd.target) && bd.set != "" {
				fmt.Fprintf(&b, "\tm.%s%s(m.%s)\n", w.fieldName, bd.set, bd.target)
			}
		}
		if firstFocusable && w.focusExpr != "" {
			// This widget holds the first focusable slot, where __focusID
			// starts. Mark the slot consumed even without a .Focus() method,
			// or a later widget that has one is wrongly focused.
			if modelHasFocusMethods(w.model.Type) {
				fmt.Fprintf(&b, "\tm.%s.Focus()\n", w.fieldName)
			}
			firstFocusable = false
		}
	}
	// m.width/m.height are still zero here, so resizeWidgets falls back to its
	// defaults; emitting it primes any widget whose default size is 0.
	if widgetsHaveResize(info.widgets) {
		b.WriteString("\tm.resizeWidgets()\n")
	}
	// Last, so every cell an `on` expression or a mount handler touches already
	// holds its initial value. Init() cannot do this -- it takes the model by
	// value and returns only a tea.Cmd, so a mutation there is discarded -- and
	// the constructor is the one place every driver goes through.
	for _, fn := range modelMountFuncs(ctx) {
		fmt.Fprintf(&b, "\tm.%s()\n", fn.Name)
	}
	b.WriteString("\treturn m\n")
	b.WriteString("}\n\n")

	// SetTerminalSize also resizes the bubbles widgets: the snapshot harness
	// sends no WindowSizeMsg, so this is the only sizing on that path.
	b.WriteString("// SetTerminalSize sets the terminal dimensions and resizes widgets.\n")
	b.WriteString("func (m *Model) SetTerminalSize(w, h int) {\n")
	b.WriteString("\tm.width = w\n")
	b.WriteString("\tm.height = h\n")
	if widgetsHaveResize(info.widgets) {
		b.WriteString("\tm.resizeWidgets()\n")
	}
	b.WriteString("}\n\n")

	// resizeWidgets is called from New(), SetTerminalSize() and the
	// tea.WindowSizeMsg case, so widgets stay sized on every path.
	if widgetsHaveResize(info.widgets) {
		b.WriteString("// resizeWidgets sizes each bubbles widget to the current terminal extent.\n")
		b.WriteString("func (m *Model) resizeWidgets() {\n")
		for _, w := range info.widgets {
			if w.model.Resize == "" {
				continue
			}
			fmt.Fprintf(&b, "\tm.%s%s\n", w.fieldName, w.model.Resize)
		}
		b.WriteString("}\n\n")
	}

	for _, comp := range info.computeds {
		fmt.Fprintf(&b, "func (m Model) %s() %s {\n", comp.name, comp.goType)
		emitted := false
		if comp.fn != nil && len(comp.fn.Block) == 1 {
			if ret, ok := comp.fn.Block[0].(*ir.Return); ok && ret.Value != nil {
				fmt.Fprintf(&b, "\treturn %s\n", gc.EvalExpr(ret.Value))
				emitted = true
			}
		}
		if !emitted {
			if comp.fn != nil {
				for _, stmt := range comp.fn.Block {
					for _, line := range gc.EvalStmt(stmt) {
						fmt.Fprintf(&b, "\t%s\n", line)
					}
				}
			} else {
				b.WriteString("\treturn \"\"\n")
			}
		}
		b.WriteString("}\n\n")
	}

	// Routed through the shared canvas Context translator. They are in no
	// func list, so the generic user-func loop below never meets them and
	// needs no exclusion -- which is what the name match on `_canvasDraw`
	// used to be for.
	inLoop := loopCanvases(ctx)
	emitCanvasSurfaceDecls(&b, ctx.Canvases, inLoop)
	emitCanvasDrawFuncs(&b, ctx.Canvases, inLoop, gc)
	hasCanvas := len(ctx.Canvases.All()) > 0
	if hasCanvas {
		emitCanvasTransmitMethod(&b, ctx, inLoop, gc)
	}

	// Every component this build renders, not only the root: one that
	// survived inlining is emitted from its own declaration, and the funcs
	// its body calls have to come with it. AllFuncs is the deduped base --
	// the loop below dedupes what this adds on top.
	allFuncs := ctx.AllFuncs()
	for _, comp := range ctx.Pkg.Components {
		allFuncs = append(allFuncs, comp.Funcs...)
	}
	componentFuncs := componentFuncSet(ctx.Pkg)
	stateFuncs := golang.ModelStateFuncs(ctx.Pkg)
	seenUserFn := make(map[*ir.Func]bool, len(allFuncs))
	for _, fn := range allFuncs {
		if seenUserFn[fn] {
			continue
		}
		seenUserFn[fn] = true
		if fn.IsTest || codegen.IsComputed(fn) {
			continue
		}
		// golang.LiftsToFreeFunc is the one answer the call site uses too.
		if fn.Receiver != "" && golang.LiftsToFreeFunc(ctx.Pkg, fn.Receiver) {
			emitIRTypeMethod(&b, fn, gc)
			continue
		}
		// stateFuncs is the set ModelFreeFuncs kept from the call sites; see
		// its doc for what a package var costs a free function.
		//
		// A func still carrying a receiver is never one of these, whatever
		// the two sets say. Past LiftsToFreeFunc above the receiver names a
		// component, so the func is a method of the Model the component was
		// inlined into -- which is what the call site spells. componentFuncs
		// misses the inliner's clone, because the clone lives in pkg.Funcs
		// rather than on any component, and a clone that touched no state was
		// not in stateFuncs either: `func (m *main) Paint__inst0` came out
		// beside the `m.paint__inst0(…)` calling it. fyne and gtk4 ask the
		// same question here and always did. A synthesized func is not in
		// ModelFreeFuncs either, so its call sites spell it on m.
		if fn.Receiver == "" && !fn.Synthesized && !componentFuncs[fn] && !stateFuncs[fn] {
			emitIRFreeFunc(&b, fn, gc)
			continue
		}
		emitIRFunc(&b, fn, gc)
	}

	emitIRGettersSetters(&b, info, ctx, gc)

	var widgetInits []string
	seenInit := map[string]bool{}
	for _, w := range info.widgets {
		if w.model.Init != "" && !seenInit[w.model.Init] {
			seenInit[w.model.Init] = true
			widgetInits = append(widgetInits, w.model.Init)
		}
	}

	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.Timers) > 0 || len(info.loopTimers) > 0 {
		b.WriteString("\tvar cmds []tea.Cmd\n")
		for _, wi := range widgetInits {
			fmt.Fprintf(&b, "\tcmds = append(cmds, %s)\n", wi)
		}
		for _, t := range info.Timers {
			tick := fmt.Sprintf("cmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))", t.IntervalMs, t.Index)
			if t.ActiveVar != "" {
				fmt.Fprintf(&b, "\tif m.%s {\n", t.ActiveVar)
				fmt.Fprintf(&b, "\t\t%s\n", tick)
				b.WriteString("\t}\n")
			} else {
				// An empty ActiveVar would emit "if m. {", which is invalid Go.
				fmt.Fprintf(&b, "\t%s\n", tick)
			}
		}
		emitLoopTimerSyncCalls(&b, info.loopTimers, "\t")
		if hasCanvas {
			// Out of band, so the kitty image data is not dropped by the cell
			// compositor.
			fmt.Fprintf(&b, "\tcmds = append(cmds, m.%s())\n", canvasTransmitMethodName)
		}
		b.WriteString("\treturn tea.Batch(cmds...)\n")
	} else if hasCanvas {
		base := "nil"
		if len(widgetInits) > 0 {
			base = strings.Join(widgetInits, ", ")
		}
		fmt.Fprintf(&b, "\treturn tea.Batch(%s, m.%s())\n", base, canvasTransmitMethodName)
	} else if len(widgetInits) == 1 {
		fmt.Fprintf(&b, "\treturn %s\n", widgetInits[0])
	} else if len(widgetInits) > 1 {
		fmt.Fprintf(&b, "\treturn tea.Batch(%s)\n", strings.Join(widgetInits, ", "))
	} else {
		b.WriteString("\treturn nil\n")
	}
	b.WriteString("}\n\n")

	var eventInvokers []btEventInvoker
	emitIRUpdate(&b, info, ctx, gc, cfg, func(n *ir.NodeInst, event string, slotIdx int, keyExpr string) {
		// A synthesized id is not something a test can write.
		if n.ID == "" || strings.HasPrefix(n.ID, "__n") {
			return
		}
		eventInvokers = append(eventInvokers, btEventInvoker{
			IDLabel:   n.ID,
			SnglEvent: event,
			SlotIdx:   slotIdx,
			KeyExpr:   keyExpr,
		})
	})
	emitBtEventInvokers(&b, eventInvokers, codegen.TriggerPayloads(ctx.Pkg))
	emitLoopTimerSyncs(&b, info.loopTimers, gc)

	emitIRView(&b, info, ctx, gc, cfg)

	for _, cc := range ctx.NonRootComponents() {
		emitIRComponentMethod(&b, cc, ctx, gc, cfg)
	}

	if ctx.Pkg != nil && ctx.Pkg.UsesRemote {
		b.WriteString("// remoteSettledMsg wakes the program when a fetch answers.\n")
		b.WriteString("type remoteSettledMsg struct{}\n\n")
	}

	if cfg.Main {
		b.WriteString("func main() {\n")
		b.WriteString("\tp := tea.NewProgram(New())\n")
		if ctx.Pkg != nil && ctx.Pkg.UsesRemote {
			// After NewProgram, since the callback holds the program. Send is
			// safe before Run: it blocks until the program is listening.
			b.WriteString("\tremote.Default.OnSettle(func() { p.Send(remoteSettledMsg{}) })\n")
			gc.RequireImport("git.duckfam.us/jonathan/sngl/pkg/go/remote")
		}
		teardown := ctx.Pkg != nil && ctx.Pkg.Teardown != nil
		if teardown {
			// The final model, not the one handed to NewProgram: bubbletea
			// passes the model by value through every Update, so the state an
			// effect has to release is the one Run gives back.
			b.WriteString("\tfinal, err := p.Run()\n")
		} else {
			b.WriteString("\t_, err := p.Run()\n")
		}
		b.WriteString("\tif err != nil {\n")
		b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"error: %v\\n\", err)\n")
		b.WriteString("\t\tos.Exit(1)\n")
		b.WriteString("\t}\n")
		if teardown {
			b.WriteString("\tif m, ok := final.(Model); ok {\n")
			fmt.Fprintf(&b, "\t\tm.%s()\n", ctx.Pkg.Teardown.Name)
			b.WriteString("\t}\n")
		}
		b.WriteString("}\n")
	}

	body := b.String()
	// lipgloss is emitted via raw view strings (JoinVertical/NewStyle/Color/…)
	// rather than through requireImport at each site. Require it only when the
	// rendered body actually references it — a lone-Widget view emits none.
	if strings.Contains(body, "lipgloss.") {
		gc.RequireImport("charm.land/lipgloss/v2")
	}

	return body, gc.Imports()
}

// emitIRTypeMethod emits a method on a user type through the shared Go
// emitter, which is also what decides whether it takes the Model.
func emitIRTypeMethod(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	for _, line := range gc.EmitTypeMethodDef(fn) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// emitIRFreeFunc emits a top-level func under its exported name, which is what
// FreeFuncs told the call sites to expect.
func emitIRFreeFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
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

// componentFuncSet is every func a component or a window declares. What is
// left in pkg.Funcs is top level: declared beside them rather than inside one,
// so nothing of a component's is in scope for it.
//
// modelMountFuncs is the effect settles New() has to run: the ones owned by
// whoever the Model is.
//
// The lowering appends each as a statement to its owner's body, which every
// mutation-model target executes. A RenderModel's body became View(), which is
// a pure function of the state and skips imperative statements outright, so the
// call reached nothing and no effect on this platform ever mounted.
//
// What has to be excluded is a component that survived inlining: it is not
// part of this Model, so its settle is not a method here to call -- the same
// filter ModelState draws state through. So the question is asked that way
// round, of componentFuncSet, rather than by listing the owners that *are* the
// Model. Those used to be the root declaration and every window; a window owns
// nothing now, so a settle synthesized while walking one is an ordinary package
// func -- and listing owners meant listing none of them, which left New() with
// no `m.__effects0_settle()` in it and no effect mounting on this platform at
// all.
func modelMountFuncs(ctx *codegen.CodegenCtx) []*ir.Func {
	if ctx == nil || ctx.Pkg == nil {
		return nil
	}
	owned := map[*ir.Func]bool{}
	if root := ctx.RootDecl(); root != nil {
		for _, fn := range root.Funcs {
			owned[fn] = true
		}
	}
	// A root declaration's own funcs are in componentFuncSet too, so the
	// explicit set is asked first: the harness that clears the windows to
	// isolate one component still mounts that component's effects.
	surviving := componentFuncSet(ctx.Pkg)
	var out []*ir.Func
	for _, fn := range ctx.Pkg.Mounts {
		if owned[fn] || !surviving[fn] {
			out = append(out, fn)
		}
	}
	return out
}

func componentFuncSet(pkg *ir.Package) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			out[fn] = true
		}
	}
	return out
}

func emitIRFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	if len(fn.Block) == 0 {
		return
	}
	// Emit user funcs as Model methods (lowercase name preserved so tests can
	// invoke `c.<name>(...)`). Defer signature/body emission to the Go language
	// driver's EmitFuncDef instead of re-implementing param/return/body here.
	fnCopy := *fn
	fnCopy.Receiver = "Model"
	for _, line := range gc.EmitFuncDef(&fnCopy) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func emitIRGettersSetters(b *strings.Builder, info *irAnalysis, ctx *codegen.CodegenCtx, gc *golang.GoIRContext) {
	for _, bind := range info.binds {
		// Consts are read-only and would collide field-vs-method when
		// the source name is already exported (e.g. APP_NAME field +
		// APP_NAME getter). Tests reach them via direct field access.
		if bind.isConst {
			continue
		}
		if bind.synthesized {
			// Pass-generated state (e.g. NoContext hidden ctx Var) has
			// no public surface. Tests don't touch it; the Model body
			// reads/writes directly via m.<name>.
			continue
		}
		getter := golang.ExportName(bind.name)
		// Getter
		fmt.Fprintf(b, "func (m Model) %s() %s {\n", getter, bind.goType)
		fmt.Fprintf(b, "\treturn m.%s\n", bind.name)
		b.WriteString("}\n\n")

		// Setter
		fmt.Fprintf(b, "func (m Model) Set%s(v %s) Model {\n", getter, bind.goType)
		fmt.Fprintf(b, "\tm.%s = v\n", bind.name)
		// Sync bound widgets
		for _, w := range info.widgets {
			for _, bd := range w.binds {
				if bd.target == bind.name && bindTargetSyncs(info.binds, bd.target) && bd.set != "" {
					fmt.Fprintf(b, "\tm.%s%s(m.%s)\n", w.fieldName, bd.set, bind.name)
				}
			}
		}
		// Emit @change handlers from IR vars. ModelState is the enumeration
		// of who owns this Model's state, so a var the inliner hoisted onto
		// the window is in it: read as pkg.Vars plus the root component's, a
		// `@change` on such a var reached the setter as nothing at all.
		for _, ov := range ctx.ModelState() {
			v := ov.Var()
			if v == nil || v.Name != bind.name {
				continue
			}
			for _, h := range v.Handlers {
				if h.Name == "change" && h.Func != nil {
					hgc := codegen.BindVarHandlerValue(gc, h, "v")
					for _, stmt := range h.Func.Block {
						for _, line := range hgc.EvalStmt(stmt) {
							fmt.Fprintf(b, "\t%s\n", line)
						}
					}
				}
			}
		}
		b.WriteString("\treturn m\n")
		b.WriteString("}\n\n")

		// Msg/Cmd types
		fmt.Fprintf(b, "type set%sMsg struct{ value %s }\n\n", getter, bind.goType)
		fmt.Fprintf(b, "func Set%sCmd(v %s) tea.Cmd {\n", getter, bind.goType)
		fmt.Fprintf(b, "\treturn func() tea.Msg { return set%sMsg{value: v} }\n", getter)
		b.WriteString("}\n\n")
	}
}

func emitIRUpdate(b *strings.Builder, info *irAnalysis, ctx *codegen.CodegenCtx, gc *golang.GoIRContext, cfg Config, invokerSink btInvokerSink) {
	b.WriteString("func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {\n")
	// Accumulate every command into a batch. Each widget forward and each
	// message handler may produce a command (a timer re-arm, a spinner tick, a
	// textinput blink); a single shared `cmd` would let later assignments clobber
	// earlier ones (e.g. the spinner's unconditional Update silently dropping a
	// gated timer's re-arm tick, freezing the timer after one fire).
	b.WriteString("\tvar cmds []tea.Cmd\n")
	b.WriteString("\tvar cmd tea.Cmd\n")
	b.WriteString("\t_ = cmd\n")

	// Overlay focus capture: while any modal/drawer overlay is open, the
	// background UI is frozen — its Tab focus-nav, key-activation handlers, and
	// widget message-forwarding are gated on !overlayOpen, and Escape closes the
	// open overlay rather than reaching the background. `overlayOpen` is the OR
	// of every overlay's `if open` gate condition. _ = overlayOpen guards the
	// no-overlay case (the var is declared but the gates below are absent).
	hasOverlays := len(info.overlays) > 0
	if hasOverlays {
		var openExprs []string
		for _, ov := range info.overlays {
			if ov.openExpr != "" {
				openExprs = append(openExprs, ov.openExpr)
			}
		}
		if len(openExprs) == 0 {
			openExprs = []string{"false"}
		}
		fmt.Fprintf(b, "\toverlayOpen := %s\n", strings.Join(openExprs, " || "))
		b.WriteString("\t_ = overlayOpen\n")
	}

	b.WriteString("\tswitch msg := msg.(type) {\n")

	// Set messages — consts have no setter, skip them. Synthesized
	// (pass-generated) vars have no public surface either.
	for _, bind := range info.binds {
		if bind.isConst || bind.synthesized {
			continue
		}
		getter := golang.ExportName(bind.name)
		fmt.Fprintf(b, "\tcase set%sMsg:\n", getter)
		fmt.Fprintf(b, "\t\tm = m.Set%s(msg.value)\n", getter)
	}

	// Timer ticks
	for _, t := range info.Timers {
		fmt.Fprintf(b, "\tcase timerTickMsg%d:\n", t.Index)
		rearm := fmt.Sprintf("cmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))", t.IntervalMs, t.Index)
		if t.ActiveVar != "" {
			// Gated timer: run the body and re-arm only while active.
			fmt.Fprintf(b, "\t\tif m.%s {\n", t.ActiveVar)
			for _, bodyStmt := range t.Body {
				for _, line := range gc.EvalStmt(bodyStmt) {
					fmt.Fprintf(b, "\t\t\t%s\n", line)
				}
			}
			fmt.Fprintf(b, "\t\t\tif m.%s {\n", t.ActiveVar)
			fmt.Fprintf(b, "\t\t\t\t%s\n", rearm)
			b.WriteString("\t\t\t}\n")
			b.WriteString("\t\t}\n")
		} else {
			// Always-on timer (no active condition): run the body and re-arm
			// unconditionally. Emitting "if m. {" (empty ActiveVar) is invalid Go.
			for _, bodyStmt := range t.Body {
				for _, line := range gc.EvalStmt(bodyStmt) {
					fmt.Fprintf(b, "\t\t%s\n", line)
				}
			}
			fmt.Fprintf(b, "\t\t%s\n", rearm)
		}
	}
	emitLoopTimerCases(b, info.loopTimers, gc)

	// Toast dismiss
	if info.NeedsToast {
		b.WriteString("\tcase toastDismissMsg:\n")
		b.WriteString("\t\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\t\tm.toasts = m.toasts[1:]\n")
		b.WriteString("\t\t\tif len(m.toasts) > 0 {\n")
		b.WriteString("\t\t\t\tcmds = append(cmds, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} }))\n")
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
	}

	// A settled fetch. The box already holds the answer -- what was missing is
	// a reason to look at it again, and in Bubble Tea that is a message: every
	// Update is followed by a View, so the case needs no body.
	if ctx.Pkg != nil && ctx.Pkg.UsesRemote {
		b.WriteString("\tcase remoteSettledMsg:\n")
	}

	// WindowSizeMsg
	b.WriteString("\tcase tea.WindowSizeMsg:\n")
	b.WriteString("\t\tm.width = msg.Width\n")
	b.WriteString("\t\tm.height = msg.Height\n")
	if widgetsHaveResize(info.widgets) {
		b.WriteString("\t\tm.resizeWidgets()\n")
	}

	// KeyPressMsg
	b.WriteString("\tcase tea.KeyPressMsg:\n")
	b.WriteString("\t\tswitch {\n")
	b.WriteString("\t\tcase msg.Code == 'c' && msg.Mod == tea.ModCtrl:\n")
	b.WriteString("\t\t\treturn m, tea.Quit\n")

	// Escape closes the topmost open overlay. Overlays are listed in source
	// order; the last-declared open one is closed first (a simple modal-over-
	// drawer / last-opened-wins priority). When the overlay's open var was
	// recovered (open=showVar binding), set it false; otherwise the key is still
	// consumed so it never falls through to a background handler.
	if hasOverlays {
		b.WriteString("\t\tcase msg.Code == tea.KeyEsc:\n")
		b.WriteString("\t\t\tswitch {\n")
		for _, ov := range slices.Backward(info.overlays) {
			if ov.openExpr == "" {
				continue
			}
			fmt.Fprintf(b, "\t\t\tcase %s:\n", ov.openExpr)
			if ov.closeVar != "" {
				fmt.Fprintf(b, "\t\t\t\tm.%s = false\n", ov.closeVar)
			} else if ov.closeCode != "" {
				fmt.Fprintf(b, "\t\t\t\t%s\n", ov.closeCode)
			} else {
				// No recoverable open var: consume the key but leave state
				// unchanged (the overlay's gate is a compound expression).
				b.WriteString("\t\t\t\t// overlay open via compound condition; cannot auto-close\n")
			}
		}
		b.WriteString("\t\t\t}\n")
	}

	// Background focus-nav and activation handlers run only when no overlay
	// captures input. With an overlay open the background is frozen.
	caseGuard := ""
	if hasOverlays {
		caseGuard = " && !overlayOpen"
	}
	if info.hasFocus {
		fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyTab && msg.Mod == 0%s:\n", caseGuard)
		b.WriteString("\t\t\tm.__focusNext()\n")
		emitIRFocusSync(b, info.widgets)
		fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyTab && msg.Mod == tea.ModShift%s:\n", caseGuard)
		b.WriteString("\t\t\tm.__focusPrev()\n")
		emitIRFocusSync(b, info.widgets)
	}

	// Button/checkbox enter handlers from IR. The overlay guard is appended to
	// each background case so its activation keys don't fire while frozen;
	// handlers inside an Overlay primitive stay unguarded (see
	// emitIRButtonHandlers) so overlay-content buttons keep working.
	wins := ctx.Windows()
	for _, win := range wins {
		emitIRButtonHandlers(b, win.Body, info, gc, caseGuard, invokerSink)
	}

	b.WriteString("\t\t}\n") // end switch
	b.WriteString("\t}\n")   // end type switch

	// Forward messages to the focused widget, then sync two-way binds back. While
	// an overlay captures input the background widgets are frozen — their message
	// forwarding is wrapped in `if !overlayOpen`. (Widgets nested inside an
	// overlay aren't currently distinguished from background widgets, so an
	// overlay containing a text widget won't receive keys; see report.)
	fwdIndent := "\t"
	if hasOverlays {
		b.WriteString("\tif !overlayOpen {\n")
		fwdIndent = "\t\t"
	}
	for _, w := range info.widgets {
		// A widget with no Update method (e.g. determinate progress, whose
		// view is a pure function of model state) takes no messages and has
		// nothing to sync back — skip its forwarding block entirely. Without
		// the Update RHS the assignment `m.f, cmd = m.f` would be malformed.
		if w.model.Update == "" {
			continue
		}
		if w.focusExpr != "" {
			fmt.Fprintf(b, "%sif %s {\n", fwdIndent, w.focusExpr)
		} else {
			fmt.Fprintf(b, "%s{\n", fwdIndent)
		}
		fmt.Fprintf(b, "%s\tm.%s, cmd = m.%s%s\n", fwdIndent, w.fieldName, w.fieldName, w.model.Update)
		fmt.Fprintf(b, "%s\tcmds = append(cmds, cmd)\n", fwdIndent)
		for _, bd := range w.binds {
			if bindTargetSyncs(info.binds, bd.target) {
				fmt.Fprintf(b, "%s\tm.%s = %s\n", fwdIndent, bd.target, bindReadBack(w.fieldName, bd.get))
			}
		}
		fmt.Fprintf(b, "%s}\n", fwdIndent)
	}
	if hasOverlays {
		b.WriteString("\t}\n")
	}

	// Toast scheduling
	if info.NeedsToast {
		b.WriteString("\tif len(m.toasts) > 0 && len(cmds) == 0 {\n")
		b.WriteString("\t\tcmds = append(cmds, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} }))\n")
		b.WriteString("\t}\n")
	}

	if len(ctx.Canvases.All()) > 0 {
		// Re-transmit canvas pixels after each update so reactive canvases reflect
		// new state; the image data goes out of band (the View carries only
		// placeholder cells). The placement is virtual, so re-transmitting causes
		// no flicker.
		fmt.Fprintf(b, "\tcmds = append(cmds, m.%s())\n", canvasTransmitMethodName)
	}
	emitLoopTimerSyncCalls(b, info.loopTimers, "\t")
	b.WriteString("\treturn m, tea.Batch(cmds...)\n")
	b.WriteString("}\n\n")
}

// emitIRButtonHandlers emits the KeyEnter/etc activation cases for the window
// body. bgGuard (e.g. " && !overlayOpen") is appended to every background
// (non-overlay) handler case so background activation is frozen while an overlay
// is open. Handlers inside an Overlay primitive get NO guard — overlay-content
// buttons (e.g. a modal's Close) stay live while the overlay captures input.
func emitIRButtonHandlers(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, bgGuard string, invokerSink btInvokerSink) {
	emitIRButtonHandlersWalk(b, stmts, info, gc, bgGuard, false, invokerSink)
}

// btInvokerSink records one (node, event) pair a test can drive. nil where
// there is no test surface to emit.
type btInvokerSink func(n *ir.NodeInst, event string, slotIdx int, keyExpr string)

// emitIRButtonHandlersWalk traverses visual IR emitting KeyEnter cases for
// button/checkbox handlers. A for-loop is a loop slot as a whole, answered by
// emitLoopSlotHandlers. bgGuard is appended to each case unless inOverlay is
// set (overlay-content handlers stay unguarded so they keep working while the
// overlay is open).
func emitIRButtonHandlersWalk(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, bgGuard string, inOverlay bool, invokerSink btInvokerSink) {
	overlayGuard := bgGuard
	if inOverlay {
		overlayGuard = ""
	}
	emitNodeCase := func(n *ir.NodeInst, keyGuard string, block []ir.Stmt) {
		slotIdx := nodeFocusSlotIdx(n)
		if slotIdx < 0 {
			return
		}
		fmt.Fprintf(b, "\t\tcase %s && m.__focusID == %d%s:\n", keyGuard, slotIdx, overlayGuard)
		for _, stmt := range block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t\t\t%s\n", line)
			}
		}
		syncMutatedInputs(b, block, info.widgets, info.binds, gc)
	}

	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.For:
			emitLoopSlotHandlers(b, n, info, gc, overlayGuard)
		case *ir.If:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, bgGuard, inOverlay, invokerSink)
			emitIRButtonHandlersWalk(b, n.Else, info, gc, bgGuard, inOverlay, invokerSink)
		case *ir.ErrorBoundary:
			emitIRButtonHandlersWalk(b, n.Children, info, gc, bgGuard, inOverlay, invokerSink)
		case *ir.NodeInst:
			if ir.IsWindowNode(n) {
				panic(fmt.Sprintf("bubbletea: unexpected nested Window in handler walk: %#v", n))
			}
			// Blueprint-driven activation: an inlined Styled primitive that
			// carries Event records maps each event name to a key. The user's
			// handler for that event name was transferred onto the node during
			// inlining (see inline_pure.go). Emit one Update() case per event,
			// keyed on the mapped tea.Key* constant.
			bp := extractBlueprint(n)
			for _, ev := range bp.Events {
				h := codegen.NodeHandler(n, ev.On)
				if h == nil || h.Func == nil || len(h.Func.Block) == 0 {
					continue
				}
				guard := teaKeyGuard(ev.Key)
				if guard == "" {
					continue
				}
				emitNodeCase(n, guard, h.Func.Block)
				if invokerSink != nil {
					if idx := nodeFocusSlotIdx(n); idx >= 0 {
						invokerSink(n, ev.On, idx, teaKeyMsg(ev.Key))
					}
				}
			}
			// Overlay-content handlers stay live while the overlay is open, so
			// descend into an Overlay primitive with inOverlay set (drops bgGuard).
			childInOverlay := inOverlay || btIntrinsic(n) == "Overlay"
			emitIRButtonHandlersWalk(b, n.Children, info, gc, bgGuard, childInOverlay, invokerSink)
		case *ir.SlotInst:
			// Slot expansion happens elsewhere; no buttons inside the marker.
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
			*ir.Break, *ir.Continue:
			// Imperative stmts — no nested visual children to walk.
		case *ir.ContextProvider:
			panic(fmt.Sprintf("bubbletea: ContextProvider should be lowered before handler walk: %#v", n))
		default:
			panic(fmt.Sprintf("bubbletea.emitIRButtonHandlersWalk: unhandled ir.Stmt %T", n))
		}
	}
}

// collectOverlays walks the visual IR finding modal/drawer Overlay primitives.
// Each Overlay's parent is the `if open { Overlay(...) }` gate emitted by the
// modal/drawer platform body, so the enclosing If's condition (threaded in as
// `gate`) is the overlay's open-expr. When that gate is a simple assignable var
// ident (the common `open=showVar` two-way binding), its field name is recorded
// as the close target so Escape can set it false. The view walk dedupes
// overlays the same way (one pendingOverlay per Overlay node), so the order and
// count here match the View's overlay compositing.
func collectOverlays(stmts []ir.Stmt, gate *ir.If, gc *golang.GoIRContext, out *[]overlayInfo) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			// Recurse into the body carrying this If as the enclosing gate, so an
			// Overlay directly inside picks up `if open` as its open condition.
			collectOverlays(n.Body, n, gc, out)
			collectOverlays(n.Else, gate, gc, out)
		case *ir.For:
			collectLoopOverlays(n, gate, gc, out)
		case *ir.ErrorBoundary:
			collectOverlays(n.Children, gate, gc, out)
		case *ir.NodeInst:
			if btIntrinsic(n) == "Overlay" {
				oi := overlayInfo{}
				if gate != nil && gate.Cond != nil {
					oi.openExpr = gc.EvalExpr(gate.Cond)
					oi.closeVar = overlayCloseVar(gate.Cond)
				}
				*out = append(*out, oi)
			}
			collectOverlays(n.Children, gate, gc, out)
		}
	}
}

// overlayCloseVar returns the Model field name to set false to close an overlay
// whose `if open` gate condition is `cond`. It recovers a target only for a bare
// assignable var ident (the `open=showVar` binding); for any compound or
// non-var condition it returns "" — the overlay still freezes the background and
// Escape is still consumed, but the open var isn't auto-cleared (see report).
func overlayCloseVar(cond ir.Expr) string {
	id, ok := cond.(*ir.Ident)
	if !ok {
		return ""
	}
	if _, ok := id.Sym.(*ir.Var); ok {
		return id.Name
	}
	return ""
}

// teaKeyGuard maps an Event record's `key` string to a bubbletea key-message
// guard expression. Returns "" for an unknown key (the case is skipped).
// btEventInvoker is one (#id, @event) pair a test can drive. Firing it sends
// the key that activates the widget through Update, which is this platform's
// real input path -- a TUI has no pointer, and a button is activated by
// pressing a key while it holds focus.
type btEventInvoker struct {
	IDLabel   string // the #id a program wrote
	SnglEvent string // the event a test writes, e.g. "click"
	SlotIdx   int    // the focus slot the widget occupies
	KeyExpr   string // the tea key message that activates it
}

// teaKeyMsg is the message a press of key arrives as. It pairs with
// teaKeyGuard: one says how Update recognises the key, the other how a test
// produces it, and they name the same constant.
func teaKeyMsg(key string) string {
	guard := teaKeyGuard(key)
	const prefix = "msg.Code == "
	if !strings.HasPrefix(guard, prefix) {
		return ""
	}
	return "tea.KeyPressMsg{Code: " + strings.TrimPrefix(guard, prefix) + "}"
}

// emitBtEventInvokers writes one Model method per (id, event) pair. Each sets
// focus to the widget, sends the activating key through Update, and restores
// focus -- so the handler runs by the same route a keypress takes, including
// whatever else Update does with the message.
//
// Update takes and returns a Model by value, so the result is assigned back
// through the pointer receiver; a test holds an addressable local, which is
// what makes `c.incClick()` legal.
//
// A payload a test passes is taken and not read: the key is the input, and
// what it produces is what the override builds from the widget's state -- a
// checkbox's press is its `checked` flipped, whatever the test asked for.
func emitBtEventInvokers(b *strings.Builder, invokers []btEventInvoker, payloads map[string]*ir.Type) {
	seen := map[string]bool{}
	for _, inv := range invokers {
		name := inv.IDLabel + golang.ExportName(inv.SnglEvent)
		if seen[name] || inv.KeyExpr == "" {
			continue
		}
		seen[name] = true
		fmt.Fprintf(b, "// %s activates the #%s widget the way a keypress does; for tests.\n", name, inv.IDLabel)
		if t := payloads[inv.IDLabel+"."+inv.SnglEvent]; t != nil {
			fmt.Fprintf(b, "func (m *Model) %s(_ %s) {\n", name, golang.IRTypeToGo(t))
		} else {
			fmt.Fprintf(b, "func (m *Model) %s() {\n", name)
		}
		fmt.Fprintf(b, "\tprev := m.__focusID\n")
		fmt.Fprintf(b, "\tm.__focusID = %d\n", inv.SlotIdx)
		fmt.Fprintf(b, "\tnm, _ := m.Update(%s)\n", inv.KeyExpr)
		fmt.Fprintf(b, "\t*m = nm.(Model)\n")
		fmt.Fprintf(b, "\tm.__focusID = prev\n")
		b.WriteString("}\n\n")
	}
}

func teaKeyGuard(key string) string {
	switch key {
	case "enter":
		return "msg.Code == tea.KeyEnter"
	case "space":
		return "msg.Code == tea.KeySpace"
	case "tab":
		return "msg.Code == tea.KeyTab"
	case "esc", "escape":
		return "msg.Code == tea.KeyEsc"
	case "up":
		return "msg.Code == tea.KeyUp"
	case "down":
		return "msg.Code == tea.KeyDown"
	case "left":
		return "msg.Code == tea.KeyLeft"
	case "right":
		return "msg.Code == tea.KeyRight"
	default:
		return ""
	}
}

func emitIRFocusSync(b *strings.Builder, widgets []widgetInfo) {
	for _, w := range widgets {
		if w.focusExpr == "" || !modelHasFocusMethods(w.model.Type) {
			continue
		}
		fmt.Fprintf(b, "\t\t\tif %s {\n", w.focusExpr)
		fmt.Fprintf(b, "\t\t\t\tm.%s.Focus()\n", w.fieldName)
		b.WriteString("\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\tm.%s.Blur()\n", w.fieldName)
		b.WriteString("\t\t\t}\n")
	}
}

// modelHasFocusMethods reports whether a bubbles model type exposes
// Focus()/Blur() methods. textinput and textarea track an internal focus flag
// (and only blink/accept keys while focused); the SNGL focus machinery drives
// that via explicit Focus()/Blur() calls. list-backed widgets (menu/tree/
// select) have no such methods — they always accept navigation messages and
// are gated purely by the focus-conditioned Update forwarding — so emitting
// Focus()/Blur() against them would not compile. Widgets absent from this set
// still participate in focus routing; they just skip the method calls.
func modelHasFocusMethods(modelType string) bool {
	switch modelType {
	case "textinput.Model", "textarea.Model":
		return true
	default:
		return false
	}
}

// nodeStaticFocusExpr returns a Go expression (suitable for an `if` guard) that
// is true when the given node is focused. It reads the __focused prop injected
// by passFocusOrder and evaluates it against the model. For loop-body nodes
// whose __focused expression references a loop-local variable (non-static), it
// returns "" to indicate the focus expression is not available outside the loop.
func nodeStaticFocusExpr(n *ir.NodeInst, gc *golang.GoIRContext) string {
	fp := codegen.NodeProp(n, "__focused")
	if fp == nil {
		return ""
	}
	// For loop-body items, __focused = __focusID==S && cursor==loopKey. The
	// loopKey is a loop-local variable not in scope at the input-dispatch site,
	// so we can't generate a valid expression there. Detect this by checking
	// whether the top-level binary op is BinAnd (compound condition).
	if bin, ok := fp.(*ir.Binary); ok && bin.Op == ast.BinAnd {
		return "" // loop-body input — not supported at static dispatch site
	}
	return gc.EvalExpr(fp)
}

// nodeFocusSlotIdx extracts the slot index from a static __focused prop
// (i.e. __focusID == S) on a NodeInst. Returns -1 if absent or compound.
func nodeFocusSlotIdx(n *ir.NodeInst) int {
	fp := codegen.NodeProp(n, "__focused")
	if fp == nil {
		return -1
	}
	bin, ok := fp.(*ir.Binary)
	if !ok || bin.Op != ast.BinEq {
		return -1
	}
	lit, ok := bin.Right.(*ir.Literal)
	if !ok {
		return -1
	}
	id, err := strconv.Atoi(lit.Value)
	if err != nil {
		return -1
	}
	return id
}

func syncMutatedInputs(b *strings.Builder, stmts []ir.Stmt, widgets []widgetInfo, binds []irBind, gc *golang.GoIRContext) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for v := range codegen.MutatedFields(nil, nil, stmt) {
			mutated[v.SymName()] = true
		}
	}
	for _, w := range widgets {
		for _, bd := range w.binds {
			if mutated[bd.target] && bindTargetSyncs(binds, bd.target) && bd.set != "" {
				fmt.Fprintf(b, "\t\t\tm.%s%s(m.%s)\n", w.fieldName, bd.set, bd.target)
			}
		}
	}
}

func irVarInit(v *ir.Var, gc *golang.GoIRContext) string {
	return golang.LowerVarInit(v, gc)
}

// widgetsHaveResize reports whether any widget declares a resize template, which
// gates emission of the resizeWidgets() method and its call sites. An empty
// method (or calls to a non-existent method) would be dead/invalid code.
func widgetsHaveResize(widgets []widgetInfo) bool {
	for _, w := range widgets {
		if w.model.Resize != "" {
			return true
		}
	}
	return false
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

// loopVarUsed reports whether block references the loop variable named name.
// The head binds it only then, since Go rejects an unused one.
func loopVarUsed(block []ir.Stmt, name string, sym ir.Symbol) bool {
	if name == "" || name == "_" {
		return false
	}
	found := false
	_ = ir.Walk(block, func(n ir.Node) error {
		id, ok := n.(*ir.Ident)
		if !ok {
			return nil
		}
		if (sym != nil && id.Sym == sym) || id.Name == name {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
