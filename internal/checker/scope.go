package checker

import "git.duckfam.us/jonathan/sngl/ir"

//go:fix inline
type Symbol = ir.Symbol

//go:fix inline
type LoopVar = ir.LoopVar

//go:fix inline
type Namespace = ir.Namespace

//go:fix inline
type Scope = ir.Scope

//go:fix inline
type TypeSym = ir.TypeSym

//go:fix inline
type SymbolTable = ir.SymbolTable

//go:fix inline
var NewScope = ir.NewScope

//go:fix inline
var NewSymbolTable = ir.NewSymbolTable

// structDecl returns the struct declaration named name, or nil when the name
// is unknown or names something other than a struct.
func structDecl(st *ir.SymbolTable, name string) *ir.StructDef {
	if sym, ok := st.LookupType(name); ok {
		if sd, isStruct := sym.(*ir.StructDef); isStruct {
			return sd
		}
	}
	return nil
}

// lookupMethod finds a method on the declaration recv names, resolved through
// the scope the checker is currently in. That is the scope AttachMethod wrote
// through, so the two always agree on which declaration a receiver name means.
func (c *checker) lookupMethod(recv, method string) (*ir.Func, bool) {
	return ir.LookupMethodIn(c.scope, recv, method)
}
