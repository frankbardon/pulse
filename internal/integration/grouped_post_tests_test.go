package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Partitioned post-tests and the grouped correction family (U46 E3-S2).
// With two or more groupers every post_tests entry runs once per
// PARTITION of the result rows — the rows sharing every key but the
// last, or an explicit params.partition_by prefix — so a trend runs down
// the last grouper within each segment. Response.PostTests is flat:
// post-tests x partitions, test-major, each result carrying the
// partition key as group_key. Every (test x group) and (post-test x
// partition) p-value joins ONE `request` family; an undefined (sparse)
// p is left out of m and counted in the echo's `excluded`.

// gptScore buckets score in threes: north 5 rows, south 4, west 4.
func gptScore() *types.Group {
	return &types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 3}
}

// gptPostTests is a Mann-Kendall trend of the summed score down the
// score buckets and a Pearson r of the bucket count against that sum.
func gptPostTests(t *testing.T, params string) []*types.Test {
	t.Helper()
	p := ""
	if params != "" {
		p = `,"params":` + params
	}
	return gtDecodeTests(t, `[`+
		`{"type":"TEST_TREND","field":"s","order_by":[{"field":"score"}],"label":"trend"`+p+`},`+
		`{"type":"TEST_PEARSON_R","field":"n","field2":"s","label":"r"`+p+`}]`)
}

func gptRequest(f *mgFixture, groups []*types.Group) *types.Request {
	return mgRequest(f, groups,
		&types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		&types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "s"})
}

// TestGroupedPostTests_EqualPerPartitionRuns is the backbone: over
// [region, score-range] every post-test runs once per region, and each
// region's result is bit for bit the post-test over a run filtered to
// that region and grouped by score-range alone — on every execution
// mode that runs post-tests (the streaming grouped tail included).
func TestGroupedPostTests_EqualPerPartitionRuns(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	regions := []string{"north", "south", "west"}
	// The reference: per region, the one-grouper run's post-tests.
	want := map[string][]string{}
	for _, region := range regions {
		req := gptRequest(f, []*types.Group{gptScore()})
		req.Filterers = []*types.Filterer{gtKeyFilter(mgRegion(), region)}
		req.PostTests = gptPostTests(t, "")
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("%s reference: %v", region, err)
		}
		if len(resp.PostTests) != 2 {
			t.Fatalf("%s reference: %d post-tests", region, len(resp.PostTests))
		}
		for _, r := range resp.PostTests {
			if len(r.GroupKey) != 0 {
				t.Fatalf("%s reference: one-grouper post-test carries group_key %v", region, r.GroupKey)
			}
			if math.IsNaN(r.PValue) {
				t.Fatalf("%s reference: undefined p; the case proves nothing", region)
			}
			want[region] = append(want[region], gtJSON(t, r))
		}
	}
	modes := gtModes()
	for _, m := range mgModes {
		if m.name == "Streaming" {
			modes = append(modes, m)
		}
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			req := gptRequest(f, []*types.Group{mgRegion(), gptScore()})
			req.PostTests = gptPostTests(t, "")
			resp, err := mode.run(t, f, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(resp.PostTests) != 2*len(regions) {
				t.Fatalf("%d post-test results, want 2 post-tests x 3 regions", len(resp.PostTests))
			}
			// Test-major, partitions in first-appearance order of Data.
			var order []string
			seen := map[string]bool{}
			for _, row := range resp.Data {
				if r := fmt.Sprint(row["region"]); !seen[r] {
					seen[r] = true
					order = append(order, r)
				}
			}
			for k, r := range resp.PostTests {
				spec, part := k/len(order), order[k%len(order)]
				if !reflect.DeepEqual(r.GroupKey, types.AxisKey{part}) {
					t.Fatalf("post_tests[%d] group_key %v, want [%s]", k, r.GroupKey, part)
				}
				got := gtJSON(t, r)
				if sorted := len(req.Sort) > 0; sorted {
					// A sort reorders the rows inside each partition, so
					// the order-sensitive sums agree to rounding only.
					if !gptClose(t, got, want[part][spec]) {
						t.Errorf("post_tests[%d] (%s, %s) sorted:\n got %s\nwant %s", k, req.PostTests[spec].Label, part, got, want[part][spec])
					}
				} else if got != want[part][spec] {
					t.Errorf("post_tests[%d] (%s, %s):\n got %s\nwant %s", k, req.PostTests[spec].Label, part, got, want[part][spec])
				}
			}
		})
	}
}

