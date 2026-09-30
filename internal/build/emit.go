package build

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A family is generated rather than rendered when a target overrides the
// family itself: `component block[go.language] { gen.emit(…) }`. Its members
// then never reach a code generator. The build walks each place the program
// renders the family -- a bodyless host whose rest slot takes it, written at
// the root of a file -- into data, hands that to the family's emitter, and
// removes the host from the package before anything else sees it.
//
// The walk and the emitter are separate on purpose. What crosses between them
// is emittedNode: which member, where it was written, and its props'
// build-time values. Today's only emitter evaluates the gen.node templates the
// member overrides declare; one that runs another process takes the same tree
// and never needs the IR.

// emittedNode is one member of an emitted family, as the walk found it.
type emittedNode struct {
	comp *ir.Component
	at   ast.Pos
	// props holds every prop the member declares: the value written at the
	// call site, else the declared default, else nil.
	props    map[*ir.Prop]any
	children []emittedNode
}

// emittedHost is one place the program renders a family that emits.
type emittedHost struct {
	host    *ir.NodeInst
	family  *ir.Component
	emit    *ir.NodeInst // the family override's gen.emit
	members []emittedNode
}

// emitFamilies runs the emitters this target's overrides select, and returns
// the files they write. Every host it answers is removed from the body it was
// written in, or replaced there by what generates it.
//
// A host is read wherever a root member may stand: at the root of a file,
// under an `if` there, and in the body of a component whose family is `root`
// -- which the inliner later splices into the package body like any other.
// Under an `if` a code-mode host is rewritten in place, so the effects its
// family's gen.emit is made of sit under the same `if` and passEffect mounts
// and unmounts them with it. A template-mode host writes its file once, at
// build time, so the `if` has to be decidable then.
func emitFamilies(pkg *ir.Package, t Target) (map[string][]byte, error) {
	e := &familyEmission{pkg: pkg, t: t, reached: map[*ir.Component]bool{nil: true}}
	pkg.Body = e.stmts(pkg.Body, nil, nil)
	for _, comp := range reachedComponents(pkg) {
		e.reached[comp] = true
	}
	for _, comp := range pkg.Components {
		if comp != nil && ir.IsAppRootTree(comp.Tree) && !comp.IsFamily() {
			comp.Body = e.stmts(comp.Body, comp, nil)
		}
	}
	if err := refuseNestedHosts(pkg); err != nil {
		e.errs = append(e.errs, err)
	}
	return e.files, errors.Join(e.errs...)
}

type familyEmission struct {
	// reached is the root components the package body renders, and nil for
	// the body itself: a template-mode host writes its file at build time, so
	// one in a component nobody renders writes nothing.
	reached map[*ir.Component]bool
	pkg     *ir.Package
	t       Target
	env     *interp.Env
	files   map[string][]byte
	errs    []error
}

// stmts answers every host in one statement list. owner is the root
// component the list belongs to, or nil for the package body; conds is the
// chain of `if` conditions above it, each negated for an `else`.
func (e *familyEmission) stmts(stmts []ir.Stmt, owner *ir.Component, conds []condFrame) []ir.Stmt {
	kept := stmts[:0:0]
	for _, st := range stmts {
		switch n := st.(type) {
		case *ir.If:
			n.Body = e.stmts(n.Body, owner, append(conds[:len(conds):len(conds)], condFrame{cond: n.Cond}))
			n.Else = e.stmts(n.Else, owner, append(conds[:len(conds):len(conds)], condFrame{cond: n.Cond, neg: true}))
			kept = append(kept, st)
			continue
		case *ir.NodeInst:
			if family := emittedFamilyOf(n); family != nil {
				if repl, ok := e.host(n, family, owner, conds); ok && repl != nil {
					kept = append(kept, repl)
				}
				continue
			}
		}
		kept = append(kept, st)
	}
	return kept
}

// condFrame is one `if` above a host.
type condFrame struct {
	cond ir.Expr
	neg  bool
}

