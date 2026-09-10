package synth

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// modelFixtureSchema is the cohort shape every test in this file fits
// against: one scalar target, a second scalar that is pure noise, a
// three-level categorical and a two-option set. It is the smallest
// schema that exercises all three admission rules at once — a dropped
// reference level, a set option that is NOT dropped, and a numeric with
// no relationship to either.
func modelFixtureSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	region := encoding.NewDictionary()
	for _, v := range []string{"east", "west", "north"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("region dict add: %v", err)
		}
	}
	assets := encoding.NewDictionary()
	for _, v := range []string{"eco", "premium"} {
		if _, err := assets.Add(v); err != nil {
			t.Fatalf("assets dict add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "noise", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: region},
		{Name: "assets", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: assets},
	}}
}

// encodeModelRows renders rows into the raw record stream profileRecords
// consumes. It deliberately produces ONLY the record block — no header,
// no schema — because profileRecords takes an already-positioned reader,
// which is what lets the scan-count test wrap that reader and count
// every byte the profiler pulls.
func encodeModelRows(t *testing.T, schema *encoding.Schema, rows []map[string]any, nulls []map[string]bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	bitmapSize := schema.BitmapByteSize()
	for ri, row := range rows {
		var rowNulls map[string]bool
		if nulls != nil {
			rowNulls = nulls[ri]
		}
		for i := range schema.Fields {
			f := &schema.Fields[i]
			if err := writeFieldValueForField(&buf, f, row[f.Name], rowNulls[f.Name]); err != nil {
				t.Fatalf("encode row %d field %q: %v", ri, f.Name, err)
			}
		}
		if bitmapSize > 0 {
			bitmap := make([]byte, bitmapSize)
			for i := range schema.Fields {
				if schema.Fields[i].Nullable && rowNulls[schema.Fields[i].Name] {
					encoding.BitmapSetNull(bitmap, i)
				}
			}
			if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
				t.Fatalf("write bitmap row %d: %v", ri, err)
			}
		}
	}
	return buf.Bytes()
}

// modelFixtureRows generates the exact, noiseless generating model
//
//	spend = 4 + 3·[region=west] + 7·[region=north] + 5·[assets has eco]
//
// walking every (region, assets) combination so no design column is
// collinear with another. Returned alongside the fixture's own row
// count so callers assert against ground truth rather than a constant.
func modelFixtureRows(reps int) ([]map[string]any, []map[string]bool) {
	regions := []string{"east", "west", "north"}
	assetSets := []map[string]bool{
		{},
		{"eco": true},
		{"premium": true},
		{"eco": true, "premium": true},
	}
	var rows []map[string]any
	var nulls []map[string]bool
	for r := 0; r < reps; r++ {
		for ri, region := range regions {
			for ai, sel := range assetSets {
				spend := 4.0
				switch region {
				case "west":
					spend += 3
				case "north":
					spend += 7
				}
				if sel["eco"] {
					spend += 5
				}
				rows = append(rows, map[string]any{
					"spend":  spend,
					"noise":  float64(r*100 + ri*10 + ai),
					"region": region,
					"assets": sel,
				})
				nulls = append(nulls, map[string]bool{})
			}
		}
	}
	return rows, nulls
}

func modelByField(models []FieldModel, field string) (FieldModel, bool) {
	for _, m := range models {
		if m.Field == field {
			return m, true
		}
	}
	return FieldModel{}, false
}

func coefficientFor(m FieldModel, field, level string) (float64, bool) {
	for _, p := range m.Predictors {
		if p.Field == field && p.Level == level {
			return p.Coefficient, true
		}
	}
	return 0, false
}

