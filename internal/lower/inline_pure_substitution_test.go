package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// A pure wrapper whose body declares events as metadata (e.g. the bubbletea
// Styled primitive's `events=[...]` prop) rather than emitting them via
// @name() must still carry the user's call-site handler onto the inlined root
// node — otherwise the handler is silently dropped and platform codegen can't
// wire activation.
func TestInlinePure_TransfersUnemittedHandler(t *testing.T) {
	// component btn(text string, @click) { Styled(content=text) }
	//   — body never invokes @click; it declares activation as metadata.
	textParam := &ir.Param{Name: "text", Type: ir.TypString}
	btn := &ir.Component{
		Name:  "btn",
		Props: []*ir.Prop{{Name: "text", Type: ir.TypString}},
		Body: []ir.Stmt{
			&ir.NodeInst{
				Name: "Styled",
				Props: []ir.Arg{{
					Name:  "content",
					Value: &ir.Ident{Name: "text", Sym: textParam, Type: ir.TypString},
				}},
			},
		},
	}
	// component main { btn(text="OK", @click { ... }) }
	clickHandler := ir.EventHandler{
		Name: "click",
		Func: &ir.Func{Block: []ir.Stmt{&ir.Return{}}},
	}
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{
			Name:      "btn",
			Component: btn,
			Handlers:  []ir.EventHandler{clickHandler},
		}},
	}
	pkg := &ir.Package{Components: []*ir.Component{btn, main}}

	if err := lowerInlinePure(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}

	styled, ok := main.Body[0].(*ir.NodeInst)
	if !ok || styled.Name != "Styled" {
		t.Fatalf("expected inlined Styled at main.Body[0], got %T", main.Body[0])
	}
	var found bool
	for _, h := range styled.Handlers {
		if h.Name == "click" {
			found = true
		}
	}
	if !found {
		t.Fatalf("user @click handler not transferred onto inlined Styled node; handlers=%+v", styled.Handlers)
	}
}

// A forwarding wrapper that DOES emit @click() in its body AND receives a
// call-site @click handler must produce EXACTLY ONE copy of the handler body
// on the inlined node — substituteEvents inlines it where the @click() emit
// sat, and the unemitted-handler transfer must NOT also append it (no
// double-transfer). emittedHandlerNames sees the body's @click() emit and
// skips "click" in the transfer step.
func TestInlinePure_EmittedHandlerNotDoubleTransferred(t *testing.T) {
	// component Button(@click) { button { @click() } }
	//   — body forwards the event via an explicit @click() emit.
	btn := &ir.Component{
		Name: "Button",
		Body: []ir.Stmt{
			&ir.NodeInst{
				Name: "button",
				Children: []ir.Stmt{
					&ir.Emit{Name: "click"},
				},
			},
		},
	}
	// The user's @click handler body carries a unique marker assignment so we
	// can count how many times the body was inlined.
	const marker = "__handlerMarker_a"
	clickHandler := ir.EventHandler{
		Name: "click",
		Func: &ir.Func{Block: []ir.Stmt{
			&ir.Assign{Target: &ir.Ident{Name: marker}, Value: &ir.Literal{Value: "1"}},
		}},
	}
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{
			Name:      "Button",
			Component: btn,
			Handlers:  []ir.EventHandler{clickHandler},
		}},
	}
	pkg := &ir.Package{Components: []*ir.Component{btn, main}}

	if err := lowerInlinePure(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}

	if got := countMarkerAssigns(main.Body, marker); got != 1 {
		t.Fatalf("handler body inlined %d times, want exactly 1 (no double-transfer)", got)
	}
}

