package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// verifyMutationsAreStatements is the backstop: a call that mutates its
// receiver has to be a statement of its own, because no host language has one
// expression that both mutates a list and yields it. Go's emitter writes the
// whole statement `xs = append(xs, v)`, JavaScript's `xs.push(v)` answers the
// new length and Kotlin's `xs.add(v)` a Boolean -- so one left in an expression
// position is emitted as Go that does not parse (`m.n = m.l = append(m.l, 2)[0]`)
// or as JavaScript and Kotlin that compile and do the wrong thing, silently.
//
// The checker refuses these where they are written, which is where the message
// belongs. This says the same thing over the whole lowered package and so does
// not have to be told about a new expression position: void rejection in the
// checker is a hand-written list of the positions someone remembered, and it
// did not grow when `push` and `remove` stopped returning their receiver --
// three positions leaked at once. Which calls mutate is `MutatesReceiver` in
// ir.Intrinsics, read through mutatingCallReceiver, so a fourth intrinsic
// declaring the flag is covered by declaring it.
//
// It is an assertion rather than a pass: it changes nothing, so it is not on
// the `passes` list and no capability gates it. It runs at the end of Lower --
// after every pass, on the IR a backend is about to walk.
func verifyMutationsAreStatements(pkg *ir.Package) error {
	stmtCalls := map[*ir.Call]bool{}
	_ = ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil {
			stmtCalls[cs.Call] = true
		}
		return nil
	})
	var err error
	_ = ir.WalkExprs(pkg, func(e ir.Expr) error {
		c, ok := e.(*ir.Call)
		if !ok || stmtCalls[c] || mutatingCallReceiver(c) == nil {
			return nil
		}
		err = fmt.Errorf("%s mutates its receiver and yields nothing, which no target can spell as an expression; write it as a statement of its own", intrinsicName(c))
		return ir.SkipAll
	})
	return err
}

func intrinsicName(c *ir.Call) string {
	if c == nil || c.Func == nil || c.Func.Intrinsic == "" {
		return "the call"
	}
	return c.Func.Intrinsic
}
