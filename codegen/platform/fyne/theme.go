package fyne

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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
func (t *fyneTranslator) themed(child ir.Expr, sp *fyneSpec) ir.Expr {
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
