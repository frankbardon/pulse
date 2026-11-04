package model

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// adstock_test.go pins the adstock lineage record (adstock.go): which
// channels get one (a REG_MMM_HILL channel that is exactly one
// WIN_DISTRIBUTED_LAG column; every REG_MMM_BAYES channel with a
// posterior decay), what it holds, and the model file's adstock section.

func f64(v float64) *float64 { return &v }

// distLag is a WIN_DISTRIBUTED_LAG of field into label.
func distLag(field, label, params string) *types.Window {
	return &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: field, Label: label,
		OrderBy: []types.OrderKey{{Field: "week"}}, Params: json.RawMessage(params)}
}

// hillSpec is a REG_MMM_HILL over channels tv_ad, radio_ad and the
// control price.
func hillSpec() *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_MMM_HILL, Name: "mmm", Target: "sales",
		Predictors: []string{"tv_ad", "radio_ad", "price"}, Params: json.RawMessage(`{"channels": ["tv_ad", "radio_ad"]}`)}
}

// hillFit is hillSpec's finished fit (coefficients only, plus its
// outputs, as the model regression reports them).
func hillFit() (*types.RegressionSpec, *types.RegressionResult) {
	return hillSpec(), &types.RegressionResult{Name: "mmm", Type: types.REG_MMM_HILL, NObs: 104,
		Coefficients: map[string]float64{InterceptKey: 10, "tv_ad": 3.5, "radio_ad": 1.25, "price": -2,
			"k:tv_ad": 40, "k:radio_ad": 12, "s:tv_ad": 1.5, "s:radio_ad": 2},
		Outputs: map[string]any{"k": map[string]any{"tv_ad": 40.0, "radio_ad": 12.0}}}
}

