package io

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestSetTypeFor_IsTheOneLadder pins the EXPORTED rung selection at the
// same boundaries TestSetWidth_LadderBoundaries pins the unexported one.
//
// The helper exists because io/spss/mrset.go used to hand-roll a second
// rung table of its own. Two ladders that agree today diverge the day one
// of them gains a rung: the SPSS one silently kept refusing above 64 while
// inference had already learned set_u128 / set_u256. So the boundaries are
// asserted here, against the exported entry point every other package
// reaches, rather than only against the inference-private wrapper.
func TestSetTypeFor_IsTheOneLadder(t *testing.T) {
	for _, c := range []struct {
		elements int
		want     encoding.FieldType
		ok       bool
	}{
		{0, 0, false},
		{-1, 0, false},
		{1, encoding.FieldTypeSetU8, true},
		{8, encoding.FieldTypeSetU8, true},
		{9, encoding.FieldTypeSetU16, true},
		{16, encoding.FieldTypeSetU16, true},
		{17, encoding.FieldTypeSetU32, true},
		{32, encoding.FieldTypeSetU32, true},
		{33, encoding.FieldTypeSetU64, true},
		{64, encoding.FieldTypeSetU64, true},
		{65, encoding.FieldTypeSetU128, true},
		{128, encoding.FieldTypeSetU128, true},
		{129, encoding.FieldTypeSetU256, true},
		{206, encoding.FieldTypeSetU256, true},
		{256, encoding.FieldTypeSetU256, true},
		{257, 0, false},
	} {
		got, ok := SetTypeFor(c.elements)
		if ok != c.ok {
			t.Errorf("SetTypeFor(%d) ok = %v, want %v", c.elements, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got != c.want {
			t.Errorf("SetTypeFor(%d) = %s, want %s", c.elements, got, c.want)
		}
		if int(got.MaxSetEntries()) < c.elements {
			t.Errorf("SetTypeFor(%d) = %s holds only %d entries",
				c.elements, got, got.MaxSetEntries())
		}
	}
}

// TestSetTypeFor_AgreesWithSetWidth is the anti-drift assertion: the
// inference wrapper must be the exported ladder and not a second reading
// of it. Every value either resolves to the same rung or is past the
// ceiling on both.
func TestSetTypeFor_AgreesWithSetWidth(t *testing.T) {
	for n := 1; n <= 300; n++ {
		ft, ok := SetTypeFor(n)
		w := setWidth(n)
		if ok != w.IsSet() {
			t.Fatalf("n=%d: SetTypeFor ok = %v but setWidth = %s (IsSet %v)",
				n, ok, w, w.IsSet())
		}
		if ok && ft != w {
			t.Fatalf("n=%d: SetTypeFor = %s, setWidth = %s", n, ft, w)
		}
	}
}

// TestMaxSetElements_IsTheTopRung pins the ceiling helper against the
// ladder it summarises. A caller that reports the ceiling in an error
// message must not be able to name a number the ladder does not honour.
func TestMaxSetElements_IsTheTopRung(t *testing.T) {
	max := MaxSetElements()
	if max != int(encoding.FieldTypeSetU256.MaxSetEntries()) {
		t.Errorf("MaxSetElements() = %d, want %d", max, encoding.FieldTypeSetU256.MaxSetEntries())
	}
	if ft, ok := SetTypeFor(max); !ok || ft != WidestSetType() {
		t.Errorf("SetTypeFor(%d) = %s, %v; want the widest rung %s", max, ft, ok, WidestSetType())
	}
	if _, ok := SetTypeFor(max + 1); ok {
		t.Errorf("SetTypeFor(%d) resolved a rung above the ceiling", max+1)
	}
	if got := WidestSetType(); int(got.MaxSetEntries()) != max {
		t.Errorf("WidestSetType() = %s holds %d entries, want %d", got, got.MaxSetEntries(), max)
	}
}