// host answers one host: the statement standing in its place, if any, and
// whether it was answered (an error is recorded and the host dropped).
func (e *familyEmission) host(ni *ir.NodeInst, family *ir.Component, owner *ir.Component, conds []condFrame) (ir.Stmt, bool) {
	emit, err := familyEmitter(ni, family, e.t)
	if err != nil {
		e.errs = append(e.errs, err)
		return nil, false
	}
	if codeMode(emit) {
		inst, err := emitCode(e.pkg, owner, ni, family, emit, e.t)
		if err != nil {
			e.errs = append(e.errs, err)
			return nil, false
		}
		return inst, true
	}
	if !e.reached[owner] {
		return nil, true
	}
	if e.env == nil {
		e.env = interp.BuildProgramEnv(e.pkg)
	}
	// A file is written once, at build time, so whether it is written has to
	// be known then.
	for _, c := range conds {
		v, err := buildValue(e.env, c.cond, nodePos(ni))
		if err != nil {
			e.errs = append(e.errs, posErr(nodePos(ni), "%s writes its file at build time, so the `if` it is written under has to be decided then, and it reads state the program changes as it runs", ni.Component.Name))
			return nil, false
		}
		if b, _ := v.(bool); b == c.neg {
			return nil, true
		}
	}
	h, err := walkHost(e.env, ni, family, emit, e.t)
	if err != nil {
		e.errs = append(e.errs, err)
		return nil, false
	}
	name, data, err := runTemplates(e.env, h, e.t)
	if err != nil {
		e.errs = append(e.errs, err)
		return nil, false
	}
	if _, dup := e.files[name]; dup {
		e.errs = append(e.errs, posErr(nodePos(ni), "%s writes %s, which another %s in this program already writes", ni.Component.Name, name, ni.Component.Name))
		return nil, false
	}
	if e.files == nil {
		e.files = map[string][]byte{}
	}
	e.files[name] = data
	return nil, true
}

// emittedFamilyOf is the family that generates this node, when it is a host
// of one.
func emittedFamilyOf(ni *ir.NodeInst) *ir.Component {
	if ni == nil {
		return nil
	}
	return ir.EmittedFamily(ni.Component)
}

// refuseNestedHosts reports a host the walk does not reach: under a `for`, or
// in a component that is not a root member. Left in place it would reach a
// code generator as a bodyless node it has no way to render.
//
// A `for` is refused rather than read because each copy of the host would be
// a family emitter of its own, and the members function the build writes for
// one is a package function that cannot see the loop's variables.
func refuseNestedHosts(pkg *ir.Package) error {
	var errs []error
	for _, owner := range ir.Owners(pkg) {
		ir.Walk(owner.Stmts(), func(n ir.Node) error { //nolint:errcheck // the visit never fails
			if ni, ok := n.(*ir.NodeInst); ok && emittedFamilyOf(ni) != nil {
				errs = append(errs, posErr(nodePos(ni), "%s is generated by its family's emitter, which reads it where a root member stands -- at the root of a file, under an `if` there, or in a component whose family is root -- and not under a `for`", ni.Component.Name))
			}
			return nil
		})
	}
	return errors.Join(errs...)
}

// override is the body t renders comp with: the platform's override, else the
// language's, which is the order ir.SpecializeForTarget picks in.
func override(comp *ir.Component, t Target) (ir.Body, bool) {
	if b, ok := comp.PlatformOverrides[t.Platform]; ok {
		return b, true
	}
	b, ok := comp.LanguageOverrides[t.Lang]
	return b, ok
}

// soleGenNode is the one statement of an emitter override body, when it is a
// node of the given kind.
func soleGenNode(b ir.Body, kind ir.BuiltinKind) *ir.NodeInst {
	if len(b.Stmts) != 1 {
		return nil
	}
	ni, ok := b.Stmts[0].(*ir.NodeInst)
	if !ok || ni.Component == nil || ni.Component.Builtin != kind {
		return nil
	}
	return ni
}

// familyEmitter is the gen.emit the family's override for t is.
func familyEmitter(host *ir.NodeInst, family *ir.Component, t Target) (*ir.NodeInst, error) {
	body, ok := override(family, t)
	if !ok {
		return nil, posErr(nodePos(host), "%s is generated by the %s family's emitter, which has no override for %s",
			host.Component.Name, family.Name, describe(t))
	}
	emit := soleGenNode(body, ir.BuiltinGenEmit)
	if emit == nil {
		return nil, posErr(family.AST.Pos, "the %s family's override for %s is a gen.emit and nothing else", family.Name, describe(t))
	}
	return emit, nil
}

func walkHost(env *interp.Env, host *ir.NodeInst, family *ir.Component, emit *ir.NodeInst, t Target) (*emittedHost, error) {
	if len(host.Slots) > 0 {
		return nil, posErr(nodePos(host), "%s's members are written as its children", host.Component.Name)
	}
	w := &walker{env: env, t: t}
	members, err := w.walkMembers(host.Children)
	if err != nil {
		return nil, err
	}
	return &emittedHost{host: host, family: family, emit: emit, members: members}, nil
}

// walker reads a host's content at build time. slots holds the children each
// composed member it is inside was written with, innermost last, which is what
// an insertion of that member's rest slot walks.
type walker struct {
	env   *interp.Env
	t     Target
	slots [][]ir.Stmt
}

