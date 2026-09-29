package ir

import (
	"fmt"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Convert builds a fresh AST Document from a type-checked IR Package,
// without referencing any embedded AST pointers. The resulting document
// can be formatted with the standard SNGL formatter.
func Convert(pkg *Package) *ast.Document {
	c := &converter{}
	return c.convertPackage(pkg)
}

type converter struct {
	// aliases maps a library package's URI to what this file imported it as,
	// so a name from one is spelled the way the source spells it.
	aliases map[string]string
	// dotted is the packages this file dot-imported. A name from one is
	// spelled bare, and qualifying it with the package would not resolve.
	dotted map[string]bool
}

// --- Package → Document ---

func (c *converter) convertPackage(pkg *Package) *ast.Document {
	var stmts []ast.Stmt

	c.aliases = map[string]string{}
	c.dotted = map[string]bool{}
	for _, imp := range pkg.Imports {
		uri, ok := strings.CutPrefix(imp.Path, "sngl:")
		if !ok {
			continue
		}
		switch {
		case imp.Alias == ".":
			c.dotted[uri] = true
		case imp.Alias != "":
			c.aliases[uri] = imp.Alias
		}
	}

	for _, imp := range pkg.Imports {
		// Macro-package imports are injected by the library load, not written
		// by this package's source, and the checker re-injects them on every
		// check. Emitting them would put compiler-internal imports in
		// user-facing output — where they also collide with the names the
		// standard-library import lifts.
		if strings.HasPrefix(imp.Path, "sngl:internal/") {
			continue
		}
		stmts = append(stmts, c.convertImport(imp))
	}
	for _, s := range pkg.Structs {
		// See convertType: an anonymous struct prints structurally, and the
		// synthesized name is one no program may declare.
		if s.Anon {
			continue
		}
		stmts = append(stmts, c.convertStructDef(s))
	}
	for _, e := range pkg.Enums {
		stmts = append(stmts, c.convertEnumDef(e))
	}
	for _, u := range pkg.Units {
		stmts = append(stmts, c.convertUnitDef(u))
	}
	// Skip funcs whose receiver is a component name in this package —
	// the checker mirrors them into both pkg.Funcs and comp.Funcs, and
	// convertComponent emits the comp.Funcs copy as nested decls. Emitting
	// them again here would produce duplicate `Comp.method` decls at the
	// document root.
	componentNames := map[string]bool{}
	for _, comp := range pkg.Components {
		componentNames[comp.Name] = true
	}
	for _, f := range pkg.Funcs {
		if f.Receiver != "" && componentNames[f.Receiver] {
			continue
		}
		stmts = append(stmts, c.convertFuncDef(f))
	}
	for _, v := range pkg.Consts {
		stmts = append(stmts, c.convertConstDecl(v))
	}
	for _, v := range pkg.Vars {
		stmts = append(stmts, c.convertVarDecl(v))
	}
	for _, comp := range pkg.Components {
		stmts = append(stmts, c.convertComponent(comp))
	}
	for _, w := range pkg.Windows {
		stmts = append(stmts, c.convertNodeInst(w))
	}
	for _, ctx := range pkg.Contexts {
		// Standard-library contexts arrive with the import, not from this
		// package's source; emitting them would redeclare the name.
		if ctx.Stdlib {
			continue
		}
		stmts = append(stmts, c.convertContext(ctx))
	}
	if len(pkg.Outputs) > 0 {
		stmts = append(stmts, c.convertOutputs(pkg.Outputs))
	}
	// The package's own body renders last, after every declaration it reads,
	// which is the order the source is written in and the order fmt keeps.
	for _, st := range pkg.Body {
		stmts = append(stmts, c.convertStmt(st))
	}

	return &ast.Document{Stmts: stmts}
}

// --- Top-level declarations ---

func (c *converter) convertImport(imp *Import) *ast.Import {
	return &ast.Import{
		Path:    imp.Path,
		Alias:   imp.Alias,
		Replace: imp.Replace,
	}
}

func (c *converter) convertStructDef(s *StructDef) *ast.StructDef {
	def := &ast.StructDef{
		Name:        s.Name,
		TypeParams:  c.convertTypeParams(s.TypeParams),
		IsMultiline: len(s.Fields) > 1,
		Attrs:       c.foreignAttrs(s.Foreign, nil),
	}
	for _, f := range s.Fields {
		sf := &ast.StructField{
			Names: []string{f.Name},
			Type:  c.convertType(f.Type),
			Attrs: c.foreignAttrs(f.Foreign, nil),
		}
		// DefaultWritten, not `Default != nil`: Normalize fills every nil
		// Default with the type's zero, so printing them all turns
		// `struct Foo { a int }` into `a int = 0` -- and re-checking that says
		// the declaration wrote a default it never wrote. Which is not only a
		// round-trip inequality: DeclaredDefault reads the flag, so a dump fed
		// back through the checker reinstated a zeroed field per prop.
		if f.DefaultWritten && f.Default != nil {
			sf.Default = c.convertExpr(f.Default)
		}
		def.Body = append(def.Body, sf)
	}
	return def
}

func (c *converter) convertAnonStructType(sd *StructDef) ast.TypeExpr {
	def := c.convertStructDef(sd)
	def.Name = ""
	return def
}

func (c *converter) convertEnumDef(e *EnumDef) *ast.EnumDef {
	def := &ast.EnumDef{
		Name:        e.Name,
		IsMultiline: len(e.Members) > 1,
	}
	for _, m := range e.Members {
		em := ast.EnumMember{Name: m.Name}
		if m.Value != nil {
			em.Value = c.convertExpr(m.Value)
		}
		def.Body = append(def.Body, &em)
	}
	return def
}

func (c *converter) convertUnitDef(u *UnitDef) *ast.UnitDef {
	def := &ast.UnitDef{
		Name:        u.Name,
		IsMultiline: len(u.Suffixes) > 1,
	}
	for _, s := range u.Suffixes {
		us := &ast.UnitSuffix{Name: s.Name}
		if !s.IsBase() {
			us.Factor = &ast.LiteralExpr{
				Kind: ast.LiteralFloat,
				Raw:  formatFloat(s.Factor),
			}
		}
		def.Body = append(def.Body, us)
	}
	return def
}

func (c *converter) convertConstDecl(v *Var) *ast.ConstDecl {
	spec := ast.VarSpec{
		Names: []string{v.Name},
		Type:  c.convertType(v.Type),
	}
	if v.Init != nil {
		spec.Default = c.convertExpr(v.Init)
	}
	return &ast.ConstDecl{Specs: []ast.VarSpec{spec}, Attrs: c.foreignAttrs(v.Foreign, nil)}
}

func (c *converter) convertVarDecl(v *Var) *ast.VarDecl {
	spec := ast.VarSpec{
		Names: []string{v.Name},
		Type:  c.convertType(v.Type),
	}
	if v.Init != nil {
		spec.Default = c.convertExpr(v.Init)
	}
	for _, h := range v.Handlers {
		spec.Handlers = append(spec.Handlers, c.convertEventHandler(h))
	}
	return &ast.VarDecl{Specs: []ast.VarSpec{spec}, Attrs: c.foreignAttrs(v.Foreign, nil)}
}

func (c *converter) convertFuncDef(f *Func) *ast.FuncDef {
	name := f.Name
	if f.Receiver != "" {
		name = f.Receiver + "." + f.Name
	}
	fd := &ast.FuncDef{
		Name:       name,
		TypeParams: c.convertTypeParams(f.TypeParams),
		Params:     c.convertParamList(f.Params),
		Attrs:      c.foreignAttrs(f.Foreign, f),
		Const:      f.Const,
	}
	if f.Return != nil {
		fd.ReturnType = c.convertType(f.Return)
	}
	// A declaration the host already has is emitted by nobody, so it has no
	// body to print -- and the one in the IR is not the author's. The checker
	// synthesizes `return <zero>` for a bodyless func, which read back as a
	// real implementation returning zero; printed beside the mark that says
	// the host has this, it is the shape the checker itself refuses. The test
	// is CodegenCtx.AllFuncs': a name, and not a #[foreign] correspondence
	// the program's own backend still emits.
	if len(f.Block) > 0 && !(f.Foreign.Name != "" && !f.Foreign.Marked) {
		fd.Block = c.convertStmtBlock(f.Block)
	}
	return fd
}

func (c *converter) convertComponent(comp *Component) *ast.ComponentDecl {
	cd := &ast.ComponentDecl{
		Name:  comp.Name,
		Const: comp.Const,
	}

	// Build prop list.
	var props []ast.ParamOrEventDecl
	for _, p := range comp.Props {
		param := ast.Param{
			Name:          p.Name,
			Type:          c.convertType(p.Type),
			Bidirectional: p.Bidirectional,
			Const:         p.Const,
		}
		if p.Default != nil {
			param.Default = c.convertExpr(p.Default)
		}
		props = append(props, param)
	}
	for _, e := range comp.Events {
		props = append(props, c.convertEventDecl(e))
	}
	for _, s := range comp.Slots {
		ct := c.slotType(s)
		var typ ast.TypeExpr = ct
		if s.Rest {
			typ = &ast.VariadicType{Elem: ct}
		}
		props = append(props, ast.Param{Name: s.Name, Type: typ, Const: s.Const})
	}
	if len(props) > 0 {
		cd.Props = ast.PropList{
			IsMultiline: len(props) > 3,
			Props:       props,
		}
	}

	// The return position is the tree the component is a member of. Its
	// children type is derived from the default slot, which prints itself.
	switch {
	case comp.Tree != nil:
		cd.ChildrenType = c.treeName(comp.Tree)
	case comp.TreeParam != "":
		// A wrapper's family is whatever it was handed, and the parameter is
		// the component's own -- so it is spelled bare, never qualified.
		cd.ChildrenType = &ast.NamedType{Name: comp.TreeParam}
	}

	// Build body: vars, consts, funcs, then body stmts.
	var bodyStmts []ast.Stmt
	for _, v := range comp.Vars {
		if v.IsConst {
			bodyStmts = append(bodyStmts, c.convertConstDecl(v))
		} else {
			bodyStmts = append(bodyStmts, c.convertVarDecl(v))
		}
	}
	for _, f := range comp.Funcs {
		fd := c.convertFuncDef(f)
		// Inside a component body the receiver is implicit; emitting it
		// as `Comp.name` would re-route the decl as a free-standing
		// method on type `Comp` rather than a component method.
		if f.Receiver != "" && f.Receiver == comp.Name {
			fd.Name = f.Name
		}
		bodyStmts = append(bodyStmts, fd)
	}
	if len(comp.Body) > 0 {
		bodyBlock := c.convertStmtBlock(comp.Body)
		bodyStmts = append(bodyStmts, bodyBlock.Stmts...)
	}
	// A signature keeps no block; anything else gets one even when it is
	// empty, since `{}` and no braces are different declarations.
	if !comp.Bodyless {
		cd.Body = ast.StmtBlock{
			IsMultiline: len(bodyStmts) > 0,
			Stmts:       bodyStmts,
			Pos:         ast.Pos{Line: 1},
		}
	}

	return cd
}

// windowContentSlot is what the window declaration calls the slot its body
// populates. Read off the declaration rather than spelled here, for the
// reason ErrorBoundary.FailedSlot gives: the library is free to rename it.
func windowContentSlot(w *Window) string {
	if rest := w.Component.RestSlot(); rest != nil {
		return rest.Name
	}
	return "content"
}

// convertContext emits a top-level context declaration as the CallStmt that the
// parser produces for `context #name(default)`. If Default is nil the arg list
// is omitted (degenerate case; real declarations always carry a default).
func (c *converter) convertContext(ctx *Context) *ast.CallStmt {
	call := &ast.CallExpr{
		Func: &ast.IdentExpr{Name: "context"},
		ID:   ctx.Name,
	}
	if ctx.Default != nil {
		call.Args = ast.ArgList{
			Args: []ast.ArgOrEventHandler{
				ast.Arg{Value: c.convertExpr(ctx.Default)},
			},
		}
	}
	return &ast.CallStmt{Call: call}
}

// convertContextProvider emits a ContextProvider as an *ast.VisualNode whose
// Target is the context name. The provider's value becomes a single positional
// arg and Body becomes the Block.
func (c *converter) convertContextProvider(p *ContextProvider) *ast.VisualNode {
	vn := &ast.VisualNode{
		Target: &ast.IdentExpr{Name: p.Ref.Name},
	}
	if p.Value != nil {
		vn.Args = ast.ArgList{
			Args: []ast.ArgOrEventHandler{
				ast.Arg{Value: c.convertExpr(p.Value)},
			},
		}
	}
	if len(p.Children) > 0 {
		vn.Block = c.convertStmtBlock(p.Children)
	}
	return vn
}

// convertOutputs rebuilds the directive's component tree from the outputs it
// was projected into:
//
//	output(shared...) { lang(langopts...) { platform(opts...) } }
//
// Each option goes back to the level that declares it, which is what the two
// component pointers on an Output are kept for: a merged record says what the
// build reads and not where it was written, and a value re-emitted a level too
// low is a prop the node does not declare.
func (c *converter) convertOutputs(outputs []*Output) *ast.VisualNode {
	// Group by language, preserving order.
	type langGroup struct {
		lang    string
		outputs []*Output
	}
	var groups []langGroup
	idx := map[string]int{}
	for _, o := range outputs {
		if i, ok := idx[o.Lang]; ok {
			groups[i].outputs = append(groups[i].outputs, o)
		} else {
			idx[o.Lang] = len(groups)
			groups = append(groups, langGroup{lang: o.Lang, outputs: []*Output{o}})
		}
	}

	var shared []ast.ArgOrEventHandler
	seenShared := map[string]bool{}
	var langNodes []ast.Stmt
	for _, g := range groups {
		langNode := &ast.VisualNode{Target: &ast.IdentExpr{Name: g.lang}}
		var langArgs []ast.ArgOrEventHandler
		seenLang := map[string]bool{}
		var platStmts []ast.Stmt
		for _, o := range g.outputs {
			platNode := &ast.VisualNode{Target: &ast.IdentExpr{Name: o.Platform}}
			var platArgs []ast.ArgOrEventHandler
			if o.Options != nil {
				for _, f := range o.Options.Fields {
					arg := ast.Arg{Name: f.Name, Value: c.convertExpr(f.Value)}
					switch {
					case declaresProp(o.PlatComp, f.Name):
						platArgs = append(platArgs, arg)
					case declaresProp(o.LangComp, f.Name):
						if !seenLang[f.Name] {
							seenLang[f.Name] = true
							langArgs = append(langArgs, arg)
						}
					case !seenShared[f.Name]:
						seenShared[f.Name] = true
						shared = append(shared, arg)
					}
				}
			}
			platNode.Args = ast.ArgList{IsMultiline: len(platArgs) > 3, Args: platArgs}
			platStmts = append(platStmts, platNode)
		}
		langNode.Args = ast.ArgList{IsMultiline: len(langArgs) > 3, Args: langArgs}
		langNode.Block = ast.StmtBlock{
			Pos:         ast.Pos{Line: 1},
			IsMultiline: len(platStmts) > 1,
			Stmts:       platStmts,
		}
		langNodes = append(langNodes, langNode)
	}

	return &ast.VisualNode{
		Target: &ast.IdentExpr{Name: "output"},
		Args:   ast.ArgList{IsMultiline: len(shared) > 3, Args: shared},
		Block: ast.StmtBlock{
			Pos:         ast.Pos{Line: 1},
			IsMultiline: len(langNodes) > 1,
			Stmts:       langNodes,
		},
	}
}

// declaresProp reports whether a target node declares a prop of this name.
func declaresProp(comp *Component, name string) bool {
	if comp == nil {
		return false
	}
	for _, p := range comp.Props {
		if p.Name == name {
			return true
		}
	}
	return false
}

// --- Statement conversion ---

func (c *converter) convertStmt(s Stmt) ast.Stmt {
	switch s := s.(type) {
	case *NodeInst:
		return c.convertNodeInst(s)
	case *CallStmt:
		return c.convertCallStmt(s)
	case *SlotInst:
		return c.convertSlotInst(s)
	case *ErrorBoundary:
		return c.convertErrorBoundary(s)
	case *Assign:
		return &ast.AssignStmt{
			Target: c.convertExpr(s.Target).(ast.TargetExpr),
			Op:     s.Op,
			Value:  c.convertExpr(s.Value),
		}
	case *Toggle:
		return &ast.ToggleStmt{
			Target: c.convertExpr(s.Target).(ast.TargetExpr),
		}
	case *Emit:
		// Emits are surfaced as ordinary calls: `event(args)`.
		return &ast.CallStmt{
			Call: &ast.CallExpr{
				Func: &ast.IdentExpr{Name: s.Name},
				Args: c.convertCallArgList(s.Args),
			},
		}
	case *LocalVar:
		vs := &ast.VarStmt{
			Name: s.Name,
			Type: c.convertType(s.Type),
		}
		if s.Init != nil {
			vs.Init = c.convertExpr(s.Init)
		}
		return vs
	case *Return:
		rs := &ast.ReturnStmt{}
		if s.Value != nil {
			rs.Value = c.convertExpr(s.Value)
		}
		return rs
	case *If:
		is := &ast.IfStmt{
			Cond: c.convertExpr(s.Cond),
			Body: c.convertStmtBlock(s.Body),
		}
		if len(s.Else) > 0 {
			is.Else = c.convertStmtBlock(s.Else)
		}
		return is
	case *For:
		fs := &ast.ForStmt{
			Key:   s.Key,
			Value: s.Value,
			Iter:  c.convertExpr(s.Iter),
			Body:  c.convertStmtBlock(s.Body),
		}
		if len(s.Else) > 0 {
			fs.Else = c.convertStmtBlock(s.Else)
		}
		return fs
	case *Break:
		return &ast.BreakStmt{Pos: posOfBreak(s)}
	case *Continue:
		return &ast.ContinueStmt{Pos: posOfContinue(s)}
	case *ContextProvider:
		return c.convertContextProvider(s)
	case *CanvasRedrawStmt:
		// A repaint the canvas lowering synthesized. No program can write one,
		// so it has no source form; printed as the call it behaves like, so
		// that `dump --stage lowered` over a reactive canvas prints rather
		// than panics.
		// Named for the canvas, not for a draw function: which function
		// paints it is codegen's to decide, and by this stage there is none
		// to print.
		name := "canvas"
		if s.Canvas != nil && s.Canvas.ID != "" {
			name = s.Canvas.ID
		}
		return &ast.CallStmt{Call: &ast.CallExpr{
			Func: &ast.IdentExpr{Name: "__canvasRedraw"},
			Args: ast.ArgList{Args: []ast.ArgOrEventHandler{
				ast.Arg{Value: &ast.IdentExpr{Name: name}},
			}},
		}}
	default:
		panic(fmt.Sprintf("ir.Convert: no AST conversion for stmt type %T", s))
	}
}

// convertBodyStmts is one statement list, converted. Shared with the window
// body's own loop, which had a second copy of it and so printed a flattened
// canvas as an empty one.
func (c *converter) convertBodyStmts(stmts []Stmt) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range stmts {
		if as := c.convertStmt(s); as != nil {
			out = append(out, as)
		}
		// A flattened canvas keeps the statements that paint it on the node
		// the flattening replaced, which convertStmt cannot reach: it answers
		// with one statement and these are several. Printed after the
		// createNode they belong to, so `dump --stage lowered` shows the
		// drawing rather than an empty canvas.
		if lv, ok := s.(*LocalVar); ok && lv.CanvasNode != nil {
			out = append(out, c.convertBodyStmts(lv.CanvasNode.Children)...)
		}
	}
	return out
}

