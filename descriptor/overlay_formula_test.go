package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestValidateOverlay_FormulaInvalidIdent asserts that an OVERLAY_FORMULA
// spec referencing an identifier not in the per-shape variable
// namespace surfaces PULSE_OVERLAY_FORMULA_INVALID_IDENT on the
// envelope. Acceptance criterion 1 of E8-S4.
//
// Cases mirror the per-host-shape namespace decisions from research
// note `.planning/result-overlay-system/research/formula-namespace.md`:
//
//   - MATRIX host: a formula referencing `undefined_var` fails the
//     allowed-vars check (allowed: cell, margin_*, sd_*).
//   - SERIES host: a formula referencing `cell` fails (allowed:
//     value, total, prior).
//   - SCALAR host: a formula referencing `total` fails (allowed:
//     value).
//   - SERIES host without `baseline_position` param: a formula
//     referencing `baseline` fails (the opt-in variable is gated on
//     the Params slot per research note § 2.2).
//   - Any host: a `slot.<label>.<field>` reference fails on a
//     Request-host (slot namespace is Compose-only).
func TestValidateOverlay_FormulaInvalidIdent(t *testing.T) {
	cases := []struct {
		name             string
		req              *types.Request
		formula          string
		wantErrFragment  string
		wantUnknownIdent string
	}{
		{
			name: "matrix_host_unknown_var",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
			},
			formula:          "(cell - undefined_var) / sd_grand",
			wantErrFragment:  "undefined_var",
			wantUnknownIdent: "undefined_var",
		},
		{
			name: "series_host_rejects_matrix_var",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			},
			formula:          "cell + value",
			wantErrFragment:  "cell",
			wantUnknownIdent: "cell",
		},
		{
			name: "scalar_host_rejects_series_var",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
			},
			formula:          "value + total",
			wantErrFragment:  "total",
			wantUnknownIdent: "total",
		},
		{
			name: "series_host_baseline_without_param",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			},
			formula:          "value / baseline",
			wantErrFragment:  "baseline",
			wantUnknownIdent: "baseline",
		},
		{
			name: "request_host_rejects_slot_namespace",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
			},
			formula:          "cell + slot.us.cell",
			wantErrFragment:  "slot.us.cell",
			wantUnknownIdent: "slot.us.cell",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindFormula,
					Scope:  formulaSpecScopeFor(req),
					Params: json.RawMessage(`{"formula":"` + tc.formula + `"}`),
				},
			}
			env := NewEnvelope(nil)
			ValidateOverlays(env, req, nil, nil)
			if !hasErrorCode(env, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Fatalf("expected PULSE_OVERLAY_FORMULA_INVALID_IDENT; got %v", codes)
			}
			// Walk the matching error and assert Details carry the offender.
			found := false
			for _, e := range env.Errors {
				if e.Code != string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
					continue
				}
				got, _ := e.Details["unknown_identifier"].(string)
				if got != tc.wantUnknownIdent {
					t.Fatalf("Details.unknown_identifier = %q; want %q",
						got, tc.wantUnknownIdent)
				}
				if _, ok := e.Details["allowed"]; !ok {
					// The slot-namespace branch carries `reason` instead of
					// `allowed`; both are acceptable as long as Details
					// carry the offending identifier.
					if _, ok2 := e.Details["reason"]; !ok2 {
						t.Errorf("Details missing both `allowed` and `reason` slot")
					}
				}
				found = true
			}
			if !found {
				t.Fatalf("matching error did not carry expected Details")
			}
		})
	}
}

