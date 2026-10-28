package descriptor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
)

// readsLine renders one slot as "path|key|request cols=[...] params=[...]"
// (+ vars / err), the shape the table tests compare.
func readsLine(s descriptor.SlotReads) string {
	var b strings.Builder
	b.WriteString(s.Path + "|" + s.Key + "|" + s.Request)
	b.WriteString(" cols=" + strings.Join(s.Columns, ","))
	b.WriteString(" params=" + strings.Join(s.Params, ","))
	if len(s.Variables) > 0 {
		b.WriteString(" vars=" + strings.Join(s.Variables, ","))
	}
	if s.Error != nil {
		b.WriteString(" err=" + s.Error.Code)
	}
	return b.String()
}

func readsLines(t *testing.T, doc string, root descriptor.ReadsRoot, inst *InstanceSnapshot) (descriptor.ReadsRoot, []string) {
	t.Helper()
	got, ce := RequestReads([]byte(doc), root, inst)
	if ce != nil {
		t.Fatalf("RequestReads: %v", ce)
	}
	out := make([]string, len(got.Slots))
	for i, s := range got.Slots {
		if s.Columns == nil || s.Params == nil {
			t.Errorf("slot %s|%s: nil columns / params", s.Path, s.Key)
		}
		out[i] = readsLine(s)
	}
	return got.Root, out
}

