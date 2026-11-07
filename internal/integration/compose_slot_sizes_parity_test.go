package integration_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestComposePredict_SlotSizesMatchProcessPredict (#466): every Compose
// slot reports the response size estimates Process predict gives the
// same request (PredictResult.Sizes), headed by the slot's `label` echo
// — exact, and kept by the standard and minimal presets as the runtime
// keeps it. A refused `return` block withholds both.
func TestComposePredict_SlotSizesMatchProcessPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	axes := `"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}]`
	bodies := []string{
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]}`,
		`{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_AVERAGE","field":"x","label":"m"}]}`,
		`{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_AVERAGE","field":"x","label":"m"}],"return":{"preset":"standard"}}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"preset":"minimal"}}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"exclude":["label","metadata"]}}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"disable_components":true}`,
		`{"tests":[{"type":"TEST_WELCH","field":"x","split_by":"g","label":"t"}],"return":{"preset":"standard"}}`,
		`{"regressions":[{"type":"REG_OLS","name":"fit","target":"y","predictors":["x"]}]}`,
		`{"regressions":[{"type":"REG_OLS","name":"fit","target":"y","predictors":["x","week"],"multiplicity":{"method":"holm"}}]}`,
		`{"vectors":[{"name":"v","fields":["x","y"]}],"matrices":[{"name":"c","type":"MAT_CORRELATION","vector":"v"}]}`,
		`{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x"}}}`,
		`{"windows":[{"type":"WIN_LAG","field":"x","label":"lx","order_by":[{"field":"week"}]}]}`,
		// Refused at its zone, before the slot's defaults resolve: the
		// sizes still read the defaulted aggregation's label.
		`{"aggregations":[{"field":"x"}],"time_zone":"Not/AZone"}`,
	}
	ctx := context.Background()
	for _, body := range bodies {
		pres := predictEnvelope(t, p, fs, cohort, slotRequest(t, cohort, body)).Data.(*descriptor.PredictResult)
		if len(pres.Sizes) == 0 {
			t.Fatalf("%s: Process predict reports no sizes", body)
		}
		slot := slotRequest(t, cohort, body)
		slot.Label = "readout"
		cenv, err := p.PredictCompose(ctx, &types.ComposedRequest{Requests: []*types.Request{slot}})
		if err != nil {
			t.Fatal(err)
		}
		cres := cenv.Data.(*pulse.ComposePredictResult)
		if len(cres.Slots) != 1 {
			t.Fatalf("%s: slots %+v, want one", body, cres.Slots)
		}
		got := cres.Slots[0].Sizes
		if len(got) == 0 || got[0].Section != "label" {
			t.Fatalf("%s: compose slot sizes %+v, want a leading label section", body, got)
		}
		if !reflect.DeepEqual(got[1:], pres.Sizes) {
			t.Errorf("%s:\n compose slot    %+v\n Process predict %+v", body, got[1:], pres.Sizes)
		}
		// `"label":"readout"` — the key, its quotes and colon, the
		// separator and the quoted value.
		label := got[0]
		if label.FullBytes != int64(len("label")+4+len(`"readout"`)) || label.Basis != "exact" {
			t.Errorf("%s: label section %+v", body, label)
		}
		excluded := slot.Return != nil && len(slot.Return.Exclude) > 0
		if want := label.FullBytes; !excluded && label.ShapedBytes != want {
			t.Errorf("%s: label shaped %d, want %d (every preset keeps it)", body, label.ShapedBytes, want)
		}
		if excluded && label.ShapedBytes != 0 {
			t.Errorf("%s: excluded label shaped %d, want 0", body, label.ShapedBytes)
		}
	}

	// A refused `return` block — its preset, or a data column the
	// request cannot produce: neither reports sizes.
	for _, bad := range []string{
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"preset":"nope"}}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"include":["data[*].nope"]}}`,
	} {
		if pres := predictEnvelope(t, p, fs, cohort, slotRequest(t, cohort, bad)).Data.(*descriptor.PredictResult); pres.Sizes != nil {
			t.Errorf("%s: Process predict sizes %+v", bad, pres.Sizes)
		}
		cenv, err := p.PredictCompose(ctx, &types.ComposedRequest{Requests: []*types.Request{slotRequest(t, cohort, bad)}})
		if err != nil {
			t.Fatal(err)
		}
		cres := cenv.Data.(*pulse.ComposePredictResult)
		if len(cres.Slots) != 1 {
			t.Fatalf("%s: slots %+v, want one", bad, cres.Slots)
		}
		if cres.Slots[0].Sizes != nil {
			t.Errorf("%s: compose slot sizes %+v", bad, cres.Slots[0].Sizes)
		}
	}
}
