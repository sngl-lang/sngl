package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// passCMarshal converts each argument of a C call through the sngl:x/c
// conversion its parameter's type names: `showText(ctx, s)` becomes
// `showText(ctx, c.cstring(s))`. A parameter of any other type -- an opaque
// handle, a typedef'd enum -- is handed over as it is. See design/ownership.md.
var passCMarshal = pass{
	name:    "CMarshal",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, _ Features, _ Options) error { return CMarshal(pkg) },
}

// CMarshal is passCMarshal for the interpreter simulating a target.
//
// Every body the program reaches is rewritten, not only the package's own: on
// the interpreter a library declaration is called where it is declared. A
// frozen declaration is one the program does not reach (ir.ClonePackageFor).
func CMarshal(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	m := &cMarshal{done: map[*ir.Call]bool{}, walked: map[*ir.Func]bool{}, indexed: map[*ir.Package]bool{}}
	m.index(pkg)
	if err := m.walk(pkg); err != nil {
		return err
	}
	seen := map[*ir.Package]bool{pkg: true}
	var visit func(p *ir.Package) error
	visit = func(p *ir.Package) error {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			for _, o := range ir.Owners(imp.Pkg) {
				if o.Comp != nil && !o.Comp.Frozen {
					if err := m.walk(o.Comp); err != nil {
						return err
					}
				}
				for _, fn := range o.Funcs {
					if err := m.walkFunc(fn); err != nil {
						return err
					}
				}
			}
			if err := visit(imp.Pkg); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(pkg); err != nil {
		return err
	}
	for len(m.queue) > 0 {
		fn := m.queue[0]
		m.queue = m.queue[1:]
		if err := m.walk(fn); err != nil {
			return err
		}
	}
	return nil
}

type cMarshal struct {
	// done is each C call already converted: a body spliced without a deep
	// copy shares its nodes with the declaration it came from.
	done map[*ir.Call]bool
	// queue is the bodied functions a call leads to, which on the
	// interpreter no package list names.
	walked  map[*ir.Func]bool
	queue   []*ir.Func
	conv    map[ir.BuiltinKind]*ir.Func
	indexed map[*ir.Package]bool
}

func (m *cMarshal) index(p *ir.Package) {
	if p == nil || m.indexed[p] {
		return
	}
	m.indexed[p] = true
	for _, fn := range p.Funcs {
		if fn.Builtin.IsCMarshal() {
			if m.conv == nil {
				m.conv = map[ir.BuiltinKind]*ir.Func{}
			}
			if m.conv[fn.Builtin] == nil {
				m.conv[fn.Builtin] = fn
			}
		}
	}
	for _, imp := range p.Imports {
		if imp != nil {
			m.index(imp.Pkg)
		}
	}
}

func (m *cMarshal) walkFunc(fn *ir.Func) error {
	if fn == nil || fn.Frozen || fn.Foreign.Name != "" || m.walked[fn] {
		return nil
	}
	m.walked[fn] = true
	return m.walk(fn)
}

func (m *cMarshal) walk(root any) error {
	err := ir.Walk(root, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil {
			return nil
		}
		if fn := call.Func; !fn.Frozen && fn.Foreign.Name == "" && !m.walked[fn] {
			m.walked[fn] = true
			m.queue = append(m.queue, fn)
		}
		return m.call(call)
	})
	if err != nil {
		return err
	}
	return ir.Rewrite(root, rewriteCasts)
}

// rewriteCasts makes each call of a `#[cnative(…, cast)]` function the
// ir.CConvert it declares. Its argument was converted as any C call's is, so
// a number reaches a typedef'd enum as a C `int` first, which cgo needs.
func rewriteCasts(n ir.Node) (ir.Node, error) {
	if c, ok := n.(*ir.Call); ok && c.Func.IsCCast() && len(c.Args) == 1 {
		return ir.NewCConvert(c.Func, c.Args[0].Value), nil
	}
	return n, nil
}

