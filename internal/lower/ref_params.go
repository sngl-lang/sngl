package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passRefParams lowers the two parameters a call passes by reference: a
// method's ambient `this`, which is the receiver's own value, and a
// parameter declared `ref[T]`, which `&x` hands the caller's cell. A named
// parameter of any other type is a value (passValueCopy).
//
// It is copy-in, copy-out, which no host needs telling about. A function that
// writes such a parameter binds its own copy at entry and hands the copy back,
// and every call assigns what it got back to the place it passed: `q.bump()`
// is `q = bump(q)`. One that returns a value of its own as well returns a
// record of the value and each parameter it writes, read back field by field
// from a temp the call is hoisted into. A pointer would have done on Go, but
// not on Compose, which recomposes on a state var reassigned to a value that
// differs from the old one -- an object mutated in place compares equal to
// itself -- and an explicit assignment is also the write every later pass
// (reactivity, an effect's reports) already reads. A by-reference parameter
// the function only reads is a plain value: the `&` at the call comes off.
//
// After passRefLoop, so an element bound with `&` is `xs[i]` by now and is
// written back to as any other place is.
var passRefParams = pass{
	name:    "RefParams",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, _ Features, _ Options) error { return RefParams(pkg) },
}

// RefParams is passRefParams for a pipeline that lowers nothing else: the
// interpreter runs it in Prepare, so a call writes back on every target the
// same way.
func RefParams(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	rp := &refParams{pkg: pkg, outs: map[*ir.Func][]int{}, done: map[*ir.Call]bool{}}
	funcs := rp.funcs()
	rp.findOuts(funcs)
	if err := rp.refuseValues(funcs); err != nil {
		return err
	}
	for _, f := range funcs {
		rp.declareResult(f)
	}
	for _, block := range imperativeBlocks(pkg) {
		out, err := rp.block(*block)
		if err != nil {
			return err
		}
		*block = out
	}
	if err := rp.refuseUnrewritten(); err != nil {
		return err
	}
	for _, f := range funcs {
		rp.rewriteBody(f)
		// A write-back may land on a named parameter, which is a value the
		// function then has to own (passValueCopy's rule).
		ownModifiedParams(f)
	}
	// What an inliner made of a by-reference argument read through, `*&x`,
	// is the place itself.
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		if u, ok := e.(*ir.Unary); ok && u.Op == ast.UnaryDeref {
			if a, ok := u.Operand.(*ir.Unary); ok && a.Op == ast.UnaryAddr {
				return a.Operand, nil
			}
		}
		return e, nil
	})
}

type refParams struct {
	pkg *ir.Package
	// outs is each function's by-reference parameters its body writes, by
	// index, in order.
	outs map[*ir.Func][]int
	// result is the record a function returns where it hands back more than
	// one value; nil where it hands back only the one parameter it writes.
	result map[*ir.Func]*ir.StructDef
	done   map[*ir.Call]bool
	temps  int
}

