// Package consteval carries the JavaScript runtime for SNGL's compile-time
// evaluation of pure js: functions. The runtime itself is consteval.mjs; the
// Go here exists only so the compiler can reach those bytes.
//
// Unlike pkg/go/consteval, which the generated program imports by module path,
// this one is written into the scratch directory beside the program that uses
// it — a generated .mjs has no stable specifier to import from. It is embedded
// rather than copied under codegen/ so that the runtime and the program that
// runs it cannot drift apart.
package consteval

import _ "embed"

// OutEnv names the environment variable holding the path the runtime's flush
// writes to. It must match outEnv in consteval.mjs.
const OutEnv = "SNGL_CONSTEVAL_OUT"

// Runtime is the JS runtime source, written next to the generated program.
//
//go:embed consteval.mjs
var Runtime []byte

// RuntimeFile is the name to write Runtime under; the generated program
// imports it by this name.
const RuntimeFile = "consteval.mjs"
