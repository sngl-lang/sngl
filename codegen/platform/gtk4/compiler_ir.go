package gtk4

import (
	"context"
	"fmt"
	"go/format"
	"sort"
	"strings"

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

var _ codegen.MutationModelEmitter = (*compilation)(nil)

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

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request) (*codegen.Response, error) {
	modelSrc, callbacksSrc, err := c.emitIR()
	if err != nil {
		return nil, err
	}

	modelFormatted, err := format.Source(modelSrc)
	if err != nil {
		return &codegen.Response{Error: fmt.Sprintf("gtk4 model.go formatting error: %v\n%s", err, modelSrc)}, nil
	}
	callbacksFormatted, err := format.Source(callbacksSrc)
	if err != nil {
		return &codegen.Response{Error: fmt.Sprintf("gtk4 callbacks.go formatting error: %v\n%s", err, callbacksSrc)}, nil
	}

	if h := codegen.Header("gtk4", req.Source, "// ", ""); h != "" {
		modelFormatted = append([]byte(h), modelFormatted...)
		callbacksFormatted = append([]byte(h), callbacksFormatted...)
	}

	return &codegen.Response{
		Files: []*codegen.OutputFile{
			codegen.BytesFile("model.go", modelFormatted),
			codegen.BytesFile("callbacks.go", callbacksFormatted),
		},
	}, nil
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
	if len(bodyStmts) > 0 {
		tr := newGtk4Translator(gc, func(name, cType string) {
			widgetFields = append(widgetFields, widgetField{name: name, goType: "*C." + cType})
		})
		body := codegen.WalkLowered(context.Background(), bodyStmts, tr)
		for _, stmt := range body {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(&buildBuf, "\t%s\n", line)
			}
		}
		topLevelRefs = tr.topLevel
	}

	updaters := vc.updaters

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
			emitIRSlotFunc(&funcBuf, fn, gc, &widgetFields)
			continue
		}
		emitGTK4Func(&funcBuf, fn, gc)
	}

	// Detect fmt usage in body/funcs (interpolation lowers to fmt.Sprint).
	if strings.Contains(buildBuf.String(), "fmt.") || strings.Contains(funcBuf.String(), "fmt.") {
		c.info.goImports["fmt"] = true
	}

	// --- Phase 3: Build template data ---
	td, err := c.newTemplateData(updaters, widgetFields, funcBuf.String(), gc)
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
	emitBuildUI(&modelBuf, &buildBuf, topLevelRefs)
	emitUpdaters(&modelBuf, updaters)
	emitEventInvokers(&modelBuf, vc.eventInvokers)
	emitPropertyReaders(&modelBuf, vc.nodeBindings)
	emitConditionalRefs(&modelBuf, vc.conditionalIDs)
	emitIfRefreshMethods(&modelBuf, vc.ifBlocks, gc)
	emitForRefreshMethods(&modelBuf, vc.forBlocks, gc)

	// --- Phase 6: Append main() to callbacks.go (NOT model.go — cgo //export
	// directives can't coexist with the model.go preamble's static defs). ---
	if c.cfg.Main {
		emitGTK4Main(&callbacksBuf, c.cfg)
	}

	// Build a refresh body that re-applies every recorded
	// reactive (nodeID, prop) binding from current state — used by
	// SetX setters (via doRefresh) so direct field mutation in tests
	// triggers the same widget updates that lowered handler bodies do.
	refreshBody := vc.buildReactiveRefresh()
	out := strings.Replace(modelBuf.String(), "/*REACTIVE_REFRESH*/", refreshBody, 1)

	// Resolve /*SNGLREACT:i*/ placeholders recorded during the visual
	// walk once every node's bindings have been registered.
	modelSrc = []byte(vc.resolveReactiveTokens(out))
	return modelSrc, []byte(callbacksBuf.String()), nil
}

func (c *compilation) newTemplateData(updaters []widgetUpdater, widgetFields []widgetField, functionCode string, gc *golang.GoIRContext) (templateData, error) {
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

	// Binds: collect setter side-effects for affected updaters.
	for _, bind := range c.info.binds {
		bd := bindData{
			Name:        bind.name,
			GoType:      bind.goType,
			InitVal:     bind.init,
			Getter:      golang.ExportName(bind.name),
			NoAccessors: bind.noAccessors,
		}
		var extra strings.Builder
		mutated := map[string]bool{bind.name: true}
		affected := codegen.FindAffected(c.info.depTracker(), updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				fmt.Fprintf(&extra, "\tm.%s()\n", u.name)
			}
		} else {
			extra.WriteString("\tm.doRefresh()\n")
		}
		bd.SetterExtra = extra.String()
		td.Binds = append(td.Binds, bd)
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

	// Updater names — used by doRefresh().
	for _, u := range updaters {
		td.UpdaterNames = append(td.UpdaterNames, u.name)
	}

	return td, nil
}

