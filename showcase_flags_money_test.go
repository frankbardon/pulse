package pulse_test

import (
	"context"
	stdcsv "encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// The four U45 showcase shapes (TODO #317, in-pulse proof): a credit
// logistic fit on a packed_bool outcome scored with exp, an A/B
// proportion test on a packed_bool conversion flag, a chi-square
// directly on a packed_bool churn flag, and calendar-month retention
// plus exact money ratios. Each runs the committed example request
// (internal/examples) against the committed subscribers fixture in a
// MemMap filesystem: predict first under Strict and clean, then the
// runtime, whose numbers are pinned against an oracle derived here
// from the CSV with the standard library — never from engine output.

const (
	showcaseCohort = "subscribers.pulse"
	showcaseCSV    = "internal/examples/fixtures/subscribers.csv"
	showcaseSchema = "internal/examples/fixtures/schemas/subscribers.json"
)

// showcaseRow is one parsed subscribers.csv row (the oracle's input).
type showcaseRow struct {
	arm                          string
	converted, churned, defaulty bool
	plan                         string
	late                         int
	signup, repeat               time.Time
	revenue, cost                int64 // cents
}

// showcaseRows parses the fixture CSV with encoding/csv.
func showcaseRows(t *testing.T, csvBytes []byte) []showcaseRow {
	t.Helper()
	recs, err := stdcsv.NewReader(strings.NewReader(string(csvBytes))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	cents := func(s string) int64 {
		whole, frac, ok := strings.Cut(s, ".")
		if !ok || len(frac) != 2 {
			t.Fatalf("money %q is not cents-exact", s)
		}
		w, err1 := strconv.ParseInt(whole, 10, 64)
		f, err2 := strconv.ParseInt(frac, 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("money %q", s)
		}
		return w*100 + f
	}
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	var rows []showcaseRow
	for _, r := range recs[1:] {
		late, err := strconv.Atoi(r[5])
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, showcaseRow{
			arm: r[1], converted: r[2] == "true", plan: r[3], churned: r[4] == "true",
			late: late, defaulty: r[6] == "true",
			signup: day(r[7]), repeat: day(r[8]),
			revenue: cents(r[9]), cost: cents(r[10]),
		})
	}
	if len(rows) != 48 {
		t.Fatalf("fixture has %d rows, want 48", len(rows))
	}
	return rows
}

// showcasePulse imports the committed fixture into a MemMap filesystem
// (optionally rewriting the CSV first) and returns a Strict instance
// over it plus the parsed oracle rows.
func showcasePulse(t *testing.T, rewrite func(string) string) (*pulse.Pulse, []showcaseRow) {
	t.Helper()
	csvBytes, err := os.ReadFile(showcaseCSV)
	if err != nil {
		t.Fatal(err)
	}
	if rewrite != nil {
		csvBytes = []byte(rewrite(string(csvBytes)))
	}
	schema, err := loadFixtureSchema(showcaseSchema)
	if err != nil {
		t.Fatal(err)
	}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "subscribers.csv", csvBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	importer, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(context.Background(), &pio.ImportJob{
		Source: csv.NewReader(fsys, "subscribers.csv"), Target: showcaseCohort,
		Schema: schema, SampleRows: 50, FS: fsys,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return p, showcaseRows(t, csvBytes)
}

// showcaseRun loads a committed example request, predicts it under
// Strict (it must be valid with no error and no warning), then runs it.
func showcaseRun(t *testing.T, p *pulse.Pulse, example string) (*types.Request, *types.Response) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("internal", "examples", example))
	if err != nil {
		t.Fatal(err)
	}
	var req types.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode %s: %v", example, err)
	}
	req.Cohort = &types.Cohort{Filename: showcaseCohort}
	pred, err := p.Predict(context.Background(), &req)
	if err != nil {
		t.Fatalf("Predict %s: %v", example, err)
	}
	if !pred.Valid || len(pred.Errors) != 0 || len(pred.Warnings) != 0 {
		t.Fatalf("Predict %s not clean under Strict: valid=%v errors=%s warnings=%s",
			example, pred.Valid, entriesString(pred.Errors), entriesString(pred.Warnings))
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s: %v", example, err)
	}
	return &req, resp
}

func entriesString(es any) string {
	b, _ := json.Marshal(es)
	return string(b)
}

