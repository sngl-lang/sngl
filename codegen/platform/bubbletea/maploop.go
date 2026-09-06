package bubbletea

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// mapRange is the head of one ordered walk over a map, split by where each
// line goes: Pre before the loop, Head the `for ... {` itself, Inner the first
// lines of the body.
type mapRange struct {
	Pre     []string
	Head    string
	Inner   []string
	Imports []string
}

// sortedMapRange walks a map in key order. View() and Update() range over the
// same map in separate executions, which Go orders differently each time, so
// only a total order on the keys leaves ordinal N naming one entry in both.
//
// keyVar/valVar are "" where the caller needs no binding; tmp is a name free in
// the caller's scope.
func sortedMapRange(iterExpr, tmp, keyVar, valVar string, keyType *ir.Type) mapRange {
	mr := mapRange{Imports: []string{"maps", "slices"}}
	src := iterExpr
	if valVar != "" {
		// The value comes back by lookup, so the map is bound once: iterExpr may
		// be a call, and the head would otherwise re-evaluate it per iteration.
		src = tmp
		mr.Pre = append(mr.Pre, fmt.Sprintf("%s := %s", tmp, iterExpr))
		if keyVar == "" {
			keyVar = tmp + "Key"
		}
		mr.Inner = append(mr.Inner, fmt.Sprintf("%s := %s[%s]", valVar, tmp, keyVar))
	}
	keys := fmt.Sprintf("slices.Sorted(maps.Keys(%s))", src)
	if !goOrderedKey(keyType) {
		mr.Imports = append(mr.Imports, "cmp", "fmt")
		keys = fmt.Sprintf(
			"slices.SortedStableFunc(maps.Keys(%s), func(a, b %s) int { return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b)) })",
			src, golang.IRTypeToGo(keyType))
	}
	if keyVar == "" {
		mr.Head = fmt.Sprintf("for range %s {", keys)
	} else {
		mr.Head = fmt.Sprintf("for _, %s := range %s {", keyVar, keys)
	}
	return mr
}

// goOrderedKey reports whether the Go type this key lowers to satisfies
// cmp.Ordered, i.e. whether slices.Sorted can take it. bool, structs and
// multi-base units cannot; they order by their rendered form instead.
func goOrderedKey(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeInt, ir.TypeFloat, ir.TypeString, ir.TypeEnum:
		return true
	case ir.TypeUnit:
		ud, ok := t.Decl.(*ir.UnitDef)
		return ok && ud != nil && golang.ClassifyUnit(ud) != golang.UnitMultiBase
	}
	return false
}

// mapKeyType is the key type of a map-typed iterable, or nil.
func mapKeyType(e ir.Expr) *ir.Type {
	if e == nil {
		return nil
	}
	t := e.ExprType()
	if t == nil || t.Kind != ir.TypeMap || len(t.Elems) != 2 {
		return nil
	}
	return t.Elems[0]
}

// loopVarUsed reports whether block references the loop variable named name.
// The head binds it only then, since Go rejects an unused one.
func loopVarUsed(block []ir.Stmt, name string, sym ir.Symbol) bool {
	if name == "" || name == "_" {
		return false
	}
	found := false
	_ = ir.Walk(block, func(n ir.Node) error {
		id, ok := n.(*ir.Ident)
		if !ok {
			return nil
		}
		if (sym != nil && id.Sym == sym) || id.Name == name {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// orDiscard spells an unbound loop position as Go's blank identifier.
func orDiscard(name string) string {
	if name == "" {
		return "_"
	}
	return name
}
