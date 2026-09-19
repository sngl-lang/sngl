package goldentest

import "testing"

// A deny that cannot assert anything must be an error rather than a pass.
// This is the whole reason the directive is not `! grep`: rsc.io/script's grep
// wants a pattern and a file, one argument is a usage error, and a usage error
// under `!` reads as a satisfied assertion. Six of those had accumulated in
// cmd/sngl/testdata, three of them asserting the absence of the very thing
// their change was about.
//
// So every malformed spelling is listed here, and each one has to fail.
func TestDenyRefusesWhatCannotAssert(t *testing.T) {
	bad := map[string]string{
		"no pattern":            "deny out/go/bubbletea/model.go",
		"no reason":             "deny out/go/bubbletea/model.go `x`",
		"empty pattern":         "deny out/go/bubbletea/model.go `` -- why",
		"unquoted pattern":      "deny out/go/bubbletea/model.go x -- why",
		"unterminated quote":    "deny out/go/bubbletea/model.go `x -- why",
		"bad regex":             "deny out/go/bubbletea/model.go `x(` -- why",
		"not a golden path":     "deny model.go `x` -- why",
		"golden path too short": "deny out/go/model.go `x` -- why",
	}
	for name, line := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := parseDenies(line); err == nil {
				t.Errorf("parsed %d denies with no error from: %s", len(got), line)
			}
		})
	}
}

func TestDenyParses(t *testing.T) {
	denies, err := parseDenies("# deny out/go/bubbletea/model.go `held := \\[\\]int` -- no slice\ndeny * `\\.toList\\(\\)` -- none of them\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(denies) != 2 {
		t.Fatalf("want 2 denies, got %d", len(denies))
	}
	if denies[0].file != "out/go/bubbletea/model.go" || denies[0].reason != "no slice" {
		t.Errorf("first deny: %+v", denies[0])
	}
	if !denies[0].pattern.MatchString("held := []int{}") {
		t.Errorf("pattern %s does not match what it is written to deny", denies[0].pattern)
	}
	if denies[1].file != "*" {
		t.Errorf("second deny: %+v", denies[1])
	}
}

// A word beginning with "deny" is prose, not a directive: the comment above a
// fixture is written for a reader.
func TestDenyIgnoresProse(t *testing.T) {
	denies, err := parseDenies("denying a list is the point.\ndenies nothing here.\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(denies) != 0 {
		t.Fatalf("want 0 denies, got %d: %+v", len(denies), denies)
	}
}

// A deny naming a file no target generates asserts nothing, and reads exactly
// like one that asserts something.
func TestDenyOnAbsentFileFails(t *testing.T) {
	denies, err := parseDenies("deny out/go/bubbletea/nope.go `x` -- why")
	if err != nil {
		t.Fatal(err)
	}
	fake := &testing.T{}
	checkDenies(fake, denies, map[string][]byte{"out/go/bubbletea/model.go": []byte("x")}, nil)
	if !fake.Failed() {
		t.Error("deny naming an ungenerated file passed")
	}
}
