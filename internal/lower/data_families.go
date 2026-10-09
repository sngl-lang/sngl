package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// DataFamilies builds, at each call site, the list a data family's slot is
// read as (ir.Component.Data, ir.SlotDecl.Values), and drops the population it
// was written as. What a caller writes is members, and `if`s, `for`s and
// components that compose members; what the container is handed is the
// records those render, in order, bound to the slot's prop like any other
// argument -- so reactivity, a component built at run time, the interpreter
// and every backend read it as the prop it is.
//
// A component composing members is spliced first, which is what puts the
// members where the list can be read off them: a composition is how members
// got there, not a member. One with state of its own is refused, since it is
// spliced away.
//
// It runs on checked IR ahead of the first optimize, which would otherwise
// fold an unwritten prop to its empty default, and ahead of the interpreter.
// analyze, when non-nil, gives a lambda it synthesizes its reads and purity.
func DataFamilies(pkg *ir.Package, analyze func(owner *ir.Component, fn *ir.Func)) error {
	if pkg == nil {
		return nil
	}
	// A population may be written in any package the program renders from --
	// an md: document's tables, a library body -- and each is spliced into
	// the program only later, so every package the import closure reaches is
	// read here. A package read before is left as it was: the population has
	// been consumed and the prop written.
	d := &dataFamilies{analyze: analyze}
	seen := map[*ir.Package]bool{}
	var visit func(p *ir.Package)
	visit = func(p *ir.Package) {
		if p == nil || seen[p] || d.err != nil {
			return
		}
		seen[p] = true
		for _, o := range ir.Owners(p) {
			d.owner = o.Comp
			d.walk(*o.Body)
			if d.err != nil {
				return
			}
		}
		for _, imp := range p.Imports {
			visit(imp.Pkg)
		}
	}
	visit(pkg)
	return d.err
}

type dataFamilies struct {
	owner   *ir.Component
	analyze func(owner *ir.Component, fn *ir.Func)
	err     error
}

func (d *dataFamilies) walk(stmts []ir.Stmt) {
	for _, s := range stmts {
		if d.err != nil {
			return
		}
		if n, ok := s.(*ir.NodeInst); ok {
			d.node(n)
		}
		for _, b := range ir.ViewBlocks(s) {
			d.walk(*b)
		}
	}
}

// node binds each of n's data slots to the list its population renders.
func (d *dataFamilies) node(n *ir.NodeInst) {
	if n.Component == nil {
		return
	}
	for _, slot := range n.Component.Slots {
		if slot.Values == nil {
			continue
		}
		if list := d.slotList(n, slot, 0); list != nil {
			n.Props = append(n.Props, ir.Arg{Name: slot.Name, Value: list})
		}
	}
}

// takePopulation removes from n what its call site wrote in slot, and
// returns it: the bare children for a rest slot and the population written by
// name.
func takePopulation(n *ir.NodeInst, slot *ir.SlotDecl) []ir.Stmt {
	var pop []ir.Stmt
	if slot.Rest {
		pop, n.Children = n.Children, nil
	}
	if sc := n.Slots[slot.Name]; sc != nil {
		pop = append(pop, sc.Body...)
		delete(n.Slots, slot.Name)
	}
	return pop
}

// slotList is the list of records n's population of the data slot renders,
// or nil where the call site gave the list itself.
func (d *dataFamilies) slotList(n *ir.NodeInst, slot *ir.SlotDecl, depth int) ir.Expr {
	pop := takePopulation(n, slot)
	if n.Prop(slot.Name) != nil {
		if len(pop) > 0 {
			d.fail(n, "%s is given %q twice: as a list and as the members written in it", n.Component.Name, slot.Name)
		}
		return nil
	}
	elems := d.elems(ir.TypeFamily(slot.Content), pop, depth)
	if d.err != nil {
		return nil
	}
	return &ir.ListLit{Type: slot.Values.Type, Elems: elems}
}

// maxComposeDepth bounds a composition of members that renders a composition,
// which only one rendering itself reaches.
const maxComposeDepth = 16

