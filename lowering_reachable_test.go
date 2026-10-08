package sngl_test

import (
	"slices"
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/lower"

	// Register every language and platform, exactly as cmd/sngl does.
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
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

	// Every gated pass no registered pair asks for.
	//
	// The universe is the passes that run for a target claiming nothing *or*
	// asking for everything, less the ones that run for everybody. The zero
	// Features alone is not the universe and reading it as one silently halved
	// this test: a `#[gen.wants]` pass is turned on by being *asked for*, so
	// the zero value can never enable one, and Canvas, CanvasReactivity,
	// FocusOrder, Context and SlotChildInstances could not have appeared here
	// however few targets wanted them.
	var got []string
	for _, name := range gatedPassUniverse() {
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

// gatedPassUniverse is every pass a target can turn on: those the zero
// Features enables (a capability withheld) plus those every `#[gen.wants]`
// enables (a pass asked for), less the ones that run whatever a target says.
//
// Built from the two extremes rather than written out, so a pass added under
// either polarity joins it with nothing to update here -- which is the property
// the list of unrequested names above depends on to mean anything.
func gatedPassUniverse() []string {
	always := map[string]bool{}
	for _, name := range lower.EnabledPasses(lower.NoLowering()) {
		always[name] = true
	}
	wantsAll := lower.NoLowering()
	wantsAll.StructComponents = true
	wantsAll.StdlibContextParam = true
	wantsAll.FocusOrder = true
	wantsAll.Canvas = true
	wantsAll.ReactiveCanvas = true

	seen := map[string]bool{}
	var out []string
	for _, feats := range []lower.Features{{}, wantsAll} {
		for _, name := range lower.EnabledPasses(feats) {
			if always[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
