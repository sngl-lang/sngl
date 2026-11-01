package checker

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// appender is the appendFormat a builder may call in place of part, or nil:
// part is a call of a format method a program or a library wrote for a type of
// its own, and that type also declares appendFormat taking format's
// parameters after the builder it appends to. A builtin type's is not one: an
// int's format is inlined to the conversion it is, which a builder gains
// nothing from.
func (c *checker) appender(part ir.Expr) *ir.Func {
	call, ok := part.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Name != "format" || !call.Func.IsMethod() || call.ReceiverArg {
		return nil
	}
	recv := call.Func.RecvDecl()
	if sd, ok := recv.(*ir.StructDef); ok && (sd.Builtin != "" || sd.Intrinsic != "") {
		return nil
	}
	fn, ok := c.memberOf(recv, "appendFormat")
	if !ok {
		return nil
	}
	fp, ap := call.Func.Params, fn.Params
	if len(fp) == 0 || len(ap) != len(fp)+1 || len(call.Args) == 0 {
		return nil
	}
	if out := ap[1].Type; out == nil || out.Kind != ir.TypeRef || len(out.Elems) != 1 || !ir.IsBuilder(out.Elems[0]) {
		return nil
	}
	for i, p := range fp[1:] {
		if q := ap[i+2]; q.Name != p.Name || !q.Type.Equal(p.Type) {
			return nil
		}
	}
	return fn
}

// buildInterpolation is an interpolated string written by one builder, or nil
// where the `+` chain of its parts is what it should be.
//
// A part whose type declares appendFormat (appender) is appended by it, with
// no string of its own made along the way; any other part is the string its
// format wrote, added. The builder is a function of the package's, `__fmtN`,
// taking what the string reads as arguments: a view and a var initializer
// hold an expression and no statements, and a call is an expression every
// target writes. A string none of whose parts has an appender stays the `+`
// chain, which each target already joins in one allocation.
func (c *checker) buildInterpolation(x *ast.InterpolationExpr, parts []ir.Expr, text []bool) ir.Expr {
	appenders := make([]*ir.Func, len(parts))
	var builderType *ir.Type
	for i, p := range parts {
		if text[i] {
			continue
		}
		if fn := c.appender(p); fn != nil {
			appenders[i] = fn
			builderType = fn.Params[1].Type.Elems[0]
		}
	}
	if builderType == nil {
		return nil
	}

	pos := x.Pos
	b := &ast.IdentExpr{Pos: pos, Name: "__b"}
	call := func(recv ast.Expr, method string, args ...ast.ArgOrEventHandler) *ast.CallExpr {
		return &ast.CallExpr{Pos: pos, Func: &ast.SelectExpr{Pos: pos, Operand: recv, Field: method}, Args: ast.ArgList{Pos: pos, Args: args}}
	}
	var params []*ir.Param
	var args []ir.CallArg
	param := func(t *ir.Type, value ir.Expr) *ast.IdentExpr {
		p := &ir.Param{Name: fmt.Sprintf("__v%d", len(params)), Type: t}
		params = append(params, p)
		args = append(args, ir.CallArg{Value: value})
		return &ast.IdentExpr{Pos: pos, Name: p.Name}
	}
	var stmts []ast.Stmt
	for i, p := range parts {
		switch {
		case text[i]:
			stmts = append(stmts, &ast.CallStmt{Pos: pos, Call: call(b, "add", ast.Arg{Value: ast.NewStringLiteral(p.(*ir.Literal).Value)})})
		case appenders[i] != nil:
			format := p.(*ir.Call)
			recv := param(exprType(format.Args[0].Value), format.Args[0].Value)
			appendArgs := []ast.ArgOrEventHandler{ast.Arg{Value: &ast.UnaryExpr{Pos: pos, Op: ast.UnaryAddr, Operand: b}}}
			// The checker writes the arguments in parameter order, the
			// positional ones first, so the j'th unnamed one is the j'th
			// parameter after the receiver; each is passed by name, since
			// appendFormat's positions are one further on.
			for j, a := range format.Args[1:] {
				name := a.Name
				if name == "" {
					name = format.Func.Params[1+j].Name
				}
				appendArgs = append(appendArgs, ast.Arg{Name: name, Value: param(exprType(a.Value), a.Value)})
			}
			stmts = append(stmts, &ast.CallStmt{Pos: pos, Call: call(recv, "appendFormat", appendArgs...)})
		default:
			stmts = append(stmts, &ast.CallStmt{Pos: pos, Call: call(b, "add", ast.Arg{Value: param(TypString, p)})})
		}
	}

	// A component body is read twice, and its strings with it: the second
	// read makes the same arguments and calls the function the first made.
	fn := c.formatBuilders[x]
	if fn == nil {
		fn = &ir.Func{Name: c.formatBuilderName(), Pkg: c.declPkg(), Params: params, Return: TypString}
		c.checkFormatBuilder(fn, builderType, stmts, &ast.ReturnStmt{Pos: pos, Value: call(b, "string")})
		if c.formatBuilders == nil {
			c.formatBuilders = map[*ast.InterpolationExpr]*ir.Func{}
		}
		c.formatBuilders[x] = fn
		c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
	}
	return &ir.Call{Type: TypString, Func: fn, Args: args}
}