// elems is the list elements a population renders: a record per member, and
// a spread for what an `if`, a `for` or a forwarded data slot renders.
func (d *dataFamilies) elems(fam *ir.Component, stmts []ir.Stmt, depth int) []ir.Expr {
	var out []ir.Expr
	list := ir.ListOf(recordType(fam))
	for _, s := range stmts {
		if d.err != nil {
			return nil
		}
		switch n := s.(type) {
		case *ir.NodeInst:
			c := n.Component
			switch {
			case c == fam:
				if depth > maxComposeDepth {
					d.fail(n, "%s nests records deeper than %d", fam.Name, maxComposeDepth)
					return nil
				}
				out = append(out, d.record(fam, n, depth))
			case c != nil && c.Family == fam && !c.Bodyless:
				if depth > maxComposeDepth {
					d.fail(n, "%s renders itself among the records of %s", c.Name, fam.Name)
					return nil
				}
				if len(c.Vars) > 0 || len(c.Funcs) > 0 {
					d.fail(n, "%s composes members of %s, which are read as a value: what composes them keeps no state of its own", c.Name, fam.Name)
					return nil
				}
				body := substituteParams(deepCloneStmts(c.Stmts), pageBindings(c, n))
				body = substituteSlots(body, n)
				out = append(out, d.elems(fam, body, depth+1)...)
			default:
				d.fail(n, "%s is not a %s", n.Name, fam.Name)
			}
		case *ir.SlotInst:
			// A data slot of the component the population was written in,
			// forwarded: its list, spread where it stands.
			decl := n.Decl
			if decl == nil && d.owner != nil {
				for _, sl := range d.owner.Slots {
					if sl.Name == n.Name {
						decl = sl
					}
				}
			}
			if decl == nil || decl.Values == nil {
				d.fail(n, "slot %s holds no members of %s to read", n.Name, fam.Name)
				return nil
			}
			out = append(out, &ir.Spread{Type: list, Operand: &ir.Ident{Name: decl.Values.Name, Type: list, Sym: decl.Values.Sym}})
		case *ir.If:
			out = append(out, &ir.Spread{Type: list, Operand: &ir.Ternary{
				Type: list,
				Cond: n.Cond,
				Then: &ir.ListLit{Type: list, Elems: d.elems(fam, n.Body, depth)},
				Else: &ir.ListLit{Type: list, Elems: d.elems(fam, n.Else, depth)},
			}})
		case *ir.For:
			if e := d.loop(fam, n, depth); e != nil {
				out = append(out, e)
			}
		default:
			d.fail(s, "a population of %s holds records, and `if`s and `for`s around them", fam.Name)
		}
	}
	return out
}

// loop is what a `for` among members renders: a map over its iterable, and a
// filter before it where the body is a member under an `if`. A body of more
// than one member would map to a list of lists, which nothing flattens yet.
func (d *dataFamilies) loop(fam *ir.Component, fs *ir.For, depth int) ir.Expr {
	list := ir.ListOf(recordType(fam))
	if fs.Iter == nil || fs.Iter.ExprType() == nil || fs.Iter.ExprType().Kind != ir.TypeList ||
		fs.Value != "" || len(fs.Else) > 0 {
		d.fail(fs, "a `for` among the records of %s walks a list with one variable and no else", fam.Name)
		return nil
	}
	body := d.elems(fam, fs.Body, depth)
	if d.err != nil {
		return nil
	}
	var cond ir.Expr
	if len(body) == 1 {
		if sp, ok := body[0].(*ir.Spread); ok {
			if t, ok := sp.Operand.(*ir.Ternary); ok {
				then, _ := t.Then.(*ir.ListLit)
				els, _ := t.Else.(*ir.ListLit)
				if then != nil && els != nil && len(then.Elems) == 1 && len(els.Elems) == 0 {
					cond, body = t.Cond, then.Elems
				}
			}
		}
	}
	if len(body) != 1 {
		d.fail(fs, "a `for` among the records of %s renders one member each time round", fam.Name)
		return nil
	}
	if _, spread := body[0].(*ir.Spread); spread {
		d.fail(fs, "a `for` among the records of %s renders one member each time round", fam.Name)
		return nil
	}
	iter := fs.Iter
	if cond != nil {
		iter = d.method(iter, "filter", fs.Iter.ExprType(), d.lambda(fs, cond, ir.TypBool))
	}
	return &ir.Spread{Type: list, Operand: d.method(iter, "map", list, d.lambda(fs, body[0], recordType(fam)))}
}

