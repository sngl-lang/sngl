package fyne

import (
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// fyneBlueprint is the platform-driven render directive for one stdlib component,
// extracted at platform load from fyne.sngl. The renderer consumes this
// instead of branching on component name.
type fyneBlueprint struct {
	Constructor *ctorMeta
	Bindings    []bindMeta
	// CondProp wraps the constructor in `if <prop> { ... }`. Used by modal,
	// drawer, etc. where rendering is gated by an open flag.
	CondProp string
	// FieldPrefix names the per-widget field on Model (e.g. "label", "btn",
	// "entry"). Lets goldens stay stable across the refactor.
	FieldPrefix string
	// UpdaterPrefix names the per-widget reactive updater (e.g. "updateLabel").
	UpdaterPrefix string
	// Transient skips Model storage: the widget is created and assigned to the
	// caller's resultVar directly. Used by containers (vbox/hbox), image, and
	// spacer where there is no reactive update path.
	Transient bool
}

type ctorMeta struct {
	GoFn    string
	GoType  string
	Args    []ctorArg
	Prelude []string
	// Imports is the set of Go import paths this constructor (and any
	// prelude/raw args) requires. The renderer accumulates these across
	// rendered components and the template emits the union.
	Imports []string
	// Switches let a constructor pick a different goFn based on a literal prop
	// value at codegen time (e.g. input(type="password") → widget.NewPasswordEntry).
	// First match wins; falls back to GoFn.
	Switches []ctorSwitch
	// ZeroArgs is the Go argument list used when constructing the widget
	// at slot time (before real prop values are available). E.g. `""` for
	// label/text, `"", nil` for button. Empty string means no args.
	ZeroArgs string
}

type ctorSwitch struct {
	Prop  string
	Value string
	GoFn  string
}

// ctorArg is one argument slot in the constructor call. Exactly one of
// Prop / Event / Raw is populated. Use `${children}` inside a Raw value to
// splice in the children-slice variable.
type ctorArg struct {
	Prop      string // sngl prop name to read off the user's NodeInst
	Transform string // optional Go transform applied to the prop value
	Event     string // sngl event name (e.g. "click") emitted as a closure
	EventSig  string // closure signature, e.g. "func()", "func(s string)"
	Raw       string // raw Go expression with `${prop}` / `${id}` / `${children}` substitution
}

type bindKind int

const (
	bindInit bindKind = iota
	bindReactive
	bindEvent
)

type bindMeta struct {
	Kind      bindKind
	Prop      string
	Target    string
	Transform string
	Quoted    bool
	Signature string
	BindParam string
	// SyncTarget is the data-setter method (e.g. ".SetText") used when this
	// Event's handler does a `var = bindParam` two-way bind. Recorded onto
	// vc.entrySync so SetVar() emits `m.<field><syncTarget>(v)` to keep the
	// widget in sync with externally-driven state changes.
	SyncTarget string
}

var (
	blueprintsOnce  sync.Once
	blueprintByName map[string]*fyneBlueprint
)

func loadBlueprints() map[string]*fyneBlueprint {
	blueprintsOnce.Do(func() {
		blueprintByName = make(map[string]*fyneBlueprint)
		for _, doc := range pkgDocs {
			for _, stmt := range doc.Stmts {
				cd, ok := stmt.(*ast.ComponentDecl)
				if !ok {
					continue
				}
				if m := buildBlueprintFromComponent(cd); m != nil {
					name := strings.TrimPrefix(cd.Name, "sngl.")
					blueprintByName[name] = m
				}
			}
		}
		// Zero-arg defaults keyed by constructor GoFn so every component
		// that lowers to the same fyne ctor gets a buildable arg list at
		// slot time (before real prop values are available). Keying by
		// component name would miss aliases like avatar/chip/divider that
		// all reduce to widget.NewLabel.
		zeroArgsByGoFn := map[string]string{
			"widget.NewLabel":          `""`,
			"widget.NewButton":         `"", nil`,
			"widget.NewCheck":          `"", nil`,
			"widget.NewHyperlink":      `"", nil`,
			"widget.NewSelect":         "nil, nil",
			"widget.NewEntry":          "",
			"widget.NewPasswordEntry":  "",
			"widget.NewMultiLineEntry": "",
			"canvas.NewImageFromURI":   `nil`,
			"canvas.NewImageFromFile":  `""`,
			"container.NewVScroll":     `nil`,
			"container.NewHScroll":     `nil`,
			"container.NewScroll":      `nil`,
		}
		for _, bp := range blueprintByName {
			if bp.Constructor == nil || bp.Constructor.ZeroArgs != "" {
				continue
			}
			if args, ok := zeroArgsByGoFn[bp.Constructor.GoFn]; ok {
				bp.Constructor.ZeroArgs = args
			}
		}
	})
	return blueprintByName
}

func buildBlueprintFromComponent(cd *ast.ComponentDecl) *fyneBlueprint {
	stmts := cd.Body.Stmts
	// Extension form: unwrap the `platform fyne { ... }` block.
	if len(stmts) > 0 {
		if pl, ok := stmts[0].(*ast.PlatformStmt); ok && pl.Platform == "fyne" {
			stmts = pl.Body.Stmts
		}
	}
	if len(stmts) == 0 {
		return nil
	}
	meta := &fyneBlueprint{}
	// Modal-style: `if <ident> { VisualNode(...) }` wraps the ctor.
	if ifStmt, ok := stmts[0].(*ast.IfStmt); ok {
		if id, ok := ifStmt.Cond.(*ast.IdentExpr); ok {
			meta.CondProp = id.Name
		}
		stmts = ifStmt.Body.Stmts
		if len(stmts) == 0 {
			return nil
		}
	}
	vn, ok := stmts[0].(*ast.VisualNode)
	if !ok {
		return nil
	}
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok {
			continue
		}
		switch arg.Name {
		case "constructor":
			meta.Constructor = parseConstructor(arg.Value)
		case "bindings":
			meta.Bindings = parseBindings(arg.Value)
		case "fieldPrefix":
			meta.FieldPrefix = stringValue(arg.Value)
		case "updaterPrefix":
			meta.UpdaterPrefix = stringValue(arg.Value)
		case "condProp":
			meta.CondProp = stringValue(arg.Value)
		case "transient":
			meta.Transient = boolValue(arg.Value)
		}
	}
	if meta.Constructor == nil {
		return nil
	}
	return meta
}

