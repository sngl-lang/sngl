package expand

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ArgKind is the declared type of a macro parameter. The expand framework
// evaluates each argument to its kind before invoking the handler, so handlers
// receive validated values rather than raw expressions.
type ArgKind int

const (
	ArgString ArgKind = iota // constant string (ast.EvalString)
	ArgInt                   // constant integer (ast.EvalInt)
	ArgIdent                 // a bare identifier; the handler gets its name
	ArgExpr                  // any expression, passed through unevaluated
)

// Param declares one macro parameter. Optional parameters must follow required
// ones (arguments are positional).
type Param struct {
	Name     string
	Kind     ArgKind
	Optional bool
	// Variadic consumes every remaining argument. It must be the last
	// parameter, and is implicitly optional — a variadic with no arguments is
	// an empty list, not a missing one. Read with Args.Idents.
	Variadic bool
	// Enum, for ArgIdent, is the set of identifiers the argument may name.
	// A mark that spells a flag wrong should say so with the alternatives
	// rather than be silently ignored.
	Enum []string
}

// PreHandler transforms a declaration before type-checking. It receives the
// macro's evaluated, signature-checked arguments.
type PreHandler func(args Args, decl ast.Stmt) (ast.Stmt, error)

type preMacro struct {
	params  []Param
	handler PreHandler
}

var (
	mu      sync.RWMutex
	preRegs = map[string]map[string]preMacro{}
)

// RegisterPre registers a pre-check macro under the given internal URI and name
// with a parameter signature. The framework checks arity and evaluates each
// argument against its declared kind before calling h.
func RegisterPre(internalURI, name string, params []Param, h PreHandler) {
	mu.Lock()
	defer mu.Unlock()
	if preRegs[internalURI] == nil {
		preRegs[internalURI] = map[string]preMacro{}
	}
	preRegs[internalURI][name] = preMacro{params: params, handler: h}
}

// HasPackage reports whether any macro is registered under the given package
// URI. Lets the checker validate a macro-package import without knowing what
// the package contains.
func HasPackage(uri string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := preRegs[uri]
	return ok
}

func lookupPre(uri, name string) (preMacro, bool) {
	mu.RLock()
	defer mu.RUnlock()
	if m, ok := preRegs[uri]; ok {
		pm, ok := m[name]
		return pm, ok
	}
	return preMacro{}, false
}
