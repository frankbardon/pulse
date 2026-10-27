package pulse

import (
	stderrors "errors"
	"fmt"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// probeFuncMatrixOp is a matrix operator whose Finalize is a closure.
type probeFuncMatrixOp func(*extend.MatrixInput) (extend.MatrixOutput, error)

func (f probeFuncMatrixOp) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	return f(in)
}

// probeScatter is a well-behaved Finalize: the scatter matrix plus the
// trace scalar and component (both declared by probeMatrixReg).
func probeScatter(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	cm := in.CoMoment
	trace := 0.0
	for i := 0; i < cm.P(); i++ {
		trace += cm.PairM2(i, i)
	}
	return extend.MatrixOutput{
		Primary:    in.Square(cm.PairM2),
		Scalars:    map[string]float64{"trace": trace},
		Components: map[string]any{"trace": trace},
	}, nil
}

// probeMatrixReg registers fin under a factory that returns it; every
// probe case starts here and mutates one thing.
func probeMatrixReg(fin probeFuncMatrixOp) MatrixOpRegistration {
	return MatrixOpRegistration{
		Name:        "MAT_ACME_SCATTER",
		Description: "Scatter matrix: each pair's raw weighted co-moment.",
		Factory: func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
			return fin, nil
		},
		Streamable: true,
		OutputKeys: descriptor.MatrixOutputKeys{
			Primary:   "scatter",
			Auxiliary: []string{"corr"},
			Vectors:   []string{"means"},
			Scalars:   []string{"trace"},
		},
		ComponentSchema: descriptor.ComponentSchema{
			Keys:         []descriptor.ComponentKey{{Name: "trace", Type: "float64", Description: "Sum of the diagonal."}},
			Mergeability: descriptor.Mergeable,
		},
	}
}

func newWithMatrixOps(t *testing.T, regs ...MatrixOpRegistration) error {
	t.Helper()
	_, err := New(Options{FS: memFsWith(t, nil), Extensions: Extensions{MatrixOps: regs}})
	return err
}

func matrixProbeError(t *testing.T, err error, code perrors.Code) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if ce.Details["category"] != "matrix" || ce.Details["name"] != "MAT_ACME_SCATTER" {
		t.Fatalf("details = %v, want category matrix, name MAT_ACME_SCATTER", ce.Details)
	}
	return ce
}

// TestExtensions_MatrixProbeValidPasses: a well-behaved operator — a
// declared key left unfilled included (OutputKeys lists what it MAY
// fill) — passes on both tiers, weighted and not.
func TestExtensions_MatrixProbeValidPasses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		streamable, weightAware bool
	}{
		{"streamable", true, false},
		{"buffered", false, false},
		{"streamable_weighted", true, true},
		{"buffered_weighted", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := probeMatrixReg(probeScatter)
			reg.Streamable, reg.WeightAware = tc.streamable, tc.weightAware
			if err := newWithMatrixOps(t, reg); err != nil {
				t.Fatalf("valid matrix operator refused: %v", err)
			}
		})
	}
}

