package interp

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func check(t *testing.T, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s: %s", d.Pos, d.Msg)
		}
	}
	return pkg
}

func keysOf(t *testing.T, src, comp string) []Key {
	t.Helper()
	c := FindComponent(check(t, src), comp)
	if c == nil {
		t.Fatalf("component %q not found", comp)
	}
	return ComponentKeys(c)
}

const keySrc = `import . "sngl:ui"

component main {
    vbox {
        text #greeting(value="hi")
        text(value="there")
        if true {
            text(value="yes")
        } else {
            text(value="no")
        }
    }
}
`

// TestKeysSurviveARecheck is the whole point of the Key projection: a reload
// replaces the ir.Package, so every pointer-keyed cache in the interpreter is
// garbage afterwards. Keys are what carry state across that.
func TestKeysSurviveARecheck(t *testing.T) {
	first := keysOf(t, keySrc, "main")
	second := keysOf(t, keySrc, "main")

	if len(first) == 0 {
		t.Fatal("no keys produced")
	}
	if !KeyPathsEqual(first, second) {
		t.Errorf("keys differ across a recheck:\nfirst:\n%ssecond:\n%s",
			KeyList(first), KeyList(second))
	}

	// And prove the keys are not merely pointer identity in disguise: the two
	// checks share no IR at all.
	a := FindComponent(check(t, keySrc), "main")
	b := FindComponent(check(t, keySrc), "main")
	if a == b {
		t.Fatal("two checks returned the same *ir.Component; the test proves nothing")
	}
	an, bn := firstNode(a.Body), firstNode(b.Body)
	if an == nil || bn == nil {
		t.Fatal("no *ir.NodeInst in body")
	}
	if an == bn {
		t.Error("two checks returned the same *ir.NodeInst; the test proves nothing")
	}
}

// TestAnIDKeyIsStableAcrossAnInsertAbove is why #id is preferred over a
// positional segment. An author who wants a node's state to survive editing
// above it gives it an id -- which is already how a test addresses one.
func TestAnIDKeyIsStableAcrossAnInsertAbove(t *testing.T) {
	before := keysOf(t, `import . "sngl:ui"

component main {
    vbox {
        text #greeting(value="hi")
    }
}
`, "main")

	after := keysOf(t, `import . "sngl:ui"

component main {
    vbox {
        image()
        text #greeting(value="hi")
    }
}
`, "main")

	if !hasPathSuffix(before, "#greeting") {
		t.Fatalf("no #greeting key before the edit:\n%s", KeyList(before))
	}
	if !hasPathSuffix(after, "#greeting") {
		t.Errorf("#greeting key did not survive an insert above it:\n%s", KeyList(after))
	}
}

// TestPositionalKeysCountAmongSameNamedSiblings: without an id the index is all
// there is, but it is scoped to the element name, so inserting a different
// element above does not renumber it.
func TestPositionalKeysCountAmongSameNamedSiblings(t *testing.T) {
	before := keysOf(t, `import . "sngl:ui"

component main {
    vbox {
        text(value="hi")
    }
}
`, "main")

	after := keysOf(t, `import . "sngl:ui"

component main {
    vbox {
        image()
        text(value="hi")
    }
}
`, "main")

	want := findPathSuffix(before, "text@0")
	if want == "" {
		t.Fatalf("no text@0 key before the edit:\n%s", KeyList(before))
	}
	if got := findPathSuffix(after, "text@0"); got != want {
		t.Errorf("text@0 path moved from %q to %q after inserting an image above it:\n%s",
			want, got, KeyList(after))
	}
}

// TestBothBranchesOfAnIfGetKeys: the walk is structural, so it does not
// evaluate the condition. Both branches must be addressable, because a reload
// may flip which one is live.
func TestBothBranchesOfAnIfGetKeys(t *testing.T) {
	keys := keysOf(t, keySrc, "main")
	var then, els bool
	for _, k := range keys {
		if contains(k.Path, "if@0:then") {
			then = true
		}
		if contains(k.Path, "if@0:else") {
			els = true
		}
	}
	if !then || !els {
		t.Errorf("if branches: then=%v else=%v; want both:\n%s", then, els, KeyList(keys))
	}
}

func firstNode(stmts []ir.Stmt) *ir.NodeInst {
	for _, s := range stmts {
		if n, ok := s.(*ir.NodeInst); ok {
			return n
		}
	}
	return nil
}

func hasPathSuffix(keys []Key, suffix string) bool {
	return findPathSuffix(keys, suffix) != ""
}

func findPathSuffix(keys []Key, suffix string) string {
	for _, k := range keys {
		if len(k.Path) >= len(suffix) && k.Path[len(k.Path)-len(suffix):] == suffix {
			return k.Path
		}
	}
	return ""
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
