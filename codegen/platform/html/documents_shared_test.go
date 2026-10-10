package html

import (
	"strings"
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/scheme/markdown"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/optimize"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/internal/testutil"
	"duckfam.us/sngl/ir"
)

// Writing the documents leaves the lowered package as it found it: the fold
// shares loop bodies with it rather than copying them first, so a write that
// reached one would edit the program every later document is written from.
// And each document's redraws name a canvas that document holds.
func TestDocumentsLeaveThePackageUnchanged(t *testing.T) {
	t.Run("in the page", func(t *testing.T) { writeDocuments(t, inPage) })
	t.Run("beside the pages", func(t *testing.T) { writeDocuments(t, besidePages) })
}

// A window @error that is the only redraw, reached through a catch block in
// every page, of a canvas outside the loop of pages.
const besidePages = `import ui "sngl:ui"
import nav "sngl:ui/nav"
import draw "sngl:ui/draw"

struct P {
    href string
    n int
}

const ps = [P{href="/", n=1}, P{href="/two", n=2}]

var r = 5.0

ui.window(title=pages.current.title, @error(e) { r = 20.0 }) {
    draw.canvas(width=50px, height=50px) {
        draw.circle(cx=25.0, cy=25.0, r=r)
    }
    nav.stack #pages {
        for var p = ps {
            nav.page(href=p.href, title=p.href) {
                ui.button(text="boom {p.n}", @click { error.raise("x", "boom") })
            }
        }
    }
}
`

const inPage = `import ui "sngl:ui"
import nav "sngl:ui/nav"
import draw "sngl:ui/draw"
import md "sngl:ui/markup/md"

struct P {
    href string
    n int
    body string
}

const ps = [P{href="/", n=1, body="- a\n  - b\n    - c"}, P{href="/two", n=2, body="1. x\n2. y"}]

var r = 5.0

ui.window(title=pages.current.title, @error(e) { r = 20.0 }) {
    nav.stack #pages {
        for var p = ps {
            nav.page(href=p.href, title=p.href) {
                draw.canvas(width=50px, height=50px) {
                    draw.circle(cx=25.0, cy=25.0, r=r)
                }
                ui.button(text="grow {p.n}", @click { r = 25.0 })
                ui.button(text="boom", @click { error.raise("x", "boom") })
                md.document(source=p.body)
                for var k = [1, 2] {
                    ui.button(text="{p.n} {k}", @click { r = 30.0 })
                }
                boundary(@error(e) { r = 1.0 }) {
                    ui.button(text="inner", @click { error.raise("x", "in") })
                    component failed {
                        ui.text(value="failed {p.n}")
                    }
                }
            }
        }
    }
}
`

func writeDocuments(t *testing.T, src string) {
	dir := t.TempDir()
	pkg := lowerHTML(t, dir, src)
	before := testutil.GraphOf(pkg)
	fold := optimize.NewFold(pkg, &optimize.Config{Platform: "html", Language: "none", Dir: dir})
	n := 0
	for doc, err := range documentsOf(pkg, fold, true) {
		if err != nil {
			t.Fatalf("documents: %v", err)
		}
		redrawn, held := redrawsAndNodes(doc.Body)
		for _, c := range redrawn {
			if !held[c] {
				t.Errorf("document %d redraws a canvas it does not hold", n)
			}
		}
		n++
	}
	if n != 2 {
		t.Fatalf("wrote %d documents, want 2", n)
	}
	if changed := before.Changed(testutil.GraphOf(pkg)); len(changed) > 0 {
		t.Errorf("writing the documents changed %d objects of the lowered package:\n%s", len(changed), strings.Join(changed[:min(len(changed), 12)], "\n"))
	}
}

func lowerHTML(t *testing.T, dir, src string) *ir.Package {
	t.Helper()
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{
		Dir:       dir,
		IsMain:    true,
		Platforms: []ir.Platform{&Generator{}},
		Languages: htmlLangs(),
		Targets:   []ir.StaticTarget{{Platform: "html", Language: "none"}},
	})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}
	gen := &Generator{}
	lang := codegen.LookupLang("none")
	if err := optimize.Optimize(pkg, &optimize.Config{Platform: "html", Language: "none", Dir: dir}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	caps := codegen.CapsOrNone(lang.LanguageIdentifier(), gen.PlatformIdentifier())
	if err := lower.Lower(pkg, caps, lower.Options{Platform: "html"}); err != nil {
		t.Fatalf("lower: %v", err)
	}
	return pkg
}

// redrawsAndNodes is the canvases stmts redraw and the nodes they hold, a
// catch block's handler included.
func redrawsAndNodes(stmts []ir.Stmt) ([]*ir.NodeInst, map[*ir.NodeInst]bool) {
	var redrawn []*ir.NodeInst
	held := map[*ir.NodeInst]bool{}
	seen := map[*ir.EventHandler]bool{}
	var visit func(n ir.Node) error
	visit = func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			held[x] = true
		case *ir.CanvasRedrawStmt:
			redrawn = append(redrawn, x.Canvas)
		case *ir.If:
			if h := x.Catch; h != nil && h.Func != nil && !seen[h] {
				seen[h] = true
				_ = ir.Walk(h.Func.Block, visit)
			}
		}
		return nil
	}
	_ = ir.Walk(stmts, visit)
	return redrawn, held
}
