package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// pcScoreOracle is one mv_pc_score.json case (scripts/reference/
// gen_fitted_attributes.R): every row's standardized component scores
// (rows × components — all p unrotated, or the k varimax-rotated after
// Pulse's order-and-sign rule), the fit over the listwise,
// positive-weight, fit_on rows; a row null on a member is null.
type pcScoreOracle struct {
	Fixture    string   `json:"fixture"`
	Fields     []string `json:"fields"`
	Weight     string   `json:"weight"`
	On         string   `json:"on"`
	Rotation   string   `json:"rotation"`
	Components *int     `json:"components"`
	FitOn      *struct {
		Field string  `json:"field"`
		Level float64 `json:"level"`
	} `json:"fit_on"`
	NFit   int          `json:"n_fit"`
	Scores [][]*float64 `json:"scores"`
}

// pcScoreOracleTol: R decomposes with LAPACK, Pulse with the
// gonum-backed SymEigen (and, rotated, a gonum SVD per varimax sweep);
// the scores are O(1) and agree to ~1e-12 on these fixtures. 1e-9
// absolute + relative leaves room for the last ulps of either
// eigensolver across architectures (matrix-and-vectors.md, tolerance
// rules).
const pcScoreOracleTol = 1e-9

func pcClose(got, want float64) bool {
	return math.Abs(got-want) <= pcScoreOracleTol*math.Max(1, math.Abs(want))
}

// pcScoreParams builds an ATTR_PC_SCORE params blob.
func pcScoreParams(component int, on, rotation string, k int, fitOn []map[string]any) json.RawMessage {
	params := map[string]any{"component": component}
	if on != "" {
		params["on"] = on
	}
	if rotation != "" && rotation != "none" {
		params["rotation"] = rotation
		params["components"] = k
	}
	if fitOn != nil {
		params["fit_on"] = fitOn
	}
	raw, _ := json.Marshal(params)
	return raw
}

