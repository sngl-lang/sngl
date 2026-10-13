package interp

import (
	"reflect"
	"slices"
	"sort"

	"duckfam.us/sngl/ir"
)

// watchState is how far one running bracket has read the write log, and the
// reports it has read and not yet run.
type watchState struct {
	cursor  int
	pending []changeReport
	// on is the key as the last report saw it. A key reading a prop is
	// written by whoever bound the prop, which is no write to a binding of
	// this scope, so a change in its value counts as a write too.
	on any
}

// changeReport is one handler run owed: an event and the values it carries.
type changeReport struct {
	event  string
	values []any
}

// startWatching begins reading the log for a lifetime that reports writes. It
// reads from now: what was written before the lifetime began is what it began
// with.
func (fx *Effects) startWatching(e MountedEffect, root *Env) {
	if len(e.Changes) == 0 {
		return
	}
	log := root.writes()
	fx.watch[e.Key] = &watchState{cursor: log.end(), on: e.On}
	log.watching = len(fx.watch)
}

// deliver runs the next report owed to a running bracket, oldest lifetime
// first, and reports whether it ran one. Like a lifetime's handlers, one per
// call: the handler's writes are a different program state, which the next
// report's scope has to be mounted from.
func (fx *Effects) deliver(next map[Key]MountedEffect, root *Env) (bool, error) {
	log := root.writes()
	defer fx.trimLog(log)
	keys := make([]Key, 0, len(fx.watch))
	for k := range fx.watch {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fx.live[keys[i]].seq < fx.live[keys[j]].seq })
	for _, k := range keys {
		w := fx.watch[k]
		// A bracket the write took out of the tree reports nothing: what it
		// would report is a list nothing holds any more, and a compiled settle
		// asks the position before it reports. The freshest mount's scope and
		// key otherwise, since the live entry's are as old as the mount that
		// began the lifetime.
		e, still := next[k]
		if !still {
			w.pending, w.cursor = nil, log.end()
			continue
		}
		if len(w.pending) == 0 {
			w.pending = reportsFor(e, w, log.since(w.cursor))
			w.cursor = log.end()
			w.on = e.On
		}
		if len(w.pending) == 0 {
			continue
		}
		r := w.pending[0]
		w.pending = w.pending[1:]
		return true, fx.runWith(e, e.Changes[r.event], root, r.values)
	}
	return false, nil
}

// trimLog drops what every watcher has read, and records how many there are:
// with none, the next write is not logged at all.
func (fx *Effects) trimLog(log *writeLog) {
	log.watching = len(fx.watch)
	low := log.end()
	for _, w := range fx.watch {
		low = min(low, w.cursor)
	}
	log.trim(low)
}

// reportsFor is what one bracket owes for the writes since it last read: a
// change event for each write its handlers describe, in the order they were
// made, and then one `@update` if any write reached the key that none of them
// described.
func reportsFor(e MountedEffect, w *watchState, writes []cellWrite) []changeReport {
	var out []changeReport
	unanswered := false
	for _, wr := range writes {
		if e.HasCell && wr.at.sameBinding(e.Cell) {
			if shiftsNoCopy(wr, e.Cell.path) {
				continue
			}
			r, answered := changeOf(e, wr)
			if answered {
				out = append(out, r)
			} else if r.event != "" || related(wr.at, e.Cell) {
				unanswered = true
			}
			continue
		}
		if slices.ContainsFunc(e.Reads, func(c cell) bool { return wr.at.sameBinding(c) }) {
			unanswered = true
		}
	}
	if !unanswered && e.Untracked && !reflect.DeepEqual(w.on, e.On) {
		unanswered = true
	}
	if unanswered && e.Changes[ir.EffectUpdate] != nil {
		out = append(out, changeReport{event: ir.EffectUpdate, values: []any{e.On}})
	}
	return out
}

// changeOf is the change event a write to e's own cell describes, and whether
// e handles it. An event e does not handle is returned with its name set, so
// the caller counts the write as one `@update` must report.
func changeOf(e MountedEffect, wr cellWrite) (changeReport, bool) {
	below, at, _, ok := relate(wr.at.path, e.Cell.path)
	if !ok {
		return changeReport{}, false
	}
	var r changeReport
	switch {
	case at && wr.kind == writeInsert:
		r = changeReport{event: ir.EffectInsert, values: []any{wr.index, wr.item}}
	case at && wr.kind == writeRemove:
		r = changeReport{event: ir.EffectRemove, values: []any{wr.index, wr.item}}
	case at && wr.kind == writeClear:
		r = changeReport{event: ir.EffectClear}
	case below:
		// A write inside one element: the step below the list is its index.
		i, isIndex := wr.at.path[len(e.Cell.path)].(int)
		if !isIndex {
			return changeReport{event: ir.EffectUpdate}, false
		}
		var item any
		if n := len(e.Cell.path) + 1; n < len(wr.at.vals) {
			item = wr.at.vals[n]
		}
		r = changeReport{event: ir.EffectSet, values: []any{i, item}}
	default:
		// The list itself, or what holds it, replaced whole.
		return changeReport{event: ir.EffectUpdate}, false
	}
	return r, e.Changes[r.event] != nil
}

// shiftsNoCopy is whether wr is a list's own mutation of a list holding the
// watched cell at an element below it -- `xs.push(r)` for a key `&x` bound
// over xs -- that leaves that element where it was. An insert or a removal at
// j moves every element from j on, which then holds another value, so those
// copies are written; one before j is untouched, and so is every copy by a
// push, which only adds one.
func shiftsNoCopy(wr cellWrite, watched []any) bool {
	if wr.kind != writeInsert && wr.kind != writeRemove {
		return false
	}
	n := len(wr.at.path)
	if n >= len(watched) {
		return false
	}
	if _, _, above, ok := relate(wr.at.path, watched); !ok || !above {
		return false
	}
	i, isIndex := watched[n].(int)
	return isIndex && i < wr.index
}

func related(a, b cell) bool {
	_, _, _, ok := relate(a.path, b.path)
	return ok
}

// readCells is every var e reads, by the scope that owns it, and whether it
// reads any other binding, which no write is logged against.
func (env *Env) readCells(e ir.Expr) (out []cell, untracked bool) {
	_ = ir.Walk(e, func(n ir.Node) error {
		id, isIdent := n.(*ir.Ident)
		if !isIdent || id.Sym == nil {
			return nil
		}
		owner := env.findVarOwner(id.Sym)
		if owner == nil {
			return nil
		}
		// A &-bound element is the list it was bound over, at its index.
		if ref, isRef := owner.vals[id.Sym].(*listRef); isRef {
			if at, ok := ref.cell(); ok {
				out = append(out, at)
			}
			return nil
		}
		if _, isVar := id.Sym.(*ir.Var); !isVar {
			untracked = true
			return nil
		}
		out = append(out, cell{owner: owner.canonical(), sym: id.Sym})
		return nil
	})
	return out, untracked
}
