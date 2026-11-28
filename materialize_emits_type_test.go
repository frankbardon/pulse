package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// featureEmitsProbes is one materialize request per FEAT_* operator
// that declares a manifest emits_type, over matSchema's source, and the
// derived columns it appends.
var featureEmitsProbes = map[types.FeatureType]struct {
	feature *types.Feature
	columns []string
}{
	types.FEAT_LOG:              {&types.Feature{Type: types.FEAT_LOG, Field: "spend", Label: "out"}, []string{"out"}},
	types.FEAT_SQRT:             {&types.Feature{Type: types.FEAT_SQRT, Field: "spend", Label: "out"}, []string{"out"}},
	types.FEAT_BUCKETIZE:        {&types.Feature{Type: types.FEAT_BUCKETIZE, Field: "spend", Label: "out", Params: json.RawMessage(`{"boundaries":[5,15,25]}`)}, []string{"out"}},
	types.FEAT_POLY:             {&types.Feature{Type: types.FEAT_POLY, Field: "spend", Label: "out", Params: json.RawMessage(`{"degree":3}`)}, []string{"out_2", "out_3"}},
	types.FEAT_FREQUENCY_ENCODE: {&types.Feature{Type: types.FEAT_FREQUENCY_ENCODE, Field: "region", Label: "out"}, []string{"out"}},
	types.FEAT_TARGET_ENCODE:    {&types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "out", Params: json.RawMessage(`{"target":"spend"}`)}, []string{"out"}},
	types.FEAT_TRAIN_TEST_SPLIT: {&types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "out", Params: json.RawMessage(`{"ratios":[0.6,0.2,0.2],"seed":7}`)}, []string{"out"}},
}

// TestManifestFeatureEmitsTypeMatchesMaterialize (#784): a FEAT_*
// operator's manifest emits_type is the field type Materialize writes
// its column as — the column a client sizes from the claim. Every
// declaring feature is probed (a new declaration without a probe
// fails), and no probe outlives its declaration.
func TestManifestFeatureEmitsTypeMatchesMaterialize(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	declared := map[types.FeatureType]string{}
	for _, op := range p.Manifest(context.Background()).Components.Features {
		if op.EmitsType != "" {
			declared[types.FeatureType(op.Name)] = op.EmitsType
		}
	}
	if len(declared) == 0 {
		t.Fatal("no feature declares an emits_type; the manifest walk is broken")
	}
	for ft := range featureEmitsProbes {
		if _, ok := declared[ft]; !ok {
			t.Errorf("probe for %s, which declares no emits_type: drop the probe or restore the declaration", ft)
		}
	}
	for ft, emits := range declared {
		probe, ok := featureEmitsProbes[ft]
		if !ok {
			t.Errorf("%s declares emits_type %q with no probe in featureEmitsProbes", ft, emits)
			continue
		}
		out := "out_" + string(ft) + ".pulse"
		req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Features: []*types.Feature{probe.feature}}
		if _, err := p.Materialize(context.Background(), req, out, MaterializeOptions{}); err != nil {
			t.Fatalf("%s: Materialize: %v", ft, err)
		}
		schema, _ := readAll(t, p, out)
		for _, col := range probe.columns {
			f := schema.Field(col)
			if f == nil {
				t.Fatalf("%s: no column %q in %v", ft, col, schema.Fields)
			}
			if got := f.Type.String(); got != emits {
				t.Errorf("%s: manifest emits_type %q, materialized column %q is %s", ft, emits, col, got)
			}
		}
	}
}
