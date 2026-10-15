package lower

import "duckfam.us/sngl/ir"

// passValueCopy makes a store of a struct, list or map a copy (see
// ir/valuecopy.go): the value an assignment, a local's initializer, a var's
// initializer, a push or an insert stores, and an element or field a literal
// is built from, is wrapped in ir.ValueCopy unless nothing else can hold it.
// A named parameter is passed by value too, and a function whose body
// modifies one takes its own copy at entry (ownModifiedParams); one that only
// reads it copies nothing. A method's ambient `this` and a `ref[T]` are
// passed by reference instead (passRefParams).
//
// Before every other pass that writes a store, so what is copied is what the
// program wrote. An element bound with `&` is copied where it is stored
// elsewhere, and passRefLoop then reads it as `xs[i]` inside the copy, while a
// write through it stays a write of the element.
//
// A view body is not rewritten: what it builds is shown and never written.
var passValueCopy = pass{
	name:    "ValueCopy",
	enabled: func(Features) bool { return true },
	apply:   lowerValueCopy,
}

func lowerValueCopy(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		for _, v := range o.Vars {
			if v.Init != nil {
				v.Init = copiedStore(v.Init)
				copyLiteralParts(v.Init)
			}
		}
	}
	for _, block := range imperativeBlocks(pkg) {
		for _, s := range *block {
			if err := ir.Rewrite(s, copyStores); err != nil {
				return err
			}
		}
	}
	for _, f := range paramOwners(pkg) {
		ownModifiedParams(f)
	}
	return nil
}

// paramOwners is every function whose parameters a call binds: the package's
// and each component's, a lambda's, and each handler's.
func paramOwners(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(f *ir.Func) {
		if f != nil && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, o := range ir.Owners(pkg) {
		for _, f := range o.Funcs {
			add(f)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Lambda:
			add(x.Func)
		case *ir.NodeInst:
			for i := range x.Handlers {
				add(x.Handlers[i].Func)
			}
		}
		return nil
	})
	return out
}

// paramWrite is what a body does to a parameter: nothing, replaces it whole,
// or writes inside it.
type paramWrite int

const (
	paramUnwritten paramWrite = iota
	paramReplaced
	paramModified
)

// ownModifiedParams gives f its own copy of each struct, list or map
// parameter its body modifies, since a parameter is passed by value: a local
// bound at entry, which every read and write in the body names instead. A
// parameter the body only reads is the caller's value, uncopied, and one it
// only replaces whole needs the local and no copy -- Kotlin's parameters
// cannot be assigned.
func ownModifiedParams(f *ir.Func) {
	var prelude []ir.Stmt
	for _, p := range f.Params {
		// A by-reference parameter is passRefParams'.
		if p == nil || p.Variadic || !ir.HoldsValue(p.Type) || byRef(p) {
			continue
		}
		w := writesTo(f.Block, p)
		if w == paramUnwritten {
			continue
		}
		own := &ir.Var{Type: p.Type}
		own.Name = p.Name + "__own"
		var init ir.Expr = &ir.Ident{Name: p.Name, Sym: p, Type: p.Type}
		if w == paramModified {
			init = ir.ValueCopy(init)
		}
		_ = ir.RewriteExprs(f.Block, func(e ir.Expr) (ir.Expr, error) {
			if id, ok := e.(*ir.Ident); ok && id.Sym == p {
				return &ir.Ident{Name: own.Name, Sym: own, Type: id.Type}, nil
			}
			return e, nil
		})
		prelude = append(prelude, &ir.LocalVar{Name: own.Name, Type: p.Type, Init: init, Sym: own})
	}
	if len(prelude) > 0 {
		f.Block = append(prelude, f.Block...)
	}
}

// writesTo is the most a body does to p: an assignment or toggle rooted at
// it, a mutating builtin called on it or on a part of it, or a loop binding
// its elements with `&`, whose writes land in it.
func writesTo(body []ir.Stmt, p *ir.Param) paramWrite {
	most := paramUnwritten
	note := func(w paramWrite) {
		if w > most {
			most = w
		}
	}
	_ = ir.Walk(body, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Assign:
			if id, ok := x.Target.(*ir.Ident); ok && id.Sym == p {
				note(paramReplaced)
			} else if rootedAt(x.Target, p) {
				note(paramModified)
			}
		case *ir.Toggle:
			if rootedAt(x.Target, p) {
				note(paramModified)
			}
		case *ir.Call:
			if x.Func != nil && x.Func.Intrinsic != "" && len(x.Args) > 0 && rootedAt(x.Args[0].Value, p) {
				if def, ok := ir.IntrinsicByName(x.Func.Intrinsic); ok && def.MutatesReceiver {
					note(paramModified)
				}
			}
		case *ir.For:
			if x.RefElem && rootedAt(x.Iter, p) {
				note(paramModified)
			}
		}
		return nil
	})
	return most
}