func (rp *refParams) funcs() []*ir.Func {
	out := paramOwners(rp.pkg)
	seen := map[*ir.Func]bool{}
	for _, f := range out {
		seen[f] = true
	}
	for _, f := range rp.pkg.Funcs {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// byRef reports whether a call passes p by reference: a method's ambient
// `this`, or a `ref[T]`. A component's `this` is its instance and no value.
func byRef(p *ir.Param) bool {
	if p == nil || p.Type == nil {
		return false
	}
	if p.Type.Kind == ir.TypeRef {
		return true
	}
	if !p.Receiver || p.Name != ir.ReceiverParam {
		return false
	}
	switch p.Type.Kind {
	case ir.TypeComponent, ir.TypeInstance:
		return false
	}
	return true
}

// findOuts works out which by-reference parameters each function writes, to
// a fixed point: a method handing its `this` to another that writes it writes
// it too.
func (rp *refParams) findOuts(funcs []*ir.Func) {
	for changed := true; changed; {
		changed = false
		for _, f := range funcs {
			for i, p := range f.Params {
				if !byRef(p) || rp.isOut(f, i) {
					continue
				}
				if rp.writes(f.Block, p) {
					rp.outs[f] = append(rp.outs[f], i)
					changed = true
				}
			}
		}
	}
	for _, idx := range rp.outs {
		slices.Sort(idx)
	}
}

func (rp *refParams) isOut(f *ir.Func, i int) bool {
	return slices.Contains(rp.outs[f], i)
}

// writes reports whether body writes p: an assignment or toggle rooted at it,
// a mutating builtin called on it, a `&` loop over it, or a call passing it to
// a parameter its callee writes.
func (rp *refParams) writes(body []ir.Stmt, p *ir.Param) bool {
	found := false
	_ = ir.Walk(body, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Assign:
			found = found || rootedAtSym(x.Target, p)
		case *ir.Toggle:
			found = found || rootedAtSym(x.Target, p)
		case *ir.For:
			found = found || (x.RefElem && rootedAtSym(x.Iter, p))
		case *ir.Call:
			if x.Func == nil {
				break
			}
			if x.Func.Intrinsic != "" && len(x.Args) > 0 && rootedAtSym(x.Args[0].Value, p) {
				if def, ok := ir.IntrinsicByName(x.Func.Intrinsic); ok && def.MutatesReceiver {
					found = true
				}
			}
			for _, k := range rp.outs[x.Func] {
				if a := argAt(x, k); a != nil && rootedAtSym(a.Value, p) {
					found = true
				}
			}
		}
		if found {
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// rootedAtSym reports whether e is sym, or a field, element, dereference or
// reference down from it.
func rootedAtSym(e ir.Expr, sym ir.Symbol) bool {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Unary:
			if x.Op != ast.UnaryDeref && x.Op != ast.UnaryAddr {
				return false
			}
			e = x.Operand
		case *ir.Ident:
			return x.Sym == sym
		default:
			return false
		}
	}
}

// argAt is the argument bound to f's parameter i at c, by name where the
// call named it.
func argAt(c *ir.Call, i int) *ir.CallArg {
	if c.Func == nil || i >= len(c.Func.Params) {
		return nil
	}
	name := c.Func.Params[i].Name
	for k := range c.Args {
		if c.Args[k].Name != "" && c.Args[k].Name == name {
			return &c.Args[k]
		}
	}
	if i < len(c.Args) && c.Args[i].Name == "" {
		return &c.Args[i]
	}
	return nil
}

// refuseValues refuses a function that writes back taken as a value, and a
// lambda that would: what calls a value cannot know where to write.
func (rp *refParams) refuseValues(funcs []*ir.Func) error {
	var err error
	_ = ir.Walk(rp.pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Ident:
			if f, ok := x.Sym.(*ir.Func); ok && len(rp.outs[f]) > 0 {
				err = fmt.Errorf("%s: %s writes %s, which a call hands back to the place it passed; taken as a value, nothing knows that place", exprPos(x), f.Name, rp.outNames(f))
			}
		case *ir.Lambda:
			if x.Func != nil && len(rp.outs[x.Func]) > 0 {
				err = fmt.Errorf("%s: a function literal may not write a parameter it takes by reference: what calls it cannot know where to write", lambdaPos(x))
			}
		}
		if err != nil {
			return ir.SkipAll
		}
		return nil
	})
	return err
}

func lambdaPos(l *ir.Lambda) string {
	if l.AST != nil {
		return l.AST.Pos.String()
	}
	return "<unknown>"
}

func (rp *refParams) outNames(f *ir.Func) string {
	var s strings.Builder
	for i, k := range rp.outs[f] {
		if i > 0 {
			s.WriteString(" and ")
		}
		s.WriteString(strconv.Quote(f.Params[k].Name))
	}
	return s.String()
}

// declareResult gives a function that hands back more than one value the
// record it returns them in.
func (rp *refParams) declareResult(f *ir.Func) {
	outs := rp.outs[f]
	if len(outs) == 0 {
		return
	}
	void := f.Return == nil || f.Return.Kind == ir.TypeVoid
	if void && len(outs) == 1 {
		return
	}
	def := &ir.StructDef{}
	def.Name = resultName(f)
	def.Pkg = rp.pkg
	if !void {
		def.Fields = append(def.Fields, &ir.StructField{Name: "result", Type: f.Return, Default: ir.ZeroExpr(f.Return)})
	}
	for _, k := range outs {
		t := valueType(f.Params[k].Type)
		def.Fields = append(def.Fields, &ir.StructField{Name: "out" + strconv.Itoa(k), Type: t, Default: ir.ZeroExpr(t)})
	}
	rp.pkg.Structs = append(rp.pkg.Structs, def)
	if rp.result == nil {
		rp.result = map[*ir.Func]*ir.StructDef{}
	}
	rp.result[f] = def
}

