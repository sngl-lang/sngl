package main

import (
	"bytes"
	"os"
	"testing"

	"duckfam.us/sngl/codegen/platform/gtk4"
)

// TestCommitted holds lib/platform/gtk4next/widgets.sngl to what gtk4gen
// writes, the go.native marks cgogen adds beside each declaration aside.
func TestCommitted(t *testing.T) {
	want, err := gtk4.WidgetSource(header)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("../../../lib/platform/gtk4next/widgets.sngl")
	if err != nil {
		t.Fatal(err)
	}
	var kept [][]byte
	for line := range bytes.Lines(have) {
		if !bytes.HasPrefix(line, []byte(`#[go.native("duckfam.us/sngl/lib/platform/gtk4next/cgo", `)) {
			kept = append(kept, line)
		}
	}
	if !bytes.Equal(bytes.Join(kept, nil), want) {
		t.Error("lib/platform/gtk4next/widgets.sngl is not what gtk4gen writes; rerun `go generate ./lib`")
	}
}
