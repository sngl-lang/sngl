// Package marshalpkg is a go:// package for sngltest's own tests: one type per
// way a Go value can reach SNGL, including one whose halves disagree.
package marshalpkg

import (
	"encoding/base64"

	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// Item has no marshalling of its own: the encoder walks it by reflection and
// the importer declares it a struct.
type Item struct {
	Name  string
	Value int
}

// Tag writes itself as the string the importer types it as.
type Tag string

// MarshalSNGL writes the tag as a SNGL string.
func (t Tag) MarshalSNGL() ([]byte, error) {
	return consteval.AppendQuote(nil, string(t)), nil
}

// Blob is the disagreement: base64 is a reasonable form for a byte slice and a
// string is not a list<int>, which is what the importer makes of []byte.
type Blob []byte

// MarshalSNGL writes the bytes base64-encoded.
func (b Blob) MarshalSNGL() ([]byte, error) {
	return consteval.AppendQuote(nil, base64.StdEncoding.EncodeToString(b)), nil
}

// Chan is a type the importer models as nothing.
type Chan chan int
