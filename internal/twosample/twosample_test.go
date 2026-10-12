package twosample

import (
	"math"
	"reflect"
	"testing"
)

func TestInputs(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		side    Side
		v, n    float64
		missing []string
	}{
		{"nil params", nil, Target, 0, 0, []string{"variance_target", "sample_size_target"}},
		{"per-side", map[string]any{"variance_target": 4.0, "sample_size_target": 9}, Target, 4, 9, nil},
		{"reference keys", map[string]any{"variance_ref": float32(2), "sample_size_ref": int64(5)}, Reference, 2, 5, nil},
		{"symmetric", map[string]any{"variance": 3.0, "sample_size": int32(7)}, Reference, 3, 7, nil},
		{"per-side wins", map[string]any{"variance": 3.0, "variance_target": 5.0, "sample_size": 7.0}, Target, 5, 7, nil},
		{"other side only", map[string]any{"variance_ref": 3.0, "sample_size_ref": 7.0}, Target, 0, 0, []string{"variance_target", "sample_size_target"}},
		{"zero variance", map[string]any{"variance_target": 0.0, "sample_size_target": 4.0}, Target, 0, 4, []string{"variance_target"}},
		{"negative n", map[string]any{"variance_target": 1.0, "sample_size_target": -4}, Target, 1, 0, []string{"sample_size_target"}},
		{"string", map[string]any{"variance_target": "4", "sample_size_target": 4.0}, Target, 0, 4, []string{"variance_target"}},
		{"inf", map[string]any{"variance_target": math.Inf(1), "sample_size_target": 4.0}, Target, 0, 4, []string{"variance_target"}},
		{"nan", map[string]any{"variance_target": 1.0, "sample_size_target": math.NaN()}, Target, 1, 0, []string{"sample_size_target"}},
		{"unusable per-side does not fall through", map[string]any{"variance_target": 0.0, "variance": 2.0, "sample_size": 4.0}, Target, 0, 4, []string{"variance_target"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, n, missing := Inputs(c.params, c.side)
			if !reflect.DeepEqual(missing, c.missing) {
				t.Fatalf("missing = %v, want %v", missing, c.missing)
			}
			if c.missing == nil && (v != c.v || n != c.n) {
				t.Fatalf("Inputs = (%v, %v), want (%v, %v)", v, n, c.v, c.n)
			}
		})
	}
}
