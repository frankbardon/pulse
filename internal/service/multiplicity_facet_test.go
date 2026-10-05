package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// multFacetRequest is a filtered facet over the facet-overlay fixture
// (the host keeps score < 70, the population is the whole cohort) so
// the two inferential FACET-host layers (CHISQ_VS_POP over category,
// KS_VS_POP over score) carry a p-value well inside (0, 0.5), plus one
// descriptive INDEX_VS_POP layer. own sets each overlay's block.
func multFacetRequest(path string, own ...*types.Multiplicity) *types.FacetRequest {
	pop := types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: path}}
	req := &types.FacetRequest{
		Cohort:             &types.Cohort{Filename: path},
		Fields:             []string{"category", "score"},
		Filterers:          []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score < 70.0"}},
		NumericPercentiles: []float64{0.25, 0.5, 0.75},
		Overlays: []types.OverlaySpec{
			{Name: "chi", Kind: types.OverlayKindChiSqVsPop, Scope: types.OverlayScopeGroup, Ref: pop, Params: json.RawMessage(`{"field":"category"}`)},
			{Name: "ks", Kind: types.OverlayKindKSVsPop, Scope: types.OverlayScopeGroup, Ref: pop, Params: json.RawMessage(`{"field":"score"}`)},
			{Name: "idx", Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup, Ref: pop, Params: json.RawMessage(`{"field":"category"}`)},
		},
	}
	for i, m := range own {
		req.Overlays[i].Multiplicity = m
	}
	return req
}

// TestFacetMultiplicity_LayerFold: the facet overlay hook corrects each
// inferential layer as its own `layer` family — two layers never pool
// (m stays 1, so the adjusted p equals the raw one under every method,
// where a pooled pair would double it) — with Options.DefaultMultiplicity
// applying when no slot names a block and a slot's own block winning;
// a descriptive or `none` layer is untouched and the host facet payload
// is byte-identical.
func TestFacetMultiplicity_LayerFold(t *testing.T) {
	ctx := context.Background()
	plain, path := buildFacetOverlayCohort(t)
	base, err := plain.FacetSchema(ctx, multFacetRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]float64, 2)
	for i := range raw {
		s := base.Overlays[i].Summary
		if s == nil || s.PValue == nil || !(*s.PValue > 0 && *s.PValue < 0.5) {
			t.Fatalf("layer %d raw p %v outside (0, 0.5); pooling would be undetectable", i, s)
		}
		raw[i] = *s.PValue
	}
	if strings.Contains(string(mustMarshal(t, base)), "p_adjusted") {
		t.Fatal("baseline carries multiplicity output")
	}
	bonf := &types.Multiplicity{Method: types.MultiplicityMethodBonferroni}
	holm := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Alpha: 0.01}
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone}
	cases := []struct {
		name string
		def  *types.Multiplicity
		own  []*types.Multiplicity
		// want echo per inferential layer (nil = untouched).
		want [2]*types.AppliedMultiplicity
	}{
		{
			name: "instance default applies",
			def:  bonf,
			want: [2]*types.AppliedMultiplicity{
				{Method: types.MultiplicityMethodBonferroni, Family: types.MultiplicityFamilyLayer, Alpha: types.DefaultMultiplicityAlpha, M: 1},
				{Method: types.MultiplicityMethodBonferroni, Family: types.MultiplicityFamilyLayer, Alpha: types.DefaultMultiplicityAlpha, M: 1},
			},
		},
		{
			name: "slot block wins over the default, none opts out",
			def:  bonf,
			own:  []*types.Multiplicity{holm, none},
			want: [2]*types.AppliedMultiplicity{
				{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyLayer, Alpha: 0.01, M: 1},
				nil,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := buildFacetOverlayCohort(t)
			svc.SetDefaultMultiplicity(c.def)
			got, err := svc.FacetSchema(ctx, multFacetRequest(path, c.own...))
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range c.want {
				l := got.Overlays[i]
				if !reflect.DeepEqual(l.Multiplicity, want) {
					t.Errorf("layer %d echo %+v, want %+v", i, l.Multiplicity, want)
				}
				if want == nil {
					if l.Summary.PAdjusted != nil || l.Summary.SignificantAdjusted != nil {
						t.Errorf("layer %d corrected without membership", i)
					}
					continue
				}
				if l.Summary.PAdjusted == nil || !sameFloat(*l.Summary.PAdjusted, raw[i]) {
					t.Errorf("layer %d p_adjusted %v, want %v (m=1)", i, l.Summary.PAdjusted, raw[i])
				}
				if s := l.Summary.SignificantAdjusted; s == nil || *s != (raw[i] < want.Alpha) {
					t.Errorf("layer %d significant_adjusted %v, want %v", i, s, raw[i] < want.Alpha)
				}
			}
			if got.Overlays[2].Multiplicity != nil || got.Overlays[2].Summary != nil && got.Overlays[2].Summary.PAdjusted != nil {
				t.Error("descriptive INDEX_VS_POP layer was corrected")
			}
			// Stripping the additive slots gives the baseline back.
			for i := range got.Overlays {
				got.Overlays[i].Multiplicity = nil
				if s := got.Overlays[i].Summary; s != nil {
					s.PAdjusted, s.SignificantAdjusted = nil, nil
				}
			}
			if a, b := mustMarshal(t, base), mustMarshal(t, got); !bytes.Equal(a, b) {
				t.Errorf("facet result differs beyond the additive slots:\n got %s\nwant %s", b, a)
			}
		})
	}
}

