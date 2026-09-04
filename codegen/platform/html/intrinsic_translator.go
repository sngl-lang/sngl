package html

import (
	"context"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/internal/htmlutil"
	"git.duckfam.us/jonathan/sngl/ir"
)

// htmlTranslator implements codegen.IntrinsicTranslator for html.
// Produces ir.Stmt fragments that the JS renderer (jc.EvalStmt)
// emits as JavaScript inside the page's bootstrap <script> block.
//
// Unlike fyne/gtk4: no Model receiver. Node refs are JS top-level
// consts initialized via document.createElement(...). State vars
// live on a `state` object: `state.<name>` (handled by the JS
// renderer's evalIdent state-var path).
type htmlTranslator struct {
	jc       *javascript.JsIRContext
	idTags   map[string]string // id ("__n0") → SNGL tag ("text")
	topLevel []string          // ids not yet AppendChild'd
	// idToNode maps an element id to the NodeInst it was built from. A
	// lowered node op names its node by id and carries nothing else, so this
	// is where the op's element and its declaration are recovered — which
	// prop is a DOM property, which SNGL prop maps to which DOM one, and
	// which events the element has.
	idToNode map[string]*ir.NodeInst
	// elem answers for an op whose node predates no prewalk entry — see
	// htmlGen.elemDecl.
	elem *ir.Component
	// refToVar maps the name a lowered node op uses for its node to the JS
	// variable the element was actually emitted as. The two differ whenever a
	// program wrote an `#id`: the lowering leaves that id on the node and
	// names it in every updater it builds, while the page allocates `$N` for
	// the element itself. Without the mapping such an updater assigns to an
	// identifier nothing declares, and the page throws on the first
	// interaction that fires it.
	refToVar map[string]string
}

func (g *htmlGen) newHTMLTranslator(jc *javascript.JsIRContext) *htmlTranslator {
	return &htmlTranslator{jc: jc, idTags: map[string]string{}, idToNode: g.idToNode, refToVar: g.refToVar, elem: g.elemDecl}
}

// nodeRef is node with an element-ref name resolved to the variable the
// element was emitted as. Identity for a ref the page emitted under its own
// name, which is every synthesized `__nN`.
func (t *htmlTranslator) nodeRef(node ir.Expr) ir.Expr {
	id, isIdent := node.(*ir.Ident)
	if !isIdent || !id.IsElementRef || t.refToVar == nil {
		return node
	}
	v, mapped := t.refToVar[id.Name]
	if !mapped || v == id.Name {
		return node
	}
	clone := *id
	clone.Name = v
	return &clone
}

// newHTMLTranslatorWithNodes is like newHTMLTranslator but also threads an
// id → NodeInst map so OnPropAssign can map SNGL component props to DOM
// props via domWriteForIR. Required when translating handler/timer/setter
// bodies that NoReactivity injects with the original SNGL prop names.
func (g *htmlGen) newHTMLTranslatorWithNodes(jc *javascript.JsIRContext, idToNode map[string]*ir.NodeInst) *htmlTranslator {
	t := g.newHTMLTranslator(jc)
	t.idToNode = idToNode
	return t
}

var (
	_ codegen.IntrinsicTranslator = (*htmlTranslator)(nil)
	// The DOM can put a child at a position, so html declares the capability
	// in Capabilities() and answers the op here. The two must agree:
	// TestInsertBeforeCapabilityMatchesTranslator checks every registered
	// platform.
	_ codegen.ChildInserter = (*htmlTranslator)(nil)
)

// The shape of a component instance in the emitted JS. An instance is a plain
// object: the root node it renders as, one updater per prop the instance can
// absorb, and a teardown. Named here rather than spelled at each use so the
// factory and the translator cannot drift -- which is how `__cf_<name>` was
// called for years without anything defining it.
const (
	instanceRootField     = "__root"
	instanceDestroyMethod = "__destroy"
)

// instanceUpdateMethod is the updater an instance carries for one prop.
func instanceUpdateMethod(prop string) string {
	return "__set_" + sanitizeInstanceProp(prop)
}

// sanitizeInstanceProp makes a prop name safe as a JS identifier fragment. A
// wildcard prop's name comes from a call site and need not be one.
func sanitizeInstanceProp(prop string) string {
	out := make([]rune, 0, len(prop))
	for _, r := range prop {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// declOf is the declaration of the element an op targets.
func (t *htmlTranslator) declOf(node ir.Expr) *ir.Component {
	if id, ok := node.(*ir.Ident); ok {
		if n := t.idToNode[id.Name]; n != nil {
			return n.Component
		}
	}
	return t.elem
}

func (t *htmlTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	// passInlinePure substitutes stdlib wrapper components (vbox, text,
	// button, ...) with html.sngl's native element bodies before this
	// translator runs, so every tag landing here is a native HTML
	// element name (span, button, input, div, ...).
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)

	// const <id> = document.createElement("<tag>")
	createCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "document"},
		Func:     &ir.Func{Name: "createElement"},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: tag}}},
	}
	return []ir.Stmt{&ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: createCall,
	}}
}

