# SNGL Static Analysis Tool — Design Document

## Purpose

The static analysis tool validates SNGL documents without producing any compiled output. It catches errors early, enforces conventions, and provides rich diagnostics for editors and CI pipelines. It is built on the compiler's parse and analyze phases using the `none/none` target.

## Relationship to the Compiler

The static analysis tool reuses the compiler's frontend:

```
SNGL Source → Parser → Checker → Diagnostics
```

No optimization or code generation occurs. The checker produces a fully typed AST and a list of diagnostics. The checker walks SNGL expression AST nodes directly using a custom type system (no external expression engine). The static analysis tool adds additional lint-style checks on top of the checker's type validation.

## Capabilities

### Type Checking

- Validate all SNGL expressions against their expected types by walking the AST
- Verify property types match component schemas (including string interpolation)
- Check that `var` initial values and `computed` expressions type-check
- Validate `param` default values match their declared types
- Ensure event handler expressions produce valid mutation targets
- Validate enum literals and special type literals (color, date, url, etc.)

### Dependency Analysis

- Build the reactive dependency graph for all `var` and `computed` values
- Detect circular dependencies
- Warn on unused bindings (declared but never referenced)
- Warn on write-only state (set in handlers but never displayed)

### Component Validation

- Verify all referenced components exist (builtins or user-defined)
- Check required properties are provided
- Flag unknown properties
- Validate `slot` usage in composite components (present when children are passed, absent when not)
- Check `for`/`if` attribute usage is well-formed

### Handler References

- Collect all handler references (`handlers.foo(...)`)
- Produce a manifest of required handler signatures
- If handler source is available (for a given language target), verify signatures match

### Structural Checks

- Exactly one `app` node exists
- No orphan nodes outside `app` or declarations
- Component definitions are not recursive
- `key` attributes are present on `for`-iterated nodes

## Linting Rules

Beyond correctness checks, the tool provides configurable lint rules:

| Rule              | Default | Description                                                                      |
| ----------------- | ------- | -------------------------------------------------------------------------------- |
| `no-unused-bind`  | warn    | Declared state is never read                                                     |
| `no-unused-var`   | warn    | `for` loop index/element variable is declared but never referenced               |
| `no-missing-key`  | error   | `for` loops without `key` attribute                                              |
| `no-complex-expr` | warn    | Expressions exceeding a complexity threshold (suggest moving to handler)         |
| `no-inline-style` | off     | Prefer `class` over inline `style.*`                                             |
| `require-id`      | off     | All interactive components must have an `id`                                     |
| `max-depth`       | warn(8) | Nesting depth limit                                                              |

Rules are configured in a project-level `.sngl-lint` file or via CLI flags.

## Output Formats

- **Human-readable** — colored terminal output with source context (default)
- **JSON** — structured diagnostics for editor integration
- **SARIF** — for CI/CD integration (GitHub Code Scanning, etc.)

## Editor Integration

The analysis pipeline is exposed to editors via the LSP (`sngl lsp`). See [tooling.md](tooling.md) for LSP capabilities, tree-sitter grammar, and editor configuration.

## CLI

```
sngl check app.sngl                      # check single file
sngl check --format=json app.sngl        # structured output
sngl check --strict app.sngl             # treat warnings as errors
sngl lint app.sngl                       # lint rules only
sngl lint --fix app.sngl                 # auto-fix where possible
```

## CI Integration

The tool exits with a nonzero status on errors, making it suitable for CI gates. A typical pipeline step:

```
sngl check --strict --format=sarif src/*.sngl > results.sarif
```

## Incremental Analysis

For large projects with many SNGL files, the analyzer supports incremental mode:

- Cache parsed ASTs and analysis results per file
- On change, re-analyze only the changed file and files that depend on its exported components
- Component libraries are analyzed once and cached
