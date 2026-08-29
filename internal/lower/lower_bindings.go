package lower

import (
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passPropBindings transforms NodeInst.Bindings into @event+handler pairs
// so downstream codegen sees normal EventHandler IR.
//
// Always enabled; runs after PlatformExtensionBody so stdlib component bodies
// are already populated, and before NoToggle so Toggle stmts in component
// bodies can be rewritten to Emit nodes before they're further desugared.
var passPropBindings = pass{
	name:    "PropBindings",
	enabled: func(Caps) bool { return true },
	apply:   lowerAllPropBindings,
}

func lowerAllPropBindings(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		stmts: rewritePropBindingStmts,
	})
	return nil
}

// rewritePropBindingStmts walks a statement list, recursing into nested
// scopes, and lowers any NodeInst that carries Bindings.
func rewritePropBindingStmts(stmts []ir.Stmt) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if len(n.Bindings) > 0 {
				lowerPropBindings(n)
			}
			// Recurse into children (handlers already walked by walkPackage).
			n.Children = rewritePropBindingStmts(n.Children)
		case *ir.If:
			n.Body = rewritePropBindingStmts(n.Body)
			n.Else = rewritePropBindingStmts(n.Else)
		case *ir.For:
			n.Body = rewritePropBindingStmts(n.Body)
			n.Else = rewritePropBindingStmts(n.Else)
		case *ir.SlotInst:
			n.Children = rewritePropBindingStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = rewritePropBindingStmts(n.Children)
		case *ir.Window:
			n.Body = rewritePropBindingStmts(n.Body)
		case *ir.ContextProvider:
			n.Children = rewritePropBindingStmts(n.Children)
		}
	}
	return stmts
}

// lowerPropBindings transforms all PropBindings on inst into EventDecl +
// EventHandler pairs. For each PropBinding{PropName:"count", Target:steps}:
//  1. Adds EventDecl{Name:"count", Type:prop.Type} to the child component (idempotent).
//  2. Rewrites count+=1 / count=x / count!! in the component body/funcs to
//     ir.Emit{Name:"count", Args:[newVal]}.
//     2a. If step 2 found no mutations (e.g. for stdlib native components that
//     delegate to DOM events), inject @count(...) into the matching native event
//     handler in the component body so passInlinePure can route it through.
//  3. Synthesizes an EventHandler{Name:"count"} on inst that writes the
//     event arg back to the binding target.
//  4. Clears inst.Bindings.
func lowerPropBindings(inst *ir.NodeInst) {
	comp := inst.Component
	if comp == nil {
		inst.Bindings = nil
		return
	}

	for _, b := range inst.Bindings {
		prop := findBidiProp(comp, b.PropName)
		if prop == nil {
			continue
		}

		// 1. Add synthetic EventDecl to component (idempotent).
		if !hasBindingEvent(comp, b.PropName) {
			comp.Events = append(comp.Events, &ir.EventDecl{
				Name: b.PropName,
				Type: prop.Type,
			})
			// 2. Rewrite prop assignments/toggles → emit in component.
			rewritePropMutationsToEmit(comp, b.PropName, prop.Type)

			// 2a. If the component body has no @propName emits after the
			// rewrite (e.g. stdlib native components like html.input that
			// fire DOM events rather than assigning to props directly),
			// inject an emit into the matching native event handler body.
			// This lets passInlinePure route the DOM event through the
			// synthetic @propName event to the call-site handler.
			if !bodyHasEmitFor(comp.Body, b.PropName) {
				injectNativeEmit(comp, b.PropName, prop)
			}
		}

		// 3. Synthesize handler on this NodeInst.
		paramName := "__" + b.PropName
		param := &ir.Param{Name: paramName, Type: prop.Type}
		inst.Handlers = append(inst.Handlers, ir.EventHandler{
			Name: b.PropName,
			Func: &ir.Func{
				Params: []*ir.Param{param},
				Block: []ir.Stmt{&ir.Assign{
					Target: b.Target,
					Op:     ast.AssignSet,
					// Sym must point to param so passInlinePure's
					// bindEventParams can substitute the call-site arg.
					Value: &ir.Ident{Name: paramName, Type: prop.Type, Sym: param},
				}},
				Synthesized: true,
			},
		})
	}

	inst.Bindings = nil
}