func resultName(f *ir.Func) string {
	if r := f.RecvName(); r != "" {
		return r + "_" + f.Name + "__result"
	}
	return f.Name + "__result"
}

// valueType is t with a `ref[T]` taken off.
func valueType(t *ir.Type) *ir.Type {
	if t != nil && t.Kind == ir.TypeRef && len(t.Elems) > 0 {
		return t.Elems[0]
	}
	return t
}

// block rewrites each call in stmts that writes back, and the blocks nested
// in them.
func (rp *refParams) block(stmts []ir.Stmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.If:
			var err error
			if x.Body, err = rp.block(x.Body); err != nil {
				return nil, err
			}
			if x.Else, err = rp.block(x.Else); err != nil {
				return nil, err
			}
		case *ir.Match:
			for _, arm := range x.Arms {
				var err error
				if arm.Body, err = rp.block(arm.Body); err != nil {
					return nil, err
				}
			}
		case *ir.For:
			var err error
			if x.Body, err = rp.block(x.Body); err != nil {
				return nil, err
			}
			if x.Else, err = rp.block(x.Else); err != nil {
				return nil, err
			}
		}
		pre, repl, err := rp.stmt(s)
		if err != nil {
			return nil, err
		}
		out = append(out, pre...)
		out = append(out, repl...)
	}
	return out, nil
}

// stmt is s with each call in its own expressions that writes back hoisted
// ahead of it: the statements to run first, and what stands in for s.
func (rp *refParams) stmt(s ir.Stmt) (pre, repl []ir.Stmt, err error) {
	h := &refHoister{rp: rp, at: s}
	switch x := s.(type) {
	case *ir.CallStmt:
		if x.Call != nil && rp.writesBack(x.Call) {
			x.Call = h.args(x.Call)
			return h.pre, h.callStmt(x.Call), h.err
		}
		if x.Call != nil {
			if c, ok := h.expr(x.Call).(*ir.Call); ok {
				x.Call = c
			}
		}
	case *ir.Assign:
		x.Target = h.expr(x.Target)
		x.Value = h.expr(x.Value)
	case *ir.Toggle:
		x.Target = h.expr(x.Target)
	case *ir.LocalVar:
		x.Init = h.expr(x.Init)
	case *ir.Return:
		x.Value = h.expr(x.Value)
	case *ir.If:
		x.Cond = h.expr(x.Cond)
	case *ir.Match:
		x.Subject = h.expr(x.Subject)
	case *ir.For:
		if t := x.Iter; t != nil && t.ExprType() != nil && t.ExprType().Kind == ir.TypeBool {
			h.conditional++
		}
		x.Iter = h.expr(x.Iter)
	case *ir.Emit:
		for i := range x.Args {
			x.Args[i].Value = h.expr(x.Args[i].Value)
		}
	}
	return h.pre, []ir.Stmt{s}, h.err
}

func (rp *refParams) writesBack(c *ir.Call) bool {
	return c != nil && c.Func != nil && len(rp.outs[c.Func]) > 0 && !rp.done[c]
}

// refHoister rewrites the expressions of one statement.
type refHoister struct {
	rp  *refParams
	at  ir.Stmt
	pre []ir.Stmt
	err error
	// conditional counts the enclosing positions that may not run, or may run
	// more than once: the right of && and ||, a ternary's branches, and the
	// head of a condition loop.
	conditional int
}

