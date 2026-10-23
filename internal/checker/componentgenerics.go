package checker

import (
	"maps"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// bindComponentTypeParams resolves a generic component's type parameters
// against the props one call site supplies, and returns the specialization
// those props are checked against.
//
// A component's parameters are bound from its props as a func's are bound from
// its arguments, after any the call site wrote, `tagged[int](v=1)`.
// `effect(on=page)` binds T from `page`, which is what then types the
// `@mount(v)` payload.
//
// The result is for checking the call site and nothing else. NodeInst.Component
// keeps the generic declaration, because that is the identity every later pass
// compares against -- the inliner's cycle set, the local-component set, the
// method table keyed by name. A specialization put there would be a component
// nothing else in the program has heard of.
func (c *checker) bindComponentTypeParams(pos ast.Pos, comp *ir.Component, args ast.ArgList, written map[string]*ir.Type) *ir.Component {
	if comp == nil || len(comp.TypeParams) == 0 {
		return comp
	}
	// What the call site wrote binds first, and the props are held to it.
	bindings := maps.Clone(written)
	if bindings == nil {
		bindings = map[string]*ir.Type{}
	}
	for _, p := range comp.Props {
		if p.Type == nil || !p.Type.HasTypeParam() {
			continue
		}
		argType := c.propArgType(args, comp, p)
		// An invalid argument states nothing: binding T to it would make every
		// other prop typed T accept anything, which is the opposite of what
		// the annotation asked for. Same rule as inferTypeParams.
		if argType == nil || argType.Kind == ir.TypeInvalid {
			continue
		}
		ir.BindTypeParams(p.Type, argType, bindings)
	}
	bindEffectChanges(comp, bindings)
	c.bindFromHandlers(comp, args, bindings)
	c.checkConstraints(pos, comp.TypeParams, bindings)
	return c.specializeWith(comp, bindings)
}

// bindEffectChanges binds an effect's change parameters from its key: a list's
// changes are reported by index and element. Nothing a call site writes says
// what an element type is but the key, and a handler annotating `item` should
// be held to it rather than bind it, so this runs before bindFromHandlers.
// A key that is not a list leaves them at their defaults, and the checker
// refuses a change handler on one (checkEffectChanges).
func bindEffectChanges(comp *ir.Component, bindings map[string]*ir.Type) {
	if comp.Builtin != ir.BuiltinEffect || len(comp.TypeParams) <= ir.EffectElemParam {
		return
	}
	key := bindings[comp.TypeParams[ir.EffectKeyParam].Name]
	if key == nil || key.Kind != ir.TypeList || len(key.Elems) != 1 {
		return
	}
	bindings[comp.TypeParams[ir.EffectIndexParam].Name] = ir.TypInt
	bindings[comp.TypeParams[ir.EffectElemParam].Name] = key.Elems[0]
}

// bindFromHandlers binds what the props left unbound from the types a handler
// writes on its parameters, the way a lambda's annotation binds a func's:
// `gen.emit(@generate(out, members list[gen.Member[Entry]]) { … })` says T is
// Entry, and nothing else at that call site could. A written type is resolved
// quietly here, since bindParams resolves it again and reports it there.
func (c *checker) bindFromHandlers(comp *ir.Component, args ast.ArgList, bindings map[string]*ir.Type) {
	for _, a := range args.Args {
		h, ok := a.(ast.EventHandler)
		if !ok {
			continue
		}
		var ev *ir.EventDecl
		for _, e := range comp.Events {
			if e.Name == h.Name {
				ev = e
			}
		}
		if ev == nil {
			continue
		}
		for i, p := range h.Params.Params {
			if p.Type == nil || i >= len(ev.Params) || ev.Params[i].Type == nil || !ev.Params[i].Type.HasTypeParam() {
				continue
			}
			got := c.resolveTypeQuietly(p.Type)
			if got == nil || got.Kind == ir.TypeInvalid {
				continue
			}
			ir.BindTypeParams(ev.Params[i].Type, got, bindings)
		}
	}
}

// specializeWith is the specialization of comp at bindings, its defaults
// filling what they leave unbound. What the props pinned is recorded, so the
// children may bind the rest once they are checked (bindFromChildren) without
// the props' expressions being walked a second time.
func (c *checker) specializeWith(comp *ir.Component, pinned map[string]*ir.Type) *ir.Component {
	bindings := maps.Clone(pinned)
	if bindings == nil {
		bindings = map[string]*ir.Type{}
	}
	// A parameter no prop pinned falls back to its default, the way a struct's
	// does when the type-argument list stops short. One with neither stays a
	// parameter, and a prop typed by it accepts anything -- the same latitude a
	// func's unpinned parameter gets.
	for _, tp := range comp.TypeParams {
		if tp.Default == nil {
			continue
		}
		if _, bound := bindings[tp.Name]; !bound {
			bindings[tp.Name] = tp.Default
		}
	}
	if len(bindings) == 0 {
		return comp
	}
	if c.entryOrigin == nil {
		c.entryOrigin = map[*ir.SlotDecl]*ir.SlotDecl{}
	}
	spec := specializeComponent(comp, bindings, c.entryOrigin)
	// The copy carries whatever family the declaration had when it was taken,
	// which is none while one is still being inferred. inferComponentTrees
	// finishes the job here.
	if c.specOrigin == nil {
		c.specOrigin = map[*ir.Component]*ir.Component{}
	}
	c.specOrigin[spec] = comp
	if c.specArgs == nil {
		c.specArgs = map[*ir.Component][]*ir.Type{}
	}
	bound := make([]*ir.Type, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		if b, ok := bindings[tp.Name]; ok {
			bound[i] = b
		} else {
			bound[i] = &ir.Type{Kind: ir.TypeTypeParam, ParamName: tp.Name}
		}
	}
	c.specArgs[spec] = bound
	if c.specPinned == nil {
		c.specPinned = map[*ir.Component]map[string]*ir.Type{}
	}
	c.specPinned[spec] = pinned
	return spec
}

// bindFromChildren binds the type parameters a generic family names in the
// rest slot's type from the children written there, where no prop pinned
// them: `stack[M any](pages ...component _page[M])` holds pages each of which is
// a `_page[Meta]`, so the stack is a `stack[Meta]`. The first child that is a
// member says what they are, and every other is held to it, since one slot
// holds one type. Returns spec, or the specialization the children bound.
func (c *checker) bindFromChildren(comp, spec *ir.Component, children []ir.Stmt) *ir.Component {
	if comp == nil || len(comp.TypeParams) == 0 {
		return spec
	}
	rest := comp.RestSlot()
	if rest == nil || rest.Content == nil || len(rest.Content.Elems) == 0 || !rest.Content.HasTypeParam() {
		return spec
	}
	fam := ir.TypeFamily(rest.Content)
	if fam == nil {
		return spec
	}
	pinned := c.specPinned[spec]
	bindings := maps.Clone(pinned)
	if bindings == nil {
		bindings = map[string]*ir.Type{}
	}
	var first *ir.Type
	var firstAt ast.Pos
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, st := range stmts {
			if _, binds, ok := familyTransparent(st); ok {
				for _, b := range binds {
					walk(b)
				}
				continue
			}
			n, ok := st.(*ir.NodeInst)
			// A member that is no value of the family has no one type to
			// hold; the members it composes are checked where it is.
			if !ok || n.Component == nil || n.Component.Family != fam || !ir.IsFamilyValue(n.Component) {
				continue
			}
			got := &ir.Type{Kind: ir.TypeComponent, Decl: fam, Elems: ir.FamilyArgs(n.Component, n.TypeArgs)}
			if first == nil {
				first = got
				if at := stmtPos(n.AST); at != nil {
					firstAt = *at
				}
				ir.BindTypeParams(rest.Content, got, bindings)
				continue
			}
			if !got.Equal(first) {
				at := stmtPos(n.AST)
				if at == nil {
					continue
				}
				c.error(*at, "this %s is a %s, and %s holds %s: every member one %s holds is the same type",
					n.Component.DisplayName(), got, comp.DisplayName(), first, comp.DisplayName())
			}
		}
	}
	walk(children)
	if first == nil {
		return spec
	}
	// What the props pinned was measured where they bound it.
	inferFromConstraints(comp.TypeParams, bindings)
	for _, tp := range comp.TypeParams {
		if _, was := pinned[tp.Name]; !was {
			if t, ok := bindings[tp.Name]; ok {
				c.checkConstraint(firstAt, tp, t, bindings)
			}
		}
	}
	return c.specializeWith(comp, bindings)
}

