package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_covariance_test.go pins RegressionSpec.Covariance on REG_OLS
// end to end against mv_robust_vcov.json (scripts/reference/
// gen_robust_vcov.R): sandwich::vcovHC HC0–HC3, vcovCL (CR1) and
// NeweyWest over a shuffled, clustered, heteroskedastic AR(1) series —
// unweighted, frequency (asserted in R on the rep()-expanded rows) and
// probability (the frequency formula on w*) — plus the classical
// default's byte identity, the refusals and predict's buffered verdict.

// robustRow is one mv_robust_vcov.json row.
type robustRow struct {
	Week     float64 `json:"week"`
	Region   string  `json:"region"`
	RegionID float64 `json:"region_id"`
	X1       float64 `json:"x1"`
	X2       float64 `json:"x2"`
	X3       float64 `json:"x3"`
	Y        float64 `json:"y"`
	WFreq    float64 `json:"w_freq"`
	WProb    float64 `json:"w_prob"`
}

// robustCase is one mv_robust_vcov.json case.
type robustCase struct {
	Weight     string          `json:"weight"`
	Covariance string          `json:"covariance"`
	Lags       json.RawMessage `json:"lags"`
	LagsUsed   *int            `json:"lags_used"`
	Clusters   int             `json:"clusters"`
	NRows      int             `json:"n_rows"`
	NStar      float64         `json:"n_star"`
	DF         float64         `json:"df"`
	Terms      []string        `json:"terms"`
	Coeffs     []float64       `json:"coefficients"`
	StdErrors  []float64       `json:"std_errors"`
	PValues    []float64       `json:"p_values"`
	CILower    []float64       `json:"ci_lower"`
	CIUpper    []float64       `json:"ci_upper"`
	Vcov       [][]float64     `json:"vcov"`
}

type robustGolden struct {
	Rows  []robustRow  `json:"rows"`
	Cases []robustCase `json:"cases"`
}

// robustTol: lm solves a QR, Pulse folds Welford moments and inverts the
// centred Gram through gonum's Cholesky; the sandwich then multiplies
// three k×k matrices. Relative (absolute below 1).
const robustTol = 1e-10

var robustPredictors = []string{"x1", "x2", "x3"}

