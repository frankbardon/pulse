package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// adding_postfit_recipe_test.go pins docs/src/internals/adding-post-fit.md:
// the recipe's example post-fit operator (POSTFIT_ACME_MROI, a
// marginal-ROI grid over a fitted REG_MMM_HILL) is THIS file's code —
// every ```go block of the page appears here verbatim
// (TestAddingPostFitRecipe_GoBlocksAreTested) — and the page's embedder
// example (its ```json block) validates at pulse.New, is served with
// its `_meta.operators` tag, runs through Compose and agrees with the
// engine's own response() reader (TestAddingPostFitRecipe_ExampleRuns).
// Pulse ships no built-in post-fit operator, so the shipped example
// library (and TestEveryOperatorHasAnExampleTag) cannot carry one: an
// embedder ships its example through Extensions.Examples, as here.

const postFitRecipeDoc = "docs/src/internals/adding-post-fit.md"

// recipe:begin

// marginalROI is POSTFIT_ACME_MROI. For each per-period spend level in
// params.spend it reads the channel's Hill curve off one REG_MMM_HILL
// fit and returns the response and the marginal return of one more unit
// of spend. Adstock carries spend forward, so a steady spend x reaches
// the curve as a = m·x, m the sum of the channel's adstock weights (1
// without an adstock record).
func marginalROI(in extend.PostFitInput) (extend.PostFitOutput, error) {
	var p struct {
		Channel  string    `json:"channel"`
		Spend    []float64 `json:"spend"`
		RefSlot  string    `json:"reference_slot"`
		RefValue string    `json:"reference_aggregation"`
	}
	if err := json.Unmarshal(in.Params(), &p); err != nil {
		return extend.PostFitOutput{}, err
	}
	if len(in.Fits()) != 1 {
		return extend.PostFitOutput{}, fmt.Errorf("name exactly one fit, not %d", len(in.Fits()))
	}
	fit := in.Fit(in.Fits()[0])
	c := fit.Coefficients() // a copy, full precision
	beta, okB := c[p.Channel]
	k, okK := c["k:"+p.Channel]
	s, okS := c["s:"+p.Channel]
	if !okB || !okK || !okS {
		return extend.PostFitOutput{}, fmt.Errorf("fit %q has no Hill curve for %q", fit.Name(), p.Channel)
	}
	m := 1.0
	if ad, ok := fit.Adstock(p.Channel); ok {
		m = 0
		for _, w := range ad.Weights {
			m += w
		}
	}
	// curve is the response β·hill(m·x) and its slope per unit of x:
	// with r = (k / (m·x))^s, hill = 1/(1+r) and the slope is
	// β·s·r / (x·(1+r)²). float64() keeps every product unfused, so the
	// figures are the same bits on every architecture.
	curve := func(x float64) (resp, slope float64) {
		r := detmath.Pow(k/float64(m*x), s)
		return beta / (1 + r), float64(beta*float64(s*r)) / float64(x*float64((1+r)*(1+r)))
	}
	grid := &types.PostFitTable{Columns: []string{"spend", "response", "marginal_roi", "vs_half_saturation"}}
	for _, x := range p.Spend {
		if !(x > 0) {
			return extend.PostFitOutput{}, fmt.Errorf("spend level %v is not positive", x)
		}
		resp, slope := curve(x)
		side := "below"
		if float64(m*x) >= k {
			side = "above"
		}
		grid.Rows = append(grid.Rows, []any{x, resp, slope, side})
	}
	atRef := math.NaN() // null on the wire without a reference
	if p.RefSlot != "" {
		x, err := in.Result(p.RefSlot, p.RefValue)
		if err != nil {
			return extend.PostFitOutput{}, err
		}
		_, atRef = curve(x)
	}
	return extend.PostFitOutput{
		Scalars: map[string]float64{"beta": beta, "half_saturation_spend": k / m, "mroi_at_reference": atRef},
		Tables:  map[string]*types.PostFitTable{"grid": grid},
	}, nil
}

