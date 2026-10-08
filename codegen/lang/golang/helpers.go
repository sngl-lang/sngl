package golang

import (
	"fmt"
	"strings"
	"unicode"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// ModelFreeFuncs names the user functions a Model-receiver platform
// (bubbletea, fyne, gtk4) emits as free package-level functions rather than as
// methods on its Model, for ExprCtx.FreeFuncs.
//
// A top-level function is emitted free where it can be, so that a lifted type
// method — which has no Model to dispatch through — can call it. What it
// cannot be is free while it touches a package-level var: those are fields of
// the same Model, and `NameStateVar` spells one `m.x` in every scope, with no
// "outside a method" case. ModelStateFuncs is that exception, and it is the
// one set the three platform emit loops read for this.
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
	// A func written in a window body is the package's, so it is not in
	// componentFuncs and reaches the Model the other way: ModelStateFuncs
	// answers for what it touches. That is the same question the window
	// membership was standing in for -- a `press()` that writes state is a
	// method, one that writes none has no receiver to want.
	stateFuncs := ModelStateFuncs(pkg)
	out := map[string]bool{}
	for _, fn := range pkg.Funcs {
		// A promoted handler is always a Model method: it is wired to a widget
		// by name, and nothing calls it, so it never had to be in the set.
		if fn.IsTest || fn.Receiver != "" || fn.Synthesized || componentFuncs[fn] || fn.LoweredFromEvent != "" {
			continue
		}
		if isComputedSig(fn) || stateFuncs[fn] {
			continue
		}
		out[fn.Name] = true
	}
	return out
}

// ModelCallee is how a Model-receiver platform's own scaffolding names a
// package function it calls -- `m.__run`, or `__run` when the function reads
// no state and was emitted free. The same rule a call in the program follows,
// for a caller written as a string rather than as IR.
func ModelCallee(pkg *ir.Package, fn *ir.Func, recv string) string {
	if ModelFreeFuncs(pkg)[fn.Name] {
		return ExportName(fn.Name)
	}
	return recv + "." + fn.Name
}

// ModelStateFuncs names the top-level funcs a Model-receiver platform must
// emit as Model methods even though they belong to no component: the ones that
// read or write a package-level var, and everything that reaches one through a
// call.
//
// The rule is codegen.PackageStateFuncs and is shared with android, which asks
// the same question and answers it with a local `fun` inside the composable
// holding the state. Kept as a name of its own because three Go platforms read
// it and what they read it *for* is the Model receiver.
//
// A method on a user type is in the set too and is answered differently, the
// receiver slot being spent: ModelParamFuncs narrows to those.
func ModelStateFuncs(pkg *ir.Package) map[*ir.Func]bool {
	return codegen.PackageStateFuncs(pkg)
}

// ModelParamFuncs names the methods on a user type that a Model-receiver
// platform lifts to a free function and that touch package state, for
// ExprCtx.ModelParamFuncs. Each takes the Model as a trailing parameter.
//
// The receiver is not available to carry it: Go has no methods to attach to
// some of these types, so the lifted form is `CalcPending(k Calc)` and the
// Model has to arrive as an argument.
//
// One map answers for the signature (EmitTypeMethodDef) and for the call site
// (evalTypeMethodCall), which cannot then disagree about the arity.
func ModelParamFuncs(pkg *ir.Package) map[*ir.Func]bool {
	out := map[*ir.Func]bool{}
	for fn := range ModelStateFuncs(pkg) {
		if fn.Receiver != "" && LiftsToFreeFunc(pkg, fn.Receiver) {
			out[fn] = true
		}
	}
	return out
}

// LiftsToFreeFunc reports whether a method on this receiver is emitted as a
// free `ReceiverMethod(recv, …)` function rather than dispatched through the
// Model. A component's method is the other case and stays a Model method, so
// it has a receiver to read state from already.
//
// bubbletea, fyne and gtk4 each route their emit loop through this rather than
// asking again: the call site (evalTypeMethodCall) lifts a method on any type
// the package declares, and a narrower emitter answer emits a definition in a
// form no call site names.
func LiftsToFreeFunc(pkg *ir.Package, receiver string) bool {
	if pkg == nil || receiver == "" {
		return false
	}
	for _, c := range pkg.Components {
		if c.Name == receiver {
			return false
		}
	}
	if isPrimitiveTypeName(receiver) {
		return true
	}
	for _, s := range pkg.Structs {
		if s.Name == receiver {
			return true
		}
	}
	for _, e := range pkg.Enums {
		if e.Name == receiver {
			return true
		}
	}
	for _, u := range pkg.Units {
		if u.Name == receiver {
			return true
		}
	}
	return false
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
		case ir.TypeFloat:
			return goFloatLiteral(n.Value)
		case ir.TypeInt, ir.TypeBool:
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

// InstanceModelField is the field an instance holds the Model it belongs to
// in, which is how it reaches the page's state and widgets. Prefixed, because
// a component may declare a var named anything else.
const InstanceModelField = "__model"

// InstanceCtorParam is what an instance ctor calls the parameter for prop, so
// a prop named for a package the file imports (`container`) does not shadow it.
func InstanceCtorParam(prop string) string { return "__a_" + prop }

// PageNodes is the names the page holds as Model fields that an instance of
// comp could reach: the vars and created nodes of every owner that is not a
// component built at run time, less comp's own, since a synthesized name may
// be spelled the same in both and in comp's scope means comp's.
func PageNodes(pkg *ir.Package, comp *ir.Component) map[string]bool {
	own := ownerNames(comp.Vars, comp.Body, comp.Funcs)
	out := map[string]bool{}
	for _, o := range ir.Owners(pkg) {
		if o.Comp != nil && o.Comp.RuntimeInstance {
			continue
		}
		for name := range ownerNames(o.Vars, o.Stmts(), o.Funcs) {
			if !own[name] {
				out[name] = true
			}
		}
	}
	return out
}

// ownerNames is the vars an owner declares and the nodes its body and funcs
// create, by name.
func ownerNames(vars []*ir.Var, body []ir.Stmt, funcs []*ir.Func) map[string]bool {
	out := map[string]bool{}
	for _, v := range vars {
		out[v.Name] = true
	}
	collect := func(stmts []ir.Stmt) {
		_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
			switch n := s.(type) {
			case *ir.LocalVar:
				out[n.Name] = true
			case *ir.NodeInst:
				if n.ID != "" {
					out[n.ID] = true
				}
			}
			return nil
		})
	}
	collect(body)
	for _, fn := range funcs {
		if fn != nil {
			collect(fn.Block)
		}
	}
	return out
}

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
	// Same reason as the func fast-path: TypeHintToGo would title-case this
	// into `Chan bool`. A channel's zero is nil, and `make` is what produces a
	// usable one.
	if strings.HasPrefix(hint, "chan ") {
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
