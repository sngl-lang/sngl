package checker

import (
	"embed"
	"io/fs"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
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
	"stdlib/units.sngl",
	"stdlib/types.sngl",
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
func (c *checker) loadStdlib() *ir.Package {
	stdlibPkg := &ir.Package{
		Symbols: NewSymbolTable(),
	}

	for _, doc := range parseStdlibDocs() {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.Import:
				c.registerImport(s)
			case *ast.StructDef:
				c.registerStdlibStruct(s, stdlibPkg)
			case *ast.EnumDef:
				c.registerStdlibEnum(s, stdlibPkg)
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
	c.scope.Declare(&ir.Namespace{
		Name: "sngl",
		Pkg:  stdlibPkg,
	})

	return stdlibPkg
}

func (c *checker) registerStdlibStruct(s *ast.StructDef, pkg *ir.Package) {
	sd := c.buildStructDef(s)
	// Main symtab + scope for unqualified access.
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
	// Stdlib package for qualified sngl.Type access.
	pkg.Structs = append(pkg.Structs, sd)
	pkg.Symbols.Types[sd.Name] = sd
	pkg.Symbols.Root.Declare(sd)
}

func (c *checker) registerStdlibEnum(e *ast.EnumDef, pkg *ir.Package) {
	ed := c.buildEnumDef(e)
	c.symtab.Types[ed.Name] = ed
	c.scope.Declare(ed)
	pkg.Enums = append(pkg.Enums, ed)
	pkg.Symbols.Types[ed.Name] = ed
	pkg.Symbols.Root.Declare(ed)
}

func (c *checker) registerStdlibUnit(u *ast.UnitDef, pkg *ir.Package) {
	ud := c.buildUnitDef(u)
	// Main symtab + scope for unqualified access.
	c.symtab.Types[ud.Name] = ud
	c.scope.Declare(ud)
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
	// Stdlib package.
	pkg.Units = append(pkg.Units, ud)
	pkg.Symbols.Types[ud.Name] = ud
}

func (c *checker) registerStdlibFunc(f *ast.FuncDef, pkg *ir.Package) {
	fn := c.buildFunc(f)
	// Stdlib funcs skip the body-check pass. When a stdlib signature omits a
	// return annotation (common for the "=>" forms that delegate to an
	// intrinsic), treat the missing return as an explicit dyn escape hatch
	// rather than void — the stdlib is trusted to know what it's doing, and
	// body-level inference would conflict with the primitive/struct aliasing
	// used internally (e.g. the `color` struct vs the `color` primitive).
	if fn.Return == nil && f.Body != nil {
		fn.Return = TypDyn
	}
	if id := detectIntrinsicCall(fn); id != "" {
		fn.Intrinsic = id
	}
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

// detectIntrinsicCall checks if a function body is a single return of a call
// to an intrinsic function (e.g., stdlib.StrIndexOf). Returns the intrinsic
// name or "".
func detectIntrinsicCall(fn *ir.Func) string {
	if len(fn.Block) != 1 {
		return ""
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok {
		return ""
	}
	call, ok := ret.Value.(*ir.Call)
	if !ok || call.Func == nil {
		return ""
	}
	if call.Func.Intrinsic != "" {
		return call.Func.Intrinsic
	}
	return ""
}

// buildIntrinsicsPkgFrom creates a synthetic package from a list of intrinsic
// definitions. Each intrinsic becomes a bodyless ir.Func with Intrinsic set.
func (c *checker) buildIntrinsicsPkgFrom(defs []ir.IntrinsicDef) *ir.Package {
	pkg := &ir.Package{Symbols: NewSymbolTable()}
	for _, def := range defs {
		params := make([]*ir.Param, len(def.Params))
		for i, p := range def.Params {
			params[i] = &ir.Param{Name: p.Name, Type: p.Type}
		}
		fn := &ir.Func{
			Name:      def.Name,
			Params:    params,
			Return:    def.Return,
			Intrinsic: def.Name,
		}
		pkg.Funcs = append(pkg.Funcs, fn)
		pkg.Symbols.Root.Declare(fn)
	}
	return pkg
}

func (c *checker) registerStdlibComponent(comp *ast.ComponentDecl, pkg *ir.Package) {
	irComp := &ir.Component{
		AST:  comp,
		Name: comp.Name,
	}

	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			typ := c.resolveType(pd.Type)
			var def ir.Expr
			if pd.Default != nil {
				// Placeholder; stdlib prop defaults don't need full checking.
				def = &ir.Literal{Type: typ}
			}
			prop := &ir.Prop{
				Name:          pd.Name,
				Type:          typ,
				Default:       def,
				Bidirectional: pd.Bidirectional,
			}
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &ir.EventDecl{
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
