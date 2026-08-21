package ir

import "fmt"

// Validate walks pkg and reports structural invariants that a well-formed
// IR package must satisfy. It returns one error per violation, or an empty
// slice when the package is sound.
//
// Validate is a diagnostic and safety-net tool, not a pipeline gate: it is
// meant to be run after a pass (e.g. ClonePackage, or a new lowering pass)
// to confirm the pass did not drop a cross-reference or leave a child
// pointer nil. The invariants checked here hold on both post-check and
// post-lower IR — cross-references that lowering legitimately synthesizes
// without a resolved symbol are excluded via the Synthesized / IsElementRef
// / Member predicates, so a clean package produces no violations at any
// phase.
func Validate(pkg *Package) []error {
	if pkg == nil {
		return nil
	}
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	Walk(pkg, func(n Node) error {
		switch x := n.(type) {
		case *Ident:
			// An identifier must resolve to a symbol — including one a
			// lowering pass synthesized, which refers to a Var, Param or
			// LoopVar that same pass created and can point at. Excluded:
			// element refs (#id and the synthesized __nN node handles),
			// which name a node in the emitted tree rather than a
			// declaration; bare enum members (carry Member instead of
			// Sym); and the magic identifiers the compiler injects with
			// nothing to resolve to.
			if x.Sym == nil && !x.IsElementRef && x.Member == "" && !isMagicIdent(x.Name) {
				add("unresolved identifier %q (nil Sym)", x.Name)
			}
		case *Call:
			if x.Type == nil {
				add("call %s has nil return Type", callDesc(x))
			}
			if x.Func == nil && x.Callee == nil && x.Receiver == nil {
				add("call %s has no Func, Callee, or Receiver", callDesc(x))
			}
			for i, a := range x.Args {
				if a.Value == nil {
					add("call %s arg %d has nil Value", callDesc(x), i)
				}
			}
		case *Binary:
			if x.Left == nil || x.Right == nil {
				add("binary %v has nil operand", x.Op)
			}
		case *Unary:
			if x.Operand == nil {
				add("unary %v has nil operand", x.Op)
			}
		case *Index:
			if x.Operand == nil || x.Idx == nil {
				add("index expression has nil operand or index")
			}
		case *Assign:
			if x.Target == nil || x.Value == nil {
				add("assign statement has nil Target or Value")
			}
		case *If:
			// A folded always-true If legitimately carries Cond == nil
			// (see optimize.foldIfStmt); accept it.
			checkNoNilStmts(add, "if body", x.Body)
			checkNoNilStmts(add, "if else", x.Else)
		case *For:
			if x.Iter == nil {
				add("for statement has nil Iter")
			}
			checkNoNilStmts(add, "for body", x.Body)
		case *NodeInst:
			checkNoNilStmts(add, "node children", x.Children)
		}
		return nil
	})
	return errs
}

// isMagicIdent reports whether name is a compiler-recognized identifier that
// legitimately resolves to no Symbol. `event` is the only one: it names the
// framework event object inside a handler scope, which no declaration in the
// program introduces — each target language maps it to its own event variable.
func isMagicIdent(name string) bool { return name == "event" }

func checkNoNilStmts(add func(string, ...any), where string, stmts []Stmt) {
	for i, s := range stmts {
		if s == nil {
			add("%s has nil statement at index %d", where, i)
		}
	}
}

// callDesc returns a short human-readable label for a call, for diagnostics.
func callDesc(c *Call) string {
	if c.Func != nil && c.Func.Name != "" {
		return fmt.Sprintf("%q", c.Func.Name)
	}
	if c.Event != "" {
		return fmt.Sprintf("event %q", c.Event)
	}
	return "<anonymous>"
}
