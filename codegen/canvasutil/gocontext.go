package canvasutil

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// GoCanvasState carries per-draw-func state for GoContextStmts — currently a
// counter so each CanvasApplyStyle binds a uniquely-named style local. Callers
// create one per draw func and pass it to every GoContextStmts call for that
// func. A nil state is treated as a fresh one (fine for one-off translations).
type GoCanvasState struct {
	styleSeq int
}

// GoContextStmts translates one canvas-intrinsic CallStmt into method-call
// statements against the pkg/go/canvas Context runtime. The Context is stateful
// (SetFill/SetStroke/... mutate a pending style; a draw primitive consumes it).
// CanvasApplyStyle binds the style to a uniquely-named local (via st) so the
// setters don't re-evaluate the style expression repeatedly.
//
// call.Args[0] is the ctx receiver; the remaining args are positional. Style
// fields are addressed by their lowercase SNGL names (fill, r, strokeWidth);
// the Go renderer ExportNames them to Fill/R/StrokeWidth, matching the
// generated Color/CanvasStyle struct fields.
//
// CanvasDrawPath emits a range loop over the SNGL cmds list, dispatching on each
// command's op to ctx.MoveTo/LineTo/CubicTo/ClosePath, then ctx.PaintPath()
// after the loop. The SNGL PathCmd slice never crosses into the runtime — the
// generated code reads its fields and calls the Context path-builder methods —
// so the generated PathCmd Go type and the (removed) runtime type need not
// match.
//
// An unknown intrinsic returns nil.
func GoContextStmts(cs *ir.CallStmt, st *GoCanvasState) []ir.Stmt {
	if st == nil {
		st = &GoCanvasState{}
	}
	call := cs.Call
	id := call.Func.Intrinsic
	ctx := call.Args[0].Value
	rest := call.Args[1:]
	arg := func(i int) ir.Expr { return rest[i].Value }

	switch id {
	case "CanvasSave":
		return []ir.Stmt{ctxCall(ctx, "Save")}
	case "CanvasRestore":
		return []ir.Stmt{ctxCall(ctx, "Restore")}
	case "CanvasApplyStyle":
		// Bind the style to a local once so the five setters (each reading
		// several fields) don't re-evaluate the (possibly composite-literal or
		// method-call) style expression ~20 times per shape.
		st.styleSeq++
		name := "_cstyle" + strconv.Itoa(st.styleSeq)
		// Typed from the style expression rather than left dyn. A dyn field
		// read is a field on nothing, and the Go backend answers it by
		// asserting to whichever single struct declares that name — which,
		// for `.g`, is as likely to be a program's own Glyph as it is a
		// colour.
		styleType := exprType(arg(0))
		styleRef := &ir.Ident{Name: name, Type: styleType}
		col := func(field string) []ir.Expr {
			c := selTyped(styleRef, field, fieldType(styleType, field))
			return []ir.Expr{
				selTyped(c, "r", ir.TypInt), selTyped(c, "g", ir.TypInt),
				selTyped(c, "b", ir.TypInt), selTyped(c, "a", ir.TypInt),
			}
		}
		return []ir.Stmt{
			&ir.LocalVar{Name: name, Init: arg(0), Type: styleType},
			ctxCall(ctx, "SetFill", col("fill")...),
			ctxCall(ctx, "SetStroke", col("stroke")...),
			ctxCall(ctx, "SetStrokeWidth", selTyped(styleRef, "strokeWidth", fieldType(styleType, "strokeWidth"))),
			ctxCall(ctx, "SetFont", selTyped(styleRef, "fontSize", fieldType(styleType, "fontSize")), selTyped(styleRef, "fontFamily", fieldType(styleType, "fontFamily"))),
			ctxCall(ctx, "SetLineStyle", selTyped(styleRef, "lineCap", fieldType(styleType, "lineCap")), selTyped(styleRef, "lineJoin", fieldType(styleType, "lineJoin"))),
		}
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
