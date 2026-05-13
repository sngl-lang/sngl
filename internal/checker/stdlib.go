package checker

import (
	"io/fs"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// Cached parsed stdlib ASTs. Parsed once, reused across Check() calls.
var (
	stdlibOnce sync.Once
	stdlibDocs []*ast.Document
)

// StdlibDocs returns the parsed stdlib documents.
// The results are cached after the first call.
func StdlibDocs() []*ast.Document {
	return parseStdlibDocs()
}

// parseStdlibDocs parses every .sngl file embedded in the lib package. File
// ordering is not significant: loadStdlib groups declarations by kind before
// registering them, so new stdlib files can be dropped into lib/ without
// touching this code.
func parseStdlibDocs() []*ast.Document {
	stdlibOnce.Do(func() {
		entries, err := lib.FS.ReadDir(".")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			data, err := fs.ReadFile(lib.FS, e.Name())
			if err != nil {
				continue
			}
			doc, err := parser.Parse(e.Name(), data)
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
//
// Declarations are grouped by kind across all stdlib files and registered in a
// fixed order — imports, then types (units, structs, enums), then functions,
// then components — so the file a declaration lives in does not affect
// resolution.
func (c *checker) loadStdlib() *ir.Package {
	stdlibPkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}

	var (
		imports    []*ast.Import
		units      []*ast.UnitDef
		structs    []*ast.StructDef
		enums      []*ast.EnumDef
		funcs      []*ast.FuncDef
		components []*ast.ComponentDecl
	)
	for _, doc := range parseStdlibDocs() {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.Import:
				imports = append(imports, s)
			case *ast.UnitDef:
				units = append(units, s)
			case *ast.StructDef:
				structs = append(structs, s)
			case *ast.EnumDef:
				enums = append(enums, s)
			case *ast.FuncDef:
				funcs = append(funcs, s)
			case *ast.ComponentDecl:
				components = append(components, s)
			}
		}
	}

	for _, s := range imports {
		c.registerImport(s)
	}
	for _, s := range units {
		c.registerStdlibUnit(s, stdlibPkg)
	}
	for _, s := range enums {
		c.registerStdlibEnum(s, stdlibPkg)
	}
	// Structs may reference any type — including other structs — so register
	// names as empty stubs first, then resolve fields in a second pass.
	structDefs := make([]*ir.StructDef, len(structs))
	for i, s := range structs {
		structDefs[i] = c.declareStdlibStruct(s, stdlibPkg)
	}
	for i, s := range structs {
		c.resolveStdlibStructFields(s, structDefs[i])
	}
	// Annotate stdlib structs that have known Go-runtime native type names.
	// These annotations ensure that IRTypeToGo emits the qualified Go type
	// (e.g. "i18n.PluralKey") rather than the plain SNGL name ("PluralKey").
	for _, sd := range structDefs {
		if sd.Native == "" {
			switch sd.Name {
			case "PluralKey":
				sd.Native = "i18n.PluralKey"
			}
		}
	}
	for _, s := range funcs {
		c.registerStdlibFunc(s, stdlibPkg)
	}
	for _, s := range components {
		c.registerStdlibComponent(s, stdlibPkg)
	}

	// Register "sngl" namespace for qualified access to stdlib.
	c.scope.Declare(&ir.Namespace{
		Name: "sngl",
		Pkg:  stdlibPkg,
	})

	// Register "i18n" namespace so that i18n.plural(...), i18n.one, etc.
	// resolve without requiring an explicit import statement. The package
	// exposes every i18n.* receiver method as a free function, plus the
	// predeclared PluralKey constants (zero, one, two, few, many, other).
	c.scope.Declare(&ir.Namespace{
		Name: "i18n",
		Pkg:  c.buildI18nNamespacePkg(structDefs),
	})

	return stdlibPkg
}

// buildI18nNamespacePkg constructs a synthetic ir.Package for the "i18n"
// namespace, exposing i18n.* receiver methods as free functions and
// predeclaring the CLDR PluralKey constants (zero/one/two/few/many/other).
func (c *checker) buildI18nNamespacePkg(structDefs []*ir.StructDef) *ir.Package {
	pkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}

	// Expose all i18n.* receiver methods as free functions in the namespace.
	for _, fn := range c.symtab.Methods["i18n"] {
		pkg.Funcs = append(pkg.Funcs, fn)
		pkg.Symbols.Root.Declare(fn)
	}

	// Locate the PluralKey struct so we can type the predeclared vars.
	var pluralKeyType *ir.Type
	for _, sd := range structDefs {
		if sd.Name == "PluralKey" {
			pluralKeyType = sd.SymType()
			break
		}
	}
	if pluralKeyType == nil {
		// PluralKey not found; skip constant registration.
		return pkg
	}

	// Register predeclared CLDR plural-category vars: zero, one, two, few,
	// many, other. These are opaque sentinel values; their actual runtime
	// values are supplied by the Go i18n runtime (PluralZero, PluralOne, …).
	for _, name := range []string{"zero", "one", "two", "few", "many", "other"} {
		v := &ir.Var{Name: name, Type: pluralKeyType, IsConst: true}
		pkg.Vars = append(pkg.Vars, v)
		pkg.Symbols.Root.Declare(v)
	}

	return pkg
}

