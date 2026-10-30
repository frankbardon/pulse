package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension vector aggregators (AggregatorRegistration.VectorInput /
// ArrayValued): the slot's vector / fields resolve by the built-in
// rules, the factory sees the literal members, an ArrayValued value is
// refused wherever one number is needed and honours expand — in
// predict and at runtime alike, on every arm.

// extProfile is an ArrayValued per-member mean: Streamable + Mergeable.
type extProfile struct {
	fields []string
	sum    []float64
	n      []float64
}

func newExtProfile(fields []string) *extProfile {
	return &extProfile{fields: fields, sum: make([]float64, len(fields)), n: make([]float64, len(fields))}
}

func (a *extProfile) fold(r extend.Record) {
	for i, f := range a.fields {
		if v, ok := r.NumericValue(f); ok {
			a.sum[i] += v
			a.n[i]++
		}
	}
}

func (a *extProfile) Aggregate(rows extend.Rows, _ string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		a.fold(rows.At(i))
	}
	return math.NaN(), nil
}
func (a *extProfile) UpdateRow(r extend.Record, _ string) error { a.fold(r); return nil }
func (a *extProfile) Finalize() (float64, error)                { return math.NaN(), nil }
func (a *extProfile) Merge(o extend.OnlineAggregator) error {
	b := o.(*extProfile)
	for i := range a.sum {
		a.sum[i] += b.sum[i]
		a.n[i] += b.n[i]
	}
	return nil
}
func (a *extProfile) Rich() (any, error) {
	out := make([]float64, len(a.sum))
	for i := range out {
		out[i] = a.sum[i] / a.n[i]
		if a.n[i] == 0 {
			out[i] = math.NaN()
		}
	}
	return out, nil
}

// extAnswered is a scalar VectorInput aggregator: rows where any member
// answered.
type extAnswered struct {
	fields []string
	n      float64
}

func (a *extAnswered) Aggregate(rows extend.Rows, _ string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		_ = a.UpdateRow(rows.At(i), "")
	}
	return a.n, nil
}
func (a *extAnswered) UpdateRow(r extend.Record, _ string) error {
	for _, f := range a.fields {
		if _, ok := r.NumericValue(f); ok {
			a.n++
			return nil
		}
	}
	return nil
}
func (a *extAnswered) Finalize() (float64, error) { return a.n, nil }

// extBadShape is ArrayValued but returns one figure too few.
type extBadShape struct{ extProfile }

func (a *extBadShape) Rich() (any, error) { return []float64{1}, nil }

// extVecSeen records the specs the vector factories were handed.
type extVecSeen struct {
	mu     sync.Mutex
	fields [][]string
	params []string
}

func (s *extVecSeen) note(spec *types.Aggregation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fields = append(s.fields, spec.Fields)
	s.params = append(s.params, string(spec.Params))
}

func extVecRegistrations(seen *extVecSeen) []pulse.AggregatorRegistration {
	return []pulse.AggregatorRegistration{
		{
			Name: "AGG_ACME_PROFILE", Description: "per-member mean", VectorInput: true, ArrayValued: true,
			Streamable: true, Mergeable: true,
			Params: []pulse.ParamMeta{{Name: "k", JSONType: "number"}},
			Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
				seen.note(spec)
				return newExtProfile(spec.Fields), nil
			},
		},
		{
			Name: "AGG_ACME_ANSWERED", Description: "rows answering any member", VectorInput: true, Streamable: true,
			Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
				return &extAnswered{fields: spec.Fields}, nil
			},
		},
		{
			Name: "AGG_ACME_BADSHAPE", Description: "wrong array length", VectorInput: true, ArrayValued: true,
			Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
				return &extBadShape{*newExtProfile(spec.Fields)}, nil
			},
		},
		{
			Name: "AGG_ACME_PLAIN", Description: "reads one field",
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return &extAnswered{}, nil },
		},
	}
}

// extVecFS writes the vexpr cohort as a single file and as an archive
// of uneven shards.
func extVecFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	const rows = 2*4096 + 300
	writeVaggBitsCohort(t, fsys, "single.pulse", 0, rows)
	bounds := []int{0, 1, 3000, 3000, 5000, rows}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("x%d.pulse", i)
		writeVaggBitsCohort(t, fsys, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	b, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	return fsys
}

func closeFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.IsNaN(a[i]) != math.IsNaN(b[i]) || math.Abs(a[i]-b[i]) > 1e-9*(1+math.Abs(b[i])) {
			return false
		}
	}
	return true
}

// TestExtensions_VectorAggregatorParity: an ArrayValued extension's
// per-member figures match AGG_VEC_MEAN's on every arm (serial,
// DecodeWorkers, ShardWorkers; grouped and ungrouped); expand writes
// the same columns predict lists; a scalar VectorInput extension's
// floor n equals the built-in's; the factory sees the literal members
// with its own params kept.
func TestExtensions_VectorAggregatorParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := extVecFS(t)
	data, err := afero.ReadFile(fsys, "single.pulse")
	if err != nil {
		t.Fatal(err)
	}
	req := func(cohort string, grouped bool) *types.Request {
		r := &types.Request{
			Cohort:  &types.Cohort{Filename: cohort},
			Vectors: []types.VectorSpec{{Name: "bat", Fields: []string{"b*"}, Labels: []string{"w", "x", "y", "z"}}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_VEC_MEAN, Vector: "bat", Label: "builtin"},
				{Type: "AGG_ACME_PROFILE", Vector: "bat", Label: "ext", Params: json.RawMessage(`{"k":2}`)},
				{Type: "AGG_ACME_PROFILE", Fields: []string{"b1", "b2"}, Label: "wide", Params: json.RawMessage(`{"expand":true,"labels":["one","two"]}`)},
				{Type: "AGG_ACME_ANSWERED", Fields: []string{"b*"}, Label: "answered"},
			},
		}
		if grouped {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}}
			r.Sort = []types.OrderKey{{Field: "qty"}}
		}
		return r
	}
	for _, grouped := range []bool{false, true} {
		for _, arm := range []struct {
			name   string
			cohort string
			opts   pulse.Options
		}{
			{"serial", "single.pulse", pulse.Options{}},
			{"decode_workers", "single.pulse", pulse.Options{DecodeWorkers: 3}},
			{"archive_serial", "archive.pulse", pulse.Options{ShardWorkers: 1}},
			{"shard_workers", "archive.pulse", pulse.Options{ShardWorkers: 3}},
		} {
			t.Run(fmt.Sprintf("%s/grouped=%v", arm.name, grouped), func(t *testing.T) {
				seen := &extVecSeen{}
				opts := arm.opts
				opts.FS = fsys
				opts.Extensions = pulse.Extensions{Aggregators: extVecRegistrations(seen)}
				p, err := pulse.New(opts)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := p.Process(ctx, req(arm.cohort, grouped))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if len(resp.Data) == 0 {
					t.Fatal("no rows")
				}
				for _, row := range resp.Data {
					want, ok := vaggArray(row["builtin"])
					got, ok2 := vaggArray(row["ext"])
					if !ok || !ok2 || !closeFloats(got, want) {
						t.Fatalf("ext = %v, AGG_VEC_MEAN = %v", row["ext"], row["builtin"])
					}
					if _, has := row["wide"]; has {
						t.Fatal("an expanded slot kept its label")
					}
					for i, col := range []string{"wide[one]", "wide[two]"} {
						v, ok := row[col].(float64)
						if !ok || !closeFloats([]float64{v}, want[i:i+1]) {
							t.Fatalf("%s = %v, want %v", col, row[col], want[i])
						}
					}
				}
				if !grouped {
					bc, ec := vaggComponents(resp, "builtin"), vaggComponents(resp, "answered")
					if bc == nil || ec == nil || bc.N != ec.N || float64(ec.N) != resp.Data[0]["answered"] {
						t.Fatalf("floor: builtin %+v answered %+v value %v", bc, ec, resp.Data[0]["answered"])
					}
				}
				seen.mu.Lock()
				defer seen.mu.Unlock()
				if len(seen.fields) == 0 {
					t.Fatal("factory never called")
				}
				for i, f := range seen.fields {
					switch len(f) {
					case 4:
						var p map[string]any
						if err := json.Unmarshal([]byte(seen.params[i]), &p); err != nil || p["k"] != float64(2) ||
							!reflect.DeepEqual(p["labels"], []any{"w", "x", "y", "z"}) || !reflect.DeepEqual(f, []string{"b1", "b2", "b3", "b4"}) {
							t.Fatalf("factory spec fields %v params %s", f, seen.params[i])
						}
					case 0, 2: // 0: the probe-validation spec at pulse.New
					default:
						t.Fatalf("factory fields %v", f)
					}
				}
			})
		}
	}

	// Predict: no refusal, and the expanded columns are listed.
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{Aggregators: extVecRegistrations(&extVecSeen{})}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.PredictBytes(ctx, data, req("single.pulse", false))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	pr := env.Data.(*descriptor.PredictResult)
	if got := pr.Aggregations[2].Columns; !reflect.DeepEqual(got, []string{"wide[one]", "wide[two]"}) {
		t.Fatalf("predict columns = %v", got)
	}

	// The manifest projects the declarations.
	m := p.Manifest(ctx)
	var found int
	for _, o := range m.Extensions.Aggregators {
		switch o.Name {
		case "AGG_ACME_PROFILE":
			found++
			if !o.VectorInput || !o.ArrayValued {
				t.Fatalf("manifest %s = %+v", o.Name, o)
			}
		case "AGG_ACME_ANSWERED":
			found++
			if !o.VectorInput || o.ArrayValued {
				t.Fatalf("manifest %s = %+v", o.Name, o)
			}
		case "AGG_ACME_PLAIN":
			found++
			if o.VectorInput || o.ArrayValued {
				t.Fatalf("manifest %s = %+v", o.Name, o)
			}
		}
	}
	if found != 3 {
		t.Fatalf("manifest extension aggregators: %d of 3", found)
	}
}