// rootedAt reports whether e is p or a field or element path down from it.
func rootedAt(e ir.Expr, p *ir.Param) bool {
	for {
		switch x := e.(type) {
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		case *ir.Ident:
			return x.Sym == p
		default:
			return false
		}
	}
}

// copyStores is the rewrite over an imperative statement: each store it
// makes, and each literal in it, copies what it stores.
func copyStores(n ir.Node) (ir.Node, error) {
	switch x := n.(type) {
	case *ir.Assign:
		x.Value = copiedStore(x.Value)
	case *ir.LocalVar:
		if x.Init != nil {
			x.Init = copiedStore(x.Init)
		}
	case *ir.Call:
		if x.Func != nil {
			switch x.Func.Intrinsic {
			case "list.push":
				if len(x.Args) == 2 {
					x.Args[1].Value = copiedStore(x.Args[1].Value)
				}
			case "list.insert":
				if len(x.Args) == 3 {
					x.Args[2].Value = copiedStore(x.Args[2].Value)
				}
			}
		}
	case *ir.ListLit, *ir.StructLit, *ir.MapLitIR:
		copyLiteralParts(x.(ir.Expr))
	}
	return n, nil
}

// copyLiteralParts copies what a literal is built from: each element, field
// and entry value, and what a spread in it contributes. A literal nested in
// one is reached by the rewrite that found this one.
func copyLiteralParts(e ir.Expr) {
	switch x := e.(type) {
	case *ir.ListLit:
		for i, el := range x.Elems {
			if sp, ok := el.(*ir.Spread); ok {
				// The spread contributes the list's elements, which need a
				// copy only where they are values themselves.
				if t := sp.Operand.ExprType(); t != nil && t.Kind == ir.TypeList && len(t.Elems) > 0 && ir.HoldsValue(t.Elems[0]) {
					sp.Operand = copiedStore(sp.Operand)
				}
				continue
			}
			x.Elems[i] = copiedStore(el)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = copiedStore(x.Fields[i].Value)
		}
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Value = copiedStore(x.Entries[i].Value)
		}
	}
}

// copiedStore is e as a store takes it: copied, unless e is a value of a type
// that copies itself or one nothing else holds.
func copiedStore(e ir.Expr) ir.Expr {
	if e == nil || !ir.HoldsValue(e.ExprType()) || freshValue(e) {
		return e
	}
	return ir.ValueCopy(e)
}

// freshValue reports whether e makes a value nothing else holds: a literal,
// a function, a copy, and a call of a function with no body of its own, which
// the host or the compiler answers by building one. A function a program
// wrote may return what it was handed, and a builtin that hands back what a
// list or map holds -- an element, or a new list of the same elements -- is
// fresh only where those elements are scalars.
func freshValue(e ir.Expr) bool {
	if t := e.ExprType(); t != nil && t.Kind == ir.TypeNull {
		return true
	}
	switch x := e.(type) {
	case *ir.Literal, *ir.ListLit, *ir.StructLit, *ir.MapLitIR, *ir.Lambda:
		return true
	case *ir.Conversion:
		// A value made an option is the value it was; a list or map converted
		// to another element type is a new one, holding new elements.
		if freshValue(x.Operand) {
			return true
		}
		ot, rt := x.Operand.ExprType(), x.ExprType()
		return ot != nil && rt != nil && !ot.Equal(rt) && (rt.Kind == ir.TypeList || rt.Kind == ir.TypeMap) && !ir.HoldsValue(rt.Elems[len(rt.Elems)-1])
	case *ir.Ident:
		// A const is never written, and the optimizer has already folded one
		// read into storage the program may write to a copy of its value
		// (foldOwned).
		v, ok := x.Sym.(*ir.Var)
		return ok && v.IsConst
	case *ir.Call:
		if ir.IsValueCopy(x) {
			return true
		}
		if x.Func == nil {
			return false
		}
		switch x.Func.Intrinsic {
		case "map.get":
			return false
		case "list.filter", "list.map", "list.reverse", "list.slice", "map.keys", "map.values":
			t := x.ExprType()
			return t != nil && len(t.Elems) > 0 && !ir.HoldsValue(t.Elems[0])
		}
		return !writtenBody(x.Func)
	}
	return false
}

// writtenBody reports whether f has a body someone wrote, rather than the
// signature of a host function, an intrinsic or a native. A function a pass
// built has no AST and is treated as written.
func writtenBody(f *ir.Func) bool {
	if f.Intrinsic != "" {
		return false
	}
	if f.AST == nil {
		return len(f.Block) > 0
	}
	return f.AST.Body != nil || f.AST.Block.IsDefined()
}