// detailsAs re-decodes a result's Details map into a typed struct.
func detailsAs(t *testing.T, details map[string]any, into any) {
	t.Helper()
	b, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("details %s: %v", b, err)
	}
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// TestShowcase_CreditLogisticOnFlag: REG_GLM binomial fits a packed_bool
// default flag directly. With one 0/1 predictor the logistic MLE is
// closed form — intercept logit(p0), slope logit(p1) − logit(p0), SEs
// √(1/a + 1/b) from the 2×2 counts — and an exp-based ATTR_FORMULA
// score at those coefficients sums to the observed default count.
func TestShowcase_CreditLogisticOnFlag(t *testing.T) {
	p, rows := showcasePulse(t, nil)
	req, resp := showcaseRun(t, p, "regression/18_logistic_flag_score.json")

	// Oracle: the 2×2 late_payment × defaulted counts.
	var cnt [2][2]float64 // [late][defaulted]
	defaults := 0.0
	for _, r := range rows {
		d := 0
		if r.defaulty {
			d = 1
			defaults++
		}
		cnt[r.late][d]++
	}
	logit := func(yes, no float64) float64 { return math.Log(yes / no) }
	b0 := logit(cnt[0][1], cnt[0][0])
	b1 := logit(cnt[1][1], cnt[1][0]) - b0
	se0 := math.Sqrt(1/cnt[0][1] + 1/cnt[0][0])
	se1 := math.Sqrt(1/cnt[0][1] + 1/cnt[0][0] + 1/cnt[1][1] + 1/cnt[1][0])
	if cnt[0][1] != 4 || cnt[1][1] != 8 {
		t.Fatalf("fixture drifted: defaults on-time %v, late %v", cnt[0][1], cnt[1][1])
	}

	if len(resp.Regressions) != 1 {
		t.Fatalf("regressions = %d", len(resp.Regressions))
	}
	fit := resp.Regressions[0]
	if fit.NObs != len(rows) {
		t.Errorf("n_obs = %d, want %d", fit.NObs, len(rows))
	}
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"b0", fit.Coefficients["(intercept)"], b0},
		{"b1", fit.Coefficients["late_payment"], b1},
		{"se0", fit.StdErrors["(intercept)"], se0},
		{"se1", fit.StdErrors["late_payment"], se1},
	} {
		if !near(c.got, c.want, 1e-7) {
			t.Errorf("%s = %.12g, want %.12g", c.name, c.got, c.want)
		}
	}

	// The example's pasted coefficients are the fit's (no 2.718 ** idiom).
	expr := req.Attributes[0].Expression
	if !strings.Contains(expr, "exp(") || strings.Contains(expr, "**") {
		t.Fatalf("score expression %q is not exp-based", expr)
	}
	lits := regexp.MustCompile(`-?\d+\.\d+`).FindAllString(expr, -1)
	if len(lits) != 2 {
		t.Fatalf("score expression %q: want two coefficient literals, got %v", expr, lits)
	}
	for i, want := range []float64{b0, b1} {
		got, _ := strconv.ParseFloat(lits[i], 64)
		if !near(got, want, 1e-12) {
			t.Errorf("pasted coefficient %d = %v, want %v", i, got, want)
		}
	}

	// Σ score = Σ y at the MLE (the binomial score equation).
	got := resp.Data[0]
	if v, _ := got["expected_defaults"].(float64); !near(v, defaults, 1e-9) {
		t.Errorf("expected_defaults = %#v, want %v", got["expected_defaults"], defaults)
	}
	if v, _ := got["mean_score"].(float64); !near(v, defaults/float64(len(rows)), 1e-12) {
		t.Errorf("mean_score = %#v, want %v", got["mean_score"], defaults/float64(len(rows)))
	}
}

