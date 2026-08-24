package purepkg

import (
	"strings"
	"time"
)

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

// Whole returns a float whose value happens to be a whole number. On the JSON
// results path such a value came back as an int and folded to an int literal;
// the type says float and must stay float.
//
//sngl:pure
func Whole() float64 { return 3.0 }

// Wait returns a duration. SNGL models one as a unit value with a base of
// milliseconds, so the fold must keep the unit rather than the bare magnitude.
//
//sngl:pure
func Wait() time.Duration { return 250 * time.Millisecond }

// Meta names its fields the way Go does and SNGL does not: an all-caps
// initialism lowercases whole (URL, ID) while a leading one in a longer name
// does not (HTTPStatus). Guessing the SNGL name from the Go name is what the
// folder used to do; the correspondence is recorded on the imported
// declaration instead.
type Meta struct {
	URL        string
	ID         int
	HTTPStatus int
}

// GetMeta returns a named struct directly, so the fold must produce a struct
// literal carrying that declaration.
//
//sngl:pure
func GetMeta() Meta { return Meta{URL: "/a", ID: 7, HTTPStatus: 404} }

// Group holds a list of structs, so a value nests a struct inside a list
// inside a struct.
type Group struct {
	Label string
	Items []Item
}

// GetGroups returns groups of items.
//
//sngl:pure
func GetGroups() []Group {
	return []Group{
		{Label: "first", Items: []Item{{Name: "alpha", Value: 1}}},
		{Label: "second", Items: nil},
	}
}

// Tally has Item's field shape under a different name, so a value of one where
// the other is expected can only be caught by the type name.
type Tally struct {
	Name  string
	Value int
}

// GetTally returns a Tally.
//
//sngl:pure
func GetTally() Tally { return Tally{Name: "t", Value: 3} }

// Stamp is embedded in Record. The encoder writes an embedded field under its
// own Go name rather than promoting its fields, because that is the shape the
// go:// importer declares.
type Stamp struct {
	At  string
	Seq int
}

// Record embeds Stamp.
type Record struct {
	Stamp
	Note string
}

// GetRecord returns a struct with an embedded struct.
//
//sngl:pure
func GetRecord() Record { return Record{Stamp: Stamp{At: "t0", Seq: 1}, Note: "n"} }

// Anything returns structs through an interface slice, so the importer can only
// type the elements as dyn — there is no declaration for the reader to map
// field names through.
//
//sngl:pure
func Anything() []any { return []any{Item{Name: "alpha", Value: 1}} }
