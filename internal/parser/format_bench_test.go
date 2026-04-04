package parser_test

import (
	"os"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func BenchmarkFormat(b *testing.B) {
	data, err := os.ReadFile("../../testdata/checker_complete.sngl")
	if err != nil {
		b.Fatal(err)
	}
	doc, err := parser.Parse("bench.sngl", strings.NewReader(string(data)))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.Format(doc)
	}
}

func BenchmarkFormatLarge(b *testing.B) {
	// Build a larger document by combining multiple fixtures
	var sb strings.Builder
	fixtures := []string{
		"../../testdata/checker_complete.sngl",
		"../../testdata/checker_advanced.sngl",
		"../../testdata/full_example.sngl",
		"../../testdata/checker_exprs.sngl",
	}
	for _, f := range fixtures {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	doc, err := parser.Parse("bench.sngl", strings.NewReader(sb.String()))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.Format(doc)
	}
}
