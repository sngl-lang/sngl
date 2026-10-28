package android

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/ir"
)

// A slot is a nullable composable parameter taking its invocation list; null
// renders the insertion's fallback.

func slotParamName(s *ir.SlotDecl) string {
	if s.Rest {
		return "slotContent"
	}
	return kotlin.SafeIdent(s.Name)
}

// composable is what the file calls the @Composable annotation, and receiver
// the scope the content is composed in, or "".
func slotParamType(s *ir.SlotDecl, composable, receiver string) string {
	params := make([]string, s.Arity())
	for i := range params {
		if entry, v := s.EntryAt(i); entry != nil {
			params[i] = slotParamType(entry, composable, "")
		} else {
			params[i] = kotlin.IRTypeToKt(s.Params[v].Type)
		}
	}
	if receiver != "" {
		receiver += "."
	}
	return "(@" + composable + " " + receiver + "(" + strings.Join(params, ", ") + ") -> Unit)?"
}

func slotParams(comp *ir.Component, composable string, receivers map[*ir.SlotDecl]string) []string {
	var out []string
	for _, s := range comp.Slots {
		out = append(out, slotParamName(s)+": "+slotParamType(s, composable, receivers[s])+" = null")
	}
	return out
}

// layoutScopes is the receiver each Compose layout composes its content with,
// by the native path or intrinsic id the platform declares it under.
var layoutScopes = map[string]string{
	"androidx.compose.foundation.layout.Column": "androidx.compose.foundation.layout.ColumnScope",
	"androidx.compose.foundation.layout.Row":    "androidx.compose.foundation.layout.RowScope",
	"androidx.compose.foundation.layout.Box":    "androidx.compose.foundation.layout.BoxScope",
	intrinsicNS + "SnglScroll":                  "androidx.compose.foundation.layout.ColumnScope",
}

// slotReceivers is the layout scope each of a composable's slots is inserted
// in, where every insertion of it stands directly in one layout's content.
//
// A slot's content is a lambda its caller writes, and what the context
// lowering hands it from a layout the slot stands in is written in that
// layout's content: android's flexShare is `Modifier.weight`, a member of
// ColumnScope and RowScope. Typed with the scope as its receiver, as
// Compose's own layouts type their content, the caller's lambda compiles and
// its weight is a share of the layout it is composed in.
func slotReceivers(body []ir.Stmt) map[*ir.SlotDecl]string {
	out := map[*ir.SlotDecl]string{}
	mixed := map[*ir.SlotDecl]bool{}
	var walk func(stmts []ir.Stmt, scope string)
	walk = func(stmts []ir.Stmt, scope string) {
		for _, s := range stmts {
			if si, ok := s.(*ir.SlotInst); ok && si.Entry == nil && si.Decl != nil {
				if was, seen := out[si.Decl]; seen && was != scope {
					mixed[si.Decl] = true
				}
				out[si.Decl] = scope
			}
			n, isNode := s.(*ir.NodeInst)
			for _, b := range ir.ViewBlocks(s) {
				inner := scope
				if isNode {
					inner = ""
					if b == &n.Children {
						inner = layoutScope(n)
					}
				}
				walk(*b, inner)
			}
		}
	}
	walk(body, "")
	for d := range mixed {
		delete(out, d)
	}
	return out
}

// layoutScope is the receiver n composes its children with, or "" where it is
// no layout Compose scopes.
func layoutScope(n *ir.NodeInst) string {
	comp, _ := composeIntrinsic(n)
	if comp == nil {
		return ""
	}
	if comp.IsNative() {
		return layoutScopes[comp.Foreign.Path]
	}
	return layoutScopes[comp.Intrinsic]
}

// eventParamName must agree with the name KtIRContext.EmitText invokes.
func eventParamName(e string) string { return "on" + strings.ToUpper(e[:1]) + e[1:] }

func eventParams(comp *ir.Component) []string {
	var out []string
	for _, e := range comp.Events {
		types := make([]string, len(e.Params))
		for i, p := range e.Params {
			types[i] = kotlin.IRTypeToKt(p.Type)
		}
		out = append(out, eventParamName(e.Name)+": (("+strings.Join(types, ", ")+") -> Unit)? = null")
	}
	return out
}

func (cc *irComposeContext) handlerArgs(n *ir.NodeInst) []string {
	var out []string
	for _, h := range n.Handlers {
		i := slices.IndexFunc(n.Component.Events, func(e *ir.EventDecl) bool { return e.Name == h.Name })
		if h.Func == nil || i < 0 {
			continue
		}
		out = append(out, cc.lambdaArg(eventParamName(h.Name), &ir.Lambda{Func: h.Func}, len(n.Component.Events[i].Params)))
	}
	return out
}

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
	for i, name := range names {
		if name != "_" {
			cc.kc = cc.kc.WithLocal(name)
			names[i] = kotlin.SafeIdent(name)
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
		decl, name = s.Entry, kotlin.SafeIdent(s.Name)
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
	call := args
	if s.Entry == nil && cc.slotReceivers[decl] != "" {
		call = func() string {
			if a := args(); a != "" {
				return "this, " + a
			}
			return "this"
		}
	}
	if len(s.Children) == 0 {
		cc.line("%s?.invoke(%s)", name, call())
		return
	}
	cc.line("if (%s != null) {", name)
	cc.indent++
	cc.line("%s.invoke(%s)", name, call())
	cc.indent--
	cc.line("} else {")
	cc.indent++
	for _, child := range s.Children {
		cc.renderStmt(child)
	}
	cc.indent--
	cc.line("}")
}
