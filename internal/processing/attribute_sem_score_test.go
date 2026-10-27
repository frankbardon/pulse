package processing

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// semScoreTestdata holds the brand-survey fixture and lavaan's
// lavPredict references (internal/sem/testdata/predict_lavaan.R).
const semScoreTestdata = "../sem/testdata/"

// semScoreRef is a predict_lavaan.R reference: both methods' scores of
// every stride-th data row (0-based), per group.
type semScoreRef struct {
	Lavaan  string   `json:"lavaan"`
	Latents []string `json:"latents"`
	Groups  []struct {
		Label      string      `json:"label"`
		Rows       []int       `json:"rows"`
		Regression [][]float64 `json:"regression"`
		Bartlett   [][]float64 `json:"bartlett"`
	} `json:"groups"`
}

func loadSEMScoreRef(t *testing.T, name string) semScoreRef {
	t.Helper()
	b, err := os.ReadFile(semScoreTestdata + name + "_lavaan.json")
	if err != nil {
		t.Fatal(err)
	}
	var r semScoreRef
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Lavaan == "" || len(r.Groups) == 0 {
		t.Fatalf("%s: no lavaan release or no scores", name)
	}
	return r
}

// brandSEMFixture is the brand-survey CSV as records: every rating an
// f64, wave categorical (dictionary "1", "2" in that order — the extra
// levels, in order, after them) and grp the harness's one-level
// grouping column. relevel maps a row index to a wave label overriding
// the CSV's ("" keeps it; "-" makes it null).
func brandSEMFixture(t *testing.T, extra []string, relevel func(r int) string) (*encoding.Schema, func() []*Record) {
	t.Helper()
	return brandSEMFixtureDict(t, append([]string{"1", "2"}, extra...), relevel)
}

