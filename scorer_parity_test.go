package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// scorer_parity_test.go pins the one scoring path (model.RowScorer,
// custom-regressions E2-S1): every built-in scorable regression —
// REG_OLS (plain, ridge, lasso, forward selection), REG_GLM on each
// link, REG_BAYES_LINEAR — scores ATTR_REG_PREDICT rows (the whole
// model, `terms` subsets, `bound` lower / upper, GLM `scale`) exactly
// as the pre-RowScorer kernel did, bit for bit. The baseline is that
// kernel, frozen below as legacyScore and computed in-test from the
// fit the response itself reports (full float64: a FitResult is never
// rounded), never a stored golden. Each case also asserts that the
// whole response JSON is byte-identical across decode / shard worker
// counts and between the in-compose fit and its saved-and-reloaded
// model file, and the U42 fit-error fixture (AGG_MAPE / RMSE / MAE over
// a scored column) holds the same identities.

const (
	scorerParityCohort  = "scorer_parity.pulse"
	scorerParityRows    = 120
	scorerParityPerShrd = 40
)

// scorerParityRow is row i of the parity cohort: an id, two predictors
// and a candidate (x2 null on every 11th row, so a score reading it is
// null there), a continuous, a count and a 0/1 target, and a positive
// continuous target. Products are wrapped (float64(a*b)) so arm64 and
// amd64 generate the same inputs.
func scorerParityRow(i int) []string {
	fi := float64(i)
	x1 := float64(fi*0.07) - 4
	x2 := math.Sin(float64(0.37*fi) + 0.4)
	x3 := math.Cos(float64(1.3 * fi))
	y := 1.5 + float64(0.8*x1) - float64(1.2*x2) + float64(0.3*math.Sin(float64(1.9*fi)))
	c := math.Round(math.Exp(0.4+float64(0.15*x1)+float64(0.3*x2)) + float64(0.6*math.Sin(float64(0.71*fi))))
	if c < 0 {
		c = 0
	}
	b := 0.0
	if float64(0.9*x1)-float64(0.7*x2)+float64(1.4*math.Sin(float64(1.9*fi))) > 0 {
		b = 1
	}
	pos := math.Exp(0.2 + float64(0.05*x1) + float64(0.1*math.Sin(float64(0.53*fi))))
	x2s := jsonNumber(x2)
	if i%11 == 0 {
		x2s = ""
	}
	return []string{strconv.Itoa(i), jsonNumber(x1), x2s, jsonNumber(x3), jsonNumber(y), jsonNumber(c), jsonNumber(b), jsonNumber(pos)}
}

var scorerParityColumns = []string{"id", "x1", "x2", "x3", "y", "c", "b", "pos"}

// scorerParityMode is one execution mode: a Pulse instance and the
// cohort path it reads.
type scorerParityMode struct {
	name   string
	p      *Pulse
	cohort string
}