func (c *converter) convertStmtBlock(stmts []Stmt) ast.StmtBlock {
	block := ast.StmtBlock{Stmts: c.convertBodyStmts(stmts)}
	block.IsMultiline = len(block.Stmts) > 0
	// Set Pos so IsDefined() returns true.
	block.Pos = ast.Pos{Line: 1}
	return block
}

func (c *converter) convertNodeInst(n *NodeInst) *ast.VisualNode {
	var target ast.TargetExpr
	if ns, field, ok := strings.Cut(n.Name, "."); ok {
		target = &ast.SelectExpr{
			Operand: &ast.IdentExpr{Name: ns},
			Field:   field,
		}
	} else {
		target = &ast.IdentExpr{Name: n.Name}
	}
	vn := &ast.VisualNode{
		Target: target,
		ID:     n.ID,
	}

	var args []ast.ArgOrEventHandler
	for _, a := range n.Props {
		args = append(args, ast.Arg{
			Name:  a.Name,
			Value: c.convertExpr(a.Value),
		})
	}
	for _, h := range n.Handlers {
		args = append(args, c.convertEventHandler(&h))
	}
	if n.ErrorHandler != nil {
		args = append(args, c.convertEventHandler(n.ErrorHandler))
	}
	if len(args) > 0 {
		vn.Args = ast.ArgList{
			IsMultiline: len(args) > 3,
			Args:        args,
		}
	}

	if len(n.Children) > 0 || len(n.Slots) > 0 {
		vn.Block = c.convertStmtBlock(n.Children)
		vn.Block.Stmts = append(c.convertSlotContents(n.Slots), vn.Block.Stmts...)
		vn.Block.IsMultiline = true
	}
	// A window that reads its route parameters wrote the population its
	// binding was named in, and the body belongs inside that rather than
	// bare: printed bare, the name the body reads is declared nowhere and the
	// dump does not check back in.
	if n.Params != nil && len(vn.Block.Stmts) > 0 {
		vn.Block.Stmts = []ast.Stmt{&ast.ComponentDecl{
			Name:      windowContentSlot(n),
			HasParens: true,
			Props:     ast.PropList{Props: []ast.ParamOrEventDecl{ast.Param{Name: n.Params.Name}}},
			Body:      ast.StmtBlock{IsMultiline: true, Stmts: vn.Block.Stmts, Pos: ast.Pos{Line: 1}},
		}}
	}
	return vn
}

