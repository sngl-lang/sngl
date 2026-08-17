package expand

import (
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
)

// PreHandler transforms an AST declaration before type-checking.
type PreHandler func(attr ast.MacroAttr, decl ast.Stmt) (ast.Stmt, error)

var (
	mu      sync.RWMutex
	preRegs = map[string]map[string]PreHandler{}
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

func lookupPre(uri, name string) (PreHandler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	if m, ok := preRegs[uri]; ok {
		h, ok := m[name]
		return h, ok
	}
	return nil, false
}
