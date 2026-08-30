package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// markImpls binds each macro the compiler implements to the code that runs
// for it, and says nothing else about one. A declaration with no entry here is
// a mark that would resolve and then do nothing.
var markImpls = map[markKey]markImpl{
	{"internal/marks", "builtin"}:   markBuiltin,
	{"internal/marks", "intrinsic"}: markIntrinsic,
	{"tree", "kind"}:                markTreeKind,
	{"macro", "options"}:            markOptions,
	{"macro", "wildcard"}:           markWildcard,
	{"macro", "foreign"}:            markForeign,
	{"macro", "identity"}:           markIdentity,
}

// markBuiltin implements #[builtin("kind")], the mark that names the IR
// construct a declaration dispatches to.
//
// It does not decide which kinds go on which declaration forms; it stamps the
// kind on whichever IR the declaration became. What a given kind then requires
// — that a node kind names a component, that a const kind names a const — is
// checked by bindBuiltinRole, where the compiler stores the reference, because
// that is where the requirement comes from.
func markBuiltin(m *mark) error {
	raw := m.args.String("kind")
	kind := ir.BuiltinKind(raw)
	if !kind.Valid() {
		return fmt.Errorf("unknown builtin kind %q (valid: %s)", raw, strings.Join(builtinKindNames(), ", "))
	}
	switch d := m.sym.(type) {
	case *ir.Component:
		d.Builtin = kind
	case *ir.StructDef:
		d.Builtin = kind
	case *ir.Var:
		d.Builtin = kind
	case *ir.UnitDef:
		d.Builtin = kind
	default:
		return fmt.Errorf("#[builtin(%q)] cannot mark %s", raw, ast.DeclFormName(m.decl))
	}
	m.c.bindBuiltinRole(kind, m.sym.(ir.Symbol))
	return nil
}

func builtinKindNames() []string {
	all := ir.AllBuiltinKinds()
	names := make([]string, len(all))
	for i, k := range all {
		names[i] = string(k)
	}
	return names
}

// The flags #[intrinsic] accepts after the id, declared as ir.IntrinsicFlag in
// lib/internal/ir. The checker validates them against that enum; these are the
// members it acts on.
const (
	flagUsable          = "usable"
	flagMutates         = "mutates"
	flagReadonly        = "readonly"
	flagMutatesReceiver = "mutatesReceiver"
)