// brandSEMFixtureDict is brandSEMFixture over a wave dictionary holding
// levels in that order.
func brandSEMFixtureDict(t *testing.T, levels []string, relevel func(r int) string) (*encoding.Schema, func() []*Record) {
	t.Helper()
	f, err := os.Open(semScoreTestdata + "brand_data.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	header := recs[0]
	waveDict := encoding.NewDictionary()
	for _, l := range levels {
		if _, err := waveDict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{}
	for _, h := range header {
		if h == "wave" {
			schema.Fields = append(schema.Fields, encoding.Field{Name: h, Type: encoding.FieldTypeCategoricalU8, Dictionary: waveDict, Nullable: true})
			continue
		}
		schema.Fields = append(schema.Fields, encoding.Field{Name: h, Type: encoding.FieldTypeF64})
	}
	grp := encoding.NewDictionary()
	if _, err := grp.Add("all"); err != nil {
		t.Fatal(err)
	}
	schema.Fields = append(schema.Fields, encoding.Field{Name: "grp", Type: encoding.FieldTypeCategoricalU8, Dictionary: grp})
	mk := func() []*Record {
		out := make([]*Record, 0, len(recs)-1)
		for r, rec := range recs[1:] {
			vals := map[string]float64{"grp": 0}
			nulls := map[string]bool{}
			for i, h := range header {
				if h == "wave" {
					l := rec[i]
					if relevel != nil && relevel(r) != "" {
						l = relevel(r)
					}
					if l == "-" {
						nulls[h] = true
						continue
					}
					idx, ok := waveDict.IDFor(l)
					if !ok {
						t.Fatalf("wave level %q not in the dictionary", l)
					}
					vals[h] = float64(idx)
					continue
				}
				v, err := strconv.ParseFloat(rec[i], 64)
				if err != nil {
					t.Fatal(err)
				}
				vals[h] = v
			}
			out = append(out, NewRecordWithNulls(schema, vals, nulls))
		}
		return out
	}
	return schema, mk
}

// brandModel reads a brand-survey model description.
func brandModel(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(semScoreTestdata + name + "_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// brandMembers are the brand survey's ratings and outcome.
var brandMembers = []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3", "recommend"}

func semScoreParamsJSON(t *testing.T, params map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// semScoreTol is the per-row parity tolerance against lavaan (absolute
// on the score scale; the fits agree to ~1e-6 relative —
// internal/sem's TestSEMScore_MatchesLavaanPredict).
const semScoreTol = 1e-4

// TestSEMScore_InlineMatchesLavaan runs ATTR_SEM_SCORE through the
// processor — streaming two-pass and buffered arms, bit-identical — on
// the brand survey and holds every recorded row's score, each latent by
// both methods, to lavaan's lavPredict: a single-group mean structure,
// and the joint two-wave fit under scalar invariance (each row scored
// with its own wave's parameters), the members alternating between
// inline fields and a named vector.
func TestSEMScore_InlineMatchesLavaan(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	for ci, c := range []struct {
		ref    string
		params map[string]any
	}{
		{"brand_score_ms", map[string]any{"meanstructure": true}},
		{"brand_score_mg", map[string]any{"group": "wave", "group_equal": []string{"loadings", "intercepts"}}},
	} {
		ref := loadSEMScoreRef(t, c.ref)
		for _, method := range []string{"regression", "bartlett"} {
			for l, lat := range ref.Latents {
				t.Run(c.ref+"/"+method+"/"+lat, func(t *testing.T) {
					params := map[string]any{"model": brandModel(t, "brand_ms"), "latent": lat, "method": method}
					for k, v := range c.params {
						params[k] = v
					}
					viaVector := (ci+l)%2 == 1
					streamed, _, resp := runFittedArms(t, schema, mk, "score", nil, func() *types.Request {
						attr := &types.Attribute{Type: types.ATTR_SEM_SCORE, Label: "score", Params: semScoreParamsJSON(t, params)}
						req := &types.Request{Attributes: []*types.Attribute{attr}}
						if viaVector {
							req.Vectors = []types.VectorSpec{{Name: "battery", Fields: brandMembers}}
							attr.Vector = "battery"
						} else {
							attr.Fields = brandMembers
						}
						return req
					})
					if len(resp.Warnings) != 0 {
						wj, _ := json.Marshal(resp.Warnings)
						t.Fatalf("warnings %s", wj)
					}
					for _, g := range ref.Groups {
						want := g.Regression
						if method == "bartlett" {
							want = g.Bartlett
						}
						for k, r := range g.Rows {
							if d := math.Abs(streamed[r] - want[k][l]); !(d <= semScoreTol) {
								t.Errorf("row %d (wave %q): %s = %.10g, lavaan %.10g", r, g.Label, lat, streamed[r], want[k][l])
							}
						}
					}
				})
			}
		}
	}
}

// TestSEMScore_GroupedMeanIsLatentMeanShift aggregates the scores by
// wave (AGG_AVERAGE grouped on the wave) under scalar invariance and
// holds the wave-2 minus wave-1 mean to MAT_SEM's latent mean shift on
// the same rows (wave 1 the reference, its latent means 0): the scores
// sit on the latent-mean scale. The fixture plants value −0.30 and
// service +0.70 in factor units (quality 0).
func TestSEMScore_GroupedMeanIsLatentMeanShift(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	model := brandModel(t, "brand_ms")
	semParams := map[string]any{"model": model, "group": "wave", "group_equal": []string{"loadings", "intercepts"}}
	sp := semScoreParamsJSON(t, semParams)
	recs := mk()
	for i, r := range recs {
		r.SetMergePosition(0, 0, i) // the matrix slot's blocked merge reads it
	}
	mresp, err := NewProcessor(schema).Process(context.Background(), &types.Request{
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: brandMembers, Params: sp}},
	}, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	est := mresp.Matrices[0].Auxiliary["estimates"]
	latentMean := func(lat string) float64 {
		i := slices.Index(est.RowKeys, "[2] "+lat+" ~1")
		if i < 0 {
			t.Fatalf("no [2] %s ~1 row in %v", lat, est.RowKeys)
		}
		return est.Values[i][0]
	}
	for _, method := range []string{"regression", "bartlett"} {
		for _, lat := range []string{"quality", "value", "service"} {
			params := map[string]any{"latent": lat, "method": method}
			for k, v := range semParams {
				params[k] = v
			}
			req := &types.Request{
				Attributes:   []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score", Label: "mean_score"}},
			}
			resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(mk()))
			if err != nil {
				t.Fatal(err)
			}
			means := map[string]float64{}
			for _, row := range resp.Data {
				v, _ := row["mean_score"].(float64)
				means[fmt.Sprint(row["wave"])] = v
			}
			shift := means["2"] - means["1"]
			want := latentMean(lat)
			// The group means of the scores are E(η_g) + w·(ȳ_g − μ_g):
			// under scalar invariance μ_g is not ȳ_g, so the gap is the
			// weighted intercept misfit — small on this fixture.
			if math.Abs(shift-want) > 0.02 || math.Abs(means["1"]) > 0.02 {
				t.Errorf("%s %s: wave means %v (shift %.4f), MAT_SEM latent mean shift %.4f", method, lat, means, shift, want)
			}
		}
	}
	for lat, planted := range map[string]float64{"value": -0.30 * 0.80, "service": 0.70 * 0.75, "quality": 0} {
		// Marker units (the first loading fixes the scale).
		if got := latentMean(lat); math.Abs(got-planted) > 0.12 {
			t.Errorf("MAT_SEM %s latent mean shift %.4f, planted %.4f (marker units)", lat, got, planted)
		}
	}
}

// TestSEMScore_FailedFitNullsEveryScore pins the fit-scoped refusals:
// a fit that cannot score nulls every score with one warning of the
// matrix code, on both arms.
func TestSEMScore_FailedFitNullsEveryScore(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	ms := brandModel(t, "brand_ms")
	for _, c := range []struct {
		name   string
		fields []string
		params map[string]any
		code   errors.Code
	}{
		{"insufficient_n", brandMembers, map[string]any{"model": ms, "latent": "value",
			"fit_on": []map[string]any{{"type": "FILTER_EXPRESSION", "expression": "q1 >= 5 && v1 >= 5 && s1 >= 5"}}},
			errors.PULSE_MATRIX_INSUFFICIENT_N},
		{"not_identified", []string{"q1", "q2"}, map[string]any{"model": "quality =~ q1 + q2"}, errors.PULSE_MATRIX_NOT_IDENTIFIED},
		{"one_level", brandMembers, map[string]any{"model": ms, "latent": "value", "group": "wave",
			"fit_on": []map[string]any{{"type": "FILTER_INCLUDE", "field": "wave", "values": []string{"1"}}}},
			errors.PULSE_MATRIX_INSUFFICIENT_N},
	} {
		t.Run(c.name, func(t *testing.T) {
			streamed, _, resp := runFittedArms(t, schema, mk, "score", nil, func() *types.Request {
				return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: c.fields, Params: semScoreParamsJSON(t, c.params)}}}
			})
			for i, v := range streamed {
				if !math.IsNaN(v) {
					t.Fatalf("row %d scored %v, want null", i, v)
				}
			}
			if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(c.code) || resp.Warnings[0].Details["scope"] != "fit" {
				wj, _ := json.Marshal(resp.Warnings)
				t.Fatalf("warnings %s, want one fit-scoped %s", wj, c.code)
			}
		})
	}
}

