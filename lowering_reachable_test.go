package sngl_test

import (
	"reflect"
	"slices"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"

	// Register every language and platform, exactly as cmd/sngl does.
	_ "git.duckfam.us/jonathan/sngl/codegen/lang"
	_ "git.duckfam.us/jonathan/sngl/codegen/platform"
)

// unrequestedCaps is every Caps flag no registered target asks for.
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
			caps := reflect.ValueOf(p.Capabilities(l).ToLowerCaps())
			ct := caps.Type()
			for i := range ct.NumField() {
				if caps.Field(i).Kind() == reflect.Bool && caps.Field(i).Bool() {
					requested[ct.Field(i).Name] = true
				}
			}
		}
	}

	var got []string
	all := reflect.TypeFor[lower.Caps]()
	for f := range all.Fields() {
		if f.Type.Kind() == reflect.Bool && !requested[f.Name] {
			got = append(got, f.Name)
		}
	}
	slices.Sort(got)
	want := slices.Clone(unrequestedCaps)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("caps no registered target requests:\n got %v\nwant %v", got, want)
	}
}
