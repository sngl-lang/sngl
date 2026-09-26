package fyne

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The paint half of sngl.Style, in Fyne.
//
// Fyne has no per-widget colour or type: a widget asks the theme, and the
// theme is the app's. What it does have is container.NewThemeOverride, which
// gives one subtree a theme of its own -- so a set of style properties becomes
// a theme, and a node carrying that set is wrapped in an override naming it.
// That is the stylesheet shape: distinct property sets are collected once,
// declared once, and referenced by name.
//
// A theme reaches only what a widget asks it for. `background` becomes the
// button colour and the input background, and a plain container paints no
// background at all, so a box asking for one gets nothing -- Fyne draws
// nothing behind a container to colour.

// ThemeImportPath is the runtime package the generated themes are instances
// of. The type used to be emitted here as a string constant, which meant the
// interpreted host had to carry a second copy of the same forty lines -- and
// two copies of a Fyne contract drift the moment Fyne changes one.
const ThemeImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/fynetheme"

// themeValue is one distinct set of paint properties. Comparable, so the set
// of styles in a program dedupes to the set of themes it needs.
type themeValue struct {
	Background   *fyneColor
	Foreground   *fyneColor
	TextSize     float64
	Radius       float64
	Bold         bool
	Italic       bool
	backgroundOK bool
	foregroundOK bool
}

// themeOf reads the paint properties off a node's style, and reports whether
// any of them was set -- a node that asked for nothing needs no theme and no
// wrapper.
func themeOf(st fyneStyle) (themeValue, bool) {
	tv := themeValue{
		TextSize: st.FontSize,
		Radius:   st.BorderRadius,
		Bold:     st.Bold,
		Italic:   st.Italic,
	}
	if st.Background != nil {
		tv.Background, tv.backgroundOK = st.Background, true
	}
	if st.Color != nil {
		tv.Foreground, tv.foregroundOK = st.Color, true
	}
	return tv, tv.backgroundOK || tv.foregroundOK || tv.TextSize > 0 || tv.Radius > 0 || tv.Bold || tv.Italic
}

// key is the comparable identity of a theme: two nodes styled alike share one.
func (t themeValue) key() string {
	var b strings.Builder
	if t.backgroundOK {
		fmt.Fprintf(&b, "bg=%v;", *t.Background)
	}
	if t.foregroundOK {
		fmt.Fprintf(&b, "fg=%v;", *t.Foreground)
	}
	fmt.Fprintf(&b, "text=%v;radius=%v;bold=%t;italic=%t", t.TextSize, t.Radius, t.Bold, t.Italic)
	return b.String()
}

// assignThemes gives every styled node the name of the theme it uses, and
// returns those themes in declaration order.
//
// Node ids are visited in sorted order so the numbering is the program's
// rather than the map's: a build that reorders its theme declarations run to
// run has no reproducible output.
func assignThemes(specs map[string]*fyneSpec) []themeValue {
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var themes []themeValue
	byKey := map[string]string{}
	for _, id := range ids {
		sp := specs[id]
		if sp.RuntimeTheme {
			continue
		}
		tv, ok := themeOf(sp.Style)
		if !ok {
			continue
		}
		k := tv.key()
		name, seen := byKey[k]
		if !seen {
			name = fmt.Sprintf("_snglTheme%d", len(themes))
			byKey[k] = name
			themes = append(themes, tv)
		}
		sp.ThemeVar = name
	}
	return themes
}