// OnCreateComponent preserves the LocalVar as-is. html inlines user
// components via renderIRUserComponent on the static-tree path; on the
// WalkLowered (JS) path a non-inlinable component instance keeps its
// original `const id = ...CreateComponent(...)` binding.
func (t *htmlTranslator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	return []ir.Stmt{&ir.LocalVar{Name: id, Type: ir.TypDyn, Init: call}}
}

// OnComponentRoot binds a name to the node an instance renders as. The record
// carries it under a fixed field, which is html's own choice of shape: nothing
// outside this platform names it.
func (t *htmlTranslator) OnComponentRoot(ctx context.Context, id string, inst ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: &ir.Select{Type: ir.TypDyn, Operand: inst, Field: instanceRootField},
	}}
}

// OnUpdateComponent patches a prop on a live instance by calling the updater
// the instance carries for it.
//
// A prop with no updater does not reach here, and this cannot be the place
// that says so: the op names the instance by an id, not the declaration whose
// setters would answer. Both sites that emit the op ask instead --
// reuseOrCreate for a prop inside a reactive slot, collectFromNode for one at
// a static position -- and each either rebuilds the instance or reports. This
// used to claim the check without there being one at either end, which is how
// `__n1.__set_start(...)` reached a page whose instance exported only
// `__set_tail`.
func (t *htmlTranslator) OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: inst,
		Func:     &ir.Func{Name: instanceUpdateMethod(prop)},
		Args:     []ir.CallArg{{Value: value}},
	}}}
}

// OnDestroyComponent runs the instance's teardown. Detaching the node is the
// caller's business: RemoveChild already says that, and an instance is
// detached and reattached more often than it is destroyed.
func (t *htmlTranslator) OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: inst,
		Func:     &ir.Func{Name: instanceDestroyMethod},
	}}}
}

func (t *htmlTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent, child = t.nodeRef(parent), t.nodeRef(child)
	// Child appended somewhere → no longer top-level.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "appendChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

// OnInsertBefore puts a child at a position. The DOM's own insertBefore takes
// a null ref to mean the end, which is the rule the protocol borrowed, so a
// reconciliation can pass the next surviving node without first asking whether
// there is one.
func (t *htmlTranslator) OnInsertBefore(ctx context.Context, parent, child, ref ir.Expr) []ir.Stmt {
	parent, child, ref = t.nodeRef(parent), t.nodeRef(child), t.nodeRef(ref)
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "insertBefore"},
		Args:     []ir.CallArg{{Value: child}, {Value: ref}},
	}}}
}

func (t *htmlTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent, child = t.nodeRef(parent), t.nodeRef(child)
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "removeChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	domEvent := domEventName(t.declOf(node), event)
	if domEvent == "" {
		return nil
	}
	node = t.nodeRef(node)
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: node,
		Func:     &ir.Func{Name: "addEventListener"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Value: domEvent}},
			{Value: handler},
		},
	}}}
}

// OnDetachHandler is addEventListener's inverse, and takes the same handler
// expression because the DOM matches listeners by reference: passing anything
// else removes nothing, and says so in no way at all.
func (t *htmlTranslator) OnDetachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	domEvent := domEventName(t.declOf(node), event)
	if domEvent == "" {
		return nil
	}
	node = t.nodeRef(node)
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: node,
		Func:     &ir.Func{Name: "removeEventListener"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Value: domEvent}},
			{Value: handler},
		},
	}}}
}

