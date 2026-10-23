package goldentest

import (
	"bytes"
	"slices"
	"testing"
)

var execSrc = map[string][]byte{"app.sngl": nil, "want.txt": []byte("ok\n"), "in.txt": []byte("click\n")}

func TestExecRefusesWhatCannotRun(t *testing.T) {
	bad := map[string]string{
		"no target":          "exec",
		"not a golden path":  "exec go/gtk4",
		"no platform":        "exec out/go",
		"a file, not a dir":  "exec out/go/gtk4/model.go",
		"stdout not a file":  "exec out/go/gtk4 -- missing.txt",
		"stdout is source":   "exec out/go/gtk4 -- app.sngl",
		"stdin not a file":   "exec out/go/gtk4 <missing.txt",
		"stderr not a file":  "exec out/go/gtk4 2>missing.txt",
		"-- takes one file":  "exec out/go/gtk4 -- want.txt in.txt",
		"-- ends the line":   "exec out/go/gtk4 -- want.txt --flag",
		"no Wayland on fyne": "exec out/go/fyne toplevels=1",
	}
	for name, line := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := parseExecs(line, execSrc); err == nil {
				t.Errorf("parsed %v with no error from: %s", got, line)
			}
		})
	}
}

func TestExecParses(t *testing.T) {
	execs, err := parseExecs("Prose that execs nothing.\n\nexec out/go/gtk4 --window x <in.txt toplevels[Load]=2 toplevels=3 -- want.txt\n", execSrc)
	if err != nil {
		t.Fatal(err)
	}
	got := execs["go/gtk4"]
	if len(execs) != 1 || len(got) != 1 {
		t.Fatalf("want one execution of go/gtk4, got %+v", execs)
	}
	e := got[0]
	if !slices.Equal(e.run.Args, []string{"--window", "x"}) || string(e.run.Stdin) != "click\n" || e.stdout != "want.txt" || !e.run.Wayland {
		t.Errorf("parsed %+v", e)
	}
	if e.toplevels["Load"] != 2 || e.toplevels[""] != 3 || e.line != 3 {
		t.Errorf("parsed %+v", e)
	}

	execs, err = parseExecs("exec out/go/fyne exit=2 2>want.txt", execSrc)
	if err != nil {
		t.Fatal(err)
	}
	if e := execs["go/fyne"][0]; e.exit != 2 || e.stderr != "want.txt" || len(e.run.Args) != 0 {
		t.Errorf("parsed %+v", e)
	}
}

// A target with no execution keeps the digest it had, so adding the directive
// to the harness moved no record.
func TestExecLeavesOtherDigestsAlone(t *testing.T) {
	files := map[string][]byte{"model.go": []byte("package main\n")}
	if execDigest(t, files, nil, execSrc) != digest(files) {
		t.Error("a target with no exec has a different digest")
	}
	execs, err := parseExecs("exec out/go/gtk4 -- want.txt", execSrc)
	if err != nil {
		t.Fatal(err)
	}
	with := execDigest(t, files, execs["go/gtk4"], execSrc)
	if with == digest(files) {
		t.Error("an exec does not move the digest")
	}
	edited := map[string][]byte{"app.sngl": nil, "want.txt": []byte("other\n")}
	if execDigest(t, files, execs["go/gtk4"], edited) == with {
		t.Error("editing the file an exec compares against does not move the digest")
	}
}

// An execution tests the runtime its program imports from the checkout as
// much as the program, so the runtime's source is in the digest.
func TestExecDigestCoversTheRuntime(t *testing.T) {
	execs, err := parseExecs("exec out/go/gtk4", execSrc)
	if err != nil {
		t.Fatal(err)
	}
	program := map[string][]byte{"model.go": []byte("package main\n\nimport _ \"duckfam.us/sngl/pkg/go/gtk4rt\"\n")}
	alone := map[string][]byte{"model.go": []byte("package main\n")}
	withRuntime, err := withExecs(program, execs["go/gtk4"], execSrc)
	if err != nil {
		t.Fatal(err)
	}
	without, err := withExecs(alone, execs["go/gtk4"], execSrc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(withRuntime["\x00exec/modules"], without["\x00exec/modules"]) {
		t.Error("a program importing gtk4rt hashes the same runtime source as one importing nothing")
	}
}

func execDigest(t *testing.T, files map[string][]byte, execs []execution, src map[string][]byte) string {
	t.Helper()
	hashed, err := withExecs(files, execs, src)
	if err != nil {
		t.Fatal(err)
	}
	return digest(hashed)
}

func TestCountToplevels(t *testing.T) {
	log := `[1.0] -> xdg_toplevel#30.set_title("Main")
[1.1] -> xdg_toplevel#31.set_title("Load")
[1.2] -> xdg_toplevel#31.set_app_id("app")
[1.3] -> wl_surface#3.commit()
[1.4] -> xdg_toplevel#40.set_title("Load")
`
	for title, want := range map[string]int{"": 3, "Load": 2, "Main": 1, "Lo": 0} {
		if got := countToplevels(log, title); got != want {
			t.Errorf("countToplevels(%q) = %d, want %d", title, got, want)
		}
	}
}
