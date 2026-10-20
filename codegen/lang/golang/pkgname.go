package golang

// PackageName is the Go package a target's files declare. An executable build
// (`sngl run`, `sngl build`) is package main whatever the output block named,
// since Go links func main() nowhere else; main alone emits main() into the
// named package, for a program that calls it from a main of its own.
func PackageName(name string, main, executable bool) string {
	switch {
	case executable:
		return "main"
	case name != "":
		return name
	case main:
		return "main"
	}
	return "ui"
}
