package model

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mmm_hill_adstock_test.go pins REG_MMM_HILL's in-model adstock static
// face (U71 E2-S2): the params probe, the term catalog, the lineage that
// records the fitted kernel instead of a window's, the delayed window's
// peak, the two request-level refusals and the advisory predicate.

// hillAdstockSpec is hillSpec with params.adstock (and extra params).
func hillAdstockSpec(extra string) *types.RegressionSpec {
	s := hillSpec()
	s.Params = json.RawMessage(`{"channels": ["tv_ad", "radio_ad"]` + extra + `}`)
	return s
}

// TestMMMHillAdstock_ProbeAndTerms: the probe sees params.adstock (null
// is absent; another type never), and the term catalog appends the
// kernel's coefficients after k and s per channel.
func TestMMMHillAdstock_ProbeAndTerms(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		spec   *types.RegressionSpec
		kernel string
		set    bool
	}{
		{hillAdstockSpec(`, "adstock": "delayed", "max_lag": 4`), "delayed", true},
		{hillAdstockSpec(`, "adstock": null`), "", false},
		{hillSpec(), "", false},
		{&types.RegressionSpec{Type: types.REG_MMM_BAYES, Params: json.RawMessage(`{"adstock": "geometric"}`)}, "", false},
	} {
		if k, ok := MMMHillAdstock(c.spec); k != c.kernel || ok != c.set {
			t.Errorf("%s: probe (%q, %v), want (%q, %v)", c.spec.Params, k, ok, c.kernel, c.set)
		}
	}
	terms, ok := ModelRegressionTerms(hillAdstockSpec(`, "adstock": "delayed", "max_lag": 4`))
	want := []string{"k:tv_ad", "k:radio_ad", "s:tv_ad", "s:radio_ad", "decay:tv_ad", "decay:radio_ad", "peak:tv_ad", "peak:radio_ad"}
	if !ok || !slices.Equal(terms, want) {
		t.Fatalf("terms (%v, %v), want %v", terms, ok, want)
	}
	if _, ok := ModelRegressionTerms(hillAdstockSpec(`, "adstock": "nope"`)); ok {
		t.Error("an unknown kernel's terms are known")
	}
}

// TestMMMHillAdstock_LineageRecordsTheFit: under params.adstock the
// record is the kernel at the fitted coefficients (unnormalised, the
// default max_lag 13 for geometric, no fixed_at) whatever window the
// request holds; StaticAdstockChannels names every channel.
func TestMMMHillAdstock_LineageRecordsTheFit(t *testing.T) {
	t.Parallel()

	_, res := hillFit()
	res.Coefficients["decay:tv_ad"], res.Coefficients["decay:radio_ad"] = 0.55, 0.2
	spec := hillAdstockSpec(`, "adstock": "geometric"`)
	got := AdstockLineage(&types.Request{}, spec, res)
	want := map[string]Adstock{
		"tv_ad":    {Kernel: "geometric", Decay: f64(0.55), MaxLag: MMMBayesDefaultMaxLag},
		"radio_ad": {Kernel: "geometric", Decay: f64(0.2), MaxLag: MMMBayesDefaultMaxLag},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lineage %s, want %s", mustJSONModel(t, got), mustJSONModel(t, want))
	}
	if ch := StaticAdstockChannels(&types.Request{}, spec); !slices.Equal(ch, []string{"radio_ad", "tv_ad"}) {
		t.Fatalf("static channels %v", ch)
	}
	if ch := StaticAdstockChannels(&types.Request{}, hillSpec()); len(ch) != 0 {
		t.Fatalf("without params.adstock or a window: static channels %v", ch)
	}
}