// TestSEMScore_SingularFitFollowsParamsSingular pins params.singular on
// a rank-deficient fit (a member an exact copy of another): "refuse"
// nulls every score with PULSE_MATRIX_SINGULAR; the default "nearest"
// fits MAT_SEM's nearest positive-definite substitute, scores every
// complete row and warns PULSE_MATRIX_NEAREST_PD.
func TestSEMScore_SingularFitFollowsParamsSingular(t *testing.T) {
	t.Parallel()

	schema, mk0 := brandSEMFixture(t, nil, nil)
	schema.Fields = append(schema.Fields, encoding.Field{Name: "q1copy", Type: encoding.FieldTypeF64})
	mk := func() []*Record {
		recs := mk0()
		for _, r := range recs {
			v, _ := r.NumericValue("q1")
			r.Set("q1copy", v)
		}
		return recs
	}
	fields := []string{"q1", "q1copy", "q2", "q3"}
	model := "quality =~ q1 + q1copy + q2 + q3"
	for _, c := range []struct {
		singular string
		code     errors.Code
		scored   bool
	}{{"refuse", errors.PULSE_MATRIX_SINGULAR, false}, {"", errors.PULSE_MATRIX_NEAREST_PD, true}} {
		t.Run("singular="+c.singular, func(t *testing.T) {
			params := map[string]any{"model": model}
			if c.singular != "" {
				params["singular"] = c.singular
			}
			streamed, _, resp := runFittedArms(t, schema, mk, "score", nil, func() *types.Request {
				return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: fields, Params: semScoreParamsJSON(t, params)}}}
			})
			var codes []string
			for _, w := range resp.Warnings {
				codes = append(codes, w.Code)
			}
			if !slices.Contains(codes, string(c.code)) {
				t.Fatalf("warnings %v, want %s", codes, c.code)
			}
			nulls := 0
			for _, v := range streamed {
				if math.IsNaN(v) {
					nulls++
				}
			}
			if c.scored && nulls != 0 || !c.scored && nulls != len(streamed) {
				t.Fatalf("%d of %d scores null, scored %v", nulls, len(streamed), c.scored)
			}
		})
	}
}

