package gtk4

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/ir"
)

// emitShared is the state every translator emitting into one generated file
// accumulates: which preamble helpers that file has to carry, and which
// property assignments could not be emitted. A nil *emitShared answers every
// method, so a scope that emits no widgets need not carry one.
type emitShared struct {
	boolToInt  bool
	gObjectSet bool
	slotAnchor bool
	// calendarDate: a handler reads a GtkCalendar's day through
	// calendarDateHelper.
	calendarDate bool
	// dropDown: the program builds a GtkDropDown, which snglDropDownOffer and
	// gtk4rt's drop-down functions serve.
	dropDown bool
	errs     []error
	seen     map[string]bool

	// cStrings are the literals handed to C, each a package-level var holding
	// its C copy for the application's lifetime; cStringN numbers them and
	// cTempN the locals a non-constant one is bound to (ownCStrings).
	cStrings      []cStringDecl
	cStringByText map[string]string
	cStringN      int
	cTempN        int

	// The canvas metadata passDeclarative flattened out of the program, which every
	// scope in the file reads: a `canvas` tag has no GIR widget behind it, so a
	// translator without these maps reports it as a component gtk4 does not
	// implement and stops the build. It rides on the shared struct rather than
	// on a builder call of its own because a per-site `with` is what three
	// separate scopes have now been found to have forgotten.
	canvasByID   map[string]*canvasMeta
	canvasByNode map[*ir.NodeInst]*canvasMeta

	// The flow and span trees, collected package-wide before the walk. Here
	// for the reason the canvas maps are: a span is no widget, so its words
	// reach the label they belong to only through this, and a reactive splice
	// assigns a span's prop in a scope that never saw the flow. Collecting it
	// once beside them also means it is walked once rather than per scope.
	markup *markupTrees

	// toplevels are every Toplevel id the file creates, since a handler or an
	// updater setting one's `visible` is a scope that did not create it; and
	// toplevelTitles the title each is built with, which the inline-cgo
	// scaffold writes on the window BuildUI makes.
	toplevels      map[string]bool
	toplevelTitles map[string]ir.Expr
	// images are every image primitive id the file creates, by tag.
	images map[string]string
	// scrolls are every Scroll primitive id the file creates.
	scrolls map[string]bool
	// toplevelIcons is each window's icon source in an inline-cgo build, by
	// the window's id.
	toplevelIcons map[string]string
}

func (s *emitShared) needBoolToInt() {
	if s != nil {
		s.boolToInt = true
	}
}

func (s *emitShared) needGObjectSet() {
	if s != nil {
		s.gObjectSet = true
	}
}

func (s *emitShared) needSlotAnchor() {
	if s != nil {
		s.slotAnchor = true
	}
}

// cStringDecl is a package-level `*C.char` made once from a literal.
type cStringDecl struct {
	Name, Text string
}

const cTempPrefix = "__ctmp"

func (s *emitShared) cTempName() string {
	name := fmt.Sprintf("%s%d", cTempPrefix, s.cTempN)
	s.cTempN++
	return name
}

// constCString is the var holding a literal's C copy, one per distinct text.
func (s *emitShared) constCString(text string) ir.Expr {
	if s.cStringByText == nil {
		s.cStringByText = map[string]string{}
	}
	name, ok := s.cStringByText[text]
	if !ok {
		name = fmt.Sprintf("__cstr%d", s.cStringN)
		s.cStringN++
		s.cStringByText[text] = name
		s.cStrings = append(s.cStrings, cStringDecl{Name: name, Text: text})
	}
	return &ir.Ident{Name: name, Type: ir.NativePointerOf("char")}
}

// ownCStrings gives back each C string the statements make. The translator
// builds these calls after lowering, so ReleaseTemps never saw the `C.CString`
// in them, and the release it would have written is written here: a
// literal's copy is a package-level var kept for the application's lifetime,
// and any other is bound to a local before the statement that hands it to C
// and freed after it. Every C function the translator calls copies the string
// it is handed, so none outlives its statement. A nil shared frees nothing,
// as it fails nothing.
func (s *emitShared) ownCStrings(stmts []ir.Stmt) []ir.Stmt {
	if s == nil {
		return stmts
	}
	return ir.RewriteBlocks(stmts, func(block []ir.Stmt) []ir.Stmt {
		out := make([]ir.Stmt, 0, len(block))
		for _, st := range block {
			if lv, ok := st.(*ir.LocalVar); ok && strings.HasPrefix(lv.Name, cTempPrefix) {
				out = append(out, st)
				continue
			}
			var frees []ir.Stmt
			// Only the statement's own expressions: a nested block was
			// rewritten before it, and binding its temps again would free
			// each twice.
			_ = ir.Rewrite(st, func(n ir.Node) (ir.Node, error) {
				if nested, ok := n.(ir.Stmt); ok && nested != st {
					return n, ir.SkipDir
				}
				c, ok := n.(*ir.Call)
				if !ok || !isCString(c) {
					return n, nil
				}
				if lit, ok := c.Args[0].Value.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
					return s.constCString(lit.Value), ir.SkipDir
				}
				ref := &ir.Ident{Name: s.cTempName(), Type: ir.NativePointerOf("char")}
				out = append(out, &ir.LocalVar{Name: ref.Name, Type: ref.Type, Init: c})
				frees = append([]ir.Stmt{&ir.CallStmt{Call: nativeCall("free", ir.TypVoid, cgoCast("", ref))}}, frees...)
				return ref, ir.SkipDir
			})
			out = append(out, st)
			out = append(out, frees...)
		}
		return out
	})
}

func isCString(c *ir.Call) bool {
	recv, ok := c.Receiver.(*ir.Ident)
	return ok && recv.Name == "C" && c.Func != nil && c.Func.Name == "CString" && len(c.Args) == 1
}

