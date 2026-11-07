package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// dateFieldKt is SnglDateField's source, scrollKt SnglScroll's, pagerKt
// SnglPager's, gridKt SnglGrid's, choosesKt snglChooses's, flexShareKt snglFlexShare's,
// flexibleKt snglFlexible's and snglFlexed's, and appbarKt SnglAppBar's with
// the menu's composables.
//
//go:embed datefield.kt
var dateFieldKt string

//go:embed scroll.kt
var scrollKt string

//go:embed pager.kt
var pagerKt string

//go:embed grid.kt
var gridKt string

//go:embed chooses.kt
var choosesKt string

//go:embed appbar.kt
var appbarKt string

//go:embed flexshare.kt
var flexShareKt string

//go:embed flexible.kt
var flexibleKt string

// keysKt is SnglWindowKeys's, with what names a key and what presses one
// for a test, and focusKt snglUnfocusable's.
//
//go:embed keys.kt
var keysKt string

//go:embed focus.kt
var focusKt string

// shippedComposables are the Kotlin the platform writes beside MainScreen.kt
// for a program that uses it: a composable by the intrinsic id
// lib/platform/android declares it under, or a function by the native name a
// call of it emits. A composable here keeps state an effect or a dialog of
// Compose's reads -- a field's half-typed text, how far a scroll has gone --
// and a function says what SNGL has no syntax for.
var shippedComposables = []struct {
	intrinsic, call, name, src string
}{
	{intrinsic: intrinsicNS + "SnglDateField", name: "SnglDateField", src: dateFieldKt},
	{intrinsic: intrinsicNS + "SnglScroll", name: "SnglScroll", src: scrollKt},
	{intrinsic: intrinsicNS + "SnglPager", name: "SnglPager", src: pagerKt},
	{intrinsic: intrinsicNS + "SnglGrid", name: "SnglGrid", src: gridKt},
	{call: "snglChooses", name: "SnglChooses", src: choosesKt},
	{intrinsic: intrinsicNS + "Screen", name: "SnglAppBar", src: appbarKt},
	{call: flexShareName, name: "SnglFlexShare", src: flexShareKt},
	{intrinsic: intrinsicNS + "SnglScroll", call: "snglFlexible", name: "SnglFlexible", src: flexibleKt},
	{intrinsic: windowKeys, name: "SnglWindowKeys", src: keysKt},
	{call: "snglUnfocusable", name: "SnglUnfocusable", src: focusKt},
}

// windowKeys is what shippedUses reports rendered for a Screen that hears
// keys: one with a `@key` handler, or one a test presses a key in.
const windowKeys = intrinsicNS + "Screen.keys"

// flexShareName is the shipped default of sngl:ui's flexShare, and
// localFlexShare what a Column the generated code writes provides it through.
const (
	flexShareName  = "snglFlexShare"
	localFlexShare = "LocalFlexShare"
)

// emitShippedComposables writes each shipped composable the program renders
// into the package MainScreen.kt is in, beside it.
func emitShippedComposables(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	rendered, called := shippedUses(req.Pkg)
	for _, c := range shippedComposables {
		if !rendered[c.intrinsic] && !called[c.call] {
			continue
		}
		src := strings.Replace(c.src, "package PACKAGE", "package "+pkg, 1)
		if err := writeAndroidFile(sink, path.Join(path.Dir(mainScreen), c.name+".kt"), []byte(src)); err != nil {
			return err
		}
	}
	return nil
}

// shippedUses is the intrinsics pkg renders and the Kotlin natives it calls or
// reads, by which the shipped composables it needs are known.
func shippedUses(pkg *ir.Package) (rendered, called map[string]bool) {
	rendered, called = map[string]bool{}, map[string]bool{}
	var triggered map[string]bool
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch n := n.(type) {
		case *ir.NodeInst:
			if n.Component != nil && n.Component.Intrinsic == intrinsicNS+"Screen" {
				if triggered == nil {
					triggered = codegen.TriggeredEvents(pkg)
				}
				if screenHearsKeys(n) || n.ID != "" && triggered[n.ID+".key"] {
					rendered[windowKeys] = true
				}
			}
			// A Screen is every program's window; only one drawing its app
			// bar needs SnglAppBar.
			if n.Component != nil && n.Component.Intrinsic != "" && (n.Component.Intrinsic != intrinsicNS+"Screen" || screenDrawsBar(n)) {
				rendered[n.Component.Intrinsic] = true
			}
		case *ir.Call:
			if n.Func != nil && n.Func.Foreign.Scheme == "kotlin" && n.Func.Foreign.Name != "" {
				called[n.Func.Foreign.Name] = true
			}
		case *ir.Ident:
			if v, ok := n.Sym.(*ir.Var); ok && v.Foreign.Scheme == "kotlin" && v.Foreign.Name != "" {
				called[v.Foreign.Name] = true
			}
		}
		return nil
	})
	return rendered, called
}
