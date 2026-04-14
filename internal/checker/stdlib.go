package checker

import (
	"embed"
	"io/fs"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

//go:embed stdlib/*.sngl
var stdlibFS embed.FS

// Cached parsed stdlib ASTs. Parsed once, reused across Check() calls.
var (
	stdlibOnce sync.Once
	stdlibDocs []*ast.Document
)

// stdlibParseOrder determines the loading order so that types/units are
// available before functions and components reference them.
var stdlibParseOrder = []string{
	"stdlib/types.sngl",
	"stdlib/units.sngl",
	"stdlib/functions.sngl",
	"stdlib/components.sngl",
}

// StdlibDocs returns the parsed stdlib documents.
// The results are cached after the first call.
func StdlibDocs() []*ast.Document {
	return parseStdlibDocs()
}

func parseStdlibDocs() []*ast.Document {
	stdlibOnce.Do(func() {
		for _, name := range stdlibParseOrder {
			data, err := fs.ReadFile(stdlibFS, name)
			if err != nil {
				continue
			}
			doc, err := parser.Parse(name, data)
			if err != nil {
				continue
			}
			stdlibDocs = append(stdlibDocs, doc)
		}
	})
	return stdlibDocs
}

// loadStdlib builds the stdlib Package, registers all stdlib declarations into
// the checker's scope and symbol table for unqualified access, and declares
// the "sngl" namespace for qualified access (sngl.text, sngl.Color, etc.).
func (c *checker) loadStdlib() *Package {
	stdlibPkg := &Package{
		TypeMap: make(map[ast.Expr]*Type),
		Symbols: NewSymbolTable(),
	}

	for _, doc := range parseStdlibDocs() {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.StructDef:
				c.registerStdlibStruct(s, stdlibPkg)
			case *ast.UnitDef:
				c.registerStdlibUnit(s, stdlibPkg)
			case *ast.FuncDef:
				c.registerStdlibFunc(s, stdlibPkg)
			case *ast.ComponentDecl:
				c.registerStdlibComponent(s, stdlibPkg)
			}
		}
	}

	// Register "sngl" namespace for qualified access to stdlib.
	c.scope.Declare(&Namespace{
		Name: "sngl",
		Pkg:  stdlibPkg,
	})

	return stdlibPkg
}

func (c *checker) registerStdlibStruct(s *ast.StructDef, pkg *Package) {
	sd := c.buildStructDef(s)
	// Main symtab + scope for unqualified access.
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
	// Stdlib package for qualified sngl.Type access.
	pkg.Structs = append(pkg.Structs, sd)
	pkg.Symbols.Types[sd.Name] = sd
	pkg.Symbols.Root.Declare(sd)
}

func (c *checker) registerStdlibUnit(u *ast.UnitDef, pkg *Package) {
	ud := c.buildUnitDef(u)
	// Main symtab for unqualified access.
	c.symtab.Types[ud.Name] = ud
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
	// Stdlib package.
	pkg.Units = append(pkg.Units, ud)
	pkg.Symbols.Types[ud.Name] = ud
}

func (c *checker) registerStdlibFunc(f *ast.FuncDef, pkg *Package) {
	fn := c.buildFunc(f)
	if fn.Receiver != "" {
		// Type-attached method — registered in main symtab only.
		c.symtab.RegisterMethod(fn.Receiver, fn)
	} else {
		// Free function — available both qualified and unqualified.
		c.scope.Declare(fn)
		pkg.Funcs = append(pkg.Funcs, fn)
		pkg.Symbols.Root.Declare(fn)
	}
}

func (c *checker) registerStdlibComponent(comp *ast.ComponentDecl, pkg *Package) {
	irComp := &Component{
		AST:  comp,
		Name: comp.Name,
		Body: &comp.Body,
		Pos:  comp.Pos,
	}

	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			prop := &Prop{
				Name:          pd.Name,
				Type:          c.resolveType(pd.Type),
				Default:       pd.Default,
				Bidirectional: pd.Bidirectional,
			}
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &EventDecl{
				Name: pd.Name,
				Type: c.resolveType(pd.Type),
			}
			irComp.Events = append(irComp.Events, evt)
		}
	}

	if comp.ChildrenType != nil {
		irComp.ChildrenType = c.resolveType(comp.ChildrenType)
	}

	// Main symtab + scope for unqualified access.
	c.symtab.Comps[irComp.Name] = irComp
	c.scope.Declare(irComp)
	// Stdlib package for qualified sngl.Component access.
	pkg.Components = append(pkg.Components, irComp)
	pkg.Symbols.Comps[irComp.Name] = irComp
	pkg.Symbols.Root.Declare(irComp)
}
