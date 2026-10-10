// Package i18n contains shared i18n-call detection used by every codegen
// backend. The set of i18n entry points is defined once in
// ir.I18nIntrinsics and lib/i18n.sngl; this package presents a single
// query surface so language and platform code never duplicate the list.
package i18n

import (
	"duckfam.us/sngl/ir"
)

// IsCall reports whether c targets an i18n stdlib entry point.
func IsCall(c *ir.Call) bool { return ir.IsI18nCall(c) }

// PluralKeyConstString returns the quoted CLDR category a predeclared
// PluralKey select denotes, or "" if sel is not one. The JS and Kotlin
// runtimes key plural forms by string category; Go emits the runtime's own
// i18n.Plural* constants instead and does not use this.
func PluralKeyConstString(sel *ir.Select) string {
	if !ir.IsI18nPluralKey(sel) {
		return ""
	}
	return `"` + sel.Field + `"`
}
