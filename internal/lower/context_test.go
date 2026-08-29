package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// --- test helpers ---

// makeStringLit returns a string literal expression.
func makeStringLit(s string) *ir.Literal {
	return &ir.Literal{Type: ir.TypString, Value: `"` + s + `"`}
}

// makeContext creates a context declaration with a string default.
func makeContext(name, defaultVal string) *ir.Context {
	return &ir.Context{
		Name:    name,
		Typ:     ir.TypString,
		Default: makeStringLit(defaultVal),
	}
}

// makeContextRead creates a ContextRead expression for a given context.
func makeContextRead(ctx *ir.Context) *ir.ContextRead {
	return &ir.ContextRead{Ref: ctx, Typ: ctx.Typ}
}

// makeComp creates a minimal component with the given name and body.
func makeComp(name string, body ...ir.Stmt) *ir.Component {
	return &ir.Component{Name: name, Body: body}
}

// makeNodeInstComp creates a NodeInst for a user component call with given props.
func makeNodeInstComp(comp *ir.Component, props ...ir.Arg) *ir.NodeInst {
	return &ir.NodeInst{Name: comp.Name, Component: comp, Props: props}
}

// findHiddenVar returns the hidden context Var with the given name, or nil.
// After NoContext, components store __ctx_<name> as a synthesized *ir.Var on
// comp.Vars (initialized to ctx.Default), not as a *ir.Prop on comp.Props.
func findHiddenVar(comp *ir.Component, name string) *ir.Var {
	for _, v := range comp.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// countContextReads counts ContextRead nodes in a stmt slice (non-recursive).
func countContextReads(stmts []ir.Stmt, ctx *ir.Context) int {
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		return e // no-op; just need to count
	})
	_ = w
	// Use a simple recursive walker.
	var count int
	var walkE func(ir.Expr)
	walkE = func(e ir.Expr) {
		if e == nil {
			return
		}
		if cr, ok := e.(*ir.ContextRead); ok {
			if cr.Ref == ctx {
				count++
			}
			return
		}
		switch n := e.(type) {
		case *ir.Binary:
			walkE(n.Left)
			walkE(n.Right)
		case *ir.Unary:
			walkE(n.Operand)
		case *ir.Ternary:
			walkE(n.Cond)
			walkE(n.Then)
			walkE(n.Else)
		case *ir.Select:
			walkE(n.Operand)
		case *ir.Index:
			walkE(n.Operand)
			walkE(n.Idx)
		case *ir.Call:
			walkE(n.Receiver)
			walkE(n.Callee)
			for _, a := range n.Args {
				walkE(a.Value)
			}
		}
	}
	var walkS func([]ir.Stmt)
	walkS = func(ss []ir.Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ir.NodeInst:
				for _, p := range n.Props {
					walkE(p.Value)
				}
				walkS(n.Children)
			case *ir.If:
				walkE(n.Cond)
				walkS(n.Body)
				walkS(n.Else)
			case *ir.For:
				walkE(n.Iter)
				walkS(n.Body)
				walkS(n.Else)
			case *ir.ContextProvider:
				walkE(n.Value)
				walkS(n.Children)
			case *ir.Assign:
				walkE(n.Target)
				walkE(n.Value)
			case *ir.LocalVar:
				walkE(n.Init)
			case *ir.Return:
				walkE(n.Value)
			}
		}
	}
	walkS(stmts)
	return count
}

// countContextProviders counts ContextProvider nodes in a stmt slice (recursive).
func countContextProviders(stmts []ir.Stmt, ctx *ir.Context) int {
	var count int
	var walk func([]ir.Stmt)
	walk = func(ss []ir.Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ir.ContextProvider:
				if n.Ref == ctx {
					count++
				}
				walk(n.Children)
			case *ir.NodeInst:
				walk(n.Children)
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			}
		}
	}
	walk(stmts)
	return count
}

