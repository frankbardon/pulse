package pulse

// Named-table cells of the hidden-name parity harness
// (feature_parity_harness_test.go). A label table rides
// capability:labels and a range table capability:range_tables: with the
// capability hidden, a request naming a REGISTERED table must come out
// byte-identical to one naming a table that was never registered, on
// every entry point (Predict included), and differently from the same
// request on an unprofiled instance.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// parityDatesCohort is region (categorical), age (f64) and day (date).
const parityDatesCohort = "parity_dates.pulse"

func writeParityDatesCohort(t *testing.T, fsys afero.Fs) {
	t.Helper()
	region := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east", "west"} {
		if _, err := region.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: region},
		{Name: "age", Type: encoding.FieldTypeF64, ByteOffset: 1},
		{Name: "day", Type: encoding.FieldTypeDate, ByteOffset: 9},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	// 2024-01-01 is epoch day 19723; spread rows over 40 days.
	for i := 0; i < 40; i++ {
		rec := make([]byte, schema.RecordByteSize())
		rec[0] = byte(i % 4)
		binary.LittleEndian.PutUint64(rec[1:9], math.Float64bits(float64(10+(i*7)%53)))
		binary.LittleEndian.PutUint32(rec[9:13], uint32(19723+i))
		buf.Write(rec)
	}
	if err := afero.WriteFile(fsys, parityDatesCohort, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	neverLabelTable = "never_label_tbl"
	neverRangeTable = "never_range_tbl"
)

func tableParams(table string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"table": table})
	return b
}

// tableParityCategories place a table name in each slot that resolves
// one. The day field is the dates cohort's.
var tableParityCategories = []parityCategory{
	{
		name:       "label_table",
		candidates: []string{sweepLabelTable},
		never:      neverLabelTable,
		hiddenBy:   "capability:labels",
		neverOK:    chainStageLabelsIgnored,
		request: func(r *types.Request, op string, f parityFields) {
			r.Labels = append(r.Labels, &types.LabelBinding{Field: f.cat, Table: op})
		},
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Fields = append(r.Fields, f.cat)
			r.Labels = append(r.Labels, &types.LabelBinding{Field: f.cat, Table: op})
		},
	},
	{
		name:       "range_table_grouper",
		candidates: []string{sweepRangeTable},
		never:      neverRangeTable,
		hiddenBy:   "capability:range_tables",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups = append([]*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "day", Params: tableParams(op)}}, r.Groups...)
		},
	},
	{
		name:       "range_table_filter",
		candidates: []string{sweepRangeTable},
		never:      neverRangeTable,
		hiddenBy:   "capability:range_tables",
		request: func(r *types.Request, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "day", Params: tableParams(op)})
		},
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "day", Params: tableParams(op)})
		},
	},
}

// sampleParityEntryPoint drives SampleWithRequest, which carries label
// bindings.
var sampleParityEntryPoint = parityEntryPoint{name: "SampleWithRequest", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
	if c.name != "label_table" {
		return nil, false
	}
	req, _ := h.request(c, op)
	resp, err := h.p.SampleWithRequest(context.Background(), &SampleRequest{Cohort: req.Cohort, N: 3, Labels: req.Labels})
	return parityOutcome(resp, err), true
}}

// tableProfiles are inline profiles offering everything the table
// slots need, with one capability off. The file fixtures hide both.
func tableProfiles() map[string][]string {
	common := []string{
		"capability:process", "capability:stream", "capability:compose", "capability:process_chain",
		"capability:facet", "capability:sample", "AGG_COUNT", "GROUP_CATEGORY",
		"GROUP_DATE_RANGES", "FILTER_DATE_RANGES",
	}
	return map[string][]string{
		"labels_off":       append(append([]string(nil), common...), "capability:range_tables"),
		"range_tables_off": append(append([]string(nil), common...), "capability:labels"),
	}
}

// chainStageLabelsIgnored: a chain stage after the first resolves no
// label binding, so any table name — registered or not — succeeds there
// unchanged.
const chainStageLabelsIgnored = "a ProcessChain stage >= 1 ignores label bindings, so any table name succeeds there"

