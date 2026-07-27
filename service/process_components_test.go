package service

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// componentsScoreCohort writes a 5-row score cohort and returns a
// service wired to the in-memory fs. The schema (u32 id + f64 score)
// is shared with the existing service helper testSchema()/testRecords()
// — duplicated here so a regression in those helpers does not silently
// migrate the parity expectations.
func componentsScoreCohort(t *testing.T) (*Service, *encoding.Schema) {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
	}
	cfg := setupTestFS(t, "scores.pulse", schema, records)
	return New(cfg), schema
}

// componentsRatioCohort writes a 5-row cohort with both numerator and
// denominator columns so AGG_RATIO can exercise the composite-family
// path end-to-end through service.Process.
func componentsRatioCohort(t *testing.T) *Service {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "num", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "den", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{math.Float64bits(10.0), math.Float64bits(2.0)},
		{math.Float64bits(20.0), math.Float64bits(4.0)},
		{math.Float64bits(30.0), math.Float64bits(6.0)},
		{math.Float64bits(40.0), math.Float64bits(8.0)},
		{math.Float64bits(50.0), math.Float64bits(10.0)},
	}
	cfg := setupTestFS(t, "ratio.pulse", schema, records)
	return New(cfg)
}

// componentsSetCohort writes a 5-row cohort with a single set_u8 field
// over a 4-entry {VISA, MC, AMEX, DISC} dictionary so AGG_SET_UNION
// can exercise the set-family path end-to-end through service.Process.
// Uses afero.WriteFile directly because the standard writePulseFile
// path encodes via WriteFieldValue, which accepts the mask as the
// raw uint64.
func componentsSetCohort(t *testing.T) *Service {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		},
	}
	// 5 rows; union should be 0b1111 = {VISA, MC, AMEX, DISC}.
	records := [][]uint64{
		{0b0001}, // VISA
		{0b0011}, // VISA, MC
		{0b0100}, // AMEX
		{0b1000}, // DISC
		{0b0110}, // MC, AMEX
	}
	cfg := setupTestFS(t, "tags.pulse", schema, records)
	return New(cfg)
}

// runProcessAndExpectOneAggSlot drives svc.Process for a one-slot
// request and returns the AggregationComponents entry plus the
// canonical Label that was assigned. Fails the test if Components
// is unpopulated or the slot count is wrong — keeps the per-family
// assertions below grep-discoverable.
func runProcessAndExpectOneAggSlot(t *testing.T, svc *Service, req *types.Request) types.AggregationComponents {
	t.Helper()
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if got := len(resp.Components.Aggregations); got != 1 {
		t.Fatalf("Aggregations slots = %d, want 1", got)
	}
	return resp.Components.Aggregations[0]
}