func (c *converter) convertTypeParams(ps []TypeParam) []ast.TypeParam {
	if len(ps) == 0 {
		return nil
	}
	out := make([]ast.TypeParam, len(ps))
	for i, p := range ps {
		out[i] = ast.TypeParam{Pos: p.Pos, Name: p.Name, Default: c.convertType(p.Default)}
	}
	return out
}

// slotType writes a slot's contract back out, its component entries in the
// positions they were written at.
func (c *converter) slotType(s *SlotDecl) *ast.ComponentType {
	ct := &ast.ComponentType{Tree: c.convertSlotContent(s)}
	for i := range s.Arity() {
		if e, v := s.EntryAt(i); e != nil {
			ct.Params = append(ct.Params, ast.FuncTypeParam{Name: e.Name, Type: c.slotType(e)})
		} else if v < len(s.Params) {
			p := s.Params[v]
			ct.Params = append(ct.Params, ast.FuncTypeParam{Name: p.Name, Type: c.convertType(p.Type)})
		}
	}
	ct.HasParens = len(ct.Params) > 0
	return ct
}

// convertSlotContents renders what a call site supplied for each named slot: a
// component declaration in the instantiation's block, which is what a
// population is.
// Sorted, because the IR holds them in a map and a dump has to be stable.
func (c *converter) convertSlotContents(slots map[string]*SlotContent) []ast.Stmt {
	names := make([]string, 0, len(slots))
	for name := range slots {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ast.Stmt, 0, len(names))
	for _, name := range names {
		sc := slots[name]
		cd := &ast.ComponentDecl{Name: name, Body: c.convertStmtBlock(sc.Body)}
		cd.Body.IsMultiline = true
		for _, p := range sc.Params {
			cd.Props.Props = append(cd.Props.Props, ast.Param{Name: p.Name})
		}
		cd.HasParens = len(cd.Props.Props) > 0
		out = append(out, cd)
	}
	return out
}