// TestValidateOverlay_FormulaParseError asserts that a malformed
// formula string (one expr-lang cannot parse) surfaces
// PULSE_OVERLAY_FORMULA_PARSE_ERROR on the envelope with the parser's
// error message echoed in Details. Acceptance criterion 2 of E8-S4.
func TestValidateOverlay_FormulaParseError(t *testing.T) {
	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFormula,
				Scope: types.OverlayScopeCell,
				// Unbalanced parenthesis — expr-lang rejects at parse time.
				Params: json.RawMessage(`{"formula":"(cell + margin_row"}`),
			},
		},
	}
	env := NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_FORMULA_PARSE_ERROR; got %v", codes)
	}
	for _, e := range env.Errors {
		if e.Code != string(errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR) {
			continue
		}
		if _, ok := e.Details["parse_error"].(string); !ok {
			t.Errorf("Details.parse_error missing or non-string: %v", e.Details)
		}
		if got, _ := e.Details["formula"].(string); got == "" {
			t.Errorf("Details.formula missing or empty")
		}
	}
}

// TestValidateOverlay_FormulaExprFunctionAllowed asserts that a formula
// calling an embedder-registered ExprFunction passes the predict gate
// when the function name is in the snapshot's ExprFunctions set, and
// fails with PULSE_OVERLAY_FORMULA_INVALID_IDENT when it is NOT
// registered. Acceptance criterion 3 of E8-S4.
func TestValidateOverlay_FormulaExprFunctionAllowed(t *testing.T) {
	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:   types.OverlayKindFormula,
				Scope:  types.OverlayScopeCell,
				Params: json.RawMessage(`{"formula":"pct_change(cell, margin_row)"}`),
			},
		},
	}
	// Case 1: function is NOT registered — predict rejects.
	envMissing := NewEnvelope(nil)
	ValidateOverlays(envMissing, req, nil, nil)
	if !hasErrorCode(envMissing, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
		codes := make([]string, 0, len(envMissing.Errors))
		for _, e := range envMissing.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_FORMULA_INVALID_IDENT for unregistered function; got %v", codes)
	}

	// Case 2: function IS registered — predict accepts.
	opts := &PredictOptions{
		Extensions: &ExtensionsSnapshot{
			ExprFunctions: []ExprFunctionMeta{
				{Name: "pct_change"},
			},
		},
	}
	envPresent := NewEnvelope(nil)
	ValidateOverlays(envPresent, req, nil, opts)
	for _, code := range []errors.Code{
		errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
		errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR,
	} {
		if hasErrorCode(envPresent, code) {
			t.Errorf("unexpected overlay error %s on registered-function happy-path", code)
		}
	}
}

// TestValidateOverlay_FormulaSlotLabelRequired asserts that a
// `slot.<label>.cell` reference on a Request-host fires
// PULSE_OVERLAY_FORMULA_INVALID_IDENT with a Detail explaining the
// slot namespace is Compose-only. Acceptance criterion 4 of E8-S4 +
// the "Slot identifiers validated against Compose slot labels" rule.
func TestValidateOverlay_FormulaSlotLabelRequired(t *testing.T) {
	req := &types.Request{
		Crosstab: crosstabHostSpec(),
		Overlays: []types.OverlaySpec{
			{
				Kind:  types.OverlayKindFormula,
				Scope: types.OverlayScopeCell,
				// `slot.foo.cell` is the canonical Compose dotted-namespace
				// shape — on Request.Overlays it must fail because there
				// are no Compose slots to resolve against.
				Params: json.RawMessage(`{"formula":"slot.foo.cell"}`),
			},
		},
	}
	env := NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)
	if !hasErrorCode(env, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("expected PULSE_OVERLAY_FORMULA_INVALID_IDENT; got %v", codes)
	}
	for _, e := range env.Errors {
		if e.Code != string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
			continue
		}
		got, _ := e.Details["unknown_identifier"].(string)
		if got != "slot.foo.cell" {
			t.Fatalf("Details.unknown_identifier = %q; want \"slot.foo.cell\"", got)
		}
		reason, _ := e.Details["reason"].(string)
		if reason != "slot namespace is Compose-only" {
			t.Errorf("Details.reason = %q; want \"slot namespace is Compose-only\"", reason)
		}
	}
}

