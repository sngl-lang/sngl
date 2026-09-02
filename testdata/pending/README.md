# Fixtures waiting on an implementation

`testutil.TestdataSamples` globs `testdata/*.sngl` and does not recurse, so
nothing here is picked up by a harness. A fixture sits here when it pins
behaviour the compiler cannot yet produce.

That is not the same as a fixture that fails. Every harness that consumes
`testdata/` compiles every fixture in it for its own target — `codegen/platform/
android`'s `TestFixtures` runs the whole directory through Compose codegen — so a
fixture naming a construct no platform can emit does not fail its own assertion,
it takes the platform down with it. Fixture-first works up to the checker; past
it, the fixture has to wait for the lowering that erases the construct.

Move a file up to `testdata/` in the commit that makes it pass.

## Waiting on the `effect` lowering

`effect_lifecycle`, `effect_on_recreates`, `effect_reads_do_not_subscribe`,
`effect_tree_lifetime`, `effect_multiple`.

`sngl:app`'s `effect` is declared and checks, so these all pass `sngl check`.
Nothing runs `@mount` or `@unmount` yet, so each fails its assertions, and
android panics outright (`no composable for component "effect"`) rather than
reporting anything.

The sibling `testdata/error_effect_shape.sngl` is *not* here: it carries
`ERROR(check)` directives, which the platform harnesses skip, and the rules it
pins are the checker's.
