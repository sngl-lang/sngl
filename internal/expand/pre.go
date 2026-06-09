package expand

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ExpandPre runs pre-check macro expansion on docs, replacing AttrDecl nodes
// with the transformed inner declarations. Returns diagnostics for any errors.
// Modifies docs in place.
func ExpandPre(docs []*ast.Document) []ir.Diagnostic {
	aliases := imports.ResolveAliases(docs)
	var diags []ir.Diagnostic

	for _, doc := range docs {
		var newStmts []ast.Stmt
		for _, stmt := range doc.Stmts {
			ad, ok := stmt.(*ast.AttrDecl)
			if !ok {
				newStmts = append(newStmts, stmt)
				continue
			}
			result, attrDiags := applyAttrs(ad, aliases)
			diags = append(diags, attrDiags...)
			newStmts = append(newStmts, result)
		}
		doc.Stmts = newStmts
	}
	return diags
}

func applyAttrs(ad *ast.AttrDecl, aliases map[string]imports.ImportRef) (ast.Stmt, []ir.Diagnostic) {
	decl := ad.Inner
	var diags []ir.Diagnostic

	for _, attr := range ad.Attrs {
		ref, aliasKnown := aliases[attr.Alias]

		if !aliasKnown {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("unresolved import alias %q in macro attribute", attr.Alias),
				Severity: ir.Error,
			})
			continue
		}

		if ref.Scheme != "internal" {
			continue
		}

		handler, found := lookupPre(ref.URI, attr.Name)
		if !found {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("unknown macro %q in package %q", attr.Name, ref.URI),
				Severity: ir.Error,
			})
			continue
		}

		result, err := handler(attr, decl)
		if err != nil {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      err.Error(),
				Severity: ir.Error,
			})
			continue
		}

		if fmt.Sprintf("%T", result) != fmt.Sprintf("%T", decl) {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("macro %s.%s changed declaration kind from %T to %T", attr.Alias, attr.Name, decl, result),
				Severity: ir.Error,
			})
			continue
		}

		decl = result
	}

	return decl, diags
}
