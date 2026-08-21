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
		var newStmts []ast.Stmt
		for _, stmt := range doc.Stmts {
			ad, ok := stmt.(*ast.AttrDecl)
			if !ok {
				newStmts = append(newStmts, stmt)
				continue
			}
			result, attrDiags := applyAttrs(ad, aliases, dotPkgs)
			diags = append(diags, attrDiags...)
			newStmts = append(newStmts, result)
		}
		doc.Stmts = newStmts
	}
	return diags
}

func applyAttrs(ad *ast.AttrDecl, aliases map[string]imports.ImportRef, dotPkgs []string) (ast.Stmt, []ir.Diagnostic) {
	decl := ad.Inner
	var diags []ir.Diagnostic

	for _, attr := range ad.Attrs {
		ref, aliasKnown := aliases[attr.Alias]

		// Resolve the macro package. Only an internal:// or sngl:// import maps
		// an alias to a macro package; any other alias is not a macro and is
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
			// Name the scheme the package actually lives under, so the
			// suggestion is a line the user can paste.
			suggest := "internal://" + attr.Alias
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
	}
}
