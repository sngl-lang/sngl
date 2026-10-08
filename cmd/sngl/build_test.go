package main

import (
	"testing"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

func TestSanitizeBinaryBase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"myapp", "myapp"},
		{"My App", "my-app"},
		{"hello_world", "hello-world"},
		{"a/b/c", "a-b-c"},
		{"--leading--", "leading"},
		{"weird!@#$name", "weird-name"},
		{"UPPER", "upper"},
		{"", ""},
	}
	for _, c := range cases {
		got := sanitizeBinaryBase(c.in)
		if got != c.want {
			t.Errorf("sanitizeBinaryBase(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	mkOpts := func(name string) *ir.StructLit {
		opts := &ir.StructLit{}
		if name != "" {
			codegen.SetOptionField(opts, "name", name)
		}
		return opts
	}

	cases := []struct {
		desc        string
		opts        *ir.StructLit
		platform    string
		multiTarget bool
		windows     bool
		want        string
	}{
		{"default when name unset", mkOpts(""), "bubbletea", false, false, "app"},
		{"single target uses bare name", mkOpts("myapp"), "bubbletea", false, false, "myapp"},
		{"multi target appends platform", mkOpts("myapp"), "bubbletea", true, false, "myapp-bubbletea"},
		{"sanitises name", mkOpts("My App!"), "fyne", false, false, "my-app"},
		{"windows extension", mkOpts("myapp"), "bubbletea", false, true, "myapp.exe"},
		{"multi target windows", mkOpts("myapp"), "fyne", true, true, "myapp-fyne.exe"},
	}
	for _, c := range cases {
		got := binaryName(c.opts, c.platform, c.multiTarget, c.windows)
		if got != c.want {
			t.Errorf("%s: binaryName = %q; want %q", c.desc, got, c.want)
		}
	}
}