// findArgInNodeInst returns the value of a named arg in a NodeInst's Props, or nil.
func findArgInNodeInst(n *ir.NodeInst, name string) ir.Expr {
	for _, a := range n.Props {
		if a.Name == name {
			return a.Value
		}
	}
	return nil
}

// identifiesHiddenParam returns true if e is an *ir.Ident naming the hidden param for ctx.
func identifiesHiddenParam(e ir.Expr, ctx *ir.Context) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	return id.Name == "__ctx_"+ctx.Name
}

// --- tests ---

func TestApplyNoContext_EmptyContexts(t *testing.T) {
	pkg := &ir.Package{}
	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext on pkg with no contexts: %v", err)
	}
}

func TestApplyNoContext_NilPkg(t *testing.T) {
	if err := applyNoContext(nil, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext(nil): %v", err)
	}
}

// TestComputeReachability_DirectReader checks that a component with a
// ContextRead in its body is marked as a direct reader.
func TestComputeReachability_DirectReader(t *testing.T) {
	ctx := makeContext("theme", "light")
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer},
	}

	reach := computeReachability(pkg, nil)
	if !reach.Components[ctx][consumer] {
		t.Errorf("Consumer should be in Reach(theme): has direct ContextRead")
	}
}

// TestComputeReachability_TransitiveReader checks that a caller of a direct
// reader also lands in Reach.
func TestComputeReachability_TransitiveReader(t *testing.T) {
	ctx := makeContext("theme", "light")
	inner := makeComp("Inner",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	outer := makeComp("Outer", makeNodeInstComp(inner))
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{inner, outer},
	}

	reach := computeReachability(pkg, nil)
	if !reach.Components[ctx][inner] {
		t.Errorf("Inner should be in Reach(theme)")
	}
	if !reach.Components[ctx][outer] {
		t.Errorf("Outer should be transitively in Reach(theme)")
	}
}

// TestComputeReachability_ShadowingPreventsTransitive checks that a provider
// block shadows the context for transitive propagation — if the callee is
// always behind a provider in the caller, the caller does not need the param.
func TestComputeReachability_ShadowingPreventsTransitive(t *testing.T) {
	ctx := makeContext("theme", "light")
	reader := makeComp("Reader",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	// Caller wraps the Reader call inside a ContextProvider — shadowing.
	caller := makeComp("Caller",
		&ir.ContextProvider{
			Ref:      ctx,
			Value:    makeStringLit("override"),
			Children: []ir.Stmt{makeNodeInstComp(reader)},
		},
	)
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{reader, caller},
	}

	reach := computeReachability(pkg, nil)
	if !reach.Components[ctx][reader] {
		t.Errorf("Reader should be in Reach(theme)")
	}
	if reach.Components[ctx][caller] {
		t.Errorf("Caller should NOT be in Reach(theme): all calls to Reader are shadowed by a provider")
	}
}

// TestAddHiddenParams checks that Reach components gain a __ctx_<name> Prop.
func TestAddHiddenParams(t *testing.T) {
	ctx := makeContext("theme", "light")
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	nonReader := makeComp("NonReader")
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer, nonReader},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)

	varName := "__ctx_theme"
	if v := findHiddenVar(consumer, varName); v == nil {
		t.Errorf("Consumer should have var %q after addHiddenParams", varName)
	} else if v.Type != ctx.Typ {
		t.Errorf("hidden var type = %v; want %v", v.Type, ctx.Typ)
	} else if v.Init != ctx.Default {
		t.Errorf("hidden var Init = %v; want ctx.Default", v.Init)
	}
	if v := findHiddenVar(nonReader, varName); v != nil {
		t.Errorf("NonReader should NOT have var %q", varName)
	}
}

// TestAddHiddenParams_Idempotent checks that calling addHiddenParams twice
// does not add duplicate props.
func TestAddHiddenParams_Idempotent(t *testing.T) {
	ctx := makeContext("theme", "light")
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer},
	}
	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	addHiddenParams(pkg, reach, nil)

	count := 0
	for _, v := range consumer.Vars {
		if v.Name == "__ctx_theme" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 __ctx_theme var; got %d", count)
	}
}