// specializeHandle gives the handle a node's `#id` declared the type
// arguments its call site bound, so `box #counts(item=1)` is a `box[int]`
// wherever it is read. The id is hoisted before any node is checked, when the
// arguments are not known, so the var is completed here -- the count it was
// hoisted through, an option or a list, is kept around it.
func (c *checker) specializeHandle(id string, comp, spec *ir.Component) {
	args := c.specArgs[spec]
	if len(args) == 0 {
		return
	}
	v := c.nodeHandleSym(id)
	if v == nil {
		return
	}
	v.Type = withComponentArgs(v.Type, comp, args)
	if owner := c.currentComponent; owner != nil {
		// Read from outside through the declaration, never the copy an
		// override is checked on.
		if owner == c.installedOverride {
			owner = c.overriddenBase
		}
		if c.handleArgs == nil {
			c.handleArgs = map[handleKey][]*ir.Type{}
		}
		c.handleArgs[handleKey{owner, id}] = args
	}
}

// preSpecializeHandles gives each generic node's handle in s the type
// arguments its props bind, before the scope holding it is checked: a read
// written above the node -- a page's handler going to a page written below it,
// `pages.go(detail, Mon{…})` -- otherwise saw the bare declaration and was
// refused (`cannot pass page as page[T, M]`), since the node's own check is
// what completes its handle. The props are read quietly, as
// bindComponentTypeParams reads them at the node, so one reading what is not
// declared yet -- a loop's variable, a local written further down -- binds
// nothing here, and its node completes the handle as before; so does what the
// children bind (bindFromChildren). nested says whether to reach into an `if`
// or a `for`, which a component's own hoist does and a block's does not:
// those hoist their own ids when they are checked.
func (c *checker) preSpecializeHandles(s ast.Stmt, nested bool) {
	block := func(b *ast.StmtBlock) {
		if b == nil || !b.IsDefined() {
			return
		}
		for _, s := range b.Stmts {
			c.preSpecializeHandles(s, nested)
		}
	}
	switch n := s.(type) {
	case *ast.VisualNode:
		c.preSpecializeHandle(n)
		block(&n.Block)
	case *ast.CallStmt:
		// `tagged #box(item=41)`, a node with no block, which checkStmt
		// checks as the node it is.
		if _, _, isElem := elementRefCallInfo(n.Call); isElem {
			if target, ok := n.Call.Func.(ast.TargetExpr); ok {
				c.preSpecializeHandle(&ast.VisualNode{Pos: n.Pos, Target: target, ID: n.Call.ID, Args: n.Call.Args})
			}
		}
	case *ast.IfStmt:
		if nested {
			block(&n.Body)
			block(&n.Else)
		}
	case *ast.MatchStmt:
		if nested {
			for _, arm := range n.Arms {
				block(&arm.Body)
			}
		}
	case *ast.ForStmt:
		if nested {
			block(&n.Body)
			block(&n.Else)
		}
	}
}

