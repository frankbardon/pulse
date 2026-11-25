package pulse_test

// Runnable GoDoc examples for setting up an engine, reading a cohort
// and running an analysis. Each runs on the in-memory tiny cohort from
// example_helpers_test.go; TestFacadeExamplesPresent pins the set.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// totalByRegion sums amount per region: the request most examples run.
func totalByRegion() *pulse.Request {
	return &pulse.Request{
		Cohort:       &types.Cohort{Filename: "sales.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
	}
}

// Build an engine over an in-memory filesystem. Options.FS replaces the
// PULSE_DATA_DIR-backed default, so nothing touches the disk.
func ExampleNew() {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	n, err := p.CountRecords(context.Background(), "missing.pulse")
	fmt.Println(n, err != nil)
	// Output:
	// 0 true
}

// Open a cohort and read it back record by record. Values come back in
// schema field order: integers as uint64, categoricals as their label.
func ExamplePulse_Open() {
	p, _ := newExampleEngine(pulse.Options{})
	c, err := p.Open(context.Background(), "sales.pulse")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	r, err := c.Reader()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer r.Close()
	fmt.Println("records:", r.Len())
	for i := int64(0); i < 2; i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(row...)
	}
	// Output:
	// records: 6
	// 1 north web 10
	// 2 south store 20
}

// Write a cohort row by row from Go values. Close seals the file and
// reports what was written.
func ExamplePulse_NewCohortBuilder() {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	b, err := p.NewCohortBuilder(context.Background(), "built.pulse", exampleSchema(), pulse.CohortBuilderOptions{Strict: true})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, r := range exampleRows {
		if err := b.Append(r); err != nil {
			fmt.Println("error:", err)
			return
		}
	}
	res, err := b.Close()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("records:", res.Records, "format version:", res.FormatVersion)
	// Output:
	// records: 6 format version: 1
}

// Group and aggregate: total amount per region.
func ExamplePulse_Process() {
	p, _ := newExampleEngine(pulse.Options{})
	resp, err := p.Process(context.Background(), totalByRegion())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, row := range resp.Data {
		fmt.Println(row["region"], row["total"])
	}
	// Output:
	// east 15
	// north 40
	// south 50
}

// Run several requests in one call; Responses[i] answers Requests[i].
func ExamplePulse_Compose() {
	p, _ := newExampleEngine(pulse.Options{})
	byChannel := &pulse.Request{
		Cohort:       &types.Cohort{Filename: "sales.pulse"},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "channel"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "orders"}},
	}
	resp, err := p.Compose(context.Background(), &pulse.ComposedRequest{
		Requests: []*types.Request{totalByRegion(), byChannel},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("responses:", len(resp.Responses))
	for _, row := range resp.Responses[1].Data {
		fmt.Println(row["channel"], row["orders"])
	}
	// Output:
	// responses: 2
	// store 2
	// web 4
}

// List a field's distinct values without running a request.
func ExamplePulse_Facet() {
	p, _ := newExampleEngine(pulse.Options{})
	values, err := p.Facet(context.Background(), "sales.pulse", "region")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(values)
	// Output:
	// [north south east]
}

// Drain a result one row at a time instead of holding it all.
func ExamplePulse_ProcessStream() {
	p, _ := newExampleEngine(pulse.Options{})
	ctx := context.Background()
	it, err := p.ProcessStream(ctx, totalByRegion())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer it.Close()
	for {
		row, ok, err := it.Next(ctx)
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		if !ok {
			break
		}
		fmt.Println(row["region"], row["total"])
	}
	// Output:
	// east 15
	// north 40
	// south 50
}

// Check a request against the cohort's schema without reading a record:
// whether it is valid and whether it streams. An invalid request is
// reported, not run.
func ExamplePulse_Predict() {
	p, _ := newExampleEngine(pulse.Options{})
	pr, err := p.Predict(context.Background(), totalByRegion())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("valid:", pr.Valid, "streamable:", pr.Streamable)

	bad := totalByRegion()
	bad.Aggregations[0].Field = "price"
	pr, err = p.Predict(context.Background(), bad)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("valid:", pr.Valid, "first error:", pr.Errors[0].Code)
	// Output:
	// valid: true streamable: true
	// valid: false first error: SERVICE_VALIDATION
}

// Describe a cohort from its header alone: record count and schema.
func ExamplePulse_Inspect() {
	p, _ := newExampleEngine(pulse.Options{})
	ir, err := p.Inspect(context.Background(), "sales.pulse")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("records:", ir.RecordCount, "fields:", ir.FieldCount)
	for _, f := range ir.Fields {
		fmt.Println(f.Name, f.Type)
	}
	// Output:
	// records: 6 fields: 4
	// id u32
	// region categorical_u8
	// channel categorical_u8
	// amount u32
}

// Ask which operator answers an analysis intent for a field, then run
// the ready-made request the recommendation carries.
func ExamplePulse_Recommend() {
	p, _ := newExampleEngine(pulse.Options{})
	ctx := context.Background()
	res, err := p.Recommend(ctx, descriptor.RecommendRequest{
		Intent: "describe",
		Fields: []string{"amount"},
		Cohort: &types.Cohort{Filename: "sales.pulse"},
		Limit:  1,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	top := res.Recommendations[0]
	var req pulse.Request
	if err := json.Unmarshal(top.Request, &req); err != nil {
		fmt.Println("error:", err)
		return
	}
	resp, err := p.Process(ctx, &req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, v := range resp.Data[0] {
		fmt.Println(top.Operator, v)
	}
	// Output:
	// AGG_AVERAGE 17.5
}

// Explain a request in plain language before running it: one step per
// slot it fills.
func ExamplePulse_Explain() {
	p, _ := newExampleEngine(pulse.Options{})
	res, err := p.Explain(context.Background(), descriptor.ExplainRequest{Request: totalByRegion()})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("root:", res.Root, "mode:", res.Mode)
	for _, s := range res.Steps {
		fmt.Println(s.Slot, s.Operator)
	}
	// Output:
	// root: request mode: request
	// groups[0] GROUP_CATEGORY
	// aggregations[0] AGG_SUM
}
