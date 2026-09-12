package canvasutil

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// GoContextStmts translates one canvas-intrinsic CallStmt into a method call
// on the pkg/go/canvas Context runtime.
//
// Two ids reach here: the save and restore that bracket a composed shape.
// The drawing itself is `sngl:language/go`'s overrides now, written in SNGL
// against that same runtime, so what is left is the bracket a draw function
// cannot express as a shape.
//
// call.Args[0] is the ctx receiver. An unknown intrinsic returns nil.
func GoContextStmts(cs *ir.CallStmt) []ir.Stmt {
	call := cs.Call
	ctx := call.Args[0].Value
	switch call.Func.Intrinsic {
	case "CanvasSave":
		return []ir.Stmt{ctxCall(ctx, "Save")}
	case "CanvasRestore":
		return []ir.Stmt{ctxCall(ctx, "Restore")}
	}
	return nil
}

// ctxCall builds `ctx.Method(args...)` as a void CallStmt.
func ctxCall(ctx ir.Expr, method string, args ...ir.Expr) ir.Stmt {
	cargs := make([]ir.CallArg, len(args))
	for i, a := range args {
		cargs[i] = ir.CallArg{Value: a}
	}
	return &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: ctx,
		Func:     &ir.Func{Name: method},
		Args:     cargs,
	}}
}

// selTyped is sel with the field's type carried along, so a backend that
// declares its types has one to emit rather than a guess to make.
func selTyped(operand ir.Expr, field string, t *ir.Type) ir.Expr {
	if t == nil {
		t = ir.TypDyn
	}
	return &ir.Select{Operand: operand, Field: field, Type: t}
}

// exprType is an expression's static type, or dyn when it carries none.
func exprType(e ir.Expr) *ir.Type {
	if e == nil {
		return ir.TypDyn
	}
	if t := e.ExprType(); t != nil {
		return t
	}
	return ir.TypDyn
}

// fieldType is the declared type of a struct's field, or nil when the type is
// not a struct or does not declare it.
func fieldType(t *ir.Type, name string) *ir.Type {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	sd, ok := t.Decl.(*ir.StructDef)
	if !ok {
		return nil
	}
	for _, f := range sd.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}