// TestPCScore_MatchesROracle pins every mv_pc_score.json case —
// attitude, mtcars (incl. a fit_on subset), weighted (zero-weight rows)
// and nulls (listwise), on the correlation and the covariance,
// unrotated and varimax, under no weight, frequency and probability
// weights — every component through the real processor on the
// streaming two-pass and buffered arms (bit-identical), alternating
// the member source between inline fields and a named vector.
func TestPCScore_MatchesROracle(t *testing.T) {
	doc := loadReference[pcScoreOracle](t, "mv_pc_score")
	seen := map[string]bool{}
	for ci, c := range doc.Cases {
		split := ""
		var fitOn []map[string]any
		if c.FitOn != nil {
			split = c.FitOn.Field
			fitOn = []map[string]any{{"type": "FILTER_INCLUDE", "field": "grp", "values": []string{fmt.Sprintf("%g", c.FitOn.Level)}}}
			seen["fit_on"] = true
		}
		k := 0
		if c.Components != nil {
			k = *c.Components
		}
		seen[c.Weight], seen[c.On], seen[c.Rotation] = true, true, true
		if len(c.Scores) == 0 {
			t.Fatalf("case %d has no scores", ci)
		}
		for comp := 1; comp <= len(c.Scores[0]); comp++ {
			t.Run(fmt.Sprintf("%d/%s/%s/%s/%s/PC%d", ci, c.Fixture, c.Weight, c.On, c.Rotation, comp), func(t *testing.T) {
				schema, _ := hotellingFixture(t, c.Fixture, split)
				mk := func() []*Record { _, recs := hotellingFixture(t, c.Fixture, split); return recs }
				viaVector := ci%2 == 1
				streamed, buffered, resp := runMahalanobisArms(t, schema, mk, "pc", func() *types.Request {
					attr := &types.Attribute{Type: types.ATTR_PC_SCORE, Label: "pc", Params: pcScoreParams(comp, c.On, c.Rotation, k, fitOn)}
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
				if len(resp.Warnings) != 0 {
					t.Fatalf("unexpected warnings %+v", resp.Warnings)
				}
				if len(c.Scores) != len(streamed) {
					t.Fatalf("%d scores, R has %d", len(streamed), len(c.Scores))
				}
				for i, row := range c.Scores {
					w := row[comp-1]
					got := streamed[i]
					if w == nil {
						if !math.IsNaN(got) {
							t.Errorf("row %d: score %v, R null (a member is null)", i, got)
						}
						continue
					}
					if !pcClose(got, *w) {
						t.Errorf("row %d: PC%d = %.17g, R = %.17g", i, comp, got, *w)
					}
				}
			})
		}
	}
	for _, want := range []string{"fit_on", "none", "frequency", "probability", "correlation", "covariance", "varimax"} {
		if !seen[want] {
			t.Fatalf("vacuous oracle: no %s case (seen %v)", want, seen)
		}
	}
}

// TestPCScore_SignAgreesWithMATPCA is the sign acceptance: on the same
// rows, every PC score's correlation with each member equals MAT_PCA's
// (rotated) loading on that member — the loading IS that correlation
// for a standardized score — so the score's sign, order and scale are
// MAT_PCA's, unrotated and varimax, unweighted and weighted.
func TestPCScore_SignAgreesWithMATPCA(t *testing.T) {
	fields := []string{"rating", "complaints", "privileges", "learning", "raises", "critical", "advance"}
	for _, c := range []struct {
		name     string
		rotation string
		k        int
		weight   string
	}{
		{"unrotated", "", 3, "none"},
		{"varimax", "varimax", 3, "none"},
		{"varimax frequency", "varimax", 2, "frequency"},
		{"unrotated probability", "", 2, "probability"},
	} {
		t.Run(c.name, func(t *testing.T) {
			schema, _ := hotellingFixture(t, "attitude", "")
			_, recs := hotellingFixture(t, "attitude", "")
			for i, r := range recs {
				r.SetMergePosition(0, i) // the matrix slot's blocked merge reads it
			}
			matParams := map[string]any{"components": c.k}
			if c.rotation != "" {
				matParams["rotation"] = c.rotation
			}
			mp, _ := json.Marshal(matParams)
			req := &types.Request{
				Weight:   oracleWeight(c.weight),
				Matrices: []types.MatrixSpec{{Name: "pca", Type: types.MAT_PCA, Fields: fields, Params: mp}},
			}
			for comp := 1; comp <= c.k; comp++ {
				req.Attributes = append(req.Attributes, &types.Attribute{
					Type: types.ATTR_PC_SCORE, Label: fmt.Sprintf("pc%d", comp), Fields: fields,
					Params: pcScoreParams(comp, "", c.rotation, c.k, nil),
				})
			}
			p := NewProcessor(schema)
			resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Matrices) != 1 || resp.Matrices[0].Primary == nil {
				t.Fatalf("no MAT_PCA result: %+v", resp.Matrices)
			}
			loadings := resp.Matrices[0].Primary.Values
			wspec := oracleWeight(c.weight)
			for comp := 1; comp <= c.k; comp++ {
				for i, f := range fields {
					r := weightedCorr(recs, fmt.Sprintf("pc%d", comp), f, wspec)
					want := loadings[i][comp-1]
					if math.Abs(r-want) > 1e-9 {
						t.Errorf("PC%d × %s: corr(score, member) = %.12f, MAT_PCA loading %.12f", comp, f, r, want)
					}
				}
			}
		})
	}
}

// weightedCorr is the (weighted) Pearson correlation of columns a and
// b over the records carrying both (and a positive weight).
func weightedCorr(recs []*Record, a, b string, w *types.WeightSpec) float64 {
	var sw, ma, mb float64
	type pt struct{ x, y, w float64 }
	var pts []pt
	for _, r := range recs {
		x, ok1 := r.NumericValue(a)
		y, ok2 := r.NumericValue(b)
		if !ok1 || !ok2 || math.IsNaN(x) || math.IsNaN(y) {
			continue
		}
		wt := 1.0
		if w != nil {
			v, ok := r.NumericValue(w.Field)
			if !ok || !(v > 0) {
				continue
			}
			wt = v
		}
		pts = append(pts, pt{x, y, wt})
		sw += wt
		ma += wt * x
		mb += wt * y
	}
	ma, mb = ma/sw, mb/sw
	var sab, saa, sbb float64
	for _, q := range pts {
		sab += q.w * (q.x - ma) * (q.y - mb)
		saa += q.w * (q.x - ma) * (q.x - ma)
		sbb += q.w * (q.y - mb) * (q.y - mb)
	}
	return sab / math.Sqrt(saa*sbb)
}

// TestPCScore_SingularNullsEveryScore: a member that is a linear
// combination of others nulls every score with PULSE_MATRIX_SINGULAR on
// both arms (a zero eigenvalue has no standardized score).
func TestPCScore_SingularNullsEveryScore(t *testing.T) {
	schema := mahaSchema()
	rows := make([][3]float64, len(mahaRows))
	for i, r := range mahaRows {
		rows[i] = [3]float64{r[0], r[1], 2*r[0] - r[1]}
	}
	mk := func() []*Record { return mahaRecords(schema, rows, mahaGroups) }
	streamed, buffered, resp := runMahalanobisArms(t, schema, mk, "pc", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Label: "pc", Fields: []string{"x", "y", "z"},
			Params: json.RawMessage(`{"component": 1}`)}}}
	})
	for i := range streamed {
		if !math.IsNaN(streamed[i]) || !math.IsNaN(buffered[i]) {
			t.Fatalf("row %d scored %v / %v, want null", i, streamed[i], buffered[i])
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_SINGULAR) {
		t.Fatalf("warnings %+v, want one PULSE_MATRIX_SINGULAR", resp.Warnings)
	}
	if d := resp.Warnings[0].Details; d["type"] != "ATTR_PC_SCORE" || d["scope"] != "fit" {
		t.Errorf("warning details %v", d)
	}
}

