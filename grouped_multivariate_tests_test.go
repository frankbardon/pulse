package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Grouped multivariate tests (#404): TEST_HOTELLING_T2, TEST_MANOVA,
// TEST_BOX_M, TEST_MARDIA and TEST_BARTLETT_SPHERICITY run once per
// composite group like every tier-1 test, and each group's result is
// the ungrouped test filtered to that group. A degenerate group never
// aborts the request: a rank-deficient one (region "c" — x3 = x1 + x2)
// is a null-statistic result carrying PULSE_MATRIX_SINGULAR (the five
// warn, never refuse, on a singular matrix), a tiny one (region "d",
// no more rows than members) the same or — where the test refuses it
// PULSE_TEST_INSUFFICIENT_N — a null-statistic result plus a
// group_key-named Response warning; the good groups are intact. A test
// that DOES refuse a group with PULSE_MATRIX_SINGULAR /
// PULSE_MATRIX_INSUFFICIENT_N (an extension's) is a sparse group like
// any other data-dependent refusal (sparseTestCodes).

// gmvRow is one record of the grouped-multivariate fixture.
type gmvRow struct {
	region, tier, sex string
	x1, x2, x3        float64
}

func gmvRows() []gmvRow {
	var rows []gmvRow
	for _, region := range []string{"a", "b", "c"} {
		for i := 0; i < 40; i++ {
			// Small integers: the sums are exact, so region c's
			// dependency is exact, and no generator product meets FMA.
			x1 := float64((i*7+len(region)*3)%13) + float64(len(rows)%5)
			x2 := float64((i*11)%17) + float64(i%3)
			x3 := float64((i*5+1)%19) + float64((i*i)%7)
			if region == "c" {
				x3 = x1 + x2
			}
			sex, tier := "F", "u"
			if i%2 == 1 {
				sex = "M"
			}
			if (i/2)%2 == 1 {
				tier = "v"
			}
			rows = append(rows, gmvRow{region, tier, sex, x1, x2, x3})
		}
	}
	// Region d: three rows over three members — too few for any of the
	// five (n ≤ p).
	for i := 0; i < 3; i++ {
		sex := "F"
		if i == 1 {
			sex = "M"
		}
		rows = append(rows, gmvRow{"d", "u", sex, float64(i), float64(2 * i), float64(i * i)})
	}
	return rows
}

func gmvCohort(t *testing.T, opts pulse.Options) (*pulse.Pulse, string) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("a", "b", "c", "d")},
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("u", "v")},
		{Name: "sex", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("F", "M")},
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64},
		{Name: "x3", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx, fields[i].ByteOffset = i, off
		fields[i].Description = "Grouped multivariate fixture column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	const cohort = "gmv.pulse"
	b, err := p.NewCohortBuilder(context.Background(), cohort, encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range gmvRows() {
		if err := b.Append(pulse.CohortRow{r.region, r.tier, r.sex, r.x1, r.x2, r.x3}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, cohort
}

func TestGroupedMultivariateTests_EqualFilteredRunsAndDegenerateGroupsDoNotAbort(t *testing.T) {
	p, cohort := gmvCohort(t, pulse.Options{})
	ctx := context.Background()
	members := `"fields":["x1","x2","x3"]`
	tests := map[types.TestType]string{
		types.TEST_HOTELLING_T2:        `{"type":"TEST_HOTELLING_T2",` + members + `,"split_by":"sex","label":"h"}`,
		types.TEST_MANOVA:              `{"type":"TEST_MANOVA",` + members + `,"split_by":"sex","label":"m"}`,
		types.TEST_BOX_M:               `{"type":"TEST_BOX_M",` + members + `,"split_by":"sex","label":"bm"}`,
		types.TEST_MARDIA:              `{"type":"TEST_MARDIA",` + members + `,"label":"md"}`,
		types.TEST_BARTLETT_SPHERICITY: `{"type":"TEST_BARTLETT_SPHERICITY",` + members + `,"label":"bs"}`,
	}
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	tier := &types.Group{Type: types.GROUP_CATEGORY, Field: "tier"}
	for typ, spec := range tests {
		for name, groups := range map[string][]*types.Group{"region": {region}, "region×tier": {region, tier}} {
			t.Run(string(typ)+"/"+name, func(t *testing.T) {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: groups,
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Label: "n"}},
					Tests:        gtDecodeTests(t, "["+spec+"]")}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("grouped run aborted: %v", err)
				}
				if len(resp.Tests) != len(resp.Data) || len(resp.Data) < 4 {
					t.Fatalf("%d results over %d groups", len(resp.Tests), len(resp.Data))
				}
				var good, degenerate, sparse int
				for _, r := range resp.Tests {
					single := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Tests: gtDecodeTests(t, "["+spec+"]")}
					for i, g := range groups {
						single.Filterers = append(single.Filterers, gtKeyFilter(g, r.GroupKey[i]))
					}
					sresp, serr := p.Process(ctx, single)
					if serr != nil {
						var coded *perr.CodedError
						if !perrAs(serr, &coded) {
							t.Fatalf("group %v: uncoded %v", r.GroupKey, serr)
						}
						if !math.IsNaN(r.Statistic) || !math.IsNaN(r.PValue) {
							t.Fatalf("group %v: filtered run refused %s, grouped result %+v", r.GroupKey, coded.Code, r)
						}
						if len(gtWarnings(t, resp.Warnings, coded.Code)[fmt.Sprint(r.GroupKey)]) != 1 {
							t.Fatalf("group %v: no %s warning naming it in %+v", r.GroupKey, coded.Code, resp.Warnings)
						}
						sparse++
						continue
					}
					if got, want := gtJSON(t, r), gtJSON(t, sresp.Tests[0]); got != want {
						t.Fatalf("group %v:\n got  %s\n want %s", r.GroupKey, got, want)
					}
					switch fmt.Sprint(r.GroupKey[0]) {
					case "c", "d":
						// Rank-deficient (c) or no more rows than members
						// (d, where the test does not refuse it outright):
						// a null statistic carrying the matrix warning.
						if !math.IsNaN(r.Statistic) || !strings.Contains(strings.Join(r.Warnings, " "), string(perr.PULSE_MATRIX_SINGULAR)) {
							t.Fatalf("degenerate group %v: %+v, want a null statistic carrying %s", r.GroupKey, r, perr.PULSE_MATRIX_SINGULAR)
						}
						degenerate++
					default:
						if math.IsNaN(r.Statistic) {
							b, _ := json.Marshal(r)
							t.Fatalf("good group %v has a null statistic: %s", r.GroupKey, b)
						}
						good++
					}
				}
				if good == 0 || degenerate == 0 {
					t.Fatalf("good %d, degenerate %d (sparse %d): the fixture must exercise both", good, degenerate, sparse)
				}
			})
		}
	}
}

