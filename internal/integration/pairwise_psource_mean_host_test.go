package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
)

// TestPairwisePSource_MeanHostRefused (survey-tables E4-S6): on an
// unnormalised crosstab an omitted p_source reads every cell as a 0..100
// percentage (cell_value_pct, ÷100). A cell that is a statistic of its
// field — a mean such as AGG_AVERAGE of a 0/1 indicator (0.86 read as
// 0.0086) — is neither a count nor a percentage, so the default scored
// every pair silently wrong. Predict and the runtime now refuse it with
// one PULSE_OVERLAY_PARAM_MISSING code and message (both proportion
// kinds, both crosstab dispatch arms, single and multi-measure hosts);
// an explicit p_source keeps working, and a count cell keeps the
// cell_value_pct default byte-identical.
func TestPairwisePSource_MeanHostRefused(t *testing.T) {
	t.Parallel()

	fs, cohort := priorCohort(t)
	xt := func(cells, extra string) string {
		return `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"h"}],"columns":[{"type":"GROUP_CATEGORY","field":"wave"}],` + cells + extra + `}`
	}
	const avg = `"cell":{"type":"AGG_AVERAGE","field":"yes","label":"c"}`
	const welford = `"cell":{"type":"AGG_WELFORD","field":"x","label":"c"}`
	const count = `"cell":{"type":"AGG_COUNT","field":"yes","label":"c"}`
	const multi = `"cells":[{"type":"AGG_COUNT","field":"yes","label":"n"},{"type":"AGG_AVERAGE","field":"yes","label":"p"}]`
	ov := func(kind, params string) string {
		return `,"overlays":[{"kind":"` + kind + `","scope":"row"` + params + `}]`
	}
	for _, kind := range []string{"OVERLAY_PAIRWISE_PROP_Z", "OVERLAY_PAIRWISE_PROBIT_T"} {
		refused := []struct{ name, body string }{
			{"mean cell", xt(avg, "") + ov(kind, "")},
			{"mean cell normalize none", xt(avg, `,"normalize":"none"`) + ov(kind, "")},
			{"welford cell", xt(welford, "") + ov(kind, "")},
			{"multi-measure host with a mean", xt(multi, "") + ov(kind, "")},
		}
		accepted := []struct{ name, body string }{
			{"mean cell explicit cell_value", xt(avg, "") + ov(kind, `,"params":{"p_source":"cell_value"}`)},
			{"mean cell explicit cell_value_pct", xt(avg, "") + ov(kind, `,"params":{"p_source":"cell_value_pct"}`)},
			{"count cell omitted", xt(count, "") + ov(kind, "")},
			{"mean cell normalized", xt(avg, `,"normalize":"column"`) + ov(kind, "")},
		}
		for _, disable := range []bool{false, true} {
			p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range refused {
				t.Run(fmt.Sprintf("%s/%s/disable_fusion_%v", kind, c.name, disable), func(t *testing.T) {
					req := priorRequest(t, cohort, c.body)
					_, rerr := p.Process(context.Background(), req)
					var ce *errors.CodedError
					if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_OVERLAY_PARAM_MISSING {
						t.Fatalf("runtime = %v, want PULSE_OVERLAY_PARAM_MISSING", rerr)
					}
					if ce.Details["param"] != "p_source" || ce.Details["cell_type"] == nil {
						t.Fatalf("details = %v", ce.Details)
					}
					env := predictEnvelope(t, p, fs, cohort, req)
					matched := false
					for _, e := range env.Errors {
						matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
					}
					if !matched {
						t.Fatalf("predict %q lacks the runtime's %s %q", envCodes(env), ce.Code, ce.Message)
					}
				})
			}
			for _, c := range accepted {
				t.Run(fmt.Sprintf("%s/%s/disable_fusion_%v", kind, c.name, disable), func(t *testing.T) {
					req := priorRequest(t, cohort, c.body)
					if _, err := p.Process(context.Background(), req); err != nil {
						t.Fatalf("runtime refused: %v", err)
					}
					if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
						t.Fatalf("predict refused: %q", envCodes(env))
					}
				})
			}
			t.Run(fmt.Sprintf("%s/count default unchanged/disable_fusion_%v", kind, disable), func(t *testing.T) {
				omitted, err := p.Process(context.Background(), priorRequest(t, cohort, xt(count, "")+ov(kind, "")))
				if err != nil {
					t.Fatal(err)
				}
				explicit, err := p.Process(context.Background(), priorRequest(t, cohort, xt(count, "")+ov(kind, `,"params":{"p_source":"cell_value_pct"}`)))
				if err != nil {
					t.Fatal(err)
				}
				a, _ := json.Marshal(omitted.Overlays)
				b, _ := json.Marshal(explicit.Overlays)
				if string(a) != string(b) {
					t.Fatalf("count cell: omitted p_source moved off cell_value_pct:\n%s\n%s", a, b)
				}
			})
		}
	}
}
