package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Grouped tier-1 tests (U46 E3-S1): with `groups` set every test runs
// once per COMPOSITE group key, and Response.Tests is flat — one result
// per (test x group), test-major, groups in the final Data row order,
// each carrying group_key. The backbone is equivalence: every group's
// result equals an ungrouped run filtered to that one group, bit for
// bit (weighted and unweighted, every execution mode that admits
// tests). A group whose data the test refuses gets null statistics and
// a coded warning naming its group_key, where the filtered run errors
// with the same code.

// gtCase is one equivalence row: the tests, the groups, and per group
// key the filters that isolate that one group in an ungrouped run.
type gtCase struct {
	name   string
	tests  string // JSON array of tests
	groups func() []*types.Group
}

func gtCases() []gtCase {
	tSex := `{"type":"TEST_T","field":"score","split_by":"sex","label":"t"}`
	chisq := `{"type":"TEST_CHISQ","rows":"sex","cols":"tier","label":"chi"}`
	mwu := `{"type":"TEST_MANN_WHITNEY_U","field":"score","split_by":"sex","label":"mwu"}`
	wT := `{"type":"TEST_T","field":"y","split_by":"sex","label":"wt","weight":{"field":"score","kind":"probability"}}`
	wChi := `{"type":"TEST_CHISQ","rows":"sex","cols":"tier","label":"wchi","weight":{"field":"score","kind":"probability"}}`
	wMwu := `{"type":"TEST_MANN_WHITNEY_U","field":"score","split_by":"sex","label":"wmwu","weight":{"field":"y","kind":"frequency"}}`
	// U48: goodness of fit and the one-proportion test run per group.
	gof := `{"type":"TEST_CHISQ","rows":"sex","params":{"expected":{"F":0.5,"M":0.5}},"label":"gof"}`
	wGof := `{"type":"TEST_CHISQ","rows":"sex","params":{"expected":{"F":0.4,"M":0.6}},"label":"wgof","weight":{"field":"score","kind":"probability"}}`
	prop1 := `{"type":"TEST_PROP_Z","field":"sex","params":{"success":"F","p0":0.4},"label":"p1"}`
	wProp1 := `{"type":"TEST_PROP_Z","field":"sex","params":{"success":"F","p0":0.4},"label":"wp1","weight":{"field":"score","kind":"frequency"}}`
	region := func() []*types.Group { return []*types.Group{mgRegion()} }
	regionTier := func() []*types.Group { return []*types.Group{mgRegion(), mgTier()} }
	regionTags := func() []*types.Group { return []*types.Group{mgRegion(), mgTags()} }
	return []gtCase{
		{"T+MWU/region", "[" + tSex + "," + mwu + "]", region},
		{"T+MWU/region×tier", "[" + tSex + "," + mwu + "]", regionTier},
		{"Chisq/region", "[" + chisq + "]", region},
		{"Chisq/region×tags", "[" + chisq + "]", regionTags},
		{"Weighted/region", "[" + wT + "," + wChi + "," + wMwu + "]", region},
		{"Weighted T+MWU/region×tier", "[" + wT + "," + wMwu + "]", regionTier},
		{"Weighted chisq/region×tags", "[" + wChi + "]", regionTags},
		{"GOF+one-proportion/region", "[" + gof + "," + prop1 + "]", region},
		{"Weighted GOF+one-proportion/region×tier", "[" + wGof + "," + wProp1 + "]", regionTier},
	}
}

func gtDecodeTests(t *testing.T, raw string) []*types.Test {
	t.Helper()
	var out []*types.Test
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode tests: %v", err)
	}
	return out
}

// gtKeyFilter isolates one group bucket: FILTER_INCLUDE on a category
// key, FILTER_SET_CONTAINS_ANY on a set-element key.
func gtKeyFilter(g *types.Group, v any) *types.Filterer {
	val := fmt.Sprint(v)
	if g.Type == types.GROUP_SET_PER_ELEMENT {
		return &types.Filterer{Type: types.FILTER_SET_CONTAINS_ANY, Field: g.Field, Values: []string{val}}
	}
	return &types.Filterer{Type: types.FILTER_INCLUDE, Field: g.Field, Values: []string{val}}
}