func (h *refHoister) expr(e ir.Expr) ir.Expr {
	if e == nil || h.err != nil {
		return e
	}
	switch x := e.(type) {
	case *ir.Lambda:
		// Its body is a block of its own, reached by imperativeBlocks.
		return e
	case *ir.Binary:
		x.Left = h.expr(x.Left)
		if x.Op == ast.BinAnd || x.Op == ast.BinOr {
			h.conditional++
			x.Right = h.expr(x.Right)
			h.conditional--
		} else {
			x.Right = h.expr(x.Right)
		}
	case *ir.Ternary:
		x.Cond = h.expr(x.Cond)
		h.conditional++
		x.Then = h.expr(x.Then)
		x.Else = h.expr(x.Else)
		h.conditional--
	case *ir.MatchExpr:
		x.Subject = h.expr(x.Subject)
		h.conditional++
		for _, arm := range x.Arms {
			arm.Value = h.expr(arm.Value)
		}
		h.conditional--
	case *ir.Unary:
		x.Operand = h.expr(x.Operand)
	case *ir.Select:
		x.Operand = h.expr(x.Operand)
	case *ir.Index:
		x.Operand = h.expr(x.Operand)
		x.Idx = h.expr(x.Idx)
	case *ir.Conversion:
		x.Operand = h.expr(x.Operand)
	case *ir.Spread:
		x.Operand = h.expr(x.Operand)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = h.expr(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = h.expr(x.Fields[i].Value)
		}
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = h.expr(x.Entries[i].Key)
			x.Entries[i].Value = h.expr(x.Entries[i].Value)
		}
	case *ir.Call:
		x.Receiver = h.expr(x.Receiver)
		x.Callee = h.expr(x.Callee)
		if h.rp.writesBack(x) {
			x = h.args(x)
			return h.hoist(x)
		}
		if x.Func != nil {
			return h.args(x)
		}
		for i := range x.Args {
			x.Args[i].Value = h.expr(x.Args[i].Value)
		}
	}
	return e
}

// args rewrites c's arguments: each nested call first, and then each one
// bound to a by-reference parameter, which passes the value it names.
func (h *refHoister) args(c *ir.Call) *ir.Call {
	for i := range c.Args {
		c.Args[i].Value = h.expr(c.Args[i].Value)
	}
	for i, p := range c.Func.Params {
		if !byRef(p) {
			continue
		}
		if a := argAt(c, i); a != nil {
			a.Value = derefArg(a.Value)
		}
	}
	return c
}

// derefArg is what a by-reference argument passes: the value at the place
// `&x` names, or at a by-reference parameter of the caller's, which is a value
// itself once this pass is done. Any other read through a reference -- an
// element an unlowered `&` loop binds -- stays one.
func derefArg(e ir.Expr) ir.Expr {
	u, ok := e.(*ir.Unary)
	if !ok {
		return e
	}
	switch u.Op {
	case ast.UnaryAddr:
		return u.Operand
	case ast.UnaryDeref:
		if id, ok := u.Operand.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && byRef(p) {
				return id
			}
		}
	}
	return e
}

// writeTarget is where a write-back to the argument e lands: the place it
// names, written through where it is a reference.
func writeTarget(e ir.Expr) ir.Expr {
	e = derefArg(e)
	if t := e.ExprType(); t != nil && t.Kind == ir.TypeRef {
		if id, ok := e.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && byRef(p) {
				return e
			}
		}
		return &ir.Unary{Type: t.Elems[0], Op: ast.UnaryDeref, Operand: e}
	}
	return e
}

// targets is where c's write-back lands for each parameter it writes: the
// place its argument names, or nil where it names none -- a value the call
// made, which nothing else holds.
func (h *refHoister) targets(c *ir.Call) []ir.Expr {
	var out []ir.Expr
	for _, k := range h.rp.outs[c.Func] {
		a := argAt(c, k)
		if a == nil || !assignable(a.Value) {
			out = append(out, nil)
			continue
		}
		out = append(out, writeTarget(ir.CloneExprSharingDecls(a.Value)))
	}
	return out
}

// assignable reports whether e names a place a program may write.
func assignable(e ir.Expr) bool {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Unary:
			if x.Op != ast.UnaryDeref {
				return false
			}
			e = x.Operand
		case *ir.Ident:
			switch s := x.Sym.(type) {
			case *ir.Var:
				return !s.IsConst
			case *ir.Param, *ir.LoopVar:
				return true
			}
			return false
		default:
			return false
		}
	}
}

