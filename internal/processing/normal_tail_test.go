package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Normal-tail p-values (E2-S3, guidance-backfill-inferential).
//
// Every normal-approximation test used to form its two-sided p as
// 2·(1 − Φ(|z|)), which cancels to exactly 0 once |z| exceeds ~8.3
// (Φ(|z|) rounds to 1). Each case below drives one family at |z| ≈ 10
// and holds the p-value to R's 2*pnorm(-|z|) (or pnorm(-z) one-sided)
// at relative 1e-10. Every pre-fix value was 0.
//
// The R literals were computed with R 4.6.1 from the family's own z
// formula (the expression is quoted beside each); the reference
// generator for the shared primitives lives in
// scripts/reference/gen_reference.R.

// normalTailRelTol is the relative tolerance for every case here:
// amd64 FMA vs arm64 differ by a few ulps, and p's relative sensitivity
// to z is ~|z|·δz, so 1e-10 leaves ample room while still failing hard
// on a cancelled 0.
const normalTailRelTol = 1e-10

func assertTinyP(t *testing.T, name string, got, want float64) {
	t.Helper()
	if !(got > 0) {
		t.Fatalf("%s: p = %v, want non-zero %v (complement form cancelled?)", name, got, want)
	}
	if rel := math.Abs(got-want) / want; rel > normalTailRelTol {
		t.Errorf("%s: p = %.17g, want %.17g (R), rel err %.3g > %g", name, got, want, rel, normalTailRelTol)
	}
}

// TestNormalTwoSidedP_MatchesR pins the shared helpers against R's
// 2*pnorm(-|z|) and pnorm(-z) across the support, deep tail included.
func TestNormalTwoSidedP_MatchesR(t *testing.T) {
	cases := []struct {
		z, two, upper float64
	}{
		{0.5, 0.61707507745197387, 0.30853753872598694},
		{1.96, 0.049995790296440856, 0.024997895148220428},
		{5, 5.7330314375838782e-07, 2.8665157187919391e-07},
		{8.5, 1.8959069644406638e-17, 9.4795348222033192e-18},
		{10, 1.5239706048321054e-23, 7.6198530241605269e-24},
		{20, 5.5072482372124675e-89, 2.7536241186062337e-89},
		{37, 1.1451142445049154e-299, 5.7255712225245771e-300},
	}
	for _, c := range cases {
		assertTinyP(t, "normalTwoSidedP(+z)", normalTwoSidedP(c.z), c.two)
		assertTinyP(t, "normalTwoSidedP(-z)", normalTwoSidedP(-c.z), c.two)
		assertTinyP(t, "normalUpperTailP(z)", normalUpperTailP(c.z), c.upper)
	}
	if got := normalTwoSidedP(0); got != 1 {
		t.Errorf("normalTwoSidedP(0) = %v, want 1", got)
	}
	if got := normalTwoSidedP(math.NaN()); !math.IsNaN(got) {
		t.Errorf("normalTwoSidedP(NaN) = %v, want NaN", got)
	}
}

// TestTwoProportionZ_TinyP — OVERLAY_PROP_Z_CELL / _PANEL /
// OVERLAY_PAIRWISE_PROP_Z kernel. 400/500 vs 250/500 ⇒ z ≈ 9.945.
// R: prop.test(c(400,250), c(500,500), correct=FALSE)$p.value.
func TestTwoProportionZ_TinyP(t *testing.T) {
	p, ok := twoProportionZ(400, 500, 250, 500)
	if !ok {
		t.Fatal("twoProportionZ undefined")
	}
	assertTinyP(t, "twoProportionZ", p, 2.6543206118111206e-23)
}

// TestWelchZTest_TinyP — OVERLAY_Z_CELL kernel. means 10 vs 0, var 50,
// n 100 per side ⇒ se = 1, z = 10. R: 2*pnorm(-10).
func TestWelchZTest_TinyP(t *testing.T) {
	p, ok := welchZTest(10, 50, 100, 0, 50, 100)
	if !ok {
		t.Fatal("welchZTest undefined")
	}
	assertTinyP(t, "welchZTest", p, 1.5239706048321054e-23)
}