// TestExtensions_MatrixProbeInput pins what the probe hands the factory
// and Finalize: a spec naming the three synthetic members, a schema
// carrying them (and the weight column), a three-member listwise
// co-moment of six rows, every part wanted, rows only on the buffered
// tier — and the weighted probe only for a WeightAware registration.
func TestExtensions_MatrixProbeInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		streamable, weightAware bool
		wantCalls               int
	}{
		{"streamable_unweighted_only", true, false, 1},
		{"buffered_unweighted_only", false, false, 1},
		{"weight_aware_probed_twice", true, true, 2},
		{"buffered_weight_aware", false, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var specs []types.MatrixSpec
			var inputs []extend.MatrixInput
			reg := probeMatrixReg(nil)
			reg.Streamable, reg.WeightAware = tc.streamable, tc.weightAware
			reg.Factory = func(spec *types.MatrixSpec, schema *encoding.Schema) (extend.MatrixOperator, error) {
				specs = append(specs, *spec)
				for _, m := range append(append([]string(nil), spec.Fields...), probeMatrixWeightField) {
					if f := schema.Field(m); f == nil || f.Type != encoding.FieldTypeF64 {
						return nil, fmt.Errorf("schema lacks f64 column %q", m)
					}
				}
				return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
					inputs = append(inputs, *in)
					return probeScatter(in)
				}), nil
			}
			if err := newWithMatrixOps(t, reg); err != nil {
				t.Fatalf("New: %v", err)
			}
			if len(specs) != tc.wantCalls || len(inputs) != tc.wantCalls {
				t.Fatalf("factory calls %d, finalize calls %d, want %d each", len(specs), len(inputs), tc.wantCalls)
			}
			for i, in := range inputs {
				weighted := i == 1
				spec := specs[i]
				if spec.Type != "MAT_ACME_SCATTER" || !slices.Equal(spec.Fields, probeMatrixMembers) {
					t.Errorf("probe %d spec = %+v, want type MAT_ACME_SCATTER over %v", i, spec, probeMatrixMembers)
				}
				if got := spec.Weight.Spec() != nil; got != weighted {
					t.Errorf("probe %d spec carries a weight = %v, want %v", i, got, weighted)
				}
				wantKind := types.WeightKind("")
				if weighted {
					wantKind = types.WeightKindProbability
				}
				if in.WeightKind != wantKind {
					t.Errorf("probe %d WeightKind = %q, want %q", i, in.WeightKind, wantKind)
				}
				if in.CoMoment == nil || in.CoMoment.P() != 3 || in.CoMoment.N() != 6 || !slices.Equal(in.Members, probeMatrixMembers) {
					t.Fatalf("probe %d input = %+v, want a 3-member co-moment of 6 rows", i, in)
				}
				if weighted == (in.CoMoment.W() == 6) {
					t.Errorf("probe %d co-moment weight sum %v (weighted %v)", i, in.CoMoment.W(), weighted)
				}
				if !in.WantAuxiliary || !in.WantVectors || !in.WantScalars {
					t.Errorf("probe %d does not want every part", i)
				}
				if (in.Rows != nil) == tc.streamable {
					t.Errorf("probe %d rows present = %v on streamable = %v", i, in.Rows != nil, tc.streamable)
				}
				if in.Rows != nil && (in.Rows.Len() != 6 || len(in.Rows.Row(0)) != 3) {
					t.Errorf("probe %d rows = %d x %d, want 6 x 3", i, in.Rows.Len(), len(in.Rows.Row(0)))
				}
			}
		})
	}
}

// TestExtensions_MatrixProbeRefusals: every probe failure with its code,
// reason / stage and the weighted flag.
func TestExtensions_MatrixProbeRefusals(t *testing.T) {
	t.Parallel()

	weightedOnly := func(bad func(*extend.MatrixInput) (extend.MatrixOutput, error)) probeFuncMatrixOp {
		return func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			if in.WeightKind != "" {
				return bad(in)
			}
			return probeScatter(in)
		}
	}
	withScatter := func(edit func(*extend.MatrixOutput)) probeFuncMatrixOp {
		return func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			out, err := probeScatter(in)
			edit(&out)
			return out, err
		}
	}
	readsRows := func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
		_ = in.Rows.Len() // nil Rows on the co-moment tier: panics
		return probeScatter(in)
	}
	cases := []struct {
		name     string
		reg      func() MatrixOpRegistration
		code     perrors.Code
		key, val string // a details key the error must carry
		weighted bool
	}{
		{"factory_panic", func() MatrixOpRegistration {
			r := probeMatrixReg(probeScatter)
			r.Factory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) { panic("boom") }
			return r
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "stage", "factory", false},
		{"factory_error", func() MatrixOpRegistration {
			r := probeMatrixReg(probeScatter)
			r.Factory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) { return nil, fmt.Errorf("no") }
			return r
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "factory_error", "no", false},
		{"factory_nil", func() MatrixOpRegistration {
			r := probeMatrixReg(probeScatter)
			r.Factory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) { return nil, nil }
			return r
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "stage", "factory", false},
		{"finalize_panic", func() MatrixOpRegistration {
			return probeMatrixReg(func(*extend.MatrixInput) (extend.MatrixOutput, error) { panic("boom") })
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "stage", "finalize", false},
		{"finalize_error", func() MatrixOpRegistration {
			return probeMatrixReg(func(*extend.MatrixInput) (extend.MatrixOutput, error) {
				return extend.MatrixOutput{}, fmt.Errorf("singular")
			})
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "finalize_error", "singular", false},
		{"weighted_finalize_panic", func() MatrixOpRegistration {
			r := probeMatrixReg(weightedOnly(func(*extend.MatrixInput) (extend.MatrixOutput, error) { panic("boom") }))
			r.WeightAware = true
			return r
		}, perrors.PULSE_EXTENSION_FACTORY_PANIC, "stage", "finalize", true},
		{"streamable_reads_rows", func() MatrixOpRegistration {
			return probeMatrixReg(readsRows)
		}, perrors.PULSE_EXTENSION_STREAMABLE_MISMATCH, "reason", "reads_rows", false},
		{"missing_primary", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) { o.Primary = nil }))
		}, perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, "reason", "missing_primary", false},
		{"undeclared_auxiliary", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) {
				o.Auxiliary = map[string]*types.MatrixValues{"corr": o.Primary, "pvalues": o.Primary}
			}))
		}, perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, "part", "auxiliary", false},
		{"undeclared_vectors", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) { o.Vectors = map[string]any{"sds": []float64{1}} }))
		}, perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, "part", "vectors", false},
		{"undeclared_scalars", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) { o.Scalars["det"] = 1 }))
		}, perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, "reason", "undeclared_output_key", false},
		{"weighted_undeclared_scalar", func() MatrixOpRegistration {
			r := probeMatrixReg(weightedOnly(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
				out, err := probeScatter(in)
				out.Scalars["sum_w"] = in.CoMoment.W()
				return out, err
			}))
			r.WeightAware = true
			return r
		}, perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH, "part", "scalars", true},
		{"undeclared_component", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) { o.Components["det"] = 1.0 }))
		}, perrors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH, "reason", "components_schema_mismatch", false},
		{"floor_component_reemitted", func() MatrixOpRegistration {
			return probeMatrixReg(withScatter(func(o *extend.MatrixOutput) { o.Components["n"] = 6 }))
		}, perrors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH, "reason", "components_schema_mismatch", false},
		{"components_without_schema", func() MatrixOpRegistration {
			r := probeMatrixReg(probeScatter)
			r.ComponentSchema = descriptor.ComponentSchema{}
			return r
		}, perrors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH, "reason", "components_schema_mismatch", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := matrixProbeError(t, newWithMatrixOps(t, tc.reg()), tc.code)
			if got := fmt.Sprint(ce.Details[tc.key]); got != tc.val {
				t.Errorf("details.%s = %q, want %q (details %v)", tc.key, got, tc.val, ce.Details)
			}
			if got := ce.Details["weighted"] == true; got != tc.weighted {
				t.Errorf("details.weighted = %v, want %v (details %v)", ce.Details["weighted"], tc.weighted, ce.Details)
			}
		})
	}
}

