package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// Type arguments at a call site. A bracket after a name is a postfix index to
// the parser, so `first[int](xs)` reaches the checker as a call of an index,
// and the checker says what the bracket is: on a name that declares something
// generic -- a func, a method, a component, a struct -- it is that
// declaration's type arguments, and on a value it is an index. The written
// arguments bind a prefix of the declaration's own parameters, inference fills
// the rest, and a default fills what neither reaches, as Go's do. What they
// bind reaches the IR as the bindings inference already produces, so nothing
// after the checker learns of them; they are kept beside the call only so it
// prints back as written.

// writtenTypeArgs is a bracket of type arguments written at a call, waiting
// for the path that resolves the callee to say whose parameters they bind.
type writtenTypeArgs struct {
	pos   ast.Pos
	name  string
	exprs []ast.Expr
	used  bool
	types []*ir.Type
}

// resolveTypeArgs reads each thing in a bracket as the type it spells: a
// name-led type parsed as the expression it is spelled as, and any other --
// `func(int) bool`, `struct {}` -- as the type the parser read. A value that
// spells no type, `first[1]`, is refused where it is written.
func (c *checker) resolveTypeArgs(exprs []ast.Expr) []*ir.Type {
	out := make([]*ir.Type, len(exprs))
	for i, e := range exprs {
		te, ok := ast.TypeFromExpr(e)
		if !ok {
			c.error(*e.ExprPos(), "a type argument is a type: write a name, such as int or option[User]")
			continue
		}
		out[i] = c.resolveType(te)
	}
	return out
}

// bindWritten binds own's parameters, in order, to the types written in a
// bracket after name. It returns the bindings and the written types, for the
// IR to print back. Nil for no bracket.
func (c *checker) bindWritten(pos ast.Pos, name string, exprs []ast.Expr, own []ir.TypeParam) (map[string]*ir.Type, []*ir.Type) {
	if len(exprs) == 0 {
		return nil, nil
	}
	types := c.resolveTypeArgs(exprs)
	if len(own) == 0 {
		c.error(pos, "%s takes no type arguments", name)
		return nil, nil
	}
	if len(types) > len(own) {
		c.error(pos, "too many type arguments for %s: want %d, got %d", name, len(own), len(types))
		types = types[:len(own)]
	}
	bindings := map[string]*ir.Type{}
	for i, t := range types {
		if t != nil && t.Kind != ir.TypeDyn {
			bindings[own[i].Name] = t
		}
	}
	return bindings, types
}

// takeCallTypeArgs is what the bracket written at call binds of own, the
// callee's own type parameters, once the path checking the call has resolved
// the callee. Nil where none was written. A call's bracket is taken once.
func (c *checker) takeCallTypeArgs(call *ast.CallExpr, own []ir.TypeParam) map[string]*ir.Type {
	w := c.callTypeArgs[call]
	if w == nil || w.used {
		return nil
	}
	w.used = true
	bindings, types := c.bindWritten(w.pos, w.name, w.exprs, own)
	w.types = types
	return bindings
}

