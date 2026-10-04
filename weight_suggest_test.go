package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Inspect and predict SUGGEST an SPSS cohort's weighting variable, read
// from the SPSS metadata sidecar beside the cohort. Suggestion only:
// never applied, never a warning, silently absent on a missing / stale /
// malformed sidecar, and hidden with capability:weighting.

const suggestCohort = "survey.pulse"

// weightedSavSpec is a three-case file weighted BY WT (2, 3, 5).
func weightedSavSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "WT", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(2)},
			{spsstest.Num(2), spsstest.Num(3)},
			{spsstest.Num(3), spsstest.Num(5)},
		},
		WeightVar: "WT",
	}
}

// importSav imports spec into suggestCohort on afs (writing the SPSS
// sidecar) and returns a Pulse built with opts over the same Fs.
func importSav(t *testing.T, afs afero.Fs, spec spsstest.Spec, opts Options) *Pulse {
	t.Helper()
	raw, err := spsstest.Build(spec)
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	if err := afero.WriteFile(afs, "src/in.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	importer, err := New(Options{FS: afs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reader, err := pio.NewReader(pio.FormatSPSS, afs, "src/in.sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	job := pio.NewImportJob(reader, suggestCohort)
	job.FS = afs
	if _, err := importer.Import(context.Background(), job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	opts.FS = afs
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func inspectSuggestion(t *testing.T, p *Pulse) (*descriptor.Envelope, *descriptor.SuggestedWeight) {
	t.Helper()
	env, err := p.InspectEnvelope(context.Background(), suggestCohort, nil)
	if err != nil {
		t.Fatalf("InspectEnvelope: %v", err)
	}
	if len(env.Errors) > 0 || len(env.Warnings) > 0 {
		t.Fatalf("inspect errors=%v warnings=%v, want neither", env.Errors, env.Warnings)
	}
	return env, env.Data.(*descriptor.InspectResult).SuggestedWeight
}

func countRequest(weight *types.WeightSpec) *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: suggestCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "ID", Label: "s"}},
		Weight:       weight,
	}
}

var wantSuggestion = descriptor.SuggestedWeight{Field: "WT", Source: "spss_sidecar", Kind: "probability"}

