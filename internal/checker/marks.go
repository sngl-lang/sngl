package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// markTarget is a syntax form a mark can be written on. The AST knows only
// that some forms carry `#[...]` attributes; a mark is what this package makes
// of one, so the interface that names them marks lives here rather than there.
type markTarget interface {
	MacroAttrs() []ast.MacroAttr
}

// markKey identifies a mark by the lib/ declaration it was written from,
// which is what an implementation is bound to.
type markKey struct{ pkg, name string }

// markImpl is what a mark does. It is handed the arguments the checker
// validated against the declared signature, the declaration as written, and
// the IR the checker has just built for it.
type markImpl func(*mark) error

type mark struct {
	c    *checker
	attr ast.MacroAttr
	args markArgs
	// decl is what the mark was written on, as written: an ast.Attributed
	// declaration form, or an ast.Param for a component prop. A mark reads it
	// for the facts the IR does not carry — how many names a field declares,
	// whether a component declared a children type of its own.
	decl markTarget
	// sym is the IR the declaration registered as: *ir.StructDef, *ir.Func,
	// *ir.Component, *ir.Var, *ir.UnitDef, *ir.StructField, *ir.Prop.
	sym any
}

// applyMarks resolves and runs the marks written on decl. sym is the IR the
// checker has just built for it; every mark writes what it has to say there.
//
// Called from each registration site rather than from a pass over the
// document, so a mark applies once, at the moment the declaration exists and
// before anything reads what the mark says about it.
func (c *checker) applyMarks(decl ast.Stmt, sym any) {
	a, ok := decl.(ast.Attributed)
	if !ok {
		return
	}
	for _, attr := range a.MacroAttrs() {
		c.applyMark(attr, a, sym, false)
	}
}

// paramMarks are the marks that may be written in a component's parameter
// list. Every other mark says something about a declaration that a parameter is
// not, so the position is refused rather than left to the mark to notice.
var paramMarks = map[markKey]bool{
	{"macro", "wildcard"}: true,
}

// applyParamMarks resolves and runs the marks written on a component prop.
// prop is the ir.Prop the checker has just built for it, which is what a mark
// legal here writes to.
func (c *checker) applyParamMarks(p ast.Param, prop *ir.Prop) {
	for _, attr := range p.MacroAttrs() {
		c.applyMark(attr, p, prop, true)
	}
}

func (c *checker) applyMark(attr ast.MacroAttr, decl markTarget, sym any, inParam bool) {
	uri, fn, ok := c.resolveMacro(attr)
	if !ok {
		return
	}
	if inParam && !paramMarks[markKey{uri, attr.Name}] {
		c.error(attr.Pos, "#[%s] cannot mark a parameter", attr.MacroName())
		return
	}
	args, err := markArgsFor(fn, attr.Args)
	if err != nil {
		c.error(attr.Pos, "macro %s: %s", attr.MacroName(), err)
		return
	}
	impl, ok := markImpls[markKey{uri, attr.Name}]
	if !ok {
		// The declaration says the macro exists; nothing in the compiler says
		// what it does.
		c.error(attr.Pos, "macro %s is declared by sngl:%s but the compiler implements no mark for it", attr.MacroName(), uri)
		return
	}
	if err := impl(&mark{c: c, attr: attr, args: args, decl: decl, sym: sym}); err != nil {
		c.error(attr.Pos, "%s", err)
	}
}

// resolveMacro finds the macro declaration an attribute names.
//
// Through the ordinary scope, the same way every other name resolves. A macro
// used to be looked up in a table this package built by re-scanning the import
// statements — a second resolver for one question, which is why a package could
// not use a macro it declared itself: its own declarations were never in that
// table, though they were always in scope.
//
// Only a sngl: import maps an alias to a package. An alias naming nothing
// imported is an error rather than an ambient lookup — a macro package is a
// dependency, and resolving it from the bare name would make `#[draw.shape]`
// mean something different depending on what was linked in.
func (c *checker) resolveMacro(attr ast.MacroAttr) (uri string, fn *ir.Func, ok bool) {
	if attr.Alias == "" {
		sym, found := c.scope.Lookup(attr.Name)
		if !found {
			c.error(attr.Pos, "unknown macro %q: nothing in scope declares it", attr.Name)
			return "", nil, false
		}
		return c.macroFrom(attr, sym)
	}
	nsSym, found := c.scope.Lookup(attr.Alias)
	if !found {
		c.error(attr.Pos, "unknown macro package %q: import it with import %q", attr.Alias, macroImportHint(attr.Alias))
		return "", nil, false
	}
	ns, isNS := nsSym.(*ir.Namespace)
	if !isNS || ns.Pkg == nil {
		c.error(attr.Pos, "%q is not a library package, so it declares no macros", attr.Alias)
		return "", nil, false
	}
	member, found := ns.Pkg.Symbols.LookupMember(attr.Name)
	if !found {
		c.error(attr.Pos, "unknown macro %q: package %q declares none of that name", attr.Name, pkgURIOf(ns.Pkg, attr.Alias))
		return "", nil, false
	}
	return c.macroFrom(attr, member)
}

