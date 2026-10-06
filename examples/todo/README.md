# SNGL Todo Example

A todo app with JSON persistence, written in SNGL and compiled to
Go/Bubbletea. `todo.sngl` is the UI; `main.go` wraps the generated model in a
Go program that loads the list from `~/.sngl-todo.json` and saves it on exit.

## Run

```
go run ./examples/todo/
```

## Regenerate the model

`ui/model.go` is generated from `todo.sngl` and checked in:

```
go generate ./examples/todo/
```

## SNGL check (validates the source)

```
sngl check examples/todo/todo.sngl
```

`todo.sngl` also declares html, fyne, and android targets in its `output`
block, so the same source builds for those with `sngl generate --platform <name>`.