// TestPCScore_UnitVarianceAndNullMember: on the fit rows the scores have
// mean 0 and unit (ddof = 1) variance; a row with a null member scores
// null and stays out of the fit.
func TestPCScore_UnitVarianceAndNullMember(t *testing.T) {
	schema := mahaSchema()
	rows := append([][3]float64(nil), mahaRows...)
	rows[3][1] = math.NaN()
	for _, on := range []string{"correlation", "covariance"} {
		for comp := 1; comp <= 3; comp++ {
			recs := mahaRecords(schema, rows, mahaGroups)
			req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_PC_SCORE, Label: "pc", Fields: []string{"x", "y", "z"},
				Params: pcScoreParams(comp, on, "", 0, nil)}}}
			p := NewProcessor(schema)
			if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
				t.Fatal(err)
			}
			var vals []float64
			for i, r := range recs {
				v, ok := r.NumericValue("pc")
				if i == 3 {
					if ok && !math.IsNaN(v) {
						t.Fatalf("%s PC%d: the null-member row scored %v", on, comp, v)
					}
					continue
				}
				vals = append(vals, v)
			}
			mean, ss := 0.0, 0.0
			for _, v := range vals {
				mean += v
			}
			mean /= float64(len(vals))
			for _, v := range vals {
				ss += (v - mean) * (v - mean)
			}
			if math.Abs(mean) > 1e-12 || math.Abs(ss/float64(len(vals)-1)-1) > 1e-12 {
				t.Errorf("%s PC%d: mean %v variance %v, want 0 and 1", on, comp, mean, ss/float64(len(vals)-1))
			}
		}
	}
}

