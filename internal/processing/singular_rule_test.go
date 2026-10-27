package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// singularityOracle is one mv_singularity.json case
// (scripts/reference/gen_singularity.R).
type singularityOracle struct {
	Name     string      `json:"name"`
	Members  []string    `json:"members"`
	Rows     [][]float64 `json:"rows"`
	Groups   []float64   `json:"groups"`
	Singular bool        `json:"singular"`
	Rank     int         `json:"rank"`
	CholOK   bool        `json:"chol_ok"`
}

// singularityRecords builds the case's rows: every member an f64 field
// and the group a categorical "grp".
func singularityRecords(c singularityOracle) (*encoding.Schema, []*Record) {
	schema := &encoding.Schema{}
	for _, m := range c.Members {
		schema.Fields = append(schema.Fields, encoding.Field{Name: m, Type: encoding.FieldTypeF64})
	}
	schema.Fields = append(schema.Fields, encoding.Field{Name: "grp", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()})
	recs := make([]*Record, len(c.Rows))
	for i, r := range c.Rows {
		vals := map[string]float64{}
		for j, m := range c.Members {
			vals[m] = r[j]
		}
		vals["grp"] = float64(dictIDOrAdd(schema, "grp", strconv.FormatFloat(c.Groups[i], 'g', -1, 64)))
		recs[i] = NewRecord(schema, vals)
	}
	return schema, recs
}

// TestSingularRule_EverySurfaceAgreesWithROracle is TODO #408's proof:
// on each mv_singularity.json case — a borderline near-singular draw
// whose Cholesky still succeeds (R's chol() and Pulse's reference
// kernel alike), a full-rank control and an exact dependency — the one
// rule agrees with R's rank-tolerance verdict (Matrix::rankMatrix), and
// EVERY multivariate surface reaches that same verdict: the vector
// tests, the matrix operators and the fitted attributes. Before the
// rule was shared, the borderline case was singular to the tests and
// invertible to MAT_PCA / MAT_FACTOR's KMO and Bartlett,
// TEST_BARTLETT_SPHERICITY, MAT_PARTIAL_CORRELATION and
// MAT_COLLINEARITY, which asked only whether InverseSPD failed.
func TestSingularRule_EverySurfaceAgreesWithROracle(t *testing.T) {
	t.Parallel()

	doc := loadReference[singularityOracle](t, "mv_singularity")
	sawBorderline := false
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			schema, recs := singularityRecords(c)
			p := len(c.Members)
			cm, err := linalg.NewCoMoment(p, linalg.Listwise)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range c.Rows {
				cm.Add(r, 1)
			}
			r := cm.Corr()
			if got := rankDeficient(r.ToRows()); got != c.Singular {
				t.Fatalf("rule says singular=%v, R's rank-tolerance verdict is %v (rank %d)", got, c.Singular, c.Rank)
			}
			if got := rankDeficient(cm.Cov(1).ToRows()); got != c.Singular {
				t.Fatalf("rule on the covariance says singular=%v, want %v (the rule is scale-free)", got, c.Singular)
			}
			if c.Name == "near_singular" {
				// The borderline: R's chol() and the reference Cholesky
				// both factor it, so the old "InverseSPD failed" rule
				// called it invertible. (exact_dependency's chol() also
				// succeeds in R, but the reference kernel's last pivot
				// lands at or below 0 — the old rule already agreed.)
				if !c.Singular || !c.CholOK {
					t.Fatalf("near_singular is not the borderline in R: singular %v, chol_ok %v", c.Singular, c.CholOK)
				}
				if _, err := linalg.InverseSPD(r); err != nil {
					t.Fatalf("borderline case: the reference InverseSPD fails (%v), so the case no longer separates the rules", err)
				}
				sawBorderline = true
			}

			verdicts := map[string]bool{}
			// The minres start (MAT_FACTOR, MAT_RELIABILITY's omega)
			// falls back from the SMC exactly when the rule says so.
			_, verdicts["minresStart"] = minresStart(r)
			for name, test := range map[string]*types.Test{
				"TEST_HOTELLING_T2":        {Type: types.TEST_HOTELLING_T2, Fields: c.Members},
				"TEST_MANOVA":              {Type: types.TEST_MANOVA, Fields: c.Members, SplitBy: "grp"},
				"TEST_BOX_M":               {Type: types.TEST_BOX_M, Fields: c.Members, SplitBy: "grp"},
				"TEST_MARDIA":              {Type: types.TEST_MARDIA, Fields: c.Members},
				"TEST_BARTLETT_SPHERICITY": {Type: types.TEST_BARTLETT_SPHERICITY, Fields: c.Members},
			} {
				test.Label = "t"
				resp := singularityProcess(t, schema, recs, &types.Request{Tests: []*types.Test{test}})
				if len(resp.Tests) != 1 {
					t.Fatalf("%s: %d results", name, len(resp.Tests))
				}
				hit := false
				for _, w := range resp.Tests[0].Warnings {
					hit = hit || strings.HasPrefix(w, string(errors.PULSE_MATRIX_SINGULAR))
				}
				verdicts[name] = hit
			}

			between, _ := json.Marshal(map[string]any{"between": map[string]any{"type": "GROUP_CATEGORY", "field": "grp"}, "metric": "mahalanobis"})
			for name, spec := range map[string]types.MatrixSpec{
				"MAT_PCA":                 {Type: types.MAT_PCA},
				"MAT_FACTOR":              {Type: types.MAT_FACTOR, Params: json.RawMessage(`{"factors": 1}`)},
				"MAT_FACTOR/ml":           {Type: types.MAT_FACTOR, Params: json.RawMessage(`{"factors": 1, "extraction": "ml"}`)},
				"MAT_PARTIAL_CORRELATION": {Type: types.MAT_PARTIAL_CORRELATION},
				"MAT_COLLINEARITY":        {Type: types.MAT_COLLINEARITY},
				"MAT_DISTANCE":            {Type: types.MAT_DISTANCE, Params: between},
			} {
				spec.Name, spec.Fields = "m", c.Members
				var res *types.MatrixResult
				verdicts[name], res = singularityMatrix(t, schema, recs, spec)
				if name == "MAT_FACTOR/ml" && c.Singular && res != nil {
					// The warning names the maximum-likelihood fit as
					// withheld; the loadings must be null to match.
					for _, row := range res.Primary.Values {
						for _, v := range row {
							if !math.IsNaN(v) {
								t.Fatalf("MAT_FACTOR ml fitted loadings %v on a singular correlation", res.Primary.Values)
							}
						}
					}
				}
			}

			for name, attr := range map[string]*types.Attribute{
				"ATTR_MAHALANOBIS":  {Type: types.ATTR_MAHALANOBIS},
				"ATTR_PC_SCORE":     {Type: types.ATTR_PC_SCORE, Params: json.RawMessage(`{"component": 1}`)},
				"ATTR_FACTOR_SCORE": {Type: types.ATTR_FACTOR_SCORE, Params: json.RawMessage(`{"factor": 1, "factors": 1}`)},
			} {
				attr.Label, attr.Fields = "a", c.Members
				resp := singularityProcess(t, schema, recs, &types.Request{Attributes: []*types.Attribute{attr}})
				hit := false
				for _, w := range resp.Warnings {
					hit = hit || w.Code == string(errors.PULSE_MATRIX_SINGULAR)
				}
				verdicts[name] = hit
			}

			for name, got := range verdicts {
				if got != c.Singular {
					t.Errorf("%s says singular=%v, the rule (and R) say %v", name, got, c.Singular)
				}
			}
		})
	}
	if !sawBorderline {
		t.Fatal("the oracle has no borderline case (singular under the tolerance, Cholesky succeeding)")
	}
}

