package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// dateFieldKt is SnglDateField's source, scrollKt SnglScroll's and choosesKt
// snglChooses's.
//
//go:embed datefield.kt
var dateFieldKt string

//go:embed scroll.kt
var scrollKt string

//go:embed chooses.kt
var choosesKt string

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
	{call: "snglChooses", name: "SnglChooses", src: choosesKt},
}

// emitShippedComposables writes each shipped composable the program renders
// into the package MainScreen.kt is in, beside it.
func emitShippedComposables(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	rendered, called := map[string]bool{}, map[string]bool{}
	_ = ir.Walk(req.Pkg, func(n ir.Node) error {
		switch n := n.(type) {
		case *ir.NodeInst:
			if n.Component != nil && n.Component.Intrinsic != "" {
				rendered[n.Component.Intrinsic] = true
			}
		case *ir.Call:
			if n.Func != nil && n.Func.Foreign.Scheme == "kotlin" && n.Func.Foreign.Name != "" {
				called[n.Func.Foreign.Name] = true
			}
		}
		return nil
	})
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
