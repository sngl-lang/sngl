package expand

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// PreHandler transforms an AST declaration before type-checking.
type PreHandler func(attr ast.MacroAttr, decl ast.Stmt) (ast.Stmt, error)

// PostHandler transforms an IR symbol after type-checking.
type PostHandler func(attr ast.MacroAttr, sym ir.Symbol) (ir.Symbol, error)

var (
	mu       sync.RWMutex
	preRegs  = map[string]map[string]PreHandler{}
	postRegs = map[string]map[string]PostHandler{}
)

// RegisterPre registers a pre-check macro handler for the given internal URI and name.
func RegisterPre(internalURI, name string, h PreHandler) {
	mu.Lock()
	defer mu.Unlock()
	if preRegs[internalURI] == nil {
		preRegs[internalURI] = map[string]PreHandler{}
	}
	preRegs[internalURI][name] = h
}

// RegisterPost registers a post-check macro handler for the given internal URI and name.
func RegisterPost(internalURI, name string, h PostHandler) {
	mu.Lock()
	defer mu.Unlock()
	if postRegs[internalURI] == nil {
		postRegs[internalURI] = map[string]PostHandler{}
	}
	postRegs[internalURI][name] = h
}

func lookupPre(uri, name string) (PreHandler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	if m, ok := preRegs[uri]; ok {
		h, ok := m[name]
		return h, ok
	}
	return nil, false
}
