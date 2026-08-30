package ir

// IntrinsicDef describes a function that must be natively implemented by
// codegen backends. The IR only tracks signatures — implementations are
// registered by each language/platform via the codegen registry.
type IntrinsicDef struct {
	Name   string   // PascalCase identifier, e.g. "string.indexOf"
	Params []*Param // parameter signatures
	Return *Type    // return type
	// Purity, if non-zero, overrides the default PurityPure assumption.
	// Use this for intrinsics whose result depends on host state
	// (env vars, filesystem, time, etc.) or that have side effects.
	Purity Purity
	// MutatesReceiver marks an intrinsic whose first parameter (the receiver)
	// is mutated in place by the native implementation, even though the SNGL
	// signature returns a value. Reactivity treats a statement-level call as a
	// write to the receiver var, and backends emit an in-place mutation.
	MutatesReceiver bool
}

// IntrinsicByName returns the intrinsic definition with the given PascalCase
// name (e.g. "list.push"), or ok=false if none exists. Lets the checker and
// lowering passes read intrinsic metadata by ID rather than matching method
// names.
func IntrinsicByName(name string) (IntrinsicDef, bool) {
	def := LookupIntrinsic(name)
	if def == nil {
		return IntrinsicDef{}, false
	}
	return *def, true
}

// Intrinsics is the canonical list of language-level intrinsic functions.
// Every language backend must provide a native implementation for each entry.
var Intrinsics = []IntrinsicDef{
	// --- string ---
	{Name: "string.length", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypInt},
	{Name: "string.indexOf", Params: []*Param{{Name: "s", Type: TypString}, {Name: "sub", Type: TypString}}, Return: TypInt},
	{Name: "string.substring", Params: []*Param{{Name: "s", Type: TypString}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}}, Return: TypString},
	{Name: "string.upper", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "string.lower", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "string.trim", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "string.replace", Params: []*Param{{Name: "s", Type: TypString}, {Name: "old", Type: TypString}, {Name: "new", Type: TypString}}, Return: TypString},
	{Name: "string.split", Params: []*Param{{Name: "s", Type: TypString}, {Name: "sep", Type: TypString}}, Return: ListOf(TypString)},

	// --- float math ---
	{Name: "float.floor", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.ceil", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.round", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.pow", Params: []*Param{{Name: "base", Type: TypFloat}, {Name: "exp", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.sqrt", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.sin", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.cos", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.tan", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.asin", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.acos", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.atan", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.atan2", Params: []*Param{{Name: "y", Type: TypFloat}, {Name: "x", Type: TypFloat}}, Return: TypFloat},

	// --- list ---
	{Name: "list.length", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}}, Return: TypInt},
	{Name: "list.push", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}}, Return: ListOf(TypDyn), Purity: PurityMutates, MutatesReceiver: true},
	{Name: "list.remove", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "index", Type: TypInt}}, Return: ListOf(TypDyn), Purity: PurityMutates, MutatesReceiver: true},
	{Name: "list.indexOf", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}}, Return: TypInt},
	{Name: "list.join", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "sep", Type: TypString}}, Return: TypString},
	{Name: "list.reverse", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "list.slice", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}}, Return: ListOf(TypDyn)},
	{Name: "list.filter", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "pred", Type: TypDyn}}, Return: ListOf(TypDyn)},
	{Name: "list.map", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "fn", Type: TypDyn}}, Return: ListOf(TypDyn)},

	// --- map ---
	{Name: "map.length", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: TypInt},
	{Name: "map.keys", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "map.values", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "map.contains", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}, {Name: "key", Type: TypDyn}}, Return: TypBool},
	{Name: "map.get", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}, {Name: "key", Type: TypDyn}, {Name: "def", Type: TypDyn}}, Return: TypDyn},

	// --- color ---
	{Name: "color.hex", Params: []*Param{{Name: "c", Type: TypDyn}}, Return: TypString},

	// --- int ---
	// min/max/abs/clamp have SNGL bodies that compute the right answer; the id
	// lets a backend emit its own form instead (Go has min/max builtins, and
	// cannot spell the body's ternary as an expression at all).
	{Name: "int.min", Params: []*Param{{Name: "a", Type: TypInt}, {Name: "b", Type: TypInt}}, Return: TypInt},
	{Name: "int.max", Params: []*Param{{Name: "a", Type: TypInt}, {Name: "b", Type: TypInt}}, Return: TypInt},
	{Name: "int.abs", Params: []*Param{{Name: "x", Type: TypInt}}, Return: TypInt},
	{Name: "int.clamp", Params: []*Param{{Name: "x", Type: TypInt}, {Name: "lo", Type: TypInt}, {Name: "hi", Type: TypInt}}, Return: TypInt},
	{Name: "float.min", Params: []*Param{{Name: "a", Type: TypFloat}, {Name: "b", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.max", Params: []*Param{{Name: "a", Type: TypFloat}, {Name: "b", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.abs", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "float.clamp", Params: []*Param{{Name: "x", Type: TypFloat}, {Name: "lo", Type: TypFloat}, {Name: "hi", Type: TypFloat}}, Return: TypFloat},
	{Name: "int.parse", Params: []*Param{{Name: "s", Type: TypString}, {Name: "base", Type: TypInt}}, Return: TypInt},

	// --- regex ---

	// --- error ---
	// ErrorRaise is recognised by effect analysis as the user-facing raise
	// primitive. Codegen emits a target-appropriate error-propagation (never
	// a normal function call) — the intrinsic name is the sentinel. Returns
	// int purely to satisfy the expression-body forwarding in stdlib; the
	// value is never used because every target lowers the call to an abort.
	{Name: "error.raise", Params: []*Param{{Name: "message", Type: TypString}, {Name: "kind", Type: TypString}}, Return: TypInt},

	// --- html placement directives (GitLab #27) ---
	// HtmlFrontend / HtmlBackend are identity intrinsics: they return their sole
	// argument unchanged. Their purpose is to survive optimization as a
	// recognizable sentinel so the html platform's placement analysis can pin
	// the wrapped expression's front/back-end placement. Every non-html target
	// emits them as a pass-through (just the translated argument). PurityPure so
	// they never block folding of their argument, but the call node itself is
	// preserved because the wrapping stdlib funcs are generic (InlinePure skips
	// generics) and carry a non-empty Intrinsic id.
	{Name: "html.frontend", Params: []*Param{{Name: "v", Type: TypDyn}}, Return: TypDyn},
	{Name: "html.backend", Params: []*Param{{Name: "v", Type: TypDyn}}, Return: TypDyn},
}

// AlertIntrinsics are platform-level intrinsics for dialog/toast operations.
// Each platform backend must provide implementations.
var AlertIntrinsics = []IntrinsicDef{
	// Alerts cause visible UI side effects (dialogs/toasts); treat as Mutates
	// so the optimizer never folds calls to them.
	{Name: "Alert.toast", Params: []*Param{{Name: "message", Type: TypString}, {Name: "variant", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Alert.info", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Alert.warn", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Alert.error", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Alert.confirm", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypBool, Purity: PurityMutates},
}

// FileIntrinsics are platform-level intrinsics for file picker operations.
// Each platform backend must provide implementations. Pickers read host
// filesystem state (and can be triggered by user interaction); mark
// PurityReadonly so they're never folded at compile time.
var FileIntrinsics = []IntrinsicDef{
	{Name: "File.pick", Params: []*Param{}, Return: TypString, Purity: PurityReadonly},
	{Name: "File.pickFolder", Params: []*Param{}, Return: TypString, Purity: PurityReadonly},
}

// I18nIntrinsics are stdlib intrinsics for i18n / locale-aware formatting.
// Each takes an explicit `locale` first parameter; the SNGL i18n wrappers
// thread the active locale context into these calls via NoContext.
//
// Plural / SelectOrdinal use MapOf(TypDyn, TypString) for the forms map
// because PluralKey is a SNGL struct (declared in lib/i18n.sngl) — the
// intrinsic doesn't need to know about that struct; it just receives the
// runtime values.
var I18nIntrinsics = []IntrinsicDef{
	// The entry points a program calls. Their parameters vary with whether
	// NoContext has threaded the locale in yet, so the table records the id and
	// the result; the emitters read the arguments they were given.
	{Name: "i18n.defaultLocale", Params: nil, Return: TypString},
	{Name: "i18n.tr", Params: nil, Return: TypString},
	{Name: "i18n.trInline", Params: nil, Return: TypString},
	{Name: "i18n.format", Params: nil, Return: TypString},
	{Name: "i18n.numberInt", Params: nil, Return: TypString},
	{Name: "i18n.numberFloat", Params: nil, Return: TypString},
	{Name: "i18n.date", Params: nil, Return: TypString},
	{Name: "i18n.time", Params: nil, Return: TypString},
	{Name: "i18n.datetime", Params: nil, Return: TypString},
	{Name: "i18n.select", Params: nil, Return: TypString},
	{Name: "i18n.plural", Params: nil, Return: TypString},
	{Name: "i18n.selectordinal", Params: nil, Return: TypString},

	{Name: "i18n.exactly", Params: []*Param{{Name: "n", Type: TypInt}}, Return: TypDyn, Purity: PurityPure},
	// DefaultLocale reads host env vars ($LC_ALL/$LC_MESSAGES/$LANG);
	// PurityReadonly prevents compile-time folding of i18n.defaultLocale().
	{Name: "i18n._defaultLocale", Params: nil, Return: TypString, Purity: PurityReadonly},
	{Name: "i18n._translate", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "key", Type: TypString},
		{Name: "inlinedTemplate", Type: TypString},
		{Name: "args", Type: MapOf(TypString, TypDyn)},
	}, Return: TypString},
	{Name: "i18n._format", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "template", Type: TypString},
		{Name: "args", Type: MapOf(TypString, TypDyn)},
	}, Return: TypString},
	{Name: "i18n._numberInt", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "n", Type: TypInt},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "i18n._numberFloat", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "n", Type: TypFloat},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	// The date/time/datetime params accept the stdlib date/time/datetime
	// structs. This intrinsic list is built at package init — before the
	// stdlib is parsed — so the params are typed dyn to stay independent of
	// stdlib registration order. The wrapping stdlib funcs (i18n.date, etc.)
	// carry the concrete struct types; codegen dispatches by intrinsic name,
	// not by these param types.
	{Name: "i18n._date", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "d", Type: TypDyn},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "i18n._time", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "t", Type: TypDyn},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "i18n._dateTime", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "dt", Type: TypDyn},
		{Name: "dateStyle", Type: TypString},
		{Name: "timeStyle", Type: TypString},
	}, Return: TypString},
	{Name: "i18n._select", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "value", Type: TypString},
		{Name: "cases", Type: MapOf(TypString, TypString)},
	}, Return: TypString},
	{Name: "i18n._plural", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "count", Type: TypInt},
		{Name: "forms", Type: MapOf(TypDyn, TypString)},
	}, Return: TypString},
	{Name: "i18n._selectOrdinal", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "count", Type: TypInt},
		{Name: "forms", Type: MapOf(TypDyn, TypString)},
	}, Return: TypString},
}

