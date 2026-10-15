package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
	"fmt"
)

// isContextDeclCallStmt reports whether s has the shape `context #id(arg)`:
// a call carrying an element-ref id whose callee names the declaration marked
// #[builtin("context")]. The mark rather than the word, so a program that
// declares its own `context` shadows the form as it would any other name.
func (c *checker) isContextDeclCallStmt(s *ast.CallStmt) bool {
	if s.Call.ID == "" {
		return false
	}
	fn := s.Call.Func
	if ix, ok := fn.(*ast.IndexExpr); ok {
		fn = ix.Operand
	}
	ident, ok := fn.(*ast.IdentExpr)
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
	return isComp && comp.Builtin == ir.BuiltinContext
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
	val := c.checkExprAs(a.Value, ctx.Typ, slot{mismatch: func(want, got string) string {
		return fmt.Sprintf("context %q: value type %v is not assignable to context type %v", ctx.Name, got, want)
	}})
	children := c.checkBlockIR(&vn.Block)
	return &ir.ContextProvider{AST: vn, Ref: ctx, Value: val, Children: children}
}

func (c *checker) registerRootContextDecl(s *ast.CallStmt) {
	name := s.Call.ID
	// Stdlib marks the ones a program did not write: sngl:i18n's `#locale` is
	// a context the language supplies, and lowering treats it as one.
	ctx := &ir.Context{AST: s, Name: name, Stdlib: c.inLibSource()}
	if name == "" {
		c.error(s.Pos, "context decl requires #identifier")
	} else if _, exists := c.scope.LookupLocal(name); exists {
		c.error(s.Pos, "duplicate declaration of %q", name)
	}
	args := s.Call.Args.Args
	if len(args) == 0 {
		c.error(s.Pos, "context decl requires a default value")
		c.declPkg().Contexts = append(c.declPkg().Contexts, ctx)
		return
	}
	if len(args) > 1 {
		c.error(s.Pos, "context decl takes exactly one default value")
		c.declPkg().Contexts = append(c.declPkg().Contexts, ctx)
		return
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(s.Pos, "context default must be positional, not named")
		c.declPkg().Contexts = append(c.declPkg().Contexts, ctx)
		return
	}
	// The default is an initializer expression, evaluated once when the
	// context's root/provider is set up — the same semantics as a var
	// initializer. It need not be a compile-time constant (e.g. the stdlib
	// `#locale` context defaults to the runtime `i18n.defaultLocale()`).
	// Declared before its own default is checked, which is what lets the rule
	// below match by symbol the way reportSelfReferentialProps does. Bound
	// after, `context #depth(depth)` was an `undefined` naming the context
	// being declared on the line declaring it -- and where an outer `depth`
	// existed it was not even that, the default quietly reading a different
	// binding.
	if name != "" {
		c.declare(s.Pos, ctx)
	}
	// Checked once every declaration is registered and marked, with the
	// package's other initializers (checkPendingInits).
	// `context[option[User]] #who(null)` states the type a default alone
	// says too little about, and the default is held to it.
	var written []ast.Expr
	if ix, ok := s.Call.Func.(*ast.IndexExpr); ok {
		written = ix.Indices
	}
	c.deferInit(s.Pos, func() {
		var stated *ir.Type
		if len(written) > 0 {
			var params []ir.TypeParam
			if comp := c.contextComp; comp != nil {
				params = comp.TypeParams
			}
			if _, types := c.bindWritten(s.Pos, "context", written, params); len(types) > 0 {
				ctx.TypeArgs = types
				stated = types[0]
			}
		}
		var def ir.Expr
		if stated != nil {
			def = c.checkExprAs(a.Value, stated, slot{mismatch: func(want, got string) string {
				return fmt.Sprintf("context %q: default %v is not assignable to context type %v", name, got, want)
			}})
		} else {
			def = c.checkExpr(a.Value)
		}
		c.reportSelfReferentialDefault(s.Pos, ctx, def)
		ctx.Default = def
		switch {
		case stated != nil:
			ctx.Typ = stated
		case def != nil:
			ctx.Typ = c.inferredType(def.ExprType(), *a.Value.ExprPos())
		}
	})
	// A context is program-global whichever tier declared it: the one sngl:i18n
	// declares reaches the program's own codegen, which reads its
	// Contexts, when the program adopts the library's (adoptLibContexts).
	c.declPkg().Contexts = append(c.declPkg().Contexts, ctx)
}

// reportSelfReferentialDefault reports a context default that reads the
// context it is declaring -- the rule reportSelfReferentialProps states for a
// node's id, asked of the one node whose id is its own declaration.
func (c *checker) reportSelfReferentialDefault(pos ast.Pos, ctx *ir.Context, def ir.Expr) {
	reported := false
	_ = ir.Walk(def, func(n ir.Node) error {
		r, ok := n.(*ir.ContextRead)
		if !ok || reported || r.Ref != ctx {
			return nil
		}
		reported = true
		at := pos
		if r.AST != nil {
			at = r.AST.Pos
		}
		c.error(at, "#%s names the context this argument list declares, so its default cannot read it", ctx.Name)
		return nil
	})
}

// refuseSetContextName reports a `t.setContext` whose first argument is not a
// context. The call names the context it sets, so the argument is a read of
// one, which every later phase takes as the name; anything else -- a string
// spelling the name -- named nothing, and failed only when the test ran.
func (c *checker) refuseSetContextName(e ir.Expr) {
	call, ok := e.(*ir.Call)
	if !ok || !ir.IsTestCall(call, ir.TestSetContextID) || len(call.Args) < 2 {
		return
	}
	arg := call.Args[len(call.Args)-2].Value
	if _, ok := arg.(*ir.ContextRead); ok {
		return
	}
	pos := call.AST.Pos
	if args := call.AST.Args.Args; len(args) > 0 {
		if a, ok := args[0].(ast.Arg); ok && a.Value.ExprPos() != nil {
			pos = *a.Value.ExprPos()
		}
	}
	c.error(pos, "t.setContext sets a context: name one declared with `context #name(default)`")
}

// adoptLibContexts puts the contexts a library package and what it imports
// declare on the program's list, in the order they were declared: a context is
// program-global whichever package declared it, and the library is loaded once
// for every check that reaches it.
func (c *checker) adoptLibContexts(pkg *ir.Package) {
	if pkg == nil || c.adoptedContexts[pkg] {
		return
	}
	if c.adoptedContexts == nil {
		c.adoptedContexts = map[*ir.Package]bool{}
	}
	c.adoptedContexts[pkg] = true
	for _, imp := range pkg.Imports {
		c.adoptLibContexts(imp.Pkg)
	}
	if pkg != c.pkg {
		c.adoptContexts(pkg)
	}
}
