package pulse

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The SPSS-sidecar advisories fire on an SPSS-imported cohort whose
// sidecar records the measure levels and weighting variable, and never
// on the same cohort without (or with a stale) sidecar: authoritative
// metadata only.

// measuredSavSpec: REGION an unlabelled numeric declared nominal,
// RATING one declared ordinal, ID scale, weighted BY WT.
func measuredSavSpec() spsstest.Spec {
	f8 := spsstest.Format{Type: spsstest.FormatF, Width: 8}
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Print: f8, Measure: spsstest.MeasureScale},
			{Name: "REGION", Print: f8, Measure: spsstest.MeasureNominal},
			{Name: "RATING", Print: f8, Measure: spsstest.MeasureOrdinal},
			{Name: "WT", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}, Measure: spsstest.MeasureScale},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1), spsstest.Num(2), spsstest.Num(2)},
			{spsstest.Num(2), spsstest.Num(3), spsstest.Num(4), spsstest.Num(3)},
			{spsstest.Num(3), spsstest.Num(2), spsstest.Num(5), spsstest.Num(5)},
		},
		WeightVar:     "WT",
		DisplayParams: true,
	}
}

func measuredRequest(w *types.WeightSpec) *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: suggestCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "REGION", Label: "m"}},
		Tests:        []*types.Test{{Type: types.TEST_T, Field: "RATING"}},
		Weight:       w,
	}
}

func advisoryCodesOf(t *testing.T, p *Pulse, req *Request) []string {
	t.Helper()
	res, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	var out []string
	for _, a := range res.Advisories {
		out = append(out, a.Code)
	}
	return out
}

var (
	advNominal = string(errors.PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC)
	advOrdinal = string(errors.PULSE_ADVISORY_ORDINAL_PARAMETRIC)
	advWeight  = string(errors.PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED)
)

func TestSidecarAdvisories_FireOnSPSSCohort(t *testing.T) {
	p := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{})
	got := advisoryCodesOf(t, p, measuredRequest(nil))
	if want := []string{advNominal, advOrdinal, advWeight}; !slices.Equal(got, want) {
		t.Fatalf("advisories = %v, want %v", got, want)
	}
	// A resolved weight silences only the weight advisory.
	got = advisoryCodesOf(t, p, measuredRequest(&types.WeightSpec{Field: "WT"}))
	if want := []string{advNominal, advOrdinal}; !slices.Equal(got, want) {
		t.Errorf("weighted advisories = %v, want %v", got, want)
	}
	// Suppression drops exactly the suppressed code.
	afs := afero.NewMemMapFs()
	sp := importSav(t, afs, measuredSavSpec(), Options{SuppressAdvisories: []string{advOrdinal}})
	if got := advisoryCodesOf(t, sp, measuredRequest(nil)); !slices.Equal(got, []string{advNominal, advWeight}) {
		t.Errorf("suppressed advisories = %v", got)
	}
}

// No sidecar (the cohort as any non-SPSS import leaves it), a stale one
// and a malformed one: the same request fires none of the three.
func TestSidecarAdvisories_SilentWithoutAUsableSidecar(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, afs afero.Fs){
		"no sidecar": func(t *testing.T, afs afero.Fs) {
			if err := afs.Remove(spss.SidecarPath(suggestCohort)); err != nil {
				t.Fatal(err)
			}
		},
		"stale sidecar": func(t *testing.T, afs afero.Fs) {
			later := time.Now().Add(time.Hour)
			if err := afs.Chtimes(suggestCohort, later, later); err != nil {
				t.Fatal(err)
			}
		},
		"malformed sidecar": func(t *testing.T, afs afero.Fs) {
			if err := afero.WriteFile(afs, spss.SidecarPath(suggestCohort), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			afs := afero.NewMemMapFs()
			p := importSav(t, afs, measuredSavSpec(), Options{})
			if len(advisoryCodesOf(t, p, measuredRequest(nil))) != 3 {
				t.Fatal("vacuous: the fresh sidecar fires nothing")
			}
			mutate(t, afs)
			if got := advisoryCodesOf(t, p, measuredRequest(nil)); len(got) != 0 {
				t.Errorf("advisories = %v, want none", got)
			}
		})
	}
}

// capability:weighting hidden: no weight advisory; the measure-level
// advisories (which are not weighting) still fire.
func TestSidecarAdvisories_WeightAdvisoryFollowsWeighting(t *testing.T) {
	for _, with := range []bool{true, false} {
		features := append(weightingProfile(with), "AGG_AVERAGE", "TEST_MANN_WHITNEY_U")
		p := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{
			FeatureProfile: &FeatureProfile{Profile: "w", Features: features},
		})
		got := advisoryCodesOf(t, p, measuredRequest(nil))
		if slices.Contains(got, advWeight) != with {
			t.Errorf("with=%v: advisories %v", with, got)
		}
		if !slices.Contains(got, advNominal) || !slices.Contains(got, advOrdinal) {
			t.Errorf("with=%v: measure-level advisories missing from %v", with, got)
		}
	}
}
