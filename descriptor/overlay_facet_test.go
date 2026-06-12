package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ValidateFacetOverlays tests (E5-S6). The per-kind contract:
//
//   - Ref.Population only; every other ref-family pointer is a shape
//     mismatch.
//   - Scope=GROUP only; other scopes fire PULSE_OVERLAY_SCOPE_UNSUPPORTED.
//   - Level / Within MUST be 0; non-zero fires
//     PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
//   - CHISQ_VS_POP rejects numeric hosts; KS_VS_POP rejects categorical
//     hosts.
//   - Single-field default: Params["field"] is optional when the
//     FacetRequest carries exactly one Field; required when multiple
//     fields are present.
//   - Non-FACET kinds attached to FacetRequest.Overlays fire
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.

// buildOverlayFacetSchema returns a schema with one categorical
// "category" field and one numeric "score" field — the canonical fixture
// for FACET-host overlay validator tests.
func buildOverlayFacetSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1},
		},
	}
}

// hasError reports whether env carries an error with the given code.
func hasError(env *Envelope, code string) bool {
	for _, e := range env.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

// errorMessagesContaining returns the messages of all errors with the
// given code on env. Diagnostic helper for tests that assert specific
// detail strings.
func errorMessagesContaining(env *Envelope, code string) []string {
	var out []string
	for _, e := range env.Errors {
		if e.Code == code {
			out = append(out, e.Message)
		}
	}
	return out
}

// TestValidateFacetOverlays_NoOverlaysShortCircuits asserts the
// no-overlay byte-identity path — an empty Overlays slot produces no
// validator errors at all.
func TestValidateFacetOverlays_NoOverlaysShortCircuits(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{Fields: []string{"category"}}
	ValidateFacetOverlays(env, req, schema)
	if len(env.Errors) != 0 {
		t.Errorf("expected no errors with empty Overlays slot, got %d: %+v", len(env.Errors), env.Errors)
	}
}

// TestValidateFacetOverlays_HappyPathAllFourKinds asserts that a valid
// FacetRequest carrying one spec per FACET-host kind passes the
// validator with no errors.
func TestValidateFacetOverlays_HappyPathAllFourKinds(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	categoryField := json.RawMessage(`{"field":"category"}`)
	scoreField := json.RawMessage(`{"field":"score"}`)
	req := &types.FacetRequest{
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}}, Params: categoryField},
			{Kind: types.OverlayKindZScoreVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}}, Params: categoryField},
			{Kind: types.OverlayKindChiSqVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}}, Params: categoryField},
			{Kind: types.OverlayKindKSVsPop, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}}, Params: scoreField},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if len(env.Errors) != 0 {
		t.Fatalf("expected no errors on happy path, got %d: %+v", len(env.Errors), env.Errors)
	}
}

// TestValidateFacetOverlays_UnknownKind asserts that a kind not in
// types.AllOverlayKinds() fires PULSE_OVERLAY_KIND_UNKNOWN.
func TestValidateFacetOverlays_UnknownKind(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{Kind: "OVERLAY_GIBBERISH", Scope: types.OverlayScopeGroup},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_KIND_UNKNOWN)) {
		t.Errorf("expected PULSE_OVERLAY_KIND_UNKNOWN, got: %+v", env.Errors)
	}
}

// TestValidateFacetOverlays_FacetHostKinds_RejectInvalidRef exercises
// the single-arm Ref union enforcement per kind. Each test arm pairs a
// FACET kind with a wrong ref-family pointer; the validator must reject
// every combination with PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateFacetOverlays_FacetHostKinds_RejectInvalidRef(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	kinds := []types.OverlayKind{
		types.OverlayKindIndexVsPop,
		types.OverlayKindZScoreVsPop,
		types.OverlayKindChiSqVsPop,
		types.OverlayKindKSVsPop,
	}
	wrongRefs := []struct {
		name string
		ref  types.OverlayRef
	}{
		{"margin", types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}},
		{"sibling", types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "f", Value: "v"}}},
		{"baseline_index", types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: 0}}},
		{"prior", types.OverlayRef{Prior: &types.OverlayPriorRef{}}},
		{"rolling_mean", types.OverlayRef{RollingMean: &types.OverlayRollingMeanRef{}}},
		{"yoy", types.OverlayRef{YoY: &types.OverlayYoYRef{}}},
		{"stage", types.OverlayRef{Stage: &types.StageRef{}}},
		{"slot", types.OverlayRef{Slot: &types.OverlaySlotRef{}}},
		{"empty_ref", types.OverlayRef{}},
		{"empty_cohort", types.OverlayRef{Population: &types.OverlayPopulationRef{}}},
	}
	for _, kind := range kinds {
		for _, wrong := range wrongRefs {
			t.Run(string(kind)+"_"+wrong.name, func(t *testing.T) {
				env := NewEnvelope(nil)
				field := "category"
				if kind == types.OverlayKindKSVsPop {
					field = "score"
				}
				req := &types.FacetRequest{
					Fields: []string{field},
					Overlays: []types.OverlaySpec{
						{Kind: kind, Scope: types.OverlayScopeGroup, Ref: wrong.ref},
					},
				}
				ValidateFacetOverlays(env, req, schema)
				if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
					t.Errorf("kind=%s wrong-ref=%s: expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, got %+v", kind, wrong.name, env.Errors)
				}
			})
		}
	}
}

