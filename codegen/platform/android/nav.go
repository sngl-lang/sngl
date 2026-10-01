package android

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/ir"
)

// sngl:ui/nav on android, which declares `navigation` and so is handed the
// three nodes and the go, back and current calls as passNavigationValues left
// them: a page named by its record, the stack by its handle.
//
// A stack is a NavHost over a NavController of its owner composable's, and
// each page a destination with a typed route of its own: a @Serializable
// class, holding the params where the page has any, whose default is the
// params the page's call site wrote. It is a class even with nothing to hold,
// so a navigation is `Route()` whatever the page, and a `go` -- which names
// its page by the record alone -- need not know which kind it names. The route is the history entry, so the params travel with it and
// `back` restores them; and Compose composes only the destination showing,
// so a page not showing is not mounted.
//
//	val pages__nav = rememberNavController()
//	val pages__entry by pages__nav.currentBackStackEntryAsState()
//	val pages__current = _page__valueOf(pages__entry) ?: _page__value(id = 1, …)
//	NavHost(navController = pages__nav, startDestination = _page__valueRoute1()) {
//	    composable<_page__valueRoute2> { __entry ->
//	        val p = __entry.toRoute<_page__valueRoute2>().params
//	        …
//	    }
//	}
//
// `current` is read off the back stack, mapped to the page family's record by
// one function per record type, which is what the record's id and a route
// class's name share. Routes are named by the record and the id rather than
// by the stack's and the page's ids, because a `go` names its page by the
// record alone.

func init() {
	codegen.RegisterPlatformIntrinsic("android", "nav.go", emitNavGo)
	codegen.RegisterPlatformIntrinsic("android", "nav.back", emitNavBack)
	codegen.RegisterPlatformIntrinsic("android", "nav.current", emitNavCurrent)
}

// androidNav is what the stacks a program renders need declared.
type androidNav struct {
	stacks []*navStackK
	// serializable is every struct a route reaches: a page's params, and what
	// those reach.
	serializable map[*ir.StructDef]bool
	// records is each record type a stack's pages are values of, in the order
	// first met, and the pages of each by id.
	records []*ir.StructDef
	pages   map[*ir.StructDef][]*navPageK
}

type navStackK struct {
	node  *ir.NodeInst
	name  string // the owner composable's local names are prefixed with it
	pages []*navPageK
	start *navPageK
}

type navPageK struct {
	node   *ir.NodeInst
	record *ir.StructLit
	id     int
	params *ir.Type // nil for the empty struct
}

func (pg *navPageK) route() string {
	return routeName(pg.record.Def, pg.id)
}

func routeName(def *ir.StructDef, id int) string {
	return exportName(def.Name) + "Route" + strconv.Itoa(id)
}

func recordFunc(def *ir.StructDef) string {
	return exportName(def.Name) + "Of"
}

// navName is the prefix a stack's locals carry in its owner composable: its
// handle's name, which is what a call naming the stack reads.
func navName(n *ir.NodeInst, seq int) string {
	if n.Handle != nil && n.Handle.Name != "" {
		return n.Handle.Name
	}
	if n.ID != "" {
		return n.ID
	}
	return "__stack" + strconv.Itoa(seq)
}

