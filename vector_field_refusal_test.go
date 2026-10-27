package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vnatPulse is a fresh instance over a memory fs holding the vnat
// native cohort (and its twin) as single files.
func vnatPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	writeVnatCohorts(t, p, encoding.FieldTypeVecF64, false)
	return p, fsys
}

func vnatRequest(t *testing.T, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request %s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: "native.pulse"}
	return &req
}

// TestVectorField_ScalarRefusalsPredictMatchRuntime: every kind of
// scalar slot naming the native vector field is refused by the runtime
// and predict with the SAME code, message and details — the bare slots
// with their smart-default suggestion (AGG_VEC_MEAN / GROUP_KMEANS), a
// weight, a date / zone slot, a window ORDER BY / PARTITION BY (the
// orderability rule shared with the runtime window sort), a sort key, a
// crosstab axis, an aggregation param. Predict reports nothing else.
func TestVectorField_ScalarRefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()

	p, fsys := vnatPulse(t)
	ctx := context.Background()
	cases := []struct {
		name, body, slot, suggest string
	}{
		{"bare aggregation", `{"aggregations":[{"field":"emb"}]}`, "aggregations[0] field", "AGG_VEC_MEAN"},
		{"bare grouper", `{"groups":[{"field":"emb"}],"aggregations":[{"type":"AGG_COUNT","field":"s"}]}`, "groups[0] field", "GROUP_KMEANS"},
		{"scalar aggregation", `{"aggregations":[{"type":"AGG_AVERAGE","field":"emb"}]}`, "aggregations[0] (AGG_AVERAGE) field", "AGG_VEC_MEAN"},
		{"aggregation param", `{"aggregations":[{"type":"AGG_RATIO","field":"s","params":{"numerator_field":"emb","denominator_field":"s"}}]}`, "aggregations[0] (AGG_RATIO) params.numerator_field", ""},
		{"request weight", `{"weight":{"field":"emb"},"aggregations":[{"type":"AGG_AVERAGE","field":"s"}]}`, "weight", ""},
		{"slot weight", `{"aggregations":[{"type":"AGG_AVERAGE","field":"s","weight":{"field":"emb"}}]}`, "aggregations[0].weight", ""},
		{"date grouper with zone", `{"groups":[{"type":"GROUP_DATE","field":"emb","tz":"America/New_York"}],"aggregations":[{"type":"AGG_COUNT","field":"s"}]}`, "groups[0] (GROUP_DATE) field", "GROUP_KMEANS"},
		{"window order_by", `{"windows":[{"type":"WIN_ROW_NUMBER","order_by":[{"field":"emb"}]}]}`, "windows[0] (WIN_ROW_NUMBER) order_by[0]", ""},
		{"window partition_by", `{"windows":[{"type":"WIN_ROW_NUMBER","order_by":[{"field":"s"}],"partition_by":["emb"]}]}`, "windows[0] (WIN_ROW_NUMBER) partition_by[0]", ""},
		{"sort key", `{"sort":[{"field":"emb"}]}`, "sort[0]", ""},
		{"crosstab axis", `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"emb"}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],"cell":{"type":"AGG_COUNT","field":"s"}}}`, "crosstab.rows[0] (GROUP_CATEGORY) field", "GROUP_KMEANS"},
		{"filter", `{"filterers":[{"type":"FILTER_TRUE","field":"emb"}],"aggregations":[{"type":"AGG_COUNT","field":"s"}]}`, "filterers[0] (FILTER_TRUE) field", ""},
		{"test split_by", `{"tests":[{"type":"TEST_WELCH","field":"s","split_by":"emb"}]}`, "tests[0] (TEST_WELCH) split_by", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, vnatRequest(t, c.body))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_FIELD_NOT_SCALAR {
				t.Fatalf("Process: %v, want %s", err, perr.PULSE_VECTOR_FIELD_NOT_SCALAR)
			}
			if ce.Details["slot"] != c.slot || ce.Details["field"] != "emb" || ce.Details["field_type"] != "vec_f64" || ce.Details["dim"] != 3 {
				t.Errorf("details = %v, want slot %q on emb vec_f64 dim 3", ce.Details, c.slot)
			}
			if got, _ := ce.Details["suggest"].(string); got != c.suggest {
				t.Errorf("suggest = %q, want %q", got, c.suggest)
			}
			env := predictEnvelope(t, p, fsys, "native.pulse", vnatRequest(t, c.body))
			if len(env.Errors) != 1 {
				for _, e := range env.Errors {
					t.Logf("predict: %s %s %v", e.Code, e.Message, e.Details)
				}
				t.Fatalf("predict errors = %d, want exactly the vector refusal", len(env.Errors))
			}
			e := env.Errors[0]
			want, _ := json.Marshal(ce.Details)
			got, _ := json.Marshal(e.Details)
			if e.Code != string(ce.Code) || e.Message != ce.Message || string(got) != string(want) {
				t.Errorf("predict %s %q %s\nruntime %s %q %s", e.Code, e.Message, got, ce.Code, ce.Message, want)
			}
		})
	}
}