// mapKeysSorted returns the sorted key set of m. Local copy of the
// helper from processing/aggregator_components_test.go so this file
// can stand alone in service/.
func mapKeysSorted(m map[string]any) []string {
	if len(m) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// manifestAggOperatorKeys returns the operator-specific key set
// (manifest schema MINUS the universal floor {n, n_null}) for the
// named aggregator. Sourced from the public BuildManifest projection
// — the same surface LLM clients consume.
func manifestAggOperatorKeys(t *testing.T, name string) []string {
	t.Helper()
	m := descriptor.BuildManifest()
	schema, ok := m.ComponentsSchemas.Aggregators[name]
	if !ok {
		t.Fatalf("manifest carries no components schema for %s", name)
	}
	out := make([]string, 0, len(schema.Keys))
	for _, k := range schema.Keys {
		if k.Name == "n" || k.Name == "n_null" {
			continue
		}
		out = append(out, k.Name)
	}
	sort.Strings(out)
	return out
}

// TestService_Process_Components_Scalar_AGG_SUM covers the scalar
// family. AGG_SUM emits {sum}. The 5-row cohort sums to 150.
func TestService_Process_Components_Scalar_AGG_SUM(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "total" {
		t.Errorf("Label = %q, want %q", entry.Label, "total")
	}
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	if entry.NNull != 0 {
		t.Errorf("NNull = %d, want 0", entry.NNull)
	}
	if got := mapKeysSorted(entry.Operator); !reflect.DeepEqual(got, []string{"sum"}) {
		t.Errorf("operator keys = %v, want [sum]", got)
	}
	sum, _ := entry.Operator["sum"].(float64)
	if sum != 150.0 {
		t.Errorf("sum = %v, want 150", sum)
	}
}

// TestService_Process_Components_Welford_AGG_VARIANCE covers the
// Welford family. AGG_VARIANCE emits {mean, m2, variance}. The 5-row
// score cohort {10,20,30,40,50} has population mean 30 and population
// variance 200.
func TestService_Process_Components_Welford_AGG_VARIANCE(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_VARIANCE, Field: "score", Label: "v"},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "v" {
		t.Errorf("Label = %q, want %q", entry.Label, "v")
	}
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	want := manifestAggOperatorKeys(t, string(types.AGG_VARIANCE))
	got := mapKeysSorted(entry.Operator)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operator keys mismatch:\n  runtime: %v\n  manifest: %v", got, want)
	}
	mean, _ := entry.Operator["mean"].(float64)
	variance, _ := entry.Operator["variance"].(float64)
	if mean != 30.0 {
		t.Errorf("mean = %v, want 30", mean)
	}
	if variance != 200.0 {
		t.Errorf("variance = %v, want 200", variance)
	}
}

// TestService_Process_Components_MapState_AGG_FREQUENCY covers the
// map-state family. AGG_FREQUENCY emits {distinct_count, mode_value,
// mode_count}. Every score is distinct so every value ties at count=1
// — the smallest-value tie-break picks 10.
func TestService_Process_Components_MapState_AGG_FREQUENCY(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_FREQUENCY, Field: "score", Label: "freq"},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "freq" {
		t.Errorf("Label = %q, want %q", entry.Label, "freq")
	}
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	want := manifestAggOperatorKeys(t, string(types.AGG_FREQUENCY))
	got := mapKeysSorted(entry.Operator)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operator keys mismatch:\n  runtime: %v\n  manifest: %v", got, want)
	}
	if dc, _ := entry.Operator["distinct_count"].(int); dc != 5 {
		t.Errorf("distinct_count = %v, want 5", dc)
	}
}

// TestService_Process_Components_Composite_AGG_RATIO covers the
// composite family. AGG_RATIO emits {numerator, denominator, ratio}.
// num sums to 150; den sums to 30; ratio is 5.0.
func TestService_Process_Components_Composite_AGG_RATIO(t *testing.T) {
	svc := componentsRatioCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ratio.pulse"},
		Aggregations: []*types.Aggregation{
			{
				Type:   types.AGG_RATIO,
				Field:  "num",
				Label:  "r",
				Params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
			},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "r" {
		t.Errorf("Label = %q, want %q", entry.Label, "r")
	}
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	want := manifestAggOperatorKeys(t, string(types.AGG_RATIO))
	got := mapKeysSorted(entry.Operator)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operator keys mismatch:\n  runtime: %v\n  manifest: %v", got, want)
	}
	num, _ := entry.Operator["numerator"].(float64)
	den, _ := entry.Operator["denominator"].(float64)
	ratio, _ := entry.Operator["ratio"].(float64)
	if num != 150.0 {
		t.Errorf("numerator = %v, want 150", num)
	}
	if den != 30.0 {
		t.Errorf("denominator = %v, want 30", den)
	}
	if ratio != 5.0 {
		t.Errorf("ratio = %v, want 5", ratio)
	}
}

