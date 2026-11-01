package lower

import (
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passBuiltinMethods makes each library method of a builtin type that a call
// still names a function of the package: `n.format(16)` is
// `__int_format(n, 16)`, and `xs.format()` of a `list[int]` is
// `__list_format__int(xs)`, a copy at the receiver's types.
//
// No output can declare a method on an int, and passLibFuncs promotes plain
// helpers only, so such a method reaches a target by being inlined or not at
// all. The optimizer inlines every one the library writes for that -- each
// builtin format reads its parameters once, and an appendFormat is one call --
// so a build that optimizes first leaves nothing here. A pipeline that lowers
// without optimizing does, and emitted `d.format(10, 0, " ")` on a Go int
// (gtk4's TestIntegration_ComputedBodyImportCollected).
//
// A method on a generic builtin is copied at the types its call binds, with
// its `this` the copy's first parameter, rather than left to ReceiverParam
// and Monomorphize: its body may call `_format` or `_appendFormat`, which are
// a call of the bound type's own format only once there is one, and a target
// keeping generic functions whole would have no type to ask. The `_formatArgs`
// such a format takes is then the anonymous struct of the bound type's
// format's parameters (ir.FormatArgsFields).
//
// Early, so that each pass after it sees an ordinary function: an
// appendFormat's builder becomes a string under StringBuilders and its
// `ref[builder]` a value handed back under RefParams, and a format's defaults
// are written by CallDefaults.
var passBuiltinMethods = pass{
	name:    "BuiltinMethods",
	enabled: func(Features) bool { return true },
	apply:   lowerBuiltinMethods,
}

func lowerBuiltinMethods(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	b := &builtinMethods{pkg: pkg, names: map[string]bool{}, promoted: map[*ir.Func]bool{}}
	if pkg.Symbols != nil {
		b.scope = pkg.Symbols.Root
	}
	// A generic function calling a constraint's function, or formatting a
	// value of its type parameter, is copied at each binding first: the call
	// is the bound type's own only once there is one.
	var reach ir.BoundReach
	monomorphizeFuncs(pkg, func(fn *ir.Func) bool {
		return reach.Reaches(fn) && !(ir.ReceiverOutsideParams(fn) && len(fn.RecvTypeParams) > 0)
	}, false)
	if err := b.run(pkg); err != nil {
		return err
	}
	b.packTypes()
	return nil
}

// rewriteBuiltinMethods is the rewrite of passBuiltinMethods applied to fns, a
// library's helpers passLibFuncs made the package's after that pass ran:
// `string(style.lineCap)` in go's applyStyle is an enum's format, which no
// output declares either.
func rewriteBuiltinMethods(pkg *ir.Package, fns []*ir.Func) error {
	if len(fns) == 0 {
		return nil
	}
	b := &builtinMethods{pkg: pkg, names: map[string]bool{}, promoted: map[*ir.Func]bool{}}
	if pkg.Symbols != nil {
		b.scope = pkg.Symbols.Root
	}
	for _, fn := range fns {
		if err := b.run(fn); err != nil {
			return err
		}
	}
	return nil
}

// run rewrites root, and each function a rewrite makes the package's -- a
// promoted method, a copy -- in turn: `__int_appendFormat` calls the int's
// format.
func (b *builtinMethods) run(root any) error {
	for _, fn := range b.pkg.Funcs {
		b.names[fn.Name] = true
	}
	if err := b.visit(root); err != nil {
		return err
	}
	for len(b.pending) > 0 {
		fn := b.pending[0]
		b.pending = b.pending[1:]
		if err := b.visit(fn); err != nil {
			return err
		}
	}
	return nil
}

type builtinMethods struct {
	pkg       *ir.Package
	scope     *ir.Scope
	names     map[string]bool
	instances []builtinInstance
	promoted  map[*ir.Func]bool
	pending   []*ir.Func
	packs     map[*ir.Type]*ir.Type
}

// visit rewrites root's calls (rewrite) and makes each builtin method one of
// them still names a function of the package.
func (b *builtinMethods) visit(root any) error {
	if err := ir.RewriteExprs(root, b.rewrite); err != nil {
		return err
	}
	return ir.Walk(root, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || !promotableBuiltinMethod(call.Func) || b.promoted[call.Func] {
			return nil
		}
		fn := call.Func
		b.promoted[fn] = true
		fn.Name = "__" + fn.RecvName() + "_" + fn.Name
		fn.Receiver = nil
		b.pkg.Funcs = append(b.pkg.Funcs, fn)
		b.pending = append(b.pending, fn)
		return nil
	})
}

type builtinInstance struct {
	fn   *ir.Func
	recv *ir.Type
	copy *ir.Func
}

