package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// rakeSchema is a survey-shaped cohort: three demographic categoricals,
// a wave, a design weight and an unrelated measure.
func rakeSchema() encoding.Schema {
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				panic(err)
			}
		}
		return d
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeCategoricalU8, Description: "Respondent age band", Dictionary: dict("18-34", "35-54", "55+")},
		{Name: "gender", Type: encoding.FieldTypeCategoricalU8, Description: "Respondent gender", Dictionary: dict("F", "M")},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Census region of residence", Dictionary: dict("NE", "MW", "S", "W")},
		{Name: "wave", Type: encoding.FieldTypeU8, Description: "Fieldwork wave number"},
		{Name: "dw", Type: encoding.FieldTypeF64, Description: "Design weight from the sample frame"},
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true, Description: "Brand consideration score"},
	}}
}

// rakeRows is a 240-record sample skewed away from the census margins
// (older, more female, north-east heavy).
func rakeRows() []CohortRow {
	ages := []string{"18-34", "35-54", "55+"}
	regions := []string{"NE", "MW", "S", "W"}
	var rows []CohortRow
	for i := 0; i < 240; i++ {
		a := 2
		switch k := (i * 7) % 10; {
		case k < 2:
			a = 0
		case k < 5:
			a = 1
		}
		g := "M"
		if (i*3)%5 < 3 {
			g = "F"
		}
		r := regions[((i*11)%13)%4]
		var score any = float64(i%9) + 0.5
		if i%17 == 0 {
			score = nil
		}
		rows = append(rows, CohortRow{ages[a], g, r, uint64(i%2 + 1), 0.5 + float64(i%5)*0.25, score})
	}
	return rows
}