// gmvMatrixTest is an extension tier-1 test that refuses its data with
// the matrix codes: PULSE_MATRIX_INSUFFICIENT_N under 5 rows,
// PULSE_MATRIX_SINGULAR when every row has x3 = x1 + x2; otherwise its
// statistic is the row count.
type gmvMatrixTest struct{ n, dependent *int }

func (g gmvMatrixTest) UpdateRow(rec extend.Record) error {
	x1, _ := rec.NumericValue("x1")
	x2, _ := rec.NumericValue("x2")
	x3, _ := rec.NumericValue("x3")
	*g.n++
	if x3 == x1+x2 {
		*g.dependent++
	}
	return nil
}

func (g gmvMatrixTest) Finalize() (*types.TestResult, error) {
	switch {
	case *g.n < 5:
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_INSUFFICIENT_N, "too few rows", map[string]any{"n": *g.n})
	case *g.dependent == *g.n:
		return nil, perr.NewCodedErrorWithDetails(perr.PULSE_MATRIX_SINGULAR, "x3 = x1 + x2", map[string]any{"n": *g.n})
	}
	return &types.TestResult{Statistic: float64(*g.n), PValue: 0.5, Alpha: 0.05}, nil
}

// TestGroupedTests_MatrixRefusalIsSparseGroup: a group a test refuses
// with PULSE_MATRIX_SINGULAR or PULSE_MATRIX_INSUFFICIENT_N used to
// abort the whole grouped request; it is now that group's
// null-statistic result plus one warning naming its group_key (the
// filtered single-group run still refuses), and the good groups keep
// their results.
func TestGroupedTests_MatrixRefusalIsSparseGroup(t *testing.T) {
	p, cohort := gmvCohort(t, pulse.Options{Extensions: pulse.Extensions{Tests: []pulse.TestRegistration{{
		Name: "TEST_ACME_GMV", Tier: pulse.TestTierRow,
		RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) {
			return gmvMatrixTest{n: new(int), dependent: new(int)}, nil
		},
	}}}})
	ctx := context.Background()
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Label: "n"}},
		Tests:        []*types.Test{{Type: "TEST_ACME_GMV", Label: "g"}}}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("grouped run aborted: %v", err)
	}
	want := map[string]perr.Code{"[c]": perr.PULSE_MATRIX_SINGULAR, "[d]": perr.PULSE_MATRIX_INSUFFICIENT_N}
	for _, r := range resp.Tests {
		key := fmt.Sprint(r.GroupKey)
		code, bad := want[key]
		if !bad {
			if r.Statistic != 40 {
				t.Fatalf("good group %s: %+v", key, r)
			}
			continue
		}
		if !math.IsNaN(r.Statistic) || !math.IsNaN(r.PValue) {
			t.Fatalf("group %s: %+v, want null statistics", key, r)
		}
		if ws := gtWarnings(t, resp.Warnings, code); len(ws) != 1 || len(ws[key]) != 1 {
			t.Fatalf("group %s: %s warnings %v, want one naming it", key, code, ws)
		}
		single := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Tests: req.Tests,
			Filterers: []*types.Filterer{gtKeyFilter(req.Groups[0], r.GroupKey[0])}}
		var coded *perr.CodedError
		if _, serr := p.Process(ctx, single); !perrAs(serr, &coded) || coded.Code != code {
			t.Fatalf("group %s filtered run: %v, want %s", key, serr, code)
		}
	}
	if len(resp.Tests) != 4 {
		t.Fatalf("%d results, want one per region", len(resp.Tests))
	}
}
