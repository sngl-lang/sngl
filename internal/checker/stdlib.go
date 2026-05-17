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
		contexts   []*ast.CallStmt
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
			case *ast.CallStmt:
				if isContextDeclCallStmt(s) {
					contexts = append(contexts, s)
				}
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
	// Phase 1: register stdlib func signatures (no body checking yet) so
	// later phases — context default expressions, context-reading wrapper
	// bodies — can resolve names against fully-populated symbol tables.
	type stdlibFuncBody struct {
		ast *ast.FuncDef
		fn  *ir.Func
	}
	var pendingBodies []stdlibFuncBody
	for _, s := range funcs {
		fn := c.registerStdlibFunc(s, stdlibPkg)
		// Defer body check: only expression-body funcs (=> expr) need to
		// be lowered into ir.Block. Block-body stdlib funcs and bodyless
		// signatures (e.g. `func i18n.exactly(n int) PluralKey {}`) keep
		// their existing semantics.
		if fn != nil && s.Body != nil {
			pendingBodies = append(pendingBodies, stdlibFuncBody{ast: s, fn: fn})
		}
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

	// Register stdlib context declarations last — after the "i18n" namespace is
	// in scope — so that default-value expressions like `i18n.defaultLocale()`
	// resolve correctly. Stdlib contexts are declared into the stdlib scope and
	// their *ir.Context pointers are appended to c.pkg.Contexts so the
	// interpreter and codegen discover them alongside user-declared contexts.
	for _, s := range contexts {
		c.registerStdlibContextDecl(s)
	}

	// Phase 2: check deferred stdlib expression-body wrappers. Run last so
	// that bodies can read freshly-registered context decls (e.g. the
	// `#locale` context used by i18n.* wrappers).
	for _, pb := range pendingBodies {
		c.checkStdlibFuncBody(pb.ast, pb.fn)
	}

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

func (c *checker) registerStdlibFunc(f *ast.FuncDef, pkg *ir.Package) *ir.Func {
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
	return fn
}

// checkStdlibFuncBody type-checks a stdlib expression-body `=>` wrapper into
// an ir.Block (single Return). The package-level scope must already contain
// all stdlib decls (imports, types, funcs, contexts) so the body can resolve
// references like `intl.Translate` or the active `locale` context.
//
// Intentional limits:
//   - Bodyless / block-bodied stdlib funcs are unaffected.
//   - If checking the body produces no return type (void), the func is left
//     with Return == nil so existing dyn-fallback in registerStdlibFunc
//     remains active.
//   - detectIntrinsicCall is re-run on the now-populated Block so wrappers
//     that are exact intrinsic pass-throughs (e.g. `float.floor` → MathFloor)
//     get fn.Intrinsic set, matching the historical behaviour.
func (c *checker) checkStdlibFuncBody(f *ast.FuncDef, fn *ir.Func) {
	if f.Body == nil {
		return
	}
	c.pushScope()
	defer c.popScope()
	for _, p := range fn.Params {
		c.scope.Declare(p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	defer func() { c.returnType = prevReturn }()
	prevTypeParams := c.typeParams
	c.typeParams = fn.TypeParams
	defer func() { c.typeParams = prevTypeParams }()

	bodyExpr := c.checkExpr(f.Body)
	if bodyExpr == nil {
		return
	}
	pos := f.Pos
	if p := f.Body.ExprPos(); p != nil {
		pos = *p
	}
	fn.Block = []ir.Stmt{&ir.Return{
		AST:   &ast.ReturnStmt{Pos: pos, Value: f.Body},
		Value: bodyExpr,
	}}
	// Re-detect intrinsic pass-through with the populated body. Wrappers
	// that prepend args (e.g. i18n.* threading `locale`) won't match —
	// detectIntrinsicCall enforces strict positional pass-through.
	if fn.Intrinsic == "" {
		if id := detectIntrinsicCall(fn); id != "" {
			fn.Intrinsic = id
		}
	}
}

// detectIntrinsicCall checks if a function body is a single return of a call
// to an intrinsic function whose arguments are a direct pass-through of the
// wrapper's own params (e.g., `func error.raise(m, k) => stdlib.ErrorRaise(m, k)`).
// Returns the intrinsic name or "".
//
// "Direct pass-through" means the call's arg list, in order, is exactly the
// wrapper's param idents. Wrappers that rearrange or augment args (e.g. the
// i18n wrappers which prepend `locale`) must keep their body so subsequent
// lowering passes (notably NoContext) can rewrite reads inside.
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
	if call.Func.Intrinsic == "" {
		return ""
	}
	// Require strict pass-through: arg count == param count, each arg is an
	// Ident referencing the corresponding wrapper param (positionally).
	if len(call.Args) != len(fn.Params) {
		return ""
	}
	for i, a := range call.Args {
		if a.Name != "" && a.Name != fn.Params[i].Name {
			return ""
		}
		id, ok := a.Value.(*ir.Ident)
		if !ok || id.Name != fn.Params[i].Name {
			return ""
		}
	}
	return call.Func.Intrinsic
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

// mergePlatformExtensions walks every registered platform's Package() docs
// for `component sngl.X` declarations and collects the checked IR body of
// each `platform <name> { ... }` block into the stdlib *ir.Component's
// PlatformBodies map (keyed by platform name).
//
// The checker is platform-agnostic: it does not know or care which platform
// will be the active build target. The lowering pass passPlatformExtensionBody
// reads PlatformBodies[opts.Platform] and swaps it into Component.Body before
// any other pass runs.
//
// Duplicate platform entries for the same stdlib X (e.g., two registered
// platforms both shipping `component sngl.text { platform foo { ... } }`)
// are an error.
//
// Note: this pass intentionally only acts on the new form (`HasParens=false`).
// Legacy `component sngl.X() { body }` declarations in platform .sngl files
// (html, bubbletea) continue to be ignored until Phase C rewrites them.
func (c *checker) mergePlatformExtensions() {
	if len(c.cfg.Platforms) == 0 {
		return
	}
	for _, p := range c.cfg.Platforms {
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
				// Walk each platform block; collect every (platformName → body)
				// pairing this decl declares. Duplicate keys across all
				// registered platforms for the same stdlib component error.
				for i := range decl.Body.Stmts {
					pl, ok := decl.Body.Stmts[i].(*ast.PlatformStmt)
					if !ok {
						continue
					}
					if stdComp.PlatformBodies == nil {
						stdComp.PlatformBodies = map[string][]ir.Stmt{}
					}
					if _, dup := stdComp.PlatformBodies[pl.Platform]; dup {
						c.error(pl.Pos, "component sngl.%s has duplicate platform block for %q", local, pl.Platform)
						continue
					}
					// Reserve the key first so duplicate-detection works even
					// when the body check appends nothing (e.g., empty body).
					stdComp.PlatformBodies[pl.Platform] = nil
					c.pendingExtensions = append(c.pendingExtensions, pendingExtension{
						comp:     stdComp,
						platform: pl.Platform,
						body:     pl.Body,
					})
				}
			}
		}
	}
}

// pendingExtension records a single `platform <name> { ... }` body that
// needs to be checked into IR and stashed under stdComp.PlatformBodies.
// Body-checking is deferred until after user pass1 so user-declared symbols
// are in scope when the platform body resolves identifiers.
type pendingExtension struct {
	comp     *ir.Component
	platform string
	body     ast.StmtBlock
}

// checkPendingExtensions runs after user pass1. For each pending extension,
// temporarily install the platform block as the stdlib component's AST.Body,
// invoke checkComponentBody, capture the resulting IR Body into the
// PlatformBodies map, and restore the component's Body slot for the next
// extension (or the final pass2). The stdlib component's AST.Body and Body
// are left empty after this routine — the active platform's IR body is
// swapped in by lower's passPlatformExtensionBody.
func (c *checker) checkPendingExtensions() {
	if len(c.pendingExtensions) == 0 {
		return
	}
	for _, pe := range c.pendingExtensions {
		savedAST := pe.comp.AST.Body
		savedBody := pe.comp.Body
		savedPlatform := c.currentPlatform
		pe.comp.AST.Body = pe.body
		pe.comp.Body = nil
		c.currentPlatform = pe.platform
		c.checkComponentBody(pe.comp)
		c.currentPlatform = savedPlatform
		pe.comp.PlatformBodies[pe.platform] = pe.comp.Body
		pe.comp.AST.Body = savedAST
		pe.comp.Body = savedBody
	}
}

// registerStdlibContextDecl registers a stdlib `context #name(default)` decl.
// The *ir.Context is declared in the current scope (stdlib scope) so user
// source can read it as an identifier, and appended to c.pkg.Contexts so the
// interpreter and codegen discover it alongside user-declared contexts.
func (c *checker) registerStdlibContextDecl(s *ast.CallStmt) {
	sel := s.Call.Func.(*ast.SelectExpr)
	name := sel.Field
	ctx := &ir.Context{AST: s, Name: name}
	if name == "" {
		c.error(s.Pos, "stdlib context decl requires #identifier")
		return
	}
	if _, exists := c.scope.LookupLocal(name); exists {
		// Already declared (e.g. duplicate stdlib file); skip silently.
		return
	}
	args := s.Call.Args.Args
	if len(args) != 1 {
		c.error(s.Pos, "stdlib context decl requires exactly one default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		c.scope.Declare(ctx)
		return
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(s.Pos, "stdlib context default must be positional, not named")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		c.scope.Declare(ctx)
		return
	}
	def := c.checkExpr(a.Value)
	if def != nil && !ir.IsConst(def) {
		pos := s.Pos
		if p := a.Value.ExprPos(); p != nil {
			pos = *p
		}
		c.error(pos, "stdlib context default must be a constant expression")
	}
	ctx.Default = def
	if def != nil {
		ctx.Typ = def.ExprType()
	}
	c.pkg.Contexts = append(c.pkg.Contexts, ctx)
	c.scope.Declare(ctx)
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
