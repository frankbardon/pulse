package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
)

// rate_grid_acceptance_test.go is the end-to-end acceptance of
// directory-loaded lookup tables on their driving use case: a mortgage
// rate per row, computed in a Materialize ATTR_FORMULA as a base rate
// plus a 5 x 4 grid adjustment keyed by two range_label() bandings.
//
// Fixtures (testdata/rate_grid/):
//   - ranges/score_bands.json — five credit-score bands, each half-open
//     [start, end): 620-659 [620, 660), 660-699 [660, 700), 700-739
//     [700, 740), 740-779 [740, 780), 780+ [780, +inf).
//   - ranges/ltv_bands.json — four loan-to-value bands, each half-open
//     [start, end): 0-60 [0, 0.60), 60-80 [0.60, 0.80), 80-90
//     [0.80, 0.90), 90-97 [0.90, 0.97).
//   - lookups/rate_grid.json — `keys` naming both range tables as its
//     axes, `complete: true`, the 20 invented cells.
//
// The same scenario runs twice — through the CLI leaf with
// PULSE_LOOKUP_TABLES_DIR / PULSE_RANGE_TABLES_DIR, and through the
// library with Options.LookupTablesDir / RangeTablesDir — and asserts
// on each: predict accepts the request; every row's written rate is
// base + its cell; a row below every score band (range_label null)
// fails the run with PULSE_LOOKUP_MISS naming details.null_key 0 and
// details.table; a misspelled table is PULSE_LOOKUP_TABLE_UNKNOWN at
// predict and at the run alike.

// rateGridBase is the base rate the formula adds the cell to.
const rateGridBase = 0.065

// rateGridScoreBands / rateGridLTVBands are the band labels in
// declared order — the grid's row and column order below.
var (
	rateGridScoreBands = []string{"620-659", "660-699", "700-739", "740-779", "780+"}
	rateGridLTVBands   = []string{"0-60", "60-80", "80-90", "90-97"}
)

// rateGridCells is the expected adjustment per (score band, LTV band),
// written out independently of the fixture so a wrong cell or band edge
// in the loaded tables cannot pass.
var rateGridCells = [5][4]float64{
	{0.0150, 0.0200, 0.0275, 0.0325},
	{0.0100, 0.0150, 0.0200, 0.0250},
	{0.0050, 0.0100, 0.0125, 0.0175},
	{0.0025, 0.0050, 0.0075, 0.0100},
	{0.0000, 0.0025, 0.0050, 0.0075},
}

// rateGridRow is one cohort record and the grid cell it must land in.
type rateGridRow struct {
	score, ltv float64
	s, l       int // expected band indexes into rateGridCells
}

// rateGridRows covers every cell once at a band's interior, plus the
// half-open edges: a score / LTV exactly on a lower bound belongs to
// that band, a value just under it to the band below.
func rateGridRows() []rateGridRow {
	scores := []float64{640, 680, 720, 760, 805}
	ltvs := []float64{0.45, 0.70, 0.85, 0.93}
	var rows []rateGridRow
	for s, sc := range scores {
		for l, lv := range ltvs {
			rows = append(rows, rateGridRow{sc, lv, s, l})
		}
	}
	return append(rows,
		rateGridRow{620, 0, 0, 0},         // both lower edges inclusive
		rateGridRow{659.99, 0.5999, 0, 0}, // just under 660 / 0.60
		rateGridRow{660, 0.60, 1, 1},
		rateGridRow{780, 0.80, 4, 2},
		rateGridRow{779.5, 0.90, 3, 3},
	)
}

// writeRateGridCohort writes a two-f64-field cohort (credit_score,
// ltv) holding rows at dir/name.
func writeRateGridCohort(t *testing.T, dir, name string, rows [][2]float64) {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "credit_score", Type: encoding.FieldTypeF64},
		{Name: "ltv", Type: encoding.FieldTypeF64},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for _, v := range r {
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(v)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rateGridRequest is the Materialize request computing rate per row
// against the named lookup table.
func rateGridRequest(cohort, table string) *pulse.Request {
	var req pulse.Request
	body, err := json.Marshal(map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"attributes": []any{map[string]any{
			"type": "ATTR_FORMULA", "label": "rate",
			"expression": `0.065 + lookup("` + table + `", range_label("score_bands", credit_score), range_label("ltv_bands", ltv))`,
		}},
	})
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(body, &req); err != nil {
		panic(err)
	}
	return &req
}

