package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// factorScoreOracle is one mv_factor_score.json case (scripts/reference/
// gen_fitted_attributes.R): MAT_FACTOR's oracle loadings on the fit
// rows' (weighted) correlation, rotated by Pulse's definitions, and every
// row's regression (Thurstone) and Bartlett scores (rows × factors), the
// fit over the listwise, positive-weight, fit_on rows; a row null on a
// member is null.
type factorScoreOracle struct {
	Fixture    string   `json:"fixture"`
	Fields     []string `json:"fields"`
	Weight     string   `json:"weight"`
	Factors    int      `json:"factors"`
	Extraction string   `json:"extraction"`
	Rotation   string   `json:"rotation"`
	Converged  bool     `json:"converged"`
	FitOn      *struct {
		Field string  `json:"field"`
		Level float64 `json:"level"`
	} `json:"fit_on"`
	NFit         int          `json:"n_fit"`
	Loadings     [][]float64  `json:"loadings"`
	Uniquenesses []float64    `json:"uniquenesses"`
	Regression   [][]*float64 `json:"regression"`
	Bartlett     [][]*float64 `json:"bartlett"`
}

// factorScoreOracleTol is the per-score absolute + relative tolerance by
// extraction. The scores inherit the loadings' gap to the oracle
// (matrix_factor_test.go's tolerances): principal axis replays psych
// step for step and ML is solved to its exact optimum (both ~1e-12 on
// the loadings, kept at 1e-8 here for the eigensolver and the oblimin
// GPA band over them); minres stops at max|d| ≤ 1e-10 against psych's
// exact optimum, so its loadings sit within faExactOracleTol and the
// scores within 1e-6.
func factorScoreOracleTol(extraction string) float64 {
	if extraction == vectors.FactorExtractionMinres {
		return 1e-6
	}
	return 1e-8
}

// factorScoreParams builds an ATTR_FACTOR_SCORE params blob.
func factorScoreParams(factor int, method string, factors any, extraction, rotation string, fitOn []map[string]any) json.RawMessage {
	params := map[string]any{"factor": factor}
	if method != "" {
		params["method"] = method
	}
	if factors != nil {
		params["factors"] = factors
	}
	if extraction != "" {
		params["extraction"] = extraction
	}
	if rotation != "" {
		params["rotation"] = rotation
	}
	if fitOn != nil {
		params["fit_on"] = fitOn
	}
	raw, _ := json.Marshal(params)
	return raw
}

