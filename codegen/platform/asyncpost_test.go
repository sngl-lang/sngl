package platform_test

import (
	"slices"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/lower"
)

// AsyncPost is optional in two places, the way InsertBefore is: a platform
// declares Features.AsyncPost so passAsyncOffload will rewrite a blocking call
// rather than refuse the program, and registers an emitter for
// lower.AsyncDoIntrinsic so something answers the call it emits.
//
// Either alone is a defect nothing else catches. The capability without the
// emitter emits `async.do(func(){…})` and falls through to the generic call
// path, which writes a call to a function that does not exist -- so a program
// that compiled before stops compiling, and only at `go build` on the output.
// The emitter without the capability is dead code the lowering never reaches.
//
// A language that answers the call itself -- C, which posts through the
// sngl_post each platform's runtime defines -- answers it for a platform
// declaring the capability under it alone.
func TestEveryPlatformPairsAsyncPost(t *testing.T) {
	// declaring is each language gen builds with under which it declares
	// AsyncPost.
	declaring := func(gen codegen.PlatformGenerator) []string {
		var out []string
		for _, lang := range codegen.Langs() {
			lt := codegen.LookupLang(lang)
			if lt == nil || !slices.Contains(gen.SupportedLangs(), lt.LanguageIdentifier()) {
				continue
			}
			if codegen.CapsOrNone(lt.LanguageIdentifier(), gen.PlatformIdentifier()).AsyncPost {
				out = append(out, lt.LanguageIdentifier())
			}
		}
		return out
	}
	for _, name := range codegen.Platforms() {
		gen := codegen.LookupPlatform(name)
		if gen == nil {
			t.Errorf("%s: registered but not resolvable", name)
			continue
		}
		langs := declaring(gen)
		emits := codegen.LookupPlatformIntrinsic(name, lower.AsyncDoIntrinsic) != nil
		answered := len(langs) > 0
		for _, lang := range langs {
			if !emits && !codegen.LangImplements(lang, lower.AsyncDoIntrinsic) {
				answered = false
			}
		}
		switch {
		case len(langs) > 0 && !answered:
			t.Errorf("%s declares Features.AsyncPost but registers no emitter for %q; a blocking call would compile to a call to nothing", name, lower.AsyncDoIntrinsic)
		case emits && len(langs) == 0:
			t.Errorf("%s registers an emitter for %q but declares Features.AsyncPost under no language, so lowering never emits the call", name, lower.AsyncDoIntrinsic)
		}
	}
}