// gtModes is every mgModes mode that admits tier-1 tests: the streaming
// modes route buffered (tests + groups never stream) and a chain stage
// refuses tests; the Return mode's include list drops tests, so it is
// replaced by gtReturn.
func gtModes() []mgMode {
	var out []mgMode
	for _, m := range mgModes {
		switch m.name {
		case "Streaming", "ProcessStream", "ProcessChain", "Return":
			continue
		}
		out = append(out, m)
	}
	return out
}

// gtJSON is a result's wire form without its group_key, so a grouped
// result compares with the filtered run's (NaN -> null on both sides).
func gtJSON(t *testing.T, r *types.TestResult) string {
	t.Helper()
	cp := *r
	cp.GroupKey = nil
	b, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gtWarnings is resp's warnings with code, keyed by their group_key
// (fmt form); each is its JSON Details without group_key.
func gtWarnings(t *testing.T, ws []*types.ResponseWarning, code perr.Code) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, w := range ws {
		if w.Code != string(code) {
			continue
		}
		d := map[string]any{}
		for k, v := range w.Details {
			d[k] = v
		}
		key := fmt.Sprint(d["group_key"])
		delete(d, "group_key")
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		out[key] = append(out[key], string(b))
	}
	return out
}

func TestGroupedTests_EqualFilteredSingleGroupRuns(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for _, c := range gtCases() {
		for _, mode := range gtModes() {
			t.Run(c.name+"/"+mode.name, func(t *testing.T) {
				specs := gtDecodeTests(t, c.tests)
				groups := c.groups()
				req := mgRequest(f, groups)
				req.Tests = specs
				if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
					t.Fatalf("predict: %+v", env.Errors)
				}
				resp, err := mode.run(t, f, req)
				if err != nil {
					t.Fatalf("grouped run: %v", err)
				}
				nG := len(resp.Data)
				if nG < 3 {
					t.Fatalf("only %d groups; the fixture must split into several", nG)
				}
				if got, want := len(resp.Tests), len(specs)*nG; got != want {
					t.Fatalf("len(tests) = %d, want tests x groups = %d", got, want)
				}
				cols := make([]string, len(groups))
				for i, g := range groups {
					cols[i] = mgKeyColumn(g)
				}
				compared := 0
				for k, r := range resp.Tests {
					si, gi := k/nG, k%nG
					if r == nil {
						t.Fatalf("tests[%d] is nil", k)
					}
					if r.Label != specs[si].Label {
						t.Fatalf("tests[%d] label %q, want %q (test-major order)", k, r.Label, specs[si].Label)
					}
					// group_key follows the Data row order, Sort included.
					row := resp.Data[gi]
					want := make(types.AxisKey, len(cols))
					for i, col := range cols {
						want[i] = row[col]
					}
					if fmt.Sprint(r.GroupKey) != fmt.Sprint(want) {
						t.Fatalf("tests[%d] group_key %v, want Data[%d]'s %v", k, r.GroupKey, gi, want)
					}

					single := &types.Request{Cohort: &types.Cohort{Filename: f.cohort}, Tests: gtDecodeTests(t, c.tests)[si : si+1]}
					for i, g := range groups {
						single.Filterers = append(single.Filterers, gtKeyFilter(g, r.GroupKey[i]))
					}
					sresp, serr := f.p.Process(context.Background(), single)
					if serr != nil {
						var coded *perr.CodedError
						if !perrAs(serr, &coded) {
							t.Fatalf("single run for %v: uncoded error %v", r.GroupKey, serr)
						}
						// The grouped run reports the refusal as null
						// statistics plus a coded warning for this group.
						if !math.IsNaN(r.Statistic) || !math.IsNaN(r.PValue) || r.RejectNull {
							t.Fatalf("group %v: single run refused %s but grouped result is %+v", r.GroupKey, coded.Code, r)
						}
						ws := gtWarnings(t, resp.Warnings, coded.Code)[fmt.Sprint(r.GroupKey)]
						if len(ws) == 0 {
							t.Fatalf("group %v: no %s warning naming its group_key in %+v", r.GroupKey, coded.Code, resp.Warnings)
						}
						continue
					}
					if len(sresp.Tests) != 1 || sresp.Tests[0] == nil {
						t.Fatalf("single run: %d results", len(sresp.Tests))
					}
					if sresp.Tests[0].GroupKey != nil {
						t.Fatalf("ungrouped result carries group_key %v", sresp.Tests[0].GroupKey)
					}
					if got, want := gtJSON(t, r), gtJSON(t, sresp.Tests[0]); got != want {
						t.Errorf("group %v %s:\n got  %s\n want %s (the filtered single-group run)", r.GroupKey, r.Label, got, want)
					}
					// Low-n_eff warnings fire per group, as the filtered
					// run fires them.
					gw := gtWarnings(t, resp.Warnings, perr.PULSE_WEIGHT_LOW_NEFF)[fmt.Sprint(r.GroupKey)]
					sw := gtWarnings(t, sresp.Warnings, perr.PULSE_WEIGHT_LOW_NEFF)["<nil>"]
					var mine []string
					for _, w := range gw {
						if json.Valid([]byte(w)) && containsLabel(w, r.Label) {
							mine = append(mine, w)
						}
					}
					if !reflect.DeepEqual(mine, sw) {
						t.Errorf("group %v %s low-n_eff warnings:\n got  %v\n want %v", r.GroupKey, r.Label, mine, sw)
					}
					compared++
				}
				if compared == 0 {
					t.Fatal("no group compared against a single-group run")
				}
			})
		}
	}
}

