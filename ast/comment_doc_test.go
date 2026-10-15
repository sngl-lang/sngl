package ast

import "testing"

func TestCommentDoc(t *testing.T) {
	line := func(s string) *Comment { return &Comment{Text: s} }
	tests := []struct {
		name     string
		leading  []*Comment
		trailing *Comment
		want     string
	}{
		{"none", nil, nil, ""},
		{"trailing alone", nil, line("// the text shown"), "the text shown"},
		{"leading joined", []*Comment{line("// one"), line("// two")}, nil, "one two"},
		{"leading wins", []*Comment{line("// above")}, line("// after"), "above"},
		{"todo is no doc", nil, line("// TODO enum"), ""},
		{"block comment ignored", nil, &Comment{Text: "/* x */", Block: true}, ""},
	}
	for _, tt := range tests {
		if got := CommentDoc(tt.leading, tt.trailing); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
