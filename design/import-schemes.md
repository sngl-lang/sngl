# Import schemes

**A scheme is served by a compiled-in importer, by a library plugin, or by a
plugin a package declares** -- `gen.scheme(name="pc", @generate(out, importPath) { … })` at the root of a file (`lib/x/gen/scheme.sngl`). The
directive is recorded in pass1, checked in pass2 and taken off the package
body onto `ir.Package.Schemes` (`internal/checker/schemes.go`), so it is never
rendered; written anywhere else it is an error. The first two are *built in*,
and a plugin may not take one's name: a scheme is a dispatch key every import
of the build shares, not a name in scope. A library plugin lives at
`lib/x/scheme/<name>` and needs no import -- the layout is the registration.

**A package resolves what its own import closure declares, in two phases.**
pass1 registers every import before any declaration, and one whose scheme
nothing built in serves is deferred (`deferImport`); once the rest have
resolved, each deferred import resolves against the schemes the imported
packages declare and reach (`resolveDeferredImports`), round after round,
since what a plugin generates may declare another. So where the plugin's
import is written -- below its use, in another file, in a library the package
imports -- does not matter, while a sibling package reaching neither cannot
use it. What is left is reported once the package's own schemes are known: one
it declares itself ("a plugin may not use a scheme it declares"), or one
nothing serves. Two plugins in a closure declaring one name are an error at
the import that brings the second. A cycle of plugins is a directory import
cycle, reported where it closes, and the scheme it left waiting is not
reported again. No registry outlives a check: the schemes hang off the IR, the
library plugins loaded off the check's `libCache`.

**The handler runs in the interpreter, through the build host**
(`internal/plugin`, reached through `build.Resolver.GenerateScheme`; a
resolver that is not a `checker.SchemeRunner` resolves no plugin scheme). Its
calls are gated by the plugin's origin -- a library plugin is trusted -- and
recorded, `out.write(name, src)` writes one `.sngl` file of the package and
records nothing, and a raise that leaves the handler is the import's error. The
output is checked as a package with no directory, so it may not import by
path, and is stored as `sngl.scheme` (see *Performance*). Fixtures:
`cmd/sngl/testdata/scheme_order.txt`, `scheme_errors.txt`,
`scheme_store.txt`, `c_pkgconfig.txt`, and `testdata/c_link.txtar`.

**`c:` declares nothing a program calls.** `c:pkg:<library>/<header>` asks
pkg-config for flags and includes `<header>`; anything else is a header
included as written. What it writes is one `c.link`, and the program declares
each function with `#[cnative]` -- GTK's headers take a C parser minutes, which
is why the Go importer that parsed them went rather than moved. A `#[cnative]`
call returning an int or a float is converted to Go's type at the call, as its
arguments are to C's.