// declareStdlibStruct registers a struct name (without fields) so other
// declarations can reference it while we are still processing the stdlib.
// Fields are filled in by resolveStdlibStructFields once every name is in
// scope.
func (c *checker) declareStdlibStruct(s *ast.StructDef, pkg *ir.Package) *ir.StructDef {
	sd := &ir.StructDef{AST: s, Name: s.Name}
	// Main symtab + scope for unqualified access.
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
	// Stdlib package for qualified sngl.Type access.
	pkg.Structs = append(pkg.Structs, sd)
	pkg.Symbols.Types[sd.Name] = sd
	pkg.Symbols.Root.Declare(sd)
	return sd
}

// resolveStdlibStructFields populates the fields of an already-declared
// struct. Safe to run after every stdlib type name is in scope, which allows
// fields to reference any stdlib type regardless of declaration order.
func (c *checker) resolveStdlibStructFields(s *ast.StructDef, sd *ir.StructDef) {
	built := c.buildStructDef(s)
	sd.Fields = built.Fields
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
	// Stdlib funcs are not body-checked, so the usual purity analysis never
	// runs. Mark them pure so the optimizer can constant-fold pure stdlib
	// methods (int.min, string.upper, etc.) when called with constant args.
	// Impure stdlib (alert.show, file.contents, anything with a NativePkg
	// effect) gets its purity overridden later by stdlib.SetImpure or via
	// scheme registration.
	if fn.Purity == ir.PurityUnknown {
		fn.Purity = ir.PurityPure
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
	pkg := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
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

// mergePlatformExtensions walks the active platform's Package() docs for
// `component sngl.X` declarations, locates the corresponding stdlib component,
// and writes the matching `platform <name>` body into the stdlib
// *ir.Component.AST.Body. The component is processed later by the normal
// checker passes (its body is now non-empty).
//
// Pre-flight: extension merge requires exactly one registered platform. Zero
// platforms is a no-op (test/tooling mode). More than one is a checker error
// — tooling that reasons across platforms should run the checker per-platform.
//
// Note: this pass intentionally only acts on the new form (`HasParens=false`).
// Legacy `component sngl.X() { body }` declarations in platform .sngl files
// (html, bubbletea) continue to be ignored until Phase C rewrites them.
func (c *checker) mergePlatformExtensions() {
	if len(c.cfg.Platforms) == 0 {
		return
	}
	// Pre-flight: only one platform may be registered when any platform ships
	// new-form extension declarations. Walk every registered platform's docs
	// looking for new-form `sngl.X` decls; if any exist and len > 1, error.
	// (Pre-Phase-C this is essentially a no-op because platform .sngl files
	// still use the legacy `component sngl.X() { body }` form.)
	if len(c.cfg.Platforms) > 1 {
		hasExtension := false
	scan:
		for _, p := range c.cfg.Platforms {
			for _, doc := range p.Package() {
				for _, stmt := range doc.Stmts {
					decl, ok := stmt.(*ast.ComponentDecl)
					if !ok || decl.HasParens {
						continue
					}
					if strings.HasPrefix(decl.Name, "sngl.") {
						hasExtension = true
						break scan
					}
				}
			}
		}
		if hasExtension {
			c.error(ast.Pos{}, "component extensions require exactly one registered platform (got %d)", len(c.cfg.Platforms))
		}
		return
	}
	p := c.cfg.Platforms[0]
	platformName := p.PlatformIdentifier()
	for _, doc := range p.Package() {
		for _, stmt := range doc.Stmts {
			decl, ok := stmt.(*ast.ComponentDecl)
			if !ok {
				continue
			}
			if decl.HasParens {
				// Legacy form — skip until Phase C rewrites.
				continue
			}
			if !strings.HasPrefix(decl.Name, "sngl.") {
				continue
			}
			local := strings.TrimPrefix(decl.Name, "sngl.")
			stdSym, ok := c.symtab.Comps[local]
			if !ok {
				c.error(decl.Pos, "extension %q references unknown stdlib component %q", decl.Name, local)
				continue
			}
			stdComp, ok := stdSym.(*ir.Component)
			if !ok {
				continue
			}
			// Find the matching platform block within the extension body.
			var matched *ast.StmtBlock
			for i := range decl.Body.Stmts {
				pl, ok := decl.Body.Stmts[i].(*ast.PlatformStmt)
				if !ok {
					continue
				}
				if pl.Platform == platformName {
					matched = &pl.Body
					break
				}
			}
			if matched == nil {
				continue // no implementation for this platform; component stays abstract
			}
			// Splice the platform body into the stdlib component's AST. The
			// IR Body itself is populated when checkComponentBody runs on this
			// component (which happens at pass2 time via the dedicated stdlib
			// extension-check loop in Check()).
			if stdComp.AST != nil {
				stdComp.AST.Body = *matched
			}
			c.mergedExtensions = append(c.mergedExtensions, stdComp)
		}
	}
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
