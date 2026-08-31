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

// TestCrosstab_MarginAggregationsInertAtRequestSurface is the
// byte-identity gate for this story. Declaring an auxiliary margin
// aggregation is a REQUEST-SURFACE change only: the cell values, the
// existing margins and the whole response shape must be exactly what
// they were with the slot absent.
//
// This is deliberately the strongest form available — the same request
// with and without the slot, marshalled whole and compared byte for
// byte — rather than a shape assertion, because the failure it guards
// against (an auxiliary aggregation leaking into a cell or perturbing a
// margin) produces a response that is still well-formed and still
// plausible.
//
// It is EXPECTED TO CHANGE in E2-S5, when the auxiliary figures land on
// Response.Components. At that point the assertion narrows to the
// crosstab matrix rather than the whole response; it must not simply be
// deleted.
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
			run := func(aux []*types.Aggregation) []byte {
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
				return out
			}

			baseline := run(nil)
			withAux := run([]*types.Aggregation{
				{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
			})

			if string(baseline) != string(withAux) {
				t.Errorf("declaring margin_aggregations changed the response\nbaseline: %s\n    with: %s",
					baseline, withAux)
			}
		})
	}
}