// recipe:end

// recipe:begin

// mroiRegistration declares POSTFIT_ACME_MROI: its params (checked at
// predict and at run time before the operator runs), its exact outputs
// and its guidance.
func mroiRegistration() pulse.PostFitRegistration {
	return pulse.PostFitRegistration{
		Name:        "POSTFIT_ACME_MROI",
		Description: "marginal-ROI grid over a fitted REG_MMM_HILL channel",
		Purpose: &descriptor.Purpose{
			Plain:   "Shows what one more unit of spend returns at each spend level of a fitted media-mix channel.",
			Intents: []string{"drivers"},
			Questions: []string{
				"What does one more unit of spend return at each level?",
				"Where does the channel start to saturate?",
			},
			UseCases: map[descriptor.Domain]string{descriptor.DomainOps: "Spend planning from a weekly media-mix model."},
			NotFor: []descriptor.Alternative{
				{When: "you need the spend split that maximises the total return", Use: "capability:compose_optimize"},
			},
			Level: descriptor.LevelIntermediate,
		},
		Params: []pulse.ParamMeta{
			{Name: "channel", JSONType: "string", Required: true, Description: "the Hill channel to read"},
			{Name: "spend", JSONType: "array", Required: true, Description: "per-period spend levels, each positive"},
			{Name: "reference_slot", JSONType: "string", Description: "slot whose value is the reference spend"},
			{Name: "reference_aggregation", JSONType: "string", Description: "that slot's aggregation label"},
		},
		Outputs: extend.PostFitSchema{
			Scalars: []string{"beta", "half_saturation_spend", "mroi_at_reference"},
			Tables: []extend.PostFitTableSchema{{Name: "grid", Columns: []extend.PostFitColumn{
				{Name: "spend", Kind: extend.PostFitFloat},
				{Name: "response", Kind: extend.PostFitFloat},
				{Name: "marginal_roi", Kind: extend.PostFitFloat},
				{Name: "vs_half_saturation", Kind: extend.PostFitString},
			}}},
		},
		Run: marginalROI,
	}
}

// recipe:end

// recipe:begin

// newPulse registers the operator and ships its examples (an fs.FS of
// top-level .json files — your embed.FS) on the instance.
func newPulse(opts pulse.Options, examples fs.FS) (*pulse.Pulse, error) {
	opts.Extensions.PostFits = append(opts.Extensions.PostFits, mroiRegistration())
	opts.Extensions.Examples = examples
	return pulse.New(opts)
}

// recipe:end

