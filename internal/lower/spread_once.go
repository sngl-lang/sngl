package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passSpreadOnce binds a spread's computed operand to a local before the
// statement holding it, so `add(...next())` calls next once rather than once
// per field it fills. The interpreter makes the same binding at the same
// point (ir.StatementSpreads), which is what keeps the order of effects in
// agreement: the operand is evaluated ahead of the rest of the statement.
//
// Imperative blocks only, for passCSE's reason. A view body cannot hold the
// temp, and the checker refuses a spread there whose operand writes state.
var passSpreadOnce = pass{
	name:    "SpreadOnce",
	enabled: func(Features) bool { return true },
	apply:   lowerSpreadOnce,
}

func lowerSpreadOnce(pkg *ir.Package, _ Features, _ Options) error {
	st := &spreadOnceState{}
	for _, block := range imperativeBlocks(pkg) {
		st.block(block)
	}
	return nil
}

type spreadOnceState struct {
	counter int
}

func (st *spreadOnceState) block(block *[]ir.Stmt) {
	out := make([]ir.Stmt, 0, len(*block))
	for _, s := range *block {
		switch n := s.(type) {
		case *ir.If:
			st.block(&n.Body)
			st.block(&n.Else)
		case *ir.For:
			st.block(&n.Body)
			st.block(&n.Else)
		}
		for _, g := range ir.StatementSpreads(s) {
			name := "__spread" + strconv.Itoa(st.counter)
			st.counter++
			typ := g.Operand.ExprType()
			sym := &ir.Var{Name: name, Type: typ, Synthesized: true}
			out = append(out, &ir.LocalVar{Name: name, Type: typ, Init: g.Operand, Sym: sym})
			for _, sel := range g.Selects {
				sel.Operand = &ir.Ident{Name: name, Sym: sym, Type: typ}
				sel.Spread = 0
			}
		}
		out = append(out, s)
	}
	*block = out
}
