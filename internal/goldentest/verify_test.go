package goldentest

import "testing"

func TestDigestIsSensitiveToNamesAndContent(t *testing.T) {
	base := map[string][]byte{"a.go": []byte("package main\n")}
	same := map[string][]byte{"a.go": []byte("package main\n")}
	renamed := map[string][]byte{"b.go": []byte("package main\n")}
	edited := map[string][]byte{"a.go": []byte("package main\n\n")}

	if digest(base) != digest(same) {
		t.Error("identical file sets digest differently")
	}
	// Two targets whose files differ only in where they were written are two
	// different programs, so the name is hashed beside the content.
	if digest(base) == digest(renamed) {
		t.Error("a renamed file digests the same")
	}
	if digest(base) == digest(edited) {
		t.Error("an edited file digests the same")
	}
}

// A concatenation-only digest cannot tell one boundary from another: two files
// split differently over the same bytes would agree. The length is in the hash
// to stop that.
func TestDigestSeparatesFileBoundaries(t *testing.T) {
	a := map[string][]byte{"f": []byte("ab"), "g": []byte("c")}
	b := map[string][]byte{"f": []byte("a"), "g": []byte("bc")}
	if digest(a) == digest(b) {
		t.Error("two splittings of the same bytes digest alike")
	}
}

func TestParseRecordRejectsWhatItCannotTrust(t *testing.T) {
	for _, bad := range []string{"", "pass\n", "sha256:abc\n", "skipped sha256:abc\n", "pass sha256:abc extra\n"} {
		if _, err := parseRecord("run/go/bubbletea", []byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	r, err := parseRecord("run/go/bubbletea", []byte("pass sha256:abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.digest != "sha256:abc" || r.status != "pass" {
		t.Errorf("digest = %q", r.digest)
	}
	if got := r.String(); got != "pass sha256:abc\n" {
		t.Errorf("round trip = %q", got)
	}
}

func TestTargetsOfRegroupsByTarget(t *testing.T) {
	got := targetsOf(map[string][]byte{
		"out/go/bubbletea/model.go":     []byte("m"),
		"out/go/bubbletea/sub/extra.go": []byte("e"),
		"out/none/html/index.html":      []byte("h"),
	})
	if len(got) != 2 {
		t.Fatalf("got %d targets, want 2", len(got))
	}
	// Sorted, so a record's position in the archive does not depend on map order.
	if got[0].lang != "go" || got[0].platform != "bubbletea" {
		t.Errorf("first target = %s/%s", got[0].lang, got[0].platform)
	}
	// The out/<lang>/<platform>/ prefix is this harness's filing, not part of
	// the program, so what reaches a compiler is relative to the target.
	if _, ok := got[0].files["model.go"]; !ok {
		t.Errorf("files keyed by %v, want model.go", sortedKeys(got[0].files))
	}
	if _, ok := got[0].files["sub/extra.go"]; !ok {
		t.Error("a nested file lost its subdirectory")
	}
	if got[0].recordName() != "run/go/bubbletea" {
		t.Errorf("recordName = %q", got[0].recordName())
	}
}
