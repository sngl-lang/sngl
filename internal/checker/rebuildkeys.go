package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// maxRebuildDepth bounds the descent into generic bodies. A generic component
// may place itself under a wider type -- `r(x=[x])` inside `r<T>(x T)` -- and
// each round is a type the walk has not seen, so the frame set alone does not
// terminate it. The bound is far past any real nesting; a program that reaches
// it has a rebuild key nobody can write down either.
const maxRebuildDepth = 16

// checkRebuildKeys refuses every value the compiler compares to decide whether
// a live instance is the one this render describes: an effect's `on`, and a
// #[construct] prop. Both are the same rule -- the value is kept and compared
// against the next render's, so every target has to agree on when two of them
// are the same value, and not every type gets that agreement (ir.RebuildComparable).
//
// One walk over the package's instances, rather than a guard on each syntactic
// path that builds one. The two rules were first written as guards, and both
// were stated on the spelling someone had in mind: an effect's key was checked
// where checkVisualNodeIR builds a node with a body, so `effect #e(on=xs, ...)`
// -- which is bodyless and takes the element-ref branch -- walked past it and
// emitted `m.__effect0_live[i] != k` over a []int, which is not Go. A walk over
// ir.NodeInst has no spellings in it: a node is a node however it was written.
//
// It also runs after every body is checked, which is what lets it answer for a
// type parameter. ir.RebuildIncomparable passes one through, because a
// declaration is where a parameter is written and judging it there would make a
// generic component undeclarable -- so the answer is owed at the call site that
// binds it, and the call site that binds it may be an outer generic's own call
// site, arbitrarily far up. Descending the instances with the bindings in hand
// is the only place all of that is known at once.
func (c *checker) checkRebuildKeys() {
	if c.pkg == nil {
		return
	}
	w := &rebuildWalker{c: c, reported: map[rebuildDiag]bool{}, frames: map[rebuildFrame]bool{}}
	for _, o := range ir.Owners(c.pkg) {
		w.body(o.Stmts(), rebuildScope{})
	}
}

// rebuildWalker carries what the descent has to remember: the diagnostics
// already stated, and the frames already walked.
type rebuildWalker struct {
	c        *checker
	reported map[rebuildDiag]bool
	frames   map[rebuildFrame]bool
}

// rebuildScope is one body being walked and what its type parameters stand for.
//
// env empty is a declaration read on its own terms -- every component body is
// walked that way once, as an owner -- and anything still typed by a parameter
// there is left for the call sites. env non-empty is that same body reached
// through a call that pinned them, where site is the outermost such call: the
// one line a reader can change, since the body itself is correct for every
// other binding.
type rebuildScope struct {
	holder *ir.Component
	env    map[string]*ir.Type
	site   ast.Pos
	depth  int
}

func (s rebuildScope) bound() bool { return len(s.env) > 0 }

// rebuildDiag is one stated diagnostic. A body is walked once as a declaration
// and again per binding that reaches it, so a key that is already concrete in
// the declaration would be reported once per instantiation.
type rebuildDiag struct {
	pos ast.Pos
	msg string
}

// rebuildFrame is a component body under one set of bindings. Walking it twice
// would say the same thing twice and, for a component that places itself, never
// stop.
type rebuildFrame struct {
	comp *ir.Component
	env  string
}

func (w *rebuildWalker) body(stmts []ir.Stmt, scope rebuildScope) {
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if inst, isInst := n.(*ir.NodeInst); isInst {
			w.node(inst, scope)
		}
		return nil
	})
}

// node judges one instance's rebuild keys and then descends into it.
func (w *rebuildWalker) node(inst *ir.NodeInst, scope rebuildScope) {
	comp := inst.Component
	if comp == nil {
		return
	}
	args := instArgs(inst)
	for _, p := range comp.Props {
		a, supplied := args[p.Name]
		if !supplied {
			continue
		}
		// The type the value HAS, substituted through what this call bound.
		// Not the prop's declared type: `on T` says nothing, and it is the
		// argument that a target will hold and compare.
		var t *ir.Type
		if a.value != nil {
			t = a.value.ExprType().Substitute(scope.env)
		}
		if mentionsTypeParam(t) {
			// Still a parameter, so this body is being read on its own terms
			// and the call sites owe the answer.
			continue
		}
		switch {
		case comp.Builtin == ir.BuiltinEffect && p.Name == "on":
			w.reportKey(inst, a, t, scope)
		case p.Construct && t != nil:
			w.reportConstruct(inst, comp, p, a, t, scope)
		}
	}
	w.descend(inst, args, scope)
}

