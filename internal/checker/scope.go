package checker

import (
	"errors"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

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

// declareMethod makes fn a member of the declaration its receiver names,
// reporting an unknown receiver at pos. Returns the member it collides with
// when the caller must report a duplicate, and nil when the attach stands:
// because it succeeded, because it deliberately shadows a standard-library
// member (which user code may do), or because the receiver carries no member
// list — a namespace, whose members are the declarations of its package.
func (c *checker) declareMethod(pos ast.Pos, fn *ir.Func) *ir.Func {
	err := ir.AttachMethod(c.scope, fn.Receiver, fn)
	switch {
	case err == nil, errors.Is(err, ir.ErrNoMethodHost):
		return nil
	case errors.Is(err, ir.ErrUnknownReceiver):
		// Without a receiver to attach to there is no member list to hold the
		// method, so it could never be found again.
		c.error(pos, "undefined: %s (no type to declare method %q on)", fn.Receiver, fn.Name)
		return nil
	}
	var red *ir.RedeclaredError
	if !errors.As(err, &red) {
		return nil
	}
	prev, isFunc := red.Prev.(*ir.Func)
	if isFunc && prev.Stdlib && !fn.Stdlib {
		if err := ir.ReplaceMethod(c.scope, fn.Receiver, fn); err != nil {
			return nil
		}
		return nil
	}
	return prev
}