// callStmt is a call made for what it does, writing back: one parameter
// assigned straight from the call, or the record bound to a temp and read
// back field by field.
func (h *refHoister) callStmt(c *ir.Call) []ir.Stmt {
	h.rp.done[c] = true
	ts := h.targets(c)
	def := h.rp.result[c.Func]
	if def == nil {
		c.Type = valueType(c.Func.Params[h.rp.outs[c.Func][0]].Type)
		if ts[0] == nil {
			return []ir.Stmt{&ir.CallStmt{Call: c}}
		}
		return []ir.Stmt{&ir.Assign{Target: ts[0], Op: ast.AssignSet, Value: c}}
	}
	tmp, stmts := h.bind(c, def, ts)
	_ = tmp
	return stmts
}

// hoist binds c, a call in an expression, to a temp ahead of the statement,
// writes back from it, and stands the temp's result in for the call.
func (h *refHoister) hoist(c *ir.Call) ir.Expr {
	if h.conditional > 0 {
		h.err = fmt.Errorf("%s: %s writes %s back where this call passed it, and it is made where it may not run, or may run again: call it in a statement of its own", callPos(c, h.at), c.Func.Name, h.rp.outNames(c.Func))
		return c
	}
	h.rp.done[c] = true
	def := h.rp.result[c.Func]
	ts := h.targets(c)
	tmp, stmts := h.bind(c, def, ts)
	h.pre = append(h.pre, stmts...)
	return &ir.Select{Operand: tmp, Field: "result", Type: def.Fields[0].Type}
}

// bind binds c's record to a fresh temp and assigns each field it hands back
// to its target.
func (h *refHoister) bind(c *ir.Call, def *ir.StructDef, ts []ir.Expr) (*ir.Ident, []ir.Stmt) {
	t := &ir.Type{Kind: ir.TypeStruct, Decl: def}
	c.Type = t
	name := "__ref" + strconv.Itoa(h.rp.temps)
	h.rp.temps++
	v := &ir.Var{Type: t}
	v.Name = name
	tmp := func() *ir.Ident { return &ir.Ident{Name: name, Sym: v, Type: t} }
	stmts := []ir.Stmt{&ir.LocalVar{Name: name, Type: t, Init: c, Sym: v}}
	for i, k := range h.rp.outs[c.Func] {
		if ts[i] == nil {
			continue
		}
		field := "out" + strconv.Itoa(k)
		stmts = append(stmts, &ir.Assign{Target: ts[i], Op: ast.AssignSet, Value: &ir.Select{Operand: tmp(), Field: field, Type: valueType(c.Func.Params[k].Type)}})
	}
	return tmp(), stmts
}

// refuseUnrewritten refuses a call that writes back where no statement holds
// it -- a view's prop, a var's initializer -- since there is nowhere to write
// what it hands back from.
func (rp *refParams) refuseUnrewritten() error {
	var err error
	_ = ir.Walk(rp.pkg, func(n ir.Node) error {
		if c, ok := n.(*ir.Call); ok && rp.writesBack(c) {
			err = fmt.Errorf("%s: %s writes %s back where the call passed it, and a call here has no statement to write it from; make it in a handler or a function", callPos(c, nil), c.Func.Name, rp.outNames(c.Func))
			return ir.SkipAll
		}
		return nil
	})
	return err
}

