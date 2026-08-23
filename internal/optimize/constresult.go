package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// parseConstResults reads the document pkg/go/consteval wrote — one
// `const <key> = <expr>` per evaluated call — and checks each initializer
// against the declared return type of the function that produced it.
//
// The document is SNGL source, so a result becomes IR the same way a
// hand-written literal does. want carries the return types, keyed as the
// consts are; a key with no entry (a call that returns nothing) is checked
// with no expectation.
func parseConstResults(path string, src []byte, want map[string]*ir.Type) (map[string]ir.Expr, error) {
	doc, err := parser.Parse(path, src)
	if err != nil {
		return nil, fmt.Errorf("parsing const evaluator results: %w", err)
	}
	out := map[string]ir.Expr{}
	for _, stmt := range doc.Stmts {
		decl, ok := stmt.(*ast.ConstDecl)
		if !ok {
			continue
		}
		for _, spec := range decl.Specs {
			if len(spec.Names) != 1 {
				continue
			}
			key := spec.Names[0]
			e, err := checker.CheckNativeValue(spec.Default, want[key])
			if err != nil {
				// A value that does not check means the encoder and the
				// importer disagree about the Go type, which no other key in
				// the batch can be trusted to have escaped. Fail the batch so
				// the message reaches the build rather than blanking one const.
				return nil, fmt.Errorf("const evaluator result %q: %w", key, err)
			}
			out[key] = e
		}
	}
	return out, nil
}