// TestAdstockLineage_DelayedWindowKeepsPeak: a REG_MMM_HILL channel
// adstocked upstream by a delayed WIN_DISTRIBUTED_LAG records its peak
// (the record was dropped before: a delayed kernel without a peak is
// not a valid kernel).
func TestAdstockLineage_DelayedWindowKeepsPeak(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	req := &types.Request{Windows: []*types.Window{
		distLag("tv", "tv_ad", `{"kernel": "delayed", "decay": 0.5, "peak": 2, "max_lag": 6, "normalize": false}`),
	}}
	got := AdstockLineage(req, spec, res)
	want := map[string]Adstock{"tv_ad": {Kernel: "delayed", Decay: f64(0.5), Peak: f64(2), MaxLag: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lineage %s, want %s", mustJSONModel(t, got), mustJSONModel(t, want))
	}
}

// TestMMMHillAdstock_Refusals: the holdout and pre-adstocked refusals
// fire only under params.adstock, with their reason and details.
func TestMMMHillAdstock_Refusals(t *testing.T) {
	t.Parallel()

	spec := hillAdstockSpec(`, "adstock": "geometric"`)
	spec.Holdout = "week > 90"
	if ce := MMMHillAdstockHoldoutRefusal(spec); ce == nil || ce.Code != errors.PROCESSING_CONFIG || ce.Details["reason"] != ReasonAdstockHoldout {
		t.Fatalf("holdout refusal %v", ce)
	}
	if ce := HoldoutScorerRefusal(spec, func(types.RegressionType) bool { return true }); ce == nil || ce.Details["reason"] != ReasonAdstockHoldout {
		t.Fatalf("HoldoutScorerRefusal %v", ce)
	}
	plain := hillSpec()
	plain.Holdout = "week > 90"
	if ce := MMMHillAdstockHoldoutRefusal(plain); ce != nil {
		t.Fatalf("plain HILL refused: %v", ce)
	}
	req := &types.Request{Windows: []*types.Window{distLag("radio", "radio_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 3}`)}}
	ce := MMMHillPreadstockedRefusal(req, spec)
	if ce == nil || ce.Details["reason"] != ReasonChannelPreadstocked || ce.Details["channel"] != "radio_ad" || ce.Details["window"] != 0 {
		t.Fatalf("pre-adstocked refusal %v", ce)
	}
	if ce := MMMHillPreadstockedRefusal(req, hillSpec()); ce != nil {
		t.Fatalf("without params.adstock: %v", ce)
	}
}

// TestMMMHillConditionAdvisory: the advisory fires above the threshold
// and on an undefined condition number (NaN in process, null read back),
// never at or below it, never without params.adstock or on a failed fit.
func TestMMMHillConditionAdvisory(t *testing.T) {
	t.Parallel()

	spec := hillAdstockSpec(`, "adstock": "geometric"`)
	res := func(cond any) *types.RegressionResult {
		return &types.RegressionResult{Outputs: map[string]any{MMMConditionKey: cond}}
	}
	for _, c := range []struct {
		name string
		spec *types.RegressionSpec
		res  *types.RegressionResult
		want bool
	}{
		{"above", spec, res(2e6), true},
		{"at", spec, res(MMMConditionThreshold), false},
		{"below", spec, res(10.0), false},
		{"infinite", spec, res(math.Inf(1)), true},
		{"null read back", spec, res(nil), true},
		{"no output", spec, &types.RegressionResult{}, false},
		{"plain hill", hillSpec(), res(2e6), false},
		{"failed", spec, &types.RegressionResult{Error: &types.SlotError{Code: "X"}, Outputs: map[string]any{MMMConditionKey: 2e6}}, false},
	} {
		msg, details, ok := MMMHillConditionAdvisory(c.spec, "regressions[0]", c.res)
		if ok != c.want {
			t.Errorf("%s: fired %v, want %v", c.name, ok, c.want)
			continue
		}
		if ok && (msg == "" || details["slot"] != "regressions[0]" || details["threshold"] != MMMConditionThreshold) {
			t.Errorf("%s: message %q details %v", c.name, msg, details)
		}
	}
}
