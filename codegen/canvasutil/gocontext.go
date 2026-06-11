package canvasutil

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// GoContextStmts translates one canvas-intrinsic CallStmt into method-call
// statements against the pkg/go/canvas Context runtime. The Context is stateful
// (SetFill/SetStroke/... mutate a pending style; a draw primitive consumes it),
// so no style-tracking state is threaded here: CanvasApplyStyle emits the
// setters inline.
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
func GoContextStmts(cs *ir.CallStmt) []ir.Stmt {
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
		style := arg(0)
		col := func(field string) []ir.Expr {
			c := sel(style, field)
			return []ir.Expr{sel(c, "r"), sel(c, "g"), sel(c, "b"), sel(c, "a")}
		}
		return []ir.Stmt{
			ctxCall(ctx, "SetFill", col("fill")...),
			ctxCall(ctx, "SetStroke", col("stroke")...),
			ctxCall(ctx, "SetStrokeWidth", sel(style, "strokeWidth")),
			ctxCall(ctx, "SetFont", sel(style, "fontSize"), sel(style, "fontFamily")),
			ctxCall(ctx, "SetLineStyle", sel(style, "lineCap"), sel(style, "lineJoin")),
		}
	case "CanvasDrawRect":
		return []ir.Stmt{ctxCall(ctx, "Rect", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawCircle":
		return []ir.Stmt{ctxCall(ctx, "Circle", arg(0), arg(1), arg(2))}
	case "CanvasDrawEllipse":
		return []ir.Stmt{ctxCall(ctx, "Ellipse", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawLine":
		return []ir.Stmt{ctxCall(ctx, "Line", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawPath":
		return pathStmts(ctx, arg(0))
	case "CanvasDrawText":
		return []ir.Stmt{ctxCall(ctx, "Text", arg(0), arg(1), arg(2))}
	case "CanvasDrawImage":
		return []ir.Stmt{ctxCall(ctx, "Image", arg(0), arg(1), arg(2), arg(3), arg(4))}
	}
	return nil
}

// pathStmts builds a range loop over the SNGL PathCmd list (cmds), emitting
// ctx.MoveTo/LineTo/CubicTo/ClosePath per command op, followed by
// ctx.PaintPath() to fill+stroke the built path under the pending style.
func pathStmts(ctx, cmds ir.Expr) []ir.Stmt {
	loopVar := &ir.Ident{Name: "_cmd", Type: ir.TypDyn}
	opSel := &ir.Select{Operand: loopVar, Field: "op", Type: ir.TypString}
	field := func(name string) ir.Expr {
		return &ir.Select{Operand: loopVar, Field: name, Type: ir.TypFloat}
	}
	cmdIf := func(op string, then ir.Stmt) *ir.If {
		return &ir.If{
			Cond: &ir.Binary{Op: ast.BinEq, Left: opSel, Right: &ir.Literal{Type: ir.TypString, Raw: op}},
			Body: []ir.Stmt{then},
		}
	}
	body := []ir.Stmt{
		cmdIf("moveTo", ctxCall(ctx, "MoveTo", field("x"), field("y"))),
		cmdIf("lineTo", ctxCall(ctx, "LineTo", field("x"), field("y"))),
		cmdIf("bezierTo", ctxCall(ctx, "CubicTo", field("cx1"), field("cy1"), field("cx2"), field("cy2"), field("x"), field("y"))),
		cmdIf("close", ctxCall(ctx, "ClosePath")),
	}
	return []ir.Stmt{
		&ir.For{Key: "_cmd", Iter: cmds, Body: body},
		ctxCall(ctx, "PaintPath"),
	}
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

// sel builds `operand.<field>` as an ir.Select. The Go renderer ExportNames the
// field, so lowercase SNGL names map to the exported Go struct fields.
func sel(operand ir.Expr, field string) ir.Expr {
	return &ir.Select{Operand: operand, Field: field, Type: ir.TypDyn}
}
