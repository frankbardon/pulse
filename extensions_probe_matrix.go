package pulse

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// probeMatrixMembers are the three member columns of the synthetic
// matrix probe; probeMatrixWeightField is the weight column a
// WeightAware operator's second probe reads (probeWeight's field).
var probeMatrixMembers = []string{"probe_m1", "probe_m2", "probe_m3"}

const probeMatrixWeightField = "probe_weight"

// probeMatrixData is the synthetic admitted row set: six complete,
// non-collinear rows over the three members (a well-conditioned
// covariance, so a correctly written operator succeeds), with the row
// weights the weighted probe folds.
var probeMatrixData = struct {
	rows    [][]float64
	weights []float64
}{
	rows: [][]float64{
		{1, 2, 4}, {2, 1, 3}, {3, 5, 2}, {4, 3, 6}, {5, 6, 1}, {6, 4, 5},
	},
	weights: []float64{1, 2, 0.5, 1.5, 1, 3},
}

// probeMatrixSchema is the synthetic schema a matrix factory is built
// against: the three f64 members plus the f64 weight column.
func probeMatrixSchema() *encoding.Schema {
	fields := make([]encoding.Field, 0, len(probeMatrixMembers)+1)
	for _, name := range append(append([]string(nil), probeMatrixMembers...), probeMatrixWeightField) {
		fields = append(fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, ByteOffset: len(fields) * 8})
	}
	return &encoding.Schema{Fields: fields}
}

// probeMatrixRows implements extend.MatrixRows over probeMatrixData.
type probeMatrixRows struct{ weighted bool }

func (probeMatrixRows) Len() int { return len(probeMatrixData.rows) }

func (probeMatrixRows) Row(i int) []float64 {
	return append([]float64(nil), probeMatrixData.rows[i]...)
}

func (r probeMatrixRows) Weight(i int) float64 {
	if r.weighted {
		return probeMatrixData.weights[i]
	}
	return 1
}

// probeMatrixInput builds the synthetic extend.MatrixInput: a fresh
// listwise co-moment over probeMatrixData (weighted when asked), every
// return part wanted, the rows attached when withRows.
func probeMatrixInput(reg MatrixOpRegistration, weighted, withRows bool) *extend.MatrixInput {
	cm, err := linalg.NewCoMoment(len(probeMatrixMembers), linalg.Listwise)
	if err != nil {
		panic(err) // unreachable: a fixed positive p and a known mode
	}
	rows := probeMatrixRows{weighted: weighted}
	for i := 0; i < rows.Len(); i++ {
		cm.Add(probeMatrixData.rows[i], rows.Weight(i))
	}
	in := &extend.MatrixInput{
		Name:          string(reg.Name),
		CoMoment:      cm,
		Members:       append([]string(nil), probeMatrixMembers...),
		Encoding:      types.MatrixEncodingFull,
		WantAuxiliary: true,
		WantVectors:   true,
		WantScalars:   true,
	}
	if weighted {
		in.WeightKind = types.WeightKindProbability
	}
	if withRows {
		in.Rows = rows
	}
	return in
}