// scorerParityModes builds the cohort as one file (serial decode and 4
// decode workers) and as a 3-shard archive (1 and 3 shard workers).
// Responses compare byte for byte only within a storage (single file vs
// archive fold the fit in different orders).
func scorerParityModes(t *testing.T) (single, sharded []scorerParityMode) {
	t.Helper()
	var rows [][]string
	for i := 0; i < scorerParityRows; i++ {
		rows = append(rows, scorerParityRow(i))
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, scorerParityCohort, scorerParityColumns, rows)
	sfs := afero.NewMemMapFs()
	var shards []string
	for s := 0; s*scorerParityPerShrd < scorerParityRows; s++ {
		path := "s" + strconv.Itoa(s) + ".pulse"
		createTestPulseFile(t, sfs, path, scorerParityColumns, rows[s*scorerParityPerShrd:(s+1)*scorerParityPerShrd])
		shards = append(shards, path)
	}
	mk := func(fs afero.Fs, o Options) *Pulse {
		o.FS = fs
		p, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	single = []scorerParityMode{
		{"serial", mk(fsys, Options{DecodeWorkers: 1}), scorerParityCohort},
		{"decode_workers_4", mk(fsys, Options{DecodeWorkers: 4}), scorerParityCohort},
	}
	sp := mk(sfs, Options{ShardWorkers: 1})
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	sharded = []scorerParityMode{
		{"shard_workers_1", sp, "archive.pulse"},
		{"shard_workers_3", mk(sfs, Options{ShardWorkers: 3}), "archive.pulse"},
	}
	return single, sharded
}

// scorerParityFits are the scorable fits the parity covers.
func scorerParityFits() map[string]types.RegressionSpec {
	xs := []string{"x1", "x2"}
	return map[string]types.RegressionSpec{
		"ols":              {Type: types.REG_OLS, Target: "y", Predictors: xs},
		"ols_ridge":        {Type: types.REG_OLS, Target: "y", Predictors: xs, Penalty: "l2", Alpha: 0.5},
		"ols_lasso":        {Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2", "x3"}, Penalty: "l1", Alpha: 0.05},
		"ols_forward":      {Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2", "x3"}, Selection: "forward", Criterion: "bic"},
		"glm_poisson_log":  {Type: types.REG_GLM, Target: "c", Predictors: xs, Family: "poisson", Link: "log"},
		"glm_binomial":     {Type: types.REG_GLM, Target: "b", Predictors: xs, Family: "binomial", Link: "logit"},
		"glm_gamma":        {Type: types.REG_GLM, Target: "pos", Predictors: xs, Family: "gamma", Link: "inverse"},
		"bayes_linear":     {Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: xs},
		"ols_intercept_x1": {Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}},
	}
}

// scorerParityRef is one ATTR_REG_PREDICT reference of a case.
type scorerParityRef struct {
	label  string
	params scorerParams
}

type scorerParams struct {
	Fit   string   `json:"fit"`
	Terms []string `json:"terms,omitempty"`
	Scale string   `json:"scale,omitempty"`
	Bound string   `json:"bound,omitempty"`
}

// scorerParityRefs are the references scored against spec: the whole
// model, two terms subsets (intercept left out / kept), single-term
// lower / upper bounds when the fit reports intervals, and both GLM
// scales.
func scorerParityRefs(spec types.RegressionSpec, intervals bool) []scorerParityRef {
	refs := []scorerParityRef{{"p_all", scorerParams{Fit: "m"}}}
	if len(spec.Predictors) > 1 {
		refs = append(refs,
			scorerParityRef{"p_x1", scorerParams{Fit: "m", Terms: []string{"x1"}}},
			scorerParityRef{"p_icpt_x2", scorerParams{Fit: "m", Terms: []string{"x2", "(intercept)"}}},
		)
	}
	if intervals {
		refs = append(refs,
			scorerParityRef{"p_lo", scorerParams{Fit: "m", Terms: []string{"x1"}, Bound: "lower"}},
			scorerParityRef{"p_hi", scorerParams{Fit: "m", Terms: []string{"x1"}, Bound: "upper"}},
		)
	}
	// x3 is the candidate a lasso may zero (its bound is then null) or a
	// selection may drop (it then scores 0).
	if slices.Contains(spec.Predictors, "x3") {
		refs = append(refs, scorerParityRef{"p_x3", scorerParams{Fit: "m", Terms: []string{"x3"}}})
		if intervals {
			refs = append(refs, scorerParityRef{"p_x3_hi", scorerParams{Fit: "m", Terms: []string{"x3"}, Bound: "upper"}})
		}
	}
	if spec.Type == types.REG_GLM {
		refs = append(refs,
			scorerParityRef{"p_link", scorerParams{Fit: "m", Scale: "link"}},
			scorerParityRef{"p_resp_x1", scorerParams{Fit: "m", Terms: []string{"(intercept)", "x1"}, Scale: "response"}},
		)
	}
	return refs
}

// scorerParitySlot scores every ref on each row (one id per group) and
// reports the predictors beside the scores, so the oracle reads the
// very values the scorer read.
func scorerParitySlot(cohort string, refs []scorerParityRef, models []types.ModelRef) *Request {
	req := &Request{
		Label:  "score",
		Cohort: &types.Cohort{Filename: cohort},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "id", Interval: 1}},
	}
	for _, col := range []string{"x1", "x2", "x3"} {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MIN, Field: col, Label: col})
	}
	for _, r := range refs {
		raw, _ := json.Marshal(r.params)
		req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: r.label, Params: raw})
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MIN, Field: r.label, Label: r.label})
	}
	return req
}

func scorerParityFitRequest(cohort string, spec types.RegressionSpec) *Request {
	spec.Name = "m"
	return &Request{
		Cohort:      &types.Cohort{Filename: cohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "89"}}},
		Regressions: []*types.RegressionSpec{&spec},
	}
}

