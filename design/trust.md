# Trust

**A malicious repository may not reach the host.** Running any `sngl`
command in a clone must not run a process, read the environment or read
outside the project on the repository's say-so (`internal/trust`). Two kinds of code ask, and they are gated
differently because they reach differently:

- **An evaluated go: or js: package** is native code with the user's
  privileges, so the grant is per package and total: `--allow-eval="go:<path>"`.
  It is asked only of a store *miss* (`gateEval`, after the lookups in
  `execConstEval`): a stored value was computed by a run that was allowed.
  A refused call does not fold. On a target that calls the scheme at run
  time it is emitted as a call and `Config.Warnings` says so once per
  package (`reportRefused`), which `build.Result.Warnings` carries and the
  CLI prints beside the checker's; on one that cannot, it is the error.
- **A build-time SNGL function** reaches the host only through
  `sngl:x/gen`'s `lines`, `exists`, `files`, `env` and `exec`, intrinsics
  marked `build` that only the build host (`internal/buildhost`) answers, in
  a const func the optimizer folds or in a scheme's handler (see *Import
  schemes*) -- a call left unfolded is refused by `refuseBuildOnlyCalls`. Each call is gated by the package that
  wrote it (the interpreter keeps the frame stack for that): inside the
  import root (`Config.Dir`, symlinks resolved) or the package's own
  directory reads unasked, anything else needs `--allow-file`/`--allow-dir`,
  and env and exec always need a grant. `gen.exec` holds every call to its
  declared prefix and banned flags whatever was granted. Every read is
  recorded, so a fold that reaches the host is a producer (`produce.go`):
  stored as `sngl.eval` under its package closure's digest, the files it read
  hashed as they streamed. A scheme's handler is gated and recorded the same
  way, and a refusal there is an error at the import that ran it.

- **A fetch** -- a `git:` or `http:` import of a package its cache does not
  hold -- contacts a host the repository chose, so the importer asks before
  any request (`codegen.NetworkScheme`'s `ResolveFSNet`, with a `NetGate`
  `build.Resolver` builds from its `Trust`), a redirect's host included:
  git is run with `http.followRedirects=false`, and the http client's
  `CheckRedirect` asks. A cache hit asks nothing. The grant is a host, exact
  or `*.<domain>` for what is below it and not it, and belongs to the project
  -- the import root -- or, recorded under `trust.everywhere`, to every
  project. The go command's own fetches (modules, a toolchain) are not
  gated: once a plugin runs it as a command sngl cannot see them, and the
  go command is trusted as a command. `sngl pkg`, which fetches because it
  was asked to, grants itself the network.

A grant comes from a flag (an import path or an origin, one invocation),
`SNGL_ALLOW` (`;`-separated, origins only, no `all`) or the user's
`os.UserConfigDir()/sngl/trust.sngl` (`sngl:x/gen/trust`, written by `sngl trust` and the terminal prompt, never read from the project). A recorded grant
names where code came from -- `dir:<path>` (with the go.mod module path for a
Go package), `go:<module>@<version>`, a fetched URI and its digest -- because
an import path is a name a repository chooses. `SNGL_ALLOW` is unset in
`setupTrust`, so no child sngl starts inherits it. **A nil `*trust.Policy`
refuses everything**: a caller that never thought about trust runs nothing a
repository brought, which is why the golden harness, docsgen and
docbrowser's `go:generate` each name their grant. A library package is
trusted.

**The LSP resolves imports, and runs plugins, under the same gate** -- the
config file's grants, the flags' and `SNGL_ALLOW`'s, and never a prompt
(`lsp.NewWithTrust`). It logs each grant the last two gave at startup, so one
an editor's workspace settings handed it is visible, and a refused call is a
diagnostic at the import that reached the plugin.

**The project is read through an `os.Root`** (`build.ProjectFS`), so a
symlink inside the import root that points out of it is refused wherever a
scheme reads the project through the FS it is handed -- `md:`, `js:`'s
node_modules -- and `file:` resolves and reads its assets through one. A
directory import is the exception, read as `../x` is, since only what code
*reads* is held to the root.
