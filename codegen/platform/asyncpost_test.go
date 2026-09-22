package platform_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
	"git.duckfam.us/jonathan/sngl/internal/lower"
)

// AsyncPost is optional in two places, the way InsertBefore is: a platform
// declares Features.AsyncPost so passAsyncOffload will rewrite a blocking call
// rather than refuse the program, and registers an emitter for
// lower.AsyncPostIntrinsic so something answers the call it emits.
//
// Either alone is a defect nothing else catches. The capability without the
// emitter emits `async.post(func(){…})` and falls through to the generic call
// path, which writes a call to a function that does not exist -- so a program
// that compiled before stops compiling, and only at `go build` on the output.
// The emitter without the capability is dead code the lowering never reaches.
func TestEveryPlatformPairsAsyncPost(t *testing.T) {
	declares := func(gen codegen.PlatformGenerator) bool {
		for _, lang := range codegen.Langs() {
			if lt := codegen.LookupLang(lang); lt != nil && codegen.CapsOrNone(lt.LanguageIdentifier(), gen.PlatformIdentifier()).AsyncPost {
				return true
			}
		}
		return false
	}
	for _, name := range codegen.Platforms() {
		gen := codegen.LookupPlatform(name)
		if gen == nil {
			t.Errorf("%s: registered but not resolvable", name)
			continue
		}
		emits := codegen.LookupPlatformIntrinsic(name, lower.AsyncPostIntrinsic) != nil
		switch {
		case declares(gen) && !emits:
			t.Errorf("%s declares Features.AsyncPost but registers no emitter for %q; a blocking call would compile to a call to nothing", name, lower.AsyncPostIntrinsic)
		case emits && !declares(gen):
			t.Errorf("%s registers an emitter for %q but declares Features.AsyncPost under no language, so lowering never emits the call", name, lower.AsyncPostIntrinsic)
		}
	}
}
