package fynehost

// The widget registry is generated from the Spec records in fyne.sngl, because
// naming those constructors here as well would be naming them twice and the two
// would drift. See internal/cmd/genfynehost.
//
//go:generate go run git.duckfam.us/jonathan/sngl/internal/cmd/genfynehost -o registry_gen.go
