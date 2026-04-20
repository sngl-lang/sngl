package bubbletea

import (
	"fmt"
	"go/format"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// CompileIR generates a Go source file from IR using the new CodegenCtx.
func CompileIR(ctx *codegen.CodegenCtx, cfg Config) ([]byte, error) {
	cfg = cfg.withDefaults()
	info := analyzeIR(ctx)
	src := emitIR(info, ctx, cfg)
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("generated code formatting error: %w\n%s", err, src)
	}
	return formatted, nil
}

// irAnalysis is the IR-based replacement for analysisResult.
type irAnalysis struct {
	*codegen.CommonAnalysis
	binds      []irBind
	externs    []irExtern
	computeds  []irComputed
	inputs     []inputInfo
	focusables []string
	forCursors []forLoopCursor
	goImports  map[string]string
	needsTime  bool
}

type irBind struct {
	name   string
	goType string
	init   string // Go expression
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
	info := &irAnalysis{
		CommonAnalysis: ctx.Analysis,
		goImports:      make(map[string]string),
	}

	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)
	_ = gc // used below for init values
	pkg := ctx.Pkg

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
		initVal := irVarInit(v, gc)
		if golang.NeedsTimeType(goType) {
			info.needsTime = true
		}
		info.binds = append(info.binds, irBind{
			name:   v.Name,
			goType: goType,
			init:   initVal,
		})
	}

	// Collect computed functions
	allFuncs := pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	for _, f := range allFuncs {
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
			info.needsTime = true
		}
	}

	// Walk visual tree for inputs/buttons/checkboxes
	wins := ctx.Windows()
	for _, win := range wins {
		focusIdx := 0
		codegen.WalkVisualTree(win.Body, func(n *ir.NodeInst, _ int) bool {
			switch n.Name {
			case "input":
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
				})
				info.focusables = append(info.focusables, fieldName)
				focusIdx++
			case "button":
				btnName := ctx.Namer.Next("button")
				info.focusables = append(info.focusables, btnName)
				focusIdx++
			case "checkbox":
				chkName := ctx.Namer.Next("checkbox")
				info.focusables = append(info.focusables, chkName)
				focusIdx++
			}
			return false
		})
	}

	if info.NeedsToast {
		info.needsTime = true
	}

	return info
}