// fail records that a property cannot be emitted. The same property assigned
// from several places is one error. A nil sink drops it, which is why every
// scope that resolves widget props is given one.
func (s *emitShared) fail(err error) {
	if s == nil {
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[err.Error()] {
		return
	}
	s.seen[err.Error()] = true
	s.errs = append(s.errs, err)
}

// gtk4Translator implements codegen.IntrinsicTranslator for gtk4. It emits IR
// fragments whose Call.Func carries Foreign.Path="C", which gc.EvalExpr
// renders as cgo source; a cgo cast is an ir.Conversion to a
// NativePointerOf type (see cgoCast).
type gtk4Translator struct {
	gc  *golang.GoIRContext
	pkg *ir.Package // optional; used to consult GIR-resolved native metadata
	// pkgCTypes is collectNodeCTypes(pkg), made when slotCType first asks.
	pkgCTypes map[string]string
	fieldSink func(name, cType string)
	// fieldCTypes is what sinkField declared each field this translator
	// writes as, which is the type a reference to it carries.
	fieldCTypes map[string]string
	// localRefs are the ref ids passNodeEscape found not to escape this scope;
	// they are emitted as function-locals rather than Model fields. nil means
	// every id is a field.
	localRefs map[string]bool
	// fieldIDs are the ids this scope registered as fields regardless of what
	// passNodeEscape concluded. A canvas is the case: its redraw reaches the
	// drawing area from whatever scope mutates the state, and a
	// CanvasRedrawStmt names the draw func rather than the node, so the escape
	// analysis cannot see that use and calls the node local.
	fieldIDs map[string]bool
	idCTypes map[string]string   // id ("__n0") → GTK C type ("GtkLabel")
	skipped  map[string]struct{} // ids whose OnCreateNode emitted nothing (unresolved tag) — later refs to them must be skipped too
	// builtSpans are the spans this scope created. Their build-time prop
	// assignments follow the create, and the markup they would each rewrite
	// was written once already; an assignment from any other scope is a
	// reactive one and is what re-writes it.
	builtSpans map[string]bool
	topLevel   []string
	// rootRenders are the calls written directly in this scope's body that
	// render a slot into the scope's own box; renderedRoot is that box once one
	// has run. Every slot in a body renders into the one box, so the body's own
	// widgets go into it too, in written order, or a widget between two slots
	// lands after both. Empty in a scope whose root is decided some other way,
	// which is every Model scope: the Model's own __root is the wrapper
	// buildWidgetTree already parents into.
	rootRenders  map[ir.Stmt]bool
	renderedRoot ir.Expr
	// slotAnchor is the anchor field of the slot this render func renders,
	// set when the func resets its slot, in wrapped mode.
	slotAnchor ir.Expr
	// wrapped emits widget ops as pkg/go/gtk4rt calls instead of inline cgo.
	// An unmapped op falls through to cgo, leaving a `C.` that triggers
	// emitIR's whole-program fallback. See wrapped.go.
	wrapped      bool
	tagComponent map[string]*ir.Component // tag ("GtkButton") → resolved Component (from pre-walk)
	nodeSource   map[string]ast.Stmt      // node id → the source the node was written as
	// plainHandle names the instance ids whose handle is the widget the render
	// returned rather than a record carrying it, as OnCreateComponent decided
	// from the component's own RuntimeInstance mark. Only a record has a Root
	// field, so OnComponentRoot reads this before selecting one.
	plainHandle map[string]bool
	// registry is the GIR data the widget declarations were generated from.
	// A declaration says which props and events a widget has; the C setter
	// behind each one is read back out of here, keyed by the C type the
	// declaration's #[intrinsic] names. nil in scopes that emit no widgets.
	registry *gir.TypeRegistry
	// shared accumulates what the file being emitted needs beyond the
	// statements themselves: preamble helpers, and the properties that
	// could not be emitted at all. nil in scopes that emit no widgets.
	shared *emitShared

	// toplevels are the Toplevel ids this scope created, and appChildren the
	// nodes it attached to the application, in order.
	toplevels   map[string]bool
	appChildren []string
	// building says this scope is the tree's own build rather than a handler
	// or an updater, which the inline-cgo scaffold can answer only for.
	building bool

	// invokerSink records one (id, event) pair per signal connected, for the
	// test-invoker methods emitted after the walk. nil in the scopes that emit
	// no test surface -- a slot func, a canvas draw -- which is also why this
	// is a sink rather than a field read back off the translator.
	invokerSink func(gtkEventInvoker)
	// indexed are the ids a test triggers one copy of (`c.row[1]`), whose
	// widgets this Model scope keeps by copy (copiesField), and slotNamed
	// those of them this slot render func creates, which its reset empties.
	indexed   map[string]bool
	slotNamed []string
	// present are the ids a test null-tests. slotPresent are the fields of
	// those this slot render func creates -- a widget's own, an instance's
	// presentField -- which its reset clears: what the render no longer builds
	// is not rendered, though its field still holds it.
	present     map[string]bool
	slotPresent []string
	// instanceCopies are the ids a test reaches one copy of whose instances,
	// built at run time, this scope lists by root in a Model map, declared
	// through modelSink: a record's own fields are not the Model's. No slot
	// reset empties the map, since one map holds every record's instances:
	// a re-key replaces an instance's entry, and a destroyed record unlists
	// itself.
	instanceCopies map[string]bool
	modelSink      func(name, cType string)
	// record is set in a component built at run time, which lists the roots
	// of the spliced instances it renders (listRoot) so its Destroy can take
	// them out again.
	record bool
}

// namedWidgetField is the Model field a widget an instance builds is handed
// to, for the test invoker that drives it.
func namedWidgetField(id string) string { return "__named_" + id }

// withIndexed has this Model scope keep the widgets of the ids a test
// indexes; stmts is the scope's body, whose creates of them a slot's reset
// empties.
func (t *gtk4Translator) withIndexed(indexed, present map[string]bool, stmts []ir.Stmt) *gtk4Translator {
	t.indexed = indexed
	t.slotNamed = nil
	t.slotPresent = nil
	t.present = present
	_ = ir.Walk(stmts, func(n ir.Node) error {
		lv, ok := n.(*ir.LocalVar)
		if !ok {
			return nil
		}
		c, ok := lv.Init.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case ir.NodeOpCreateNode:
			if indexed[lv.Name] {
				t.slotNamed = append(t.slotNamed, lv.Name)
			}
			if t.present[lv.Name] {
				t.slotPresent = append(t.slotPresent, lv.Name)
			}
		case ir.NodeOpComponentRoot:
			inst := codegen.IdentBareName(c.Args[0].Value)
			if t.present[inst] {
				t.slotPresent = append(t.slotPresent, presentField(inst))
			}
		}
		return nil
	})
	return t.withInstanceCopies(indexed, t.sinkField)
}

// withRecordPresent has this scope of a component built at run time hand the
// widgets of the ids a test null-tests to the Model (namedWidgetField), which
// a test reaches no record through; stmts is the scope's body, whose creates
// of them a slot's reset takes back.
func (t *gtk4Translator) withRecordPresent(present map[string]bool, stmts []ir.Stmt) *gtk4Translator {
	t.present = present
	t.slotPresent = nil
	_ = ir.Walk(stmts, func(n ir.Node) error {
		lv, ok := n.(*ir.LocalVar)
		if !ok {
			return nil
		}
		c, ok := lv.Init.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case ir.NodeOpCreateNode:
			if present[lv.Name] {
				t.slotPresent = append(t.slotPresent, lv.Name)
			}
		case ir.NodeOpComponentRoot:
			if inst := codegen.IdentBareName(c.Args[0].Value); present[inst] {
				t.slotPresent = append(t.slotPresent, presentField(inst))
			}
		}
		return nil
	})
	return t
}

// modelPresentRef is the Model field holding what a test null-tests by id,
// for a record's scope: the field a Model scope's own widget is.
func (t *gtk4Translator) modelPresentRef(field, cType string) ir.Expr {
	return codegen.RecvFieldRef(&ir.Ident{Name: t.gc.ModelRef()}, field, ir.NativeGoNamed(widgetFieldGoType(cType, t.wrapped)))
}

// releasePresent takes back what a record handed the Model for a test's null
// test of field -- a node's id, or an instance's presentField. A node is taken
// back only where the Model still holds this record's widget: another record
// may have handed its own since.
func (t *gtk4Translator) releasePresent(field string) ir.Stmt {
	if strings.HasPrefix(field, presentField("")) {
		return &ir.Assign{Target: t.modelPresentRef(field, "GtkWidget"), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}}
	}
	cType := t.idCTypes[field]
	return &ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: t.modelPresentRef(namedWidgetField(field), cType), Right: t.fieldRef(field)},
		Body: []ir.Stmt{&ir.Assign{Target: t.modelPresentRef(namedWidgetField(field), cType), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}}},
	}
}

// inRecord marks this scope as the body or a method of a component built at
// run time.
func (t *gtk4Translator) inRecord() *gtk4Translator {
	t.record = true
	return t
}

// withInstanceCopies has this scope list the instances of the ids a test
// reaches one copy of, declaring each map through modelSink.
func (t *gtk4Translator) withInstanceCopies(ids map[string]bool, modelSink func(name, cType string)) *gtk4Translator {
	t.instanceCopies, t.modelSink = ids, modelSink
	return t
}

func newGtk4Translator(gc *golang.GoIRContext, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{
		gc:           gc,
		fieldSink:    fieldSink,
		idCTypes:     map[string]string{},
		fieldIDs:     map[string]bool{},
		skipped:      map[string]struct{}{},
		tagComponent: map[string]*ir.Component{},
		nodeSource:   map[string]ast.Stmt{},
	}
}

func (t *gtk4Translator) withLocalRefs(local map[string]bool) *gtk4Translator {
	t.localRefs = local
	return t
}

func (t *gtk4Translator) withSlotRoot(name string, body []ir.Stmt) *gtk4Translator {
	t.rootRenders = codegen.RootSlotRenders(body, name)
	return t
}

// addTopsTo places the top-level widgets written so far into root, ahead of
// the slot render that follows: a slot's first render appends at the end of
// its box, and lowering finishes a node's subtree before anything later.
func (t *gtk4Translator) addTopsTo(ctx context.Context, root ir.Expr) []ir.Stmt {
	var stmts []ir.Stmt
	for _, ref := range slices.Clone(t.topLevel) {
		stmts = append(stmts, t.OnAppendChild(ctx, root, &ir.Ident{Name: ref, IsElementRef: true, Synthesized: true})...)
	}
	t.topLevel = nil
	return stmts
}

func (t *gtk4Translator) isLocalRef(id string) bool {
	if t.fieldIDs[id] {
		return false
	}
	return t.localRefs != nil && t.localRefs[id]
}

func (t *gtk4Translator) withInvokerSink(sink func(gtkEventInvoker)) *gtk4Translator {
	t.invokerSink = sink
	return t
}

func (t *gtk4Translator) withPkg(pkg *ir.Package) *gtk4Translator {
	t.pkg = pkg
	return t
}

func (t *gtk4Translator) withRegistry(reg *gir.TypeRegistry) *gtk4Translator {
	t.registry = reg
	return t
}

func (t *gtk4Translator) withShared(s *emitShared) *gtk4Translator {
	t.shared = s
	return t
}

func (t *gtk4Translator) withWrapped(on bool) *gtk4Translator {
	t.wrapped = on
	return t
}

// collectTagComponents records each CreateNode tag's resolved ir.Component, so
// OnCreateNode reaches the Native metadata without re-querying GIR.
func (t *gtk4Translator) collectTagComponents(stmts []ir.Stmt) {
	for _, s := range stmts {
		t.collectFromStmt(s)
	}
}

