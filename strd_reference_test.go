package pulse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// strd_reference_test.go runs the NIST Statistical Reference Datasets
// (testdata/strd, vendored verbatim) through the public API and scores
// each figure by its log relative error against NIST's certified value:
// LRE = -log10(|x - c| / |c|), the digits of agreement (McCullough
// 1998). StRD is the accuracy benchmark statistical packages are
// measured against, so the achieved digits are published on the book's
// "How Pulse is validated" page from lre.json, which these tests both
// assert and (under -update) rewrite.
//
// Each dataset runs end to end: an f64 cohort built through
// NewCohortBuilder, then a plain Process request — REG_OLS for the
// linear regressions (polynomial terms are f64 power columns, as a
// user would materialize them), TEST_ANOVA_F split by the treatment
// for the ANOVA sets, AGG_AVERAGE and AGG_WELFORD's sample SD for the
// univariate sets.
// What Pulse cannot fit (regression through the origin, the lag-1
// autocorrelation) is recorded as not offered, never skipped silently.

const (
	strdDir = "testdata/strd"
	// strdLRECap caps a perfect match: certified values carry 15
	// significant digits.
	strdLRECap = 15.0
)

// strdLRE is the digits of agreement between got and certified c.
func strdLRE(got, c float64) float64 {
	if math.IsNaN(got) || math.IsInf(got, 0) {
		return 0
	}
	if got == c {
		return strdLRECap
	}
	var e float64
	if c == 0 {
		e = -math.Log10(math.Abs(got))
	} else {
		e = -math.Log10(math.Abs(got-c) / math.Abs(c))
	}
	return math.Max(0, math.Min(strdLRECap, e))
}

// strdFile is one parsed .dat file: its lines (CR stripped) and the
// numeric data rows.
type strdFile struct {
	name  string
	lines []string
	rows  [][]float64
}

var strdDataRange = regexp.MustCompile(`Data\s*:?\s*\(?lines\s+(\d+)\s+to\s+(\d+)`)

func loadStRD(t *testing.T, name string) strdFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(strdDir, name+".dat"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	f := strdFile{name: name, lines: strings.Split(strings.ReplaceAll(string(raw), "\r", ""), "\n")}
	var from, to int
	for _, l := range f.lines[:60] {
		if m := strdDataRange.FindStringSubmatch(l); m != nil {
			from, _ = strconv.Atoi(m[1])
			to, _ = strconv.Atoi(m[2])
			break
		}
	}
	if from == 0 {
		t.Fatalf("%s: no data line range in the header", name)
	}
	for i := from - 1; i < to && i < len(f.lines); i++ {
		fields := strings.Fields(f.lines[i])
		if len(fields) == 0 {
			continue
		}
		row := make([]float64, 0, len(fields))
		for _, s := range fields {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				row = nil
				break
			}
			row = append(row, v)
		}
		if row != nil {
			f.rows = append(f.rows, row)
		}
	}
	if len(f.rows) == 0 {
		t.Fatalf("%s: no data rows", name)
	}
	return f
}

// value returns the last number on the first line containing key at
// or after line index from.
func (f strdFile) value(t *testing.T, key string) float64 {
	t.Helper()
	for i, l := range f.lines {
		if !strings.Contains(l, key) {
			continue
		}
		fields := strings.Fields(strings.ReplaceAll(l, ":", " "))
		// "Standard Deviation" sits on the line after "Certified
		// Residual" in the ANOVA files; the number follows on it.
		for j := len(fields) - 1; j >= 0; j-- {
			if v, err := strconv.ParseFloat(fields[j], 64); err == nil {
				return v
			}
		}
		if i+1 < len(f.lines) {
			return strdFile{lines: f.lines[i+1 : i+2]}.value(t, "")
		}
	}
	t.Fatalf("%s: no certified %q", f.name, key)
	return 0
}

// numbers returns the numeric fields of the first line whose first
// field is key (a parameter row such as "B3").
func (f strdFile) numbers(key string) []float64 {
	for _, l := range f.lines {
		fields := strings.Fields(l)
		if len(fields) == 0 || fields[0] != key {
			continue
		}
		var out []float64
		for _, s := range fields[1:] {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				out = append(out, v)
			}
		}
		return out
	}
	return nil
}

