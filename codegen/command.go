package codegen

import (
	"sync"

	"duckfam.us/sngl/ir"
)

// CommandFunc answers a build-only intrinsic a target's command handler calls
// -- `gen.run`'s or `gen.build`'s, in the body of its build-tree node. opts are
// the options the target was built with, and args the call's arguments in
// parameter order, as the interpreter holds them.
//
// It is how a command reaches what only Go can do for it: writing a go.mod
// against the host checkout, serving a directory, driving an emulator. Such a
// call is a name the target's package declares with
// `#[marks.intrinsic("<id>", build)]`, so the handler that calls it reads as
// SNGL and the Go behind it is one function.
type CommandFunc func(opts *ir.StructLit, args []any) (any, error)

var (
	commandsMu sync.RWMutex
	commands   = map[string]CommandFunc{}
)

// RegisterCommand makes fn the answer to the intrinsic id in a command
// handler. Called from a target's init.
func RegisterCommand(id string, fn CommandFunc) {
	commandsMu.Lock()
	defer commandsMu.Unlock()
	commands[id] = fn
}

// LookupCommand is the answer registered for id, or nil.
func LookupCommand(id string) CommandFunc {
	commandsMu.RLock()
	defer commandsMu.RUnlock()
	return commands[id]
}
