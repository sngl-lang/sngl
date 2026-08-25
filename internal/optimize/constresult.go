package optimize

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// resultSep separates a key from its value in the results file. Neither half
// can hold one: a key is a hash, and an encoder escapes a tab inside a string
// rather than writing it.
const resultSep = "\t"

// parseNativeResults reads the file pkg/go/consteval and pkg/js/consteval
// write — a header comment and then one `<key><TAB><expression>` line per
// evaluated call — and checks each value against the declared return type of
// the function that produced it.
//
// Each value is SNGL, so it becomes IR the same way a hand-written literal
// does, but through the native-value entry point: only there may a value name
// its own type. want carries the return types, keyed as the records are; a key
// with no entry (a call that returns nothing) is checked with no expectation.
//
// The second result carries the keys that failed, one error each: a value that
// does not check says nothing about the next key, which came from a different
// function with its own declared type. Only a record with no key at all — real
// corruption, with nothing to attribute the failure to — is returned as a
// whole-batch error.
func parseNativeResults(path string, src []byte, want map[string]*ir.Type, types ir.NativeDecls) (map[string]ir.Expr, map[string]error, error) {
	out := map[string]ir.Expr{}
	bad := map[string]error{}
	for n, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		where := fmt.Sprintf("%s:%d", path, n+1)
		key, val, ok := strings.Cut(line, resultSep)
		if !ok {
			return nil, nil, fmt.Errorf("%s: const evaluator result has no key", where)
		}
		expr, err := parser.ParseNativeValue(where, []byte(val))
		if err != nil {
			bad[key] = err
			continue
		}
		e, err := checker.CheckNativeValue(expr, want[key], types)
		if err != nil {
			bad[key] = err
			continue
		}
		out[key] = e
	}
	return out, bad, nil
}
