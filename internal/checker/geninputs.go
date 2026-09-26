package checker

// checkGenInputs checks each `cache.inputs` directive pass1 found at the root
// of a file: the tree is an ordinary one, so a misspelled input kind is an
// unresolved name and a widget written there is a member of the wrong family,
// and every value in it is held constant by the const props each input
// declares.
//
// Nothing downstream reads the checked tree. The store holding a generated
// file reads the directive off the parsed source before any checking, since
// deciding whether the file is current is what decides whether to check it at
// all; this is what holds a hand-written or stored directive to the same
// vocabulary the store understands.
func (c *checker) checkGenInputs() {
	for _, vn := range c.genInputs {
		c.pushScope()
		c.checkVisualNodeIR(vn)
		c.popScope()
	}
}
