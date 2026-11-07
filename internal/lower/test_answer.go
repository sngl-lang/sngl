package lower

import (
	"slices"

	"duckfam.us/sngl/ir"
)

// passTestAnswer starts each test of a program that makes requests with
// sngl:http's answer set to its `closed`, as if the test's first statement
// were `t.setContext(http.answer, http.closed)`: no test reaches the network
// unless it says so, `t.setContext(http.answer, http.network)`, which a later
// statement of its own does. Both are found by their marks (ir.BuiltinHTTPAnswer,
// ir.BuiltinHTTPClosed), and the context through the reads passCallDefaults
// wrote at each request, so a program that makes none is left alone.
//
// After passCallDefaults, which writes the reads, and before
// passUnprovidedContext, which would fold a context only a test sets to its
// default, and passContext, which lowers the setting.
var passTestAnswer = pass{
	name:    "TestAnswer",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, opts Options) error {
		if pkg == nil || !opts.Tests {
			return nil
		}
		answer := markedContext(pkg, ir.BuiltinHTTPAnswer)
		if answer == nil || answer.Pkg == nil {
			return nil
		}
		closed := markedFunc(answer.Pkg, ir.BuiltinHTTPClosed)
		if closed == nil {
			return nil
		}
		for _, fn := range pkg.Funcs {
			if fn == nil || !fn.IsTest || len(fn.Params) == 0 {
				continue
			}
			set := setContextOf(fn.Params[0])
			if set == nil {
				continue
			}
			fn.Block = append([]ir.Stmt{setContextCall(set, fn.Params[0], answer, closed)}, fn.Block...)
		}
		return nil
	},
}

// markedContext is the context marked kind that something in pkg reads, or
// nil.
func markedContext(pkg *ir.Package, kind ir.BuiltinKind) *ir.Context {
	var found *ir.Context
	roots := []any{pkg}
	for _, fn := range collectReachableExternalFuncs(pkg) {
		roots = append(roots, fn)
	}
	for _, r := range roots {
		_ = ir.Walk(r, func(n ir.Node) error {
			if rd, ok := n.(*ir.ContextRead); ok && rd.Ref != nil && rd.Ref.Builtin == kind {
				found = rd.Ref
				return ir.SkipAll
			}
			return nil
		})
		if found != nil {
			return found
		}
	}
	return nil
}

// markedFunc is the function pkg declares marked kind, or nil.
func markedFunc(pkg *ir.Package, kind ir.BuiltinKind) *ir.Func {
	i := slices.IndexFunc(pkg.Funcs, func(f *ir.Func) bool { return f != nil && f.Builtin == kind })
	if i < 0 {
		return nil
	}
	return pkg.Funcs[i]
}

// setContextOf is `setContext` of the test type t is a parameter of, or nil.
func setContextOf(t *ir.Param) *ir.Func {
	if t.Type == nil {
		return nil
	}
	sd, ok := t.Type.Decl.(*ir.StructDef)
	if !ok || sd.Pkg == nil {
		return nil
	}
	i := slices.IndexFunc(sd.Pkg.Funcs, func(f *ir.Func) bool { return f != nil && f.Intrinsic == ir.TestSetContextID })
	if i < 0 {
		return nil
	}
	return sd.Pkg.Funcs[i]
}

// setContextCall is `t.setContext(ctx, fn)`.
func setContextCall(set *ir.Func, t *ir.Param, ctx *ir.Context, fn *ir.Func) ir.Stmt {
	value := &ir.Conversion{Type: ctx.Typ, Operand: &ir.Ident{Name: fn.Name, Type: fn.SymType(), Sym: fn}}
	return &ir.CallStmt{Call: &ir.Call{
		Type:        ir.TypVoid,
		Func:        set,
		ReceiverArg: true,
		TypeArgs:    []*ir.Type{ctx.Typ},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: t.Name, Type: t.Type, Sym: t}},
			{Name: "ctx", Value: &ir.ContextRead{Ref: ctx, Typ: ctx.Typ}},
			{Name: "value", Value: value},
		},
	}}
}