// collectAndroidNav finds the stacks, and refuses what a route cannot carry:
// params of a type with no serializer, and a record or a start that is not a
// constant, since both are written in a declaration rather than where the
// page is.
func collectAndroidNav(ctx *codegen.CodegenCtx) (*androidNav, error) {
	nav := &androidNav{serializable: map[*ir.StructDef]bool{}, pages: map[*ir.StructDef][]*navPageK{}}
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	_ = ir.Walk(ctx.Pkg, func(n ir.Node) error {
		ni, ok := n.(*ir.NodeInst)
		if !ok || ni.Component == nil || ni.Component.Builtin != ir.BuiltinNavStack {
			return nil
		}
		st := &navStackK{node: ni, name: navName(ni, len(nav.stacks))}
		for _, s := range ni.Children {
			p, ok := s.(*ir.NodeInst)
			if !ok || p.Component == nil || p.Component.Builtin != ir.BuiltinNavPage {
				continue
			}
			lit, ok := p.Record.(*ir.StructLit)
			if !ok || lit.Def == nil {
				continue
			}
			pg := &navPageK{node: p, record: lit, id: recordID(lit)}
			if e := p.Prop("params"); e != nil {
				if t := e.ExprType(); t != nil && !emptyStruct(t) {
					pg.params = t
				}
			}
			if err := checkConstRecord(p, lit); err != nil {
				fail(err)
			}
			if pg.params != nil {
				if bad, where := unserializable(pg.params, nav.serializable, map[*ir.StructDef]bool{}); bad != nil {
					fail(fmt.Errorf("%s: android carries a page's params in its route, and %s is a %s, which has no serializer: give the page params of strings, numbers, bools, enums and structs, lists, maps and options of them", nodeAt(p), where, bad))
				}
				if !ir.IsConst(p.Prop("params")) {
					fail(fmt.Errorf("%s: android starts a page's params in its route's declaration, so the params a page is written with are a constant", nodeAt(p)))
				}
			}
			st.pages = append(st.pages, pg)
			if _, seen := nav.pages[lit.Def]; !seen {
				nav.records = append(nav.records, lit.Def)
			}
			nav.pages[lit.Def] = append(nav.pages[lit.Def], pg)
		}
		if len(st.pages) > 0 {
			st.start = st.pages[0]
			for _, pg := range st.pages {
				if l, ok := pg.node.Prop("href").(*ir.Literal); ok && l.Value == "/" {
					st.start = pg
					break
				}
			}
		}
		nav.stacks = append(nav.stacks, st)
		return nil
	})
	for _, def := range nav.records {
		sort.SliceStable(nav.pages[def], func(i, j int) bool { return nav.pages[def][i].id < nav.pages[def][j].id })
	}
	return nav, firstErr
}

func nodeAt(n *ir.NodeInst) string {
	if p := ir.NodePos(n); p.IsValid() {
		return p.String()
	}
	return n.Name
}

func recordID(lit *ir.StructLit) int {
	for _, f := range lit.Fields {
		if f.Name == "id" {
			if l, ok := f.Value.(*ir.Literal); ok {
				n, _ := strconv.Atoi(l.Value)
				return n
			}
		}
	}
	return 0
}

func emptyStruct(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	f, ok := t.Decl.(ir.Fielded)
	return t.Decl == nil || ok && len(f.FieldList()) == 0
}

func checkConstRecord(p *ir.NodeInst, lit *ir.StructLit) error {
	for _, f := range lit.Fields {
		if !ir.IsConst(f.Value) {
			return fmt.Errorf("%s: android reads a page's %s off its route's declaration, so it is a constant", nodeAt(p), f.Name)
		}
	}
	return nil
}

// unserializable is the first type t reaches that kotlinx.serialization has no
// serializer for, and the field that reaches it; nil when there is none. Each
// struct reached is recorded in ok, to be declared @Serializable.
func unserializable(t *ir.Type, ok map[*ir.StructDef]bool, seen map[*ir.StructDef]bool) (*ir.Type, string) {
	if t == nil {
		return nil, ""
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeInt, ir.TypeFloat, ir.TypeBool, ir.TypeEnum:
		return nil, ""
	case ir.TypeList, ir.TypeMap, ir.TypeOption:
		for _, e := range t.Elems {
			if bad, where := unserializable(e, ok, seen); bad != nil {
				return bad, where
			}
		}
		return nil, ""
	case ir.TypeStruct:
		sd, isDef := t.Decl.(*ir.StructDef)
		if !isDef || sd.Builtin != ir.BuiltinNone || len(sd.TypeParams) > 0 {
			return t, t.String()
		}
		if seen[sd] {
			return nil, ""
		}
		seen[sd] = true
		for _, f := range sd.Fields {
			if bad, where := unserializable(f.Type, ok, seen); bad != nil {
				if where == bad.String() {
					where = sd.Name + "." + f.Name
				}
				return bad, where
			}
		}
		ok[sd] = true
		return nil, ""
	}
	return t, t.String()
}