// robustCohort writes the golden's rows as a typed cohort: week u16 (the
// series order), region categorical_u8 and region_id u8 (the same eight
// clusters), the numeric columns f64.
func robustCohort(t *testing.T, rows []robustRow) (*Service, []byte) {
	t.Helper()
	dict := encoding.NewDictionary()
	ids := map[string]uint64{}
	for _, r := range rows {
		if _, ok := ids[r.Region]; !ok {
			id, err := dict.Add(r.Region)
			if err != nil {
				t.Fatal(err)
			}
			ids[r.Region] = uint64(id)
		}
	}
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "region_id", Type: encoding.FieldTypeU8},
	}
	for _, n := range []string{"x1", "x2", "x3", "y", "w_freq", "w_prob", "label"} {
		fields = append(fields, encoding.Field{Name: n, Type: encoding.FieldTypeF64})
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset, fields[i].CsvColumnIdx = off, i
		off += fields[i].Type.ByteSize()
	}
	schema := &encoding.Schema{Fields: fields}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		vals := []uint64{uint64(r.Week), ids[r.Region], uint64(r.RegionID)}
		for _, f := range []float64{r.X1, r.X2, r.X3, r.Y, r.WFreq, r.WProb, float64(i % 2)} {
			vals = append(vals, math.Float64bits(f))
		}
		for j, v := range vals {
			if err := encoding.WriteFieldValue(&buf, fields[j].Type, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	memFs := afero.NewMemMapFs()
	if err := afero.WriteFile(memFs, "robust.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), buf.Bytes()
}

func loadRobust(t *testing.T) robustGolden {
	t.Helper()
	var g robustGolden
	loadMV(t, "mv_robust_vcov.json", &g)
	if len(g.Rows) != 48 || len(g.Cases) != 21 {
		t.Fatalf("golden has %d rows / %d cases, want 48 / 21", len(g.Rows), len(g.Cases))
	}
	return g
}

// robustSpec is the OLS spec for c.
func robustSpec(t *testing.T, c robustCase, cluster string) *types.RegressionSpec {
	t.Helper()
	cov := &types.RegressionCovariance{Type: types.CovarianceType(c.Covariance)}
	switch c.Covariance {
	case "cluster":
		cov.Cluster = cluster
	case "hac":
		cov.OrderBy = "week"
		if err := json.Unmarshal(c.Lags, &cov.Lags); err != nil {
			t.Fatalf("lags %s: %v", c.Lags, err)
		}
	}
	s := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: robustPredictors, Vcov: true, Covariance: cov}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func processOne(t *testing.T, svc *Service, specs ...*types.RegressionSpec) []*types.RegressionResult {
	t.Helper()
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: "robust.pulse"},
		Regressions: specs,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp.Regressions
}

// TestRegressionCovariance_MatchesROracle: every mv_robust_vcov.json
// case — coefficients, standard errors, p-values, 95% intervals, df and
// the full Vcov match R's sandwich estimator; √diag(Vcov) is the
// standard error bit for bit; the echo names the estimator, the cluster
// count and the resolved lag; a cluster keyed by an integer id is the
// same fit to the bit as the categorical one.
func TestRegressionCovariance_MatchesROracle(t *testing.T) {
	t.Parallel()

	t.Run("REG_OLS", testRobustOLSOracle)
	t.Run("REG_GLM+REG_BAYES_LINEAR", testRobustGLMBayesOracle)
}

func testRobustOLSOracle(t *testing.T) {
	g := loadRobust(t)
	svc, _ := robustCohort(t, g.Rows)
	for _, c := range g.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s", c.Weight, c.Covariance, c.Lags), func(t *testing.T) {
			res := processOne(t, svc, robustSpec(t, c, "region"))[0]
			keys := append([]string{"(intercept)"}, robustPredictors...)
			check := func(what string, got map[string]float64, want []float64) {
				for i, k := range keys {
					if !vcovNear(got[k], want[i], robustTol) {
						t.Errorf("%s[%s] = %.17g, R = %.17g", what, k, got[k], want[i])
					}
				}
			}
			check("coefficients", res.Coefficients, c.Coeffs)
			check("std_errors", res.StdErrors, c.StdErrors)
			check("p_values", res.PValues, c.PValues)
			check("ci_lower", res.CILower, c.CILower)
			check("ci_upper", res.CIUpper, c.CIUpper)
			if !vcovNear(res.DF, c.DF, robustTol) {
				t.Errorf("df = %v, R = %v", res.DF, c.DF)
			}
			if res.Vcov == nil {
				t.Fatal("vcov absent")
			}
			for i := range keys {
				for j := range keys {
					if !vcovNear(res.Vcov.Values[i][j], c.Vcov[i][j], robustTol) {
						t.Errorf("vcov[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], res.Vcov.Values[i][j], c.Vcov[i][j])
					}
					if res.Vcov.Values[i][j] != res.Vcov.Values[j][i] {
						t.Errorf("vcov[%s][%s] not exactly symmetric", keys[i], keys[j])
					}
				}
				if math.Sqrt(res.Vcov.Values[i][i]) != res.StdErrors[keys[i]] {
					t.Errorf("√vcov[%s] = %.17g, std_error %.17g (must be exact)", keys[i], math.Sqrt(res.Vcov.Values[i][i]), res.StdErrors[keys[i]])
				}
			}
			echo := res.Covariance
			if echo == nil || string(echo.Type) != c.Covariance {
				t.Fatalf("covariance echo %+v, want type %s", echo, c.Covariance)
			}
			switch c.Covariance {
			case "cluster":
				if echo.Cluster != "region" || echo.Clusters != c.Clusters {
					t.Errorf("echo %+v, want cluster region / %d clusters", echo, c.Clusters)
				}
				byID := robustSpec(t, c, "region_id")
				idRes := processOne(t, svc, byID)[0]
				for _, k := range keys {
					if idRes.StdErrors[k] != res.StdErrors[k] {
						t.Errorf("cluster by region_id SE[%s] = %v, by region %v", k, idRes.StdErrors[k], res.StdErrors[k])
					}
				}
			case "hac":
				if echo.OrderBy != "week" || echo.Lags == nil || c.LagsUsed == nil || *echo.Lags != *c.LagsUsed {
					t.Errorf("echo %+v, want order_by week / lags %v", echo, c.LagsUsed)
				}
			}
		})
	}
}

