package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension operators over a NATIVE vector field (U29 E2-G1): a
// VectorInput extension aggregator receives a native vector's members
// as literal element names "emb[k]" and reads them through the public
// extend.Record.NumericValue / IsNull, which resolve an element member
// in place — a NaN element and a null vector being a null member, the
// shared fill's rule. Without that every element read as null and the
// operator silently answered over nothing. Each operator here must give
// the native cohort the same answer as its scalar twin (vparCohorts),
// on every arm, weighted or not.

// xnvWProfile is a WeightAware per-member weighted mean, buffered and
// streaming: it reads members through NumericValue and the row weight.
type xnvWProfile struct {
	fields []string
	sum    []float64
	w      []float64
}

func (a *xnvWProfile) UpdateRow(r extend.Record, _ string) error {
	w, ok := r.Weight()
	if !ok {
		w = 1
	}
	for i, f := range a.fields {
		if r.IsNull(f) {
			continue
		}
		if v, ok := r.NumericValue(f); ok {
			a.sum[i] += w * v
			a.w[i] += w
		}
	}
	return nil
}

func (a *xnvWProfile) Aggregate(rows extend.Rows, _ string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		_ = a.UpdateRow(rows.At(i), "")
	}
	return math.NaN(), nil
}
func (a *xnvWProfile) Finalize() (float64, error) { return math.NaN(), nil }
func (a *xnvWProfile) Rich() (any, error) {
	out := make([]float64, len(a.sum))
	for i := range out {
		out[i] = a.sum[i] / a.w[i]
		if a.w[i] == 0 {
			out[i] = math.NaN()
		}
	}
	return out, nil
}

// xnvMembers is a row-local attribute reading the members named in
// params.members: present-member count * 1000 + their sum. It proves
// the attribute arm (row-local and buffered Compute) resolves element
// names too.
type xnvMembers struct{ members []string }

func (a *xnvMembers) Row(r extend.Record, _ string) (float64, error) {
	n, s := 0.0, 0.0
	for _, m := range a.members {
		if v, ok := r.NumericValue(m); ok {
			if r.IsNull(m) {
				return 0, fmt.Errorf("%s: NumericValue present but IsNull true", m)
			}
			n++
			s += v
		} else if !r.IsNull(m) {
			return 0, fmt.Errorf("%s: NumericValue absent but IsNull false", m)
		}
	}
	return n*1000 + s, nil
}

func (a *xnvMembers) Compute(rows extend.Rows, f string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		v, err := a.Row(rows.At(i), f)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func xnvExtensions() pulse.Extensions {
	members := func(spec *types.Attribute) []string {
		var p struct {
			Members []string `json:"members"`
		}
		if len(spec.Params) > 0 {
			_ = json.Unmarshal(spec.Params, &p)
		}
		return p.Members
	}
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{
				Name: "AGG_XNV_PROFILE", Description: "per-member mean", VectorInput: true, ArrayValued: true,
				Streamable: true, Mergeable: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					return newExtProfile(spec.Fields), nil
				},
			},
			{
				Name: "AGG_XNV_ANSWERED", Description: "rows answering any member", VectorInput: true, Streamable: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					return &extAnswered{fields: spec.Fields}, nil
				},
			},
			{
				Name: "AGG_XNV_WPROFILE", Description: "per-member weighted mean", VectorInput: true, ArrayValued: true,
				Streamable: true, WeightAware: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					n := len(spec.Fields)
					return &xnvWProfile{fields: spec.Fields, sum: make([]float64, n), w: make([]float64, n)}, nil
				},
			},
		},
		Attributes: []pulse.AttributeRegistration{
			{
				Name: "ATTR_XNV_MEMBERS", Description: "reads params.members", Mode: pulse.AttributeModeRowLocal,
				Emits:  pulse.AttributeEmitFloat64,
				Params: []pulse.ParamMeta{{Name: "members", JSONType: "array"}},
				Factory: func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
					return &xnvMembers{members: members(spec)}, nil
				},
			},
			{
				Name: "ATTR_XNV_MEMBERS_BUF", Description: "reads params.members, buffered", Mode: pulse.AttributeModeBuffered,
				Emits:  pulse.AttributeEmitFloat64,
				Params: []pulse.ParamMeta{{Name: "members", JSONType: "array"}},
				Factory: func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
					return &xnvMembers{members: members(spec)}, nil
				},
			},
		},
	}
}