// gptClose compares two result wire forms leaf by leaf: equal strings
// and bools, numbers within 1e-9 relative.
func gptClose(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		t.Fatal("undecodable result")
	}
	var walk func(x, y any) bool
	walk = func(x, y any) bool {
		switch xv := x.(type) {
		case map[string]any:
			yv, ok := y.(map[string]any)
			if !ok || len(xv) != len(yv) {
				return false
			}
			for k := range xv {
				if !walk(xv[k], yv[k]) {
					return false
				}
			}
			return true
		case float64:
			yv, ok := y.(float64)
			return ok && math.Abs(xv-yv) <= 1e-9*math.Max(1, math.Abs(yv))
		default:
			return reflect.DeepEqual(x, y)
		}
	}
	return walk(x, y)
}

// TestGroupedPostTests_PartitionByAndByteIdentity: an explicit [] runs
// once over every row (the pre-U46 walk), one grouper never partitions,
// and neither carries a group_key.
func TestGroupedPostTests_PartitionByAndByteIdentity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	run := func(groups []*types.Group, params string) *types.Response {
		t.Helper()
		req := gptRequest(f, groups)
		req.PostTests = gptPostTests(t, params)
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return resp
	}
	whole := run([]*types.Group{mgRegion(), gptScore()}, `{"partition_by":[]}`)
	explicit := run([]*types.Group{mgRegion(), gptScore()}, `{"partition_by":["region"]}`)
	implicit := run([]*types.Group{mgRegion(), gptScore()}, "")
	if len(whole.PostTests) != 2 || len(whole.PostTests[0].GroupKey) != 0 {
		t.Fatalf("partition_by []: %d results, group_key %v; want one unkeyed run per post-test", len(whole.PostTests), whole.PostTests[0].GroupKey)
	}
	if n := fmt.Sprint(whole.PostTests[1].Details["n"]); n != fmt.Sprint(len(whole.Data)) {
		t.Fatalf("partition_by []: pearson n %s over %d rows", n, len(whole.Data))
	}
	ej, _ := json.Marshal(explicit.PostTests)
	ij, _ := json.Marshal(implicit.PostTests)
	if string(ej) != string(ij) {
		t.Fatalf("explicit [region] differs from the implicit default:\n%s\n%s", ej, ij)
	}
	one := run([]*types.Group{gptScore()}, "")
	if len(one.PostTests) != 2 || len(one.PostTests[0].GroupKey) != 0 {
		t.Fatalf("one grouper: %d results, group_key %v", len(one.PostTests), one.PostTests[0].GroupKey)
	}
}

// TestGroupedPostTests_SparsePartition: a partition too small for the
// test gets null statistics and a coded warning naming its group_key;
// the others proceed.
func TestGroupedPostTests_SparsePartition(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	req := gptRequest(f, []*types.Group{mgRegion(), gptScore()})
	// ids 23-24 leave west two score buckets: a trend needs three.
	req.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "score", Values: []string{"1", "24"}}}
	req.PostTests = gptPostTests(t, "")[:1]
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ws := gtWarnings(t, resp.Warnings, perr.PULSE_TEST_INSUFFICIENT_N)
	if len(ws) != 1 || len(ws["[west]"]) != 1 {
		t.Fatalf("INSUFFICIENT_N warnings %v, want exactly west", ws)
	}
	nulls := 0
	for _, r := range resp.PostTests {
		if math.IsNaN(r.PValue) {
			nulls++
			if !reflect.DeepEqual(r.GroupKey, types.AxisKey{"west"}) {
				t.Fatalf("sparse result group_key %v", r.GroupKey)
			}
		}
	}
	if nulls != 1 || len(resp.PostTests) != 3 {
		t.Fatalf("%d null of %d results, want 1 of 3", nulls, len(resp.PostTests))
	}
}

