//go:build heavy

package pulse_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestExamples_RunEndToEnd builds the shared fixture cohorts into a
// temp directory, then runs every example JSON across all categories
// (attributes, filterers, groupers, windows, features) through
// pulse.Process. Asserts no errors come back and no limit trips on the
// built-in defaults (the runtime half of
// TestLimitsDefaultsNeverTripGoldens for the heavyExamples, which run
// only here); every regression example fills Regressions with no slot
// failure; and the per-example figure checks (exampleChecks) hold.
// Catches both example bitrot (schema drift) and operator regressions.
func TestExamples_RunEndToEnd(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	fs := afero.NewOsFs()

	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	buildExampleFixtures(t, p, fs, tmp)

	categories := []struct {
		name    string
		dir     string
		minJSON int
		glob    string // "" = every *.json in dir
		check   func(t *testing.T, resp *pulse.Response)
	}{
		{"features", "features", 10, "", nil},
		{"attributes", "attributes", 6, "", nil},
		{"filterers", "filterers", 4, "", nil},
		{"groupers", "groupers", 5, "", nil},
		{"windows", "windows", 10, "", nil},
		{"aggregations", "aggregations", 4, "", nil},
		{"tests", "tests", 27, "", nil},
		{"regression", "regression", 17, "", checkRegressionExample},
		{"matrices", "matrices", 2, "", nil},
		{"crosstab", "crosstab", 13, "", nil},
		// The overlays directory mixes Request-, Compose- and Facet-host
		// examples, and only Request-host ones run through pulse.Process,
		// so this arm is scoped to the MATRIX-host axis-pairing family.
		{"overlays-crosstab-pairwise", "overlays", 4, "*_crosstab_pairwise_*.json", nil},
		// The flows overlays (U30 E7) on a from x to crosstab.
		{"overlays-crosstab-symmetry", "overlays", 1, "*_crosstab_symmetry.json", nil},
		{"overlays-crosstab-retention", "overlays", 1, "*_crosstab_retention.json", nil},
		// The three flows overlays straight from a long panel (U55).
		{"overlays-crosstab-previous-state", "overlays", 1, "*_crosstab_previous_state_*.json", nil},
	}

	// A renamed example would silently drop its figure checks.
	for key := range exampleChecks {
		if _, err := os.Stat(filepath.Join("internal", "examples", filepath.FromSlash(key))); err != nil {
			t.Errorf("exampleChecks %s: %v", key, err)
		}
	}
	for _, cat := range categories {
		t.Run(cat.name, func(t *testing.T) {
			pattern := cat.glob
			if pattern == "" {
				pattern = "*.json"
			}
			matches, err := filepath.Glob(filepath.Join("internal", "examples", cat.dir, pattern))
			if err != nil {
				t.Fatalf("glob %s: %v", cat.dir, err)
			}
			if len(matches) < cat.minJSON {
				t.Fatalf("category %s: expected >= %d examples, found %d", cat.name, cat.minJSON, len(matches))
			}
			for _, ex := range matches {
				t.Run(filepath.Base(ex), func(t *testing.T) {
					resp := runExample(t, p, ex, tmp)
					if trip := limitTripIn(t, nil, resp); trip != "" {
						t.Fatalf("%s tripped a limit under the defaults: %s", ex, trip)
					}
					if cat.check != nil {
						cat.check(t, resp)
					}
					key := cat.dir + "/" + filepath.Base(ex)
					if c, ok := exampleChecks[key]; ok {
						c(t, resp)
					}
				})
			}
		})
	}
}

// checkRegressionExample is the regression arm's tighter contract:
// every regression example emits a non-empty Regressions slice, each
// result typed, over rows (n_obs > 0) and not a failed slot — the drift
// between the example wire shape and the regression engine surface
// that a slot failing alone would otherwise hide.
func checkRegressionExample(t *testing.T, resp *pulse.Response) {
	t.Helper()
	if len(resp.Regressions) == 0 {
		t.Fatal("expected Regressions to be populated")
	}
	for i, r := range resp.Regressions {
		if r == nil || r.Type == "" {
			t.Errorf("regression result %d is empty (%+v)", i, r)
		}
		if r != nil && r.NObs == 0 {
			t.Errorf("regression result %d has zero n_obs", i)
		}
		if r != nil && r.Error != nil {
			t.Errorf("regression result %d failed: %s %s", i, r.Error.Code, r.Error.Message)
		}
	}
}