func (t *htmlTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	// SNGL component prop → DOM prop mapping. NoReactivity-lowered
	// handler/timer/setter bodies arrive here with the original SNGL
	// prop name (e.g. text.value), so consult idToNode when set to
	// produce the correct DOM-side write.
	// The declaration is looked up under the name the op used, and the write
	// is emitted against the variable the element was emitted as. For a
	// program-written `#id` those are two different strings.
	if t.idToNode != nil {
		if id, ok := node.(*ir.Ident); ok && id.IsElementRef {
			if n := t.idToNode[id.Name]; n != nil {
				if stmts, ok := domWriteForIR(n.Name, prop, t.nodeRef(node), value); ok {
					return stmts
				}
			}
		}
	}
	node = t.nodeRef(node)
	// The prop the tag name binds to names the element; the tag already
	// reached OnCreateNode, and there is no attribute to write it as.
	if prop == tagProp {
		return nil
	}
	setAttr := func(name string, v ir.Expr) []ir.Stmt {
		return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: node,
			Func:     &ir.Func{Name: "setAttribute"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Value: name}},
				{Value: v},
			},
		}}}
	}
	// A `style={...}` struct is a set of CSS declarations, not a value any
	// DOM sink accepts: passed through it stringifies to "[object Object]".
	if prop == "style" {
		if sl, ok := value.(*ir.StructLit); ok {
			css := htmlutil.BuildCSSStyleIR([]ir.Arg{{Name: "style", Value: sl}})
			if css == "" {
				return nil
			}
			return setAttr(prop, &ir.Literal{Type: ir.TypString, Value: css})
		}
	}
	// A wildcard prop is a map of the names it collected, not a name of its
	// own: writing it as one produces an attribute literally called "attrs"
	// whose value stringifies to "[object Map]", and loses every name in it.
	// The two markup paths (nodeProps, elementAttrs) already unpack it; this is
	// the third, and the one a node inside a `for` or a reactive slot takes.
	if prop == attrsProp {
		m, ok := value.(*ir.MapLitIR)
		if !ok {
			return nil
		}
		var out []ir.Stmt
		for _, e := range m.Entries {
			k, ok := e.Key.(*ir.Literal)
			if !ok {
				continue
			}
			out = append(out, t.OnPropAssign(ctx, node, k.Value, e.Value)...)
		}
		return out
	}
	if field, ok := domPropForProp(t.declOf(node), prop); ok {
		// node.<field> = value
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: node, Field: field, Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  value,
		}}
	}
	return setAttr(prop, value)
}

// domWriteForIR is the IR-level mirror of domWriteFor in html.go. Returns
// (stmts, true) when the prop has to be written somewhere other than the node
// itself, or (nil, false) when the caller should fall back to the generic
// DOM-name dispatch -- which is where a prop's DOM spelling is decided, off
// the element's declaration.
//
// What remains here is structural: which element a prop lands on, not what it
// is called. A checkbox's `checked` belongs to the <input> inside the <label>
// the node id is on, and no declaration of that label says so.

func domWriteForIR(componentName, prop string, node, value ir.Expr) ([]ir.Stmt, bool) {
	switch componentName {
	// No `progress` case: it wrote `value` through setAttribute while the
	// init path, reading the same prop off the element declaration, wrote the
	// property -- one reactive value, two spellings. The declaration decides
	// for both now. What stays here is structural, where a prop has to be
	// written rather than what it is called.
	case "checkbox", "toggle":
		if prop == "checked" {
			// The __n* id is on the wrapping <label>; descend to the
			// inner <input> to actually flip the checked state.
			inner := &ir.Call{
				Type:     ir.TypDyn,
				Receiver: node,
				Func:     &ir.Func{Name: "querySelector"},
				Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Value: "input"}}},
			}
			return []ir.Stmt{&ir.Assign{
				Target: &ir.Select{Operand: inner, Field: "checked", Type: ir.TypDyn},
				Op:     ast.AssignSet,
				Value:  value,
			}}, true
		}
	case "modal", "drawer", "popover", "menu":
		if prop == "open" {
			// el.style.display = (cond) ? "" : "none"
			styleSel := &ir.Select{Operand: node, Field: "style", Type: ir.TypDyn}
			displaySel := &ir.Select{Operand: styleSel, Field: "display", Type: ir.TypDyn}
			tern := &ir.Ternary{
				Cond: value,
				Then: &ir.Literal{Type: ir.TypString, Value: ""},
				Else: &ir.Literal{Type: ir.TypString, Value: "none"},
			}
			return []ir.Stmt{&ir.Assign{
				Target: displaySel,
				Op:     ast.AssignSet,
				Value:  tern,
			}}, true
		}
	}
	return nil, false
}

func (t *htmlTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: slot.Name, Synthesized: true, Type: slot.Type},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: slot.Type, Elems: nil},
	}}
}

func (t *htmlTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := &ir.Ident{Name: slot.Name, Synthesized: true, Type: slot.Type}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: slotRef,
		Func:     &ir.Func{Name: "push"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	// JS for-of iterates iterable directly. Synthesized refs render as
	// bare identifiers via JsIRContext.evalIdent's Synthesized hook.
	return iter
}

func (t *htmlTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *htmlTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
