package html

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// stripUnreferencedIDsLoop is the implementation stripUnreferencedIDs
// replaced, kept as the oracle its single scan is checked against.
func stripUnreferencedIDsLoop(doc string, referenced map[string]bool, nextID int) string {
	for i := 0; i < nextID; i++ {
		id := fmt.Sprintf("$%d", i)
		if !referenced[id] {
			doc = strings.Replace(doc, fmt.Sprintf(` id="%s"`, id), "", 1)
		}
	}
	return doc
}

func TestStripUnreferencedIDsCases(t *testing.T) {
	cases := []struct {
		name       string
		doc        string
		referenced []string
		nextID     int
		want       string
	}{{
		name:   "drops an unreferenced allocated id",
		doc:    `<span id="$0">a</span>`,
		nextID: 1,
		want:   `<span>a</span>`,
	}, {
		name:       "keeps a referenced id",
		doc:        `<span id="$0">a</span>`,
		referenced: []string{"$0"},
		nextID:     1,
		want:       `<span id="$0">a</span>`,
	}, {
		// The loop only ever looked at ids below nextID, so an id above it
		// belongs to something else and is not ours to remove.
		name:   "keeps an id at or above nextID",
		doc:    `<span id="$4">a</span>`,
		nextID: 3,
		want:   `<span id="$4">a</span>`,
	}, {
		// fmt.Sprintf("$%d", 0) is "$0"; "$00" is nobody's allocated id.
		name:   "keeps a non-canonical spelling",
		doc:    `<span id="$00">a</span>`,
		nextID: 5,
		want:   `<span id="$00">a</span>`,
	}, {
		name:   "keeps a non-numeric id",
		doc:    `<span id="$x">a</span>`,
		nextID: 5,
		want:   `<span id="$x">a</span>`,
	}, {
		name:   "keeps a lowered ref id",
		doc:    `<span id="__n3">a</span>`,
		nextID: 5,
		want:   `<span id="__n3">a</span>`,
	}, {
		// strings.Replace(…, 1) removed one occurrence per id.
		name:   "drops only the first occurrence of a repeated id",
		doc:    `<a id="$1"></a><b id="$1"></b>`,
		nextID: 2,
		want:   `<a></a><b id="$1"></b>`,
	}, {
		name:   "leaves an unterminated attribute alone",
		doc:    `<span id="$1`,
		nextID: 5,
		want:   `<span id="$1`,
	}, {
		name:   "nothing allocated",
		doc:    `<span id="$0">a</span>`,
		nextID: 0,
		want:   `<span id="$0">a</span>`,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref := map[string]bool{}
			for _, id := range c.referenced {
				ref[id] = true
			}
			if got := stripUnreferencedIDs(c.doc, ref, c.nextID); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			if got := stripUnreferencedIDsLoop(c.doc, ref, c.nextID); got != c.want {
				t.Errorf("the oracle disagrees with the case: got %q, want %q", got, c.want)
			}
		})
	}
}

// The single scan must agree with the loop on documents nobody thought to
// write down, including the shapes the loop deliberately left alone.
func TestStripUnreferencedIDsMatchesTheLoop(t *testing.T) {
	values := []string{"$0", "$1", "$2", "$3", "$7", "$00", "$01", "$x", "$", "$12", "__n0", "__n1", "0", ""}
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20000; iter++ {
		nextID := rng.Intn(6)
		var b strings.Builder
		for n := rng.Intn(6); n > 0; n-- {
			switch rng.Intn(4) {
			case 0:
				b.WriteString("<div>")
			case 1:
				b.WriteString(" id=\"")
			default:
				fmt.Fprintf(&b, `<p id="%s">t</p>`, values[rng.Intn(len(values))])
			}
		}
		doc := b.String()
		ref := map[string]bool{}
		for _, v := range values {
			if rng.Intn(3) == 0 {
				ref[v] = true
			}
		}
		want := stripUnreferencedIDsLoop(doc, ref, nextID)
		if got := stripUnreferencedIDs(doc, ref, nextID); got != want {
			t.Fatalf("diverged on %q (nextID=%d, referenced=%v)\n got  %q\n want %q", doc, nextID, ref, got, want)
		}
	}
}
