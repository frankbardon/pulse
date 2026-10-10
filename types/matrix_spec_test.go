package types

import (
	"encoding/json"
	"testing"
)

// TestMatrixSpec_StreamableMergeable: the spec-level answers fold the
// type with its params — a co-moment spec streams and merges; a rank
// method on MAT_CORRELATION (any params.method but "pearson") buffers
// and does not merge; an unknown type does neither; malformed params
// read as the type's own answer (resolution refuses them).
func TestMatrixSpec_StreamableMergeable(t *testing.T) {
	cases := []struct {
		name   string
		spec   MatrixSpec
		stream bool
		merge  bool
	}{
		{"covariance", MatrixSpec{Type: MAT_COVARIANCE}, true, true},
		{"correlation default", MatrixSpec{Type: MAT_CORRELATION}, true, true},
		{"correlation null params", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`null`)}, true, true},
		{"correlation other params", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`{"missing":"pairwise"}`)}, true, true},
		{"correlation pearson", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`{"method":"pearson"}`)}, true, true},
		{"correlation spearman", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`{"method":"spearman"}`)}, false, false},
		{"correlation kendall", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`{"method":"kendall"}`)}, false, false},
		{"correlation malformed params", MatrixSpec{Type: MAT_CORRELATION, Params: json.RawMessage(`[1]`)}, true, true},
		{"covariance ignores method", MatrixSpec{Type: MAT_COVARIANCE, Params: json.RawMessage(`{"method":"spearman"}`)}, true, true},
		{"unknown type", MatrixSpec{Type: "MAT_NOPE"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.Streamable(); got != c.stream {
				t.Errorf("Streamable = %v, want %v", got, c.stream)
			}
			if got := c.spec.Mergeable(); got != c.merge {
				t.Errorf("Mergeable = %v, want %v", got, c.merge)
			}
		})
	}
}
