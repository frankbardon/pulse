package integration_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstab_NilAxisGrouperRefusedOnEveryArm: a null grouper on a
// crosstab axis (`rows: [null]` / `columns: [null]`) is refused at
// request validation, never a panic, with the one PROCESSING_CONFIG
// refusal of crosstabaxis.NilGrouperRefusals — the same code, message
// and details from predict, the fused instance and the fusion-disabled
// (buffered) instance. Before the shared rule the fused gate declined
// the request, the buffered partitioner dereferenced the nil slot and
// predict accepted it.
func TestCrosstab_NilAxisGrouperRefusedOnEveryArm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := paritySchema(t)
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", schema, 0, 40)
	data, err := afero.ReadFile(fsys, "parity.pulse")
	if err != nil {
		t.Fatal(err)
	}
	region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	cell := &types.Aggregation{Type: types.AGG_SUM, Field: "score"}
	cases := []struct {
		name string
		spec *types.CrosstabSpec
		want string
	}{
		{"rows", &types.CrosstabSpec{Rows: []*types.Group{nil}, Columns: region, Cell: cell},
			"crosstab rows contains a null grouper at index 0"},
		{"columns", &types.CrosstabSpec{Rows: region, Columns: []*types.Group{region[0], nil}, Cell: cell},
			"crosstab columns contains a null grouper at index 1"},
		{"cells", &types.CrosstabSpec{Rows: []*types.Group{region[0], nil}, Columns: region,
			Cells: []*types.Aggregation{cell, {Type: types.AGG_COUNT, Field: "score"}}},
			"crosstab rows contains a null grouper at index 1"},
	}
	for _, disable := range []bool{false, true} {
		arm := map[bool]string{false: "fused", true: "buffered"}[disable]
		p, err := pulse.New(pulse.Options{FS: fsys, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			t.Run(arm+"/"+c.name, func(t *testing.T) {
				req := &types.Request{Cohort: &types.Cohort{Filename: "parity.pulse"}, Crosstab: c.spec}
				env, err := p.PredictBytes(ctx, data, req)
				if err != nil {
					t.Fatal(err)
				}
				_, rerr := p.Process(ctx, req)
				ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
				if ce.Message != c.want {
					t.Errorf("runtime message = %q, want %q", ce.Message, c.want)
				}
				sameEntry(t, env, rerr)
				if pr, ok := env.Data.(*descriptor.PredictResult); ok && pr.CrosstabFusable != nil && *pr.CrosstabFusable {
					t.Error("predict reports a null-axis crosstab fusable")
				}
			})
		}
	}
}
