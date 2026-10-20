package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The release binary must not reach a reflective method call --
// reflect.Value.MethodByName or Method with an index not known until run
// time, which text/template's field lookup is. Once the linker sees one it
// keeps every exported method of every type the binary holds, because it can
// no longer tell which go unused: glamour running a style's Format through
// text/template made the CLI half again as large (66.6 MB to 44.8 MB
// stripped). cobra imports text/template too, and only runs it for a custom
// help template, so asking whether the package is linked would not do: the
// linker's dependency dump marks each reached call <ReflectMethod>.
func TestBinaryReachesNoReflectiveMethodCall(t *testing.T) {
	if testing.Short() {
		t.Skip("links the binary")
	}
	cmd := exec.Command("go", "build", "-o", os.DevNull, "-ldflags=-dumpdep", ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var dump bytes.Buffer
	cmd.Stderr = &dump
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, tail(dump.String()))
	}
	sc := bufio.NewScanner(&dump)
	sc.Buffer(nil, 1<<20)
	seen := map[string]bool{}
	for sc.Scan() {
		// Each line is an edge, `from [<tags>] -> to [<tags>]`; the
		// function making the call is the one marked on the left.
		from, _, _ := strings.Cut(sc.Text(), " -> ")
		sym, tags, _ := strings.Cut(from, " ")
		if strings.Contains(tags, "<ReflectMethod>") && !seen[sym] {
			seen[sym] = true
			t.Errorf("the binary reaches a reflective method call in %s", sym)
		}
	}
}

func tail(s string) string {
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}
