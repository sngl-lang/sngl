package bubbletea

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A render method takes each slot as a func rendering what the caller
// supplied, whose arguments are the slot's invocation list, a component entry
// among them being a func of its own. Nil is "the caller supplied nothing",
// which is what renders the insertion's fallback block.

// slotParamName is what a slot is called as a parameter. The rest slot keeps
// the name its bare children have always arrived under.
func slotParamName(s *ir.SlotDecl) string {
	if s.Rest {
		return "slotContent"
	}
	return s.Name
}

func slotFuncType(s *ir.SlotDecl) string {
	params := make([]string, s.Arity())
	for i := range params {
		if entry, v := s.EntryAt(i); entry != nil {
			params[i] = slotFuncType(entry)
		} else {
			params[i] = golang.IRTypeToGo(s.Params[v].Type)
		}
	}
	return "func(" + strings.Join(params, ", ") + ") string"
}

// slotParams declares comp's slots, after its props.
func slotParams(comp *ir.Component) []string {
	var out []string
	for _, s := range comp.Slots {
		out = append(out, slotParamName(s)+" "+slotFuncType(s))
	}
	return out
}

// populationArgs declares a func for each slot n supplies and answers with
// what the render method is passed for every slot it declares.
func (vc *irViewContext) populationArgs(n *ir.NodeInst, resultVar string) []string {
	var out []string
	for _, s := range n.Component.Slots {
		name := resultVar + "_" + s.Name
		switch sc := n.Slots[s.Name]; {
		case sc != nil:
			vc.slotFunc(name, s, sc, sc.Body)
		case s.Rest && len(n.Children) > 0:
			vc.slotFunc(name, s, nil, n.Children)
		default:
			name = "nil"
		}
		out = append(out, name)
	}
	return out
}

// slotFunc declares name as the func a slot of decl's shape is passed as. The
// population's names are its parameters; bare children bind none, which is
// what `_` says for each value they are handed.
func (vc *irViewContext) slotFunc(name string, decl *ir.SlotDecl, sc *ir.SlotContent, body []ir.Stmt) {
	params := make([]string, decl.Arity())
	saved := vc.gc
	for i := range params {
		bound := "_"
		entry, v := decl.EntryAt(i)
		typ := ""
		if entry != nil {
			typ = slotFuncType(entry)
			if b := entryBinding(body, entry); b != "" {
				bound = b
			}
		} else {
			typ = golang.IRTypeToGo(decl.Params[v].Type)
			if sc != nil && v < len(sc.Params) {
				bound = sc.Params[v].Name
			}
		}
		if bound != "_" {
			vc.gc = vc.gc.WithLocal(bound)
		}
		params[i] = bound + " " + typ
	}
	vc.line("%s := func(%s) string {", name, strings.Join(params, ", "))
	vc.indent++
	vc.renderBody(body, "result")
	vc.line("return result")
	vc.indent--
	vc.line("}")
	vc.gc = saved
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

// slotCall is the call rendering an insertion -- of one of the component's
// slots, or of an entry a population was handed -- and the func it calls,
// which is nil when the caller supplied nothing. Funcs for the entries the
// insertion populates are declared first.
func (vc *irViewContext) slotCall(s *ir.SlotInst, resultVar string) (fn, call string) {
	decl := s.Decl
	switch {
	case s.Entry != nil:
		decl, fn = s.Entry, s.Name
	case decl != nil:
		fn = slotParamName(decl)
	default:
		panic(fmt.Sprintf("bubbletea: slot insertion %q names no declaration", s.Name))
	}
	args := make([]string, decl.Arity())
	for i := range args {
		entry, v := decl.EntryAt(i)
		switch {
		case entry != nil && s.Slots[entry.Name] != nil:
			args[i] = resultVar + "_" + entry.Name
			sc := s.Slots[entry.Name]
			vc.slotFunc(args[i], entry, sc, sc.Body)
		case entry != nil:
			args[i] = "nil"
		case v < len(s.Args):
			args[i] = vc.gc.EvalExpr(s.Args[v])
		default:
			panic(fmt.Sprintf("bubbletea: slot insertion %q passes no argument %d", s.Name, i))
		}
	}
	return fn, fn + "(" + strings.Join(args, ", ") + ")"
}

func (vc *irViewContext) renderSlotInst(s *ir.SlotInst, resultVar string) {
	fn, call := vc.slotCall(s, resultVar)
	vc.line("if %s != nil {", fn)
	vc.indent++
	vc.line("%s = %s", resultVar, call)
	vc.indent--
	if len(s.Children) == 0 {
		vc.line("}")
		return
	}
	vc.line("} else {")
	vc.indent++
	fallback := resultVar + "Fallback"
	vc.renderBody(s.Children, fallback)
	vc.line("%s = %s", resultVar, fallback)
	vc.indent--
	vc.line("}")
}
