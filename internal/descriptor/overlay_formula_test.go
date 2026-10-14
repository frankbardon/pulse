package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

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
			env := descriptor.NewEnvelope(nil)
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
	env := descriptor.NewEnvelope(nil)
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
	envMissing := descriptor.NewEnvelope(nil)
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
			ExprFunctions: []descriptor.ExprFunctionMeta{
				{Name: "pct_change"},
			},
		},
	}
	envPresent := descriptor.NewEnvelope(nil)
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
	env := descriptor.NewEnvelope(nil)
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
			name: "scalar_host_value_only",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
			},
			formula: "value * 2.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			params := json.RawMessage(`{"formula":"` + tc.formula + `"}`)
			req.Overlays = []types.OverlaySpec{
				{
					Kind:   types.OverlayKindFormula,
					Scope:  formulaSpecScopeFor(req),
					Params: params,
				},
			}
			env := descriptor.NewEnvelope(nil)
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

// TestValidateOverlay_FormulaBuiltinSubset pins the overlay subset of
// the reserved built-ins: scalar maths passes predict, every vector
// family name fires the unknown-function arm.
func TestValidateOverlay_FormulaBuiltinSubset(t *testing.T) {
	cases := []struct {
		name      string
		formula   string
		wantIdent string // "" = accepted
	}{
		{"sqrt", "sqrt(cell)", ""},
		{"log", "log(cell + 1)", ""},
		{"exp", "exp(cell / sd_grand)", ""},
		{"pow", "pow(cell, 2)", ""},
		{"vmean_refused", "vmean([cell, margin_row])", "vmean"},
		{"dot_refused", "dot([cell], [margin_row])", "dot"},
		{"vnormalize_refused", "sqrt(cell) + vsum(vnormalize([cell], \"l1\"))", "vsum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]any{"formula": tc.formula})
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{{
					Kind:   types.OverlayKindFormula,
					Scope:  types.OverlayScopeCell,
					Params: params,
				}},
			}
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, req, nil, nil)
			if tc.wantIdent == "" {
				for _, e := range env.Errors {
					t.Errorf("unexpected error %s: %s", e.Code, e.Message)
				}
				return
			}
			found := false
			for _, e := range env.Errors {
				if e.Code != string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) {
					continue
				}
				got, _ := e.Details["unknown_identifier"].(string)
				if got != tc.wantIdent && !(tc.name == "vnormalize_refused" && got == "vnormalize") {
					t.Errorf("unknown_identifier = %q, want %q", got, tc.wantIdent)
				}
				found = true
			}
			if !found {
				t.Fatalf("want PULSE_OVERLAY_FORMULA_INVALID_IDENT; got %v", env.Errors)
			}
		})
	}
}

// TestValidateOverlay_FormulaSetHelpersAndLookup: the request-host gate
// admits what the runtime overlay compile registers — the unreserved
// set helpers always, lookup() only when the instance registers a
// LookupTable (without one the runtime compile has no lookup and the
// gate refuses it).
func TestValidateOverlay_FormulaSetHelpersAndLookup(t *testing.T) {
	withTable := &PredictOptions{Extensions: &ExtensionsSnapshot{
		LookupTables: []descriptor.LookupTableMeta{{Name: "rates", HasRowsData: true}},
	}}
	cases := []struct {
		name      string
		formula   string
		opts      *PredictOptions
		wantIdent string // "" = accepted
	}{
		{"has_any", `has_any(["a"], ["a"]) ? cell : 0`, nil, ""},
		{"popcount", `cell + popcount(["a", "b"])`, nil, ""},
		{"set_xor", `len(set_xor(["a"], ["b"])) + cell`, nil, ""},
		{"lookup_with_table", `cell * lookup("rates", "x")`, withTable, ""},
		{"lookup_without_table", `cell * lookup("rates", "x")`, nil, "lookup"},
		{"lookup_no_tables_registered", `cell * lookup("rates", "x")`, &PredictOptions{Extensions: &ExtensionsSnapshot{}}, "lookup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]any{"formula": tc.formula})
			req := &types.Request{
				Crosstab: crosstabHostSpec(),
				Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula, Scope: types.OverlayScopeCell, Params: params}},
			}
			env := descriptor.NewEnvelope(nil)
			ValidateOverlays(env, req, nil, tc.opts)
			if tc.wantIdent == "" {
				for _, e := range env.Errors {
					t.Errorf("unexpected error %s: %s", e.Code, e.Message)
				}
				return
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT) ||
				env.Errors[0].Details["unknown_identifier"] != tc.wantIdent {
				t.Fatalf("errors = %+v, want %s on %q", env.Errors, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT, tc.wantIdent)
			}
		})
	}
}

// TestValidateOverlay_FormulaSeriesHostRefusedByShape: a Process
// OVERLAY_FORMULA runs on a MATRIX host only (its SERIES namespace
// executes on a Compose host), so a grouped request refuses it with the
// shared overlayhost shape refusal before any identifier walk — the
// refusal the runtime's Process hooks raise.
func TestValidateOverlay_FormulaSeriesHostRefusedByShape(t *testing.T) {
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
			Params: json.RawMessage(`{"formula":"value / total"}`)}},
	}
	env := descriptor.NewEnvelope(nil)
	ValidateOverlays(env, req, nil, nil)
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) ||
		env.Errors[0].Details["shape"] != "series" {
		t.Fatalf("errors = %+v, want one shape refusal over a series host", env.Errors)
	}
}
