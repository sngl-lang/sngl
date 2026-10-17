# `match`

Implemented on `feat/match`; what is left is at the end. The rules are in
CLAUDE.md's *Match* section and the specification's *The match statement*.

## Done

1. **Grammar**: `kw_match` (0x3E), `Match` in `Stmt` and `PrimaryExpr`,
   `MatchArm`; arms separated by a line break or `;`. Parser regenerated.
2. **AST**: `ast.Match`, `MatchArm`, `MatchStmt`, `MatchExpr`; builder,
   formatter (comments on arms, after `{`, before `}`), and every AST walk.
   `ast.BranchBlocks` replaces five hand-written if/for walks in the checker.
3. **Checker** (`internal/checker/match.go`): a statement becomes an
   if/else-if chain and an expression nested ternaries, so no later phase
   learns of a match -- there is no `ir.Match`, and no lowering pass, since
   every target already emits both. Exhaustiveness, duplicates, unreachable
   arms, arm form by position, constant patterns.
4. **Subject once**: bound to `__matchN` ahead of the statement in an
   imperative block or an expression body; read per arm elsewhere, and refused
   there if it writes state.
5. **Interpreter, optimizer, lowering, backends**: nothing to do -- each reads
   the if or ternary.
6. **Fixtures**: `testdata/match.sngl` (run on the interpreter),
   `error_match.sngl`, `error_match_impure_subject.sngl`; golden
   `testdata/match.txtar` on html, gtk4, fyne, bubbletea and android, with
   `deny` lines confirmed to fire when the behaviour is reverted.
7. **Editors**: tree-sitter grammar, corpus and highlights; neovim and vscode
   keywords; LSP semantic tokens and completion.
8. **Docs**: specification (grammar and prose, keyword list regenerated),
   effective SNGL's *Matching* (recommend `else` for a match that should
   survive the enum growing), CLAUDE.md.

## Left

1. **Rebase onto `origin/main`**: the branch was cut from a stale local `main`,
   208 commits behind. After it: regenerate `internal/parser/zparser.go`
   (`go generate ./internal/parser`), `docs/reference/specification.md`
   (`go generate ./docs`) and `editors/tree-sitter-sngl/src/` (tree-sitter
   0.25.10 `generate`), and re-run `go test . -run TestGolden/match -update`.
2. **Record `cmd/sngl/testdata/match_runs.txt`** with
   `go test ./cmd/sngl -run TestScript/match_runs -update`. On this base html
   fails `c.on == null` for a node in an untaken arm -- an existing gap in
   html's presence check for a plain `if` too, which `origin/main` has fixed
   (`codegen/refpresence.go`).
3. Delete this file.

## Later

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