// marshalSpliced converts the C calls of an expression a pass splices after
// passCMarshal ran, and is e with its casts made conversions.
func marshalSpliced(e ir.Expr) (ir.Expr, error) {
	m := &cMarshal{done: map[*ir.Call]bool{}, walked: map[*ir.Func]bool{}, indexed: map[*ir.Package]bool{}}
	err := ir.Walk(e, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && c.Func != nil {
			return m.call(c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := rewriteCasts(e)
	if err != nil {
		return nil, err
	}
	e = out.(ir.Expr)
	return e, ir.Rewrite(e, rewriteCasts)
}

// call converts each argument of c, when c calls a C function other than a
// conversion. Aligned from the end: a native in the receiver-first shape is
// called with the receiver as argument zero, which no parameter declares.
func (m *cMarshal) call(c *ir.Call) error {
	fn := c.Func
	if fn.Foreign.Path != "C" || fn.Builtin.IsCMarshal() || m.done[c] {
		return nil
	}
	m.done[c] = true
	insertCContext(c)
	off := len(c.Args) - len(fn.Params)
	if off < 0 {
		return nil
	}
	for i, p := range fn.Params {
		if arg := &c.Args[off+i]; p.CompilerPassed() || p.Type != nil && p.Type.Kind == ir.TypeFunc {
			if _, done := arg.Value.(*ir.CConvert); !done {
				arg.Value = callbackArg(p, arg.Value)
			}
			continue
		}
		if p.CAlias != nil && p.CAlias.C != nil {
			if err := m.aliasArg(fn, p, &c.Args[off+i]); err != nil {
				return err
			}
			continue
		}
		kind := marshalKind(p.Type)
		if kind == ir.BuiltinNone {
			s, err := optionString(fn, p.Type, c.Args[off+i].Value)
			if err != nil {
				return err
			}
			if s != nil {
				kind, c.Args[off+i].Value = ir.BuiltinCString, s
			}
		}
		if kind == ir.BuiltinNone {
			continue
		}
		m.index(fn.Pkg)
		conv := m.conv[kind]
		if conv == nil {
			return fmt.Errorf("C function %q takes a %s, and no package it or the program imports declares #[builtin(%q)], which sngl:x/c does", fn.Foreign.Name, p.Type, string(kind))
		}
		arg := &c.Args[off+i]
		// Converted already: a body CMarshal rewrote in place and a pass then
		// spliced is walked again with the splice.
		if t := arg.Value.ExprType(); t != nil && marshalKind(t) != kind {
			continue
		}
		arg.Value = convertArg(conv, arg.Value)
	}
	return nil
}

// insertCContext writes into c the arguments for its callee's #[c.context]
// and #[c.release] parameters, which no caller passes and CallDefaults
// leaves to a native: the placeholder each defaults to, which callbackArg
// makes what the translator writes.
func insertCContext(c *ir.Call) {
	params := c.Func.Params
	passed := 0
	for _, p := range params {
		if p.CompilerPassed() && c.ArgFor(p.Name) == nil {
			passed++
		}
	}
	if passed == 0 {
		return
	}
	// Aligned from the end, as call aligns: a receiver-first native's
	// receiver is an argument no parameter declares.
	at := len(c.Args) - (len(params) - passed)
	if at < 0 {
		return
	}
	for _, p := range params {
		if p.CompilerPassed() && c.ArgFor(p.Name) == nil {
			c.Args = slices.Insert(c.Args, at, ir.CallArg{Name: p.Name, Value: ir.CloneExprSharingDecls(p.Default)})
		}
		at++
	}
}

// callbackArg is v, the argument for p, as it crosses to C: a closure as the
// callback a C function pointer calls, a context as the `void *` the
// translator writes from it, and a release as its function letting it go.
func callbackArg(p *ir.Param, v ir.Expr) ir.Expr {
	switch {
	case p.CContext:
		return &ir.CConvert{Kind: ir.CConvertContext, To: ir.CTypeOf(p.Type), Operand: v, Type: p.Type}
	case p.CRelease:
		return &ir.CConvert{Kind: ir.CConvertRelease, To: ir.CTypeOf(p.Type), Operand: v, Type: p.Type}
	}
	return &ir.CConvert{Kind: ir.CConvertCallback, Operand: v, Type: p.Type}
}

// aliasArg converts arg for p, a parameter written with a `#[cnative]`
// alias, to the C typedef the alias spells: a number cast to it, a bool made
// 0 or 1 first, and a string encoded as UTF-16. A `null` for an option of
// one is the NULL it already is.
func (m *cMarshal) aliasArg(fn *ir.Func, p *ir.Param, arg *ir.CallArg) error {
	if _, done := arg.Value.(*ir.CConvert); done {
		return nil
	}
	a := p.CAlias
	v := arg.Value
	switch a.Type.Kind {
	case ir.TypeString:
		if p.Type.Kind == ir.TypeOption {
			s, err := optionString(fn, p.Type, v)
			if err != nil || s == nil {
				return err
			}
			v = s
		}
	case ir.TypeBool:
		m.index(fn.Pkg)
		conv := m.conv[ir.BuiltinCBool]
		if conv == nil {
			return fmt.Errorf("C function %q takes a %s, a bool, and no package it or the program imports declares #[builtin(%q)], which sngl:x/c does", fn.Foreign.Name, a.Name, string(ir.BuiltinCBool))
		}
		v = convertArg(conv, v)
	}
	arg.Value = ir.NewCAliasConvert(a, v)
	return nil
}

func marshalKind(t *ir.Type) ir.BuiltinKind {
	if t == nil {
		return ir.BuiltinNone
	}
	switch t.Kind {
	case ir.TypeString:
		return ir.BuiltinCString
	case ir.TypeInt:
		return ir.BuiltinCInt
	case ir.TypeFloat:
		return ir.BuiltinCDouble
	case ir.TypeBool:
		return ir.BuiltinCBool
	}
	return ir.BuiltinNone
}

// optionString is the string an argument for an `option[string]` parameter
// is, which crosses as a C string, or nil for a `null`, which is the NULL it
// already is. A value that may be either has no conversion a call can be
// written with.
func optionString(fn *ir.Func, param *ir.Type, arg ir.Expr) (ir.Expr, error) {
	if param == nil || param.Kind != ir.TypeOption || len(param.Elems) != 1 || param.Elems[0].Kind != ir.TypeString {
		return nil, nil
	}
	if conv, ok := arg.(*ir.Conversion); ok && ir.IsOptionWrap(conv) {
		arg = conv.Operand
	}
	switch t := arg.ExprType(); {
	case t != nil && t.Kind == ir.TypeString:
		return arg, nil
	case t != nil && t.Kind == ir.TypeOption && !isNull(arg):
		return nil, fmt.Errorf("C function %q takes an option[string], which crosses as a C string or NULL, and is handed a value that may be either", fn.Foreign.Name)
	}
	return nil, nil
}

// isNull reports whether e is `null`: the literal the optimizer folds it to,
// or the predeclared name in a body it never saw, either converted to the
// option it stands for.
func isNull(e ir.Expr) bool {
	if conv, ok := e.(*ir.Conversion); ok {
		e = conv.Operand
	}
	switch x := e.(type) {
	case *ir.Literal:
		return x.Type != nil && x.Type.Kind == ir.TypeNull
	case *ir.Ident:
		v, ok := x.Sym.(*ir.Var)
		return ok && v.Builtin == ir.BuiltinNull
	}
	return false
}

// convertArg is arg made through conv: the conversion a cast declares, or a
// bodied one's `=>` expression with arg in place of its parameter, since a
// library function is emitted only when the program reaches it.
func convertArg(conv *ir.Func, arg ir.Expr) ir.Expr {
	if body := exprBody(conv); conv.Foreign.Name == "" && body != nil && len(conv.Params) == 1 {
		param := conv.Params[0]
		out := ir.CloneExprSharingDecls(body)
		if id, ok := out.(*ir.Ident); ok && id.Sym == param {
			return arg
		}
		_ = ir.Rewrite(out, func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok && id.Sym == param {
				return arg, nil
			}
			return n, nil
		})
		return out
	}
	if conv.IsCCast() {
		return ir.NewCConvert(conv, arg)
	}
	return &ir.Call{Type: conv.Return, Func: conv, Args: []ir.CallArg{{Value: arg}}}
}

func exprBody(fn *ir.Func) ir.Expr {
	if len(fn.Block) != 1 {
		return nil
	}
	if r, ok := fn.Block[0].(*ir.Return); ok {
		return r.Value
	}
	return nil
}