// TestFactorScore_MatchesROracle pins every mv_factor_score.json case —
// attitude (minres unrotated and varimax, principal axis oblimin, ML
// promax), mtcars (a fit_on subset), weighted (zero-weight rows) and
// nulls (listwise, ML), under no weight, frequency and probability
// weights — every factor by both methods through the real processor on
// the streaming two-pass and buffered arms (bit-identical), alternating
// the member source between inline fields and a named vector.
func TestFactorScore_MatchesROracle(t *testing.T) {
	doc := loadReference[factorScoreOracle](t, "mv_factor_score")
	seen := map[string]bool{}
	for ci, c := range doc.Cases {
		split := ""
		var fitOn []map[string]any
		if c.FitOn != nil {
			split = c.FitOn.Field
			fitOn = []map[string]any{{"type": "FILTER_INCLUDE", "field": "grp", "values": []string{fmt.Sprintf("%g", c.FitOn.Level)}}}
			seen["fit_on"] = true
		}
		seen[c.Weight], seen[c.Extraction], seen[c.Rotation] = true, true, true
		tol := factorScoreOracleTol(c.Extraction)
		// The Heywood rule against R's uniquenesses: a ψ ≤ 0 (or ML's
		// ψ at its 0.005 floor) owes a PULSE_MATRIX_HEYWOOD; Bartlett
		// over a ψ ≤ 0 nulls every score, every other fit scores as R.
		heywood, nonPositive := false, false
		for _, u := range c.Uniquenesses {
			if u <= 0 {
				heywood, nonPositive = true, true
			}
			if c.Extraction == vectors.FactorExtractionML && u <= 0.005+1e-9 {
				heywood = true
			}
		}
		for _, method := range vectors.FactorScoreMethods() {
			nulled := nonPositive && method == vectors.FactorScoreBartlett
			switch {
			case nulled:
				seen["heywood nulled"] = true
			case heywood:
				seen["heywood scored"] = true
			}
			if !c.Converged {
				seen["not converged"] = true
			}
			want := c.Regression
			if method == vectors.FactorScoreBartlett {
				want = c.Bartlett
			}
			for f := 1; f <= c.Factors; f++ {
				t.Run(fmt.Sprintf("%d/%s/%s/%s/%s/%s/F%d", ci, c.Fixture, c.Weight, c.Extraction, c.Rotation, method, f), func(t *testing.T) {
					schema, _ := hotellingFixture(t, c.Fixture, split)
					mk := func() []*Record { _, recs := hotellingFixture(t, c.Fixture, split); return recs }
					viaVector := ci%2 == 1
					streamed, buffered, resp := runMahalanobisArms(t, schema, mk, "fs", func() *types.Request {
						attr := &types.Attribute{Type: types.ATTR_FACTOR_SCORE, Label: "fs",
							Params: factorScoreParams(f, method, c.Factors, c.Extraction, c.Rotation, fitOn)}
						req := &types.Request{Weight: oracleWeight(c.Weight), Attributes: []*types.Attribute{attr}}
						if viaVector {
							req.Vectors = []types.VectorSpec{{Name: "battery", Fields: c.Fields}}
							attr.Vector = "battery"
						} else {
							attr.Fields = c.Fields
						}
						return req
					})
					if !sameBits(streamed, buffered) {
						t.Fatalf("streaming two-pass and buffered scores differ:\n%v\n%v", streamed, buffered)
					}
					// psych's principal axis at its 50-iteration cap owes
					// PULSE_MATRIX_NOT_CONVERGED, a Heywood fit
					// PULSE_MATRIX_HEYWOOD (in that order); a refusal
					// (Bartlett over ψ ≤ 0) is the one warning.
					var wantCodes []string
					if !c.Converged && !nulled {
						wantCodes = append(wantCodes, string(errors.PULSE_MATRIX_NOT_CONVERGED))
					}
					if heywood {
						wantCodes = append(wantCodes, string(errors.PULSE_MATRIX_HEYWOOD))
					}
					var gotCodes []string
					for _, w := range resp.Warnings {
						gotCodes = append(gotCodes, w.Code)
					}
					if fmt.Sprint(gotCodes) != fmt.Sprint(wantCodes) {
						wj, _ := json.Marshal(resp.Warnings)
						t.Fatalf("warnings %s, want codes %v (R uniquenesses %v, converged %v)", wj, wantCodes, c.Uniquenesses, c.Converged)
					}
					if len(want) != len(streamed) {
						t.Fatalf("%d scores, R has %d", len(streamed), len(want))
					}
					for i, row := range want {
						w := row[f-1]
						got := streamed[i]
						if nulled {
							if !math.IsNaN(got) {
								t.Errorf("row %d: Bartlett over a uniqueness ≤ 0 scored %v, want null", i, got)
							}
							continue
						}
						if w == nil {
							if !math.IsNaN(got) {
								t.Errorf("row %d: score %v, R null (a member is null)", i, got)
							}
							continue
						}
						if math.Abs(got-*w) > tol*math.Max(1, math.Abs(*w)) {
							t.Errorf("row %d: F%d = %.17g, R = %.17g", i, f, got, *w)
						}
					}
				})
			}
		}
	}
	for _, want := range []string{"fit_on", "none", "frequency", "probability", "minres", "principal_axis", "ml", "varimax", "promax", "oblimin", "heywood nulled", "heywood scored", "not converged"} {
		if !seen[want] {
			t.Fatalf("vacuous oracle: no %s case (seen %v)", want, seen)
		}
	}
}

