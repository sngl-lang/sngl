package codegen

import "git.duckfam.us/jonathan/sngl/ast"

// Extraction helpers for v2 Document. The v2 Document only has Stmts []Stmt;
// these helpers provide the v1-style named-slice access patterns that
// downstream code (platforms, cmd) still uses during the migration.

func DocVarDecls(doc *ast.Document) []*ast.VarDecl {
	var out []*ast.VarDecl
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.VarDecl); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocConstDecls(doc *ast.Document) []*ast.ConstDecl {
	var out []*ast.ConstDecl
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.ConstDecl); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocFuncDefs(doc *ast.Document) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, s := range doc.Stmts {
		if f, ok := s.(*ast.FuncDef); ok {
			out = append(out, f)
		}
	}
	return out
}

func DocStructDefs(doc *ast.Document) []*ast.StructDef {
	var out []*ast.StructDef
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.StructDef); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocEnumDefs(doc *ast.Document) []*ast.EnumDef {
	var out []*ast.EnumDef
	for _, s := range doc.Stmts {
		if d, ok := s.(*ast.EnumDef); ok {
			out = append(out, d)
		}
	}
	return out
}

func DocComponents(doc *ast.Document) []*ast.ComponentDecl {
	var out []*ast.ComponentDecl
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.ComponentDecl); ok {
			out = append(out, c)
		}
	}
	return out
}

func DocUnitDefs(doc *ast.Document) []*ast.UnitDef {
	var out []*ast.UnitDef
	for _, s := range doc.Stmts {
		if u, ok := s.(*ast.UnitDef); ok {
			out = append(out, u)
		}
	}
	return out
}

func DocImports(doc *ast.Document) []*ast.Import {
	var out []*ast.Import
	for _, s := range doc.Stmts {
		if i, ok := s.(*ast.Import); ok {
			out = append(out, i)
		}
	}
	return out
}
