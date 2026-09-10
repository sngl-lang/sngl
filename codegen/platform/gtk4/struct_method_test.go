package gtk4

import "testing"

// A method on a user struct is emitted as the free `ReceiverMethod(this, …)`
// the Go translator spells at every call site -- Go has no receiver to hang
// one of those on, since the struct is emitted into the generated package
// alongside the Model.
//
// The func loop skipped everything carrying a receiver, so the definition was
// never written while the call sites still named it: examples/calculator,
// whose whole state machine is methods on one struct, emitted a model.go
// where every CalcDigit, CalcOperate and CalcEquals was undefined.
func TestAUserStructsMethodsAreEmitted(t *testing.T) {
	model := generateGTK4ModelBuilt(t, `
import . "sngl:ui"

struct Counter {
    n int = 0
    tag string = ""

    func bump() Counter {
        var next = this
        next.n = this.n + 1
        return next
    }

}

window {
    var c Counter

    func press() {
        c = c.bump()
    }

    button(text=c.tag, @click { press() })
}
`)
	buildGeneratedGo(t, "gtk4-struct-method-", model)
}
