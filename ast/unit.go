package ast

import (
	"maps"
	"math"
	"strconv"
	"strings"
)

// ParseUnitNumber parses a unit literal's number string, stripping
// underscore separators before conversion.
func ParseUnitNumber(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
}

// UnitValue represents a compound unit measurement at runtime.
// Keys are base suffix names, values are numeric amounts normalized to the base.
type UnitValue struct {
	Unit       string             // unit type name: "measurement", "duration"
	Components map[string]float64 // base suffix → amount
	Suffixes   []string           // declaration-order base suffixes (for deterministic formatting)
}

// UnitConversion describes how a suffix normalizes to its base.
type UnitConversion struct {
	Base       string  // the base suffix this normalizes to
	Multiplier float64 // how many base units per 1 of this suffix
}

// UnitTable maps suffix → conversion info for a single unit type.
type UnitTable struct {
	Name        string
	Conversions map[string]UnitConversion
	Bases       []string // declaration-order base suffixes
}

// BuildUnitTable resolves conversion chains from a UnitDef.
// For example, given unit duration(ms, s = 1000ms, m = 60s, h = 60m):
//   - ms → {Base: "ms", Multiplier: 1}
//   - s  → {Base: "ms", Multiplier: 1000}
//   - m  → {Base: "ms", Multiplier: 60000}
//   - h  → {Base: "ms", Multiplier: 3600000}
func BuildUnitTable(def *UnitDef) *UnitTable {
	t := &UnitTable{
		Name:        def.Name,
		Conversions: make(map[string]UnitConversion, len(def.Suffixes)),
	}

	// First pass: record raw factors (direct references).
	type rawFactor struct {
		amount float64
		target string // suffix it converts to
	}
	raw := map[string]*rawFactor{}
	for _, s := range def.Suffixes {
		if s.Factor == nil {
			// Base suffix
			raw[s.Name] = nil
		} else {
			// Factor is a LiteralExpr with UnitLiteral value (e.g., 1000ms)
			if lit, ok := s.Factor.(*LiteralExpr); ok {
				if ul, ok := lit.Value.(UnitLiteral); ok {
					num, _ := ParseUnitNumber(ul.Number)
					raw[s.Name] = &rawFactor{amount: num, target: ul.Suffix}
				}
			}
		}
	}

	// Resolve each suffix to its ultimate base.
	var resolve func(name string) UnitConversion
	resolve = func(name string) UnitConversion {
		if conv, ok := t.Conversions[name]; ok {
			return conv
		}
		rf := raw[name]
		if rf == nil {
			// This is a base suffix.
			conv := UnitConversion{Base: name, Multiplier: 1}
			t.Conversions[name] = conv
			return conv
		}
		// Resolve the target first, then multiply.
		target := resolve(rf.target)
		conv := UnitConversion{
			Base:       target.Base,
			Multiplier: rf.amount * target.Multiplier,
		}
		t.Conversions[name] = conv
		return conv
	}

	for _, s := range def.Suffixes {
		resolve(s.Name)
	}

	// Collect base suffixes in declaration order.
	seen := map[string]bool{}
	for _, s := range def.Suffixes {
		base := t.Conversions[s.Name].Base
		if !seen[base] {
			seen[base] = true
			t.Bases = append(t.Bases, base)
		}
	}

	return t
}

// NewUnitValue creates a UnitValue from a literal suffix and number using the table.
func (t *UnitTable) NewUnitValue(suffix string, amount float64) UnitValue {
	conv := t.Conversions[suffix]
	return UnitValue{
		Unit:       t.Name,
		Components: map[string]float64{conv.Base: amount * conv.Multiplier},
		Suffixes:   t.Bases,
	}
}

// Add returns a new UnitValue with component-wise addition.
func (u UnitValue) Add(other UnitValue) UnitValue {
	result := UnitValue{
		Unit:       u.Unit,
		Components: make(map[string]float64, len(u.Components)),
		Suffixes:   u.Suffixes,
	}
	maps.Copy(result.Components, u.Components)
	for k, v := range other.Components {
		result.Components[k] += v
	}
	return result
}

// Sub returns a new UnitValue with component-wise subtraction.
func (u UnitValue) Sub(other UnitValue) UnitValue {
	result := UnitValue{
		Unit:       u.Unit,
		Components: make(map[string]float64, len(u.Components)),
		Suffixes:   u.Suffixes,
	}
	maps.Copy(result.Components, u.Components)
	for k, v := range other.Components {
		result.Components[k] -= v
	}
	return result
}

// Scale multiplies all components by a scalar.
func (u UnitValue) Scale(factor float64) UnitValue {
	result := UnitValue{
		Unit:       u.Unit,
		Components: make(map[string]float64, len(u.Components)),
		Suffixes:   u.Suffixes,
	}
	for k, v := range u.Components {
		result.Components[k] = v * factor
	}
	return result
}

// Equal reports whether two UnitValues have the same components.
func (u UnitValue) Equal(other UnitValue) bool {
	if u.Unit != other.Unit {
		return false
	}
	// Check all keys in u
	for k, v := range u.Components {
		if math.Abs(v-other.Components[k]) > 1e-9 {
			return false
		}
	}
	// Check keys in other that might not be in u
	for k, v := range other.Components {
		if _, ok := u.Components[k]; !ok {
			if math.Abs(v) > 1e-9 {
				return false
			}
		}
	}
	return true
}

// IsSingleComponent reports whether this value has exactly one non-zero component.
func (u UnitValue) IsSingleComponent() (string, float64, bool) {
	var base string
	var amount float64
	count := 0
	for k, v := range u.Components {
		if math.Abs(v) > 1e-9 {
			base = k
			amount = v
			count++
		}
	}
	return base, amount, count == 1
}

// String formats the unit value for display.
func (u UnitValue) String() string {
	var parts []string
	for _, base := range u.Suffixes {
		v, ok := u.Components[base]
		if !ok || math.Abs(v) < 1e-9 {
			continue
		}
		// Format number nicely: integer if possible
		var num string
		if v == math.Trunc(v) && !math.IsInf(v, 0) {
			num = strconv.FormatInt(int64(v), 10)
		} else {
			num = strconv.FormatFloat(v, 'f', -1, 64)
		}
		parts = append(parts, num+base)
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " + ")
}