func TestSuggestedWeight_InspectReportsSidecarWeight(t *testing.T) {
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, weightedSavSpec(), Options{})

	env, got := inspectSuggestion(t, p)
	if got == nil || *got != wantSuggestion {
		t.Fatalf("suggested_weight = %+v, want %+v", got, wantSuggestion)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"suggested_weight":{"field":"WT","source":"spss_sidecar","kind":"probability"}`) {
		t.Errorf("envelope JSON lacks the suggestion: %s", raw)
	}
	// The result-only wrapper carries it too.
	res, err := p.Inspect(context.Background(), suggestCohort)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if res.SuggestedWeight == nil || *res.SuggestedWeight != wantSuggestion {
		t.Errorf("Inspect suggested_weight = %+v", res.SuggestedWeight)
	}
}

// No sidecar, an unweighted file, a stale sidecar (variable gone, or the
// cohort fingerprint moved) and a malformed one: no suggestion, no
// warning, no error — and no key on the wire.
func TestSuggestedWeight_AbsentOrStaleIsSilent(t *testing.T) {
	cases := map[string]func(t *testing.T, afs afero.Fs){
		"no sidecar": func(t *testing.T, afs afero.Fs) {
			if err := afs.Remove(spss.SidecarPath(suggestCohort)); err != nil {
				t.Fatal(err)
			}
		},
		"variable not in schema": func(t *testing.T, afs afero.Fs) {
			path := spss.SidecarPath(suggestCohort)
			raw, err := afero.ReadFile(afs, path)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			doc["payload"].(map[string]any)["weight"].(map[string]any)["variable"] = "GONE"
			out, _ := json.Marshal(doc)
			if err := afero.WriteFile(afs, path, out, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"cohort fingerprint moved": func(t *testing.T, afs afero.Fs) {
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
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			afs := afero.NewMemMapFs()
			p := importSav(t, afs, weightedSavSpec(), Options{})
			mutate(t, afs)
			env, got := inspectSuggestion(t, p)
			if got != nil {
				t.Fatalf("suggested_weight = %+v, want none", got)
			}
			raw, _ := json.Marshal(env)
			if strings.Contains(string(raw), "suggested_weight") {
				t.Errorf("envelope carries a suggested_weight key: %s", raw)
			}
			res, err := p.Predict(context.Background(), countRequest(nil))
			if err != nil || !res.Valid {
				t.Fatalf("Predict: %v valid=%v", err, res != nil && res.Valid)
			}
			if res.SuggestedWeight != nil {
				t.Errorf("predict suggested_weight = %+v, want none", res.SuggestedWeight)
			}
		})
	}

	t.Run("unweighted file", func(t *testing.T) {
		spec := weightedSavSpec()
		spec.WeightVar = ""
		p := importSav(t, afero.NewMemMapFs(), spec, Options{})
		if _, got := inspectSuggestion(t, p); got != nil {
			t.Fatalf("suggested_weight = %+v, want none", got)
		}
	})
}

// Predict echoes the suggestion as DATA when no weight resolves and drops
// it once a weight resolves. (Never-a-warning under strict is pinned on
// the envelope in internal/descriptor: TestPredict_SuggestedWeightIsDataNotWarning.)
func TestSuggestedWeight_PredictEchoesWhenNoWeightResolves(t *testing.T) {
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, weightedSavSpec(), Options{})
	ctx := context.Background()

	res, err := p.Predict(ctx, countRequest(nil))
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !res.Valid {
		t.Fatalf("valid=false: %+v", res)
	}
	if res.SuggestedWeight == nil || *res.SuggestedWeight != wantSuggestion {
		t.Fatalf("predict suggested_weight = %+v, want %+v", res.SuggestedWeight, wantSuggestion)
	}
	if len(res.Weights) != 0 {
		t.Errorf("weights = %+v: the suggestion must not resolve as a weight", res.Weights)
	}

	// Every slot opted out: still nothing resolves, still echoed.
	optOut := countRequest(nil)
	optOut.Aggregations[0].Weight = types.NullSlotWeight()
	if res, err = p.Predict(ctx, optOut); err != nil || res.SuggestedWeight == nil {
		t.Fatalf("opted-out predict: err=%v suggested=%+v, want the echo", err, res.SuggestedWeight)
	}

	// A weight resolves: no echo.
	res, err = p.Predict(ctx, countRequest(&types.WeightSpec{Field: "WT"}))
	if err != nil {
		t.Fatalf("Predict weighted: %v", err)
	}
	if res.SuggestedWeight != nil {
		t.Errorf("suggested_weight = %+v beside a resolved weight, want none", res.SuggestedWeight)
	}
}

// The suggestion is never applied: an unweighted request over a cohort
// with a suggested weight sums raw values, and only an explicit weight
// weights them.
func TestSuggestedWeight_NeverAutoApplied(t *testing.T) {
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, weightedSavSpec(), Options{})
	ctx := context.Background()
	if _, got := inspectSuggestion(t, p); got == nil {
		t.Fatal("fixture suggests no weight; the test would be vacuous")
	}

	sum := func(w *types.WeightSpec) float64 {
		t.Helper()
		resp, err := p.Process(ctx, countRequest(w))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		v, ok := resp.Data[0]["s"].(float64)
		if !ok {
			t.Fatalf("s = %#v", resp.Data[0]["s"])
		}
		return v
	}
	if got := sum(nil); got != 6 {
		t.Errorf("unweighted sum = %v, want 6 (1+2+3): the suggested weight was applied", got)
	}
	if got := sum(&types.WeightSpec{Field: "WT"}); got != 1*2+2*3+3*5 {
		t.Errorf("explicitly weighted sum = %v, want 23", got)
	}
}

// capability:weighting hidden: no suggestion on inspect or predict.
func TestSuggestedWeight_HiddenWithWeighting(t *testing.T) {
	for _, with := range []bool{true, false} {
		afs := afero.NewMemMapFs()
		p := importSav(t, afs, weightedSavSpec(), Options{
			FeatureProfile: &FeatureProfile{Profile: "w", Features: weightingProfile(with)},
		})
		_, got := inspectSuggestion(t, p)
		res, err := p.Predict(context.Background(), &Request{
			Cohort:       &types.Cohort{Filename: suggestCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "ID", Label: "n"}},
		})
		if err != nil || !res.Valid {
			t.Fatalf("with=%v Predict: err=%v errors=%v", with, err, res)
		}
		if with != (got != nil) || with != (res.SuggestedWeight != nil) {
			t.Errorf("with=%v: inspect suggestion %+v, predict suggestion %+v", with, got, res.SuggestedWeight)
		}
	}
}
