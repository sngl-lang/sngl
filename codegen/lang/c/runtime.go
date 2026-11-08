package c

import crt "duckfam.us/sngl/pkg/c"

// RuntimeHeader is the header every C file the translator writes includes.
const RuntimeHeader = crt.HeaderFile

// Runtime is the C runtime's files, by the name each is written under beside
// the program.
func Runtime() map[string][]byte {
	return map[string][]byte{crt.HeaderFile: crt.Header, crt.SourceFile: crt.Source}
}
