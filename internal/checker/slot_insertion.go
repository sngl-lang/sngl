package checker

import (
	"fmt"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// checkSlotInsertions holds a body to inserting each slot as often as its
// declared count says, counted over every path through the body: a slot of
// exactly one -- a bare named slot, or the rest slot -- renders once whichever
// way the body goes, `option[…]` at most once, `list[…]` any number of times.
// The count is what a ref read through the slot is wrapped in, so it has to be
// true of the body, not of a caller's reading of it.
//
// The rest slot is the strict case for a second reason: the children a caller
// writes bare are the caller's -- their `#id`s are the caller's refs -- so where
// they render has to be a fact of the caller's body alone.
//
// A signature has no insertion to hold, so the rule is asked of a body only.
// A library declaration's own `{}` is the same: its render is a platform's
// primitive or override, which is asked instead, and so is a declaration the
// compiler or an emitter renders from its marks. A `const` slot holds
// build-time data, which nothing renders.
func (c *checker) checkSlotInsertions(comp *ir.Component) {
	if comp.AST == nil || !comp.AST.Body.IsDefined() {
		return
	}
	if comp.Builtin != ir.BuiltinNone || comp.Intrinsic != "" || comp.Wildcard != "" {
		return
	}
	if comp.PkgPath() != "" && len(comp.AST.Body.Stmts) == 0 && !c.overrideAt.IsSet() {
		return
	}
	for _, slot := range comp.Slots {
		// A data family's records are read as a list and never inserted, and
		// their content is shown per record (SlotDecl.Shows), at most once each.
		switch {
		case slot.Shows != nil:
			c.checkRecordContentInserts(comp, slot)
		case !slot.Const && slot.Values == nil:
			c.checkSlotInsertCount(comp, slot)
		}
	}
}

func (c *checker) checkSlotInsertCount(comp *ir.Component, slot *ir.SlotDecl) {
	w := &insertionWalk{slot: slot.Name, rest: slot.Rest}
	got := w.stmts(comp.Stmts, "")
	what := fmt.Sprintf("slot %q", slot.Name)
	why := "it is declared to render exactly once; declare it option[…] if the body may leave it out, or list[…] if it may repeat"
	if slot.Rest {
		what = fmt.Sprintf("rest slot %q", slot.Name)
		why = fmt.Sprintf("the children a caller writes bare render where the body inserts %s, exactly once; a slot the body may leave out is a named option[component …]", slot.Name)
	}
	switch slot.Inserts {
	case ir.InsertMany:
		return
	case ir.InsertOptional:
		if got.max > 1 {
			c.reportRepeatedInsertion(w.sites, what, "it is declared option[…] and renders at most once; declare it list[…] if it may repeat")
		}
		return
	}
	if got.max == 0 {
		c.error(c.slotPos(comp, slot), "%s of %s is never inserted: %s", what, comp.Name, why)
		return
	}
	if got.min == 0 {
		for _, s := range w.sites {
			// A repeating site is reported for repeating, below.
			if s.under != "" && s.repeats == "" {
				if slot.Rest {
					c.error(s.pos, "%s is inserted under %s: the children a caller writes bare render exactly once, so insert %s where it always renders, or make it a named option[component …] slot", what, s.under, slot.Name)
				} else {
					c.error(s.pos, "%s is inserted under %s: %s", what, s.under, why)
				}
			}
		}
	}
	if got.max > 1 {
		reason := "the children a caller writes bare render exactly once"
		if !slot.Rest {
			reason = why
		}
		c.reportRepeatedInsertion(w.sites, what, reason)
	}
}

// reportRepeatedInsertion names the insertions that make a slot render more
// often than it is declared to: one under a loop, or every site after the
// first.
func (c *checker) reportRepeatedInsertion(sites []insertionSite, what, reason string) {
	for _, s := range sites {
		if s.repeats != "" {
			c.error(s.pos, "%s is inserted under %s: %s", what, s.repeats, reason)
			return
		}
	}
	for _, s := range sites[1:] {
		c.error(s.pos, "%s is inserted more than once: %s (first inserted at %s)", what, reason, sites[0].pos)
	}
}

func (c *checker) slotPos(comp *ir.Component, slot *ir.SlotDecl) ast.Pos {
	if c.overrideAt.IsSet() {
		return c.overrideAt
	}
	// The library body is what fails to insert; its slot is a published contract.
	if comp.PkgPath() != "" {
		return comp.AST.Body.Pos
	}
	for _, p := range comp.AST.Props.Props {
		if pd, ok := p.(ast.Param); ok && pd.Name == slot.Name {
			return pd.Pos
		}
	}
	return compDeclPos(comp)
}

// renders is how many times a block renders an insertion, at least and at
// most, over every path through it. Counts above one are held at many: only
// "more than one" is ever asked.
type renders struct{ min, max int }

const many = 2

func (a renders) then(b renders) renders {
	return renders{min(a.min+b.min, many), min(a.max+b.max, many)}
}

func (a renders) or(b renders) renders {
	return renders{min(a.min, b.min), max(a.max, b.max)}
}

func (a renders) maybe() renders { return renders{0, a.max} }

func (a renders) repeated() renders {
	if a.max == 0 {
		return a
	}
	return renders{0, many}
}

type insertionSite struct {
	pos ast.Pos
	// under names the construct that may leave the insertion out, and repeats
	// the one that may render it more than once; empty where neither does.
	under, repeats string
}

type insertionWalk struct {
	slot  string
	rest  bool
	sites []insertionSite
	// perIteration counts a loop's body once rather than as repeating, and
	// reports a body that renders the slot more than once.
	perIteration bool
}

func (w *insertionWalk) stmts(stmts []ir.Stmt, under string) renders {
	var r renders
	for _, st := range stmts {
		r = r.then(w.stmt(st, under))
	}
	return r
}

// repeating walks a block that may render more than once, marking the sites in
// it with what repeats them.
func (w *insertionWalk) repeating(stmts []ir.Stmt, under, why string) renders {
	from := len(w.sites)
	r := w.stmts(stmts, orUnder(under, why))
	for i := from; i < len(w.sites); i++ {
		if w.sites[i].repeats == "" {
			w.sites[i].repeats = why
		}
	}
	return r.repeated()
}

func orUnder(under, s string) string {
	if under != "" {
		return under
	}
	return s
}

func (w *insertionWalk) stmt(st ir.Stmt, under string) renders {
	switch n := st.(type) {
	case *ir.SlotInst:
		var r renders
		if n.Entry == nil && n.Name == w.slot {
			var pos ast.Pos
			if n.AST != nil {
				pos = n.AST.Pos
			}
			w.sites = append(w.sites, insertionSite{pos: pos, under: under})
			r = renders{1, 1}
		}
		r = r.then(w.stmts(n.Children, orUnder(under, "a slot fallback")).maybe())
		for _, name := range ir.SlotNames(n.Slots) {
			var entry *ir.SlotDecl
			if n.Decl != nil {
				entry = slotNamed(n.Decl.Slots, name)
			}
			r = r.then(w.population(entry, n.Slots[name].Body, under))
		}
		return r
	case *ir.NodeInst:
		// An emitted family's member writes its children between gen.node's open
		// and close: that node is where the rest slot is inserted.
		if w.rest && n.Component != nil && n.Component.Builtin == ir.BuiltinGenNode {
			w.sites = append(w.sites, insertionSite{pos: ir.NodePos(n), under: under})
			return renders{1, 1}
		}
		r := w.stmts(n.Children, under)
		for _, name := range ir.SlotNames(n.Slots) {
			var decl *ir.SlotDecl
			if n.Component != nil {
				decl = slotNamed(n.Component.Slots, name)
			}
			r = r.then(w.population(decl, n.Slots[name].Body, under))
		}
		return r
	case *ir.If:
		return w.stmts(n.Body, orUnder(under, "an if")).or(w.stmts(n.Else, orUnder(under, "an if")))
	case *ir.Match:
		var r renders
		for i, arm := range n.Arms {
			a := w.stmts(arm.Body, orUnder(under, "a match"))
			if i == 0 {
				r = a
			} else {
				r = r.or(a)
			}
		}
		return r
	case *ir.For:
		if w.perIteration {
			body := w.stmts(n.Body, orUnder(under, "a for"))
			if body.max > 1 {
				return body
			}
			return body.maybe()
		}
		return w.repeating(n.Body, under, "a for").then(w.stmts(n.Else, orUnder(under, "a for-else")).maybe())
	case *ir.ErrorBoundary:
		if len(n.Failed) == 0 {
			return w.stmts(n.Children, under)
		}
		inner := orUnder(under, "a boundary with a fallback")
		return w.stmts(n.Children, inner).or(w.stmts(n.Failed, inner))
	case *ir.ContextProvider:
		return w.stmts(n.Children, under)
	}
	return renders{}
}

// population counts an insertion written in a population of another node's
// slot, or of an insertion's component entry, which renders as often as that
// slot or entry is inserted.
func (w *insertionWalk) population(decl *ir.SlotDecl, body []ir.Stmt, under string) renders {
	switch {
	case decl == nil:
		return w.stmts(body, orUnder(under, "a slot population")).maybe()
	case decl.Inserts == ir.InsertOptional:
		return w.stmts(body, orUnder(under, "an option slot's population")).maybe()
	case decl.Inserts == ir.InsertMany:
		return w.repeating(body, under, "a list slot's population")
	}
	return w.stmts(body, under)
}

func slotNamed(slots []*ir.SlotDecl, name string) *ir.SlotDecl {
	for _, s := range slots {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// checkRecordContentInserts holds a container to showing each record's
// content at most once, which is what a ref into it is typed by
// (ir.Component.InsertsOf): at most once outside a loop, and at most once in
// each iteration of the loop it is shown in.
func (c *checker) checkRecordContentInserts(comp *ir.Component, slot *ir.SlotDecl) {
	w := &insertionWalk{slot: slot.Name, perIteration: true}
	got := w.stmts(comp.Stmts, "")
	if got.max > 1 {
		c.reportRepeatedInsertion(w.sites, "a record's content", "it renders at most once for the record")
	}
}