func (c *checker) preSpecializeHandle(n *ast.VisualNode) {
	if n.ID == "" {
		return
	}
	name := visualNodeTarget(n)
	comp := c.componentNamed(name)
	if comp == nil || len(comp.TypeParams) == 0 {
		return
	}
	var spec *ir.Component
	c.quietly(func() {
		written, _ := c.bindWritten(n.Pos, name, n.TypeArgs, comp.TypeParams)
		spec = c.bindComponentTypeParams(n.Pos, comp, n.Args, written)
	})
	if spec != comp {
		c.specializeHandle(n.ID, comp, spec)
	}
}

// handleKey names a handle from outside the body declaring it, the way a
// select `c.box` reaches one: the component and the id.
type handleKey struct {
	owner *ir.Component
	id    string
}

// componentArgBindings is what a component type's arguments bind its
// declaration's type parameters to: `page[Pkg]` binds T to Pkg, so a select
// through it reads `params` as a Pkg.
func componentArgBindings(t *ir.Type, comp *ir.Component) map[string]*ir.Type {
	if t == nil || len(t.Elems) != len(comp.TypeParams) || len(comp.TypeParams) == 0 {
		return nil
	}
	b := make(map[string]*ir.Type, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		b[tp.Name] = t.Elems[i]
	}
	return b
}

