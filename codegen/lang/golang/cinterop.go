package golang

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// Go reaches C through cgo: a C name is `C.` and the name, `void *` is
// `unsafe.Pointer`, and a conversion is a Go conversion or cgo's own
// `C.CString`.
var _ codegen.CInterop = (*GoIRContext)(nil)

// cgoNames is cgo's name for each C type C spells in more than one word.
var cgoNames = map[string]string{
	"signed char":        "schar",
	"unsigned char":      "uchar",
	"unsigned short":     "ushort",
	"unsigned int":       "uint",
	"unsigned":           "uint",
	"unsigned long":      "ulong",
	"long long":          "longlong",
	"unsigned long long": "ulonglong",
}

// CTypeToGo is t as cgo spells it, with no import beside it: `*C.cairo_t`,
// `C.int`, `unsafe.Pointer`. const is C's alone; cgo drops it.
func CTypeToGo(t *ir.CType) string {
	if t.Name == "void" && t.Ptr > 0 {
		return strings.Repeat("*", t.Ptr-1) + "unsafe.Pointer"
	}
	name := t.Name
	if n, ok := cgoNames[name]; ok {
		name = n
	}
	return strings.Repeat("*", t.Ptr) + "C." + name
}

// CType is t as cgo spells it, requiring `unsafe` where the spelling names it.
func (gc *GoIRContext) CType(t *ir.CType) string {
	s := CTypeToGo(t)
	if strings.Contains(s, "unsafe.") {
		gc.RequireImport("unsafe")
	}
	return s
}

// CConvert is x as cgo writes it: a string is copied with `C.CString`, or
// encoded by cbind.UTF16 into Go memory valid for the call, a number
// converted to its C type, and a pointer converted through
// `unsafe.Pointer`, since cgo converts no C pointer to another. One that is
// `void *` already, or becomes it, needs only the one conversion.
func (gc *GoIRContext) CConvert(x *ir.CConvert) string {
	switch x.Kind {
	case ir.CConvertCallback:
		// The closure: CCall makes it the context, and hands C the
		// dispatcher for its type in its place.
		return gc.EvalExpr(x.Operand)
	case ir.CConvertContext, ir.CConvertRelease:
		return "nil"
	case ir.CConvertHeld:
		// The value: CCall registers it, and hands C the context finding it.
		return gc.EvalExpr(x.Operand)
	case ir.CConvertStruct:
		return gc.cValueIn(x.Type, nil, gc.EvalExpr(x.Operand))
	}
	operand := gc.EvalExpr(x.Operand)
	switch x.Kind {
	case ir.CConvertString:
		return "C.CString(" + operand + ")"
	case ir.CConvertNumber:
		return gc.castType(x) + "(" + operand + ")"
	case ir.CConvertUTF16:
		gc.RequireImport("duckfam.us/sngl/pkg/go/cbind")
		gc.RequireImport("unsafe")
		return "(" + gc.CType(x.To) + ")(unsafe.Pointer(cbind.UTF16(" + operand + ")))"
	}
	gc.RequireImport("unsafe")
	if x.To.IsVoidPointer() {
		return "unsafe.Pointer(" + operand + ")"
	}
	if !ir.CTypeOf(x.Operand.ExprType()).IsVoidPointer() {
		operand = "unsafe.Pointer(" + operand + ")"
	}
	return "(" + gc.castType(x) + ")(" + operand + ")"
}

// castType is the type x converts to as Go spells it: the wrapper package's
// where the struct declaring it has one (`cgo.PGtkLabel`), since a program
// calling C through wrappers has no `C` of its own, and cgo's otherwise.
func (gc *GoIRContext) castType(x *ir.CConvert) string {
	if sd, ok := x.Type.Decl.(*ir.StructDef); ok && x.Type.Kind == ir.TypeStruct && gc.Wrapping == "" && wrapper(&sd.Foreign, gc.calls()) != nil {
		return gc.goType(x.Type)
	}
	return gc.CType(x.To)
}