// TestPropZ_TinyP — TEST_PROP_Z with the same 400/500 vs 250/500 split.
func TestPropZ_TinyP(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "converted", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	spec := &types.Test{
		Type: types.TEST_PROP_Z, Field: "converted", SplitBy: "treatment", Alpha: 0.05,
		Params: json.RawMessage(`{"success": "yes"}`),
	}
	rt, err := newPropZRow(spec, schema)
	if err != nil {
		t.Fatalf("newPropZRow: %v", err)
	}
	feed := func(treatment, converted string, count int) {
		tid := dictIDOrAdd(schema, "treatment", treatment)
		cid := dictIDOrAdd(schema, "converted", converted)
		for range count {
			if err := rt.UpdateRow(NewRecord(schema, map[string]float64{"treatment": float64(tid), "converted": float64(cid)})); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	feed("a", "yes", 400)
	feed("a", "no", 100)
	feed("b", "yes", 250)
	feed("b", "no", 250)
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	assertTinyP(t, "TEST_PROP_Z", res.PValue, 2.6543206118111206e-23)
}

// TestZTwoSample_TinyP — TEST_Z_TWO_SAMPLE. a = 1..50, b = a + 30 ⇒
// z ≈ −10.29. R: z <- (mean(a)-mean(b))/sqrt(var(a)/50+var(b)/50);
// 2*pnorm(-abs(z)).
func TestZTwoSample_TinyP(t *testing.T) {
	schema := twoSampleFixtureSchema()
	rt, err := newZTestRow(&types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
	if err != nil {
		t.Fatalf("newZTestRow: %v", err)
	}
	for i := 1; i <= 50; i++ {
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": float64(i)}, "treatment", "a"))
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": float64(i + 30)}, "treatment", "b"))
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	assertTinyP(t, "TEST_Z_TWO_SAMPLE", res.PValue, 7.8245326377963891e-25)
}

// TestMannWhitney_TinyP — TEST_MANN_WHITNEY_U, fully separated groups
// 1..70 vs 71..140. R: wilcox.test(1:70, 71:140, exact=FALSE,
// correct=TRUE)$p.value.
func TestMannWhitney_TinyP(t *testing.T) {
	schema := twoSampleFixtureSchema()
	rt, err := newMannWhitneyRow(&types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
	if err != nil {
		t.Fatalf("newMannWhitneyRow: %v", err)
	}
	for i := 1; i <= 70; i++ {
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": float64(i)}, "treatment", "a"))
		_ = rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": float64(i + 70)}, "treatment", "b"))
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	assertTinyP(t, "TEST_MANN_WHITNEY_U", res.PValue, 1.8171559696388552e-24)
}

// wilcoxonTinyPWant is 2*pnorm(-z) for 120 all-positive distinct
// differences: z = (n(n+1)/4 − 0.5)/sqrt(n(n+1)(2n+1)/24) ≈ 9.505.
// (R's own wilcox.test(..., paired=TRUE, exact=FALSE) returns 0 here;
// the literal is the formula evaluated in R.)
const wilcoxonTinyPWant = 1.9969198180780431e-21

func wilcoxonTinyPRows() [][2]float64 {
	out := make([][2]float64, 120)
	for i := range out {
		b := float64(i + 1)
		out[i] = [2]float64{b, 2 * b} // before, after: diff = i+1
	}
	return out
}

// TestWilcoxonSR_TinyP — TEST_WILCOXON_SR, row tier.
func TestWilcoxonSR_TinyP(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "before", Type: encoding.FieldTypeF64},
		{Name: "after", Type: encoding.FieldTypeF64},
	}}
	rt, err := newWilcoxonSRRow(&types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before", Alpha: 0.05}, schema)
	if err != nil {
		t.Fatalf("newWilcoxonSRRow: %v", err)
	}
	for _, p := range wilcoxonTinyPRows() {
		_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"before": p[0], "after": p[1]}))
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	assertTinyP(t, "TEST_WILCOXON_SR row", res.PValue, wilcoxonTinyPWant)
}

// TestWilcoxonSRPost_TinyP — TEST_WILCOXON_SR, post tier.
func TestWilcoxonSRPost_TinyP(t *testing.T) {
	var rows []map[string]any
	for _, p := range wilcoxonTinyPRows() {
		rows = append(rows, map[string]any{"before": p[0], "after": p[1]})
	}
	res := runRegisteredPost(t, &types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before"}, rows)
	assertTinyP(t, "TEST_WILCOXON_SR post", res.PValue, wilcoxonTinyPWant)
}

// kendallTinyPWant: x = 1..45, y = x² (perfect concordance, no ties) ⇒
// z = (S − 1)/sqrt(n(n−1)(2n+5)/18) ≈ 9.675. R: cor.test(1:45,
// (1:45)^2, method="kendall", exact=FALSE, continuity=TRUE)$p.value.
// TEST_TREND's Mann-Kendall S on the same series is the identical
// statistic, so it shares the literal.
const kendallTinyPWant = 3.861689407088341e-22

// TestKendallTau_TinyP — TEST_KENDALL_TAU, row tier.
func TestKendallTau_TinyP(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	rt, err := newKendallTauRow(&types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y", Alpha: 0.05}, schema)
	if err != nil {
		t.Fatalf("newKendallTauRow: %v", err)
	}
	for i := 1; i <= 45; i++ {
		x := float64(i)
		_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"x": x, "y": x * x}))
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	assertTinyP(t, "TEST_KENDALL_TAU row", res.PValue, kendallTinyPWant)
}

// TestKendallTauPost_TinyP — TEST_KENDALL_TAU, post tier.
func TestKendallTauPost_TinyP(t *testing.T) {
	var rows []map[string]any
	for i := 1; i <= 45; i++ {
		x := float64(i)
		rows = append(rows, map[string]any{"x": x, "y": x * x})
	}
	res := runRegisteredPost(t, &types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y"}, rows)
	assertTinyP(t, "TEST_KENDALL_TAU post", res.PValue, kendallTinyPWant)
}

// TestTrend_TinyP — TEST_TREND (Mann-Kendall) on a strictly rising
// series of 45 periods.
func TestTrend_TinyP(t *testing.T) {
	rows := make([]map[string]any, 45)
	for i := range rows {
		x := float64(i + 1)
		rows[i] = map[string]any{"period": x, "value": x * x}
	}
	res := runRegisteredPost(t, &types.Test{Type: types.TEST_TREND, Field: "value", OrderBy: []types.OrderKey{{Field: "period"}}}, rows)
	assertTinyP(t, "TEST_TREND", res.PValue, kendallTinyPWant)
}

// TestShapiroFrancia_TinyP — TEST_SHAPIRO_WILK's one-sided upper tail
// on a strongly skewed sample x_i = exp(i/50), i = 1..1000 ⇒ z ≈ 13.8.
// R replicates the Royston transform: m <- qnorm(((1:n)-0.375)/(n+0.25));
// W <- sum(m*x)^2/(sum(m^2)*sum((x-mean(x))^2)); then pnorm(-z).
//
// The tolerance here is looser than normalTailRelTol: the Blom scores
// come from inverseNormalCDF (Beasley-Springer-Moro, ~1e-9), so W — and
// through it z — carries that error, amplified by |z| in log p. The
// assertion that matters is that p is non-zero (it was 0) and within
// that propagated error of R.
func TestShapiroFrancia_TinyP(t *testing.T) {
	x := make([]float64, 1000)
	for i := range x {
		x[i] = math.Exp(float64(i+1) / 50)
	}
	_, z, p, _ := shapiroFranciaStat(x)
	if !(p > 0) {
		t.Fatalf("p = %v, want non-zero (z = %v)", p, z)
	}
	if p != normalUpperTailP(z) {
		t.Errorf("p = %v, want normalUpperTailP(z) = %v", p, normalUpperTailP(z))
	}
	const want = 9.9516036087274291e-44
	if rel := math.Abs(p-want) / want; rel > 1e-6 {
		t.Errorf("p = %.17g, want %.17g (R), rel err %.3g", p, want, rel)
	}
}
