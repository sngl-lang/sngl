package testrunner

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
// reads, field writes, and (eventually) method dispatch.
type ComponentValue interface {
	GetField(field string) (any, error)
	SetField(op ast.AssignOp, field string, val any) error
}