// rewriteBody makes f's by-reference parameters values: each read through
// one reads it, and each it writes is bound to a copy at entry that the body
// names and every return hands back.
func (rp *refParams) rewriteBody(f *ir.Func) {
	own := map[*ir.Param]*ir.Var{}
	var prelude []ir.Stmt
	for _, k := range rp.outs[f] {
		p := f.Params[k]
		t := valueType(p.Type)
		v := &ir.Var{Type: t}
		v.Name = p.Name + "__own"
		own[p] = v
		var init ir.Expr = &ir.Ident{Name: p.Name, Sym: p, Type: t}
		if ir.HoldsValue(t) && writesInside(f.Block, p) {
			init = ir.ValueCopy(init)
		}
		prelude = append(prelude, &ir.LocalVar{Name: v.Name, Type: t, Init: init, Sym: v})
	}
	var refs []*ir.Param
	for _, p := range f.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeRef {
			refs = append(refs, p)
		}
	}
	if len(own) == 0 && len(refs) == 0 {
		return
	}
	_ = ir.RewriteExprs(f.Block, func(e ir.Expr) (ir.Expr, error) {
		switch x := e.(type) {
		case *ir.Unary:
			if x.Op == ast.UnaryDeref {
				if id, ok := x.Operand.(*ir.Ident); ok {
					if p, ok := id.Sym.(*ir.Param); ok && byRef(p) {
						return paramRead(p, own), nil
					}
				}
			}
		case *ir.Ident:
			if p, ok := x.Sym.(*ir.Param); ok && byRef(p) {
				return paramRead(p, own), nil
			}
		}
		return e, nil
	})
	for _, p := range refs {
		p.Type = valueType(p.Type)
	}
	if len(own) == 0 {
		return
	}
	def := rp.result[f]
	handBack := func(result ir.Expr) ir.Expr {
		if def == nil {
			p := f.Params[rp.outs[f][0]]
			return &ir.Ident{Name: own[p].Name, Sym: own[p], Type: own[p].Type}
		}
		t := &ir.Type{Kind: ir.TypeStruct, Decl: def}
		lit := &ir.StructLit{Type: t, Def: def}
		if result != nil {
			lit.Fields = append(lit.Fields, ir.FieldInit{Name: "result", Value: result})
		}
		for _, k := range rp.outs[f] {
			v := own[f.Params[k]]
			lit.Fields = append(lit.Fields, ir.FieldInit{Name: "out" + strconv.Itoa(k), Value: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type}})
		}
		return lit
	}
	rewriteReturns(f.Block, handBack)
	if n := len(f.Block); n == 0 {
		f.Block = append(f.Block, &ir.Return{Value: handBack(nil)})
	} else if _, ok := f.Block[n-1].(*ir.Return); !ok {
		f.Block = append(f.Block, &ir.Return{Value: handBack(nil)})
	}
	f.Block = append(prelude, f.Block...)
	if def != nil {
		f.Return = &ir.Type{Kind: ir.TypeStruct, Decl: def}
	} else {
		f.Return = own[f.Params[rp.outs[f][0]]].Type
	}
}

// writesInside reports whether body writes a part of p rather than replacing
// it whole. A copy at entry is what keeps such a write off the caller's value
// until the call hands it back; one the body only replaces -- as every
// write-back from another call does -- hands back a value nothing else holds.
func writesInside(body []ir.Stmt, p *ir.Param) bool {
	whole := func(e ir.Expr) bool {
		if u, ok := e.(*ir.Unary); ok && u.Op == ast.UnaryDeref {
			e = u.Operand
		}
		id, ok := e.(*ir.Ident)
		return ok && id.Sym == p
	}
	found := false
	_ = ir.Walk(body, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Assign:
			found = found || (!whole(x.Target) && rootedAtSym(x.Target, p))
		case *ir.Toggle:
			found = found || rootedAtSym(x.Target, p)
		case *ir.For:
			found = found || (x.RefElem && rootedAtSym(x.Iter, p))
		case *ir.Call:
			if x.Func != nil && x.Func.Intrinsic != "" && len(x.Args) > 0 && rootedAtSym(x.Args[0].Value, p) {
				if def, ok := ir.IntrinsicByName(x.Func.Intrinsic); ok && def.MutatesReceiver {
					found = true
				}
			}
		}
		if found {
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// paramRead is what a read of p becomes: the copy the body owns, or p itself
// where the body only reads it.
func paramRead(p *ir.Param, own map[*ir.Param]*ir.Var) ir.Expr {
	if v, ok := own[p]; ok {
		return &ir.Ident{Name: v.Name, Sym: v, Type: v.Type}
	}
	return &ir.Ident{Name: p.Name, Sym: p, Type: valueType(p.Type)}
}

// rewriteReturns replaces each return in stmts, outside a function literal,
// with the one handBack makes of its value.
func rewriteReturns(stmts []ir.Stmt, handBack func(ir.Expr) ir.Expr) {
	for _, s := range stmts {
		switch x := s.(type) {
		case *ir.Return:
			x.Value = handBack(x.Value)
		case *ir.If, *ir.For, *ir.Match:
			for _, b := range ir.TransparentBlocks(x) {
				rewriteReturns(*b, handBack)
			}
		}
	}
}