// TestService_Process_Components_OrderStat_AGG_MEDIAN covers the
// order-stat family. AGG_MEDIAN emits {position_low, position_high,
// median}. Five rows {10,20,30,40,50} → median 30 at sorted index 2.
func TestService_Process_Components_OrderStat_AGG_MEDIAN(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "score", Label: "m"},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "m" {
		t.Errorf("Label = %q, want %q", entry.Label, "m")
	}
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	want := manifestAggOperatorKeys(t, string(types.AGG_MEDIAN))
	got := mapKeysSorted(entry.Operator)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operator keys mismatch:\n  runtime: %v\n  manifest: %v", got, want)
	}
	median, _ := entry.Operator["median"].(float64)
	if median != 30.0 {
		t.Errorf("median = %v, want 30", median)
	}
}

// TestService_Process_Components_Set_AGG_SET_UNION covers the set
// family. AGG_SET_UNION emits {mask_union, popcount, labels}. The 5
// row masks OR to 0b1111 = {VISA, MC, AMEX, DISC}.
func TestService_Process_Components_Set_AGG_SET_UNION(t *testing.T) {
	svc := componentsSetCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "tags.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "u"},
		},
	}
	entry := runProcessAndExpectOneAggSlot(t, svc, req)
	if entry.Label != "u" {
		t.Errorf("Label = %q, want %q", entry.Label, "u")
	}
	want := manifestAggOperatorKeys(t, string(types.AGG_SET_UNION))
	got := mapKeysSorted(entry.Operator)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operator keys mismatch:\n  runtime: %v\n  manifest: %v", got, want)
	}
	mask, _ := entry.Operator["mask_union"].(uint64)
	if mask != 0b1111 {
		t.Errorf("mask_union = %b, want 1111", mask)
	}
	popcount, _ := entry.Operator["popcount"].(int)
	if popcount != 4 {
		t.Errorf("popcount = %d, want 4", popcount)
	}
	labels, _ := entry.Operator["labels"].([]string)
	wantLabels := []string{"VISA", "MC", "AMEX", "DISC"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
}

