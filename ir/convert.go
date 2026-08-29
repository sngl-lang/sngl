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

// ConvertExpr reconstructs an AST expression from an IR expression.
// Synthetic expressions (no AST backref) are materialized from IR fields.
// Bridges the IR → AST-codegen migration while platform generators are
// incrementally ported to consume IR directly.
func ConvertExpr(e Expr) ast.Expr {
	c := &converter{}
	return c.convertExpr(e)
}

// ConvertStmt reconstructs an AST statement from an IR statement.
func ConvertStmt(s Stmt) ast.Stmt {
	c := &converter{}
	return c.convertStmt(s)
}

type converter struct {
	// treeAlias is what this package imported sngl://tree under, so a slot's
	// count wrapper is spelled the way the source spells it.
	treeAlias string
}

// --- Package → Document ---

func (c *converter) convertPackage(pkg *Package) *ast.Document {
	var stmts []ast.Stmt

	for _, imp := range pkg.Imports {
		if imp.Path == "sngl://tree" {
			c.treeAlias = imp.Alias
			break
		}
	}

	for _, imp := range pkg.Imports {
		// Macro-package imports are injected by the library load, not written
		// by this package's source, and the checker re-injects them on every
		// check. Emitting them would put compiler-internal imports in
		// user-facing output — where they also collide with the names the
		// standard-library import lifts.
		if strings.HasPrefix(imp.Path, "sngl://internal/") {
			continue
		}
		stmts = append(stmts, c.convertImport(imp))
	}
	for _, s := range pkg.Structs {
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
		if w.Checked && w.Name == "" && len(w.Body) == 0 {
			continue // component-scoped stub; content is in the component body
		}
		stmts = append(stmts, c.convertWindow(w))
	}
	for _, t := range pkg.Timers {
		stmts = append(stmts, c.convertTimer(t))
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
		IsMultiline: len(s.Fields) > 1,
	}
	for _, f := range s.Fields {
		sf := &ast.StructField{
			Names: []string{f.Name},
			Type:  c.convertType(f.Type),
		}
		if f.Default != nil {
			sf.Default = c.convertExpr(f.Default)
		}
		def.Body = append(def.Body, sf)
	}
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
		def.Suffixes = append(def.Suffixes, us)
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
	return &ast.ConstDecl{Specs: []ast.VarSpec{spec}}
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
	return &ast.VarDecl{Specs: []ast.VarSpec{spec}}
}

func (c *converter) convertFuncDef(f *Func) *ast.FuncDef {
	name := f.Name
	if f.Receiver != "" {
		name = f.Receiver + "." + f.Name
	}
	fd := &ast.FuncDef{
		Name:       name,
		TypeParams: f.TypeParams,
		Params:     c.convertParamList(f.Params),
	}
	if f.Return != nil {
		fd.ReturnType = c.convertType(f.Return)
	}
	if len(f.Block) > 0 {
		fd.Block = c.convertStmtBlock(f.Block)
	}
	return fd
}

