package fieldcast

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/internal/temporal"
)

func fp(v float64) *float64 { return &v }
func sp(s string) *string   { return &s }

// TestBucketApply_Numeric: the half-open rule at both edges for every
// numeric representation, compared exactly (u64 2^53+3 below a 2^53+4
// bound, a decimal), a NaN failing not_finite and a value outside every
// range no_range, rendered exactly; nil stays nil.
func TestBucketApply_Numeric(t *testing.T) {
	t.Parallel()

	set, err := numrange.Compile([]numrange.Spec{
		{Label: "lo", Start: fp(0), End: fp(10)},
		{Label: "hi", Start: fp(10), End: fp(1<<53 + 4)},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := func(f encoding.Field) *Bucket { return &Bucket{From: f, Numeric: set} }
	for _, c := range []struct {
		name   string
		b      *Bucket
		v      any
		want   any
		reason string
		value  string
	}{
		{"f64 lower edge", b(fld(encoding.FieldTypeF64)), 0.0, "lo", "", ""},
		{"f64 at end", b(fld(encoding.FieldTypeF64)), 10.0, "hi", "", ""},
		{"f64 below", b(fld(encoding.FieldTypeF64)), -0.5, nil, "no_range", "-0.5"},
		{"f64 NaN", b(fld(encoding.FieldTypeF64)), math.NaN(), nil, "not_finite", "NaN"},
		{"f32", b(fld(encoding.FieldTypeF32)), float32(9.5), "lo", "", ""},
		{"u64 exact below", b(fld(encoding.FieldTypeU64)), uint64(1<<53 + 3), "hi", "", ""},
		{"u64 exact at end", b(fld(encoding.FieldTypeU64)), uint64(1<<53 + 4), nil, "no_range", "9007199254740996"},
		{"decimal at end", b(dec(10, 2)), mustDec(t, "10.00"), "hi", "", ""},
		{"decimal below end", b(dec(10, 2)), mustDec(t, "9.99"), "lo", "", ""},
		{"nil", b(fld(encoding.FieldTypeF64)), nil, nil, "", ""},
	} {
		got, fail := c.b.Apply(c.v)
		switch {
		case c.reason != "" && (fail == nil || fail.Reason != c.reason || fail.Value != c.value):
			t.Errorf("%s: failure %+v, want %s %s", c.name, fail, c.reason, c.value)
		case c.reason == "" && (fail != nil || got != c.want):
			t.Errorf("%s: %v %+v, want %v", c.name, got, fail, c.want)
		}
	}
}

// TestBucketApply_Dates: date bounds are inclusive; a datetime is
// matched by its local day in the bucket's zone (UTC when nil).
func TestBucketApply_Dates(t *testing.T) {
	t.Parallel()

	set, err := daterange.Compile([]daterange.Spec{
		{Label: "Q1", Start: sp("2024-01-01"), End: sp("2024-03-31")},
		{Label: "Q2", Start: sp("2024-04-01"), End: sp("2024-06-30")},
	})
	if err != nil {
		t.Fatal(err)
	}
	const mar31 = 19813
	date := &Bucket{From: fld(encoding.FieldTypeDate), Dates: set}
	for v, want := range map[int32]any{mar31: "Q1", mar31 + 1: "Q2", mar31 - 90: "Q1", mar31 - 91: nil} {
		got, fail := date.Apply(v)
		if got != want || (want == nil) != (fail != nil) {
			t.Errorf("day %d = %v %+v, want %v", v, got, fail, want)
		}
	}
	if _, fail := date.Apply(int32(mar31 - 91)); fail == nil || fail.Value != "2023-12-31" || fail.Reason != "no_range" {
		t.Errorf("out-of-range failure %+v, want no_range 2023-12-31", fail)
	}
	berlin, err := temporal.LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	late := int64(mar31)*86400 + 23*3600 + 1800 // 23:30 UTC = 01:30 CEST next day
	for _, c := range []struct {
		zone *temporal.Zone
		want string
	}{{nil, "Q1"}, {berlin, "Q2"}} {
		dt := &Bucket{From: fld(encoding.FieldTypeDateTime), Dates: set, Zone: c.zone}
		if got, fail := dt.Apply(late); got != c.want || fail != nil {
			t.Errorf("datetime in %v = %v %+v, want %s", c.zone, got, fail, c.want)
		}
	}
}
