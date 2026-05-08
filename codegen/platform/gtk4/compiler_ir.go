package gtk4

import (
	"fmt"
	"go/format"
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
	name   string
	goType string
	init   string
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
		info.goImports[golang.SnglLibImportPath] = true
	}

	allVars := pkg.Vars
	if main := ctx.MainComponent(); main != nil {
		allVars = append(allVars, main.Vars...)
	}
	for _, v := range allVars {
		if v.IsConst {
			continue
		}
		goType := irVarGoType(v)
		initVal := irVarInit(v)
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

	// --- Phase 1: Render BuildUI body into a buffer ---
	var buildBuf strings.Builder
	vc := &viewContext{
		gc:         gc,
		registry:   c.registry,
		ctx:        c.ctx,
		buf:        &buildBuf,
		indent:     1,
		depTracker: c.info.depTracker(),
	}

	bodyStmts := mainBodyStmts(c.ctx)
	if len(bodyStmts) > 0 {
		vc.line("var content *C.GtkWidget")
		if len(bodyStmts) == 1 {
			vc.renderStmt(bodyStmts[0], "content")
		} else {
			// Multi-root: wrap in a vertical box.
			vc.line("box := C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 0)")
			for i, child := range bodyStmts {
				childVar := fmt.Sprintf("child%d", i)
				vc.line("var %s *C.GtkWidget", childVar)
				vc.renderStmt(child, childVar)
				vc.line("if %s != nil { C.gtk_box_append((*C.GtkBox)(unsafe.Pointer(box)), %s) }", childVar, childVar)
			}
			vc.line("content = box")
		}
	}

	widgetFields := vc.fields
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
		emitGTK4Func(&funcBuf, fn, gc)
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
	emitBuildUI(&modelBuf, &buildBuf)
	emitUpdaters(&modelBuf, updaters)

	// --- Phase 6: Append main() to callbacks.go (NOT model.go — cgo //export
	// directives can't coexist with the model.go preamble's static defs). ---
	if c.cfg.Main {
		emitGTK4Main(&callbacksBuf, c.cfg)
	}

	return []byte(modelBuf.String()), []byte(callbacksBuf.String()), nil
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
			Name:    bind.name,
			GoType:  bind.goType,
			InitVal: bind.init,
			Getter:  golang.ExportName(bind.name),
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
	goName := golang.ExportName(fn.Name)
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
// It expects the body buffer to set up `content *C.GtkWidget`.
func emitBuildUI(b *strings.Builder, buildBuf *strings.Builder) {
	b.WriteString("// BuildUI constructs the widget tree and returns the top-level window.\n")
	b.WriteString("func (m *Model) BuildUI(app *C.GtkApplication) *C.GtkWidget {\n")
	if buildBuf.Len() == 0 {
		b.WriteString("\twin := C.gtk_application_window_new(app)\n")
		b.WriteString("\treturn win\n")
		b.WriteString("}\n\n")
		return
	}
	b.WriteString(buildBuf.String())
	// If `content` is itself a window (GtkApplicationWindow), return it as-is.
	// Otherwise wrap it in a new application window.
	b.WriteString("\tif content == nil {\n")
	b.WriteString("\t\tcontent = (*C.GtkWidget)(unsafe.Pointer(C.gtk_application_window_new(app)))\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn content\n")
	b.WriteString("}\n\n")
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
	b.WriteString("\tC.gtk_widget_set_visible(win, 1)\n")
	b.WriteString("}\n\n")
	b.WriteString("func main() {\n")
	b.WriteString("\truntime.LockOSThread()\n")
	b.WriteString("\tapp := C.gtk_application_new(nil, C.G_APPLICATION_DEFAULT_FLAGS)\n")
	b.WriteString("\tC.g_signal_connect_data((C.gpointer)(unsafe.Pointer(app)),\n")
	b.WriteString("\t\tC.CString(\"activate\"),\n")
	b.WriteString("\t\tC.GCallback(C.snglActivate), nil, nil, 0)\n")
	b.WriteString("\tC.g_application_run((*C.GApplication)(unsafe.Pointer(app)), 0, nil)\n")
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

func irVarInit(v *ir.Var) string {
	if v.Init == nil {
		return golang.ZeroValueGo(golang.IRTypeToGo(v.Type))
	}
	return golang.IRLiteralToGo(v.Init)
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
