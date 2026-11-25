package observe_test

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Watch every operation an engine runs through Options.Hooks. Each hook
// is optional; OnOperationEnd fires exactly once per operation with its
// counters and its outcome code ("ok", or the error code).
func ExampleHooks() {
	hooks := &observe.Hooks{
		OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
			fmt.Printf("%s %s: scanned %d, out %d, code %q\n",
				info.Kind, info.Cohort, res.RowsScanned, res.RowsOut, res.Code)
		},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Hooks: hooks})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	ctx := context.Background()
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region."},
		{Name: "amount", Type: encoding.FieldTypeU32, Description: "Order amount."},
	}}
	b, err := p.NewCohortBuilder(ctx, "sales.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, r := range []pulse.CohortRow{{"north", uint64(10)}, {"south", uint64(20)}, {"north", uint64(5)}} {
		if err := b.Append(r); err != nil {
			fmt.Println("error:", err)
			return
		}
	}
	if _, err := b.Close(); err != nil {
		fmt.Println("error:", err)
		return
	}

	req := &pulse.Request{
		Cohort:       &types.Cohort{Filename: "sales.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}},
	}
	_, _ = p.Process(ctx, req)
	req.Cohort.Filename = "missing.pulse"
	_, _ = p.Process(ctx, req)
	// Output:
	// process sales.pulse: scanned 3, out 2, code "ok"
	// process missing.pulse: scanned 0, out 0, code "SERVICE_RESOURCE"
}
