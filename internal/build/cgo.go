package build

import (
	"fmt"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// The values of the Go language's `cgo` option (sngl:language/go): how a Go
// build calls a `#[c.native]` declaration.
const (
	// CgoWrapper calls the Go wrapper `sngl tool cgogen` marks beside a
	// declaration, and the declaration directly where it has none.
	CgoWrapper = "wrapper"
	// CgoDirect calls every C declaration from the program.
	CgoDirect = "direct"
	// CgoPurego calls C through purego, with no cgo.
	CgoPurego = "purego"
)

// ApplyCgo makes pkg, a build's copy, call C as the target's `cgo` option
// says, which the copy records (ir.Package.CCalls). Under "direct" each later
// phase calls a C declaration as it calls one of a program's own `c:`
// imports, whatever wrapper it has: lowering converts its arguments, the
// program writes `import "C"` and the C its package ships is compiled beside
// it. Under "purego" nothing is converted to C: the program hands purego Go's
// own values.
func ApplyCgo(pkg *ir.Package, target Target) error {
	if target.Lang != "go" {
		return nil
	}
	mode := codegen.OptionString(target.Options, "cgo")
	switch mode {
	case "", CgoWrapper:
		return nil
	case CgoDirect:
		pkg.CCalls = ir.CCallsDirect
		return nil
	case CgoPurego:
		pkg.CCalls = ir.CCallsPurego
		return nil
	}
	return fmt.Errorf("cgo=%q: Go calls C %q, %q or %q", mode, CgoWrapper, CgoDirect, CgoPurego)
}
