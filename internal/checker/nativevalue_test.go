package checker_test

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

// enumType builds the declaration a scheme importer would have produced for a
// foreign enum, which is the only place the member values come from.
func enumType(members ...*ir.EnumMember) *ir.Type {
	ed := &ir.EnumDef{Name: "Color", Members: members}
	return ed.SymType()
}

func numMember(name, raw string) *ir.EnumMember {
	return &ir.EnumMember{Name: name, Value: &ir.Literal{Type: ir.TypFloat, Raw: raw}}
}

func strMember(name, raw string) *ir.EnumMember {
	return &ir.EnumMember{Name: name, Value: &ir.Literal{Type: ir.TypString, Raw: raw}}
}

func num(raw string) ast.Expr {
	return &ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: raw}
}

func str(raw string) ast.Expr {
	return &ast.LiteralExpr{Kind: ast.LiteralStringQuoted, Raw: raw}
}

// TestNativeEnumValue covers the erasure a foreign enum member crosses as: the
// encoder writes the value, and the expected type is what turns it back into a
// member.
func TestNativeEnumValue(t *testing.T) {
	cases := []struct {
		name    string
		value   ast.Expr
		want    *ir.Type
		member  string
		errWant string
	}{{
		name:   "numeric member",
		value:  num("1.0"),
		want:   enumType(numMember("Red", "0"), numMember("Green", "1")),
		member: "Green",
	}, {
		name:   "string member",
		value:  str("warn"),
		want:   enumType(strMember("Info", "info"), strMember("Warn", "warn")),
		member: "Warn",
	}, {
		// Two members with one value resolve to the first declared.
		name:   "duplicate value takes the first declared",
		value:  num("1.0"),
		want:   enumType(numMember("Green", "1"), numMember("Verdant", "1")),
		member: "Green",
	}, {
		// A number and a string are separate domains: "1" is not 1.
		name:    "a string does not match a numeric member",
		value:   str("1"),
		want:    enumType(numMember("One", "1")),
		errWant: `"1" is not the value of any member of Color`,
	}, {
		// An ambient member carries no value, so nothing can match it — least
		// of all the 0 TypeScript would have numbered it with.
		name:    "a valueless member matches nothing",
		value:   num("0.0"),
		want:    enumType(&ir.EnumMember{Name: "Red"}, numMember("Green", "1")),
		errWant: "0.0 is not the value of any member of Color",
	}, {
		name:    "an unmatched value does not survive as a number",
		value:   num("7.0"),
		want:    enumType(numMember("Red", "0")),
		errWant: "7.0 is not the value of any member of Color",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checker.CheckNativeValue(tc.value, tc.want)
			if tc.errWant != "" {
				if err == nil {
					t.Fatalf("got %#v, want error %q", got, tc.errWant)
				}
				if !strings.Contains(err.Error(), tc.errWant) {
					t.Fatalf("got error %q, want it to contain %q", err, tc.errWant)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckNativeValue: %v", err)
			}
			id, ok := got.(*ir.Ident)
			if !ok {
				t.Fatalf("got %T, want *ir.Ident", got)
			}
			if id.Member != tc.member {
				t.Errorf("resolved to member %q, want %q", id.Member, tc.member)
			}
			if id.Type != tc.want {
				t.Errorf("resolved to type %v, want the expected enum", id.Type)
			}
		})
	}
}
