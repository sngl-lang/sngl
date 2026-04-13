package golang

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// CompileHTTP implements codegen.HTTPCompiler for Go.
// TODO: Port to v2 AST — requires Doc.Stmts iteration instead of Doc.App/Data/NativeImports.
func (t *Translator) CompileHTTP(req *codegen.HTTPRequest) ([]byte, error) {
	return nil, fmt.Errorf("golang HTTP compiler not yet ported to v2 AST")
}