// convertErrorBoundary writes a boundary back out as the node it was written
// as: the @error handler in the argument list, the content bare in the block,
// and the fallback as a `component failed` population beside it.
//
// The node's name is the AST's, because the boundary is reached by the
// #[builtin] mark rather than by a name -- a program that shadowed the
// ambient `boundary` and reached the built-in through a qualified alias must
// print back as what it wrote. Without the AST there is nothing left to read
// it off, and the mark's own kind is what is left to say.
//
// Convert had no case at all for a boundary, so `sngl dump --stage checked`
// panicked on every program that wrote one.
func (c *converter) convertErrorBoundary(n *ErrorBoundary) ast.Stmt {
	name := n.AST.TargetName()
	if name == "" {
		name = string(BuiltinErrorBoundary)
	}
	vn := &ast.VisualNode{Target: &ast.IdentExpr{Name: name}}
	if n.Handler != nil {
		vn.Args = ast.ArgList{Args: []ast.ArgOrEventHandler{c.convertEventHandler(n.Handler)}}
	}
	vn.Block = c.convertStmtBlock(n.Children)
	vn.Block.IsMultiline = true
	if len(n.Failed) > 0 {
		cd := &ast.ComponentDecl{Name: n.FailedSlot, Body: c.convertStmtBlock(n.Failed)}
		cd.Body.IsMultiline = true
		vn.Block.Stmts = append([]ast.Stmt{cd}, vn.Block.Stmts...)
	}
	return vn
}

