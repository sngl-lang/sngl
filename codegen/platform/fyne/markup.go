package fyne

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// fynetextPath is the runtime package Fyne's rich text is rendered through.
// See its package comment for what Fyne does not do for itself and why each
// of those is a widget implementation rather than a language gap.
const fynetextPath = "git.duckfam.us/jonathan/sngl/pkg/go/fynetext"

// markupTags are the two primitives whose Spec is built here rather than read
// off a `spec` record.
//
// They are the exception because their construction is not data a declaration
// can carry: a run is constructed with the style it was written with, and
// which fields a `SpanStyle` literal set is a question about the literal. html
// answers the same question in `spanStyleCSS` and bubbletea in its cascade;
// this is the third asking, and the only difference is that the answer is Go
// source for a constructor argument instead of CSS or a lipgloss chain.
func markupTags(tag string) bool { return tag == "Flow" || tag == "Span" }

// markupSpec builds the Spec for one flow or one span.
//
// Everything downstream is the ordinary path: OnCreateNode calls `new` with
// `args`, OnAppendChild calls `Add`, OnPropAssign finds the setter, and the
// Model field is typed `goType`. A span is a value with an Add and a SetText,
// which is all this platform has ever asked a node to be.
func markupSpec(tag string, props map[string]ir.Expr) (*fyneSpec, error) {
	sp := &fyneSpec{
		Setters:   map[string]string{},
		Handlers:  map[string]fyneHandler{},
		CtorProps: props,
		Add:       "Add",
	}
	switch tag {
	case "Flow":
		sp.New = fyneNative{Path: fynetextPath, Name: "NewFlow"}
		sp.GoType = fyneNative{Path: fynetextPath, Name: "*Flow"}
		// A flow's block styling is read the way every node's is, which is
		// what gives a heading its size and a code sample its background.
		sp.Style = styleFromProps(props)
	case "Span":
		sp.New = fyneNative{Path: fynetextPath, Name: "NewSpan"}
		sp.GoType = fyneNative{Path: fynetextPath, Name: "*Span"}
		sp.Args = []fyneArg{{
			Type: fyneNative{Path: fynetextPath, Name: "Style"},
			Raw:  spanStyleChain(props),
		}}
		sp.CtorOnly = map[string]bool{"kind": true, "spanStyle": true}
		sp.Setters["text"] = "SetText"
		sp.Setters["href"] = "SetHref"
		if c, ok := props["color"]; ok && !codegen.SpanStyleKnownColor(c) {
			sp.Setters["color"] = "SetColor"
		} else {
			sp.CtorOnly["color"] = true
		}
	default:
		return nil, fmt.Errorf("fyne: %s is not a markup primitive", tag)
	}
	return sp, nil
}

// spanStyleChain is the builder call that follows `fynetext.Style`, written
// from what this run said about its own words.
//
// A chain and not a struct literal because only the first name needs
// qualifying: the emitting file's alias for the runtime package is not known
// here -- it is the Go context's to assign -- and a chain spends it once.
//
// A field the run left alone contributes nothing, which is the whole of the
// cascade's requirement on this side: "not set" has to reach the runtime as
// silence, or a span inside a bold one un-bolds it. The two enums say that by
// leading with `inherit` and color says it with alpha zero, which is
// `codegen.SpanStyleUnsetColor`.
func spanStyleChain(props map[string]ir.Expr) string {
	var b strings.Builder
	b.WriteString("()")
	if k := enumOrString(props["kind"]); k != "" {
		fmt.Fprintf(&b, ".WithToken(%q)", k)
	}
	if c := colorFromExpr(props["color"]); c != nil && !codegen.SpanStyleUnsetColor(props["color"]) {
		fmt.Fprintf(&b, ".WithRGBA(%d, %d, %d, %d)", c.R, c.G, c.B, c.A)
	}
	sl, ok := props["spanStyle"].(*ir.StructLit)
	if !ok {
		return b.String()
	}
	for _, f := range sl.Fields {
		switch f.Name {
		case "color":
			if codegen.SpanStyleUnsetColor(f.Value) {
				continue
			}
			if c := colorFromExpr(f.Value); c != nil {
				fmt.Fprintf(&b, ".WithRGBA(%d, %d, %d, %d)", c.R, c.G, c.B, c.A)
			}
		case "fontSize":
			if px, ok := literalNumber(f.Value); ok && px > 0 {
				fmt.Fprintf(&b, ".WithSize(%s)", float32Lit(px))
			}
		case "fontWeight":
			switch enumOrString(f.Value) {
			case "bold", "bolder":
				b.WriteString(".WithBold()")
			case "normal", "lighter":
				// Fyne has two faces, so a run asking for anything but bold is
				// asking for the regular one.
				b.WriteString(".NoBold()")
			}
		case "fontStyle":
			switch enumOrString(f.Value) {
			case "italic", "oblique":
				b.WriteString(".WithItalic()")
			case "normal":
				b.WriteString(".NoItalic()")
			}
		case "fontFamily":
			// Fyne's only family distinction is the monospace face, so this
			// is the one value it can answer; anything else is a font this
			// host has no way to load for one run.
			if s, ok := codegen.IRLiteralString(f.Value); ok && s == "monospace" {
				b.WriteString(".WithMono()")
			}
		case "underline":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				b.WriteString(".WithUnderline()")
			}
		case "strike":
			if v, ok := codegen.IRLiteralBool(f.Value); ok && v {
				b.WriteString(".WithStrike()")
			}
		}
	}
	return b.String()
}
