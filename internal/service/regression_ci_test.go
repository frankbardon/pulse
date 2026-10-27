package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// regression_ci_test.go pins RegressionResult.CILower / CIUpper / DF /
// ConfLevel end to end against R on the committed reference fixtures
// (internal/processing/testdata/reference/fixtures: mtcars, attitude,
// with their seeded w_freq / w_prob columns) — REG_OLS and REG_GLM
// binomial / poisson, unweighted, frequency and probability weights, at
// 0.95 and 0.90.

// ciOracle is one R case. df is R's residual df (0 = Wald normal, no
// df on the result).
type ciOracle struct {
	fixture, formula, family, weight string
	level, df                        float64
	lower, upper                     []float64
}

// ciOracles were computed by R 4.6.1 (2026-06-24) from the fixture CSVs
// (read.csv(comment.char = "#"), listwise on the formula's variables):
//
//   - gaussian, none: confint(lm(fml, d), level = lv); df.residual(fit).
//   - gaussian, frequency: confint(lm(fml, d[rep(seq_len(nrow(d)), w), ]),
//     level = lv) — the rep()-expanded rows, so df = Σw − p − 1 (the N*
//     rule, .claude/reference/weighting.md).
//   - gaussian, probability: the frequency formula on w* = w·n_eff/Σw,
//     n_eff = (Σw)²/Σw²: b = (XᵀW*X)⁻¹XᵀW*y, s² = Σw*e²/(n_eff − p − 1),
//     b ∓ qt(1 − (1 − lv)/2, n_eff − p − 1)·√diag(s²(XᵀW*X)⁻¹) —
//     fractional df.
//   - binomial / poisson: Wald b ∓ qnorm(1 − (1 − lv)/2)·√diag(V) at the
//     IRLS fixed point (glm(control = glm.control(epsilon = 1e-14,
//     maxit = 200), weights = w*) refined as gen_multivariate.R's
//     glm_fixed_point does), asserted within 1e-5 of R's
//     confint.default(fit, level = lv) — which evaluates vcov(glm) one
//     IRLS step behind (see vcovGLMTol).
var ciOracles = []ciOracle{
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "none", level: 0.95, df: 28,
		lower: []float64{10.363085233456655, -5.9006340592484054, -0.048509804868143977, -0.3888708288109135},
		upper: []float64{44.857968482953183, -2.8169603410769639, 0.012865261657059052, 1.4105382173010301}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "none", level: 0.95, df: 23,
		lower: []float64{-13.187128812204854, 0.28016866204083857, -0.35381805643321684, -0.028278723160616637, -0.37642934930816407, -0.26570179299058122, -0.58571106016514873},
		upper: []float64{34.761281583624346, 0.94620655357855887, 0.20771777023416182, 0.66894295591283781, 0.53989361637209621, 0.34246468759468801, 0.15159769699214615}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "none", level: 0.95, df: 0,
		lower: []float64{4.2771922815405059, -14.097968202458706, 0.001497292295325435},
		upper: []float64{33.455405152867812, -2.0689821624305962, 0.071013899869107777}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "none", level: 0.95, df: 0,
		lower: []float64{-0.64258681119123617, -0.25221855356620126, 0.0022647692837677912},
		upper: []float64{0.920163393091877, 0.26118242515771439, 0.0087097112145172947}},
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "frequency", level: 0.95, df: 83,
		lower: []float64{15.371493326170317, -5.5743876774351282, -0.03416491919572287, 0.050803524338076556},
		upper: []float64{37.272758424430918, -3.7288126050986317, 0.0052764935335777204, 1.1743963116087706}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "frequency", level: 0.95, df: 71,
		lower: []float64{-2.0401329885033768, 0.45973185059897947, -0.23866368425930656, 0.12744555625922277, -0.13588601601469236, -0.074549260329190403, -0.47586812936845008},
		upper: []float64{23.916602987449814, 0.76295240495346051, 0.030697724267169102, 0.4950415389009814, 0.30432363838687521, 0.26689201944600488, -0.03046810213684864}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "frequency", level: 0.95, df: 0,
		lower: []float64{8.5033632103242738, -10.261724120758881, 0.01455767319895802},
		upper: []float64{22.659866238657628, -4.0505792547256849, 0.061229005205046247}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "frequency", level: 0.95, df: 0,
		lower: []float64{-0.43670459241058374, -0.13715339282254252, 0.0032552707258924466},
		upper: []float64{0.56052786685222966, 0.18534093156666737, 0.0075641409398010447}},
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "probability", level: 0.95, df: 21.848764733922664,
		lower: []float64{13.308665912844148, -6.3290193692583685, -0.058276716813764395, -0.71275502689370618},
		upper: []float64{51.912408329416863, -2.5935027951997687, 0.012475420804148984, 1.2999630722801314}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "probability", level: 0.95, df: 17.287405650142993,
		lower: []float64{-30.307431083412041, 0.21292476999751941, -0.36270675850220491, 0.0032985970387968688, -0.44244777108280731, -0.19515688106125928, -0.80891307247033617},
		upper: []float64{26.058334572773198, 1.0706321532372198, 0.39019255873580105, 0.81208297887490621, 0.593126660137138, 0.48307262078182833, 0.12755240794874817}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "probability", level: 0.95, df: 0,
		lower: []float64{2.5383763531334296, -14.139900138499474, -0.0011839996077950415},
		upper: []float64{34.618243698976542, -1.3447009019159246, 0.06665885736814485}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "probability", level: 0.95, df: 0,
		lower: []float64{-0.89935748574399144, -0.25454963277435627, 0.0017197592888854255},
		upper: []float64{0.86523672818648945, 0.34809366401400266, 0.0088629292594218489}},
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "none", level: 0.9, df: 28,
		lower: []float64{13.287126062457292, -5.6392391216272992, -0.04330720136592911, -0.2363396408996975},
		upper: []float64{41.933927653952537, -3.0783552786980706, 0.007662658154844168, 1.2580070293898138}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "none", level: 0.9, df: 23,
		lower: []float64{-9.0754216333547966, 0.33728323074946076, -0.30566482527019578, 0.031509940518327395, -0.29785215175292595, -0.21354985586659381, -0.52248482002901342},
		upper: []float64{30.649574404774281, 0.88909198486993657, 0.15956453907114071, 0.60915429223389372, 0.46131641881685798, 0.29031275047070054, 0.088371456856010722}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "none", level: 0.9, df: 0,
		lower: []float64{6.6227346395133129, -13.130996839107143, 0.0070855080083963677},
		upper: []float64{31.109862794895008, -3.0359535257821602, 0.065425684156036848}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "none", level: 0.9, df: 0,
		lower: []float64{-0.51696236615612945, -0.21094790592468718, 0.0027828573640731819},
		upper: []float64{0.79453894805677028, 0.21991177751620036, 0.0081916231342119041}},
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "frequency", level: 0.9, df: 83,
		lower: []float64{17.163819462114841, -5.4233519987583243, -0.030937166161542441, 0.14275459244984334},
		upper: []float64{35.480432288486391, -3.8798482837754369, 0.0020487404993972877, 1.0824452434970036}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "frequency", level: 0.9, df: 71,
		lower: []float64{0.090512840847502218, 0.48462155977147925, -0.216553286318799, 0.15761949061011296, -0.0997516243462018, -0.046522221821914012, -0.43930768851437113},
		upper: []float64{21.785957158098931, 0.73806269578096073, 0.0085873263266615374, 0.46486760455009118, 0.26818924671838462, 0.23886498093872846, -0.067028542990927614}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "frequency", level: 0.9, df: 0,
		lower: []float64{9.6413587976535986, -9.7624302318894127, 0.018309430943890537},
		upper: []float64{21.521870651328303, -4.5498731435951534, 0.057477247460113734}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "frequency", level: 0.9, df: 0,
		lower: []float64{-0.35654029364899487, -0.11122911495821745, 0.0036016468952040875},
		upper: []float64{0.48036356809064085, 0.15941665370234231, 0.007217764770489403}},
	{fixture: "mtcars", formula: "mpg ~ wt + hp + qsec", family: "gaussian", weight: "probability", level: 0.9, df: 21.848764733922664,
		lower: []float64{16.630353186262127, -6.0075941113184674, -0.052188797343250909, -0.53956922478003322},
		upper: []float64{48.590721055998884, -2.9149280531396693, 0.006387501333635498, 1.1267772701664585}},
	{fixture: "attitude", formula: "rating ~ complaints + privileges + learning + raises + critical + advance", family: "gaussian", weight: "probability", level: 0.9, df: 17.287405650142993,
		lower: []float64{-25.36938457544219, 0.28806610622102535, -0.2967473627402839, 0.074153926438338125, -0.35172400206633225, -0.13573909822465063, -0.7268719604303665},
		upper: []float64{21.120288064803347, 0.99549081701371389, 0.32423316297388005, 0.7412276494753649, 0.50240289112066294, 0.42365483794521969, 0.04551129590877856}},
	{fixture: "mtcars", formula: "am ~ wt + hp", family: "binomial", weight: "probability", level: 0.9, df: 0,
		lower: []float64{5.1171733490167775, -13.111335369284298, 0.0042696686968641956},
		upper: []float64{32.039446703093191, -2.3732656711311009, 0.061205189063485613}},
	{fixture: "mtcars", formula: "carb ~ wt + hp", family: "poisson", weight: "probability", level: 0.9, df: 0,
		lower: []float64{-0.75750745223920146, -0.20610508321955784, 0.0022939756677287258},
		upper: []float64{0.72338669468169947, 0.29964911445920422, 0.0082887128805785477}},
}

