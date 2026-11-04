package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestInstanceSnapshot_SweepOptions: the expansions' instance facts are
// the visible extension operators' Expression-flagged params by
// operator name (matrix ops included), an unflagged param and a hidden
// operator flag nothing, and no extension at all is the zero value.
func TestInstanceSnapshot_SweepOptions(t *testing.T) {
	t.Parallel()

	expr := []descriptor.OperatorParamMeta{{Name: "scale", JSONType: "number", Expression: true}, {Name: "tag", JSONType: "string"}}
	snap := &ExtensionsSnapshot{
		Aggregators:   []descriptor.OperatorMeta{{Name: "AGG_ACME_X", Params: expr}},
		Features:      []descriptor.OperatorMeta{{Name: "FEAT_ACME_X", Params: expr}},
		Windows:       []descriptor.OperatorMeta{{Name: "WIN_ACME_X", Params: []descriptor.OperatorParamMeta{{Name: "tag", JSONType: "string"}}}},
		MatrixOps:     []descriptor.OperatorMeta{{Name: "MAT_ACME_X"}},
		MatrixDetails: map[string]ExtensionMatrixOp{"MAT_ACME_X": {Params: []descriptor.Param{{Name: "k", Expression: true}}}},
	}
	got := UnscopedInstanceSnapshot(snap).SweepOptions().ExprParams
	want := map[string][]string{"AGG_ACME_X": {"scale"}, "FEAT_ACME_X": {"scale"}, "MAT_ACME_X": {"k"}}
	if len(got) != len(want) {
		t.Fatalf("ExprParams = %v, want %v", got, want)
	}
	for op, keys := range want {
		if !slices.Equal(got[op], keys) {
			t.Fatalf("ExprParams[%s] = %v, want %v", op, got[op], keys)
		}
	}

	hiding := NewInstanceSnapshot(snap, FeatureSet{Enabled: []string{featProcess, "FEAT_ACME_X", "MAT_ACME_X"}, Hidden: []string{"AGG_ACME_X"}})
	if got := hiding.SweepOptions().ExprParams; got["AGG_ACME_X"] != nil || !slices.Equal(got["FEAT_ACME_X"], []string{"scale"}) {
		t.Fatalf("hiding AGG_ACME_X: ExprParams = %v", got)
	}
	var none *InstanceSnapshot
	if got := none.SweepOptions(); got.ExprParams != nil {
		t.Fatalf("nil snapshot: %v", got)
	}
}
