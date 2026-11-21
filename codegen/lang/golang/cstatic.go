package golang

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

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

// WrapperName is the exported Go name `sngl tool cgogen` wraps a C
// identifier as: `sngl_win32_append` is `SnglWin32Append`, `CreateWindowExW`
// itself, and a member of a C struct, `GError.message`, `GErrorMessage`.
func WrapperName(c string) string {
	var b strings.Builder
	for part := range strings.FieldsFuncSeq(c, func(r rune) bool { return r == '_' || r == '.' }) {
		if part == "" {
			continue
		}
		r := []rune(part)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// WrapperTypeName is the exported Go name of the type a wrapper package
// declares for a SNGL struct named n: n capitalized, or, where n is a
// package's own (`_p_GtkWidget`), each of its words capitalized
// (`PGtkWidget`), as WrapperName spells a C function.
func WrapperTypeName(n string) string {
	if strings.HasPrefix(n, "_") {
		return WrapperName(n)
	}
	return ExportName(n)
}

// WrapperOutsName is the record a wrapper named name declares for what its
// `#[c.outs]` callback p answers, field for field the program's own, which
// the program's call converts (nativeCall).
func WrapperOutsName(name string, p *ir.Param) string {
	return name + ExportName(p.Name)
}

// staticArg is fn, a function value as the program names it, as the function
// pointer C is handed for p, a static callback whose export is n: the
// export, once the variable it calls through holds fn, or the C function
// calling it with p's convention (staticTrampoline).
func staticArg(n string, p *ir.Param, fn string) string {
	return "func() *[0]byte { " + n + "Fn = " + fn + "; return " + staticTrampoline(n, p) + "Ptr() }()"
}

// staticTrampoline is the C function C is handed for p where p names a
// calling convention (#[c.static(convention=…)]): cgo exports only the
// platform's default, so a static C function of that convention, which the
// callbacks' preamble defines, calls the export n. Without one, n itself.
func staticTrampoline(n string, p *ir.Param) string {
	if p.CConvention == "" {
		return n
	}
	return n + "_" + ir.CIdentToken(p.CConvention)
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

// staticAliasedCType is staticCType written with alias where the callback's
// signature names one: a Win32 `LPARAM` crosses whole, as a C `int` would
// not.
func staticAliasedCType(t *ir.Type, alias *ir.Alias) *ir.CType {
	if alias != nil && alias.C != nil {
		return alias.C
	}
	return staticCType(t)
}

// staticTypedefCType is the C type C's typedef declares for what crosses as
// t with alias: as the export takes it, but a string's `const char *`.
func staticTypedefCType(t *ir.Type, alias *ir.Alias) string {
	if alias == nil && t.Kind == ir.TypeString {
		return "const char *"
	}
	return staticAliasedCType(t, alias).String()
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
	exported := map[string]bool{}
	for _, s := range statics {
		sig := s.Param.Type.Sig
		n := s.Name
		var cparams, params, args, tparams []string
		for i, p := range sig.Params {
			a := fmt.Sprintf("a%d", i)
			ct := staticAliasedCType(p.Type, p.CAlias)
			cparams = append(cparams, exportedC(ct))
			tparams = append(tparams, staticTypedefCType(p.Type, p.CAlias))
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
				tparams = append(tparams, exportedC(ct)+" *")
				params = append(params, o+" *"+CTypeToGo(ct))
				v := CTypeToGo(ct) + "(r." + ExportName(f.Name) + ")"
				if f.Type.Kind == ir.TypeBool {
					v = "map[bool]" + CTypeToGo(ct) + "{true: 1}[r." + ExportName(f.Name) + "]"
				}
				body += "\tif " + o + " != nil {\n\t\t*" + o + " = " + v + "\n\t}\n"
			}
		case r != nil && r.Kind != ir.TypeVoid:
			ct := staticAliasedCType(r, sig.ReturnCAlias)
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
		var tExt, tSrc string
		if t := staticTrampoline(n, s.Param); t != n {
			// The function of the convention C calls, which calls the
			// export, and a function answering it, since a static one has
			// no symbol cgo could take the address of.
			var named, passed []string
			for k, tp := range tparams {
				named = append(named, fmt.Sprintf("%s a%d", tp, k))
				passed = append(passed, fmt.Sprintf("(%s)a%d", cparams[k], k))
			}
			if len(tparams) == 0 {
				named, tparams = []string{"void"}, []string{"void"}
			}
			tret, call := "void", n+"("+strings.Join(passed, ", ")+");"
			if cret != "void" {
				tret, call = staticTypedefCType(sig.Return, sig.ReturnCAlias), "return "+call
			}
			tExt = fmt.Sprintf("\nstatic %s %s %s(%s) {\n\t%s\n}\n\ntypedef %s (%s *%s_t)(%s);\n\nstatic %s_t %s_get(void) {\n\treturn %s;\n}\n",
				tret, s.Param.CConvention, t, strings.Join(named, ", "), call,
				tret, s.Param.CConvention, t, strings.Join(tparams, ", "),
				t, t, t)
			tSrc = fmt.Sprintf("\n// %sPtr is %s as the function pointer C calls: %s, which calls it %s.\nfunc %sPtr() *[0]byte { return (*[0]byte)(C.%s_get()) }\n", t, n, t, s.Param.CConvention, t, t)
		}
		if exported[n] {
			ext.WriteString(tExt)
			src.WriteString(tSrc)
			continue
		}
		exported[n] = true
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
		ext.WriteString(tExt)
		src.WriteString(tSrc)
	}
	return ext.String(), src.String(), imports
}

func exportedC(ct *ir.CType) string {
	c := *ct
	c.Const = false
	return c.String()
}

// shimsHeader is the header a program calling a C function cgo cannot call
// as it is (ir.CalledCShimmed) includes after its links' own.
const shimsHeader = "sngl_shims.h"

// variadicName is the C function of one shape a call of fn, a variadic C
// function, goes through: cgo calls no variadic function.
func variadicName(fn *ir.Func) string { return "snglV_" + fn.Name }

// freeName is the C function a string a C function hands over is freed
// through, which calls the one #[c.frees] names, f, unless the pointer is
// NULL: cgo would hand f a pointer at f's own parameter type, which the
// declaration does not say (GLib's gpointer is no `void *` to cgo).
func freeName(f string) string { return "snglFree_" + f }

// shimsLink is the link whose header defines a C function for each C
// function pkg calls itself that cgo cannot call as it is: of a variadic
// one's shape, and freeing what one hands over; nil where pkg calls none.
// One a wrapper package wraps is that package's to define, in its own
// preamble (CShims), so the program calling it has no cgo of its own.
func shimsLink(pkg *ir.Package) *ir.CLink {
	fns := slices.DeleteFunc(ir.CalledCShimmed(pkg), func(fn *ir.Func) bool { return wrapper(&fn.Foreign, pkg.CCalls) != nil })
	if len(fns) == 0 {
		return nil
	}
	text := "// Code generated by sngl; DO NOT EDIT.\n\n#ifndef SNGL_SHIMS_H\n#define SNGL_SHIMS_H\n" + CShims(fns) + "\n#endif\n"
	return &ir.CLink{Include: shimsHeader, Sources: []ir.CSource{{Name: shimsHeader, Path: shimsHeader, Text: text}}}
}

// CShims defines, for each of fns, the C functions a Go call of it goes
// through (ir.CalledCShimmed): the function of a variadic one's one shape
// (variadicName), since cgo calls no variadic function, and the function
// freeing each string it hands over (freeName), once each, a name declared
// twice included. Static inline
// ones, which a preamble may hold.
func CShims(fns []*ir.Func) string {
	var b strings.Builder
	var freed, shimmed []string
	for _, fn := range fns {
		for _, f := range fn.CFreed() {
			if !slices.Contains(freed, f) {
				freed = append(freed, f)
				fmt.Fprintf(&b, "\nstatic inline void %s(void *p) {\n\tif (p) {\n\t\t%s(p);\n\t}\n}\n", freeName(f), f)
			}
		}
		// Two declarations of one name are one function: a library's and
		// the copy a test build promotes of it.
		if !fn.Foreign.Variadic && !fn.Foreign.Field || slices.Contains(shimmed, shimName(fn)) {
			continue
		}
		shimmed = append(shimmed, shimName(fn))
		var params, args []string
		for i, p := range fn.Params {
			a := fmt.Sprintf("a%d", i)
			params = append(params, shimCType(p.Type, p.CAlias)+" "+a)
			args = append(args, a)
		}
		if len(params) == 0 {
			params = []string{"void"}
		}
		ret, pre := "void", ""
		if r := fn.Return; r != nil && r.Kind != ir.TypeVoid {
			ret, pre = shimCType(r, fn.ReturnCAlias), "return "
		}
		call := ir.CCallExpr(fn, args)
		fmt.Fprintf(&b, "\nstatic inline %s %s(%s) {\n\t%s%s;\n}\n", ret, shimName(fn), strings.Join(params, ", "), pre, call)
	}
	return b.String()
}

// shimName is the C function of one shape a Go call of fn goes through: a
// variadic one's, or a member's of a C struct.
func shimName(fn *ir.Func) string {
	if fn.Foreign.Field {
		return "snglF_" + fn.Name
	}
	return variadicName(fn)
}

// shimCType is the C type a parameter or result of type t, written with
// alias where its declaration names one, crosses a shim as: a ref as a
// pointer to its element's, which a string C stores is a `char *`.
func shimCType(t *ir.Type, alias *ir.Alias) string {
	if t.Kind == ir.TypeRef && len(t.Elems) == 1 {
		if t.Elems[0].Kind == ir.TypeString {
			return "char **"
		}
		return shimCType(t.Elems[0], alias) + " *"
	}
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
