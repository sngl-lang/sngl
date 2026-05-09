package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// TestPlatformOverridesHaveQualifiedNames verifies that the parser preserves
// dotted component names (e.g. "sngl.vbox") in platform .sngl override files.
// Before the parser grammar accepted `[ dot ident ]` in ComponentDecl, the
// dot+suffix was silently discarded by parser error recovery, leaving every
// override component named just "sngl".
func TestPlatformOverridesHaveQualifiedNames(t *testing.T) {
	gen := codegen.LookupPlatform("android")
	if gen == nil {
		t.Fatal("platform android not registered")
	}
	docs := gen.Package()
	if len(docs) == 0 {
		t.Fatal("no Package() documents")
	}
	found := false
	for _, doc := range docs {
		for _, s := range doc.Stmts {
			c, ok := s.(*ast.ComponentDecl)
			if !ok {
				continue
			}
			if strings.Contains(c.Name, ".") {
				found = true
				t.Logf("component %s", c.Name)
			}
		}
	}
	if !found {
		t.Fatal("no qualified-name component decls — parser still stripping dots?")
	}
}