// TestRewriteReads checks that ContextRead nodes are replaced by Ident refs.
func TestRewriteReads(t *testing.T) {
	ctx := makeContext("theme", "light")
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	hidden := buildHiddenParamIndex(pkg, reach, nil)
	rewriteReads(pkg, reach, nil, hidden)

	// No ContextRead should remain.
	if n := countContextReads(consumer.Body, ctx); n != 0 {
		t.Errorf("after rewriteReads: still %d ContextRead nodes in Consumer.Body", n)
	}

	// The prop value should now be an Ident naming the hidden param.
	ni := consumer.Body[0].(*ir.NodeInst)
	if !identifiesHiddenParam(ni.Props[0].Value, ctx) {
		t.Errorf("after rewriteReads: prop value = %T %v; want Ident(__ctx_theme)", ni.Props[0].Value, ni.Props[0].Value)
	}
}

// TestLowerProviders_Basic checks that ContextProvider is removed and the
// enclosed component call gets the context arg threaded in.
func TestLowerProviders_Basic(t *testing.T) {
	ctx := makeContext("theme", "light")
	inner := makeComp("Inner",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:      ctx,
				Value:    makeStringLit("dark"),
				Children: []ir.Stmt{makeNodeInstComp(inner)},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{inner},
		Windows:    []*ir.Window{win},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	lowerProviders(pkg, reach, nil, hiddenParamIndex{funcs: map[*ir.Func]map[*ir.Context]*ir.Param{}})

	// Provider should be gone from window body.
	if n := countContextProviders(win.Body, ctx); n != 0 {
		t.Errorf("after lowerProviders: %d ContextProvider nodes remain in window body", n)
	}

	// The NodeInst for Inner should have a __ctx_theme arg.
	if len(win.Body) == 0 {
		t.Fatal("window body is empty after lowerProviders")
	}
	ni, ok := win.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.NodeInst", win.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_theme")
	if val == nil {
		t.Error("NodeInst after lowerProviders missing __ctx_theme arg")
	} else {
		lit, ok := val.(*ir.Literal)
		if !ok || lit.Value != `"dark"` {
			t.Errorf("__ctx_theme arg = %v; want Literal(\"dark\")", val)
		}
	}
}

// TestLowerProviders_RootDefault checks that component calls at window root
// get the context's default value.
func TestLowerProviders_RootDefault(t *testing.T) {
	ctx := makeContext("theme", "light")
	inner := makeComp("Inner",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{makeNodeInstComp(inner)},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{inner},
		Windows:    []*ir.Window{win},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	lowerProviders(pkg, reach, nil, hiddenParamIndex{funcs: map[*ir.Func]map[*ir.Context]*ir.Param{}})

	ni, ok := win.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.NodeInst", win.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_theme")
	if val == nil {
		t.Error("NodeInst at window root missing __ctx_theme arg (should get default)")
	} else {
		lit, ok := val.(*ir.Literal)
		if !ok || lit.Value != `"light"` {
			t.Errorf("root default arg = %v; want Literal(\"light\")", val)
		}
	}
}