func strdPulse(t *testing.T, cohort string, fields []string, rows [][]any, cat map[string][]string) *Pulse {
	t.Helper()
	p, _ := matPulse(t, Options{})
	var schema encoding.Schema
	for _, name := range fields {
		f := encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "StRD column " + name + " of the certified dataset"}
		if labels, ok := cat[name]; ok {
			d := encoding.NewDictionary()
			for _, l := range labels {
				if _, err := d.Add(l); err != nil {
					t.Fatal(err)
				}
			}
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU16, d
		}
		schema.Fields = append(schema.Fields, f)
	}
	b, err := p.NewCohortBuilder(context.Background(), cohort, schema, CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range rows {
		if err := b.Append(CohortRow(r)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p
}

// strdResult is one dataset's achieved digits, figure by figure; a
// figure Pulse does not offer is listed under NotOffered with why.
type strdResult struct {
	Dataset    string             `json:"dataset"`
	Difficulty string             `json:"difficulty"`
	Digits     map[string]float64 `json:"digits,omitempty"`
	Min        *float64           `json:"min_digits,omitempty"`
	NotOffered string             `json:"not_offered,omitempty"`
}

func (r *strdResult) add(figure string, got, certified float64) {
	if r.Digits == nil {
		r.Digits = map[string]float64{}
	}
	d := math.Floor(strdLRE(got, certified)*10) / 10
	r.Digits[figure] = d
	if r.Min == nil || d < *r.Min {
		r.Min = &d
	}
}

func strdDifficulty(f strdFile) string {
	for _, l := range f.lines[:60] {
		for _, lvl := range []string{"Lower", "Average", "Higher"} {
			if strings.Contains(l, lvl+" Level of Difficulty") {
				return strings.ToLower(lvl)
			}
		}
	}
	return ""
}

var strdLLS = []string{"Norris", "Pontius", "NoInt1", "NoInt2", "Filip", "Longley", "Wampler1", "Wampler2", "Wampler3", "Wampler4", "Wampler5"}

// strdSpec is the REG_OLS slot of a StRD regression, through the origin
// when the certified model has no B0.
func strdSpec(preds []string, intercept bool) *types.RegressionSpec {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: preds}
	if !intercept {
		spec.Intercept = &intercept
	}
	return spec
}

func runStRDRegression(t *testing.T, name string) strdResult {
	f := loadStRD(t, "lls_"+name)
	res := strdResult{Dataset: "lls/" + name, Difficulty: strdDifficulty(f)}
	// NoInt1 / NoInt2 certify a fit through the origin (no B0): run with
	// intercept: false.
	first := 0
	if f.numbers("B0") == nil {
		first = 1
	}
	params := first
	for f.numbers(fmt.Sprintf("B%d", params)) != nil {
		params++
	}
	// Longley has six observed predictors; every other set is a
	// polynomial in one x of degree params-1.
	var preds []string
	var rows [][]any
	nx := len(f.rows[0]) - 1
	for _, r := range f.rows {
		row := []any{r[0]}
		if nx > 1 {
			for _, v := range r[1:] {
				row = append(row, v)
			}
		} else {
			for k := 1; k < params; k++ {
				row = append(row, math.Pow(r[1], float64(k)))
			}
		}
		rows = append(rows, row)
	}
	width := nx
	if nx == 1 {
		width = params - 1
	}
	for k := 1; k <= width; k++ {
		preds = append(preds, fmt.Sprintf("x%d", k))
	}
	p := strdPulse(t, "strd.pulse", append([]string{"y"}, preds...), rows, nil)
	resp, err := p.Process(context.Background(), &Request{
		Cohort:      &types.Cohort{Filename: "strd.pulse"},
		Regressions: []*types.RegressionSpec{strdSpec(preds, first == 0)},
	})
	if err != nil {
		t.Fatalf("%s: Process: %v", name, err)
	}
	if len(resp.Regressions) != 1 {
		t.Fatalf("%s: %d regression results", name, len(resp.Regressions))
	}
	reg := resp.Regressions[0]
	if reg.Error != nil {
		res.NotOffered = "REG_OLS refused the fit: " + string(reg.Error.Code)
		return res
	}
	for k := first; k < params; k++ {
		cert := f.numbers(fmt.Sprintf("B%d", k))
		key := "(intercept)"
		if k > 0 {
			key = preds[k-1]
		}
		res.add(fmt.Sprintf("B%d", k), reg.Coefficients[key], cert[0])
		res.add(fmt.Sprintf("SE(B%d)", k), reg.StdErrors[key], cert[1])
	}
	res.add("residual_sd", reg.ResidualStdErr, f.value(t, "Standard Deviation  "))
	res.add("r2", reg.R2, f.value(t, "R-Squared"))
	return res
}

var strdANOVA = []string{"SiRstv", "SmLs01", "SmLs02", "SmLs03", "AtmWtAg", "SmLs04", "SmLs05", "SmLs06", "SmLs07", "SmLs08", "SmLs09"}

func runStRDANOVA(t *testing.T, name string) strdResult {
	f := loadStRD(t, "anova_"+name)
	res := strdResult{Dataset: "anova/" + name, Difficulty: strdDifficulty(f)}
	seen := map[string]bool{}
	var labels []string
	var rows [][]any
	for _, r := range f.rows {
		g := strconv.FormatFloat(r[0], 'g', -1, 64)
		if !seen[g] {
			seen[g] = true
			labels = append(labels, g)
		}
		rows = append(rows, []any{g, r[1]})
	}
	p := strdPulse(t, "strd.pulse", []string{"g", "y"}, rows, map[string][]string{"g": labels})
	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "strd.pulse"},
		Tests:  []*types.Test{{Type: types.TEST_ANOVA_F, Field: "y", SplitBy: "g"}},
	})
	if err != nil {
		t.Fatalf("%s: Process: %v", name, err)
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Error != nil {
		t.Fatalf("%s: tests %+v", name, resp.Tests)
	}
	tr := resp.Tests[0]
	num := func(k string) float64 {
		v, ok := tr.Details[k].(float64)
		if !ok {
			t.Fatalf("%s: details[%q] = %v", name, k, tr.Details[k])
		}
		return v
	}
	var between, within []float64
	for _, l := range f.lines {
		fields := strings.Fields(l)
		if len(fields) > 2 && fields[0] == "Between" {
			between = parseFloats(fields[2:])
		}
		if len(fields) > 2 && fields[0] == "Within" {
			within = parseFloats(fields[2:])
		}
	}
	ssB, ssW := num("ss_between"), num("ss_within")
	res.add("ss_between", ssB, between[1])
	res.add("ms_between", ssB/tr.DF, between[2])
	res.add("f", tr.Statistic, between[3])
	res.add("ss_within", ssW, within[1])
	res.add("ms_within", num("ms_within"), within[2])
	res.add("r2", ssB/(ssB+ssW), f.value(t, "Certified R-Squared"))
	res.add("residual_sd", math.Sqrt(num("ms_within")), f.value(t, "Standard Deviation  "))
	return res
}