func (c *converter) convertComponent(comp *Component) *ast.ComponentDecl {
	cd := &ast.ComponentDecl{
		Name: comp.Name,
	}

	// Build prop list.
	var props []ast.ParamOrEventDecl
	for _, p := range comp.Props {
		param := ast.Param{
			Name:          p.Name,
			Type:          c.convertType(p.Type),
			Bidirectional: p.Bidirectional,
		}
		if p.Default != nil {
			param.Default = c.convertExpr(p.Default)
		}
		props = append(props, param)
	}
	for _, e := range comp.Events {
		ed := ast.EventDecl{Name: e.Name}
		if e.Type != nil {
			ed.Type = c.convertType(e.Type)
		}
		props = append(props, ed)
	}
	for _, s := range comp.Slots {
		sd := ast.SlotDecl{Name: s.Name, Type: c.convertSlotContent(s)}
		for _, p := range s.Params {
			sd.Params = append(sd.Params, c.convertType(p))
		}
		props = append(props, sd)
	}
	if len(props) > 0 {
		cd.Props = ast.PropList{
			IsMultiline: len(props) > 3,
			Props:       props,
		}
	}

	// A default slot already says what ChildrenType says — it is what set it —
	// and a declaration carrying both is refused on the way back in.
	if comp.ChildrenType != nil && findSlotDecl(comp, DefaultSlot) == nil {
		cd.ChildrenType = c.convertType(comp.ChildrenType)
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
	for _, t := range comp.Timers {
		bodyStmts = append(bodyStmts, c.convertTimer(t))
	}
	if len(comp.Body) > 0 {
		bodyBlock := c.convertStmtBlock(comp.Body)
		bodyStmts = append(bodyStmts, bodyBlock.Stmts...)
	}
	if len(bodyStmts) > 0 {
		cd.Body = ast.StmtBlock{
			IsMultiline: true,
			Stmts:       bodyStmts,
			Pos:         ast.Pos{Line: 1},
		}
	}

	return cd
}

func (c *converter) convertWindow(w *Window) *ast.VisualNode {
	vn := &ast.VisualNode{
		Target: &ast.IdentExpr{Name: "window"},
		ID:     w.Name,
	}
	var bodyStmts []ast.Stmt
	for _, v := range w.Vars {
		if v.IsConst {
			bodyStmts = append(bodyStmts, c.convertConstDecl(v))
		} else {
			bodyStmts = append(bodyStmts, c.convertVarDecl(v))
		}
	}
	for _, f := range w.Funcs {
		bodyStmts = append(bodyStmts, c.convertFuncDef(f))
	}
	for _, s := range w.Body {
		bodyStmts = append(bodyStmts, c.convertStmt(s))
	}
	if len(bodyStmts) > 0 {
		vn.Block = ast.StmtBlock{
			IsMultiline: len(bodyStmts) > 0,
			Stmts:       bodyStmts,
			Pos:         ast.Pos{Line: 1},
		}
	}
	return vn
}

func (c *converter) convertTimer(t *Timer) *ast.VisualNode {
	vn := &ast.VisualNode{
		Target: &ast.IdentExpr{Name: "timer"},
	}
	if t.Interval != nil {
		vn.Args = ast.ArgList{
			Args: []ast.ArgOrEventHandler{
				ast.Arg{Value: c.convertExpr(t.Interval)},
			},
		}
	}
	if len(t.Handler.Block) > 0 {
		vn.Block = c.convertStmtBlock(t.Handler.Block)
	}
	return vn
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

// convertOutputs groups outputs by language and emits the nested form:
//
//	output { lang { platform(opts...) } }
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

	var langNodes []ast.Stmt
	for _, g := range groups {
		langNode := &ast.VisualNode{
			Target: &ast.IdentExpr{Name: g.lang},
		}
		if len(g.outputs) > 0 {
			var platStmts []ast.Stmt
			for _, o := range g.outputs {
				platNode := &ast.VisualNode{
					Target: &ast.IdentExpr{Name: o.Platform},
				}
				if o.Options != nil && len(o.Options.Fields) > 0 {
					args := make([]ast.ArgOrEventHandler, 0, len(o.Options.Fields))
					for _, f := range o.Options.Fields {
						args = append(args, ast.Arg{Name: f.Name, Value: c.convertExpr(f.Value)})
					}
					platNode.Args = ast.ArgList{
						IsMultiline: len(args) > 3,
						Args:        args,
					}
				}
				platStmts = append(platStmts, platNode)
			}
			langNode.Block = ast.StmtBlock{
				Pos:         ast.Pos{Line: 1},
				IsMultiline: len(platStmts) > 1,
				Stmts:       platStmts,
			}
		}
		langNodes = append(langNodes, langNode)
	}

	return &ast.VisualNode{
		Target: &ast.IdentExpr{Name: "output"},
		Block: ast.StmtBlock{
			Pos:         ast.Pos{Line: 1},
			IsMultiline: len(langNodes) > 1,
			Stmts:       langNodes,
		},
	}
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
	case *Window:
		return c.convertWindow(s)
	case *ContextProvider:
		return c.convertContextProvider(s)
	default:
		panic(fmt.Sprintf("ir.Convert: no AST conversion for stmt type %T", s))
	}
}

