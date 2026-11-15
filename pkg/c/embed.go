// Package c carries the C runtime every C program SNGL writes includes:
// runtime/sngl.h, runtime/sngl.c, runtime/sngl_codec.c and runtime/sngl_work.c. The Go here exists only so the compiler can reach those
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

// CodecSource is sngl_codec.c: JSON, a query and settings, compiled with
// every program, which links what it calls.
//
//go:embed runtime/sngl_codec.c
var CodecSource []byte

// WorkSource is sngl_work.c: the stacks async.start runs work on, compiled
// with every program.
//
//go:embed runtime/sngl_work.c
var WorkSource []byte

// I18nHeader and I18nSource are sngl:i18n's runtime over ICU, written
// beside a program that translates.
//
//go:embed runtime/sngl_i18n.h
var I18nHeader []byte

//go:embed runtime/sngl_i18n.c
var I18nSource []byte

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
	CodecFile      = "sngl_codec.c"
	WorkFile       = "sngl_work.c"
	I18nHeaderFile = "sngl_i18n.h"
	I18nSourceFile = "sngl_i18n.c"
	TestHeaderFile = "sngl_test.h"
	TestSourceFile = "sngl_test.c"
)
