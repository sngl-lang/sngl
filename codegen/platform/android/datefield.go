package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// dateFieldKt is SnglDateField's source, the composable `android:SnglDateField`
// names, which keeps the half-typed text and whether its dialog is open; the
// override renders it.
//
//go:embed datefield.kt
var dateFieldKt string

// dateFieldIntrinsic is the id lib/platform/android declares SnglDateField
// under.
const dateFieldIntrinsic = intrinsicNS + "SnglDateField"

// emitDateFieldFile writes SnglDateField into the package MainScreen.kt is in,
// beside it, when the program renders one.
func emitDateFieldFile(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	if !rendersDateField(req.Pkg) {
		return nil
	}
	src := strings.Replace(dateFieldKt, "package PACKAGE", "package "+pkg, 1)
	return writeAndroidFile(sink, path.Join(path.Dir(mainScreen), "SnglDateField.kt"), []byte(src))
}

// rendersDateField reports whether any node in pkg is a SnglDateField.
func rendersDateField(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && ni.Component != nil && ni.Component.Intrinsic == dateFieldIntrinsic {
			found = true
		}
		return nil
	})
	return found
}