// TestExtensions_NativeVectorElementsReadLikeTwin: every extension
// operator reading element members answers the native vec_f64 /
// vec_f32 cohort exactly as the scalar twin, NaN elements and null
// vectors included, single file and shard archive, streaming,
// buffered and grouped.
func TestExtensions_NativeVectorElementsReadLikeTwin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	memberList := func(twin bool) string {
		names := make([]string, len(vparLabels))
		for k, l := range vparLabels {
			names[k] = fmt.Sprintf("emb[%d]", k+1)
			if twin {
				names[k] = "emb_" + l
			}
		}
		b, _ := json.Marshal(map[string]any{"members": names})
		return string(b)
	}
	req := func(twin bool, mode string) *types.Request {
		r := &types.Request{
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "id", Label: "n"},
				{Type: "AGG_XNV_PROFILE", Vector: "emb", Label: "prof", Params: vparRaw(`{"expand":true}`)},
				{Type: "AGG_XNV_ANSWERED", Vector: "emb2", Label: "answered"},
				{Type: "AGG_XNV_WPROFILE", Vector: "emb", Label: "wprof", Weight: types.SlotWeightField("s"), Params: vparRaw(`{"expand":true}`)},
				{Type: types.AGG_SUM, Field: "mem", Label: "sum_mem"},
			},
			Attributes: []*types.Attribute{
				{Type: "ATTR_XNV_MEMBERS", Field: "s", Label: "mem", Params: vparRaw(memberList(twin))},
			},
		}
		switch mode {
		case "buffered":
			r.Attributes = append(r.Attributes,
				&types.Attribute{Type: "ATTR_XNV_MEMBERS_BUF", Field: "s", Label: "memb", Params: vparRaw(memberList(twin))})
			r.Aggregations = append(r.Aggregations,
				&types.Aggregation{Type: types.AGG_SUM, Field: "memb", Label: "sum_memb"})
		case "grouped":
			r.Groups = append(r.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: "g"})
		}
		return vparReq(twin, r)
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeVecF64, encoding.FieldTypeVecF32} {
		for _, sharded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sharded=%v", ft, sharded), func(t *testing.T) {
				p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), ShardWorkers: 3, Extensions: xnvExtensions()})
				if err != nil {
					t.Fatal(err)
				}
				writeVparCohorts(t, p, ft, sharded)
				for _, mode := range []string{"streaming", "buffered", "grouped"} {
					t.Run(mode, func(t *testing.T) {
						run := func(twin bool) *types.Response {
							r := req(twin, mode)
							r.Cohort = &types.Cohort{Filename: "native.pulse"}
							if twin {
								r.Cohort.Filename = "twin.pulse"
							}
							resp, err := p.Process(ctx, r)
							if err != nil {
								t.Fatalf("twin=%v: %v", twin, err)
							}
							return resp
						}
						nat, tw := run(false), run(true)
						got, want := vparWire(t, nat, true), vparWire(t, tw, false)
						if got != want {
							t.Errorf("native response differs from the twin %s", vparDiff(got, want))
						}
						// Non-vacuous: the twin's members are not all null.
						if mode != "grouped" {
							row := tw.Data[0]
							if a, _ := row["answered"].(float64); a == 0 {
								t.Fatalf("twin answered = %v; the fixture reads nothing", row["answered"])
							}
							if s, _ := row["sum_mem"].(float64); s < 1000 {
								t.Fatalf("twin sum_mem = %v; the fixture reads nothing", row["sum_mem"])
							}
						}
					})
				}
			})
		}
	}
}
