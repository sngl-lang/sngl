package lower

import "git.duckfam.us/jonathan/sngl/ir"

var passCanvas = pass{
	name:    "Canvas",
	enabled: func(c Caps) bool { return c.Canvas },
	apply:   lowerCanvas,
}

func lowerCanvas(pkg *ir.Package, _ Caps, _ Options) error {
	// TODO: implemented in Task 7
	return nil
}