// A non-bubbletea path: a plain pure user wrapper whose body declares no
// @name() emit and which receives an unemitted call-site handler must wire
// that handler onto the first inlined node exactly once. This proves the
// handler-transfer rule is cross-cutting (not bubbletea-specific).
func TestInlinePure_NonBubbleteaUnemittedHandlerTransferred(t *testing.T) {
	// component card { div { } }   — ordinary pure wrapper, no event metadata,
	// no @name() emit.
	card := &ir.Component{
		Name: "card",
		Body: []ir.Stmt{&ir.NodeInst{Name: "div"}},
	}
	// component main { card(@click { ... }) }
	clickHandler := ir.EventHandler{
		Name: "click",
		Func: &ir.Func{Block: []ir.Stmt{&ir.Return{}}},
	}
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{
			Name:      "card",
			Component: card,
			Handlers:  []ir.EventHandler{clickHandler},
		}},
	}
	pkg := &ir.Package{Components: []*ir.Component{card, main}}

	if err := lowerInlinePure(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}

	div, ok := main.Body[0].(*ir.NodeInst)
	if !ok || div.Name != "div" {
		t.Fatalf("expected inlined div at main.Body[0], got %T", main.Body[0])
	}
	var count int
	for _, h := range div.Handlers {
		if h.Name == "click" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("@click handler wired %d times onto first inlined node, want exactly 1", count)
	}
}

// countMarkerAssigns counts *ir.Assign statements whose target is an *ir.Ident
// named marker, recursing through node children and handler bodies.
func countMarkerAssigns(stmts []ir.Stmt, marker string) int {
	n := 0
	for _, s := range stmts {
		switch t := s.(type) {
		case *ir.Assign:
			if id, ok := t.Target.(*ir.Ident); ok && id.Name == marker {
				n++
			}
		case *ir.NodeInst:
			n += countMarkerAssigns(t.Children, marker)
			for _, h := range t.Handlers {
				if h.Func != nil {
					n += countMarkerAssigns(h.Func.Block, marker)
				}
			}
		}
	}
	return n
}

// A pure component whose body references a prop the call site omits must
// have that reference substituted with the prop's zero-value, not left as a
// bare param identifier (which leaks to codegen as e.g. disabled="disabled").
func TestInlinePure_OmittedParamGetsZeroValue(t *testing.T) {
	// component wrap(flag bool) { div(data-flag=flag) }   — flag has no default
	flagParam := &ir.Param{Name: "flag", Type: ir.TypBool}
	body := []ir.Stmt{
		&ir.NodeInst{
			Name: "div",
			Props: []ir.Arg{{
				Name:  "data-flag",
				Value: &ir.Ident{Name: "flag", Sym: flagParam, Type: ir.TypBool},
			}},
		},
	}
	wrap := &ir.Component{
		Name:  "wrap",
		Props: []*ir.Prop{{Name: "flag", Type: ir.TypBool}}, // no Default
		Body:  body,
	}
	// component main { wrap() }   — no flag arg passed
	main := &ir.Component{
		Name: "main",
		Body: []ir.Stmt{&ir.NodeInst{Name: "wrap", Component: wrap}},
	}
	pkg := &ir.Package{Components: []*ir.Component{wrap, main}}

	if err := lowerInlinePure(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("lowerInlinePure: %v", err)
	}

	// main.Body[0] is now the inlined <div>; its data-flag prop must be a
	// literal false, not the bare `flag` identifier.
	div, ok := main.Body[0].(*ir.NodeInst)
	if !ok || div.Name != "div" {
		t.Fatalf("expected inlined div at main.Body[0], got %T", main.Body[0])
	}
	var val ir.Expr
	for _, p := range div.Props {
		if p.Name == "data-flag" {
			val = p.Value
		}
	}
	if _, isIdent := val.(*ir.Ident); isIdent {
		t.Fatal("omitted param left as bare identifier — not substituted")
	}
	lit, ok := val.(*ir.Literal)
	if !ok {
		t.Fatalf("data-flag value: got %T, want *ir.Literal", val)
	}
	if lit.Value != "false" {
		t.Errorf("data-flag zero-value: got %q, want \"false\"", lit.Value)
	}
}
