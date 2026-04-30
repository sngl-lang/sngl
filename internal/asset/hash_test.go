package asset

import (
	"regexp"
	"testing"
)

func TestHashedName(t *testing.T) {
	data := []byte("body { color: red; }")
	got := HashedName("style.css", data)
	if !regexp.MustCompile(`^style\.[0-9a-f]{8}\.css$`).MatchString(got) {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestHashedNameNoExt(t *testing.T) {
	got := HashedName("Makefile", []byte("hello"))
	if !regexp.MustCompile(`^Makefile\.[0-9a-f]{8}$`).MatchString(got) {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestHashedNameLeadingDot(t *testing.T) {
	// Dotfile-only names (".gitignore") have no extension to split before.
	got := HashedName(".gitignore", []byte("foo"))
	if !regexp.MustCompile(`^\.gitignore\.[0-9a-f]{8}$`).MatchString(got) {
		t.Fatalf("unexpected name: %q", got)
	}
}

func TestHashedNameDeterministic(t *testing.T) {
	data := []byte("payload")
	a := HashedName("a.wasm", data)
	b := HashedName("a.wasm", data)
	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}
}

func TestHashedNameContentSensitive(t *testing.T) {
	a := HashedName("a.js", []byte("v1"))
	b := HashedName("a.js", []byte("v2"))
	if a == b {
		t.Fatalf("hash should differ across content; got %q for both", a)
	}
}

func TestHashedNameMultipleDots(t *testing.T) {
	// "foo.min.js" should split at the last dot, becoming "foo.min.<hash>.js".
	got := HashedName("foo.min.js", []byte("x"))
	if !regexp.MustCompile(`^foo\.min\.[0-9a-f]{8}\.js$`).MatchString(got) {
		t.Fatalf("unexpected name: %q", got)
	}
}