// CCall is c, a call of a C function, with its arguments rendered. A C
// function's number comes back as cgo's type for it, which a Go int or
// float64 is not assignable from, so it is converted back, and a C struct
// field by field: the arguments went the other way in lower.CMarshal.
//
// A callback crosses as the dispatcher cgo exports for its type (ccallback.go)
// and the closure as the context, registered for as long as the callback is
// held: for the call, until C calls the release a `#[c.release]` parameter is
// handed, or for the rest of the program where the parameter is `#[retains]`.
//
// A `ref` C fills is a C temp the call is handed the address of, which the
// call hands back as lower.RefParams made it: the one value, or the record of
// the result and each one.
func (gc *GoIRContext) CCall(c *ir.Call, args []string) string {
	fn := c.Func
	if gc.calls() == ir.CCallsPurego {
		return gc.puregoCall(fn, args)
	}
	off := len(args) - len(fn.Params)
	if off < 0 {
		return gc.cCall(fn, args)
	}
	args = slices.Clone(args)
	var body []string
	var outs []string
	for i, p := range fn.Params {
		if p.Type == nil || p.Type.Kind != ir.TypeRef {
			continue
		}
		tmp := fmt.Sprintf("__snglOut%d", i)
		elem := p.Type.Elems[0]
		body = append(body, tmp+" := "+gc.cValueIn(elem, p.CAlias, args[off+i]))
		args[off+i] = "&" + tmp
		outs = append(outs, gc.cValueOut(elem, p.CAlias, tmp))
	}
	cb := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return !p.CompilerPassed() && p.Type != nil && p.Type.Kind == ir.TypeFunc })
	ctx := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CContext })
	rel := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CRelease })
	if cb >= 0 && ctx >= 0 {
		closure := args[off+cb]
		args[off+cb] = callbackName(fn.Params[cb].Type.Sig) + "Ptr()"
		// A context the C function spells otherwise than `void *` -- GLib's
		// gpointer -- is a type of cgo's own.
		asContext := func(v string) string {
			if t := ir.CTypeOf(fn.Params[ctx].Type); !t.IsVoidPointer() {
				return gc.CType(t) + "(" + v + ")"
			}
			return v
		}
		// A function pointer is cgo's *[0]byte, which any C function
		// pointer type is assignable from: GClosureNotify takes it as it is.
		if rel >= 0 {
			args[off+rel] = "snglReleasePtr()"
		}
		if rel >= 0 || fn.Params[cb].Transfer == ir.TransferRetains {
			args[off+ctx] = asContext("snglContext(" + closure + ")")
		} else {
			args[off+ctx] = asContext("__snglCtx")
			body = append([]string{"__snglCtx := snglContext(" + closure + ")", "defer snglRelease(__snglCtx)"}, body...)
		}
	}
	// A held value is registered as a closure is, until C calls the
	// release, and crosses as the context finding it: a pointer typedef
	// #[c.type] names, GLib's gpointer, is a type of cgo's own.
	if held := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CAlias.IsCHeld() }); held >= 0 && rel >= 0 {
		v := "snglContext(" + args[off+held] + ")"
		if t := fn.Params[held].CFuncType; t != nil {
			v = gc.CType(t) + "(" + v + ")"
		}
		args[off+held] = v
		args[off+rel] = "snglReleasePtr()"
	}
	call := gc.cCall(fn, args)
	if len(body) == 0 {
		return call
	}
	void := fn.Return == nil || fn.Return.Kind == ir.TypeVoid
	var ret string
	switch {
	case len(outs) == 0 && void:
		body = append(body, call)
	case len(outs) == 0:
		ret = call
	case void && len(outs) == 1:
		body = append(body, call)
		ret = outs[0]
	default:
		// The record lower.RefParams declared: the result, then each out.
		def, _ := c.Type.Decl.(*ir.StructDef)
		var fields []string
		k := 0
		for _, f := range def.Fields {
			if f.Name == "result" {
				body = append(body, "__snglResult := "+call)
				fields = append(fields, ExportName(f.Name)+": __snglResult")
				continue
			}
			fields = append(fields, ExportName(f.Name)+": "+outs[k])
			k++
		}
		if void {
			body = append(body, call)
		}
		ret = gc.goType(c.Type) + "{" + strings.Join(fields, ", ") + "}"
	}
	if ret == "" {
		return "func() { " + strings.Join(body, "; ") + " }()"
	}
	return "func() " + gc.goType(c.Type) + " { " + strings.Join(body, "; ") + "; return " + ret + " }()"
}