// tableVacuous: predict resolves no range table by name (a table it
// cannot see predicts like one it can), so the range cells are vacuous
// at the predict entry points; the chain gate refuses a
// GROUP_DATE_RANGES stage (not mergeable) for every table.
var tableVacuous = map[string]map[string]string{
	"ProcessChain/stage1": {
		"label_table": chainStageLabelsIgnored,
	},
	"Predict": {
		"range_table_grouper": "predict does not resolve a range table name",
		"range_table_filter":  "predict does not resolve a range table name",
	},
	"PredictBytes": {
		"range_table_grouper": "predict does not resolve a range table name",
		"range_table_filter":  "predict does not resolve a range table name",
	},
	"PredictChain/stage0": {
		"label_table":         "the chain validator does not validate a stage's label bindings",
		"range_table_grouper": "predict does not resolve a range table name",
		"range_table_filter":  "predict does not resolve a range table name",
	},
}

func tableEntryPoints() []parityEntryPoint {
	out := append([]parityEntryPoint(nil), parityEntryPoints...)
	out = append(out, sampleParityEntryPoint)
	for i, ep := range out {
		extra := tableVacuous[ep.name]
		if len(extra) == 0 {
			continue
		}
		merged := map[string]string{}
		for k, v := range ep.vacuousOK {
			merged[k] = v
		}
		for k, v := range extra {
			merged[k] = v
		}
		out[i].vacuousOK = merged
	}
	return out
}

// TestHiddenNamedTableParity: with capability:labels hidden a
// registered label table resolves exactly as an unregistered one (the
// label-binding validation and resolver, auto labels), and with
// capability:range_tables hidden a registered range table resolves
// exactly as an unregistered one (GROUP_DATE_RANGES / FILTER_DATE_RANGES
// `table:`), on every entry point.
func TestHiddenNamedTableParity(t *testing.T) {
	fsys := parityFS(t)
	writeParityDatesCohort(t, fsys)
	profiles := tableProfiles()
	fixtures := append([]string(nil), featureSetFixtures...)
	for name := range profiles {
		fixtures = append(fixtures, name)
	}
	sort.Strings(fixtures)
	runHiddenParityWith(t, fsys, parityHostConfig{
		options:  func(o *Options) { o.Extensions = sweepNamedTables() },
		cohort:   parityDatesCohort,
		profiles: profiles,
	}, fixtures, tableParityCategories, tableEntryPoints())
}

// TestHiddenLabelsCapability_AutoLabelsNotInjected: an instance's
// AutoLabels default binding references a registered label table; with
// capability:labels hidden the table is not offered, so the default is
// not injected (as for any table the instance does not offer) and the
// output equals the same profiled instance configured without
// AutoLabels. Unprofiled, the default applies.
func TestHiddenLabelsCapability_AutoLabelsNotInjected(t *testing.T) {
	fsys := parityFS(t)
	ext := Extensions{LabelTables: map[string]LabelTable{"region_names": {Rows: map[string]string{
		"north": "North", "south": "South", "east": "East", "west": "West",
	}}}}
	auto := []LabelBinding{{Field: "region", Table: "region_names"}}
	// A fresh request per run: Process injects the default into it.
	req := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: parityCohort},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
		}
	}
	run := func(profile []string, autoLabels []LabelBinding) string {
		opts := Options{FS: fsys, Extensions: ext, AutoLabels: autoLabels}
		if profile != nil {
			opts.FeatureProfile = &FeatureProfile{Features: profile}
		}
		p, err := New(opts)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		resp, err := p.Process(context.Background(), req())
		return string(parityOutcome(resp, err))
	}
	off := []string{"capability:process", "GROUP_CATEGORY", "AGG_COUNT"}
	on := append(append([]string(nil), off...), "capability:labels")
	if got, want := run(off, auto), run(off, nil); got != want {
		t.Errorf("hidden labels: auto default injected\ngot:  %s\nwant: %s", got, want)
	}
	if run(on, auto) == run(on, nil) || run(nil, auto) == run(nil, nil) {
		t.Fatal("vacuous: the auto default does not apply where labels are offered")
	}
}
