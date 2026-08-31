package fyne

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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

// snglThemeType is the theme every generated theme value is an instance of. It
// embeds the app's own theme, so a property this platform does not set is
// still whatever the app would have used.
//
// The button radius moved theme keys in Fyne 2.8 (SizeNameInputRadius ->
// SizeNameButtonRadius); the untyped string answers both, and the constant
// that exists in the built-against version answers with it.
const snglThemeType = `// snglTheme is one set of SNGL style properties as a Fyne theme, for the
// subtree a container.ThemeOverride applies it to.
type snglTheme struct {
	fyne.Theme
	background, foreground color.Color
	textSize, radius       float32
	bold                   bool
}

func (t snglTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch n {
	case theme.ColorNameButton, theme.ColorNameBackground, theme.ColorNameInputBackground:
		if t.background != nil {
			return t.background
		}
	case theme.ColorNameForeground:
		if t.foreground != nil {
			return t.foreground
		}
	}
	return t.Theme.Color(n, v)
}

func (t snglTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		if t.textSize > 0 {
			return t.textSize
		}
	case theme.SizeNameInputRadius, "buttonRadius":
		if t.radius > 0 {
			return t.radius
		}
	}
	return t.Theme.Size(n)
}

func (t snglTheme) Font(s fyne.TextStyle) fyne.Resource {
	if t.bold {
		s.Bold = true
	}
	return t.Theme.Font(s)
}
`

// themeValue is one distinct set of paint properties. Comparable, so the set
// of styles in a program dedupes to the set of themes it needs.
type themeValue struct {
	Background   *fyneColor
	Foreground   *fyneColor
	TextSize     float64
	Radius       float64
	Bold         bool
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
	}
	if st.Background != nil {
		tv.Background, tv.backgroundOK = st.Background, true
	}
	if st.Color != nil {
		tv.Foreground, tv.foregroundOK = st.Color, true
	}
	return tv, tv.backgroundOK || tv.foregroundOK || tv.TextSize > 0 || tv.Radius > 0 || tv.Bold
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
	fmt.Fprintf(&b, "text=%v;radius=%v;bold=%t", t.TextSize, t.Radius, t.Bold)
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

// emitThemeDecls writes the theme type and one var per distinct theme.
func emitThemeDecls(themes []themeValue) string {
	if len(themes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(snglThemeType)
	b.WriteString("\n")
	for i, t := range themes {
		fmt.Fprintf(&b, "var _snglTheme%d = snglTheme{Theme: theme.DefaultTheme()", i)
		if t.backgroundOK {
			fmt.Fprintf(&b, ", background: %s", goColorLit(*t.Background))
		}
		if t.foregroundOK {
			fmt.Fprintf(&b, ", foreground: %s", goColorLit(*t.Foreground))
		}
		if t.TextSize > 0 {
			fmt.Fprintf(&b, ", textSize: %s", float32Lit(t.TextSize))
		}
		if t.Radius > 0 {
			fmt.Fprintf(&b, ", radius: %s", float32Lit(t.Radius))
		}
		if t.Bold {
			b.WriteString(", bold: true")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// themeImports are what the emitted theme declarations reference.
func themeImports() []string {
	return slices.Clone([]string{"image/color", "fyne.io/fyne/v2", "fyne.io/fyne/v2/theme"})
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
