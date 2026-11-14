package regmmm

import (
	"context"
	stderrors "errors"
	"math"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// bayes_draws_test.go holds params.return_draws / params.draws_thin to
// their contract inside the package: the spec refusals (predict's too),
// the seed they never move, the payload count against the tables it
// counts, the Limits.MaxMMMDrawValues refusal before any row is read,
// the outputs.draws shape and thinning, and a null draw on the wire
// that reads back as NaN. The fit-level checks (draws reproduce the
// summaries, absent is byte-identical) are bayes_draws_heavy_test.go's.

// TestMMMBayes_DrawsSpecRefusals: draws_thin outside [1, 20000], or set
// without return_draws true, is PROCESSING_CONFIG slot params.draws_thin
// from the spec alone — the error predict raises (BayesSpecRefusal).
func TestMMMBayes_DrawsSpecRefusals(t *testing.T) {
	t.Parallel()

	const base = `{"channels":["tv"],"time":"week"`
	for name, tc := range map[string]struct {
		params, reason string
	}{
		"thin 0":                  {base + `,"return_draws":true,"draws_thin":0}`, ""},
		"thin past the cap":       {base + `,"return_draws":true,"draws_thin":20001}`, ""},
		"thin without draws":      {base + `,"draws_thin":2}`, reasonDrawsThinWithoutDraws},
		"thin with draws false":   {base + `,"return_draws":false,"draws_thin":2}`, reasonDrawsThinWithoutDraws},
		"thin not an integer":     {base + `,"return_draws":true,"draws_thin":2.5}`, ""},
		"return_draws not a bool": {base + `,"return_draws":"yes"}`, ""},
	} {
		spec := BayesSpec(tc.params)
		_, err := BayesFactory(spec, nil)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
			t.Errorf("%s: %v, want PROCESSING_CONFIG", name, err)
			continue
		}
		if r, _ := ce.Details["reason"].(string); r != tc.reason {
			t.Errorf("%s: reason %q, want %q", name, r, tc.reason)
		}
		if p := BayesSpecRefusal(spec); p == nil || p.Message != ce.Message {
			t.Errorf("%s: predict %v, runtime %v", name, p, ce)
		}
	}
	for _, ok := range []string{base + `,"return_draws":true,"draws_thin":1}`, base + `,"return_draws":true,"draws_thin":20000}`, base + `,"return_draws":false}`} {
		if _, err := BayesFactory(BayesSpec(ok), nil); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

// TestMMMBayes_DrawsNeverMoveSeed: at rng_seed 0, return_draws and
// draws_thin are left out of the derived seed — asking for the draws
// samples the same posterior — while a spec without them derives the
// seed it always has.
func TestMMMBayes_DrawsNeverMoveSeed(t *testing.T) {
	t.Parallel()

	plain := bayesEffectiveSeed(BayesSpec(`{"channels":["tv"],"time":"week","draws":30}`))
	for _, params := range []string{
		`{"channels":["tv"],"time":"week","draws":30,"return_draws":true}`,
		`{"return_draws":true,"draws_thin":3,"channels":["tv"],"time":"week","draws":30}`,
		`{"channels":["tv"],"time":"week","draws":30,"return_draws":false}`,
	} {
		if got := bayesEffectiveSeed(BayesSpec(params)); got != plain {
			t.Errorf("%s: seed %d, want the draws-free %d", params, got, plain)
		}
	}
	raw := []byte(`{"draws":30, "channels":["tv"]}`)
	if got := withoutOutputOnlyParams(raw); string(got) != string(raw) {
		t.Errorf("params without output-only keys rewritten: %s", got)
	}
}

// TestMMMBayes_DrawValuesCountTables: the payload count is chains × kept
// draws (⌈draws / thin⌉) × (coefficient-table length + roi entries) —
// the tables the fit reports, for every kernel and curve, one market and
// a geo panel with and without per-geo curves.
func TestMMMBayes_DrawValuesCountTables(t *testing.T) {
	t.Parallel()

	for _, tail := range []string{
		`}`,
		`,"adstock":"delayed","max_lag":4}`,
		`,"adstock":"weibull_pdf","max_lag":4,"saturation":"logistic"}`,
		`,"saturation":"michaelis_menten"}`,
	} {
		for _, params := range []string{
			`{"channels":["tv","search"],"time":"week","chains":3,"draws":10,"return_draws":true,"draws_thin":4` + tail,
			geoParams + `,"chains":3,"draws":10,"return_draws":true,"draws_thin":4` + tail,
			geoParams + `,"per_geo_curves":true,"chains":3,"draws":10,"return_draws":true,"draws_thin":4` + tail,
		} {
			spec := geoSpec(params)
			spec.Predictors = []string{"tv", "search", "price"}
			reg, err := BayesFactory(spec, nil)
			if err != nil {
				t.Fatalf("%s: %v", params, err)
			}
			r := reg.(*bayesRegression)
			for _, g := range []int64{1, 3, 7} {
				coefs, roi := r.coefNames(), r.cfg.channels
				if r.cfg.geo != "" {
					geos := make([]string, g)
					for i := range geos {
						geos[i] = string(rune('A' + i))
					}
					coefs, roi = r.geoCoefNames(geos), r.geoEffectNames(geos)
				}
				want := int64(3 * 3 * (len(coefs) + len(roi))) // ⌈10 / 4⌉ = 3 kept draws
				got, geo, ok := BayesDrawValues(spec, g)
				if !ok || geo != (r.cfg.geo != "") || got != want {
					t.Errorf("%s over %d geos: BayesDrawValues %d (geo %v, ok %v), want %d", params, g, got, geo, ok, want)
				}
			}
		}
	}
	if _, _, ok := BayesDrawValues(BayesSpec(`{"channels":["tv"],"time":"week"}`), 0); ok {
		t.Error("a spec without return_draws reports a draws payload")
	}
	if _, _, ok := BayesDrawValues(BayesSpec(`{"channels":["tv"],"time":"week","return_draws":true,"draws_thin":0}`), 0); ok {
		t.Error("a refused spec reports a draws payload")
	}
}

// TestMMMBayes_DrawLimitRefusesBeforeReading: over
// Limits.MaxMMMDrawValues (from the fit's context) the fit is
// PULSE_LIMIT_EXCEEDED naming the limit with the payload as observed —
// on one market before a row is read (a short input is never even
// counted), on a geo panel over the geos it read; at the limit it fits.
func TestMMMBayes_DrawLimitRefusesBeforeReading(t *testing.T) {
	t.Parallel()

	trip := func(t *testing.T, err error, observed int64) {
		t.Helper()
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != string(limits.MaxMMMDrawValues) ||
			ce.Details["observed"] != observed {
			t.Fatalf("err %v (details %v), want PULSE_LIMIT_EXCEEDED max_mmm_draw_values observed %d", err, ce, observed)
		}
	}
	at := func(n int64) context.Context {
		l := limits.Defaults()
		l.MaxMMMDrawValues = n
		return limits.WithContext(context.Background(), l)
	}

	const one = `{"channels":["tv"],"time":"week","chains":1,"warmup":10,"draws":5,"return_draws":true,"draws_thin":2}`
	n, _, _ := BayesDrawValues(BayesSpec(one), 0)
	if n != 1*3*(7+1) {
		t.Fatalf("one-market payload %d, want 24", n)
	}
	reg, err := BayesFactory(BayesSpec(one), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctxReg := reg.(extend.ContextRegression)
	// A row with no column: reading it would be a time refusal.
	_, err = ctxReg.FitContext(at(n-1), append(digestRows(), probeRow{}))
	trip(t, err, n)
	if _, err := ctxReg.FitContext(at(n), digestRows()); err != nil {
		t.Fatalf("at the limit: %v", err)
	}

	gspec := geoSpec(geoParams + `,"chains":1,"warmup":10,"draws":5,"return_draws":true}`)
	geos := map[float64]bool{}
	for _, r := range geoShortRows() {
		geos[r["geo"]] = true
	}
	gn, _, _ := BayesDrawValues(gspec, int64(len(geos)))
	greg, err := BayesFactory(gspec, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = greg.(extend.ContextRegression).FitContext(at(gn-1), geoShortRows())
	trip(t, err, gn)
}

// TestMMMBayes_DrawsOutputShape: outputs.draws carries the table's
// names, draws[chain][coefficient][kept draw] and roi[entry][chain][kept
// draw], every thin-th draw from draw 0, and thin; a NaN draw is null on
// the wire and reads back as NaN.
func TestMMMBayes_DrawsOutputShape(t *testing.T) {
	t.Parallel()

	r := &bayesRegression{cfg: &bayesConfig{returnDraws: true, drawsThin: 3}}
	seq := func(base float64) []float64 {
		out := make([]float64, 7)
		for i := range out {
			out[i] = base + float64(i)
		}
		return out
	}
	post := &bayesPosterior{
		coefs: [][][]float64{{seq(0), seq(10)}, {seq(100), seq(110)}},
		roi:   [][][]float64{{seq(1000)}, {seq(2000)}},
	}
	post.coefs[1][1][3] = math.NaN()
	out := r.drawsOutput(post, []string{"a", "b"}, []string{"tv"})
	draws := out["draws"].([][][]float64)
	if !slices.Equal(draws[0][1], []float64{10, 13, 16}) || !slices.Equal(draws[1][0], []float64{100, 103, 106}) {
		t.Fatalf("thinned draws %v, want every third from draw 0", draws)
	}
	roi := out["roi"].(map[string]any)["tv"].([][]float64)
	if !slices.Equal(roi[1], []float64{2000, 2003, 2006}) || out["thin"] != 3 || !slices.Equal(out["names"].([]string), []string{"a", "b"}) {
		t.Fatalf("draws output %v", out)
	}
	body, err := types.MarshalFinite(out)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Draws [][][]float64          `json:"draws"`
		ROI   map[string][][]float64 `json:"roi"`
	}
	if err := jsonfinite.Unmarshal(body, &back); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if !math.IsNaN(back.Draws[1][1][1]) || back.Draws[1][1][0] != 110 || back.ROI["tv"][0][2] != 1006 {
		t.Fatalf("read back %v from %s, want the NaN draw NaN", back.Draws, body)
	}

	r.cfg.drawsThin = 1
	full := r.drawsOutput(post, []string{"a", "b"}, []string{"tv"})["draws"].([][][]float64)
	if !slices.Equal(full[0][0], seq(0)) {
		t.Fatalf("thin 1 keeps %v, want every draw", full[0][0])
	}
}
