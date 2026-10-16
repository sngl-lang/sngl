package ir

import (
	"strings"

	"duckfam.us/sngl/ast"
)

// CopyProp is the prop a node a test indexes carries the key of its copy in,
// and the prop a component built at run time is handed the key of the copy it
// stands in, which its own copies' keys extend.
const CopyProp = "__copy"

// TriggerTarget reads a test's event trigger -- `c.inc.click()`, a call the
// checker tagged with an Event -- off the call's receiver as the checker
// resolved it: the variable holding the instance under test, the `#id` of the
// node whose event it fires, and the index into each loop around that node,
// outermost first, for `c.cell[1][0].click()`.
func TriggerTarget(c *Call) (recv, id string, path []Expr, ok bool) {
	if c == nil || c.Event == "" {
		return "", "", nil, false
	}
	r := c.Receiver
	// A receiver read after a null test is the narrowing of it.
	for conv, narrowed := r.(*Conversion); narrowed; conv, narrowed = r.(*Conversion) {
		r = conv.Operand
	}
	for ix, indexed := r.(*Index); indexed; ix, indexed = r.(*Index) {
		path = append([]Expr{ix.Idx}, path...)
		r = ix.Operand
	}
	sel, isSel := r.(*Select)
	if !isSel {
		return "", "", nil, false
	}
	base, isIdent := sel.Operand.(*Ident)
	if !isIdent {
		return "", "", nil, false
	}
	return base.Name, sel.Field, path, true
}

// IndexedTriggerIDs is every `#id` a test triggers one copy of --
// `c.row[1].change(true)`.
func IndexedTriggerIDs(pkg *Package) map[string]bool {
	out := map[string]bool{}
	if pkg == nil {
		return out
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = Walk(fn.Block, func(n Node) error {
			if c, ok := n.(*Call); ok {
				if _, id, path, ok := TriggerTarget(c); ok && len(path) > 0 {
					out[id] = true
				}
			}
			return nil
		})
	}
	return out
}

// CopyKey is the key of one copy of a node loops rendered: the index into each
// loop, outermost first, joined by "/", so one loop's copy 1 is "1" and the
// first copy in a nested loop's second iteration is "1/0".
func CopyKey(indices []Expr) Expr {
	if len(indices) == 0 {
		return nil
	}
	if lits := literalIndices(indices); lits != nil {
		return &Literal{Type: TypString, Value: strings.Join(lits, "/")}
	}
	var key Expr
	for _, idx := range indices {
		part := Expr(&Conversion{Type: TypString, Operand: idx})
		if key == nil {
			key = part
			continue
		}
		key = JoinCopyKey(key, part)
	}
	return key
}

// JoinCopyKey is the key of a copy inside the copy prefix names.
func JoinCopyKey(prefix, key Expr) Expr {
	return &Binary{Type: TypString, Op: ast.BinAdd, Left: &Binary{Type: TypString, Op: ast.BinAdd, Left: prefix, Right: &Literal{Type: TypString, Value: "/"}}, Right: key}
}

// literalIndices is each index spelled as written, or nil unless every one is
// an int literal: a test's path is, and its key is then a literal too.
func literalIndices(indices []Expr) []string {
	out := make([]string, len(indices))
	for i, idx := range indices {
		lit, ok := idx.(*Literal)
		if !ok || lit.Type == nil || lit.Type.Kind != TypeInt {
			return nil
		}
		out[i] = lit.Value
	}
	return out
}
