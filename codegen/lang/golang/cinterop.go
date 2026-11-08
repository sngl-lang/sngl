package golang

import (
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
	}
	operand := gc.EvalExpr(x.Operand)
	switch x.Kind {
	case ir.CConvertString:
		return "C.CString(" + operand + ")"
	case ir.CConvertNumber:
		return gc.CType(x.To) + "(" + operand + ")"
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
	return "(" + gc.CType(x.To) + ")(" + operand + ")"
}

// CCall is a call of the C function fn with args rendered. A C function's
// number comes back as cgo's type for it, which a Go int or float64 is not
// assignable from, so it is converted back: the arguments went the other way
// in lower.CMarshal.
//
// A callback crosses as the dispatcher cgo exports for its type (ccallback.go)
// and the closure as the context, registered for as long as the callback is
// held: for the call, until C calls the release a `#[c.release]` parameter is
// handed, or for the rest of the program where the parameter is `#[retains]`.
func (gc *GoIRContext) CCall(fn *ir.Func, args []string) string {
	off := len(args) - len(fn.Params)
	cb := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return !p.CompilerPassed() && p.Type != nil && p.Type.Kind == ir.TypeFunc })
	ctx := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CContext })
	rel := slices.IndexFunc(fn.Params, func(p *ir.Param) bool { return p.CRelease })
	if off < 0 || cb < 0 || ctx < 0 {
		return gc.cCall(fn, args)
	}
	args = slices.Clone(args)
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
	// A function pointer is cgo's *[0]byte, which any C function pointer
	// type is assignable from: GClosureNotify takes it as it is.
	if rel >= 0 {
		args[off+rel] = "snglReleasePtr()"
	}
	if rel >= 0 || fn.Params[cb].Transfer == ir.TransferRetains {
		args[off+ctx] = asContext("snglContext(" + closure + ")")
		return gc.cCall(fn, args)
	}
	args[off+ctx] = asContext("__snglCtx")
	hold := "__snglCtx := snglContext(" + closure + "); defer snglRelease(__snglCtx); "
	if fn.Return == nil || fn.Return.Kind == ir.TypeVoid {
		return "func() { " + hold + gc.cCall(fn, args) + " }()"
	}
	return "func() " + gc.goType(fn.Return) + " { " + hold + "return " + gc.cCall(fn, args) + " }()"
}

func (gc *GoIRContext) cCall(fn *ir.Func, args []string) string {
	call := "C." + fn.Foreign.Name + "(" + strings.Join(args, ", ") + ")"
	if fn.Return != nil {
		switch fn.Return.Kind {
		case ir.TypeInt:
			call = "int(" + call + ")"
		case ir.TypeFloat:
			call = "float64(" + call + ")"
		}
	}
	return call
}
