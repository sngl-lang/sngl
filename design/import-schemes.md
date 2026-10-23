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
recorded, `out.file(name)` opens one `.sngl` file of the package and records
nothing, and a raise that leaves the handler is the import's error. The
output is checked as a package with no directory, so it may not import by
path, and is stored as `sngl.scheme` (see *Performance*). Fixtures:
`cmd/sngl/testdata/scheme_order.txt`, `scheme_errors.txt`,
`scheme_store.txt`, `c_pkgconfig.txt`, and `testdata/c_link.txtar`.

**`out.note(message)` is a warning at the import** -- what a handler left
out, a name it changed -- given on every check, a replay from the store
included: the notes are stored ahead of the files (`// sngl:note`), and
`SchemeRunner.GenerateScheme` hands them back beside the documents. A failing
check prints its warnings before its error (`build.Check`), since a note is
usually why a read of the package fails.

**A handler's relative read inside the project goes through the project's
FS** (`Host.ReadThrough`, from `Resolver.FS`): on disk that is an `os.Root`,
so a symlink out of the project is refused where it is opened, and the file
is recorded at its place under the import root; in memory -- the golden
harness's `MapFS` -- nothing is recorded and the output is not stored, since
no store could read the file again.

**What a SNGL-writing scheme's package declares is the program's own**
(`lower.userPackages`): its structs are declared and its functions emitted
like a directory package's, `sngl:` and a native scheme's package excepted.

**`c:` declares nothing a program calls.** `c:pkg:<library>/<header>` asks
pkg-config for flags and includes `<header>`; anything else is a header
included as written. What it writes is one `c.link`, and the program declares
each function with `#[cnative]` -- GTK's headers take a C parser minutes, which
is why the Go importer that parsed them went rather than moved. A `#[cnative]`
call returning an int or a float is converted to Go's type at the call, as its
arguments are to C's.

## The `openapi:` scheme

**`import pets "openapi:./petstore.yml"` writes a package from an OpenAPI 3
document**, YAML or JSON: a declaration per schema, a `…Call` function per
operation answering `async.Result`, a component over `async.query` per `GET`,
and `server`, a context defaulting to the spec's first server
(`lib/x/scheme/openapi`). The path is the project's own file, read through
the project; one outside it is refused before anything is read.

**Go reads the document and writes the package.** The SNGL handler reads
the file -- its lines joined once, since `s = s + line` in a loop copies the
string every time -- and
hands it to `openapi.generate` (`codegen/scheme/openapi`), which answers the
file, its source and the notes. `Parse` reads the document into a `Spec`,
every reference resolved, every name a SNGL identifier and every construct
with no declaration left out with a note; `Write` writes the source and
prints it through `parser.Format`, so it reads as `sngl fmt` writes it and a
mistake there is a parse error rather than a package saying something else.
An object is a struct, a string enum an enum, a property neither
required nor nullable, or nullable (`nullable: true`, `type: [T, 'null']`,
`oneOf: [$ref, {type: 'null'}]`) an option, an array a list,
`additionalProperties` of a schema a `map[string, V]`, `allOf` its parts'
fields merged, and an inline object or enum a declaration named for where it
stands. An integer is `int` whatever its format -- the sized ints mix with
nothing -- and a `date`/`date-time` string `time.date`/`time.datetime`. A
key that is no SNGL name is camelCased, carrying `#[json.identifier]`; a
query parameter likewise carries `#[http.identifier]` on a declared
`<Op>Query` struct, and the query is an anonymous literal where every name
is its own. Names collide into a `2` -- two differing only in their first
letter's case are one, since Go exports both -- a keyword takes a `_`, and a parameter
named like an adapter's own prop (`value`, `pending`, `failure`, `load`,
`fail`) or like `server` is renamed; each is a note.

**Left out, and said**: an `anyOf`, a `oneOf` that is not one schema or
null, a `discriminator` (a payload enum once sngl:json reads that layout), a
map of any value, an enum of anything but strings, a `$ref` outside the
document -- each leaves out the field reaching it, or the schema, and
whatever names that -- and a header or cookie parameter, a body that is not
JSON or a success with no JSON, which leave out the operation. A `GET` with a
list parameter keeps its function and has no adapter, since a list cannot be
an effect's key. A Swagger 2 or OpenAPI 4 document, and text that is not
YAML, are errors at the import.

**What the package composes** (`testdata/openapi_petstore.txtar`, its
`written` package shown in the archive): each function is
`_answered(http.get(…))` -- the package's one helper, the response where it
arrived with a 2xx status -- then `json.decode[T]` of its body. The
codec refuses a type parameter, so there is no generic `getJSON[T, Q]`: the
decode is written per operation. The URL is `"{server}…"` with each path
parameter escaped by `http.pathEscape`, so a provider of `server` around a
component reaches its request. The adapter's key is its one parameter, with
the function as `fetch`, or an anonymous struct of several with a lambda
spreading it back out; it renders nothing, so its family is `F`.

An importer believes the document, so where a published document disagrees
with its API's answers the correction belongs in the document: `examples/pokedex`
vendors a whole one and lists each correction in its README.

The runs are `cmd/sngl/testdata/openapi_runs.txt`, against the script
harness's server on all six targets; the refusals
`cmd/sngl/testdata/error_openapi_*.txt`, the renames `openapi_renamed.txt`
and the store `openapi_store.txt`.
