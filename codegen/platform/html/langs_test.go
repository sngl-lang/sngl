package html

import (
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

// htmlLangs is what a check of this platform needs registered.
//
// html.sngl declares setInterval and clearInterval with #[js.native], so it
// imports sngl:language/js and the import resolves only against a registered
// JavaScript translator -- whatever language the build is *for*, since the page
// this platform writes carries script either way. A real build passes every
// registered language (internal/build.Check), and a helper here that passed
// none reported html.sngl's own import as an unknown language.
func htmlLangs(extra ...ir.Language) []ir.Language {
	return append([]ir.Language{&javascript.Translator{}}, extra...)
}
