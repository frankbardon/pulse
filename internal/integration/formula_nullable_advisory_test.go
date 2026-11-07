package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFormulaNullableResult_AdvisoryMatchesRuntime: Pulse.Predict
// raises PULSE_ADVISORY_FORMULA_NULLABLE_RESULT exactly for the
// ATTR_FORMULAs Process really writes nulls for — an unguarded nullable
// read or a literal nil branch over a cohort with null rows yields
// n_null > 0 downstream, a `??` / `== nil` guarded one yields none — and
// a null under an operator (which the run refuses, never nulls) stays
// silent. The parity cohort's score is null on 14 of its 96 rows.
func TestFormulaNullableResult_AdvisoryMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "fn.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	code := string(errors.PULSE_ADVISORY_FORMULA_NULLABLE_RESULT)
	for _, tc := range []struct {
		expr     string
		advisory bool
		refused  bool
	}{
		{"score", true, false},
		{"qty > 6 ? qty : nil", true, false},
		{"score ?? 0", false, false},
		{"score == nil ? 0 : score", false, false},
		{"score != nil ? score : 0", false, false},
		{"score * 2", false, true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "fn.pulse"},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: tc.expr, Label: "out"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "out", Label: "s"}},
			}
			pred, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			fired := false
			for _, a := range pred.Advisories {
				fired = fired || a.Code == code
			}
			if fired != tc.advisory || !pred.Valid {
				t.Fatalf("advisory fired=%v valid=%v (%+v), want fired=%v on a valid request", fired, pred.Valid, pred.Advisories, tc.advisory)
			}
			resp, err := p.Process(ctx, req)
			if tc.refused {
				if !errors.HasCode(err, errors.PROCESSING_RUNTIME) {
					t.Fatalf("Process: %v, want PROCESSING_RUNTIME (a null under an operator is refused, never nulled)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
				t.Fatalf("components %+v, want one aggregation entry", resp.Components)
			}
			nulls := resp.Components.Aggregations[0].NNull
			if (nulls > 0) != tc.advisory {
				t.Errorf("n_null = %d with advisory=%v: the advisory must fire exactly when the run writes nulls", nulls, tc.advisory)
			}
		})
	}
}