func (c *converter) convertStmtBlock(stmts []Stmt) ast.StmtBlock {
	block := ast.StmtBlock{}
	for _, s := range stmts {
		if as := c.convertStmt(s); as != nil {
			block.Stmts = append(block.Stmts, as)
		}
	}
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
	if len(args) > 0 {
		vn.Args = ast.ArgList{
			IsMultiline: len(args) > 3,
			Args:        args,
		}
	}

	if len(n.Children) > 0 || len(n.Slots) > 0 {
		vn.Block = c.convertStmtBlock(n.Children)
		vn.Block.Stmts = append(c.convertSlotContents(n), vn.Block.Stmts...)
		vn.Block.IsMultiline = true
	}
	return vn
}

// convertSlotContents renders what a call site supplied for each named slot.
// Sorted, because the IR holds them in a map and a dump has to be stable.
func (c *converter) convertSlotContents(n *NodeInst) []ast.Stmt {
	names := make([]string, 0, len(n.Slots))
	for name := range n.Slots {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ast.Stmt, 0, len(names))
	for _, name := range names {
		sc := n.Slots[name]
		sn := &ast.SlotNode{Name: name, Block: c.convertStmtBlock(sc.Body)}
		for _, p := range sc.Params {
			sn.Args = append(sn.Args, &ast.IdentExpr{Name: p.Name})
		}
		out = append(out, sn)
	}
	return out
}

// convertSlotContent is a slot's declared type: the element type, rewrapped in
// whatever the count was read from.
func (c *converter) convertSlotContent(s *SlotDecl) ast.TypeExpr {
	if s.Content == nil {
		return nil
	}
	elem := c.convertType(s.Content)
	switch s.Card {
	case SlotOne:
		pkg := c.treeAlias
		if pkg == "" {
			pkg = "tree"
		}
		return &ast.NamedType{Package: pkg, Name: "one", TypeArgs: []ast.TypeExpr{elem}}
	case SlotOptional:
		return &ast.NamedType{Name: "option", TypeArgs: []ast.TypeExpr{elem}}
	}
	return elem
}

func findSlotDecl(comp *Component, name string) *SlotDecl {
	for _, s := range comp.Slots {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (c *converter) convertCallStmt(cs *CallStmt) *ast.CallStmt {
	callExpr := c.convertCallExpr(cs.Call)
	return &ast.CallStmt{Call: callExpr}
}

func (c *converter) convertSlotInst(s *SlotInst) *ast.VisualNode {
	// The default slot's insertion is spelled `slot`, not by its name: `_` is
	// what the declaration calls it, and the body has the keyword for it.
	name := s.Name
	if name == "" || name == DefaultSlot {
		name = "slot"
	}
	vn := &ast.VisualNode{Target: &ast.IdentExpr{Name: name}}
	for _, a := range s.Args {
		vn.Args.Args = append(vn.Args.Args, ast.Arg{Value: c.convertExpr(a)})
	}
	if len(s.Children) > 0 {
		vn.Block = c.convertStmtBlock(s.Children)
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
		return &ast.UnitLiteral{
			LiteralExpr: ast.LiteralExpr{
				Kind: ast.LiteralUnit,
				Raw:  lit.Raw,
			},
			Suffix: lit.Suffix,
		}
	}
	return &ast.LiteralExpr{
		Kind: literalKindFromType(lit.Type),
		Raw:  lit.Raw,
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

func (c *converter) convertConversion(conv *Conversion) *ast.CallExpr {
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
	if sl.Def != nil {
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
	case TypeStruct, TypeEnum, TypeUnit, TypeComponent:
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
			Name: p.Name,
			Type: c.convertType(p.Type),
		}
		if p.Default != nil {
			ap.Default = c.convertExpr(p.Default)
		}
		pl.Params = append(pl.Params, ap)
	}
	return pl
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
