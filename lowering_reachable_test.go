package sngl_test

import (
	"slices"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"

	// Register every language and platform, exactly as cmd/sngl does.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// unrequestedCaps is every lowering pass no registered target asks for.
//
// By pass rather than by capability field, which is what the two records
// becoming one forced and what it should always have said: "requested" used to
// mean a Caps field set true, and under the polarity a capability is asked for
// by being *withheld* while a want or a grant is asked for by being set. Three
// readings of one question. EnabledPasses answers it once.
//
// A pass behind one of these runs in no build, so nothing compiles or executes
// what it emits and the only judge left is ir.Validate, which has opinions
// about shape and none about whether a backend can read the shape. NoTimer sat
// here: passTimer emitted calls to `lower.scheduleTimer`, an intrinsic no
// language registers an emitter for and one the Go backend panics on. Its
// golden fixtures and the validate calibration both passed the whole time.
//
// The six below are the compensating passes for a language less capable than
// the three registered ones, and each still has a caller in a golden fixture.
// Listing them is the point: a seventh name appearing here is a pass that has
// stopped answering for anybody.
var unrequestedCaps = []string{
	"NoToggle",
	"NoLambda",
	"NoRef",
	"NoUnit",
	"NoEnum",
	"NoComputed",
}

func TestEveryLoweringCapIsRequestedBySomeTarget(t *testing.T) {
	requested := map[string]bool{}
	for _, ln := range codegen.Langs() {
		l := codegen.LookupLang(ln)
		if l == nil {
			continue
		}
		for _, pn := range codegen.PlatformsForLang(ln) {
			p := codegen.LookupPlatform(pn)
			if p == nil {
				continue
			}
			for _, name := range lower.EnabledPasses(codegen.CapsOrNone(l.LanguageIdentifier(), p.PlatformIdentifier())) {
				requested[name] = true
			}
		}
	}

	// Every pass that runs for the target claiming nothing but is asked for by
	// no registered pair. The ungated ones run for everybody and so are never
	// in the difference.
	var got []string
	for _, name := range lower.EnabledPasses(lower.Features{}) {
		if !requested[name] {
			got = append(got, name)
		}
	}
	slices.Sort(got)
	want := slices.Clone(unrequestedCaps)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("caps no registered target requests:\n got %v\nwant %v", got, want)
	}
}
