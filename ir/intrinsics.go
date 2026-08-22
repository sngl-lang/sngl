package ir

// IntrinsicDef describes a function that must be natively implemented by
// codegen backends. The IR only tracks signatures — implementations are
// registered by each language/platform via the codegen registry.
type IntrinsicDef struct {
	Name   string   // PascalCase identifier, e.g. "StrIndexOf"
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
// name (e.g. "ListPush"), or ok=false if none exists. Lets the checker and
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
	{Name: "StrLength", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypInt},
	{Name: "StrIndexOf", Params: []*Param{{Name: "s", Type: TypString}, {Name: "sub", Type: TypString}}, Return: TypInt},
	{Name: "StrSubstring", Params: []*Param{{Name: "s", Type: TypString}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}}, Return: TypString},
	{Name: "StrUpper", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "StrLower", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "StrTrim", Params: []*Param{{Name: "s", Type: TypString}}, Return: TypString},
	{Name: "StrReplace", Params: []*Param{{Name: "s", Type: TypString}, {Name: "old", Type: TypString}, {Name: "new", Type: TypString}}, Return: TypString},
	{Name: "StrSplit", Params: []*Param{{Name: "s", Type: TypString}, {Name: "sep", Type: TypString}}, Return: ListOf(TypString)},

	// --- float math ---
	{Name: "MathFloor", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypInt},
	{Name: "MathCeil", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypInt},
	{Name: "MathRound", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypInt},
	{Name: "MathPow", Params: []*Param{{Name: "base", Type: TypFloat}, {Name: "exp", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathSqrt", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathSin", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathCos", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathTan", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathAsin", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathAcos", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathAtan", Params: []*Param{{Name: "x", Type: TypFloat}}, Return: TypFloat},
	{Name: "MathAtan2", Params: []*Param{{Name: "y", Type: TypFloat}, {Name: "x", Type: TypFloat}}, Return: TypFloat},

	// --- list ---
	{Name: "ListLength", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}}, Return: TypInt},
	{Name: "ListPush", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}}, Return: ListOf(TypDyn), Purity: PurityMutates, MutatesReceiver: true},
	{Name: "ListRemove", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "index", Type: TypInt}}, Return: ListOf(TypDyn), Purity: PurityMutates, MutatesReceiver: true},
	{Name: "ListIndexOf", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}}, Return: TypInt},
	{Name: "ListJoin", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "sep", Type: TypString}}, Return: TypString},
	{Name: "ListReverse", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "ListSlice", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "start", Type: TypInt}, {Name: "end", Type: TypInt}}, Return: ListOf(TypDyn)},
	{Name: "ListFilter", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "pred", Type: TypDyn}}, Return: ListOf(TypDyn)},
	{Name: "ListMap", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "fn", Type: TypDyn}}, Return: ListOf(TypDyn)},

	// --- map ---
	{Name: "MapLength", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: TypInt},
	{Name: "MapKeys", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "MapValues", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}}, Return: ListOf(TypDyn)},
	{Name: "MapContains", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}, {Name: "key", Type: TypDyn}}, Return: TypBool},
	{Name: "MapGet", Params: []*Param{{Name: "m", Type: MapOf(TypDyn, TypDyn)}, {Name: "key", Type: TypDyn}, {Name: "def", Type: TypDyn}}, Return: TypDyn},

	// --- color ---
	{Name: "ColorHex", Params: []*Param{{Name: "c", Type: TypDyn}}, Return: TypString},

	// --- regex ---
	{Name: "RegexMatches", Params: []*Param{{Name: "re", Type: TypString}, {Name: "s", Type: TypString}}, Return: TypBool},
	{Name: "RegexFind", Params: []*Param{{Name: "re", Type: TypString}, {Name: "s", Type: TypString}}, Return: TypString},

	// --- error ---
	// ErrorRaise is recognised by effect analysis as the user-facing raise
	// primitive. Codegen emits a target-appropriate error-propagation (never
	// a normal function call) — the intrinsic name is the sentinel. Returns
	// int purely to satisfy the expression-body forwarding in stdlib; the
	// value is never used because every target lowers the call to an abort.
	{Name: "ErrorRaise", Params: []*Param{{Name: "message", Type: TypString}, {Name: "kind", Type: TypString}}, Return: TypInt},

	// --- html placement directives (GitLab #27) ---
	// HtmlFrontend / HtmlBackend are identity intrinsics: they return their sole
	// argument unchanged. Their purpose is to survive optimization as a
	// recognizable sentinel so the html platform's placement analysis can pin
	// the wrapped expression's front/back-end placement. Every non-html target
	// emits them as a pass-through (just the translated argument). PurityPure so
	// they never block folding of their argument, but the call node itself is
	// preserved because the wrapping stdlib funcs are generic (InlinePure skips
	// generics) and carry a non-empty Intrinsic id.
	{Name: "HtmlFrontend", Params: []*Param{{Name: "v", Type: TypDyn}}, Return: TypDyn},
	{Name: "HtmlBackend", Params: []*Param{{Name: "v", Type: TypDyn}}, Return: TypDyn},
}

