Build a testing framework into sngl.

```sngl
component main {
  var name = "World"

  vbox {
    input :value=name
    label value="Hello, {name}"
  }
}

test main "sample" {
  // ...statements
}
```

Tests are a series of statements run in sngl code in the context of a given component and do interactions, fire events, assert, etc. Tests can be in any sngl file in the same package. sngl test functions similar to go test in CLI. Tests run in a virtual DOM in sngl, but codegen may also generate tests for a given platform.