// convertSlotContent is a slot's declared type: the element type, rewrapped in
// whatever the count was read from.
func (c *converter) convertSlotContent(s *SlotDecl) ast.TypeExpr {
	if s.Content == nil {
		// A count with no tree of its own: bare `tree.one` is one of whatever
		// the slot already accepts, which is its owner's family.
		if s.Card == SlotOne {
			return &ast.NamedType{Package: c.treePkg(), Name: "one"}
		}
		return nil
	}
	var elem ast.TypeExpr
	if f := TypeFamily(s.Content); f != nil {
		// A family is spelled through whatever this file imported its package
		// as; convertType reads the name off the declaration and loses that.
		elem = c.treeName(f)
	} else {
		elem = c.convertType(s.Content)
	}
	switch s.Card {
	case SlotOne:
		return &ast.NamedType{Package: c.treePkg(), Name: "one", TypeArgs: []ast.TypeExpr{elem}}
	case SlotOptional:
		return &ast.NamedType{Name: "option", TypeArgs: []ast.TypeExpr{elem}}
	}
	return elem
}

// treePkg is what this file imported sngl:tree as.
func (c *converter) treePkg() string { return c.aliasFor("tree") }

// aliasFor is what this file imported a library package as, defaulting to the
// last segment of its URI — which is the alias an unaliased import binds.
func (c *converter) aliasFor(uri string) string {
	uri = strings.TrimPrefix(uri, "sngl:")
	// sngl:builtin is ambient: every file reaches its names bare, and it
	// cannot be imported under any alias.
	if c.dotted[uri] || uri == "builtin" {
		return ""
	}
	if a := c.aliases[uri]; a != "" {
		return a
	}
	if i := strings.LastIndexByte(uri, '/'); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// treeName spells a family the way the file that names it does: qualified when
// it was declared elsewhere, bare when it was declared here.
func (c *converter) treeName(f *Component) *ast.NamedType {
	nt := &ast.NamedType{Name: f.Name}
	if f.Pkg != "" {
		nt.Package = c.aliasFor(f.Pkg)
	}
	return nt
}

func (c *converter) convertCallStmt(cs *CallStmt) *ast.CallStmt {
	callExpr := c.convertCallExpr(cs.Call)
	return &ast.CallStmt{Call: callExpr}
}

func (c *converter) convertSlotInst(s *SlotInst) *ast.VisualNode {
	// Every insertion is written by name now, the rest slot included.
	vn := &ast.VisualNode{Target: &ast.IdentExpr{Name: s.Name}}
	for _, a := range s.Args {
		vn.Args.Args = append(vn.Args.Args, ast.Arg{Value: c.convertExpr(a)})
	}
	if len(s.Children) > 0 || len(s.Slots) > 0 {
		vn.Block = c.convertStmtBlock(s.Children)
		vn.Block.Stmts = append(c.convertSlotContents(s.Slots), vn.Block.Stmts...)
		vn.Block.IsMultiline = true
	}
	return vn
}

// --- Expression conversion ---

func (c *converter) convertExpr(e Expr) ast.Expr {
	if e == nil {
		return nil
	}
	switch e := e.(type) {
	case *Literal:
		return c.convertLiteral(e)
	case *Ident:
		return c.convertIdent(e)
	case *Binary:
		return &ast.BinaryExpr{
			Op:    e.Op,
			Left:  parenIfLowerPrec(c.convertExpr(e.Left), e.Op, false),
			Right: parenIfLowerPrec(c.convertExpr(e.Right), e.Op, true),
		}
	case *Unary:
		return &ast.UnaryExpr{
			Op:      e.Op,
			Operand: c.convertExpr(e.Operand),
		}
	case *Ternary:
		return &ast.TernaryExpr{
			Cond: c.convertExpr(e.Cond),
			Then: c.convertExpr(e.Then),
			Else: c.convertExpr(e.Else),
		}
	case *Call:
		return c.convertCallExpr(e)
	case *Conversion:
		return c.convertConversion(e)
	case *Select:
		return &ast.SelectExpr{
			Operand: c.convertExpr(e.Operand),
			Field:   e.Field,
		}
	case *Index:
		return &ast.IndexExpr{
			Operand: c.convertExpr(e.Operand),
			Index:   c.convertExpr(e.Idx),
		}
	case *StructLit:
		return c.convertStructLit(e)
	case *ListLit:
		return c.convertListLit(e)
	case *MapLitIR:
		return c.convertMapLit(e)
	case *Spread:
		return &ast.SpreadExpr{
			Operand: c.convertExpr(e.Operand),
		}
	case *Lambda:
		return c.convertLambda(e)
	case *ContextRead:
		return &ast.IdentExpr{Name: e.Ref.Name}
	case *Closure:
		// Render as a synthetic call: __closure(funcRef, structLit). Debug-only —
		// not parseable as user syntax; provides readability for `dump lowered`.
		funcName := "_"
		if e.Func != nil {
			funcName = e.Func.Name
		}
		args := []ast.ArgOrEventHandler{
			ast.Arg{Value: &ast.IdentExpr{Name: funcName}},
		}
		if e.State != nil {
			args = append(args, ast.Arg{Value: c.convertExpr(e.State)})
		}
		return &ast.CallExpr{
			Func: &ast.IdentExpr{Name: "__closure"},
			Args: ast.ArgList{Args: args},
		}
	default:
		panic(fmt.Sprintf("ir.Convert: no AST conversion for expr type %T", e))
	}
}

func (c *converter) convertLiteral(lit *Literal) ast.Expr {
	if lit.Suffix != "" {
		// Raw is the spelling, and a unit literal's is its number followed by
		// its suffix -- the two halves the IR holds apart.
		return &ast.UnitLiteral{
			LiteralExpr: ast.LiteralExpr{
				Kind: ast.LiteralUnit,
				Raw:  lit.Value + lit.Suffix,
			},
			Suffix: lit.Suffix,
		}
	}
	kind := literalKindFromType(lit.Type)
	// The IR holds the value; a literal expression holds the spelling of one.
	if style, isString := ast.StringStyleOf(kind); isString {
		return &ast.LiteralExpr{Kind: kind, Raw: ast.EscapeString(lit.Value, style)}
	}
	return &ast.LiteralExpr{
		Kind: kind,
		Raw:  lit.Value,
	}
}

func (c *converter) convertIdent(id *Ident) ast.Expr {
	// Synthesized element references (IsElementRef) carry reserved __-prefixed
	// names and convert to plain identifiers; the checker re-recognizes the
	// __ prefix as an unresolved dyn reference when the source is reparsed.
	// Bare enum member: Status.active referenced as just "active" in source.
	if id.Member != "" {
		if id.Sym != nil {
			return &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: id.Sym.SymName()},
				Field:   id.Member,
			}
		}
		return &ast.IdentExpr{Name: id.Member}
	}
	return &ast.IdentExpr{Name: id.Name}
}

