package c

import crt "duckfam.us/sngl/pkg/c"

// RuntimeHeader is the header every C file the translator writes includes.
const RuntimeHeader = crt.HeaderFile

// Runtime is the C runtime's files, by the name each is written under beside
// the program.
func Runtime() map[string][]byte {
	return map[string][]byte{crt.HeaderFile: crt.Header, crt.SourceFile: crt.Source, crt.CodecFile: crt.CodecSource}
}

// TestRuntime is the test agent's files, by name, which a program built for
// `sngl test` is written with beside the runtime.
func TestRuntime() map[string][]byte {
	return map[string][]byte{crt.TestHeaderFile: crt.TestHeader, crt.TestSourceFile: crt.TestSource}
}

// TestHeader is the header a program built for `sngl test` includes.
const TestHeader = crt.TestHeaderFile
