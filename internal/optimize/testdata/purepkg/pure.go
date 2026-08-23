package purepkg

import "strings"

// Double returns x * 2.
//
//sngl:pure
func Double(x int) int { return x * 2 }

// Greet returns a greeting string.
//
//sngl:pure
func Greet(name string) string { return "Hello, " + name + "!" }

// Item is a test struct for list returns.
type Item struct {
	Name  string
	Value int
}

// GetItems returns a fixed list of items.
//
//sngl:pure
func GetItems() []Item {
	return []Item{
		{Name: "alpha", Value: 1},
		{Name: "beta", Value: 2},
	}
}

// Boom always fails at compile-time evaluation: it panics, so the
// gen-and-run subprocess exits non-zero. Used to test that a failed
// go:// import evaluation aborts the build on the html platform.
//
//sngl:pure
func Boom() string { panic("boom") }

// Join concatenates parts. Exercises a compile-time call whose argument is a
// list, so the generated call site has to render a Go slice literal.
//
//sngl:pure
func Join(parts []string, sep string) string { return strings.Join(parts, sep) }

// Nothing returns nothing: a pure call the folder can only turn into null.
//
//sngl:pure
func Nothing() {}