// TestFactorScore_SignAgreesWithMATFactor is the sign acceptance: on the
// same rows, every regression score's correlation with each member is
// the structure coefficient (LΦ)_ic of MAT_FACTOR's loadings L and
// factor correlation Φ times ONE positive factor per score (the
// regression score's covariance with z is R·R⁻¹S = S), so the
// attribute's loadings, column order and sign are MAT_FACTOR's — for
// the default (Kaiser, minres) fit and rotated ones, unweighted and
// weighted. A Bartlett score correlates with every member with the
// structure coefficient's sign wherever it is clearly non-zero.
func TestFactorScore_SignAgreesWithMATFactor(t *testing.T) {
	fields := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	for _, c := range []struct {
		name       string
		factors    any
		k          int
		extraction string
		rotation   string
		weight     string
	}{
		{"kaiser minres", nil, 2, "", "", "none"},
		{"varimax", 2, 2, "", "varimax", "none"},
		{"oblimin principal axis frequency", 3, 3, "principal_axis", "oblimin", "frequency"},
		{"promax ml probability", 2, 2, "ml", "promax", "probability"},
	} {
		t.Run(c.name, func(t *testing.T) {
			schema, _ := hotellingFixture(t, "attitude", "")
			_, recs := hotellingFixture(t, "attitude", "")
			for i, r := range recs {
				r.SetMergePosition(0, i) // the matrix slot's blocked merge reads it
			}
			matParams := map[string]any{}
			if c.factors != nil {
				matParams["factors"] = c.factors
			}
			if c.extraction != "" {
				matParams["extraction"] = c.extraction
			}
			if c.rotation != "" {
				matParams["rotation"] = c.rotation
			}
			mp, _ := json.Marshal(matParams)
			req := &types.Request{
				Weight:   oracleWeight(c.weight),
				Matrices: []types.MatrixSpec{{Name: "fa", Type: types.MAT_FACTOR, Fields: fields, Params: mp}},
			}
			for f := 1; f <= c.k; f++ {
				for _, method := range vectors.FactorScoreMethods() {
					req.Attributes = append(req.Attributes, &types.Attribute{
						Type: types.ATTR_FACTOR_SCORE, Label: fmt.Sprintf("%s%d", method, f), Fields: fields,
						Params: factorScoreParams(f, method, c.factors, c.extraction, c.rotation, nil),
					})
				}
			}
			p := NewProcessor(schema)
			resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
			if err != nil {
				t.Fatal(err)
			}
			// The attribute owes the warnings MAT_FACTOR owes on the same
			// fit (a Heywood case, an extraction at its cap), never a
			// refusal.
			for _, w := range resp.Warnings {
				if w.Code != string(errors.PULSE_MATRIX_HEYWOOD) && w.Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) {
					wj, _ := json.Marshal(w)
					t.Fatalf("unexpected warning %s", wj)
				}
			}
			if len(resp.Matrices) != 1 || resp.Matrices[0].Primary == nil {
				t.Fatalf("no MAT_FACTOR result: %+v", resp.Matrices)
			}
			l := resp.Matrices[0].Primary.Values
			if len(l[0]) != c.k {
				t.Fatalf("MAT_FACTOR fitted %d factors, want %d", len(l[0]), c.k)
			}
			phi := identityRows(c.k)
			if fc := resp.Matrices[0].Auxiliary["factor_correlation"]; fc != nil {
				phi = fc.Values
			}
			wspec := oracleWeight(c.weight)
			for f := 1; f <= c.k; f++ {
				ratio := math.NaN()
				for i, field := range fields {
					s := 0.0
					for b := 0; b < c.k; b++ {
						s += l[i][b] * phi[b][f-1]
					}
					reg := weightedCorr(recs, fmt.Sprintf("regression%d", f), field, wspec)
					if math.IsNaN(ratio) {
						ratio = reg / s
						if !(ratio > 0) {
							t.Fatalf("F%d × %s: corr(score, member) %.6f against structure %.6f — not one positive factor", f, field, reg, s)
						}
					} else if math.Abs(reg/s-ratio) > 1e-8*ratio {
						t.Errorf("F%d × %s: corr / structure = %.12f, the first member's %.12f", f, field, reg/s, ratio)
					}
					bart := weightedCorr(recs, fmt.Sprintf("bartlett%d", f), field, wspec)
					if math.Abs(s) > 0.2 && math.Signbit(bart) != math.Signbit(s) {
						t.Errorf("F%d × %s: Bartlett corr %.6f against structure %.6f", f, field, bart, s)
					}
				}
			}
		})
	}
}

