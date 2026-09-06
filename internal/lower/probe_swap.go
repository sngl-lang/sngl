package lower

// TEMPORARY probe hook -- delete before committing.
//
// SNGL_PROBE_SWAP="A,B" swaps the registry entries named A and B at init, so a
// pass-order experiment needs no source edit and several can run concurrently
// against one tree.

import (
	"fmt"
	"os"
	"strings"
)

func init() {
	if drop := os.Getenv("SNGL_PROBE_DROP"); drop != "" {
		out := passes[:0]
		found := false
		for _, p := range passes {
			if p.name == drop {
				found = true
				continue
			}
			out = append(out, p)
		}
		if !found {
			panic("SNGL_PROBE_DROP: unknown pass " + drop)
		}
		passes = out
	}
	spec := os.Getenv("SNGL_PROBE_SWAP")
	if spec == "" {
		return
	}
	parts := strings.Split(spec, ",")
	if len(parts) != 2 {
		panic("SNGL_PROBE_SWAP: want A,B")
	}
	ia, ib := -1, -1
	for i, p := range passes {
		if p.name == parts[0] {
			ia = i
		}
		if p.name == parts[1] {
			ib = i
		}
	}
	if ia < 0 || ib < 0 {
		panic(fmt.Sprintf("SNGL_PROBE_SWAP: unknown pass in %q", spec))
	}
	passes[ia], passes[ib] = passes[ib], passes[ia]
}