// exampleChecks are per-example figure checks, keyed
// "<dir>/<file>", run on the response TestExamples_RunEndToEnd already
// holds — so a heavyExamples entry is executed once.
var exampleChecks = map[string]func(t *testing.T, resp *pulse.Response){
	"matrices/17_sem_contribution.json": checkSEMContributionRecipe,
}

// checkSEMContributionRecipe holds matrices/17_sem_contribution.json over
// the brand_survey fixture: the := rows close the table (vectors.group
// null, vectors.label the name) with a delta-method SE, p and Wald
// interval; the planted wave-2 shift makes value's contribution
// negative and service's positive, each distinguishable from zero
// (p < 0.05, the interval excluding 0); the slot's Holm block adjusts
// every row with a p, := rows included; and the bootstrap slot keeps
// the estimates, echoes 200 replicates and replaces the SEs and
// intervals.
func checkSEMContributionRecipe(t *testing.T, resp *pulse.Response) {
	t.Helper()
	if len(resp.Matrices) != 2 || resp.Matrices[0].Error != nil || resp.Matrices[1].Error != nil {
		t.Fatalf("matrices %+v", resp.Matrices)
	}
	delta, boot := resp.Matrices[0], resp.Matrices[1]
	est := delta.Auxiliary["estimates"]
	row := func(name string) (int, []float64) {
		for i, k := range est.RowKeys {
			if strings.HasPrefix(k, name+" := ") {
				return i, est.Values[i]
			}
		}
		t.Fatalf("no %s := row in %v", name, est.RowKeys)
		return 0, nil
	}
	for name, sign := range map[string]float64{"dv": -1, "ds": 1} {
		i, r := row(name)
		v, p, lo, hi := r[0], r[4], r[5], r[6]
		if !(v*sign > 0) || !(p < 0.05) || !(lo*sign > 0 && hi*sign > 0) {
			t.Errorf("%s = %.4f, p %.3g, interval [%.4f, %.4f]: want a %+g-signed contribution distinguishable from zero", name, v, p, lo, hi, sign)
		}
		if g := delta.Vectors["group"].([]any)[i]; g != nil {
			t.Errorf("%s vectors.group %v, want null", name, g)
		}
		if l := delta.Vectors["label"].([]string)[i]; l != name {
			t.Errorf("%s vectors.label %q", name, l)
		}
	}
	adj := delta.Auxiliary["p_adjusted"]
	if adj == nil {
		t.Fatal("no p_adjusted under the slot's Holm block")
	}
	var family []float64
	for _, r := range est.Values {
		if !math.IsNaN(r[4]) {
			family = append(family, r[4])
		}
	}
	di, _ := row("dv")
	if math.IsNaN(adj.Values[di][0]) || !(adj.Values[di][0] >= est.Values[di][4]) || len(family) < 40 {
		t.Errorf("dv p_adjusted %v vs raw %v over a family of %d", adj.Values[di][0], est.Values[di][4], len(family))
	}
	if boot.Scalars["bootstrap_requested"] != 200 || !(boot.Scalars["bootstrap_succeeded"] >= 190) {
		t.Errorf("bootstrap echoes %v / %v", boot.Scalars["bootstrap_requested"], boot.Scalars["bootstrap_succeeded"])
	}
	bv := boot.Auxiliary["estimates"].Values
	if bv[di][0] != est.Values[di][0] || bv[di][2] == est.Values[di][2] || bv[di][5] == est.Values[di][5] {
		t.Errorf("bootstrap dv row %v vs delta %v: want the estimate kept, the SE and interval replaced", bv[di], est.Values[di])
	}
}