// instantiatedCall checks a call whose callee is a bracket on a generic
// declaration, `first[int](xs)`: the call without the bracket, with the
// bracket's types waiting for whichever path resolves the callee. False where
// the bracket is an index, `handlers[0](x)`.
func (c *checker) instantiatedCall(x *ast.CallExpr) (ir.Expr, bool) {
	ix, ok := x.Func.(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	name, ok := c.namesDeclaration(ix.Operand)
	if !ok {
		return nil, false
	}
	call := *x
	call.Func = ix.Operand
	if c.stmtCall == x {
		c.stmtCall = &call
	}
	w := &writtenTypeArgs{pos: ix.Pos, name: name, exprs: ix.Indices}
	if c.callTypeArgs == nil {
		c.callTypeArgs = map[*ast.CallExpr]*writtenTypeArgs{}
	}
	c.callTypeArgs[&call] = w
	out := c.inferCall(&call)
	if !w.used {
		// No path reached a generic declaration's parameters: a conversion,
		// a call through a value.
		c.resolveTypeArgs(ix.Indices)
		c.error(ix.Pos, "%s takes no type arguments", name)
	}
	if ic, ok := out.(*ir.Call); ok {
		ic.TypeArgs = w.types
	}
	return out, true
}

// namesDeclaration reports whether e names a declaration a bracket may hold
// type arguments for -- a func, a component, a struct, or a method -- rather
// than a value one indexes, and the name to report it by.
func (c *checker) namesDeclaration(e ast.Expr) (string, bool) {
	switch o := e.(type) {
	case *ast.IdentExpr:
		// What the name resolves to as an expression, quietly: a component's
		// method named like a type, `list[0]` reading `func list()`, is a
		// value however the scope spells it.
		e := c.checkExprQuietly(o)
		if id, ok := e.(*ir.Ident); ok && namesItself(id) {
			return o.Name, true
		}
		return "", false
	case *ast.SelectExpr:
		if id, ok := o.Operand.(*ast.IdentExpr); ok {
			if sym, ok := c.scope.Lookup(id.Name); ok {
				if ns, isNS := sym.(*ir.Namespace); isNS {
					if ns.Pkg == nil || ns.Pkg.Symbols == nil {
						return "", false
					}
					m, ok := ns.Pkg.Symbols.LookupMember(o.Field)
					if ok && isGenericCandidate(m) {
						return id.Name + "." + o.Field, true
					}
					return "", false
				}
			}
		}
		// A method: of a type named statically, `Box.same`, or of a value,
		// `xs.map`. The operand is checked quietly, since the call checks
		// it again.
		t := exprType(c.checkExprQuietly(o.Operand))
		if c.hasMethod(t, o.Field) {
			return o.Field, true
		}
	}
	return "", false
}

// namesItself reports whether a name read as an expression stands for a
// declaration rather than a value: a func or a component, or a struct named
// as a type. A struct's symbol also answers for a value that only shares its
// name, whose type is then not the bare struct's own.
//
// A func that takes nothing and binds nothing is read as its result, `counts`
// for `counts()`, so a bracket after one indexes that.
func namesItself(id *ir.Ident) bool {
	switch sym := id.Sym.(type) {
	case *ir.StructDef:
		return id.Type != nil && id.Type.Decl == sym && len(id.Type.Elems) == 0
	case *ir.Func:
		sig := sym.FuncSig()
		if id.Type != nil && id.Type.Kind == ir.TypeFunc && id.Type.Sig != nil {
			sig = id.Type.Sig
		}
		return len(sig.TypeParams) > 0 || len(sig.Params) > 0
	}
	return isGenericCandidate(id.Sym)
}

// isGenericCandidate reports whether sym is a declaration a bracket after its
// name instantiates rather than indexes, generic or not: `plain[int](1)` is
// a declaration written with type arguments it does not take.
func isGenericCandidate(sym ir.Symbol) bool {
	switch sym.(type) {
	case *ir.Func, *ir.Component, *ir.StructDef:
		return true
	}
	return false
}

// hasMethod reports whether a value of type t has a method called name,
// found the way inferMethodCall finds one.
func (c *checker) hasMethod(t *ir.Type, name string) bool {
	if t == nil {
		return false
	}
	if t.Kind == ir.TypeRef && len(t.Elems) > 0 {
		t = t.Elems[0]
	}
	if _, ok := c.memberOf(t.Decl, name); ok {
		return true
	}
	if _, ok := c.lookupMethod(t.String(), name); ok {
		return true
	}
	switch t.Kind {
	case ir.TypeList:
		_, ok := c.lookupMethod("list", name)
		return ok
	case ir.TypeOption:
		_, ok := c.lookupMethod("option", name)
		return ok
	case ir.TypeMap:
		_, ok := c.lookupMethod("map", name)
		return ok
	}
	return false
}
