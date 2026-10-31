package lower

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passPropBindings transforms NodeInst.Bindings into @event+handler pairs
// so downstream codegen sees normal EventHandler IR.
//
// Always enabled; runs after PlatformExtensionBody so stdlib component bodies
// are already populated, and before NoToggle so Toggle stmts in component
// bodies can be rewritten to Emit nodes before they're further desugared.
var passPropBindings = pass{
	name:    "PropBindings",
	enabled: func(Features) bool { return true },
	apply:   lowerAllPropBindings,
}

func lowerAllPropBindings(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		stmts: rewritePropBindingStmts,
	})
	// A library component's body is no body of the package's, and a binding
	// written there -- an override handing its own two-way prop on to the
	// primitive it renders, `Toplevel(:visible=visible)` -- is spliced into
	// the program with the rest of that body, after this pass. So each one
	// the program renders is lowered here too, and the bodies those render.
	// Idempotent, as it has to be for a declaration every build shares:
	// lowering a binding clears it.
	own := make(map[*ir.Component]bool, len(pkg.Components))
	for _, c := range pkg.Components {
		own[c] = true
	}
	seen := map[*ir.Component]bool{}
	var visit func(root any)
	visit = func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			inst, ok := n.(*ir.NodeInst)
			if !ok || inst.Component == nil || own[inst.Component] || seen[inst.Component] {
				return nil
			}
			c := inst.Component
			seen[c] = true
			if len(c.Stmts) == 0 {
				return nil
			}
			c.Stmts = rewritePropBindingStmts(c.Stmts)
			visit(c.Stmts)
			return nil
		})
	}
	visit(pkg)
	return nil
}

// rewritePropBindingStmts lowers every node in a view that carries Bindings,
// wherever it stands in the view: a slot's population and a boundary's
// fallback are content the program wrote as much as a node's children are.
// Handlers are walked by walkPackage.
func rewritePropBindingStmts(stmts []ir.Stmt) []ir.Stmt {
	ir.WalkView(stmts, func(s ir.Stmt) bool {
		if n, ok := s.(*ir.NodeInst); ok && len(n.Bindings) > 0 {
			lowerPropBindings(n)
		}
		return true
	})
	return stmts
}

// lowerPropBindings transforms all PropBindings on inst into EventDecl +
// EventHandler pairs. For each PropBinding{PropName:"count", Target:steps}:
//  1. Adds EventDecl{Name:"count", Params:[prop.Type]} to the child component (idempotent).
//  2. Rewrites count+=1 / count=x / count!! in the component body/funcs to
//     ir.Emit{Name:"count", Args:[newVal]}. A platform override reports what
//     its control holds the same way, by writing the prop in the handler of
//     the host event that reported it -- `@change(e) { checked =
//     e.currentTarget.checked }` -- so nothing here guesses which event that
//     is. A primitive's own two-way prop is its platform's to write back.
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
				Name:   b.PropName,
				Params: []*ir.Param{{Name: b.PropName, Type: prop.Type}},
			})
			// 2. Rewrite prop assignments/toggles → emit in component. A body
			// handing the prop on to what it renders -- an override's
			// `Toplevel(:visible=visible)` -- writes it through that binding,
			// which is lowered first so the write it becomes is one of these:
			// left for later, it was a second report of the prop beside the
			// emit, and the node carried the handler twice.
			comp.Stmts = rewritePropBindingStmts(comp.Stmts)
			rewritePropMutationsToEmit(comp, b.PropName, prop.Type)
		}

		// 3. Synthesize handler on this NodeInst -- unless step 2 already
		// did: a component binding its own two-way prop to a recursion of
		// itself lowers this node from inside its own body, and the handler
		// that lowering wrote has since become the emit that reports it.
		if slices.ContainsFunc(inst.Handlers, func(h ir.EventHandler) bool { return h.WriteBack && h.Name == b.PropName }) {
			continue
		}
		paramName := "__" + b.PropName
		// The handler is the call site's, so its parameter has the type the
		// site binds: `T` erased to `any` on a copy built at run time.
		pt := prop.Type
		if tb := ir.ComponentBindings(inst); tb != nil {
			pt = pt.Substitute(tb)
		}
		param := &ir.Param{Name: paramName, Type: pt}
		inst.Handlers = append(inst.Handlers, ir.EventHandler{
			Name:      b.PropName,
			WriteBack: true,
			Func: &ir.Func{
				Params: []*ir.Param{param},
				Block: []ir.Stmt{&ir.Assign{
					Target: b.Target,
					Op:     ast.AssignSet,
					// Sym must point to param so passInlinePure's
					// bindEventParams can substitute the call-site arg.
					Value: &ir.Ident{Name: paramName, Type: pt, Sym: param},
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
	has := func(f *ir.Func) bool { return f != nil && bodyHasEmitFor(f.Block, name) }
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Emit:
			if n.Name == name {
				return true
			}
		case *ir.NodeInst:
			for _, h := range n.Handlers {
				if has(h.Func) {
					return true
				}
			}
			if slices.ContainsFunc(propLambdas(n), has) {
				return true
			}
		case *ir.ErrorBoundary:
			if n.Handler != nil && has(n.Handler.Func) {
				return true
			}
		}
		for _, b := range ir.ViewBlocks(s) {
			if bodyHasEmitFor(*b, name) {
				return true
			}
		}
	}
	return false
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
	comp.Stmts = walk(comp.Stmts)
	for _, fn := range comp.Funcs {
		fn.Block = walk(fn.Block)
	}
	// Walk timer handler blocks.
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
	case *ir.Match:
		for _, arm := range x.Arms {
			arm.Body = walk(arm.Body)
		}
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