// TestVectorField_PresenceSlotsCountVectors: the presence-only slots
// accept a vector field and count it right — a null vector is null, a
// vector with NaN elements is present — on every arm (streaming,
// buffered, grouped; single file and shard archive), and AGG_RATIO's
// ignored field may name one.
func TestVectorField_PresenceSlotsCountVectors(t *testing.T) {
	t.Parallel()

	nulls := 0
	for r := 0; r < vnatRows; r++ {
		if vnatNull(r) {
			nulls++
		}
	}
	for _, sharded := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), ShardWorkers: 3})
		if err != nil {
			t.Fatal(err)
		}
		writeVnatCohorts(t, p, encoding.FieldTypeVecF32, sharded)
		for _, mode := range []string{"streaming", "buffered", "grouped"} {
			t.Run(fmt.Sprintf("sharded=%v/%s", sharded, mode), func(t *testing.T) {
				run := func(body string) map[string]float64 {
					req := vnatRequest(t, body)
					switch mode {
					case "buffered":
						req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "s", Label: "s_pct"})
					case "grouped":
						req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
					}
					resp, err := p.Process(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					sum := map[string]float64{}
					for _, row := range resp.Data {
						for _, k := range []string{"n", "nn"} {
							if v, ok := row[k]; ok {
								var f float64
								fmt.Sscan(fmt.Sprint(v), &f)
								sum[k] += f
							}
						}
					}
					return sum
				}
				got := run(`{"aggregations":[
					{"type":"AGG_COUNT","field":"emb","label":"n"},
					{"type":"AGG_NULL_COUNT","field":"emb","label":"nn"},
					{"type":"AGG_RATIO","field":"emb","label":"r","params":{"numerator_field":"s","denominator_field":"s"}}]}`)
				if got["n"] != float64(vnatRows-nulls) || got["nn"] != float64(nulls) {
					t.Errorf("AGG_COUNT %v / AGG_NULL_COUNT %v, want %d / %d", got["n"], got["nn"], vnatRows-nulls, nulls)
				}
				for _, c := range []struct {
					mode string
					want int
				}{{"is_null", nulls}, {"is_not_null", vnatRows - nulls}} {
					got := run(`{"filterers":[{"type":"FILTER_NULL","field":"emb","values":["` + c.mode + `"]}],"aggregations":[{"type":"AGG_COUNT","field":"id","label":"n"}]}`)
					if got["n"] != float64(c.want) {
						t.Errorf("FILTER_NULL %s kept %v rows, want %d", c.mode, got["n"], c.want)
					}
				}
			})
		}
	}
}

// TestVectorField_JoinKeyRefused: a vector join key is
// PULSE_JOIN_TYPE_MISMATCH, details.reason "vector_key", from the
// runtime and predict alike.
func TestVectorField_JoinKeyRefused(t *testing.T) {
	t.Parallel()

	p, fsys := vnatPulse(t)
	body := `{"joins":[{"right":"native.pulse","on":[{"left_field":"emb","right_field":"emb"}]}],"aggregations":[{"type":"AGG_COUNT","field":"s"}]}`
	_, err := p.Process(context.Background(), vnatRequest(t, body))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_JOIN_TYPE_MISMATCH || ce.Details["reason"] != "vector_key" {
		t.Fatalf("Process: %v, want PULSE_JOIN_TYPE_MISMATCH reason vector_key", err)
	}
	env := predictEnvelope(t, p, fsys, "native.pulse", vnatRequest(t, body))
	if !slices.ContainsFunc(env.Errors, func(e *descriptor.EnvelopeEntry) bool {
		return e.Code == string(perr.PULSE_JOIN_TYPE_MISMATCH) && e.Details["reason"] == "vector_key"
	}) {
		t.Errorf("predict errors = %+v, want PULSE_JOIN_TYPE_MISMATCH reason vector_key", env.Errors)
	}
}

