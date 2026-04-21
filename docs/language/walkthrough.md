<!-- This file is intended to become an interactive walkthrough. With top level # sections and secondary level ## sections for the examples. The final sngl code fence should be populated into an interactive playground on one side, with the remainder of the section off to the side, describing what is being learned. The lessons can be walked through with a drop-down to select any particular one. -->

# Basics

## Packages: Hello World

SNGL packages are a directory with one or more sngl files. Each sngl application must have one main package that defines component main. This serves as the entrypoint to the GUI, although you have you may generate the code, so you may call it directly. This is your most basic main component.

```sngl
component main {
    text(value="Hello, world!")
}
```

In this example, you're defining a component named "main" that renders a text node with the value "Hello, world!". The first thing you'll want do to is to run this code. There are a few options for doing that. The easiest is to use thej `sngl run` command to build your app in a temporary directly. 

```bash
sngl run example.sngl --target html
```

This will compile your application to HTML, then "launch" it by starting a static web server. You can view the result on http://localhost:8080. SNGL is a multi-platform language. If you'd instead like to launch it as an android app, you just need to change the target.

```bash
sngl run example.sngl --target android
```

This will take much longer and will require dependencies on your machine. See [platforms](TODO) for a list of supported platforms, their featuresets, requirements, and limitations. Since this example doesn't use any platform-specific features, it should deploy to any supported platforms.

## Data Tracking: Hello Anyone

Of course, static data doesn't make much of an interactive application. SNGL supports tracking state for an application as well.

```sngl
component main {
    var name = "world"
    vbox {
        text(value="Hello, {world}!")
        input(:value=name)
    }
}
```

This example is a little more complicated. We have a variable, which defines state that's tracked with the component. Components can each have their own state to help keep data management tidy. The `:value=name` syntax tells SNGL that the value is bidirectional, so input may update the value in addition to reading it. Note that the value of text doesn't need any annotation to tell it to update when name changes. SNGL tracks uses/assignments at compile time, so it can generate the updating logic directly. We also have string interpolation here. Any sngl expression may exist between braces in a string to interpolate the value.

## Computed Logic

Just replacing strings isn't all that useful. Often you need to do some simple computations for user feedback, validation, and even animations.

```sngl
component main {
    var name = "world"
    func isLong() => name.length > 3
    vbox {
        text(value="Hello, {world}!")
        input(:value=name)
        if isLong {
            text(value="Your name is is {name.length - 3} letters longer than Bob.")
        }
    }
}
```

Here we have a function definition serving the role of a computed value. Like node property expressions, they're updated whenever the values they reference change. We also have a conditional component here. Just wrap your node in an if statement. Also note here, that SNGL is strongly typed. See the [language reference](TODO) for a list of types that you can use, but usually SNGL can infer the type you want from the value, so we don't need to define that isLong is a bool.

## Compile-time: Constant expressions

Sometimes things should be evaluted at compile time, especially with limited targets like html. For example, the SNGL documentation site is written in SNGL, but we wanted to write the prose in markdown. We'll look at how to walk the filesystem later at compile time, but for now it's important to know that constant expressions are evaluated at compile time.This is something that happens automatically. SNGL tracks which functions are "pure" in a functional sense and if all arguments are constant, it can run the function at compile time.

In this example, name is a constant, so the text value will always be known at compile time. Note that complex types like colors, regex, URL, etc can be parsed and referenced at compile time.

```

component main {
  const name = "world"
  vbox {
    text(value="Hello, {world}! Your name has {name.length} letters.")
  }
}
```

## External Imports: Incorporating other langauges

External imports allow you to use code in other langauges. Non-constant expressions are compiled/translated/linked to the target language and used. Not all conbinations work, for example you can't import JavaScript code and call it in Rust. SNGL doesn't ship with a JavaScript runtime in Rust. :smile: However, constant expressions are evaluated at compile time, so any language that supports constant evaluation will work for any target. For the SNGL website, we use a Go markdown parser to translate our pages to HTML at compile time. The playground is dynamic and the compiler is written in Go, so SNGL imports the compiler and SNGL compiles it to WASM to generate the playground.

# Language Syntax

## Numeric Operations

- `op1 + op2` - Either adds to numeric (int/float) values or concatenates strings. Strings and numbers cannot be mixed.
- `op1 - op2` - Subtract numeric values
- `op1 * op2` - Multiply numeric values
- `op1 / op2` - Divide numeric values <!-- TODO: What happens on divide by zero? -->
- `op1 % op2` - Modulo integer values