// fsRun runs one ATTR_FACTOR_SCORE slot over recs on both arms and
// returns the streamed scores and the response.
func fsRun(t *testing.T, fixture string, fields []string, params string) ([]float64, *types.Response) {
	t.Helper()
	schema, _ := hotellingFixture(t, fixture, "")
	mk := func() []*Record { _, recs := hotellingFixture(t, fixture, ""); return recs }
	streamed, _, resp := runMahalanobisArms(t, schema, mk, "fs", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FACTOR_SCORE, Label: "fs", Fields: fields,
			Params: json.RawMessage(params)}}}
	})
	return streamed, resp
}

// TestFactorScore_FitWarnings pins the fit-failure rule: a model that
// cannot give a score nulls every score with one fit-scoped warning, a
// usable fit that owes a caveat scores and warns.
func TestFactorScore_FitWarnings(t *testing.T) {
	heywood := []string{"h1", "h2", "h3"}
	attitude := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	for _, c := range []struct {
		name    string
		fixture string
		fields  []string
		params  string
		code    errors.Code
		null    bool
		msg     string
	}{
		{"heywood regression scores", "heywood", heywood, `{"factor": 1, "factors": 1}`, errors.PULSE_MATRIX_HEYWOOD, false, "as fitted"},
		{"heywood bartlett nulls", "heywood", heywood, `{"factor": 1, "factors": 1, "method": "bartlett"}`, errors.PULSE_MATRIX_HEYWOOD, true, "divide by each uniqueness"},
		{"heywood ml floor scores bartlett", "heywood", heywood, `{"factor": 1, "factors": 1, "extraction": "ml", "method": "bartlett"}`, errors.PULSE_MATRIX_HEYWOOD, false, "as fitted"},
		{"not identified", "attitude", attitude[:3], `{"factor": 1, "factors": 2}`, errors.PULSE_MATRIX_NOT_IDENTIFIED, true, "not identified"},
		{"kaiser keeps fewer", "attitude", attitude, `{"factor": 3}`, errors.PULSE_MATRIX_NOT_IDENTIFIED, true, "Kaiser rule keeps 2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			scores, resp := fsRun(t, c.fixture, c.fields, c.params)
			if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(c.code) {
				t.Fatalf("warnings %+v, want one %s", resp.Warnings, c.code)
			}
			w := resp.Warnings[0]
			if w.Details["scope"] != "fit" || w.Details["type"] != string(types.ATTR_FACTOR_SCORE) || !strings.Contains(w.Message, c.msg) {
				t.Errorf("warning %+v, want fit-scoped mentioning %q", w, c.msg)
			}
			// The code's own details ride beside the fit's figures.
			wantKey := map[errors.Code]string{errors.PULSE_MATRIX_HEYWOOD: "heywood_members", errors.PULSE_MATRIX_NOT_IDENTIFIED: "max_factors"}[c.code]
			if c.name == "kaiser keeps fewer" {
				wantKey = "factor"
			}
			if _, ok := w.Details[wantKey]; !ok {
				t.Errorf("warning details %v carry no %s", w.Details, wantKey)
			}
			scored := 0
			for _, v := range scores {
				if !math.IsNaN(v) {
					scored++
				}
			}
			if c.null && scored != 0 {
				t.Errorf("%d rows scored, want every score null", scored)
			}
			if !c.null && scored != len(scores) {
				t.Errorf("%d of %d rows scored, want every row", scored, len(scores))
			}
		})
	}
}

