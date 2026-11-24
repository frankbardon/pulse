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

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_covariance_panel_test.go pins the panel series covariance
// estimators (U69 E4-S1, #401) — covariance hac + entity (per-entity
// Newey–West) and driscoll_kraay — on REG_OLS, REG_GLM (binomial,
// poisson) and REG_BAYES_LINEAR against robust_vcov_panel.json
// (scripts/reference/gen_robust_vcov_panel.R: sandwich::vcovPL,
// cross-checked in R by plm::vcovNW / vcovSCC) over an unbalanced panel
// with interior gaps, where a lag counts periods; plus the refusals'
// predict / runtime parity and the plain hac's untouched echo.

// panelRow is one robust_vcov_panel.json row.
type panelRow struct {
	Week   float64 `json:"week"`
	Geo    string  `json:"geo"`
	GeoID  float64 `json:"geo_id"`
	X1     float64 `json:"x1"`
	X2     float64 `json:"x2"`
	Y      float64 `json:"y"`
	YBin   float64 `json:"y_bin"`
	YCount float64 `json:"y_count"`
	WFreq  float64 `json:"w_freq"`
	WProb  float64 `json:"w_prob"`
}

// panelCase is one robust_vcov_panel.json case.
type panelCase struct {
	Family     string          `json:"family"`
	Target     string          `json:"target"`
	Weight     string          `json:"weight"`
	Covariance string          `json:"covariance"`
	Entity     string          `json:"entity"`
	Lags       json.RawMessage `json:"lags"`
	LagsUsed   int             `json:"lags_used"`
	Periods    int             `json:"periods"`
	Entities   int             `json:"entities"`
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

type panelGolden struct {
	Rows  []panelRow  `json:"rows"`
	Cases []panelCase `json:"cases"`
}

// panelTol is the robust oracles' 1e-10 (OLS solves through Welford
// moments, GLM stops IRLS at a 1e-8 step, R polishes; both sides then
// multiply three k×k matrices).
const panelTol = 1e-10

var panelPredictors = []string{"x1", "x2"}

func loadPanel(t *testing.T) panelGolden {
	t.Helper()
	var g panelGolden
	loadMV(t, "robust_vcov_panel.json", &g)
	if len(g.Rows) != 78 || len(g.Cases) != 44 {
		t.Fatalf("golden has %d rows / %d cases, want 78 / 44", len(g.Rows), len(g.Cases))
	}
	return g
}

// panelCohort writes the golden's rows as panel.pulse: week u16 (the
// period), geo categorical_u8 and geo_id u8 (the same six entities),
// x1 / x2 / the responses / the weights f64.
func panelCohort(t *testing.T, rows []panelRow) (*Service, []byte) {
	t.Helper()
	dict := encoding.NewDictionary()
	ids := map[string]uint64{}
	for _, r := range rows {
		if _, ok := ids[r.Geo]; !ok {
			id, err := dict.Add(r.Geo)
			if err != nil {
				t.Fatal(err)
			}
			ids[r.Geo] = uint64(id)
		}
	}
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "geo", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "geo_id", Type: encoding.FieldTypeU8},
	}
	for _, n := range []string{"x1", "x2", "y", "y_bin", "y_count", "w_freq", "w_prob"} {
		fields = append(fields, encoding.Field{Name: n, Type: encoding.FieldTypeF64})
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset, fields[i].CsvColumnIdx = off, i
		off += fields[i].Type.ByteSize()
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, &encoding.Schema{Fields: fields}); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		vals := []uint64{uint64(r.Week), ids[r.Geo], uint64(r.GeoID)}
		for _, f := range []float64{r.X1, r.X2, r.Y, r.YBin, r.YCount, r.WFreq, r.WProb} {
			vals = append(vals, math.Float64bits(f))
		}
		for j, v := range vals {
			if err := encoding.WriteFieldValue(&buf, fields[j].Type, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	memFs := afero.NewMemMapFs()
	if err := afero.WriteFile(memFs, "panel.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), buf.Bytes()
}

// panelSpec is c's spec with its covariance keyed by entity ("" drops
// the entity).
func panelSpec(t *testing.T, c panelCase, entity string) *types.RegressionSpec {
	t.Helper()
	cov := &types.RegressionCovariance{Type: types.CovarianceType(c.Covariance), OrderBy: "week", Entity: entity}
	if err := json.Unmarshal(c.Lags, &cov.Lags); err != nil {
		t.Fatalf("lags %s: %v", c.Lags, err)
	}
	s := &types.RegressionSpec{Name: "fit", Target: c.Target, Predictors: panelPredictors, Vcov: true, Covariance: cov}
	switch c.Family {
	case "ols":
		s.Type = types.REG_OLS
	case "bayes":
		s.Type = types.REG_BAYES_LINEAR
	default:
		s.Type, s.Family = types.REG_GLM, c.Family
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func processPanel(t *testing.T, svc *Service, specs ...*types.RegressionSpec) []*types.RegressionResult {
	t.Helper()
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: "panel.pulse"},
		Regressions: specs,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp.Regressions
}

// TestRegressionCovariancePanel_MatchesROracle: every case — the
// coefficients, standard errors, p-values (OLS t on N* − k df; GLM Wald
// z; Bayes none), 95% intervals and the full Vcov match vcovPL;
// √diag(Vcov) is the standard error bit for bit; the echo names the
// order_by, the resolved lag, the period count and the entity with its
// level count; keying the entity by its integer id (geo_id) is the same
// fit to the bit; driscoll_kraay without an entity is the same
// estimator (period sums do not read the entity).
func TestRegressionCovariancePanel_MatchesROracle(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	svc, _ := panelCohort(t, g.Rows)
	keys := append([]string{"(intercept)"}, panelPredictors...)
	for _, c := range g.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s/%s", c.Family, c.Weight, c.Covariance, c.Lags), func(t *testing.T) {
			specs := []*types.RegressionSpec{panelSpec(t, c, "geo"), panelSpec(t, c, "geo_id")}
			if c.Covariance == "driscoll_kraay" {
				specs = append(specs, panelSpec(t, c, ""))
			}
			res := processPanel(t, svc, specs...)
			r := res[0]
			check := func(what string, got map[string]float64, want []float64) {
				for i, k := range keys {
					if !vcovNear(got[k], want[i], panelTol) {
						t.Errorf("%s[%s] = %.17g, R = %.17g", what, k, got[k], want[i])
					}
				}
			}
			check("coefficients", r.Coefficients, c.Coeffs)
			check("std_errors", r.StdErrors, c.StdErrors)
			check("ci_lower", r.CILower, c.CILower)
			check("ci_upper", r.CIUpper, c.CIUpper)
			switch c.Family {
			case "bayes":
				if r.PValues != nil {
					t.Errorf("bayes reports p-values %v", r.PValues)
				}
			default:
				check("p_values", r.PValues, c.PValues)
			}
			if c.Family == "ols" && !vcovNear(r.DF, c.DF, panelTol) {
				t.Errorf("df = %v, R = %v", r.DF, c.DF)
			}
			if r.Vcov == nil {
				t.Fatal("vcov absent")
			}
			for i := range keys {
				for j := range keys {
					if !vcovNear(r.Vcov.Values[i][j], c.Vcov[i][j], panelTol) {
						t.Errorf("vcov[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], r.Vcov.Values[i][j], c.Vcov[i][j])
					}
					if r.Vcov.Values[i][j] != r.Vcov.Values[j][i] {
						t.Errorf("vcov[%s][%s] not exactly symmetric", keys[i], keys[j])
					}
				}
				if math.Sqrt(r.Vcov.Values[i][i]) != r.StdErrors[keys[i]] {
					t.Errorf("√vcov[%s] = %.17g, std_error %.17g (must be exact)", keys[i], math.Sqrt(r.Vcov.Values[i][i]), r.StdErrors[keys[i]])
				}
			}
			echo := r.Covariance
			if echo == nil || string(echo.Type) != c.Covariance || echo.OrderBy != "week" || echo.Lags == nil || *echo.Lags != c.LagsUsed ||
				echo.Periods != c.Periods || echo.Entity != "geo" || echo.Entities != c.Entities || echo.Cluster != "" || echo.Clusters != 0 {
				t.Errorf("covariance echo %+v, want %s over week / lags %d / %d periods / entity geo, %d entities", echo, c.Covariance, c.LagsUsed, c.Periods, c.Entities)
			}
			byID := res[1]
			for _, k := range keys {
				if byID.StdErrors[k] != r.StdErrors[k] {
					t.Errorf("entity geo_id SE[%s] = %v, by geo %v", k, byID.StdErrors[k], r.StdErrors[k])
				}
			}
			if byID.Covariance == nil || byID.Covariance.Entity != "geo_id" || byID.Covariance.Entities != c.Entities {
				t.Errorf("geo_id echo %+v", byID.Covariance)
			}
			if c.Covariance == "driscoll_kraay" {
				bare := res[2]
				check("driscoll_kraay without entity std_errors", bare.StdErrors, c.StdErrors)
				if e := bare.Covariance; e == nil || e.Entity != "" || e.Entities != 0 || e.Periods != c.Periods {
					t.Errorf("entity-free echo %+v, want no entity, %d periods", e, c.Periods)
				}
			}
		})
	}
}

// TestRegressionCovariancePanel_PlainHACUnchanged: a hac without an
// entity keeps its one-series echo — no entity / entities / periods key
// on the wire — and the per-entity estimator moves the standard errors
// only (the coefficients are the classical fit's to the bit).
func TestRegressionCovariancePanel_PlainHACUnchanged(t *testing.T) {
	t.Parallel()

	g := loadRobust(t)
	svc, _ := robustCohort(t, g.Rows)
	plain := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: robustPredictors,
		Covariance: &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week"}}
	entity := *plain
	entity.Covariance = &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week", Entity: "region"}
	res := processOne(t, svc, plain, &entity)
	raw, err := json.Marshal(res[0].Covariance)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"entity"`, `"entities"`, `"periods"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("plain hac echo carries %s: %s", key, raw)
		}
	}
	if res[1].Covariance.Entities != 8 || res[1].Covariance.Periods != 48 {
		t.Errorf("per-entity echo %+v, want 8 entities / 48 periods", res[1].Covariance)
	}
	if res[1].Coefficients["x1"] != res[0].Coefficients["x1"] || res[1].StdErrors["x1"] == res[0].StdErrors["x1"] {
		t.Errorf("entity should move the SE only: coef %v / %v, SE %v / %v", res[1].Coefficients["x1"], res[0].Coefficients["x1"], res[1].StdErrors["x1"], res[0].StdErrors["x1"])
	}
}

