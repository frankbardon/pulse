// Package fixtures generates the shared CSV fixtures every category
// under internal/examples/ reads. Regenerate them from the repo root:
//
//	go run ./internal/examples/fixtures/cmd/genfixtures
//
// The CSVs are committed, and TestFixtures_RegenerateByteIdentical
// regenerates every one in memory and byte-compares it with the
// committed file, so a fixture is pinned by its generator. The
// generator is portable by construction: math/rand/v2 PCG streams,
// normal variates from detmath.NormFloat64, exponentials from
// detmath.Exp, and every multiply-add written with a float64(...)
// barrier so it cannot fuse. The same bytes come out on every
// architecture, which CI's amd64 run of the pin test proves.
//
// Not generated here: all_types.csv and the hand-built cohorts
// (card_issuers, tier_migrations, plan_history, plan_history_wide,
// warehouse_orders). all_types exists to exercise every supported
// field type end to end (decimal128 with per-field precision/scale,
// etc.); random generation buys nothing on a 5-row type-coverage file.
package fixtures

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/frankbardon/pulse/internal/brandfixture"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/linalg/detmath"
)

const (
	seed       = 20260501
	dateFormat = "2006-01-02"

	// BrandSpecPath is the module-relative path of the SEM brand spec
	// whose rows brand_survey.csv carries; Generate takes its bytes.
	BrandSpecPath = "internal/sem/testdata/brand_spec.json"
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
	categories = []string{"A", "B", "C", "D", "E"}
	treatments = []string{"control", "variant"}
	segments   = []string{"segment_a", "segment_b", "segment_c"}
)

// File is one generated fixture: its file name under
// internal/examples/fixtures/ and its exact CSV bytes.
type File struct {
	Name string
	Data []byte
}

// newStream returns the seeded PCG source of one fixture family. Each
// family draws from its own stream, so adding a family never moves
// the bytes of another.
func newStream(stream uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, stream))
}

// Generate returns every generated fixture CSV, in a fixed order.
// brandSpec is the content of BrandSpecPath. The result is a pure
// function of the code and brandSpec: identical on every architecture.
func Generate(brandSpec []byte) ([]File, error) {
	g := &generator{}
	r := newStream(0)
	g.add("transactions.csv", func(w *csv.Writer) error { return writeTransactions(w, r, 200) })
	g.add("customers.csv", func(w *csv.Writer) error { return writeCustomers(w, r, 200) })
	g.add("orders.csv", func(w *csv.Writer) error { return writeOrders(w, r, 200) })
	g.add("training_data.csv", func(w *csv.Writer) error { return writeTrainingData(w, r, 300) })
	g.add("experiment.csv", func(w *csv.Writer) error { return writeExperiment(w, r, 400) })
	g.add("repeated_measures.csv", func(w *csv.Writer) error { return writeRepeatedMeasures(w, r, 40) })
	g.add("survey.csv", func(w *csv.Writer) error { return writeSurvey(w, newStream(1), 300) })
	// The subscriber flags draw nothing: every value is a function of
	// the row index, so the showcase tests can derive their expected
	// numbers by hand.
	g.add("subscribers.csv", func(w *csv.Writer) error { return writeSubscribers(w, 48) })
	g.add("battery.csv", func(w *csv.Writer) error { return writeBattery(w, newStream(2), 300) })
	g.add("intake.csv", func(w *csv.Writer) error { return writeIntake(w, newStream(3), 300) })
	g.add("brand_ratings.csv", func(w *csv.Writer) error { return writeBrandRatings(w, newStream(4), 600) })
	// The weekly media-mix cohort draws nothing from a source: it is the
	// mmmfixture rows verbatim, so the decay-sweep example and the
	// acceptance suites read the same data.
	g.add("mmm_weekly.csv", writeMMMWeekly)
	// The brand survey draws nothing from a source either: it is the
	// lavaan oracles' rows (internal/brandfixture over brand_spec.json
	// and its seed), so the SEM examples read the data the oracles fit.
	g.add("brand_survey.csv", func(w *csv.Writer) error { return writeBrandSurvey(w, brandSpec) })
	if g.err != nil {
		return nil, g.err
	}
	return g.files, nil
}

