package trust

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// Every grant the config file is written with is a member of
// sngl:x/gen/trust, so the file checks as the SNGL it is. ReadConfig never
// goes through the checker; this writes one allow of every kind and checks
// the result, so the reader and the vocabulary cannot drift.
func TestEveryGrantKindIsDeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.sngl")
	grants := []Grant{
		{Kind: Eval, Subject: "dir:/src/docs", Module: "example.com/site"},
		{Kind: Command, Subject: "dir:/src/pc", Prefix: []string{"go", "list"}, BanFlags: []string{"-toolexec"}},
		{Kind: Env, Subject: "dir:/src/pc", Value: "GOOS"},
		{Kind: File, Subject: "dir:/src/pc", Value: "/usr/share/gir-1.0/Gtk-4.0.gir"},
		{Kind: Dir, Subject: "dir:/src/pc", Value: "/usr/share/gir-1.0"},
		{Kind: Eval, Subject: "git://example.com/p@v1", Digest: "abc"},
	}
	if err := AppendConfig(path, grants); err != nil {
		t.Fatal(err)
	}
	allows, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	var back []Grant
	for _, a := range allows {
		back = append(back, a.Grants...)
	}
	if len(back) != len(grants) {
		t.Fatalf("read back %d grants, wrote %d", len(back), len(grants))
	}
	for i, g := range grants {
		b := back[i]
		if b.Kind != g.Kind || b.Subject != g.Subject || b.Module != g.Module || b.Digest != g.Digest || b.Value != g.Value || len(b.Prefix) != len(g.Prefix) || len(b.BanFlags) != len(g.BanFlags) {
			t.Errorf("grant %d: wrote %+v, read %+v", i, g, b)
		}
	}

	data, err := readFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.Parse("trust.sngl", data)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	_, diags := checker.Check(doc, &checker.Config{})
	for _, d := range diags {
		t.Errorf("%s: %s", d.Pos, d.Msg)
	}
	if t.Failed() {
		t.Logf("config:\n%s", data)
	}
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