// TestProfileModels_CleanMultiPredictorFit is the acceptance bar: an
// exactly-linear cohort must have its generating coefficients recovered
// to tolerance, with the reference level absorbed into the intercept and
// BOTH set options retained (a set is not a partition, so nothing is
// dropped from it).
func TestProfileModels_CleanMultiPredictorFit(t *testing.T) {
	schema := modelFixtureSchema(t)
	rows, nulls := modelFixtureRows(20)
	data := encodeModelRows(t, schema, rows, nulls)

	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	models := prof.FittedModels()
	spend, ok := modelByField(models, "spend")
	if !ok {
		t.Fatalf("no model for spend; models=%+v warnings=%v", models, prof.Warnings)
	}
	if spend.NObs != len(rows) {
		t.Errorf("NObs = %d, want %d", spend.NObs, len(rows))
	}

	const tol = 1e-6
	if math.Abs(spend.Intercept-4) > tol {
		t.Errorf("intercept = %.8f, want 4 (east + no options, the dropped reference)", spend.Intercept)
	}
	want := []struct {
		field, level string
		coef         float64
	}{
		{"region", "west", 3},
		{"region", "north", 7},
		{"assets", "eco", 5},
		{"assets", "premium", 0},
	}
	for _, w := range want {
		got, found := coefficientFor(spend, w.field, w.level)
		if !found {
			t.Errorf("no coefficient for %s=%s", w.field, w.level)
			continue
		}
		if math.Abs(got-w.coef) > tol {
			t.Errorf("coefficient %s=%s = %.8f, want %.8f", w.field, w.level, got, w.coef)
		}
	}
	// The reference level must NOT carry its own coefficient — that is
	// precisely the rank-deficiency modelColumns exists to avoid.
	if _, found := coefficientFor(spend, "region", "east"); found {
		t.Error("region=east must be the dropped reference level, not a fitted column")
	}
	if math.Abs(spend.R2-1) > 1e-9 {
		t.Errorf("R2 = %.12f, want 1 on a noiseless generating model", spend.R2)
	}
	if spend.ResidualStd > 1e-6 {
		t.Errorf("ResidualStd = %.12f, want ~0 on a noiseless generating model", spend.ResidualStd)
	}
}

// TestProfileModels_ResidualsAreFitted covers FR-6: residuals exist per
// numeric field, are row-aligned across fields, and are the fit's own
// observed − predicted rather than a raw value.
func TestProfileModels_ResidualsAreFitted(t *testing.T) {
	schema := modelFixtureSchema(t)
	rows, nulls := modelFixtureRows(20)
	// Perturb one row so its residual is a known non-zero number: an
	// all-zero residual vector would pass a weaker assertion by accident.
	rows[7]["spend"] = rows[7]["spend"].(float64) + 100
	data := encodeModelRows(t, schema, rows, nulls)

	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	spend, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}
	if len(spend.Residuals) != len(rows) {
		t.Fatalf("len(Residuals) = %d, want %d", len(spend.Residuals), len(rows))
	}
	if len(spend.ResidualPresent) != len(spend.Residuals) {
		t.Fatalf("presence vector length %d != residual length %d",
			len(spend.ResidualPresent), len(spend.Residuals))
	}
	for i, present := range spend.ResidualPresent {
		if !present {
			t.Fatalf("row %d has no residual, but the fixture has no nulls", i)
		}
	}
	// The perturbed row must carry the largest residual, and the
	// residuals must sum to ~0 (an OLS fit with an intercept has
	// mean-zero residuals — the property that makes them the right
	// input to a correlation).
	maxIdx, maxAbs := -1, -1.0
	sum := 0.0
	for i, r := range spend.Residuals {
		sum += r
		if a := math.Abs(r); a > maxAbs {
			maxAbs, maxIdx = a, i
		}
	}
	if maxIdx != 7 {
		t.Errorf("largest |residual| at row %d, want row 7 (the perturbed row)", maxIdx)
	}
	if math.Abs(sum) > 1e-6 {
		t.Errorf("residuals sum to %.8f, want ~0", sum)
	}
	// Every model from one capture must index the same retained rows.
	for _, m := range prof.FittedModels() {
		if len(m.Residuals) != len(spend.Residuals) {
			t.Errorf("model %q residual length %d, want %d (row-aligned across fields)",
				m.Field, len(m.Residuals), len(spend.Residuals))
		}
	}
}

// TestProfileModels_NullsListwiseDeleted asserts the fit and the residual
// vector agree on which rows contributed. They are two views of ONE
// admission rule (both read through the same dummyRecord), and a drift
// between them would silently correlate residuals over a different row
// set than the one the coefficients were fitted on.
func TestProfileModels_NullsListwiseDeleted(t *testing.T) {
	schema := modelFixtureSchema(t)
	rows, nulls := modelFixtureRows(20)
	// Three distinct missingness shapes: null target, null categorical
	// predictor, null set predictor.
	nulls[0]["spend"] = true
	nulls[1]["region"] = true
	nulls[2]["assets"] = true
	data := encodeModelRows(t, schema, rows, nulls)

	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	spend, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}
	wantN := len(rows) - 3
	if spend.NObs != wantN {
		t.Errorf("NObs = %d, want %d (three rows listwise-deleted)", spend.NObs, wantN)
	}
	present := 0
	for _, ok := range spend.ResidualPresent {
		if ok {
			present++
		}
	}
	if present != wantN {
		t.Errorf("%d residuals present, want %d — presence must mirror NObs", present, wantN)
	}
	for _, i := range []int{0, 1, 2} {
		if spend.ResidualPresent[i] {
			t.Errorf("row %d carries a null and must have no residual", i)
		}
	}
	// The nulled rows still leave the coefficients exactly recoverable,
	// because deletion removes rows rather than biasing them.
	if math.Abs(spend.Intercept-4) > 1e-6 {
		t.Errorf("intercept = %.8f, want 4", spend.Intercept)
	}
}