// TestShowcase_ABPropZOnFlag: TEST_PROP_Z reads a packed_bool conversion
// flag with success "1", split by arm. Oracle: the pooled two-proportion
// z and its two-sided normal p (math.Erfc).
func TestShowcase_ABPropZOnFlag(t *testing.T) {
	p, rows := showcasePulse(t, nil)
	_, resp := showcaseRun(t, p, "tests/35_prop_z_on_flag.json")

	n := map[string]float64{}
	x := map[string]float64{}
	for _, r := range rows {
		n[r.arm]++
		if r.converted {
			x[r.arm]++
		}
	}
	p1, p2 := x["control"]/n["control"], x["variant"]/n["variant"]
	pool := (x["control"] + x["variant"]) / (n["control"] + n["variant"])
	z := (p1 - p2) / math.Sqrt(pool*(1-pool)*(1/n["control"]+1/n["variant"]))
	pv := math.Erfc(math.Abs(z) / math.Sqrt2)
	if x["control"] != 6 || x["variant"] != 9 {
		t.Fatalf("fixture drifted: conversions %v", x)
	}

	if len(resp.Tests) != 1 {
		t.Fatalf("tests = %d", len(resp.Tests))
	}
	res := resp.Tests[0]
	var d struct {
		Groups     []string  `json:"groups"`
		N          []float64 `json:"n"`
		Successes  []float64 `json:"successes"`
		Proportion []float64 `json:"proportion"`
		Diff       float64   `json:"diff"`
		Pooled     float64   `json:"pooled"`
	}
	detailsAs(t, res.Details, &d)
	if strings.Join(d.Groups, ",") != "control,variant" {
		t.Fatalf("groups = %v", d.Groups)
	}
	if fmt.Sprint(d.N, d.Successes) != "[24 24] [6 9]" {
		t.Errorf("n, successes = %v %v, want [24 24] [6 9]", d.N, d.Successes)
	}
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"z", res.Statistic, z},
		{"p", res.PValue, pv},
		{"diff", d.Diff, p1 - p2},
		{"pooled", d.Pooled, pool},
	} {
		if !near(c.got, c.want, 1e-12) {
			t.Errorf("%s = %.15g, want %.15g", c.name, c.got, c.want)
		}
	}
}

// TestShowcase_PrivacyChiSqOnFlag: TEST_CHISQ reads a packed_bool churn
// flag directly as its columns. Oracle: Pearson χ² over the hand-counted
// plan × churned table, df 2, p = exp(−χ²/2) (the df-2 survival).
func TestShowcase_PrivacyChiSqOnFlag(t *testing.T) {
	p, rows := showcasePulse(t, nil)
	_, resp := showcaseRun(t, p, "tests/36_chisq_on_flag.json")

	// Labels are sorted byte-wise (categorical and flag alike; U48
	// E1-S2 — first-appearance before); a flag's levels read "0" / "1".
	level := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	var rowLabels, colLabels []string
	ri, ci := map[string]int{}, map[string]int{}
	for _, r := range rows {
		if _, ok := ri[r.plan]; !ok {
			ri[r.plan] = len(rowLabels)
			rowLabels = append(rowLabels, r.plan)
		}
		if _, ok := ci[level(r.churned)]; !ok {
			ci[level(r.churned)] = len(colLabels)
			colLabels = append(colLabels, level(r.churned))
		}
	}
	sort.Strings(rowLabels)
	sort.Strings(colLabels)
	for i, l := range rowLabels {
		ri[l] = i
	}
	for i, l := range colLabels {
		ci[l] = i
	}
	obs := make([][]float64, len(rowLabels))
	for i := range obs {
		obs[i] = make([]float64, len(colLabels))
	}
	for _, r := range rows {
		obs[ri[r.plan]][ci[level(r.churned)]]++
	}
	rowT := make([]float64, len(rowLabels))
	colT := make([]float64, len(colLabels))
	total := 0.0
	for i := range obs {
		for j := range obs[i] {
			rowT[i] += obs[i][j]
			colT[j] += obs[i][j]
			total += obs[i][j]
		}
	}
	chi := 0.0
	for i := range obs {
		for j := range obs[i] {
			e := rowT[i] * colT[j] / total
			chi += (obs[i][j] - e) * (obs[i][j] - e) / e
		}
	}
	if len(rowLabels) != 3 || len(colLabels) != 2 || obs[ri["basic"]][ci["1"]] != 8 ||
		obs[ri["plus"]][ci["1"]] != 5 || obs[ri["pro"]][ci["1"]] != 2 {
		t.Fatalf("fixture drifted: plan × churned %v × %v = %v", rowLabels, colLabels, obs)
	}

	if len(resp.Tests) != 1 {
		t.Fatalf("tests = %d", len(resp.Tests))
	}
	res := resp.Tests[0]
	var d struct {
		RowLabels   []string    `json:"row_labels"`
		ColLabels   []string    `json:"col_labels"`
		Contingency [][]float64 `json:"contingency"`
	}
	detailsAs(t, res.Details, &d)
	if fmt.Sprint(d.RowLabels, d.ColLabels) != fmt.Sprint(rowLabels, colLabels) {
		t.Fatalf("labels = %v × %v, want %v × %v", d.RowLabels, d.ColLabels, rowLabels, colLabels)
	}
	if fmt.Sprint(d.Contingency) != fmt.Sprint(obs) {
		t.Errorf("contingency = %v, want %v", d.Contingency, obs)
	}
	if res.DF != 2 {
		t.Errorf("df = %v, want 2", res.DF)
	}
	if !near(res.Statistic, chi, 1e-12) {
		t.Errorf("chi2 = %.15g, want %.15g", res.Statistic, chi)
	}
	// The χ² survival runs through a math-library tail: compare within
	// tolerance, never bit-exact.
	if !near(res.PValue, math.Exp(-chi/2), 1e-10) {
		t.Errorf("p = %.15g, want %.15g", res.PValue, math.Exp(-chi/2))
	}
}