// TestGroupedPostTests_RefusalsMatchPredict: a partition_by off the
// prefix rule, post-tests at mixed depths and a split on a partition
// key are refused by predict and runtime with identical Details.
func TestGroupedPostTests_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name   string
		tests  string
		code   perr.Code
		reason string
	}{
		{"not_prefix", `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}],"params":{"partition_by":["sex"]}}]`,
			perr.PULSE_TEST_PARTITION_INVALID, "not_prefix"},
		{"unknown_field", `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}],"params":{"partition_by":["warehouse"]}}]`,
			perr.PULSE_TEST_PARTITION_INVALID, "unknown_field"},
		{"not_string_list", `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}],"params":{"partition_by":"region"}}]`,
			perr.PULSE_TEST_PARTITION_INVALID, "not_string_list"},
		{"mixed_depth", `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}]},{"type":"TEST_ANOVA_F","field":"n","split_by":"region","params":{"partition_by":[]}}]`,
			perr.PULSE_TEST_PARTITION_INVALID, "mixed_depth"},
		{"split_is_partition_key", `[{"type":"TEST_ANOVA_F","field":"n","split_by":"region"}]`,
			perr.PULSE_TEST_SPLIT_GROUPS_LT_2, "split_is_partition_key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
			req.PostTests = gtDecodeTests(t, c.tests)
			env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
			var pd map[string]any
			for _, e := range env.Errors {
				if e.Code == string(c.code) {
					pd = e.Details
				}
			}
			if pd == nil {
				t.Fatalf("predict: no %s in %+v", c.code, env.Errors)
			}
			if pd["reason"] != c.reason {
				t.Fatalf("predict reason %v, want %s", pd["reason"], c.reason)
			}
			_, err := f.p.Process(context.Background(), req)
			var coded *perr.CodedError
			if !perrAs(err, &coded) || coded.Code != c.code {
				t.Fatalf("runtime: %v, want %s", err, c.code)
			}
			pj, _ := json.Marshal(pd)
			rj, _ := json.Marshal(coded.Details)
			if string(pj) != string(rj) {
				t.Fatalf("details diverge:\n predict %s\n runtime %s", pj, rj)
			}
		})
	}
	// The escape hatch: a split on the partition key with partition_by
	// [] runs once over every composite row.
	req := mgRequest(f, []*types.Group{mgSex(), mgRegion()})
	req.PostTests = gtDecodeTests(t, `[{"type":"TEST_KS","field":"n","split_by":"sex"}]`)
	if _, err := f.p.Process(context.Background(), req); err == nil {
		t.Fatal("KS split on the partition key sex ran")
	}
	req.PostTests[0].Params = json.RawMessage(`{"partition_by":[]}`)
	resp, err := f.p.Process(context.Background(), req)
	if err != nil || len(resp.PostTests) != 1 || len(resp.PostTests[0].GroupKey) != 0 || math.IsNaN(resp.PostTests[0].PValue) {
		t.Fatalf("partition_by [] across sexes: %v %+v", err, resp)
	}
}