func parseConstructor(e ast.Expr) *ctorMeta {
	se, ok := e.(*ast.StructExpr)
	if !ok {
		return nil
	}
	c := &ctorMeta{}
	for _, f := range se.Fields {
		switch f.Name {
		case "goFn":
			c.GoFn = stringValue(f.Value)
		case "goType":
			c.GoType = stringValue(f.Value)
		case "imports":
			c.Imports = parseStringList(f.Value)
		case "args":
			c.Args = parseCtorArgs(f.Value)
		case "prelude":
			c.Prelude = parseStringList(f.Value)
		case "switches":
			c.Switches = parseSwitches(f.Value)
		}
	}
	return c
}

func parseSwitches(e ast.Expr) []ctorSwitch {
	le, ok := e.(*ast.ListExpr)
	if !ok {
		return nil
	}
	var out []ctorSwitch
	for _, el := range le.Elements {
		se, ok := el.(*ast.StructExpr)
		if !ok || se.Name != "Switch" {
			continue
		}
		var s ctorSwitch
		for _, f := range se.Fields {
			switch f.Name {
			case "prop":
				s.Prop = stringValue(f.Value)
			case "value":
				s.Value = stringValue(f.Value)
			case "goFn":
				s.GoFn = stringValue(f.Value)
			}
		}
		if s.Prop != "" && s.GoFn != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseCtorArgs(e ast.Expr) []ctorArg {
	le, ok := e.(*ast.ListExpr)
	if !ok {
		return nil
	}
	out := make([]ctorArg, 0, len(le.Elements))
	for _, el := range le.Elements {
		out = append(out, parseCtorArg(el))
	}
	return out
}

func parseCtorArg(e ast.Expr) ctorArg {
	if se, ok := e.(*ast.StructExpr); ok {
		var arg ctorArg
		switch se.Name {
		case "Prop":
			for _, f := range se.Fields {
				switch f.Name {
				case "name":
					arg.Prop = stringValue(f.Value)
				case "transform":
					arg.Transform = stringValue(f.Value)
				}
			}
		case "Event":
			for _, f := range se.Fields {
				switch f.Name {
				case "name":
					arg.Event = stringValue(f.Value)
				case "signature":
					arg.EventSig = stringValue(f.Value)
				}
			}
		case "Raw":
			for _, f := range se.Fields {
				if f.Name == "value" {
					arg.Raw = stringValue(f.Value)
				}
			}
		}
		return arg
	}
	if s, ok := codegen.ExprLiteralString(e); ok {
		return ctorArg{Raw: s}
	}
	return ctorArg{}
}

func parseBindings(e ast.Expr) []bindMeta {
	le, ok := e.(*ast.ListExpr)
	if !ok {
		return nil
	}
	out := make([]bindMeta, 0, len(le.Elements))
	for _, el := range le.Elements {
		se, ok := el.(*ast.StructExpr)
		if !ok {
			continue
		}
		var b bindMeta
		switch se.Name {
		case "Init":
			b.Kind = bindInit
		case "Reactive":
			b.Kind = bindReactive
		case "Event":
			b.Kind = bindEvent
		default:
			continue
		}
		for _, f := range se.Fields {
			switch f.Name {
			case "prop":
				b.Prop = stringValue(f.Value)
			case "target":
				b.Target = stringValue(f.Value)
			case "transform":
				b.Transform = stringValue(f.Value)
			case "quoted":
				b.Quoted = boolValue(f.Value)
			case "signature":
				b.Signature = stringValue(f.Value)
			case "bindParam":
				b.BindParam = stringValue(f.Value)
			case "syncTarget":
				b.SyncTarget = stringValue(f.Value)
			}
		}
		out = append(out, b)
	}
	return out
}

func parseStringList(e ast.Expr) []string {
	le, ok := e.(*ast.ListExpr)
	if !ok {
		return nil
	}
	var out []string
	for _, el := range le.Elements {
		if s, ok := codegen.ExprLiteralString(el); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringValue(e ast.Expr) string {
	if s, ok := codegen.ExprLiteralString(e); ok {
		return s
	}
	return ""
}

func boolValue(e ast.Expr) bool {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return false
	}
	if lit.Kind != ast.LiteralBool {
		return false
	}
	return lit.Raw == "true"
}
