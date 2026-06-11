package canvasutil

import "git.duckfam.us/jonathan/sngl/ir"

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
// CanvasDrawPath passes the SNGL cmds expression straight through as
// ctx.Path(cmds). The generated PathCmd struct and runtime canvas.PathCmd
// differ as Go types; bridging that conversion is the concern of the platform
// tasks that wire this up (Tasks 7/8), not this helper.
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
		return []ir.Stmt{ctxCall(ctx, "Path", arg(0))}
	case "CanvasDrawText":
		return []ir.Stmt{ctxCall(ctx, "Text", arg(0), arg(1), arg(2))}
	case "CanvasDrawImage":
		return []ir.Stmt{ctxCall(ctx, "Image", arg(0), arg(1), arg(2), arg(3), arg(4))}
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

// sel builds `operand.<field>` as an ir.Select. The Go renderer ExportNames the
// field, so lowercase SNGL names map to the exported Go struct fields.
func sel(operand ir.Expr, field string) ir.Expr {
	return &ir.Select{Operand: operand, Field: field, Type: ir.TypDyn}
}