// ciTol: the standard errors match R to ≈ 1e-13 (TestRegressionVcov_
// MatchesROracle), statdist's t / normal quantiles to 1e-10 relative
// (TestReferenceOracle_*); 1e-9 relative keeps headroom for cross-arch
// ulps while sitting far below the third-digit t-vs-1.96 gap.
const ciTol = 1e-9

func ciSpec(c ciOracle) *types.RegressionSpec {
	s := vcovSpec(vcovOracle{Formula: c.formula, Family: c.family, Weight: c.weight})
	s.Vcov = false
	if c.level != 0.95 {
		s.ConfLevel = c.level // 0.95 rides the default
	}
	return s
}

// TestRegressionCI_MatchesROracle: every case — bounds keyed like the
// coefficients, df (fractional under probability weights) and conf_level
// as R has them.
func TestRegressionCI_MatchesROracle(t *testing.T) {
	dir := filepath.Join("..", "processing", "testdata", "reference", "fixtures")
	cfg := fixtureFS(t, dir)
	if len(ciOracles) != 24 {
		t.Fatalf("%d oracle cases, want 24", len(ciOracles))
	}
	for _, c := range ciOracles {
		t.Run(fmt.Sprintf("%s/%s/%s/%v", c.fixture, c.family, c.weight, c.level), func(t *testing.T) {
			spec := ciSpec(c)
			resp, err := New(cfg).Process(context.Background(), &types.Request{
				Cohort:      &types.Cohort{Filename: filepath.Join(dir, c.fixture+".pulse")},
				Regressions: []*types.RegressionSpec{spec},
			})
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			r := resp.Regressions[0]
			if r.ConfLevel != c.level {
				t.Errorf("conf_level %v, want %v", r.ConfLevel, c.level)
			}
			if !vcovNear(r.DF, c.df, ciTol) {
				t.Errorf("df %.17g, R %.17g", r.DF, c.df)
			}
			keys := append([]string{"(intercept)"}, spec.Predictors...)
			if len(r.CILower) != len(keys) || len(r.CIUpper) != len(keys) {
				t.Fatalf("intervals keyed %v / %v, want %v", r.CILower, r.CIUpper, keys)
			}
			for i, k := range keys {
				if !vcovNear(r.CILower[k], c.lower[i], ciTol) || !vcovNear(r.CIUpper[k], c.upper[i], ciTol) {
					t.Errorf("%s: [%.17g, %.17g], R [%.17g, %.17g]", k, r.CILower[k], r.CIUpper[k], c.lower[i], c.upper[i])
				}
			}
		})
	}
}

