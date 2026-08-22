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
func structDecl(st *SymbolTable, name string) *ir.StructDef {
	if sym, ok := st.LookupType(name); ok {
		if sd, isStruct := sym.(*ir.StructDef); isStruct {
			return sd
		}
	}
	return nil
}
