package ir_test

import (
	"testing"

	"duckfam.us/sngl/ir"
)

func TestPointsToKey_VarAndField(t *testing.T) {
	v := &ir.Var{Name: "h"}
	k1 := ir.SlotVarKey(v)
	if k1.Kind != ir.SlotVar || k1.Var != v {
		t.Fatalf("SlotVarKey(v) malformed: %+v", k1)
	}
	sd := &ir.StructDef{Name: "Handler"}
	sty := &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	k2 := ir.SlotFieldKey(sty, "onClick")
	if k2.Kind != ir.SlotField || k2.Type != sty || k2.Field != "onClick" {
		t.Fatalf("SlotFieldKey malformed: %+v", k2)
	}
}

func TestPointsToInfo_AddCandidate(t *testing.T) {
	info := ir.NewPointsToInfo()
	v := &ir.Var{Name: "h"}
	fn := &ir.Func{Name: "syncFn"}
	key := ir.SlotVarKey(v)
	if !info.AddCandidate(key, fn) {
		t.Fatalf("first AddCandidate should report new=true")
	}
	if got := info.Candidates(key); len(got) != 1 || got[0] != fn {
		t.Fatalf("expected 1 candidate, got %v", got)
	}
	if info.AddCandidate(key, fn) {
		t.Fatalf("dedup AddCandidate should return false")
	}
	if got := info.Candidates(key); len(got) != 1 {
		t.Fatalf("expected dedup; got %d", len(got))
	}
}

func TestPointsToInfo_NilSafeRead(t *testing.T) {
	var info *ir.PointsToInfo
	// Calling Candidates on a non-nil but empty info is fine.
	info = ir.NewPointsToInfo()
	if got := info.Candidates(ir.SlotVarKey(&ir.Var{})); got != nil {
		t.Fatalf("missing key should return nil, got %v", got)
	}
}
