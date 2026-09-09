package checker_test

import (
	"io/fs"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// An imported document is checked by a checker of its own, and it declares no
// `output` block of its own to resolve a target from -- so whatever the root
// resolved is the only thing it can build for. These are the two ways that
// went wrong when the nested config did not carry it: the target set widened
// to every registered target, and `platform` blocks stopped being filtered.

// subResolver serves one directory import, "./sub".
type subResolver struct{ src string }

func (r subResolver) Resolve(_ fs.FS, path string) ([]*ast.Document, error) {
	doc, err := parser.Parse("sub.sngl", []byte(r.src))
	if err != nil {
		return nil, err
	}
	return []*ast.Document{doc}, nil
}
func (subResolver) ResolveScheme(string, string, string) (*ir.NativeImport, error) {
	return nil, nil
}
func (subResolver) ResolveSchemeFS(string, string, string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

func checkWithSub(t *testing.T, main, sub string, targets ...ir.StaticTarget) []ir.Diagnostic {
	t.Helper()
	doc, err := parser.Parse("main.sngl", []byte(main))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := tgtStubConfig(t, targets...)
	cfg.Resolver = subResolver{src: sub}
	_, diags := checker.Check(doc, cfg)
	return diags
}

func errorsMentioning(diags []ir.Diagnostic, sub string) []string {
	var out []string
	for _, d := range diags {
		if d.Severity == ir.Error && strings.Contains(d.Msg, sub) {
			out = append(out, d.Msg)
		}
	}
	return out
}

// An untargeted platform's overrides stay unloaded no matter how the document
// is reached. Importing a subdirectory used to run a nested check that named
// no target at all, which loaded every registered platform into the build's
// shared cache -- so one import re-broke every build that a target had just
// been narrowed to fix.
func TestImportingASubdirectoryDoesNotWidenTheTargetSet(t *testing.T) {
	const sub = `import . "sngl:ui"

component Sub() ui {
    text(value="sub")
}
`
	const main = `import . "sngl:ui"
import "./sub"

component main ui {
    Sub()
}
`
	diags := checkWithSub(t, main, sub, ir.StaticTarget{Platform: "html"})
	if got := errorsMentioning(diags, "noSuchIdentifierAnywhere"); len(got) > 0 {
		t.Errorf("building for html loaded the tgtstub overrides through the subdirectory import: %v", got)
	}
}
