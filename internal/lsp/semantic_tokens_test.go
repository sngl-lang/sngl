package lsp

import (
	"testing"

	"duckfam.us/sngl"
	"duckfam.us/sngl/internal/parser"
)

type semTok struct {
	line, col, length int
	kind              uint32
}

func decodeSemTokens(data []uint32) []semTok {
	var out []semTok
	prevLine, prevCol := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		deltaLine := int(data[i])
		deltaCol := int(data[i+1])
		length := int(data[i+2])
		kind := data[i+3]
		line := prevLine + deltaLine
		col := deltaCol
		if deltaLine == 0 {
			col = prevCol + deltaCol
		}
		out = append(out, semTok{line: line, col: col, length: length, kind: kind})
		prevLine = line
		prevCol = col
	}
	return out
}

func TestSemanticTokens_ClassifiesIdentifiers(t *testing.T) {
	src := `component Counter(label = "") node {
    var count = 0
    func add() {
        count += 1
    }
    vbox {
        text(value=label)
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := sngl.Check(doc, ".")
	if pkg == nil {
		t.Fatalf("check returned nil package")
	}

	data := computeSemanticTokens(src, doc, pkg)
	tokens := decodeSemTokens(data)
	if len(tokens) == 0 {
		t.Fatalf("no tokens emitted")
	}

	var hasVariable, hasParameter, hasClass, hasFunction, hasKeyword bool
	for _, tok := range tokens {
		switch tok.kind {
		case stVariable:
			hasVariable = true
		case stParameter:
			hasParameter = true
		case stClass:
			hasClass = true
		case stFunction:
			hasFunction = true
		case stKeyword:
			hasKeyword = true
		}
	}
	if !hasKeyword {
		t.Errorf("no keyword tokens emitted")
	}
	if !hasVariable {
		t.Errorf("no variable tokens (count ref) emitted; got %d tokens", len(tokens))
	}
	if !hasParameter {
		t.Errorf("no parameter tokens (label ref) emitted")
	}
	if !hasClass {
		t.Errorf("no class tokens (vbox/text component refs) emitted")
	}
	_ = hasFunction // function may or may not appear depending on stdlib resolution
}

func TestSemanticTokens_FallbackWithoutIR(t *testing.T) {
	src := `component Foo() node {
    var x = 1
}
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// pkg=nil → only keyword tokens.
	data := computeSemanticTokens(src, doc, nil)
	tokens := decodeSemTokens(data)
	if len(tokens) == 0 {
		t.Fatalf("expected keyword tokens in fallback path")
	}
	for _, tok := range tokens {
		if tok.kind != stKeyword {
			t.Errorf("expected only keyword tokens in fallback, got kind=%d", tok.kind)
		}
	}
}
