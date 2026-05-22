//go:build keep_test_deps

// This file is never built. Its purpose is to keep `go mod tidy` from
// removing fyne and its transitive deps, which are referenced only as
// string literals in compiler_ir.go but are needed by the integration
// test (intrinsic_integration_test.go) that compiles emitted code via
// `go build` inside the project module.
package fyne

import (
	_ "fyne.io/fyne/v2"
	_ "fyne.io/fyne/v2/app"
	_ "fyne.io/fyne/v2/container"
	_ "fyne.io/fyne/v2/test"
	_ "fyne.io/fyne/v2/widget"
)
