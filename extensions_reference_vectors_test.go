package pulse_test

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Reference vectors (U27 E3-S1): Extensions.ReferenceVectors registers
// labelled target profiles; the reserved ref("table", key) built-in reads
// one, aligned BY LABEL to the vector it is compared with.

// refRegionTables is the fixture: "w" weights the vector {s: score,
// q: qty} with its labels in the OPPOSITE order to the vector's, one
// profile per parity-cohort region.
func refRegionTables() map[string]types.ReferenceVectorTable {
	return map[string]types.ReferenceVectorTable{
		"w": {
			Description: "per-region weights",
			Labels:      []string{"q", "s"},
			Profiles: map[string][]float64{
				"north": {1, 0}, "south": {2, 0}, "east": {3, 0}, "west": {4, 0},
			},
		},
		"partial": {
			Labels:   []string{"q", "s"},
			Profiles: map[string][]float64{"north": {1, 0}},
		},
		"other": {
			Labels:   []string{"q", "x"},
			Profiles: map[string][]float64{"k": {1, 1}},
		},
	}
}

func openReferenceCohort(t *testing.T, tables map[string]types.ReferenceVectorTable) (*pulse.Pulse, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "ref.pulse", paritySchema(t), 0, paritySmallRows)
	data, err := afero.ReadFile(fsys, "ref.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{ReferenceVectors: tables}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, data
}

// refRequest sums two formulas: got (the ref() expression) and want (the
// same figure spelled over the fields, the by-label oracle).
func refRequest(got, want string) *types.Request {
	return &types.Request{
		Cohort:  &types.Cohort{Filename: "ref.pulse"},
		Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "qty", Expression: got, Label: "got"},
			{Type: types.ATTR_FORMULA, Field: "qty", Expression: want, Label: "want"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "got", Label: "got"},
			{Type: types.AGG_SUM, Field: "want", Label: "want"},
		},
	}
}

// TestReferenceVectors_AlignByLabel: ref() inside dot / dist is reordered
// to the compared vector's labels — a literal key and a per-row key
// (the categorical region) alike — and matches the field-spelled oracle.
// A positional pairing would weight score instead of qty.
func TestReferenceVectors_AlignByLabel(t *testing.T) {
	p, data := openReferenceCohort(t, refRegionTables())
	regionWeight := `(region == "north" ? 1 : region == "south" ? 2 : region == "east" ? 3 : 4)`
	cases := []struct{ name, got, want string }{
		{"literal key", `dot(v, ref("w", "east"))`, `qty * 3`},
		{"literal key first", `dot(ref("w", "south"), v)`, `qty * 2`},
		{"row key", `dot(v, ref("w", region))`, `qty * ` + regionWeight},
		{"under a transform", `dot(vnormalize(ref("w", "west"), "l1"), v)`, `qty`},
		{"distance", `dist(v, ref("w", "north")) * dist(v, ref("w", "north"))`, `(score ?? 0) * (score ?? 0) + (qty - 1) * (qty - 1) + (score == nil ? (qty - 1) * (qty - 1) : 0)`},
		{"outside a pair keeps table order", `ref("w", "west")[0] + vsum(ref("w", "north"))`, `5 + 0 * qty`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := refRequest(tc.got, tc.want)
			env, err := p.PredictBytes(context.Background(), data, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused: %+v", env.Errors[0])
			}
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			got, want := resp.Data[0]["got"].(float64), resp.Data[0]["want"].(float64)
			if want == 0 || math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
				t.Errorf("sum(%s) = %v, want %v (sum(%s))", tc.got, got, want, tc.want)
			}
		})
	}
}

// TestReferenceVectors_RefusalParity: every reference refusal is
// reported by predict and refused by Process under the same code and
// reason — literal names before any row, a computed key on the row that
// misses.
func TestReferenceVectors_RefusalParity(t *testing.T) {
	p, data := openReferenceCohort(t, refRegionTables())
	cases := []struct {
		name, expr   string
		code         perr.Code
		reason       string
		predictKnows bool
	}{
		{"unknown table", `dot(v, ref("nope", "north"))`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, "table_unknown", true},
		{"table not literal", `dot(v, ref(region, "north"))`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, "table_not_literal", true},
		{"unknown literal key", `dot(v, ref("w", "nowhere"))`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, "key_unknown", true},
		{"labels differ", `cosine(v, ref("other", "k"))`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, "labels_differ", true},
		{"two references differ", `dot(ref("w", "north"), ref("other", "k"))`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, "labels_differ", true},
		{"literal partner", `dot([score, qty], ref("w", "north"))`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, "unlabeled_partner", true},
		{"row key missing", `dot(v, ref("partial", region))`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, "key_unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := refRequest(tc.expr, `qty`)
			env, err := p.PredictBytes(context.Background(), data, req)
			if err != nil {
				t.Fatal(err)
			}
			if tc.predictKnows {
				if len(env.Errors) != 1 || env.Errors[0].Code != string(tc.code) || env.Errors[0].Details["reason"] != tc.reason {
					t.Fatalf("predict errors %+v, want one %s reason %s", env.Errors, tc.code, tc.reason)
				}
				if env.Errors[0].Details["slot"] != "attributes[0].expression" {
					t.Errorf("predict slot %v, want attributes[0].expression", env.Errors[0].Details["slot"])
				}
			} else if len(env.Errors) != 0 {
				t.Fatalf("predict refused a computed key: %+v", env.Errors[0])
			}
			_, err = p.Process(context.Background(), req)
			assertCodedError(t, err, tc.code)
			var ce *perr.CodedError
			if stderrors.As(err, &ce) && ce.Details["reason"] != tc.reason {
				t.Errorf("Process reason %v, want %s", ce.Details["reason"], tc.reason)
			}
		})
	}
}

