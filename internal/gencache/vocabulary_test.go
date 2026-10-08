package gencache_test

import (
	"os"
	"path/filepath"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/gencache"
	"duckfam.us/sngl/internal/parser"
)

// Every kind of input the store writes is a member of sngl:x/gen/cache, so a
// stored file checks as the SNGL it is. The store's own check never goes
// through the checker, which is how `godir` was written and re-checked for a
// while with no declaration behind it; this renders one input of every kind
// the store has a constructor for and checks the result.
func TestEveryInputKindIsDeclared(t *testing.T) {
	t.Setenv("SNGL_VOCABULARY_TEST_SET", "v")
	tmp := t.TempDir()
	file := filepath.Join(tmp, "f")
	os.WriteFile(file, []byte("x"), 0o644)
	store := gencache.Open(t.TempDir())
	must := func(in gencache.Input, err error) gencache.Input {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	goenv, err := store.GoEnv(tmp, "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	gencache.Register("test.vocabulary", func(*gencache.Store, []string) (gencache.Output, error) {
		return gencache.Output{Body: []byte("const v = 1\n")}, nil
	})
	entry, _, err := store.Entry(gencache.Request{Producer: "test.vocabulary"})
	if err != nil {
		t.Fatal(err)
	}
	gencache.RegisterSetting("test.vocabulary", func() string { return "v" })
	inputs := append([]gencache.Input{
		must(gencache.Setting("test.vocabulary")),
		must(gencache.File(file)),
		gencache.Absent(filepath.Join(tmp, "missing")),
		must(gencache.Dir(tmp)),
		must(gencache.GoDir(tmp)),
		gencache.Env("SNGL_VOCABULARY_TEST_SET"),
		gencache.Env("SNGL_VOCABULARY_TEST_UNSET"),
		entry,
	}, goenv...)

	data := gencache.Render(gencache.Request{Producer: "test.vocabulary"}, gencache.Output{Inputs: inputs, Body: []byte("const v = 1\n")})
	doc, err := parser.Parse("stored.sngl", data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	_, diags := checker.Check(doc, &checker.Config{})
	for _, d := range diags {
		t.Errorf("%s: %s", d.Pos, d.Msg)
	}
	if t.Failed() {
		t.Logf("stored file:\n%s", data)
	}
}
