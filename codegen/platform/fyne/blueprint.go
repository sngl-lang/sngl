package fyne

import (
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// fyneBlueprint is the platform-driven render directive for one stdlib component,
// extracted at platform load from fyne.sngl. The renderer consumes this
// instead of branching on component name.
type fyneBlueprint struct {
	Constructor *ctorMeta
	Bindings    []bindMeta
}

type ctorMeta struct {
	GoFn   string
	GoType string
	// Imports is the set of Go import paths this constructor requires. The
	// renderer accumulates these across rendered components and the template
	// emits the union.
	Imports []string
	// ZeroArgs is the Go argument list used when constructing the widget
	// at slot time (before real prop values are available). E.g. `""` for
	// label/text, `"", nil` for button. Empty string means no args.
	ZeroArgs string
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
	Signature string
	BindParam string
	// BindProp is the bidi prop name whose `:prop=var` two-way binding this
	// Event services (e.g. "value" for an Entry's OnChanged). The prop-binding
	// lower pass names the synthesized writeback handler after the prop, not
	// the DOM event, so handler lookups match on BindProp as well as Prop.
	BindProp string
}

// matchesEvent reports whether b is the bindEvent that services the given
// sngl event name — either its own DOM event (Prop) or the bidi prop whose
// two-way binding it backs (BindProp).
func (b bindMeta) matchesEvent(event string) bool {
	return b.Kind == bindEvent && (b.Prop == event || (b.BindProp != "" && b.BindProp == event))
}

var (
	blueprintsOnce  sync.Once
	blueprintByName map[string]*fyneBlueprint
)

func loadBlueprints() map[string]*fyneBlueprint {
	blueprintsOnce.Do(func() {
		blueprintByName = make(map[string]*fyneBlueprint)
		for _, doc := range checker.PackageDocsFor("platforms/fyne") {
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
		case "init":
			meta.Bindings = append(meta.Bindings, parseBindings(bindInit, arg.Value)...)
		case "reactive":
			meta.Bindings = append(meta.Bindings, parseBindings(bindReactive, arg.Value)...)
		case "events":
			meta.Bindings = append(meta.Bindings, parseBindings(bindEvent, arg.Value)...)
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
		}
	}
	return c
}

func parseBindings(kind bindKind, e ast.Expr) []bindMeta {
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
		b := bindMeta{Kind: kind}
		for _, f := range se.Fields {
			switch f.Name {
			case "prop":
				b.Prop = stringValue(f.Value)
			case "target":
				b.Target = stringValue(f.Value)
			case "transform":
				b.Transform = stringValue(f.Value)
			case "signature":
				b.Signature = stringValue(f.Value)
			case "bindParam":
				b.BindParam = stringValue(f.Value)
			case "bindProp":
				b.BindProp = stringValue(f.Value)
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
