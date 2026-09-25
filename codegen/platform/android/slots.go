package android

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A user composable takes each slot as a nullable composable parameter whose
// arguments are the slot's invocation list, a component entry among them being
// a composable of its own. Null is "the caller supplied nothing", which is
// what renders the insertion's fallback block.

// slotParamName is what a slot is called as a parameter. The rest slot keeps
// the name its bare children have always arrived under.
func slotParamName(s *ir.SlotDecl) string {
	if s.Rest {
		return "slotContent"
	}
	return s.Name
}

func slotParamType(s *ir.SlotDecl) string {
	params := make([]string, s.Arity())
	for i := range params {
		if entry, v := s.EntryAt(i); entry != nil {
			params[i] = slotParamType(entry)
		} else {
			params[i] = kotlin.IRTypeToKt(s.Params[v].Type)
		}
	}
	return "(@Composable (" + strings.Join(params, ", ") + ") -> Unit)?"
}

// slotParams declares comp's slots, after its props.
func slotParams(comp *ir.Component) []string {
	var out []string
	for _, s := range comp.Slots {
		out = append(out, slotParamName(s)+": "+slotParamType(s)+" = null")
	}
	return out
}

// eventParamName is what a user composable calls an event it emits, the name
// KtIRContext.EmitText invokes.
func eventParamName(e string) string { return "on" + strings.ToUpper(e[:1]) + e[1:] }

// eventParams declares comp's events, after its slots.
func eventParams(comp *ir.Component) []string {
	var out []string
	for _, e := range comp.Events {
		payload := ""
		if e.Type != nil {
			payload = kotlin.IRTypeToKt(e.Type)
		}
		out = append(out, eventParamName(e.Name)+": (("+payload+") -> Unit)? = null")
	}
	return out
}

// handlerArgs is the lambda an instantiation passes for each event it
// subscribes to.
func (cc *irComposeContext) handlerArgs(n *ir.NodeInst) []string {
	var out []string
	for _, h := range n.Handlers {
		if h.Func == nil || !slices.ContainsFunc(n.Component.Events, func(e *ir.EventDecl) bool { return e.Name == h.Name }) {
			continue
		}
		out = append(out, cc.lambdaArg(eventParamName(h.Name), &ir.Lambda{Func: h.Func}))
	}
	return out
}

// populationArgs is what an instantiation passes for each slot it supplies.
func (cc *irComposeContext) populationArgs(n *ir.NodeInst) []string {
	var out []string
	for _, s := range n.Component.Slots {
		switch sc := n.Slots[s.Name]; {
		case sc != nil:
			out = append(out, slotParamName(s)+" = "+cc.slotLambda(s, sc, sc.Body))
		case s.Rest && len(n.Children) > 0:
			out = append(out, slotParamName(s)+" = "+cc.slotLambda(s, nil, n.Children))
		}
	}
	return out
}

// slotLambda is body as the composable a slot of decl's shape is passed as.
// The population's names are the lambda's parameters; bare children bind
// none, which is what `_` says for each value they are handed.
func (cc *irComposeContext) slotLambda(decl *ir.SlotDecl, sc *ir.SlotContent, body []ir.Stmt) string {
	names := make([]string, decl.Arity())
	for i := range names {
		names[i] = "_"
		if entry, v := decl.EntryAt(i); entry != nil {
			if bound := entryBinding(body, entry); bound != "" {
				names[i] = bound
			}
		} else if sc != nil && v < len(sc.Params) {
			names[i] = sc.Params[v].Name
		}
	}
	saved := cc.kc
	for _, name := range names {
		if name != "_" {
			cc.kc = cc.kc.WithLocal(name)
		}
	}
	inner := cc.renderNested(body)
	cc.kc = saved
	head := "{"
	if len(names) > 0 {
		head = "{ " + strings.Join(names, ", ") + " ->"
	}
	return head + "\n" + inner + strings.Repeat("    ", cc.indent) + "}"
}

// entryBinding is the name body inserts entry under, or "" where it never does.
func entryBinding(body []ir.Stmt, entry *ir.SlotDecl) string {
	name := ""
	_ = ir.Walk(body, func(n ir.Node) error {
		if si, ok := n.(*ir.SlotInst); ok && si.Entry == entry {
			name = si.Name
			return ir.SkipAll
		}
		return nil
	})
	return name
}

// renderSlotInst calls the composable an insertion names -- one of the
// component's slots, or an entry a population was handed -- and renders the
// insertion's own block when it is null.
func (cc *irComposeContext) renderSlotInst(s *ir.SlotInst) {
	decl, name := s.Decl, ""
	switch {
	case s.Entry != nil:
		decl, name = s.Entry, s.Name
	case decl != nil:
		name = slotParamName(decl)
	default:
		panic(fmt.Sprintf("android: slot insertion %q names no declaration", s.Name))
	}
	args := func() string {
		out := make([]string, decl.Arity())
		for i := range out {
			entry, v := decl.EntryAt(i)
			switch {
			case entry != nil && s.Slots[entry.Name] != nil:
				sc := s.Slots[entry.Name]
				out[i] = cc.slotLambda(entry, sc, sc.Body)
			case entry != nil:
				out[i] = "null"
			case v < len(s.Args):
				out[i] = cc.kc.EvalExpr(s.Args[v])
			default:
				panic(fmt.Sprintf("android: slot insertion %q passes no argument %d", s.Name, i))
			}
		}
		return strings.Join(out, ", ")
	}
	if len(s.Children) == 0 {
		cc.line("%s?.invoke(%s)", name, args())
		return
	}
	cc.line("if (%s != null) {", name)
	cc.indent++
	cc.line("%s(%s)", name, args())
	cc.indent--
	cc.line("} else {")
	cc.indent++
	for _, child := range s.Children {
		cc.renderStmt(child)
	}
	cc.indent--
	cc.line("}")
}
