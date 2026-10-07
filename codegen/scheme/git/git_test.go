package git

import (
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// Each part of a git: import becomes a directory under the cache, so a part
// naming the directory above it would have `git clone` write outside the
// cache. Every such import is refused before anything is fetched.
func TestParseGitURIRefusesEscapes(t *testing.T) {
	for _, uri := range []string{
		"//example.com/../../../tmp/x@v1",
		"//example.com/user/repo@../../x",
		"//../repo@v1",
		"//example.com/user//repo@v1",
		"//example.com/./repo@v1",
		"//example.com/user/repo@-uhoh",
		`//example.com/user\..\repo@v1`,
	} {
		if g, err := parseGitURI(uri); err == nil {
			t.Errorf("parseGitURI(%q) = %+v, want an error", uri, g)
		}
	}
	g, err := parseGitURI("//example.com/user/repo@feature/x")
	if err != nil {
		t.Fatal(err)
	}
	dir := gitCacheDir(g.host, g.path, g.ref)
	if base := filepath.Join(codegen.SnglCacheDir(), "git"); !strings.HasPrefix(dir, base+string(filepath.Separator)) {
		t.Errorf("cache dir %s is outside %s", dir, base)
	}
}
