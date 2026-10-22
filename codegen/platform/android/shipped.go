package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// dateFieldKt is SnglDateField's source, selectKt SnglSelect's and scrollKt
// SnglScroll's.
//
//go:embed datefield.kt
var dateFieldKt string

//go:embed select.kt
var selectKt string

//go:embed scroll.kt
var scrollKt string

// shippedComposables are the composables the platform writes beside
// MainScreen.kt for a program that renders them, by the intrinsic id
// lib/platform/android declares each under. Each holds state its render
// reads -- a field's half-typed text, whether a dialog or a menu is open, how
// far a scroll has gone -- which a const override has nowhere to hold, so it
// is Kotlin and the override renders it.
var shippedComposables = []struct {
	intrinsic, name, src string
}{
	{intrinsicNS + "SnglDateField", "SnglDateField", dateFieldKt},
	{intrinsicNS + "SnglSelect", "SnglSelect", selectKt},
	{intrinsicNS + "SnglScroll", "SnglScroll", scrollKt},
}

// emitShippedComposables writes each shipped composable the program renders
// into the package MainScreen.kt is in, beside it.
func emitShippedComposables(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	rendered := map[string]bool{}
	_ = ir.Walk(req.Pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.Component != nil && ni.Component.Intrinsic != "" {
			rendered[ni.Component.Intrinsic] = true
		}
		return nil
	})
	for _, c := range shippedComposables {
		if !rendered[c.intrinsic] {
			continue
		}
		src := strings.Replace(c.src, "package PACKAGE", "package "+pkg, 1)
		if err := writeAndroidFile(sink, path.Join(path.Dir(mainScreen), c.name+".kt"), []byte(src)); err != nil {
			return err
		}
	}
	return nil
}