func TestPredict_ComponentSchemaMatchesRuntime(t *testing.T) {
	fixtures := allAggServiceFixtures(t)
	for _, op := range types.AllAggregationTypes() {
		op := op
		fix, ok := fixtures[op]
		if !ok {
			t.Fatalf("no service fixture for %s — add one in allAggServiceFixtures", op)
		}
		t.Run(string(op), func(t *testing.T) {
			req := &types.Request{
				Cohort: &types.Cohort{Filename: fix.cohortName},
				Aggregations: []*types.Aggregation{
					{Type: op, Field: fix.field, Label: "primary", Params: fix.params},
				},
			}

			runtimeEntry := runProcessAndExpectOneAggSlot(t, fix.svc, req)
			runtimeKeys := mapKeysSorted(runtimeEntry.Operator)

			// Build a header-only .pulse bytes buffer the descriptor
			// path can validate against. The in-memory fs already
			// carries the schema; just re-marshal a header-only blob
			// from the same schema so PredictFromBytes can parse it.
			hdr := buildHeaderOnlyPulseBytes(t, fix.schema)
			predictReq := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: op, Field: fix.field, Label: "primary", Params: fix.params},
				},
			}
			env := descriptor.PredictFromBytes(hdr, predictReq, nil)
			result, ok := env.Data.(*descriptor.PredictResult)
			if !ok {
				t.Fatalf("Predict.Data is %T, want *PredictResult (errors: %v)",
					env.Data, env.Errors)
			}
			if len(result.Aggregations) != 1 {
				t.Fatalf("predict Aggregations slots = %d, want 1", len(result.Aggregations))
			}
			declared := result.Aggregations[0].ComponentSchema.Keys
			predictKeys := make([]string, 0, len(declared))
			for _, k := range declared {
				if k.Name == "n" || k.Name == "n_null" {
					continue
				}
				predictKeys = append(predictKeys, k.Name)
			}
			sort.Strings(predictKeys)

			if !reflect.DeepEqual(runtimeKeys, predictKeys) {
				t.Errorf("%s component-schema parity drift:\n  predict declared: %v\n  runtime emitted: %v",
					op, predictKeys, runtimeKeys)
			}
		})
	}

	groupFixtures := allGroupServiceFixtures(t)
	for _, op := range types.AllGroupTypes() {
		op := op
		gfix, ok := groupFixtures[op]
		if !ok {
			t.Fatalf("no service fixture for %s — add one in allGroupServiceFixtures", op)
		}
		t.Run(string(op), func(t *testing.T) {
			// Header-only blob for the descriptor path.
			hdr := buildHeaderOnlyPulseBytes(t, gfix.schema)
			predictReq := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: gfix.companionField, Label: "n"},
				},
				Groups: []*types.Group{
					{Type: op, Field: gfix.field, Interval: gfix.interval, Params: gfix.params},
				},
			}
			env := descriptor.PredictFromBytes(hdr, predictReq, nil)
			result, ok := env.Data.(*descriptor.PredictResult)
			if !ok {
				t.Fatalf("Predict.Data is %T, want *PredictResult (errors: %v)",
					env.Data, env.Errors)
			}
			if len(result.Groups) != 1 {
				t.Fatalf("predict Groups slots = %d, want 1", len(result.Groups))
			}
			declared := result.Groups[0].ComponentSchema.Keys
			if len(declared) == 0 {
				t.Fatalf("%s: predict GroupPredict.ComponentSchema.Keys empty; want at least the universal floor {total_n, n_null}", op)
			}
			// Universal floor must lead the schema (groupSchema prepends it).
			if got := declared[0].Name; got != "total_n" {
				t.Errorf("%s: ComponentSchema.Keys[0].Name = %q, want %q (universal floor)", op, got, "total_n")
			}
			if len(declared) < 2 || declared[1].Name != "n_null" {
				t.Errorf("%s: ComponentSchema.Keys[1].Name missing or wrong, want %q (universal floor)", op, "n_null")
			}

			// BufferedComponents parity with the static capability
			// table: GROUP_QUANTILE is the only None-mergeability
			// grouper today; everything else is Mergeable.
			wantBuffered := op == types.GROUP_QUANTILE
			if got := result.Groups[0].BufferedComponents; got != wantBuffered {
				t.Errorf("%s: BufferedComponents = %v, want %v", op, got, wantBuffered)
			}

			// Runtime-comparison half: drive svc.Process on the same
			// fixture and assert the runtime-emitted operator-specific
			// key set equals the predict-declared key set with the
			// universal floor stripped from both sides.
			runReq := &types.Request{
				Cohort: &types.Cohort{Filename: gfix.cohortName},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: gfix.companionField, Label: "n"},
				},
				Groups: []*types.Group{
					{Type: op, Field: gfix.field, Interval: gfix.interval, Params: gfix.params},
				},
			}
			resp, err := gfix.svc.Process(context.Background(), runReq)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatalf("Response.Components nil; want populated")
			}
			if got := len(resp.Components.Groupers); got != 1 {
				t.Fatalf("Groupers slots = %d, want 1", got)
			}
			runtimeKeys := mapKeysSorted(resp.Components.Groupers[0].Operator)
			predictKeys := []string{}
			for _, k := range declared {
				if k.Name == "total_n" || k.Name == "n_null" {
					continue
				}
				predictKeys = append(predictKeys, k.Name)
			}
			sort.Strings(predictKeys)
			if !reflect.DeepEqual(runtimeKeys, predictKeys) {
				t.Errorf("%s component-schema parity drift:\n  predict declared: %v\n  runtime emitted: %v",
					op, predictKeys, runtimeKeys)
			}
		})
	}

	filFixtures := allFilterServiceFixtures(t)
	for _, op := range types.AllFiltererTypes() {
		op := op
		ffix, ok := filFixtures[op]
		if !ok {
			t.Fatalf("no service fixture for %s — add one in allFilterServiceFixtures", op)
		}
		t.Run(string(op), func(t *testing.T) {
			hdr := buildHeaderOnlyPulseBytes(t, ffix.schema)
			predictReq := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: ffix.companionField, Label: "n"},
				},
				Filterers: []*types.Filterer{
					{Type: op, Field: ffix.field, Values: ffix.values, Expression: ffix.expression},
				},
			}
			env := descriptor.PredictFromBytes(hdr, predictReq, nil)
			result, ok := env.Data.(*descriptor.PredictResult)
			if !ok {
				t.Fatalf("Predict.Data is %T, want *PredictResult (errors: %v)",
					env.Data, env.Errors)
			}
			if len(result.Filterers) != 1 {
				t.Fatalf("predict Filterers slots = %d, want 1", len(result.Filterers))
			}
			declared := result.Filterers[0].ComponentSchema.Keys
			if len(declared) == 0 {
				t.Fatalf("%s: predict FiltererPredict.ComponentSchema.Keys empty; want at least the universal floor {n_in, n_out, n_null_input}", op)
			}
			// Universal floor must lead the schema (filterSchema
			// prepends it).
			if got := declared[0].Name; got != "n_in" {
				t.Errorf("%s: ComponentSchema.Keys[0].Name = %q, want %q (universal floor)", op, got, "n_in")
			}
			if len(declared) < 2 || declared[1].Name != "n_out" {
				t.Errorf("%s: ComponentSchema.Keys[1].Name missing or wrong, want %q (universal floor)", op, "n_out")
			}
			if len(declared) < 3 || declared[2].Name != "n_null_input" {
				t.Errorf("%s: ComponentSchema.Keys[2].Name missing or wrong, want %q (universal floor)", op, "n_null_input")
			}

			// BufferedComponents parity with the static capability
			// table: every built-in filterer is Mergeable in v1
			// (counter triples fold trivially), so the flag is false
			// for everyone. Future per-filter specifics may downgrade
			// individual entries; reopen this assertion when that
			// lands.
			if got := result.Filterers[0].BufferedComponents; got {
				t.Errorf("%s: BufferedComponents = %v, want false (every v1 filterer is Mergeable)", op, got)
			}

			runReq := &types.Request{
				Cohort: &types.Cohort{Filename: ffix.cohortName},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: ffix.companionField, Label: "n"},
				},
				Filterers: []*types.Filterer{
					{Type: op, Field: ffix.field, Values: ffix.values, Expression: ffix.expression},
				},
			}
			resp, err := ffix.svc.Process(context.Background(), runReq)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatalf("Response.Components nil; want populated")
			}
			if got := len(resp.Components.Filterers); got != 1 {
				t.Fatalf("Filterers slots = %d, want 1", got)
			}
			// The universal floor is typed onto FiltererComponents
			// (NIn / NOut / NNullInput); the operator-specific key
			// set rides in the MetaFilterer payload, which v1 leaves
			// unimplemented for every built-in. The runtime side
			// therefore carries no operator map, so the key set is
			// trivially empty — matching the predict declaration
			// minus floor.
			runtimeKeys := []string{}
			predictKeys := []string{}
			for _, k := range declared {
				if k.Name == "n_in" || k.Name == "n_out" || k.Name == "n_null_input" {
					continue
				}
				predictKeys = append(predictKeys, k.Name)
			}
			if !reflect.DeepEqual(runtimeKeys, predictKeys) {
				t.Errorf("%s component-schema parity drift:\n  predict declared: %v\n  runtime emitted: %v",
					op, predictKeys, runtimeKeys)
			}
		})
	}
}

