package pulse_test

import (
	"errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// stubMatrixOp is a finalize-only matrix operator: the scatter (raw
// co-moment) matrix.
type stubMatrixOp struct{}

func (stubMatrixOp) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	return extend.MatrixOutput{Primary: in.Square(in.CoMoment.PairM2)}, nil
}

func stubMatrixFactory(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
	return stubMatrixOp{}, nil
}

// validMatrixOp is a registration every matrix validation case starts
// from; each case mutates one thing.
func validMatrixOp() pulse.MatrixOpRegistration {
	return pulse.MatrixOpRegistration{
		Name:        "MAT_ACME_SCATTER",
		Description: "Scatter matrix: each pair's raw weighted co-moment.",
		Factory:     stubMatrixFactory,
		Streamable:  true,
		Params:      []pulse.ParamMeta{{Name: "scale", JSONType: "number"}},
		OutputKeys: descriptor.MatrixOutputKeys{
			Primary: "scatter",
			Scalars: []string{"trace"},
		},
		ComponentSchema: descriptor.ComponentSchema{
			Keys:         []descriptor.ComponentKey{{Name: "trace", Type: "float64", Description: "Sum of the diagonal."}},
			Mergeability: descriptor.Mergeable,
		},
	}
}

func TestExtensions_MatrixOpValidRegistrationPasses(t *testing.T) {
	floorOnly := validMatrixOp()
	floorOnly.Name = "MAT_ACME_PLAIN"
	floorOnly.ComponentSchema = descriptor.ComponentSchema{}
	ext := pulse.Extensions{MatrixOps: []pulse.MatrixOpRegistration{validMatrixOp(), floorOnly}}
	if _, err := pulse.New(newTestOptions(ext)); err != nil {
		t.Fatalf("valid matrix registration rejected: %v", err)
	}
}

