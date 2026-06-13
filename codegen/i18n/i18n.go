// Package i18n contains shared i18n-call detection used by every codegen
// backend. The set of i18n entry points is defined once in
// ir.I18nIntrinsics and lib/i18n.sngl; this package presents a single
// query surface so language and platform code never duplicate the list.
package i18n

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// IsCall reports whether c targets an i18n stdlib entry point. Thin alias for
// ir.IsI18nCall, kept so existing language/platform callers need no change; the
// matching logic lives in ir so the lowering pass can share it.
func IsCall(c *ir.Call) bool { return ir.IsI18nCall(c) }

// IsIntrinsic reports whether name matches one of ir.I18nIntrinsics. Thin alias
// for ir.IsI18nIntrinsic.
func IsIntrinsic(name string) bool { return ir.IsI18nIntrinsic(name) }
