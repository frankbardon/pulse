package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/types"
)

// intFacetRows are the rows of the integer-coded facet fixture: per
// column a (u4), b (u8), c (u16), d (u32) — code 1 twice, 2 once, a code
// the "codes" table lacks once (9; d's 4000000000, past 2^31) and a null
// — beside an f64 x.
var intFacetRows = [][]any{
	{uint64(1), uint64(1), uint64(1), uint64(1), 1.0},
	{uint64(1), uint64(1), uint64(1), uint64(1), 2.0},
	{uint64(2), uint64(2), uint64(2), uint64(2), 3.0},
	{uint64(9), uint64(9), uint64(9), uint64(4000000000), 4.0},
	{nil, nil, nil, nil, 5.0},
}

// intFacetMiss is the code each column holds that the table lacks.
var intFacetMiss = map[string]string{"a": "9", "b": "9", "c": "9", "d": "4000000000"}

// intFacetPulse is an instance over c.pulse (every row) and arch.pulse,
// a two-shard archive of the same rows, with the "codes" table
// (1 → One, 2 → Two). opts tunes the instance (limits).
func intFacetPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	fields := []encoding.Field{}
	for _, f := range intLabelFields {
		fields = append(fields, encoding.Field{Name: f.name, Type: f.typ, Nullable: true})
	}
	fields = append(fields, encoding.Field{Name: "x", Type: encoding.FieldTypeF64})
	write := func(name string, rows [][]any) {
		b, err := iio.NewCohortBuild(context.Background(), fsys, name, &encoding.Schema{Fields: fields}, iio.CohortBuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if err := b.Append(row); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("c.pulse", intFacetRows)
	write("s1.pulse", intFacetRows[:2])
	write("s2.pulse", intFacetRows[2:])
	opts.FS = fsys
	opts.Extensions = Extensions{LabelTables: map[string]LabelTable{
		"codes": {Rows: map[string]string{"1": "One", "2": "Two"}},
	}}
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{"s1.pulse", "s2.pulse"}); err != nil {
		t.Fatal(err)
	}
	return p
}

func intFacetReq(cohort, field string, labels []*types.LabelBinding) *types.FacetRequest {
	return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{field}, Labels: labels}
}

func intFacetWarningCodes(ws []*types.ResponseWarning) map[string]int {
	out := map[string]int{}
	for _, w := range ws {
		if w != nil {
			out[w.Code]++
		}
	}
	return out
}

