package descriptor

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// buildFacetOverlayPulseBytes returns a minimal .pulse file with one
// categorical "category" field and one numeric "score" field — the
// canonical fixture for FACET-host predict tests. Carries four records so
// the schema reader has payload bytes to skip even though predict never
// reads beyond the schema block.
func buildFacetOverlayPulseBytes(t *testing.T) []byte {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	for i := uint64(0); i < 4; i++ {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, i%3)
		encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(float64(i)))
	}
	return buf.Bytes()
}

func TestPredict_FacetOverlaysApplied(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)

	categoryField := json.RawMessage(`{"field":"category"}`)
	scoreField := json.RawMessage(`{"field":"score"}`)

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Name:   "idx_pop",
				Kind:   types.OverlayKindIndexVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: categoryField,
			},
			{
				Name:   "z_pop",
				Kind:   types.OverlayKindZScoreVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: categoryField,
			},
			{
				Name:   "chisq_pop",
				Kind:   types.OverlayKindChiSqVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: categoryField,
			},
			{
				Name:   "ks_pop",
				Kind:   types.OverlayKindKSVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: scoreField,
			},
		},
	}

	env := ValidateFacetFromBytes(data, req)
	result, ok := env.Data.(*FacetValidationResult)
	if !ok {
		t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("expected zero errors on happy-path four-kind FacetRequest, got %+v", env.Errors)
	}
	if !result.Valid {
		t.Fatalf("FacetValidationResult.Valid = false; expected true on happy path")
	}

	if got, want := len(result.OverlaysApplied), len(req.Overlays); got != want {
		t.Fatalf("OverlaysApplied length = %d, want %d", got, want)
	}
	for i, spec := range req.Overlays {
		desc := result.OverlaysApplied[i]
		if desc.Name != spec.Name {
			t.Errorf("OverlaysApplied[%d].Name = %q, want %q", i, desc.Name, spec.Name)
		}
		if desc.Kind != spec.Kind {
			t.Errorf("OverlaysApplied[%d].Kind = %q, want %q", i, desc.Kind, spec.Kind)
		}
		if desc.Scope != spec.Scope {
			t.Errorf("OverlaysApplied[%d].Scope = %q, want %q", i, desc.Scope, spec.Scope)
		}
		wantStreamable, known := types.OverlayStreamable(spec.Kind)
		if !known {
			t.Errorf("OverlaysApplied[%d]: kind %q absent from streamability table", i, spec.Kind)
			continue
		}
		if desc.Streamable != wantStreamable {
			t.Errorf("OverlaysApplied[%d].Streamable = %v, want %v (from OverlayStreamable)",
				i, desc.Streamable, wantStreamable)
		}
	}
}

// TestPredict_FacetOverlayCost_PerKind pins the per-kind OverlayCost
// dispatch for the four FACET-host kinds. INDEX_VS_POP and ZSCORE_VS_POP
// stream (cost = overlayCostStreamable); CHISQ_VS_POP and KS_VS_POP are
// inherently buffered per PRD §2 Non-Goals (cost = overlayCostBuffered).
// The dispatch reads types.OverlayStreamable(kind) — flipping a kind's
// streamability automatically flips its cost here.
func TestPredict_FacetOverlayCost_PerKind(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)

	categoryField := json.RawMessage(`{"field":"category"}`)
	scoreField := json.RawMessage(`{"field":"score"}`)

	cases := []struct {
		name   string
		kind   types.OverlayKind
		field  json.RawMessage
		want   float64
		reason string
	}{
		{
			name:   "idx_pop",
			kind:   types.OverlayKindIndexVsPop,
			field:  categoryField,
			want:   overlayCostStreamable,
			reason: "INDEX_VS_POP streams (descriptive FACET-host kind)",
		},
		{
			name:   "z_pop",
			kind:   types.OverlayKindZScoreVsPop,
			field:  categoryField,
			want:   overlayCostStreamable,
			reason: "ZSCORE_VS_POP streams (descriptive FACET-host kind)",
		},
		{
			name:   "chisq_pop",
			kind:   types.OverlayKindChiSqVsPop,
			field:  categoryField,
			want:   overlayCostBuffered,
			reason: "CHISQ_VS_POP buffered (inferential FACET-host kind)",
		},
		{
			name:   "ks_pop",
			kind:   types.OverlayKindKSVsPop,
			field:  scoreField,
			want:   overlayCostBuffered,
			reason: "KS_VS_POP buffered (inferential FACET-host kind)",
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			req := &types.FacetRequest{
				Cohort: &types.Cohort{Filename: "x.pulse"},
				Fields: []string{"category", "score"},
				Overlays: []types.OverlaySpec{
					{
						Name:   tc.name,
						Kind:   tc.kind,
						Scope:  types.OverlayScopeGroup,
						Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
						Params: tc.field,
					},
				},
			}

			env := ValidateFacetFromBytes(data, req)
			result, ok := env.Data.(*FacetValidationResult)
			if !ok {
				t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
			}
			cost, ok := result.OverlayCost[tc.name]
			if !ok {
				keys := make([]string, 0, len(result.OverlayCost))
				for k := range result.OverlayCost {
					keys = append(keys, k)
				}
				t.Fatalf("OverlayCost missing key %q; keys: %v", tc.name, keys)
			}
			if cost != tc.want {
				t.Errorf("OverlayCost[%q] = %v, want %v (%s)",
					tc.name, cost, tc.want, tc.reason)
			}
		})
	}
}

