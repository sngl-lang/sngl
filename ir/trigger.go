package ir

import (
	"strings"

	"duckfam.us/sngl/ast"
)

// CopyProp is the prop a node a test indexes carries the key of its copy in,
// and the prop a component built at run time is handed the key of the copy it
// stands in, which its own copies' keys extend.
const CopyProp = "__copy"

// InstanceRootProp is the prop each root node of a spliced component instance
// a test null-tests carries, on a target that finds a node by what it carries
// rather than by the roots NodeInst.Instances names: its value is the key of
// the copy the instance stands in (CopyKey), "" for one rendered once. A root
// of several nested instances carries one per id.
func InstanceRootProp(id string) string { return InstanceRootPrefix + id }

// InstanceRootPrefix is what every InstanceRootProp begins with.
const InstanceRootPrefix = "__instance_"

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

// TriggeredIDs is every `#id` a test fires an event on, `c.save.click()`, of
// one copy or of the node.
func TriggeredIDs(pkg *Package) map[string]bool {
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
				if _, id, _, ok := TriggerTarget(c); ok {
					out[id] = true
				}
			}
			return nil
		})
	}
	return out
}

// IndexedRefIDs is every `#id` a test reaches one copy of -- triggers,
// `c.row[1].change(true)`, null-tests, `c.row[1] == null`, and a member of an
// instance read or called through one, `c.row[1].hits` -- by an index into a
// ref to the nodes loops rendered under it.
func IndexedRefIDs(pkg *Package) map[string]bool {
	out := map[string]bool{}
	if pkg == nil {
		return out
	}
	for _, fn := range pkg.Funcs {
		if fn == nil || !fn.IsTest {
			continue
		}
		_ = Walk(fn.Block, func(n Node) error {
			switch x := n.(type) {
			case *Call:
				if _, id, path, ok := TriggerTarget(x); ok && len(path) > 0 {
					out[id] = true
				}
			case *Binary:
				if sel, path, _, ok := NullTestedRef(x); ok && len(path) > 0 {
					out[sel.Field] = true
				}
			case *Select:
				if id, path, ok := InstanceRef(x.Operand); ok && len(path) > 0 {
					out[id] = true
				}
			}
			if c, ok := n.(*Call); ok {
				// A method handed its receiver first -- a function a lowering
				// made of the instance's component -- is called through it too.
				recv := c.Receiver
				if recv == nil && c.Func != nil && len(c.Func.Params) > 0 && c.Func.Params[0].Receiver && len(c.Args) > 0 {
					recv = c.Args[0].Value
				}
				if recv != nil {
					if id, path, ok := InstanceRef(recv); ok && len(path) > 0 {
						out[id] = true
					}
				}
			}
			return nil
		})
	}
	return out
}