// TestLabels_IntegerCodedFacetDiscrete: an augment binding on a u4 / u8 /
// u16 / u32 field turns its facet entry into discrete per-code counts —
// count descending, ties by value (the categorical rule), the null cell
// in null_count — with a "<field>_label" sibling carrying each code's
// label (the raw code on a miss, as a categorical facet's sibling does)
// and one PULSE_LABEL_LOOKUP_MISS naming the missed code; no
// PULSE_LABEL_NOT_APPLIED. A shard archive of the same rows facets
// identically. Without a binding the field keeps its numeric summary,
// and a binding on another field leaves it untouched.
func TestLabels_IntegerCodedFacetDiscrete(t *testing.T) {
	t.Parallel()
	p := intFacetPulse(t, Options{})
	ctx := context.Background()
	for _, f := range intLabelFields {
		t.Run(f.name, func(t *testing.T) {
			miss := intFacetMiss[f.name]
			wantValues := []types.FacetValueCount{{Value: "1", Count: 2}, {Value: "2", Count: 1}, {Value: miss, Count: 1}}
			wantLabels := []types.FacetValueCount{{Value: "One", Count: 2}, {Value: "Two", Count: 1}, {Value: miss, Count: 1}}
			for _, cohort := range []string{"c.pulse", "arch.pulse"} {
				got, err := p.FacetSchema(ctx, intFacetReq(cohort, f.name, augmentOn(f.name, "codes")))
				if err != nil {
					t.Fatalf("%s: %v", cohort, err)
				}
				ff := got.Fields[f.name]
				if ff == nil || ff.Kind != "discrete" || ff.Discrete == nil || ff.Numeric != nil {
					t.Fatalf("%s: entry = %+v, want a discrete entry", cohort, ff)
				}
				if ff.TypeName != f.typ.String() || ff.NullCount != 1 || ff.Discrete.DistinctCount != 3 {
					t.Errorf("%s: type %s nulls %d distinct %d, want %s 1 3", cohort, ff.TypeName, ff.NullCount, ff.Discrete.DistinctCount, f.typ)
				}
				if !reflect.DeepEqual(ff.Discrete.Values, wantValues) {
					t.Errorf("%s: values = %v, want %v", cohort, ff.Discrete.Values, wantValues)
				}
				sib := got.Fields[f.name+"_label"]
				if sib == nil || sib.Discrete == nil || !reflect.DeepEqual(sib.Discrete.Values, wantLabels) || sib.NullCount != 1 {
					t.Errorf("%s: sibling = %+v, want labels %v", cohort, sib, wantLabels)
				}
				codes := intFacetWarningCodes(got.Warnings)
				if codes[string(perrors.PULSE_LABEL_NOT_APPLIED)] != 0 {
					t.Errorf("%s: PULSE_LABEL_NOT_APPLIED fired: %v", cohort, got.Warnings)
				}
				if d := intLabelWarnings(got.Warnings)[string(perrors.PULSE_LABEL_LOOKUP_MISS)]; d == nil || !reflect.DeepEqual(d["unresolved_values"], []string{miss}) || d["field"] != f.name {
					t.Errorf("%s: PULSE_LABEL_LOOKUP_MISS details = %v, want the missed code %s", cohort, d, miss)
				}
			}

			plain, err := p.FacetSchema(ctx, intFacetReq("c.pulse", f.name, nil))
			if err != nil {
				t.Fatal(err)
			}
			if e := plain.Fields[f.name]; e.Kind != "numeric" || e.Numeric == nil || e.Discrete != nil || len(plain.Fields) != 1 {
				t.Errorf("unbound entry = %+v, want the numeric summary alone", e)
			}
			other := "a"
			if f.name == "a" {
				other = "b"
			}
			req := intFacetReq("c.pulse", f.name, augmentOn(other, "codes"))
			req.Fields = []string{f.name, other}
			mixed, err := p.FacetSchema(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			pj, _ := json.Marshal(plain.Fields[f.name])
			mj, _ := json.Marshal(mixed.Fields[f.name])
			if string(pj) != string(mj) {
				t.Errorf("a binding on %s moved %s's entry:\n%s\n%s", other, f.name, mj, pj)
			}
		})
	}
}

// TestLabels_IntegerCodedFacetPredictEqualsRuntime: numeric_percentiles
// and include_histogram compute nothing for a labelled integer-coded
// field — it is discrete now — so predict and the run raise the same
// PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL warnings for it (and none
// unbound), and an additive field the binding names is tallied as codes
// too.
func TestLabels_IntegerCodedFacetPredictEqualsRuntime(t *testing.T) {
	t.Parallel()
	p := intFacetPulse(t, Options{})
	ctx := context.Background()
	for _, f := range intLabelFields {
		for _, bound := range []bool{true, false} {
			var labels []*types.LabelBinding
			if bound {
				labels = augmentOn(f.name, "codes")
			}
			req := func() *types.FacetRequest {
				r := intFacetReq("c.pulse", f.name, labels)
				r.NumericPercentiles = []float64{0.5}
				r.IncludeHistogram = true
				r.HistogramRange = [2]float64{0, 10}
				r.AdditiveFields = []string{f.name}
				return r
			}
			env, err := p.PredictFacet(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.FacetSchema(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			var pw, rw []string
			for _, w := range env.Warnings {
				if w.Code == string(perrors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
					pw = append(pw, w.Message)
				}
			}
			for _, w := range got.Warnings {
				if w.Code == string(perrors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL) {
					rw = append(rw, w.Message)
				}
			}
			want := 0
			if bound {
				want = 2
			}
			if len(pw) != want || !reflect.DeepEqual(pw, rw) {
				t.Errorf("%s bound=%v: predict %v, runtime %v, want %d each", f.name, bound, pw, rw, want)
			}
			if add := got.Additive[f.name]; add == nil || (add.Kind == "discrete") != bound {
				t.Errorf("%s bound=%v: additive entry = %+v", f.name, bound, add)
			}
		}
	}
}

// TestLabels_IntegerCodedFacetOverlays: over a labelled integer-coded
// host field OVERLAY_INDEX_VS_POP, OVERLAY_ZSCORE_VS_POP and
// OVERLAY_CHISQ_VS_POP run on the discrete shape — the population is
// tallied as codes too, so the layer keys are the raw codes — while
// OVERLAY_KS_VS_POP (an ordered numeric CDF) is refused
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with predict's code, message
// and details. Unbound, CHISQ is refused and KS runs, as before.
func TestLabels_IntegerCodedFacetOverlays(t *testing.T) {
	t.Parallel()
	p := intFacetPulse(t, Options{})
	ctx := context.Background()
	kinds := []types.OverlayKind{types.OverlayKindIndexVsPop, types.OverlayKindZScoreVsPop, types.OverlayKindChiSqVsPop, types.OverlayKindKSVsPop}
	for _, f := range intLabelFields {
		for _, kind := range kinds {
			for _, bound := range []bool{true, false} {
				var labels []*types.LabelBinding
				if bound {
					labels = augmentOn(f.name, "codes")
				}
				req := func() *types.FacetRequest {
					r := intFacetReq("c.pulse", f.name, labels)
					r.NumericPercentiles = []float64{0.25, 0.5, 0.75}
					r.Overlays = []types.OverlaySpec{{
						Name:  "o",
						Kind:  kind,
						Scope: types.OverlayScopeGroup,
						Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "c.pulse"}},
					}}
					return r
				}
				refused := (kind == types.OverlayKindKSVsPop && bound) || (kind == types.OverlayKindChiSqVsPop && !bound)
				env, err := p.PredictFacet(ctx, req())
				if err != nil {
					t.Fatal(err)
				}
				got, rerr := p.FacetSchema(ctx, req())
				name := f.name + "/" + string(kind)
				if !bound {
					name += "/unbound"
				}
				if !refused {
					if rerr != nil || len(env.Errors) != 0 {
						t.Errorf("%s: predict %v, runtime %v; want it to run", name, env.Errors, rerr)
						continue
					}
					if len(got.Overlays) != 1 {
						t.Errorf("%s: %d layers, want 1", name, len(got.Overlays))
						continue
					}
					if bound && kind != types.OverlayKindChiSqVsPop {
						s := got.Overlays[0].Payload.Series
						if s == nil || len(s.Entries) != 3 || s.Entries[0].Key[0] != "1" {
							t.Errorf("%s: series = %+v, want 3 entries keyed by raw codes", name, s)
						}
					}
					continue
				}
				assertParity(t, name, envFirst(env), rerr, perrors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
				if ce := intLabelCoded(rerr); ce != nil && bound && ce.Details["label_coded"] != true {
					t.Errorf("%s: details %v lack label_coded", name, ce.Details)
				}
			}
		}
	}
}

// TestLabels_IntegerCodedFacetCardinality: a labelled integer-coded field
// meets the categorical facet's guards — discrete_top_k cuts its value
// list with PULSE_FACET_VALUES_TRUNCATED, Options.Limits.MaxGroups
// refuses the code past the limit (PULSE_LIMIT_EXCEEDED), and predict
// reports the possible MaxGroups bound (the code range clamped by the
// record count) a numeric field never mints.
func TestLabels_IntegerCodedFacetCardinality(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := intFacetPulse(t, Options{})
	limited := intFacetPulse(t, Options{Limits: Limits{MaxGroups: 2}})
	for _, f := range intLabelFields {
		req := intFacetReq("c.pulse", f.name, augmentOn(f.name, "codes"))
		req.DiscreteTopK = 1
		got, err := p.FacetSchema(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if d := got.Fields[f.name].Discrete; d == nil || len(d.Values) != 1 || d.TruncatedAt != 2 || intFacetWarningCodes(got.Warnings)[string(perrors.PULSE_FACET_VALUES_TRUNCATED)] != 1 {
			t.Errorf("%s: top-1 entry = %+v, warnings %v", f.name, d, got.Warnings)
		}

		for _, cohort := range []string{"c.pulse", "arch.pulse"} {
			_, err = limited.FacetSchema(ctx, intFacetReq(cohort, f.name, augmentOn(f.name, "codes")))
			if ce := intLabelCoded(err); ce == nil || ce.Code != perrors.PULSE_LIMIT_EXCEEDED {
				t.Errorf("%s %s: MaxGroups 2 over 3 codes: %v, want PULSE_LIMIT_EXCEEDED", f.name, cohort, err)
			}
			if _, err := limited.FacetSchema(ctx, intFacetReq(cohort, f.name, nil)); err != nil {
				t.Errorf("%s %s: unbound numeric facet refused: %v", f.name, cohort, err)
			}
		}

		for _, bound := range []bool{true, false} {
			var labels []*types.LabelBinding
			if bound {
				labels = augmentOn(f.name, "codes")
			}
			env, err := limited.PredictFacet(ctx, intFacetReq("c.pulse", f.name, labels))
			if err != nil {
				t.Fatal(err)
			}
			est := maxGroupsEstimate(t, env)
			if bound && est != int64(len(intFacetRows)) {
				t.Errorf("%s: predicted MaxGroups bound %d, want the record count %d", f.name, est, len(intFacetRows))
			}
			if !bound && est != -1 {
				t.Errorf("%s unbound: predicted MaxGroups bound %d, want none", f.name, est)
			}
		}
	}
}

// maxGroupsEstimate is the facet predict's max_groups finding estimate,
// -1 when there is none.
func maxGroupsEstimate(t *testing.T, env *descriptor.Envelope) int64 {
	t.Helper()
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		LimitFindings []descriptor.LimitFinding `json:"limit_findings"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, f := range data.LimitFindings {
		if f.Limit == "max_groups" {
			return f.Estimated
		}
	}
	return -1
}