// TestProfileModels_DegenerateAndNoCandidateFieldsSkip covers the two
// no-predictor paths, which are deliberately NOT the same outcome.
//
// A cohort with no candidate predictor field AT ALL has nothing to
// regress on and produces no model. A cohort whose only candidate is a
// single-level categorical does produce one — an intercept-only model —
// because the candidate was REJECTED by the selection rule rather than
// absent, and "nothing in this cohort explains this field" is a finding
// the document should carry. Neither may fail the run, and both must say
// so in Warnings.
func TestProfileModels_DegenerateAndNoCandidateFieldsSkip(t *testing.T) {
	t.Run("no candidates", func(t *testing.T) {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		}}
		rows := make([]map[string]any, 40)
		nulls := make([]map[string]bool, 40)
		for i := range rows {
			rows[i] = map[string]any{"spend": float64(i)}
			nulls[i] = map[string]bool{}
		}
		prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
			ProfileOptions{FitModels: true})
		if err != nil {
			t.Fatalf("profileRecords must not fail when nothing is fittable: %v", err)
		}
		if got := prof.FittedModels(); got != nil {
			t.Errorf("FittedModels() = %+v, want nil", got)
		}
		if prof.RowCount != len(rows) {
			t.Errorf("RowCount = %d, want %d — the profile itself must be complete", prof.RowCount, len(rows))
		}
		if len(prof.Fields) != 1 || prof.Fields[0].Numeric == nil {
			t.Error("the numeric field profile must be present regardless of the skipped model")
		}
	})

	t.Run("single-level categorical", func(t *testing.T) {
		only := encoding.NewDictionary()
		if _, err := only.Add("east"); err != nil {
			t.Fatalf("dict add: %v", err)
		}
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: only},
		}}
		rows := make([]map[string]any, 40)
		nulls := make([]map[string]bool, 40)
		for i := range rows {
			rows[i] = map[string]any{"spend": float64(i), "region": "east"}
			nulls[i] = map[string]bool{}
		}
		prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
			ProfileOptions{FitModels: true})
		if err != nil {
			t.Fatalf("profileRecords must not fail on a degenerate design: %v", err)
		}
		// A one-level candidate has zero between-group variance, so the
		// rule rejects it without needing to know it is degenerate —
		// which is the point: `wave` on a one-wave cohort is dropped by
		// the same arithmetic that drops a real but negligible
		// predictor, not by a special case.
		spend, ok := modelByField(prof.FittedModels(), "spend")
		if !ok {
			t.Fatalf("want an intercept-only model, got %+v (warnings=%v)",
				prof.FittedModels(), prof.Warnings)
		}
		if len(spend.Predictors) != 0 {
			t.Errorf("Predictors = %+v, want none — the only level explains nothing", spend.Predictors)
		}
		if len(spend.References) != 0 {
			t.Errorf("References = %+v, want none — a baseline without a contrast is meaningless",
				spend.References)
		}
		// spend runs 0..39, so its own spread is the whole residual.
		wantStd := 11.690451944500122
		if math.Abs(spend.ResidualStd-wantStd) > 1e-9 {
			t.Errorf("ResidualStd = %.12f, want %.12f (the field's own sample std)",
				spend.ResidualStd, wantStd)
		}
		if spend.R2 != 0 {
			t.Errorf("R2 = %v, want exactly 0", spend.R2)
		}
		if len(prof.Warnings) == 0 {
			t.Fatal("a predictor-less model must be named in Warnings, not silently emitted")
		}
		// The wording must not read as a failure — a skip and a
		// zero-predictor model mean opposite things.
		joined := strings.Join(prof.Warnings, "\n")
		if !strings.Contains(joined, "carries no predictors") {
			t.Errorf("warnings = %q, want the no-predictor wording", prof.Warnings)
		}
		if strings.Contains(joined, "skipped") {
			t.Errorf("warnings = %q, must not call a complete model a skip", prof.Warnings)
		}
	})
}

