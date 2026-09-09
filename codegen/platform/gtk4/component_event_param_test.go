package gtk4

import (
	"strings"
	"testing"
)

// TestAComponentEventHandlerKeepsItsPayloadParameter compiles the Go emitted
// for testdata/type_params_on_components.sngl, whose `job<T>(seed T, @done T)`
// is subscribed to as `@done(v) { got = v }`.
//
// emitIRPromotedHandler declared every promoted handler with no parameters,
// which is right only for one caller: the GTK trampoline, which is
// (instance, user_data) returning void. An event no GTK signal answers to is
// not connected to a trampoline at all -- OnAttachHandler emits nothing for it
// -- and the payload reaches the handler as the argument its declaration
// names. Dropping it left `func (m *Model) __n0_done_handler() { m.got = v }`,
// where v is undefined.
//
// Both sides now ask signalFor, so the signature and the wiring cannot
// disagree about whether this handler is a trampoline's.
func TestAComponentEventHandlerKeepsItsPayloadParameter(t *testing.T) {
	model := generateGTK4Model(t, fixtureSource(t, "type_params_on_components.sngl"))

	// The handler is a method on the instance record now: `main` is an
	// ordinary component under the window that renders it, rather than the
	// Model it used to be by harness convention.
	const want = "func (c *MainInstance) __n0_done_handler(v int) {"
	if !strings.Contains(model, want) {
		t.Errorf("emitted Go missing %q\n--- model.go ---\n%s", want, model)
	}
	buildGeneratedGo(t, "gtk4-event-param-", model)
}