// The node operations: the protocol between the lowering passes that flatten a
// visual tree into imperative statements and the platforms that rebuild it.
//
// They are ids stamped on a synthesized Func rather than declarations in a
// library package, because no program calls them. Nothing in SNGL can name
// them, no language registers an emitter for them, and the only consumer is
// codegen.WalkLowered, which matches on the id and dispatches to a method of
// IntrinsicTranslator — a platform interface.
const (
	NodeOpCreateNode      = "CreateNode"
	NodeOpCreateComponent = "CreateComponent"
	NodeOpAppendChild     = "AppendChild"
	NodeOpRemoveChild     = "RemoveChild"
	NodeOpAttachHandler   = "AttachHandler"
)

// NodeOps is every node operation, for the passes that build a call per op.
var NodeOps = []string{
	NodeOpCreateNode,
	NodeOpCreateComponent,
	NodeOpAppendChild,
	NodeOpRemoveChild,
	NodeOpAttachHandler,
}

// CanvasIntrinsics are platform-level intrinsics for Canvas2D drawing.
// Each canvas-capable platform must provide native implementations.
// The ctx parameter is an opaque platform draw context (TypDyn).
var CanvasIntrinsics = []IntrinsicDef{
	{Name: "CanvasSave", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasRestore", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasApplyStyle", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "style", Type: TypDyn},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawRect", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "x", Type: TypFloat},
		{Name: "y", Type: TypFloat},
		{Name: "w", Type: TypFloat},
		{Name: "h", Type: TypFloat},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawCircle", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "cx", Type: TypFloat},
		{Name: "cy", Type: TypFloat},
		{Name: "r", Type: TypFloat},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawEllipse", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "cx", Type: TypFloat},
		{Name: "cy", Type: TypFloat},
		{Name: "rx", Type: TypFloat},
		{Name: "ry", Type: TypFloat},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawLine", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "x1", Type: TypFloat},
		{Name: "y1", Type: TypFloat},
		{Name: "x2", Type: TypFloat},
		{Name: "y2", Type: TypFloat},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawPath", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "cmds", Type: ListOf(TypDyn)},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawText", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "x", Type: TypFloat},
		{Name: "y", Type: TypFloat},
		{Name: "content", Type: TypString},
	}, Return: TypVoid, Purity: PurityMutates},
	{Name: "CanvasDrawImage", Params: []*Param{
		{Name: "ctx", Type: TypDyn},
		{Name: "x", Type: TypFloat},
		{Name: "y", Type: TypFloat},
		{Name: "w", Type: TypFloat},
		{Name: "h", Type: TypFloat},
		{Name: "src", Type: TypString},
	}, Return: TypVoid, Purity: PurityMutates},
}