// TestShowcase_RetentionMonthIndex: calendar-month retention flags from
// month_index differences. Oracle: (year·12 + month − 1) from
// time.Time.Date over the parsed CSV days.
func TestShowcase_RetentionMonthIndex(t *testing.T) {
	p, rows := showcasePulse(t, nil)
	_, resp := showcaseRun(t, p, "attributes/21_retention_month_index.json")

	mi := func(d time.Time) int { return d.Year()*12 + int(d.Month()) - 1 }
	total, m1, q1 := 0, 0, 0
	sameGapDiffers := map[int]map[int]bool{} // day gap → month offsets seen
	for _, r := range rows {
		off := mi(r.repeat) - mi(r.signup)
		total += off
		if off == 1 {
			m1++
		}
		if off <= 3 {
			q1++
		}
		gap := int(r.repeat.Sub(r.signup).Hours() / 24)
		if sameGapDiffers[gap] == nil {
			sameGapDiffers[gap] = map[int]bool{}
		}
		sameGapDiffers[gap][off] = true
	}
	// The fixture must make the calendar matter: one day gap lands in
	// two different month offsets (a day-range rule cannot do this).
	if len(sameGapDiffers[18]) < 2 {
		t.Fatalf("fixture drifted: the 18-day gap maps to month offsets %v only", sameGapDiffers[18])
	}

	got := resp.Data[0]
	for _, c := range []struct {
		key  string
		want int
	}{
		{"subscribers", len(rows)},
		{"repeat_month_total", total},
		{"retained_m1", m1},
		{"retained_q1", q1},
	} {
		if v, ok := got[c.key].(float64); !ok || v != float64(c.want) {
			t.Errorf("%s = %#v, want %d", c.key, got[c.key], c.want)
		}
	}
}

// TestShowcase_DecimalMargin: an exact decimal compare in
// FILTER_EXPRESSION and an exact decimal ratio in ATTR_FORMULA. Oracle:
// integer cents — keep revenue > 2·cost; margin (r − c)/r rounded
// half-to-even at 4 places in integer arithmetic, then summed.
func TestShowcase_DecimalMargin(t *testing.T) {
	p, rows := showcasePulse(t, nil)
	_, resp := showcaseRun(t, p, "attributes/22_decimal_margin.json")

	kept, revenue := 0, int64(0)
	marginSum := 0.0
	for _, r := range rows {
		if r.revenue <= 2*r.cost {
			continue
		}
		kept++
		revenue += r.revenue
		q, rem := (r.revenue-r.cost)*10000/r.revenue, (r.revenue-r.cost)*10000%r.revenue
		switch {
		case 2*rem > r.revenue, 2*rem == r.revenue && q%2 == 1:
			q++
		}
		marginSum += float64(q) / 10000
	}
	if kept == 0 || kept == len(rows) {
		t.Fatalf("fixture drifted: the filter keeps %d of %d", kept, len(rows))
	}

	got := resp.Data[0]
	if v, ok := got["kept"].(float64); !ok || v != float64(kept) {
		t.Errorf("kept = %#v, want %d", got["kept"], kept)
	}
	wantRev := fmt.Sprintf("%d.%02d", revenue/100, revenue%100)
	if fmt.Sprint(got["kept_revenue"]) != wantRev {
		t.Errorf("kept_revenue = %#v, want %s", got["kept_revenue"], wantRev)
	}
	if v, ok := got["margin_total"].(float64); !ok || !near(v, marginSum, 1e-9) {
		t.Errorf("margin_total = %#v, want %.10g", got["margin_total"], marginSum)
	}
}