// checkFormatBuilder checks the body buildInterpolation wrote for fn: a
// builder `__b`, stmts appending to it, and ret reading it.
func (c *checker) checkFormatBuilder(fn *ir.Func, builderType *ir.Type, stmts []ast.Stmt, ret *ast.ReturnStmt) {
	prevFunc, prevOuter, prevNested := c.currentFunc, c.funcOuterScope, c.nestedScope
	c.currentFunc, c.funcOuterScope, c.nestedScope = fn, c.scope, nil
	defer func() {
		c.currentFunc, c.funcOuterScope, c.nestedScope = prevFunc, prevOuter, prevNested
	}()
	c.pushScope()
	defer c.popScope()
	defer c.enterFuncBody()()
	prevReturn := c.returnType
	c.returnType = fn.Return
	defer func() { c.returnType = prevReturn }()

	// Declared without declare, whose reserved-name check is for what a
	// program writes: these names are reserved so that nothing it writes
	// can take one.
	for _, p := range fn.Params {
		_ = c.scope.Declare(p)
	}
	b := &ir.Var{Name: "__b", Type: builderType}
	_ = c.scope.Declare(b)
	fn.Block = []ir.Stmt{&ir.LocalVar{Name: b.Name, Type: builderType, Sym: b}}
	for _, s := range append(stmts, ret) {
		if st := c.checkStmt(s); st != nil {
			fn.Block = append(fn.Block, st)
		}
	}
}

// formatBuilderName is the name of the next function buildInterpolation
// declares. A library's carry its path, since a program's helper and a
// library's promoted beside it share one namespace.
func (c *checker) formatBuilderName() string {
	n := c.formatBuilderCount
	c.formatBuilderCount++
	path := c.declPkg().Path
	if path == "" {
		return fmt.Sprintf("__fmt%d", n)
	}
	return fmt.Sprintf("__fmt_%s_%d", strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, path), n)
}

// formatArgsLit is the `_formatArgs[X]` of type t a call's args make: a field
// per parameter of X's format after its receiver, holding the argument args
// give it, bound as a call of that format binds them, or its default. A
// parameter that is itself a `_formatArgs` takes what X's format declares no
// parameter for in turn, so `"{xss, base=16}"` reaches each int. fits is false,
// after an error, where X declares no format to take them.
func (c *checker) formatArgsLit(pos ast.Pos, t *ir.Type, args []ast.ArgOrEventHandler) (lit ir.Expr, fits bool) {
	out := &ir.StructLit{Type: t}
	x := t.Elems[0]
	if x == nil || x.Kind == ir.TypeInvalid {
		return out, false
	}
	_, params, ok := ir.FormatParams(c.scope, x, "format")
	if !ok || x.HasTypeParam() {
		c.error(pos, "cannot format %s: type has no format method", x)
		for _, a := range args {
			c.checkExpr(a.(ast.Arg).Value)
		}
		return out, false
	}
	bound, fits := c.bindArgs(pos, args, &ir.FuncSig{Params: params})
	for i, p := range params {
		var v ir.Expr
		switch {
		case bound[i] != nil:
			v = *bound[i]
		case p.Default != nil:
			v = typeParamZeroFor(ir.CloneExprSharingDecls(p.Default), p.Type)
		default:
			continue
		}
		out.Fields = append(out.Fields, ir.FieldInit{Name: p.Name, Value: v})
	}
	return out, fits
}