// TestFacetMultiplicity_NoneIsIdentity: no block, a `none` instance
// default and `none` on every slot all answer byte-identically.
func TestFacetMultiplicity_NoneIsIdentity(t *testing.T) {
	ctx := context.Background()
	plain, path := buildFacetOverlayCohort(t)
	base, err := plain.FacetSchema(ctx, multFacetRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	want := mustMarshal(t, base)
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone}
	svc, _ := buildFacetOverlayCohort(t)
	svc.SetDefaultMultiplicity(none)
	for name, run := range map[string]func() (*types.FacetResult, error){
		"instance default none": func() (*types.FacetResult, error) { return svc.FacetSchema(ctx, multFacetRequest(path)) },
		"slot none": func() (*types.FacetResult, error) {
			return plain.FacetSchema(ctx, multFacetRequest(path, none, none, none))
		},
	} {
		got, err := run()
		if err != nil {
			t.Fatal(err)
		}
		if b := mustMarshal(t, got); !bytes.Equal(b, want) {
			t.Errorf("%s: differs from the no-block baseline:\n got %s\nwant %s", name, b, want)
		}
	}
}

// TestFacetMultiplicity_RefusalsMatchPredict: on a facet overlay the
// `request` and `compose` families are refused, and so are `row` /
// `column` (every FACET-host kind is SCALAR) — by FacetSchema with the
// code, message and details ValidateFacet reports, whether the slot or
// the instance default names the family.
func TestFacetMultiplicity_RefusalsMatchPredict(t *testing.T) {
	ctx := context.Background()
	_, path := buildFacetOverlayCohort(t)
	for _, family := range []types.MultiplicityFamily{
		types.MultiplicityFamilyRequest, types.MultiplicityFamilyCompose,
		types.MultiplicityFamilyRow, types.MultiplicityFamilyColumn,
	} {
		t.Run(string(family), func(t *testing.T) {
			svc, _ := buildFacetOverlayCohort(t)
			svc.SetDefaultMultiplicity(&types.Multiplicity{Method: types.MultiplicityMethodHolm})
			req := multFacetRequest(path, nil, &types.Multiplicity{Family: family})
			_, rerr := svc.FacetSchema(ctx, req)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_MULTIPLICITY_INVALID {
				t.Fatalf("FacetSchema error = %v, want PULSE_MULTIPLICITY_INVALID", rerr)
			}
			if ce.Details["slot"] != "overlays[1].multiplicity" {
				t.Errorf("refusal located at %v", ce.Details["slot"])
			}
			data, err := afero.ReadFile(svc.fs.Fs(), path)
			if err != nil {
				t.Fatal(err)
			}
			env := descx.ValidateFacetWithOptions(bytes.NewReader(data), req, &descx.PredictOptions{DefaultMultiplicity: svc.DefaultMultiplicity()})
			if len(env.Errors) == 0 {
				t.Fatal("ValidateFacet accepted what FacetSchema refused")
			}
			e := env.Errors[0]
			wantDetails, _ := json.Marshal(ce.Details)
			gotDetails, _ := json.Marshal(e.Details)
			if e.Code != string(ce.Code) || e.Message != ce.Message || !bytes.Equal(wantDetails, gotDetails) {
				t.Errorf("predict %s %q %s, runtime %s %q %s", e.Code, e.Message, gotDetails, ce.Code, ce.Message, wantDetails)
			}
		})
	}
}
