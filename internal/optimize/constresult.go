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
//
// The second result carries the keys that failed, one error each: a value that
// does not check says nothing about the next key, which came from a different
// function with its own declared type. Only an unparseable document — real
// corruption — is returned as a whole-batch error.
func parseConstResults(path string, src []byte, want map[string]*ir.Type) (map[string]ir.Expr, map[string]error, error) {
	doc, err := parser.Parse(path, src)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing const evaluator results: %w", err)
	}
	out := map[string]ir.Expr{}
	bad := map[string]error{}
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
				bad[key] = err
				continue
			}
			out[key] = e
		}
	}
	return out, bad, nil
}
