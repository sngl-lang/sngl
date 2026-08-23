package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// isContextDeclCallStmt reports whether s has the shape `context #id(arg)`:
// a call carrying an element-ref id whose callee names the declaration marked
// #[builtin("context")]. The mark rather than the word, so a program that
// declares its own `context` shadows the form as it would any other name.
func (c *checker) isContextDeclCallStmt(s *ast.CallStmt) bool {
	if s.Call.ID == "" {
		return false
	}
	ident, ok := s.Call.Func.(*ast.IdentExpr)
	if !ok {
		return false
	}
	sym, found := c.scope.Lookup(ident.Name)
	if !found {
		return false
	}
	if c.contextComp != nil {
		return sym == c.contextComp
	}
	// The stdlib's own load reaches here before the marks are collected, so
	// read the mark off the declaration the name resolves to.
	comp, isComp := sym.(*ir.Component)
	return isComp && comp.Builtin == ast.BuiltinContext
}

// buildContextProvider checks `name(value) { children }` where name resolves
// to an *ir.Context and returns an *ir.ContextProvider.
func (c *checker) buildContextProvider(vn *ast.VisualNode, ctx *ir.Context) *ir.ContextProvider {
	args := vn.Args.Args
	if len(args) != 1 {
		c.error(vn.Pos, "context provider %q requires exactly one value argument", ctx.Name)
		return &ir.ContextProvider{AST: vn, Ref: ctx}
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(vn.Pos, "context provider %q argument must be positional", ctx.Name)
		return &ir.ContextProvider{AST: vn, Ref: ctx}
	}
	val := c.checkExpr(a.Value)
	if val != nil && ctx.Typ != nil {
		valType := val.ExprType()
		if valType != nil && valType.Kind != ir.TypeDyn && ctx.Typ.Kind != ir.TypeDyn && !valType.IsAssignableTo(ctx.Typ) {
			pos := vn.Pos
			if p := a.Value.ExprPos(); p != nil {
				pos = *p
			}
			c.error(pos, "context %q: value type %v is not assignable to context type %v", ctx.Name, valType, ctx.Typ)
		}
	}
	children := c.checkBlockIR(&vn.Block)
	return &ir.ContextProvider{AST: vn, Ref: ctx, Value: val, Children: children}
}

func (c *checker) registerRootContextDecl(s *ast.CallStmt) {
	name := s.Call.ID
	ctx := &ir.Context{AST: s, Name: name}
	if name == "" {
		c.error(s.Pos, "context decl requires #identifier")
	} else if _, exists := c.scope.LookupLocal(name); exists {
		c.error(s.Pos, "duplicate declaration of %q", name)
	}
	args := s.Call.Args.Args
	if len(args) == 0 {
		c.error(s.Pos, "context decl requires a default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		return
	}
	if len(args) > 1 {
		c.error(s.Pos, "context decl takes exactly one default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		return
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(s.Pos, "context default must be positional, not named")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		return
	}
	// The default is an initializer expression, evaluated once when the
	// context's root/provider is set up — the same semantics as a var
	// initializer. It need not be a compile-time constant (e.g. the stdlib
	// `#locale` context defaults to the runtime `i18n.defaultLocale()`).
	def := c.checkExpr(a.Value)
	ctx.Default = def
	if def != nil {
		ctx.Typ = def.ExprType()
	}
	c.pkg.Contexts = append(c.pkg.Contexts, ctx)
	if name != "" {
		c.declare(s.Pos, ctx)
	}
}
