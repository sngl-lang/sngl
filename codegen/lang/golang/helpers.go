package golang

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ir"
)

// ModelFreeFuncs names the user functions a Model-receiver platform
// (bubbletea, fyne, gtk4) emits as free package-level functions rather than as
// methods on its Model, for ExprCtx.FreeFuncs.
//
// A top-level function is emitted free where it can be, because a *method* on
// a user type is free too — Go has no receiver to hang one of those on — and a
// type method calling a Model method has no Model to call it through. What it
// cannot be is free while it touches a package-level var: those are fields of
// the same Model, and `NameStateVar` spells one `m.x` in every scope.
// ModelStateFuncs is that exception.
func ModelFreeFuncs(pkg *ir.Package) map[string]bool {
	if pkg == nil {
		return nil
	}
	componentFuncs := map[*ir.Func]bool{}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			componentFuncs[fn] = true
		}
	}
	// A window owns funcs the way a component does, and its state is in the
	// same Model -- so one of its funcs is a method too. Left out, a `press()`
	// written in a window body was emitted free and named `m.c` with no
	// receiver to read it from. A harness convention hid this: `component
	// main` owned the state instead, and a window rarely had any.
	for _, w := range pkg.Windows {
		for _, fn := range w.Funcs {
			componentFuncs[fn] = true
		}
	}
	stateFuncs := ModelStateFuncs(pkg)
	out := map[string]bool{}
	for _, fn := range pkg.Funcs {
		if fn.IsTest || fn.Receiver != "" || fn.Synthesized || componentFuncs[fn] {
			continue
		}
		if isComputedSig(fn) || stateFuncs[fn] {
			continue
		}
		out[fn.Name] = true
	}
	return out
}

// ModelStateFuncs names the top-level funcs a Model-receiver platform must
// emit as Model methods even though they belong to no component: the ones that
// read or write a package-level var, and everything that reaches one through a
// call.
//
// The transitive half is not defensive. A caller emitted free spells a call to
// a Model method `m.callee(…)`, so one state-touching func drags every
// top-level caller of it onto the receiver or the call site names an `m` its
// own signature does not declare — which is the same undefined `m` this set
// exists to remove from the callee.
//
// A method on a user type is out of scope and stays free: Go has no receiver
// to hang one on. One that reaches a package var still emits an undefined `m`,
// which this does not fix and cannot -- there is nowhere to put the receiver.
func ModelStateFuncs(pkg *ir.Package) map[*ir.Func]bool {
	if pkg == nil {
		return nil
	}
	// Consts are excluded because EvalIdent already asks the scope about one
	// (NameConst) and spells it bare outside a Model method; a state var
	// (NameStateVar) is `m.x` unconditionally.
	state := map[ir.Symbol]bool{}
	for _, v := range pkg.Vars {
		state[v] = true
	}

	touches := map[*ir.Func]bool{}
	calls := map[*ir.Func][]*ir.Func{}
	for _, fn := range pkg.Funcs {
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			switch e := n.(type) {
			case *ir.Ident:
				if state[e.Sym] {
					touches[fn] = true
				}
				if callee, ok := e.Sym.(*ir.Func); ok {
					calls[fn] = append(calls[fn], callee)
				}
			case *ir.Call:
				if e.Func != nil {
					calls[fn] = append(calls[fn], e.Func)
				}
			}
			return nil
		})
	}

	for changed := true; changed; {
		changed = false
		for fn, callees := range calls {
			if touches[fn] {
				continue
			}
			for _, callee := range callees {
				if touches[callee] {
					touches[fn] = true
					changed = true
					break
				}
			}
		}
	}
	return touches
}

// isComputedSig mirrors codegen.IsComputed without the import: a zero-arg
// function with a return type is derived state, and those stay Model methods
// so the runtime can re-read them.
func isComputedSig(fn *ir.Func) bool {
	return len(fn.Params) == 0 && fn.Return != nil && fn.Return.Kind != ir.TypeVoid
}

// translateIRLiteral renders an ir.Literal as its Go source form. Used by
// Translator.TranslateIRLiteral (the LangTranslator literal hook); the main
// expression path uses GoIRContext.evalLiteral. Unit/temporal literals route
// through the dedicated Lower*LiteralGo helpers.
func translateIRLiteral(n *ir.Literal) string {
	if n == nil {
		return "nil"
	}
	if n.Suffix != "" {
		if out, ok := LowerUnitLiteralGo(n); ok {
			return out
		}
		// Non-unit literal that carries a suffix (shouldn't normally
		// happen) — fall back to a quoted "raw+suffix" string.
		return fmt.Sprintf("%q", n.Value)
	}
	if n.Type != nil {
		switch n.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", n.Value)
		case ir.TypeInt, ir.TypeFloat, ir.TypeBool:
			return n.Value
		case ir.TypeNull:
			return "nil"
		case ir.TypeStruct:
			if out, ok := LowerTimeLiteralGo(n); ok {
				return out
			}
			if ir.StringReprStruct(n.Type) {
				return fmt.Sprintf("%q", n.Value)
			}
		}
	}
	return n.Value
}

