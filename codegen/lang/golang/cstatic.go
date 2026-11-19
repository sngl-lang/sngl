package golang

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// A callback C calls with no context (sngl:x/c's #[static]) has nothing to
// find a closure by, so Go exports one function for each function of the
// program's handed to one, beside the dispatchers. It calls through a
// variable of the function's type, which the call handing it to C sets to
// the function as the program names it: the callbacks' file is written from
// the package alone, and how a function is named -- free, or a platform's
// Model's -- is the writer's of the call to say.

// Static is a function cgo exports for a callback C calls with no context:
// its name, and the parameter taking it, which says how it crosses.
type Static struct {
	Name  string
	Param *ir.Param
}

// staticName is the function cgo exports for callee, a program's function.
func staticName(callee *ir.Func) string {
	return "snglStatic_" + ExportName(callee.Name)
}

// WrapperStaticName is the function a wrapper package (sngl tool cgogen)
// exports for p, a static callback of the C declaration fn: the package
// cannot name the program's function, so its wrapper takes the function as
// a Go value and the export calls whichever the last call handed it.
func WrapperStaticName(fn *ir.Func, p *ir.Param) string {
	return "snglStatic_" + ExportName(fn.Name) + "_" + p.Name
}

// WrapperOutsName is the record a wrapper named name declares for what its
// `#[c.outs]` callback p answers, field for field the program's own, which
// the program's call converts (nativeCall).
func WrapperOutsName(name string, p *ir.Param) string {
	return name + ExportName(p.Name)
}

// staticArg is fn, a function value as the program names it, as the function
// pointer C is handed for callee: the export, once the variable it calls
// through holds fn.
func staticArg(n, fn string) string {
	return "func() *[0]byte { " + n + "Fn = " + fn + "; return " + n + "Ptr() }()"
}

// staticCType is the C type t crosses a static callback as: a handle as the
// C type it names, a string as a `char *`, which cgo's export spells with no
// const, and a bool as an int.
func staticCType(t *ir.Type) *ir.CType {
	if t.Kind == ir.TypeString {
		return &ir.CType{Name: "char", Ptr: 1}
	}
	return callbackCType(t)
}

// staticsSource is the externs the callbacks' preamble declares for statics,
// and the Go exporting each.
func staticsSource(statics []Static, calls ir.CCalls) (externs, source string, imports []string) {
	var ext, src strings.Builder
	cSpelled := calls == ir.CCallsDirect
	// A handle the program holds as a wrapper package's type -- its own
	// cgo's pointer is another type -- crosses through unsafe.Pointer.
	wrapped := func(t *ir.Type) *ir.Foreign {
		sd, ok := t.Decl.(*ir.StructDef)
		if !ok || t.Kind != ir.TypeStruct || cSpelled {
			return nil
		}
		w := wrapper(&sd.Foreign, calls)
		if w != nil && !slices.Contains(imports, w.Path) {
			imports = append(imports, w.Path)
		}
		return w
	}
	goType := func(t *ir.Type) string {
		if w := wrapped(t); w != nil {
			return w.Name
		}
		if ct := ir.CTypeOf(t); ct != nil && cSpelled {
			return CTypeToGo(ct)
		}
		return IRTypeToGo(t)
	}
	for _, s := range statics {
		sig := s.Param.Type.Sig
		n := s.Name
		var cparams, params, args []string
		for i, p := range sig.Params {
			a := fmt.Sprintf("a%d", i)
			ct := staticCType(p.Type)
			cparams = append(cparams, exportedC(ct))
			params = append(params, a+" "+CTypeToGo(ct))
			switch {
			case p.Type.Kind == ir.TypeString:
				args = append(args, "C.GoString("+a+")")
			case wrapped(p.Type) != nil:
				args = append(args, goType(p.Type)+"(unsafe.Pointer("+a+"))")
			case ir.CTypeOf(p.Type) != nil:
				args = append(args, a)
			case p.Type.Kind == ir.TypeBool:
				args = append(args, a+" != 0")
			default:
				args = append(args, IRTypeToGo(p.Type)+"("+a+")")
			}
		}
		call := n + "Fn(" + strings.Join(args, ", ") + ")"
		cret, ret, body := "void", "", "\t"+call+"\n"
		switch r := sig.Return; {
		case s.Param.COuts:
			sd, _ := r.Decl.(*ir.StructDef)
			body = "\tr := " + call + "\n"
			for k, f := range sd.Fields {
				o := fmt.Sprintf("o%d", k)
				ct := staticCType(f.Type)
				cparams = append(cparams, exportedC(ct)+" *")
				params = append(params, o+" *"+CTypeToGo(ct))
				v := CTypeToGo(ct) + "(r." + ExportName(f.Name) + ")"
				if f.Type.Kind == ir.TypeBool {
					v = "map[bool]" + CTypeToGo(ct) + "{true: 1}[r." + ExportName(f.Name) + "]"
				}
				body += "\tif " + o + " != nil {\n\t\t*" + o + " = " + v + "\n\t}\n"
			}
		case r != nil && r.Kind != ir.TypeVoid:
			ct := staticCType(r)
			cret, ret = exportedC(ct), " "+CTypeToGo(ct)
			switch {
			case wrapped(r) != nil:
				body = "\treturn (" + CTypeToGo(ct) + ")(unsafe.Pointer(" + call + "))\n"
			case ir.CTypeOf(r) != nil:
				body = "\treturn " + call + "\n"
			case r.Kind == ir.TypeBool:
				body = "\tif " + call + " {\n\t\treturn 1\n\t}\n\treturn 0\n"
			default:
				body = "\treturn " + CTypeToGo(ct) + "(" + call + ")\n"
			}
		}
		if len(cparams) == 0 {
			cparams = []string{"void"}
		}
		fmt.Fprintf(&ext, "extern %s %s(%s);\n", cret, n, strings.Join(cparams, ", "))
		var pts []string
		for _, p := range sig.Params {
			pts = append(pts, goType(p.Type))
		}
		fnType := "func(" + strings.Join(pts, ", ") + ")"
		if r := sig.Return; r != nil && r.Kind != ir.TypeVoid {
			fnType += " " + goType(r)
		}
		fmt.Fprintf(&src, "\n// %sFn is the program's function C calls %s for, which the call handing it to C sets.\nvar %sFn %s\n", n, n, n, fnType)
		fmt.Fprintf(&src, "\n//export %s\nfunc %s(%s)%s {\n%s}\n", n, n, strings.Join(params, ", "), ret, body)
		fmt.Fprintf(&src, "\n// %sPtr is %s as the function pointer C calls.\nfunc %sPtr() *[0]byte { return (*[0]byte)(C.%s) }\n", n, n, n, n)
	}
	return ext.String(), src.String(), imports
}