// macroFrom accepts a resolved symbol as a macro, or says why it is not one.
//
// A macro is a declaration whose return type is sngl:internal/ir's Macro, and
// nothing else about it is special — which is the point of resolving it here
// rather than in a table of its own.
func (c *checker) macroFrom(attr ast.MacroAttr, sym ir.Symbol) (string, *ir.Func, bool) {
	fn, isFunc := sym.(*ir.Func)
	if !isFunc {
		c.error(attr.Pos, "%q is not a macro: it is %T; a mark names a declaration returning ir.Macro", attr.MacroName(), sym)
		return "", nil, false
	}
	if !c.isMacroSig(fn.Return) {
		c.error(attr.Pos, "%q is not a macro: it answers %s; a mark names a declaration returning ir.Macro", attr.MacroName(), fn.Return)
		return "", nil, false
	}
	// The package the macro was declared in, whose root scope its parameter
	// types resolve against. Asking libPkg for the package currently loading
	// would re-enter its own load, which is what kept a package from using a
	// macro it declares; the one being built is already in hand.
	// Symbols may be nil for a package still assembling its own: the signature
	// is resolved once, and a mark applied before there is a scope to resolve
	// against gets the declared types as written.
	if pkg := c.macroHome(fn); pkg != nil && pkg.Symbols != nil {
		c.resolveMacroSig(pkg, fn)
	}
	return strings.TrimPrefix(fn.Pkg, "sngl:"), fn, true
}

// refuseParamMarks reports the marks written on a function or lambda
// parameter. The grammar accepts one there, but a mark annotates a
// declaration and such a parameter is not one: there is nothing for a mark to
// say about it. Refusing at registration puts the report where the parameter
// is known, so no walk of the document is needed to find one.
//
// A component prop is the exception and goes through applyParamMarks.
func (c *checker) refuseParamMarks(params []ast.Param) {
	for _, p := range params {
		for _, attr := range p.MacroAttrs() {
			c.error(attr.Pos, "#[%s] cannot mark a parameter", attr.MacroName())
		}
	}
}

// markArgs holds a mark's arguments, checked against the declared parameter
// list and addressable by parameter name.
type markArgs struct {
	params []*ir.Param
	vals   []markVal
}

type markVal struct {
	set    bool // false when an optional parameter was omitted
	str    string
	num    int64
	ident  string
	idents []string
	expr   ast.Expr
}

