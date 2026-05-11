//go:build ignore

// Generator for the shared CSV fixtures referenced by every category
// under examples/. Run from the repo root:
//
//	go run examples/fixtures/gen.go
//
// Output is written into examples/fixtures/. The same seed produces
// byte-identical output so the CSVs are checked in.
//
// Not generated here: all_types.csv. That fixture is hand-curated
// because it exists specifically to exercise every one of the 19
// supported field types end to end (h3_cell, point_f64, decimal128 with
// per-field precision/scale, etc.). Random generation does not buy us
// anything when the goal is type coverage on a 5-row file.
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
	outputDir  = "examples/fixtures"
	dateFormat = "2006-01-02"
)

var (
	regions     = []string{"north", "south", "east", "west"}
	cities      = []string{
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
	fmt.Println("wrote 5 CSVs to", outputDir)
}

// writeExperiment produces an A/B testing cohort designed so every
// statistical test type in examples/tests/ has a clean, non-trivial
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
		"revenue", "session_minutes", "period",
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
		// Revenue: log-normal base * region lift * treatment lift +
		// mild upward drift over time (period index).
		base := math.Exp(4.5 + 0.5*r.NormFloat64())
		treatmentLift := 1.0
		if treatment == "variant" {
			treatmentLift = 1.22
		}
		periodIdx := r.Intn(90)
		drift := 1.0 + 0.003*float64(periodIdx)
		revenue := base * regionLift[region] * treatmentLift * drift
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
	if err := w.Write([]string{"id", "order_date", "revenue"}); err != nil {
		return err
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		offset := r.Intn(365)
		date := start.AddDate(0, 0, offset).Format(dateFormat)
		revenue := math.Exp(6.0 + 0.8*r.NormFloat64())
		if err := w.Write([]string{
			itoa(i + 1),
			date,
			fmt.Sprintf("%.2f", revenue),
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
