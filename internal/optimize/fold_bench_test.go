package optimize_test

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/internal/parser"
)

// foldBenchSource is a static html page of many styled nodes, each label a
// const func whose body filters through a lambda: the shape that made folding
// the docs site's pages its largest cost.
func foldBenchSource(nodes int) string {
	var b strings.Builder
	b.WriteString(`import ui "sngl:ui"

output {
    none { html }
}

const words = ["alpha", "", "beta", "gamma", "", "delta", "epsilon", "zeta", "", "eta"]

`)
	// A package's consts are the environment every interpreted call runs in.
	for i := range 300 {
		fmt.Fprintf(&b, "const c%d = %d\n", i, i)
	}
	b.WriteString(`
const func label(i int) string {
    var kept = words.filter(func(w string) => w.length > i % 4)
    return "{kept.length}:{i}"
}

ui.window(title="bench") {
    ui.vbox(style={padding=8px}) {
`)
	for i := range nodes {
		fmt.Fprintf(&b, "        ui.text(value=label(%d), style={padding=%dpx, margin=2px})\n", i%100, i%5)
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

func BenchmarkFoldStyledPage(b *testing.B) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	src := foldBenchSource(400)
	fsys := fstest.MapFS{"main.sngl": {Data: []byte(src)}}
	doc, err := parser.Parse("main.sngl", []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	pkg, err := build.Check(doc, build.CheckConfig{FS: fsys, IsMain: true})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := build.Emit(pkg, build.Options{Name: "main.sngl", Dir: ".", ProjectFS: fsys}); err != nil {
			b.Fatal(err)
		}
	}
}
