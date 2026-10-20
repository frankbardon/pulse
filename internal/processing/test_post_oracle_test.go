package processing

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Post-test twins over rows holding nulls (U36 E1-S5). The golden
// test_post_nulls.json is written by scripts/reference/gen_post_tests.R
// (`make reference`): one case per post family, its rows verbatim (null
// where R reads NA), the R figures at R's default NA handling. Pulse
// drops a row with a null in any column the test reads (listwise) and
// reports the n it used; the shared relative 1e-10 applies throughout.

type postNullCase struct {
	Family string `json:"family"`
	Name   string `json:"name"`
	Spec   struct {
		Field   string          `json:"field"`
		Field2  string          `json:"field2"`
		SplitBy string          `json:"split_by"`
		OrderBy string          `json:"order_by"`
		Params  json.RawMessage `json:"params"`
	} `json:"spec"`
	Rows     []map[string]any   `json:"rows"`
	NUsed    int                `json:"n_used"`
	Expect   map[string]float64 `json:"expect"`
	RawValue []float64          `json:"raw_value"`
	RawGroup []string           `json:"raw_group"`
}

func (c postNullCase) test() *types.Test {
	t := &types.Test{
		Type: types.TestType(c.Family), Field: c.Spec.Field, Field2: c.Spec.Field2,
		SplitBy: c.Spec.SplitBy, Params: c.Spec.Params,
	}
	if c.Spec.OrderBy != "" {
		t.OrderBy = []types.OrderKey{{Field: c.Spec.OrderBy}}
	}
	return t
}

func runPostCase(t *testing.T, c postNullCase, rows []map[string]any) *types.TestResult {
	t.Helper()
	factory, ok := postTestRegistry[types.TestType(c.Family)]
	if !ok {
		t.Fatalf("%s: %s has no post-test", c.Name, c.Family)
	}
	pt, err := factory(c.test(), nil)
	if err != nil {
		t.Fatalf("%s: build %s post: %v", c.Name, c.Family, err)
	}
	res, err := pt.Run(rows)
	if err != nil {
		t.Fatalf("%s: %s post Run: %v", c.Name, c.Family, err)
	}
	return res
}

// resultPath reads "statistic" / "p_value" / "df" or a dotted
// "details.<key>[.<index>|.<key>]..." path out of res.
func resultPath(t *testing.T, res *types.TestResult, path string) float64 {
	t.Helper()
	switch path {
	case "statistic":
		return res.Statistic
	case "p_value":
		return res.PValue
	case "df":
		return res.DF
	}
	parts := strings.Split(path, ".")
	if parts[0] != "details" {
		t.Fatalf("expect path %q: want statistic, p_value, df or details.*", path)
	}
	var cur any = res.Details
	for _, p := range parts[1:] {
		idx, _ := strconv.Atoi(p)
		switch v := cur.(type) {
		case map[string]any:
			cur = v[p]
		case []map[string]any:
			cur = v[idx]
		case []any:
			cur = v[idx]
		case []float64:
			cur = v[idx]
		case []int:
			cur = v[idx]
		case []int64:
			cur = v[idx]
		default:
			t.Fatalf("expect path %q: cannot index %T with %q", path, cur, p)
		}
	}
	switch v := cur.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	t.Fatalf("expect path %q = %#v (%T), want a number", path, cur, cur)
	return 0
}

func loadPostNullCases(t *testing.T) []postNullCase {
	t.Helper()
	doc := loadReference[postNullCase](t, "test_post_nulls")
	if doc.Packages["nortest"] == "" {
		t.Fatal("test_post_nulls golden does not record the nortest version")
	}
	return doc.Cases
}