// emitNavDecls writes each page's route class and each record type's mapping
// from a back-stack entry to the record.
func (nav *androidNav) emitNavDecls(b *strings.Builder, kc *kotlin.KtIRContext) {
	if nav == nil || len(nav.stacks) == 0 {
		return
	}
	nav.requireImports(kc)
	if nav.hasParams() {
		b.WriteString(navTypeDecl)
		// One per type, and declared once: a graph is compared with the one
		// it replaces on every composition, and a NavType made afresh each
		// time is a different graph each time -- which Compose never stops
		// recomposing.
		seen := map[string]bool{}
		for _, st := range nav.stacks {
			for _, pg := range st.pages {
				if pg.params == nil {
					continue
				}
				t := kotlin.IRTypeToKt(pg.params)
				if seen[t] {
					continue
				}
				seen[t] = true
				fmt.Fprintf(b, "val %s = snglNavType<%s>()\n\n", navTypeVal(t), t)
			}
		}
	}
	for _, def := range nav.records {
		for _, pg := range nav.pages[def] {
			b.WriteString("@Serializable\n")
			if pg.params == nil {
				fmt.Fprintf(b, "class %s\n\n", pg.route())
				continue
			}
			fmt.Fprintf(b, "data class %s(val params: %s = %s)\n\n", pg.route(), kotlin.IRTypeToKt(pg.params), kc.EvalExpr(pg.node.Prop("params")))
		}
		rec := exportName(def.Name)
		fmt.Fprintf(b, "fun %s(entry: NavBackStackEntry?): %s? {\n", recordFunc(def), rec)
		b.WriteString("    val destination = entry?.destination ?: return null\n")
		b.WriteString("    return when {\n")
		for _, pg := range nav.pages[def] {
			fmt.Fprintf(b, "        destination.hasRoute<%s>() -> %s\n", pg.route(), kc.EvalExpr(pg.record))
		}
		b.WriteString("        else -> null\n")
		b.WriteString("    }\n")
		b.WriteString("}\n\n")
	}
}

// navTypeDecl is the NavType a route's params travel as: a class of the
// program's is no type Navigation knows how to put in a back-stack entry, so
// each destination holding one names this for it in its typeMap, and it
// carries the params as their JSON.
const navTypeDecl = `inline fun <reified T> snglNavType(): NavType<T> = object : NavType<T>(isNullableAllowed = false) {
    override fun get(bundle: Bundle, key: String): T? = bundle.getString(key)?.let { Json.decodeFromString<T>(it) }
    override fun parseValue(value: String): T = Json.decodeFromString<T>(Uri.decode(value))
    override fun serializeAsValue(value: T): String = Uri.encode(Json.encodeToString(value))
    override fun put(bundle: Bundle, key: String, value: T) {
        bundle.putString(key, Json.encodeToString(value))
    }
}

`

func (nav *androidNav) hasParams() bool {
	for _, st := range nav.stacks {
		for _, pg := range st.pages {
			if pg.params != nil {
				return true
			}
		}
	}
	return false
}

func (nav *androidNav) requireImports(kc *kotlin.KtIRContext) {
	if nav.hasParams() {
		for _, imp := range []string{
			"android.net.Uri",
			"android.os.Bundle",
			"androidx.navigation.NavType",
			"kotlin.reflect.typeOf",
			"kotlinx.serialization.encodeToString",
			"kotlinx.serialization.json.Json",
		} {
			kc.RequireImport(imp)
		}
	}
	for _, imp := range []string{
		"androidx.navigation.NavBackStackEntry",
		"androidx.navigation.NavDestination.Companion.hasRoute",
		"androidx.navigation.compose.NavHost",
		"androidx.navigation.compose.composable",
		"androidx.navigation.compose.currentBackStackEntryAsState",
		"androidx.navigation.compose.rememberNavController",
		"androidx.navigation.toRoute",
		"kotlinx.serialization.Serializable",
	} {
		kc.RequireImport(imp)
	}
}

// stacksIn is the stacks a composable's view renders itself.
func (nav *androidNav) stacksIn(stmts []ir.Stmt) []*navStackK {
	if nav == nil || len(nav.stacks) == 0 {
		return nil
	}
	byNode := map[*ir.NodeInst]*navStackK{}
	for _, st := range nav.stacks {
		byNode[st.node] = st
	}
	var out []*navStackK
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if st := byNode[n]; st != nil {
					out = append(out, st)
					for _, pg := range st.pages {
						walk(pageContent(pg.node))
					}
					continue
				}
				walk(n.Children)
				for _, name := range ir.SlotNames(n.Slots) {
					walk(n.Slots[name].Body)
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.ErrorBoundary:
				walk(n.Children)
				walk(n.Failed)
			case *ir.ContextProvider:
				walk(n.Children)
			}
		}
	}
	walk(stmts)
	return out
}