// singularityProcess runs req (plus one AGG_COUNT) over recs.
func singularityProcess(t *testing.T, schema *encoding.Schema, recs []*Record, req *types.Request) *types.Response {
	t.Helper()
	req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_COUNT, Field: "grp", Label: "n"})
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// singularityMatrix folds recs into one matrix slot and reports whether
// it refused or warned PULSE_MATRIX_SINGULAR, with the result (nil on a
// refusal).
func singularityMatrix(t *testing.T, schema *encoding.Schema, recs []*Record, spec types.MatrixSpec) (bool, *types.MatrixResult) {
	t.Helper()
	slots, err := buildMatrixSlotsFor(&types.Request{Matrices: []types.MatrixSpec{spec}}, schema, nil, FullComputePlan())
	if err != nil || len(slots) != 1 {
		t.Fatalf("%s: buildMatrixSlotsFor: %d slots, %v", spec.Type, len(slots), err)
	}
	for i, rec := range recs {
		rec.SetMergePosition(0, 0, i)
		if err := slots[0].UpdateRow(rec, ""); err != nil {
			t.Fatalf("%s: UpdateRow: %v", spec.Type, err)
		}
	}
	res, _, err := finalizeMatrixSlots(slots)
	if err != nil {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) && ce.Code == errors.PULSE_MATRIX_SINGULAR {
			return true, nil
		}
		t.Fatalf("%s: finalize: %v", spec.Type, err)
	}
	// A refused (singular) slot fails alone: its result carries the
	// error (per-slot partial success).
	if e := res[0].Error; e != nil {
		if e.Code == string(errors.PULSE_MATRIX_SINGULAR) {
			return true, nil
		}
		t.Fatalf("%s: slot failed: %s", spec.Type, e.Code)
	}
	for _, w := range res[0].Warnings {
		if w.Code == string(errors.PULSE_MATRIX_SINGULAR) {
			return true, &res[0]
		}
	}
	return false, &res[0]
}
