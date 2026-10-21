package pulse

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
)

// TestExtRecord_Uint64Value (#459): the extend view reads a u64 field's
// exact word (2^53+1 and MaxUint64-1, which share an echo with their
// neighbour), a null as (0, false), and a native vector element member
// "f[k]" through its element — (0, false) exactly when NumericValue is.
func TestExtRecord_Uint64Value(t *testing.T) {
	s := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "k", Type: encoding.FieldTypeU64, Nullable: true},
			{Name: "nk", Type: encoding.FieldTypeU64, Nullable: true},
			{Name: "emb", Type: encoding.FieldTypeVecF64, Nullable: true},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 2, Dim: 2}},
	}
	for _, want := range []uint64{1<<53 + 1, math.MaxUint64 - 1, math.MaxUint64} {
		rec := processing.NewRecordWithWide(s,
			map[string]float64{"k": float64(want), "x": 1e300},
			map[string]bool{"nk": true},
			map[string]any{"emb": []float64{12, math.NaN()}})
		rec.SetExactU64("k", want)
		var r extend.Record = extRecord{rec}
		if got, ok := r.Uint64Value("k"); !ok || got != want {
			t.Errorf("Uint64Value(k) = (%d, %v), want (%d, true)", got, ok, want)
		}
		cases := []struct {
			name   string
			want   uint64
			wantOK bool
		}{
			{"nk", 0, false},            // null
			{"missing", 0, false},       // no such field
			{"emb", 0, false},           // a vector has no scalar value
			{"emb[1]", 12, true},        // element member
			{"emb[2]", 0, false},        // NaN element: a null member
			{"x", math.MaxUint64, true}, // echo past 2^64 saturates
		}
		for _, c := range cases {
			got, ok := r.Uint64Value(c.name)
			if got != c.want || ok != c.wantOK {
				t.Errorf("Uint64Value(%q) = (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.wantOK)
			}
			if _, nok := r.NumericValue(c.name); nok != ok {
				t.Errorf("%q: Uint64Value ok %v disagrees with NumericValue ok %v", c.name, ok, nok)
			}
		}
	}
}

// echoOnlyFeatureRecord is a feature.Record with no exact u64 read.
type echoOnlyFeatureRecord map[string]float64

func (r echoOnlyFeatureRecord) NumericValue(n string) (float64, bool) { v, ok := r[n]; return v, ok }
func (echoOnlyFeatureRecord) StringValueChecked(string) (string, bool, error) {
	return "", false, nil
}
func (echoOnlyFeatureRecord) LevelValueChecked(string) (string, bool, error) { return "", false, nil }
func (echoOnlyFeatureRecord) Set(string, float64)                            {}
func (echoOnlyFeatureRecord) SetNull(string)                                 {}

// TestFeatureRecordView_Uint64Value: a non-engine feature record degrades
// to its float echo (saturating past 2^64), false when absent.
func TestFeatureRecordView_Uint64Value(t *testing.T) {
	r := featureRecord(echoOnlyFeatureRecord{"a": 42, "big": 1e30})
	if got, ok := r.Uint64Value("a"); !ok || got != 42 {
		t.Errorf("Uint64Value(a) = (%d, %v), want (42, true)", got, ok)
	}
	if got, ok := r.Uint64Value("big"); !ok || got != math.MaxUint64 {
		t.Errorf("Uint64Value(big) = (%d, %v), want (MaxUint64, true)", got, ok)
	}
	if got, ok := r.Uint64Value("none"); ok || got != 0 {
		t.Errorf("Uint64Value(none) = (%d, %v), want (0, false)", got, ok)
	}
}