// containsLabel reports whether a warning's Details JSON names test
// label.
func containsLabel(detailsJSON, label string) bool {
	var d map[string]any
	if json.Unmarshal([]byte(detailsJSON), &d) != nil {
		return false
	}
	return d["test"] == label
}

func perrAs(err error, target **perr.CodedError) bool {
	return stderrors.As(err, target)
}

// TestGroupedTests_SparseGroupsAndLowNEff pins the fixture facts the
// equivalence relies on, so a fixture edit cannot silently stop
// exercising them: south×gold holds one F record (TEST_T refuses it
// PULSE_TEST_INSUFFICIENT_N), west carries a single tier level
// (TEST_CHISQ refuses PULSE_TEST_CONTINGENCY_DEGENERATE), and the
// probability-weighted TEST_T is short of n_eff in some group.
func TestGroupedTests_SparseGroupsAndLowNEff(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	run := func(tests string, groups ...*types.Group) *types.Response {
		t.Helper()
		req := mgRequest(f, groups)
		req.Tests = gtDecodeTests(t, tests)
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return resp
	}
	resp := run(`[{"type":"TEST_T","field":"score","split_by":"sex","label":"t"}]`, mgRegion(), mgTier())
	ws := gtWarnings(t, resp.Warnings, perr.PULSE_TEST_INSUFFICIENT_N)
	if _, ok := ws["[south gold]"]; !ok || len(ws) != 1 {
		t.Fatalf("INSUFFICIENT_N warnings %v, want exactly south×gold", ws)
	}
	var nulls int
	for _, r := range resp.Tests {
		if math.IsNaN(r.PValue) {
			nulls++
			b, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			if m["statistic"] != nil || m["p_value"] != nil {
				t.Fatalf("sparse group's statistic / p_value are not JSON null: %s", b)
			}
			if fmt.Sprint(m["group_key"]) != "[south gold]" {
				t.Fatalf("sparse result group_key %v", m["group_key"])
			}
		}
	}
	if nulls != 1 || len(resp.Tests) != 5 {
		t.Fatalf("%d null results of %d, want 1 of 5", nulls, len(resp.Tests))
	}

	resp = run(`[{"type":"TEST_CHISQ","rows":"sex","cols":"tier","label":"chi"}]`, mgRegion())
	if ws := gtWarnings(t, resp.Warnings, perr.PULSE_TEST_CONTINGENCY_DEGENERATE); len(ws["[west]"]) != 1 || len(ws) != 1 {
		t.Fatalf("CONTINGENCY_DEGENERATE warnings %v, want exactly west", ws)
	}

	resp = run(`[{"type":"TEST_T","field":"y","split_by":"sex","label":"wt","weight":{"field":"score","kind":"probability"}}]`, mgRegion(), mgTier())
	low := gtWarnings(t, resp.Warnings, perr.PULSE_WEIGHT_LOW_NEFF)
	if len(low) == 0 {
		t.Fatal("no per-group PULSE_WEIGHT_LOW_NEFF warning; the weighted cases no longer exercise it")
	}
	for key := range low {
		if key == "<nil>" {
			t.Fatalf("a grouped low-n_eff warning names no group_key: %v", low)
		}
	}
}

