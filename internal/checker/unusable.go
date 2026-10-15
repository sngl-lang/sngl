package checker

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

const anyNotAType = "`any` is a constraint, not a type: every value's type is one the program can name"

// holdAny types decl with resolve, holding back the refusal of `any` written
// as a type until decl's marks have run (refuseAnyUnlessUnusable). A host
// declaration marked #[macro.unusable] has a type SNGL has no name for, and
// `any` is how ir.Convert writes one: nothing reads it, since every use of the
// declaration is refused. Returns where `any` was written.
func (c *checker) holdAny(decl ast.Stmt, resolve func()) []ast.Pos {
	if a, ok := decl.(ast.Attributed); !ok || len(a.MacroAttrs()) == 0 {
		resolve()
		return nil
	}
	saved := c.anyWritten
	var at []ast.Pos
	c.anyWritten = &at
	resolve()
	c.anyWritten = saved
	return at
}

// refuseAnyUnlessUnusable refuses each `any` holdAny held back, once the
// declaration's marks have run, unless they made it unusable.
func (c *checker) refuseAnyUnlessUnusable(at []ast.Pos, f *ir.Foreign) {
	if len(at) == 0 {
		return
	}
	c.afterMarks(func() {
		if f.Unusable != "" {
			return
		}
		for _, p := range at {
			c.error(p, anyNotAType)
		}
	})
}