func (t *gtk4Translator) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.LocalVar:
		if n.Type != nil && n.Type.Kind == ir.TypeComponent {
			if comp, ok := n.Type.Decl.(*ir.Component); ok && comp != nil {
				if call, ok := n.Init.(*ir.Call); ok && len(call.Args) >= 1 {
					if lit, ok := call.Args[0].Value.(*ir.Literal); ok && lit.Type == ir.TypString {
						t.tagComponent[lit.Value] = comp
						t.nodeSource[n.Name] = n.NodeAST
					}
				}
			}
		}
	case *ir.For:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
		for _, c := range n.Else {
			t.collectFromStmt(c)
		}
	case *ir.If:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
		for _, c := range n.Else {
			t.collectFromStmt(c)
		}
	case *ir.Match:
		for _, arm := range n.Arms {
			for _, c := range arm.Body {
				t.collectFromStmt(c)
			}
		}
	case *ir.NodeInst:
		for _, c := range n.Children {
			t.collectFromStmt(c)
		}
	case *ir.SlotInst, *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt,
		*ir.Break, *ir.Continue:
	default:
		panic(fmt.Sprintf("gtk4.collectFromStmt: unhandled ir.Stmt %T", n))
	}
}

// widgetClass returns the GTK class behind a tag: the C type its declaration's
// #[intrinsic] names, and the GIR entry the emitter reads the class's C API
// from. Zero values for a tag that is not one of this platform's widgets.
func (t *gtk4Translator) widgetClass(tag string) (string, *gir.ClassInfo) {
	cType := widgetCType(t.tagComponent[tag])
	if cType == "" {
		return "", nil
	}
	info := t.classFor(cType)
	if info == nil {
		return "", nil
	}
	return cType, info
}

// unresolvedTagError explains why a node tag resolved to no GTK widget, or
// returns nil when emitting nothing for it is correct.
//
// Every case is read off the declaration, never matched against a list of
// names. A library component is abstract, rendering only through the gtk4.sngl
// override that gets inlined away, so one arriving here under its own name is
// one this platform never implemented. A user component with an empty body
// says it draws nothing and is left alone.
func (t *gtk4Translator) unresolvedTagError(id, tag string) error {
	comp := t.tagComponent[tag]
	if comp == nil {
		return fmt.Errorf("gtk4: no widget for node %q", tag)
	}
	if cType := widgetCType(comp); cType != "" {
		return fmt.Errorf("gtk4: widget %q names C type %s, which the installed Gtk-4.0.gir does not declare", tag, cType)
	}
	if !codegen.DeclinesNode(comp) {
		// Lowering drops such a node, so this is unreachable through a
		// compile; answering here beats telling a user their own component
		// has no gtk4 implementation.
		return nil
	}
	// PlatformOverrides survives passPlatformExtensionBody, so it is still the
	// record of which platforms declared an override: a gtk4 entry here means
	// the override existed but did not reach a widget.
	if _, ok := comp.PlatformOverrides[platformName]; ok {
		return fmt.Errorf("gtk4: component %q has a gtk4 implementation that did not lower to a widget", tag)
	}
	return codegen.UnimplementedNode(comp, t.nodeSource[id], tag, platformName)
}

func (t *gtk4Translator) classFor(cType string) *gir.ClassInfo {
	if t.registry == nil {
		return nil
	}
	return t.registry.ByCType[cType]
}

// fieldRef is a `<recv>.<name>` selector against the struct this emission's
// scope dispatches through -- the Model in a Model method, the instance record
// inside a component's ctor. Every node field a translator writes goes through
// here rather than codegen.ModelFieldRef, which names the Model and only the
// Model.
func (t *gtk4Translator) fieldRef(name string) ir.Expr {
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(name), name, t.fieldType(name))
}

// sinkField declares a field of the C type cType, and remembers it as the
// type a reference to the field carries.
func (t *gtk4Translator) sinkField(name, cType string) {
	if t.fieldCTypes == nil {
		t.fieldCTypes = map[string]string{}
	}
	t.fieldCTypes[name] = cType
	t.fieldSink(name, cType)
}

// fieldType is the host type of the field name: what it was declared as, or
// for a node another scope created, its widget's C type.
func (t *gtk4Translator) fieldType(name string) *ir.Type {
	if ft, ok := t.declaredType(name); ok {
		return ft
	}
	panic(fmt.Sprintf("gtk4: the field %s was declared by no translator", name))
}

func (t *gtk4Translator) declaredType(name string) (*ir.Type, bool) {
	cType, ok := t.fieldCTypes[name]
	if !ok {
		cType, ok = t.idCTypes[name]
	}
	if !ok {
		return nil, false
	}
	return ir.NativeGoNamed(widgetFieldGoType(cType, t.wrapped)), true
}

// nodeFieldRef is fieldRef for a reference the lowered IR made, which
// another scope may have declared: typed as the field was declared where this
// translator knows it, and as the reference is otherwise.
func (t *gtk4Translator) nodeFieldRef(id *ir.Ident) ir.Expr {
	ft, ok := t.declaredType(id.Name)
	if !ok {
		ft = id.Type
	}
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(id.Name), id.Name, ft)
}

var _ codegen.IntrinsicTranslator = (*gtk4Translator)(nil)

// nativeFunc constructs an *ir.Func that renders as `C.<identifier>`. Callers
// pass a bare name; the renderer adds the "C." prefix.
func nativeFunc(nativeName string) *ir.Func {
	return &ir.Func{Foreign: ir.Foreign{Path: "C", Name: nativeName}, Name: nativeName}
}

// nativeCall builds a single-level cgo C-API call.
func nativeCall(nativeName string, ret *ir.Type, args ...ir.Expr) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     ret,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc(nativeName),
		Args:     callArgs,
	}
}

// cgoCast wraps an expression in `(*C.<typeName>)(unsafe.Pointer(expr))`, as
// an ir.Conversion to a NativePointer type that evalConversion recognizes.
func cgoCast(typeName string, expr ir.Expr) ir.Expr {
	return &ir.Conversion{
		Type:    ir.NativePointerOf(typeName),
		Operand: expr,
	}
}

// cScalar is the cgo conversion `C.<cType>(v)`, a value of that C type.
func cScalar(cType string, v ir.Expr) *ir.Call {
	return nativeCall(cType, ir.NativeGoNamed("C."+cType), v)
}

// cString is `C.CString(v)`, the C copy of a Go string.
func cString(v ir.Expr) *ir.Call {
	return nativeCall("CString", ir.NativePointerOf("char"), v)
}

// boolToGoInt exists because cgo cannot convert a bool to a named C integer
// typedef: `C.gboolean(boolVar)` does not compile, so the bool becomes an int
// first. boolToInt is emitted into model.go only when this is called.
func (t *gtk4Translator) boolToGoInt(expr ir.Expr) ir.Expr {
	t.shared.needBoolToInt()
	return &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "boolToInt"},
		Args: []ir.CallArg{{Value: expr}},
	}
}

func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	// A `canvas` has no GIR-native widget, so it is intercepted before the
	// native-tag lookup and built as a GtkDrawingArea with a cairo callback.
	if tag == "canvas" {
		if t.canvasMetaForID(id) != nil {
			return t.emitCanvasCreate(id)
		}
	}
	// A flow is a GtkLabel and a span is a piece of its markup, so neither
	// names a GIR class and both are intercepted before the lookup. The span
	// emits nothing at all: it is recorded as skipped so the AppendChild
	// naming it is dropped too, and its words reach the label through
	// emitFlowMarkup.
	switch tag {
	case toplevelTag:
		return t.emitToplevelCreate(id)
	case imageTag, imageURLTag, imageDrawingTag:
		return t.emitImageCreate(id, tag)
	case scrollTag:
		return t.emitScrollCreate(id)
	case flowTag:
		return t.emitFlowCreate(id)
	case spanTag:
		t.skipped[id] = struct{}{}
		if t.builtSpans == nil {
			t.builtSpans = map[string]bool{}
		}
		t.builtSpans[id] = true
		return nil
	}
	// passInlinePure already substituted the stdlib wrappers with their
	// gtk4.sngl bodies, so every tag here is a GIR-resolved widget name.
	cType, info := t.widgetClass(tag)
	if info == nil {
		// The build has to fail: dropping the node emits a window missing the
		// widgets its source asked for and says so nowhere.
		if err := t.unresolvedTagError(id, tag); err != nil {
			t.shared.fail(err)
		}
		// Recorded so later references are dropped rather than emitting
		// `m.<id>` for a field never declared on Model.
		t.skipped[id] = struct{}{}
		return nil
	}
	// A window is a toplevel, which GTK will not put inside a container, and
	// the platform makes one where a program writes ui.window: its Toplevel
	// is created with the application and attached to it.
	if t.isWindowClass(info) {
		t.shared.fail(fmt.Errorf("gtk4: %s is a window, which GTK places in no container; write ui.window, which this platform makes a GtkApplicationWindow of", cType))
		t.skipped[id] = struct{}{}
		return nil
	}
	if cType == "GtkDropDown" {
		t.shared.dropDown = true
	}
	ctorInfo := pickPrimaryConstructorInfo(info)
	if t.wrapped {
		if ctor, ok := rtCtorForCType(cType); ok {
			return t.emitConstructorAssign(id, cType, ctor)
		}
	}
	// GIR describes no constructor for this class, and the name derived from
	// the class is not one GTK ships, so emitting the call would leave a link
	// error in the user's build instead of a diagnostic here.
	if len(info.Constructors) == 0 {
		t.shared.fail(fmt.Errorf("gtk4: %s cannot be constructed: GTK declares no constructor for it", cType))
		return nil
	}
	// A typed zero per required parameter, so the cgo call type-checks;
	// OnPropAssign immediately rewrites any user-supplied value.
	args := []ir.Expr{}
	for _, p := range ctorInfo.Params {
		arg, err := ctorZeroArg(t.registry, p)
		if err != nil {
			t.shared.fail(fmt.Errorf("gtk4: %s cannot be constructed: %s takes %s %s, %w", cType, ctorInfo.Name, p.GIRType, p.Name, err))
			return nil
		}
		args = append(args, arg)
	}
	ctor := nativeCall(ctorInfo.Name, ir.NativePointerOf("GtkWidget"), args...)
	return t.shared.ownCStrings(t.emitConstructorAssign(id, cType, ctor))
}