// bodyHasEmitFor reports whether stmts (recursively) contain an ir.Emit
// with the given name. Used to detect whether rewritePropMutationsToEmit
// actually inserted any emits for the prop.
func bodyHasEmitFor(stmts []ir.Stmt, name string) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Emit:
			if n.Name == name {
				return true
			}
		case *ir.NodeInst:
			if bodyHasEmitFor(n.Children, name) {
				return true
			}
			for _, h := range n.Handlers {
				if h.Func != nil && bodyHasEmitFor(h.Func.Block, name) {
					return true
				}
			}
			for _, f := range propLambdas(n) {
				if bodyHasEmitFor(f.Block, name) {
					return true
				}
			}
		case *ir.If:
			if bodyHasEmitFor(n.Body, name) || bodyHasEmitFor(n.Else, name) {
				return true
			}
		case *ir.For:
			if bodyHasEmitFor(n.Body, name) || bodyHasEmitFor(n.Else, name) {
				return true
			}
		case *ir.SlotInst:
			if bodyHasEmitFor(n.Children, name) {
				return true
			}
		case *ir.ErrorBoundary:
			if bodyHasEmitFor(n.Children, name) {
				return true
			}
		case *ir.Window:
			if bodyHasEmitFor(n.Body, name) {
				return true
			}
		}
	}
	return false
}

// injectNativeEmit adds an ir.Emit{Name:propName} to the first native event
// handler in comp's body tree that matches a candidate event name. This bridges
// stdlib components (e.g. sngl.input) that fire DOM events but have no SNGL
// prop assignments: by injecting the emit into the existing @input/@change
// handler, passInlinePure's substituteEvents can route the DOM event to the
// synthetic @propName handler at the call site.
//
// For bool-typed props: emits @propName(!propName) — toggles the current value.
// For other types:      emits @propName(event.value) — reads from the DOM event.
func injectNativeEmit(comp *ir.Component, propName string, prop *ir.Prop) {
	// Choose candidate event names by prop type.
	var candidates []string
	if prop.Type != nil && prop.Type.Kind == ir.TypeBool {
		// Boolean props (e.g. checked) are toggled via @change.
		candidates = []string{"change"}
	} else {
		// String/other props: prefer @input (live updates), fallback to @change.
		candidates = []string{"input", "change"}
	}

	// Build the emit argument expression.
	var emitVal ir.Expr
	if prop.Type != nil && prop.Type.Kind == ir.TypeBool {
		// !propName — toggle the prop's current value.
		// After passInlinePure substitutes the prop param, this becomes !boundVar.
		synthParam := &ir.Param{Name: propName, Type: prop.Type}
		emitVal = &ir.Unary{
			Op:      ast.UnaryNot,
			Operand: &ir.Ident{Name: propName, Type: prop.Type, Sym: synthParam},
			Type:    prop.Type,
		}
	}
	// Otherwise the emit reads event.value, and which parameter `event` names
	// is only known once the receiving handler is found — emitFor builds it.

	injectEmitIntoHandlers(comp.Body, candidates, propName, eventPayloadType(comp, candidates), emitVal)
}

// eventPayloadType is the declared payload of whichever candidate event the
// component declares, for a handler that has to be given a parameter.
func eventPayloadType(comp *ir.Component, candidates []string) *ir.Type {
	for _, ev := range comp.Events {
		if slices.Contains(candidates, ev.Name) && ev.Type != nil {
			return ev.Type
		}
	}
	return ir.TypDyn
}

// handlerReportsValueChange reports whether h fires for one of the candidate
// value-changing SNGL events. Native stdlib wrappers name their handler after
// the DOM/framework signal (html `@input`, gtk4 `@changed`) but forward it by
// emitting the SNGL event (`input()`) from the body — so both the handler name
// and the emitted events count.
func handlerReportsValueChange(h *ir.EventHandler, candidates []string) bool {
	return slices.Contains(candidates, h.Name) || lambdaReportsValueChange(h.Func, candidates)
}

// lambdaReportsValueChange is the body half of the same question, for a
// callback that has no name of its own to be recognised by.
func lambdaReportsValueChange(f *ir.Func, candidates []string) bool {
	for _, c := range candidates {
		if bodyHasEmitFor(f.Block, c) {
			return true
		}
	}
	return false
}

