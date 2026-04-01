package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

func parseGoAST(source string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "input.go", source, parser.AllErrors)
	if err != nil {
		return []string{"Error: " + err.Error()}
	}

	var nodes []string
	var depth int
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			depth--
			return false
		}
		pos := fset.Position(n.Pos())
		indent := strings.Repeat("  ", depth)
		typeName := fmt.Sprintf("%T", n)
		// Strip the *ast. prefix for readability.
		typeName = strings.TrimPrefix(typeName, "*ast.")
		line := fmt.Sprintf("%s%s (line %d)", indent, typeName, pos.Line)
		nodes = append(nodes, line)
		depth++
		return true
	})
	return nodes
}