// descend walks a generic component's body with its parameters pinned to what
// this call bound them to.
//
// Only a generic one: a body with no parameters in it says the same thing
// wherever it is placed, and it was already walked as an owner.
func (w *rebuildWalker) descend(inst *ir.NodeInst, args map[string]rebuildArg, scope rebuildScope) {
	comp := inst.Component
	if len(comp.TypeParams) == 0 || len(comp.Body) == 0 || scope.depth >= maxRebuildDepth {
		return
	}
	env := w.bindings(comp, args, scope.env)
	if len(env) == 0 {
		return
	}
	frame := rebuildFrame{comp: comp, env: envKey(env)}
	if w.frames[frame] {
		return
	}
	w.frames[frame] = true
	site := scope.site
	if !scope.bound() {
		site = ir.StmtPos(inst)
	}
	w.body(comp.Body, rebuildScope{holder: comp, env: env, site: site, depth: scope.depth + 1})
}

// bindings is what this call site pins comp's type parameters to, read from the
// types of the values it supplies. The same rule bindComponentTypeParams states
// for a call site, over the checked IR rather than the AST: a value whose own
// type is still a parameter of the enclosing generic contributes what that
// parameter was bound to, which is what makes the answer transitive.
func (w *rebuildWalker) bindings(comp *ir.Component, args map[string]rebuildArg, env map[string]*ir.Type) map[string]*ir.Type {
	out := map[string]*ir.Type{}
	for _, p := range comp.Props {
		if !mentionsTypeParam(p.Type) {
			continue
		}
		a, supplied := args[p.Name]
		if !supplied || a.value == nil {
			continue
		}
		t := a.value.ExprType().Substitute(env)
		// A dyn argument states nothing, and one still typed by a parameter
		// states only that this call did not pin it either.
		if t == nil || t.Kind == ir.TypeDyn || mentionsTypeParam(t) {
			continue
		}
		bindTypeParams(p.Type, t, out)
	}
	for _, tp := range comp.TypeParams {
		if tp.Default == nil {
			continue
		}
		if _, bound := out[tp.Name]; !bound {
			out[tp.Name] = tp.Default
		}
	}
	return out
}

func envKey(env map[string]*ir.Type) string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(env[name].String())
		b.WriteByte(';')
	}
	return b.String()
}

// reportKey refuses an `on` whose type does not compare alike everywhere.
//
// `on` is the identity of the lifetime: while it holds the same value this is
// the same effect, so every target has to agree on when two of them are the
// same value. Not every type does -- Go compares two lists by refusing to, JS
// compares two objects by identity -- and the settle's only answer for one that
// does not was to rebuild the bracket on every pass, which is `+` under
// `sngl test` and `+,-,+` compiled.
//
// Nothing is said about an effect with no `on`: the parameter's default binds T
// to a struct with no fields, which ir.RebuildComparable admits and the
// comparison reduces to "never differs" -- exactly what a bracket that lives as
// long as its node means.
func (w *rebuildWalker) reportKey(inst *ir.NodeInst, a rebuildArg, t *ir.Type, scope rebuildScope) {
	why := ir.RebuildIncomparable(t)
	if why == "" {
		return
	}
	if !scope.bound() {
		w.report(a.pos, inst, "an effect's `on` is the identity of its lifetime, so it must compare alike on every target, and %s does not%s", why, effectKeyHint(t))
		return
	}
	// Reported at the call that bound the parameter, which names no effect at
	// all -- so the message says whose body holds the bracket, and what this
	// call made its key.
	w.report(scope.site, inst, "component %s keys an effect on a value this call binds to %s: an effect's `on` is the identity of its lifetime, so it must compare alike on every target, and %s does not%s",
		scope.holder.Name, t, why, effectKeyHint(t))
}