// TestLowerProviders_NestedShadowing checks that nested providers supply the
// correct value to each call site.
func TestLowerProviders_NestedShadowing(t *testing.T) {
	ctx := makeContext("theme", "outer-default")
	show := makeComp("Show",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)

	// Window body:
	//   theme("outer-val") {
	//     theme("inner-val") { Show() }   ← should get "inner-val"
	//     Show()                          ← should get "outer-val"
	//   }
	//   Show()                            ← should get default "outer-default"
	innerShow := makeNodeInstComp(show)
	outerShow := makeNodeInstComp(show)
	rootShow := makeNodeInstComp(show)

	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:   ctx,
				Value: makeStringLit("outer-val"),
				Children: []ir.Stmt{
					&ir.ContextProvider{
						Ref:      ctx,
						Value:    makeStringLit("inner-val"),
						Children: []ir.Stmt{innerShow},
					},
					outerShow,
				},
			},
			rootShow,
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{show},
		Windows:    []*ir.Window{win},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	lowerProviders(pkg, reach, nil, hiddenParamIndex{funcs: map[*ir.Func]map[*ir.Context]*ir.Param{}})

	// No providers should remain.
	if n := countContextProviders(win.Body, ctx); n != 0 {
		t.Errorf("after lowerProviders: %d ContextProvider nodes remain", n)
	}

	// Helper to extract __ctx_theme literal from a NodeInst in the flattened body.
	argLit := func(n *ir.NodeInst) string {
		v := findArgInNodeInst(n, "__ctx_theme")
		if v == nil {
			return "<nil>"
		}
		if l, ok := v.(*ir.Literal); ok {
			return l.Value
		}
		return "<non-literal>"
	}

	// After lowerProviders, win.Body should be: [innerShow, outerShow, rootShow]
	// (providers unwrapped, children spliced inline at each level)
	if len(win.Body) != 3 {
		t.Fatalf("win.Body length = %d; want 3 (innerShow, outerShow, rootShow)", len(win.Body))
	}

	innerNI, ok0 := win.Body[0].(*ir.NodeInst)
	outerNI, ok1 := win.Body[1].(*ir.NodeInst)
	rootNI, ok2 := win.Body[2].(*ir.NodeInst)
	if !ok0 || !ok1 || !ok2 {
		t.Fatalf("expected all three body stmts to be *ir.NodeInst, got %T, %T, %T",
			win.Body[0], win.Body[1], win.Body[2])
	}

	if got := argLit(innerNI); got != `"inner-val"` {
		t.Errorf("innerShow __ctx_theme = %s; want \"inner-val\"", got)
	}
	if got := argLit(outerNI); got != `"outer-val"` {
		t.Errorf("outerShow __ctx_theme = %s; want \"outer-val\"", got)
	}
	if got := argLit(rootNI); got != `"outer-default"` {
		t.Errorf("rootShow __ctx_theme = %s; want \"outer-default\"", got)
	}
}

// TestApplyNoContext_ClearsContexts checks that pkg.Contexts is nil after pass.
func TestApplyNoContext_ClearsContexts(t *testing.T) {
	ctx := makeContext("theme", "light")
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:      ctx,
				Value:    makeStringLit("dark"),
				Children: []ir.Stmt{makeNodeInstComp(consumer)},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer},
		Windows:    []*ir.Window{win},
	}

	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}
	if pkg.Contexts != nil {
		t.Errorf("pkg.Contexts should be nil after pass; got %v", pkg.Contexts)
	}
}

// TestApplyNoContext_FullPipeline exercises the complete pass end-to-end using
// the consumer_basic scenario: context declared, inner component reads it,
// window provides a value.
func TestApplyNoContext_FullPipeline(t *testing.T) {
	ctx := makeContext("theme", "light")
	toolbar := makeComp("Toolbar",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:      ctx,
				Value:    makeStringLit("dark"),
				Children: []ir.Stmt{makeNodeInstComp(toolbar)},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{toolbar},
		Windows:    []*ir.Window{win},
	}

	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}

	// Contexts cleared.
	if pkg.Contexts != nil {
		t.Error("pkg.Contexts not cleared")
	}

	// Toolbar has hidden var.
	if v := findHiddenVar(toolbar, "__ctx_theme"); v == nil {
		t.Error("Toolbar missing __ctx_theme var")
	}

	// No ContextRead remains in Toolbar.
	if n := countContextReads(toolbar.Body, ctx); n != 0 {
		t.Errorf("Toolbar.Body still has %d ContextRead nodes", n)
	}

	// No ContextProvider remains in window.
	if n := countContextProviders(win.Body, ctx); n != 0 {
		t.Errorf("window still has %d ContextProvider nodes", n)
	}

	// The Toolbar NodeInst in window has __ctx_theme = "dark".
	if len(win.Body) == 0 {
		t.Fatal("window body empty after pass")
	}
	ni, ok := win.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.NodeInst", win.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_theme")
	if val == nil {
		t.Error("Toolbar NodeInst missing __ctx_theme arg")
	} else if lit, ok := val.(*ir.Literal); !ok || lit.Value != `"dark"` {
		t.Errorf("__ctx_theme = %v; want Literal(\"dark\")", val)
	}
}