// groupServiceFixture pairs a grouper type with the schema, cohort
// filename, field name, optional Interval / Params, and a companion
// numeric field that the AGG_COUNT slot can reference. Mirrors
// aggServiceFixture for the grouper half of TestPredict_ComponentSchemaMatchesRuntime.
type groupServiceFixture struct {
	svc            *Service
	schema         *encoding.Schema
	cohortName     string
	field          string
	companionField string
	interval       float64
	params         json.RawMessage
}

func allGroupServiceFixtures(t *testing.T) map[types.GroupType]groupServiceFixture {
	t.Helper()

	// Shared score cohort (u32 id + f64 score, 5 rows) — used by every
	// numeric grouper plus GROUP_CATEGORY over the u32 id column.
	scoreSvc, scoreSchema := componentsScoreCohort(t)

	// Date cohort: f64 score + date waveDate, 5 rows.
	dateSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "waveDate", Type: encoding.FieldTypeDate, ByteOffset: 8, CsvColumnIdx: 1},
		},
	}
	dateRecords := [][]uint64{
		{math.Float64bits(10.0), 19000}, // 2022-01-08
		{math.Float64bits(20.0), 19031}, // 2022-02-08
		{math.Float64bits(30.0), 19059}, // 2022-03-08
		{math.Float64bits(40.0), 19090}, // 2022-04-08
		{math.Float64bits(50.0), 19120}, // 2022-05-08
	}
	dateCfg := setupTestFS(t, "predict_dates.pulse", dateSchema, dateRecords)
	dateSvc := New(dateCfg)

	// Set cohort: set_u8 with a 4-entry dictionary, 5 rows of masks.
	// Companion AGG_COUNT slot rides on the tags field too (AGG_COUNT
	// is field-agnostic).
	setDict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := setDict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	setSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: setDict},
		},
	}
	setRecords := [][]uint64{
		{0b0001},
		{0b0011},
		{0b0100},
		{0b1000},
		{0b0110},
	}
	setCfg := setupTestFS(t, "predict_grp_tags.pulse", setSchema, setRecords)
	setSvc := New(setCfg)

	return map[types.GroupType]groupServiceFixture{
		types.GROUP_CATEGORY: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "id",
			companionField: "score",
		},
		types.GROUP_DATE: {
			svc:            dateSvc,
			schema:         dateSchema,
			cohortName:     "predict_dates.pulse",
			field:          "waveDate",
			companionField: "score",
			params:         json.RawMessage(`{"component":"month"}`),
		},
		types.GROUP_DATE_RANGES: {
			svc:            dateSvc,
			schema:         dateSchema,
			cohortName:     "predict_dates.pulse",
			field:          "waveDate",
			companionField: "score",
			params:         json.RawMessage(`{"ranges":[{"label":"q1","start":"2022-01-01","end":"2022-03-31"},{"label":"q2","start":"2022-04-01","end":"2022-06-30"}]}`),
		},
		types.GROUP_QUANTILE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			interval:       4,
		},
		types.GROUP_RANGE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			interval:       10,
		},
		types.GROUP_ROUNDED: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			interval:       10,
		},
		types.GROUP_SET_VALUE: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_grp_tags.pulse",
			field:          "tags",
			companionField: "tags",
		},
		types.GROUP_SET_PER_ELEMENT: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_grp_tags.pulse",
			field:          "tags",
			companionField: "tags",
		},
	}
}

