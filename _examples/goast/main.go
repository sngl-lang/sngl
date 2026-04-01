package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

//go:generate go tool sngl compile --lang go --platform fyne --opt package=main app.sngl

func main() {
	a := app.New()
	w := a.NewWindow("Go AST Explorer")

	m := New()
	m.ParseGoAST = parseGoAST

	w.SetContent(m.BuildUI())
	w.Resize(fyne.NewSize(600, 800))
	w.ShowAndRun()
}