// AlertIntrinsics are platform-level intrinsics for dialog/toast operations.
// Each platform backend must provide implementations.
var AlertIntrinsics = []IntrinsicDef{
	// Alerts cause visible UI side effects (dialogs/toasts); treat as Mutates
	// so the optimizer never folds calls to them.
	{Name: "Toast", Params: []*Param{{Name: "message", Type: TypString}, {Name: "variant", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Info", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Warn", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Error", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt, Purity: PurityMutates},
	{Name: "Confirm", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypBool, Purity: PurityMutates},
}

// FileIntrinsics are platform-level intrinsics for file picker operations.
// Each platform backend must provide implementations. Pickers read host
// filesystem state (and can be triggered by user interaction); mark
// PurityReadonly so they're never folded at compile time.
var FileIntrinsics = []IntrinsicDef{
	{Name: "Pick", Params: []*Param{}, Return: TypString, Purity: PurityReadonly},
	{Name: "PickFolder", Params: []*Param{}, Return: TypString, Purity: PurityReadonly},
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
	// DefaultLocale reads host env vars ($LC_ALL/$LC_MESSAGES/$LANG);
	// PurityReadonly prevents compile-time folding of i18n.defaultLocale().
	{Name: "DefaultLocale", Params: nil, Return: TypString, Purity: PurityReadonly},
	{Name: "Translate", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "key", Type: TypString},
		{Name: "inlinedTemplate", Type: TypString},
		{Name: "args", Type: MapOf(TypString, TypDyn)},
	}, Return: TypString},
	{Name: "Format", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "template", Type: TypString},
		{Name: "args", Type: MapOf(TypString, TypDyn)},
	}, Return: TypString},
	{Name: "NumberInt", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "n", Type: TypInt},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "NumberFloat", Params: []*Param{
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
	{Name: "Date", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "d", Type: TypDyn},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "Time", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "t", Type: TypDyn},
		{Name: "style", Type: TypString},
	}, Return: TypString},
	{Name: "DateTime", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "dt", Type: TypDyn},
		{Name: "dateStyle", Type: TypString},
		{Name: "timeStyle", Type: TypString},
	}, Return: TypString},
	{Name: "Select", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "value", Type: TypString},
		{Name: "cases", Type: MapOf(TypString, TypString)},
	}, Return: TypString},
	{Name: "Plural", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "count", Type: TypInt},
		{Name: "forms", Type: MapOf(TypDyn, TypString)},
	}, Return: TypString},
	{Name: "SelectOrdinal", Params: []*Param{
		{Name: "locale", Type: TypString},
		{Name: "count", Type: TypInt},
		{Name: "forms", Type: MapOf(TypDyn, TypString)},
	}, Return: TypString},
}

// LowerIntrinsics are intrinsics emitted by lowering passes. They live in
// the `lower` namespace (imported by the synthetic internal://lower
// package). Every codegen backend that consumes lowered output must
// provide native translations.
var LowerIntrinsics = []IntrinsicDef{
	{Name: "CreateNode", Params: []*Param{{Name: "tag", Type: TypString}}, Return: TypDyn},
	{Name: "AppendChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
	{Name: "RemoveChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
	{Name: "AttachHandler", Params: []*Param{{Name: "node", Type: TypDyn}, {Name: "event", Type: TypString}, {Name: "handler", Type: TypDyn}}, Return: TypVoid},
	{Name: "CreateComponent", Params: []*Param{{Name: "comp", Type: TypDyn}, {Name: "props", Type: TypDyn}}, Return: TypDyn},
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

// IsI18nCall reports whether c targets an i18n stdlib entry point. Two shapes
// match: the pre-inlining wrapper (c.Func.Receiver == "i18n", e.g. i18n.tr /
// i18n.numberInt) and the post-inlining direct intrinsic (c.Func.Intrinsic in
// I18nIntrinsics, after NoContext+InlinePure collapse the wrapper). Either
// match keeps "package uses i18n" correct regardless of which passes have run.
func IsI18nCall(c *Call) bool {
	if c == nil || c.Func == nil {
		return false
	}
	if c.Func.Receiver == "i18n" {
		return true
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

// IsI18nPluralKey reports whether name is a CLDR plural category (zero, one,
// two, few, many, other). Plural-map literals carry these as bare i18n.<key>
// Selects that survive lowering, so usage detection must recognize them.
func IsI18nPluralKey(name string) bool {
	switch name {
	case "zero", "one", "two", "few", "many", "other":
		return true
	}
	return false
}

// LookupIntrinsic returns the intrinsic definition for the given name, or nil.
// Searches all intrinsic lists.
func LookupIntrinsic(name string) *IntrinsicDef {
	for _, list := range [][]IntrinsicDef{Intrinsics, AlertIntrinsics, FileIntrinsics, I18nIntrinsics, LowerIntrinsics, CanvasIntrinsics} {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
	}
	return nil
}