// markIntrinsic implements #[intrinsic("Id", flags...)], stamping the id and
// what the compiler needs to know about a call onto the function — or, on a
// component, the id alone: a component is emitted by the platform codegen
// that answers to the id, and no call cost follows from that.
func markIntrinsic(m *mark) error {
	id := m.args.String("id")
	if id == "" {
		return fmt.Errorf("#[intrinsic] requires a non-empty id")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[intrinsic(%q)]", id))
	if err != nil {
		return err
	}
	if slices.Contains(flags, flagMutates) && slices.Contains(flags, flagReadonly) {
		return fmt.Errorf("#[intrinsic(%q)] is both %s and %s; a call either has an effect or only reads host state",
			id, flagMutates, flagReadonly)
	}
	if comp, ok := m.sym.(*ir.Component); ok {
		// A component has no signature, so the flags — which all describe
		// what a call costs — have nothing to describe.
		if len(flags) > 0 {
			return fmt.Errorf("#[intrinsic(%q)] carries %s, which describes a call; a component has none", id, flags[0])
		}
		if comp.Intrinsic != "" {
			return fmt.Errorf("#[intrinsic(%q)]: already an intrinsic (%q)", id, comp.Intrinsic)
		}
		comp.Intrinsic = id
		return nil
	}
	fn, ok := m.sym.(*ir.Func)
	if !ok {
		return fmt.Errorf("#[intrinsic(%q)] cannot mark %s", id, ast.DeclFormName(m.decl))
	}
	if fn.Intrinsic != "" {
		return fmt.Errorf("#[intrinsic(%q)]: already an intrinsic (%q)", id, fn.Intrinsic)
	}
	fn.Intrinsic = id
	fn.IntrinsicBodyUsable = slices.Contains(flags, flagUsable)
	fn.MutatesReceiver = slices.Contains(flags, flagMutatesReceiver)
	switch {
	case slices.Contains(flags, flagMutates):
		fn.Purity = ir.PurityMutates
	case slices.Contains(flags, flagReadonly):
		fn.Purity = ir.PurityReadonly
	}
	return nil
}

// The flags #[foreign] accepts after the name, declared as ir.ForeignFlag.
const (
	flagPure  = "pure"
	flagAsync = "async"
)

// markForeign implements #[foreign("scheme://path", "Name", flags...)].
//
// Two arguments are the import path the declaration comes from and its name
// there, written the way a program's own import line writes them. One argument
// is the name alone: that is all a struct field can say, since a field has no
// package of its own. Nothing here inspects either string — the path is not
// resolved and the name is not checked to exist, because a plugin's output is
// trusted.
//
// The mark never confers type identity. ir.Foreign.Origin is left nil: Origin
// is a scheme importer's own key for a declaration it read, and it is what
// makes two declarations the same type. A marked declaration unifies with
// nothing.
func markForeign(m *mark) error {
	path, name := m.args.String("path"), m.args.String("name")
	if !m.args.Has("name") {
		path, name = "", path
	}
	if name == "" {
		return fmt.Errorf("#[foreign] requires a non-empty name")
	}
	flags, err := uniqueFlags(m.args.Idents("flags"), fmt.Sprintf("#[foreign(%q)]", name))
	if err != nil {
		return err
	}
	if _, isFunc := m.sym.(*ir.Func); len(flags) > 0 && !isFunc {
		return fmt.Errorf("#[foreign(%q)] carries %s, which describes a call; %s has none", name, flags[0], ast.DeclFormName(m.decl))
	}
	if n := markedNames(m.decl); n > 1 {
		return fmt.Errorf("#[foreign(%q)] marks %d names at once; one foreign name cannot stand for several declarations", name, n)
	}
	scheme, pkgPath := imports.ParseScheme(path)
	fm := ir.Foreign{Scheme: scheme, Path: pkgPath, Name: name, Marked: true}
	switch d := m.sym.(type) {
	case *ir.StructDef:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		d.Foreign = fm
	case *ir.StructField:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		d.Foreign = fm
	case *ir.Func:
		if d.Foreign.Marked {
			return fmt.Errorf("#[foreign(%q)]: already marked as %q", name, d.Foreign.Name)
		}
		d.Foreign = fm
		// A foreign function's body describes the declaration rather than
		// implementing it, so what a call costs is what the mark says. Purity
		// is left unknown without the flag: inferring it from the body would
		// fold a stub's result into the program in place of the call.
		d.IsAsync = slices.Contains(flags, flagAsync)
		if slices.Contains(flags, flagPure) {
			d.Purity = ir.PurityPure
		}
	default:
		return fmt.Errorf("#[foreign(%q)] cannot mark %s", name, ast.DeclFormName(m.decl))
	}
	return nil
}

// markedNames counts the names one declaration binds. A foreign name stands
// for one declaration, so a grouped one cannot carry the mark.
func markedNames(decl markTarget) int {
	switch d := decl.(type) {
	case *ast.StructField:
		return len(d.Names)
	case *ast.ConstDecl:
		return specNameCount(d.Specs)
	case *ast.VarDecl:
		return specNameCount(d.Specs)
	}
	return 1
}

func specNameCount(specs []ast.VarSpec) int {
	n := 0
	for _, s := range specs {
		n += len(s.Names)
	}
	return n
}

// uniqueFlags rejects a repeated flag. The enum membership of each one is
// already checked against the declared parameter type.
func uniqueFlags(flags []string, mark string) ([]string, error) {
	for i, f := range flags {
		if slices.Contains(flags[:i], f) {
			return nil, fmt.Errorf("%s repeats flag %s", mark, f)
		}
	}
	return flags, nil
}

// markOptions implements #[options], which says the struct it annotates is a
// target's build-option schema — the fields an `output(...)` block may name.
//
// The mark, not the declaration's name, is what the compiler keys on: every
// site that looks up an options schema finds the marked struct, so a target may
// call the struct whatever it likes and a struct incidentally named Options is
// not one.
func markOptions(m *mark) error {
	sd, ok := m.sym.(*ir.StructDef)
	if !ok {
		return fmt.Errorf("#[options] cannot mark %s; only a struct declares an options schema", ast.DeclFormName(m.decl))
	}
	if sd.Options {
		return fmt.Errorf("#[options]: already marked as an options schema")
	}
	sd.Options = true
	return nil
}

func markTreeKind(m *mark) error {
	sd, ok := m.sym.(*ir.StructDef)
	if !ok {
		return fmt.Errorf("#[tree.kind] cannot mark %s; a tree is named by a struct", ast.DeclFormName(m.decl))
	}
	if decl, ok := m.decl.(*ast.StructDef); ok && len(decl.Body) > 0 {
		return fmt.Errorf("#[tree.kind]: a tree struct holds nothing; remove its fields")
	}
	sd.IsTree = true
	return nil
}

// markWildcard implements #[macro.wildcard("pattern")], which says what a
// name nobody declared resolves to: a component reached by any matching name
// in its package's namespace, or a prop bound by any matching prop name.
//
// The pattern is anchored to the whole name, so a partial match is not one.
// It is compiled here, where the mark is written, so a pattern RE2 cannot
// parse is reported at its own position rather than at the first call site
// that would have matched it.
func markWildcard(m *mark) error {
	pat := m.args.String("pattern")
	if pat == "" {
		return fmt.Errorf("#[wildcard] requires a non-empty pattern")
	}
	if err := compileWildcard(pat); err != nil {
		return fmt.Errorf("#[wildcard(%q)]: %w", pat, err)
	}
	switch d := m.sym.(type) {
	case *ir.Component:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		d.Wildcard, d.WildcardInto = pat, m.args.String("into")
		// The prop it names is checked by finishWildcardMarks: a component's
		// marks run before its props are built, so there is nothing to look
		// the name up in yet.
	case *ir.Prop:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		if m.args.Has("into") {
			return fmt.Errorf("#[wildcard(%q)]: `into` names where a matched name is bound, which only a component's mark has to say; a prop's matched names are its own keys", pat)
		}
		if err := checkWildcardPropType(d); err != nil {
			return fmt.Errorf("#[wildcard(%q)] on prop %q: %w", pat, d.Name, err)
		}
		d.Wildcard = pat
	case *ir.EventDecl:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		if m.args.Has("into") {
			return fmt.Errorf("#[wildcard(%q)]: `into` names where a matched name is bound, which only a component's mark has to say; a handler already carries the name it was written under", pat)
		}
		// No map, unlike a prop: a handler is bound under its own name in the
		// IR, so the names a wildcard event matched are already distinct
		// without being collected anywhere.
		d.Wildcard = pat
	default:
		return fmt.Errorf("#[wildcard(%q)] cannot mark %s; only a component, one of its props or one of its events stands for names nobody declared", pat, ast.DeclFormName(m.decl))
	}
	return nil
}

// checkWildcardPropType requires a wildcard prop to be a map keyed by string.
// A wildcard prop stands for many names at once, so one call site can bind it
// many times; a scalar has room for one of them, and the rest were being
// accepted and then dropped on the floor.
func checkWildcardPropType(p *ir.Prop) error {
	t := p.Type
	if t == nil || t.Kind != ir.TypeMap || len(t.Elems) != 2 || t.Elems[0].Kind != ir.TypeString {
		got := "no type"
		if t != nil {
			got = t.String()
		}
		return fmt.Errorf("a wildcard prop collects every name it matches, so it is a map keyed by that name; declare it map<string, V> rather than %s", got)
	}
	return nil
}

// finishWildcardMarks validates the `into` prop a component's wildcard mark
// names, once its props exist. The matched name is a name, so the prop it
// binds to holds a string.
func (c *checker) finishWildcardMarks(pos ast.Pos, comp *ir.Component) {
	if comp == nil || comp.WildcardInto == "" {
		return
	}
	for _, p := range comp.Props {
		if p.Name != comp.WildcardInto {
			continue
		}
		if p.Type == nil || p.Type.Kind != ir.TypeString {
			c.error(pos, "#[wildcard(..., %q)] on component %s: the matched name is a name, so %q holds a string", comp.WildcardInto, comp.Name, p.Name)
		}
		return
	}
	c.error(pos, "#[wildcard(..., %q)] on component %s: no prop %q to bind the matched name to", comp.WildcardInto, comp.Name, comp.WildcardInto)
}

// markIdentity implements #[identity], which names the const carrying a
// target's own identity.
//
// The const is named for the type it has -- `platform`, `language` -- so it
// cannot annotate itself: the annotation would resolve to the const being
// declared rather than to the builtin struct it shadows. The mark supplies the
// type instead, choosing it by that name, and retypes the literal with it so
// the value compares equal to nothing but another identity of the same kind.
func markIdentity(m *mark) error {
	v, ok := m.sym.(*ir.Var)
	if !ok || !v.IsConst {
		return fmt.Errorf("#[identity] cannot mark %s; only a const carries a target identity", ast.DeclFormName(m.decl))
	}
	var typ *ir.StructDef
	switch v.Name {
	case "platform":
		typ = m.c.platformType
	case "language":
		typ = m.c.languageType
	default:
		return fmt.Errorf("#[identity] on %q: a target identity is named for its type, `platform` or `language`", v.Name)
	}
	if typ == nil {
		return fmt.Errorf("#[identity]: sngl:builtin declares no %q type", v.Name)
	}
	// Read the value from the source rather than the IR: a const's initialiser
	// is checked in a deferred pass, so at mark time there is nothing on the
	// var yet. Supplying both halves here is also what takes this const out of
	// that pass, which would otherwise check a string against a target type.
	decl, ok := m.decl.(*ast.ConstDecl)
	if !ok {
		return fmt.Errorf("#[identity] cannot mark %s", ast.DeclFormName(m.decl))
	}
	name, ok := identityLiteral(decl, v.Name)
	if !ok {
		return fmt.Errorf("#[identity] on %q: the value is the target's name, written as a string literal", v.Name)
	}
	v.Type = typ.SymType()
	v.Init = &ir.Literal{Type: typ.SymType(), Value: name}
	v.Synthesized = true
	return nil
}

// identityLiteral is the string a const declaration gives the named const.
func identityLiteral(decl *ast.ConstDecl, name string) (string, bool) {
	for _, spec := range decl.Specs {
		if !slices.Contains(spec.Names, name) {
			continue
		}
		lit, ok := spec.Default.(*ast.LiteralExpr)
		if !ok {
			return "", false
		}
		return lit.StringValue()
	}
	return "", false
}
