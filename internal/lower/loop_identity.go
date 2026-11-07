package lower

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passLoopIdentity tells apart the copies of a keyed loop over a list whose
// elements share an identity. Every target keeps a copy's state under its
// identity (CopyIdentity), so two copies of "a" were one: on bubbletea they
// shared their cells, and an effect under the loop mounted once for both --
// the Pokédex asks again for a species by pushing its slug a second time, and
// the second copy never asked.
//
// The loop walks a list of records instead, `{id, elem}`, which a function the
// pass declares builds from the list in one pass: each element's identity,
// the second and later of one suffixed `#1`, `#2`. The loop's element is
// `rec.elem` wherever the body read it, and its identity is `rec.id`. Built
// from the iterable, so every walk that evaluates the head -- a settle's
// rebuilt loops, a handler's scan for its copy, a test's read of one -- sees
// the same identities the render did, which a counter beside the loop would
// not have given them.
//
// A loop whose element is bound by `&` is left as it is: its element is a
// place in the list, which a record's field is not. Its copies sharing a
// `key()` share it.
var passLoopIdentity = pass{
	name:    "LoopIdentity",
	enabled: func(Features) bool { return true },
	apply:   lowerLoopIdentity,
}

func lowerLoopIdentity(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &loopIdentityState{pkg: pkg}
	for _, c := range pkg.Components {
		st.stmts(c.Stmts)
		for _, fn := range c.Funcs {
			if fn != nil {
				st.stmts(fn.Block)
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			st.stmts(fn.Block)
		}
	}
	st.stmts(pkg.Body)
	pkg.Funcs = append(pkg.Funcs, st.added...)
	return nil
}

type loopIdentityState struct {
	pkg   *ir.Package
	added []*ir.Func
}

func (st *loopIdentityState) stmts(stmts []ir.Stmt) {
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if fs, ok := n.(*ir.For); ok {
			st.loop(fs)
		}
		return nil
	})
}

// loop rewrites fs to walk its records, where it is a keyed loop over a list.
func (st *loopIdentityState) loop(fs *ir.For) {
	if fs.Identity == nil || fs.RefElem || fs.Iter == nil {
		return
	}
	list := fs.Iter.ExprType()
	if list == nil || list.Kind != ir.TypeList || len(list.Elems) != 1 {
		return
	}
	elemSym := fs.KeySym
	if fs.ValueSym != nil {
		elemSym = fs.ValueSym
	}
	if elemSym == nil {
		return
	}
	elemT := list.Elems[0]
	def := &ir.StructDef{Anon: true, Fields: []*ir.StructField{
		{Name: "id", Type: ir.TypString},
		{Name: "elem", Type: elemT},
	}}
	recT := def.SymType()
	fn := st.identified(fs, elemSym, elemT, def, recT)

	// The record takes the element's name, which is what an error about the
	// loop's variable says and what the emitted loop reads: `it.elem`.
	rec := &ir.LoopVar{Name: elemSym.Name, Type: recT}
	recRef := func() *ir.Ident { return &ir.Ident{Name: rec.Name, Type: recT, Sym: rec, Synthesized: true} }
	field := func(name string, t *ir.Type) ir.Expr {
		return &ir.Select{Type: t, Operand: recRef(), Field: name}
	}
	readsElem := func(nd ir.Node) (ir.Node, error) {
		if id, ok := nd.(*ir.Ident); ok && id.Sym == ir.Symbol(elemSym) {
			return field("elem", id.Type), nil
		}
		return nd, nil
	}
	_ = ir.Rewrite(fs.Body, readsElem)
	_ = ir.Rewrite(fs.Else, readsElem)

	fs.Iter = &ir.Call{Type: ir.ListOf(recT), Func: fn, Args: []ir.CallArg{{Value: fs.Iter}}}
	fs.ElemType = recT
	if fs.ValueSym != nil {
		fs.Value, fs.ValueSym = rec.Name, rec
	} else {
		fs.Key, fs.KeySym = rec.Name, rec
	}
	fs.Identity = field("id", ir.TypString)
}

