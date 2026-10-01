package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// marginAggBaseReq is the well-formed crosstab every test in this file
// varies: three margins on, so a declared auxiliary aggregation always
// has somewhere to land.
func marginAggBaseReq() *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "distinct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "audience"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// TestCrosstab_MarginAggregationsMalformedRejected drives every
// structural defect down BOTH dispatch arms. Both call the one
// validateCrosstabSpec, but the fused arm reaches it from a different
// entry point, and a refusal that only fired on the buffered path would
// let a malformed request through on every fusable Visa-shaped scan.
func TestCrosstab_MarginAggregationsMalformedRejected(t *testing.T) {
	schema := distinctMarginSchema()
	cfg := setupTestFS(t, "distinct.pulse", schema, distinctMarginRecords())
	ctx := context.Background()

	cases := []struct {
		name string
		aux  []*types.Aggregation
		cell *types.Aggregation
		want errors.Code
	}{
		{
			name: "null entry",
			aux:  []*types.Aggregation{nil},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID,
		},
		{
			name: "entry with no type",
			aux:  []*types.Aggregation{{Field: "respondent"}},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID,
		},
		{
			name: "two entries share a derived label",
			aux: []*types.Aggregation{
				{Type: types.AGG_DISTINCT_COUNT, Field: "respondent"},
				{Type: types.AGG_DISTINCT_COUNT, Field: "respondent"},
			},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
		},
		{
			name: "entry collides with the cell label",
			cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "respondent", Label: "base"},
			aux:  []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"}},
			want: errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL,
		},
	}

	for _, path := range []struct {
		name       string
		disableFus bool
	}{{"fused", false}, {"buffered", true}} {
		for _, tc := range cases {
			t.Run(path.name+"/"+tc.name, func(t *testing.T) {
				req := marginAggBaseReq()
				if tc.cell != nil {
					req.Crosstab.Cell = tc.cell
				}
				req.Crosstab.MarginAggregations = tc.aux

				svc := New(cfg)
				svc.SetDisableCrosstabFusion(path.disableFus)
				_, err := svc.Process(ctx, req)
				if err == nil {
					t.Fatalf("malformed margin_aggregations accepted; expected %s", tc.want)
				}
				if !errors.HasCode(err, tc.want) {
					t.Errorf("expected %s, got %v", tc.want, err)
				}
			})
		}
	}
}