// TestProfileModels_TooFewObservationsSkips asserts that a sample too
// short to say anything never reaches the solver at all.
//
// Five rows against a six-level categorical is the design the closed
// form cannot solve (n < p + 1). Selection refuses to SCORE a sample
// that thin — below minSelectionObservations the adjusted statistic is
// dominated by its own degrees-of-freedom correction — so the candidate
// is rejected and the target lands on the intercept-only path instead of
// on a rank failure. Narrowing on merit is what prevents the unsolvable
// design; the solver never sees it.
func TestProfileModels_TooFewObservationsSkips(t *testing.T) {
	region := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: region},
	}}
	levels := []string{"a", "b", "c", "d", "e"}
	rows := make([]map[string]any, len(levels))
	nulls := make([]map[string]bool, len(levels))
	for i, lv := range levels {
		rows[i] = map[string]any{"spend": float64(i) * 1.5, "region": lv}
		nulls[i] = map[string]bool{}
	}
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("too few observations must warn, not fail the run: %v", err)
	}
	spend, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("want an intercept-only model, got %+v (warnings=%v)",
			prof.FittedModels(), prof.Warnings)
	}
	if len(spend.Predictors) != 0 {
		t.Errorf("Predictors = %+v, want none — five rows cannot support five levels",
			spend.Predictors)
	}
	if spend.NObs != len(rows) {
		t.Errorf("NObs = %d, want %d", spend.NObs, len(rows))
	}
	if len(prof.Warnings) == 0 {
		t.Fatal("expected a warning naming the predictor-less field")
	}
	if prof.RowCount != len(rows) {
		t.Errorf("RowCount = %d, want %d", prof.RowCount, len(rows))
	}
}

// TestProfileModels_CollinearCandidatesResolvedByRefit covers the
// nested-categorical case — `region` inside `dma`, `age` inside
// `ageExact` — end to end.
//
// Selection scores candidates MARGINALLY, so two exactly-nested ones
// both clear the floor and both enter, and the design comes out
// rank-deficient. That is deliberate: the redundancy is resolved by the
// solver's refusal rather than by a second rule, and the fit is retried
// with its weakest-scoring predictor dropped. The field must end up with
// a MODEL carrying the surviving candidate — not a skip, which is what
// this produced before the refit existed and what cost the motivating
// cohort 23 of its 105 targets.
func TestProfileModels_CollinearCandidatesResolvedByRefit(t *testing.T) {
	coarse := encoding.NewDictionary()
	for _, v := range []string{"a", "b"} {
		if _, err := coarse.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	fine := encoding.NewDictionary()
	for _, v := range []string{"a1", "b1"} {
		if _, err := fine.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "coarse", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: coarse},
		{Name: "fine", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: fine},
	}}
	rows := make([]map[string]any, 40)
	nulls := make([]map[string]bool, 40)
	for i := range rows {
		c, f := "a", "a1"
		spend := 100.0
		if i%2 == 1 {
			c, f, spend = "b", "b1", 200.0
		}
		// A little within-group spread so the target has variance to
		// explain; the between-group split still dominates, which is
		// what puts both nested candidates over the floor.
		spend += float64(i) / 40
		rows[i] = map[string]any{"spend": spend, "coarse": c, "fine": f}
		nulls[i] = map[string]bool{}
	}
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("a rank-deficient design must be narrowed, not fail the run: %v", err)
	}
	m, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; the refit must recover one: %v", prof.Warnings)
	}
	got := predictorFields(m)
	if len(got) != 1 {
		t.Fatalf("admitted predictors = %v, want exactly one of the nested pair", got)
	}
	// The two candidates explain the target identically here, so the tie
	// break decides — the LATER field in schema order is given up, which
	// is arbitrary but fixed, and which of them survives changes the
	// reading of the coefficients and no fitted value.
	if got[0] != "coarse" {
		t.Errorf("survivor = %q, want coarse (ties give up the later field)", got[0])
	}
	if m.R2 < 0.99 {
		t.Errorf("R2 = %v, want a near-perfect fit — the surviving candidate carries the target", m.R2)
	}
	if prof.RowCount != len(rows) || len(prof.Fields) != 3 {
		t.Errorf("profile incomplete: RowCount=%d fields=%d", prof.RowCount, len(prof.Fields))
	}
}