type filterServiceFixture struct {
	svc            *Service
	schema         *encoding.Schema
	cohortName     string
	field          string
	companionField string
	values         []string
	expression     string
}

// allFilterServiceFixtures builds one fixture per registered filterer
// type, sharing svc / cohort across operators that accept the same
// field type. Schema choice matches the capability table's
// AcceptsTypes: FILTER_RANGE needs numeric, FILTER_SET_* needs set_u8,
// FILTER_EXPRESSION runs over any record field, the rest accept any
// cohort field type.
func allFilterServiceFixtures(t *testing.T) map[types.FiltererType]filterServiceFixture {
	t.Helper()

	// Shared score cohort (u32 id + f64 score, 5 rows) — drives the
	// numeric and any-type filterers.
	scoreSvc, scoreSchema := componentsScoreCohort(t)

	// Set cohort: set_u8 with a 4-entry dictionary, 5 rows of masks.
	// Drives every FILTER_SET_* slot.
	setDict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := setDict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	setSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: setDict},
		},
	}
	setRecords := [][]uint64{
		{0b0001},
		{0b0011},
		{0b0100},
		{0b1000},
		{0b0110},
	}
	setCfg := setupTestFS(t, "predict_fil_tags.pulse", setSchema, setRecords)
	setSvc := New(setCfg)

	return map[types.FiltererType]filterServiceFixture{
		types.FILTER_INCLUDE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"10", "20"},
		},
		types.FILTER_EXCLUDE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"10"},
		},
		types.FILTER_RANGE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"10", "40"},
		},
		types.FILTER_EXPRESSION: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			companionField: "score",
			expression:     "score > 10",
		},
		types.FILTER_NULL: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"is_not_null"},
		},
		types.FILTER_TRUE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"truthy"},
		},
		types.FILTER_FALSE: {
			svc:            scoreSvc,
			schema:         scoreSchema,
			cohortName:     "scores.pulse",
			field:          "score",
			companionField: "score",
			values:         []string{"truthy"},
		},
		types.FILTER_SET_CONTAINS_ANY: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_fil_tags.pulse",
			field:          "tags",
			companionField: "tags",
			values:         []string{"VISA", "MC"},
		},
		types.FILTER_SET_CONTAINS_ALL: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_fil_tags.pulse",
			field:          "tags",
			companionField: "tags",
			values:         []string{"VISA", "MC"},
		},
		types.FILTER_SET_CONTAINS_NONE: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_fil_tags.pulse",
			field:          "tags",
			companionField: "tags",
			values:         []string{"DISC"},
		},
		types.FILTER_SET_EQUALS: {
			svc:            setSvc,
			schema:         setSchema,
			cohortName:     "predict_fil_tags.pulse",
			field:          "tags",
			companionField: "tags",
			values:         []string{"VISA"},
		},
	}
}

