// Package c carries the C runtime every C program SNGL writes includes:
// runtime/sngl.h and runtime/sngl.c. The Go here exists only so the compiler can reach those
// bytes, which the C translator writes beside the program.
package c

import _ "embed"

// Header is sngl.h, which the program includes by HeaderFile.
//
//go:embed runtime/sngl.h
var Header []byte

// Source is sngl.c, compiled with the program.
//
//go:embed runtime/sngl.c
var Source []byte

// TestHeader and TestSource are the test agent a program built for `sngl
// test` runs in, written beside it then.
//
//go:embed runtime/sngl_test.h
var TestHeader []byte

//go:embed runtime/sngl_test.c
var TestSource []byte

// The names the runtime is written under, beside the program.
const (
	HeaderFile     = "sngl.h"
	SourceFile     = "sngl.c"
	TestHeaderFile = "sngl_test.h"
	TestSourceFile = "sngl_test.c"
)