func (c *converter) convertCallExpr(call *Call) *ast.CallExpr {
	var funcExpr ast.Expr
	if call.Receiver != nil && call.Func != nil {
		// Method call: receiver.method(args)
		funcExpr = &ast.SelectExpr{
			Operand: c.convertExpr(call.Receiver),
			Field:   call.Func.Name,
		}
	} else if call.Receiver != nil {
		// Namespaced component/element (html.div, docui.Foo) — preserve the
		// "ns.Field" select form.
		if call.AST != nil {
			if sel, ok := call.AST.Func.(*ast.SelectExpr); ok {
				funcExpr = &ast.SelectExpr{
					Operand: c.convertExpr(call.Receiver),
					Field:   sel.Field,
				}
			}
		}
		if funcExpr == nil {
			// No AST backref — best effort with just the receiver.
			funcExpr = c.convertExpr(call.Receiver)
		}
	} else if call.Func != nil {
		if call.Func.Receiver != "" {
			// Type-attached method: render as Type.method(args). After checker
			// normalization Args[0] is the receiver value; both "x.method(...)"
			// and "Type.method(...)" syntaxes collapse to this shape.
			funcExpr = &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: call.Func.Receiver},
				Field:   call.Func.Name,
			}
		} else if id, ok := call.Receiver.(*Ident); ok && id.Name != "" {
			// A package function called through its import: the namespace is
			// the alias at the call site, not a receiver on the declaration.
			funcExpr = &ast.SelectExpr{
				Operand: &ast.IdentExpr{Name: id.Name},
				Field:   call.Func.Name,
			}
		} else {
			funcExpr = &ast.IdentExpr{Name: call.Func.Name}
		}
	} else if call.AST != nil {
		// Unresolved call — fall back to AST callee expression.
		funcExpr = call.AST.Func
	} else {
		funcExpr = &ast.IdentExpr{Name: "_"}
	}
	return &ast.CallExpr{
		Func: funcExpr,
		Args: c.convertCallArgList(call.Args),
	}
}