// IsI18nCall reports whether c targets an i18n stdlib entry point. The mark on
// the declaration answers for both shapes — the wrapper a program writes and
// the primitive left after NoContext and InlinePure collapse it — so "package
// uses i18n" is correct whichever passes have run, and stays correct when the
// program imports the package under an alias.
func IsI18nCall(c *Call) bool {
	if c == nil || c.Func == nil {
		return false
	}
	return IsI18nIntrinsic(c.Func.Intrinsic)
}

// IsI18nIntrinsic reports whether name matches one of I18nIntrinsics. Empty
// name returns false (most user funcs carry no Intrinsic tag).
func IsI18nIntrinsic(name string) bool {
	if name == "" {
		return false
	}
	for i := range I18nIntrinsics {
		if I18nIntrinsics[i].Name == name {
			return true
		}
	}
	return false
}

// IsI18nPluralKey reports whether sel reads one of i18n's predeclared
// PluralKey constants — a CLDR category (zero, one, two, few, many, other).
// Plural-map literals carry these as bare Selects that survive lowering, so
// both usage detection and the JS/Kotlin backends have to recognize them.
//
// The select's *type* is what identifies it. Matching the operand's spelling
// instead read `i18n.one` and missed `t.one` under `import t "sngl:i18n"`,
// which then emitted the alias as a bare identifier.
func IsI18nPluralKey(sel *Select) bool {
	if sel == nil {
		return false
	}
	switch sel.Field {
	case "zero", "one", "two", "few", "many", "other":
	default:
		return false
	}
	return isPluralKeyType(sel.Type)
}

func isPluralKeyType(t *Type) bool {
	if t == nil || t.Kind != TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*StructDef)
	return ok && sd.Name == pluralKeyTypeName
}

// The one name left: PluralKey is a lib/i18n declaration, not something a
// program spells, so it does not vary with how the import is written.
const pluralKeyTypeName = "PluralKey"

// LookupIntrinsic returns the intrinsic definition for the given name, or nil.
// Searches all intrinsic lists.
func LookupIntrinsic(name string) *IntrinsicDef {
	for _, list := range [][]IntrinsicDef{Intrinsics, AlertIntrinsics, FileIntrinsics, I18nIntrinsics, CanvasIntrinsics} {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
	}
	return nil
}
