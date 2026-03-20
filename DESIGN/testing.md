# SNGL Testing Framework

## Overview

Tests are written in `.sngl` files using the `test` keyword as a top-level declaration that targets a specific component. Test bodies use SNGL's existing statement syntax (assignments, toggles, event emission) plus the `assert()` function. A Go interpreter evaluates test bodies headlessly against component state.

## Syntax

```sngl
component counter {
    var count = 0
    button(text="+", key="inc", @click={ count += 1 })
    text(value="Count: {count}", key="display")
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

## DOM Access

Tests can inspect the rendered virtual DOM via the `root` variable. The DOM is re-rendered after every state mutation, so assertions always see the current state.

```sngl
test counter "dom inspection" {
    // root is the virtual DOM root node
    assert(root._children.length() == 2)

    // find elements by key
    assert(root._find("display").value == "Count: 0")
    assert(root._find("inc").text == "+")

    // trigger events on elements
    root._find("inc").@click()
    assert(count == 1)
    assert(root._find("display").value == "Count: 1")

    // access component vars via root
    assert(root.count == 1)
}
```

### DOM API

| Expression | Description |
|---|---|
| `root` | Virtual DOM root node for the component under test |
| `root._children` | List of child elements |
| `root._find("key")` | Recursively find element by `key` attribute |
| `elem.propName` | Access any prop on a rendered element (`value`, `text`, `disabled`, etc.) |
| `elem.@event()` | Trigger an event handler on the element |

## Built-in Functions

| Function | Description |
|---|---|
| `assert(expr)` | Fail the test if `expr` is falsy |
| `string(value)` | Convert to string |
| `int(value)` | Convert to int |
| `float(value)` | Convert to float |

## Built-in Methods

| Method | Applies to | Description |
|---|---|---|
| `.length()` | list, string, map | Length / size |
| `.push(value)` | list | Append to list |
| `.remove(index)` | list | Remove by index |
| `.contains(substr)` | string | Substring check |
| `._find(key)` | DOM element | Find child by key |
| `.@event()` | DOM element | Trigger event handler |

## Expressions

Test bodies support the full SNGL expression set:

- **Arithmetic:** `+`, `-`, `*`, `/`, `%`
- **Comparison:** `==`, `!=`, `<`, `>`, `<=`, `>=`
- **Logical:** `&&`, `||`, `!`
- **Ternary:** `x ? a : b`
- **Interpolation:** `"Count: {count}"`
- **List literals:** `[1, 2, 3]`
- **Struct literals:** `User{name: "World"}`
- **Field access:** `user.name`
- **Indexing:** `list[0]`

## Expected Errors

Tests can assert that a runtime error occurs by adding a `// ERROR(test)` comment:

```sngl
test items "out of bounds" { // ERROR(test) "out of range"
    assert(list[10] == 0)
}
```

The test passes if execution produces an error containing the quoted substring.

## CLI

```
sngl test                          # run all tests
sngl test app.sngl                 # run tests in specific file
sngl test --run "increments"       # filter by description
sngl test --verbose                # show === RUN lines
sngl test --format json            # JSON output for CI
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
- **Checker** (`internal/checker`): `CheckTests()` validates test references against component declarations. Type checking walks SNGL expression AST nodes directly.
- **Interpreter** (`codegen/platform/none/testrunner`): Tree-walking evaluator executes test bodies headlessly against component state
- **Platform test runners**: HTML platform runs tests via CDP (go-rod) in a browser; bubbletea platform runs tests via Go `testing` codegen
- **CLI** (`cmd/sngl/test.go`): Discovers files, merges sibling component definitions, runs tests

## JSON Output

`--format json` outputs a structured JSON object for CI integration:

```json
{
  "passed": true,
  "tests": 4,
  "failures": 0,
  "results": [
    {
      "name": "counter/increments",
      "passed": true,
      "duration_s": 0.001,
      "children": [
        {
          "name": "counter/increments/multiple increments",
          "passed": true,
          "duration_s": 0.0005
        }
      ]
    }
  ]
}
```

Failed tests include an `"error"` field with the assertion message. The exit code is nonzero on any failure.

## Future Work

- Platform-specific codegen for tests (compile tests to target language test frameworks)
