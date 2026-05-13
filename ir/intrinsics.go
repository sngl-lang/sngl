package ir

// IntrinsicDef describes a function that must be natively implemented by
// codegen backends. The IR only tracks signatures — implementations are
// registered by each language/platform via the codegen registry.
type IntrinsicDef struct {
	Name   string   // PascalCase identifier, e.g. "StrIndexOf"
	Params []*Param // parameter signatures
	Return *Type    // return type
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
	{Name: "ListPush", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "item", Type: TypDyn}}, Return: ListOf(TypDyn)},
	{Name: "ListRemove", Params: []*Param{{Name: "l", Type: ListOf(TypDyn)}, {Name: "index", Type: TypInt}}, Return: ListOf(TypDyn)},
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
	{Name: "RegexMatches", Params: []*Param{{Name: "re", Type: TypRegex}, {Name: "s", Type: TypString}}, Return: TypBool},
	{Name: "RegexFind", Params: []*Param{{Name: "re", Type: TypRegex}, {Name: "s", Type: TypString}}, Return: TypString},

	// --- error ---
	// ErrorRaise is recognised by effect analysis as the user-facing raise
	// primitive. Codegen emits a target-appropriate error-propagation (never
	// a normal function call) — the intrinsic name is the sentinel. Returns
	// int purely to satisfy the expression-body forwarding in stdlib; the
	// value is never used because every target lowers the call to an abort.
	{Name: "ErrorRaise", Params: []*Param{{Name: "message", Type: TypString}, {Name: "kind", Type: TypString}}, Return: TypInt},
}

// AlertIntrinsics are platform-level intrinsics for dialog/toast operations.
// Each platform backend must provide implementations.
var AlertIntrinsics = []IntrinsicDef{
	{Name: "Toast", Params: []*Param{{Name: "message", Type: TypString}, {Name: "variant", Type: TypString}}, Return: TypInt},
	{Name: "Info", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt},
	{Name: "Warn", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt},
	{Name: "Error", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypInt},
	{Name: "Confirm", Params: []*Param{{Name: "message", Type: TypString}}, Return: TypBool},
}

// FileIntrinsics are platform-level intrinsics for file picker operations.
// Each platform backend must provide implementations.
var FileIntrinsics = []IntrinsicDef{
	{Name: "Pick", Params: []*Param{}, Return: TypString},
	{Name: "PickFolder", Params: []*Param{}, Return: TypString},
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
}

// LookupIntrinsic returns the intrinsic definition for the given name, or nil.
// Searches all intrinsic lists.
func LookupIntrinsic(name string) *IntrinsicDef {
	for _, list := range [][]IntrinsicDef{Intrinsics, AlertIntrinsics, FileIntrinsics, LowerIntrinsics} {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
	}
	return nil
}