// TestCrosstab_MarginAggregationsWellFormedAccepted is the non-vacuity
// control for the refusals above: a well-formed auxiliary set must NOT
// be refused, or the tests above would pass against a validator that
// rejects the slot outright.
func TestCrosstab_MarginAggregationsWellFormedAccepted(t *testing.T) {
	schema := distinctMarginSchema()
	cfg := setupTestFS(t, "distinct.pulse", schema, distinctMarginRecords())
	ctx := context.Background()

	for _, path := range []struct {
		name       string
		disableFus bool
	}{{"fused", false}, {"buffered", true}} {
		t.Run(path.name, func(t *testing.T) {
			req := marginAggBaseReq()
			req.Crosstab.MarginAggregations = []*types.Aggregation{
				{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
			}
			svc := New(cfg)
			svc.SetDisableCrosstabFusion(path.disableFus)
			if _, err := svc.Process(ctx, req); err != nil {
				t.Fatalf("well-formed margin_aggregations refused: %v", err)
			}
		})
	}
}

// auxWireKeys names the three Response.Components.Crosstab keys E2-S5
// added. They are listed once so the back-compat assertion and the
// presence assertion cannot drift apart on WHICH keys are new.
var auxWireKeys = []string{
	"row_margin_aggregations",
	"column_margin_aggregations",
	"grand_total_aggregations",
}

// TestCrosstab_MarginAggregationsInertAtRequestSurface is the
// back-compat gate for the auxiliary margin slot, and it has NARROWED
// rather than been deleted.
//
// Through E2-S1 to E2-S4 the slot was accumulated but never emitted, so
// the strongest available statement was that declaring it changed
// NOTHING at all, whole response, byte for byte. E2-S5 puts the figures
// on the wire, so that statement is now false by design and asserting it
// would only pin the feature as unimplemented.
//
// What survives — and is the real guarantee a downstream consumer holds
// — is the OTHER half: a request that declares no auxiliary must produce
// exactly the bytes it produced before the slot existed. That is
// asserted here in the strongest form the test can reach: the response
// for the request WITH the slot, with the three new keys deleted and
// nothing else touched, must be byte-identical to the response for the
// request WITHOUT it. So the auxiliary is proved additive in both
// directions at once — it emitted no key the baseline lacks beyond those
// three, and it perturbed no cell, no margin and no existing components
// entry on the way.
//
// The failure it guards against is unchanged and still produces a
// well-formed, plausible response: an auxiliary aggregation leaking into
// a cell or shifting a margin.
func TestCrosstab_MarginAggregationsInertAtRequestSurface(t *testing.T) {
	schema := distinctMarginSchema()
	cfg := setupTestFS(t, "distinct.pulse", schema, distinctMarginRecords())
	ctx := context.Background()

	// Non-vacuity: the fused arm below is only meaningful while the
	// gate admits this request.
	if ok, reason := processing.CanFuseCrosstab(marginAggBaseReq(), schema, nil); !ok {
		t.Fatalf("baseline crosstab declined fusion: %q — the fused arm would silently be a second buffered run", reason)
	}

	for _, path := range []struct {
		name       string
		disableFus bool
	}{{"fused", false}, {"buffered", true}} {
		t.Run(path.name, func(t *testing.T) {
			run := func(aux []*types.Aggregation) map[string]any {
				t.Helper()
				req := marginAggBaseReq()
				req.Crosstab.MarginAggregations = aux
				svc := New(cfg)
				svc.SetDisableCrosstabFusion(path.disableFus)
				resp, err := svc.Process(ctx, req)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				out, err := json.Marshal(resp)
				if err != nil {
					t.Fatalf("Marshal response: %v", err)
				}
				var generic map[string]any
				if err := json.Unmarshal(out, &generic); err != nil {
					t.Fatalf("Unmarshal response: %v", err)
				}
				return generic
			}

			baseline := run(nil)
			withAux := run([]*types.Aggregation{
				{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
			})

			// The undeclared request must carry none of the new keys —
			// not an empty array, not a null. Anything else is a byte
			// change every existing caller sees.
			baselineCT := crosstabComponentsOf(t, baseline)
			for _, key := range auxWireKeys {
				if _, present := baselineCT[key]; present {
					t.Errorf("a request with NO margin_aggregations emitted %s", key)
				}
			}

			// Delete the three new keys from the declaring response and
			// require the remainder to be identical. This is what makes
			// the addition additive rather than merely additive-looking:
			// a leak into a cell or a shifted margin lands here.
			withCT := crosstabComponentsOf(t, withAux)
			for _, key := range auxWireKeys {
				delete(withCT, key)
			}

			// json.Marshal sorts map keys, so re-marshalling the two
			// generic trees is a canonical comparison.
			before := remarshal(t, baseline)
			after := remarshal(t, withAux)
			if before != after {
				t.Errorf("declaring margin_aggregations changed something other than the "+
					"auxiliary block\nwithout: %s\n   with: %s", before, after)
			}
		})
	}
}

// crosstabComponentsOf reaches into a generic response tree for
// components.crosstab, failing loudly when it is absent — every request
// in this file emits it, so a missing block is a defect rather than a
// case to tolerate.
func crosstabComponentsOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	comps, ok := resp["components"].(map[string]any)
	if !ok {
		t.Fatalf("response carries no components block: %v", resp)
	}
	ct, ok := comps["crosstab"].(map[string]any)
	if !ok {
		t.Fatalf("components carry no crosstab block: %v", comps)
	}
	return ct
}

func remarshal(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(out)
}

// TestCrosstab_MarginAggregationsOnTheWire is the positive half: the
// figures a caller asked for actually arrive, at the same coordinates
// the rest of the components block uses, and IDENTICALLY on both
// dispatch arms.
//
// The cross-arm comparison is the load-bearing part. Dispatch picks
// fused or buffered on request SHAPE — a non-mergeable cell aggregator,
// a quantile axis, a feature — and nothing in Response reports which one
// ran, so a sample-size figure that differed between them would move for
// reasons a caller cannot see and cannot ask about.
func TestCrosstab_MarginAggregationsOnTheWire(t *testing.T) {
	schema := distinctMarginSchema()
	cfg := setupTestFS(t, "distinct.pulse", schema, distinctMarginRecords())
	ctx := context.Background()

	blocks := map[string]string{}
	for _, path := range []struct {
		name       string
		disableFus bool
	}{{"fused", false}, {"buffered", true}} {
		req := marginAggBaseReq()
		req.Crosstab.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
		}
		svc := New(cfg)
		svc.SetDisableCrosstabFusion(path.disableFus)
		resp, err := svc.Process(ctx, req)
		if err != nil {
			t.Fatalf("%s Process: %v", path.name, err)
		}
		ct := resp.Components.Crosstab
		if ct == nil {
			t.Fatalf("%s: no crosstab components", path.name)
		}

		// One entry per axis key, aligned with the matrix coordinates
		// every other margin vector uses.
		matrix := resp.Crosstab.Matrix
		if len(ct.RowMarginAggregations) != len(matrix.RowKeys) {
			t.Errorf("%s: %d row auxiliary entries for %d row keys", path.name,
				len(ct.RowMarginAggregations), len(matrix.RowKeys))
		}
		if len(ct.ColumnMarginAggregations) != len(matrix.ColumnKeys) {
			t.Errorf("%s: %d column auxiliary entries for %d column keys", path.name,
				len(ct.ColumnMarginAggregations), len(matrix.ColumnKeys))
		}
		fig, ok := ct.GrandTotalAggregations["base"]
		if !ok {
			t.Fatalf("%s: grand slot carries no figure labelled base; got %v",
				path.name, ct.GrandTotalAggregations)
		}
		if !fig.Present {
			t.Errorf("%s: grand base reports Present false over a fixture where every "+
				"record is admitted", path.name)
		}
		if fig.Components == nil {
			t.Errorf("%s: grand base carries no components, so the operator's own keys "+
				"are unreachable per slot", path.name)
		}

		out, err := json.Marshal(map[string]any{
			"row":    ct.RowMarginAggregations,
			"column": ct.ColumnMarginAggregations,
			"grand":  ct.GrandTotalAggregations,
		})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		blocks[path.name] = string(out)
	}

	if blocks["fused"] != blocks["buffered"] {
		t.Errorf("auxiliary block differs across dispatch arms\n   fused: %s\nbuffered: %s",
			blocks["fused"], blocks["buffered"])
	}
}
