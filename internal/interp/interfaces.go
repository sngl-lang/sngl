package interp

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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
	SetField(op ast.AssignOp, field string, val any) error
	// InvokeMethod attempts to dispatch `method` on the component. Returns
	// (result, true, err) when the method exists (including `@event` no-ops);
	// (nil, false, nil) when the component has no such method. The caller may
	// then fall back to other dispatch paths.
	InvokeMethod(env *Env, method string, args []ir.Expr) (any, bool, error)
	// Toggle flips a boolean field, propagating to the underlying env.
	Toggle(field string) error
	// WriteBackList sets a list field after in-place list mutation.
	WriteBackList(field string, list []any) error
}