// injectEmitIntoHandlers recursively searches stmts for the first NodeInst that
// reports a value change — a handler whose own name is one of the candidate
// SNGL events (html: `@input { input() }`), a handler whose body emits one of
// them (gtk4: `@changed { input() }`, where the handler is named for the native
// signal), or a callback passed as a func-typed prop, which has only its body
// to say so with (android: `onValueChange=func(e) { input(e) }`). It prepends
// emit there. Returns true if the injection was performed.
// emitFor builds the statement to prepend to f, the handler or callback the
// change arrives in. A nil value means the emit reads the new value from f's
// own parameter: it was an ambient name once and is a declared parameter now,
// so the reference names a declaration. A handler written without one
// (`@input { }`) has nothing to name, and this is the pass that needs it, so
// this is the pass that declares it.
//
// The parameter reaches the value either way: an event parameter carries it in
// the field the event declares, and a callback parameter is the value.
func emitFor(f *ir.Func, propName string, evtType *ir.Type, value ir.Expr, candidates []string) *ir.Emit {
	if value == nil {
		// A callback with no parameter of its own already carries the new value
		// in the event it emits -- android's radio writes
		// `onClick=func() { change({value = opt}) }`, where the value is the
		// option the loop is on. Declaring a parameter on it instead invented
		// one the caller never passes: Compose's onClick is `() -> Unit`, so the
		// emitted Kotlin did not compile, and `opt` -- the whole point of the
		// loop -- never reached the write-back.
		if len(f.Params) == 0 {
			if v := emittedValue(f.Block, candidates); v != nil {
				return &ir.Emit{Name: propName, Args: []ir.CallArg{{Value: v}}}
			}
			f.Params = []*ir.Param{{Name: "__event", Type: evtType}}
		}
		p := f.Params[0]
		t := p.Type
		if t == nil {
			t = ir.TypDyn
		}
		ref := &ir.Ident{Name: p.Name, Type: t, Sym: p}
		if t.Kind == ir.TypeStruct {
			value = &ir.Select{Operand: ref, Field: "value", Type: ir.TypString}
		} else {
			value = ref
		}
	}
	return &ir.Emit{Name: propName, Args: []ir.CallArg{{Value: value}}}
}

// emittedValue is the value a body's emit of one of the candidate events
// carries: the `value` field of a struct-literal payload, or the argument
// itself. Nil when the body emits none of them, or emits one with no argument.
func emittedValue(stmts []ir.Stmt, candidates []string) ir.Expr {
	for _, st := range stmts {
		e, ok := st.(*ir.Emit)
		if !ok || !slices.Contains(candidates, e.Name) || len(e.Args) == 0 {
			continue
		}
		arg := e.Args[0].Value
		if sl, ok := arg.(*ir.StructLit); ok {
			for _, f := range sl.Fields {
				if f.Name == "value" {
					return f.Value
				}
			}
			continue
		}
		return arg
	}
	return nil
}

// injectEmitIntoHandlers injects into *every* handler that reports the change,
// not the first. Stopping at the first put the write-back wherever the walk
// reached it soonest, which for a platform body written as
// `if direction == "horizontal" { ... } else { ... }` is the branch that is not
// taken -- so the live one emitted `onClick = {}` and the binding did nothing.
func injectEmitIntoHandlers(stmts []ir.Stmt, candidates []string, propName string, evtType *ir.Type, value ir.Expr) bool {
	injected := false
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			for i := range n.Handlers {
				h := &n.Handlers[i]
				if h.Func == nil {
					continue
				}
				if handlerReportsValueChange(h, candidates) {
					h.Func.Block = append([]ir.Stmt{emitFor(h.Func, propName, evtType, value, candidates)}, h.Func.Block...)
					injected = true
				}
			}
			// A callback passed as a func-typed prop reports the change the
			// same way a handler does — by emitting the SNGL event from its
			// body — so the write-back belongs in it for the same reason.
			for _, f := range propLambdas(n) {
				if !lambdaReportsValueChange(f, candidates) {
					continue
				}
				f.Block = append([]ir.Stmt{emitFor(f, propName, evtType, value, candidates)}, f.Block...)
				injected = true
			}
			// Recurse into children.
			if injectEmitIntoHandlers(n.Children, candidates, propName, evtType, value) {
				injected = true
			}
		case *ir.For:
			if injectEmitIntoHandlers(n.Body, candidates, propName, evtType, value) {
				injected = true
			}
		case *ir.If:
			if injectEmitIntoHandlers(n.Body, candidates, propName, evtType, value) {
				injected = true
			}
			if injectEmitIntoHandlers(n.Else, candidates, propName, evtType, value) {
				injected = true
			}
		}
	}
	return injected
}