// generator runs the writers in order, keeping the first error.
type generator struct {
	files []File
	err   error
}

func (g *generator) add(name string, write func(*csv.Writer) error) {
	if g.err != nil {
		return
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := write(w); err != nil {
		g.err = fmt.Errorf("%s: %w", name, err)
		return
	}
	w.Flush()
	if err := w.Error(); err != nil {
		g.err = fmt.Errorf("%s: %w", name, err)
		return
	}
	g.files = append(g.files, File{Name: name, Data: buf.Bytes()})
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
func writeExperiment(w *csv.Writer, r *rand.Rand, n int) error {
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
		treatment := treatments[r.IntN(len(treatments))]
		region := regions[r.IntN(len(regions))]
		segment := segments[r.IntN(len(segments))]
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
		base := detmath.Exp(4.5 + float64(0.5*detmath.NormFloat64(r)))
		treatmentLift := 1.0
		if treatment == "variant" {
			treatmentLift = 1.22
		}
		periodIdx := r.IntN(90)
		drift := 1.0 + float64(0.003*float64(periodIdx))
		revenueBefore := base * regionLift[region]
		revenue := revenueBefore * treatmentLift * drift
		// session_minutes: half-normal with treatment lift.
		sessionBase := math.Abs(detmath.NormFloat64(r)) * 15.0
		// Asymmetric treatment effect on session_minutes: variant shifts
		// the right tail more than the median, so KS detects a clear
		// distribution change even though mean differences stay modest.
		sessionTreatLift := 1.0
		if treatment == "variant" {
			sessionTreatLift = 1.0 + float64(0.35*math.Abs(detmath.NormFloat64(r)))
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
	return nil
}

// writeTransactions produces id (u32) and amount (f64). Amounts are
// log-normally distributed so FEAT_LOG visibly compresses the tail.
func writeTransactions(w *csv.Writer, r *rand.Rand, n int) error {
	if err := w.Write([]string{"id", "amount"}); err != nil {
		return err
	}
	for i := range n {
		// log-normal: exp(N(mu, sigma)) with mu=4, sigma=1.2
		amt := detmath.Exp(4.0 + float64(1.2*detmath.NormFloat64(r)))
		if err := w.Write([]string{
			itoa(i + 1),
			fmt.Sprintf("%.2f", amt),
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeCustomers covers id, age, income, region, city. Income is again
// log-normal; region is uniform over 4 values; city is sampled from a
// long tail to exercise FEAT_FREQUENCY_ENCODE.
func writeCustomers(w *csv.Writer, r *rand.Rand, n int) error {
	if err := w.Write([]string{"id", "age", "income", "region", "city"}); err != nil {
		return err
	}
	for i := range n {
		age := 18 + r.IntN(60)
		income := detmath.Exp(10.5 + float64(0.6*detmath.NormFloat64(r)))
		region := regions[r.IntN(len(regions))]
		// Skew city: weight toward the first few entries.
		cityIdx := int(math.Abs(detmath.NormFloat64(r)) * float64(len(cities)) / 3)
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
	return nil
}

// writeOrders produces id, order_date, revenue. Dates span a full year
// so FEAT_DATE_FEATURES yields all four quarters and seven dow values.
func writeOrders(w *csv.Writer, r *rand.Rand, n int) error {
	if err := w.Write([]string{"id", "order_date", "revenue", "ordered_at"}); err != nil {
		return err
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		offset := r.IntN(365)
		day := start.AddDate(0, 0, offset)
		date := day.Format(dateFormat)
		revenue := detmath.Exp(6.0 + float64(0.8*detmath.NormFloat64(r)))
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
	return nil
}

// writeTrainingData covers every field referenced by examples 07-10.
// label is binary (0/1) with class imbalance ~30% positives so
// stratified split has a meaningful effect.
func writeTrainingData(w *csv.Writer, r *rand.Rand, n int) error {
	if err := w.Write([]string{
		"id", "region", "occupation", "label", "category", "price", "income", "signup_date",
	}); err != nil {
		return err
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		region := regions[r.IntN(len(regions))]
		occupation := occupations[r.IntN(len(occupations))]
		category := categories[r.IntN(len(categories))]
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
		price := detmath.Exp(3.5 + float64(0.7*detmath.NormFloat64(r)))
		income := detmath.Exp(10.5 + float64(0.5*detmath.NormFloat64(r)))
		offset := r.IntN(500)
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
	return nil
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
func writeRepeatedMeasures(w *csv.Writer, r *rand.Rand, nSubjects int) error {
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
		baseline := 20.0 + float64(4.0*detmath.NormFloat64(r))
		subjID := fmt.Sprintf("subj_%03d", s+1)
		for _, c := range conditions {
			// Per-cell noise small relative to subject baseline.
			metric := baseline + c.lift + float64(0.8*detmath.NormFloat64(r))
			if err := w.Write([]string{
				subjID,
				c.name,
				fmt.Sprintf("%.3f", metric),
			}); err != nil {
				return err
			}
		}
	}
	return nil
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
func writeSurvey(w *csv.Writer, r *rand.Rand, n int) error {
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
		z1, z2 := detmath.NormFloat64(r), detmath.NormFloat64(r)
		service := z1
		value := float64(phi*z1) + float64(math.Sqrt(1-float64(phi*phi))*z2)
		panel := "a"
		if i%2 == 1 {
			panel = "b"
		}
		row := []string{itoa(i + 1), panel}
		for _, l := range loadings {
			common := float64(l[0]*service) + float64(l[1]*value)
			communality := float64(l[0]*l[0]) + float64(l[1]*l[1]) + float64(2*l[0]*l[1]*phi)
			item := common + float64(math.Sqrt(1-communality)*detmath.NormFloat64(r))
			row = append(row, fmt.Sprintf("%.2f", 4+float64(1.2*item)))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
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
func writeBattery(w *csv.Writer, r *rand.Rand, n int) error {
	header := []string{"id", "bat_energy", "bat_proud", "bat_absorbed", "bat_recommend", "bat_time_flies", "bat_dread"}
	if err := w.Write(header); err != nil {
		return err
	}
	// Loadings on the single trait; the last item is reverse-keyed.
	loadings := [6]float64{0.80, 0.75, 0.70, 0.65, 0.60, -0.70}
	// Cut points on the unit-variance item scale: 1 | 2 | 3 | 4 | 5.
	cuts := [4]float64{-1.5, -0.5, 0.5, 1.5}
	for i := range n {
		trait := detmath.NormFloat64(r)
		row := []string{itoa(i + 1)}
		for _, l := range loadings {
			item := float64(l*trait) + float64(math.Sqrt(1-float64(l*l))*detmath.NormFloat64(r))
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
	return nil
}

// writeBrandRatings produces the perceptual-map cohort: n respondents,
// each rating one of five brands (cycling by row, n/5 per brand) on six
// 1-7 items. Two latent axes are planted: premium (quality and premium
// load positively, value negatively) and modern (modern and innovative
// positively, heritage negatively). Each brand sits at a fixed
// (premium, modern) position — brand_a premium-modern, brand_b
// premium-traditional, brand_c budget-modern, brand_d
// budget-traditional, brand_e in the middle — and every respondent's
// latent pair is that position plus unit noise, so the first two
// principal components of the ratings recover the two axes and the
// brand centroids on them form the corners of a square around brand_e.
//
// Fields:
//
//	id                         — u32, 1..n
//	brand                      — categorical, brand_a .. brand_e
//	quality .. heritage        — u8 ratings 1..7
func writeBrandRatings(w *csv.Writer, r *rand.Rand, n int) error {
	header := []string{"id", "brand", "quality", "premium", "value", "modern", "innovative", "heritage"}
	if err := w.Write(header); err != nil {
		return err
	}
	brands := [5]string{"brand_a", "brand_b", "brand_c", "brand_d", "brand_e"}
	// (premium, modern) position of each brand, in latent SD units. The
	// premium spread is twice the modern one, so the two components
	// have well-separated eigenvalues and come out as the two planted
	// axes rather than a rotation of them.
	positions := [5][2]float64{{1.4, 0.7}, {1.4, -0.7}, {-1.4, 0.7}, {-1.4, -0.7}, {0, 0}}
	// Loadings on (premium, modern).
	loadings := [6][2]float64{
		{0.80, 0.00}, {0.85, 0.00}, {-0.60, 0.00},
		{0.00, 0.65}, {0.00, 0.60}, {0.00, -0.55},
	}
	for i := range n {
		b := i % len(brands)
		premium := positions[b][0] + float64(0.7*detmath.NormFloat64(r))
		modern := positions[b][1] + float64(0.7*detmath.NormFloat64(r))
		row := []string{itoa(i + 1), brands[b]}
		for _, l := range loadings {
			item := float64(l[0]*premium) + float64(l[1]*modern) + float64(0.6*detmath.NormFloat64(r))
			rating := math.Round(4 + float64(1.1*item))
			rating = math.Max(1, math.Min(7, rating))
			row = append(row, itoa(int(rating)))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// writeIntake produces the missing-data cohort the data_quality
// examples read: 300 sign-up intake forms, 100 from each channel
// (web, phone, paper), with nulls planted in two nullable fields. An
// empty CSV cell in a nullable numeric column imports as a null (the
// per-record null bitmap), never as zero.
//
// Fields:
//
//	id      — u32, 1..n
//	channel — categorical: web / phone / paper, cycling by row
//	age     — u8, nullable; 18..80, left blank on about 3% of forms in
//	          every channel (missing at random)
//	income  — f64, nullable; log-normal yearly income, left blank on
//	          about 5% of web, 15% of phone and 40% of paper forms, so
//	          the missing rate depends on the channel
func writeIntake(w *csv.Writer, r *rand.Rand, n int) error {
	if err := w.Write([]string{"id", "channel", "age", "income"}); err != nil {
		return err
	}
	channels := []string{"web", "phone", "paper"}
	incomeGap := map[string]float64{"web": 0.05, "phone": 0.15, "paper": 0.40}
	for i := range n {
		channel := channels[i%len(channels)]
		age := itoa(18 + r.IntN(63))
		if r.Float64() < 0.03 {
			age = ""
		}
		income := fmt.Sprintf("%.2f", detmath.Exp(10.8+float64(0.5*detmath.NormFloat64(r))))
		if r.Float64() < incomeGap[channel] {
			income = ""
		}
		if err := w.Write([]string{itoa(i + 1), channel, age, income}); err != nil {
			return err
		}
	}
	return nil
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
func writeSubscribers(w *csv.Writer, n int) error {
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
	return nil
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}

// writeMMMWeekly writes mmm_weekly.csv from mmmfixture.Rows(): two
// markets x 104 weeks of four channel spends, two controls and sales,
// floats at full precision so the cohort equals the fixture's.
func writeMMMWeekly(w *csv.Writer) error {
	if err := w.Write([]string{"market", "week", "tv", "radio", "search", "social", "price", "promo", "sales"}); err != nil {
		return err
	}
	f := func(v any) string { return strconv.FormatFloat(v.(float64), 'g', -1, 64) }
	for _, row := range mmmfixture.Rows() {
		rec := []string{row[0].(string), strconv.FormatUint(row[1].(uint64), 10)}
		for _, v := range row[2:] {
			rec = append(rec, f(v))
		}
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	return nil
}

// writeBrandSurvey writes brand_survey.csv: the generic two-wave brand
// survey of internal/sem/testdata/brand_spec.json (1200 respondents,
// 600 per wave), row for row the lavaan oracles' brand_data.csv with an
// id and the wave spelled wave1 / wave2. Three correlated factors —
// quality (q1-q3), value (v1-v3), service (s1-s3) — each rated 1-5, and
// recommend 0-10. Wave 2's latent means are PLANTED: value −0.30 and
// service +0.70 in factor SD (−0.24 and +0.525 in marker units),
// quality 0 — the shift a scalar-invariant multi-group MAT_SEM reads.
func writeBrandSurvey(w *csv.Writer, body []byte) error {
	spec, err := brandfixture.Parse(body)
	if err != nil {
		return err
	}
	if err := w.Write(append([]string{"id", spec.WaveField}, spec.Columns()...)); err != nil {
		return err
	}
	for i, r := range spec.Rows() {
		row := []string{itoa(i + 1), "wave" + itoa(r.Wave)}
		for _, v := range r.Values {
			row = append(row, strconv.FormatFloat(v, 'f', -1, 64))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}