// TestRegressionCovariance_ClassicalByteIdentical: no covariance block
// and an explicit {type: classical} give the same wire bytes — no
// "covariance" key, the classical standard errors — with and without
// vcov; a robust type moves the standard errors and never the
// coefficients.
func TestRegressionCovariance_ClassicalByteIdentical(t *testing.T) {
	t.Parallel()

	g := loadRobust(t)
	svc, _ := robustCohort(t, g.Rows)
	for _, vcov := range []bool{false, true} {
		plain := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: robustPredictors, Vcov: vcov}
		classical := *plain
		classical.Covariance = &types.RegressionCovariance{Type: types.CovarianceClassical}
		res := processOne(t, svc, plain, &classical)
		want, err := json.Marshal(res[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(want), `"covariance"`) {
			t.Fatalf("classical fit carries a covariance key: %s", want)
		}
		for i, r := range res[1:] {
			got, _ := json.Marshal(r)
			if !bytes.Equal(got, want) {
				t.Errorf("vcov=%v variant %d differs:\n got  %s\n want %s", vcov, i+1, got, want)
			}
		}
		robust := *plain
		robust.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3}
		r := processOne(t, svc, &robust)[0]
		if r.StdErrors["x1"] == res[0].StdErrors["x1"] || r.Coefficients["x1"] != res[0].Coefficients["x1"] {
			t.Errorf("hc3 should move the SE only: SE %v vs %v, coef %v vs %v", r.StdErrors["x1"], res[0].StdErrors["x1"], r.Coefficients["x1"], res[0].Coefficients["x1"])
		}
	}
}

// TestRegressionCovariance_Refusals: the spec-level refusals fire in
// predict and the runtime with the same code and details.reason (the
// one rule, internal/regcov); the data-dependent ones (one cluster, a
// repeated order_by) fire at runtime; field-type faults in predict.
func TestRegressionCovariance_Refusals(t *testing.T) {
	t.Parallel()

	g := loadRobust(t)
	svc, data := robustCohort(t, g.Rows)
	hc1 := func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1} }
	cases := []struct {
		name    string
		code    errors.Code
		reason  string
		predict bool // predict reports it too
		runtime bool
		mut     func(*types.RegressionSpec)
	}{
		{"ridge", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "penalty", true, true, func(s *types.RegressionSpec) { hc1(s); s.Penalty, s.Alpha = "l2", 0.1 }},
		{"lasso", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "penalty", true, true, func(s *types.RegressionSpec) { hc1(s); s.Penalty, s.Alpha, s.Vcov = "l1", 0.1, false }},
		{"resample", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "resample", true, true, func(s *types.RegressionSpec) { hc1(s); s.Resample = "jackknife" }},
		{"selection", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "selection", true, true, func(s *types.RegressionSpec) {
			hc1(s)
			s.Selection, s.Criterion = "forward", "aic"
		}},
		{"glm resample", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "resample", true, true, func(s *types.RegressionSpec) {
			hc1(s)
			s.Type, s.Family, s.Target, s.Resample = types.REG_GLM, "binomial", "label", "jackknife"
		}},
		{"glm one cluster", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "clusters", false, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "region"}
			s.Type, s.Family, s.Target, s.Predictors = types.REG_GLM, "binomial", "label", []string{"x1"}
		}},
		{"bayes order ties", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "order_by_ties", false, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "region_id"}
			s.Type = types.REG_BAYES_LINEAR
		}},
		{"one cluster", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "clusters", false, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "region"}
			s.Predictors = []string{"x1"}
		}},
		{"order ties", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "order_by_ties", false, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "region_id"}
		}},
		{"cluster without field", errors.PROCESSING_CONFIG, "", true, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster}
		}},
		{"order_by on hc1", errors.PROCESSING_CONFIG, "", true, true, func(s *types.RegressionSpec) { hc1(s); s.Covariance.OrderBy = "week" }},
		{"lags on cluster", errors.PROCESSING_CONFIG, "", true, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "region", Lags: types.LagsOf(2)}
		}},
		{"negative lags", errors.PROCESSING_CONFIG, "", true, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week", Lags: types.LagsOf(-1)}
		}},
		{"unknown type", errors.PROCESSING_CONFIG, "", true, true, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: "hc4"}
		}},
		{"float cluster", errors.SERVICE_VALIDATION, "", true, false, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "x1"}
		}},
		{"categorical order_by", errors.SERVICE_VALIDATION, "", true, false, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "region"}
		}},
		{"unknown cluster field", errors.SERVICE_VALIDATION, "", true, false, func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "nope"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: robustPredictors, Vcov: true}
			c.mut(spec)
			req := &types.Request{Cohort: &types.Cohort{Filename: "robust.pulse"}, Regressions: []*types.RegressionSpec{spec}}
			if c.name == "one cluster" || c.name == "glm one cluster" {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{g.Rows[0].Region}}}
			}
			if c.runtime {
				_, err := svc.Process(context.Background(), req)
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("Process err = %v, want %s", err, c.code)
				}
				if c.reason != "" && ce.Details["reason"] != c.reason {
					t.Errorf("details.reason = %v, want %s", ce.Details["reason"], c.reason)
				}
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(c.code) && (c.reason == "" || e.Details["reason"] == c.reason) {
					found = true
				}
			}
			if found != c.predict {
				t.Errorf("predict reports it = %v, want %v (errors %+v)", found, c.predict, pred.Errors)
			}
			if !c.predict && len(pred.Errors) != 0 {
				t.Errorf("a data-dependent refusal predict cannot see, yet predict errors %+v", pred.Errors)
			}
		})
	}
}