func (c *converter) convertConversion(conv *Conversion) ast.Expr {
	// These conversions have no source spelling -- `list<float>(xs)` names
	// nothing -- and only ever arise implicitly, so the checker re-inserts them.
	if conv.Type != nil {
		switch conv.Type.Kind {
		case TypeList, TypeOption, TypeIter:
			return c.convertExpr(conv.Operand)
		}
	}
	// Type conversions look like calls: int(x), string(x), etc.
	name := conv.Type.String()
	return &ast.CallExpr{
		Func: &ast.IdentExpr{Name: name},
		Args: ast.ArgList{
			Args: []ast.ArgOrEventHandler{
				ast.Arg{Value: c.convertExpr(conv.Operand)},
			},
		},
	}
}

func (c *converter) convertStructLit(sl *StructLit) *ast.StructExpr {
	se := &ast.StructExpr{
		Multiline: len(sl.Fields) > 1,
	}
	if sl.Def != nil && sl.Def.Anon {
		// See convertType. The fields re-intern to the same declaration.
	} else if sl.Def != nil {
		se.Name = sl.Def.Name
		if sl.Type != nil && sl.Type.Package != "" {
			se.Package = sl.Type.Package
		}
	} else if sl.Type != nil && sl.Type.Decl != nil {
		se.Name = sl.Type.Decl.SymName()
	}
	for _, f := range sl.Fields {
		sf := ast.StructFieldLit{
			Name:   f.Name,
			Spread: f.Spread,
		}
		if f.Value != nil {
			sf.Value = c.convertExpr(f.Value)
		}
		se.Fields = append(se.Fields, sf)
	}
	return se
}

func (c *converter) convertMapLit(ml *MapLitIR) *ast.MapLit {
	out := &ast.MapLit{}
	for _, en := range ml.Entries {
		out.Entries = append(out.Entries, ast.MapEntry{
			Key:   c.convertExpr(en.Key),
			Value: c.convertExpr(en.Value),
		})
	}
	return out
}

func (c *converter) convertListLit(ll *ListLit) *ast.ListExpr {
	le := &ast.ListExpr{
		IsMultiline: len(ll.Elems) > 3,
	}
	for _, e := range ll.Elems {
		le.Elements = append(le.Elements, c.convertExpr(e))
	}
	return le
}

func (c *converter) convertLambda(lam *Lambda) *ast.LambdaExpr {
	le := &ast.LambdaExpr{
		Params: c.convertParamList(lam.Func.Params),
	}
	if lam.Func.Return != nil {
		le.ReturnType = c.convertType(lam.Func.Return)
	}
	if len(lam.Func.Block) > 0 {
		le.Block = c.convertStmtBlock(lam.Func.Block)
	}
	return le
}

// --- Type conversion ---

func (c *converter) convertType(t *Type) ast.TypeExpr {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case TypeList:
		nt := &ast.NamedType{Name: "list"}
		if len(t.Elems) > 0 {
			nt.TypeArgs = []ast.TypeExpr{c.convertType(t.Elems[0])}
		}
		return nt
	case TypeOption:
		nt := &ast.NamedType{Name: "option"}
		if len(t.Elems) > 0 {
			nt.TypeArgs = []ast.TypeExpr{c.convertType(t.Elems[0])}
		}
		return nt
	case TypeRef:
		nt := &ast.NamedType{Name: "ref"}
		if len(t.Elems) > 0 {
			nt.TypeArgs = []ast.TypeExpr{c.convertType(t.Elems[0])}
		}
		return nt
	case TypeRemote:
		// Qualified, unlike list and option: the declaration lives in sngl:remote
		// rather than being ambient, so `Value<T>` on its own would not resolve
		// where the printed source is read back.
		nt := &ast.NamedType{Package: "remote", Name: "Value"}
		if len(t.Elems) > 0 {
			nt.TypeArgs = []ast.TypeExpr{c.convertType(t.Elems[0])}
		}
		return nt
	case TypeStruct, TypeEnum, TypeUnit, TypeComponent, TypeInstance:
		// An anonymous struct's spelling is its fields: the synthesized name
		// resolves to nothing where the printed source is read back.
		if sd, ok := t.Decl.(*StructDef); ok && sd.Anon {
			return c.convertAnonStructType(sd)
		}
		// Bare `component` carries no declaration — it is the widest component
		// type, not an unresolved one, so it has a spelling of its own.
		name := "dyn" // anonymous/unresolved declaration
		switch {
		case t.Decl != nil:
			name = t.Decl.SymName()
		case t.Kind == TypeComponent:
			name = "component"
		}
		nt := &ast.NamedType{Name: name}
		if t.Package != "" {
			nt.Package = t.Package
		}
		// A parameterized struct holds its bound arguments in Elems, in the
		// declaration order the spelling wants them back in.
		if sd, ok := t.Decl.(*StructDef); ok && len(sd.TypeParams) > 0 {
			for _, e := range t.Elems {
				nt.TypeArgs = append(nt.TypeArgs, c.convertType(e))
			}
		}
		return nt
	case TypeFunc:
		return c.convertFuncType(t.Sig)
	case TypeTypeParam:
		return &ast.NamedType{Name: t.ParamName}
	default:
		// Primitives: bool, int, float, string, color, date, etc.
		return &ast.NamedType{Name: t.String()}
	}
}

