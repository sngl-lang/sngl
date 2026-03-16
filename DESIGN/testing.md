# SNGL Testing Framework

## Overview

Tests are written in `.sngl` files using the `test` keyword as a top-level declaration that targets a specific component. Test bodies use SNGL's existing statement syntax (assignments, toggles, event emission) plus the `assert()` function. A Go interpreter evaluates test bodies headlessly against component state.

## Syntax

```sngl
component counter {
    var count = 0
    button(text="+", @click={ count += 1 })
    text(value="Count: {count}")
}

test counter "increments" {
    assert(count == 0)
    count += 1
    assert(count == 1)

    test "multiple increments" {
        count += 5
        assert(count == 6)
    }
}

test counter "starts fresh" {
    assert(count == 0)
}
```

### Rules

- **Top-level**: `test <ComponentName> "<description>" { ... }`
- **Nested subtests**: `test "<description>" { ... }` — inherits a snapshot of parent state
- **Body**: SNGL statements (assign, toggle, emit) + `assert(expr)` calls
- Each top-level test gets fresh component state initialized from `var` defaults
- Tests targeting `main` use document-level `var`/`computed`/`const` declarations

## CLI

```
sngl test                          # run all tests
sngl test app.sngl                 # run tests in specific file
sngl test --run "increments"       # filter by description
sngl test --verbose                # show === RUN lines
```

## Output

```
--- PASS: counter/increments (0.00s)
--- PASS: counter/increments/multiple increments (0.00s)
--- PASS: counter/starts fresh (0.00s)
PASS
ok   3 tests, 0 failures
```

On failure:
```
--- FAIL: counter/increments (0.00s)
    assert(count == 1) failed — got false
```

## Architecture

- **Parser** (`internal/snglparser`): `test` is a keyword; `parseTestDef()` produces `ast.TestDef` nodes
- **Checker** (`internal/checker`): `CheckTests()` validates test references against component declarations
- **Interpreter** (`internal/testrunner`): Tree-walking evaluator (`Env.Eval`, `Env.Exec`) executes test bodies headlessly
- **CLI** (`cmd/sngl/test.go`): Discovers files, merges sibling component definitions, runs tests

## Future Work

- Platform-specific codegen for tests (compile tests to target language test frameworks)
- `--format json` output for CI integration
- Event simulation (`@click`, `@input`) beyond simple state manipulation