// TestLowerProviders_InIfAndFor checks that lowerInStmts recurses into If/For.
func TestLowerProviders_InIfAndFor(t *testing.T) {
	ctx := makeContext("theme", "light")
	inner := makeComp("Inner",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: makeContextRead(ctx)}},
		},
	)
	cond := &ir.Literal{Type: ir.TypBool, Value: "true"}
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:   ctx,
				Value: makeStringLit("themed"),
				Children: []ir.Stmt{
					&ir.If{
						Cond: cond,
						Body: []ir.Stmt{makeNodeInstComp(inner)},
					},
				},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{inner},
		Windows:    []*ir.Window{win},
	}

	reach := computeReachability(pkg, nil)
	addHiddenParams(pkg, reach, nil)
	lowerProviders(pkg, reach, nil, hiddenParamIndex{funcs: map[*ir.Func]map[*ir.Context]*ir.Param{}})

	// Provider gone.
	if n := countContextProviders(win.Body, ctx); n != 0 {
		t.Errorf("ContextProvider still present: %d", n)
	}

	// Should be one If stmt in win.Body.
	if len(win.Body) != 1 {
		t.Fatalf("win.Body len = %d; want 1", len(win.Body))
	}
	ifStmt, ok := win.Body[0].(*ir.If)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.If", win.Body[0])
	}
	if len(ifStmt.Body) != 1 {
		t.Fatalf("if.Body len = %d; want 1", len(ifStmt.Body))
	}
	ni, ok := ifStmt.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("if.Body[0] = %T; want *ir.NodeInst", ifStmt.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_theme")
	if val == nil {
		t.Error("NodeInst inside if.Body missing __ctx_theme arg")
	} else if lit, ok := val.(*ir.Literal); !ok || lit.Value != `"themed"` {
		t.Errorf("__ctx_theme = %v; want Literal(\"themed\")", val)
	}
}

// TestMultipleContexts checks that two independent contexts are lowered
// independently without interference.
func TestMultipleContexts(t *testing.T) {
	ctxA := makeContext("theme", "light")
	ctxB := makeContext("locale", "en")

	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name: "text",
			Props: []ir.Arg{
				{Name: "value", Value: makeContextRead(ctxA)},
				{Name: "lang", Value: makeContextRead(ctxB)},
			},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:   ctxA,
				Value: makeStringLit("dark"),
				Children: []ir.Stmt{
					&ir.ContextProvider{
						Ref:      ctxB,
						Value:    makeStringLit("fr"),
						Children: []ir.Stmt{makeNodeInstComp(consumer)},
					},
				},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctxA, ctxB},
		Components: []*ir.Component{consumer},
		Windows:    []*ir.Window{win},
	}

	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}

	if pkg.Contexts != nil {
		t.Error("pkg.Contexts not cleared")
	}
	if v := findHiddenVar(consumer, "__ctx_theme"); v == nil {
		t.Error("Consumer missing __ctx_theme var")
	}
	if v := findHiddenVar(consumer, "__ctx_locale"); v == nil {
		t.Error("Consumer missing __ctx_locale var")
	}
	if n := countContextReads(consumer.Body, ctxA); n != 0 {
		t.Errorf("Consumer still has %d ContextRead(theme)", n)
	}
	if n := countContextReads(consumer.Body, ctxB); n != 0 {
		t.Errorf("Consumer still has %d ContextRead(locale)", n)
	}
}

// --- func-level reach + threading tests ---

