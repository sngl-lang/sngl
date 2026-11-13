package golang

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/ir"
)

// WriteProgram writes pkg into fe whole, as the C language's WriteProgram
// does: a program passPlacement left plain, whose every node operation is
// already a call of its platform's protocol functions, so nothing here knows
// a platform. fe is one this language made (Translator.NewFileEmitter).
//
// The program's types are declared; each package var is a field of the one
// state the file holds, `m`, since a test sets the program's state afresh
// before it builds and a function reads a var as `m.name`; each const is a
// file-scope var; each function the program declares is a free function;
// snglInit sets the vars, snglBuild is the package body, snglTeardown the
// program's Teardown and snglRun, where RunsHandler says, its `@run`
// handler. The platform writes the entry that calls them.
func WriteProgram(fe codegen.FileEmitter, pkg *ir.Package, platform string) error {
	f, ok := fe.(*fileEmitter)
	if !ok {
		return fmt.Errorf("golang.WriteProgram: %T is not a Go file", fe)
	}
	gc := f.gc
	// A `#[c.native]` the program calls directly -- its own `c:` import, a
	// class no wrapper covers -- is reached through the headers its c.link
	// directives name; the platform's own C is its wrappers'.
	if f.opts.CgoPreamble == "" {
		if links := CLinks(pkg); len(links) > 0 {
			f.opts.CgoPreamble = (&Translator{}).EmitCHeader(links)
		}
	}
	gc.Ctx = codegen.NewExprCtx(pkg)
	gc.Ctx.Platform = platform
	gc.Ctx.FreeBody = true
	gc.FreeFuncScope = true
	gc.Ctx.FreeFuncs = placedFreeFuncs(pkg)
	gc.AlertFunc = placedAlert

	a := codegen.AnalyzeCommon(pkg)
	var b strings.Builder
	helpers := HelpersNeeded(pkg)
	for _, imp := range helpers.Imports() {
		gc.RequireImport(imp)
	}
	b.WriteString(helpers.Emit())
	if pkg.UsesErrorHandling {
		b.WriteString(ErrorEventDecl)
	}
	b.WriteString(EmitMergeFuncs(pkg.MergeStructs, pkg.MergeSets))
	b.WriteString(EmitUnitTypeDecls(a.Units))
	b.WriteString(EmitEnumTypeDecls(a.Enums))
	for _, sd := range a.Structs {
		fmt.Fprintf(&b, "type %s struct {\n", ExportName(sd.Name))
		for _, fd := range sd.Fields {
			for _, p := range TypeImports(fd.Type) {
				gc.RequireImport(p)
			}
			fmt.Fprintf(&b, "\t%s %s\n", ExportName(fd.Name), IRTypeToGo(fd.Type))
		}
		b.WriteString("}\n\n")
	}
	b.WriteString(PackageConstDecls(pkg, gc))

	b.WriteString("type snglState struct {\n")
	for _, v := range pkg.Vars {
		for _, p := range TypeImports(v.Type) {
			gc.RequireImport(p)
		}
		fmt.Fprintf(&b, "\t%s %s\n", gc.StateFieldName(v.Name), IRTypeToGo(v.Type))
	}
	fmt.Fprintf(&b, "}\n\nvar %s = &snglState{}\n", gc.RecvName())

	for _, fn := range pkg.Funcs {
		if fn.IsTest || !codegen.HasBody(fn) || fn.Foreign.Name != "" || fn.Intrinsic != "" {
			continue
		}
		// A method is the free function its call site names (LiftsToFreeFunc),
		// as on every Go platform.
		var lines []string
		if fn.IsMethod() && LiftsToFreeFunc(pkg, fn) {
			lines = gc.EmitTypeMethodDef(fn)
		} else {
			free := *fn
			free.Name = gc.FreeFuncName(fn.Name)
			lines = gc.EmitFuncDef(&free)
		}
		b.WriteByte('\n')
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}

	fmt.Fprintf(&b, "\nfunc snglInit() {\n\t%s = &snglState{}\n", gc.RecvName())
	for _, v := range pkg.Vars {
		if v.Init != nil {
			fmt.Fprintf(&b, "\t%s.%s = %s\n", gc.RecvName(), gc.StateFieldName(v.Name), LowerVarInit(v, gc))
		}
	}
	b.WriteString("}\n\nfunc snglBuild() {\n")
	for _, s := range pkg.Body {
		for _, l := range gc.EvalStmt(s) {
			b.WriteString("\t" + l + "\n")
		}
	}
	b.WriteString("}\n\nfunc snglTeardown() {\n")
	if pkg.Teardown != nil {
		fmt.Fprintf(&b, "\t%s()\n", gc.FreeFuncName(pkg.Teardown.Name))
	}
	b.WriteString("}\n")
	if RunsHandler(pkg) {
		args, run := pkg.Run.Params[0], pkg.Run.Params[1]
		fmt.Fprintf(&b, "\nfunc snglRun(args %s, run %s) {\n\t%s(args, run)\n}\n", gc.goType(args.Type), gc.goType(run.Type), gc.FreeFuncName(pkg.Run.Name))
	}
	_, err := io.WriteString(fe, b.String())
	return err
}

