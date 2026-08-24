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

// widgetInfo describes one inlined Widget blueprint primitive (e.g. an `input`
// backed by textinput, a `textarea` backed by textarea). It is model-meta-driven
// — the field type, constructor, view/update methods, and init cmd all come from
// the node's `Model` record rather than from the stdlib component name.
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

// bindReadBack builds the Go expression that reads a widget's current value out
// of model field `field`, given the Bind record's `get`. Two shapes are
// supported:
//
//	get = ".Value()"          → m.<field>.Value()            (method chain)
//	get = "tui.SelectedString" → tui.SelectedString(m.<field>) (converter wrap)
//
// The converter-wrap form (any get starting with "tui.") lets a list-backed
// widget read its selection through a nil-safe helper — m.<field>.SelectedItem()
// can be nil, so a bare ".SelectedItem().FilterValue()" chain would panic on an
// empty list. Wrapping centralizes the nil guard in pkg/go/tui.
func bindReadBack(field, get string) string {
	if strings.HasPrefix(get, "tui.") {
		return get + "(m." + field + ")"
	}
	return "m." + field + get
}

// widgetSyncGoTypes is the set of Go types a bubbles widget value may round-trip
// through. A bind is only synced (seeded in the constructor, pushed on Set,
// pulled back in Update) when its target model field has one of these types.
//
//   - string: two-way value widgets (input/textarea) via SetValue(string) /
//     Value() string, and read-only list-backed widgets via SelectedString.
//   - int: the read-only `selected` cursor bind on `table` (read back via
//     tui.Cursor / .Cursor()). Int binds are read-only in practice — the three
//     push sites (constructor seed, Set* setter, mutated-handler sync) are all
//     additionally gated on `set != ""`, so only the Update reverse-sync
//     (m.target = m.field.Cursor()) is emitted, which is valid int Go.
//
// Types outside this set aren't synced — wiring them would emit type-mismatched
// Go. A follow-on could carry the widget's value type in the Bind record so
// binds of arbitrary types are coerced or rejected explicitly.
var widgetSyncGoTypes = map[string]struct{}{
	"string": {},
	"int":    {},
}

// bindTargetSyncs reports whether a widget bind targeting model field `target`
// may be synced to/from the widget — true only when the target's Go type is one
// the widget value engine supports (string or int). Keeps the constructor seed,
// the Set* setter, and the Update reverse-sync consistent so an unsupported bind
// type never emits type-mismatched Go.
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
	// Target not found among model binds (shouldn't happen for a real two-way
	// bind); be conservative and don't emit a sync.
	return false
}