// TestValidateFacetOverlays_ScopeMustBeGroup asserts every non-GROUP
// scope is rejected for every FACET kind.
func TestValidateFacetOverlays_ScopeMustBeGroup(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	scopes := []types.OverlayScope{
		types.OverlayScopeCell,
		types.OverlayScopeRow,
		types.OverlayScopeColumn,
		types.OverlayScopeMatrix,
		types.OverlayScopeTotal,
	}
	kinds := []types.OverlayKind{
		types.OverlayKindIndexVsPop,
		types.OverlayKindZScoreVsPop,
		types.OverlayKindChiSqVsPop,
		types.OverlayKindKSVsPop,
	}
	for _, kind := range kinds {
		for _, scope := range scopes {
			t.Run(string(kind)+"_"+string(scope), func(t *testing.T) {
				env := NewEnvelope(nil)
				field := "category"
				if kind == types.OverlayKindKSVsPop {
					field = "score"
				}
				req := &types.FacetRequest{
					Fields: []string{field},
					Overlays: []types.OverlaySpec{
						{Kind: kind, Scope: scope, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}}},
					},
				}
				ValidateFacetOverlays(env, req, schema)
				if !hasError(env, string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED)) {
					t.Errorf("kind=%s scope=%s: expected PULSE_OVERLAY_SCOPE_UNSUPPORTED, got %+v", kind, scope, env.Errors)
				}
			})
		}
	}
}

// TestValidateFacetOverlays_LevelWithinMustBeZero asserts that non-zero
// Level / Within slots fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on every
// FACET kind — the population-comparison family is a single-value
// lookup, not an axis-prefix denominator.
func TestValidateFacetOverlays_LevelWithinMustBeZero(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	kinds := []types.OverlayKind{
		types.OverlayKindIndexVsPop,
		types.OverlayKindZScoreVsPop,
		types.OverlayKindChiSqVsPop,
		types.OverlayKindKSVsPop,
	}
	for _, kind := range kinds {
		for _, mod := range []struct {
			name   string
			level  int
			within int
		}{{"level", 1, 0}, {"within", 0, 1}, {"both", 1, 1}} {
			t.Run(string(kind)+"_"+mod.name, func(t *testing.T) {
				env := NewEnvelope(nil)
				field := "category"
				if kind == types.OverlayKindKSVsPop {
					field = "score"
				}
				req := &types.FacetRequest{
					Fields: []string{field},
					Overlays: []types.OverlaySpec{
						{
							Kind:   kind,
							Scope:  types.OverlayScopeGroup,
							Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
							Level:  mod.level,
							Within: mod.within,
						},
					},
				}
				ValidateFacetOverlays(env, req, schema)
				if !hasError(env, string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE)) {
					t.Errorf("kind=%s mod=%s: expected PULSE_OVERLAY_LEVEL_OUT_OF_RANGE, got %+v", kind, mod.name, env.Errors)
				}
			})
		}
	}
}

// TestValidateFacetOverlays_CHISQ_VS_POP_RejectsNumericHost asserts the
// discrete-arm-only contract — a numeric host field fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateFacetOverlays_CHISQ_VS_POP_RejectsNumericHost(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	scoreField := json.RawMessage(`{"field":"score"}`)
	req := &types.FacetRequest{
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Kind:   types.OverlayKindChiSqVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: scoreField,
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for numeric host on CHISQ_VS_POP, got %+v", env.Errors)
	}
	msgs := errorMessagesContaining(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE))
	if len(msgs) == 0 || !strings.Contains(msgs[0], "discrete host") {
		t.Errorf("expected error mentioning discrete host requirement, got %+v", msgs)
	}
}