// RunsHandler reports whether pkg's `@run` handler is one an entry calls,
// with the command line and the `run` that presents the windows the build
// held hidden and runs the loop (design/windows.md); WriteProgram writes it
// as snglRun.
func RunsHandler(pkg *ir.Package) bool {
	return pkg.Run != nil && len(pkg.Run.Block) > 0 && len(pkg.Run.Params) == 2
}

// WriteTests writes pkg's tests into fe as the agent's, after WriteProgram,
// as the C language's WriteTests does: a function each, registered with
// testagent under its name. A test builds the program where it mounts the
// component it takes, or first where it takes none, each from state set
// afresh; a placed test's every other call is the program's own
// (passPlacement), so nothing here knows a platform. A test's wait is the
// platform's TestWaitID intrinsic, which lets its loop run meanwhile, and
// reset the platform's call taking down what the last test's build put up
// -- its windows -- before its teardown, or "" for none.
func WriteTests(fe codegen.FileEmitter, pkg *ir.Package, reset string) error {
	f, ok := fe.(*fileEmitter)
	if !ok {
		return fmt.Errorf("golang.WriteTests: %T is not a Go file", fe)
	}
	gc := *f.gc
	gc.placedTests = true
	gc.RequireImport(testagentPkg)
	var b strings.Builder
	if reset != "" {
		reset = "\t\t" + reset + "\n"
	}
	b.WriteString("\nvar snglRan bool\n\nfunc snglReset() {\n\tif snglRan {\n" + reset + "\t\tsnglTeardown()\n\t}\n\tsnglRan = true\n\tsnglInit()\n}\n")
	var register strings.Builder
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			continue
		}
		name := "snglTest" + ExportName(strings.TrimPrefix(fn.Name, "test"))
		fmt.Fprintf(&b, "\nfunc %s(t *testagent.T) {\n\tsnglReset()\n", name)
		if !mountsProgram(fn) {
			b.WriteString("\tsnglBuild()\n")
		}
		block := clang.SetContexts(fn.Block, func(name string) *ir.Var {
			for _, v := range pkg.Vars {
				if v.Name == name {
					return v
				}
			}
			return nil
		})
		body := &gc
		for _, p := range fn.Params {
			body = body.WithLocal(p.Name)
		}
		for _, s := range block {
			for _, l := range lowerTestStmt(s, body) {
				b.WriteString("\t" + l + "\n")
			}
		}
		b.WriteString("}\n")
		fmt.Fprintf(&register, "\ttestagent.RegisterTest(%q, %s)\n", strings.TrimPrefix(fn.Name, "test"), name)
	}
	if register.Len() > 0 {
		b.WriteString("\nfunc init() {\n" + register.String() + "}\n")
	}
	_, err := io.WriteString(fe, b.String())
	return err
}

// testagentPkg is the runtime a Go test agent serves its tests through.
const testagentPkg = "duckfam.us/sngl/pkg/go/testagent"

// mountsProgram reports whether fn mounts the component it takes, which
// builds the program where it does (passTestMount).
func mountsProgram(fn *ir.Func) bool {
	found := false
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && ir.IsTestCall(c, ir.TestMountID) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// placedFreeFuncs is the name each function of a placed program is declared
// and called by: exported, and kept off a type's name, which Go declares in
// the same scope -- a component's `tree()` beside a struct `Tree`.
func placedFreeFuncs(pkg *ir.Package) map[string]string {
	types := map[string]bool{}
	for _, sd := range pkg.Structs {
		types[ExportName(sd.Name)] = true
	}
	for _, ed := range pkg.Enums {
		types[ExportName(ed.Name)] = true
	}
	for _, ud := range pkg.Units {
		types[ExportName(ud.Name)] = true
	}
	out := map[string]string{}
	for _, fn := range pkg.Funcs {
		// A method WriteProgram declares on its type is named there.
		if fn.IsMethod() && LiftsToFreeFunc(pkg, fn) {
			continue
		}
		name := ExportName(fn.Name)
		for types[name] {
			name += "Func"
		}
		out[fn.Name] = name
	}
	return out
}

// placedAlert is sngl:dialog's Alert on a placed program, whose platform
// shows no toast of its own: the message on stderr, tagged with its variant.
// The fallback wrote a Model's toast list, which no placed program declares.
func placedAlert(gc *GoIRContext, method string, args []ir.CallArg) []string {
	variant := strconv.Quote(method)
	switch method {
	case "toast":
		variant = `"info"`
		if len(args) > 1 {
			variant = gc.EvalExpr(args[1].Value)
		}
	case "info", "warn", "error":
	case "confirm":
		return []string{"true"}
	default:
		return []string{"// unsupported Alert." + method}
	}
	gc.RequireImport("fmt")
	gc.RequireImport("os")
	return []string{fmt.Sprintf("fmt.Fprintln(os.Stderr, %s+\": \"+%s)", variant, gc.EvalExpr(args[0].Value))}
}