// makeFunc builds a SNGL func with the given name + body, optionally with
// a list of params.
func makeFunc(name string, params []*ir.Param, body ...ir.Stmt) *ir.Func {
	return &ir.Func{Name: name, Params: params, Block: body}
}

// findFuncParam returns the param with the given name, or nil.
func findFuncParam(fn *ir.Func, name string) *ir.Param {
	for _, p := range fn.Params {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// findCallArgNamed returns the value of a named arg in a Call's Args, or nil.
func findCallArgNamed(c *ir.Call, name string) ir.Expr {
	for _, a := range c.Args {
		if a.Name == name {
			return a.Value
		}
	}
	return nil
}

// TestReachabilityFuncDirectRead verifies a func whose body reads a context
// is in Reach.Funcs.
func TestReachabilityFuncDirectRead(t *testing.T) {
	ctx := makeContext("theme", "light")
	fn := makeFunc("readTheme", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{fn},
	}
	reach := computeReachability(pkg, nil)
	if !reach.Funcs[ctx][fn] {
		t.Errorf("fn readTheme should be in Reach.Funcs(theme): has direct ContextRead in body")
	}
}

// TestReachabilityFuncTransitive verifies transitive call edges
// Top -> Mid -> Leaf where Leaf reads the context — all three end up in Reach.
func TestReachabilityFuncTransitive(t *testing.T) {
	ctx := makeContext("theme", "light")
	leaf := makeFunc("Leaf", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	mid := makeFunc("Mid", nil,
		&ir.Return{Value: &ir.Call{Func: leaf}},
	)
	top := makeFunc("Top", nil,
		&ir.Return{Value: &ir.Call{Func: mid}},
	)
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{leaf, mid, top},
	}
	reach := computeReachability(pkg, nil)
	if !reach.Funcs[ctx][leaf] {
		t.Errorf("Leaf should be in Reach.Funcs(theme)")
	}
	if !reach.Funcs[ctx][mid] {
		t.Errorf("Mid should be in Reach.Funcs(theme) transitively")
	}
	if !reach.Funcs[ctx][top] {
		t.Errorf("Top should be in Reach.Funcs(theme) transitively")
	}
}

// TestFuncHiddenParam verifies that addHiddenParams puts a synthesized
// __ctx_<name> Var on pkg.Vars when a user pkg.Func reads the ctx. The
// func itself does NOT receive a hidden Param — only stdlib wrappers
// (extraFuncs) do, since user funcs read through the pkg-level state.
func TestFuncHiddenParam(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeFunc("tr", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{fn},
	}
	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}
	if findFuncParam(fn, "__ctx_locale") != nil {
		t.Errorf("user pkg.Func tr should NOT have __ctx_locale Param (state lives on pkg.Vars)")
	}
	var pkgVar *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "__ctx_locale" && v.Synthesized {
			pkgVar = v
			break
		}
	}
	if pkgVar == nil {
		t.Fatalf("expected synthesized __ctx_locale Var on pkg.Vars after applyNoContext; got %+v", pkg.Vars)
	}
	if pkgVar.Type != ctx.Typ {
		t.Errorf("__ctx_locale Var type = %v; want %v", pkgVar.Type, ctx.Typ)
	}
	if pkgVar.Init != ctx.Default {
		t.Errorf("__ctx_locale Var Init = %v; want ctx.Default", pkgVar.Init)
	}
}

