package html

import (
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

// htmlLangs is what a check of this platform needs registered: html.sngl
// declares its setInterval with #[js.native], so sngl:language/js has to
// resolve whatever language the build is for.
func htmlLangs(extra ...ir.Language) []ir.Language {
	return append([]ir.Language{&javascript.Translator{}}, extra...)
}
