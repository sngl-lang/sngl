package parser

import "testing"

func TestFormatI18nFull(t *testing.T) {
	assertFormat(t, `var x = $"Login"`, `var x = $"Login"`)
}

func TestFormatI18nInterp(t *testing.T) {
	assertFormat(t, `var x = $"Hello {name}!"`, `var x = $"Hello {name}!"`)
}

func TestFormatI18nPlural(t *testing.T) {
	src := `var x = $"You have {count, plural, one{message} other{messages}}"`
	assertFormat(t, src, src)
}

func TestFormatI18nFormatter(t *testing.T) {
	src := `var x = $"Created on {d, date, short}"`
	assertFormat(t, src, src)
}

func TestFormatI18nTriple(t *testing.T) {
	src := `var x = $"""hello"""`
	assertFormat(t, src, src)
}

func TestFormatI18nEqSelector(t *testing.T) {
	src := `var x = $"{count, plural, =0{none} other{some}}"`
	assertFormat(t, src, src)
}