// TestExtensions_MatrixProbeUndeclaredDetails pins the mismatch payload:
// the undeclared keys and the declared ones of the offending part.
func TestExtensions_MatrixProbeUndeclaredDetails(t *testing.T) {
	t.Parallel()

	reg := probeMatrixReg(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
		out, err := probeScatter(in)
		out.Scalars["det"], out.Scalars["cond"] = 1, 2
		return out, err
	})
	ce := matrixProbeError(t, newWithMatrixOps(t, reg), perrors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH)
	if got, _ := ce.Details["undeclared"].([]string); !slices.Equal(got, []string{"cond", "det"}) {
		t.Errorf("details.undeclared = %v, want [cond det]", ce.Details["undeclared"])
	}
	if got, _ := ce.Details["declared_keys"].([]string); !slices.Equal(got, []string{"trace"}) {
		t.Errorf("details.declared_keys = %v, want [trace]", ce.Details["declared_keys"])
	}
}

// TestExtensions_MatrixProbeStreamableFailsBothWays: a co-moment-tier
// operator that fails with AND without rows is a plain finalize
// failure, not a streamability mismatch.
func TestExtensions_MatrixProbeStreamableFailsBothWays(t *testing.T) {
	t.Parallel()

	reg := probeMatrixReg(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
		return extend.MatrixOutput{}, fmt.Errorf("always")
	})
	ce := matrixProbeError(t, newWithMatrixOps(t, reg), perrors.PULSE_EXTENSION_FACTORY_PANIC)
	if ce.Details["stage"] != "finalize" {
		t.Errorf("details = %v, want stage finalize", ce.Details)
	}
	// The buffered tier hands the rows, so the same rows-reading
	// operator passes there.
	rowsReader := probeMatrixReg(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
		_ = in.Rows.Len()
		return probeScatter(in)
	})
	rowsReader.Streamable = false
	if err := newWithMatrixOps(t, rowsReader); err != nil {
		t.Fatalf("buffered rows reader refused: %v", err)
	}
}

// TestExtensions_MatrixPurposeValidated: a matrix registration's
// Purpose runs the built-in tier's rules at pulse.New.
func TestExtensions_MatrixPurposeValidated(t *testing.T) {
	t.Parallel()

	reg := probeMatrixReg(probeScatter)
	reg.Purpose = validExtPurpose()
	if err := newWithMatrixOps(t, reg); err != nil {
		t.Fatalf("valid matrix Purpose refused: %v", err)
	}
	reg.Purpose.Intents = []string{"vibes"}
	ce := purposeInvalid(t, newWithMatrixOps(t, reg))
	if ce.Details["category"] != "matrix" || ce.Details["name"] != "MAT_ACME_SCATTER" || ce.Details["part"] != "purpose" {
		t.Errorf("details = %v, want category matrix, name MAT_ACME_SCATTER, part purpose", ce.Details)
	}
}
