package golang

import (
	"fmt"
	"io"
	"strings"

	"duckfam.us/sngl/codegen"
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
// snglInit sets the vars and snglBuild is the package body. The platform
// writes the entry that calls the two.
func WriteProgram(fe codegen.FileEmitter, pkg *ir.Package, platform string) error {
	f, ok := fe.(*fileEmitter)
	if !ok {
		return fmt.Errorf("golang.WriteProgram: %T is not a Go file", fe)
	}
	gc := f.gc
	gc.Ctx = codegen.NewExprCtx(pkg)
	gc.Ctx.Platform = platform
	gc.Ctx.FreeBody = true
	gc.FreeFuncScope = true

	a := codegen.AnalyzeCommon(pkg)
	var b strings.Builder
	helpers := HelpersNeeded(pkg)
	for _, imp := range helpers.Imports() {
		gc.RequireImport(imp)
	}
	b.WriteString(helpers.Emit())
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
	b.WriteString("}\n")
	_, err := io.WriteString(fe, b.String())
	return err
}