// TestValidateFacetOverlays_KS_VS_POP_RejectsCategoricalHost asserts the
// numeric-arm-only contract — a categorical host field fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateFacetOverlays_KS_VS_POP_RejectsCategoricalHost(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	categoryField := json.RawMessage(`{"field":"category"}`)
	req := &types.FacetRequest{
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
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for categorical host on KS_VS_POP, got %+v", env.Errors)
	}
	msgs := errorMessagesContaining(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE))
	if len(msgs) == 0 || !strings.Contains(msgs[0], "numeric host") {
		t.Errorf("expected error mentioning numeric host requirement, got %+v", msgs)
	}
}

// TestValidateFacetOverlays_MultiFieldRequiresParamsField asserts that
// when the FacetRequest declares multiple Fields, an OverlaySpec without
// Params["field"] fires PULSE_OVERLAY_PARAM_MISSING.
func TestValidateFacetOverlays_MultiFieldRequiresParamsField(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{
		Fields: []string{"category", "score"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				// No Params; multi-field req.
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_PARAM_MISSING)) {
		t.Errorf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
	}
}

// TestValidateFacetOverlays_SingleFieldDefaultsToHost asserts that when
// the FacetRequest declares exactly one Field, an OverlaySpec without
// Params["field"] resolves to that single field with no error.
func TestValidateFacetOverlays_SingleFieldDefaultsToHost(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				// No Params; single-field req — default applies.
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if len(env.Errors) != 0 {
		t.Errorf("single-field default should pass without errors, got %+v", env.Errors)
	}
}

// TestValidateFacetOverlays_UnknownFieldRejected asserts that a
// Params["field"] that does not reference any FacetRequest.Fields entry
// fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
func TestValidateFacetOverlays_UnknownFieldRejected(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	wrongField := json.RawMessage(`{"field":"nope"}`)
	req := &types.FacetRequest{
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:   types.OverlayKindIndexVsPop,
				Scope:  types.OverlayScopeGroup,
				Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
				Params: wrongField,
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for unknown field, got %+v", env.Errors)
	}
}

// TestValidateFacetOverlays_NonFacetKindOnFacetHost asserts that a
// Request-host kind (e.g. INDEX_VS_MARGIN) attached to
// FacetRequest.Overlays fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
// with a "not a FACET-host kind" message so the caller redirects to
// Request.Overlays.
func TestValidateFacetOverlays_NonFacetKindOnFacetHost(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on non-FACET kind, got %+v", env.Errors)
	}
	msgs := errorMessagesContaining(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE))
	if len(msgs) == 0 || !strings.Contains(msgs[0], "FACET-host kind") {
		t.Errorf("expected error mentioning FACET-host kind, got %+v", msgs)
	}
}

// TestValidateOverlays_FacetKindOnRequest asserts that the reciprocal
// — a FACET-host kind attached to Request.Overlays — is rejected by
// the Request-host validator (descriptor/overlay.go switch arm). Failure
// shape: PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with a message naming
// the FacetRequest.Overlays redirection.
func TestValidateOverlays_FacetKindOnRequest(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "category"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "category"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT},
		},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
			},
		},
	}
	ValidateOverlays(env, req, schema, nil)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Fatalf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE on FACET kind in Request.Overlays, got %+v", env.Errors)
	}
	msgs := errorMessagesContaining(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE))
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "FacetRequest.Overlays") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error mentioning FacetRequest.Overlays redirection, got %+v", msgs)
	}
}

// TestValidateFacetOverlays_PopulationCohortRequired asserts that an
// empty Cohort string on Ref.Population fires
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. The runtime resolver cannot
// open an unnamed cohort.
func TestValidateFacetOverlays_PopulationCohortRequired(t *testing.T) {
	schema := buildOverlayFacetSchema(t)
	env := NewEnvelope(nil)
	req := &types.FacetRequest{
		Fields: []string{"category"},
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: ""}},
			},
		},
	}
	ValidateFacetOverlays(env, req, schema)
	if !hasError(env, string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)) {
		t.Errorf("expected PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE for empty cohort, got %+v", env.Errors)
	}
}