// walkMembers reads a block of an emitted family's content. The members are
// what the file is written from; an `if` and a `for` are how they got there,
// and are decided here rather than written out.
func (w *walker) walkMembers(stmts []ir.Stmt) ([]emittedNode, error) {
	var out []emittedNode
	for _, st := range stmts {
		var (
			nodes []emittedNode
			err   error
		)
		switch s := st.(type) {
		case *ir.NodeInst:
			nodes, err = w.walkMember(s)
		case *ir.If:
			var v any
			if v, err = buildValue(w.env, s.Cond, stmtAt(s.AST)); err == nil {
				branch := s.Else
				if b, _ := v.(bool); b {
					branch = s.Body
				}
				nodes, err = w.walkMembers(branch)
			}
		case *ir.For:
			nodes, err = w.walkFor(s)
		case *ir.SlotInst:
			nodes, err = w.walkSlot(s)
		default:
			err = posErr(stmtAt(stmtAST(st)), "an emitted family's content is its members, and the ifs and fors around them")
		}
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}

// walkSlot reads a composed member's insertion of its rest slot: the children
// that member was written with, walked outside the member they were handed to.
func (w *walker) walkSlot(s *ir.SlotInst) ([]emittedNode, error) {
	if !s.Rest || len(w.slots) == 0 {
		return nil, posErr(stmtAt(s.AST), "an emitted family's member inserts the children it was written with, and nothing else")
	}
	top := w.slots[len(w.slots)-1]
	w.slots = w.slots[:len(w.slots)-1]
	defer func() { w.slots = append(w.slots, top) }()
	if len(top) == 0 {
		return w.walkMembers(s.Children)
	}
	return w.walkMembers(top)
}

func (w *walker) walkFor(s *ir.For) ([]emittedNode, error) {
	at := stmtAt(s.AST)
	if s.Iter == nil || s.KeySym == nil {
		return nil, posErr(at, "an emitted family's loop walks a list")
	}
	v, err := buildValue(w.env, s.Iter, at)
	if err != nil {
		return nil, err
	}
	elems, ok := v.([]any)
	if !ok {
		return nil, posErr(at, "an emitted family's loop walks a list, and this is a %T", v)
	}
	if len(elems) == 0 {
		return w.walkMembers(s.Else)
	}
	var out []emittedNode
	for i, e := range elems {
		if s.ValueSym != nil {
			w.env.Set(s.KeySym, i)
			w.env.Set(s.ValueSym, e)
		} else {
			w.env.Set(s.KeySym, e)
		}
		nodes, err := w.walkMembers(s.Body)
		if err != nil {
			return nil, err
		}
		out = append(out, nodes...)
	}
	return out, nil
}

// walkMember reads one member. A member the target says what it writes for --
// an override whose body is a gen.node -- is a node of the emitted tree. One
// with a body of its own composes, as any component does: its body is read
// with its props bound, and what it holds takes its place. A bodyless one with
// neither has nothing to write, which is refused rather than left out.
func (w *walker) walkMember(ni *ir.NodeInst) ([]emittedNode, error) {
	at := nodePos(ni)
	comp := ni.Component
	if len(ni.Handlers) > 0 || len(ni.Slots) > 0 {
		return nil, posErr(at, "%s is written into a file at build time, which has nothing to handle an event or fill a slot", comp.Name)
	}
	props := map[*ir.Prop]any{}
	for _, p := range comp.Props {
		var (
			v   any
			err error
		)
		switch arg := argNamed(ni.Props, p.Name); {
		case arg != nil:
			v, err = buildValue(w.env, arg, at)
		case p.Default != nil:
			v, err = buildValue(w.env, p.Default, at)
		}
		if err != nil {
			return nil, err
		}
		props[p] = v
	}
	body, ok := override(comp, w.t)
	if ok && soleGenNode(body, ir.BuiltinGenNode) != nil {
		children, err := w.walkMembers(ni.Children)
		if err != nil {
			return nil, err
		}
		return []emittedNode{{comp: comp, at: at, props: props, children: children}}, nil
	}
	if comp.Bodyless {
		return nil, posErr(at, "the %s family emits for %s, and %s says nothing about what it writes: give it an override whose body is a gen.node, or a body of its own",
			comp.Tree.Name, describe(w.t), comp.Name)
	}
	for p, v := range props {
		w.env.Set(p.Sym, v)
	}
	w.slots = append(w.slots, ni.Children)
	defer func() { w.slots = w.slots[:len(w.slots)-1] }()
	return w.walkMembers(comp.Body)
}

func argNamed(args []ir.Arg, name string) ir.Expr {
	for _, a := range args {
		if a.Name == name {
			return a.Value
		}
	}
	return nil
}

// buildValue evaluates e at build time. What it may read is what the build
// knows: constants, the loop variables the walk binds, and the props of the
// member being written. A read of state is refused rather than evaluated,
// since the value it has before the program runs is not what it will have.
func buildValue(env *interp.Env, e ir.Expr, at ast.Pos) (any, error) {
	if a := interp.IRASTOf(e); a != nil {
		if p := a.ExprPos(); p != nil && p.IsSet() {
			at = *p
		}
	}
	var state *ir.Ident
	ir.Walk(e, func(n ir.Node) error { //nolint:errcheck // the visit never fails
		if id, ok := n.(*ir.Ident); ok && state == nil {
			if v, ok := id.Sym.(*ir.Var); ok && !v.IsConst {
				state = id
			}
		}
		return nil
	})
	if state != nil {
		if state.AST != nil && state.AST.Pos.IsSet() {
			at = state.AST.Pos
		}
		return nil, posErr(at, "an emitted family is written at build time, and %s is state the program changes as it runs", state.Name)
	}
	v, err := env.Eval(e)
	if err != nil {
		return nil, posErr(at, "%v", err)
	}
	return v, nil
}

// runTemplates is the emitter every family has today: the family override's
// gen.emit brackets what each member's gen.node writes, in the order the walk
// reached them, with a member's props bound where its templates are read.
func runTemplates(env *interp.Env, h *emittedHost, t Target) (string, []byte, error) {
	file, err := templateArg(env, h.emit, "file")
	if err != nil {
		return "", nil, err
	}
	if file == "" || path.IsAbs(file) || strings.HasPrefix(path.Clean(file), "..") {
		return "", nil, posErr(nodePos(h.emit), "gen.emit's file is a path inside the output directory, and %q is not", file)
	}
	var b strings.Builder
	open, err := templateArg(env, h.emit, "open")
	if err != nil {
		return "", nil, err
	}
	b.WriteString(open)
	var write func(nodes []emittedNode) error
	write = func(nodes []emittedNode) error {
		for _, n := range nodes {
			body, ok := override(n.comp, t)
			node := soleGenNode(body, ir.BuiltinGenNode)
			if !ok || node == nil {
				return posErr(n.at, "the %s family emits for %s, and %s says nothing about what it writes: give it an override whose body is a gen.node",
					h.family.Name, describe(t), n.comp.Name)
			}
			for p, v := range n.props {
				env.Set(p.Sym, v)
			}
			s, err := templateArg(env, node, "open")
			if err != nil {
				return err
			}
			b.WriteString(s)
			if err := write(n.children); err != nil {
				return err
			}
			for p, v := range n.props {
				env.Set(p.Sym, v)
			}
			if s, err = templateArg(env, node, "close"); err != nil {
				return err
			}
			b.WriteString(s)
		}
		return nil
	}
	if err := write(h.members); err != nil {
		return "", nil, err
	}
	closeText, err := templateArg(env, h.emit, "close")
	if err != nil {
		return "", nil, err
	}
	b.WriteString(closeText)
	return path.Clean(file), []byte(b.String()), nil
}

// templateArg evaluates one of a gen node's props, or its default.
func templateArg(env *interp.Env, ni *ir.NodeInst, name string) (string, error) {
	e := argNamed(ni.Props, name)
	if e == nil {
		for _, p := range ni.Component.Props {
			if p.Name == name && p.Default != nil {
				e = p.Default
			}
		}
	}
	if e == nil {
		return "", nil
	}
	v, err := env.Eval(e)
	if err != nil {
		return "", posErr(nodePos(ni), "%s: %v", name, err)
	}
	s, _ := v.(string)
	return s, nil
}

func describe(t Target) string {
	return "platform " + t.Platform + " with language " + t.Lang
}

func nodePos(ni *ir.NodeInst) ast.Pos { return stmtAt(ni.AST) }

func stmtAST(st ir.Stmt) ast.Stmt {
	switch s := st.(type) {
	case *ir.NodeInst:
		return s.AST
	case *ir.If:
		return s.AST
	case *ir.For:
		return s.AST
	}
	return nil
}

func stmtAt(s ast.Stmt) ast.Pos {
	if s == nil {
		return ast.Pos{}
	}
	if p := s.StmtPos(); p != nil {
		return *p
	}
	return ast.Pos{}
}

func posErr(at ast.Pos, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if !at.IsSet() {
		return errors.New(msg)
	}
	return fmt.Errorf("%s:%d:%d: %s", at.File, at.Line, at.Column, msg)
}

// reachedComponents is every component the package body renders, directly or
// through the bodies of the components it renders.
func reachedComponents(pkg *ir.Package) []*ir.Component {
	var out []*ir.Component
	seen := map[*ir.Component]bool{}
	var scan func([]ir.Stmt)
	scan = func(stmts []ir.Stmt) {
		_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
			if n, ok := s.(*ir.NodeInst); ok && n.Component != nil && !seen[n.Component] {
				seen[n.Component] = true
				out = append(out, n.Component)
				scan(n.Component.Body)
			}
			return nil
		})
	}
	scan(pkg.Body)
	return out
}