// reportConstruct refuses a #[construct] prop whose bound type does not compare
// alike on every target.
//
// The declaration answers this one only when it is written concretely, which
// checkConstructProps does. `#[construct] seed T` is admitted where it is
// written -- judging a parameter there would make a generic component
// undeclarable -- and the call is the only place T becomes a type. Unchecked,
// bound to a list, the prop reached fyne's rebuild comparison as
// `m.__inst0_was_seed[i] == m.xs` over a []any holding a []int, which is a Go
// runtime panic rather than any kind of diagnostic.
//
// The diagnostic names the bound type as well as the prop, because the
// declaration the reader is sent to says `T` and nothing about a list.
func (w *rebuildWalker) reportConstruct(inst *ir.NodeInst, comp *ir.Component, p *ir.Prop, a rebuildArg, t *ir.Type, scope rebuildScope) {
	why := ir.RebuildIncomparable(t)
	if why == "" {
		return
	}
	pos := a.pos
	if scope.bound() {
		pos = scope.site
	}
	w.report(pos, inst, "#[construct] prop %q on component %s is bound to %s here: the instance is rebuilt when this value changes, so it must compare alike on every target, and %s does not",
		p.Name, comp.Name, t, why)
}

func (w *rebuildWalker) report(pos ast.Pos, inst *ir.NodeInst, format string, args ...any) {
	if !pos.IsValid() {
		pos = ir.StmtPos(inst)
	}
	msg := fmt.Sprintf(format, args...)
	d := rebuildDiag{pos: pos, msg: msg}
	if w.reported[d] {
		return
	}
	w.reported[d] = true
	w.c.error(pos, "%s", msg)
}

// rebuildArg is one value a call site supplied for a declared prop: the
// expression, and where to point at it.
//
// pos is the name the value was written under, which is the only position the
// checked IR keeps -- an ir.Expr carries none. A positional arg has none
// either, and those fall back to the node's own position.
type rebuildArg struct {
	value ir.Expr
	pos   ast.Pos
}

// instArgs is what one call site supplied, keyed by the declared prop it
// supplied it for.
//
// Positional args arrive unnamed when the split had no component to match them
// against -- which is every stdlib element, `effect` included -- so the order
// is replayed here against the same prop list checkAndSplitArgs uses: the
// declared props minus the wildcard, which stands for names and takes no
// position. A binding is the same prop written to be written back, so its
// target is the value this instance is built from.
func instArgs(inst *ir.NodeInst) map[string]rebuildArg {
	comp := inst.Component
	var ordered []*ir.Prop
	for _, p := range comp.Props {
		if p.Wildcard == "" {
			ordered = append(ordered, p)
		}
	}
	out := make(map[string]rebuildArg, len(inst.Props))
	positional := 0
	for _, a := range inst.Props {
		name := a.Name
		if name == "" {
			if positional < len(ordered) {
				name = ordered[positional].Name
			}
			positional++
		}
		if name == "" {
			continue
		}
		if _, dup := out[name]; dup {
			continue
		}
		out[name] = rebuildArg{value: a.Value, pos: a.NamePos}
	}
	for _, b := range inst.Bindings {
		if _, dup := out[b.PropName]; dup {
			continue
		}
		out[b.PropName] = rebuildArg{value: b.Target, pos: b.NamePos}
	}
	return out
}

// effectKeyHint names the form that does work, for the two mistakes that have
// one. A key over several values is what a struct is for; the list and the
// anonymous struct literal are both people reaching for that and finding the
// nearest brace.
func effectKeyHint(t *ir.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeList:
		return "; a key over several values is a struct of them, not a list"
	case ir.TypeMap:
		return "; a key over several values is a struct of them, not a map"
	case ir.TypeDyn:
		return "; a key over several values is a struct of them, and the struct has to be declared"
	case ir.TypeStruct:
		// An unresolved struct type. An anonymous literal is not one: the
		// checker interns a declaration for it, fields and all.
		if t.Decl == nil {
			return "; a key over several values is a struct of them, and the struct has to be declared"
		}
	}
	return ""
}
