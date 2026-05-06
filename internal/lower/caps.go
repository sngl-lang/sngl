// Package lower performs capability-driven IR-to-IR transformations between
// the optimizer and codegen. Each lowering pass is gated by a Caps flag:
// passes whose flag is true rewrite high-level constructs into simpler
// primitives that the target platform/language can natively emit.
package lower

import "strings"

// Caps declares which high-level SNGL constructs the target cannot consume
// directly. A flag set to true requests the corresponding lowering pass.
//
// Caps values come from the platform's and language's Capabilities() methods
// and are merged field-wise via OR before lower.Lower runs.
type Caps struct {
	NoToggle        bool // x!! → x = !x
	NoTernary       bool // a ? b : c → if/else stmt with temp var
	NoLambda        bool // closures → top-level funcs + captured-state struct
	NoRef           bool // ref<T> → synthesized one-field reference-semantic struct
	NoUnit          bool // unit values → underlying int
	NoEnum          bool // enum members → int constants
	NoAsyncReactive bool // async in reactive contexts → settled state-field + kicker
	NoComputed      bool // computed vars → inlined exprs or memoized funcs
	NoTimer         bool // timer decls → explicit scheduler.At()/cancel() calls
	NoReactivity    bool // reactive deps → explicit updater stmts after each mutation
	NoDeclarative   bool // visual node tree → flat stream of create/update/delete IR calls
}

// Merge returns the field-wise OR of c and other. Either side disabling a
// feature requests the corresponding lowering pass.
func (c Caps) Merge(other Caps) Caps {
	return Caps{
		NoToggle:        c.NoToggle || other.NoToggle,
		NoTernary:       c.NoTernary || other.NoTernary,
		NoLambda:        c.NoLambda || other.NoLambda,
		NoRef:           c.NoRef || other.NoRef,
		NoUnit:          c.NoUnit || other.NoUnit,
		NoEnum:          c.NoEnum || other.NoEnum,
		NoAsyncReactive: c.NoAsyncReactive || other.NoAsyncReactive,
		NoComputed:      c.NoComputed || other.NoComputed,
		NoTimer:         c.NoTimer || other.NoTimer,
		NoReactivity:    c.NoReactivity || other.NoReactivity,
		NoDeclarative:   c.NoDeclarative || other.NoDeclarative,
	}
}

// String returns a comma-separated list of enabled flags in pass-execution
// order (NoUnit first, NoDeclarative last). Empty string when no flags set.
func (c Caps) String() string {
	var parts []string
	if c.NoUnit {
		parts = append(parts, "NoUnit")
	}
	if c.NoEnum {
		parts = append(parts, "NoEnum")
	}
	if c.NoTernary {
		parts = append(parts, "NoTernary")
	}
	if c.NoAsyncReactive {
		parts = append(parts, "NoAsyncReactive")
	}
	if c.NoComputed {
		parts = append(parts, "NoComputed")
	}
	if c.NoLambda {
		parts = append(parts, "NoLambda")
	}
	if c.NoRef {
		parts = append(parts, "NoRef")
	}
	if c.NoToggle {
		parts = append(parts, "NoToggle")
	}
	if c.NoReactivity {
		parts = append(parts, "NoReactivity")
	}
	if c.NoTimer {
		parts = append(parts, "NoTimer")
	}
	if c.NoDeclarative {
		parts = append(parts, "NoDeclarative")
	}
	return strings.Join(parts, ",")
}
