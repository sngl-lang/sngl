package bubbletea

import "duckfam.us/sngl/ir"

// handlerLines is a handler's body written where Update runs it. A return
// in it ends the handler, so a body holding one is a closure called in
// place: inline, the return was Update's, which answers two values.
func handlerLines(gc goEvaluator, block []ir.Stmt) []string {
	var lines []string
	for _, s := range block {
		lines = append(lines, gc.EvalStmt(s)...)
	}
	if !returnsEarly(block) {
		return lines
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, "func() {")
	for _, l := range lines {
		out = append(out, "\t"+l)
	}
	return append(out, "}()")
}

// returnsEarly reports whether block returns from the handler, rather than
// from a function written in it.
func returnsEarly(block []ir.Stmt) bool {
	found := false
	for _, s := range block {
		_ = ir.Walk(s, func(n ir.Node) error {
			switch n.(type) {
			case *ir.Lambda:
				return ir.SkipDir
			case *ir.Return:
				found = true
				return ir.SkipAll
			}
			return nil
		})
	}
	return found
}