// rateGridFixture writes the covering cohort (rates.pulse), the cohort
// with one row below every score band (subprime.pulse) and the three
// requests into dir; the requests name each cohort under cohortDir
// (the CLI's OS filesystem reads absolute paths, a DataDir instance
// paths relative to its root).
func rateGridFixture(t *testing.T, dir, cohortDir string) {
	t.Helper()
	var rows [][2]float64
	for _, r := range rateGridRows() {
		rows = append(rows, [2]float64{r.score, r.ltv})
	}
	writeRateGridCohort(t, dir, "rates.pulse", rows)
	writeRateGridCohort(t, dir, "subprime.pulse", [][2]float64{{700, 0.70}, {600, 0.70}})
	for name, req := range map[string]*pulse.Request{
		"ok.json":       rateGridRequest(filepath.Join(cohortDir, "rates.pulse"), "rate_grid"),
		"subprime.json": rateGridRequest(filepath.Join(cohortDir, "subprime.pulse"), "rate_grid"),
		"typo.json":     rateGridRequest(filepath.Join(cohortDir, "rates.pulse"), "rate_gird"),
	} {
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rateGridTablesDir returns the absolute testdata dir of one table kind.
func rateGridTablesDir(t *testing.T, kind string) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("testdata", "rate_grid", kind))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// checkRateGridOutput reads the materialized cohort and asserts every
// row's rate is base + its cell. The comparison is exact: the formula's
// `0.065 + cell` and the want below are the same single IEEE-754 double
// addition of the same two operands (no multiply, so no FMA contraction
// on any arch), and the written f64 column round-trips its bits.
func checkRateGridOutput(t *testing.T, p *pulse.Pulse, out string) {
	t.Helper()
	c, err := p.Open(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	col := -1
	for i, f := range r.Schema().Fields {
		if f.Name == "rate" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no rate column in %+v", r.Schema().Fields)
	}
	rows := rateGridRows()
	if r.Len() != int64(len(rows)) {
		t.Fatalf("output rows = %d, want %d", r.Len(), len(rows))
	}
	base := rateGridBase
	covered := map[[2]int]bool{}
	for i, want := range rows {
		rec, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		cell := rateGridCells[want.s][want.l]
		if got, exp := rec[col], base+cell; got != exp {
			t.Errorf("row %d (score %v, ltv %v → %s|%s): rate = %v, want %v",
				i, want.score, want.ltv, rateGridScoreBands[want.s], rateGridLTVBands[want.l], got, exp)
		}
		covered[[2]int{want.s, want.l}] = true
	}
	if len(covered) != 20 {
		t.Fatalf("fixture covers %d cells, want all 20", len(covered))
	}
}

// rateGridCoded unwraps the CodedError a failed run returns.
func rateGridCoded(t *testing.T, err error, code perrors.Code) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ce
}

// TestRateGridAcceptance_CLI drives the scenario through `pulse api
// materialize` with the table directories named by environment
// variables.
func TestRateGridAcceptance_CLI(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	dir := withTempDataDir(t)
	t.Setenv("PULSE_LOOKUP_TABLES_DIR", rateGridTablesDir(t, "lookups"))
	t.Setenv("PULSE_RANGE_TABLES_DIR", rateGridTablesDir(t, "ranges"))
	rateGridFixture(t, dir, dir)
	req := func(name string) string { return filepath.Join(dir, name) }
	out := filepath.Join(dir, "out.pulse")

	type envelope struct {
		Errors []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"errors"`
	}
	decode := func(buf *bytes.Buffer) envelope {
		t.Helper()
		var env envelope
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v\n%s", err, buf.String())
		}
		return env
	}

	// Predict accepts the request.
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "materialize", "--predict", "--json", "-r", req("ok.json")); err != nil {
		t.Fatalf("predict: %v\n%s", err, buf.String())
	}
	if env := decode(&buf); len(env.Errors) != 0 {
		t.Fatalf("predict refused the rate-grid request: %s", buf.String())
	}

	// The run writes base + cell on every row.
	buf.Reset()
	if err := runAPICLI(t, &buf, "materialize", "--json", "-r", req("ok.json"), "-o", out); err != nil {
		t.Fatalf("materialize: %v\n%s", err, buf.String())
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatal(err)
	}
	checkRateGridOutput(t, p, out)

	// A score below every band: range_label is null, lookup misses on
	// its null key 0 and the run fails loudly.
	buf.Reset()
	err = runAPICLI(t, &buf, "materialize", "--json", "-r", req("subprime.json"), "-o", filepath.Join(dir, "subprime_out.pulse"))
	rateGridCoded(t, err, perrors.PULSE_LOOKUP_MISS)
	env := decode(&buf)
	if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_LOOKUP_MISS" ||
		env.Errors[0].Details["null_key"] != 0.0 || env.Errors[0].Details["table"] != "rate_grid" {
		t.Fatalf("envelope must carry PULSE_LOOKUP_MISS with null_key 0 and table rate_grid:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "subprime_out.pulse")); !os.IsNotExist(err) {
		t.Fatalf("a failed run must write nothing: stat err = %v", err)
	}

	// A misspelled table: the same code at predict and at the run.
	buf.Reset()
	_ = runAPICLI(t, &buf, "materialize", "--predict", "--json", "-r", req("typo.json"))
	if env := decode(&buf); len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_LOOKUP_TABLE_UNKNOWN" {
		t.Fatalf("predict must refuse the misspelled table with PULSE_LOOKUP_TABLE_UNKNOWN:\n%s", buf.String())
	}
	buf.Reset()
	err = runAPICLI(t, &buf, "materialize", "--json", "-r", req("typo.json"), "-o", filepath.Join(dir, "typo_out.pulse"))
	rateGridCoded(t, err, perrors.PULSE_LOOKUP_TABLE_UNKNOWN)
}

// TestRateGridAcceptance_Library drives the same scenario through the
// facade with the table directories named by Options.
func TestRateGridAcceptance_Library(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rateGridFixture(t, dir, "")
	p, err := pulse.New(pulse.Options{
		DataDir:         dir,
		LookupTablesDir: rateGridTablesDir(t, "lookups"),
		RangeTablesDir:  rateGridTablesDir(t, "ranges"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	out := "out.pulse"
	ok := rateGridRequest("rates.pulse", "rate_grid")
	env, err := p.PredictMaterialize(ctx, ok)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env)
	}
	if _, err := p.Materialize(ctx, ok, out, pulse.MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	checkRateGridOutput(t, p, out)

	_, err = p.Materialize(ctx, rateGridRequest("subprime.pulse", "rate_grid"), "subprime_out.pulse", pulse.MaterializeOptions{})
	ce := rateGridCoded(t, err, perrors.PULSE_LOOKUP_MISS)
	if ce.Details["null_key"] != 0 || ce.Details["table"] != "rate_grid" {
		t.Fatalf("details = %v, want null_key 0 and table rate_grid", ce.Details)
	}

	typo := rateGridRequest("rates.pulse", "rate_gird")
	env, err = p.PredictMaterialize(ctx, typo)
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perrors.PULSE_LOOKUP_TABLE_UNKNOWN) {
		t.Fatalf("predict on a misspelled table: %v %+v, want PULSE_LOOKUP_TABLE_UNKNOWN", err, env)
	}
	_, err = p.Materialize(ctx, typo, "typo_out.pulse", pulse.MaterializeOptions{})
	rateGridCoded(t, err, perrors.PULSE_LOOKUP_TABLE_UNKNOWN)
}