func exportedC(ct *ir.CType) string {
	c := *ct
	c.Const = false
	return c.String()
}

// variadicHeader is the header a program calling a C function that takes a
// variable argument list includes after its links' own.
const variadicHeader = "sngl_variadic.h"

// variadicName is the C function of one shape a call of fn, a variadic C
// function, goes through: cgo calls no variadic function.
func variadicName(fn *ir.Func) string { return "snglV_" + fn.Name }

// variadicLink is the link whose header defines a C function for each
// variadic one pkg calls, taking what its declaration lists; nil where pkg
// calls none.
func variadicLink(pkg *ir.Package) *ir.CLink {
	fns := ir.CalledCVariadics(pkg)
	if len(fns) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("// Code generated by sngl; DO NOT EDIT.\n\n#ifndef SNGL_VARIADIC_H\n#define SNGL_VARIADIC_H\n")
	for _, fn := range fns {
		var params, args []string
		for i, p := range fn.Params {
			a := fmt.Sprintf("a%d", i)
			params = append(params, variadicCType(p.Type, p.CAlias)+" "+a)
			args = append(args, a)
		}
		if len(params) == 0 {
			params = []string{"void"}
		}
		ret, pre := "void", ""
		if r := fn.Return; r != nil && r.Kind != ir.TypeVoid {
			ret, pre = variadicCType(r, fn.ReturnCAlias), "return "
		}
		fmt.Fprintf(&b, "\nstatic inline %s %s(%s) {\n\t%s%s(%s);\n}\n", ret, variadicName(fn), strings.Join(params, ", "), pre, fn.Foreign.Name, strings.Join(args, ", "))
	}
	b.WriteString("\n#endif\n")
	return &ir.CLink{Include: variadicHeader, Sources: []ir.CSource{{Name: variadicHeader, Path: variadicHeader, Text: b.String()}}}
}

// variadicCType is the C type a parameter or result of type t, written with
// alias where its declaration names one, crosses as.
func variadicCType(t *ir.Type, alias *ir.Alias) string {
	if alias != nil && alias.C != nil {
		return alias.C.String()
	}
	if t.Kind == ir.TypeOption && len(t.Elems) == 1 {
		t = t.Elems[0]
	}
	if ct := ir.CTypeOf(t); ct != nil {
		return ct.String()
	}
	switch t.Kind {
	case ir.TypeString:
		return "const char *"
	case ir.TypeFloat:
		return "double"
	}
	return "int"
}
