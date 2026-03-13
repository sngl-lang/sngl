# SNGL Static Analysis Tool — Design Document

## Purpose

The static analysis tool validates SNGL KDL documents without producing any compiled output. It catches errors early, enforces conventions, and provides rich diagnostics for editors and CI pipelines. It is built on the compiler's parse and analyze phases using the `none/none` target.

## Relationship to the Compiler

The static analysis tool reuses the compiler's frontend:

```
KDL Source → Parser → Analyzer → Diagnostics
```

No IR lowering or emission occurs. The analyzer produces a fully typed, dependency-resolved AST and a list of diagnostics. The static analysis tool adds additional lint-style checks on top of the compiler's type checking.

## Capabilities

### Type Checking

- Validate all `(cel)` type-annotated expressions against their expected types
- Verify property types match component schemas
- Check that `bind` initial values and `computed` expressions type-check
- Ensure event handler expressions produce valid mutation targets
- Validate protobuf message field references

### Dependency Analysis

- Build the reactive dependency graph for all `bind` and `computed` values
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
| `no-complex-cel`  | warn    | `(cel)` expressions exceeding a complexity threshold (suggest moving to handler) |
| `no-inline-style` | off     | Prefer `class` over inline `style.*`                                             |
| `require-id`      | off     | All interactive components must have an `id`                                     |
| `max-depth`       | warn(8) | Nesting depth limit                                                              |

Rules are configured in a project-level `.sngl-lint.kdl` file or via CLI flags.

## Output Formats

- **Human-readable** — colored terminal output with source context (default)
- **JSON** — structured diagnostics for editor integration
- **SARIF** — for CI/CD integration (GitHub Code Scanning, etc.)

## Editor Integration

The analysis pipeline is exposed to editors via the LSP (`sngl lsp`). See [tooling.md](tooling.md) for LSP capabilities, tree-sitter grammar, and editor configuration.

## CLI

```
sngl check app.kdl                      # check single file
sngl check --format=json app.kdl        # structured output
sngl check --strict app.kdl             # treat warnings as errors
sngl lint app.kdl                       # lint rules only
sngl lint --fix app.kdl                 # auto-fix where possible
```

## CI Integration

The tool exits with a nonzero status on errors, making it suitable for CI gates. A typical pipeline step:

```
sngl check --strict --format=sarif src/*.kdl > results.sarif
```

## Incremental Analysis

For large projects with many KDL files, the analyzer supports incremental mode:

- Cache parsed ASTs and analysis results per file
- On change, re-analyze only the changed file and files that depend on its exported components
- Component libraries are analyzed once and cached