// identified declares the function a keyed loop's records come from:
//
//	func __identifiedN(__xs list[T]) list[{id string, elem T}] {
//	    var __seen map[string, int] = {}
//	    var __out list[{id string, elem T}] = []
//	    for var __x = __xs {
//	        var __id = <the loop's identity of __x>.replace("#", "##")
//	        var __n = __seen.get(__id, 0)
//	        __seen[__id] = __n + 1
//	        if __n > 0 { __id = __id + "#" + string(__n) }
//	        __out.push({id = __id, elem = __x})
//	    }
//	    return __out
//	}
//
// A repeat is suffixed `#n`, and every identity has its own `#`s doubled
// first, so no suffix spells another element's identity: undoubled, `["a",
// "a", "a#1"]` handed out `a#1` twice, and the two copies shared their cells.
// The interpreter names repeats the same way (interp.iterationID).
//
// Its names are the compiler's, so a golden's line about a program's own
// `n` reads none of them.
func (st *loopIdentityState) identified(fs *ir.For, elemSym *ir.LoopVar, elemT *ir.Type, def *ir.StructDef, recT *ir.Type) *ir.Func {
	n := len(st.added)
	listT := ir.ListOf(elemT)
	outT := ir.ListOf(recT)
	seenT := ir.MapOf(ir.TypString, ir.TypInt)

	xs := &ir.Param{Name: "__xs", Type: listT}
	x := &ir.LoopVar{Name: "__x", Type: elemT}
	seen := &ir.Var{Name: "__seen", Type: seenT, Synthesized: true}
	out := &ir.Var{Name: "__out", Type: outT, Synthesized: true}
	id := &ir.Var{Name: "__id", Type: ir.TypString, Synthesized: true}
	count := &ir.Var{Name: "__n", Type: ir.TypInt, Synthesized: true}
	xRef := func() *ir.Ident { return &ir.Ident{Name: x.Name, Type: elemT, Sym: x, Synthesized: true} }

	identity := renameInExpr(deepCloneExpr(fs.Identity), map[ir.Symbol]string{elemSym: x.Name}, map[ir.Symbol]ir.Symbol{elemSym: x})
	body := []ir.Stmt{
		&ir.LocalVar{Name: id.Name, Type: ir.TypString, Sym: id, Init: &ir.Call{
			Type: ir.TypString,
			Func: ir.IntrinsicMethod("replace", "string.replace", ir.TypString, ir.TypString),
			Args: []ir.CallArg{{Value: identity}, {Value: stringLit("#")}, {Value: stringLit("##")}},
		}},
		&ir.LocalVar{Name: count.Name, Type: ir.TypInt, Sym: count, Init: callMapGet(varRef(seen), varRef(id), intLiteralLit(0), ir.TypInt)},
		&ir.Assign{
			Target: &ir.Index{Type: ir.TypInt, Operand: varRef(seen), Idx: varRef(id)},
			Op:     ast.AssignSet,
			Value:  &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: varRef(count), Right: intLiteralLit(1)},
		},
		&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGt, Left: varRef(count), Right: intLiteralLit(0)},
			Body: []ir.Stmt{&ir.Assign{
				Target: varRef(id),
				Op:     ast.AssignSet,
				Value: &ir.Binary{Type: ir.TypString, Op: ast.BinAdd,
					Left:  &ir.Binary{Type: ir.TypString, Op: ast.BinAdd, Left: varRef(id), Right: stringLit("#")},
					Right: &ir.Conversion{Type: ir.TypString, Operand: varRef(count)}},
			}},
		},
		callListPush(varRef(out), &ir.StructLit{Type: recT, Def: def, Fields: []ir.FieldInit{
			{Name: "id", Value: varRef(id)},
			{Name: "elem", Value: xRef()},
		}}, recT),
	}
	fn := &ir.Func{
		Pkg:    st.pkg,
		Name:   fmt.Sprintf("__identified%d", n),
		Params: []*ir.Param{xs},
		Return: outT,
		Purity: ir.PurityPure,
		// An ordinary function, not a synthesized one: fyne and gtk4 emit a
		// synthesized package func as a render slot's, which returns nothing.
		Block: []ir.Stmt{
			&ir.LocalVar{Name: seen.Name, Type: seenT, Sym: seen, Init: &ir.MapLitIR{Type: seenT}},
			&ir.LocalVar{Name: out.Name, Type: outT, Sym: out, Init: &ir.ListLit{Type: outT}},
			&ir.For{
				Key:      x.Name,
				KeySym:   x,
				Iter:     &ir.Ident{Name: xs.Name, Type: listT, Sym: xs, Synthesized: true},
				ElemType: elemT,
				Body:     body,
			},
			&ir.Return{Value: varRef(out)},
		},
	}
	st.added = append(st.added, fn)
	return fn
}
