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

	Walk(pkg, VisitorFuncs{
		Expr: func(e Expr) bool {
			switch x := e.(type) {
			case *Ident:
				// A user-level identifier must resolve to a symbol. Excluded:
				// pass-synthesized refs (Synthesized), element refs (#id),
				// bare enum members (carry Member instead of Sym), and the
				// magic platform-gate identifiers (PLATFORM/LANGUAGE) which
				// the checker resolves without a symbol.
				if x.Sym == nil && !x.Synthesized && !x.IsElementRef && x.Member == "" && !isMagicIdent(x.Name) {
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
			}
			return false
		},
		Stmt: func(s Stmt) bool {
			switch n := s.(type) {
			case *Assign:
				if n.Target == nil || n.Value == nil {
					add("assign statement has nil Target or Value")
				}
			case *If:
				if n.Cond == nil {
					// A folded always-true If legitimately carries Cond == nil
					// (see optimize.foldIfStmt); accept it.
				}
				checkNoNilStmts(add, "if body", n.Body)
				checkNoNilStmts(add, "if else", n.Else)
			case *For:
				if n.Iter == nil {
					add("for statement has nil Iter")
				}
				checkNoNilStmts(add, "for body", n.Body)
			case *NodeInst:
				checkNoNilStmts(add, "node children", n.Children)
			}
			return false
		},
	})
	return errs
}

// isMagicIdent reports whether name is a compiler-recognized identifier that
// legitimately resolves to no Symbol (platform/language build-gate literals).
func isMagicIdent(name string) bool {
	switch name {
	case "PLATFORM", "LANGUAGE", "true", "false", "null":
		return true
	}
	return false
}

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