// TestExtensions_VectorAggregatorRefusalParity: Process and predict
// refuse the same requests with the same code and reason — the
// array-valued hosts, expand on a scalar extension, a vector on a plain
// extension — and a wrong Rich shape is PROCESSING_INTERNAL at run.
func TestExtensions_VectorAggregatorRefusalParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{Aggregators: extVecRegistrations(&extVecSeen{})}})
	if err != nil {
		t.Fatal(err)
	}
	vec := func() *types.Aggregation {
		return &types.Aggregation{Type: "AGG_ACME_PROFILE", Fields: []string{"b*"}, Label: "m"}
	}
	qty := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}} }
	base := func(mut func(r *types.Request)) func() *types.Request {
		return func() *types.Request {
			r := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"}}
			mut(r)
			return r
		}
	}
	cases := []struct {
		name   string
		req    func() *types.Request
		code   perr.Code
		reason string
	}{
		{"crosstab cell", base(func(r *types.Request) {
			r.Crosstab = &types.CrosstabSpec{Rows: qty(), Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}}, Cell: vec()}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"crosstab margin", base(func(r *types.Request) {
			r.Crosstab = &types.CrosstabSpec{Rows: qty(), Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}},
				Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "qty"},
				MarginAggregations: []*types.Aggregation{vec()}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"sort", base(func(r *types.Request) {
			r.Groups, r.Aggregations, r.Sort = qty(), []*types.Aggregation{vec()}, []types.OrderKey{{Field: "m"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"overlay host", base(func(r *types.Request) {
			r.Groups, r.Aggregations = qty(), []*types.Aggregation{vec()}
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"field on a vector extension", base(func(r *types.Request) {
			a := vec()
			a.Field = "b1"
			r.Aggregations = []*types.Aggregation{a}
		}), perr.SERVICE_VALIDATION, "field_on_vector_aggregation"},
		{"expand on a scalar extension", base(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: "AGG_ACME_ANSWERED", Fields: []string{"b*"}, Params: json.RawMessage(`{"expand":true}`)}}
		}), perr.SERVICE_VALIDATION, "expand_not_array_valued"},
		{"vector on a plain extension", base(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: "AGG_ACME_PLAIN", Field: "b1", Fields: []string{"b2"}}}
		}), perr.SERVICE_VALIDATION, "vector_not_accepted"},
		{"unknown vector", base(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: "AGG_ACME_PROFILE", Vector: "nope"}}
		}), perr.PULSE_VECTOR_UNKNOWN, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, perrRun := p.Process(ctx, c.req())
			ce := codeOf(t, perrRun)
			if ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Fatalf("Process = %s %v, want %s %s", ce.Code, ce.Details, c.code, c.reason)
			}
			env, err := p.PredictBytes(ctx, data, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) ||
				(c.reason != "" && env.Errors[0].Details["reason"] != c.reason) {
				t.Fatalf("predict = %+v, want %s %s", env.Errors, c.code, c.reason)
			}
		})
	}

	chain := &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*types.ChainStage{
		{Name: "profile", Request: &types.Request{Groups: qty(), Aggregations: []*types.Aggregation{vec()}}},
		{Name: "count", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty"}}}},
	}}
	_, err = p.ProcessChain(ctx, chain)
	if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != "array_valued" {
		t.Fatalf("ProcessChain = %s %v", ce.Code, ce.Details)
	}
	cenv, err := p.PredictChain(ctx, chain)
	if err != nil || len(cenv.Errors) == 0 || cenv.Errors[0].Details["reason"] != "array_valued" {
		t.Fatalf("PredictChain = %v %+v", err, cenv.Errors)
	}

	_, err = p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"},
		Aggregations: []*types.Aggregation{{Type: "AGG_ACME_BADSHAPE", Fields: []string{"b*"}}}})
	if ce := codeOf(t, err); ce.Code != perr.PROCESSING_INTERNAL {
		t.Fatalf("wrong Rich shape = %s %v", ce.Code, ce.Details)
	}
}

