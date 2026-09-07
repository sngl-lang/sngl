# What a timer's position does not do

`timer` is the one builtin node the checker deletes. `BuiltinEffect` is
validated and then left to the ordinary component path, so an `effect` stays a
node in the body and its enclosing `if`/`for` chain reaches lowering. `timer`
returns `nil` from that same switch and is appended to a flat
`Component.Timers` / `Package.Timers` instead (`internal/checker/expr.go`,
`internal/checker/checker.go`), so its position is gone before any pass or
backend can read it.

Two consequences, both reproduced by running `sngl test` — not by reading the
source. They are recorded here rather than in `testdata/` because the repo has
no directive for a fixture that is expected to fail: `internal/testutil/sample.go`
knows `ERROR`, `FOLD`, `NOFMT` and `SKIP(codegen)`, and none of them means
"known broken". `SKIP(codegen)` would be worse than useless here — these
assertions are evaluated by the interpreter, which is the `none` platform, so
skipping codegen would silence exactly the thing that proves the point and
leave a green fixture asserting nothing.

Land these as ordinary fixtures in the commit that makes them pass.

## A timer in a component other than the root never fires

`interp.NewTimers` builds a schedule from `env.Comp.Timers` for the session's
component alone, and `codegen.AnalyzeCommon` collects `pkg.Timers` plus
`main`'s. A timer anywhere else is checked, type-correct, and inert.

```sngl
import . "sngl:ui"
import . "sngl:time"
import . "sngl:app"
import . "sngl:test"

output {
    none { html }
}

var childTicks = 0

func testChildTimerFires(t Test, c main) {
    t.tick()
    t.assert(c.outer == 1)
    t.assert(childTicks == 1)
}

component ticker(n int) {
    timer(interval=100ms, enabled=true, @tick {
        childTicks += 1
    })

    text(value=string(n))
}

component main {
    var outer = 0

    timer(interval=100ms, enabled=true, @tick {
        outer += 1
    })

    ticker(n=1)
    text(value=string(outer))
}
```

```
--- FAIL: main/testChildTimerFires (0.00s)
    wanted 1, got 0 (childTicks)
```

The root's timer fires. The child's does not.

## A timer under a false `if` fires anyway

The hoist happens while the body is being checked, so the branch the node was
written in is not recorded anywhere. The timer runs as though it were at
component top level.

```sngl
import . "sngl:ui"
import . "sngl:time"
import . "sngl:app"
import . "sngl:test"

output {
    none { html }
}

// The `if` is false, so the timer is not in the tree and must not fire.
func testTimerInDeadBranchDoesNotFire(t Test, c main) {
    t.tick()
    t.assert(c.n == 0)
}

component main {
    var (
        show = false
        n = 0
    )

    if show {
        timer(interval=100ms, enabled=true, @tick {
            n += 1
        })
    }

    text(value=string(n))
}
```

```
--- FAIL: main/testTimerInDeadBranchDoesNotFire (0.00s)
    wanted 0, got 1 (c.n)
```

Both contradict shipped prose: `docs/learn/tour.md` says timers compose with
`if` blocks cleanly, and `docs/reference/specification.md` says a timer is torn
down when its component is removed.

## The same two questions asked of `effect` both pass

This is the payoff of the rewrite, on the harness that reproduces the defects.
Nothing here is a fixture to land — it already passes — but it is the evidence
that an effect's position is what a timer's is missing.

```sngl
component leaf() {
    effect(@mount {
        childMounts += 1
    })

    text(value="leaf")
}

component main {
    var (
        show = false
        mounted = 0
    )

    if show {
        effect(@mount {
            mounted += 1
        })
    }

    leaf()
    text(value=string(mounted))
}
```

```
--- PASS: main/testEffectInDeadBranchDoesNotMount (0.00s)
--- PASS: main/testChildEffectMounts (0.00s)
```

## Two more, found by generating rather than by testing

Neither is about position, and neither has a golden. Both come from the same
place: `CommonAnalysis.TimerInfo` reduces `ir.Timer.Enabled` to `ActiveVar`, a
bare string, which is empty whenever `enabled` is anything but an identifier.

- **gtk4 emits no timer at all.** Generating a program with a timer for
  `go/gtk4` produces a `model.go` in which the tick body appears nowhere; the
  only mentions of the mutated var are its getter and setter. The timer is
  dropped silently and the build succeeds.

- **android emits invalid Kotlin for a literal `enabled`.** `enabled=true`
  gives

  ```
  LaunchedEffect() {
      while () {
          delay(100L)
          n += 1
      }
  }
  ```

  `while ()` is a syntax error and `LaunchedEffect` with no keys does not
  compile either. `enabled=running` — a bare var — is the only form that works.
  bubbletea guards this case; android does not, and no golden covers an android
  timer.
