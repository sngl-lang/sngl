package checker

import (
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A trigger is a target's override of a component's event: how a test raises
// the event on that target, `func ui.select[T].change[platform](value)`. It is
// written beside the target's override of the component and is a method of
// that override, so its body reads what the override selected and the nodes it
// named; its parameters are the event's, selected by name as an override's
// are. A test's `c.pick.change(v)` runs it in place of reaching the widget
// itself.
type pendingTrigger struct {
	decl     *ast.FuncDef
	comp     *ir.Component
	event    *ir.EventDecl
	platform string
	kind     ir.BuiltinKind
	// libURI is the target package the trigger was written in, empty for a
	// program's.
	libURI string
	// taken says an override of the component consumed it.
	taken bool
}

// triggerOf resolves the component and event an override's name says, nil
// when it names no event: `dial.change`, or `ui.select.change` through the
// import aliases (a target package's) or the scope (a program's). A method of
// the name is the method's override, never the event's.
func (c *checker) triggerOf(decl *ast.FuncDef, aliases map[string]string) (*ir.Component, *ir.EventDecl) {
	recv, event, ok := ast.SplitMethodName(decl.Name)
	if !ok {
		return nil, nil
	}
	var comp *ir.Component
	if ns, member, qualified := strings.Cut(recv, "."); qualified {
		var pkg *ir.Package
		if name, ok := aliases[ns]; ok {
			pkg = c.libPkg(name)
		} else if sym, ok := c.scope.Lookup(ns); ok {
			if n, isNS := sym.(*ir.Namespace); isNS {
				pkg = n.Pkg
			}
		}
		if pkg == nil || pkg.Symbols == nil {
			return nil, nil
		}
		sym, ok := pkg.Symbols.LookupRootComponent(member)
		if !ok {
			return nil, nil
		}
		comp, _ = sym.(*ir.Component)
	} else if sym, ok := c.scope.Lookup(recv); ok {
		comp, _ = sym.(*ir.Component)
	}
	if comp == nil {
		return nil, nil
	}
	if _, isMethod := comp.Methods[event]; isMethod {
		return nil, nil
	}
	for _, e := range comp.Events {
		if e.Name == event {
			return comp, e
		}
	}
	return nil, nil
}

// collectTrigger records decl as a trigger when its name is an event's, and
// says whether it was one. What it can check without the override it belongs
// to is checked here: the target, a receiver's parameters, and the event's
// parameters it selects.
func (c *checker) collectTrigger(decl *ast.FuncDef, aliases map[string]string, libURI string) bool {
	comp, event := c.triggerOf(decl, aliases)
	if comp == nil {
		return false
	}
	plat, kind, ok := c.resolveTargetIndex(decl.Target)
	if !ok {
		return true
	}
	if owner, tier, isTarget := targetTierName(libURI); isTarget && (kind != tier || plat != owner) {
		c.error(decl.Pos, "package for %q may not declare an override for %q", owner, plat)
		return true
	}
	if decl.ReturnType != nil || len(decl.TypeParams) > 0 {
		c.error(decl.Pos, "override %q of an event declares nothing of its own: its parameters are the event's", decl.Name)
		return true
	}
	// The receiver is written as its declaration names it, parameters and
	// all: an event of a generic component is overridden on `pick[T]`.
	want := make([]string, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		want[i] = tp.Name
	}
	got := make([]string, len(decl.RecvTypeParams))
	for i, tp := range decl.RecvTypeParams {
		got[i] = tp.Name
	}
	if !slices.Equal(want, got) {
		recv, _, _ := ast.SplitMethodName(decl.Name)
		written := recv
		if len(want) > 0 {
			written += "[" + strings.Join(want, ", ") + "]"
		}
		c.error(decl.Pos, "override %q names its receiver as %s declares it: %s", decl.Name, comp.Name, written)
		return true
	}
	byName := map[string]*ir.Param{}
	for _, p := range event.Params {
		if p.Name != "" {
			byName[p.Name] = p
		}
	}
	seen := map[string]bool{}
	for _, p := range decl.Params.Params {
		var t *ir.Type
		if bp := byName[p.Name]; bp != nil {
			t = bp.Type
		}
		c.selectEntry(decl.Name, p, t, byName[p.Name] != nil, seen)
	}
	for _, pt := range c.pendingTriggers {
		if pt.comp == comp && pt.event == event && pt.platform == plat && pt.kind == kind {
			c.error(decl.Pos, "event %q of %s already has an override for %q", event.Name, comp.Name, plat)
			return true
		}
	}
	c.pendingTriggers = append(c.pendingTriggers, &pendingTrigger{decl: decl, comp: comp, event: event, platform: plat, kind: kind, libURI: libURI})
	return true
}

// triggerName is the name a trigger is held under among its override's
// methods: never the event's own, which the override's body calls to emit it.
func triggerName(event string) string { return event + "__trigger" }

// triggersOf takes the triggers written for base's override on platform, as
// the nested funcs that override registers: each is the event's parameters
// the declaration selected, in the event's order, and the body it wrote.
func (c *checker) triggersOf(base *ir.Component, kind ir.BuiltinKind, platform string) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, pt := range c.pendingTriggers {
		if pt.taken || pt.comp != base || pt.kind != kind || pt.platform != platform {
			continue
		}
		pt.taken = true
		written := map[string]ast.Param{}
		for _, p := range pt.decl.Params.Params {
			written[p.Name] = p
		}
		// Each entry carries the type the event's declaration wrote, so the
		// method registers as the event's shape; typeTriggers then gives
		// it the event's resolved types.
		declared := map[string]ast.TypeExpr{}
		if base.AST != nil {
			for _, p := range base.AST.Props.Props {
				if ed, ok := p.(ast.EventDecl); ok && ed.Name == pt.event.Name {
					for _, ep := range ed.Params.Params {
						declared[ep.Name] = ep.Type
					}
				}
			}
		}
		var params []ast.Param
		for _, p := range pt.event.Params {
			if w, ok := written[p.Name]; ok {
				if w.Type == nil {
					w.Type = declared[p.Name]
				}
				params = append(params, w)
			}
		}
		fd := *pt.decl
		fd.Name = triggerName(pt.event.Name)
		fd.Target = nil
		fd.RecvTypeParams = nil
		fd.Params = ast.ParamList{Pos: pt.decl.Params.Pos, IsMultiline: pt.decl.Params.IsMultiline, Params: params}
		out = append(out, &fd)
	}
	return out
}

// typeTriggers gives each trigger among fns the event's parameter types,
// which its declaration did not write, and the event it raises.
func typeTriggers(comp *ir.Component, fns []*ir.Func) {
	for _, fn := range fns {
		event, ok := strings.CutSuffix(fn.Name, "__trigger")
		if !ok {
			continue
		}
		var decl *ir.EventDecl
		for _, e := range comp.Events {
			if e.Name == event {
				decl = e
			}
		}
		if decl == nil {
			continue
		}
		fn.Trigger = event
		for _, p := range fn.Params {
			if p.Receiver {
				continue
			}
			for _, ep := range decl.Params {
				if ep.Name == p.Name {
					p.Type = ep.Type
				}
			}
		}
	}
}

// reportUntakenTriggers reports each trigger no override of its component
// took: its body reads the target's override, so one written for a target
// that has none reads nothing that exists.
func (c *checker) reportUntakenTriggers(libURI string) {
	for _, pt := range c.pendingTriggers {
		if pt.taken || pt.libURI != libURI {
			continue
		}
		pt.taken = true
		c.error(pt.decl.Pos, "override %q of an event is a method of the override of %s for %q, and there is none", pt.decl.Name, pt.comp.Name, pt.platform)
	}
}