// TestPCScore_DefaultLabels: "<TYPE>_<vector>_PC<component>" and
// "<TYPE>_<first fields entry>_PC<component>", so two components of one
// vector never collide.
func TestPCScore_DefaultLabels(t *testing.T) {
	schema := mahaSchema()
	recs := mahaRecords(schema, mahaRows, mahaGroups)
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "battery", Fields: []string{"x", "y", "z"}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_PC_SCORE, Vector: "battery", Params: json.RawMessage(`{"component": 1}`)},
			{Type: types.ATTR_PC_SCORE, Vector: "battery", Params: json.RawMessage(`{"component": 2}`)},
			{Type: types.ATTR_PC_SCORE, Fields: []string{"y", "x"}, Params: json.RawMessage(`{"component": 2}`)},
		},
	}
	p := NewProcessor(schema)
	if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"ATTR_PC_SCORE_battery_PC1", "ATTR_PC_SCORE_battery_PC2", "ATTR_PC_SCORE_y_PC2"} {
		if _, ok := recs[0].NumericValue(label); !ok {
			t.Errorf("no value under %s", label)
		}
	}
}

// TestPCScore_FactoryRefusals: missing / out-of-range components and a
// components key without a rotation are refused at construction.
func TestPCScore_FactoryRefusals(t *testing.T) {
	schema := mahaSchema()
	for _, c := range []struct {
		params string
		want   string
	}{
		{``, "component is required"},
		{`{"on": "covariance"}`, "component is required"},
		{`{"component": 0}`, "integer ≥ 1"},
		{`{"component": 4}`, "only 3 components"},
		{`{"component": 1, "rotation": "varimax", "components": 4}`, "only 3 components"},
		{`{"component": 1, "components": 2}`, "needs rotation"},
		{`{"component": 2, "rotation": "varimax"}`, "required with rotation"},
		{`{"component": 3, "rotation": "varimax", "components": 2}`, "≥ params.component"},
		{`{"component": 1, "rotation": "promax", "components": 2}`, "rotation must be"},
		{`{"component": 1, "on": "ranks"}`, "on must be"},
	} {
		_, err := newPCScoreAttribute(&types.Attribute{Type: types.ATTR_PC_SCORE, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(c.params)}, schema)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("params %s: %v, want %q", c.params, err, c.want)
		}
	}
}

// stubNoteModel is a fittedModel whose usable fit owes one note.
type stubNoteModel struct{}

func (stubNoteModel) fit(fittedState) *fitRefusal { return nil }
func (stubNoteModel) score([]float64) float64     { return 1 }
func (stubNoteModel) fitNotes() []*fitRefusal {
	return []*fitRefusal{{code: errors.PULSE_MATRIX_NOT_CONVERGED, msg: "stopped"}}
}

// TestFittedAttribute_FitNotesBecomeWarnings: a usable fit's notes
// (ATTR_PC_SCORE's unconverged rotation) become fit-scoped warnings
// while every row is still scored.
func TestFittedAttribute_FitNotesBecomeWarnings(t *testing.T) {
	schema := mahaSchema()
	a, err := newFittedVectorAttribute(&types.Attribute{Type: types.ATTR_PC_SCORE, Label: "pc", Fields: []string{"x", "y"}}, schema, nil, stubNoteModel{})
	if err != nil {
		t.Fatal(err)
	}
	vals, err := a.Compute(mahaRecords(schema, mahaRows, mahaGroups), "")
	if err != nil {
		t.Fatal(err)
	}
	if vals[0] != 1 {
		t.Fatalf("score %v, want the model's 1", vals[0])
	}
	w := a.attributeWarnings()
	if len(w) != 1 || w[0].Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) || w[0].Details["scope"] != "fit" || !strings.HasPrefix(w[0].Message, "pc: ") {
		t.Fatalf("warnings %+v, want one fit-scoped PULSE_MATRIX_NOT_CONVERGED", w)
	}
}