// ctorZeroArg returns the IR expression for a typed-zero value matching one
// constructor parameter, suitable as a placeholder a later prop assignment
// overwrites. Primitives become typed zero casts, a Gtk enumeration a bare 0
// (cgo coerces an untyped int constant into the named type), and a pointer
// nil — which the parameter's c:type is the only thing in GIR that identifies,
// since a GIR name alone does not say whether GApplicationFlags is an integer
// or GFile is a pointer.
//
// It errors for a parameter whose zero cannot be spelled: a varargs or array
// form with no c:type at all, and a named type from a namespace other than
// Gtk, which the platform does not parse and so cannot tell a foreign enum
// (a bare 0) from a foreign callback (nil). Refusing here is a build
// diagnostic naming the class; guessing emits Go that does not compile.
func ctorZeroArg(reg *gir.TypeRegistry, p gir.ConstructorParam) (ir.Expr, error) {
	switch p.GIRType {
	case "utf8", "filename", "gchararray":
		// gtk_link_button_new refuses NULL and returns no widget at all.
		if !p.Nullable {
			return cString(&ir.Literal{Type: ir.TypString, Value: ""}), nil
		}
		return &ir.Literal{Type: ir.TypNull}, nil
	case "gboolean", "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return cScalar(ctorScalarCast(p), &ir.Literal{Type: ir.TypInt, Value: "0"}), nil
	case "gdouble", "gfloat":
		return cScalar(ctorScalarCast(p), &ir.Literal{Type: ir.TypFloat, Value: "0"}), nil
	}
	if reg != nil && reg.Enums[p.GIRType] != nil {
		return &ir.Literal{Type: ir.TypInt, Value: "0"}, nil
	}
	// gpointer is void*, which cgo renders as unsafe.Pointer — nil, not 0,
	// even though the c:type carries no star.
	if strings.HasSuffix(p.CType, "*") || p.CType == "gpointer" {
		return &ir.Literal{Type: ir.TypNull}, nil
	}
	if p.CType == "" {
		return nil, fmt.Errorf("which GIR describes with no C type of its own")
	}
	return nil, fmt.Errorf("whose C type %s is from a namespace this platform does not parse, so its zero value cannot be spelled", p.CType)
}

// ctorScalarCast is the cgo type a numeric zero takes to satisfy one
// constructor parameter. It is the parameter's own c:type, not the GIR type's
// canonical cgo spelling — GTK constructors take guint and float parameters of
// gint- and gdouble-named GIR types, and cgo does not convert between named C
// numeric types on its own. A c:type that is not a plain cgo identifier
// ("unsigned int") falls back to the glib typedef, which cgo does accept.
// Mirrors girScalarCast, which does the same for a property's setter.
func ctorScalarCast(p gir.ConstructorParam) string {
	if c := p.CType; c != "" && !strings.ContainsAny(c, "* ") {
		return c
	}
	return p.GIRType
}

// OnCreateComponent binds a name to a live instance of a non-inlinable user
// component.
//
// The id names the INSTANCE, so it is not a top-level widget candidate --
// ComponentRoot is what yields something a container can hold.
func (t *gtk4Translator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	instType := t.instanceGoType(call)
	if instType == "" {
		t.idCTypes[id] = "GtkWidget"
		if t.plainHandle == nil {
			t.plainHandle = map[string]bool{}
		}
		t.plainHandle[id] = true
	}
	if t.isLocalRef(id) {
		// A function-local, so each recursion frame and each row of a list
		// keeps its own instance rather than clobbering a shared Model field.
		lv := &ir.LocalVar{Name: id, Init: call}
		if instType == "" {
			lv.Type = ir.NativePointerOf("GtkWidget")
		}
		return []ir.Stmt{lv}
	}
	if instType != "" {
		t.sinkField(id, instType)
	} else {
		t.sinkField(id, "GtkWidget")
	}
	return []ir.Stmt{&ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  call,
	}}
}

// instanceGoType is the Go type a held instance handle has when this host
// builds a component as a record, and "" when the render still yields a plain
// widget.
func (t *gtk4Translator) instanceGoType(call *ir.Call) string {
	if !t.gc.InstanceRecords {
		return ""
	}
	comp := golang.CreateComponentTarget(call)
	if comp == nil || !comp.RuntimeInstance {
		return ""
	}
	return "*" + golang.ComponentInstanceType(comp.Name)
}

// OnDetachHandler reports that gtk4 cannot yet take a signal back off.
//
// GLib can -- g_signal_handler_disconnect -- but it wants the handler id that
// g_signal_connect returned, and OnAttachHandler does not keep one. Nothing
// emits this op for gtk4 today, because the op only appears where a node is
// retained across a render and gtk4 does not retain one. Reporting rather than
// returning nothing, so the day it does the build says what is missing instead
// of stacking a second handler on every row.
func (t *gtk4Translator) OnDetachHandler(ctx context.Context, node ir.Expr, event string, _ ir.Expr) []ir.Stmt {
	t.shared.fail(fmt.Errorf("gtk4: cannot detach the %q signal: the connect does not keep the handler id g_signal_handler_disconnect needs", event))
	return nil
}

// OnComponentRoot binds a name to the widget an instance renders as. A local,
// so a recursion frame keeps its own rather than clobbering a shared Model
// field -- the same reason OnCreateComponent takes the local-ref path.
func (t *gtk4Translator) OnComponentRoot(ctx context.Context, id string, inst, key ir.Expr) []ir.Stmt {
	t.idCTypes[id] = "GtkWidget"
	t.topLevel = append(t.topLevel, id)
	var root ir.Expr = t.qualifyNodeExpr(inst)
	if !t.plainHandle[codegen.IdentBareName(inst)] {
		// Only a record has a Root field. Where the build renders the
		// component as a method of the enclosing scope instead, the handle IS
		// the widget it returned -- `__n0.Root` on a gtk4rt.Handle names no
		// field the emitted file has.
		root = &ir.Select{Type: ir.NativeGoNamed(widgetFieldGoType("GtkWidget", t.wrapped)), Operand: root, Field: golang.ComponentRootField}
	}
	// An instance a test null-tests hands its root to the Model while it is
	// rendered, as a widget is its own field.
	var present []ir.Stmt
	if name := codegen.IdentBareName(inst); t.present[name] {
		field := presentField(name)
		var target ir.Expr
		if t.record {
			t.modelSink(field, "GtkWidget")
			target = t.modelPresentRef(field, "GtkWidget")
		} else {
			t.sinkField(field, "GtkWidget")
			target = t.fieldRef(field)
		}
		present = append(present, &ir.Assign{Target: target, Op: ast.AssignSet, Value: root})
	}
	// One copy of it is found by the key it stands in now, which a reused
	// instance was handed before its root is bound again.
	if name := codegen.IdentBareName(inst); key != nil && t.instanceCopies[name] {
		t.modelSink(copiesField(name), copyMapType("GtkWidget"))
		present = append(present, golang.CopyCall(&ir.Ident{Name: t.gc.ModelRef()}, name, key, root))
	}
	if hold := t.holdInstance(inst, key); hold != nil {
		present = append(present, hold)
	}
	if t.isLocalRef(id) {
		return append([]ir.Stmt{&ir.LocalVar{
			Name: id,
			Type: ir.NativePointerOf("GtkWidget"),
			Init: root,
		}}, present...)
	}
	t.sinkField(id, "GtkWidget")
	return append([]ir.Stmt{&ir.Assign{Target: t.fieldRef(id), Op: ast.AssignSet, Value: root}}, present...)
}

