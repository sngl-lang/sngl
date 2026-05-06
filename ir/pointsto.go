package ir

import "slices"

// SlotKind identifies the kind of slot a PointsToKey refers to.
type SlotKind int

const (
	SlotVar      SlotKind = iota // top-level var or local holding a funcvar
	SlotParam                    // function parameter of funcvar type
	SlotField                    // struct field of funcvar type, field-insensitive across instances
	SlotListElem                 // element of a list of funcvar type
	SlotReturn                   // function return slot when result is a funcvar
	SlotLocal                    // local variable (ir.LocalVar) holding a funcvar
)

// PointsToKey identifies a single slot in the points-to graph. Equal
// keys mean the same slot (the struct is value-comparable).
type PointsToKey struct {
	Kind     SlotKind
	Var      *Var      // SlotVar
	Param    *Param    // SlotParam
	Func     *Func     // SlotReturn
	Type     *Type     // SlotField, SlotListElem
	Field    string    // SlotField
	LocalVar *LocalVar // SlotLocal
}

func SlotVarKey(v *Var) PointsToKey     { return PointsToKey{Kind: SlotVar, Var: v} }
func SlotParamKey(p *Param) PointsToKey { return PointsToKey{Kind: SlotParam, Param: p} }
func SlotReturnKey(f *Func) PointsToKey { return PointsToKey{Kind: SlotReturn, Func: f} }
func SlotFieldKey(t *Type, name string) PointsToKey {
	return PointsToKey{Kind: SlotField, Type: t, Field: name}
}
func SlotListElemKey(t *Type) PointsToKey   { return PointsToKey{Kind: SlotListElem, Type: t} }
func SlotLocalKey(lv *LocalVar) PointsToKey { return PointsToKey{Kind: SlotLocal, LocalVar: lv} }

// PointsToInfo is the analysis result attached to a Package via Package.PointsTo.
type PointsToInfo struct {
	Sites     map[PointsToKey][]*Func
	SlotColor map[PointsToKey]Color
}

// NewPointsToInfo returns an empty PointsToInfo with all maps initialized.
func NewPointsToInfo() *PointsToInfo {
	return &PointsToInfo{
		Sites:     map[PointsToKey][]*Func{},
		SlotColor: map[PointsToKey]Color{},
	}
}

// AddCandidate inserts fn into the candidate set for key, deduping. Returns
// true when fn was newly added; false when it was already present.
func (p *PointsToInfo) AddCandidate(key PointsToKey, fn *Func) bool {
	if slices.Contains(p.Sites[key], fn) {
		return false
	}
	p.Sites[key] = append(p.Sites[key], fn)
	return true
}

// Candidates returns the candidate set for key (nil if no candidates have
// been added).
func (p *PointsToInfo) Candidates(key PointsToKey) []*Func {
	return p.Sites[key]
}