// aggServiceFixture pairs an aggregation type with the schema, cohort
// filename, field name, and optional params needed to drive one
// svc.Process round-trip through the in-memory fs. The svc is reused
// across slots that share a schema so the test set stays bounded.
type aggServiceFixture struct {
	svc        *Service
	schema     *encoding.Schema
	cohortName string
	field      string
	params     json.RawMessage
}

// allAggServiceFixtures builds one fixture per registered aggregator
// type, sharing svc / cohort across operators that accept the same
// field type. The function exists at the package level so the
// per-family service tests above and the manifest-parity sweep below
// agree on the same cohort shape — drift between them surfaces as
// either a test failure or a missing fixture map entry.
func allAggServiceFixtures(t *testing.T) map[types.AggregationType]aggServiceFixture {
	t.Helper()

	// Shared score cohort (u32 id + f64 score, 5 rows).
	scoreSvc, scoreSchema := componentsScoreCohort(t)
	scoreFix := func() aggServiceFixture {
		return aggServiceFixture{
			svc:        scoreSvc,
			schema:     scoreSchema,
			cohortName: "scores.pulse",
			field:      "score",
		}
	}

	// Ratio cohort: numerator + denominator on f64. Re-encoded here
	// because composite aggregators need both fields adjacent.
	ratioSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "num", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "den", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		},
	}
	ratioRecords := [][]uint64{
		{math.Float64bits(10.0), math.Float64bits(2.0)},
		{math.Float64bits(20.0), math.Float64bits(4.0)},
		{math.Float64bits(30.0), math.Float64bits(6.0)},
		{math.Float64bits(40.0), math.Float64bits(8.0)},
		{math.Float64bits(50.0), math.Float64bits(10.0)},
	}
	ratioCfg := setupTestFS(t, "predict_ratio.pulse", ratioSchema, ratioRecords)
	ratioSvc := New(ratioCfg)

	// Weighted-mean cohort: value + weight columns on f64.
	wmSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		},
	}
	wmRecords := [][]uint64{
		{math.Float64bits(10.0), math.Float64bits(1.0)},
		{math.Float64bits(20.0), math.Float64bits(1.0)},
		{math.Float64bits(30.0), math.Float64bits(2.0)},
		{math.Float64bits(40.0), math.Float64bits(1.0)},
		{math.Float64bits(50.0), math.Float64bits(2.0)},
	}
	wmCfg := setupTestFS(t, "predict_wm.pulse", wmSchema, wmRecords)
	wmSvc := New(wmCfg)

	// Set cohort: set_u8 with a 4-entry dictionary, 5 rows of masks.
	setDict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := setDict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	setSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: setDict},
		},
	}
	setRecords := [][]uint64{
		{0b0001},
		{0b0011},
		{0b0100},
		{0b1000},
		{0b0110},
	}
	setCfg := setupTestFS(t, "predict_tags.pulse", setSchema, setRecords)
	setSvc := New(setCfg)

	fixtures := map[types.AggregationType]aggServiceFixture{
		// Scalar / numeric ops over the f64 score cohort.
		types.AGG_COUNT:          scoreFix(),
		types.AGG_SUM:            scoreFix(),
		types.AGG_AVERAGE:        scoreFix(),
		types.AGG_MIN:            scoreFix(),
		types.AGG_MAX:            scoreFix(),
		types.AGG_STDDEV:         scoreFix(),
		types.AGG_RANGE:          scoreFix(),
		types.AGG_FREQUENCY:      scoreFix(),
		types.AGG_ZSCORE:         scoreFix(),
		types.AGG_MEDIAN:         scoreFix(),
		types.AGG_VARIANCE:       scoreFix(),
		types.AGG_MODE:           scoreFix(),
		types.AGG_SKEWNESS:       scoreFix(),
		types.AGG_KURTOSIS:       scoreFix(),
		types.AGG_DISTINCT_COUNT: scoreFix(),
		types.AGG_NULL_COUNT:     scoreFix(),
		types.AGG_PERCENTILE: {
			svc:        scoreSvc,
			schema:     scoreSchema,
			cohortName: "scores.pulse",
			field:      "score",
			params:     json.RawMessage(`{"percentile":75}`),
		},
		types.AGG_WELFORD:  scoreFix(),
		types.AGG_CI_LOWER: scoreFix(),
		types.AGG_CI_UPPER: scoreFix(),

		// Composite ops use their own cohort.
		types.AGG_WEIGHTED_MEAN: {
			svc:        wmSvc,
			schema:     wmSchema,
			cohortName: "predict_wm.pulse",
			field:      "value",
			params:     json.RawMessage(`{"weight_field":"weight"}`),
		},
		types.AGG_RATIO: {
			svc:        ratioSvc,
			schema:     ratioSchema,
			cohortName: "predict_ratio.pulse",
			field:      "num",
			params:     json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
		},

		// Set ops use the set_u8 dictionary cohort.
		types.AGG_SET_UNION:           {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
		types.AGG_SET_INTERSECTION:    {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
		types.AGG_SET_FREQUENCY:       {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
		types.AGG_SET_CARDINALITY_SUM: {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
		types.AGG_SET_CARDINALITY_AVG: {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
		types.AGG_SET_DISTINCT_VALUES: {svc: setSvc, schema: setSchema, cohortName: "predict_tags.pulse", field: "tags"},
	}

	// Sanity: the fixture map must cover every registered aggregator.
	for _, op := range types.AllAggregationTypes() {
		if _, ok := fixtures[op]; !ok {
			t.Fatalf("allAggServiceFixtures missing fixture for %s", op)
		}
	}
	return fixtures
}

// buildHeaderOnlyPulseBytes returns a complete header+schema (no
// records) byte buffer suitable for descriptor.PredictFromBytes.
// Mirrors descriptor/predict_test.go's buildTestPulseFile helper but
// lives here so service tests do not reach into another package's
// test file.
func buildHeaderOnlyPulseBytes(t *testing.T, schema *encoding.Schema) []byte {
	t.Helper()
	// Use the in-memory fs to render bytes via writePulseFile (which
	// already produces a header+schema blob followed by zero records).
	cfg := setupTestFS(t, "_hdr.pulse", schema, nil)
	data, err := afero.ReadFile(cfg.Fs(), "_hdr.pulse")
	if err != nil {
		t.Fatalf("ReadFile header: %v", err)
	}
	return data
}
