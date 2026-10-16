package interp

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// TestingT is the runtime interface for the SNGL `Test` parameter. The
// concrete *testingT implementation lives in this package; eval.go uses this
// interface so the interpreter code can be moved to internal/interp without
// taking testrunner along.
type TestingT interface {
	CallMethod(env *Env, method string, args []ir.Expr) (any, error)
}

// ComponentValue is the runtime interface for the SNGL `Component` parameter
// passed into a test function. Eval/exec call into this interface for field
// reads, field writes, method dispatch, and list/toggle writebacks.
type ComponentValue interface {
	GetField(field string) (any, error)
	// ElementRef is every node `#id` names in this instance's own body, in mount
	// order -- or, with supplied set, in a population this instance wrote for
	// that one: an element as its props, a component of the program's as its
	// own value. The read's ir.RefCount says what shape the caller gets.
	ElementRef(id string, supplied *Env) (RefSet, error)
	// Populated is the instance whose body rendered this one, and so wrote the
	// populations it was handed -- nil for the instance a test was given --
	// this one's own scope, which those populations were supplied to, and the
	// node that rendered it.
	Populated() (by ComponentValue, inst *Env, node *ir.NodeInst)
	SetField(op ast.AssignOp, field string, val any) error
	// InvokeMethod attempts to dispatch `method` on the component. Returns
	// (result, true, err) when the method exists (including `@event` no-ops);
	// (nil, false, nil) when the component has no such method. The caller may
	// then fall back to other dispatch paths. Args carry the original
	// ir.CallArg values (including Name fields) so named/default dispatch works.
	InvokeMethod(env *Env, method string, args []ir.CallArg) (any, bool, error)
	// Toggle flips a boolean field, propagating to the underlying env.
	Toggle(field string) error
	// WriteBackList sets a list field after in-place list mutation.
	WriteBackList(field string, list []any) error
	// FieldCell is the binding a field names and the scope that owns it, or
	// false for a field that is no binding.
	FieldCell(field string) (owner *Env, sym ir.Symbol, ok bool)
}