// rewrite is e with a `_format` at a bound type that type's format, a
// builtin method the optimizer would have inlined inlined, and a generic
// builtin's method called through its copy at the call's types.
func (b *builtinMethods) rewrite(e ir.Expr) (ir.Expr, error) {
	call, ok := e.(*ir.Call)
	if !ok {
		return e, nil
	}
	if formatted, ok := ir.BoundCall(b.scope, call); ok {
		return b.rewrite(formatted)
	}
	// An enum's or a unit's format the compiler declared is declared by no
	// output either, and is the text the target writes for the value.
	if ir.IsDefaultFormat(call.Func) && len(call.Args) == 1 {
		return &ir.Conversion{Type: call.Type, Operand: call.Args[0].Value}, nil
	}
	if promotableBuiltinMethod(call.Func) {
		if inlined := inlineSettled(call, 0); inlined != nil {
			return inlined, nil
		}
	}
	if cp := b.instance(call); cp != nil {
		call.Func = cp
		call.ReceiverArg = false
	}
	return e, nil
}

// instance is the copy of call's method on a generic builtin at the types its
// receiver binds, made the first time one is asked for; nil for any other
// call, and for one whose receiver is still generic.
func (b *builtinMethods) instance(call *ir.Call) *ir.Func {
	fn := call.Func
	if fn == nil || !ir.ReceiverOutsideParams(fn) || len(fn.RecvTypeParams) == 0 || len(fn.TypeParams) > 0 ||
		fn.PkgPath() == "" || fn.Intrinsic != "" || fn.Foreign.Path != "" || len(fn.Block) == 0 ||
		len(call.Args) == 0 || !call.ReceiverArg {
		return nil
	}
	recv := call.Args[0].Value.ExprType()
	if recv == nil || recv.HasTypeParam() {
		return nil
	}
	for _, in := range b.instances {
		if in.fn == fn && in.recv.Equal(recv) {
			return in.copy
		}
	}
	bind := map[string]*ir.Type{}
	ir.BindTypeParams(fn.RecvParam.Type, recv, bind)
	cp := ir.InstantiateFunc(fn, bind)
	cp.Params = append([]*ir.Param{cp.RecvParam}, cp.Params...)
	cp.RecvParam = nil
	cp.Receiver = nil
	// The program's, from here: every pass after this one writes it.
	cp.Frozen = false
	cp.Name = freshName(b.names, "__"+fn.RecvName()+"_"+fn.Name+"__"+typeKey(recv))
	b.instances = append(b.instances, builtinInstance{fn, recv, cp})
	b.pkg.Funcs = append(b.pkg.Funcs, cp)
	b.pending = append(b.pending, cp)
	return cp
}

// packTypes makes every `_formatArgs[X]` left the anonymous struct of X's
// format's parameters, for passAnonStructs to declare.
func (b *builtinMethods) packTypes() {
	b.packs = map[*ir.Type]*ir.Type{}
	ir.RewriteTypes(b.pkg, b.packType)
	_ = ir.Walk(b.pkg, func(n ir.Node) error {
		// A literal names its declaration as well as its type.
		if lit, ok := n.(*ir.StructLit); ok && (lit.Def == nil || lit.Def.Builtin == ir.BuiltinFormatArgs) {
			if sd := ir.AnonStructDef(lit.Type); sd != nil {
				lit.Def = sd
			}
		}
		return nil
	})
}

func (b *builtinMethods) packType(t *ir.Type) *ir.Type {
	if t == nil {
		return nil
	}
	if !ir.IsFormatArgs(t) {
		if !t.Any(ir.IsFormatArgs) {
			return t
		}
		return t.MapChildren(b.packType)
	}
	if out, ok := b.packs[t]; ok {
		return out
	}
	x := t.Elems[0]
	fields, ok := ir.FormatArgsFields(b.scope, x)
	if !ok || x.HasTypeParam() {
		return t
	}
	for i, f := range fields {
		g := *f
		g.Type = b.packType(f.Type)
		g.Default = nil
		g.DefaultWritten = false
		fields[i] = &g
	}
	out := &ir.Type{Kind: ir.TypeStruct, Decl: &ir.StructDef{Anon: true, Fields: fields}}
	b.packs[t] = out
	return out
}

// inlineSettled is call, of a library function whose body returns one
// expression, as that expression with the arguments in place of its
// parameters and each conditional they settle taken -- `n.format()` is
// `_text(n)`, as the optimizer would have made it -- or nil where an argument
// that is not a path or a literal would be read other than once. A library
// helper it calls in turn is inlined the same way, or left for passLibFuncs.
func inlineSettled(call *ir.Call, depth int) ir.Expr {
	fn := call.Func
	if depth > 8 || fn == nil || fn.PkgPath() == "" || fn.Intrinsic != "" || fn.Foreign.Path != "" ||
		len(fn.TypeParams) > 0 || len(fn.RecvTypeParams) > 0 || fn.RecvParam != nil || len(fn.Block) != 1 {
		return nil
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	// The arguments are in parameter order, a named one for its own
	// parameter, and CallDefaults has not yet written the rest.
	args := map[*ir.Param]ir.Expr{}
	next := 0
	for _, p := range fn.Params {
		switch {
		case next < len(call.Args) && (call.Args[next].Name == "" || call.Args[next].Name == p.Name):
			args[p] = call.Args[next].Value
			next++
		case p.Default != nil:
			args[p] = p.Default
		default:
			return nil
		}
	}
	if next != len(call.Args) {
		return nil
	}
	consts := map[*ir.Param]ir.Expr{}
	for p, a := range args {
		if _, ok := constValue(a); ok {
			consts[p] = a
		}
	}
	body := settle(substitute(ir.CloneExprSharingDecls(ret.Value), consts))
	reads := map[*ir.Param]int{}
	_ = ir.WalkExprs(body, func(e ir.Expr) error {
		if id, ok := e.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok {
				reads[p]++
			}
		}
		return nil
	})
	for p, n := range reads {
		if n > 1 && !copyable(args[p]) {
			return nil
		}
	}
	return rewriteExpr(substitute(body, args), func(e ir.Expr) (ir.Expr, error) {
		if c, ok := e.(*ir.Call); ok && c != call && c.Func != nil && !c.Func.IsMethod() {
			if inlined := inlineSettled(c, depth+1); inlined != nil {
				return inlined, nil
			}
		}
		return e, nil
	})
}

