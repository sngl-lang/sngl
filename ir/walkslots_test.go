package ir

import (
	"reflect"
	"sort"
	"testing"
)

// The Rewrite scaffold panics on an IR node *kind* it does not know, so a new
// Expr or Stmt type cannot be added without noticing. A new Expr-typed *field*
// on an existing node has no such guard: the walk simply never visits it, and
// every pass built on Rewrite silently stops seeing that slot. That is how a
// top-level window's Href came to be documented as folded during optimization
// while nothing folded it.
//
// This test closes that gap. The expected set is derived from the IR types by
// reflection, so adding an Expr-typed field puts it in the set automatically;
// the actual set comes from walking a package that carries a distinct marker
// in every slot. A new field therefore fails here, by name, until it is both
// wired into the traversal and given a marker below.

// exprSlots returns "Type.Field" for every Expr-typed field (including
// []Expr) declared on the IR node types that a package can reach.
func exprSlots() []string {
	types := []reflect.Type{
		reflect.TypeFor[Package](), reflect.TypeFor[Component](),
		reflect.TypeFor[Window](), reflect.TypeFor[Func](),
		reflect.TypeFor[Var](),
		reflect.TypeFor[Output](), reflect.TypeFor[Context](),
		reflect.TypeFor[ContextProvider](), reflect.TypeFor[Prop](),
		reflect.TypeFor[Param](), reflect.TypeFor[StructField](),
		reflect.TypeFor[EnumMember](), reflect.TypeFor[EventHandler](),
		reflect.TypeFor[CallArg](), reflect.TypeFor[FieldInit](),
		reflect.TypeFor[MapEntry](),
		reflect.TypeFor[Literal](), reflect.TypeFor[Ident](),
		reflect.TypeFor[Binary](), reflect.TypeFor[Unary](),
		reflect.TypeFor[Ternary](), reflect.TypeFor[Call](),
		reflect.TypeFor[Conversion](), reflect.TypeFor[Select](),
		reflect.TypeFor[Index](), reflect.TypeFor[StructLit](),
		reflect.TypeFor[ListLit](), reflect.TypeFor[MapLitIR](),
		reflect.TypeFor[Spread](), reflect.TypeFor[Lambda](),
		reflect.TypeFor[Closure](),
		reflect.TypeFor[SlotInst](), reflect.TypeFor[NodeInst](),
	}
	want := reflect.TypeFor[Expr]()
	var out []string
	for _, rt := range types {
		for f := range rt.Fields() {
			ft := f.Type
			if ft.Kind() == reflect.Slice {
				ft = ft.Elem()
			}
			if ft == want {
				out = append(out, rt.Name()+"."+f.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// mark returns a Literal that names the slot it was placed in, so a walk can
// report which slots it reached.
func mark(slot string) Expr { return &Literal{Type: TypString, Value: slot} }

// markedPackage puts a distinct marker in every Expr slot exprSlots reports.
// Keep it in step with that list: an unpopulated slot shows up as a failure
// here just as an unvisited one does, which is the point.
func markedPackage() *Package {
	fn := func(slot string) *Func {
		return &Func{Block: []Stmt{&Return{Value: mark(slot)}}}
	}
	// Every Expr node kind, each carrying markers in its own Expr slots.
	exprs := []Expr{
		&Binary{Left: mark("Binary.Left"), Right: mark("Binary.Right")},
		&Unary{Operand: mark("Unary.Operand")},
		&Ternary{Cond: mark("Ternary.Cond"), Then: mark("Ternary.Then"), Else: mark("Ternary.Else")},
		&Call{
			Receiver:     mark("Call.Receiver"),
			Callee:       mark("Call.Callee"),
			Args:         []CallArg{{Value: mark("CallArg.Value")}},
			ErrorHandler: &EventHandler{Func: fn("EventHandler.Func")},
		},
		&Conversion{Operand: mark("Conversion.Operand")},
		&Select{Operand: mark("Select.Operand")},
		&Index{Operand: mark("Index.Operand"), Idx: mark("Index.Idx")},
		&StructLit{Fields: []FieldInit{{Name: "f", Value: mark("FieldInit.Value")}}},
		&ListLit{Elems: []Expr{mark("ListLit.Elems")}},
		&MapLitIR{Entries: []MapEntry{{Key: mark("MapEntry.Key"), Value: mark("MapEntry.Value")}}},
		&Spread{Operand: mark("Spread.Operand")},
		&Lambda{Func: fn("Lambda.Func")},
		&Closure{
			State: &StructLit{Fields: []FieldInit{{Name: "c", Value: mark("Closure.State")}}},
		},
	}
	body := make([]Stmt, 0, len(exprs))
	for _, e := range exprs {
		body = append(body, &Return{Value: e})
	}

	return &Package{
		Consts: []*Var{{
			Name:     "c",
			Init:     mark("Var.Init"),
			Handlers: []*EventHandler{{Func: fn("Var.Handlers")}},
		}},
		Structs: []*StructDef{{
			Fields: []*StructField{{Name: "f", Default: mark("StructField.Default")}},
		}},
		Enums: []*EnumDef{{
			Members: []*EnumMember{{Name: "m", Value: mark("EnumMember.Value")}},
		}},
		Contexts: []*Context{{Name: "ctx", Default: mark("Context.Default")}},
		Outputs: []*Output{{
			Options: &StructLit{Fields: []FieldInit{{Name: "o", Value: mark("Output.Options")}}},
		}},
		Funcs: []*Func{{
			Params: []*Param{{Name: "p", Default: mark("Param.Default")}},
			Block:  body,
		}},
		Components: []*Component{{
			Props: []*Prop{{Name: "p", Default: mark("Prop.Default")}},
			Body: []Stmt{
				&ContextProvider{
					Value:    mark("ContextProvider.Value"),
					Children: []Stmt{},
				},
				&NodeInst{
					Key: mark("NodeInst.Key"),
					Ref: mark("NodeInst.Ref"),
					Slots: map[string]*SlotContent{
						"s": {Body: []Stmt{&Return{Value: mark("SlotContent.Body")}}},
					},
				},
				&SlotInst{Args: []Expr{mark("SlotInst.Args")}},
			},
		}},
		Windows: []*Window{{
			Props: []Arg{
				{Name: WindowHref, Value: mark("Window.Props[0]")},
				{Name: WindowTitle, Value: mark("Window.Props[1]")},
			},
			ErrorHandler: &EventHandler{Func: fn("Window.ErrorHandler")},
		}},
	}
}

func TestRewriteVisitsEveryExprSlot(t *testing.T) {
	pkg := markedPackage()
	seen := map[string]bool{}
	if err := Walk(pkg, func(n Node) error {
		if lit, ok := n.(*Literal); ok && lit.Value != "" {
			seen[lit.Value] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}

	// Slots whose marker is placed indirectly: the fixture reaches them
	// through an owned Func or StructLit rather than by holding an Expr.
	indirect := map[string]bool{
		"Lambda.Func":       true,
		"EventHandler.Func": true, "Var.Handlers": true,
		"Window.ErrorHandler": true, "Output.Options": true,
	}

	for _, slot := range exprSlots() {
		if !seen[slot] {
			t.Errorf("ir.Rewrite never visited %s\n"+
				"\tEvery Expr-typed field must be reached by the walk, or named as a\n"+
				"\tdeliberate reference in the opt-out list on Rewrite's doc comment.\n"+
				"\tIf this is a new field: wire it into walkexprs.go and add a marker\n"+
				"\tto markedPackage in this file.", slot)
		}
	}
	for slot := range indirect {
		if !seen[slot] {
			t.Errorf("ir.Rewrite never reached the body owned by %s", slot)
		}
	}
}