// lambda is `func(<loop variable>) => e`, e reading the loop's variable
// through the lambda's parameter.
func (d *dataFamilies) lambda(fs *ir.For, e ir.Expr, ret *ir.Type) *ir.Lambda {
	name := fs.Key
	if name == "" {
		name = "_"
	}
	param := &ir.Param{Name: name, Type: fs.ElemType}
	body := ir.CloneExprSharingDecls(e)
	if fs.KeySym != nil {
		body = newExprWalker(func(x ir.Expr) ir.Expr {
			if id, ok := x.(*ir.Ident); ok && id.Sym == fs.KeySym {
				return &ir.Ident{Name: id.Name, Type: id.Type, Sym: param}
			}
			return x
		}).expr(body)
	}
	fn := &ir.Func{Params: []*ir.Param{param}, Return: ret, Block: []ir.Stmt{&ir.Return{Value: body}}}
	if d.analyze != nil {
		d.analyze(d.owner, fn)
	}
	return &ir.Lambda{Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Func: fn}
}

// method is a call of one of sngl:builtin's list methods on recv.
func (d *dataFamilies) method(recv ir.Expr, name string, ret *ir.Type, fn *ir.Lambda) ir.Expr {
	m, ok := ir.MemberOf(recv.ExprType().Decl, name)
	if !ok {
		panic(fmt.Sprintf("DataFamilies: list has no %s", name))
	}
	return &ir.Call{Type: ret, Func: m, Args: []ir.CallArg{{Value: recv}, {Value: fn}}}
}

// record is a data component's record: each of its props, as the call site
// wrote it or as its default, and each of its data slots, the list its
// population renders.
func (d *dataFamilies) record(fam *ir.Component, n *ir.NodeInst, depth int) ir.Expr {
	lit := &ir.StructLit{Type: recordType(fam), Def: fam.Record}
	for _, fp := range fam.Props {
		if slot := fam.DataSlotOf(fp); slot != nil {
			if list := d.slotList(n, slot, depth+1); list != nil {
				lit.Fields = append(lit.Fields, ir.FieldInit{Name: fp.Name, Value: list})
				continue
			}
			if d.err != nil {
				return nil
			}
		}
		v := n.Prop(fp.Name)
		if v == nil && fp.Default != nil {
			v = ir.CloneExprSharingDecls(fp.Default)
		}
		if v == nil {
			continue
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: fp.Name, Value: v})
	}
	for _, e := range fam.Events {
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: e.Name, Value: d.eventFunc(e, n)})
	}
	return lit
}

// eventFunc is what a record's event field holds: the handler n's call site
// wrote, as a function taking every parameter the event declares -- a handler
// may bind fewer -- or, where none was written, one that does nothing.
func (d *dataFamilies) eventFunc(e *ir.EventDecl, n *ir.NodeInst) *ir.Lambda {
	typ := ir.EventFuncType(e)
	fn := &ir.Func{}
	for _, h := range n.Handlers {
		if h.Name == e.Name && h.Func != nil {
			fn = h.Func
			break
		}
	}
	params := make([]*ir.Param, len(e.Params))
	for i, p := range e.Params {
		if i < len(fn.Params) {
			params[i] = fn.Params[i]
			continue
		}
		params[i] = &ir.Param{Name: fmt.Sprintf("__unbound%d", i), Type: p.Type}
	}
	fn.Params = params
	fn.Return = nil
	if d.analyze != nil {
		d.analyze(d.owner, fn)
	}
	return &ir.Lambda{Type: typ, Func: fn}
}

func recordType(fam *ir.Component) *ir.Type {
	return &ir.Type{Kind: ir.TypeStruct, Decl: fam.Record}
}

func (d *dataFamilies) fail(at any, format string, args ...any) {
	if d.err != nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	switch n := at.(type) {
	case *ir.NodeInst:
		d.err = fmt.Errorf("%s: %s", ir.NodePos(n), msg)
		return
	case *ir.For:
		if n.AST != nil {
			d.err = fmt.Errorf("%s: %s", n.AST.Pos, msg)
			return
		}
	case *ir.SlotInst:
		if n.AST != nil {
			d.err = fmt.Errorf("%s: %s", n.AST.Pos, msg)
			return
		}
	}
	d.err = fmt.Errorf("%s", msg)
}
