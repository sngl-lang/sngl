package lower

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passInlinePure substitutes pure-component calls with their inlined
// bodies. Pure = no Vars, no Funcs, no Timers.
//
// Two modes share the substitution engine:
//   - Optimization (always-on): inlines user-defined pure components.
//   - Strict (Caps.NoStdlibWrappers): inlines platform-stdlib wrappers
//     and errors if any platform-stdlib component is impure.
//
// Runs between passReactivity and passTimer. After passReactivity so
// reactive deps wire against user-level props before inlining flattens
// them; before passDeclarative so the inlined native NodeInsts get
// flattened along with everything else.
var passInlinePure = pass{
	name:    "InlinePure",
	enabled: func(c Caps) bool { return true }, // always on (strict path gated internally)
	apply:   lowerInlinePure,
}

func lowerInlinePure(pkg *ir.Package, caps Caps) error {
	if pkg == nil {
		return nil
	}
	st := &inlinePureState{
		pkg:        pkg,
		strictMode: caps.NoStdlibWrappers,
		inFlight:   map[*ir.Component]bool{},
	}
	for _, comp := range pkg.Components {
		body, err := st.inlineStmts(comp.Body)
		if err != nil {
			return err
		}
		comp.Body = body
	}
	for _, w := range pkg.Windows {
		body, err := st.inlineStmts(w.Body)
		if err != nil {
			return err
		}
		w.Body = body
	}
	return nil
}

type inlinePureState struct {
	pkg        *ir.Package
	strictMode bool
	inFlight   map[*ir.Component]bool
}

// isPure reports whether a component is structurally pure (no internal
// state). nil component → false.
func (st *inlinePureState) isPure(c *ir.Component) bool {
	if c == nil {
		return false
	}
	// A component with no body is platform-resolved (e.g. stdlib widget
	// stubs with Native metadata). Nothing to inline.
	if len(c.Body) == 0 {
		return false
	}
	return len(c.Vars) == 0 && len(c.Funcs) == 0 && len(c.Timers) == 0
}

// inlineStmts walks a stmt slice, recursing into nested control-flow
// bodies and NodeInst children/handlers, and inlines eligible
// NodeInst → component calls in place.
func (st *inlinePureState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, error) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		replaced, err := st.inlineStmt(s)
		if err != nil {
			return nil, err
		}
		out = append(out, replaced...)
	}
	return out, nil
}

// inlineStmt processes one stmt. Returns the slice of replacement stmts
// (may be one or many).
func (st *inlinePureState) inlineStmt(s ir.Stmt) ([]ir.Stmt, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		return st.inlineNodeInst(n)
	case *ir.If:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.For:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.PlatformFilter:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		n.Body = body
		return []ir.Stmt{n}, nil
	case *ir.SlotInst:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		return []ir.Stmt{n}, nil
	}
	return []ir.Stmt{s}, nil
}

// inlineNodeInst decides whether to inline n. If yes, runs the
// substitution engine and returns the substituted stmts. If no,
// recurses into n's children/handlers and returns n unchanged.
func (st *inlinePureState) inlineNodeInst(n *ir.NodeInst) ([]ir.Stmt, error) {
	// Recurse first so nested calls inline bottom-up.
	children, err := st.inlineStmts(n.Children)
	if err != nil {
		return nil, err
	}
	n.Children = children
	for _, h := range n.Handlers {
		if h.Func == nil {
			continue
		}
		body, err := st.inlineStmts(h.Func.Block)
		if err != nil {
			return nil, err
		}
		h.Func.Block = body
	}

	comp := n.Component
	if comp == nil {
		return []ir.Stmt{n}, nil
	}

	// Decide eligibility.
	pure := st.isPure(comp)
	strictApplies := st.strictMode && isPlatformStdlibComponent(st.pkg, comp)
	if !pure && !strictApplies {
		return []ir.Stmt{n}, nil
	}
	if strictApplies && !pure {
		return nil, fmt.Errorf("platform stdlib wrapper %q must be pure (declares %s)", comp.Name, impurityReason(comp))
	}

	// Cycle check.
	if st.inFlight[comp] {
		return nil, fmt.Errorf("inline cycle in component %q", comp.Name)
	}
	st.inFlight[comp] = true
	defer delete(st.inFlight, comp)

	// Substitute.
	body, err := st.substitute(comp, n)
	if err != nil {
		return nil, err
	}
	// Recurse on substituted body (the wrapper's body may itself contain
	// pure-component calls that need inlining).
	return st.inlineStmts(body)
}

// impurityReason returns a short string describing why comp is impure.
// Caller has already established len(Vars|Funcs|Timers) > 0.
func impurityReason(comp *ir.Component) string {
	var parts []string
	if len(comp.Vars) > 0 {
		parts = append(parts, fmt.Sprintf("var %q", comp.Vars[0].Name))
	}
	if len(comp.Funcs) > 0 {
		parts = append(parts, fmt.Sprintf("func %q", comp.Funcs[0].Name))
	}
	if len(comp.Timers) > 0 {
		parts = append(parts, "timer")
	}
	return strings.Join(parts, ", ")
}

// isPlatformStdlibComponent reports whether comp came from one of the
// package's platform:// imports.
func isPlatformStdlibComponent(pkg *ir.Package, comp *ir.Component) bool {
	for _, imp := range pkg.Imports {
		if !strings.HasPrefix(imp.Path, "platform://") {
			continue
		}
		if imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c == comp {
				return true
			}
		}
	}
	return false
}

// substitute applies the three substitutions (params, slot, events) to
// the wrapper's body, returning a fresh stmt slice ready to splice into
// the caller's position.
func (st *inlinePureState) substitute(comp *ir.Component, callsite *ir.NodeInst) ([]ir.Stmt, error) {
	if len(comp.Body) == 0 {
		return nil, fmt.Errorf("component %q has no body to inline", comp.Name)
	}
	// Implementation: Task 3 fills this in.
	return nil, fmt.Errorf("substitute: not implemented")
}