// TestSEMScore_UnseenLevelAndNullGroup pins the grouped row rules: a
// level fit_on keeps out of the fit scores null and is tallied into one
// PULSE_SEM_SCORE_LEVEL_UNSEEN warning; a null group scores null
// without a warning; every other row is its wave's score, the same bits
// as the fit without those rows.
func TestSEMScore_UnseenLevelAndNullGroup(t *testing.T) {
	t.Parallel()

	relevel := func(r int) string {
		switch {
		case r < 12:
			return "3"
		case r < 15:
			return "-"
		}
		return ""
	}
	schema, mk := brandSEMFixture(t, []string{"3"}, relevel)
	params := map[string]any{"model": brandModel(t, "brand_ms"), "latent": "service", "group": "wave",
		"group_equal": "loadings,intercepts",
		"fit_on":      []map[string]any{{"type": "FILTER_INCLUDE", "field": "wave", "values": []string{"1", "2"}}}}
	streamed, _, resp := runFittedArms(t, schema, mk, "score", nil, func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}}}
	})
	for r := 0; r < 15; r++ {
		if !math.IsNaN(streamed[r]) {
			t.Errorf("row %d scored %v, want null (unseen level or null group)", r, streamed[r])
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_SEM_SCORE_LEVEL_UNSEEN) {
		wj, _ := json.Marshal(resp.Warnings)
		t.Fatalf("warnings %s, want one PULSE_SEM_SCORE_LEVEL_UNSEEN", wj)
	}
	d := resp.Warnings[0].Details
	if fmt.Sprint(d["levels"]) != "[3]" || fmt.Sprint(d["rows"]) != "[12]" || fmt.Sprint(d["fit_levels"]) != "[1 2]" {
		t.Fatalf("details %v", d)
	}
	// The same fit without the relabelled rows: every other row's
	// score is the same bits.
	schema2, mk2 := brandSEMFixture(t, []string{"3"}, nil)
	params2 := map[string]any{}
	for k, v := range params {
		params2[k] = v
	}
	params2["fit_on"] = []map[string]any{{"type": "FILTER_EXPRESSION", "expression": "row_ok == 1"}}
	schema2.Fields = append(schema2.Fields, encoding.Field{Name: "row_ok", Type: encoding.FieldTypeF64})
	ref, _, _ := runFittedArms(t, schema2, func() []*Record {
		out := mk2()
		for r, rec := range out {
			rec.Set("row_ok", map[bool]float64{true: 0, false: 1}[r < 15])
		}
		return out
	}, "score", nil, func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params2)}}}
	})
	if !sameBits(streamed[15:], ref[15:]) {
		t.Fatal("the scored rows differ from the fit on the same rows")
	}
}

// TestSEMScore_DownstreamRegression uses the score as a REG_OLS
// predictor of recommend in the same request: the attribute column is
// an ordinary numeric column downstream.
func TestSEMScore_DownstreamRegression(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	params := map[string]any{"model": "service =~ s1 + s2 + s3"}
	req := &types.Request{
		Attributes:  []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "service_score", Fields: []string{"s1", "s2", "s3"}, Params: semScoreParamsJSON(t, params)}},
		Regressions: []*types.RegressionSpec{{Name: "rec", Type: types.REG_OLS, Target: "recommend", Predictors: []string{"service_score"}}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(mk()))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		rj, _ := json.Marshal(resp.Regressions)
		t.Fatalf("regressions %s", rj)
	}
	slope, found := resp.Regressions[0].Coefficients["service_score"]
	// recommend = 6 + 0.9·service + … in factor units; the score is in
	// s1's units (loading 0.75), so the slope is ≈ 0.9 / 0.75 attenuated
	// by the score's reliability — clearly positive.
	if !found || !(slope > 0.5) {
		rj, _ := json.Marshal(resp.Regressions[0].Coefficients)
		t.Fatalf("service_score slope %v (found %v): %s", slope, found, rj)
	}
}