// legacyScore is the pre-RowScorer scoring kernel (regression.Scorer
// before custom-regressions E2-S1), frozen: intercept (unless `terms`
// leaves it out) + Σ coef·x (regression.LinearPredictor) in the FIT's
// predictor order, coef the CI bound under `bound`, a bound the fit lacks null,
// then the GLM inverse link unless `scale` is link. ok=false is a null
// score.
func legacyScore(t *testing.T, res *types.RegressionResult, terms []string, p scorerParams, row map[string]any) (float64, bool) {
	t.Helper()
	coefs := res.Coefficients
	if p.Bound == "lower" {
		coefs = res.CILower
	} else if p.Bound == "upper" {
		coefs = res.CIUpper
	}
	keep := func(term string) bool { return p.Terms == nil || slices.Contains(p.Terms, term) }
	s := 0.0
	if keep("(intercept)") {
		s = res.Coefficients["(intercept)"]
	}
	var cs, xs []float64
	for _, pred := range terms {
		if !keep(pred) {
			continue
		}
		c, ok := coefs[pred]
		if !ok {
			c = math.NaN()
		}
		cs = append(cs, c)
		x, ok := row[pred].(float64)
		if !ok || math.IsNaN(x) {
			return 0, false
		}
		xs = append(xs, x)
	}
	if p.Bound == "lower" || p.Bound == "upper" {
		if len(cs) == 0 || math.IsNaN(cs[0]) {
			return 0, false
		}
	}
	// The summation kernel itself is unchanged by the migration; reading
	// it keeps the oracle's multiply-add fusion the scorer's on every
	// architecture.
	s = regression.LinearPredictor(s, cs, xs)
	if res.Type == types.REG_GLM && p.Scale != "link" {
		inv, err := regression.InverseLink(res.Family, res.Link)
		if err != nil {
			t.Fatal(err)
		}
		return inv(s), true
	}
	return s, true
}

func scorerParityJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := types.MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func scorerSameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

// TestRowScorer_BuiltinScoresByteIdentical: per fit, per mode, every
// reference's per-row score equals legacyScore bit for bit (nulls where
// legacyScore is null); the response JSON is byte-identical across the
// worker counts of a storage; a model saved by the fit and reloaded by
// a later Process scores the same JSON; a fit without intervals refuses
// `bound` with PULSE_MODEL_INTERVALS_UNAVAILABLE.
func TestRowScorer_BuiltinScoresByteIdentical(t *testing.T) {
	t.Parallel()

	single, sharded := scorerParityModes(t)
	ctx := context.Background()
	for name, spec := range scorerParityFits() {
		t.Run(name, func(t *testing.T) {
			for _, storage := range [][]scorerParityMode{single, sharded} {
				var baseline string
				for _, mode := range storage {
					fit := scorerParityFitRequest(mode.cohort, spec)
					probe, err := mode.p.Compose(ctx, &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: fit}},
						Requests: []*Request{{Label: "n", Cohort: &types.Cohort{Filename: mode.cohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}}}})
					if err != nil {
						t.Fatalf("%s: %v", mode.name, err)
					}
					res := probe.Fits["m"].RegressionResult
					intervals := res.ConfLevel > 0
					refs := scorerParityRefs(spec, intervals)
					// A fresh path per mode: an overwrite would add a
					// warning the first mode's response lacks.
					save := "models/" + name + "_" + mode.name + ".pulse-model.json"
					out, err := mode.p.Compose(ctx, &ComposedRequest{
						Fits:     []types.FitSpec{{Name: "m", Request: fit, Save: save}},
						Requests: []*Request{scorerParitySlot(mode.cohort, refs, nil)},
					})
					if err != nil {
						t.Fatalf("%s: %v", mode.name, err)
					}
					res = out.Fits["m"].RegressionResult
					terms := spec.Predictors
					if spec.Selection != "" {
						terms = res.SelectedFeatures
					}
					data := out.Responses[0].Data
					if len(data) != scorerParityRows {
						t.Fatalf("%s: %d rows, want %d", mode.name, len(data), scorerParityRows)
					}
					nulls := 0
					for _, row := range data {
						for _, r := range refs {
							want, wantOK := legacyScore(t, res, terms, r.params, row)
							got, gotOK := row[r.label].(float64)
							if gotOK && math.IsNaN(got) {
								gotOK = false
							}
							if wantOK != gotOK || (wantOK && !scorerSameBits(got, want)) {
								t.Fatalf("%s row %v %s: got %v (%v), legacy %v (%v)", mode.name, row["id"], r.label, row[r.label], gotOK, want, wantOK)
							}
							if !gotOK {
								nulls++
							}
						}
					}
					if nulls == 0 && slices.Contains(terms, "x2") {
						t.Fatalf("%s: no null score; the fixture's null x2 rows never reached a scorer", mode.name)
					}
					js := scorerParityJSON(t, out)
					if baseline == "" {
						baseline = js
					} else if js != baseline {
						t.Fatalf("%s: response JSON differs from %s", mode.name, storage[0].name)
					}

					loaded, err := mode.p.Process(ctx, scorerParitySlot(mode.cohort, refs, []types.ModelRef{{Name: "m", Path: save}}))
					if err != nil {
						t.Fatalf("%s loaded: %v", mode.name, err)
					}
					if a, b := scorerParityJSON(t, loaded.Data), scorerParityJSON(t, out.Responses[0].Data); a != b {
						t.Fatalf("%s: the reloaded model scores differently from the in-compose fit", mode.name)
					}

					if !intervals {
						_, err := mode.p.Compose(ctx, &ComposedRequest{
							Fits:     []types.FitSpec{{Name: "m", Request: fit}},
							Requests: []*Request{scorerParitySlot(mode.cohort, []scorerParityRef{{"p_lo", scorerParams{Fit: "m", Terms: []string{"x1"}, Bound: "lower"}}}, nil)},
						})
						var ce *perr.CodedError
						if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_INTERVALS_UNAVAILABLE {
							t.Fatalf("%s: bound on a fit without intervals: %v, want PULSE_MODEL_INTERVALS_UNAVAILABLE", mode.name, err)
						}
					}
				}
			}
		})
	}
}

