// Package i18n contains shared i18n-call detection used by every codegen
// backend. The set of i18n entry points is defined once in
// ir.I18nIntrinsics and lib/i18n.sngl; this package presents a single
// query surface so language and platform code never duplicate the list.
package i18n

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// IsCall reports whether c targets an i18n stdlib entry point.
//
// Two shapes match:
//   - pre-inlining: c.Func.Receiver == "i18n" (the stdlib wrapper, e.g.
//     i18n.tr / i18n.numberInt). The plural/select wrappers live on the
//     same receiver.
//   - post-inlining: c.Func.Intrinsic is one of the i18n intrinsics from
//     ir.I18nIntrinsics. NoContext + InlinePure replaces the wrapper
//     call with a direct intrinsic call, so detection at this layer
//     covers post-lowering IR.
//
// Either match returns true so downstream "package uses i18n" checks
// stay correct regardless of which lowering passes have run.
func IsCall(c *ir.Call) bool {
	if c == nil || c.Func == nil {
		return false
	}
	if c.Func.Receiver == "i18n" {
		return true
	}
	return IsIntrinsic(c.Func.Intrinsic)
}

// IsIntrinsic reports whether name matches one of ir.I18nIntrinsics.
// Empty name returns false (most user-written funcs carry no Intrinsic
// tag).
func IsIntrinsic(name string) bool {
	if name == "" {
		return false
	}
	for i := range ir.I18nIntrinsics {
		if ir.I18nIntrinsics[i].Name == name {
			return true
		}
	}
	return false
}