// probeMatrixOps runs every matrix registration end to end over a
// synthetic co-moment — the first probe that feeds data. The factory is
// built against a synthetic three-member schema (probeMatrixSchema)
// with a spec naming those members, and Finalize runs over a small
// well-conditioned co-moment of them with every return part wanted.
// Each step runs under recover():
//
//   - a factory panic, error or nil instance, and a Finalize panic or
//     error — PULSE_EXTENSION_FACTORY_PANIC (details.stage factory /
//     finalize).
//   - a Streamable=true operator that fails without
//     extend.MatrixInput.Rows yet succeeds with them —
//     PULSE_EXTENSION_STREAMABLE_MISMATCH, reason reads_rows: the
//     co-moment tier never carries rows, so the request would fail at
//     run time.
//   - no Primary, or an Auxiliary / Vectors / Scalars key OutputKeys
//     does not declare — PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH. A
//     declared key left unfilled is allowed: OutputKeys lists what the
//     operator MAY fill (a part can depend on params or data, as for
//     the built-ins).
//   - a MatrixOutput.Components key ComponentSchema does not declare
//     (the matrix floor keys are never declarable, so re-emitting one
//     is undeclared too) — PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH.
//
// A WeightAware registration is probed a second time with probeWeight
// on its spec, a weighted co-moment and WeightKind probability; a
// failure there carries details.weighted: true. An operator that is
// not WeightAware never sees a weight (the engine refuses one in
// force), so it is probed unweighted only.
func probeMatrixOps(regs []MatrixOpRegistration) error {
	schema := probeMatrixSchema()
	for _, reg := range regs {
		probes := []bool{false}
		if reg.WeightAware {
			probes = append(probes, true)
		}
		for _, weighted := range probes {
			out, err := probeMatrixRun(reg, schema, weighted)
			if err != nil {
				return err
			}
			if err := verifyMatrixOutputKeys(reg, out, weighted); err != nil {
				return err
			}
			if err := verifyMatrixComponentKeys(reg, out.Components, weighted); err != nil {
				return err
			}
		}
	}
	return nil
}

// probeMatrixRun builds the operator and finalizes it once. The
// co-moment tier (Streamable=true) finalizes without rows; when that
// fails, a retry with rows tells a STREAMABLE_MISMATCH (the operator
// reads rows the tier never carries) from a plain finalize failure.
func probeMatrixRun(reg MatrixOpRegistration, schema *encoding.Schema, weighted bool) (extend.MatrixOutput, error) {
	op, err := safeBuildMatrixOp(reg, schema, weighted)
	if err != nil {
		return extend.MatrixOutput{}, err
	}
	out, err := safeFinalizeMatrixOp(reg, op, probeMatrixInput(reg, weighted, !reg.Streamable), weighted)
	if err == nil || !reg.Streamable {
		return out, err
	}
	if _, retryErr := safeFinalizeMatrixOp(reg, op, probeMatrixInput(reg, weighted, true), weighted); retryErr == nil {
		return extend.MatrixOutput{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_EXTENSION_STREAMABLE_MISMATCH,
			fmt.Sprintf("matrix %q declares Streamable=true but Finalize fails without MatrixInput.Rows (the co-moment tier carries none; declare Streamable=false to receive rows)", reg.Name),
			probeDetails(map[string]any{
				"category":   string(categoryMatrix),
				"name":       string(reg.Name),
				"streamable": true,
				"reason":     "reads_rows",
			}, weighted),
		)
	}
	return extend.MatrixOutput{}, err
}

// safeBuildMatrixOp invokes the factory under a deferred recover with
// the same contract as safeBuildAggregator.
func safeBuildMatrixOp(reg MatrixOpRegistration, schema *encoding.Schema, weighted bool) (op extend.MatrixOperator, err error) {
	fail := func(what string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryMatrix), "name": string(reg.Name), "stage": "factory"}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("matrix factory %q %s during probe-validation", reg.Name, what),
			probeDetails(details, weighted))
	}
	defer func() {
		if r := recover(); r != nil {
			op, err = nil, fail(fmt.Sprintf("panicked: %v", r), map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	spec := &types.MatrixSpec{Type: reg.Name, Fields: append([]string(nil), probeMatrixMembers...)}
	if weighted {
		spec.Weight = probeWeight
	}
	op, err = reg.Factory(spec, schema)
	if err != nil {
		return nil, fail(fmt.Sprintf("returned error: %v", err), map[string]any{"factory_error": err.Error()})
	}
	if op == nil {
		return nil, fail("returned a nil instance", nil)
	}
	return op, nil
}

// safeFinalizeMatrixOp runs Finalize under a deferred recover; a panic
// or an error is PULSE_EXTENSION_FACTORY_PANIC, details.stage finalize.
func safeFinalizeMatrixOp(reg MatrixOpRegistration, op extend.MatrixOperator, in *extend.MatrixInput, weighted bool) (out extend.MatrixOutput, err error) {
	fail := func(what string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryMatrix), "name": string(reg.Name), "stage": "finalize"}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_FACTORY_PANIC,
			fmt.Sprintf("matrix %q Finalize %s during probe-validation", reg.Name, what),
			probeDetails(details, weighted))
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = extend.MatrixOutput{}, fail(fmt.Sprintf("panicked: %v", r), map[string]any{"panic": fmt.Sprintf("%v", r)})
		}
	}()
	out, err = op.Finalize(in)
	if err != nil {
		return extend.MatrixOutput{}, fail(fmt.Sprintf("returned error: %v", err), map[string]any{"finalize_error": err.Error()})
	}
	return out, nil
}