// TestRegressionCovariance_PredictBuffered: a robust covariance makes
// predict mark the request buffered and say why; the classical one
// streams, and the runtime takes the same path (the oracle test above
// passes through the buffered second pass; a streamed robust fit would
// fail with PROCESSING_INTERNAL).
func TestRegressionCovariance_PredictBuffered(t *testing.T) {
	t.Parallel()

	g := loadRobust(t)
	_, data := robustCohort(t, g.Rows)
	agg := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}}
	// REG_BAYES_LINEAR streams classically like REG_OLS, so a robust
	// covariance downgrades it the same way (E5-S2).
	for _, regType := range []types.RegressionType{types.REG_OLS, types.REG_BAYES_LINEAR} {
		for _, typ := range types.AllCovarianceTypes() {
			cov := &types.RegressionCovariance{Type: typ}
			switch typ {
			case types.CovarianceCluster:
				cov.Cluster = "region"
			case types.CovarianceHAC:
				cov.OrderBy = "week"
			}
			spec := &types.RegressionSpec{Name: "fit", Type: regType, Target: "y", Predictors: robustPredictors, Covariance: cov}
			req := &types.Request{Cohort: &types.Cohort{Filename: "robust.pulse"}, Aggregations: agg, Regressions: []*types.RegressionSpec{spec}}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			if len(pred.Errors) != 0 {
				t.Fatalf("%s %s: predict errors %+v", regType, typ, pred.Errors)
			}
			res := pred.Data.(*descriptor.PredictResult)
			robust := typ != types.CovarianceClassical
			if res.Streamable == robust {
				t.Errorf("%s %s: streamable = %v, want %v", regType, typ, res.Streamable, !robust)
			}
			if spec.Streamable() == robust {
				t.Errorf("%s %s: RegressionSpec.Streamable = %v", regType, typ, spec.Streamable())
			}
			mentions := false
			for _, r := range res.StreamableReasons {
				if strings.Contains(r, "covariance "+string(typ)) {
					mentions = true
				}
			}
			if mentions != robust {
				t.Errorf("%s %s: reasons %v, want a covariance reason = %v", regType, typ, res.StreamableReasons, robust)
			}
		}
	}
}