// TestRowScorer_FitErrorFixtureByteIdentical: the U42 fit-error slot
// (AGG_MAPE / RMSE / MAE over an ATTR_REG_PREDICT column, train and
// holdout buckets) answers byte-identical JSON under serial and
// parallel decode and from the saved-and-reloaded model, and every
// score it reads is legacyScore's.
func TestRowScorer_FitErrorFixtureByteIdentical(t *testing.T) {
	t.Parallel()

	fsys := merrFS(t)
	ctx := context.Background()
	fit := &Request{
		Cohort:      &types.Cohort{Filename: merrCohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week_index", Values: []string{"0", "90"}}},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}}},
	}
	var baseline string
	for _, workers := range []int{1, 4} {
		save := "models/merr_" + strconv.Itoa(workers) + ".pulse-model.json"
		p, err := New(Options{FS: fsys, DecodeWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Compose(ctx, &ComposedRequest{
			Fits:     []types.FitSpec{{Name: "m", Request: fit, Save: save}},
			Requests: []*Request{merrSlot()},
		})
		if err != nil {
			t.Fatal(err)
		}
		js := scorerParityJSON(t, out)
		if baseline == "" {
			baseline = js
		} else if js != baseline {
			t.Fatalf("decode workers %d: response JSON differs from serial:\n%s\n%s", workers, js, baseline)
		}
		loadedSlot := merrSlot()
		loadedSlot.Models = []types.ModelRef{{Name: "m", Path: save}}
		loaded, err := p.Process(ctx, loadedSlot)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := scorerParityJSON(t, loaded.Data), scorerParityJSON(t, out.Responses[0].Data); a != b {
			t.Fatalf("decode workers %d: reloaded model's fit errors %s, in-compose %s", workers, a, b)
		}

		perRow := &Request{
			Label:        "rows",
			Cohort:       &types.Cohort{Filename: merrCohort},
			Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "pred", Params: json.RawMessage(`{"fit":"m"}`)}},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "week_index", Interval: 1}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "x", Label: "x"}, {Type: types.AGG_MIN, Field: "pred", Label: "pred"}},
		}
		rows, err := p.Compose(ctx, &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: fit}}, Requests: []*Request{perRow}})
		if err != nil {
			t.Fatal(err)
		}
		res := rows.Fits["m"].RegressionResult
		if len(rows.Responses[0].Data) != 104 {
			t.Fatalf("%d scored weeks, want 104", len(rows.Responses[0].Data))
		}
		for _, row := range rows.Responses[0].Data {
			want, _ := legacyScore(t, res, []string{"x"}, scorerParams{Fit: "m"}, row)
			if got, _ := row["pred"].(float64); !scorerSameBits(got, want) {
				t.Fatalf("week %v: pred %v, legacy %v", row["week_index"], got, want)
			}
		}
	}
}
