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
	if lit.Raw != "false" {
		t.Errorf("data-flag zero-value: got %q, want \"false\"", lit.Raw)
	}
}