// withComponentArgs is t with the component type naming comp, under any
// options and lists, carrying args.
func withComponentArgs(t *ir.Type, comp *ir.Component, args []*ir.Type) *ir.Type {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case ir.TypeComponent:
		if t.Decl != ir.Symbol(comp) {
			return t
		}
		cp := *t
		cp.Elems = args
		return &cp
	case ir.TypeOption, ir.TypeList:
		if len(t.Elems) != 1 {
			return t
		}
		cp := *t
		cp.Elems = []*ir.Type{withComponentArgs(t.Elems[0], comp, args)}
		return &cp
	}
	return t
}

// propArgType is the type of the value a call site supplied for prop p, or nil
// when it supplied none. The three forms checkAndSplitArgs accepts are the
// three answered here: a named arg, a positional one, and a field of a struct
// spread.
func (c *checker) propArgType(args ast.ArgList, comp *ir.Component, p *ir.Prop) *ir.Type {
	// Quietly: checkAndSplitArgs checks the argument against what this binds,
	// and reports it there.
	if val := propArgValue(args, comp, p); val != nil {
		return exprType(c.checkExprQuietly(val))
	}
	return c.spreadPropType(args, comp, p)
}

// spreadPropType is the type a `...expr` argument gives prop p: the type of the
// field of that name on the struct being spread.
//
// A spread is where a call site's props come from without any of them being
// written, so a type parameter has to be bound from one -- and the field type
// is the whole of what the parameter can learn.
func (c *checker) spreadPropType(args ast.ArgList, comp *ir.Component, p *ir.Prop) *ir.Type {
	for _, a := range args.Args {
		arg, isArg := a.(ast.Arg)
		if !isArg || arg.Value == nil {
			continue
		}
		spread, isSpr := arg.Value.(*ast.SpreadExpr)
		if !isSpr {
			continue
		}
		t := exprType(c.checkExprQuietly(spread.Operand))
		if t == nil || t.Kind != ir.TypeStruct {
			continue
		}
		sd, _ := t.Decl.(*ir.StructDef)
		if sd == nil {
			continue
		}
		for _, f := range sd.Fields {
			if f.Name == p.Name {
				return f.Type
			}
		}
	}
	return nil
}

// propArgValue is the expression a call site supplied for prop p, or nil:
// the argument naming it, or else the one bound to it by position.
func propArgValue(args ast.ArgList, comp *ir.Component, p *ir.Prop) ast.Expr {
	var positional ast.Expr
	for i, name := range argPropNames(args, comp) {
		if name != p.Name {
			continue
		}
		arg := args.Args[i].(ast.Arg)
		if arg.Name != "" {
			return arg.Value
		}
		if positional == nil {
			positional = arg.Value
		}
	}
	return positional
}