// CompileIR generates a Go source file from IR using the new CodegenCtx.
func CompileIR(ctx *codegen.CodegenCtx, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	info := analyzeIR(ctx)
	body, imports := emitIR(info, ctx, cfg)

	// Assemble through the language FileEmitter so import rendering, gofmt,
	// and the package clause are owned by the code writer. A MemSink lets us
	// keep returning bytes (android's go path + tests consume them); the
	// generated-by header is added by the caller's emitter (Source left empty).
	mem := codegen.NewMemSink()
	e := (&golang.Translator{}).NewFileEmitter(mem, codegen.FileOptions{
		Name:        "model.go",
		PackageName: cfg.Package,
	})
	// The SNGL canvas runtime (pkg/go/canvas) default-aliases to "canvas"; the
	// draw-func selectors reference it as snglcanvas (*snglcanvas.Context,
	// snglcanvas.New), so force that alias.
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

// irAnalysis is the IR-based replacement for analysisResult.
type irAnalysis struct {
	*codegen.CommonAnalysis
	binds     []irBind
	externs   []irExtern
	computeds []irComputed
	widgets   []widgetInfo
	overlays  []overlayInfo
	hasFocus  bool // true when __focusOrder pass injected __focusID/__focusNext/__focusPrev
	gc        *golang.GoIRContext
}

// overlayInfo records one modal/drawer Overlay primitive for the Update()
// focus-capture logic. openExpr is the Go boolean expression that is true while
// the overlay is open (the `if open` gate condition — e.g. "m.showModal").
// closeVar is the Model field name to set false on dismiss (Escape), recovered
// when the gate condition is a simple assignable var ident; "" when the gate is
// a compound expression we can't invert into a single assignment.
type overlayInfo struct {
	openExpr string
	closeVar string
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
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		gc:             gc,
	}
	pkg := ctx.Pkg

	// Collect Go imports declared by SNGL (go:// natives, i18n runtime).
	for _, imp := range golang.BaseImports(pkg) {
		gc.RequireImport(imp.Path)
	}

	// After NoInlineComponents, every non-main component has been inlined
	// into main. Walk pkg.Vars + pkg.Consts + main.Vars only — there are
	// no remaining child-component vars to collect.
	var allVars []*ir.Var
	for _, v := range pkg.Vars {
		allVars = append(allVars, v)
	}
	for _, c := range pkg.Consts {
		allVars = append(allVars, c)
	}
	if main := ctx.MainComponent(); main != nil {
		allVars = append(allVars, main.Vars...)
	}
	for _, v := range allVars {
		// Consts emit as Model fields too: tests read them via `c.<name>`
		// and component-method bodies via `m.<name>`. Top-level consts
		// also get a file-scope `var` emission earlier in the file so
		// top-level free funcs (not Model methods) can reach them.
		goType := golang.VarGoType(v)
		initVal := irVarInit(v, gc)
		if strings.HasPrefix(goType, "time.") {
			gc.RequireImport("time")
		}
		info.binds = append(info.binds, irBind{
			name:        v.Name,
			goType:      goType,
			init:        initVal,
			isConst:     v.IsConst,
			synthesized: v.Synthesized,
		})
	}

	// Collect computed functions. pkg.Funcs and main.Funcs overlap for nested
	// component methods (registered in both since T7); dedupe by pointer.
	allFuncs := pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
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

	// Timer analysis
	for _, t := range info.Timers {
		if t.IntervalMs > 0 {
			gc.RequireImport("time")
		}
	}

	// Walk visual tree for Widget blueprint primitives. Each carries a Model
	// record (field type, constructor, view/update methods, init cmd, import) so
	// the subsystem is model-meta-driven rather than keyed on a stdlib name. The
	// __focused prop injected by passFocusOrder is evaluated to a Go expression;
	// "" means focus tracking is not active.
	wins := ctx.Windows()
	for _, win := range wins {
		codegen.WalkVisualTree(win.Body, func(n *ir.NodeInst, _ int) bool {
			if n.Component == nil || n.Name != "Widget" {
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
			// Two-way binds: find the Model var the bind syncs to. A `:value`
			// bind lowers to a handler named after the prop ("value"); an
			// explicit live-update handler (@input/@change) carries the same
			// write. Check the prop-named handler first, then the live-update
			// events, taking the first whose body assigns to a var. Pair that
			// target with the model getter declared in the Bind record.
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
			// Expand `${prop}`/`${prop|conv}` template tokens in the Model
			// strings against this node's props. No-token strings (input,
			// textarea) pass through unchanged, so their output stays
			// byte-identical. Converter tokens require the pkg/go/tui import.
			model := bp.Model
			model.New = expandWidgetTemplate(gc, model.New, n, fieldName)
			model.View = expandWidgetTemplate(gc, model.View, n, fieldName)
			model.Update = expandWidgetTemplate(gc, model.Update, n, fieldName)
			model.Init = expandWidgetTemplate(gc, model.Init, n, fieldName)
			model.Resize = expandWidgetTemplate(gc, model.Resize, n, fieldName)
			// A model string may reference a pkg/go/tui helper inline (e.g.
			// progress's `tui.Percent(...)`) without going through a `|conv`
			// token, so the converter path's RequireImport doesn't fire. Pull
			// the import in whenever any expanded string names the tui package.
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

	// Collect modal/drawer overlays and their `if open` gate conditions so
	// Update() can freeze the background and Escape-close the open overlay.
	for _, win := range wins {
		collectOverlays(win.Body, nil, gc, &info.overlays)
	}

	// Detect whether passFocusOrder ran by checking for the __focusID var.
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

// emitIR renders the model file body (no package clause or import block) and
// returns it along with the Go import paths it uses. Imports are recorded at
// their emit sites: the bubbletea framework packages are required structurally
// here (every model has Init/Update/View, which reference tea + lipgloss +
// fmt); dynamic imports (e.g. "math" for a float intrinsic) accumulate on gc
// as the body is translated; go:// natives, "time", and var-init imports
// arrive via info.gc (accumulated during analyzeIR). The caller feeds these to
// a FileEmitter, which renders the package clause + import block, gofmt, and
// source-map directives.
func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config) (string, []string) {
	var b strings.Builder
	gc := info.gc

	// Structural framework import: every model's Init/Update/View shells
	// reference tea. "lipgloss" and "fmt" are NOT structural — a view that
	// renders only through a bubbles Widget (e.g. a lone textarea/input) emits
	// neither, so both are required at the end gated on the rendered body
	// referencing them.
	gc.RequireImport("charm.land/bubbletea/v2")
	if cfg.Main {
		gc.RequireImport("os")
		gc.RequireImport("fmt") // main() prints errors via fmt.Fprintf
	}
	// Widget model packages are required during analyzeIR (info.gc accumulates
	// each Model.Pkg), so no structural widget import is needed here.

	// Lang-tracked helpers (mustParse*) — picked up via HelpersNeeded.
	helpers := golang.HelpersNeeded(ctx.Pkg)
	for _, imp := range helpers.Imports() {
		gc.RequireImport(imp)
	}
	b.WriteString(helpers.Emit())
	b.WriteString(golang.EmitMergeFuncs(ctx.Pkg.MergeStructs))

	// Unit types (excluding the special-cased `duration`).
	b.WriteString(golang.EmitUnitTypeDecls(info.Units))

	// Struct types
	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "type %s struct {\n", golang.ExportName(sd.Name))
		for _, f := range sd.Fields {
			goType := golang.IRTypeToGo(f.Type)
			fmt.Fprintf(&b, "\t%s %s\n", golang.ExportName(f.Name), goType)
		}
		b.WriteString("}\n\n")
	}

	// Canvas stdlib structs (Color/CanvasStyle/PathCmd) are read by the
	// synthesized draw funcs (style.Fill.R etc.) but aren't carried on
	// pkg.Structs for the Go path, so declare them here when any canvas node is
	// present. Skip any name a user struct already declares to avoid duplicates.
	if hasCanvasNodes(ctx.Pkg) {
		b.WriteString(canvasStdlibDecls(info.Structs))
	}

	// ErrorEvent is emitted when any error-handling construct is present.
	// The stdlib defines the struct but codegen doesn't flow stdlib types
	// into user output, so it needs to materialise here.
	if ctx.Pkg.UsesErrorHandling {
		b.WriteString("type ErrorEvent struct {\n\tMessage string\n\tKind    string\n}\n\n")
	}

	// Top-level const decls are emitted at file scope as Go `var` so
	// free-function bodies (which are top-level Go funcs, not Model
	// methods) can reference them by bare name. Component-level consts
	// don't get a file-scope emission — they only live as Model fields,
	// reached via `m.<name>` from any component method.
	if len(ctx.Pkg.Consts) > 0 {
		for _, c := range ctx.Pkg.Consts {
			init := golang.LowerVarInit(c, gc)
			goType := golang.IRTypeToGo(c.Type)
			fmt.Fprintf(&b, "var %s %s = %s\n", c.Name, goType, init)
		}
		b.WriteString("\n")
	}

	// Timer tick messages
	for _, t := range info.Timers {
		fmt.Fprintf(&b, "type timerTickMsg%d struct{}\n", t.Index)
	}
	if len(info.Timers) > 0 {
		b.WriteString("\n")
	}

	// Toast
	if info.NeedsToast {
		b.WriteString("type snglToast struct {\n\tmessage string\n\tvariant string\n}\n\n")
		b.WriteString("type toastDismissMsg struct{}\n\n")
	}

	// Model struct
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
	if info.NeedsToast {
		b.WriteString("\ttoasts []snglToast\n")
	}
	b.WriteString("\twidth, height int\n")
	b.WriteString("}\n\n")

	// New(). Binds are initialized sequentially so later inits can
	// reference earlier fields via `m.<name>` (e.g. interpolated
	// `$"Hello, {nm}!"` reading `m.nm`). A pre-fix struct-literal init
	// left `m` undefined inside the literal.
	b.WriteString("// New creates a Model with default bind values.\n")
	b.WriteString("func New() Model {\n")
	b.WriteString("\tm := Model{}\n")
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
			// This widget occupies the first focusable slot (where __focusID
			// starts). Only emit a construction-time .Focus() if it actually
			// has the method; either way mark the slot consumed so a later
			// method-having widget (e.g. an input after a list/table) isn't
			// wrongly focused while __focusID still points here.
			if modelHasFocusMethods(w.model.Type) {
				fmt.Fprintf(&b, "\tm.%s.Focus()\n", w.fieldName)
			}
			firstFocusable = false
		}
	}
	// Size widgets to the initial terminal extent. m.width/m.height are still
	// zero here (no WindowSizeMsg yet), so resizeWidgets falls back to its
	// defaults — but emitting it keeps construction consistent with the post-
	// resize state and primes any widget whose default size is 0.
	if widgetsHaveResize(info.widgets) {
		b.WriteString("\tm.resizeWidgets()\n")
	}
	b.WriteString("\treturn m\n")
	b.WriteString("}\n\n")

	// SetTerminalSize. Also resizes the bubbles widgets: the snapshot harness
	// calls New() + SetTerminalSize(w,h) + View() with no WindowSizeMsg, so this
	// is the only place widget sizing happens on that path.
	b.WriteString("// SetTerminalSize sets the terminal dimensions and resizes widgets.\n")
	b.WriteString("func (m *Model) SetTerminalSize(w, h int) {\n")
	b.WriteString("\tm.width = w\n")
	b.WriteString("\tm.height = h\n")
	if widgetsHaveResize(info.widgets) {
		b.WriteString("\tm.resizeWidgets()\n")
	}
	b.WriteString("}\n\n")

	// resizeWidgets applies each widget's resize template against the current
	// m.width/m.height. Called from New(), SetTerminalSize(), and the
	// tea.WindowSizeMsg case so widgets stay sized to the terminal on every
	// path. Omitted entirely when no widget declares a resize template.
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

	// Computed methods. Single-Return bodies emit `return <expr>` for
	// minimal output; multi-statement bodies (e.g. those introduced by
	// passNoListLambdas hoisting `var __listN ...; for ...; return ...`)
	// emit the full block.
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

	// Canvas2D draw funcs (passCanvas-synthesized `_canvasDrawN(ctx)`). These
	// are emitted specially as `func (m Model) _canvasDrawN(ctx
	// *snglcanvas.Context)` with their bodies routed through the shared canvas
	// Context translator, so they're excluded from the generic user-func loop.
	canvasDraws := canvasDrawFuncSet(ctx.Pkg)
	emitCanvasDrawFuncs(&b, ctx.Pkg, gc)
	hasCanvas := hasCanvasNodes(ctx.Pkg)
	if hasCanvas {
		emitCanvasTransmitMethod(&b, ctx.Pkg, gc)
	}

	// User-defined functions (dedupe overlap between pkg.Funcs and main.Funcs).
	allFuncs := ctx.Pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	seenUserFn := make(map[*ir.Func]bool, len(allFuncs))
	for _, fn := range allFuncs {
		if seenUserFn[fn] {
			continue
		}
		seenUserFn[fn] = true
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) || canvasDraws[fn] {
			continue
		}
		emitIRFunc(&b, fn, gc)
	}

	// Getters/Setters
	emitIRGettersSetters(&b, info, ctx, gc)

	// Init()
	// Widget init cmds (e.g. textinput.Blink) — deduped, model-meta-driven.
	var widgetInits []string
	seenInit := map[string]bool{}
	for _, w := range info.widgets {
		if w.model.Init != "" && !seenInit[w.model.Init] {
			seenInit[w.model.Init] = true
			widgetInits = append(widgetInits, w.model.Init)
		}
	}

	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.Timers) > 0 {
		b.WriteString("\tvar cmds []tea.Cmd\n")
		for _, wi := range widgetInits {
			fmt.Fprintf(&b, "\tcmds = append(cmds, %s)\n", wi)
		}
		for _, t := range info.Timers {
			tick := fmt.Sprintf("cmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))", t.IntervalMs, t.Index)
			if t.ActiveVar != "" {
				// Gated timer: only start when its active condition holds.
				fmt.Fprintf(&b, "\tif m.%s {\n", t.ActiveVar)
				fmt.Fprintf(&b, "\t\t%s\n", tick)
				b.WriteString("\t}\n")
			} else {
				// Always-on timer (no active condition): start unconditionally.
				// Emitting "if m. {" (empty ActiveVar) would be invalid Go.
				fmt.Fprintf(&b, "\t%s\n", tick)
			}
		}
		if hasCanvas {
			// Transmit each canvas's pixels on first paint (out of band, so the
			// kitty image data isn't dropped by the cell compositor).
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

	// Update()
	emitIRUpdate(&b, info, ctx, gc, cfg)

	// View()
	emitIRView(&b, info, ctx, gc, cfg)

	// Component render methods
	for _, cc := range ctx.NonMainComponents() {
		emitIRComponentMethod(&b, cc, ctx, gc, cfg)
	}

	// main()
	if cfg.Main {
		b.WriteString("func main() {\n")
		b.WriteString("\tp := tea.NewProgram(New())\n")
		b.WriteString("\tif _, err := p.Run(); err != nil {\n")
		b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"error: %v\\n\", err)\n")
		b.WriteString("\t\tos.Exit(1)\n")
		b.WriteString("\t}\n")
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
		// Emit @change handlers from IR vars
		allVars := ctx.Pkg.Vars
		if main := ctx.MainComponent(); main != nil {
			allVars = append(allVars, main.Vars...)
		}
		for _, v := range allVars {
			if v.Name != bind.name {
				continue
			}
			for _, h := range v.Handlers {
				if h.Name == "change" && h.Func != nil {
					for _, stmt := range h.Func.Block {
						for _, line := range gc.EvalStmt(stmt) {
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

func emitIRUpdate(b *strings.Builder, info *irAnalysis, ctx *codegen.CodegenCtx, gc *golang.GoIRContext, cfg Config) {
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
		emitIRButtonHandlers(b, win.Body, info, gc, caseGuard)
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

	if hasCanvasNodes(ctx.Pkg) {
		// Re-transmit canvas pixels after each update so reactive canvases reflect
		// new state; the image data goes out of band (the View carries only
		// placeholder cells). The placement is virtual, so re-transmitting causes
		// no flicker.
		fmt.Fprintf(b, "\tcmds = append(cmds, m.%s())\n", canvasTransmitMethodName)
	}
	b.WriteString("\treturn m, tea.Batch(cmds...)\n")
	b.WriteString("}\n\n")
}

// emitIRButtonHandlers emits the KeyEnter/etc activation cases for the window
// body. bgGuard (e.g. " && !overlayOpen") is appended to every background
// (non-overlay) handler case so background activation is frozen while an overlay
// is open. Handlers inside an Overlay primitive get NO guard — overlay-content
// buttons (e.g. a modal's Close) stay live while the overlay captures input.
func emitIRButtonHandlers(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, bgGuard string) {
	emitIRButtonHandlersWalk(b, stmts, info, gc, nil, bgGuard, false)
}

// emitIRButtonHandlersWalk traverses visual IR emitting KeyEnter cases for
// button/checkbox handlers. currentFor is non-nil when inside a for-loop that
// passFocusOrder turned into a loop slot; handlers inside it are emitted with a
// loop-wrapped body that matches the cursor to the current iteration. bgGuard is
// appended to each case unless inOverlay is set (overlay-content handlers stay
// unguarded so they keep working while the overlay is open).
func emitIRButtonHandlersWalk(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, currentFor *ir.For, bgGuard string, inOverlay bool) {
	overlayGuard := bgGuard
	if inOverlay {
		overlayGuard = ""
	}
	emitStaticCase := func(slotIdx int, keyGuard string, block []ir.Stmt) {
		fmt.Fprintf(b, "\t\tcase %s && m.__focusID == %d%s:\n", keyGuard, slotIdx, overlayGuard)
		for _, stmt := range block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t\t\t%s\n", line)
			}
		}
		syncMutatedInputs(b, block, info.widgets, info.binds, gc)
	}
	emitLoopCase := func(slotIdx int, keyGuard, cursorVar, keyName, valName string, iterExpr string, block []ir.Stmt) {
		// Render body into a temp buffer to check if valName is actually used.
		var tmp strings.Builder
		for _, stmt := range block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(&tmp, "\t\t\t\t\t%s\n", line)
			}
		}
		syncMutatedInputs(&tmp, block, info.widgets, info.binds, gc)
		body := tmp.String()
		// Only bind element variable if the body actually references it.
		emitVal := "_"
		if strings.Contains(body, valName) {
			emitVal = valName
		}
		fmt.Fprintf(b, "\t\tcase %s && m.__focusID == %d%s:\n", keyGuard, slotIdx, overlayGuard)
		fmt.Fprintf(b, "\t\t\tfor %s, %s := range %s {\n", keyName, emitVal, iterExpr)
		fmt.Fprintf(b, "\t\t\t\tif m.%s == %s {\n", cursorVar, keyName)
		b.WriteString(body)
		b.WriteString("\t\t\t\t\tbreak\n")
		b.WriteString("\t\t\t\t}\n")
		b.WriteString("\t\t\t}\n")
	}

	emitNodeCase := func(n *ir.NodeInst, keyGuard string, block []ir.Stmt) {
		if currentFor == nil {
			// Static slot.
			if idx := nodeFocusSlotIdx(n); idx >= 0 {
				emitStaticCase(idx, keyGuard, block)
			}
			return
		}
		// Loop-body slot: extract slot and cursor info from __focused prop.
		fp := codegen.NodeProp(n, "__focused")
		if fp == nil {
			return
		}
		outer, ok := fp.(*ir.Binary)
		if !ok || outer.Op != ast.BinAnd {
			return
		}
		// Left: __focusID == slotIdx
		leftBin, ok := outer.Left.(*ir.Binary)
		if !ok || leftBin.Op != ast.BinEq {
			return
		}
		lit, ok := leftBin.Right.(*ir.Literal)
		if !ok {
			return
		}
		slotIdx, err := strconv.Atoi(lit.Raw)
		if err != nil {
			return
		}
		// Right: cursor == key
		rightBin, ok := outer.Right.(*ir.Binary)
		if !ok || rightBin.Op != ast.BinEq {
			return
		}
		cursorIdent, ok := rightBin.Left.(*ir.Ident)
		if !ok {
			return
		}
		keyName := currentFor.Key
		valName := currentFor.Value
		if valName == "" || valName == "_" {
			valName = "_"
		}
		iterExpr := gc.EvalExpr(currentFor.Iter)
		emitLoopCase(slotIdx, keyGuard, cursorIdent.Name, keyName, valName, iterExpr, block)
	}

	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.For:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, n, bgGuard, inOverlay)
		case *ir.If:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, currentFor, bgGuard, inOverlay)
			emitIRButtonHandlersWalk(b, n.Else, info, gc, currentFor, bgGuard, inOverlay)
		case *ir.PlatformFilter:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, currentFor, bgGuard, inOverlay)
		case *ir.ErrorBoundary:
			emitIRButtonHandlersWalk(b, n.Children, info, gc, currentFor, bgGuard, inOverlay)
		case *ir.NodeInst:
			// Blueprint-driven activation: an inlined Styled primitive that
			// carries Event records maps each event name to a key. The user's
			// handler for that event name was transferred onto the node during
			// inlining (see inline_pure.go). Emit one Update() case per event,
			// keyed on the mapped tea.Key* constant.
			bp := extractBlueprint(n)
			for _, ev := range bp.Events {
				h := codegen.NodeHandler(n, ev.On)
				if h == nil || h.Func == nil {
					continue
				}
				guard := teaKeyGuard(ev.Key)
				if guard == "" {
					continue
				}
				emitNodeCase(n, guard, h.Func.Block)
			}
			// Overlay-content handlers stay live while the overlay is open, so
			// descend into an Overlay primitive with inOverlay set (drops bgGuard).
			childInOverlay := inOverlay || (n.Component != nil && n.Name == "Overlay")
			emitIRButtonHandlersWalk(b, n.Children, info, gc, currentFor, bgGuard, childInOverlay)
		case *ir.SlotInst:
			// Slot expansion happens elsewhere; no buttons inside the marker.
		case *ir.Window:
			panic(fmt.Sprintf("bubbletea: unexpected nested Window in handler walk: %#v", n))
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
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
			collectOverlays(n.Body, gate, gc, out)
		case *ir.PlatformFilter:
			collectOverlays(n.Body, gate, gc, out)
		case *ir.ErrorBoundary:
			collectOverlays(n.Children, gate, gc, out)
		case *ir.NodeInst:
			if n.Component != nil && n.Name == "Overlay" {
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
	id, err := strconv.Atoi(lit.Raw)
	if err != nil {
		return -1
	}
	return id
}

func syncMutatedInputs(b *strings.Builder, stmts []ir.Stmt, widgets []widgetInfo, binds []irBind, gc *golang.GoIRContext) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for v := range codegen.MutatedFields(nil, nil, stmt) {
			mutated[v.Name] = true
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

// --- helpers ---

func irVarInit(v *ir.Var, gc *golang.GoIRContext) string {
	return golang.LowerVarInit(v, gc)
}

func irExprGoType(e ir.Expr) string {
	if e == nil {
		return "any"
	}
	if t := e.ExprType(); t != nil {
		return golang.IRTypeToGo(t)
	}
	return "any"
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
