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

		// Resolve the macro package. An explicit internal:// import maps the
		// alias to a package URI; a non-internal import alias is not a macro
		// and is left untouched. When the alias is not imported at all, it is
		// treated as an ambient macro-package name so registered macros (e.g.
		// builtin, canvas) work without an internal:// import.
		var uri string
		switch {
		case aliasKnown && ref.Scheme == "internal":
			uri = ref.URI
		case aliasKnown:
			continue
		default:
			uri = attr.Alias
		}

		macro, found := lookupPre(uri, attr.Name)
		if !found {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("unknown macro %q in package %q", attr.Name, uri),
				Severity: ir.Error,
			})
			continue
		}

		args, err := evalArgs(macro.params, attr.Args)
		if err != nil {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("macro %s.%s: %s", attr.Alias, attr.Name, err),
				Severity: ir.Error,
			})
			continue
		}

		result, err := macro.handler(args, decl)
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

	// Carry the attribute block's start position onto the unwrapped decl so a
	// doc comment written above the #[...] attributes still reads as adjacent to
	// the declaration (the attribute lines would otherwise leave a gap that
	// breaks doc-comment association after the AttrDecl is replaced).
	inheritPos(decl, ad.Pos)

	return decl, diags
}

// inheritPos moves a declaration's start position to pos (the attribute block's
// position). Only the decl kinds that can carry #[...] attributes and have doc
// comments are handled.
func inheritPos(decl ast.Stmt, pos ast.Pos) {
	switch d := decl.(type) {
	case *ast.StructDef:
		d.Pos = pos
	case *ast.ComponentDecl:
		d.Pos = pos
	case *ast.EnumDef:
		d.Pos = pos
	case *ast.FuncDef:
		d.Pos = pos
	case *ast.VarDecl:
		d.Pos = pos
	case *ast.ConstDecl:
		d.Pos = pos
	case *ast.UnitDef:
		d.Pos = pos
	}
}