func buildRakeSource(t *testing.T, p *Pulse, path string, opts CohortBuilderOptions) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, rakeSchema(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rakeRows() {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func censusRake(cohort, out string) RakeRequest {
	return RakeRequest{
		Cohort: cohort,
		Out:    out,
		Margins: []RakeMargin{
			{Field: "age", Targets: map[string]float64{"18-34": 29, "35-54": 33, "55+": 38}},
			{Field: "gender", Targets: map[string]float64{"F": 51, "M": 49}},
			{Field: "region", Targets: map[string]float64{"NE": 17, "MW": 21, "S": 38, "W": 24}},
		},
		Tol: 1e-12,
	}
}

// TestRake_WritesWeightedCohort: Rake writes every source record with
// its exact cells plus a nullable f64 weight column; the weights sum to
// the record count, and a Process weighted by the new column reproduces
// the census age shares.
func TestRake_WritesWeightedCohort(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildRakeSource(t, p, "src.pulse", CohortBuilderOptions{})
	res, err := p.Rake(ctx, censusRake("src.pulse", "out.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Target != "out.pulse" || res.Records != 240 || res.WeightField != RakeDefaultWeightField || !res.Converged || len(res.Warnings) != 0 {
		t.Fatalf("result %+v", res)
	}
	if len(res.Margins) != 3 || res.Groups != nil {
		t.Fatalf("margins %d groups %v", len(res.Margins), res.Groups)
	}
	if e := res.Efficiency; e.N != 240 || math.Abs(e.SumWeights-240) > 1e-9 || !(e.DeffKish > 1) || !(e.NEff < 240) {
		t.Fatalf("efficiency %+v", e)
	}
	head, err := afero.ReadFile(fsys, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if head[8] != encoding.FormatVersionV1 {
		t.Fatalf("output format %#x", head[8])
	}
	src := rakeSchema()
	outSchema, outRows := readAll(t, p, "out.pulse")
	if len(outSchema.Fields) != len(src.Fields)+1 {
		t.Fatalf("output fields %d", len(outSchema.Fields))
	}
	wf := outSchema.Fields[len(src.Fields)]
	if wf.Name != RakeDefaultWeightField || wf.Type != encoding.FieldTypeF64 || !wf.Nullable || !strings.Contains(wf.Description, "probability") {
		t.Fatalf("weight field %+v", wf)
	}
	want := rakeRows()
	sum := 0.0
	for i, r := range outRows {
		assertRowEqual(t, "row", &src, r[:len(src.Fields)], want[i])
		sum += r[len(src.Fields)].(float64)
	}
	if math.Abs(sum-240) > 1e-9 {
		t.Fatalf("weights sum %g", sum)
	}

	resp, err := p.Process(ctx, &Request{
		Cohort:       &types.Cohort{Filename: "out.pulse"},
		Weight:       &types.WeightSpec{Field: RakeDefaultWeightField},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "age"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "gender", Label: "n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	shares := map[string]float64{"18-34": .29, "35-54": .33, "55+": .38}
	if len(resp.Data) != 3 {
		t.Fatalf("rows %v", resp.Data)
	}
	for _, row := range resp.Data {
		age, _ := row["age"].(string)
		n, _ := row["n"].(float64)
		if math.Abs(n/240-shares[age]) > 1e-9 {
			t.Errorf("weighted %s share %g, want %g (row %v)", age, n/240, shares[age], row)
		}
	}
}

// TestRake_ShardArchiveMatchesSingleFile: an archive source is raked
// over its records in shard order, giving the single-file weights.
func TestRake_ShardArchiveMatchesSingleFile(t *testing.T) {
	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildRakeSource(t, p, "single.pulse", CohortBuilderOptions{})
	buildRakeSource(t, p, "arch.pulse", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 50}})
	a, err := p.Rake(ctx, censusRake("single.pulse", "a.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Rake(ctx, censusRake("arch.pulse", "b.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Records != b.Records || a.Iterations != b.Iterations {
		t.Fatalf("single %+v archive %+v", a, b)
	}
	_, ar := readAll(t, p, "a.pulse")
	_, br := readAll(t, p, "b.pulse")
	for i := range ar {
		x, y := ar[i][6].(float64), br[i][6].(float64)
		if math.Float64bits(x) != math.Float64bits(y) {
			t.Fatalf("record %d: single %v archive %v", i, x, y)
		}
	}
}

// TestRake_ByAndBaseWeight: `by` rakes each wave on its own from the
// design weight; every group reports its own fit.
func TestRake_ByAndBaseWeight(t *testing.T) {
	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildRakeSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := censusRake("src.pulse", "out.pulse")
	req.By = "wave"
	req.BaseWeight = "dw"
	res, err := p.Rake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 || res.Groups[0].Value != "1" || res.Groups[0].Records != 120 || !res.Converged {
		t.Fatalf("groups %+v", res.Groups)
	}
	for _, g := range res.Groups {
		if math.Abs(g.Efficiency.SumWeights-float64(g.Records)) > 1e-9 {
			t.Errorf("group %s sum %g", g.Value, g.Efficiency.SumWeights)
		}
	}
}

// TestRake_RefusalsWriteNothing: every refusal leaves nothing at out.
func TestRake_RefusalsWriteNothing(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		mut  func(r *RakeRequest)
		code errors.Code
	}{
		{"anchored target", func(r *RakeRequest) { r.Out = "arch.pulse#x.pulse" }, errors.SERVICE_VALIDATION},
		{"no cohort", func(r *RakeRequest) { r.Cohort = "" }, errors.PULSE_RAKE_INVALID},
		{"missing cohort", func(r *RakeRequest) { r.Cohort = "nope.pulse" }, errors.SERVICE_RESOURCE},
		{"weight field exists", func(r *RakeRequest) { r.WeightField = "score" }, errors.PULSE_RAKE_INVALID},
		{"unknown category", func(r *RakeRequest) { r.Margins[1].Targets = map[string]float64{"F": .5, "X": .5} }, errors.PULSE_RAKE_UNKNOWN_CATEGORY},
		{"untargeted", func(r *RakeRequest) { r.Margins[2].Targets = map[string]float64{"NE": 30, "MW": 30, "S": 40} }, errors.PULSE_RAKE_UNTARGETED_CATEGORY},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := matPulse(t, Options{})
			buildRakeSource(t, p, "src.pulse", CohortBuilderOptions{})
			req := censusRake("src.pulse", "out.pulse")
			c.mut(&req)
			_, err := p.Rake(ctx, req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
			if ok, _ := afero.Exists(fsys, "out.pulse"); ok {
				t.Fatal("a refused rake left a cohort at out")
			}
		})
	}
}

// TestRake_MissingValueRefusedUnlessBase: a null margin value is
// refused by default; missing "base" keeps the base factor.
func TestRake_MissingValueRefusedUnlessBase(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	b, err := p.NewCohortBuilder(ctx, "src.pulse", rakeSchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rakeRows() {
		if i == 10 {
			r[2] = nil
		}
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = p.Rake(ctx, censusRake("src.pulse", "out.pulse"))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RAKE_MISSING_VALUE || ce.Details["first_row"] != int64(10) {
		t.Fatalf("got %v", err)
	}
	if ok, _ := afero.Exists(fsys, "out.pulse"); ok {
		t.Fatal("refused rake wrote out")
	}
	req := censusRake("src.pulse", "out.pulse")
	req.Missing = "base"
	res, err := p.Rake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Margins[2].NMissing != 1 || !res.Converged {
		t.Fatalf("n_missing %d converged %v", res.Margins[2].NMissing, res.Converged)
	}
}

// TestRake_OverwriteGuards: an existing target needs Overwrite (refused
// before any scan, the old cohort untouched); Overwrite also replaces an
// existing weight column in place.
func TestRake_OverwriteGuards(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildRakeSource(t, p, "src.pulse", CohortBuilderOptions{})
	if _, err := p.Rake(ctx, censusRake("src.pulse", "out.pulse")); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "out.pulse")
	_, err := p.Rake(ctx, censusRake("src.pulse", "out.pulse"))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("got %v", err)
	}
	if after, _ := afero.ReadFile(fsys, "out.pulse"); string(after) != string(before) {
		t.Fatal("refused overwrite changed the target")
	}
	// Re-rake the raked cohort into a new file, replacing its weight.
	req := censusRake("out.pulse", "again.pulse")
	req.WeightField = RakeDefaultWeightField
	req.Overwrite = true
	req.Margins = req.Margins[:2]
	res, err := p.Rake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := readAll(t, p, "again.pulse")
	if len(s.Fields) != 7 || s.Fields[6].Name != RakeDefaultWeightField || !strings.Contains(s.Fields[6].Description, "age; gender") ||
		strings.Contains(s.Fields[6].Description, "region") || res.WeightField != RakeDefaultWeightField {
		t.Fatalf("replaced weight %+v", s.Fields[6])
	}
}

// TestRake_ResultJSONUndefinedIsNull: an undefined efficiency figure is
// null on the wire, never 0, and the result round-trips its keys.
func TestRake_ResultJSONUndefinedIsNull(t *testing.T) {
	r := RakeResult{Efficiency: RakeEfficiency{N: 2, WeightRangeRatio: math.NaN(), DeffKish: math.NaN()}, Residual: 0}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"weight_range_ratio":null`, `"deff_kish":null`, `"converged":false`, `"weight_field":""`} {
		if !strings.Contains(s, want) {
			t.Errorf("%s lacks %s", s, want)
		}
	}
}
