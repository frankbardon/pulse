package vectors

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func cmpAttr(typ types.AttributeType, params string, mod func(*types.Attribute)) *types.Attribute {
	a := &types.Attribute{Type: typ, Params: json.RawMessage(params)}
	mod(a)
	return a
}

func cmpVecs() []types.VectorSpec {
	return []types.VectorSpec{
		{Name: "vb", Fields: []string{"b1", "b2"}, Labels: []string{"x", "y"}},
		{Name: "vscale", Fields: []string{"b1", "b2"}, Labels: []string{"x", "y"}, Kind: types.VectorKindScale},
		{Name: "vmeasure", Fields: []string{"b1", "b2"}, Labels: []string{"x", "y"}, Kind: types.VectorKindMeasure},
		{Name: "vcomp", Fields: []string{"b1", "b2"}, Kind: types.VectorKindComposition},
		{Name: "vbin", Fields: []string{"b1", "b2"}, Kind: types.VectorKindBinary},
	}
}

// TestComparisonParams_Rules pins the comparison pair's slot rules
// through the one resolver predict and the runtime share: the decoded
// metric / reference of each valid slot and the code + reason (or
// pointer) of each refusal.
func TestComparisonParams_Rules(t *testing.T) {
	t.Parallel()

	sim, dist := types.ATTR_PROFILE_SIMILARITY, types.ATTR_VEC_DISTANCE
	vec := func(n string) func(*types.Attribute) { return func(a *types.Attribute) { a.Vector = n } }
	inline := func(a *types.Attribute) { a.Fields = []string{"b1", "b2"} }
	cases := []struct {
		name      string
		attr      *types.Attribute
		metric    string
		defaulted bool
		values    []float64
		form      string
		minValid  int
		fitted    bool
		code      errors.Code
		reason    string
		use       string
	}{
		// Metric resolution: explicit wins, else the kind's default.
		{name: "explicit", attr: cmpAttr(sim, `{"metric":"cosine","reference":[1,2]}`, vec("vb")), metric: "cosine", values: []float64{1, 2}, form: ReferenceValues, minValid: 1},
		{name: "scale similarity default", attr: cmpAttr(sim, `{"reference":[1,2]}`, vec("vscale")), metric: "correlation", defaulted: true, values: []float64{1, 2}, form: ReferenceValues, minValid: 1},
		{name: "scale distance default", attr: cmpAttr(dist, `{"reference":[1,2]}`, vec("vscale")), metric: "euclidean", defaulted: true, values: []float64{1, 2}, form: ReferenceValues, minValid: 1},
		{name: "measure distance default fits", attr: cmpAttr(dist, `{"reference":[1,2]}`, vec("vmeasure")), metric: "std_euclidean", defaulted: true, values: []float64{1, 2}, form: ReferenceValues, minValid: 1, fitted: true},
		{name: "composition distance default", attr: cmpAttr(dist, `{"reference":[1,2]}`, vec("vcomp")), metric: "aitchison", defaulted: true, values: []float64{1, 2}, form: ReferenceValues, minValid: 1},
		{name: "binary similarity default", attr: cmpAttr(sim, `{"reference":[1,0]}`, vec("vbin")), metric: "jaccard", defaulted: true, values: []float64{1, 0}, form: ReferenceValues, minValid: 1},
		{name: "binary distance default", attr: cmpAttr(dist, `{"reference":[1,0]}`, vec("vbin")), metric: "hamming", defaulted: true, values: []float64{1, 0}, form: ReferenceValues, minValid: 1},
		{name: "measure has no similarity default", attr: cmpAttr(sim, `{"reference":[1,2]}`, vec("vmeasure")), code: errors.PULSE_VECTOR_METRIC_REQUIRED},
		{name: "no kind no metric", attr: cmpAttr(dist, `{"reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_REQUIRED},
		{name: "empty metric", attr: cmpAttr(dist, `{"metric":"","reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		// Direction: a metric of the other direction points at the other
		// operator; one neither serves does not.
		{name: "distance on similarity op", attr: cmpAttr(sim, `{"metric":"euclidean","reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN, use: string(dist)},
		{name: "fitted on similarity op", attr: cmpAttr(sim, `{"metric":"std_euclidean","reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN, use: string(dist)},
		{name: "correlation on distance op", attr: cmpAttr(dist, `{"metric":"correlation","reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN, use: string(sim)},
		{name: "jaccard on distance op", attr: cmpAttr(dist, `{"metric":"jaccard","reference":[1,0]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN, use: string(sim)},
		{name: "chi_square not accepted", attr: cmpAttr(dist, `{"metric":"chi_square","reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN},
		{name: "unregistered", attr: cmpAttr(sim, `{"metric":"bogus","reference":[1,2]}`, inline), code: errors.PULSE_VECTOR_METRIC_UNKNOWN},
		{name: "cosine distance form", attr: cmpAttr(dist, `{"metric":"cosine","reference":[1,2]}`, inline), metric: "cosine", values: []float64{1, 2}, form: ReferenceValues, minValid: 1},
		// min_valid.
		{name: "min_valid", attr: cmpAttr(dist, `{"metric":"euclidean","min_valid":2,"reference":[1,2]}`, inline), metric: "euclidean", values: []float64{1, 2}, form: ReferenceValues, minValid: 2},
		{name: "min_valid beyond p", attr: cmpAttr(dist, `{"metric":"euclidean","min_valid":3,"reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "min_valid zero", attr: cmpAttr(dist, `{"metric":"euclidean","min_valid":0,"reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		// References.
		{name: "reference required", attr: cmpAttr(dist, `{"metric":"euclidean"}`, inline), code: errors.SERVICE_VALIDATION, reason: "reference_required"},
		{name: "literal wrong length", attr: cmpAttr(dist, `{"metric":"euclidean","reference":[1,2,3]}`, vec("vb")), code: errors.PULSE_VECTOR_REFERENCE_MISMATCH, reason: ReferenceReasonLengthDiffers},
		{name: "literal null element", attr: cmpAttr(dist, `{"metric":"euclidean","reference":[1,null]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		{name: "label map reorders", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"y":5,"x":7}}`, vec("vb")), metric: "euclidean", values: []float64{7, 5}, form: ReferenceLabelMap, minValid: 1},
		{name: "label map over field names", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"b2":5,"b1":7}}`, inline), metric: "euclidean", values: []float64{7, 5}, form: ReferenceLabelMap, minValid: 1},
		{name: "label map over params labels", attr: cmpAttr(dist, `{"metric":"euclidean","labels":["p","q"],"reference":{"q":5,"p":7}}`, inline), metric: "euclidean", values: []float64{7, 5}, form: ReferenceLabelMap, minValid: 1},
		{name: "label map differs", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"x":1,"z":2}}`, vec("vb")), code: errors.PULSE_VECTOR_REFERENCE_MISMATCH, reason: ReferenceReasonLabelsDiffer},
		{name: "label map non-number", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"x":1,"y":"2"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		{name: "table key", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"table":"t","key":"k"}}`, vec("vb")), metric: "euclidean", form: ReferenceTableKey, minValid: 1},
		{name: "table key_field", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","key_field":"region"}}`, vec("vb")), metric: "cosine", form: ReferenceTableKeyField, minValid: 1},
		{name: "key and key_field", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","key":"k","key_field":"region"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		{name: "neither key", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		{name: "table unknown key", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","row":"k"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		{name: "key_field unknown", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","key_field":"nope"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "key_field_unknown"},
		{name: "key_field float", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","key_field":"a1"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "key_field_type"},
		{name: "key_field packed_bool", attr: cmpAttr(sim, `{"metric":"cosine","reference":{"table":"t","key_field":"flag"}}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "key_field_type"},
		{name: "key_field unsigned integer", attr: cmpAttr(dist, `{"metric":"euclidean","reference":{"table":"t","key_field":"a2"}}`, vec("vb")), metric: "euclidean", form: ReferenceTableKeyField, minValid: 1},
		{name: "reference scalar", attr: cmpAttr(sim, `{"metric":"cosine","reference":3}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "bad_reference"},
		// Labels and fit keys.
		{name: "labels on a named vector", attr: cmpAttr(sim, `{"metric":"cosine","labels":["p","q"],"reference":[1,2]}`, vec("vb")), code: errors.SERVICE_VALIDATION, reason: "labels_on_vector"},
		{name: "labels short", attr: cmpAttr(sim, `{"metric":"cosine","labels":["p"],"reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "labels duplicate", attr: cmpAttr(sim, `{"metric":"cosine","labels":["p","p"],"reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "fit_on beside a row-local metric", attr: cmpAttr(dist, `{"metric":"euclidean","fit_on":"filtered","reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "fit_on_row_local"},
		{name: "fit_on beside a fitted metric", attr: cmpAttr(dist, `{"metric":"mahalanobis","fit_on":[{"type":"FILTER_TRUE"}],"reference":[1,2]}`, inline), metric: "mahalanobis", values: []float64{1, 2}, form: ReferenceValues, minValid: 1, fitted: true},
		{name: "pairwise refused", attr: cmpAttr(dist, `{"metric":"mahalanobis","missing":"pairwise","reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "pairwise_unsupported"},
		{name: "fit_on on the similarity op", attr: cmpAttr(sim, `{"metric":"cosine","fit_on":"filtered","reference":[1,2]}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown key", attr: cmpAttr(sim, `{"metric":"cosine","reference":[1,2],"stat":"mean"}`, inline), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		// The shared vector-input rules.
		{name: "field set", attr: cmpAttr(sim, `{"metric":"cosine","reference":[1,2]}`, func(a *types.Attribute) { a.Field, a.Vector = "a1", "vb" }), code: errors.SERVICE_VALIDATION, reason: "field_on_vector_attribute"},
		{name: "unknown vector", attr: cmpAttr(sim, `{"metric":"cosine","reference":[1,2]}`, vec("nope")), code: errors.PULSE_VECTOR_UNKNOWN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: cmpVecs(), Attributes: []*types.Attribute{c.attr}}
			got, err := ResolveAttributes(req, testInputSchema(), nil, nil, nil)
			if c.code != "" {
				if err == nil {
					t.Fatalf("want %s, resolved %+v", c.code, got)
				}
				if err.Code != c.code {
					t.Fatalf("code %s, want %s (%s)", err.Code, c.code, err.Message)
				}
				if c.reason != "" && err.Details["reason"] != c.reason {
					t.Fatalf("reason %v, want %s (%s)", err.Details["reason"], c.reason, err.Message)
				}
				if use, _ := err.Details["use"].(string); use != c.use {
					t.Fatalf("use %q, want %q (%s)", use, c.use, err.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal %s: %s", err.Code, err.Message)
			}
			cfg, cerr := ComparisonParams("at", c.attr.Type, c.attr.Params, got[0].Members, testInputSchema())
			if cerr != nil {
				t.Fatal(cerr)
			}
			if cfg.Metric.Name() != c.metric || cfg.Defaulted != c.defaulted || cfg.Fitted() != c.fitted {
				t.Errorf("metric %s defaulted %v fitted %v, want %s %v %v", cfg.Metric.Name(), cfg.Defaulted, cfg.Fitted(), c.metric, c.defaulted, c.fitted)
			}
			if cfg.Reference.Form != c.form || !reflect.DeepEqual(cfg.Reference.Values, c.values) || cfg.MinValid != c.minValid {
				t.Errorf("reference %+v min_valid %d, want %s %v %d", cfg.Reference, cfg.MinValid, c.form, c.values, c.minValid)
			}
			if c.fitted != (len(got[0].FitOn) > 0) && c.name == "fit_on beside a fitted metric" {
				t.Errorf("fit_on %v not carried", got[0].FitOn)
			}
			if f := ComparisonFitted(c.attr.Type, c.attr, req.Vectors, nil); f != c.fitted {
				t.Errorf("ComparisonFitted = %v, want %v", f, c.fitted)
			}
			// The canonical rewrite over the literal members decodes back
			// to the same metric, reference and min_valid — the kind is
			// gone, the metric is written out.
			canon := cfg.CanonicalParams()
			lit := Resolved{Members: got[0].Members.Members, Labels: got[0].Members.Members}
			back, berr := ComparisonParams("at", c.attr.Type, canon, lit, testInputSchema())
			if berr != nil {
				t.Fatalf("canonical %s refused: %s", canon, berr.Message)
			}
			// A label map is written out in member order (a literal).
			wantRef := cfg.Reference
			if wantRef.Form == ReferenceLabelMap {
				wantRef.Form = ReferenceValues
			}
			if back.Metric.Name() != cfg.Metric.Name() || !reflect.DeepEqual(back.Reference, wantRef) ||
				back.MinValid != cfg.MinValid || !reflect.DeepEqual(back.Labels, cfg.Labels) || len(back.FitOn) != len(cfg.FitOn) {
				t.Errorf("canonical %s decodes to %+v, want %+v", canon, back, cfg)
			}
		})
	}
}

// TestComparisonTableIssue pins the table judgement predict and the
// runtime bind share: an unregistered table or key, labels that are
// not the vector's, and the by-label alignment of a good one.
func TestComparisonTableIssue(t *testing.T) {
	t.Parallel()

	tables := NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t":     {Labels: []string{"y", "x"}, Profiles: map[string][]float64{"k": {5, 7}, "j": {1, 2}}},
		"other": {Labels: []string{"x", "z"}, Profiles: map[string][]float64{"k": {1, 2}}},
	})
	cfgOf := func(ref string) ComparisonConfig {
		cfg, err := ComparisonParams("attributes[0]", types.ATTR_VEC_DISTANCE, json.RawMessage(`{"metric":"euclidean","reference":`+ref+`}`),
			Resolved{Name: "vb", Members: []string{"b1", "b2"}, Labels: []string{"x", "y"}}, testInputSchema())
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	cases := []struct {
		name   string
		ref    string
		static []float64
		perm   []int
		code   errors.Code
		reason string
	}{
		{name: "literal needs no table", ref: `[1,2]`, static: []float64{1, 2}},
		{name: "key aligned by label", ref: `{"table":"t","key":"k"}`, static: []float64{7, 5}},
		{name: "key_field binds the permutation", ref: `{"table":"t","key_field":"region"}`, perm: []int{1, 0}},
		{name: "unknown table", ref: `{"table":"nope","key":"k"}`, code: errors.PULSE_VECTOR_REFERENCE_UNKNOWN, reason: ReferenceReasonTableUnknown},
		{name: "unknown key", ref: `{"table":"t","key":"zz"}`, code: errors.PULSE_VECTOR_REFERENCE_UNKNOWN, reason: ReferenceReasonKeyUnknown},
		{name: "labels differ", ref: `{"table":"other","key":"k"}`, code: errors.PULSE_VECTOR_REFERENCE_MISMATCH, reason: ReferenceReasonLabelsDiffer},
		{name: "labels differ under key_field", ref: `{"table":"other","key_field":"region"}`, code: errors.PULSE_VECTOR_REFERENCE_MISMATCH, reason: ReferenceReasonLabelsDiffer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := ComparisonTableIssue("attributes[0]", cfgOf(c.ref), tables)
			if c.code != "" {
				if err == nil || err.Code != c.code || err.Details["reason"] != c.reason {
					t.Fatalf("got %v, want %s %s", err, c.code, c.reason)
				}
				if err.Details["slot"] != "attributes[0].params.reference" {
					t.Errorf("slot %v", err.Details["slot"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(b.Static, c.static) || !reflect.DeepEqual(b.Perm, c.perm) {
				t.Errorf("binding static %v perm %v, want %v %v", b.Static, b.Perm, c.static, c.perm)
			}
		})
	}
	// ReferenceIssues reports a comparison slot's table issue beside the
	// expressions' (predict's view: labels and keys, no values).
	req := &types.Request{
		Vectors: cmpVecs(),
		Attributes: []*types.Attribute{
			{Type: types.ATTR_VEC_DISTANCE, Vector: "vb", Params: json.RawMessage(`{"metric":"euclidean","reference":{"table":"t","key":"zz"}}`)},
			{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "vb", Params: json.RawMessage(`{"metric":"cosine","reference":{"table":"t","key":"k"}}`)},
			{Type: types.ATTR_PROFILE_SIMILARITY, Fields: []string{"b1", "b2"}, Params: json.RawMessage(`{"metric":"cosine","reference":{"table":"t","key_field":"region"}}`)},
		},
	}
	resolved, rerr := Resolve(req.Vectors, testInputSchema())
	if rerr != nil {
		t.Fatal(rerr)
	}
	view := ReferenceTables{"t": {Labels: tables["t"].Labels, Keys: tables["t"].Keys}}
	issues := ReferenceIssues(req, testInputSchema(), resolved, view)
	if len(issues) != 2 || issues[0].Details["reason"] != ReferenceReasonKeyUnknown || issues[1].Details["reason"] != ReferenceReasonLabelsDiffer ||
		issues[1].Details["attribute"] != "attributes[2]" {
		t.Fatalf("issues = %+v, want key_unknown on [0] and labels_differ on [2]", issues)
	}
}

// TestComparisonKindReadings pins the kind-driven readings outside the
// resolver: the smart-defaults fill (never inline fields, never over an
// explicit metric), the unsuited warning on an explicit poor fit, the
// per-slot two-pass predicate and the projection's key_field.
func TestComparisonKindReadings(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Vectors: cmpVecs(),
		Attributes: []*types.Attribute{
			{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "vscale", Params: json.RawMessage(`{"reference":[1,2]}`)},
			{Type: types.ATTR_VEC_DISTANCE, Vector: "vmeasure", Params: json.RawMessage(`{"metric":null,"reference":[1,2]}`)},
			{Type: types.ATTR_VEC_DISTANCE, Vector: "vscale", Params: json.RawMessage(`{"metric":"euclidean","reference":[1,2]}`)},
			{Type: types.ATTR_VEC_DISTANCE, Fields: []string{"b1", "b2"}, Params: json.RawMessage(`{"reference":[1,2]}`)},
			{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "vscale", Params: json.RawMessage(`{"metric":"cosine","reference":{"table":"t","key_field":"region"}}`)},
			{Type: types.ATTR_VEC_DISTANCE, Vector: "vcomp", Params: json.RawMessage(`{"metric":"euclidean","reference":[1,2]}`)},
		},
	}
	fills := AttributeMetricDefaults(req, nil)
	if len(fills) != 2 || fills[0].Index != 0 || fills[0].Metric != "correlation" || fills[1].Index != 1 || fills[1].Metric != "std_euclidean" {
		t.Fatalf("fills = %+v, want [0]=correlation, [1]=std_euclidean", fills)
	}
	var back struct {
		Metric    string    `json:"metric"`
		Reference []float64 `json:"reference"`
	}
	if err := json.Unmarshal(fills[1].Params, &back); err != nil || back.Metric != "std_euclidean" || !reflect.DeepEqual(back.Reference, []float64{1, 2}) {
		t.Errorf("filled params %s", fills[1].Params)
	}
	issues := MetricUnsuitedIssues(req, nil)
	var slots []string
	for _, w := range issues {
		slots = append(slots, w.Details["slot"].(string))
	}
	if want := []string{"attributes[4].params.metric", "attributes[5].params.metric"}; !reflect.DeepEqual(slots, want) {
		t.Errorf("unsuited slots %v, want %v", slots, want)
	}
	for i, want := range []bool{false, true, false, false, false, false} {
		if got := ComparisonFitted(req.Attributes[i].Type, req.Attributes[i], req.Vectors, nil); got != want {
			t.Errorf("ComparisonFitted[%d] = %v, want %v", i, got, want)
		}
	}
	if kf := ComparisonKeyField(req.Attributes[4].Type, req.Attributes[4]); kf != "region" {
		t.Errorf("key field %q", kf)
	}
	if kf := ComparisonKeyField(req.Attributes[0].Type, req.Attributes[0]); kf != "" {
		t.Errorf("literal reference key field %q", kf)
	}
	members, ok := AttributeInputMembers(req, testInputSchema())
	if !ok || !reflect.DeepEqual(members, []string{"b1", "b2", "region"}) {
		t.Errorf("projection members %v (%v), want the inline members and the key_field", members, ok)
	}
}

// TestComparisonKeyFieldAccepts pins the key_field type matrix over
// every field type: the categorical rungs and the unsigned integers are
// accepted (the integers matched by their base-10 form), everything
// else refused — and the refusal names exactly the accepted list.
func TestComparisonKeyFieldAccepts(t *testing.T) {
	t.Parallel()

	want := map[encoding.FieldType]bool{
		encoding.FieldTypeCategoricalU8: false, encoding.FieldTypeCategoricalU16: false, encoding.FieldTypeCategoricalU32: false,
		encoding.FieldTypeU4: true, encoding.FieldTypeU8: true, encoding.FieldTypeU16: true, encoding.FieldTypeU32: true, encoding.FieldTypeU64: true,
	}
	var accepted []string
	for ft := encoding.FieldTypeU8; ft <= encoding.FieldTypeSetU256; ft++ {
		integer, ok := want[ft]
		if got := ComparisonKeyFieldAccepts(ft); got != ok {
			t.Errorf("%s: accepts = %v, want %v", ft, got, ok)
		}
		if got := ComparisonKeyFieldInteger(ft); got != integer {
			t.Errorf("%s: integer = %v, want %v", ft, got, integer)
		}
		if ok {
			accepted = append(accepted, ft.String())
		}
		if !ok {
			schema := &encoding.Schema{Fields: []encoding.Field{{Name: "b1", Type: encoding.FieldTypeF64}, {Name: "b2", Type: encoding.FieldTypeF64}, {Name: "k", Type: ft}}}
			_, err := ComparisonParams("at", types.ATTR_VEC_DISTANCE, json.RawMessage(`{"metric":"euclidean","reference":{"table":"t","key_field":"k"}}`),
				Resolved{Members: []string{"b1", "b2"}, Labels: []string{"b1", "b2"}}, schema)
			if err == nil || err.Details["reason"] != "key_field_type" {
				t.Errorf("%s: refusal %v, want reason key_field_type", ft, err)
				continue
			}
			if got, _ := err.Details["accepted"].([]string); !reflect.DeepEqual(got, ComparisonKeyFieldTypes()) {
				t.Errorf("%s: details.accepted = %v", ft, err.Details["accepted"])
			}
		}
	}
	slices.Sort(accepted)
	listed := slices.Clone(ComparisonKeyFieldTypes())
	slices.Sort(listed)
	if !reflect.DeepEqual(accepted, listed) {
		t.Errorf("ComparisonKeyFieldTypes() = %v, the predicate accepts %v", listed, accepted)
	}
}
