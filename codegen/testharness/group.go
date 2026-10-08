package testharness

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// GroupIR bins test funcs by the component named in their second
// parameter, working directly off IR Func nodes. Tests whose second
// parameter is missing or not a component-typed param land in the group
// keyed by the empty string.
//
// Returned slice has stable iteration order: components appear in the
// order their first test was encountered. Used by the IR-direct test
// launcher (cmd/sngl/testdriver.go) to avoid round-tripping IR through
// ir.Convert + Promote + re-check, which is lossy for maps, contexts,
// and inferred types.
func GroupIR(testFuncs []*ir.Func) []TestGroup {
	order := []string{}
	idx := map[string]int{}
	for _, fn := range testFuncs {
		comp := componentOfIR(fn)
		if _, ok := idx[comp]; !ok {
			idx[comp] = len(order)
			order = append(order, comp)
		}
	}
	groups := make([]TestGroup, len(order))
	for i, comp := range order {
		groups[i].Component = comp
	}
	for _, fn := range testFuncs {
		comp := componentOfIR(fn)
		groups[idx[comp]].Funcs = append(groups[idx[comp]].Funcs, TestFunc{Name: fn.Name, Component: comp})
	}
	return groups
}

func componentOfIR(fn *ir.Func) string {
	if fn == nil || len(fn.Params) < 2 {
		return ""
	}
	p := fn.Params[1]
	if p == nil || p.Type == nil || p.Type.Decl == nil {
		return ""
	}
	if _, ok := p.Type.Decl.(*ir.Component); !ok {
		return ""
	}
	return p.Type.Decl.SymName()
}

// Group bins test FuncDefs by the component named in their second
// parameter. Tests whose second parameter is missing or not a NamedType
// land in the group keyed by the empty string.
//
// Returned slice has stable iteration order: components appear in the
// order their first test was encountered.
func Group(testFuncs []*ast.FuncDef) []TestGroup {
	order := []string{}
	idx := map[string]int{}
	for _, fn := range testFuncs {
		comp := componentOf(fn)
		if _, ok := idx[comp]; !ok {
			idx[comp] = len(order)
			order = append(order, comp)
		}
	}
	groups := make([]TestGroup, len(order))
	for i, comp := range order {
		groups[i].Component = comp
	}
	for _, fn := range testFuncs {
		comp := componentOf(fn)
		groups[idx[comp]].Funcs = append(groups[idx[comp]].Funcs, TestFunc{Name: fn.Name, Component: comp})
	}
	return groups
}

func componentOf(fn *ast.FuncDef) string {
	if fn == nil || len(fn.Params.Params) < 2 {
		return ""
	}
	nt, ok := fn.Params.Params[1].Type.(*ast.NamedType)
	if !ok {
		return ""
	}
	return nt.Name
}