func TestRequestReads_RootKinds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		root descriptor.ReadsRoot
		want []string
	}{
		{
			name: "request params filter",
			doc:  `{"params":{"wave":"2"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.wave"}],"aggregations":[{"type":"AGG_COUNT","field":"id"}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{
				"aggregations[0]|field| cols=id params=",
				"filterers[0]|expression| cols=wave params=wave",
			},
		},
		{
			name: "request computed column feeds MAT_SEM group",
			doc: `{"attributes":[{"type":"ATTR_FORMULA","label":"period","expression":"wave == \"2\" ? 2 : 1"}],
				"vectors":[{"name":"items","fields":["q1","q2","q3"]}],
				"matrices":[{"type":"MAT_SEM","vector":"items","params":{"model":"f =~ q1 + q2 + q3","group":"period"}}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{
				"attributes[0]|expression| cols=wave params=",
				"matrices[0]|params.group| cols=period params=",
				"matrices[0]|vector| cols=items params=",
				"vectors[0]|fields| cols=q1,q2,q3 params=",
			},
		},
		{
			name: "request typed slots",
			doc: `{"time_zone_field":"tz","weight":{"field":"w"},
				"groups":[{"type":"GROUP_CATEGORY","field":"region"}],
				"windows":[{"type":"WIN_LAG","field":"sales","partition_by":["region"],"order_by":[{"field":"week"}]}],
				"regressions":[{"type":"REG_OLS","target":"y","predictors":["x1","x2"],"covariance":{"type":"cluster","cluster":"store"},"holdout":"week > 50","weight":"w2"}],
				"tests":[{"type":"TEST_T","field":"score","split_by":"arm","params":{"covariate":"baseline"}}],
				"sort":[{"field":"region"}],
				"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"r"}],"columns":[{"type":"GROUP_CATEGORY","field":"c"}],"cell":{"type":"AGG_MEAN","field":"v"}}}`,
			root: descriptor.ReadsRootRequest,
			want: []string{
				"|time_zone_field| cols=tz params=",
				"|weight.field| cols=w params=",
				"crosstab.cell|field| cols=v params=",
				"crosstab.columns[0]|field| cols=c params=",
				"crosstab.rows[0]|field| cols=r params=",
				"groups[0]|field| cols=region params=",
				"regressions[0]|covariance.cluster| cols=store params=",
				"regressions[0]|holdout| cols=week params=",
				"regressions[0]|predictors| cols=x1,x2 params=",
				"regressions[0]|target| cols=y params=",
				"regressions[0]|weight| cols=w2 params=",
				"sort[0]|field| cols=region params=",
				"tests[0]|field| cols=score params=",
				"tests[0]|params.covariate| cols=baseline params=",
				"tests[0]|split_by| cols=arm params=",
				"windows[0]|field| cols=sales params=",
				"windows[0]|order_by| cols=week params=",
				"windows[0]|partition_by| cols=region params=",
			},
		},
		{
			name: "nested components in params",
			doc: `{"attributes":[{"type":"ATTR_MAHALANOBIS","fields":["a","b"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"split == 0"}]}}],
				"features":[{"type":"FEAT_TARGET_ENCODE","field":"cat","params":{"target":"y","fit_on":"split == 0"}}],
				"matrices":[{"type":"MAT_DISTANCE","fields":["a","b"],"params":{"between":{"type":"GROUP_CATEGORY","field":"seg"}}}]}`,
			root: descriptor.ReadsRootRequest,
			want: []string{
				"attributes[0]|fields| cols=a,b params=",
				"attributes[0].params.fit_on[0]|expression| cols=split params=",
				"features[0]|field| cols=cat params=",
				"features[0]|params.fit_on| cols=split params=",
				"features[0]|params.target| cols=y params=",
				"matrices[0]|fields| cols=a,b params=",
				"matrices[0]|params.between.field| cols=seg params=",
			},
		},
		{
			name: "compose optimize and sub-requests",
			doc: `{"params":{"budget":100},
				"requests":[{"filterers":[{"type":"FILTER_EXPRESSION","expression":"spend < params.budget"}]}],
				"fits":[{"name":"m","request":{"regressions":[{"type":"REG_OLS","target":"sales","predictors":["tv"]}]}}],
				"optimize":[{"name":"o","variables":[{"name":"tv","lower":0,"upper":"params.budget"}],
					"objective":"coef(\"m\", \"tv\") * tv","direction":"maximize",
					"constraints":[{"expr":"tv <= params.budget","type":"ineq"}]}]}`,
			root: "",
			want: []string{
				"fits[0].request.regressions[0]|predictors|fits[0].request cols=tv params=",
				"fits[0].request.regressions[0]|target|fits[0].request cols=sales params=",
				"optimize[0]|objective| cols= params= vars=tv",
				"optimize[0].constraints[0]|expr| cols= params=budget vars=tv",
				"optimize[0].variables[0]|upper| cols= params=budget",
				"requests[0].filterers[0]|expression|requests[0] cols=spend params=budget",
			},
		},
		{
			name: "compose sweep and banner",
			doc: `{"sweep":{"axes":[{"name":"w","values":[1,2]}],"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == sweep_axis(\"w\")"}]}},
				"banner":{"request":{"aggregations":[{"type":"AGG_COUNT","field":"id"}]},
					"sections":[{"label":"s","filterers":[{"type":"FILTER_IN","field":"region","values":["n"]}],"rows":[{"type":"GROUP_CATEGORY","field":"age"}]}],
					"groups":[{"label":"g","columns":[{"type":"GROUP_CATEGORY","field":"sex"}]}]}}`,
			root: descriptor.ReadsRootComposed,
			want: []string{
				"banner.groups[0].columns[0]|field|banner.request cols=sex params=",
				"banner.request.aggregations[0]|field|banner.request cols=id params=",
				"banner.sections[0].filterers[0]|field|banner.request cols=region params=",
				"banner.sections[0].rows[0]|field|banner.request cols=age params=",
				"sweep.request.filterers[0]|expression|sweep.request cols=wave params=",
			},
		},
		{
			name: "chain stage reads root params",
			doc: `{"params":{"wave":"2"},"stages":[
				{"request":{"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.wave"}],"groups":[{"type":"GROUP_CATEGORY","field":"region"}]}},
				{"request":{"aggregations":[{"type":"AGG_SUM","field":"AGG_COUNT"}]}}]}`,
			root: "",
			want: []string{
				"stages[0].request.filterers[0]|expression|stages[0].request cols=wave params=wave",
				"stages[0].request.groups[0]|field|stages[0].request cols=region params=",
				"stages[1].request.aggregations[0]|field|stages[1].request cols=AGG_COUNT params=",
			},
		},
		{
			name: "facet filters and fields",
			doc:  `{"fields":["region","age"],"additive_fields":["spend"],"filterers":[{"type":"FILTER_EXPRESSION","expression":"age >= 18"},{"type":"FILTER_IN","field":"wave","values":["2"]}]}`,
			root: "",
			want: []string{
				"|additive_fields| cols=spend params=",
				"|fields| cols=region,age params=",
				"filterers[0]|expression| cols=age params=",
				"filterers[1]|field| cols=wave params=",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, got := readsLines(t, tc.doc, tc.root, nil)
			if !slices.Equal(got, tc.want) {
				t.Errorf("slots:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(tc.want, "\n     "))
			}
		})
	}
}

func TestRequestReads_DetectsRoot(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]descriptor.ReadsRoot{
		`{}`:                     descriptor.ReadsRootRequest,
		`{"filterers":[]}`:       descriptor.ReadsRootRequest,
		`{"requests":[]}`:        descriptor.ReadsRootComposed,
		`{"sweep":{}}`:           descriptor.ReadsRootComposed,
		`{"stages":[]}`:          descriptor.ReadsRootChain,
		`{"fields":["a"]}`:       descriptor.ReadsRootFacet,
		`{"optimize":[],"x":{}}`: descriptor.ReadsRootComposed,
	} {
		got, _ := readsLines(t, doc, "", nil)
		if got != want {
			t.Errorf("%s: root %q, want %q", doc, got, want)
		}
	}
}

// A malformed slot fails alone; the rest of the map is returned.
func TestRequestReads_PerSlotErrors(t *testing.T) {
	t.Parallel()
	doc := `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == {{wave}}"},{"type":"FILTER_IN","field":3}],
		"aggregations":[{"type":"AGG_MEAN","field":"spend","weight":7}],
		"matrices":[{"type":"MAT_SEM","fields":["a"],"params":{"group":{"x":1}}}]}`
	_, got := readsLines(t, doc, descriptor.ReadsRootRequest, nil)
	want := []string{
		"aggregations[0]|field| cols=spend params=",
		"aggregations[0]|weight| cols= params= err=SERVICE_VALIDATION",
		"filterers[0]|expression| cols= params= err=PULSE_FORMULA_COMPILE_ERROR",
		"filterers[1]|field| cols= params= err=SERVICE_VALIDATION",
		"matrices[0]|fields| cols=a params=",
		"matrices[0]|params.group| cols= params= err=SERVICE_VALIDATION",
	}
	if !slices.Equal(got, want) {
		t.Errorf("slots:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}

func TestRequestReads_WholeCallRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		doc  string
		root descriptor.ReadsRoot
	}{
		{`[1,2]`, ""}, {`not json`, ""}, {`null`, ""}, {`{}`, "process"},
	} {
		got, ce := RequestReads([]byte(tc.doc), tc.root, nil)
		if ce == nil || ce.Code != errors.SERVICE_VALIDATION {
			t.Errorf("%s / %q: want SERVICE_VALIDATION, got %v", tc.doc, tc.root, ce)
		}
		if got.Slots == nil {
			t.Errorf("%s: nil slots", tc.doc)
		}
	}
}

// An extension operator's params are read through its FieldInputs hook.
func TestRequestReads_ExtensionFieldInputs(t *testing.T) {
	t.Parallel()
	ext := &ExtensionsSnapshot{FieldInputs: map[string]func(json.RawMessage) []string{
		"aggregator|AGG_X_SPREAD": func(raw json.RawMessage) []string {
			var p struct{ Lo, Hi string }
			_ = json.Unmarshal(raw, &p)
			return []string{p.Lo, p.Hi}
		},
	}}
	inst := UnscopedInstanceSnapshot(ext)
	doc := `{"aggregations":[{"type":"AGG_X_SPREAD","params":{"lo":"p10","hi":"p90"}}]}`
	_, got := readsLines(t, doc, "", inst)
	want := []string{"aggregations[0]|params| cols=p10,p90 params="}
	if !slices.Equal(got, want) {
		t.Errorf("slots: got %v, want %v", got, want)
	}
}

// The manifest's names_column entries drive the params reads: a marked
// param is read, an unmarked lookalike is not.
func TestRequestReads_ManifestMarks(t *testing.T) {
	t.Parallel()
	doc := `{"aggregations":[{"type":"AGG_RATIO","params":{"numerator_field":"n","denominator_field":"d"}}],
		"matrices":[{"type":"MAT_PARTIAL_CORRELATION","fields":["a","b","c"],"params":{"control":"all"}}],
		"post_tests":[{"type":"TEST_T","field":"m","params":{"n_col":"n"}}]}`
	_, got := readsLines(t, doc, "", nil)
	want := []string{
		"aggregations[0]|params.denominator_field| cols=d params=",
		"aggregations[0]|params.numerator_field| cols=n params=",
		"matrices[0]|fields| cols=a,b,c params=",
		"post_tests[0]|field| cols=m params=",
	}
	if !slices.Equal(got, want) {
		t.Errorf("slots:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}

// BenchmarkRequestReads_ComposeSweep reads a ~1000-slot compose: 100
// sub-requests of 10 reading slots each (filters, attributes, groups,
// aggregations, a regression), the shape of a large model-fit sweep.
func BenchmarkRequestReads_ComposeSweep(b *testing.B) {
	sub := `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.wave and spend > 0"}],
		"attributes":[{"type":"ATTR_FORMULA","label":"period","expression":"week > params.cut ? 2 : 1"}],
		"groups":[{"type":"GROUP_CATEGORY","field":"region"}],
		"aggregations":[{"type":"AGG_MEAN","field":"sales"},{"type":"AGG_RATIO","params":{"numerator_field":"a","denominator_field":"b"}}],
		"regressions":[{"type":"REG_OLS","target":"sales","predictors":["tv","radio","period"],"holdout":"week > 40","weight":"w"}]}`
	var sb strings.Builder
	sb.WriteString(`{"params":{"wave":"2","cut":10},"requests":[`)
	for i := range 100 {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(sub)
	}
	sb.WriteString(`]}`)
	doc := []byte(sb.String())
	got, ce := RequestReads(doc, "", nil)
	if ce != nil || len(got.Slots) != 1000 {
		b.Fatalf("slots %d, err %v", len(got.Slots), ce)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = RequestReads(doc, "", nil)
	}
}
