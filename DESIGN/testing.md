# SNGL Testing Tool — Design Document

## Purpose

The SNGL testing tool provides automated UI testing for SNGL applications, similar in function to Cypress. Tests are written as KDL documents using CEL expressions for assertions and interactions. Tests run against compiled SNGL applications or against the `none/none` preview renderer for layout and binding verification without a full platform runtime.

## Design Principles

- **KDL-native** — Tests are KDL documents, not code in a target language. This keeps tests portable across all compilation targets.
- **CEL for logic** — Assertions, selectors, and conditions use CEL expressions, the same language used in SNGL templates.
- **Declarative** — Tests describe what to do and what to expect, not how to drive the UI at a low level.
- **Target-independent** — The same test suite runs against any compiled target (Go/Gio, TypeScript/Web, etc.) or the headless preview renderer.

## Test Document Structure

```kdl
test "user can log in" {
    setup {
        // initial state
        set user.loggedIn false
        set user.name "Alice"
    }

    step "click login button" {
        click "#loginBtn"
        assert (cel)"user.loggedIn == true"
    }

    step "greeting is displayed" {
        assert-text "#greeting" (cel)"'Hello Alice'"
        assert-visible "#greeting"
    }
}

test "counter increments" {
    setup {
        set count 0
    }

    step "click increment" {
        click "#incBtn"
        assert (cel)"count == 1"
    }

    step "click three more times" {
        repeat 3 {
            click "#incBtn"
        }
        assert (cel)"count == 4"
    }
}
```

## Test Nodes

### `test`

Top-level node declaring a single test case.

```kdl
test "<description>" { ... }
```

### `setup`

Optional initialization block. Runs before the first step. Sets initial state values for `bind` declarations.

```kdl
setup {
    set <binding> <value | cel-expr>
}
```

### `step`

A named sequence of actions and assertions. Steps execute in order. A failing assertion in any step fails the test.

```kdl
step "<description>" { ... }
```

### Actions

Actions simulate user interaction:

| Action | Syntax | Description |
|--------|--------|-------------|
| `click` | `click "<selector>"` | Click a component |
| `type` | `type "<selector>" "<text>"` | Type text into an input |
| `clear` | `clear "<selector>"` | Clear an input's value |
| `scroll` | `scroll "<selector>" x=0 y=100` | Scroll a container |
| `set` | `set <binding> <value \| (cel)"expr">` | Directly set a bound value (bypass UI) |
| `wait` | `wait <milliseconds>` | Pause execution (for async handlers) |
| `trigger` | `trigger "<selector>" "<event>"` | Fire an arbitrary event |

### Assertions

Assertions verify UI state using CEL expressions:

| Assertion | Syntax | Description |
|-----------|--------|-------------|
| `assert` | `assert (cel)"<expr>"` | General boolean assertion |
| `assert-text` | `assert-text "<selector>" (cel)"<expr>"` | Component's text content matches |
| `assert-visible` | `assert-visible "<selector>"` | Component is rendered and visible |
| `assert-hidden` | `assert-hidden "<selector>"` | Component is not rendered or hidden |
| `assert-prop` | `assert-prop "<selector>" "<prop>" (cel)"<expr>"` | Property value matches |
| `assert-count` | `assert-count "<selector>" <number>` | Number of matching components |
| `assert-layout` | `assert-layout "<selector>" x=.. y=.. w=.. h=..` | Computed layout bounds (approximate) |

### Selectors

Components are selected by:

- **ID** — `"#loginBtn"` matches `id="loginBtn"`
- **Component type** — `"button"` matches all `button` nodes
- **Class** — `".primary"` matches `class="primary"`
- **Compound** — `"vbox > button"` matches `button` nodes that are direct children of `vbox`
- **Index** — `"button[0]"` matches the first button
- **Attribute** — `"button[text='Save']"` matches by attribute value

## Execution Modes

### Headless (none/none)

Tests run against the compiler's analyzed AST and the preview renderer (Yoga layout + CEL evaluation). No actual platform runtime is involved.

- Fast — no GUI process to start
- Validates: layout, bindings, computed values, CEL expressions, component structure
- Cannot validate: platform-specific rendering, native event behavior, handler implementations

This is the default for CI and the primary mode during development.

### Platform Runner

Tests run against a compiled application on a real platform. The test runner communicates with the running application via an instrumentation protocol:

```
Test Runner ◄──── instrumentation protocol ────► Running Application
```

The compiler emits a thin instrumentation layer when building in test mode (`sngl compile --test`). This layer exposes:

- Component tree queries (selectors)
- Property reads
- Event injection (click, type, etc.)
- State reads and writes
- Layout geometry queries

Each platform backend implements this instrumentation layer using platform-appropriate mechanisms (e.g., accessibility APIs, debug bridges, or an embedded RPC server).

### Visual Regression

When running against the platform runner, tests can capture screenshots for visual comparison:

```kdl
step "verify layout" {
    snapshot "login-screen"
}
```

Snapshots are stored as baseline images. Subsequent runs diff against baselines and flag regressions beyond a configurable threshold.

## CEL Test Extensions

The CEL environment in tests is extended with test-specific functions:

| Function | Description |
|----------|-------------|
| `text(selector)` | Returns the text content of a component |
| `prop(selector, name)` | Returns a property value |
| `visible(selector)` | Returns boolean visibility |
| `count(selector)` | Returns number of matching components |
| `layout(selector)` | Returns layout rect `{x, y, w, h}` |

These allow complex assertions:

```kdl
assert (cel)"count('button') > 0 && text('#greeting').contains('Alice')"
```

## Test Organization

### Suites

Multiple test files can be grouped into suites. The runner discovers test files by convention:

```
app.kdl                  # source
app.test.kdl             # tests for app.kdl
components/
    card.kdl
    card.test.kdl
```

### Tags

Tests can be tagged for selective execution:

```kdl
test "slow integration test" tags=["integration", "slow"] { ... }
```

```
sngl test --tag=integration
sngl test --exclude-tag=slow
```

## CLI

```
sngl test                               # run all tests, headless
sngl test app.test.kdl                  # run specific file
sngl test --platform=gio                # run against Gio platform runner
sngl test --tag=smoke                   # run tagged subset
sngl test --snapshot-update             # update visual baselines
sngl test --format=json                 # structured output
sngl test --parallel=4                  # parallel execution
```

## Output

- **Terminal** — pass/fail per test with step-level detail on failure, source location of failing assertion
- **JSON** — structured results for CI integration
- **JUnit XML** — for CI systems that consume JUnit format

## Failure Diagnostics

On failure, the test runner reports:

- The failing assertion's CEL expression and its evaluated value
- The component tree state at the point of failure
- The reactive state (all `bind` and `computed` values)
- For platform runner mode: a screenshot at the point of failure