// TestDropWeakestChoice covers the narrowing step's own contract: it
// gives up the lowest-scoring predictor, breaks ties on the later field,
// and refuses to narrow a design that has nothing left to give.
func TestDropWeakestChoice(t *testing.T) {
	names := func(cs []predictorChoice) []string {
		out := make([]string, len(cs))
		for i := range cs {
			out[i] = cs[i].field
		}
		return out
	}
	cases := []struct {
		name    string
		in      []predictorChoice
		wantOK  bool
		wantOut []string
	}{
		{
			name:   "nothing to give up",
			in:     []predictorChoice{{field: "a", score: 0.5}},
			wantOK: false,
		},
		{
			name:   "empty",
			in:     nil,
			wantOK: false,
		},
		{
			name: "lowest score goes",
			in: []predictorChoice{
				{field: "a", score: 0.5}, {field: "b", score: 0.02}, {field: "c", score: 0.3},
			},
			wantOK:  true,
			wantOut: []string{"a", "c"},
		},
		{
			name: "ties give up the later field",
			in: []predictorChoice{
				{field: "a", score: 0.2}, {field: "b", score: 0.2}, {field: "c", score: 0.9},
			},
			wantOK:  true,
			wantOut: []string{"a", "c"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := dropWeakestChoice(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if fmt.Sprint(names(got)) != fmt.Sprint(tc.wantOut) {
				t.Errorf("survivors = %v, want %v", names(got), tc.wantOut)
			}
		})
	}
}

// countingReader records every byte pulled from the underlying stream.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TestProfileModels_RidesTheExistingScan is the "no second pass" gate.
// profileRecords consumes a one-shot io.Reader, so a second pass over
// the cohort is not merely undesirable here — it is unrepresentable
// without re-reading bytes. Counting them is therefore a direct
// measurement: fitting must pull EXACTLY the same byte count as not
// fitting.
func TestProfileModels_RidesTheExistingScan(t *testing.T) {
	schema := modelFixtureSchema(t)
	rows, nulls := modelFixtureRows(20)
	data := encodeModelRows(t, schema, rows, nulls)

	baseline := &countingReader{r: bytes.NewReader(data)}
	if _, err := profileRecords(schema, baseline, ProfileOptions{}); err != nil {
		t.Fatalf("baseline profileRecords: %v", err)
	}
	fitted := &countingReader{r: bytes.NewReader(data)}
	prof, err := profileRecords(schema, fitted, ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("fitted profileRecords: %v", err)
	}
	if len(prof.FittedModels()) == 0 {
		t.Fatal("fixture must produce at least one model for this test to mean anything")
	}
	if fitted.n != baseline.n {
		t.Errorf("fitting read %d bytes, baseline read %d — the fit must ride the existing scan",
			fitted.n, baseline.n)
	}
}

// TestProfileModels_FitStateIndependentOfRowCount asserts the engine's
// own O(p²) property as it surfaces here: ten times the rows produces
// the same design, the same coefficient count and the same coefficients.
// The residual reservoir is the one row-dependent structure and is
// bounded by modelResidualCap — asserted alongside so the two are never
// confused for each other.
func TestProfileModels_FitStateIndependentOfRowCount(t *testing.T) {
	schema := modelFixtureSchema(t)
	small, smallNulls := modelFixtureRows(5)
	profSmall, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, small, smallNulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("small profileRecords: %v", err)
	}
	// A fresh schema: dictionaries are mutated by the encoder, and the
	// two runs must not share one.
	schema2 := modelFixtureSchema(t)
	large, largeNulls := modelFixtureRows(50)
	profLarge, err := profileRecords(schema2, bytes.NewReader(encodeModelRows(t, schema2, large, largeNulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("large profileRecords: %v", err)
	}

	a, ok := modelByField(profSmall.FittedModels(), "spend")
	if !ok {
		t.Fatalf("small run produced no spend model: %v", profSmall.Warnings)
	}
	b, ok := modelByField(profLarge.FittedModels(), "spend")
	if !ok {
		t.Fatalf("large run produced no spend model: %v", profLarge.Warnings)
	}
	if len(a.Predictors) != len(b.Predictors) {
		t.Fatalf("predictor count moved with row count: %d vs %d", len(a.Predictors), len(b.Predictors))
	}
	for i := range a.Predictors {
		if a.Predictors[i].Column != b.Predictors[i].Column {
			t.Fatalf("column %d = %q vs %q", i, a.Predictors[i].Column, b.Predictors[i].Column)
		}
		if math.Abs(a.Predictors[i].Coefficient-b.Predictors[i].Coefficient) > 1e-6 {
			t.Errorf("coefficient %q = %.8f vs %.8f", a.Predictors[i].Column,
				a.Predictors[i].Coefficient, b.Predictors[i].Coefficient)
		}
	}
	if len(a.Residuals) != len(small) {
		t.Errorf("small residual vector = %d, want %d", len(a.Residuals), len(small))
	}
	if len(b.Residuals) > modelResidualCap {
		t.Errorf("residual reservoir = %d, above the %d cap", len(b.Residuals), modelResidualCap)
	}
}
