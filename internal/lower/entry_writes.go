package lower

import (
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passEntryWrites writes a write through a map's entry -- `m[k].f = v`,
// `m[k].n += 1`, `m[k].on!!`, `m[k].xs.push(x)` -- as a read of the entry
// into a local, the write to the local, and the local stored back:
//
//	var __entryKey0 = k
//	var __entry0 = m[__entryKey0]
//	__entry0.f = v
//	m[__entryKey0] = __entry0
//
// for a language that does not claim `entryWrites`. Go's map entry is no
// place, so `m[k].F = v` did not compile, and Kotlin's map is a read-only
// `Map` whose `m[k]` is nullable, so neither did `m[k].f = v`. A key the map
// does not hold reads as the value's zero, and the store makes the entry, as
// C's write through the entry's slot does.
//
// A map index is rewritten from the root of the path out: what is left of
// the write names the local, which a second map further along the path is
// then rewritten in. A mutating method's receiver that is the entry itself,
// `m[k].push(x)`, is the entry's write too.
//
// After ValueCopy, so the local and the store are the entry and copy
// nothing, and after LibFuncs, so a library body the program carries is
// rewritten with the program's.
var passEntryWrites = pass{
	name:    "EntryWrites",
	enabled: func(f Features) bool { return !f.EntryWrites },
	apply:   lowerEntryWrites,
}

func lowerEntryWrites(pkg *ir.Package, _ Features, _ Options) error {
	n := 0
	name := func(prefix string) string {
		s := prefix + strconv.Itoa(n)
		n++
		return s
	}
	ir.RewriteBlocks(pkg, func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			out = append(out, entryWrite(s, name)...)
		}
		return out
	})
	return nil
}

// entryWrite is s, or the statements writing it through a local where it
// writes through a map's entry.
func entryWrite(s ir.Stmt, name func(string) string) []ir.Stmt {
	slot, self := writtenPath(s)
	if slot == nil {
		return []ir.Stmt{s}
	}
	at := rootMostEntry(slot, self)
	if at == nil {
		return []ir.Stmt{s}
	}
	idx := (*at).(*ir.Index)
	mt := idx.Operand.ExprType()
	keyT, valT := mt.Elems[0], mt.Elems[1]
	key := &ir.Var{Name: name("__entryKey"), Type: keyT, Synthesized: true}
	entry := &ir.Var{Name: name("__entry"), Type: valT, Synthesized: true}
	bindKey := &ir.LocalVar{Name: key.Name, Type: keyT, Init: idx.Idx, Sym: key}
	read := &ir.LocalVar{Name: entry.Name, Type: valT, Init: &ir.Index{Type: valT, Operand: ir.CloneExprSharingDecls(idx.Operand), Idx: identOf(key)}, Sym: entry}
	store := &ir.Assign{Target: &ir.Index{Type: valT, Operand: idx.Operand, Idx: identOf(key)}, Value: identOf(entry)}
	*at = identOf(entry)
	out := []ir.Stmt{bindKey, read}
	out = append(out, entryWrite(s, name)...)
	return append(out, store)
}

// writtenPath is the place s writes through, and whether the place itself
// is written whole -- an assignment's or a toggle's target, which a map's
// entry may be -- rather than written into, as a mutating method's receiver
// is. Nil where s writes no place.
func writtenPath(s ir.Stmt) (*ir.Expr, bool) {
	switch x := s.(type) {
	case *ir.Assign:
		return &x.Target, true
	case *ir.Toggle:
		return &x.Target, true
	case *ir.CallStmt:
		c := x.Call
		if c == nil || c.Func == nil || len(c.Args) == 0 {
			return nil, false
		}
		if def, ok := ir.IntrinsicByName(c.Func.Intrinsic); ok && def.MutatesReceiver {
			recv := &c.Args[0].Value
			if u, ok := (*recv).(*ir.Unary); ok && u.Op == ast.UnaryAddr {
				recv = &u.Operand
			}
			return recv, false
		}
	}
	return nil, false
}

// rootMostEntry is the map index nearest the root of the path at slot that
// is written through, or nil: the place itself is one only where it is
// written into (whole is false).
func rootMostEntry(slot *ir.Expr, whole bool) *ir.Expr {
	var found *ir.Expr
	at, top := slot, true
	for {
		switch x := (*at).(type) {
		case *ir.Index:
			if t := x.Operand.ExprType(); t != nil && t.Kind == ir.TypeMap && len(t.Elems) == 2 && (!top || !whole) {
				found = at
			}
			at = &x.Operand
		case *ir.Select:
			at = &x.Operand
		default:
			return found
		}
		top = false
	}
}