// TestRegressionCI_ConfLevelRefusedByPredictAndRuntime: an out-of-range
// conf_level is PROCESSING_CONFIG from Process and from Predict alike.
func TestRegressionCI_ConfLevelRefusedByPredictAndRuntime(t *testing.T) {
	dir := filepath.Join("..", "processing", "testdata", "reference", "fixtures")
	cfg := fixtureFS(t, dir)
	data, err := os.ReadFile(filepath.Join(dir, "mtcars.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types.AllRegressionTypes() {
		for _, lv := range []float64{-0.5, 1, 2} {
			spec := &types.RegressionSpec{Type: typ, Target: "am", Predictors: []string{"wt"}, ConfLevel: lv}
			switch typ {
			case types.REG_GLM:
				spec.Family = "binomial"
			case types.REG_MMM_HILL:
				spec.Params = json.RawMessage(`{"channels":["wt"]}`)
			case types.REG_MMM_BAYES:
				spec.Params = json.RawMessage(`{"channels":["wt"],"time":"gear"}`)
			}
			req := &types.Request{
				Cohort:      &types.Cohort{Filename: filepath.Join(dir, "mtcars.pulse")},
				Regressions: []*types.RegressionSpec{spec},
			}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || !strings.Contains(ce.Message, "conf_level") {
				t.Errorf("%s conf_level %v: Process err %v, want the PROCESSING_CONFIG conf_level refusal", typ, lv, err)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(errors.PROCESSING_CONFIG) && e.Details["conf_level"] == lv {
					found = true
				}
			}
			if !found {
				t.Errorf("%s conf_level %v: predict errors %+v lack the PROCESSING_CONFIG refusal", typ, lv, pred.Errors)
			}
		}
	}
}