// TestValidateOverlay_FormulaHappyPath asserts that a well-formed
// FORMULA spec referencing only per-shape namespace variables passes
// the predict gate without surfacing any FORMULA-specific errors.
// Covers each host shape (matrix / series / scalar) with the canonical
// expression for that shape per research note § 2.
func TestValidateOverlay_FormulaHappyPath(t *testing.T) {
	cases := []struct {
		name    string
		req     *types.Request
		formula string
	}{
		{
			name: "matrix_host_cell_margin_sd",
			req: &types.Request{
				Crosstab: crosstabHostSpec(),
			},
			formula: "(cell - margin_grand) / sd_grand",
		},
		{
			name: "series_host_value_total_prior",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			},
			formula: "value / total * (value - prior)",
		},
		{
			name: "scalar_host_value_only",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
			},
			formula: "value * 2.0",
		},
		{
			name: "series_host_baseline_opt_in",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			},
			formula: "value / baseline",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			// Build the formula spec; the baseline_position param is set
			// on the baseline-opt-in arm.
			var params json.RawMessage
			if tc.name == "series_host_baseline_opt_in" {
				params = json.RawMessage(`{"formula":"` + tc.formula + `","baseline_position":0}`)
			} else {
				params = json.RawMessage(`{"formula":"` + tc.formula + `"}`)
			}
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindFormula,
					Scope:  formulaSpecScopeFor(req),
					Params: params,
				},
			}
			env := NewEnvelope(nil)
			ValidateOverlays(env, req, nil, nil)
			for _, code := range []errors.Code{
				errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
				errors.PULSE_OVERLAY_FORMULA_PARSE_ERROR,
			} {
				if hasErrorCode(env, code) {
					codes := make([]string, 0, len(env.Errors))
					for _, e := range env.Errors {
						codes = append(codes, e.Code+": "+e.Message)
					}
					t.Errorf("unexpected overlay error %s on happy-path formula %q: %v",
						code, tc.formula, codes)
				}
			}
		})
	}
}

// formulaSpecScopeFor returns the canonical OverlayScope for a FORMULA
// spec given the Request's resolved host shape. Mirrors the per-shape
// scope contract documented on OverlayKindFormula (matrix ⇒ cell,
// series ⇒ group, scalar ⇒ total). Test-only helper — keeps the table
// of cases above tight.
func formulaSpecScopeFor(req *types.Request) types.OverlayScope {
	if req == nil {
		return types.OverlayScopeTotal
	}
	if req.Crosstab != nil {
		return types.OverlayScopeCell
	}
	if len(req.Groups) > 0 {
		return types.OverlayScopeGroup
	}
	return types.OverlayScopeTotal
}

// TestFormulaNamespace_PerShape asserts that the canonical per-shape
// variable set returned by `types.FormulaNamespace` matches the
// research-note tables (§ 2.1 / 2.2 / 2.3). Pinning the contract here
// guards against silent drift between the runtime env builders and
// the predict-time allowed set.
func TestFormulaNamespace_PerShape(t *testing.T) {
	cases := []struct {
		shape   types.OverlayShape
		wantSet map[string]bool
	}{
		{
			shape: types.OverlayShapeMatrix,
			wantSet: map[string]bool{
				"cell":         true,
				"margin_row":   true,
				"margin_col":   true,
				"margin_grand": true,
				"sd_row":       true,
				"sd_col":       true,
				"sd_grand":     true,
			},
		},
		{
			shape: types.OverlayShapeSeries,
			wantSet: map[string]bool{
				"value": true,
				"total": true,
				"prior": true,
			},
		},
		{
			shape: types.OverlayShapeScalar,
			wantSet: map[string]bool{
				"value": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.shape), func(t *testing.T) {
			got := types.FormulaNamespace(tc.shape)
			gotSet := map[string]bool{}
			for _, v := range got {
				gotSet[v] = true
			}
			if len(gotSet) != len(tc.wantSet) {
				t.Fatalf("namespace size = %d; want %d (got %v, want %v)",
					len(gotSet), len(tc.wantSet), got, tc.wantSet)
			}
			for k := range tc.wantSet {
				if !gotSet[k] {
					t.Errorf("namespace missing identifier %q (got %v)", k, got)
				}
			}
		})
	}
}