func emitIR(info *irAnalysis, ctx *codegen.CodegenCtx, cfg Config) []byte {
	var b strings.Builder
	exprCtx := ctx.ExprCtx
	if main := ctx.MainComponent(); main != nil {
		exprCtx = exprCtx.ForComponent(main)
	}
	gc := golang.NewIRContext(exprCtx)

	// Package
	fmt.Fprintf(&b, "package %s\n\n", cfg.Package)

	// Imports
	b.WriteString("import (\n")
	b.WriteString("\t\"fmt\"\n")
	if cfg.GenerateMain {
		b.WriteString("\t\"os\"\n")
	}
	b.WriteString("\t\"strings\"\n")
	if info.needsTime {
		b.WriteString("\t\"time\"\n")
	}
	if len(info.goImports) > 0 {
		var pkgs []string
		for pkg := range info.goImports {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		for _, pkg := range pkgs {
			ns := info.goImports[pkg]
			fmt.Fprintf(&b, "\t%s %q\n", ns, pkg)
		}
	}
	b.WriteString("\n")
	b.WriteString("\ttea \"charm.land/bubbletea/v2\"\n")
	b.WriteString("\t\"charm.land/lipgloss/v2\"\n")
	if len(info.inputs) > 0 {
		b.WriteString("\t\"charm.land/bubbles/v2/textinput\"\n")
	}
	b.WriteString(")\n\n")

	// Suppress unused imports
	b.WriteString("var _ = fmt.Sprint\n")
	b.WriteString("var _ = strings.Join\n")
	b.WriteString("var _ = lipgloss.NewStyle\n\n")

	// Ternary helper
	b.WriteString("func ternary[T any](cond bool, a, b T) T {\n")
	b.WriteString("\tif cond {\n\t\treturn a\n\t}\n\treturn b\n}\n\n")

	// Time helpers
	if info.needsTime {
		b.WriteString("func mustParseDuration(s string) time.Duration {\n")
		b.WriteString("\td, err := time.ParseDuration(s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn d\n}\n\n")

		b.WriteString("func mustParseDate(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(\"2006-01-02\", s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn t\n}\n\n")

		b.WriteString("func mustParseTime(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(\"15:04:05\", s)\n")
		b.WriteString("\tif err != nil {\n")
		b.WriteString("\t\tt, err = time.Parse(\"15:04\", s)\n")
		b.WriteString("\t\tif err != nil { panic(err) }\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn t\n}\n\n")

		b.WriteString("func mustParseDateTime(s string) time.Time {\n")
		b.WriteString("\tt, err := time.Parse(time.RFC3339, s)\n")
		b.WriteString("\tif err != nil { panic(err) }\n")
		b.WriteString("\treturn t\n}\n\n")
	}

	// Struct types
	for _, sd := range info.Structs {
		fmt.Fprintf(&b, "type %s struct {\n", golang.ExportName(sd.Name))
		for _, f := range sd.Fields {
			goType := golang.IRTypeToGo(f.Type)
			fmt.Fprintf(&b, "\t%s %s\n", golang.ExportName(f.Name), goType)
		}
		b.WriteString("}\n\n")
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
	for _, fc := range info.forCursors {
		fmt.Fprintf(&b, "\t%s int\n", fc.cursorField)
	}
	if len(info.forCursors) > 0 {
		b.WriteString("\n")
	}
	if info.NeedsToast {
		b.WriteString("\ttoasts []snglToast\n")
	}
	b.WriteString("\tfocus int\n")
	b.WriteString("\twidth, height int\n")
	b.WriteString("}\n\n")

	// New()
	b.WriteString("// New creates a Model with default bind values.\n")
	b.WriteString("func New() Model {\n")
	b.WriteString("\tm := Model{\n")
	for _, bind := range info.binds {
		fmt.Fprintf(&b, "\t\t%s: %s,\n", bind.name, bind.init)
	}
	b.WriteString("\t}\n")
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

	// Computed methods
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
		fmt.Fprintf(&b, "func (m Model) %s() %s {\n", comp.name, comp.goType)
		fmt.Fprintf(&b, "\treturn %s\n", body)
		b.WriteString("}\n\n")
	}

	// User-defined functions
	allFuncs := ctx.Pkg.Funcs
	if main := ctx.MainComponent(); main != nil {
		allFuncs = append(allFuncs, main.Funcs...)
	}
	for _, fn := range allFuncs {
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
			fmt.Fprintf(&b, "\tif m.%s {\n", t.ActiveVar)
			fmt.Fprintf(&b, "\t\tcmds = append(cmds, tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} }))\n", t.IntervalMs, t.Index)
			b.WriteString("\t}\n")
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
	if cfg.GenerateMain {
		b.WriteString("func main() {\n")
		b.WriteString("\tp := tea.NewProgram(New())\n")
		b.WriteString("\tif _, err := p.Run(); err != nil {\n")
		b.WriteString("\t\tfmt.Fprintf(os.Stderr, \"error: %v\\n\", err)\n")
		b.WriteString("\t\tos.Exit(1)\n")
		b.WriteString("\t}\n")
		b.WriteString("}\n")
	}

	return []byte(b.String())
}

func emitIRFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		goType := golang.IRTypeToGo(p.Type)
		params[i] = p.Name + " " + goType
	}
	paramStr := strings.Join(params, ", ")

	retType := ""
	if fn.Return != nil {
		retType = golang.IRTypeToGo(fn.Return)
	}

	receiver := "m Model"
	if fn.Return == nil {
		receiver = "m *Model"
	}

	goName := golang.ExportName(fn.Name)

	// Add params as locals
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

func emitIRGettersSetters(b *strings.Builder, info *irAnalysis, ctx *codegen.CodegenCtx, gc *golang.GoIRContext) {
	for _, bind := range info.binds {
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

	// Set messages
	for _, bind := range info.binds {
		getter := golang.ExportName(bind.name)
		fmt.Fprintf(b, "\tcase set%sMsg:\n", getter)
		fmt.Fprintf(b, "\t\tm = m.Set%s(msg.value)\n", getter)
	}

	// Timer ticks
	for _, t := range info.Timers {
		fmt.Fprintf(b, "\tcase timerTickMsg%d:\n", t.Index)
		fmt.Fprintf(b, "\t\tif m.%s {\n", t.ActiveVar)
		for _, bodyStmt := range t.Body {
			for _, line := range gc.EvalStmt(bodyStmt) {
				fmt.Fprintf(b, "\t\t\t%s\n", line)
			}
		}
		fmt.Fprintf(b, "\t\t\tif m.%s {\n", t.ActiveVar)
		fmt.Fprintf(b, "\t\t\t\tcmd = tea.Tick(%d*time.Millisecond, func(time.Time) tea.Msg { return timerTickMsg%d{} })\n", t.IntervalMs, t.Index)
		b.WriteString("\t\t\t}\n")
		b.WriteString("\t\t}\n")
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

	if len(info.focusables) > 1 {
		nFocus := len(info.focusables)
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == 0:\n")
		fmt.Fprintf(b, "\t\t\tm.focus = (m.focus + 1) %% %d\n", nFocus)
		emitIRFocusSync(b, info.inputs)
		b.WriteString("\t\tcase msg.Code == tea.KeyTab && msg.Mod == tea.ModShift:\n")
		fmt.Fprintf(b, "\t\t\tm.focus = (m.focus - 1 + %d) %% %d\n", nFocus, nFocus)
		emitIRFocusSync(b, info.inputs)
	}

	// Button/checkbox enter handlers from IR
	wins := ctx.Windows()
	for _, win := range wins {
		buttonIdx := 0
		checkboxIdx := 0
		emitIRButtonHandlers(b, win.Body, info, gc, &buttonIdx, &checkboxIdx)
	}

	b.WriteString("\t\t}\n") // end switch
	b.WriteString("\t}\n")   // end type switch

	// Forward messages to focused input
	for i, inp := range info.inputs {
		fmt.Fprintf(b, "\tif m.focus == %d {\n", i)
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

func emitIRButtonHandlers(b *strings.Builder, stmts []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, buttonIdx *int, checkboxIdx *int) {
	codegen.WalkVisualTree(stmts, func(n *ir.NodeInst, _ int) bool {
		switch n.Name {
		case "checkbox":
			if h := codegen.NodeHandler(n, "change"); h != nil && h.Func != nil {
				focusIdx := findFocusIndex(info.focusables, fmt.Sprintf("checkbox%d", *checkboxIdx))
				if focusIdx >= 0 {
					fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
					for _, stmt := range h.Func.Block {
						for _, line := range gc.EvalStmt(stmt) {
							fmt.Fprintf(b, "\t\t\t%s\n", line)
						}
					}
					syncMutatedInputs(b, h.Func.Block, info.inputs, gc)
				}
			}
			*checkboxIdx++
		case "button":
			if h := codegen.NodeHandler(n, "click"); h != nil && h.Func != nil {
				focusIdx := findFocusIndex(info.focusables, fmt.Sprintf("button%d", *buttonIdx))
				if focusIdx >= 0 {
					fmt.Fprintf(b, "\t\tcase msg.Code == tea.KeyEnter && m.focus == %d:\n", focusIdx)
					for _, stmt := range h.Func.Block {
						for _, line := range gc.EvalStmt(stmt) {
							fmt.Fprintf(b, "\t\t\t%s\n", line)
						}
					}
					syncMutatedInputs(b, h.Func.Block, info.inputs, gc)
				}
			}
			*buttonIdx++
		}
		return false
	})
}

func emitIRFocusSync(b *strings.Builder, inputs []inputInfo) {
	for i, inp := range inputs {
		fmt.Fprintf(b, "\t\t\tif m.focus == %d {\n", i)
		fmt.Fprintf(b, "\t\t\t\tm.%s.Focus()\n", inp.fieldName)
		b.WriteString("\t\t\t} else {\n")
		fmt.Fprintf(b, "\t\t\t\tm.%s.Blur()\n", inp.fieldName)
		b.WriteString("\t\t\t}\n")
	}
}

func findFocusIndex(focusables []string, name string) int {
	for i, f := range focusables {
		if f == name {
			return i
		}
	}
	return -1
}

func syncMutatedInputs(b *strings.Builder, stmts []ir.Stmt, inputs []inputInfo, gc *golang.GoIRContext) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for k, v := range codegen.MutatedFields(stmt) {
			mutated[k] = v
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
	if v.Init == nil {
		return golang.ZeroValueGo(golang.IRTypeToGo(v.Type))
	}
	// Handle special types that need runtime parsing
	if lit, ok := v.Init.(*ir.Literal); ok && lit.Type != nil {
		switch lit.Type.Kind {
		case ir.TypeDuration:
			return fmt.Sprintf("mustParseDuration(%q)", lit.Raw+lit.Suffix)
		case ir.TypeDate:
			return fmt.Sprintf("mustParseDate(%s)", fmt.Sprintf("%q", lit.Raw))
		case ir.TypeTime:
			return fmt.Sprintf("mustParseTime(%s)", fmt.Sprintf("%q", lit.Raw))
		case ir.TypeDateTime:
			return fmt.Sprintf("mustParseDateTime(%s)", fmt.Sprintf("%q", lit.Raw))
		}
	}
	return golang.IRLiteralToGo(v.Init)
}

func irFuncReturnType(f *ir.Func) string {
	if f.Return != nil {
		return golang.IRTypeToGo(f.Return)
	}
	// Infer from expression body
	if len(f.Block) == 1 {
		if ret, ok := f.Block[0].(*ir.Return); ok && ret.Value != nil {
			return irExprGoType(ret.Value)
		}
	}
	return "any"
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