// TestAdstockLineage_Hill: a REG_MMM_HILL channel that is the column of
// exactly one WIN_DISTRIBUTED_LAG records that window's params as
// written (normalize defaulting to true), geometric and Weibull alike;
// every other construction — a WIN_LAG column, two windows writing the
// column, an adstock of another window's column, a feature or attribute
// overwriting it, an unresolved expression, a control, another type —
// records nothing for that channel.
func TestAdstockLineage_Hill(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	req := &types.Request{Windows: []*types.Window{
		distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.6000000000000001, "max_lag": 8}`),
		distLag("radio", "radio_ad", `{"kernel": "weibull_pdf", "shape": 1.5, "scale": 3, "max_lag": 6, "normalize": false}`),
		distLag("price", "price", `{"kernel": "geometric", "decay": 0.3, "max_lag": 2}`),
	}}
	got := AdstockLineage(req, spec, res)
	want := map[string]Adstock{
		"tv_ad":    {Kernel: "geometric", Decay: f64(0.6000000000000001), MaxLag: 8, Normalize: true},
		"radio_ad": {Kernel: "weibull_pdf", Shape: f64(1.5), Scale: f64(3), MaxLag: 6, Normalize: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lineage %s, want %s", mustJSONModel(t, got), mustJSONModel(t, want))
	}

	only := func(mut func(r *types.Request)) map[string]Adstock {
		r := &types.Request{Windows: []*types.Window{
			distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`),
		}}
		mut(r)
		return AdstockLineage(r, spec, res)
	}
	for name, mut := range map[string]func(r *types.Request){
		"win_lag column": func(r *types.Request) {
			r.Windows[0] = &types.Window{Type: types.WIN_LAG, Field: "tv", Label: "tv_ad", OrderBy: []types.OrderKey{{Field: "week"}}}
		},
		"two windows": func(r *types.Request) {
			r.Windows = append(r.Windows, distLag("tv", "tv_ad", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`))
		},
		"adstock of a lag": func(r *types.Request) {
			r.Windows[0].Field = "tv_l1"
			r.Windows = append(r.Windows, &types.Window{Type: types.WIN_LAG, Field: "tv", Label: "tv_l1", OrderBy: []types.OrderKey{{Field: "week"}}})
		},
		"feature overwrites": func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "tv", Label: "tv_ad"}}
		},
		"attribute overwrites": func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "tv_ad", Expression: "tv * 2"}}
		},
		"unresolved expression": func(r *types.Request) {
			r.Windows[0].Params = json.RawMessage(`{"kernel": "geometric", "decay": "params.d", "max_lag": 4}`)
		},
		"invalid kernel": func(r *types.Request) {
			r.Windows[0].Params = json.RawMessage(`{"kernel": "geometric", "decay": 1.5, "max_lag": 4}`)
		},
		"no window": func(r *types.Request) { r.Windows = nil },
	} {
		if got := only(mut); got != nil {
			t.Errorf("%s: recorded %s, want none", name, mustJSONModel(t, got))
		}
	}
	// The control's window records nothing (not a channel), and a type
	// other than the media-mix models records nothing at all.
	if _, ok := got["price"]; ok {
		t.Error("the control price got a record")
	}
	ols := &types.RegressionSpec{Type: types.REG_OLS, Target: "sales", Predictors: spec.Predictors, Params: spec.Params}
	if got := AdstockLineage(req, ols, res); got != nil {
		t.Errorf("REG_OLS recorded %v", got)
	}
	failed := *res
	failed.Error = &types.SlotError{Code: "PROCESSING_REGRESSION_NO_CONVERGE"}
	if got := AdstockLineage(req, spec, &failed); got != nil {
		t.Errorf("a failed fit recorded %v", got)
	}
}

// TestAdstockLineage_Bayes: a REG_MMM_BAYES channel records geometric at
// its decay:<ch> posterior mean (bit for bit) over the fit's max_lag —
// params.max_lag, else MMMBayesDefaultMaxLag — unnormalised; a channel
// whose decay is absent or undefined records nothing.
func TestAdstockLineage_Bayes(t *testing.T) {
	t.Parallel()

	decay := 0.41999999999999993
	spec := &types.RegressionSpec{Type: types.REG_MMM_BAYES, Target: "sales", Predictors: []string{"tv", "radio", "price"},
		Params: json.RawMessage(`{"channels": ["tv", "radio"], "time": "week"}`)}
	res := &types.RegressionResult{Type: types.REG_MMM_BAYES, Coefficients: map[string]float64{
		InterceptKey: 1, "tv": 2, "radio": 3, "price": -1, "decay:tv": decay, "decay:radio": math.NaN(),
		"k:tv": 1, "k:radio": 1, "s:tv": 1, "s:radio": 1, "sigma": 0.5}}
	got := AdstockLineage(nil, spec, res)
	want := map[string]Adstock{"tv": {Kernel: "geometric", Decay: &decay, MaxLag: MMMBayesDefaultMaxLag, Normalize: false}}
	if !reflect.DeepEqual(got, want) || math.Float64bits(*got["tv"].Decay) != math.Float64bits(decay) {
		t.Fatalf("lineage %s, want %s", mustJSONModel(t, got), mustJSONModel(t, want))
	}
	spec.Params = json.RawMessage(`{"channels": ["tv", "radio"], "time": "week", "max_lag": 8}`)
	if got := AdstockLineage(nil, spec, res); got["tv"].MaxLag != 8 {
		t.Fatalf("max_lag 8 recorded %+v", got["tv"])
	}
	delete(res.Coefficients, "decay:tv")
	if got := AdstockLineage(nil, spec, res); got != nil {
		t.Fatalf("no posterior decay recorded %v", got)
	}
}

// TestModelFile_AdstockRoundTrip: a Hill fit's file carrying its adstock
// lists feature adstock (beside outputs and sweep_axes), reads back the
// records exactly, binds them (Bound.Adstock — a copy, never the file's
// own), and re-encodes to the same bytes; weights come from the
// window's own reading.
func TestModelFile_AdstockRoundTrip(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	tenth, fifth := 0.1, 0.2
	sum := tenth + fifth // 0.30000000000000004, not the constant 0.3
	records := map[string]Adstock{
		"tv_ad":    {Kernel: "geometric", Decay: f64(sum), MaxLag: 8, Normalize: true},
		"radio_ad": {Kernel: "weibull_cdf", Shape: f64(2), Scale: f64(math.Nextafter(3, 4)), MaxLag: 5},
	}
	f, err := NewFile("mmm", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetSweepAxes(map[string]any{"decay_tv": json.Number("0.30000000000000004")}); err != nil {
		t.Fatal(err)
	}
	f.SetAdstock(records)
	got, data := roundTrip(t, f)
	wantFeatures(t, got, data, FeatureOutputs, FeatureSweepAxes, FeatureAdstock)
	if !reflect.DeepEqual(got.Adstock, records) {
		t.Fatalf("adstock %s, want %s", mustJSONModel(t, got.Adstock), mustJSONModel(t, records))
	}
	if !bytes.Contains(data, []byte(`"decay": 0.30000000000000004`)) || !bytes.Contains(data, []byte(`"normalize": false`)) {
		t.Fatalf("adstock section not written as expected:\n%s", data)
	}
	b, err := got.Bind("loaded")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := b.Adstock("tv_ad")
	if !ok || !reflect.DeepEqual(a, records["tv_ad"]) {
		t.Fatalf("Bound.Adstock(tv_ad) = %+v %v", a, ok)
	}
	*a.Decay = 0.9
	if again, _ := b.Adstock("tv_ad"); *again.Decay != sum {
		t.Fatal("Bound.Adstock returned the bound record itself, not a copy")
	}
	if _, ok := b.Adstock("price"); ok {
		t.Fatal("the control price has a record")
	}
	w, err := a.Weights()
	if err != nil || len(w) != 9 {
		t.Fatalf("weights %v %v", w, err)
	}
	re, _ := got.Encode()
	if !bytes.Equal(data, re) {
		t.Fatalf("re-encode differs:\n%s\n%s", data, re)
	}
	// SetAdstock with nothing leaves a plain fit's file without it.
	plain, _ := NewFile("mmm", spec, res, Provenance{})
	plain.SetAdstock(nil)
	if plain.Adstock != nil || !reflect.DeepEqual(plain.Features, []string{FeatureOutputs}) {
		t.Fatalf("SetAdstock(nil) gave %v %v", plain.Adstock, plain.Features)
	}
}

// TestDecodeFile_AdstockRefusals: the adstock section's rule — a
// media-mix fit's only, every key a channel, every record a valid
// kernel — and the listing rule are PULSE_MODEL_FILE_INVALID reason
// schema.
func TestDecodeFile_AdstockRefusals(t *testing.T) {
	t.Parallel()

	spec, res := hillFit()
	f, err := NewFile("mmm", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	f.SetAdstock(map[string]Adstock{"tv_ad": {Kernel: "geometric", Decay: f64(0.5), MaxLag: 4, Normalize: true}})
	good, _ := f.Encode()
	var base map[string]any
	if err := json.Unmarshal(good, &base); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"not a channel": {func(m map[string]any) {
			m["adstock"] = map[string]any{"price": map[string]any{"kernel": "geometric", "decay": 0.5, "max_lag": 4, "normalize": true}}
		}, `adstock for "price", which is not a channel`},
		"bad decay": {func(m map[string]any) {
			m["adstock"] = map[string]any{"tv_ad": map[string]any{"kernel": "geometric", "decay": 1.5, "max_lag": 4, "normalize": true}}
		}, "not a valid kernel: params.decay must be in [0, 1)"},
		"weibull decay": {func(m map[string]any) {
			m["adstock"] = map[string]any{"tv_ad": map[string]any{"kernel": "weibull_cdf", "decay": 0.5, "max_lag": 4, "normalize": true}}
		}, "not a valid kernel"},
		"empty":          {func(m map[string]any) { m["adstock"] = map[string]any{} }, "empty adstock"},
		"unlisted":       {func(m map[string]any) { m["features"] = []any{"outputs"} }, "carries adstock without listing feature adstock"},
		"listed without": {func(m map[string]any) { delete(m, "adstock") }, "lists feature adstock without its adstock section"},
		"unknown key": {func(m map[string]any) {
			m["adstock"] = map[string]any{"tv_ad": map[string]any{"kernel": "geometric", "decay": 0.5, "max_lag": 4, "lag": 1}}
		}, "unknown field"},
		"not mmm": {func(m map[string]any) { m["type"] = "REG_ORDINAL" }, "only a REG_MMM_HILL or REG_MMM_BAYES fit has"},
	}
	for name, tc := range cases {
		m := map[string]any{}
		raw, _ := json.Marshal(base)
		_ = json.Unmarshal(raw, &m)
		tc.edit(m)
		doc, _ := json.Marshal(m)
		_, err := DecodeFile("m"+FileSuffix, doc)
		ce := coded(t, err)
		if ce.Code != errors.PULSE_MODEL_FILE_INVALID || ce.Details["reason"] != ReasonFileSchema || !strings.Contains(ce.Message, tc.want) {
			t.Errorf("%s: %s %v %q, want schema %q", name, ce.Code, ce.Details["reason"], ce.Message, tc.want)
		}
	}
}

func mustJSONModel(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// inlineHillAdstock is an inline Hill model declaring tv's adstock with
// its numbers as params expressions.
const inlineHillAdstock = `{"model_format_version": 1, "type": "REG_MMM_HILL", "target": "sales",
	"predictors": ["tv", "price"], "terms": ["tv", "price"],
	"coefficients": {"(intercept)": 10, "tv": 3, "price": -2, "k:tv": 50, "s:tv": 1.5},
	"features": ["adstock"],
	"adstock": {"tv": {"kernel": "geometric", "decay": "params.d", "max_lag": "params.lag", "normalize": false}}}`

// TestInline_DeclaresAdstock: an inline model's adstock numbers are
// scalar expressions over params, resolved before the body is judged,
// and bind as the model's record; a non-integer max_lag is
// inline_not_integer at its field, and a resolved decay outside the
// kernel's range is the section's schema refusal.
func TestInline_DeclaresAdstock(t *testing.T) {
	t.Parallel()

	load := func(params types.Params) (*Set, *errors.CodedError) {
		rs := Refs{Models: []types.ModelRef{inlineRef("truth", inlineHillAdstock)}, Params: params}
		loaded, cerr := LoadFiles(rs, nil, stubInline)
		if cerr != nil {
			return nil, cerr
		}
		set, err := (*Set)(nil).WithLoaded(rs, loaded)
		if err != nil {
			t.Fatal(err)
		}
		return set, nil
	}
	set, cerr := load(types.Params{"d": 0.7, "lag": 6.0})
	if cerr != nil {
		t.Fatal(cerr)
	}
	b, err := set.Lookup("truth")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := b.Adstock("tv")
	if want := (Adstock{Kernel: "geometric", Decay: f64(0.7), MaxLag: 6}); !ok || !reflect.DeepEqual(a, want) {
		t.Fatalf("inline adstock %+v %v, want %+v", a, ok, want)
	}

	rs := Refs{Models: []types.ModelRef{inlineRef("truth", strings.Replace(inlineHillAdstock, `"params.lag"`, `"2.5"`, 1))}, Params: types.Params{"d": 0.7}}
	_, cerr = LoadFiles(rs, nil, stubInline)
	if cerr == nil || cerr.Details["reason"] != InlineReasonPrefix+"not_integer" || cerr.Details["field"] != "adstock.tv.max_lag" {
		t.Fatalf("max_lag 2.5: %v", cerr)
	}
	_, cerr = load(types.Params{"d": 1.5, "lag": 6.0})
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_FILE_INVALID || !strings.Contains(cerr.Message, "params.decay must be in [0, 1)") {
		t.Fatalf("decay 1.5: %v", cerr)
	}
}
