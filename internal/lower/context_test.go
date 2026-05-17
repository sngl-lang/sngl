package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// --- test helpers ---

// makeStringLit returns a string literal expression.
func makeStringLit(s string) *ir.Literal {
	return &ir.Literal{Type: ir.TypString, Raw: `"` + s + `"`}
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

// findHiddenProp returns the hidden prop with the given name, or nil.
func findHiddenProp(comp *ir.Component, name string) *ir.Prop {
	for _, p := range comp.Props {
		if p.Name == name {
			return p
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

	reach := computeReachability(pkg)
	if !reach[ctx][consumer] {
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

	reach := computeReachability(pkg)
	if !reach[ctx][inner] {
		t.Errorf("Inner should be in Reach(theme)")
	}
	if !reach[ctx][outer] {
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

	reach := computeReachability(pkg)
	if !reach[ctx][reader] {
		t.Errorf("Reader should be in Reach(theme)")
	}
	if reach[ctx][caller] {
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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)

	propName := "__ctx_theme"
	if p := findHiddenProp(consumer, propName); p == nil {
		t.Errorf("Consumer should have prop %q after addHiddenParams", propName)
	} else if p.Type != ctx.Typ {
		t.Errorf("hidden prop type = %v; want %v", p.Type, ctx.Typ)
	}
	if p := findHiddenProp(nonReader, propName); p != nil {
		t.Errorf("NonReader should NOT have prop %q", propName)
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
	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	addHiddenParams(pkg, reach)

	count := 0
	for _, p := range consumer.Props {
		if p.Name == "__ctx_theme" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 __ctx_theme prop; got %d", count)
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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	rewriteReads(pkg, reach)

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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	lowerProviders(pkg, reach)

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
		if !ok || lit.Raw != `"dark"` {
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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	lowerProviders(pkg, reach)

	ni, ok := win.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.NodeInst", win.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_theme")
	if val == nil {
		t.Error("NodeInst at window root missing __ctx_theme arg (should get default)")
	} else {
		lit, ok := val.(*ir.Literal)
		if !ok || lit.Raw != `"light"` {
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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	lowerProviders(pkg, reach)

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
			return l.Raw
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

	// Toolbar has hidden prop.
	if p := findHiddenProp(toolbar, "__ctx_theme"); p == nil {
		t.Error("Toolbar missing __ctx_theme prop")
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
	} else if lit, ok := val.(*ir.Literal); !ok || lit.Raw != `"dark"` {
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
	cond := &ir.Literal{Type: ir.TypBool, Raw: "true"}
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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	lowerProviders(pkg, reach)

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
	} else if lit, ok := val.(*ir.Literal); !ok || lit.Raw != `"themed"` {
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
	if p := findHiddenProp(consumer, "__ctx_theme"); p == nil {
		t.Error("Consumer missing __ctx_theme prop")
	}
	if p := findHiddenProp(consumer, "__ctx_locale"); p == nil {
		t.Error("Consumer missing __ctx_locale prop")
	}
	if n := countContextReads(consumer.Body, ctxA); n != 0 {
		t.Errorf("Consumer still has %d ContextRead(theme)", n)
	}
	if n := countContextReads(consumer.Body, ctxB); n != 0 {
		t.Errorf("Consumer still has %d ContextRead(locale)", n)
	}
}

// --- Tests for ReadsContexts threading (i18n-style functions) ---

// makeI18nLikeFunc creates a fake stdlib function whose ReadsContexts is set to
// the given context — simulating what annotateI18nReadsContexts does for i18n.tr.
func makeI18nLikeFunc(name string, ctx *ir.Context) *ir.Func {
	return &ir.Func{
		Name:          name,
		Receiver:      "i18n",
		ReadsContexts: []*ir.Context{ctx},
		Purity:        ir.PurityPure,
	}
}

// makeI18nCall builds an *ir.Call to an i18n-like function with the given args.
func makeI18nCall(fn *ir.Func, args ...ir.CallArg) *ir.Call {
	return &ir.Call{
		Func: fn,
		Args: args,
		Type: ir.TypString,
	}
}

// TestDirectContextReads_ReadsContextsFunc checks that a component containing a
// call to a Func with ReadsContexts is counted as a direct reader of that context.
func TestDirectContextReads_ReadsContextsFunc(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeI18nLikeFunc("tr", ctx)

	// Component body: var x = i18n.tr("key", {})
	call := makeI18nCall(fn,
		ir.CallArg{Name: "", Value: makeStringLit("key")},
		ir.CallArg{Name: "", Value: makeStringLit("{}")},
	)
	comp := makeComp("I18nUser",
		&ir.Return{Value: call},
	)

	reads := directContextReads(comp)
	if !reads[ctx] {
		t.Errorf("directContextReads: component calling a func with ReadsContexts should read that context")
	}
}

// TestReachability_ReadsContextsFuncPropagates checks that a component calling an
// i18n-like func is included in Reach(locale) and gains a hidden prop.
func TestReachability_ReadsContextsFuncPropagates(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeI18nLikeFunc("tr", ctx)

	call := makeI18nCall(fn,
		ir.CallArg{Name: "", Value: makeStringLit("hello")},
		ir.CallArg{Name: "", Value: makeStringLit("{}")},
	)
	comp := makeComp("Label",
		&ir.Return{Value: call},
	)
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{comp},
	}

	reach := computeReachability(pkg)
	if !reach[ctx][comp] {
		t.Errorf("Label should be in Reach(locale) because it calls a func with ReadsContexts")
	}

	addHiddenParams(pkg, reach)
	if p := findHiddenProp(comp, "__ctx_locale"); p == nil {
		t.Error("Label should have a __ctx_locale hidden prop")
	}
}

// TestLowerProviders_I18nCallGetsLocaleArg checks that when lowerProviders runs,
// a call to an i18n-like function inside a component body (accessed via a
// CallStmt) receives the active locale as a trailing named arg.
func TestLowerProviders_I18nCallGetsLocaleArg(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeI18nLikeFunc("tr", ctx)

	call := makeI18nCall(fn,
		ir.CallArg{Name: "", Value: makeStringLit("key")},
		ir.CallArg{Name: "", Value: makeStringLit("{}")},
	)
	comp := makeComp("Greeter",
		&ir.Return{Value: call},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:      ctx,
				Value:    makeStringLit("fr"),
				Children: []ir.Stmt{makeNodeInstComp(comp)},
			},
		},
	}
	pkg := &ir.Package{
		Contexts:   []*ir.Context{ctx},
		Components: []*ir.Component{comp},
		Windows:    []*ir.Window{win},
	}

	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}

	// Verify that the NodeInst in win.Body got __ctx_locale threaded.
	if len(win.Body) == 0 {
		t.Fatal("window body empty after applyNoContext")
	}
	ni, ok := win.Body[0].(*ir.NodeInst)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.NodeInst", win.Body[0])
	}
	val := findArgInNodeInst(ni, "__ctx_locale")
	if val == nil {
		t.Error("NodeInst for Greeter missing __ctx_locale arg")
	} else if lit, ok := val.(*ir.Literal); !ok || lit.Raw != `"fr"` {
		t.Errorf("__ctx_locale = %v; want Literal(\"fr\")", val)
	}

	// Verify that the i18n.tr call inside comp.Body got the __ctx_locale arg
	// threaded as the active locale for the component (the hidden prop ident).
	if len(comp.Body) == 0 {
		t.Fatal("comp.Body empty after applyNoContext")
	}
	ret, ok2 := comp.Body[0].(*ir.Return)
	if !ok2 {
		t.Fatalf("comp.Body[0] = %T; want *ir.Return", comp.Body[0])
	}
	i18nCall, ok3 := ret.Value.(*ir.Call)
	if !ok3 {
		t.Fatalf("comp.Body[0] return value = %T; want *ir.Call", ret.Value)
	}
	if !hasCallArgNamed(i18nCall.Args, "__ctx_locale") {
		t.Errorf("i18n call in comp.Body missing __ctx_locale arg; args = %v", i18nCall.Args)
	}
	for _, a := range i18nCall.Args {
		if a.Name == "__ctx_locale" {
			id, isIdent := a.Value.(*ir.Ident)
			if !isIdent || id.Name != "__ctx_locale" {
				t.Errorf("__ctx_locale in comp.Body = %v; want Ident(__ctx_locale)", a.Value)
			}
		}
	}
}

// TestLowerProviders_I18nCallInWindowBody checks that an i18n call directly in
// a window body (not inside a component) gets the locale arg threaded in.
func TestLowerProviders_I18nCallInWindowBody(t *testing.T) {
	ctx := makeContext("locale", "en")
	fn := makeI18nLikeFunc("tr", ctx)

	call := makeI18nCall(fn,
		ir.CallArg{Name: "", Value: makeStringLit("key")},
		ir.CallArg{Name: "", Value: makeStringLit("{}")},
	)
	win := &ir.Window{
		Name: "home",
		Body: []ir.Stmt{
			&ir.ContextProvider{
				Ref:   ctx,
				Value: makeStringLit("de"),
				Children: []ir.Stmt{
					&ir.Return{Value: call},
				},
			},
		},
	}
	pkg := &ir.Package{
		Contexts: []*ir.Context{ctx},
		Windows:  []*ir.Window{win},
	}

	if err := applyNoContext(pkg, Caps{}, Options{}); err != nil {
		t.Fatalf("applyNoContext: %v", err)
	}

	// After lowering, the provider is replaced by its children.
	// The Return stmt should contain the call with the locale arg appended.
	if len(win.Body) == 0 {
		t.Fatal("window body empty after applyNoContext")
	}
	ret, ok := win.Body[0].(*ir.Return)
	if !ok {
		t.Fatalf("win.Body[0] = %T; want *ir.Return", win.Body[0])
	}
	i18nCall, ok := ret.Value.(*ir.Call)
	if !ok {
		t.Fatalf("return value = %T; want *ir.Call", ret.Value)
	}
	// The call should now have a trailing __ctx_locale arg.
	if !hasCallArgNamed(i18nCall.Args, "__ctx_locale") {
		t.Errorf("i18n call missing __ctx_locale arg after lowering; args = %v", i18nCall.Args)
	}
	for _, a := range i18nCall.Args {
		if a.Name == "__ctx_locale" {
			if lit, ok := a.Value.(*ir.Literal); !ok || lit.Raw != `"de"` {
				t.Errorf("__ctx_locale = %v; want Literal(\"de\")", a.Value)
			}
		}
	}
}