// TestVectorField_IndexKeyRefused: a point-lookup index cannot key on
// a vector field.
func TestVectorField_IndexKeyRefused(t *testing.T) {
	t.Parallel()

	p, _ := vnatPulse(t)
	_, err := p.BuildIndex(context.Background(), "native.pulse", []string{"emb"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_FIELD_NOT_SCALAR || ce.Details["slot"] != "index key" || ce.Details["reason"] == nil {
		t.Fatalf("BuildIndex: %v, want %s at slot \"index key\" with a reason", err, perr.PULSE_VECTOR_FIELD_NOT_SCALAR)
	}
}

// TestNativeVector_PredictMatrixEchoMatchesRuntime: predict's matrix
// echo (axis keys, labels) and resolved_vectors equal what the runtime
// returns for a native vector named directly, listed inline, re-kinded
// by a spec and re-scoped to single elements.
func TestNativeVector_PredictMatrixEchoMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fsys := vnatPulse(t)
	cases := []string{
		`{"matrices":[{"type":"MAT_CORRELATION","name":"m","vector":"emb"}]}`,
		`{"matrices":[{"type":"MAT_CORRELATION","name":"m","fields":["emb","s"]}]}`,
		`{"vectors":[{"name":"rk","fields":["emb"],"kind":"measure"}],"matrices":[{"type":"MAT_CORRELATION","name":"m","vector":"rk"}]}`,
		`{"vectors":[{"name":"mix","fields":["emb[2]","s","emb[1]"]}],"matrices":[{"type":"MAT_COVARIANCE","name":"m","vector":"mix"}]}`,
		`{"vectors":[{"name":"lab","fields":["emb"],"labels":["x","y","z"]}],"matrices":[{"type":"MAT_CORRELATION","name":"m","vector":"lab"}]}`,
	}
	for _, body := range cases {
		resp, err := p.Process(context.Background(), vnatRequest(t, body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		got := resp.Matrices[0].Primary
		env := predictEnvelope(t, p, fsys, "native.pulse", vnatRequest(t, body))
		if len(env.Errors) != 0 {
			t.Fatalf("%s: predict errors %+v", body, env.Errors)
		}
		pr := env.Data.(*descriptor.PredictResult)
		mp := pr.Matrices[0]
		if !slices.Equal(mp.AxisKeys, got.RowKeys) || !slices.Equal(mp.Labels, got.Labels) {
			t.Errorf("%s:\npredict axis %v labels %v\nruntime rows %v labels %v", body, mp.AxisKeys, mp.Labels, got.RowKeys, got.Labels)
		}
		req := vnatRequest(t, body)
		if len(req.Vectors) > 0 {
			if members := pr.ResolvedVectors[req.Vectors[0].Name]; !slices.Equal(members, got.RowKeys) {
				t.Errorf("%s: resolved_vectors %v, runtime members %v", body, members, got.RowKeys)
			}
		} else if pr.ResolvedVectors != nil {
			t.Errorf("%s: resolved_vectors echoes schema metadata: %v", body, pr.ResolvedVectors)
		}
	}
}

// TestVectorField_InspectSurfaces (FR-17): Pulse.InspectEnvelope (the
// CLI's path) and pulse_inspect report a vector field's dim, kind,
// scale range and labels, single file and shard archive alike.
func TestVectorField_InspectSurfaces(t *testing.T) {
	t.Parallel()

	for _, sharded := range []bool{false, true} {
		t.Run(fmt.Sprintf("sharded=%v", sharded), func(t *testing.T) {
			t.Parallel()

			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
			if err != nil {
				t.Fatal(err)
			}
			writeVnatCohorts(t, p, encoding.FieldTypeVecF32, sharded)
			check := func(surface string, res *descriptor.InspectResult) {
				var emb *descriptor.InspectField
				for _, f := range res.Fields {
					if f.Name == "emb" {
						emb = f
					}
				}
				v := emb.Vector
				if v == nil || v.Dim != 3 || v.Kind != "scale" || v.ScaleMin == nil || *v.ScaleMin != 0 || v.ScaleMax == nil || *v.ScaleMax != 10 ||
					v.Labels == nil || v.Labels.Truncated || !slices.Equal(v.Labels.Values, vnatLabels) {
					t.Errorf("%s: emb vector = %+v", surface, v)
				}
				if emb.Type != "vec_f32" || emb.Description != "Vector field: emb (3 elements)" {
					t.Errorf("%s: emb type %s description %q", surface, emb.Type, emb.Description)
				}
			}
			env, err := p.InspectEnvelope(context.Background(), "native.pulse", nil)
			if err != nil {
				t.Fatal(err)
			}
			check("InspectEnvelope", env.Data.(*descriptor.InspectResult))
			out, err := mcp.HandleInspect(context.Background(), p, mcp.InspectIn{Path: "native.pulse"})
			if err != nil {
				t.Fatal(err)
			}
			check("pulse_inspect", &out.InspectResult)
		})
	}
}

// TestVectorField_CrosstabCountCell: a crosstab AGG_COUNT cell over a
// vector field counts the non-null vectors (the cell-admission gate's
// presence question).
func TestVectorField_CrosstabCountCell(t *testing.T) {
	t.Parallel()

	p, _ := vnatPulse(t)
	resp, err := p.Process(context.Background(), vnatRequest(t, `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],"cell":{"type":"AGG_COUNT","field":"emb"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	total, nonNull := 0.0, 0
	for r := 0; r < vnatRows; r++ {
		if !vnatNull(r) {
			nonNull++
		}
	}
	for _, row := range resp.Crosstab.Matrix.Cells {
		for _, c := range row {
			if c.Present {
				total += c.Scalar()
			}
		}
	}
	if total != float64(nonNull) {
		t.Errorf("crosstab AGG_COUNT cells sum to %v, want %d", total, nonNull)
	}
}