// emitNavState declares a composable's stacks: the controller, the entry
// showing, and that entry as its record. In test mode the controller is also
// handed to the state class, which reads `current` off it for a test.
func emitNavState(b *strings.Builder, stacks []*navStackK, kc *kotlin.KtIRContext, testMode bool) {
	for _, st := range stacks {
		fmt.Fprintf(b, "    val %s__nav = rememberNavController()\n", st.name)
		fmt.Fprintf(b, "    val %s__entry by %s__nav.currentBackStackEntryAsState()\n", st.name, st.name)
		if st.start != nil {
			fmt.Fprintf(b, "    val %s__current = %s(%s__entry) ?: %s\n", st.name, recordFunc(st.start.record.Def), st.name, kc.EvalExpr(st.start.record))
		}
		if testMode {
			fmt.Fprintf(b, "    state.%s__nav = %s__nav\n", st.name, st.name)
		}
	}
	if len(stacks) > 0 {
		b.WriteString("\n")
	}
}

// emitNavStateMembers is the test-mode state class's half: the controller a
// composition hands it, and `current` read off it.
func emitNavStateMembers(b *strings.Builder, stacks []*navStackK, kc *kotlin.KtIRContext) {
	for _, st := range stacks {
		fmt.Fprintf(b, "    var %s__nav: androidx.navigation.NavHostController? = null\n", st.name)
		if st.start != nil {
			rec := exportName(st.start.record.Def.Name)
			fmt.Fprintf(b, "    val %s__current: %s get() = %s(%s__nav?.currentBackStackEntry) ?: %s\n", st.name, rec, recordFunc(st.start.record.Def), st.name, kc.EvalExpr(st.start.record))
		}
	}
}

// pageContent is what a page renders: its content population, or the content
// it was written with bare.
func pageContent(n *ir.NodeInst) []ir.Stmt {
	if sc := n.Slots["content"]; sc != nil {
		return sc.Body
	}
	return n.Children
}

// renderNavStack draws a stack as its NavHost, one destination per page.
func (cc *irComposeContext) renderNavStack(n *ir.NodeInst) {
	st := cc.nav.stackFor(n)
	if st == nil || st.start == nil {
		return
	}
	startRoute := st.start.route() + "()"
	cc.line("NavHost(navController = %s__nav, startDestination = %s, %s) {", st.name, startRoute, cc.buildModifier(n))
	cc.indent++
	for _, pg := range st.pages {
		sc := pg.node.Slots["content"]
		var param *ir.Param
		if sc != nil && len(sc.Params) > 0 {
			param = sc.Params[0]
		}
		switch {
		case pg.params == nil:
			cc.line("composable<%s> {", pg.route())
		case param == nil:
			cc.line("composable<%s>(typeMap = %s) {", pg.route(), typeMapFor(pg))
		default:
			cc.line("composable<%s>(typeMap = %s) { __entry ->", pg.route(), typeMapFor(pg))
		}
		cc.indent++
		saved := cc.kc
		if param != nil {
			if pg.params == nil {
				cc.line("val %s = %s", param.Name, kotlin.KtZeroFor(param.Type))
			} else {
				cc.line("val %s = __entry.toRoute<%s>().params", param.Name, pg.route())
			}
			cc.kc = cc.kc.WithLocal(param.Name)
		}
		outerAxis, outerRoot := cc.parentAxis, cc.atRoot
		cc.parentAxis, cc.atRoot = "", false
		body := pageContent(pg.node)
		if len(body) > 1 {
			cc.line("Column {")
			cc.indent++
			cc.parentAxis = "Column"
		}
		for _, s := range body {
			cc.renderStmt(s)
		}
		if len(body) > 1 {
			cc.indent--
			cc.line("}")
		}
		cc.parentAxis, cc.atRoot = outerAxis, outerRoot
		cc.kc = saved
		cc.indent--
		cc.line("}")
	}
	cc.indent--
	cc.line("}")
}

// typeMapFor is the destination's typeMap: its params' type, as the NavType
// that carries it.
func typeMapFor(pg *navPageK) string {
	t := kotlin.IRTypeToKt(pg.params)
	return fmt.Sprintf("mapOf(typeOf<%s>() to %s)", t, navTypeVal(t))
}

