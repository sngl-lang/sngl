package main

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/playground"
)

func TestExampleArchiveSkipsBinaryFiles(t *testing.T) {
	src, ok, err := exampleArchive(fstest.MapFS{
		"app.sngl":       {Data: []byte("import ui \"sngl:ui\"\n")},
		"doc.sngl":       {Data: []byte("// An example.\n")},
		"lib/lib.sngl":   {Data: []byte("const n = 1\n")},
		"assets/img.png": {Data: []byte("\x89PNG\x00\x01")},
	})
	if err != nil || !ok {
		t.Fatalf("exampleArchive: ok=%v err=%v", ok, err)
	}
	want := "import ui \"sngl:ui\"\n-- doc.sngl --\n// An example.\n-- lib/lib.sngl --\nconst n = 1"
	if src != want {
		t.Errorf("archive:\n%s\nwant:\n%s", src, want)
	}
}

func TestExamplesCompileInThePlayground(t *testing.T) {
	examples := loadExamples("../../../examples")
	if len(examples) == 0 {
		t.Fatal("no examples found")
	}
	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) {
			var result struct {
				HTML  string `json:"html"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(playground.Compile(ex.source)), &result); err != nil {
				t.Fatal(err)
			}
			if result.Error != "" {
				t.Fatalf("compile: %s", result.Error)
			}
			if !strings.Contains(result.HTML, "<body") {
				t.Errorf("no document in the output")
			}
		})
	}
}