// verifyMatrixOutputKeys checks the finalized output against the
// registration's OutputKeys: Primary present, and every Auxiliary /
// Vectors / Scalars key declared in its part.
func verifyMatrixOutputKeys(reg MatrixOpRegistration, out extend.MatrixOutput, weighted bool) error {
	mismatch := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"category": string(categoryMatrix), "name": string(reg.Name), "reason": reason}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH,
			fmt.Sprintf("matrix %q %s", reg.Name, msg), probeDetails(details, weighted))
	}
	if out.Primary == nil {
		return mismatch("missing_primary",
			fmt.Sprintf("Finalize returned no Primary matrix (OutputKeys.Primary declares %q)", reg.OutputKeys.Primary),
			map[string]any{"part": "primary"})
	}
	for _, part := range []struct {
		name     string
		declared []string
		emitted  []string
	}{
		{"auxiliary", reg.OutputKeys.Auxiliary, sortedKeys(out.Auxiliary)},
		{"vectors", reg.OutputKeys.Vectors, sortedKeys(out.Vectors)},
		{"scalars", reg.OutputKeys.Scalars, sortedKeys(out.Scalars)},
	} {
		if extras := undeclaredKeys(part.emitted, part.declared); len(extras) > 0 {
			return mismatch("undeclared_output_key",
				fmt.Sprintf("Finalize filled %s keys OutputKeys does not declare: %v (declared: %v)", part.name, extras, part.declared),
				map[string]any{"part": part.name, "undeclared": extras, "declared_keys": append([]string{}, part.declared...)})
		}
	}
	return nil
}

// verifyMatrixComponentKeys checks MatrixOutput.Components against
// ComponentSchema.Keys. Unlike verifyComponentKeysSubset no floor key
// is tolerated: the matrix floor is the engine's, an operator copy
// would shadow it, and validation already forbids declaring one.
func verifyMatrixComponentKeys(reg MatrixOpRegistration, emitted map[string]any, weighted bool) error {
	declared := make([]string, 0, len(reg.ComponentSchema.Keys))
	for _, k := range reg.ComponentSchema.Keys {
		declared = append(declared, k.Name)
	}
	extras := undeclaredKeys(sortedKeys(emitted), declared)
	if len(extras) == 0 {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH,
		fmt.Sprintf("extension matrix %q MatrixOutput.Components emitted keys not declared in ComponentSchema: %v (declared: %v)", reg.Name, extras, declared),
		probeDetails(map[string]any{
			"category":      string(categoryMatrix),
			"name":          string(reg.Name),
			"undeclared":    extras,
			"declared_keys": declared,
			"reason":        "components_schema_mismatch",
		}, weighted),
	)
}

// undeclaredKeys returns the emitted keys absent from declared, in
// emitted order.
func undeclaredKeys(emitted, declared []string) []string {
	allowed := make(map[string]bool, len(declared))
	for _, k := range declared {
		allowed[k] = true
	}
	var out []string
	for _, k := range emitted {
		if !allowed[k] {
			out = append(out, k)
		}
	}
	return out
}
