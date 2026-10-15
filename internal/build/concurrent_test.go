package build_test

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
)

// Each reaches a different part of the library, so the loads one makes land
// part-way through another's check.
var concurrentPrograms = []string{
	`import ui "sngl:ui"

output {
    none { html }
}

var n = 0

ui.window(title="t") {
    ui.button(text="{n}", @click { n += 1 })
}
`,
	`import ui "sngl:ui"
import draw "sngl:ui/draw"

output {
    go { bubbletea }
}

ui.window(title="t") {
    draw.canvas(width=40px, height=20px) {
        draw.circle(cx=10.0, cy=10.0, r=5.0)
    }
}
`,
	`import ui "sngl:ui"
import markup "sngl:ui/markup"

output {
    go { fyne }
}

ui.window(title="t") {
    markup.richText {
        markup.bold {
            markup.text(value="b")
        }
        markup.text(value=" and ")
        markup.monospace {
            markup.text(value="m")
        }
    }
}
`,
	`import ui "sngl:ui"
import time "sngl:time"

output {
    kotlin { android }
}

var ticks = 0

ui.window(title="t") {
    time.timer(interval=1s, @tick { ticks += 1 })
    ui.text(value="{ticks}")
}
`,
	`import ui "sngl:ui"
import nav "sngl:ui/nav"

output {
    go { bubbletea }
    none { html }
}

ui.window(title="t") {
    nav.stack #pages {
        nav.page #home(href="/", title="Home") {
            ui.text(value="home")
        }
    }
}
`,
	`import ui "sngl:ui"
import badge "./badge"

output {
    go { fyne }
    none { html }
}

var n = 0

ui.window(title="t") {
    badge.count(n=n, @bump { n += 1 })
}
`,
}

// concurrentPackages are directory packages every program's directory holds,
// which the checks keep between them.
var concurrentPackages = map[string]string{
	"badge/badge.sngl": `import ui "sngl:ui"
import style "./style"

component count(n int, @bump()) ui.node {
    ui.button(text="{style.prefix}{n}", @click { bump() })
}
`,
	"badge/style/style.sngl": `const prefix = "n="
`,
}

func buildProgram(src string) (map[string][]byte, error) {
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		return nil, err
	}
	fsys := fstest.MapFS{"main.sngl": {Data: []byte(src)}}
	for name, data := range concurrentPackages {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	pkg, err := build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true})
	if err != nil {
		return nil, err
	}
	results, err := build.Emit(pkg, build.Options{Name: "main.sngl", Dir: ".", ProjectFS: fsys})
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, r := range results {
		for name, data := range r.Files {
			files[r.Target.Lang+"/"+r.Target.Platform+"/"+name] = data
		}
	}
	return files, nil
}

// Checks and builds run at once against the one library every check in the
// process shares, which -race holds to writing none of it, and each builds
// what it builds alone.
func TestConcurrentChecksAndBuildsShareTheLibrary(t *testing.T) {
	// Cold, so the workers load the library between them.
	checker.EjectLibraries()
	const workers = 6
	got := make([][]map[string][]byte, workers)
	var wg sync.WaitGroup
	errs := make(chan error, workers*len(concurrentPrograms))
	for w := range workers {
		wg.Go(func() {
			got[w] = make([]map[string][]byte, len(concurrentPrograms))
			for k := range concurrentPrograms {
				i := (w + k) % len(concurrentPrograms)
				files, err := buildProgram(concurrentPrograms[i])
				if err != nil {
					errs <- fmt.Errorf("worker %d, program %d: %v", w, i, err)
				}
				got[w][i] = files
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Against a library loaded by one check at a time.
	checker.EjectLibraries()
	for i, src := range concurrentPrograms {
		want, err := buildProgram(src)
		if err != nil {
			t.Fatalf("program %d alone: %v", i, err)
		}
		for w := range workers {
			if got[w][i] != nil && !maps.EqualFunc(got[w][i], want, slices.Equal) {
				t.Errorf("worker %d built program %d differently than a check alone", w, i)
			}
		}
	}
}