// TestFuncCallSiteThreaded verifies that a call site to a Reach'd func
// inside a component body gets __ctx_<name>=<active> threaded.
func TestFuncCallSiteThreaded(t *testing.T) {
	ctx := makeContext("locale", "en")
	tr := makeFunc("tr", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	// A component whose body has a NodeInst with a prop value = tr() call.
	trCall := &ir.Call{Func: tr}
	consumer := makeComp("Consumer",
		&ir.NodeInst{
			Name:  "text",
			Props: []ir.Arg{{Name: "value", Value: trCall}},
		},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:      ctx,
				Value:    makeStringLit("fr"),
				Children: []ir.Stmt{makeNodeInstComp(consumer)},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{consumer},
		Funcs:      []*ir.Func{tr},
		Windows:    []*ir.Window{win},
	}
	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}
	// User pkg.Func tr has no hidden Param under the new model, so call
	// sites do NOT thread a __ctx_locale arg. The callee reads state
	// directly from the pkg-level synth Var.
	if findCallArgNamed(trCall, "__ctx_locale") != nil {
		t.Errorf("tr() call should NOT have __ctx_locale arg threaded to a user pkg.Func; args=%+v", trCall.Args)
	}
	if findFuncParam(tr, "__ctx_locale") != nil {
		t.Errorf("tr() should NOT have __ctx_locale Param under the new model")
	}
}

// TestFuncCallSiteThreadedFromFunc verifies threading across a func->func
// call boundary: caller has the hidden param, calls a Reach'd callee, and
// passes its own hidden param value through.
func TestFuncCallSiteThreadedFromFunc(t *testing.T) {
	ctx := makeContext("locale", "en")
	leaf := makeFunc("leaf", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	leafCall := &ir.Call{Func: leaf}
	caller := makeFunc("caller", nil,
		&ir.Return{Value: leafCall},
	)
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{leaf, caller},
	}
	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}
	if findFuncParam(caller, "__ctx_locale") != nil {
		t.Errorf("caller (user pkg.Func) should NOT have __ctx_locale Param under the new model")
	}
	if findCallArgNamed(leafCall, "__ctx_locale") != nil {
		t.Errorf("leaf() call inside caller should NOT have __ctx_locale arg threaded (callee is user pkg.Func)")
	}
	if findFuncParam(leaf, "__ctx_locale") != nil {
		t.Errorf("leaf (user pkg.Func) should NOT have __ctx_locale Param")
	}
	// Inside leaf body, the ContextRead is rewritten to an Ident that
	// resolves the pkg-level synth Var. Surface that to make sure the
	// rewrite didn't leave a dangling ContextRead.
	if n := countContextReads(leaf.Block, ctx); n != 0 {
		t.Errorf("leaf body still has %d ContextRead nodes after pass", n)
	}
	_ = identifiesHiddenParam
}

// TestFuncBodyContextReadRewritten verifies that a ContextRead inside a
// func body is rewritten to a read of the hidden param.
func TestFuncBodyContextReadRewritten(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeFunc("tr", nil,
		&ir.Return{Value: makeContextRead(ctx)},
	)
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{fn},
	}
	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}
	// No ContextRead should remain in fn.Block.
	if n := countContextReadsInBlock(fn.Block, ctx); n != 0 {
		t.Errorf("after pass: %d ContextRead nodes still in fn.Block", n)
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok {
		t.Fatalf("fn.Block[0] = %T; want *ir.Return", fn.Block[0])
	}
	if !identifiesHiddenParam(ret.Value, ctx) {
		t.Errorf("return value = %T %v; want Ident(__ctx_locale)", ret.Value, ret.Value)
	}
}

// TestIntrinsicFuncNotInReach verifies that funcs without a body
// (intrinsics, native imports) are not direct readers.
func TestIntrinsicFuncNotInReach(t *testing.T) {
	ctx := makeContext("theme", "light")
	// Intrinsic func — has no SNGL body. Should not be in Reach.
	intr := &ir.Func{Name: "float.round", Intrinsic: "float.round"}
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Funcs:    []*ir.Func{intr},
	}
	reach := computeReachability(pkg, nil)
	if reach.Funcs[ctx][intr] {
		t.Errorf("intrinsic func should never be in Reach (no body to inspect)")
	}
}

// countContextReadsInBlock counts ContextRead nodes anywhere in a stmt
// slice — same as countContextReads but reused for func bodies.
func countContextReadsInBlock(stmts []ir.Stmt, ctx *ir.Context) int {
	return countContextReads(stmts, ctx)
}
