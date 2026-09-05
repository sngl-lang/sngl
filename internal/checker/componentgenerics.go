package checker

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// bindComponentTypeParams resolves a generic component's type parameters
// against the props one call site supplies, and returns the specialization
// those props are checked against.
//
// A component's parameters are bound from its props for the same reason a
// func's are bound from its arguments: there is no syntax for naming a type
// argument at a call site, so the values written there are the only statement
// of what the parameters are. `effect(on=page)` binds T from `page`, which is
// what then types the `@mount(v)` payload.
//
// The result is for checking the call site and nothing else. NodeInst.Component
// keeps the generic declaration, because that is the identity every later pass
// compares against -- the inliner's cycle set, the local-component set, the
// method table keyed by name. A specialization put there would be a component
// nothing else in the program has heard of.
func (c *checker) bindComponentTypeParams(comp *ir.Component, args ast.ArgList) *ir.Component {
	if comp == nil || len(comp.TypeParams) == 0 {
		return comp
	}
	bindings := map[string]*ir.Type{}
	for _, p := range comp.Props {
		if p.Type == nil || !mentionsTypeParam(p.Type) {
			continue
		}
		argType := c.propArgType(args, comp, p)
		// A dyn argument states nothing: binding T to it would make every
		// other prop typed T accept anything, which is the opposite of what
		// the annotation asked for. Same rule as inferTypeParams.
		if argType == nil || argType.Kind == ir.TypeDyn {
			continue
		}
		bindTypeParams(p.Type, argType, bindings)
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
	spec := specializeComponent(comp, bindings)
	c.checkConstructBindings(args, comp, spec)
	return spec
}

// checkConstructBindings refuses a #[construct] prop whose *bound* type does not
// compare alike on every target.
//
// The declaration cannot answer this one. `#[construct] seed T` is admitted
// where it is written because RebuildIncomparable passes a type parameter
// through -- judging it there would make a generic component undeclarable --
// and the call site is the only place T becomes a type. So the rule the mark
// carries is stated in two halves, and this is the half nobody was checking:
// bound to a list, the prop reached fyne's rebuild comparison as
// `m.__inst0_was_seed[i] == m.xs` over a []any holding a []int, which is a Go
// runtime panic rather than any kind of diagnostic.
//
// The diagnostic names the bound type as well as the prop, because the
// declaration the reader is sent to says `T` and nothing about a list.
func (c *checker) checkConstructBindings(args ast.ArgList, comp, spec *ir.Component) {
	for i, p := range comp.Props {
		if !p.Construct || !mentionsTypeParam(p.Type) {
			continue
		}
		bound := spec.Props[i].Type
		// Still a parameter: this call bound something else, and a prop nobody
		// pinned takes the same latitude an unpinned parameter takes anywhere.
		if mentionsTypeParam(bound) {
			continue
		}
		why := ir.RebuildIncomparable(bound)
		if why == "" {
			continue
		}
		pos := args.Pos
		if v := propArgValue(args, comp, p); v != nil {
			pos = *v.ExprPos()
		}
		c.error(pos, "#[construct] prop %q on component %s is bound to %s here: the instance is rebuilt when this value changes, so it must compare alike on every target, and %s does not",
			p.Name, comp.Name, bound, why)
	}
}

// propArgType is the type of the value a call site supplied for prop p, or nil
// when it supplied none. The three forms checkAndSplitArgs accepts are the
// three answered here: a named arg, a positional one, and a field of a struct
// spread.
func (c *checker) propArgType(args ast.ArgList, comp *ir.Component, p *ir.Prop) *ir.Type {
	if val := propArgValue(args, comp, p); val != nil {
		return exprType(c.checkExpr(val))
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
		t := exprType(c.checkExpr(spread.Operand))
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

// propArgValue is the expression a call site supplied for prop p, or nil.
// Named args win over position, and a wildcard prop has no position at all --
// it stands for names, so binding one positionally would give it a name nobody
// wrote. Mirrors the matching checkAndSplitArgs does.
func propArgValue(args ast.ArgList, comp *ir.Component, p *ir.Prop) ast.Expr {
	var ordered []*ir.Prop
	for _, q := range comp.Props {
		if q.Wildcard == "" {
			ordered = append(ordered, q)
		}
	}
	pos := 0
	var positional ast.Expr
	for _, a := range args.Args {
		arg, isArg := a.(ast.Arg)
		if !isArg || arg.Value == nil {
			continue
		}
		if arg.Name == "key" {
			continue
		}
		if arg.Name != "" {
			// A binding is the same prop under a different spelling: the
			// leading `:` says the value is written back, not that a different
			// name was supplied, and checkAndSplitArgs strips it before it
			// matches. Compared raw, `:on=n` bound no parameter at all, so
			// every other prop typed T accepted anything -- `T` spelled at
			// greater length, which is exactly what the annotation exists to
			// prevent.
			if strings.TrimPrefix(arg.Name, ":") == p.Name {
				return arg.Value
			}
			continue
		}
		// A spread carries no name and takes no position: it expands to named
		// props. Read as a positional it bound the first prop's type parameter
		// to the struct being spread -- `effect(...a)` made T the struct, and
		// the `@mount(v)` payload with it.
		if _, isSpr := arg.Value.(*ast.SpreadExpr); isSpr {
			continue
		}
		if pos < len(ordered) && ordered[pos] == p {
			positional = arg.Value
		}
		pos++
	}
	return positional
}

// mentionsTypeParam reports whether t names a type parameter anywhere inside
// it, so a prop that cannot contribute a binding is not checked twice for
// nothing.
func mentionsTypeParam(t *ir.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == ir.TypeTypeParam {
		return true
	}
	if slices.ContainsFunc(t.Elems, mentionsTypeParam) {
		return true
	}
	if t.Kind == ir.TypeFunc && t.Sig != nil {
		for _, p := range t.Sig.Params {
			if mentionsTypeParam(p.Type) {
				return true
			}
		}
		return mentionsTypeParam(t.Sig.Return)
	}
	return false
}

// specializeComponent is comp with bindings applied to everything a call site
// is checked against: the props it passes, the payload of the events it
// handles, and the content its slots accept. The body is not substituted --
// nothing checks a body here, and the declaration keeps the one body it has.
func specializeComponent(comp *ir.Component, bindings map[string]*ir.Type) *ir.Component {
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
		f.Type = e.Type.Substitute(bindings)
		out.Events[i] = &f
	}
	out.Slots = make([]*ir.SlotDecl, len(comp.Slots))
	for i, s := range comp.Slots {
		t := *s
		t.Content = s.Content.Substitute(bindings)
		if len(s.Params) > 0 {
			t.Params = make([]*ir.Type, len(s.Params))
			for j, pt := range s.Params {
				t.Params[j] = pt.Substitute(bindings)
			}
		}
		out.Slots[i] = &t
	}
	return &out
}

// checkEffectHandlers reports an effect that brackets nothing.
//
// Either handler alone is a whole bracket -- a subscription with nothing to
// give back, or a teardown for something the tree itself set up -- so the rule
// is that at least one is written. Neither is a node that runs no code and
// holds no state, which is a program that meant to say something else.
func (c *checker) checkEffectHandlers(vn *ast.VisualNode) {
	for _, a := range vn.Args.Args {
		if h, isHandler := a.(ast.EventHandler); isHandler {
			if h.Name == "mount" || h.Name == "unmount" {
				return
			}
		}
	}
	c.error(vn.Pos, "effect declares neither @mount nor @unmount, so it brackets nothing")
}

// checkEffectKey refuses an `on` whose type does not compare alike everywhere.
//
// `on` is the identity of the lifetime: while it holds the same value this is
// the same effect, so every target has to agree on when two of them are the
// same value. Not every type does -- Go compares two lists by refusing to, JS
// compares two objects by identity -- and the settle's only answer for one that
// does not was to rebuild the bracket on every pass, which is `+` under
// `sngl test` and `+,-,+` compiled. Reported where the key is written rather
// than lowered into a program whose brackets mean something different on each
// build.
//
// Nothing is said about an effect with no `on`: the parameter's default binds T
// to a struct with no fields, which ir.RebuildComparable admits and the
// comparison reduces to "never differs" -- exactly what a bracket that lives as
// long as its node means.
func (c *checker) checkEffectKey(node ast.Pos, props []ir.Arg) {
	for _, p := range props {
		if p.Name != "on" || p.Value == nil {
			continue
		}
		t := p.Value.ExprType()
		why := ir.RebuildIncomparable(t)
		if why == "" {
			continue
		}
		pos := p.NamePos
		if !pos.IsValid() {
			pos = node
		}
		c.error(pos, "an effect's `on` is the identity of its lifetime, so it must compare alike on every target, and %s does not%s", why, effectKeyHint(t))
	}
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
		// An anonymous struct literal. It reaches here as a struct with no
		// declaration, which is the whole reason it cannot be compared: there
		// are no fields to walk.
		if t.Decl == nil {
			return "; a key over several values is a struct of them, and the struct has to be declared"
		}
	}
	return ""
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