// TestAddingPostFitRecipe_GoBlocksAreTested: every ```go block of the
// recipe is one of this file's recipe regions verbatim (tabs read as
// four spaces), and every region appears in the recipe — so the page
// teaches exactly the code TestAddingPostFitRecipe_ExampleRuns runs.
func TestAddingPostFitRecipe_GoBlocksAreTested(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(postFitRecipeDoc)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.ReadFile("internal/integration/adding_postfit_recipe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var regions []string
	for _, m := range regexp.MustCompile(`(?s)// recipe:begin\n\n(.*?)\n// recipe:end`).FindAllSubmatch(self, -1) {
		regions = append(regions, strings.ReplaceAll(string(m[1]), "\t", "    "))
	}
	blocks := regexp.MustCompile("(?s)```go\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(regions) != 3 || len(blocks) != len(regions) {
		t.Fatalf("%d go blocks in the recipe, %d recipe regions here; want 3 of each", len(blocks), len(regions))
	}
	for i, b := range blocks {
		if string(b[1]) != regions[i] {
			t.Errorf("go block %d of %s differs from recipe region %d:\n--- doc\n%s\n--- test\n%s", i+1, postFitRecipeDoc, i+1, b[1], regions[i])
		}
	}
}

// postFitRecipeExample is the recipe's embedder example file (its
// first ```json block), the cohort renamed onto the fixture.
func postFitRecipeExample(t *testing.T) []byte {
	t.Helper()
	doc, err := os.ReadFile(postFitRecipeDoc)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```json[^\n]*\n(.*?)```").FindSubmatch(doc)
	if m == nil {
		t.Fatal("recipe has no json example block")
	}
	return m[1]
}

// mediaSeries is 104 weeks of bursty spend x and y = 5 + 8·hill(a, 60,
// 2) + a small deterministic wobble, a x's geometric adstock at decay
// 0.6 over lags 0..6.
func mediaSeries() (x, y []float64) {
	const n = 104
	x, y = make([]float64, n), make([]float64, n)
	for i := range x {
		if (i*37)%17 > 7 {
			x[i] = float64((i*53)%29)*4 + 10
		}
	}
	for i := range n {
		a := 0.0
		for l := 0; l <= 6 && l <= i; l++ {
			a += float64(detmath.Pow(0.6, float64(l)) * x[i-l])
		}
		h := 0.0
		if a > 0 {
			h = 1 / (1 + detmath.Pow(60/a, 2))
		}
		y[i] = float64(8*h) + 5 + float64(0.01*float64((i*13)%7-3))
	}
	return x, y
}

// writeMediaCohort writes mediaSeries as a u32 week index and f64 x, y.
func writeMediaCohort(t *testing.T, fsys afero.Fs, path string) {
	t.Helper()
	fields := []encoding.Field{{Name: "week", Type: encoding.FieldTypeU32}, {Name: "x", Type: encoding.FieldTypeF64}, {Name: "y", Type: encoding.FieldTypeF64}}
	off := 0
	for i := range fields {
		fields[i].ByteOffset, fields[i].CsvColumnIdx = off, i
		off += fields[i].Type.ByteSize()
	}
	s := &encoding.Schema{Fields: fields}
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	x, y := mediaSeries()
	for i := range x {
		buf = append(buf, byte(i), byte(i>>8), 0, 0)
		for _, v := range []float64{x[i], y[i]} {
			b := math.Float64bits(v)
			for k := 0; k < 8; k++ {
				buf = append(buf, byte(b>>(8*k)))
			}
		}
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAddingPostFitRecipe_ExampleRuns: the recipe's example validates
// at pulse.New beside the registration and is served with its
// `_meta.operators` tag; its body predicts clean and runs through
// Compose, the grid's response equal to response("mmm", "x", spend) and
// its marginal ROI to that reader's central difference; a non-positive
// spend fails the entry alone; Explain describes the entry and reads
// its result.
func TestAddingPostFitRecipe_ExampleRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeMediaCohort(t, fsys, "media.pulse")
	p, err := newPulse(pulse.Options{FS: fsys}, fstest.MapFS{"acme-mroi-grid.json": {Data: postFitRecipeExample(t)}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ex, ok := p.ExampleGet("acme-mroi-grid")
	if !ok || !slices.Contains(ex.Operators, "POSTFIT_ACME_MROI") {
		t.Fatalf("example not served with its post-fit tag: %+v", ex)
	}
	var c pulse.ComposedRequest
	if err := json.Unmarshal(ex.Body, &c); err != nil {
		t.Fatalf("example body: %v", err)
	}
	// An oracle slot: the engine's response() reader at each spend
	// level and either side of it, and at the reference (mean) spend.
	spends := []float64{20, 40, 60, 80, 100}
	const h = 1e-3
	oracle := &types.Request{Label: "oracle", Cohort: &types.Cohort{Filename: "media.pulse"}}
	at := func(label string, x float64) {
		oracle.Attributes = append(oracle.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Label: label,
			Expression: fmt.Sprintf(`response("mmm", "x", %.17g)`, x)})
		oracle.Aggregations = append(oracle.Aggregations, &types.Aggregation{Type: types.AGG_MAX, Field: label, Label: label})
	}
	for i, x := range spends {
		at(fmt.Sprintf("r%d", i), x)
		at(fmt.Sprintf("up%d", i), x+h)
		at(fmt.Sprintf("dn%d", i), x-h)
	}
	run := c
	run.Requests = append(slices.Clone(c.Requests), oracle)

	env, err := p.PredictCompose(ctx, &run)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors[0])
	}
	resp, err := p.Compose(ctx, &run)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(resp.PostFit) != 1 || resp.PostFit[0].Error != nil {
		t.Fatalf("post_fit %+v", resp.PostFit)
	}
	r := resp.PostFit[0]
	value := func(label string) float64 {
		t.Helper()
		v, ok := resp.Responses[1].Data[0][label].(float64)
		if !ok {
			t.Fatalf("oracle %s: %v", label, resp.Responses[1].Data[0][label])
		}
		return v
	}
	grid := r.Tables["grid"]
	if grid == nil || len(grid.Rows) != len(spends) {
		t.Fatalf("grid %+v", grid)
	}
	near := func(what string, got, want, rel float64) {
		t.Helper()
		if math.Abs(got-want) > rel*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %.12g, want %.12g", what, got, want)
		}
	}
	prev := math.Inf(1)
	for i, row := range grid.Rows {
		if row[0] != spends[i] {
			t.Fatalf("row %d spend %v", i, row[0])
		}
		near(fmt.Sprintf("response at %v", spends[i]), row[1].(float64), value(fmt.Sprintf("r%d", i)), 1e-9)
		fd := (value(fmt.Sprintf("up%d", i)) - value(fmt.Sprintf("dn%d", i))) / (2 * h)
		near(fmt.Sprintf("marginal ROI at %v", spends[i]), row[2].(float64), fd, 1e-5)
		if s := row[2].(float64); s > prev && spends[i] > 60 {
			t.Errorf("marginal ROI rises past saturation: %v after %v", s, prev)
		} else {
			prev = s
		}
		if side := row[3].(string); side != "below" && side != "above" {
			t.Errorf("row %d side %q", i, side)
		}
	}
	if b := resp.Fits["mmm"].RegressionResult.Coefficients["x"]; r.Scalars["beta"] != b {
		t.Errorf("beta %v, fit %v", r.Scalars["beta"], b)
	}
	if v := r.Scalars["mroi_at_reference"]; math.IsNaN(v) || v <= 0 {
		t.Errorf("mroi_at_reference = %v", v)
	}

	// A non-positive spend fails the entry alone.
	bad := c
	bad.PostFit = []types.PostFitSpec{c.PostFit[0]}
	bad.PostFit[0].Params = json.RawMessage(`{"channel":"x","spend":[0]}`)
	badResp, err := p.Compose(ctx, &bad)
	if err != nil {
		t.Fatalf("a failed entry failed the batch: %v", err)
	}
	if e := badResp.PostFit[0].Error; e == nil || e.Code != string(perr.PULSE_POSTFIT_FAILED) {
		t.Errorf("bad entry error %+v", badResp.PostFit[0])
	}

	// Explain names the entry, its fit and its params, then reads it.
	ereq, err := p.Explain(ctx, descriptor.ExplainRequest{Composed: &c})
	if err != nil {
		t.Fatal(err)
	}
	var step string
	for _, s := range ereq.Steps {
		if s.Slot == "post_fit[0]" {
			step = s.Text
		}
	}
	for _, want := range []string{"runs POSTFIT_ACME_MROI", "over the fit `mmm`", "`channel` = \"x\"", "`spend` (5 values)", "Shows what one more unit of spend returns"} {
		if !strings.Contains(step, want) {
			t.Errorf("explain step lacks %q: %q", want, step)
		}
	}
	eresp, err := p.Explain(ctx, descriptor.ExplainRequest{ComposedResponse: resp, Composed: &run})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range eresp.Findings {
		if f.Slot == "post_fit[0]" && f.Subject == "mroi" && f.Numbers["tables.grid.rows"] != nil && *f.Numbers["tables.grid.rows"] == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("no post_fit[0] finding: %+v", eresp.Findings)
	}
}
