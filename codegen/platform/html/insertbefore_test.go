package html

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
)

// The capability and the implementation must agree. Declaring one without the
// other is a bug no compile catches: a capability with nothing to answer it
// emits an op that falls through to OnDefault and is rendered as a call to a
// function nothing declares, and an implementation with no capability is a
// method lowering never reaches.
func TestInsertBeforeCapabilityMatchesTranslator(t *testing.T) {
	_, implements := any((*htmlTranslator)(nil)).(codegen.ChildInserter)
	declared := (&Generator{}).Capabilities(&javascript.Translator{}).InsertBefore
	if declared != implements {
		t.Errorf("Features.InsertBefore = %v but htmlTranslator implements codegen.ChildInserter = %v; declare both or neither", declared, implements)
	}
}