// navTypeVal names the NavType declared for a params type.
func navTypeVal(t string) string {
	var b strings.Builder
	for _, r := range t {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return "__navType_" + b.String()
}

func (nav *androidNav) stackFor(n *ir.NodeInst) *navStackK {
	if nav == nil {
		return nil
	}
	for _, st := range nav.stacks {
		if st.node == n {
			return st
		}
	}
	return nil
}

// pageFor is the page a record names, and the stack holding it.
func (nav *androidNav) pageFor(e ir.Expr) (*navStackK, *navPageK) {
	lit, ok := e.(*ir.StructLit)
	if nav == nil || !ok || lit.Def == nil {
		return nil, nil
	}
	id := recordID(lit)
	for _, st := range nav.stacks {
		for _, pg := range st.pages {
			if pg.record.Def == lit.Def && pg.id == id {
				return st, pg
			}
		}
	}
	return nil, nil
}

// renderNavLink draws a link as a button showing its text, whose click runs
// the link's own `@click` and then navigates to its page.
func (cc *irComposeContext) renderNavLink(n *ir.NodeInst) {
	st, pg := cc.nav.pageFor(n.Prop("to"))
	cc.line("Button(onClick = {")
	cc.indent++
	if h := codegen.NodeHandler(n, "click"); h != nil && h.Func != nil {
		for _, stmt := range h.Func.Block {
			for _, l := range cc.kc.EvalStmt(stmt) {
				cc.line("%s", l)
			}
		}
	}
	if st != nil {
		cc.line("%s", navigateTo(st.name+"__nav", pg, n.Prop("params"), cc.kc.EvalExpr))
	}
	cc.indent--
	cc.line("}, %s) {", cc.buildModifier(n))
	cc.indent++
	text := `""`
	if t := n.Prop("text"); t != nil {
		text = cc.kc.EvalExpr(t)
	}
	cc.line("Text(text = %s)", text)
	cc.indent--
	cc.line("}")
}

// navigateTo is the navigation to pg, handed params or, where they are
// absent, the page's own -- which is its route's default.
func navigateTo(nav string, pg *navPageK, params ir.Expr, tr func(ir.Expr) string) string {
	arg := ""
	if v := handedParams(params); v != nil {
		arg = "params = " + tr(v)
	}
	return fmt.Sprintf("%s.navigate(%s(%s))", nav, pg.route(), arg)
}

// handedParams is the params a `go` or a link passed, nil where it passed
// none: the absent argument is `null`, and a written one the promotion of a
// value into the option.
func handedParams(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	if t := e.ExprType(); t != nil && t.Kind == ir.TypeNull {
		return nil
	}
	if conv, ok := e.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeOption {
		if t := conv.Operand.ExprType(); t != nil && t.Kind == ir.TypeNull {
			return nil
		}
		return conv.Operand
	}
	return e
}

// stackRef is the name a call reaches a stack's locals by, and the spelling
// of the receiver they hang off: bare inside the composable, and through the
// instance a test holds when the handle was reached through it (`c.pages`).
func stackRef(e ir.Expr, suffix string, tr func(ir.Expr) string) string {
	switch x := e.(type) {
	case *ir.Ident:
		return tr(&ir.Ident{Name: x.Name + suffix, Type: ir.TypDyn})
	case *ir.Select:
		return tr(x.Operand) + "." + x.Field + suffix
	}
	return ""
}

// emitNavGo is `pages.go(page, params)`: a navigation to the page's route.
func emitNavGo(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) < 2 {
		return "", nil
	}
	lit, ok := args[1].(*ir.StructLit)
	if !ok || lit.Def == nil {
		return "", nil
	}
	pg := &navPageK{record: lit, id: recordID(lit)}
	var params ir.Expr
	if len(args) > 2 {
		params = args[2]
	}
	nav := stackRef(args[0], "__nav", tr)
	if nav == "" {
		return "", nil
	}
	return navigateTo(nav, pg, params, tr), nil
}

// emitNavBack is `pages.back()`: a pop, and nothing at the bottom, where the
// system back is the window's.
func emitNavBack(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) < 1 {
		return "", nil
	}
	nav := stackRef(args[0], "__nav", tr)
	if nav == "" {
		return "", nil
	}
	return fmt.Sprintf("run { if (%s.previousBackStackEntry != null) %s.popBackStack() }", nav, nav), nil
}

// emitNavCurrent is `pages.current`: the record of the entry showing.
func emitNavCurrent(args []ir.Expr, tr func(ir.Expr) string) (string, []string) {
	if len(args) < 1 {
		return "", nil
	}
	if ref := stackRef(args[0], "__current", tr); ref != "" {
		return ref, nil
	}
	return "", nil
}