// argPropNames is the prop each of args binds, by index: the name an argument
// wrote, or the next prop by position for one that wrote none. "" is an
// argument that binds no one prop -- a handler, `key`, a spread (which
// expands to named props and takes no position), and a positional argument
// past the last prop.
//
// A binding is the same prop under a different spelling: the leading `:` says
// the value is written back, not that another name was supplied. And a
// wildcard prop has no position: it stands for names, and binding one
// positionally would give it a name nobody wrote.
func argPropNames(args ast.ArgList, comp *ir.Component) []string {
	var ordered []*ir.Prop
	if comp != nil {
		for _, q := range comp.Props {
			if q.Wildcard == "" {
				ordered = append(ordered, q)
			}
		}
	}
	names := make([]string, len(args.Args))
	pos := 0
	for i, a := range args.Args {
		arg, isArg := a.(ast.Arg)
		if !isArg || arg.Name == "key" {
			continue
		}
		if arg.Name != "" {
			names[i] = strings.TrimPrefix(arg.Name, ":")
			continue
		}
		if _, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
			continue
		}
		if pos < len(ordered) {
			names[i] = ordered[pos].Name
		}
		pos++
	}
	return names
}

// specializeComponent is comp with bindings applied to everything a call site
// is checked against: the props it passes, the payload of the events it
// handles, and the content its slots and their component entries accept. The
// body is not substituted -- nothing checks a body here, and the declaration
// keeps the one body it has.
//
// Each entry copied is recorded in origin against the declaration's own, which
// is what an insertion of it carries past the checker: the splicer and the
// interpreter find an entry by pointer among its slot's declared ones.
func specializeComponent(comp *ir.Component, bindings map[string]*ir.Type, origin map[*ir.SlotDecl]*ir.SlotDecl) *ir.Component {
	out := *comp
	out.Props = make([]*ir.Prop, len(comp.Props))
	for i, p := range comp.Props {
		q := *p
		q.Type = p.Type.Substitute(bindings)
		out.Props[i] = &q
	}
	out.Events = make([]*ir.EventDecl, len(comp.Events))
	for i, e := range comp.Events {
		f := *e
		f.Params = substituteParams(e.Params, bindings)
		out.Events[i] = &f
	}
	out.Slots = specializeSlots(comp.Slots, bindings)
	for i, s := range comp.Slots {
		for j, e := range s.Slots {
			recordEntryOrigin(out.Slots[i].Slots[j], e, origin)
		}
	}
	return &out
}

func specializeSlots(slots []*ir.SlotDecl, bindings map[string]*ir.Type) []*ir.SlotDecl {
	if len(slots) == 0 {
		return slots
	}
	out := make([]*ir.SlotDecl, len(slots))
	for i, s := range slots {
		t := *s
		t.Content = s.Content.Substitute(bindings)
		t.Params = substituteParams(s.Params, bindings)
		t.Slots = specializeSlots(s.Slots, bindings)
		out[i] = &t
	}
	return out
}

// recordEntryOrigin maps spec, and every entry under it, to the declared entry
// it was copied from.
func recordEntryOrigin(spec, decl *ir.SlotDecl, origin map[*ir.SlotDecl]*ir.SlotDecl) {
	origin[spec] = decl
	for i, e := range decl.Slots {
		recordEntryOrigin(spec.Slots[i], e, origin)
	}
}

// substituteParams copies a parameter list with each type specialized.
func substituteParams(params []*ir.Param, bindings map[string]*ir.Type) []*ir.Param {
	if len(params) == 0 {
		return params
	}
	out := make([]*ir.Param, len(params))
	for j, sp := range params {
		q := *sp
		q.Type = sp.Type.Substitute(bindings)
		out[j] = &q
	}
	return out
}

// declTypeBindings is what a component's type parameters stand for where the
// declaration itself is checked: their own defaults, and nothing else. A call
// site knows more -- it has props to bind from -- but a declaration is checked
// once, without one.
func declTypeBindings(comp *ir.Component) map[string]*ir.Type {
	if comp == nil || len(comp.TypeParams) == 0 {
		return nil
	}
	out := map[string]*ir.Type{}
	for _, tp := range comp.TypeParams {
		if tp.Default != nil {
			out[tp.Name] = tp.Default
		}
	}
	return out
}