// TestPredict_FacetOverlaysApplied_EmptyWhenNoOverlays pins the empty-
// not-nil contract for the FacetValidationResult overlay surface. A
// FacetRequest without Overlays must still emit OverlaysApplied as an
// empty (non-nil) slice and OverlayCost as an empty (non-nil) map so the
// JSON envelope output stays byte-identical regardless of whether the
// caller asked for overlays. Mirrors PredictResult's empty-but-not-nil
// guarantee.
func TestPredict_FacetOverlaysApplied_EmptyWhenNoOverlays(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)
	env := ValidateFacetFromBytes(data, &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"category"},
	})
	result, ok := env.Data.(*FacetValidationResult)
	if !ok {
		t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
	}
	if result.OverlaysApplied == nil {
		t.Errorf("OverlaysApplied = nil; expected empty (non-nil) slice")
	}
	if len(result.OverlaysApplied) != 0 {
		t.Errorf("OverlaysApplied = %+v; expected empty", result.OverlaysApplied)
	}
	if result.OverlayCost == nil {
		t.Errorf("OverlayCost = nil; expected empty (non-nil) map")
	}
	if len(result.OverlayCost) != 0 {
		t.Errorf("OverlayCost = %+v; expected empty", result.OverlayCost)
	}
}

// TestPredict_FacetOverlaysApplied_SynthesizesDefaultName mirrors the
// PredictResult contract: an OverlaySpec with empty Name produces a
// synthesised descriptor name derived from Kind + Scope so the OverlayCost
// key matches the OverlayAppliedDescriptor.Name. The synthesised default
// stays aligned with overlayDescriptorName so renderers can correlate the
// cost map back to the descriptor entry without re-parsing the spec.
func TestPredict_FacetOverlaysApplied_SynthesizesDefaultName(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				// No Name set — populator must synthesise a deterministic
				// default keyed on Kind + Scope.
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
			},
		},
	}

	env := ValidateFacetFromBytes(data, req)
	result, ok := env.Data.(*FacetValidationResult)
	if !ok {
		t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
	}
	if len(result.OverlaysApplied) != 1 {
		t.Fatalf("OverlaysApplied length = %d, want 1", len(result.OverlaysApplied))
	}
	desc := result.OverlaysApplied[0]
	if desc.Name == "" {
		t.Fatalf("OverlaysApplied[0].Name is empty; expected synthesised default")
	}
	// OverlayCost key must match the descriptor Name — same alignment
	// contract as PredictResult so renderers can correlate the cost
	// score back to its descriptor entry.
	if _, ok := result.OverlayCost[desc.Name]; !ok {
		keys := make([]string, 0, len(result.OverlayCost))
		for k := range result.OverlayCost {
			keys = append(keys, k)
		}
		t.Errorf("OverlayCost missing synthesised key %q; keys: %v", desc.Name, keys)
	}
}

func TestValidateOverlay_FacetKsOnCategoricalRejected(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)
	categoryField := json.RawMessage(`{"field":"category"}`)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Kind:   types.OverlayKindKSVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: categoryField,
			},
		},
	}
	env := ValidateFacetFromBytes(data, req)
	result, ok := env.Data.(*FacetValidationResult)
	if !ok {
		t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
	}
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for categorical host on KS_VS_POP, got %+v", env.Errors)
	}
	if result.Valid {
		t.Errorf("FacetValidationResult.Valid = true; expected false on KS_VS_POP categorical-host rejection")
	}
}

func TestValidateOverlay_FacetPopulationRefUnknown(t *testing.T) {
	data := buildFacetOverlayPulseBytes(t)
	unknownField := json.RawMessage(`{"field":"nope"}`)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Kind:   types.OverlayKindIndexVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: unknownField,
			},
		},
	}
	env := ValidateFacetFromBytes(data, req)
	result, ok := env.Data.(*FacetValidationResult)
	if !ok {
		t.Fatalf("envelope Data is not *FacetValidationResult: %T", env.Data)
	}
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for unknown population-reference field, got %+v", env.Errors)
	}
	if result.Valid {
		t.Errorf("FacetValidationResult.Valid = true; expected false on unknown-field rejection")
	}
}
