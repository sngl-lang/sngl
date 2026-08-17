package opeval

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestArithIntVsFloat(t *testing.T) {
	cases := []struct {
		name    string
		op      ast.BinaryOp
		l, r    any
		want    any
		wantErr bool
	}{
		{"int div is integer", ast.BinDiv, 3, 2, 1, false},
		{"int div exact", ast.BinDiv, 12, 4, 3, false},
		{"int mod", ast.BinMod, 7, 3, 1, false},
		{"int add stays int", ast.BinAdd, 2, 3, 5, false},
		{"float operand → float div", ast.BinDiv, 3.0, 2.0, 1.5, false},
		{"mixed int/float → float", ast.BinDiv, 3, 2.0, 1.5, false},
		{"float keeps float (no int collapse)", ast.BinMul, 2.0, 2.0, 4.0, false},
		{"float mod", ast.BinMod, 3.5, 2.0, 1.5, false},
		{"int div by zero", ast.BinDiv, 1, 0, nil, true},
		{"int mod by zero", ast.BinMod, 1, 0, nil, true},
		{"float div by zero", ast.BinDiv, 1.0, 0.0, nil, true},
		{"non-numeric", ast.BinAdd, true, 1, nil, true},
	}
	for _, c := range cases {
		got, err := Arith(c.op, c.l, c.r, NumKind{})
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: Arith(%v,%v,%v) = %v (%T); want %v (%T)", c.name, c.op, c.l, c.r, got, got, c.want, c.want)
		}
	}
}