// InstanceRef reads e as a test's ref to a node, `c.b` or one copy of it,
// `c.row[1]`, read after a null test or not: its id and the index into each
// loop around it.
func InstanceRef(e Expr) (id string, path []Expr, ok bool) {
	e = stripConversions(e)
	for ix, isIx := e.(*Index); isIx; ix, isIx = e.(*Index) {
		path = append([]Expr{ix.Idx}, path...)
		e = stripConversions(ix.Operand)
	}
	sel, isSel := e.(*Select)
	if !isSel || sel.Ref == RefNone || len(sel.Ref) != len(path)+1 && sel.Ref[len(path)] != RefOption {
		return "", nil, false
	}
	if _, isIdent := sel.Operand.(*Ident); !isIdent {
		return "", nil, false
	}
	return sel.Field, path, true
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

// NullTestedRef reads a test's `c.x == null` or `c.x != null`, either way
// round, where `c.x` is a ref to a node -- or `c.x[1]`, one copy of a node
// loops rendered, under an `if` or not, whose path holds the index into each
// loop. present is true for `!=`.
func NullTestedRef(e Expr) (sel *Select, path []Expr, present, ok bool) {
	sel, path, present, ok = NullTestedCopy(e)
	if !ok {
		return nil, nil, false, false
	}
	if _, isIdent := sel.Operand.(*Ident); !isIdent {
		return nil, nil, false, false
	}
	return sel, path, present, true
}

// NullTestedCopy is NullTestedRef through any operand: `c.r[1].cl[2] ==
// null` null-tests a copy of a node an instance a test reached renders.
func NullTestedCopy(e Expr) (sel *Select, path []Expr, present, ok bool) {
	bin, isBin := e.(*Binary)
	if !isBin || (bin.Op != ast.BinEq && bin.Op != ast.BinNeq) {
		return nil, nil, false, false
	}
	ref, other := bin.Left, bin.Right
	if IsNullLiteral(ref) {
		ref, other = other, ref
	}
	if !IsNullLiteral(other) {
		return nil, nil, false, false
	}
	ref = stripConversions(ref)
	for ix, isIx := ref.(*Index); isIx; ix, isIx = ref.(*Index) {
		path = append([]Expr{ix.Idx}, path...)
		ref = stripConversions(ix.Operand)
	}
	sel, isSel := ref.(*Select)
	if !isSel || len(sel.Ref) < len(path)+1 {
		return nil, nil, false, false
	}
	// A copy past the copies its loops rendered is not rendered, so a copy is
	// null-tested whether or not anything inside the loops decides it. A node
	// rendered once is RenderedNullTest's.
	if rest := sel.Ref[len(path):]; rest[0] != RefOption && (len(path) == 0 || rest != RefOne) {
		return nil, nil, false, false
	}
	for i := range path {
		if sel.Ref[i] != RefList {
			return nil, nil, false, false
		}
	}
	return sel, path, bin.Op == ast.BinNeq, true
}

// RenderedNullTest reads a test's null test of a ref to a node its component
// renders exactly once, whose answer is the operator's: such a ref is never
// null, and nothing on a target answers for it at run time.
func RenderedNullTest(e Expr) (present, ok bool) {
	bin, isBin := e.(*Binary)
	if !isBin || (bin.Op != ast.BinEq && bin.Op != ast.BinNeq) {
		return false, false
	}
	ref, other := bin.Left, bin.Right
	if IsNullLiteral(ref) {
		ref, other = other, ref
	}
	if !IsNullLiteral(other) {
		return false, false
	}
	sel, isSel := stripConversions(ref).(*Select)
	if !isSel || sel.Ref != RefOne {
		return false, false
	}
	if _, isIdent := sel.Operand.(*Ident); !isIdent {
		return false, false
	}
	return bin.Op == ast.BinNeq, true
}

// IsNullLiteral reports a `null`: a literal once folded, and the universe
// identifier in checked IR.
func IsNullLiteral(e Expr) bool {
	switch x := stripConversions(e).(type) {
	case *Literal:
		return x.Type != nil && x.Type.Kind == TypeNull
	case *Ident:
		return x.Type != nil && x.Type.Kind == TypeNull
	}
	return false
}

func stripConversions(e Expr) Expr {
	for conv, ok := e.(*Conversion); ok; conv, ok = e.(*Conversion) {
		e = conv.Operand
	}
	return e
}

// EmittedIndex is the loop variable block emits event with when the emit hands
// on that and nothing else -- a tab strip's `@click { selected = k; change(k) }`
// -- or nil. A trigger of event on a component whose body emits it so, from a
// node a loop renders, is a press of that node's copy at the index the
// trigger names: `c.strip.change(1)` presses the second tab.
func EmittedIndex(block []Stmt, event string) *LoopVar {
	var found *LoopVar
	_ = Walk(block, func(n Node) error {
		e, ok := n.(*Emit)
		if !ok || e.Name != event {
			return nil
		}
		if len(e.Args) == 1 {
			if id, ok := e.Args[0].Value.(*Ident); ok {
				if lv, ok := id.Sym.(*LoopVar); ok {
					found = lv
				}
			}
		}
		return SkipDir
	})
	return found
}

// RecordBranchOf is the loop among loops whose element cond picks one record
// of, `t.__id == 3`, or nil. A data family's content is a population branching
// on the record's number (RecordID), and once the container's body is spliced
// that branch sits in the container's loop over its records. Numbers are
// unique, so at most one iteration renders what the branch holds: the loop
// makes no copies of it, and a node written there is counted, as the checker
// and the interpreter count it, by the loops around where it was written.
func RecordBranchOf(cond Expr, loops []*For) *For {
	b, ok := cond.(*Binary)
	if !ok || b.Op != ast.BinEq {
		return nil
	}
	for _, side := range []Expr{b.Left, b.Right} {
		sel, ok := side.(*Select)
		if !ok || sel.Field != RecordID {
			continue
		}
		id, ok := sel.Operand.(*Ident)
		if !ok {
			continue
		}
		for _, f := range loops {
			if f.ValueSym != nil && id.Sym == Symbol(f.ValueSym) {
				return f
			}
		}
	}
	return nil
}