// ExportName capitalizes the first letter for Go exported names.
func ExportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// TypeHintToGo converts a SNGL type hint to a Go type string.
func TypeHintToGo(hint string) string {
	if strings.HasPrefix(hint, "[]") {
		return "[]" + TypeHintToGo(hint[2:])
	}
	if strings.HasPrefix(hint, "list:") {
		return "[]" + TypeHintToGo(hint[5:])
	}
	if strings.HasPrefix(hint, "option:") {
		return "*" + TypeHintToGo(hint[7:])
	}
	if strings.HasPrefix(hint, "enum:") {
		return "string"
	}
	switch hint {
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idnEmail", "idnHostname", "irl", "irlReference", "urlReference",
		"urlTemplate", "currency", "country2", "country3", "countrySubdivision", "decimal":
		return "string"
	case "date", "time", "datetime":
		return "time.Time"
	case "duration":
		return "time.Duration"
	default:
		if strings.Contains(hint, ".") && !strings.ContainsAny(hint, ":~<>") {
			return hint
		}
		if !strings.ContainsAny(hint, ":~<>") && hint != "" {
			return ExportName(hint)
		}
		return "any"
	}
}

// ComponentRenderMethod returns the Model method name a Go backend emits for
// a user component's per-instance render (e.g. "renderTreeView" for "TreeView").
// Shared by every Go platform (fyne, bubbletea, gtk4) and the
// lower.CreateComponent dispatch so call sites and definitions stay in sync.
func ComponentRenderMethod(componentName string) string {
	return "render" + ExportName(componentName)
}

// CreateComponentTarget returns the component a lower.CreateComponent call
// instantiates, or nil when call is not one. The declaration is the argument,
// so a platform emitting the handle can name the record's type without
// re-deriving which component it belongs to.
func CreateComponentTarget(call *ir.Call) *ir.Component {
	if call == nil || call.Func == nil || call.Func.Name != "CreateComponent" || len(call.Args) == 0 {
		return nil
	}
	id, ok := call.Args[0].Value.(*ir.Ident)
	if !ok {
		return nil
	}
	comp, _ := id.Sym.(*ir.Component)
	return comp
}

// The shape of a component instance in emitted Go. An instance is a struct
// pointer carrying the state a component's own `var`s need when it cannot be
// inlined, the node it renders as, one setter per prop it can absorb, and a
// teardown.
//
// Named here for the same reason ComponentRenderMethod is: every Go platform
// emits these and the lowering dispatches to them, so a name spelled twice is
// a name that drifts.

// ComponentInstanceType is the struct a non-inlinable component's instances
// are allocated as.
func ComponentInstanceType(componentName string) string {
	return ExportName(componentName) + "Instance"
}

// ComponentInstanceCtor is the function that allocates one.
func ComponentInstanceCtor(componentName string) string {
	return "new" + ExportName(componentName) + "Instance"
}

// ComponentRootField is the field holding the node an instance renders as.
const ComponentRootField = "Root"

// ComponentDestroyMethod ends an instance's lifetime: effect teardowns, timer
// cancels, whatever the host has to be given back. Not detachment, which
// RemoveChild already says and which happens far more often.
const ComponentDestroyMethod = "Destroy"

// ComponentSetterMethod is the setter an instance carries for one prop it can
// absorb. A prop it cannot is never routed here: lowering recreates the
// instance instead, which is what #[construct] selects.
func ComponentSetterMethod(prop string) string {
	return "Set" + ExportName(prop)
}

// ZeroValueGo returns the Go zero-value expression for a SNGL type hint or
// a Go type string (func(...), []T, pkg.T, etc.).
func ZeroValueGo(hint string) string {
	// Fast-path on Go syntax — don't re-run TypeHintToGo which would mangle
	// `func(...)` into `Func(...)`.
	if strings.HasPrefix(hint, "func(") || strings.HasPrefix(hint, "func ") {
		return "nil"
	}
	if strings.HasPrefix(hint, "[]") || strings.HasPrefix(hint, "*") {
		return "nil"
	}
	goType := TypeHintToGo(hint)
	switch goType {
	case "int":
		return "0"
	case "float64":
		return "0.0"
	case "bool":
		return "false"
	case "string":
		return `""`
	case "time.Time":
		return "time.Time{}"
	case "time.Duration":
		return "0"
	case "any":
		return "nil"
	default:
		if strings.HasPrefix(goType, "[]") {
			return "nil"
		}
		if strings.HasPrefix(goType, "*") {
			return "nil"
		}
		if strings.HasPrefix(goType, "func(") || strings.HasPrefix(goType, "func ") {
			return "nil"
		}
		// Named/qualified type → struct literal zero.
		return goType + "{}"
	}
}

// ErrorEventDecl is the Go declaration of `ErrorEvent`, the payload
// evalErrorAwareCall names at every raise site.
//
// The stdlib declares the struct, but codegen does not flow stdlib types into
// user output, so each Model-receiver platform materialises it. Shared here
// because it was materialised by exactly one of the three: `error.raise` on
// fyne or gtk4 emitted a panic naming a type nothing declared, and the program
// did not build. Emit it when Package.UsesErrorHandling.
const ErrorEventDecl = "type ErrorEvent struct {\n\tMessage string\n\tKind    string\n}\n\n"
