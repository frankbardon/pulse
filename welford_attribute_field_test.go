package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestWelford_AttributeDerivedField (survey-tables E4-S6): AGG_WELFORD
// reads an attribute output (an ATTR_FORMULA column) like every other
// numeric aggregator — predict accepted it while the runtime refused
// "AGG_WELFORD: unknown field". The derived-field run equals the run on
// the stored field the formula copies, on every Welford consumer: an
// ungrouped and a grouped aggregation, a crosstab cell (both dispatch
// arms), the prior-period T / Z kinds and the pairwise Welch kinds.
func TestWelford_AttributeDerivedField(t *testing.T) {
	t.Parallel()

	fs, cohort := priorCohort(t)
	const attr = `"attributes":[{"type":"ATTR_FORMULA","field":"x","label":"xs","expression":"x == nil ? nil : x * 1.0"}],`
	cases := []struct{ name, body string }{
		{"ungrouped", `"aggregations":[{"type":"AGG_WELFORD","field":"%s","label":"m"}]`},
		{"grouped", `"groups":[{"type":"GROUP_CATEGORY","field":"wave"}],"aggregations":[{"type":"AGG_WELFORD","field":"%s","label":"m"}]`},
		{"series t vs prior", `"groups":[{"type":"GROUP_CATEGORY","field":"wave"}],"aggregations":[{"type":"AGG_WELFORD","field":"%s","label":"m"}],` +
			`"overlays":[{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"m"}}]`},
		{"crosstab z vs prior", `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_WELFORD","field":"%s","label":"c"}},` +
			`"overlays":[{"kind":"OVERLAY_Z_VS_PRIOR","scope":"group"}]`},
		{"crosstab pairwise welch", `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"columns":[{"type":"GROUP_CATEGORY","field":"h"}],` +
			`"cell":{"type":"AGG_WELFORD","field":"%s","label":"c"}},"overlays":[{"kind":"OVERLAY_PAIRWISE_WELCH_T","scope":"column"}]`},
		{"crosstab pairwise two means z", `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"columns":[{"type":"GROUP_CATEGORY","field":"h"}],` +
			`"cell":{"type":"AGG_WELFORD","field":"%s","label":"c"}},"overlays":[{"kind":"OVERLAY_PAIRWISE_TWO_MEANS_Z","scope":"row"}]`},
	}
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", c.name, disable), func(t *testing.T) {
				derived := priorRequest(t, cohort, attr+fmt.Sprintf(c.body, "xs"))
				if env := predictEnvelope(t, p, fs, cohort, derived); len(env.Errors) != 0 {
					t.Fatalf("predict refused: %q", envCodes(env))
				}
				got, err := p.Process(context.Background(), derived)
				if err != nil {
					t.Fatalf("runtime refused what predict accepted: %v", err)
				}
				want, err := p.Process(context.Background(), priorRequest(t, cohort, attr+fmt.Sprintf(c.body, "x")))
				if err != nil {
					t.Fatalf("stored-field run: %v", err)
				}
				for name, pair := range map[string][2]any{
					"data": {got.Data, want.Data}, "crosstab": {got.Crosstab, want.Crosstab}, "overlays": {got.Overlays, want.Overlays},
				} {
					a, _ := json.Marshal(pair[0])
					b, _ := json.Marshal(pair[1])
					if string(a) != string(b) {
						t.Fatalf("%s differs between the derived and the stored field:\n%s\n%s", name, a, b)
					}
				}
				if got.Data == nil && got.Crosstab == nil {
					t.Fatal("vacuous: no data")
				}
			})
		}
	}
}

// TestWelford_UndefinedFieldRefusedAlike: a field neither the schema nor
// an attribute defines is refused with one code and message by predict
// and the runtime — the AGG_WELFORD factory no longer judges a name the
// schema lacks, so the shared field-reference walk owns it.
func TestWelford_UndefinedFieldRefusedAlike(t *testing.T) {
	t.Parallel()

	fs, cohort := priorCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	req := priorRequest(t, cohort, `"aggregations":[{"type":"AGG_WELFORD","field":"nope","label":"m"}]`)
	_, rerr := p.Process(context.Background(), req)
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("runtime = %v, want a coded refusal", rerr)
	}
	env := predictEnvelope(t, p, fs, cohort, req)
	for _, e := range env.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			return
		}
	}
	t.Fatalf("predict %q lacks the runtime's %s %q", envCodes(env), ce.Code, ce.Message)
}

// TestWelford_AttributeDerivedField_Compose: the two-sample vs-ref kinds
// read an attribute-derived AGG_WELFORD's triple off the slot components
// exactly as they read a stored field's.
func TestWelford_AttributeDerivedField_Compose(t *testing.T) {
	t.Parallel()

	fs, cohort := priorCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	compose := func(field string, kind types.OverlayKind) *pulse.ComposedRequest {
		slot := func(label string, waves ...string) *types.Request {
			r := &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Label: "xs", Expression: "x == nil ? nil : x * 1.0"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: field, Label: "m"}}}
			r.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: waves}}
			return r
		}
		return &pulse.ComposedRequest{Requests: []*types.Request{slot("a", "w1", "w2"), slot("b", "w4")},
			Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeGroup, Reference: "a", Targets: []string{"b"}}}}
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		t.Run(string(kind), func(t *testing.T) {
			got, err := p.Compose(context.Background(), compose("xs", kind))
			if err != nil {
				t.Fatalf("derived field: %v", err)
			}
			want, err := p.Compose(context.Background(), compose("x", kind))
			if err != nil {
				t.Fatalf("stored field: %v", err)
			}
			a, _ := json.Marshal(got.Overlays)
			b, _ := json.Marshal(want.Overlays)
			if string(a) != string(b) || len(got.Overlays) == 0 {
				t.Fatalf("overlays differ:\n%s\n%s", a, b)
			}
		})
	}
}