// holdInstance is the Model's hold on the record inst names, where a test
// reads a member through it (golang.HoldInstance), or nil.
func (t *gtk4Translator) holdInstance(inst, key ir.Expr) ir.Stmt {
	name := codegen.IdentBareName(inst)
	r, ok := t.gc.InstanceRead(name)
	if !ok || t.plainHandle[name] {
		return nil
	}
	if !r.Keyed {
		key = nil
	} else if key == nil {
		return nil
	}
	return golang.HoldInstance(&ir.Ident{Name: t.gc.ModelRef()}, name, r.Comp, t.qualifyNodeExpr(inst), key)
}

// OnUpdateComponent patches a prop on a live instance through the setter the
// instance carries for it.
func (t *gtk4Translator) OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: t.qualifyNodeExpr(inst),
		Func:     &ir.Func{Name: golang.ComponentSetterMethod(prop)},
		Args:     []ir.CallArg{{Value: value}},
	}}}
}

// OnDestroyComponent ends the instance's lifetime. On a refcounted toolkit
// this is also where the reference the record holds is dropped, which is why
// detaching a node is not the same event: an unparented widget is routinely
// attached again.
func (t *gtk4Translator) OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: t.qualifyNodeExpr(inst),
		Func:     &ir.Func{Name: golang.ComponentDestroyMethod},
	}}}
}

func (t *gtk4Translator) emitConstructorAssign(id, cType string, ctor ir.Expr) []ir.Stmt {
	t.idCTypes[id] = cType
	t.topLevel = append(t.topLevel, id)
	// ctor already yields a gtk4rt.Handle, so no cgo cast; the fieldSink
	// closure gives the field its Handle type.
	initVal := ctor
	if !t.wrapped {
		initVal = cgoCast(cType, ctor)
	}
	if t.isLocalRef(id) {
		// A function-local, so each call frame gets its own widget temp —
		// required for recursive component render methods.
		return []ir.Stmt{&ir.LocalVar{
			Name: id,
			Type: ir.NativePointerOf(cType),
			Init: initVal,
		}}
	}
	t.sinkField(id, cType)
	create := []ir.Stmt{&ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  initVal,
	}}
	if t.indexed[id] {
		t.sinkField(copiesField(id), copyMapType(cType))
	}
	if t.record && t.present[id] {
		named := namedWidgetField(id)
		t.modelSink(named, cType)
		create = append(create, &ir.Assign{Target: t.modelPresentRef(named, cType), Op: ast.AssignSet, Value: t.fieldRef(id)})
	}
	return create
}

// childAdder is how a parent takes a child, read from the introspection data
// the widget declarations were generated from.
//
// GTK's container APIs are not uniform enough for every class to be derivable
// -- gtk_grid_attach takes four arguments and gtk_notebook_append_page takes a
// tab label -- so a class with no single-widget way in is reported rather than
// guessed at, since a dropped child is silently reparented to the root by
// emitBuildUI. What is derivable is the three shapes that take a widget and
// nothing else; see gir.ChildAdder.
// adderReceiver is the C type a child adder's receiver is cast to: the class
// that declares it, or the container's own where GIR named none.
func adderReceiver(adder gir.ChildAdder, cType string) string {
	if adder.Owner != "" {
		return adder.Owner
	}
	return cType
}

// isWindowClass reports whether a GIR class is GtkWindow or descends from it.
func (t *gtk4Translator) isWindowClass(info *gir.ClassInfo) bool {
	for c := info; c != nil; c = t.registry.Classes[c.Parent] {
		if c.CType == "GtkWindow" {
			return true
		}
	}
	return false
}

func (t *gtk4Translator) childAdder(cType string) gir.ChildAdder {
	if cls := t.classFor(cType); cls != nil {
		return cls.ChildAdd
	}
	return gir.ChildAdder{}
}

func gtk4ChildRemoveFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "gtk_box_remove"
	}
	return ""
}

func (t *gtk4Translator) parentCType(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		if id.Name == "container" || id.Name == "parent" {
			return "GtkBox"
		}
		return t.idCTypes[codegen.IdentBareName(e)]
	}
	return ""
}

// isSkipped reports whether the expression refers to a widget id OnCreateNode
// emitted nothing for; later refs to it must be dropped.
func (t *gtk4Translator) isSkipped(e ir.Expr) bool {
	if id, ok := e.(*ir.Ident); ok {
		if _, ok := t.skipped[id.Name]; ok {
			return true
		}
	}
	return false
}

func (t *gtk4Translator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if t.isSkipped(parent) || t.isSkipped(child) {
		return nil
	}
	if ir.IsAppParent(parent) {
		return t.appAttach(child)
	}
	if t.windowIcon(parent, child) {
		return nil
	}
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	adder := t.childAdder(cType)
	if adder.Func == "" {
		t.shared.fail(fmt.Errorf("gtk4: %s hosts children through no single-widget call this platform can emit; "+
			"its GTK API needs arguments a child append cannot supply", cType))
		return nil
	}
	fn := adder.Func
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" && t.slotAnchor != nil && cType == "GtkBox" {
		if t.wrapped {
			return []ir.Stmt{&ir.CallStmt{Call: rtCall("InsertBefore", ir.TypVoid, parent, t.slotAnchor, t.qualifyNodeExpr(child))}}
		}
		// Through gtk4rt on cgo too: a box's alignItems is its, and a child
		// inserted with GTK's own call sat where GTK put it.
		call := rtCall("InsertBefore", ir.TypVoid, t.rtHandle(parent), t.rtHandle(t.slotAnchor), t.rtHandle(t.qualifyNodeExpr(child)))
		return []ir.Stmt{&ir.CallStmt{Call: call}}
	}
	if t.wrapped {
		stmt, ok := rtChildAppendCall(adder, t.qualifyNodeExpr(parent), t.qualifyNodeExpr(child))
		if !ok {
			// A cgo call in a file built without cgo does not compile, so the
			// gap is reported where it is rather than emitted.
			t.shared.fail(fmt.Errorf("gtk4: %s adds its child through %s, which pkg/go/gtk4rt does not wrap; "+
				"a wrapped build cannot emit the cgo call directly", cType, adder.Func))
			return nil
		}
		return []ir.Stmt{stmt}
	}
	if fn == "gtk_box_append" {
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("BoxAppend", ir.TypVoid, t.rtHandle(t.qualifyNodeExpr(parent)), t.rtHandle(t.qualifyNodeExpr(child)))}}
	}
	parentArg := cgoCast(adderReceiver(adder, cType), t.qualifyNodeExpr(parent))
	childArg := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, ir.TypVoid, parentArg, childArg)}}
}

func (t *gtk4Translator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if t.isSkipped(parent) || t.isSkipped(child) {
		return nil
	}
	return append(t.unlistRemoved(child), t.removeChild(parent, child)...)
}

func (t *gtk4Translator) removeChild(parent, child ir.Expr) []ir.Stmt {
	if ir.IsAppParent(parent) {
		return t.appDetach(child)
	}
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	fn := gtk4ChildRemoveFn(cType)
	if fn == "" {
		return nil
	}
	if t.wrapped {
		if stmt, ok := rtChildRemoveCall(cType, t.qualifyNodeExpr(parent), t.qualifyNodeExpr(child)); ok {
			return []ir.Stmt{stmt}
		}
	}
	parentArg := cgoCast(cType, t.qualifyNodeExpr(parent))
	childArg := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, ir.TypVoid, parentArg, childArg)}}
}

// qualifyNodeExpr re-qualifies a synthesized widget ident as a Model field
// reference; a non-synth ident passes through.
func (t *gtk4Translator) qualifyNodeExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		// The __renderSlotN parent is a function parameter, but lowerNodeForSlot
		// marks it IsElementRef+Synthesized, which would rewrite it to a bogus
		// `m.Parent`.
		if id.Name == "parent" || id.Name == "container" {
			return e
		}
		if t.isLocalRef(id.Name) {
			return &ir.Ident{Name: id.Name, Type: id.Type}
		}
		// Any element ref is a Model field: the id a program wrote as `#inc`
		// exactly as much as the `__nN` lowering synthesized. Asking for
		// Synthesized as well, and separately for the `__n` prefix, named two
		// proxies for "is a node handle" and missed the one shape neither
		// covers -- a tagged widget then reached the setter as a bare `inc`,
		// which is not a binding this file has.
		if id.IsElementRef || strings.HasPrefix(id.Name, "__n") {
			return t.nodeFieldRef(id)
		}
	}
	return e
}