func (a markArgs) index(name string) int {
	for i, p := range a.params {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Has reports whether an optional argument was supplied.
func (a markArgs) Has(name string) bool {
	i := a.index(name)
	return i >= 0 && a.vals[i].set
}

func (a markArgs) String(name string) string {
	if i := a.index(name); i >= 0 {
		return a.vals[i].str
	}
	return ""
}

// Idents is the value of a list-typed parameter, empty when the mark supplied
// none.
func (a markArgs) Idents(name string) []string {
	if i := a.index(name); i >= 0 {
		return a.vals[i].idents
	}
	return nil
}

// argKind is how a declared parameter type is written at a mark's use site.
// A mark's arguments are constants read at check time, so a parameter's type
// says which constant form its argument takes rather than what a value of it
// would be.
type argKind int

const (
	argExpr   argKind = iota // any expression; nothing reads one yet
	argString                // constant string
	argInt                   // constant integer
	argIdent                 // one enum member, written bare
	argIdents                // the remaining arguments, each an enum member
)

// markArgKind reads a parameter's declared type as an argument form, plus the
// member names an enum-typed one accepts. A list of enum members is variadic:
// the flags a mark carries are written as bare identifiers after its other
// arguments, not as a list literal.
func markArgKind(t *ir.Type) (argKind, []string) {
	if t == nil {
		return argExpr, nil
	}
	switch t.Kind {
	case ir.TypeString:
		return argString, nil
	case ir.TypeInt:
		return argInt, nil
	case ir.TypeEnum:
		return argIdent, enumMemberNames(t)
	case ir.TypeList:
		if len(t.Elems) == 1 && t.Elems[0] != nil && t.Elems[0].Kind == ir.TypeEnum {
			return argIdents, enumMemberNames(t.Elems[0])
		}
	}
	return argExpr, nil
}

func enumMemberNames(t *ir.Type) []string {
	ed, ok := t.Decl.(*ir.EnumDef)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(ed.Members))
	for _, m := range ed.Members {
		names = append(names, m.Name)
	}
	return names
}

// markArgsFor checks a mark's raw arguments against the macro's declared
// parameters and reads each to its form. Arguments are positional; a parameter
// with a default may be omitted from the tail, and a trailing list parameter
// takes however many remain.
func markArgsFor(fn *ir.Func, raw []ast.Expr) (markArgs, error) {
	params := fn.Params
	required, variadic := 0, false
	for i, p := range params {
		kind, _ := markArgKind(p.Type)
		if kind == argIdents && i == len(params)-1 {
			variadic = true
			continue
		}
		if p.Default == nil {
			required++
		}
	}
	if len(raw) < required || (!variadic && len(raw) > len(params)) {
		return markArgs{}, fmt.Errorf("expected %s, got %d", arityDesc(required, len(params), variadic), len(raw))
	}
	vals := make([]markVal, len(params))
	for i, p := range params {
		kind, enum := markArgKind(p.Type)
		if variadic && i == len(params)-1 {
			rest := raw[min(i, len(raw)):]
			idents := make([]string, 0, len(rest))
			for _, e := range rest {
				v, err := readMarkArg(argIdent, enum, e)
				if err != nil {
					return markArgs{}, fmt.Errorf("argument %q: %w", p.Name, err)
				}
				idents = append(idents, v.ident)
			}
			vals[i] = markVal{set: true, idents: idents}
			break
		}
		if i >= len(raw) {
			break // the rest have defaults and were omitted
		}
		v, err := readMarkArg(kind, enum, raw[i])
		if err != nil {
			return markArgs{}, fmt.Errorf("argument %q: %w", p.Name, err)
		}
		vals[i] = v
	}
	return markArgs{params: params, vals: vals}, nil
}

func readMarkArg(kind argKind, enum []string, e ast.Expr) (markVal, error) {
	switch kind {
	case argString:
		s, err := ast.EvalString(e)
		if err != nil {
			return markVal{}, err
		}
		return markVal{set: true, str: s}, nil
	case argInt:
		n, err := ast.EvalInt(e)
		if err != nil {
			return markVal{}, err
		}
		return markVal{set: true, num: n}, nil
	case argIdent:
		id, ok := e.(*ast.IdentExpr)
		if !ok {
			return markVal{}, fmt.Errorf("expected an identifier")
		}
		if len(enum) > 0 && !slices.Contains(enum, id.Name) {
			return markVal{}, fmt.Errorf("unknown value %q (want one of: %s)", id.Name, strings.Join(enum, ", "))
		}
		return markVal{set: true, ident: id.Name}, nil
	default:
		return markVal{set: true, expr: e}, nil
	}
}

func arityDesc(required, total int, variadic bool) string {
	if variadic {
		if required == 1 {
			return "at least 1 argument"
		}
		return fmt.Sprintf("at least %d arguments", required)
	}
	if required == total {
		if total == 1 {
			return "1 argument"
		}
		return fmt.Sprintf("%d arguments", total)
	}
	return fmt.Sprintf("%d to %d arguments", required, total)
}

// MacroIsImplemented reports whether the compiler implements the macro
// `sngl:<pkg>` declares as name. A declaration is what makes a macro exist,
// so this is the one thing about a macro that Go still decides; lib's drift
// test asserts that every declaration has an answer here.
func MacroIsImplemented(pkg, name string) bool {
	_, ok := markImpls[markKey{pkg, name}]
	return ok
}

// macroImportHint is the import a mark's alias most likely wanted, for the
// error that says the alias names nothing. A guess at the URI from the alias:
// the marks a program writes are all `sngl:<alias>` but for the compiler's own
// tier, which no program imports.
func macroImportHint(alias string) string {
	return "sngl:" + alias
}

// pkgURIOf names a package the way an import writes it, for a diagnostic about
// one. A Package does not carry its own URI, so it is read off a declaration in
// it; a package with nothing to read falls back to the alias the mark used.
func pkgURIOf(pkg *ir.Package, alias string) string {
	if pkg != nil {
		for _, sd := range pkg.Structs {
			if sd.Pkg != "" {
				return sd.Pkg
			}
		}
		for _, fn := range pkg.Funcs {
			if fn.Pkg != "" {
				return fn.Pkg
			}
		}
		for _, m := range pkg.Macros {
			if m.Pkg != "" {
				return m.Pkg
			}
		}
	}
	return alias
}

// macroHome is the package a macro was declared in, without re-entering a load.
func (c *checker) macroHome(fn *ir.Func) *ir.Package {
	if fn.Pkg == "" || fn.Pkg == c.libPkgName {
		return c.declPkg()
	}
	return c.libPkg(strings.TrimPrefix(fn.Pkg, "sngl:"))
}
