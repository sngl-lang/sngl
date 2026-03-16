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

Tests run in sngl code and do interactions, fire events, etc. Tests can be in any sngl file in the same package. sngl test functions similar to go test in CLI.