// TestGroupedTests_UngroupedByteIdentical: an ungrouped request's tests
// carry no group_key and one result per slot.
func TestGroupedTests_UngroupedByteIdentical(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	req := &types.Request{Cohort: &types.Cohort{Filename: f.cohort},
		Tests: gtDecodeTests(t, `[{"type":"TEST_T","field":"score","split_by":"sex"},{"type":"TEST_CHISQ","rows":"sex","cols":"region"}]`)}
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Tests) != 2 {
		t.Fatalf("%d results, want 2", len(resp.Tests))
	}
	b, err := json.Marshal(resp.Tests)
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatal(err)
	}
	for i, m := range arr {
		if _, ok := m["group_key"]; ok {
			t.Fatalf("ungrouped tests[%d] carries group_key: %s", i, b)
		}
	}
}

// TestGroupedTests_SplitIsGrouperRefused: a test whose split field is
// also a grouper is refused by predict and the runtime with the same
// code and Details.
func TestGroupedTests_SplitIsGrouperRefused(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name   string
		tests  string
		groups []*types.Group
		code   perr.Code
		slot   string
	}{
		{"split_by", `[{"type":"TEST_T","field":"score","split_by":"sex"}]`, []*types.Group{mgSex()}, perr.PULSE_TEST_SPLIT_GROUPS_LT_2, "split_by"},
		{"split_by inner", `[{"type":"TEST_MANN_WHITNEY_U","field":"score","split_by":"sex"}]`, []*types.Group{mgRegion(), mgSex()}, perr.PULSE_TEST_SPLIT_GROUPS_LT_2, "split_by"},
		{"chisq rows", `[{"type":"TEST_CHISQ","rows":"tier","cols":"sex"}]`, []*types.Group{mgRegion(), mgTier()}, perr.PULSE_TEST_CONTINGENCY_DEGENERATE, "rows"},
		{"chisq cols", `[{"type":"TEST_CHISQ","rows":"sex","cols":"region"}]`, []*types.Group{mgRegion()}, perr.PULSE_TEST_CONTINGENCY_DEGENERATE, "cols"},
		{"chisq gof rows", `[{"type":"TEST_CHISQ","rows":"sex","params":{"expected":{"F":0.5,"M":0.5}}}]`, []*types.Group{mgSex()}, perr.PULSE_TEST_CONTINGENCY_DEGENERATE, "rows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := mgRequest(f, c.groups)
			req.Tests = gtDecodeTests(t, c.tests)
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
			_, err := f.p.Process(context.Background(), req)
			var coded *perr.CodedError
			if !perrAs(err, &coded) || coded.Code != c.code {
				t.Fatalf("runtime: %v, want %s", err, c.code)
			}
			if pd["slot"] != c.slot || pd["reason"] != "split_is_grouper" {
				t.Fatalf("predict details %v", pd)
			}
			pj, _ := json.Marshal(pd)
			rj, _ := json.Marshal(coded.Details)
			if string(pj) != string(rj) {
				t.Fatalf("details diverge:\n predict %s\n runtime %s", pj, rj)
			}
		})
	}
}