// emitIRSlotFunc emits a passReactivity-synthesized __renderSlot<N>
// Func as a Model method. The body is a mix of plain Go statements
// (for-teardown, Assign reset, If gate) and lower.* intrinsic calls.
// codegen.WalkLowered routes intrinsic shapes through gtk4Translator
// into ir.Stmt fragments; we then feed them through gc.EvalStmt at
// the source-emission boundary.
func emitIRSlotFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, widgetFields *[]widgetField) {
	fmt.Fprintf(b, "func (m *Model) %s(container *C.GtkBox) {\n", fn.Name)
	tr := newGtk4Translator(gc, func(name, cType string) {
		*widgetFields = append(*widgetFields, widgetField{name: name, goType: "*C." + cType})
	})
	body := codegen.WalkLowered(context.Background(), fn.Block, tr)
	for _, stmt := range body {
		for _, line := range gc.EvalStmt(stmt) {
			b.WriteString("\t")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("}\n\n")
}

// emitGTK4Func emits a top-level user function as a method on *Model.
func emitGTK4Func(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = p.Name + " " + golang.IRTypeToGo(p.Type)
	}
	retType := ""
	if fn.Return != nil && fn.Return.Kind != ir.TypeDyn {
		retType = golang.IRTypeToGo(fn.Return)
	}
	// Lowercase name to match computed-method convention so tests can
	// invoke c.<name>(...) uniformly across platforms.
	goName := fn.Name
	localGC := gc
	for _, p := range fn.Params {
		localGC = localGC.WithLocal(p.Name)
	}
	if len(fn.Block) == 0 {
		return
	}
	fmt.Fprintf(b, "func (m *Model) %s(%s) %s {\n", goName, strings.Join(params, ", "), retType)
	for _, stmt := range fn.Block {
		for _, line := range localGC.EvalStmt(stmt) {
			fmt.Fprintf(b, "\t%s\n", line)
		}
	}
	b.WriteString("}\n\n")
}

// emitBuildUI emits BuildUI(app *C.GtkApplication) *C.GtkWidget.
// With NoDeclarative on, the body buffer is a flat stream of intrinsic
// calls (CreateNode → m.<id> = ctor; AppendChild → gtk_box_append; etc.)
// translated by gtk4Translator. Top-level widget refs that weren't
// consumed by an AppendChild get parented into m.__root, which BuildUI
// initializes lazily and embeds in a GtkApplicationWindow.
func emitBuildUI(b *strings.Builder, buildBuf *strings.Builder, topLevelRefs []string) {
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
	if buildBuf.Len() == 0 && len(topLevelRefs) == 0 {
		b.WriteString("\twin := C.gtk_application_window_new(app)\n")
		b.WriteString("\treturn win\n")
		b.WriteString("}\n\n")
		return
	}
	// Lazy-init __root — cgo calls aren't valid in field initializers,
	// so the binds-loop in New() puts nil there and BuildUI promotes it.
	b.WriteString("\tif m.__root == nil {\n")
	b.WriteString("\t\tm.__root = (*C.GtkBox)(unsafe.Pointer(C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 6)))\n")
	b.WriteString("\t}\n")
	b.WriteString(buildBuf.String())
	for _, ref := range topLevelRefs {
		fmt.Fprintf(b, "\tC.gtk_box_append((*C.GtkBox)(unsafe.Pointer(m.__root)), (*C.GtkWidget)(unsafe.Pointer(m.%s)))\n", ref)
	}
	b.WriteString("\twin := C.gtk_application_window_new(app)\n")
	b.WriteString("\tC.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), 480, 640)\n")
	b.WriteString("\tC.gtk_window_set_child((*C.GtkWindow)(unsafe.Pointer(win)), (*C.GtkWidget)(unsafe.Pointer(m.__root)))\n")
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

// emitIfRefreshMethods emits one `(m *Model) refreshIfN()` per
// recorded `if` block. The method evaluates the cond, mounts when
// it flipped true (running the captured body), unmounts when it
// flipped false (unparenting top-level children + nulling the
// conditional refs they own). Idempotent — safe to call from both
// BuildUI's initial pass and doRefresh after every mutation.
func emitIfRefreshMethods(b *strings.Builder, blocks []ifBlockInfo, gc *golang.GoIRContext) {
	for _, blk := range blocks {
		cond := gc.EvalExpr(blk.Cond)
		fmt.Fprintf(b, "func (m *Model) %s() {\n", blk.MethodName)
		fmt.Fprintf(b, "\t_cond := %s\n", cond)
		fmt.Fprintf(b, "\tif _cond && !m.%s {\n", blk.MountedField)
		b.WriteString(blk.MountBody)
		b.WriteString("\t} else if !_cond && m.")
		b.WriteString(blk.MountedField)
		b.WriteString(" {\n")
		// Unparent every model field that received a widget pointer
		// during this block's body. We use UnmountNulls which lists
		// conditional-ref fields (label, etc.) — same set we null below.
		for _, fld := range blk.UnmountNulls {
			fmt.Fprintf(b, "\t\tif m.%s != nil { C.gtk_widget_unparent((*C.GtkWidget)(unsafe.Pointer(m.%s))) }\n", fld, fld)
		}
		for _, fld := range blk.UnmountNulls {
			fmt.Fprintf(b, "\t\tm.%s = nil\n", fld)
		}
		fmt.Fprintf(b, "\t\tm.%s = false\n", blk.MountedField)
		b.WriteString("\t}\n")
		b.WriteString("}\n\n")
	}
}

func sortedPropNames(m map[string]gtkBinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// emitForRefreshMethods emits one `(m *Model) refreshForN()` per
// recorded `for` block plus the ref struct (`<id>Ref` with widget
// pointer + per-prop Value() getter) and a `(m *Model) <id>()
// []*<id>Ref` list reader. Each refresh tears down the prior
// iteration widgets (unparent + clear list) and re-builds from the
// current iter expression — wired into the reactive refresh body so
// mutations of the underlying list propagate.
func emitForRefreshMethods(b *strings.Builder, blocks []forBlockInfo, gc *golang.GoIRContext) {
	for _, blk := range blocks {
		// Ref struct + per-prop getter methods per id. The set of
		// methods mirrors the bindings captured by the (refactored)
		// renderStdlib* code in local mode — same getter info the
		// field-mode property readers consume, so coverage stays in
		// step across widget types.
		for _, info := range blk.IDs {
			fmt.Fprintf(b, "type %s struct { w *C.%s }\n", info.RefType, info.CType)
			seen := map[string]bool{}
			for _, prop := range sortedPropNames(info.Props) {
				bnd := info.Props[prop]
				if bnd.Getter == "" {
					continue
				}
				methodName := golang.ExportName(prop)
				if seen[methodName] {
					continue
				}
				seen[methodName] = true
				retType := "string"
				conv := "C.GoString"
				if bnd.ValueIRType != nil && bnd.ValueIRType.Kind == ir.TypeBool {
					retType = "bool"
					conv = ""
				}
				cast := bnd.GetterCType
				if cast == "" {
					cast = bnd.CType
				}
				fmt.Fprintf(b, "func (r *%s) %s() %s {\n", info.RefType, methodName, retType)
				call := fmt.Sprintf("C.%s((*C.%s)(unsafe.Pointer(r.w)))", bnd.Getter, cast)
				if conv != "" {
					fmt.Fprintf(b, "\treturn %s(%s)\n", conv, call)
				} else if retType == "bool" {
					fmt.Fprintf(b, "\treturn %s != 0\n", call)
				} else {
					fmt.Fprintf(b, "\treturn %s\n", call)
				}
				b.WriteString("}\n\n")
			}
			fmt.Fprintf(b, "// %s returns the current per-iteration refs for #%s; for tests.\n", info.ID, info.ID)
			fmt.Fprintf(b, "func (m *Model) %s() []*%s {\n\treturn m.%s\n}\n\n",
				info.ID, info.RefType, info.ListField)
		}
		// Refresh method: unparent + clear list, then for-range iter.
		iterStr := gc.EvalExpr(blk.IterExpr)
		fmt.Fprintf(b, "func (m *Model) %s() {\n", blk.MethodName)
		for _, info := range blk.IDs {
			fmt.Fprintf(b, "\tfor _, r := range m.%s {\n", info.ListField)
			b.WriteString("\t\tif r.w != nil { C.gtk_widget_unparent((*C.GtkWidget)(unsafe.Pointer(r.w))) }\n")
			b.WriteString("\t}\n")
			fmt.Fprintf(b, "\tm.%s = nil\n", info.ListField)
		}
		// IR For on a list: Key holds the value var, Value holds the
		// optional index var. Mirrors translateIRForGo's mapping.
		indexVar := "_"
		valueVar := blk.KeyVar
		if blk.ValueVar != "" {
			indexVar = blk.ValueVar
		}
		if valueVar == "" {
			valueVar = "_"
		}
		fmt.Fprintf(b, "\tfor %s, %s := range %s {\n", indexVar, valueVar, iterStr)
		if indexVar != "_" {
			fmt.Fprintf(b, "\t\t_ = %s\n", indexVar)
		}
		if valueVar != "_" {
			fmt.Fprintf(b, "\t\t_ = %s\n", valueVar)
		}
		b.WriteString(blk.Body)
		b.WriteString("\t}\n")
		b.WriteString("}\n\n")
	}
}

// emitConditionalRefs emits a `(m *Model) <id>() *C.<CType>` reader
// for every user-id'd widget materialized inside an `if` branch.
// Returns the widget pointer (nil when the branch hasn't run). Tests
// drive `c.<id> == null` / `!= null` through these methods. Widget
// teardown when the if-cond flips false is out of scope for now —
// the pointer stays non-nil until the branch is re-walked, which the
// gtk4 platform doesn't do yet.
func emitConditionalRefs(b *strings.Builder, refs map[string]conditionalRef) {
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := refs[id]
		fmt.Fprintf(b, "// %s returns the #%s widget when its `if` branch is mounted; for tests.\n", id, id)
		fmt.Fprintf(b, "func (m *Model) %s() *C.%s {\n", id, r.CType)
		fmt.Fprintf(b, "\treturn m.%s\n", r.Field)
		b.WriteString("}\n\n")
	}
}

// emitPropertyReaders emits one Model method per (#id, propName) that
// reads the underlying widget's current value via gtk_<widget>_get_<prop>.
// Tests use them to inspect state after firing events: testlower
// translates `c.<id>.<prop>` into `c.<id><Prop>()`. Only user-set ids
// (synthetic __nN ids skipped) produce readers, and only props with a
// registered getter in gtkGetterTable.
func emitPropertyReaders(b *strings.Builder, bindings map[string]map[string]gtkBinding) {
	// Stable iteration: sort by id, then prop, so repeated compiles
	// produce identical output.
	ids := make([]string, 0, len(bindings))
	for id := range bindings {
		if strings.HasPrefix(id, "__n") || id == "" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	for _, id := range ids {
		props := bindings[id]
		names := make([]string, 0, len(props))
		for p := range props {
			names = append(names, p)
		}
		sort.Strings(names)
		for _, prop := range names {
			binding := props[prop]
			if binding.Getter == "" {
				continue
			}
			methodName := id + golang.ExportName(prop)
			if seen[methodName] {
				continue
			}
			seen[methodName] = true
			retType := "string"
			conv := "C.GoString"
			if binding.ValueIRType != nil && binding.ValueIRType.Kind == ir.TypeBool {
				retType = "bool"
				conv = "" // gtk_*_get_active returns gboolean (C.int) — handle below
			}
			fmt.Fprintf(b, "// %s reads %q on #%s; for tests.\n", methodName, binding.Getter, id)
			fmt.Fprintf(b, "func (m *Model) %s() %s {\n", methodName, retType)
			cast := binding.GetterCType
			if cast == "" {
				cast = binding.CType
			}
			call := fmt.Sprintf("C.%s((*C.%s)(unsafe.Pointer(m.%s)))", binding.Getter, cast, binding.Field)
			if conv != "" {
				fmt.Fprintf(b, "\treturn %s(%s)\n", conv, call)
			} else if retType == "bool" {
				fmt.Fprintf(b, "\treturn %s != 0\n", call)
			} else {
				fmt.Fprintf(b, "\treturn %s\n", call)
			}
			b.WriteString("}\n\n")
		}
	}
}

func emitUpdaters(b *strings.Builder, updaters []widgetUpdater) {
	for _, u := range updaters {
		fmt.Fprintf(b, "func (m *Model) %s() {\n", u.name)
		fmt.Fprintf(b, "\t%s\n", u.body)
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
