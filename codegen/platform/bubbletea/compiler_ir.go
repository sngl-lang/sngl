package bubbletea

import (
	"fmt"
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

type inputInfo struct {
	fieldName   string
	bindTarget  string
	placeholder string
	focusExpr   string // Go expr that is true when this input is focused; "" = no tracking
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
	for _, p := range imports {
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
	inputs    []inputInfo
	hasFocus  bool // true when __focusOrder pass injected __focusID/__focusNext/__focusPrev
	gc        *golang.GoIRContext
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
		goType := irVarGoType(v)
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
			goType := irFuncReturnType(f)
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

	// Walk visual tree for inputs. The __focused prop injected by passFocusOrder
	// is evaluated to a Go expression; "" means focus tracking is not active.
	wins := ctx.Windows()
	for _, win := range wins {
		codegen.WalkVisualTree(win.Body, func(n *ir.NodeInst, _ int) bool {
			if n.Name != "input" {
				return false
			}
			fieldName := ctx.Namer.Next("input")
			placeholder := ""
			if s, ok := codegen.IRLiteralString(codegen.NodeProp(n, "placeholder")); ok {
				placeholder = s
			}
			bindTarget := ""
			if h := codegen.NodeHandler(n, "input"); h != nil && h.Func != nil {
				bindTarget = extractIRAssignTarget(h.Func.Block)
			}
			info.inputs = append(info.inputs, inputInfo{
				fieldName:   fieldName,
				bindTarget:  bindTarget,
				placeholder: placeholder,
				focusExpr:   nodeStaticFocusExpr(n, gc),
			})
			return false
		})
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

	// Structural framework imports: every model's Init/Update/View shells
	// reference tea, and View renders through lipgloss. "fmt" is NOT structural
	// — it's required at the emit site (irViewContext.line, and main below)
	// only when an fmt.* reference is actually written.
	gc.RequireImport("charm.land/bubbletea/v2")
	gc.RequireImport("charm.land/lipgloss/v2")
	if cfg.Main {
		gc.RequireImport("os")
		gc.RequireImport("fmt") // main() prints errors via fmt.Fprintf
	}
	if len(info.inputs) > 0 {
		gc.RequireImport("charm.land/bubbles/v2/textinput")
	}

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

	// ErrorEvent is emitted when any error-handling construct is present.
	// The stdlib defines the struct but codegen doesn't flow stdlib types
	// into user output, so it needs to materialise here.
	if codegen.PackageUsesErrorHandling(ctx.Pkg) {
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
	for _, inp := range info.inputs {
		fmt.Fprintf(&b, "\t%s textinput.Model\n", inp.fieldName)
	}
	if len(info.inputs) > 0 {
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
	for i, inp := range info.inputs {
		fmt.Fprintf(&b, "\tm.%s = textinput.New()\n", inp.fieldName)
		if inp.placeholder != "" {
			fmt.Fprintf(&b, "\tm.%s.Placeholder = %q\n", inp.fieldName, inp.placeholder)
		}
		if inp.bindTarget != "" {
			fmt.Fprintf(&b, "\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
		}
		if i == 0 {
			fmt.Fprintf(&b, "\tm.%s.Focus()\n", inp.fieldName)
		}
	}
	b.WriteString("\treturn m\n")
	b.WriteString("}\n\n")

	// SetTerminalSize
	b.WriteString("// SetTerminalSize sets the terminal dimensions.\n")
	b.WriteString("func (m *Model) SetTerminalSize(w, h int) {\n")
	b.WriteString("\tm.width = w\n")
	b.WriteString("\tm.height = h\n")
	b.WriteString("}\n\n")

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
		if fn.IsTest || fn.Receiver != "" || codegen.IsComputed(fn) {
			continue
		}
		emitIRFunc(&b, fn, gc)
	}

	// Getters/Setters
	emitIRGettersSetters(&b, info, ctx, gc)

	// Init()
	b.WriteString("func (m Model) Init() tea.Cmd {\n")
	if len(info.Timers) > 0 {
		b.WriteString("\tvar cmds []tea.Cmd\n")
		if len(info.inputs) > 0 {
			b.WriteString("\tcmds = append(cmds, textinput.Blink)\n")
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
		b.WriteString("\treturn tea.Batch(cmds...)\n")
	} else if len(info.inputs) > 0 {
		b.WriteString("\treturn textinput.Blink\n")
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

	return b.String(), gc.Imports()
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
		// Sync bound inputs
		for _, inp := range info.inputs {
			if inp.bindTarget == bind.name && bind.goType == "string" {
				fmt.Fprintf(b, "\tm.%s.SetValue(m.%s)\n", inp.fieldName, bind.name)
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
	b.WriteString("\tvar cmd tea.Cmd\n")
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
		rearm := fmt.Sprintf("cmd = tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} })", t.IntervalMs, t.Index)
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
		b.WriteString("\t\t\t\tcmd = tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} })\n")
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
	}

	// WindowSizeMsg
	b.WriteString("\tcase tea.WindowSizeMsg:\n")
	b.WriteString("\t\tm.width = msg.Width\n")
	b.WriteString("\t\tm.height = msg.Height\n")

	// KeyPressMsg
	b.WriteString("\tcase tea.KeyPressMsg:\n")
	b.WriteString("\t\tswitch {\n")
	b.WriteString("\t\tcase msg.Code == 'c' && msg.Mod == tea.ModCtrl:\n")
	b.WriteString("\t\t\treturn m, tea.Quit\n")

	if info.hasFocus {
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == 0:\n")
		b.WriteString("\t\t\tm.__focusNext()\n")
		emitIRFocusSync(b, info.inputs)
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == tea.ModShift:\n")
		b.WriteString("\t\t\tm.__focusPrev()\n")
		emitIRFocusSync(b, info.inputs)
	}

	// Button/checkbox enter handlers from IR
	wins := ctx.Windows()
	for _, win := range wins {
		emitIRButtonHandlers(b, win.Body, info, gc)
	}

	b.WriteString("\t\t}\n") // end switch
	b.WriteString("\t}\n")   // end type switch

	// Forward messages to focused input
	for _, inp := range info.inputs {
		if inp.focusExpr != "" {
			fmt.Fprintf(b, "\tif %s {\n", inp.focusExpr)
		} else {
			b.WriteString("\t{\n")
		}
		fmt.Fprintf(b, "\t\tm.%s, cmd = m.%s.Update(msg)\n", inp.fieldName, inp.fieldName)
		if inp.bindTarget != "" {
			fmt.Fprintf(b, "\t\tm.%s = m.%s.Value()\n", inp.bindTarget, inp.fieldName)
		}
		b.WriteString("\t}\n")
	}

	// Toast scheduling
	if info.NeedsToast {
		b.WriteString("\tif len(m.toasts) > 0 && cmd == nil {\n")
		b.WriteString("\t\tcmd = tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastDismissMsg{} })\n")
		b.WriteString("\t}\n")
	}

	b.WriteString("\treturn m, cmd\n")
	b.WriteString("}\n\n")
}

func emitIRButtonHandlers(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext) {
	emitIRButtonHandlersWalk(b, stmts, info, gc, nil)
}

// emitIRButtonHandlersWalk traverses visual IR emitting KeyEnter cases for
// button/checkbox handlers. currentFor is non-nil when inside a for-loop that
// passFocusOrder turned into a loop slot; handlers inside it are emitted with a
// loop-wrapped body that matches the cursor to the current iteration.
func emitIRButtonHandlersWalk(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, currentFor *ir.For) {
	emitStaticCase := func(slotIdx int, block []ir.Stmt) {
		fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.__focusID == %d:\n", slotIdx)
		for _, stmt := range block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t\t\t%s\n", line)
			}
		}
		syncMutatedInputs(b, block, info.inputs, gc)
	}
	emitLoopCase := func(slotIdx int, cursorVar, keyName, valName string, iterExpr string, block []ir.Stmt) {
		fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.__focusID == %d:\n", slotIdx)
		fmt.Fprintf(b, "\t\t\tfor %s, %s := range %s {\n", keyName, valName, iterExpr)
		fmt.Fprintf(b, "\t\t\t\tif m.%s == %s {\n", cursorVar, keyName)
		for _, stmt := range block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t\t\t\t\t%s\n", line)
			}
		}
		syncMutatedInputs(b, block, info.inputs, gc)
		b.WriteString("\t\t\t\t\tbreak\n")
		b.WriteString("\t\t\t\t}\n")
		b.WriteString("\t\t\t}\n")
	}

	emitNodeCase := func(n *ir.NodeInst, block []ir.Stmt) {
		if currentFor == nil {
			// Static slot.
			if idx := nodeFocusSlotIdx(n); idx >= 0 {
				emitStaticCase(idx, block)
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
		emitLoopCase(slotIdx, cursorIdent.Name, keyName, valName, iterExpr, block)
	}

	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.For:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, n)
		case *ir.If:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, currentFor)
			emitIRButtonHandlersWalk(b, n.Else, info, gc, currentFor)
		case *ir.PlatformFilter:
			emitIRButtonHandlersWalk(b, n.Body, info, gc, currentFor)
		case *ir.ErrorBoundary:
			emitIRButtonHandlersWalk(b, n.Children, info, gc, currentFor)
		case *ir.NodeInst:
			switch n.Name {
			case "checkbox":
				if h := codegen.NodeHandler(n, "change"); h != nil && h.Func != nil {
					emitNodeCase(n, h.Func.Block)
				}
			case "button":
				if h := codegen.NodeHandler(n, "click"); h != nil && h.Func != nil {
					emitNodeCase(n, h.Func.Block)
				}
			}
			emitIRButtonHandlersWalk(b, n.Children, info, gc, currentFor)
		case *ir.SlotInst:
			// Slot expansion happens elsewhere; no buttons inside the marker.
		case *ir.Window:
			panic(fmt.Sprintf("bubbletea: unexpected nested Window in handler walk: %#v", n))
		case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle:
			// Imperative stmts — no nested visual children to walk.
		case *ir.ContextProvider:
			panic(fmt.Sprintf("bubbletea: ContextProvider should be lowered before handler walk: %#v", n))
		default:
			panic(fmt.Sprintf("bubbletea.emitIRButtonHandlersWalk: unhandled ir.Stmt %T", n))
		}
	}
}

func emitIRFocusSync(b *strings.Builder, inputs []inputInfo) {
	for _, inp := range inputs {
		if inp.focusExpr == "" {
			continue
		}
		fmt.Fprintf(b, "\t\t\tif %s {\n", inp.focusExpr)
		fmt.Fprintf(b, "\t\t\t\tm.%s.Focus()\n", inp.fieldName)
		b.WriteString("\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\tm.%s.Blur()\n", inp.fieldName)
		b.WriteString("\t\t\t}\n")
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

func syncMutatedInputs(b *strings.Builder, stmts []ir.Stmt, inputs []inputInfo, gc *golang.GoIRContext) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for v := range codegen.MutatedFields(nil, nil, stmt) {
			mutated[v.Name] = true
		}
	}
	for _, inp := range inputs {
		if inp.bindTarget != "" && mutated[inp.bindTarget] {
			fmt.Fprintf(b, "\t\t\tm.%s.SetValue(m.%s)\n", inp.fieldName, inp.bindTarget)
		}
	}
}

// --- helpers ---

func irVarGoType(v *ir.Var) string {
	if v.Type != nil {
		return golang.IRTypeToGo(v.Type)
	}
	if v.Init != nil {
		return irExprGoType(v.Init)
	}
	return "any"
}

func irVarInit(v *ir.Var, gc *golang.GoIRContext) string {
	return golang.LowerVarInit(v, gc)
}

func irFuncReturnType(f *ir.Func) string {
	if f.Return != nil {
		return golang.IRTypeToGo(f.Return)
	}
	// Infer from expression body (single-return functions)
	if len(f.Block) == 1 {
		if ret, ok := f.Block[0].(*ir.Return); ok && ret.Value != nil {
			return irExprGoType(ret.Value)
		}
	}
	return ""
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
