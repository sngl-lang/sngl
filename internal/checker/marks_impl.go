package checker

import (
	"fmt"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// markImpls binds each macro the compiler implements to the code that runs for
// it. It says nothing else about a macro: the package, the name, the parameter
// list and the documentation are all read off the `func X(...) ir.Macro`
// declaration in lib/, which is also the only place they can be read from —
// `sngl doc` renders the declaration.
//
// A declaration with no entry here is a mark that would resolve and then do
// nothing; lib/macros_test.go reports one.
var markImpls = map[markKey]markImpl{
	{"internal/marks", "builtin"}:   markBuiltin,
	{"internal/marks", "intrinsic"}: markIntrinsic,
	{"internal/tree", "kind"}:       markTreeKind,
	{"internal/tree", "children"}:   markTreeChildren,
	{"platforms", "options"}:        markOptions,
	{"platforms", "wildcard"}:       markWildcard,
	{"std", "foreign"}:              markForeign,
	{"draw", "shape"}:               markShape,
}

// --- #[builtin] ---

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
	kind := ast.BuiltinKind(raw)
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
		return fmt.Errorf("#[builtin(%q)] cannot mark %T", raw, m.decl)
	}
	m.c.bindBuiltinRole(kind, m.sym.(ir.Symbol))
	return nil
}

func builtinKindNames() []string {
	all := ast.AllBuiltinKinds()
	names := make([]string, len(all))
	for i, k := range all {
		names[i] = string(k)
	}
	return names
}

// --- #[intrinsic] ---

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
// what the compiler needs to know about a call onto the function.
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
	fn, ok := m.sym.(*ir.Func)
	if !ok {
		return fmt.Errorf("#[intrinsic(%q)] cannot mark %T", id, m.decl)
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

// --- #[foreign] ---

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
		return fmt.Errorf("#[foreign(%q)] carries %s, which describes a call; %T has none", name, flags[0], m.decl)
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
		return fmt.Errorf("#[foreign(%q)] cannot mark %T", name, m.decl)
	}
	return nil
}

// markedNames counts the names one declaration binds. A foreign name stands
// for one declaration, so a grouped one cannot carry the mark.
func markedNames(decl ast.Marked) int {
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

// --- #[options] ---

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
		return fmt.Errorf("#[options] cannot mark %T; only a struct declares an options schema", m.decl)
	}
	if sd.Options {
		return fmt.Errorf("#[options]: already marked as an options schema")
	}
	sd.Options = true
	return nil
}

// --- #[tree.kind] / #[tree.children] / #[draw.shape] ---

func markTreeKind(m *mark) error {
	return applyTreeMark(m, "kind", m.args.String("name"), setTreeKind)
}

// A component is a node of one tree, so a second kind would make it two.
func setTreeKind(c *ir.Component, name string) error {
	if c.TreeKind != "" {
		return fmt.Errorf("already a %q node", c.TreeKind)
	}
	c.TreeKind = name
	return nil
}

func markTreeChildren(m *mark) error {
	return applyTreeMark(m, "children", m.args.String("name"), func(c *ir.Component, name string) error {
		if c.ChildKind != "" {
			return fmt.Errorf("children are already restricted to %q", c.ChildKind)
		}
		c.ChildKind = name
		return nil
	})
}

func applyTreeMark(m *mark, name, kind string, set func(*ir.Component, string) error) error {
	if kind == "" {
		return fmt.Errorf("#[tree.%s] requires a non-empty tree name", name)
	}
	comp, ok := m.sym.(*ir.Component)
	if !ok {
		return fmt.Errorf("#[tree.%s(%q)] cannot mark %T; only a component is a node in a tree", name, kind, m.decl)
	}
	if err := set(comp, kind); err != nil {
		return fmt.Errorf("#[tree.%s(%q)]: %w", name, kind, err)
	}
	return nil
}

// shapeKind is the tree sngl://draw's components form. #[draw.shape] is the
// public spelling of #[tree.kind("shape")]: the tree marks are internal to the
// compiler, so a user declaring a shape reaches them only through this one.
const shapeKind = "shape"

func markShape(m *mark) error {
	decl, ok := m.decl.(*ast.ComponentDecl)
	if !ok {
		return fmt.Errorf("#[draw.shape] requires a component declaration")
	}
	// A painted shape has nothing to raise an event from. This is a rule about
	// drawing rather than about trees, so it is enforced here and not by the
	// tree marks.
	for _, p := range decl.Props.Props {
		if _, isEvent := p.(ast.EventDecl); isEvent {
			return fmt.Errorf("shape components do not support event declarations")
		}
	}
	if decl.ChildrenType != nil {
		return fmt.Errorf("shape components may only have shape children; remove the children type")
	}
	return applyTreeMark(m, "kind", shapeKind, setTreeKind)
}

// --- #[wildcard] ---

// markWildcard implements #[platforms.wildcard("pattern")], which says what a
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
	if _, err := compileWildcard(pat); err != nil {
		return fmt.Errorf("#[wildcard(%q)]: %w", pat, err)
	}
	switch d := m.sym.(type) {
	case *ir.Component:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		d.Wildcard = pat
	case *ir.Prop:
		if d.Wildcard != "" {
			return fmt.Errorf("#[wildcard(%q)]: already a wildcard for %q", pat, d.Wildcard)
		}
		d.Wildcard = pat
	default:
		return fmt.Errorf("#[wildcard(%q)] cannot mark %T; only a component or one of its props stands for names nobody declared", pat, m.decl)
	}
	return nil
}
