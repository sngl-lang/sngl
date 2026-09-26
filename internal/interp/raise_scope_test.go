package interp

import "testing"

const raiseScopeSrc = `import . "sngl:ui"

func check(s string) {
    if s.length == 0 {
        error.raise("empty", "validation")
    }
}

component leaf(label string) node {
    var after = false
    button #go(text=label, @click {
        check("")
        after = true
    })
}

component shell node {
    var problem = "none"
    boundary(@error(e) {
        problem = "boundary: " + e.message
    }) {
        leaf(label="inside")
    }
    leaf(label="outside")
    text #shown(value=problem)
}

var caught = "none"

window #home(title="t", @error(e) {
    caught = "window: " + e.message
}) {
    vbox {
        shell()
        text #windowShown(value=caught)
    }
}
`

// TestRaiseInComponentReachesTheWindowRenderingIt: one declaration rendered
// under a boundary and beside it, in a window with an @error. Each instance's
// raise is caught by what is around that instance, and the handler that raised
// goes no further.
func TestRaiseInComponentReachesTheWindowRenderingIt(t *testing.T) {
	s := sessionFor(t, raiseScopeSrc, "")
	buttons := s.View().Find("go")
	if len(buttons) != 2 {
		t.Fatalf("#go resolved to %d nodes", len(buttons))
	}
	byLabel := map[string]*Node{}
	for _, b := range buttons {
		byLabel[b.Props["text"].(string)] = b
	}
	read := func(id string) any { return s.View().Find(id)[0].Props["value"] }

	if _, err := s.Invoke(byLabel["outside"].Key, "click"); err != nil {
		t.Fatalf("outside click: %v", err)
	}
	if got := read("windowShown"); got != "window: empty" {
		t.Errorf("window handler: caught = %v", got)
	}
	if got := read("shown"); got != "none" {
		t.Errorf("the boundary caught a raise from outside it: problem = %v", got)
	}

	if _, err := s.Invoke(byLabel["inside"].Key, "click"); err != nil {
		t.Fatalf("inside click: %v", err)
	}
	if got := read("shown"); got != "boundary: empty" {
		t.Errorf("boundary handler: problem = %v", got)
	}
}