// TestFactorScore_KaiserMatchesFixedCount: the default factors "kaiser"
// fits the k the eigenvalue rule keeps — the same scores as that fixed k.
func TestFactorScore_KaiserMatchesFixedCount(t *testing.T) {
	fields := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	for f := 1; f <= 2; f++ {
		kaiser, _ := fsRun(t, "attitude", fields, fmt.Sprintf(`{"factor": %d, "rotation": "varimax"}`, f))
		fixed, _ := fsRun(t, "attitude", fields, fmt.Sprintf(`{"factor": %d, "factors": 2, "rotation": "varimax"}`, f))
		if !sameBits(kaiser, fixed) {
			t.Fatalf("F%d: kaiser %v, factors 2 %v", f, kaiser[:3], fixed[:3])
		}
	}
}

// TestFactorScore_SingularNullsEveryScore: a member that is a linear
// combination of others nulls every score with PULSE_MATRIX_SINGULAR
// (regression scores need R⁻¹).
func TestFactorScore_SingularNullsEveryScore(t *testing.T) {
	schema := mahaSchema()
	rows := make([][3]float64, len(mahaRows))
	for i, r := range mahaRows {
		rows[i] = [3]float64{r[0], r[1], 2*r[0] - r[1]}
	}
	mk := func() []*Record { return mahaRecords(schema, rows, mahaGroups) }
	streamed, _, resp := runMahalanobisArms(t, schema, mk, "fs", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FACTOR_SCORE, Label: "fs", Fields: []string{"x", "y", "z"},
			Params: json.RawMessage(`{"factor": 1, "factors": 1}`)}}}
	})
	for i, v := range streamed {
		if !math.IsNaN(v) {
			t.Fatalf("row %d scored %v, want null", i, v)
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_SINGULAR) {
		t.Fatalf("warnings %+v, want one PULSE_MATRIX_SINGULAR", resp.Warnings)
	}
}

// TestFactorScore_DefaultLabels: "<TYPE>_<vector>_F<factor>" and
// "<TYPE>_<first fields entry>_F<factor>".
func TestFactorScore_DefaultLabels(t *testing.T) {
	schema := mahaSchema()
	recs := mahaRecords(schema, mahaRows, mahaGroups)
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "battery", Fields: []string{"x", "y", "z"}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FACTOR_SCORE, Vector: "battery", Params: json.RawMessage(`{"factor": 1, "factors": 1}`)},
			{Type: types.ATTR_FACTOR_SCORE, Fields: []string{"y", "x", "z"}, Params: json.RawMessage(`{"factor": 1, "factors": 1}`)},
		},
	}
	p := NewProcessor(schema)
	if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"ATTR_FACTOR_SCORE_battery_F1", "ATTR_FACTOR_SCORE_y_F1"} {
		if _, ok := recs[0].NumericValue(label); !ok {
			t.Errorf("no value under %s", label)
		}
	}
}

// TestFactorScore_FactoryRefusals: a missing or out-of-range factor,
// MAT_FACTOR's knob rules and unknown keys are refused at construction.
func TestFactorScore_FactoryRefusals(t *testing.T) {
	schema := mahaSchema()
	for _, c := range []struct {
		params string
		want   string
	}{
		{``, "factor is required"},
		{`{"factors": 1}`, "factor is required"},
		{`{"factor": 0}`, "integer ≥ 1"},
		{`{"factor": 4}`, "at most 3 factors"},
		{`{"factor": 1, "factors": 4}`, "at most 3 factors"},
		{`{"factor": 2, "factors": 1}`, "at least params.factor"},
		{`{"factor": 1, "method": "anderson"}`, "method must be"},
		{`{"factor": 1, "extraction": "pca"}`, "extraction must be"},
		{`{"factor": 1, "rotation": "quartimax"}`, "rotation must be"},
		{`{"factor": 1, "kappa": 3}`, "kappa applies to rotation \"promax\" only"},
		{`{"factor": 1, "rotation": "oblimin", "gamma": "x"}`, "ATTR_FACTOR_SCORE params"},
		{`{"factor": 1, "repair": "nearest"}`, "unknown field"},
		{`{"factor": 1, "factors": "auto"}`, "factors must be"},
	} {
		_, err := newFactorScoreAttribute(&types.Attribute{Type: types.ATTR_FACTOR_SCORE, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(c.params)}, schema)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("params %s: %v, want %q", c.params, err, c.want)
		}
	}
}