// TestReferenceOracle_PostTestNulls: every post family over rows holding
// nulls against R at its default NA handling (listwise), 1e-10.
func TestReferenceOracle_PostTestNulls(t *testing.T) {
	cases := loadPostNullCases(t)
	seen := map[types.TestType]bool{}
	c := newOracleCheck(t, "post-test nulls", oracleRelTol, oracleFigureAbsFloor)
	figures := 0
	for _, k := range cases {
		seen[types.TestType(k.Family)] = true
		nulls := 0
		for _, row := range k.Rows {
			for _, v := range row {
				if v == nil {
					nulls++
					break
				}
			}
		}
		if nulls == 0 {
			t.Fatalf("%s: no row holds a null; the case must exercise the listwise drop", k.Name)
		}
		// A raw-row family's n_used is its complete rows (the summary
		// posts count observations, the Tukey post groups).
		if _, raw := postTwinFamilies[types.TestType(k.Family)]; raw && k.Family != string(types.TEST_ANOVA_F) &&
			k.Family != string(types.TEST_ANOVA_WELCH) && len(k.Rows)-nulls != k.NUsed {
			t.Fatalf("%s: %d rows, %d with a null, n_used %d", k.Name, len(k.Rows), nulls, k.NUsed)
		}
		res := runPostCase(t, k, k.Rows)
		if res.Error != nil {
			t.Fatalf("%s: slot failed: %+v", k.Name, res.Error)
		}
		keys := make([]string, 0, len(k.Expect))
		for p := range k.Expect {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		for _, p := range keys {
			c.check(resultPath(t, res, p), k.Expect[p], "%s %s %s", k.Family, k.Name, p)
			figures++
		}
	}
	for tt := range postTestRegistry {
		if !seen[tt] {
			t.Errorf("post family %s has no R case with nulls in test_post_nulls.json", tt)
		}
	}
	c.done(figures)
}

// TestPostTests_NonNumericNonNullStillRefused: the listwise drop is for
// nulls only — a present non-numeric value still refuses with
// PULSE_TEST_FIELD_NOT_NUMERIC, a missing column with SERVICE_VALIDATION.
func TestPostTests_NonNumericNonNullStillRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		row  map[string]any
		code errors.Code
	}{
		{"string value", map[string]any{"x": "seven", "y": 1.0}, errors.PULSE_TEST_FIELD_NOT_NUMERIC},
		{"missing column", map[string]any{"y": 1.0}, errors.SERVICE_VALIDATION},
	} {
		rows := []map[string]any{
			{"x": nil, "y": 2.0}, {"x": 1.0, "y": 2.5}, {"x": 2.0, "y": 2.0}, {"x": 3.0, "y": 4.0}, c.row,
		}
		pt, err := newPearsonRPost(&types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pt.Run(rows)
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != c.code {
			t.Errorf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
}

// postTwinRowSchema is the raw-record schema a twin's row variant reads:
// x / y for the paired families, value (+ a categorical group) for the
// one-column families.
func postTwinRowSchema(paired bool) *encoding.Schema {
	if paired {
		return pairedFixtureSchema()
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "value", Type: encoding.FieldTypeF64},
		{Name: "group", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

type twinRecord struct {
	x, y  float64
	group string
}

// runTwinRow runs the row variant of family over recs.
func runTwinRow(t *testing.T, name string, spec *types.Test, paired bool, recs []twinRecord) *types.TestResult {
	t.Helper()
	schema := postTwinRowSchema(paired)
	factory := rowTestRegistry[spec.Type]
	rt, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("%s: build %s row: %v", name, spec.Type, err)
	}
	for _, r := range recs {
		var rec *Record
		if paired {
			rec = NewRecord(schema, map[string]float64{"x": r.x, "y": r.y})
		} else {
			rec = NewRecord(schema, map[string]float64{
				"value": r.x, "group": float64(dictIDOrAdd(schema, "group", r.group)),
			})
		}
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatalf("%s: UpdateRow: %v", name, err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s: %s row Finalize: %v", name, spec.Type, err)
	}
	return res
}

// twinCheck compares a post twin's result with its row variant's:
// statistic, p, df and every scalar numeric details key both carry.
// NaN matches NaN and ±Inf the same infinity (an undefined figure is the
// same undefined figure on both arms).
func twinCheck(t *testing.T, c *oracleCheck, name string, post, row *types.TestResult) int {
	t.Helper()
	pairs := [][3]any{{"statistic", post.Statistic, row.Statistic}, {"p_value", post.PValue, row.PValue}, {"df", post.DF, row.DF}}
	keys := make([]string, 0, len(post.Details))
	for k := range post.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pv, pok := scalarNumber(post.Details[k])
		rv, rok := scalarNumber(row.Details[k])
		if pok && rok {
			pairs = append(pairs, [3]any{"details." + k, pv, rv})
		}
	}
	for _, p := range pairs {
		got, want := p[1].(float64), p[2].(float64)
		switch {
		case math.IsNaN(want) || math.IsInf(want, 0):
			if !(math.IsNaN(got) && math.IsNaN(want)) && got != want {
				t.Errorf("%s %s: post %v, row %v", name, p[0], got, want)
			}
		default:
			c.check(got, want, "%s %s", name, p[0])
		}
	}
	return len(pairs)
}

func scalarNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// postTwinFamilies are the post twins that have a row variant (TEST_TREND
// and TEST_TUKEY_HSD are post-only; their R oracles pin the post arm
// directly) and whether the family reads two paired columns.
var postTwinFamilies = map[types.TestType]bool{
	types.TEST_PEARSON_R: true, types.TEST_SPEARMAN_R: true, types.TEST_KENDALL_TAU: true,
	types.TEST_PAIRED_T: true, types.TEST_WILCOXON_SR: true,
	types.TEST_ANOVA_F: false, types.TEST_ANOVA_WELCH: false, types.TEST_BROWN_FORSYTHE: false,
	types.TEST_SHAPIRO_WILK: false, types.TEST_KS: false,
}

// TestPostTwins_MatchRowVariant: every post twin equals its R-pinned row
// variant on identical data, relative 1e-10 — the post twin fed the
// golden's rows WITH their nulls, the row variant only the complete
// records (the summary posts: the raw rows of the non-null groups), so
// the listwise drop is part of what is compared. The R-pinned goldens of
// the paired / split families add the untouched-data leg.
func TestPostTwins_MatchRowVariant(t *testing.T) {
	c := newOracleCheck(t, "post twin vs row variant", oracleRelTol, oracleFigureAbsFloor)
	figures := 0
	covered := map[types.TestType]bool{}
	for _, k := range loadPostNullCases(t) {
		tt := types.TestType(k.Family)
		paired, twin := postTwinFamilies[tt]
		if !twin {
			continue
		}
		covered[tt] = true
		post := runPostCase(t, k, k.Rows)
		rowSpec := k.test()
		var recs []twinRecord
		switch tt {
		case types.TEST_ANOVA_F, types.TEST_ANOVA_WELCH:
			rowSpec.Field, rowSpec.SplitBy, rowSpec.Params = "value", "group", nil
			for i, v := range k.RawValue {
				recs = append(recs, twinRecord{x: v, group: k.RawGroup[i]})
			}
		default:
			for _, row := range k.Rows {
				if postRowHasNull(row, k.Spec.Field, k.Spec.Field2, k.Spec.SplitBy) {
					continue
				}
				r := twinRecord{x: row[k.Spec.Field].(float64)}
				if paired {
					r.y = row[k.Spec.Field2].(float64)
				} else if k.Spec.SplitBy != "" {
					r.group = row[k.Spec.SplitBy].(string)
				}
				recs = append(recs, r)
			}
			if !paired {
				rowSpec.Field = "value"
				if k.Spec.SplitBy != "" {
					rowSpec.SplitBy = "group"
				}
			}
		}
		if len(recs) != k.NUsed {
			t.Fatalf("%s: %d complete records, golden n_used %d", k.Name, len(recs), k.NUsed)
		}
		figures += twinCheck(t, c, k.Family+" "+k.Name, post, runTwinRow(t, k.Name, rowSpec, paired, recs))
	}
	for tt := range postTwinFamilies {
		if !covered[tt] {
			t.Errorf("post twin %s has no equivalence case", tt)
		}
	}

	// Untouched data: the R-pinned row goldens replayed through both arms.
	pairedLeg := func(tt types.TestType, name string, x, y []float64, alpha float64) {
		rows := make([]map[string]any, len(x))
		recs := make([]twinRecord, len(x))
		for i := range x {
			rows[i] = map[string]any{"x": x[i], "y": y[i]}
			recs[i] = twinRecord{x: x[i], y: y[i]}
		}
		spec := &types.Test{Type: tt, Field: "x", Field2: "y", Alpha: alpha}
		pt, err := postTestRegistry[tt](spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		post, err := pt.Run(rows)
		if err != nil {
			t.Fatalf("%s: post: %v", name, err)
		}
		figures += twinCheck(t, c, string(tt)+" "+name, post, runTwinRow(t, name, spec, true, recs))
	}
	splitLeg := func(tt types.TestType, name string, values []float64, groups []string) {
		rows := make([]map[string]any, len(values))
		recs := make([]twinRecord, len(values))
		for i := range values {
			rows[i] = map[string]any{"value": values[i], "group": groups[i]}
			recs[i] = twinRecord{x: values[i], group: groups[i]}
		}
		spec := &types.Test{Type: tt, Field: "value", SplitBy: "group"}
		pt, err := postTestRegistry[tt](spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		post, err := pt.Run(rows)
		if err != nil {
			t.Fatalf("%s: post: %v", name, err)
		}
		figures += twinCheck(t, c, string(tt)+" "+name, post, runTwinRow(t, name, spec, false, recs))
	}
	// summaryLeg: the one-way posts read per-group summary rows (n, mean,
	// sample variance, two-pass here) of the values the row variant reads.
	summaryLeg := func(tt types.TestType, name string, values []float64, groups []string) {
		byGroup := map[string][]float64{}
		var order []string
		recs := make([]twinRecord, len(values))
		for i, v := range values {
			if _, ok := byGroup[groups[i]]; !ok {
				order = append(order, groups[i])
			}
			byGroup[groups[i]] = append(byGroup[groups[i]], v)
			recs[i] = twinRecord{x: v, group: groups[i]}
		}
		rows := make([]map[string]any, 0, len(order))
		for _, g := range order {
			vs := byGroup[g]
			var mean, ss float64
			for _, v := range vs {
				mean += v
			}
			mean /= float64(len(vs))
			for _, v := range vs {
				ss += (v - mean) * (v - mean)
			}
			rows = append(rows, map[string]any{"group": g, "mean": mean, "n": int64(len(vs)), "variance": ss / float64(len(vs)-1)})
		}
		params := json.RawMessage(`{"n_col":"n","variance_col":"variance"}`)
		pt, err := postTestRegistry[tt](&types.Test{Type: tt, Field: "mean", SplitBy: "group", Params: params}, nil)
		if err != nil {
			t.Fatal(err)
		}
		post, err := pt.Run(rows)
		if err != nil {
			t.Fatalf("%s %s: post: %v", tt, name, err)
		}
		row := runTwinRow(t, name, &types.Test{Type: tt, Field: "value", SplitBy: "group"}, false, recs)
		figures += twinCheck(t, c, string(tt)+" "+name, post, row)
	}
	for _, k := range loadReference[struct {
		Name  string    `json:"name"`
		Alpha float64   `json:"alpha"`
		X     []float64 `json:"x"`
		Y     []float64 `json:"y"`
	}](t, "test_pearson_r").Cases {
		pairedLeg(types.TEST_PEARSON_R, k.Name, k.X, k.Y, k.Alpha)
		pairedLeg(types.TEST_SPEARMAN_R, k.Name, k.X, k.Y, k.Alpha)
		pairedLeg(types.TEST_PAIRED_T, k.Name, k.X, k.Y, k.Alpha)
	}
	for _, k := range loadReference[struct {
		Name string    `json:"name"`
		X    []float64 `json:"x"`
		Y    []float64 `json:"y"`
	}](t, "test_kendall_tau").Cases {
		pairedLeg(types.TEST_KENDALL_TAU, k.Name, k.X, k.Y, 0)
		if len(k.X) >= 6 {
			pairedLeg(types.TEST_WILCOXON_SR, k.Name, k.X, k.Y, 0)
		}
	}
	for _, k := range loadReference[struct {
		Name string    `json:"name"`
		A    []float64 `json:"a"`
		B    []float64 `json:"b"`
	}](t, "test_ks").Cases {
		values := append(append([]float64(nil), k.A...), k.B...)
		groups := make([]string, 0, len(values))
		for i := range values {
			groups = append(groups, map[bool]string{true: "a", false: "b"}[i < len(k.A)])
		}
		splitLeg(types.TEST_KS, k.Name, values, groups)
		splitLeg(types.TEST_BROWN_FORSYTHE, k.Name, values, groups)
		splitLeg(types.TEST_SHAPIRO_WILK, k.Name, values, groups)
		summaryLeg(types.TEST_ANOVA_F, k.Name, values, groups)
		summaryLeg(types.TEST_ANOVA_WELCH, k.Name, values, groups)
	}
	for _, k := range loadReference[bfOracleCase](t, "test_brown_forsythe").Cases {
		splitLeg(types.TEST_BROWN_FORSYTHE, k.Name, k.Value, k.Group)
		summaryLeg(types.TEST_ANOVA_F, k.Name, k.Value, k.Group)
	}
	c.done(figures)
}