func (gc *GoIRContext) cCall(fn *ir.Func, args []string) string {
	call := "C." + fn.Foreign.Name + "(" + strings.Join(args, ", ") + ")"
	if fn.Return != nil {
		switch fn.Return.Kind {
		case ir.TypeInt:
			call = "int(" + call + ")"
		case ir.TypeFloat:
			call = "float64(" + call + ")"
		case ir.TypeBool:
			call = "(" + call + " != 0)"
		case ir.TypeString:
			// The C function keeps what it returns; the copy is Go's.
			call = "C.GoString(" + call + ")"
		case ir.TypeStruct:
			if ir.CStructOf(fn.Return) != nil {
				call = gc.cValueOut(fn.Return, nil, call)
			}
		case ir.TypeOption:
			// A pointer C answers NULL for: Go's option of it is a pointer
			// to the pointer, nil for NULL.
			if elem := fn.Return.Elems[0]; !fn.ReturnCAlias.IsCHeld() && ir.CTypeOf(elem) != nil && ir.CStructOf(elem) == nil && ir.CTypeOf(elem).Ptr > 0 {
				// Spelled as castType spells a conversion to it: cgo's
				// pointer, which a wrapper package's handle type aliases.
				t := gc.CType(ir.CTypeOf(elem))
				if sd, ok := elem.Decl.(*ir.StructDef); ok && gc.Wrapping == "" && wrapper(&sd.Foreign, gc.calls()) != nil {
					t = gc.goType(elem)
				}
				call = "func() *" + t + " { p := (" + t + ")(" + call + "); if p == nil { return nil }; return &p }()"
			}
			// What a held parameter registered, found by the pointer C
			// answers: as it is in a wrapper, which cannot name the
			// program's type, and asserted back to it in the program.
			if fn.ReturnCAlias.IsCHeld() {
				gc.RequireImport("unsafe")
				call = "snglClosure(unsafe.Pointer(" + call + "))"
				if gc.Wrapping == "" {
					call = gc.heldOut(fn.Return, call)
				}
			}
		}
	}
	return call
}

// heldOut is v, the value a C function answering opt, an
// `option[c.held[T]]`, registered, as the option it is: nil where C answered
// NULL or a pointer finding nothing of T's.
func (gc *GoIRContext) heldOut(opt *ir.Type, v string) string {
	t := gc.goType(opt.Elems[0])
	return "func(h any) *" + t + " { if v, ok := h.(" + t + "); ok { return &v }; return nil }(" + v + ")"
}

// cScalar is the C type a SNGL scalar t crosses as, written with alias where
// the declaration names one: a bool is C's `int`, as c.cbool makes one.
func cScalar(t *ir.Type, alias *ir.Alias) *ir.CType {
	if alias != nil && alias.C != nil {
		return alias.C
	}
	if t.Kind == ir.TypeFloat {
		return &ir.CType{Name: "double"}
	}
	return &ir.CType{Name: "int"}
}

// cValueIn is v, a SNGL scalar or C struct of type t, as the C value it
// crosses as: a struct field by field, through a function so v is read once.
func (gc *GoIRContext) cValueIn(t *ir.Type, alias *ir.Alias, v string) string {
	if sd := ir.CStructOf(t); sd != nil {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, f.Name+": "+gc.cValueIn(f.Type, f.CAlias, "v."+ExportName(f.Name)))
		}
		return "func(v " + gc.goType(t) + ") " + gc.CType(sd.Foreign.C) + " { return " + gc.CType(sd.Foreign.C) + "{" + strings.Join(fields, ", ") + "} }(" + v + ")"
	}
	ct := gc.CType(cScalar(t, alias))
	if t.Kind == ir.TypeBool {
		return "func(b bool) " + ct + " { if b { return 1 }; return 0 }(" + v + ")"
	}
	return ct + "(" + v + ")"
}

// cValueOut is v, a C value, as the SNGL scalar or C struct of type t it
// crosses back as.
func (gc *GoIRContext) cValueOut(t *ir.Type, alias *ir.Alias, v string) string {
	if sd := ir.CStructOf(t); sd != nil {
		var fields []string
		for _, f := range sd.Fields {
			fields = append(fields, ExportName(f.Name)+": "+gc.cValueOut(f.Type, f.CAlias, "c."+f.Name))
		}
		return "func(c " + gc.CType(sd.Foreign.C) + ") " + gc.goType(t) + " { return " + gc.goType(t) + "{" + strings.Join(fields, ", ") + "} }(" + v + ")"
	}
	switch t.Kind {
	case ir.TypeBool:
		return "(" + v + " != 0)"
	case ir.TypeFloat:
		return "float64(" + v + ")"
	}
	return "int(" + v + ")"
}

// wrapper is the Go wrapper of a C declaration f describes, which `sngl tool
// cgogen` marks beside its C spelling (design/win32.md), or nil where it has
// none, or in a build calling C otherwise than through wrappers
// (ir.Package.CCalls). Go calls the wrapper and spells the type as the
// wrapper package's, and lower.CMarshal converts nothing for it.
func wrapper(f *ir.Foreign, calls ir.CCalls) *ir.Foreign {
	if f.Scheme != "c" || calls != ir.CCallsWrapper {
		return nil
	}
	return f.In("go")
}

// spelling is how Go names the declaration f describes: its wrapper where it
// has one, and f otherwise.
func spelling(f *ir.Foreign, calls ir.CCalls) *ir.Foreign {
	if g := wrapper(f, calls); g != nil {
		return g
	}
	return f
}