// TestGroupedPostTests_PredictCountsPartitions: predict's p_values count
// post-tests x partitions (dictionary basis) beside tests x groups.
func TestGroupedPostTests_PredictCountsPartitions(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
	req.Tests = gtDecodeTests(t, `[{"type":"TEST_T","field":"score","split_by":"tier"}]`)
	req.PostTests = gtDecodeTests(t, `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}]},{"type":"TEST_PEARSON_R","field":"n","field2":"n"}]`)
	pr, err := f.p.Predict(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// 1 test x 6 groups + 2 post-tests x 3 regions.
	if pr.PValues == nil || pr.PValues.Total != 12 || pr.PValues.Basis != "dictionary" {
		t.Fatalf("p_values %+v, want 6 + 6 = 12 on the dictionary basis", pr.PValues)
	}
	req.PostTests = gtDecodeTests(t, `[{"type":"TEST_TREND","field":"n","order_by":[{"field":"sex"}],"params":{"partition_by":[]}}]`)
	if pr, err = f.p.Predict(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 7 {
		t.Fatalf("p_values %+v, want 6 + 1 unpartitioned = 7", pr.PValues)
	}
}

// refAdjust is a reference implementation independent of the engine's
// multiplicity core: Holm step-down and Benjamini-Hochberg step-up over
// the defined p-values, NaN kept NaN and out of m.
func refAdjust(method types.MultiplicityMethod, ps []float64) []float64 {
	type ip struct {
		i int
		p float64
	}
	var def []ip
	for i, p := range ps {
		if !math.IsNaN(p) {
			def = append(def, ip{i, p})
		}
	}
	m := len(def)
	sort.SliceStable(def, func(a, b int) bool { return def[a].p < def[b].p })
	out := make([]float64, len(ps))
	for i := range out {
		out[i] = math.NaN()
	}
	switch method {
	case types.MultiplicityMethodHolm:
		running := 0.0
		for k, d := range def {
			v := math.Min(1, float64(m-k)*d.p)
			running = math.Max(running, v)
			out[d.i] = running
		}
	case types.MultiplicityMethodBH:
		running := 1.0
		for k := m - 1; k >= 0; k-- {
			v := math.Min(1, float64(m)/float64(k+1)*def[k].p)
			running = math.Min(running, v)
			out[def[k].i] = running
		}
	}
	return out
}

// TestGroupedPostTests_OneFamily: with a request family, p_adjusted over
// every grouped test and partitioned post-test equals a hand-assembled
// single family of their raw p-values (BH and Holm); the raw p never
// moves; a sparse group's undefined p is left out of m and counted in
// every member's `excluded` stamp.
func TestGroupedPostTests_OneFamily(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	build := func(mult *types.Multiplicity) *types.Request {
		req := gptRequest(f, []*types.Group{mgRegion(), gptScore()})
		// ids 1-24: west keeps two score buckets (its trend is sparse).
		req.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "score", Values: []string{"1", "24"}}}
		req.Tests = gtDecodeTests(t, `[{"type":"TEST_T","field":"y","split_by":"sex","label":"t"}]`)
		req.PostTests = gptPostTests(t, "")
		req.Multiplicity = mult
		return req
	}
	raw, err := f.p.Process(context.Background(), build(nil))
	if err != nil {
		t.Fatal(err)
	}
	var ps []float64
	for _, r := range append(append([]*types.TestResult{}, raw.Tests...), raw.PostTests...) {
		ps = append(ps, r.PValue)
	}
	nan := 0
	for _, p := range ps {
		if math.IsNaN(p) {
			nan++
		}
	}
	if nan == 0 || len(raw.PostTests) != 6 || len(raw.Tests) < 3 {
		t.Fatalf("%d tests, %d post-tests, %d undefined p: the case proves nothing", len(raw.Tests), len(raw.PostTests), nan)
	}
	for _, method := range []types.MultiplicityMethod{types.MultiplicityMethodBH, types.MultiplicityMethodHolm} {
		t.Run(string(method), func(t *testing.T) {
			resp, err := f.p.Process(context.Background(), build(&types.Multiplicity{Method: method}))
			if err != nil {
				t.Fatal(err)
			}
			want := refAdjust(method, ps)
			all := append(append([]*types.TestResult{}, resp.Tests...), resp.PostTests...)
			moved := false
			for k, r := range all {
				if !sameP(r.PValue, ps[k]) {
					t.Fatalf("member %d raw p moved: %v -> %v", k, ps[k], r.PValue)
				}
				if r.PAdjusted == nil || r.Multiplicity == nil {
					t.Fatalf("member %d (%s %v) uncorrected", k, r.Label, r.GroupKey)
				}
				if math.Abs(*r.PAdjusted-want[k]) > 1e-12 && !(math.IsNaN(*r.PAdjusted) && math.IsNaN(want[k])) {
					t.Errorf("member %d (%s %v) p_adjusted %v, want %v", k, r.Label, r.GroupKey, *r.PAdjusted, want[k])
				}
				moved = moved || (!math.IsNaN(want[k]) && want[k] != ps[k])
				if r.Multiplicity.M != len(ps)-nan || r.Multiplicity.Excluded != nan {
					t.Errorf("member %d echo m=%d excluded=%d, want m=%d excluded=%d", k, r.Multiplicity.M, r.Multiplicity.Excluded, len(ps)-nan, nan)
				}
			}
			if !moved {
				t.Fatal("no adjusted p moved; the case proves nothing")
			}
		})
	}
	// No undefined p: the stamp is absent on the wire.
	req := build(&types.Multiplicity{Method: types.MultiplicityMethodHolm})
	req.Filterers, req.Tests = nil, nil
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(resp.PostTests[0].Multiplicity)
	if resp.PostTests[0].Multiplicity == nil || resp.PostTests[0].Multiplicity.Excluded != 0 || strings.Contains(string(b), "excluded") {
		t.Fatalf("fully defined family stamps excluded: %s", b)
	}
}

func sameP(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }
