package parser_test

import (
	"os"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// The corpus is a captured snapshot of the gtk4 platform package — 148KB
// synthesized from GObject introspection data — which is the largest single
// document the compiler parses, and it parses it on every check that targets
// gtk4. Captured rather than generated so the benchmark does not vary with
// the GTK version installed on the machine running it.
func BenchmarkParseLarge(b *testing.B) {
	src, err := os.ReadFile("testdata/bench_large.sngl")
	if err != nil {
		b.Skip("no bench corpus")
	}
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse("bench.sngl", src); err != nil {
			b.Fatal(err)
		}
	}
}
