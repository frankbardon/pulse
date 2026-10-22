//go:build ignore

// Generator for the shared CSV fixtures referenced by every category
// under internal/examples/. Run from the repo root:
//
//	go run internal/examples/fixtures/gen.go
//
// Output is written into internal/examples/fixtures/. The same seed produces
// byte-identical output so the CSVs are checked in.
//
// Not generated here: all_types.csv. That fixture is hand-curated
// because it exists specifically to exercise every one of the 17
// supported field types end to end (decimal128 with per-field
// precision/scale, etc.). Random generation does not buy us anything
// when the goal is type coverage on a 5-row file.
package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

const (
	seed       = 20260501
	outputDir  = "internal/examples/fixtures"
	dateFormat = "2006-01-02"
)

var (
	regions = []string{"north", "south", "east", "west"}
	cities  = []string{
		"Austin", "Boston", "Chicago", "Denver", "El Paso",
		"Fresno", "Gainesville", "Houston", "Indianapolis", "Jacksonville",
		"Kansas City", "Lincoln", "Memphis", "Nashville", "Omaha",
	}
	occupations = []string{
		"engineer", "teacher", "nurse", "driver", "analyst",
		"artist", "manager", "writer",
	}
	categories  = []string{"A", "B", "C", "D", "E"}
	treatments  = []string{"control", "variant"}
	segments    = []string{"segment_a", "segment_b", "segment_c"}
	conversions = []string{"yes", "no"}
)

func main() {
	r := rand.New(rand.NewSource(seed))
	must(writeTransactions(r, 200))
	must(writeCustomers(r, 200))
	must(writeOrders(r, 200))
	must(writeTrainingData(r, 300))
	must(writeExperiment(r, 400))
	must(writeRepeatedMeasures(r, 40))
	// The survey battery draws from its own source so adding it left
	// every CSV above byte-identical.
	must(writeSurvey(rand.New(rand.NewSource(seed+1)), 300))
	// The subscriber flags draw nothing from r: every value is a
	// function of the row index, so the showcase tests can derive their
	// expected numbers by hand.
	must(writeSubscribers(48))
	// The one-trait battery draws from its own source too, so adding it
	// left every CSV above byte-identical.
	must(writeBattery(rand.New(rand.NewSource(seed+2)), 300))
	fmt.Println("wrote 9 CSVs to", outputDir)
}