// TestExtensions_MatrixOpRegistrationRefusals pins every refusal the
// MatrixOps slot adds — the shared naming policy (MAT prefix, reserved
// namespaces, built-in collision, duplicates, wrong category), the
// shared ParamMeta rules, and the matrix-only factory / OutputKeys /
// ComponentSchema rules — with its code and details.reason.
func TestExtensions_MatrixOpRegistrationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*pulse.MatrixOpRegistration)
		extra  bool // register validMatrixOp() a second time ahead of it
		code   perr.Code
		reason string
	}{
		{"wrong_prefix", func(r *pulse.MatrixOpRegistration) { r.Name = "AGG_ACME_SCATTER" }, false, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"two_segment", func(r *pulse.MatrixOpRegistration) { r.Name = "MAT_SCATTER" }, false, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"lowercase", func(r *pulse.MatrixOpRegistration) { r.Name = "MAT_acme_SCATTER" }, false, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"empty_name", func(r *pulse.MatrixOpRegistration) { r.Name = "" }, false, perr.PULSE_EXTENSION_NAME_INVALID, ""},
		{"reserved_namespace", func(r *pulse.MatrixOpRegistration) { r.Name = "MAT_PULSE_SCATTER" }, false, perr.PULSE_EXTENSION_NAME_RESERVED, ""},
		{"builtin_collision_two_segment", func(r *pulse.MatrixOpRegistration) { r.Name = types.MAT_COVARIANCE }, false, perr.PULSE_EXTENSION_NAME_COLLISION, ""},
		{"builtin_collision_regex_shaped", func(r *pulse.MatrixOpRegistration) { r.Name = types.MAT_PARTIAL_CORRELATION }, false, perr.PULSE_EXTENSION_NAME_COLLISION, ""},
		{"duplicate", func(*pulse.MatrixOpRegistration) {}, true, perr.PULSE_EXTENSION_DUPLICATE, ""},
		{"param_bad_json_type", func(r *pulse.MatrixOpRegistration) { r.Params = []pulse.ParamMeta{{Name: "k", JSONType: "integer"}} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, ""},
		{"param_required_with_default", func(r *pulse.MatrixOpRegistration) {
			r.Params = []pulse.ParamMeta{{Name: "k", JSONType: "number", Required: true, Default: 2}}
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, ""},
		{"param_empty_name", func(r *pulse.MatrixOpRegistration) { r.Params = []pulse.ParamMeta{{JSONType: "number"}} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, ""},
		{"nil_factory", func(r *pulse.MatrixOpRegistration) { r.Factory = nil }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "missing_factory"},
		{"missing_output_keys", func(r *pulse.MatrixOpRegistration) { r.OutputKeys = descriptor.MatrixOutputKeys{} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "missing_output_keys"},
		{"blank_primary", func(r *pulse.MatrixOpRegistration) { r.OutputKeys.Primary = "  " }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "missing_output_keys"},
		{"empty_auxiliary_key", func(r *pulse.MatrixOpRegistration) { r.OutputKeys.Auxiliary = []string{""} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
		{"duplicate_vectors_key", func(r *pulse.MatrixOpRegistration) { r.OutputKeys.Vectors = []string{"a", "a"} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
		{"duplicate_scalars_key", func(r *pulse.MatrixOpRegistration) { r.OutputKeys.Scalars = []string{"trace", "trace"} }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_output_key"},
		{"mergeability_without_keys", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema = descriptor.ComponentSchema{Mergeability: descriptor.Mergeable}
		}, false, perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA, "mergeability_without_keys"},
		{"keys_without_mergeability", func(r *pulse.MatrixOpRegistration) { r.ComponentSchema.Mergeability = "" }, false, perr.PULSE_EXTENSION_PARAM_INVALID, "missing_mergeability"},
		{"empty_component_key", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema.Keys = []descriptor.ComponentKey{{Type: "int"}}
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_component_key"},
		{"duplicate_component_key", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema.Keys = append(r.ComponentSchema.Keys, r.ComponentSchema.Keys[0])
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, "invalid_component_key"},
		{"floor_key_n", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema.Keys = []descriptor.ComponentKey{{Name: "n", Type: "int"}}
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, "reserved_component_key"},
		{"pairwise_floor_key", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema.Keys = []descriptor.ComponentKey{{Name: "min_pair_n", Type: "int"}}
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, "reserved_component_key"},
		{"weighted_floor_key", func(r *pulse.MatrixOpRegistration) {
			r.ComponentSchema.Keys = []descriptor.ComponentKey{{Name: "n_eff", Type: "float64"}}
		}, false, perr.PULSE_EXTENSION_PARAM_INVALID, "reserved_component_key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := validMatrixOp()
			tc.mutate(&reg)
			var regs []pulse.MatrixOpRegistration
			if tc.extra {
				regs = append(regs, validMatrixOp())
			}
			regs = append(regs, reg)
			_, err := pulse.New(newTestOptions(pulse.Extensions{MatrixOps: regs}))
			assertCodedError(t, err, tc.code)
			var ce *perr.CodedError
			_ = errors.As(err, &ce)
			if ce.Details["category"] != "matrix" {
				t.Fatalf("details.category = %v, want matrix (details %v)", ce.Details["category"], ce.Details)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("details.reason = %v, want %s (details %v)", ce.Details["reason"], tc.reason, ce.Details)
			}
		})
	}
}

// TestExtensions_MatrixPrefixOnlyInMatrixOps pins the category check in
// the other direction: a MAT_* name in another slot is refused.
func TestExtensions_MatrixPrefixOnlyInMatrixOps(t *testing.T) {
	ext := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{
		Name: "MAT_ACME_SCATTER", Factory: stubAggregatorFactory,
	}}}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_INVALID)
	var ce *perr.CodedError
	_ = errors.As(err, &ce)
	if ce.Details["expected_prefix"] != "AGG" || ce.Details["actual_prefix"] != "MAT" {
		t.Fatalf("details = %v, want expected_prefix AGG, actual_prefix MAT", ce.Details)
	}
}