// substitute is e with each parameter subs binds read as its argument.
func substitute(e ir.Expr, subs map[*ir.Param]ir.Expr) ir.Expr {
	return rewriteExpr(e, func(x ir.Expr) (ir.Expr, error) {
		if id, ok := x.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && subs[p] != nil {
				return ir.CloneExprSharingDecls(subs[p]), ir.SkipDir
			}
		}
		return x, nil
	})
}

// rewriteExpr is e rewritten by f, e itself included.
func rewriteExpr(e ir.Expr, f func(ir.Expr) (ir.Expr, error)) ir.Expr {
	holder := &ir.Return{Value: e}
	_ = ir.RewriteExprs(holder, f)
	return holder.Value
}

// settle is e with each conditional whose condition is a constant replaced
// by the branch it takes.
func settle(e ir.Expr) ir.Expr {
	t, ok := e.(*ir.Ternary)
	if !ok {
		return e
	}
	if v, ok := constValue(t.Cond); ok {
		if b, isBool := v.(bool); isBool {
			if b {
				return settle(t.Then)
			}
			return settle(t.Else)
		}
	}
	return e
}

// copyable reports whether e may be evaluated as many times as it is read.
func copyable(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Literal:
		return true
	case *ir.Ident:
		return true
	case *ir.Select:
		return copyable(x.Operand)
	}
	_, ok := constValue(e)
	return ok
}

// constValue is e's value where e is a literal number, string or bool, a
// negated number, or a comparison or a logical operator over those.
func constValue(e ir.Expr) (any, bool) {
	switch x := e.(type) {
	case *ir.Literal:
		if x.Type == nil {
			return nil, false
		}
		switch x.Type.Kind {
		case ir.TypeBool:
			return x.Value == "true", true
		case ir.TypeInt, ir.TypeFloat:
			f, err := strconv.ParseFloat(x.Value, 64)
			return f, err == nil
		case ir.TypeString:
			return x.Value, true
		}
	case *ir.Unary:
		if x.Op == ast.UnaryNeg {
			if v, ok := constValue(x.Operand); ok {
				if f, isNum := v.(float64); isNum {
					return -f, true
				}
			}
		}
	case *ir.Binary:
		l, lok := constValue(x.Left)
		r, rok := constValue(x.Right)
		if !lok || !rok {
			return nil, false
		}
		if lb, ok := l.(bool); ok {
			rb, ok := r.(bool)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinAnd:
				return lb && rb, true
			case ast.BinOr:
				return lb || rb, true
			case ast.BinEq:
				return lb == rb, true
			case ast.BinNeq:
				return lb != rb, true
			}
			return nil, false
		}
		if lf, ok := l.(float64); ok {
			rf, ok := r.(float64)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinEq:
				return lf == rf, true
			case ast.BinNeq:
				return lf != rf, true
			case ast.BinLt:
				return lf < rf, true
			case ast.BinLte:
				return lf <= rf, true
			case ast.BinGt:
				return lf > rf, true
			case ast.BinGte:
				return lf >= rf, true
			}
			return nil, false
		}
		if ls, ok := l.(string); ok {
			rs, ok := r.(string)
			if !ok {
				return nil, false
			}
			switch x.Op {
			case ast.BinEq:
				return ls == rs, true
			case ast.BinNeq:
				return ls != rs, true
			}
		}
	}
	return nil, false
}

// promotableBuiltinMethod reports whether fn is a method a library wrote, with
// a body, for a builtin type that takes no type parameters, in a package this
// build copied.
func promotableBuiltinMethod(fn *ir.Func) bool {
	if fn == nil || fn.Frozen || fn.PkgPath() == "" || fn.Intrinsic != "" || len(fn.Block) == 0 ||
		len(fn.RecvTypeParams) > 0 || len(fn.TypeParams) > 0 || fn.RecvParam != nil {
		return false
	}
	sd, ok := fn.RecvDecl().(*ir.StructDef)
	return ok && (sd.Builtin != "" || sd.Intrinsic != "") && len(sd.TypeParams) == 0
}