// TestReferenceVectors_Validation: each malformed table is refused at
// pulse.New with PULSE_EXTENSION_PARAM_INVALID and its reason.
func TestReferenceVectors_Validation(t *testing.T) {
	ok := func() types.ReferenceVectorTable {
		return types.ReferenceVectorTable{Labels: []string{"a", "b"}, Profiles: map[string][]float64{"k": {1, 2}}}
	}
	cases := []struct {
		name   string
		table  string
		mut    func(*types.ReferenceVectorTable)
		reason string
	}{
		{"empty name", " ", func(*types.ReferenceVectorTable) {}, "empty_name"},
		{"no labels", "t", func(r *types.ReferenceVectorTable) { r.Labels = nil }, "no_labels"},
		{"empty label", "t", func(r *types.ReferenceVectorTable) { r.Labels = []string{"a", ""} }, "empty_label"},
		{"duplicate label", "t", func(r *types.ReferenceVectorTable) { r.Labels = []string{"a", "a"} }, "duplicate_label"},
		{"no profiles", "t", func(r *types.ReferenceVectorTable) { r.Profiles = nil }, "no_profiles"},
		{"empty key", "t", func(r *types.ReferenceVectorTable) { r.Profiles = map[string][]float64{"": {1, 2}} }, "empty_key"},
		{"short profile", "t", func(r *types.ReferenceVectorTable) { r.Profiles["k"] = []float64{1} }, "length_mismatch"},
		{"NaN", "t", func(r *types.ReferenceVectorTable) { r.Profiles["k"] = []float64{1, math.NaN()} }, "non_finite"},
		{"Inf", "t", func(r *types.ReferenceVectorTable) { r.Profiles["k"] = []float64{math.Inf(-1), 1} }, "non_finite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tbl := ok()
			tc.mut(&tbl)
			_, err := pulse.New(newTestOptions(pulse.Extensions{ReferenceVectors: map[string]types.ReferenceVectorTable{tc.table: tbl}}))
			assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
			var ce *perr.CodedError
			_ = stderrors.As(err, &ce)
			if ce.Details["reason"] != tc.reason || ce.Details["category"] != "reference_vectors" {
				t.Errorf("details %v, want reason %s category reference_vectors", ce.Details, tc.reason)
			}
		})
	}
	if _, err := pulse.New(newTestOptions(pulse.Extensions{ReferenceVectors: map[string]types.ReferenceVectorTable{"t": ok()}})); err != nil {
		t.Errorf("a well-formed table: %v", err)
	}
}

// TestReferenceVectors_ReservedNames: ref is a reserved expression name
// and the two reference-comparison attribute names are reserved against
// extensions.
func TestReferenceVectors_ReservedNames(t *testing.T) {
	_, err := pulse.New(newTestOptions(pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{{Name: "ref", Fn: func(x float64) float64 { return x }}},
	}))
	assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_RESERVED)
	for _, name := range []types.AttributeType{"ATTR_PROFILE_SIMILARITY", "ATTR_VEC_DISTANCE"} {
		_, err := pulse.New(newTestOptions(pulse.Extensions{
			Attributes: []pulse.AttributeRegistration{{Name: name, Mode: pulse.AttributeModeRowLocal, Factory: stubNilAttributeFactory}},
		}))
		assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_COLLISION)
	}
}

// TestReferenceVectors_ManifestAndCopy: the manifest lists each table's
// labels and sorted keys (never the values); the instance keeps its own
// copy, so mutating the registered maps after pulse.New changes nothing.
func TestReferenceVectors_ManifestAndCopy(t *testing.T) {
	tables := refRegionTables()
	p, _ := openReferenceCohort(t, tables)
	m := p.Manifest(context.Background())
	got := m.Extensions.ReferenceVectors
	if len(got) != 3 || got[0].Name != "other" || got[1].Name != "partial" || got[2].Name != "w" {
		t.Fatalf("reference_vectors %+v, want other / partial / w", got)
	}
	if !reflect.DeepEqual(got[2].Labels, []string{"q", "s"}) || !reflect.DeepEqual(got[2].Keys, []string{"east", "north", "south", "west"}) || got[2].Description != "per-region weights" {
		t.Errorf("w meta %+v", got[2])
	}
	tables["w"].Profiles["east"][0] = 100
	resp, err := p.Process(context.Background(), refRequest(`dot(v, ref("w", region))`, `qty * (region == "north" ? 1 : region == "south" ? 2 : region == "east" ? 3 : 4)`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data[0]["got"] != resp.Data[0]["want"] {
		t.Errorf("a post-New mutation reached the run: got %v want %v", resp.Data[0]["got"], resp.Data[0]["want"])
	}
	empty, err := pulse.New(newTestOptions(pulse.Extensions{}))
	if err != nil {
		t.Fatal(err)
	}
	if rv := empty.Manifest(context.Background()).Extensions.ReferenceVectors; rv == nil || len(rv) != 0 {
		t.Errorf("no tables: reference_vectors %v, want an empty non-nil list", rv)
	}
}
