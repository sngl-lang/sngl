package expand

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// ExpandPre runs pre-check macro expansion on docs, replacing AttrDecl nodes
// with the transformed inner declarations. Returns diagnostics for any errors.
// Modifies docs in place.
func ExpandPre(docs []*ast.Document) []ir.Diagnostic {
	aliases := imports.ResolveAliases(docs)
	dotPkgs := imports.DotMacroPackages(docs)
	var diags []ir.Diagnostic

	for _, doc := range docs {
		doc.Stmts, diags = expandStmts(doc.Stmts, aliases, dotPkgs, diags)
		for _, stmt := range doc.Stmts {
			diags = expandNested(stmt, aliases, dotPkgs, diags)
		}
	}
	return diags
}

// expandStmts replaces every attributed declaration in a statement list with
// what its marks made of it.
func expandStmts(stmts []ast.Stmt, aliases map[string]imports.ImportRef, dotPkgs []string, diags []ir.Diagnostic) ([]ast.Stmt, []ir.Diagnostic) {
	for i, stmt := range stmts {
		ad, ok := stmt.(*ast.AttrDecl)
		if !ok {
			continue
		}
		result, attrDiags := applyAttrs(ad, aliases, dotPkgs)
		diags = append(diags, attrDiags...)
		stmts[i] = result
	}
	return stmts, diags
}

// expandNested applies the marks written on declarations a top-level one
// contains: a struct's fields and methods, a component's. Neither is a
// top-level declaration, so neither is reached from the document — but both go
// through the same AttrDecl and the same handlers, so a mark means the same
// thing wherever it is written.
//
// A body a mark can be written in has to be reached here. One that is not
// leaves the AttrDecl standing, and the checker then reads a wrapper where a
// declaration should be: the declaration is never registered and the name it
// binds is undefined.
func expandNested(stmt ast.Stmt, aliases map[string]imports.ImportRef, dotPkgs []string, diags []ir.Diagnostic) []ir.Diagnostic {
	switch s := ast.UnwrapAttrs(stmt).(type) {
	case *ast.StructDef:
		for i, item := range s.Body {
			ad, ok := item.(*ast.AttrDecl)
			if !ok {
				continue
			}
			result, attrDiags := applyAttrs(ad, aliases, dotPkgs)
			diags = append(diags, attrDiags...)
			if inner, ok := result.(ast.StructBodyItem); ok {
				s.Body[i] = inner
			}
		}
	case *ast.ComponentDecl:
		diags = expandBlock(&s.Body, aliases, dotPkgs, diags)
	case *ast.PlatformStmt:
		diags = expandBlock(&s.Body, aliases, dotPkgs, diags)
	}
	return diags
}

// expandBlock expands a statement list and everything nested in it.
func expandBlock(b *ast.StmtBlock, aliases map[string]imports.ImportRef, dotPkgs []string, diags []ir.Diagnostic) []ir.Diagnostic {
	b.Stmts, diags = expandStmts(b.Stmts, aliases, dotPkgs, diags)
	for _, inner := range b.Stmts {
		diags = expandNested(inner, aliases, dotPkgs, diags)
	}
	return diags
}

func applyAttrs(ad *ast.AttrDecl, aliases map[string]imports.ImportRef, dotPkgs []string) (ast.Stmt, []ir.Diagnostic) {
	decl := ad.Inner
	var diags []ir.Diagnostic

	for _, attr := range ad.Attrs {
		ref, aliasKnown := aliases[attr.Alias]

		// Resolve the macro package. Only a sngl:// import maps an alias to a
		// macro package; any other alias is not a macro and is
		// left untouched. An alias that names nothing imported is an error
		// rather than an ambient lookup — a macro package is a dependency, and
		// resolving it from the bare name would make `#[draw.shape]` mean
		// something different depending on what happened to be registered.
		//
		// A lib package may carry macros alongside its declarations, which is
		// why sngl:// is not restricted to the internal/ prefix: sngl://draw
		// ships the `shape` mark next to the components it applies to.
		var uri string
		switch {
		case aliasKnown && (ref.Scheme == "internal" || ref.Scheme == "sngl"):
			if !HasPackage(ref.URI) {
				diags = append(diags, ir.Diagnostic{
					Pos:      attr.Pos,
					Msg:      fmt.Sprintf("package %q declares no macros", ref.Scheme+"://"+ref.URI),
					Severity: ir.Error,
				})
				continue
			}
			uri = ref.URI
		case aliasKnown:
			continue
		case attr.Alias == "":
			// Unqualified `#[name]` resolves against the dot-imported macro
			// packages, the same way an unqualified declaration does.
			found := false
			for _, pkg := range dotPkgs {
				if _, ok := lookupPre(pkg, attr.Name); ok {
					uri, found = pkg, true
					break
				}
			}
			if !found {
				diags = append(diags, ir.Diagnostic{
					Pos:      attr.Pos,
					Msg:      fmt.Sprintf("unknown macro %q: no dot-imported macro package declares it", attr.Name),
					Severity: ir.Error,
				})
				continue
			}
		default:
			// Name the package the macro actually lives in, so the suggestion
			// is a line the user can paste. A macro package is a library
			// package like any other; the compiler's own live under
			// internal/, which is where an unknown one most likely belongs.
			suggest := "sngl://internal/" + attr.Alias
			if lib.HasPackage(attr.Alias) {
				suggest = "sngl://" + attr.Alias
			}
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("unknown macro package %q: import it with import %q", attr.Alias, suggest),
				Severity: ir.Error,
			})
			continue
		}

		macro, found := lookupPre(uri, attr.Name)
		if !found {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      unknownMacroMsg(attr.Name, uri),
				Severity: ir.Error,
			})
			continue
		}

		args, err := evalArgs(macro.params, attr.Args)
		if err != nil {
			diags = append(diags, ir.Diagnostic{
				Pos:      attr.Pos,
				Msg:      fmt.Sprintf("macro %s: %s", macroName(attr), err),
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
				Msg:      fmt.Sprintf("macro %s changed declaration kind from %T to %T", macroName(attr), decl, result),
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

// macroName renders an attribute for diagnostics. A bare macro (no package
// segment, e.g. #[builtin]) has an empty alias, so the dotted form would print
// as ".builtin".
func macroName(attr ast.MacroAttr) string {
	if attr.Alias == "" {
		return attr.Name
	}
	return attr.Alias + "." + attr.Name
}

func unknownMacroMsg(name, uri string) string {
	if uri == "" {
		return fmt.Sprintf("unknown macro %q", name)
	}
	return fmt.Sprintf("unknown macro %q in package %q", name, uri)
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
	case *ast.StructField:
		d.Pos = pos
	}
}