// TestGroupedTests_PredictCostAndPath: predict counts a grouped request's
// p-values as tests x groups (the many-tests advisory reads it), routes
// it buffered with a reason, and grades tests x groups against
// MaxGroups — the product the runtime refuses.
func TestGroupedTests_PredictCostAndPath(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	tests := `[{"type":"TEST_T","field":"score","split_by":"tier"},{"type":"TEST_MANN_WHITNEY_U","field":"y","split_by":"tier"}]`
	grouped := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
	grouped.Tests = gtDecodeTests(t, tests)
	pr, err := f.p.Predict(context.Background(), grouped)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 12 || pr.PValues.Uncorrected != 12 || pr.PValues.Basis != "dictionary" {
		t.Fatalf("p_values %+v, want 2 tests x 6 groups = 12 on the dictionary basis", pr.PValues)
	}
	many := false
	for _, a := range pr.Advisories {
		many = many || a.Code == string(perr.PULSE_ADVISORY_MANY_TESTS)
	}
	if !many {
		t.Fatalf("no PULSE_ADVISORY_MANY_TESTS over 12 grouped p-values: %+v", pr.Advisories)
	}
	if pr.Streamable {
		t.Fatal("grouped tests predicted streamable")
	}
	reason := false
	for _, r := range pr.StreamableReasons {
		reason = reason || r == "grouped tier-1 tests run once per group via the buffered path"
	}
	if !reason {
		t.Fatalf("streamable reasons %v name no grouped-test reason", pr.StreamableReasons)
	}

	ungrouped := mgRequest(f, nil)
	ungrouped.Tests = gtDecodeTests(t, tests)
	if pr, err = f.p.Predict(context.Background(), ungrouped); err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 2 || pr.PValues.Basis != "exact" {
		t.Fatalf("ungrouped p_values %+v, want 2 exact", pr.PValues)
	}
	for _, a := range pr.Advisories {
		if a.Code == string(perr.PULSE_ADVISORY_MANY_TESTS) {
			t.Fatalf("ungrouped request advised many tests: %+v", a)
		}
	}

	// A data-dependent grouper's bucket count is unknown: one p-value
	// per test, a lower bound.
	rng := mgRequest(f, []*types.Group{mgScoreRng()})
	rng.Tests = gtDecodeTests(t, tests)
	if pr, err = f.p.Predict(context.Background(), rng); err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 2 || pr.PValues.Basis != "lower_bound" {
		t.Fatalf("range-grouped p_values %+v, want 2 lower_bound", pr.PValues)
	}
}

func TestGroupedTests_LimitsCountTestsTimesGroups(t *testing.T) {
	f := newMGFixture(t, pulse.Options{Limits: pulse.Limits{MaxGroups: 10}})
	req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
	if _, err := f.p.Process(context.Background(), req); err != nil {
		t.Fatalf("6 groups under MaxGroups 10: %v", err)
	}
	req.Tests = gtDecodeTests(t, `[{"type":"TEST_T","field":"score","split_by":"tier"},{"type":"TEST_T","field":"y","split_by":"tier"}]`)
	pr, err := f.p.Predict(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lf := range pr.LimitFindings {
		if lf.Limit == "max_groups" {
			found = lf.Estimated == 12
		}
	}
	if !found {
		t.Fatalf("limit findings %+v, want max_groups estimated 2 tests x 6 groups = 12", pr.LimitFindings)
	}
	_, err = f.p.Process(context.Background(), req)
	var coded *perr.CodedError
	if !perrAs(err, &coded) || coded.Code != perr.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("runtime: %v, want PULSE_LIMIT_EXCEEDED for 12 grouped test runs", err)
	}
}

// TestGroupedTests_MultiplicityPoolsEveryGroup: a grouped test's results
// pair with their slot by groupedtests.SpecIndex, so a `request` family
// pools every (test x group) p-value, the sparse group's undefined p
// left out of m and counted in `excluded`.
func TestGroupedTests_MultiplicityPoolsEveryGroup(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	req := mgRequest(f, []*types.Group{mgRegion(), mgTier()})
	req.Tests = gtDecodeTests(t, `[{"type":"TEST_T","field":"score","split_by":"sex"},{"type":"TEST_MANN_WHITNEY_U","field":"score","split_by":"sex"}]`)
	req.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	resp, err := f.p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defined := 0
	for _, r := range resp.Tests {
		if !math.IsNaN(r.PValue) {
			defined++
		}
	}
	if defined < 6 {
		t.Fatalf("only %d defined p-values", defined)
	}
	for k, r := range resp.Tests {
		if r.Multiplicity == nil || r.PAdjusted == nil {
			t.Fatalf("tests[%d] (%v) uncorrected", k, r.GroupKey)
		}
		if r.Multiplicity.M != defined || r.Multiplicity.Excluded != len(resp.Tests)-defined {
			t.Fatalf("tests[%d] m = %d excluded = %d, want every defined (test x group) p = %d, %d excluded", k, r.Multiplicity.M, r.Multiplicity.Excluded, defined, len(resp.Tests)-defined)
		}
	}
}