func (t *gtk4Translator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	// A span's props are read off the tree rather than assigned to a widget,
	// so an assignment to one is a reason to write its flow's label again --
	// which is the whole of reactivity here, the markup being an expression
	// over the same state the run was written against. Asked before the skip
	// check, which a span's own id is in.
	if bare := codegen.IdentBareName(node); t.markupTrees().ownerOf[bare] != "" {
		// The props a span is built with were already read off the tree and
		// written into the label emitConstructorAssign followed, so the
		// assignments the build emits next would each set the same string
		// again. A scope that did not create this span is a handler or an
		// updater, and there the assignment is the reason to write it.
		if t.builtSpans[bare] {
			return nil
		}
		return t.emitFlowMarkup(t.markupTrees().ownerOf[bare])
	}
	if t.isSkipped(node) {
		return nil
	}
	if prop == lookPropName {
		return t.setLook(node, value)
	}
	bare := codegen.IdentBareName(node)
	if prop == ir.CopyProp {
		return []ir.Stmt{t.listCopy(bare, node, value)}
	}
	if id, ok := codegen.InstanceRootID(prop); ok {
		return t.listRoot(id, node, value)
	}
	if t.isToplevel(bare) {
		return t.toplevelPropAssign(node, prop, value)
	}
	if tag := t.imageTagOf(bare); tag != "" {
		return t.imagePropAssign(node, tag, prop, value)
	}
	cType, ok := t.idCTypes[bare]
	if !ok {
		return nil
	}
	info := t.classFor(cType)
	p, hasProp := girProp(info, prop)

	// A construct-only property has no reachable write — the widget exists by
	// the time any prop is assigned, and GObject answers with a g_critical.
	// declgen withholds these, so reaching here means the prop arrived some
	// other way; refuse rather than emit a call that silently does nothing.
	if hasProp && p.ConstructOnly {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s is construct-only: GTK accepts it only when the widget is created, and nothing can set it afterwards", cType, p.Name))
		return nil
	}
	var entry gtkSetterEntry
	if hasProp {
		entry = girSetter(info, p)
	}
	// Before anything cgo-shaped is built: a coercion records that its helper
	// is needed, and here nothing would use it.
	if t.wrapped && entry.Setter != "" {
		if rt, ok := rtSetterTable[entry.Setter]; ok {
			if arg, ok := rtSetterValue(rt.kind, value); ok {
				return []ir.Stmt{&ir.CallStmt{Call: rtCall(rt.fn, ir.TypVoid, t.qualifyNodeExpr(node), arg)}}
			}
		}
	}
	recvType := cType
	if entry.RecvType != "" {
		recvType = entry.RecvType
	}
	if !hasProp {
		if t.canvasMetaForID(bare) != nil {
			// A canvas's own width and height, which emitCanvasCreate already
			// read off the drawing to size the GtkDrawingArea. They arrive
			// here as ordinary props because that is what they are in the
			// tree; there is nothing further to write.
			return nil
		}
		// A write that goes missing is a rendering bug with nothing said, as
		// the construct-only and unsettable cases are.
		t.shared.fail(fmt.Errorf("gtk4: %s has no property named %q: GIR describes none for the class, so the value reaches the widget not at all", cType, prop))
		return nil
	}
	kind := propKind(t.registry, p)
	if kind == propUnsettable {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s: no SNGL value can produce %s, which is what this GTK property takes", cType, p.Name, girTypeLabel(p)))
		return nil
	}
	if entry.Setter == "" {
		return t.emitGObjectPropSet(node, p, kind, value)
	}
	valArg, err := t.setterValueArg(kind, p, entry.ValType, value)
	if err != nil {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s: %w", cType, p.Name, err))
		return nil
	}
	return t.emitSetterCall(node, recvType, entry.Setter, valArg)
}

func (t *gtk4Translator) emitSetterCall(node ir.Expr, recvType, setter string, valArg ir.Expr) []ir.Stmt {
	cast := cgoCast(recvType, t.qualifyNodeExpr(node))
	return t.shared.ownCStrings([]ir.Stmt{&ir.CallStmt{Call: nativeCall(setter, ir.TypVoid, cast, valArg)}})
}

// gObjectSetFn is the preamble helper that carries each kind of value through
// GObject's generic property path. Every kind but propUnsettable has one, so
// a property GTK ships no setter for is still settable.
var gObjectSetFn = map[propValueKind]string{
	propString: "sngl_set_prop_string",
	propBool:   "sngl_set_prop_bool",
	propInt:    "sngl_set_prop_int",
	propFloat:  "sngl_set_prop_double",
	propEnum:   "sngl_set_prop_int",
}

// emitGObjectPropSet sets a property GTK ships no setter for by name, through
// g_object_set_property. The property's GIR name is the GObject property name;
// the helper transforms the primitive value into whatever GType the class's
// GParamSpec declares, which is how an enum-, float- or unsigned-typed
// property is reached from one of four helpers.
func (t *gtk4Translator) emitGObjectPropSet(node ir.Expr, p gir.Prop, kind propValueKind, value ir.Expr) []ir.Stmt {
	fn, ok := gObjectSetFn[kind]
	if !ok {
		return nil
	}
	valArg, err := t.gObjectValueArg(kind, p, value)
	if err != nil {
		t.shared.fail(fmt.Errorf("gtk4: %s: %w", p.Name, err))
		return nil
	}
	t.shared.needGObjectSet()
	obj := cgoCast("", t.qualifyNodeExpr(node))
	name := cString(&ir.Literal{Type: ir.TypString, Value: p.Name})
	return t.shared.ownCStrings([]ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, ir.TypVoid, obj, name, valArg)}})
}

func (t *gtk4Translator) setterValueArg(kind propValueKind, p gir.Prop, valType string, value ir.Expr) (ir.Expr, error) {
	switch kind {
	case propString:
		return cString(value), nil
	case propBool:
		return cScalar("gboolean", t.boolToGoInt(value)), nil
	case propInt, propFloat:
		return cScalar(girScalarCast(p), value), nil
	case propEnum:
		member, isMember, err := t.enumValueArg(p, value)
		if err != nil {
			return nil, err
		}
		if isMember {
			return member, nil
		}
		return cScalar(valType, member), nil
	}
	return nil, fmt.Errorf("no value of this type can be set")
}

func (t *gtk4Translator) gObjectValueArg(kind propValueKind, p gir.Prop, value ir.Expr) (ir.Expr, error) {
	switch kind {
	case propString:
		return cString(value), nil
	case propBool:
		return cScalar("int", t.boolToGoInt(value)), nil
	case propInt:
		return cScalar("int", value), nil
	case propFloat:
		return cScalar("double", value), nil
	case propEnum:
		member, _, err := t.enumValueArg(p, value)
		if err != nil {
			return nil, err
		}
		return cScalar("int", member), nil
	}
	return nil, fmt.Errorf("no value of this type can be set")
}

// enumValueArg resolves the value bound to an enum- or bitfield-typed
// property: a string literal names one of its members, and an int is the
// member's own number. isMember reports the former, whose cgo spelling is
// already typed and needs no cast.
func (t *gtk4Translator) enumValueArg(p gir.Prop, value ir.Expr) (arg ir.Expr, isMember bool, err error) {
	if lit, ok := value.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
		cst := girEnumMember(t.registry, p.GIRType, lit.Value)
		if cst == "" {
			return nil, false, fmt.Errorf("%q names no member of the %s enumeration", lit.Value, p.GIRType)
		}
		// A member of the GIR enumeration, as cgo spells its constant, of
		// the enumeration's C type.
		return &ir.Ident{Name: cst, Type: ir.NativeGoNamed("C." + t.registry.Enums[p.GIRType].CType)}, true, nil
	}
	if vt := exprIRType(value); vt != nil && vt.Kind == ir.TypeInt {
		return value, false, nil
	}
	return nil, false, fmt.Errorf("a %s value is a string literal naming one of its members, or an int", p.GIRType)
}

// girTypeLabel names the type a property takes, for an error a reader can act
// on. An array property carries no GIR type of its own — its inner <type>
// names the element.
func girTypeLabel(p gir.Prop) string {
	if p.GIRType == "" {
		return "an array"
	}
	return "a " + p.GIRType
}

// coerceSetterValue wraps the SNGL value expression so its Go-side
// representation matches the cgo type the C setter expects. This is the path
// for a prop the loaded GIR does not describe, where the SNGL value's own type
// is all there is to go on:
//   - Booleans → C.gboolean(v)
//   - Integers / Floats → C.int(v) / C.double(v)
//   - anything else → C.CString(v)
func (t *gtk4Translator) coerceSetterValue(setter string, value ir.Expr) ir.Expr {
	// A boolean setter whose property the GIR did not carry: the value may be
	// one exprIRType cannot type (a `!x`), and the fallback below would make a
	// string of it.
	if setter == "gtk_check_button_set_active" {
		return cScalar("gboolean", t.boolToGoInt(value))
	}
	if vt := exprIRType(value); vt != nil {
		switch vt.Kind {
		case ir.TypeBool:
			return cScalar("gboolean", t.boolToGoInt(value))
		case ir.TypeInt:
			return cScalar("int", value)
		case ir.TypeFloat:
			return cScalar("double", value)
		}
	}
	return cString(value)
}

func exprIRType(e ir.Expr) *ir.Type {
	switch n := e.(type) {
	case *ir.Literal:
		return n.Type
	case *ir.Ident:
		return n.Type
	case *ir.Select:
		return n.Type
	case *ir.Call:
		return n.Type
	case *ir.Binary:
		return n.Type
	case *ir.Conversion:
		return n.Type
	}
	return nil
}

func gtk4SignalFor(cType, event string) string {
	switch cType {
	case "GtkButton":
		if event == "click" {
			return "clicked"
		}
	case "GtkEntry":
		if event == "input" || event == "change" {
			return "changed"
		}
	case "GtkCheckButton":
		if event == "change" {
			return "toggled"
		}
	}
	return ""
}

// signalFor is the GTK signal an event written on this node connects to, or ""
// when no signal answers to it.
//
// Both callers need the same answer for different reasons: OnAttachHandler
// wires the trampoline, and emitIRPromotedHandler decides the handler's
// signature from it. A "" means nothing here connects the handler, so whatever
// calls it is not a GTK trampoline and its declared parameters stand.
func (t *gtk4Translator) signalFor(nodeID, event string) string {
	cType := t.idCTypes[nodeID]
	if s := gtk4SignalFor(cType, event); s != "" {
		return s
	}
	if s := notifySignal(cType, event); s != "" {
		return s
	}
	if sig, ok := girSignal(t.classFor(cType), event); ok && sig.Connectable() {
		return sig.Name
	}
	return ""
}

// signalLambda gives a handler written in place -- in an instance record,
// where nothing promotes it to a method -- the shape emitIRPromotedHandler
// gives a promoted one: the trampoline passes no arguments, so the signal's
// instance is the node it is attached to, and what the body reads of a widget
// is read off it.
func (t *gtk4Translator) signalLambda(lam *ir.Lambda, self ir.Expr) ir.Expr {
	if lam.Func == nil {
		return lam
	}
	fn := *lam.Func
	fn.Params = nil
	fn.Block = t.boxSlotRenders(t.spellLocalNodes(readWidgets(selfIsNode(lam.Func.Block, lam.Func.Params, self), t.widgetReader())))
	out := *lam
	out.Func = &fn
	return &out
}

// spellLocalNodes respells each node ref in stmts that this scope declares as a
// local, which the Go renderer otherwise writes as a field of the receiver: a
// handler in an instance's render slot handing its widget on wrote `c.__n1`
// for the slot's own `__n1`.
func (t *gtk4Translator) spellLocalNodes(stmts []ir.Stmt) []ir.Stmt {
	local := func(e ir.Expr) bool {
		id, ok := e.(*ir.Ident)
		return ok && id.IsElementRef && t.isLocalRef(id.Name)
	}
	found := false
	_ = ir.WalkExprs(stmts, func(e ir.Expr) error {
		if local(e) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	if !found {
		return stmts
	}
	stmts = ir.CloneStmtsSharingDecls(stmts)
	_ = ir.RewriteExprs(stmts, func(e ir.Expr) (ir.Expr, error) {
		if local(e) {
			return t.qualifyNodeExpr(e), nil
		}
		return e, nil
	})
	return stmts
}

// widgetReader is readWidgets' read for what this translator emits, which
// spells a node the way it spells every other.
func (t *gtk4Translator) widgetReader() widgetAccess {
	return widgetReader(t.wrapped, t.shared, func(name string) ir.Expr {
		return t.qualifyNodeExpr(&ir.Ident{Name: name, IsElementRef: true})
	})
}

func (t *gtk4Translator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	if t.isSkipped(node) {
		return nil
	}
	bare := codegen.IdentBareName(node)
	if t.isToplevel(bare) {
		return t.toplevelAttachHandler(node, event, handler)
	}
	if tag := t.imageTagOf(bare); tag != "" {
		return t.imageAttachHandler(node, tag, event, handler)
	}
	if t.isScroll(bare) {
		return t.scrollAttachHandler(node, event, handler)
	}
	cType := t.idCTypes[bare]
	if event == "click" && t.canvasMetaForID(bare) != nil {
		return t.attachCanvasClick(bare, event, handler)
	}
	signal := t.signalFor(bare, event)
	if signal == "" {
		// The trampoline is (instance, user_data) returning void; a signal of
		// any other shape hands its first argument to the dispatcher in place
		// of the callback index. declgen withholds these, so reaching here
		// means the event arrived some other way.
		if sig, ok := girSignal(t.classFor(cType), event); ok && !sig.Connectable() {
			t.shared.fail(fmt.Errorf("gtk4: %s.%s: the %q signal passes %d argument(s) and returns %s; only a void signal with none can be connected",
				cType, event, sig.Name, sig.Params, girSignalReturnLabel(sig)))
		}
		return nil
	}
	// Everything a test invoker needs is known here and nowhere later: the id a
	// program wrote, the SNGL event written on it, and the GTK signal that event
	// maps to. The statements emitted below keep only the signal, which is why
	// this is recorded rather than recovered.
	t.recordInvoker(bare, codegen.TriggerEventName(handler, event), signal, cType)
	if lam, ok := handler.(*ir.Lambda); ok {
		handler = t.signalLambda(lam, node)
	}

	// gtk4rt.Connect registers the handler and wires the signal in one call —
	// no per-program snglCallbacks slice or cgo.
	if t.wrapped {
		sigLit := &ir.Literal{Type: ir.TypString, Value: signal}
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("Connect", ir.TypVoid, t.qualifyNodeExpr(node), sigLit, handler)}}
	}
	// snglCallbacks = append(snglCallbacks, handler)
	cbList := snglCallbacksRef()
	appendCall := &ir.Call{
		Type: cbList.Type,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{
			{Value: cbList},
			{Value: handler},
		},
	}
	registerStmt := &ir.Assign{
		Target: cbList,
		Op:     ast.AssignSet,
		Value:  appendCall,
	}
	// sngl_connect's C signature is `void *widget`, so cgo expects
	// unsafe.Pointer.
	widget := cgoCast("", t.qualifyNodeExpr(node))
	signalCStr := cString(&ir.Literal{Type: ir.TypString, Value: signal})
	lenCall := &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "len"},
		Args: []ir.CallArg{{Value: cbList}},
	}
	idxExpr := &ir.Binary{
		Op:    ast.BinSub,
		Left:  lenCall,
		Right: &ir.Literal{Type: ir.TypInt, Value: "1"},
	}
	idxArg := cScalar("int", idxExpr)
	connectStmt := &ir.CallStmt{
		Call: nativeCall("sngl_connect", ir.TypVoid, widget, signalCStr, idxArg),
	}
	return t.shared.ownCStrings([]ir.Stmt{registerStmt, connectStmt})
}

func (t *gtk4Translator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	// A slot's fields are the widgets it holds and the anchor it inserts
	// them before, as the compiler declares them.
	widget := ir.NativeGoNamed(widgetFieldGoType("GtkWidget", t.wrapped))
	reset := &ir.Assign{
		Target: t.slotFieldRef(slot),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}
	t.slotAnchor = codegen.RecvFieldRef(t.gc.NodeRecvRef(slot.Name), codegen.SlotAnchorField(slot.Name), widget)
	var anchor ir.Expr
	if t.wrapped {
		anchor = rtCall("SlotAnchor", gtk4rtHandleIRType(), &ir.Ident{Name: "parent"}, t.slotAnchor)
	} else {
		t.shared.needSlotAnchor()
		anchor = nativeCall("sngl_slot_anchor", ir.NativePointerOf("GtkWidget"), cgoCast("GtkBox", &ir.Ident{Name: "parent"}), t.slotAnchor)
	}
	stmts := []ir.Stmt{reset, &ir.Assign{Target: t.slotAnchor, Op: ast.AssignSet, Value: anchor}}
	// The render builds every copy again, so the map of them starts over with
	// it.
	for _, id := range t.slotNamed {
		list := copiesField(id)
		stmts = append(stmts, &ir.Assign{Target: codegen.RecvFieldRef(t.gc.NodeRecvRef(list), list, t.copiesType(id)), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}})
	}
	for _, id := range t.slotPresent {
		if t.isLocalRef(id) {
			continue
		}
		if t.record {
			stmts = append(stmts, t.releasePresent(id))
			continue
		}
		stmts = append(stmts, &ir.Assign{Target: codegen.RecvFieldRef(t.gc.NodeRecvRef(id), id, ir.NativeGoNamed(widgetFieldGoType("GtkWidget", t.wrapped))), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}})
	}
	return stmts
}

func (t *gtk4Translator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := t.slotFieldRef(slot)
	childArg := ir.Expr(cgoCast("GtkWidget", t.qualifyNodeExpr(child)))
	if t.wrapped {
		childArg = t.qualifyNodeExpr(child)
	}
	appendCall := &ir.Call{
		Type: slot.Type,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{
			{Value: slotRef},
			{Value: childArg},
		},
	}
	return []ir.Stmt{&ir.Assign{
		Target: t.slotFieldRef(slot),
		Op:     ast.AssignSet,
		Value:  appendCall,
	}}
}

// OnIter reads a slot's list, a field the scope declares, through the
// receiver. Any other name a pass made is a local of the code it is in: the
// accumulator a `map` or `filter` builds, which a second one ranges over.
func (t *gtk4Translator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized && (id.IsElementRef || ir.IsSlotVarName(id.Name)) {
		return t.nodeFieldRef(id)
	}
	return iter
}

func (t *gtk4Translator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *gtk4Translator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	if t.rootRenders[stmt] {
		t.renderedRoot = stmt.(*ir.CallStmt).Call.Args[0].Value
		return append(t.addTopsTo(ctx, t.renderedRoot), t.translateDefault(stmt)...)
	}
	return t.translateDefault(stmt)
}

func (t *gtk4Translator) translateDefault(stmt ir.Stmt) []ir.Stmt {
	switch n := stmt.(type) {
	case *ir.CanvasRedrawStmt:
		return t.translateCanvasRedraw(n)
	case *ir.CallStmt:
		if app, ok := t.appSlotCall(n); ok {
			return app
		}
		if boxed, ok := t.boxedSlotRenderCall(n); ok {
			return boxed
		}
		if local := localSlotRenderCall(n, t.isLocalRef); local != nil {
			return []ir.Stmt{local}
		}
	}
	return []ir.Stmt{stmt}
}

// boxedSlotRenderCall renders a slot whose container holds one child -- a
// scrolled window -- into a box that is that child, since a slot renders into
// a GtkBox and adds and removes any number of entries. A container holding
// several children through a call of its own is refused rather than handed to
// gtk_box_append.
func (t *gtk4Translator) boxedSlotRenderCall(cs *ir.CallStmt) ([]ir.Stmt, bool) {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 {
		return nil, false
	}
	bare := codegen.IdentBareName(cs.Call.Args[0].Value)
	cType := t.slotCType(bare)
	if cType == "" || cType == "GtkBox" {
		return nil, false
	}
	adder := t.childAdder(cType)
	if !strings.HasSuffix(adder.Func, "_set_child") {
		t.shared.fail(fmt.Errorf("gtk4: a reactive `if` or `for` directly inside a %s cannot be rendered: its slot renders into a GtkBox; wrap it in a vbox", cType))
		return nil, false
	}
	boxName := bare + "_box"
	t.sinkField(boxName, "GtkBox")
	box := t.fieldRef(boxName)
	parent := t.qualifyNodeExpr(&ir.Ident{Name: bare, IsElementRef: true})
	var slotBox, parentOf, parentArg ir.Expr
	var set ir.Stmt
	if t.wrapped {
		var ok bool
		if set, ok = rtChildAppendCall(adder, parent, box); !ok {
			return nil, false
		}
		slotBox, parentOf, parentArg = rtCall("SlotBox", gtk4rtHandleIRType(), box), rtCall("ParentOf", gtk4rtHandleIRType(), box), parent
	} else {
		t.shared.needSlotAnchor()
		set = &ir.CallStmt{Call: nativeCall(adder.Func, ir.TypVoid, cgoCast(adderReceiver(adder, cType), parent), cgoCast("GtkWidget", box))}
		// A scroll is gtk4rt's, which holds its content in a probe: the box's
		// parent is never the scroll, and GTK refused setting its child again
		// on every render.
		if adder.Func == "gtk_scrolled_window_set_child" {
			set = &ir.CallStmt{Call: rtCall("ScrolledWindowSetChild", ir.TypVoid, t.rtHandle(parent), t.rtHandle(box))}
		}
		slotBox = nativeCall("sngl_slot_box", ir.NativePointerOf("GtkWidget"), box)
		parentOf = nativeCall("gtk_widget_get_parent", ir.NativePointerOf("GtkWidget"), cgoCast("GtkWidget", box))
		parentArg = cgoCast("GtkWidget", parent)
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Value: box}}
	return []ir.Stmt{
		&ir.Assign{Target: box, Op: ast.AssignSet, Value: slotBox},
		&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: parentOf, Right: parentArg},
			Body: []ir.Stmt{set},
		},
		&ir.CallStmt{Call: &call},
	}, true
}

// boxSlotRenders gives each render slot call in a handler's body the box its
// container holds where that container holds one child, as translateDefault
// does for one the build makes: a handler re-rendering a slot inside a card's
// frame handed the frame itself to a function taking a GtkBox. The statements
// are copied first, being the lowered IR's.
func (t *gtk4Translator) boxSlotRenders(stmts []ir.Stmt) []ir.Stmt {
	boxed := func(s ir.Stmt) bool {
		cs, ok := s.(*ir.CallStmt)
		if !ok || cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 {
			return false
		}
		cType := t.slotCType(codegen.IdentBareName(cs.Call.Args[0].Value))
		return cType != "" && cType != "GtkBox"
	}
	found := false
	_ = ir.Walk(stmts, func(n ir.Node) error {
		if s, ok := n.(ir.Stmt); ok && boxed(s) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	if !found {
		return stmts
	}
	return ir.RewriteBlocks(ir.CloneStmtsSharingDecls(stmts), func(list []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range list {
			if cs, ok := s.(*ir.CallStmt); ok && boxed(s) {
				if b, ok := t.boxedSlotRenderCall(cs); ok {
					out = append(out, b...)
					continue
				}
			}
			out = append(out, s)
		}
		return out
	})
}

// slotCType is the C type of the container a slot renders into. A handler can
// be attached before the node it re-renders into is built -- a card below the
// loop whose click handler writes what the card shows -- so a container this
// walk has not reached yet is looked up in the whole package.
func (t *gtk4Translator) slotCType(bare string) string {
	if c := t.idCTypes[bare]; c != "" {
		return c
	}
	if t.pkgCTypes == nil {
		t.pkgCTypes = collectNodeCTypes(t.pkg)
	}
	return t.pkgCTypes[bare]
}

// localSlotRenderCall rewrites a render slot call whose container is a local
// of this scope -- a slot nothing but the ctor renders -- to pass the local,
// which the language context would otherwise spell as a field.
func localSlotRenderCall(cs *ir.CallStmt, isLocal func(string) bool) *ir.CallStmt {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	id, ok := cs.Call.Args[0].Value.(*ir.Ident)
	if !ok || !isLocal(id.Name) {
		return nil
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: &ir.Ident{Name: id.Name, Type: id.Type}}}
	return &ir.CallStmt{Call: &call}
}

// recordInvoker notes one (id, event) pair for the test-invoker methods emitted
// after the walk.
//
// A synthesized id is skipped: `c.__n0.click()` is not something a test can
// write, so a method for it would be dead. A scope with no sink -- a slot func,
// a canvas draw -- records nothing.
func (t *gtk4Translator) recordInvoker(id, event, signal, cType string) {
	if t.invokerSink == nil || id == "" || strings.HasPrefix(id, "__n") {
		return
	}
	inv := gtkEventInvoker{
		IDLabel:    id,
		SnglEvent:  event,
		FieldName:  id,
		GTKSignal:  signal,
		WidgetType: cType,
		State:      invokerStateSetter(cType, t.wrapped) != "",
	}
	inv.List = t.indexed[id]
	t.invokerSink(inv)
}

// invokerStateSetter is the call a test invoker writes a test's state into
// a widget with, "" for a widget it has none for. Setting the state is what
// fires the signal, as a user's flip does.
func invokerStateSetter(cType string, wrapped bool) string {
	switch {
	case cType == "GtkCheckButton" && wrapped:
		return "gtk4rt.CheckButtonSetActive"
	case cType == "GtkCheckButton":
		return "C.gtk_check_button_set_active"
	case cType == "GtkSwitch" && !wrapped:
		return "C.gtk_switch_set_active"
	}
	return ""
}

// slotFieldRef is the field a render slot's var is held in.
func (t *gtk4Translator) slotFieldRef(slot *ir.Var) ir.Expr {
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(slot.Name), slot.Name, slot.Type)
}

// copiesType is the type of copiesField(id).
func (t *gtk4Translator) copiesType(id string) *ir.Type {
	if ft, ok := t.declaredType(copiesField(id)); ok {
		return ft
	}
	if cType, ok := t.idCTypes[id]; ok {
		return ir.NativeGoNamed(widgetFieldGoType(copyMapType(cType), t.wrapped))
	}
	return ir.NativeGoNamed(widgetFieldGoType(copyMapType("GtkWidget"), t.wrapped))
}

// setLook gives a widget what style.sngl's lookOf built from its Style.
func (t *gtk4Translator) setLook(node, value ir.Expr) []ir.Stmt {
	if codegen.IRIsZeroLiteral(value) {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("SetLook", ir.TypVoid, t.rtHandle(t.qualifyNodeExpr(node)), codegen.IRWrittenFields(value))}}
}