func (c *converter) convertFuncType(sig *FuncSig) ast.TypeExpr {
	if sig == nil {
		return &ast.NamedType{Name: "func"}
	}
	ft := &ast.FuncType{}
	for _, p := range sig.Params {
		ft.Params = append(ft.Params, ast.FuncTypeParam{Type: c.convertType(p.Type)})
	}
	if sig.Return != nil {
		ft.Return = c.convertType(sig.Return)
	}
	return ft
}

// --- Helpers ---

func (c *converter) convertParamList(params []*Param) ast.ParamList {
	pl := ast.ParamList{
		IsMultiline: len(params) > 3,
	}
	for _, p := range params {
		ap := ast.Param{
			Name:  p.Name,
			Type:  c.convertType(p.Type),
			Const: p.Const,
		}
		if p.Default != nil {
			ap.Default = c.convertExpr(p.Default)
		}
		pl.Params = append(pl.Params, ap)
	}
	return pl
}

// convertEventDecl spells an event so it parses as the same declaration: the
// bare `@change T` for one unnamed parameter, `@tick` for the loose `dyn` one
// that spelling has always meant, and the parenthesised list otherwise --
// `@done()` for none, since a bare `@done` would read back as the loose form.
func (c *converter) convertEventDecl(e *EventDecl) ast.EventDecl {
	ed := ast.EventDecl{Name: e.Name}
	if len(e.Params) == 1 && e.Params[0].Name == "" {
		if t := e.Params[0].Type; t != nil && t.Kind != TypeDyn {
			ed.Params = []ast.FuncTypeParam{{Type: c.convertType(t)}}
		}
		return ed
	}
	ed.HasParens = true
	for _, p := range e.Params {
		ed.Params = append(ed.Params, ast.FuncTypeParam{Name: p.Name, Type: c.convertType(p.Type)})
	}
	return ed
}

func (c *converter) convertEventHandler(h *EventHandler) ast.EventHandler {
	eh := ast.EventHandler{
		Name:   h.Name,
		Params: c.convertParamList(h.Func.Params),
	}
	if len(h.Func.Block) > 0 {
		eh.Body = c.convertStmtBlock(h.Func.Block)
	}
	return eh
}

func (c *converter) convertCallArgList(args []CallArg) ast.ArgList {
	al := ast.ArgList{
		IsMultiline: len(args) > 3,
	}
	for _, a := range args {
		al.Args = append(al.Args, ast.Arg{
			Name:  a.Name,
			Value: c.convertExpr(a.Value),
		})
	}
	return al
}

// literalKindFromType maps an IR type to the AST literal kind.
func literalKindFromType(t *Type) ast.LiteralKind {
	if t == nil {
		return ast.LiteralInt
	}
	switch t.Kind {
	case TypeBool:
		return ast.LiteralBool
	case TypeInt:
		return ast.LiteralInt
	case TypeFloat:
		return ast.LiteralFloat
	case TypeString:
		return ast.LiteralStringQuoted
	case TypeNull:
		return ast.LiteralNull
	case TypeUnit:
		return ast.LiteralUnit
	default:
		return ast.LiteralInt
	}
}

func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// binaryPrec returns the operator precedence used for paren insertion when
// converting back to AST. Higher = binds tighter. Mirrors common arithmetic
// precedence; the formatter never reads this — only the relative ordering
// matters.
func binaryPrec(op ast.BinaryOp) int {
	switch op {
	case ast.BinMul, ast.BinDiv, ast.BinMod:
		return 5
	case ast.BinAdd, ast.BinSub:
		return 4
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		return 3
	case ast.BinEq, ast.BinNeq:
		return 2
	case ast.BinAnd:
		return 1
	case ast.BinOr:
		return 0
	}
	return 0
}

// parenIfLowerPrec wraps child in a ParenExpr when its binary operator
// would group differently than the parent's without parens. Left children
// need parens only when their precedence is strictly lower than the
// parent's; right children also need parens at equal precedence to
// preserve left-associativity (`a - (b - c)` would otherwise lose its
// parens and become `a - b - c`).
func parenIfLowerPrec(child ast.Expr, parentOp ast.BinaryOp, isRight bool) ast.Expr {
	cb, ok := child.(*ast.BinaryExpr)
	if !ok {
		return child
	}
	cp := binaryPrec(cb.Op)
	pp := binaryPrec(parentOp)
	if cp < pp || (cp == pp && isRight) {
		return &ast.ParenExpr{Inner: child}
	}
	return child
}

// posOfBreak and posOfContinue recover the escape's source position, which is
// the only thing either node carries.
func posOfBreak(s *Break) ast.Pos {
	if s == nil || s.AST == nil {
		return ast.Pos{}
	}
	return s.AST.Pos
}

func posOfContinue(s *Continue) ast.Pos {
	if s == nil || s.AST == nil {
		return ast.Pos{}
	}
	return s.AST.Pos
}
