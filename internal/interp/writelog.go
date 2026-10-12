package interp

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// An effect's `@update` and a list's change events report writes, not values
// that compare different: a list has no equality every target agrees on, and a
// write of the value a cell already holds is still a write. So the interpreter
// keeps what the compiled targets know statically -- where each write landed and
// what it did -- as a log, which a settle reads per effect (Effects.deliver).

// writeKind is what a write did to the cell it landed in.
type writeKind int

const (
	// writeReplace put a new value in the cell, or somewhere below it.
	writeReplace writeKind = iota
	// The list in the cell changed by one of its own mutations.
	writeInsert
	writeRemove
	writeClear
)

// mapKey stands in a path for an entry of a map. A map's changes are not
// reported, so a write below one is a write to the map and nothing finer.
type mapKey struct{}

// cell is where a write lands: a binding, in the scope that owns it, and the
// fields and indices below it. owner is the scope the binding was declared in
// rather than a snapshot of it, so a write made from a loop iteration or a
// handler is the same cell as the one an effect was keyed on.
type cell struct {
	owner *Env
	sym   ir.Symbol
	path  []any // string for a field, int for a list index, mapKey for a map entry
	// vals is the value at each prefix of path after the write, vals[i] being
	// the value path[:i] names. An `@set` hands its handler the element as the
	// write left it, which a later write in the same handler may move.
	vals []any
}

// sameBinding reports whether a and b name one binding.
func (a cell) sameBinding(b cell) bool { return a.owner == b.owner && a.sym == b.sym }

// cellWrite is one logged write.
type cellWrite struct {
	at    cell
	kind  writeKind
	index int
	item  any
}

// writeLog is every write since the oldest watching effect last read it.
// Sequence numbers are absolute, so trimming the front moves base.
type writeLog struct {
	entries []cellWrite
	base    int
	// watching is how many running effects read the log. With none, nothing is
	// recorded: a program with no `@update` pays for no log.
	watching int
}

func (l *writeLog) end() int { return l.base + len(l.entries) }

func (l *writeLog) since(seq int) []cellWrite {
	if seq < l.base {
		seq = l.base
	}
	return l.entries[seq-l.base:]
}

// trim drops what every watcher has read.
func (l *writeLog) trim(seq int) {
	if seq <= l.base {
		return
	}
	if seq >= l.end() {
		l.base, l.entries = l.end(), nil
		return
	}
	l.entries = slices.Clone(l.entries[seq-l.base:])
	l.base = seq
}

// writes is the program's log, shared by every scope the way the count of
// running handlers is: a child scope and a snapshot take their parent's.
func (env *Env) writes() *writeLog {
	if env.wlog == nil {
		env.wlog = &writeLog{}
	}
	return env.wlog
}

// canonical is the scope a snapshot was taken from, followed to the scope that
// is no snapshot.
func (env *Env) canonical() *Env {
	for env != nil && env.origin != nil {
		env = env.origin
	}
	return env
}

// noteWrite records a write to target, if anything is watching.
func (env *Env) noteWrite(target ir.Expr, kind writeKind, index int, item any) {
	l := env.writes()
	if l.watching == 0 {
		return
	}
	at, ok := env.cellOf(target)
	if !ok {
		return
	}
	l.entries = append(l.entries, cellWrite{at: at, kind: kind, index: index, item: CopyValue(item)})
}

// NoteReplace records that a harness replaced sym's value in this scope -- a
// test's `c.n = 1`, which reaches the binding without an assignment statement.
func (env *Env) NoteReplace(sym ir.Symbol) {
	l := env.writes()
	if l.watching == 0 {
		return
	}
	owner := env.findVarOwner(sym)
	if owner == nil {
		owner = env
	}
	v := owner.vals[sym]
	l.entries = append(l.entries, cellWrite{at: cell{owner: owner.canonical(), sym: sym, vals: []any{v}}})
}

// CellOf is where a write to the binding sym in this scope lands, for a
// ComponentValue answering for one of its fields.
func (env *Env) CellOf(sym ir.Symbol) (owner *Env, ok bool) {
	o := env.findVarOwner(sym)
	if o == nil {
		return nil, false
	}
	return o.canonical(), true
}

// cellOf resolves an expression naming storage to the cell it names, or false
// where it names none -- a call's result, a literal, a value computed from one.
func (env *Env) cellOf(e ir.Expr) (cell, bool) {
	switch x := e.(type) {
	case *ir.Ident:
		owner := env.findVarOwner(x.Sym)
		if owner == nil {
			return cell{}, false
		}
		v := owner.vals[x.Sym]
		if ref, isRef := v.(*listRef); isRef {
			return ref.cell()
		}
		return cell{owner: owner.canonical(), sym: x.Sym, vals: []any{v}}, true
	case *ir.Select:
		obj, err := env.Eval(x.Operand)
		if err != nil {
			return cell{}, false
		}
		if cv, isComp := obj.(ComponentValue); isComp {
			owner, sym, ok := cv.FieldCell(x.Field)
			if !ok {
				return cell{}, false
			}
			v, _ := owner.Value(sym)
			return cell{owner: owner, sym: sym, vals: []any{v}}, true
		}
		at, ok := env.cellOf(x.Operand)
		if !ok {
			return cell{}, false
		}
		var v any
		if st, isStruct := obj.(*Struct); isStruct {
			v, _ = st.Get(x.Field)
		}
		return at.extend(x.Field, v), true
	case *ir.Index:
		at, ok := env.cellOf(x.Operand)
		if !ok {
			return cell{}, false
		}
		obj, err := env.Eval(x.Operand)
		if err != nil {
			return cell{}, false
		}
		idx, err := env.Eval(x.Idx)
		if err != nil {
			return cell{}, false
		}
		switch o := obj.(type) {
		case []any:
			i := ToInt(idx)
			var v any
			if i >= 0 && i < len(o) {
				v = o[i]
			}
			return at.extend(i, v), true
		case map[string]any:
			return at.extend(mapKey{}, o[fmt.Sprintf("%v", idx)]), true
		}
		return cell{}, false
	case *ir.Unary:
		// A read through a &-bound element derefs it, so the binding is
		// asked for itself: the Ident case finds the listRef it holds.
		if x.Op == ast.UnaryDeref {
			return env.cellOf(x.Operand)
		}
	}
	return cell{}, false
}

func (c cell) extend(step, v any) cell {
	// An element is what an `@set` hands its handler, as this write left it:
	// a struct is shared, so a later write in the same handler would move a
	// value held by reference.
	if _, isIndex := step.(int); isIndex {
		v = CopyValue(v)
	}
	c.path = append(slices.Clone(c.path), step)
	c.vals = append(slices.Clone(c.vals), v)
	return c
}

// cell is where the element a &-bound loop variable names lives: the iterable's
// cell and the element's index in it.
func (r *listRef) cell() (cell, bool) {
	if r.at == nil || r.iter == nil {
		return cell{}, false
	}
	at, ok := r.at.cellOf(r.iter)
	if !ok {
		return cell{}, false
	}
	return at.extend(r.idx, r.get()), true
}

// relate says how a write at w stands to a cell an effect watches at c: below
// it (w's path extends c's), at it, or above it (c's extends w's). Unrelated
// writes report false.
func relate(w, c []any) (below, at, above, ok bool) {
	n := min(len(w), len(c))
	for i := range n {
		if w[i] != c[i] {
			return false, false, false, false
		}
	}
	switch {
	case len(w) > len(c):
		return true, false, false, true
	case len(w) == len(c):
		return false, true, false, true
	}
	return false, false, true, true
}
