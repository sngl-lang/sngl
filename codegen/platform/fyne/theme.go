package fyne

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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
const ThemeImportPath = "duckfam.us/sngl/pkg/go/fynetheme"

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
	var themed ir.Expr = nativeCallAt(name, "fyne.io/fyne/v2/container",
		[]ir.Expr{child, rawGoExpr(sp.ThemeVar, fyneTheme())}, ir.NativeGoPointerOf("container.ThemeOverride"))
	// The override is what the box holds, so it is what asks for a share.
	if sp.shares() {
		themed = shareCall(themed, sp)
	}
	return themed
}

func goColorLit(c fyneColor) string {
	return fmt.Sprintf("color.NRGBA{R: %d, G: %d, B: %d, A: %d}", c.R, c.G, c.B, c.A)
}

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
	ov := ir.NativeGoPointerOf("container.ThemeOverride")
	if t.isLocalRef(id) {
		return &ir.Ident{Name: name, Type: ov}
	}
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(id), name, ov)
}

// createThemeOverride wraps a runtime-themed node in its override as soon as
// the node exists, so the style write that follows has something to set. The
// children appended later are themed when the override first renders.
func (t *fyneTranslator) createThemeOverride(id string, node ir.Expr) ir.Stmt {
	path := "fyne.io/fyne/v2/container"
	ctor := nativeCallAt((fyneNative{Path: path, Name: "NewThemeOverride"}).qualify(t.gc), path,
		[]ir.Expr{node, t.baseTheme()}, ir.NativeGoPointerOf("container.ThemeOverride"))
	if t.isLocalRef(id) {
		return &ir.LocalVar{Name: themeOverrideField(id), Init: ctor}
	}
	t.sinkField(themeOverrideField(id), (fyneNative{Path: path, Name: "*ThemeOverride"}).qualify(t.gc))
	return &ir.Assign{Target: t.themeOverrideRef(id), Op: ast.AssignSet, Value: ctor}
}

func (t *fyneTranslator) baseTheme() ir.Expr {
	def := (fyneNative{Path: "fyne.io/fyne/v2/theme", Name: "DefaultTheme"}).qualify(t.gc)
	newTheme := (fyneNative{Path: ThemeImportPath, Name: "New"}).qualify(t.gc)
	return nativeCallAt(newTheme, ThemeImportPath,
		[]ir.Expr{nativeCallAt(def, "fyne.io/fyne/v2/theme", nil, fyneTheme())}, snglTheme())
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
		th = methodCall(th, method, []ir.Expr{v}, snglTheme())
	}
	ov := t.themeOverrideRef(id)
	return []ir.Stmt{
		&ir.Assign{Target: &ir.Select{Operand: ov, Field: "Theme", Type: fyneTheme()}, Op: ast.AssignSet, Value: th},
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

// styleThemeFunc builds a theme from a whole Style value. A field left unset
// in the value is its zero, which is also what __merge_Style reads as unset,
// so a zero field leaves the default theme's answer in place.
const styleThemeFunc = "__styleTheme"

func (t *fyneTranslator) setRuntimeThemeFrom(id string, style ir.Expr) []ir.Stmt {
	ov := t.themeOverrideRef(id)
	return []ir.Stmt{
		&ir.Assign{Target: &ir.Select{Operand: ov, Field: "Theme", Type: fyneTheme()}, Op: ast.AssignSet,
			Value: nativeCall(styleThemeFunc, []ir.Expr{style}, snglTheme())},
		&ir.CallStmt{Call: methodCall(ov, "Refresh", nil, ir.TypVoid)},
	}
}

// styleValueTheme is the Go declaration of styleThemeFunc, when a node takes
// its style as a whole value; "" otherwise. It is themeWith applied to a
// value, so a field added there belongs here too.
func styleValueTheme(specs map[string]*fyneSpec) string {
	for _, sp := range specs {
		if sp.StyleValue {
			return `
func ` + styleThemeFunc + `(s Style) fynetheme.Theme {
	t := fynetheme.New(theme.DefaultTheme())
	if s.Background != (snglcolor.Color{}) {
		t = t.WithBackground(s.Background)
	}
	if s.Color != (snglcolor.Color{}) {
		t = t.WithForeground(s.Color)
	}
	return t.WithTextSize(s.FontSize.Px).WithRadius(s.BorderRadius.Px).WithWeight(s.FontWeight).WithSlant(s.FontStyle)
}
`
		}
	}
	return ""
}

// fyneTheme is fyne.Theme, what a theme override holds.
func fyneTheme() *ir.Type { return ir.NativeGoNamed("fyne.Theme") }

// snglTheme is fynetheme.Theme, the theme a style builds, which is a
// fyne.Theme.
func snglTheme() *ir.Type { return ir.NativeGoNamed("fynetheme.Theme") }