func parseFloats(ss []string) []float64 {
	var out []float64
	for _, s := range ss {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

var strdUniv = []string{"PiDigits", "Lottery", "Lew", "Mavro", "Michelso", "NumAcc1", "NumAcc2", "NumAcc3", "NumAcc4"}

func runStRDUnivariate(t *testing.T, name string) strdResult {
	f := loadStRD(t, "univ_"+name)
	res := strdResult{Dataset: "univ/" + name, Difficulty: strdDifficulty(f)}
	var rows [][]any
	for _, r := range f.rows {
		rows = append(rows, []any{r[0]})
	}
	p := strdPulse(t, "strd.pulse", []string{"y"}, rows, nil)
	resp, err := p.Process(context.Background(), &Request{
		Cohort: &types.Cohort{Filename: "strd.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "y", Label: "mean"},
			{Type: types.AGG_STDDEV, Field: "y", Label: "sd", Params: json.RawMessage(`{"ddof":1}`)},
			{Type: types.AGG_WELFORD, Field: "y", Label: "w"},
		},
	})
	if err != nil {
		t.Fatalf("%s: Process: %v", name, err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("%s: %d data rows", name, len(resp.Data))
	}
	res.add("mean", resp.Data[0]["mean"].(float64), f.value(t, "ybar:"))
	// NIST certifies the sample SD (n-1): AGG_STDDEV with params.ddof 1
	// (its default ddof 0 is the population SD). AGG_WELFORD's stddev
	// component is the same n-1 figure and must reach the same digits.
	certSD := f.value(t, "(denom. = n-1)")
	sd := resp.Data[0]["sd"].(float64)
	res.add("sd", sd, certSD)
	var wsd float64
	found := false
	if resp.Components != nil {
		for _, a := range resp.Components.Aggregations {
			if a.Label == "w" {
				wsd, found = a.Operator["stddev"].(float64)
			}
		}
	}
	if !found {
		t.Fatalf("%s: no AGG_WELFORD stddev component", name)
	}
	if got, want := math.Floor(strdLRE(wsd, certSD)*10)/10, res.Digits["sd"]; got != want {
		t.Errorf("%s: AGG_WELFORD stddev reaches %.1f digits, AGG_STDDEV ddof 1 %.1f", name, got, want)
	}
	res.NotOffered = "lag-1 autocorrelation: no built-in aggregator computes it"
	return res
}

// strdLREPath is the committed record of the digits achieved;
// strdDocgenCopy is the byte-identical copy internal/docgen embeds to
// render the published table (an export outside the repository
// carries it).
const (
	strdLREPath    = strdDir + "/lre.json"
	strdDocgenCopy = "internal/docgen/strd_lre.json"
)

func strdRunAll(t *testing.T) []strdResult {
	var out []strdResult
	for _, n := range strdLLS {
		out = append(out, runStRDRegression(t, n))
	}
	for _, n := range strdANOVA {
		out = append(out, runStRDANOVA(t, n))
	}
	for _, n := range strdUniv {
		out = append(out, runStRDUnivariate(t, n))
	}
	return out
}

func strdEncode(t *testing.T, rs []strdResult) []byte {
	t.Helper()
	body, err := json.MarshalIndent(map[string]any{
		"source":  "NIST/ITL StRD, https://www.itl.nist.gov/div898/strd/",
		"metric":  "log relative error (digits of agreement with the certified value), floored to 0.1, capped at 15",
		"results": rs,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	sum := sha256.Sum256(body)
	return append(body, []byte("\n// golden-hash: "+hex.EncodeToString(sum[:])+"\n")...)
}

// TestStRD_DigitsMatchRecord: every dataset's achieved digits equal the
// committed record, so the published table cannot drift from the code.
// A change in accuracy (better or worse) fails until the record is
// regenerated with -update and the change is reviewed.
func TestStRD_DigitsMatchRecord(t *testing.T) {
	t.Parallel()
	got := strdEncode(t, strdRunAll(t))
	if *updateDocs {
		for _, p := range []string{strdLREPath, strdDocgenCopy} {
			if err := os.WriteFile(p, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	want, err := os.ReadFile(strdLREPath)
	if err != nil {
		t.Fatalf("read %s: %v (generate with go test -run TestStRD -update)", strdLREPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: achieved digits changed; review, then regenerate with go test -run TestStRD_DigitsMatchRecord -update", strdLREPath)
	}
}

// TestStRD_AccuracyFloors: lower- and average-difficulty sets need 7
// digits on every offered figure.
func TestStRD_AccuracyFloors(t *testing.T) {
	t.Parallel()
	for _, r := range strdRunAll(t) {
		if len(r.Digits) == 0 {
			continue
		}
		// Higher-difficulty sets are published as achieved, not floored;
		// TestStRD_DigitsMatchRecord still fails on any change to them.
		if r.Difficulty == "higher" {
			continue
		}
		const floor = 7.0
		if *r.Min < floor {
			keys := make([]string, 0, len(r.Digits))
			for k := range r.Digits {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var worst []string
			for _, k := range keys {
				if r.Digits[k] < floor {
					worst = append(worst, fmt.Sprintf("%s=%.1f", k, r.Digits[k]))
				}
			}
			t.Errorf("%s (%s difficulty): %s below the %.0f-digit floor", r.Dataset, r.Difficulty, strings.Join(worst, ", "), floor)
		}
	}
}

// TestStRD_DocgenCopyCurrent: the table the book publishes is the
// record these tests assert.
func TestStRD_DocgenCopyCurrent(t *testing.T) {
	t.Parallel()
	a, errA := os.ReadFile(strdLREPath)
	b, errB := os.ReadFile(strdDocgenCopy)
	if errA != nil || errB != nil {
		t.Fatalf("read: %v / %v", errA, errB)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("%s differs from %s; regenerate with go test -run TestStRD_DigitsMatchRecord -update", strdDocgenCopy, strdLREPath)
	}
}

// TestStRD_FixturesIntact: the vendored NIST files are byte-identical
// to the copies pinned in SHA256SUMS.
func TestStRD_FixturesIntact(t *testing.T) {
	t.Parallel()
	sums, err := os.ReadFile(filepath.Join(strdDir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatalf("bad SHA256SUMS line %q", line)
		}
		raw, err := os.ReadFile(filepath.Join(strdDir, parts[1]))
		if err != nil {
			t.Errorf("%s: %v", parts[1], err)
			continue
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != parts[0] {
			t.Errorf("%s: bytes differ from SHA256SUMS (never edit a NIST file)", parts[1])
		}
		n++
	}
	if want := len(strdLLS) + len(strdANOVA) + len(strdUniv); n != want {
		t.Errorf("SHA256SUMS lists %d files, want %d", n, want)
	}
}