// TestExtensions_VectorAggregatorRegistrationRefusals: pulse.New
// refuses an ArrayValued registration without VectorInput, with a
// margin class or without RichAggregator, and a VectorInput
// registration declaring params.labels / params.expand.
func TestExtensions_VectorAggregatorRegistrationRefusals(t *testing.T) {
	t.Parallel()

	scalar := func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return &extAnswered{}, nil }
	rich := func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return newExtProfile(nil), nil }
	cases := []struct {
		name   string
		reg    pulse.AggregatorRegistration
		code   perr.Code
		reason string
	}{
		{"array without vector", pulse.AggregatorRegistration{Name: "AGG_ACME_R1", ArrayValued: true, Factory: rich},
			perr.PULSE_EXTENSION_ARRAY_VALUED_MISMATCH, "array_valued_without_vector_input"},
		{"array with margin", pulse.AggregatorRegistration{Name: "AGG_ACME_R2", VectorInput: true, ArrayValued: true, MarginReducibility: types.MarginRecompute, Factory: rich},
			perr.PULSE_EXTENSION_ARRAY_VALUED_MISMATCH, "array_valued_margin_reducibility"},
		{"array without rich", pulse.AggregatorRegistration{Name: "AGG_ACME_R3", VectorInput: true, ArrayValued: true, Factory: scalar},
			perr.PULSE_EXTENSION_ARRAY_VALUED_MISMATCH, "missing_rich_interface"},
		{"reserved labels", pulse.AggregatorRegistration{Name: "AGG_ACME_R4", VectorInput: true, Factory: scalar,
			Params: []pulse.ParamMeta{{Name: "labels", JSONType: "array"}}},
			perr.PULSE_EXTENSION_PARAM_INVALID, "reserved_vector_param"},
		{"reserved expand", pulse.AggregatorRegistration{Name: "AGG_ACME_R5", VectorInput: true, Factory: scalar,
			Params: []pulse.ParamMeta{{Name: "expand", JSONType: "boolean"}}},
			perr.PULSE_EXTENSION_PARAM_INVALID, "reserved_vector_param"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{c.reg}}})
			ce := codeOf(t, err)
			if ce.Code != c.code || ce.Details["reason"] != c.reason {
				t.Fatalf("New = %s %v, want %s %s", ce.Code, ce.Details, c.code, c.reason)
			}
		})
	}
	// A plain field-reading param named labels stays legal off a vector input.
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		{Name: "AGG_ACME_OK", Factory: scalar, Params: []pulse.ParamMeta{{Name: "labels", JSONType: "array"}}},
	}}}); err != nil {
		t.Fatalf("labels param on a field aggregator refused: %v", err)
	}
}