// TestRegressionCovariancePanel_Refusals: the panel shape refusals fire
// in predict and the runtime alike (internal/regcov.Check); an entity
// repeating an order_by value is runtime-only (data), while the same
// order_by repeated ACROSS entities runs; an entity field of a
// non-partition type is predict's (SERVICE_VALIDATION).
func TestRegressionCovariancePanel_Refusals(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	svc, data := panelCohort(t, g.Rows)
	cases := []struct {
		name    string
		code    errors.Code
		reason  string
		predict bool
		runtime bool
		cov     *types.RegressionCovariance
		regType types.RegressionType
	}{
		{"dk without order_by", errors.PROCESSING_CONFIG, "", true, true, &types.RegressionCovariance{Type: types.CovarianceDriscollKraay, Entity: "geo"}, types.REG_OLS},
		{"entity on hc1", errors.PROCESSING_CONFIG, "", true, true, &types.RegressionCovariance{Type: types.CovarianceHC1, Entity: "geo"}, types.REG_GLM},
		{"entity is order_by", errors.PROCESSING_CONFIG, "", true, true, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week", Entity: "week"}, types.REG_BAYES_LINEAR},
		{"dk on multinomial", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "type", true, false, &types.RegressionCovariance{Type: types.CovarianceDriscollKraay, OrderBy: "week"}, types.REG_MULTINOMIAL},
		{"in-entity tie hac", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "order_by_ties", false, true, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "geo_id", Entity: "geo"}, types.REG_OLS},
		{"in-entity tie dk", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "order_by_ties", false, true, &types.RegressionCovariance{Type: types.CovarianceDriscollKraay, OrderBy: "geo_id", Entity: "geo"}, types.REG_GLM},
		{"float entity", errors.SERVICE_VALIDATION, "", true, false, &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week", Entity: "x1"}, types.REG_OLS},
		{"unknown entity", errors.SERVICE_VALIDATION, "", true, false, &types.RegressionCovariance{Type: types.CovarianceDriscollKraay, OrderBy: "week", Entity: "nope"}, types.REG_OLS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Name: "fit", Type: c.regType, Target: "y", Predictors: panelPredictors, Covariance: c.cov}
			switch c.regType {
			case types.REG_GLM:
				spec.Family, spec.Target = "poisson", "y_count"
			case types.REG_MULTINOMIAL:
				spec.Target = "geo"
			}
			req := &types.Request{Cohort: &types.Cohort{Filename: "panel.pulse"}, Regressions: []*types.RegressionSpec{spec}}
			if c.runtime {
				_, err := svc.Process(context.Background(), req)
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("Process err = %v, want %s", err, c.code)
				}
				if c.reason != "" && ce.Details["reason"] != c.reason {
					t.Errorf("details.reason = %v, want %s", ce.Details["reason"], c.reason)
				}
				if c.reason == "order_by_ties" && (ce.Details["entity"] != "geo" || !strings.HasPrefix(fmt.Sprint(ce.Details["entity_value"]), "geo_")) {
					t.Errorf("tie details %v, want the entity field and its value", ce.Details)
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

	// A week shared by every geo is no tie: plain hac on week refuses
	// it, the per-entity estimator runs.
	plain := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: panelPredictors,
		Covariance: &types.RegressionCovariance{Type: types.CovarianceHAC, OrderBy: "week"}}
	_, err := svc.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "panel.pulse"}, Regressions: []*types.RegressionSpec{plain}})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != "order_by_ties" {
		t.Errorf("plain hac over a panel: err %v, want order_by_ties", err)
	}
	plain.Covariance.Entity = "geo"
	if res := processPanel(t, svc, plain); res[0].Covariance == nil || res[0].Covariance.Entities != 6 {
		t.Errorf("per-entity hac over a panel: %+v", res[0].Covariance)
	}
}

// TestRegressionCovariancePanel_ProjectedDecode: under projected
// buffered decode the entity field is decoded (processing.NeededFields
// lists covariance.entity), so a panel fit is the full-decode fit to
// the bit.
func TestRegressionCovariancePanel_ProjectedDecode(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	full, _ := panelCohort(t, g.Rows)
	projected, _ := panelCohort(t, g.Rows)
	projected.SetProjectBufferedFields(true)
	for _, c := range g.Cases {
		if c.Weight != "none" || c.Lags[0] == '"' {
			continue
		}
		want := processPanel(t, full, panelSpec(t, c, "geo"))[0]
		got := processPanel(t, projected, panelSpec(t, c, "geo"))[0]
		for k, se := range want.StdErrors {
			if got.StdErrors[k] != se {
				t.Errorf("%s/%s: projected SE[%s] = %v, full %v", c.Family, c.Covariance, k, got.StdErrors[k], se)
			}
		}
		if got.Covariance == nil || got.Covariance.Entities != c.Entities {
			t.Errorf("%s/%s: projected echo %+v", c.Family, c.Covariance, got.Covariance)
		}
	}
}