// emitThemeDecls writes one var per distinct theme.
func emitThemeDecls(themes []themeValue) string {
	if len(themes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("\n")
	for i, t := range themes {
		fmt.Fprintf(&b, "var _snglTheme%d = %s.Theme{Theme: theme.DefaultTheme()", i, themeAlias)
		if t.backgroundOK {
			fmt.Fprintf(&b, ", Background: %s", goColorLit(*t.Background))
		}
		if t.foregroundOK {
			fmt.Fprintf(&b, ", Foreground: %s", goColorLit(*t.Foreground))
		}
		if t.TextSize > 0 {
			fmt.Fprintf(&b, ", TextSize: %s", float32Lit(t.TextSize))
		}
		if t.Radius > 0 {
			fmt.Fprintf(&b, ", Radius: %s", float32Lit(t.Radius))
		}
		if t.Bold {
			b.WriteString(", Bold: true")
		}
		if t.Italic {
			b.WriteString(", Italic: true")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// themeAlias is the identifier the runtime theme package is referred to by.
const themeAlias = "fynetheme"

// themeImports are what the emitted theme declarations reference. The type
// itself now comes from a runtime package rather than being written into every
// generated program, so image/color is only needed for the colour literals --
// and only when one of these themes actually writes one. A program whose
// themes name a size, a radius or a face and no colour emitted the import
// anyway, which Go refuses outright; every fixture happened to have a colour
// somewhere until one asked for a slant alone.
func themeImports(themes []themeValue) []string {
	out := []string{"fyne.io/fyne/v2/theme", ThemeImportPath}
	for _, t := range themes {
		if t.backgroundOK || t.foregroundOK {
			return slices.Concat([]string{"image/color"}, out)
		}
	}
	return slices.Clone(out)
}

// themed wraps a styled widget in the container.ThemeOverride carrying its
// theme. Called where the child is appended rather than where it is created:
// an override themes the subtree it is handed, so the subtree has to be
// finished -- and a container is finished only once its children are in.
//
// The Model field still holds the widget itself, so a setter written for a
// reactive prop reaches the widget and not the wrapper.
func (t *fyneTranslator) themed(child ir.Expr, sp *fyneSpec, id string) ir.Expr {
	if sp != nil && sp.RuntimeTheme {
		return t.themeOverrideRef(id)
	}
	if sp == nil || sp.ThemeVar == "" {
		return child
	}
	name := (fyneNative{Path: "fyne.io/fyne/v2/container", Name: "NewThemeOverride"}).qualify(t.gc)
	return nativeCallAt(name, "fyne.io/fyne/v2/container",
		[]ir.Expr{child, rawGoExpr(sp.ThemeVar)}, ir.TypDyn)
}

func goColorLit(c fyneColor) string {
	return fmt.Sprintf("color.NRGBA{R: %d, G: %d, B: %d, A: %d}", c.R, c.G, c.B, c.A)
}

// themeWith names the fynetheme.Theme method each paint field is set through
// when the theme is built at run time.
var themeWith = map[string]string{
	"background":   "WithBackground",
	"color":        "WithForeground",
	"fontSize":     "WithTextSize",
	"borderRadius": "WithRadius",
	"fontWeight":   "WithWeight",
	"fontStyle":    "WithSlant",
}

// readsRuntimePaint reports whether a paint field of a style literal has a
// value the build cannot read, which is what themeOf leaves out.
func readsRuntimePaint(lit *ir.StructLit) bool {
	for _, f := range lit.Fields {
		if f.Value == nil || themeWith[f.Name] == "" {
			continue
		}
		switch f.Name {
		case "background", "color":
			if colorFromExpr(f.Value) == nil {
				return true
			}
		case "fontWeight", "fontStyle":
			if enumOrString(f.Value) == "" {
				return true
			}
		default:
			if _, ok := literalNumber(f.Value); !ok {
				return true
			}
		}
	}
	return false
}

// themeOverrideField is the Model field holding a runtime-themed node's
// override, beside the field holding the node.
func themeOverrideField(id string) string { return id + "__theme" }

func (t *fyneTranslator) themeOverrideRef(id string) ir.Expr {
	name := themeOverrideField(id)
	if t.isLocalRef(id) {
		return localElementRef(name)
	}
	return codegen.RecvFieldRef(t.gc.NodeRecv(id), name)
}

// createThemeOverride wraps a runtime-themed node in its override as soon as
// the node exists, so the style write that follows has something to set. The
// children appended later are themed when the override first renders.
func (t *fyneTranslator) createThemeOverride(id string, node ir.Expr) ir.Stmt {
	path := "fyne.io/fyne/v2/container"
	ctor := nativeCallAt((fyneNative{Path: path, Name: "NewThemeOverride"}).qualify(t.gc), path,
		[]ir.Expr{node, t.baseTheme()}, ir.TypDyn)
	if t.isLocalRef(id) {
		return &ir.LocalVar{Name: themeOverrideField(id), Init: ctor}
	}
	t.fieldSink(themeOverrideField(id), (fyneNative{Path: path, Name: "*ThemeOverride"}).qualify(t.gc))
	return &ir.Assign{Target: t.themeOverrideRef(id), Op: ast.AssignSet, Value: ctor}
}

func (t *fyneTranslator) baseTheme() ir.Expr {
	def := (fyneNative{Path: "fyne.io/fyne/v2/theme", Name: "DefaultTheme"}).qualify(t.gc)
	newTheme := (fyneNative{Path: ThemeImportPath, Name: "New"}).qualify(t.gc)
	return nativeCallAt(newTheme, ThemeImportPath,
		[]ir.Expr{nativeCallAt(def, "fyne.io/fyne/v2/theme", nil, ir.TypDyn)}, ir.TypDyn)
}

// setRuntimeTheme is a write to a runtime-themed node's style: the whole
// theme rebuilt from the literal, static fields included, and the override
// refreshed so the subtree repaints under it.
func (t *fyneTranslator) setRuntimeTheme(id string, lit *ir.StructLit) []ir.Stmt {
	th := t.baseTheme()
	for _, f := range lit.Fields {
		method := themeWith[f.Name]
		if method == "" || f.Value == nil {
			continue
		}
		v := f.Value
		if f.Name == "fontSize" || f.Name == "borderRadius" {
			v = themeLength(v)
		}
		th = methodCall(th, method, []ir.Expr{v}, ir.TypDyn)
	}
	ov := t.themeOverrideRef(id)
	return []ir.Stmt{
		&ir.Assign{Target: &ir.Select{Operand: ov, Field: "Theme", Type: ir.TypDyn}, Op: ast.AssignSet, Value: th},
		&ir.CallStmt{Call: methodCall(ov, "Refresh", nil, ir.TypVoid)},
	}
}

// themeLength is a length as the float64 a theme size takes. A measurement's
// px is what Fyne's device-independent pixels answer to; its other bases have
// no Fyne counterpart, as a literal's suffix has none in themeOf.
func themeLength(v ir.Expr) ir.Expr {
	if n, ok := literalNumber(v); ok {
		return &ir.Literal{Type: ir.TypFloat, Value: strconv.FormatFloat(n, 'f', -1, 64)}
	}
	if vt := v.ExprType(); vt != nil && vt.Kind == ir.TypeUnit {
		v = &ir.Select{Operand: v, Field: "px", Type: ir.TypFloat}
	}
	return &ir.Conversion{Type: ir.TypFloat, Operand: v}
}
