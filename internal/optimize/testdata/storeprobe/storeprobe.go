// Package storeprobe is a producer both halves of a compile-time evaluation
// link: the evaluator reads through it, as a pure function reading a
// target's generated package does, and the compiler checks what the
// evaluator reported having read by producing it again.
package storeprobe

import (
	"bytes"
	"os"

	"git.duckfam.us/jonathan/sngl/internal/gencache"
	"git.duckfam.us/jonathan/sngl/pkg/go/consteval"
)

// Producer names the file this package stores: one file's content.
const Producer = "test.storeprobe"

func init() { gencache.Register(Producer, produce) }

func produce(_ *gencache.Store, params []string) (gencache.Output, error) {
	in, err := gencache.File(params[0])
	if err != nil {
		return gencache.Output{}, err
	}
	data, err := os.ReadFile(params[0])
	if err != nil {
		return gencache.Output{}, err
	}
	return gencache.Output{Inputs: []gencache.Input{in}, Body: append(consteval.AppendQuote([]byte("const content = "), string(data)), '\n')}, nil
}

// Read returns path's content as the store holds it.
func Read(path string) (string, error) {
	data, err := gencache.Default().Get(gencache.Request{Producer: Producer, Params: []string{path}})
	if err != nil {
		return "", err
	}
	line, _, _ := bytes.Cut(gencache.Body(data), []byte("\n"))
	return string(bytes.TrimPrefix(line, []byte("const content = "))), nil
}
