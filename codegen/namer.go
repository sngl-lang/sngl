package codegen

import "fmt"

// Namer generates unique names for codegen targets on demand.
// Counters are per-category, producing deterministic sequences
// like "input0", "input1", "$0", "$1", etc.
type Namer struct {
	counters map[string]int
}

// NewNamer creates a Namer with empty counters.
func NewNamer() *Namer {
	return &Namer{counters: make(map[string]int)}
}

// Next returns the next unique name for a category.
// Next("input") → "input0", "input1", "input2", ...
func (n *Namer) Next(category string) string {
	idx := n.counters[category]
	n.counters[category] = idx + 1
	return fmt.Sprintf("%s%d", category, idx)
}

// NextPrefixed returns a prefixed counter.
// NextPrefixed("$") → "$0", "$1", ...
func (n *Namer) NextPrefixed(prefix string) string {
	idx := n.counters[prefix]
	n.counters[prefix] = idx + 1
	return fmt.Sprintf("%s%d", prefix, idx)
}