// rewritePropMutationsToEmit rewrites every Assign/Toggle targeting propName
// in comp.Body and comp.Funcs to emit the corresponding event instead.
func rewritePropMutationsToEmit(comp *ir.Component, propName string, propType *ir.Type) {
	var walk func([]ir.Stmt) []ir.Stmt
	walk = func(stmts []ir.Stmt) []ir.Stmt {
		out := make([]ir.Stmt, len(stmts))
		for i, s := range stmts {
			out[i] = rewriteStmtPropMutation(s, propName, propType, walk)
		}
		return out
	}
	comp.Body = walk(comp.Body)
	for _, fn := range comp.Funcs {
		fn.Block = walk(fn.Block)
	}
	// Also walk any event handler blocks already present on the component's vars.
	for _, v := range comp.Vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = walk(h.Func.Block)
			}
		}
	}
	// Walk timer handler blocks.
	for _, t := range comp.Timers {
		if t.Handler != nil {
			t.Handler.Block = walk(t.Handler.Block)
		}
	}
}

// rewriteStmtPropMutation converts Assign/Toggle targeting propName into an
// Emit, recursing into nested blocks.
func rewriteStmtPropMutation(s ir.Stmt, propName string, propType *ir.Type, walk func([]ir.Stmt) []ir.Stmt) ir.Stmt {
	switch x := s.(type) {
	case *ir.Assign:
		if identTargets(x.Target, propName) {
			return &ir.Emit{
				Name: propName,
				Args: []ir.CallArg{{Value: computeAssignNewVal(x.Target, x.Op, x.Value, propType)}},
			}
		}
	case *ir.Toggle:
		if identTargets(x.Target, propName) {
			return &ir.Emit{
				Name: propName,
				Args: []ir.CallArg{{Value: &ir.Unary{
					Op:      ast.UnaryNot,
					Operand: x.Target,
					Type:    propType,
				}}},
			}
		}
	case *ir.If:
		x.Body = walk(x.Body)
		x.Else = walk(x.Else)
	case *ir.For:
		x.Body = walk(x.Body)
		x.Else = walk(x.Else)
	case *ir.NodeInst:
		// Recurse into handler blocks on nested nodes.
		for j := range x.Handlers {
			if x.Handlers[j].Func != nil {
				x.Handlers[j].Func.Block = walk(x.Handlers[j].Func.Block)
			}
		}
		for _, f := range propLambdas(x) {
			f.Block = walk(f.Block)
		}
		x.Children = walk(x.Children)
	case *ir.SlotInst:
		x.Children = walk(x.Children)
	case *ir.ErrorBoundary:
		x.Children = walk(x.Children)
		if x.Handler != nil && x.Handler.Func != nil {
			x.Handler.Func.Block = walk(x.Handler.Func.Block)
		}
	case *ir.ContextProvider:
		x.Children = walk(x.Children)
	case *ir.Window:
		x.Body = walk(x.Body)
		for _, fn := range x.Funcs {
			fn.Block = walk(fn.Block)
		}
		for _, v := range x.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = walk(h.Func.Block)
				}
			}
		}
		if x.ErrorHandler != nil && x.ErrorHandler.Func != nil {
			x.ErrorHandler.Func.Block = walk(x.ErrorHandler.Func.Block)
		}
	}
	return s
}

// computeAssignNewVal converts an assignment op + value into the new-value
// expression for the Emit payload.
func computeAssignNewVal(target ir.Expr, op ast.AssignOp, value ir.Expr, typ *ir.Type) ir.Expr {
	if op == ast.AssignSet {
		return value
	}
	return &ir.Binary{
		Op:    assignOpToBinOp(op),
		Left:  target,
		Right: value,
		Type:  typ,
	}
}

func assignOpToBinOp(op ast.AssignOp) ast.BinaryOp {
	switch op {
	case ast.AssignAdd:
		return ast.BinAdd
	case ast.AssignSub:
		return ast.BinSub
	case ast.AssignMul:
		return ast.BinMul
	case ast.AssignDiv:
		return ast.BinDiv
	case ast.AssignMod:
		return ast.BinMod
	default:
		return ast.BinAdd
	}
}

// identTargets reports whether expr is an *ir.Ident with the given name.
func identTargets(e ir.Expr, name string) bool {
	id, ok := e.(*ir.Ident)
	return ok && id.Name == name
}

// findBidiProp finds a bidirectional prop by name on comp.
func findBidiProp(comp *ir.Component, name string) *ir.Prop {
	for _, p := range comp.Props {
		if p.Name == name && p.Bidirectional {
			return p
		}
	}
	return nil
}

// hasBindingEvent reports whether comp already has an event named name
// (to keep the operation idempotent when a component is used at multiple
// binding sites).
func hasBindingEvent(comp *ir.Component, name string) bool {
	for _, e := range comp.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}
