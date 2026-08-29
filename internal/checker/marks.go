package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
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
//
// tree.children is legal here for a slot and nowhere else: a slot is a position
// that hosts content, so which family that content belongs to is a fact about
// the slot. tree.kind stays out, because kind says what a node *is* and a slot
// is a position rather than a node. The mark implementation is what holds a
// tree.children written on an ordinary prop to that rule.
var paramMarks = map[markKey]bool{
	{"platforms", "wildcard"}: true,
	{"tree", "children"}:      true,
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
		c.error(attr.Pos, "macro %s is declared by sngl://%s but the compiler implements no mark for it", attr.MacroName(), uri)
		return
	}
	if err := impl(&mark{c: c, attr: attr, args: args, decl: decl, sym: sym}); err != nil {
		c.error(attr.Pos, "%s", err)
	}
}

// resolveMacro finds the macro declaration an attribute names.
//
// Only a sngl:// import maps an alias to a package. An alias that names
// nothing imported is an error rather than an ambient lookup — a macro package
// is a dependency, and resolving it from the bare name would make
// `#[draw.shape]` mean something different depending on what was linked in.
func (c *checker) resolveMacro(attr ast.MacroAttr) (uri string, fn *ir.Func, ok bool) {
	ref, aliasKnown := c.markAliases[attr.Alias]
	switch {
	case attr.Alias == "":
		// Unqualified `#[name]` resolves against the dot-imported packages,
		// the same way an unqualified declaration does.
		for _, pkg := range c.markDotPkgs {
			if fn := c.macroDecl(pkg, attr.Name); fn != nil {
				return pkg, fn, true
			}
		}
		c.error(attr.Pos, "unknown macro %q: no dot-imported package declares it", attr.Name)
		return "", nil, false
	case aliasKnown && (ref.Scheme == "internal" || ref.Scheme == "sngl"):
		fn := c.macroDecl(ref.URI, attr.Name)
		if fn == nil {
			c.error(attr.Pos, "unknown macro %q: package %q declares none of that name", attr.Name, "sngl://"+ref.URI)
			return "", nil, false
		}
		return ref.URI, fn, true
	case aliasKnown:
		// Imported, but not from a scheme that can carry a macro. Nothing
		// downstream reads a mark that resolved to nothing, so a silent skip
		// would drop it and report nothing.
		c.error(attr.Pos, "%q is not a library package, so it declares no macros", attr.Alias)
		return "", nil, false
	default:
		c.error(attr.Pos, "unknown macro package %q: import it with import %q", attr.Alias, macroImportHint(attr.Alias))
		return "", nil, false
	}
}

// macroImportHint names the package an unknown alias most likely meant, so the
// suggestion is a line the user can paste. A macro package is a library
// package like any other; the compiler's own live under internal/.
func macroImportHint(alias string) string {
	if HasPackage(alias) {
		return "sngl://" + alias
	}
	return "sngl://internal/" + alias
}

// macroDecl returns the macro of this name declared by sngl://uri, or nil.
// The package is loaded to read it — a macro's signature is a declared
// signature, resolved in the scope of the package that wrote it.
func (c *checker) macroDecl(uri, name string) *ir.Func {
	// A package still loading cannot be asked what it declares. That is a
	// package whose own source marks a declaration with one of its own
	// macros, which no lib package does.
	if !c.hasLibPkg(uri) || c.libs.loading[uri] {
		return nil
	}
	pkg := c.libPkg(uri)
	for _, fn := range pkg.Macros {
		if fn.Name == name {
			c.resolveMacroSig(pkg, fn)
			return fn
		}
	}
	return nil
}

// setMarkScope points mark resolution at the documents whose declarations are
// being registered, and returns a function restoring the previous set. An
// alias binds a macro package for the whole package, as it does for every
// other imported name.
func (c *checker) setMarkScope(docs []*ast.Document) func() {
	savedAliases, savedDot := c.markAliases, c.markDotPkgs
	c.markAliases = imports.ResolveAliases(docs)
	c.markDotPkgs = imports.DotPackages(docs)
	return func() { c.markAliases, c.markDotPkgs = savedAliases, savedDot }
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

// Int is a constant-integer parameter's value (0 if absent).
func (a markArgs) Int(name string) int64 {
	if i := a.index(name); i >= 0 {
		return a.vals[i].num
	}
	return 0
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
// `sngl://<pkg>` declares as name. A declaration is what makes a macro exist,
// so this is the one thing about a macro that Go still decides; lib's drift
// test asserts that every declaration has an answer here.
func MacroIsImplemented(pkg, name string) bool {
	_, ok := markImpls[markKey{pkg, name}]
	return ok
}