// writeExperiment produces an A/B testing cohort designed so every
// statistical test type in internal/examples/tests/ has a clean, non-trivial
// signal:
//
//   - treatment vs control: variant has ≈22% higher revenue mean and
//     a right-tail-heavy lift on session_minutes (median moves little,
//     upper deciles stretch). The first yields a clear two-sample
//     t-test reject; the second yields a clear KS reject while the
//     mean t-test stays ambiguous — a useful split that demonstrates
//     why distribution-shape tests still matter alongside mean tests.
//   - region: four regions with planted mean-revenue differences so
//     ANOVA across regions rejects clearly.
//   - segment × converted: segments_a / b / c have different conversion
//     base rates (0.20 / 0.35 / 0.50) producing a dependent contingency.
//   - period: 90 sequential dates; revenue carries a mild upward drift
//     so Mann-Kendall on the time-ordered series rejects.
//   - session_minutes vs revenue follow different shapes: revenue is
//     log-normal, session_minutes is half-normal, so a KS two-sample
//     comparing them across treatments shows distribution structure.
func writeExperiment(r *rand.Rand, n int) error {
	w, close := openCSV("experiment.csv")
	defer close()
	if err := w.Write([]string{
		"id", "treatment", "region", "segment", "converted",
		"revenue_before", "revenue", "session_minutes", "period",
	}); err != nil {
		return err
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Planted per-region revenue lifts (multiplicative on the base
	// log-normal). Means stay ordered north < south < east < west.
	regionLift := map[string]float64{
		"north": 1.00,
		"south": 1.10,
		"east":  1.22,
		"west":  1.35,
	}
	segmentConvRate := map[string]float64{
		"segment_a": 0.20,
		"segment_b": 0.35,
		"segment_c": 0.50,
	}
	for i := range n {
		treatment := treatments[r.Intn(len(treatments))]
		region := regions[r.Intn(len(regions))]
		segment := segments[r.Intn(len(segments))]
		// Conversion depends on segment AND treatment so chi-square on
		// (region, converted) is independent but (segment, converted)
		// is dependent.
		convRate := segmentConvRate[segment]
		if treatment == "variant" {
			convRate += 0.05
		}
		converted := "no"
		if r.Float64() < convRate {
			converted = "yes"
		}
		// revenue_before is the pre-treatment baseline drawn from the
		// same log-normal as revenue but without the treatment lift
		// and without the time drift. Paired t-test and Wilcoxon
		// signed-rank examples test the per-unit delta.
		base := math.Exp(4.5 + 0.5*r.NormFloat64())
		treatmentLift := 1.0
		if treatment == "variant" {
			treatmentLift = 1.22
		}
		periodIdx := r.Intn(90)
		drift := 1.0 + 0.003*float64(periodIdx)
		revenueBefore := base * regionLift[region]
		revenue := revenueBefore * treatmentLift * drift
		// session_minutes: half-normal with treatment lift.
		sessionBase := math.Abs(r.NormFloat64()) * 15.0
		// Asymmetric treatment effect on session_minutes: variant shifts
		// the right tail more than the median, so KS detects a clear
		// distribution change even though mean differences stay modest.
		sessionTreatLift := 1.0
		if treatment == "variant" {
			sessionTreatLift = 1.0 + 0.35*math.Abs(r.NormFloat64())
		}
		sessionMinutes := sessionBase * sessionTreatLift
		period := start.AddDate(0, 0, periodIdx).Format(dateFormat)
		if err := w.Write([]string{
			itoa(i + 1),
			treatment,
			region,
			segment,
			converted,
			fmt.Sprintf("%.2f", revenueBefore),
			fmt.Sprintf("%.2f", revenue),
			fmt.Sprintf("%.2f", sessionMinutes),
			period,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeTransactions produces id (u32) and amount (f64). Amounts are
// log-normally distributed so FEAT_LOG visibly compresses the tail.
func writeTransactions(r *rand.Rand, n int) error {
	w, close := openCSV("transactions.csv")
	defer close()
	if err := w.Write([]string{"id", "amount"}); err != nil {
		return err
	}
	for i := range n {
		// log-normal: exp(N(mu, sigma)) with mu=4, sigma=1.2
		amt := math.Exp(4.0 + 1.2*r.NormFloat64())
		if err := w.Write([]string{
			itoa(i + 1),
			fmt.Sprintf("%.2f", amt),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeCustomers covers id, age, income, region, city. Income is again
// log-normal; region is uniform over 4 values; city is sampled from a
// long tail to exercise FEAT_FREQUENCY_ENCODE.
func writeCustomers(r *rand.Rand, n int) error {
	w, close := openCSV("customers.csv")
	defer close()
	if err := w.Write([]string{"id", "age", "income", "region", "city"}); err != nil {
		return err
	}
	for i := range n {
		age := 18 + r.Intn(60)
		income := math.Exp(10.5 + 0.6*r.NormFloat64())
		region := regions[r.Intn(len(regions))]
		// Skew city: weight toward the first few entries.
		cityIdx := int(math.Abs(r.NormFloat64()) * float64(len(cities)) / 3)
		if cityIdx >= len(cities) {
			cityIdx = len(cities) - 1
		}
		city := cities[cityIdx]
		if err := w.Write([]string{
			itoa(i + 1),
			itoa(age),
			fmt.Sprintf("%.2f", income),
			region,
			city,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeOrders produces id, order_date, revenue. Dates span a full year
// so FEAT_DATE_FEATURES yields all four quarters and seven dow values.
func writeOrders(r *rand.Rand, n int) error {
	w, close := openCSV("orders.csv")
	defer close()
	if err := w.Write([]string{"id", "order_date", "revenue", "ordered_at"}); err != nil {
		return err
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		offset := r.Intn(365)
		day := start.AddDate(0, 0, offset)
		date := day.Format(dateFormat)
		revenue := math.Exp(6.0 + 0.8*r.NormFloat64())
		// ordered_at: the order's instant (a `datetime`) on order_date,
		// its UTC time of day derived from the row index so the column
		// draws nothing from r and every other fixture stays
		// byte-identical.
		at := day.Add(time.Duration((i*7)%24)*time.Hour + time.Duration((i*13)%60)*time.Minute)
		if err := w.Write([]string{
			itoa(i + 1),
			date,
			fmt.Sprintf("%.2f", revenue),
			at.Format(time.RFC3339),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeTrainingData covers every field referenced by examples 07-10.
// label is binary (0/1) with class imbalance ~30% positives so
// stratified split has a meaningful effect.
func writeTrainingData(r *rand.Rand, n int) error {
	w, close := openCSV("training_data.csv")
	defer close()
	if err := w.Write([]string{
		"id", "region", "occupation", "label", "category", "price", "income", "signup_date",
	}); err != nil {
		return err
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		region := regions[r.Intn(len(regions))]
		occupation := occupations[r.Intn(len(occupations))]
		category := categories[r.Intn(len(categories))]
		// Bias label toward category A and occupation 'analyst' to make
		// target-encoding interesting.
		labelProb := 0.20
		if category == "A" {
			labelProb += 0.25
		}
		if occupation == "analyst" {
			labelProb += 0.15
		}
		label := 0
		if r.Float64() < labelProb {
			label = 1
		}
		price := math.Exp(3.5 + 0.7*r.NormFloat64())
		income := math.Exp(10.5 + 0.5*r.NormFloat64())
		offset := r.Intn(500)
		signup := start.AddDate(0, 0, offset).Format(dateFormat)
		if err := w.Write([]string{
			itoa(i + 1),
			region,
			occupation,
			itoa(label),
			category,
			fmt.Sprintf("%.2f", price),
			fmt.Sprintf("%.2f", income),
			signup,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeRepeatedMeasures produces a small repeated-measures cohort designed
// for TEST_ANOVA_RM. Each subject contributes one observation in each of
// three conditions (baseline, treatment_a, treatment_b). The wide
// table reshapes to (n subjects) × 3, balanced. Per-subject random
// effect dominates; treatment_a adds a moderate lift, treatment_b a
// larger one — so RM-ANOVA rejects clearly.
//
// Fields:
//
//	subject_id    — categorical, n distinct values
//	condition     — categorical, {baseline, treatment_a, treatment_b}
//	metric        — f64
func writeRepeatedMeasures(r *rand.Rand, nSubjects int) error {
	w, close := openCSV("repeated_measures.csv")
	defer close()
	if err := w.Write([]string{"subject_id", "condition", "metric"}); err != nil {
		return err
	}
	conditions := []struct {
		name string
		lift float64
	}{
		{"baseline", 0.0},
		{"treatment_a", 3.0},
		{"treatment_b", 6.0},
	}
	for s := range nSubjects {
		// Subject-level baseline ability ~ N(20, 4). Carries through every
		// condition so within-subject correlation is large.
		baseline := 20.0 + 4.0*r.NormFloat64()
		subjID := fmt.Sprintf("subj_%03d", s+1)
		for _, c := range conditions {
			// Per-cell noise small relative to subject baseline.
			metric := baseline + c.lift + 0.8*r.NormFloat64()
			if err := w.Write([]string{
				subjID,
				c.name,
				fmt.Sprintf("%.3f", metric),
			}); err != nil {
				return err
			}
		}
	}
	w.Flush()
	return w.Error()
}

// writeSurvey produces a six-item rating battery with a planted
// two-factor structure, for factor-analysis and rotation examples
// (MAT_FACTOR / MAT_PCA rotations, vector fits): q1-q3 load on a
// "service" factor, q4-q6 on a "value" factor, the factors correlate
// 0.35, and q3 / q6 cross-load modestly so orthogonal and oblique
// rotations visibly differ. panel splits respondents into two waves
// (a / b) for fit-on-one, score-the-other examples.
//
// Fields:
//
//	id      — u32, 1..n
//	panel   — categorical, {a, b}
//	q1..q6  — f64 ratings centred on 4 (two decimals)
func writeSurvey(r *rand.Rand, n int) error {
	w, close := openCSV("survey.csv")
	defer close()
	if err := w.Write([]string{"id", "panel", "q1", "q2", "q3", "q4", "q5", "q6"}); err != nil {
		return err
	}
	// Loadings on (service, value); unique variance tops each item up
	// to unit variance before scaling.
	loadings := [6][2]float64{
		{0.80, 0.00}, {0.75, 0.00}, {0.60, 0.30},
		{0.00, 0.80}, {0.00, 0.70}, {0.25, 0.55},
	}
	const phi = 0.35
	for i := range n {
		z1, z2 := r.NormFloat64(), r.NormFloat64()
		service := z1
		value := phi*z1 + math.Sqrt(1-phi*phi)*z2
		panel := "a"
		if i%2 == 1 {
			panel = "b"
		}
		row := []string{itoa(i + 1), panel}
		for _, l := range loadings {
			common := float64(l[0]*service) + float64(l[1]*value)
			communality := float64(l[0]*l[0]) + float64(l[1]*l[1]) + float64(2*l[0]*l[1]*phi)
			item := common + float64(math.Sqrt(1-communality)*r.NormFloat64())
			row = append(row, fmt.Sprintf("%.2f", 4+float64(1.2*item)))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeBattery produces a six-item Likert battery measuring ONE latent
// trait (job engagement), so reliability and principal-component
// examples have a well-defined answer: a one-factor fit with no
// Heywood case (omega defined) and one dominant component. Each item
// is a 1-5 rating cut from loading*trait + unique noise at fixed
// thresholds. bat_dread is reverse-worded: it loads NEGATIVELY on the
// trait, so a reliability request flips it with params.reverse
// (scale 1..5) before the fold.
//
// Fields:
//
//	id                         — u32, 1..n
//	bat_energy .. bat_absorbed — u8 ratings 1..5, positively keyed
//	bat_dread                  — u8 rating 1..5, reverse-keyed
func writeBattery(r *rand.Rand, n int) error {
	w, close := openCSV("battery.csv")
	defer close()
	header := []string{"id", "bat_energy", "bat_proud", "bat_absorbed", "bat_recommend", "bat_time_flies", "bat_dread"}
	if err := w.Write(header); err != nil {
		return err
	}
	// Loadings on the single trait; the last item is reverse-keyed.
	loadings := [6]float64{0.80, 0.75, 0.70, 0.65, 0.60, -0.70}
	// Cut points on the unit-variance item scale: 1 | 2 | 3 | 4 | 5.
	cuts := [4]float64{-1.5, -0.5, 0.5, 1.5}
	for i := range n {
		trait := r.NormFloat64()
		row := []string{itoa(i + 1)}
		for _, l := range loadings {
			item := float64(l*trait) + float64(math.Sqrt(1-l*l)*r.NormFloat64())
			rating := 1
			for _, c := range cuts {
				if item > c {
					rating++
				}
			}
			row = append(row, itoa(rating))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// writeSubscribers produces the flag-and-money cohort the U45 showcase
// examples read: binary outcomes stored as packed_bool (no 0/1 twin
// column), a signup and a first-repeat-order date for calendar
// retention flags, and decimal128 money for exact ratios. Every value
// is a closed-form function of the row index i:
//
//   - arm alternates control / variant; within an arm (a = i/2)
//     control converts when a%4 == 0 (6 of 24) and variant when
//     a%8 < 3 (9 of 24).
//   - plan cycles basic / plus / pro; within a plan (k = i/3) the
//     first 8 / 5 / 2 of 16 rows churned.
//   - late_payment is 1 when i%4 >= 2; within a late_payment group
//     (g = (i/4)*2 + i%2) a late payer defaulted when g%3 == 0 (8 of
//     24), an on-time one when g%6 == 0 (4 of 24).
//   - signup_date steps 13 days from 2024-01-03; first_repeat_order
//     follows it by 4, 18, 29, 41, 58 or 90 days (i%6), so the same
//     day gap lands in different calendar months.
//   - revenue / cost are cents-exact money.
func writeSubscribers(n int) error {
	w, close := openCSV("subscribers.csv")
	defer close()
	if err := w.Write([]string{"id", "arm", "converted", "plan", "churned", "late_payment", "defaulted", "signup_date", "first_repeat_order", "revenue", "cost"}); err != nil {
		return err
	}
	plans := []string{"basic", "plus", "pro"}
	churnedPerPlan := []int{8, 5, 2}
	gaps := []int{4, 18, 29, 41, 58, 90}
	start := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)
	flag := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	cents := func(c int) string { return fmt.Sprintf("%d.%02d", c/100, c%100) }
	for i := range n {
		a := i / 2
		converted := a%4 == 0
		if i%2 == 1 {
			converted = a%8 < 3
		}
		late := i%4 >= 2
		g := (i/4)*2 + i%2
		defaulted := g%6 == 0
		if late {
			defaulted = g%3 == 0
		}
		lateCol := "0"
		if late {
			lateCol = "1"
		}
		signup := start.AddDate(0, 0, 13*i)
		if err := w.Write([]string{
			itoa(i + 1),
			treatments[i%2],
			flag(converted),
			plans[i%3],
			flag(i/3 < churnedPerPlan[i%3]),
			lateCol,
			flag(defaulted),
			signup.Format(dateFormat),
			signup.AddDate(0, 0, gaps[i%6]).Format(dateFormat),
			cents(2000 + (i*937)%7000),
			cents(700 + (i*613)%2900),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func openCSV(name string) (*csv.Writer, func()) {
	path := filepath.Join(outputDir, name)
	f, err := os.Create(path)
	must(err)
	w := csv.NewWriter(f)
	return w, func() {
		w.Flush()
		f.Close()
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
