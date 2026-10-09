package checker_test

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testtargets"
	"duckfam.us/sngl/ir"
)

// #[js.native] is #[go.native] for JavaScript, and parts from it in one place:
// a Go identifier is always package-qualified, so that mark requires a path,
// while `setInterval` is a property of globalThis and has none. The name comes
// first and the module is optional.
func TestJSNativeNamesAGlobalOrAModule(t *testing.T) {
	src := `import . "sngl:macro"
import js "sngl:language/js"

#[js.native("setInterval")]
func every(f func(), ms int) int

#[js.native("readFile", "node:fs/promises", async)]
func readFile(path string) string
`
	pkg := checkedPkgWithTargets(t, src)

	global := findFn(t, pkg, "every")
	if global.Foreign.Name != "setInterval" {
		t.Errorf("global Foreign.Name = %q, want setInterval", global.Foreign.Name)
	}
	if global.Foreign.Path != "" {
		t.Errorf("a global carries module %q; it has none to carry", global.Foreign.Path)
	}
	if global.Foreign.Scheme != "js" {
		t.Errorf("Foreign.Scheme = %q, want js", global.Foreign.Scheme)
	}
	// Unset for the reason go.native leaves it unset: the identifier already
	// exists, so a call becomes a call to it rather than to something emitted.
	if global.Foreign.Marked {
		t.Error("a native declaration is Marked; a backend would emit it instead of calling it")
	}
	if global.IsAsync {
		t.Error("a native with no async flag is awaited")
	}

	mod := findFn(t, pkg, "readFile")
	if mod.Foreign.Path != "node:fs/promises" {
		t.Errorf("module Foreign.Path = %q, want node:fs/promises", mod.Foreign.Path)
	}
	// The same fact the TypeScript importer reads off a `Promise<T>` return.
	if !mod.IsAsync {
		t.Error("the async flag did not reach IsAsync, so a call to it is not awaited")
	}
}

// A bodyless declaration is where the mark belongs: the answer comes from the
// identifier it names. The check that a function says where its answer comes
// from used to test the module path alone, so a global -- which has no path --
// was rejected as having no body at all.
func TestJSNativeGlobalNeedsNoBody(t *testing.T) {
	for name, src := range map[string]string{
		"global": `import . "sngl:macro"
import js "sngl:language/js"

#[js.native("btoa")]
func encode(s string) string
`,
		"module": `import . "sngl:macro"
import js "sngl:language/js"

#[js.native("join", "node:path")]
func join(a string, b string) string
`,
	} {
		t.Run(name, func(t *testing.T) {
			checkedPkgWithTargets(t, src)
		})
	}
}

// The body rejection is not JavaScript's: a native names an identifier that
// already exists, whichever language it belongs to. Both marks tried to say it
// themselves and could not -- a mark runs while the declaration is registered,
// which is before any body is checked -- so #[go.native] accepted a body and
// silently dropped it for as long as the mark has existed.
func TestANativeWithABodyIsRejectedInEitherLanguage(t *testing.T) {
	for name, src := range map[string]string{
		"js": `import . "sngl:macro"
import js "sngl:language/js"

#[js.native("btoa")]
func encode(s string) string {
    return s
}
`,
		"go": `import . "sngl:macro"
import go "sngl:language/go"

#[go.native("strings", "ToUpper")]
func shout(s string) string {
    return s
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := checkErrors(t, src); !strings.Contains(got, "already exists: the body would be emitted by nobody") {
				t.Errorf("diagnostics %q do not reject the body", got)
			}
		})
	}
}

// checkErrors returns the error diagnostics of a check, joined.
func checkErrors(t *testing.T, src string) string {
	t.Helper()
	doc, err := parser.Parse("test.sngl", []byte(withStd(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	langs, plats := testtargets.Targets()
	_, diags := checker.Check(doc, &checker.Config{IsMain: true, Languages: langs, Platforms: plats})
	var got []string
	for _, d := range diags {
		if d.Severity == ir.Error {
			got = append(got, d.Msg)
		}
	}
	return strings.Join(got, "\n")
}

func TestJSNativeRejections(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"no name": {`import . "sngl:macro"
import js "sngl:language/js"

#[js.native("")]
func encode(s string) string
`, "an identifier is required"},
		"a flag on a struct": {`import . "sngl:macro"
import js "sngl:language/js"

#[js.native("Blob", "", async)]
struct blob {}
`, "describes a call"},
	} {
		t.Run(name, func(t *testing.T) {
			joined := checkErrors(t, tc.src)
			if !strings.Contains(joined, tc.want) {
				t.Errorf("diagnostics %q do not mention %q", joined, tc.want)
			}
		})
	}
}
