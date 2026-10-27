package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_matrix_engine_test.go: an extension MAT_* operator runs
// end to end like a built-in (U30 E1-S3). The reference operator,
// MAT_ACME_COV, renders exactly MAT_COVARIANCE's arithmetic (Cov with
// ddof 1 over the engine's co-moment), so every run is compared BITWISE
// against the built-in over the same request.

// extMatrixCohort writes a 9000-row cohort (three merge blocks): three
// f64 members with scattered nulls, a region and a positive weight.
func extMatrixCohort(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	rows := make([][]string, 0, 9000)
	for i := 0; i < 9000; i++ {
		a := jsonNumber(1.5 + float64((i*7)%11) + float64(i%3)*0.125)
		b := jsonNumber(2.25*float64(i%5) + 0.5 + float64((i*3)%7)*0.01)
		c := jsonNumber(float64((i*13)%17) - 3.75)
		if i%29 == 0 {
			a = ""
		}
		if i%31 == 0 {
			c = ""
		}
		rows = append(rows, []string{a, b, c, []string{"north", "south", "east"}[i%3], jsonNumber(0.5 + float64(i%4))})
	}
	createTestPulseFile(t, fsys, "m.pulse", []string{"a", "b", "c", "region", "w"}, rows)
	return fsys
}

// extCovReg registers MAT_ACME_COV: the ddof-1 covariance over the
// engine's co-moment, the trace as a scalar and a component, and the
// params it saw recorded in *seen.
func extCovReg(streamable, weightAware bool, seen *atomic.Value) MatrixOpRegistration {
	return MatrixOpRegistration{
		Name:        "MAT_ACME_COV",
		Description: "Covariance over the engine co-moment (test twin of MAT_COVARIANCE).",
		Factory: func(spec *types.MatrixSpec, _ *encoding.Schema) (extend.MatrixOperator, error) {
			if seen != nil {
				seen.Store(string(spec.Params))
			}
			return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
				cov := in.CoMoment.Cov(1)
				trace := 0.0
				for i := 0; i < cov.N(); i++ {
					trace += cov.At(i, i)
				}
				out := extend.MatrixOutput{
					Primary:    in.Square(cov.At),
					Components: map[string]any{"trace": trace},
				}
				if in.WantScalars {
					out.Scalars = map[string]float64{"trace": trace}
				}
				if in.Rows != nil && in.WantVectors {
					out.Vectors = map[string]any{"rows": in.Rows.Len()}
				}
				return out, nil
			}), nil
		},
		Streamable:  streamable,
		WeightAware: weightAware,
		Params:      []ParamMeta{{Name: "scale", JSONType: "number", Description: "Unused knob the factory sees."}},
		OutputKeys: descriptor.MatrixOutputKeys{
			Primary: "covariance",
			Vectors: []string{"rows"},
			Scalars: []string{"trace"},
		},
		ComponentSchema: descriptor.ComponentSchema{
			Keys:         []descriptor.ComponentKey{{Name: "trace", Type: "float64", Description: "Sum of the variances."}},
			Mergeability: descriptor.Mergeable,
		},
	}
}

func extMatrixPulse(t *testing.T, fsys afero.Fs, opts Options, regs ...MatrixOpRegistration) *Pulse {
	t.Helper()
	opts.FS = fsys
	opts.Extensions.MatrixOps = append(opts.Extensions.MatrixOps, regs...)
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// extMatrixRequest is a request over m.pulse carrying one matrix spec
// of type t (members a, b, c) with params, optionally grouped by
// region and weighted by w.
func extMatrixRequest(t types.MatrixType, params string, grouped, weighted bool) *types.Request {
	spec := types.MatrixSpec{Type: t, Name: "m", Fields: []string{"a", "b", "c"}}
	if params != "" {
		spec.Params = json.RawMessage(params)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: []types.MatrixSpec{spec}}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}
	}
	if weighted {
		req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	}
	return req
}

// sameMatrixBits fails unless every primary cell of got equals want's
// bit for bit (NaN matches NaN).
func sameMatrixBits(t *testing.T, label string, got, want []types.MatrixResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d results, want %d", label, len(got), len(want))
	}
	for k := range want {
		g, w := got[k].Primary, want[k].Primary
		if g == nil || w == nil || !slices.Equal(g.RowKeys, w.RowKeys) || len(g.Values) != len(w.Values) {
			t.Fatalf("%s[%d]: primary shape %+v vs %+v", label, k, g, w)
		}
		for r := range w.Values {
			for c := range w.Values[r] {
				if math.Float64bits(g.Values[r][c]) != math.Float64bits(w.Values[r][c]) {
					t.Fatalf("%s[%d] cell (%d,%d) = %v, want %v (bitwise)", label, k, r, c, g.Values[r][c], w.Values[r][c])
				}
			}
		}
		if !slices.Equal(g.Labels, w.Labels) || g.Encoding != w.Encoding {
			t.Fatalf("%s[%d]: labels/encoding %v %q vs %v %q", label, k, g.Labels, g.Encoding, w.Labels, w.Encoding)
		}
		if gk, wk := matrixWarningCodes(got[k].Warnings), matrixWarningCodes(want[k].Warnings); !slices.Equal(gk, wk) {
			t.Fatalf("%s[%d]: warnings %v, want the engine's %v", label, k, gk, wk)
		}
	}
}

func matrixWarningCodes(ws []*types.ResponseWarning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Code)
	}
	return out
}

// sameMatrixFloor fails unless the components floors agree.
func sameMatrixFloor(t *testing.T, label string, got, want *types.ResponseComponents) {
	t.Helper()
	if got == nil || want == nil || len(got.Matrices) != len(want.Matrices) {
		t.Fatalf("%s: components %+v vs %+v", label, got, want)
	}
	for k := range want.Matrices {
		g, w := got.Matrices[k], want.Matrices[k]
		g.Type, g.Operator = w.Type, w.Operator
		gj, _ := json.Marshal(g)
		wj, _ := json.Marshal(w)
		if string(gj) != string(wj) {
			t.Fatalf("%s[%d]: floor %s, want %s", label, k, gj, wj)
		}
		if _, ok := got.Matrices[k].Operator["trace"]; !ok {
			t.Fatalf("%s[%d]: operator keys %v lack the declared trace", label, k, got.Matrices[k].Operator)
		}
	}
}

// TestExtensionMatrix_BuiltinParity: MAT_ACME_COV equals MAT_COVARIANCE
// bitwise — primary, engine warnings and the components floor —
// ungrouped and grouped, listwise and pairwise, unweighted and weighted,
// and on the co-moment and the buffered tier alike. The engine owns
// missing / max_drop_share and strips them from the params the operator
// sees.
func TestExtensionMatrix_BuiltinParity(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	var seen atomic.Value
	for _, streamable := range []bool{true, false} {
		p := extMatrixPulse(t, fsys, Options{}, extCovReg(streamable, true, &seen))
		for _, c := range []struct {
			name, params      string
			grouped, weighted bool
		}{
			{"listwise", "", false, false},
			{"pairwise", `{"missing":"pairwise"}`, false, false},
			{"heavy drop", `{"max_drop_share":0.01}`, false, false},
			{"grouped", "", true, false},
			{"grouped pairwise weighted", `{"missing":"pairwise"}`, true, true},
			{"weighted", "", false, true},
		} {
			t.Run(c.name+"/streamable="+strconv.FormatBool(streamable), func(t *testing.T) {
				want, err := p.Process(ctx, extMatrixRequest(types.MAT_COVARIANCE, c.params, c.grouped, c.weighted))
				if err != nil {
					t.Fatal(err)
				}
				extParams := c.params
				if extParams == "" {
					extParams = `{"scale":2}`
				} else {
					extParams = extParams[:len(extParams)-1] + `,"scale":2}`
				}
				got, err := p.Process(ctx, extMatrixRequest("MAT_ACME_COV", extParams, c.grouped, c.weighted))
				if err != nil {
					t.Fatal(err)
				}
				if s, _ := seen.Load().(string); s != `{"scale":2}` {
					t.Fatalf("factory saw params %s, want the engine-owned keys stripped", s)
				}
				sameMatrixBits(t, c.name, got.Matrices, want.Matrices)
				sameMatrixFloor(t, c.name, got.Components, want.Components)
				if got.Matrices[0].Scalars["trace"] == 0 {
					t.Fatalf("scalars = %v, want the declared trace", got.Matrices[0].Scalars)
				}
				// The buffered tier hands Finalize the admitted rows.
				if !streamable && got.Matrices[0].Vectors["rows"] == nil {
					t.Fatalf("buffered tier vectors = %v, want rows", got.Matrices[0].Vectors)
				}
			})
		}
	}
}

// TestExtensionMatrix_PredictParity: predict reports the declared tier
// (the buffered tier is Streamable / Mergeable false and forces the
// request off the stream), resolves the members like the runtime, and
// refuses what the runtime refuses with the same code.
func TestExtensionMatrix_PredictParity(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	for _, streamable := range []bool{true, false} {
		p := extMatrixPulse(t, fsys, Options{}, extCovReg(streamable, true, nil))
		res, err := p.Predict(ctx, extMatrixRequest("MAT_ACME_COV", `{"missing":"pairwise"}`, true, false))
		if err != nil || !res.Valid {
			t.Fatalf("predict: valid=%v err=%v errors=%v", res != nil && res.Valid, err, res)
		}
		if res.Streamable != streamable {
			t.Fatalf("predict Streamable = %v (reasons %v), want %v", res.Streamable, res.StreamableReasons, streamable)
		}
		if len(res.Matrices) != 1 || res.Matrices[0].Streamable != streamable || res.Matrices[0].Mergeable != streamable ||
			!slices.Equal(res.Matrices[0].AxisKeys, []string{"a", "b", "c"}) {
			t.Fatalf("predict matrices = %+v", res.Matrices)
		}
		if (res.Matrices[0].RowBufferBytes != nil) == streamable {
			t.Fatalf("row buffer bytes = %v on streamable=%v", res.Matrices[0].RowBufferBytes, streamable)
		}
	}

	accepts := extCovReg(true, false, nil)
	accepts.Accepts = []encoding.FieldType{encoding.FieldTypeU8}
	p := extMatrixPulse(t, fsys, Options{}, accepts)
	for _, c := range []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		// Accepts narrows the members; empty Accepts admits the
		// built-in member types (the other cases).
		{"accepts", extMatrixRequest("MAT_ACME_COV", "", false, false), perr.PULSE_VECTOR_MEMBER_TYPE},
		{"bad engine param", extMatrixRequest("MAT_ACME_COV", `{"missing":"sometimes"}`, false, false), perr.SERVICE_VALIDATION},
		{"params not an object", extMatrixRequest("MAT_ACME_COV", `[1]`, false, false), perr.SERVICE_VALIDATION},
		// Not WeightAware: any weight in force is refused, the
		// request's included.
		{"not weight aware", extMatrixRequest("MAT_ACME_COV", "", false, true), perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := p
			if c.code == perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE {
				p = extMatrixPulse(t, fsys, Options{}, extCovReg(true, false, nil))
			}
			_, rerr := p.Process(ctx, c.req)
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", rerr, c.code)
			}
			data, _ := afero.ReadFile(fsys, "m.pulse")
			env, _ := p.PredictBytes(ctx, data, c.req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("predict errors = %+v, want %s first", env, c.code)
			}
		})
	}
}

// TestExtensionMatrix_WeightStamped: a WeightAware operator sees the
// resolved weight's kind; with no weight in force it sees "".
func TestExtensionMatrix_WeightStamped(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	var kind atomic.Value
	reg := extCovReg(true, true, nil)
	inner := reg.Factory
	reg.Factory = func(spec *types.MatrixSpec, s *encoding.Schema) (extend.MatrixOperator, error) {
		op, err := inner(spec, s)
		if err != nil {
			return nil, err
		}
		return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			kind.Store(string(in.WeightKind))
			return op.Finalize(in)
		}), nil
	}
	p := extMatrixPulse(t, fsys, Options{}, reg)
	for _, c := range []struct {
		weighted bool
		want     string
	}{{true, "frequency"}, {false, ""}} {
		if _, err := p.Process(context.Background(), extMatrixRequest("MAT_ACME_COV", "", false, c.weighted)); err != nil {
			t.Fatal(err)
		}
		if got := kind.Load().(string); got != c.want {
			t.Fatalf("weighted=%v: WeightKind %q, want %q", c.weighted, got, c.want)
		}
	}
}

// TestExtensionMatrix_CoMomentIsolated: an operator that mutates the
// co-moment it is handed (against the contract) reaches neither the
// components floor nor another bucket: Finalize gets a per-call clone.
func TestExtensionMatrix_CoMomentIsolated(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	reg := extCovReg(true, false, nil)
	inner := reg.Factory
	reg.Factory = func(spec *types.MatrixSpec, s *encoding.Schema) (extend.MatrixOperator, error) {
		op, err := inner(spec, s)
		if err != nil {
			return nil, err
		}
		return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			out, err := op.Finalize(in)
			for i := 0; i < 50; i++ {
				in.CoMoment.Add([]float64{1e6, -1e6, 1e6}, 1)
			}
			return out, err
		}), nil
	}
	p := extMatrixPulse(t, fsys, Options{}, reg)
	want, err := p.Process(ctx, extMatrixRequest(types.MAT_COVARIANCE, "", true, false))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Process(ctx, extMatrixRequest("MAT_ACME_COV", "", true, false))
	if err != nil {
		t.Fatal(err)
	}
	sameMatrixBits(t, "mutating operator", got.Matrices, want.Matrices)
	sameMatrixFloor(t, "mutating operator", got.Components, want.Components)
}

// TestExtensionMatrix_RuntimeOutputCheck: the declared OutputKeys and
// ComponentSchema bind on every run, not only at the probe: a key the
// operator fills only on real data is refused there.
func TestExtensionMatrix_RuntimeOutputCheck(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	reg := extCovReg(true, false, nil)
	reg.Factory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
		return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			out := extend.MatrixOutput{Primary: in.Square(in.CoMoment.Cov(1).At)}
			if in.CoMoment.N() > 100 { // never on the six-row probe
				out.Scalars = map[string]float64{"surprise": 1}
			}
			return out, nil
		}), nil
	}
	p := extMatrixPulse(t, fsys, Options{}, reg)
	_, err := p.Process(context.Background(), extMatrixRequest("MAT_ACME_COV", "", false, false))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH || ce.Details["reason"] != "undeclared_output_key" {
		t.Fatalf("err = %v, want PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH undeclared_output_key", err)
	}
}

// TestExtensionMatrix_RefusalsUnchanged: the matrix host refusals —
// joins, a crosstab, a chain stage after 0 — refuse an extension
// operator exactly as a built-in.
func TestExtensionMatrix_RefusalsUnchanged(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	p := extMatrixPulse(t, fsys, Options{}, extCovReg(true, false, nil))
	spec := []types.MatrixSpec{{Type: "MAT_ACME_COV", Fields: []string{"a", "b"}}}
	joined := &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: spec,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}},
		Joins:        []*types.JoinSpec{{Right: "m.pulse", On: []types.OnPair{{LeftField: "region", RightField: "region"}}, As: "r_"}}}
	crosstab := &types.Request{Cohort: &types.Cohort{Filename: "m.pulse"}, Matrices: spec,
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region"},
		}}
	for _, c := range []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		{"joins", joined, perr.PULSE_MATRIX_UNSUPPORTED_SOURCE},
		{"crosstab", crosstab, perr.PULSE_MATRIX_HOST_CONFLICT},
	} {
		_, err := p.Process(ctx, c.req)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != c.code {
			t.Fatalf("%s: err = %v, want %s", c.name, err, c.code)
		}
	}
	chain := &types.ChainRequest{Cohort: &types.Cohort{Filename: "m.pulse"}, Stages: []*types.ChainStage{
		{Name: "s0", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "total"}},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}}},
		{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
			Matrices: []types.MatrixSpec{{Type: "MAT_ACME_COV", Fields: []string{"total"}}}}},
	}}
	_, err := p.ProcessChain(ctx, chain)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MATRIX_UNSUPPORTED_SOURCE {
		t.Fatalf("chain stage 1: err = %v, want PULSE_MATRIX_UNSUPPORTED_SOURCE", err)
	}
}

// TestExtensionMatrix_SelfDescription: the manifest lists the operator
// in the built-in MatrixMeta shape (engine params, the matrix floor plus
// its keys, weight kinds when WeightAware) and in the matrices
// ComponentsSchemas; the payload schema's MatrixType enum and the
// ontology carry it; the MCP binder enum reads the same snapshot slice.
func TestExtensionMatrix_SelfDescription(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	reg := extCovReg(true, true, nil)
	reg.Purpose = validExtPurpose()
	p := extMatrixPulse(t, fsys, Options{}, reg)
	m := p.Manifest(context.Background())
	if len(m.Extensions.MatrixOps) != 1 {
		t.Fatalf("manifest extensions.matrix_ops = %+v", m.Extensions.MatrixOps)
	}
	meta := m.Extensions.MatrixOps[0]
	var params []string
	for _, x := range meta.Params {
		params = append(params, x.Name)
	}
	var keys []string
	for _, k := range meta.ComponentSchema.Keys {
		keys = append(keys, k.Name)
	}
	if meta.Name != "MAT_ACME_COV" || meta.OutputKeys.Primary != "covariance" || !meta.Streamable || !meta.Mergeable ||
		!slices.Equal(params, []string{"missing", "max_drop_share", "fit", "scale"}) ||
		!slices.Contains(keys, "n") || !slices.Contains(keys, "sum_weights") || !slices.Contains(keys, "trace") ||
		len(meta.WeightKinds) != 2 || len(meta.Intents) == 0 || !slices.Contains(meta.AcceptsTypes, "f64") {
		t.Fatalf("matrix meta = %+v (params %v, keys %v)", meta, params, keys)
	}
	if _, ok := m.ComponentsSchemas.Matrices["MAT_ACME_COV"]; !ok {
		t.Fatal("components_schemas.matrices lacks the extension operator")
	}
	raw, err := p.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range schema.Defs {
		if slices.Contains(d.Enum, "MAT_COVARIANCE") {
			found = slices.Contains(d.Enum, "MAT_ACME_COV")
		}
	}
	if !found {
		t.Fatal("payload schema MatrixType enum lacks MAT_ACME_COV")
	}
	onto := p.Ontology()
	node := false
	for _, n := range onto.Nodes {
		node = node || n.Name == "MAT_ACME_COV"
	}
	edge := false
	for _, e := range onto.Edges {
		edge = edge || (e.From == "operator:MAT_ACME_COV" && e.To == "capability:matrices" && e.Kind == descriptor.OntologyEdgeRequiresCapability)
	}
	if !node || !edge {
		t.Fatalf("ontology node=%v requires-capability edge=%v", node, edge)
	}
}

// TestExtensionMatrix_FeatureProfile: a profile that leaves the operator
// out hides it everywhere (an unknown matrix type at run time, absent
// from the manifest); listing it needs capability:matrices.
func TestExtensionMatrix_FeatureProfile(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	hidden := extMatrixPulse(t, fsys, Options{FeatureProfile: &FeatureProfile{
		Features: []string{"capability:process", "capability:matrices", "MAT_COVARIANCE"}}}, extCovReg(true, false, nil))
	_, err := hidden.Process(ctx, extMatrixRequest("MAT_ACME_COV", "", false, false))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION || ce.Details["reason"] != "unknown_type" {
		t.Fatalf("hidden operator: err = %v, want SERVICE_VALIDATION unknown_type", err)
	}
	if n := len(hidden.Manifest(ctx).Extensions.MatrixOps); n != 0 {
		t.Fatalf("hidden operator in the manifest (%d)", n)
	}
	_, err = New(Options{FS: fsys, Extensions: Extensions{MatrixOps: []MatrixOpRegistration{extCovReg(true, false, nil)}},
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "MAT_ACME_COV"}}})
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_FEATURE_PROFILE_DEPENDENCY {
		t.Fatalf("operator without capability:matrices: err = %v, want PULSE_FEATURE_PROFILE_DEPENDENCY", err)
	}
	shown := extMatrixPulse(t, fsys, Options{FeatureProfile: &FeatureProfile{
		Features: []string{"capability:process", "capability:matrices", "MAT_ACME_COV"}}}, extCovReg(true, false, nil))
	if _, err := shown.Process(ctx, extMatrixRequest("MAT_ACME_COV", "", false, false)); err != nil {
		t.Fatalf("listed operator: %v", err)
	}
}

// pvalueReg is MAT_ACME_PV: a correlation whose auxiliary key holds a
// deterministic per-pair p-value, opted into multiplicity correction
// when pvalues names that key.
func pvalueReg(key, pvalues string) MatrixOpRegistration {
	return MatrixOpRegistration{
		Name:        "MAT_ACME_PV",
		Description: "Correlation with a per-pair p matrix (test operator).",
		Factory: func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
			return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
				out := extend.MatrixOutput{Primary: in.Square(in.CoMoment.Corr().At)}
				if in.WantAuxiliary {
					out.Auxiliary = map[string]*types.MatrixValues{key: in.Square(func(r, c int) float64 {
						if r == c {
							return math.NaN()
						}
						return 0.01 * float64(r+c+1)
					})}
				}
				return out, nil
			}), nil
		},
		Streamable: true,
		PValues:    pvalues,
		OutputKeys: descriptor.MatrixOutputKeys{Primary: "correlation", Auxiliary: []string{key}},
	}
}

// TestExtensionMatrix_Multiplicity: an operator declaring PValues is
// corrected like a built-in inferential one — <key>_adjusted and
// significant_adjusted beside the raw p, the echo, predict p_values —
// under its own block or an inherited request block (it always emits
// its p-values, so it needs no params.inference); without the
// declaration a spec-level block is refused.
func TestExtensionMatrix_Multiplicity(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	ctx := context.Background()
	block := &types.Multiplicity{Method: types.MultiplicityMethodBonferroni}
	own := func() *types.Request {
		r := extMatrixRequest("MAT_ACME_PV", "", false, false)
		r.Matrices[0].Multiplicity = block
		return r
	}
	inherited := func() *types.Request {
		r := extMatrixRequest("MAT_ACME_PV", "", false, false)
		r.Multiplicity = block
		return r
	}
	p := extMatrixPulse(t, fsys, Options{}, pvalueReg("pv", "pv"))
	for name, req := range map[string]func() *types.Request{"own block": own, "request block": inherited} {
		t.Run(name, func(t *testing.T) {
			resp, err := p.Process(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			aux := resp.Matrices[0].Auxiliary
			if aux["pv_adjusted"] == nil || aux["significant_adjusted"] == nil || resp.Matrices[0].Multiplicity == nil {
				t.Fatalf("auxiliary = %v, echo = %v; want the adjusted twins", aux, resp.Matrices[0].Multiplicity)
			}
			// Bonferroni over the three distinct pairs: p(0,1) = 0.02 -> 0.06.
			if got := aux["pv_adjusted"].Values[0][1]; math.Abs(got-0.06) > 1e-12 || aux["pv"].Values[0][1] != 0.02 {
				t.Fatalf("pv_adjusted(0,1) = %v, raw %v", got, aux["pv"].Values[0][1])
			}
			res, err := p.Predict(ctx, req())
			if err != nil || res.PValues == nil || res.PValues.Total != 3 || res.PValues.Uncorrected != 0 {
				t.Fatalf("predict p_values = %+v (err %v), want 3 corrected", res.PValues, err)
			}
		})
	}

	q := extMatrixPulse(t, fsys, Options{}, pvalueReg("p", ""))
	_, err := q.Process(ctx, own())
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MULTIPLICITY_INVALID {
		t.Fatalf("undeclared PValues with a block: err = %v, want PULSE_MULTIPLICITY_INVALID", err)
	}
	resp, err := q.Process(ctx, inherited())
	if err != nil {
		t.Fatal(err)
	}
	if aux := resp.Matrices[0].Auxiliary; aux["p_adjusted"] != nil {
		t.Fatalf("an operator declaring no PValues was corrected: %v", aux)
	}
}

// TestExtensionMatrix_ComponentsOnly: a run that keeps
// components.matrices but excludes the matrices slot still runs the
// finalizer (every part off) for the operator's own component keys, as
// the built-in finalizerComponents do.
func TestExtensionMatrix_ComponentsOnly(t *testing.T) {
	t.Parallel()

	fsys := extMatrixCohort(t)
	var wanted atomic.Value
	reg := extCovReg(true, false, nil)
	inner := reg.Factory
	reg.Factory = func(spec *types.MatrixSpec, s *encoding.Schema) (extend.MatrixOperator, error) {
		op, err := inner(spec, s)
		if err != nil {
			return nil, err
		}
		return probeFuncMatrixOp(func(in *extend.MatrixInput) (extend.MatrixOutput, error) {
			wanted.Store(in.WantAuxiliary || in.WantVectors || in.WantScalars)
			return op.Finalize(in)
		}), nil
	}
	p := extMatrixPulse(t, fsys, Options{}, reg)
	req := extMatrixRequest("MAT_ACME_COV", "", false, false)
	req.Return = &types.Return{Exclude: []string{"matrices"}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Matrices) != 0 || resp.Components == nil || len(resp.Components.Matrices) != 1 {
		t.Fatalf("matrices = %v, components = %+v", resp.Matrices, resp.Components)
	}
	if _, ok := resp.Components.Matrices[0].Operator["trace"]; !ok || wanted.Load() != false {
		t.Fatalf("operator keys = %v, parts wanted = %v; want trace with every part off", resp.Components.Matrices[0].Operator, wanted.Load())
	}
}

// TestExtensionMatrix_RegistrationRefusals: the engine-owned params and
// the multiplicity twins are reserved; PValues must name a declared
// auxiliary key.
func TestExtensionMatrix_RegistrationRefusals(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		mut    func(*MatrixOpRegistration)
		reason string
		code   perr.Code
	}{
		// DependsOn is judged against the feature universe like every
		// category's.
		{"unknown DependsOn", func(r *MatrixOpRegistration) { r.DependsOn = []string{"AGG_NOPE_MISSING"} }, "", perr.PULSE_FEATURE_PROFILE_UNKNOWN},
		{"reserved param missing", func(r *MatrixOpRegistration) {
			r.Params = append(r.Params, ParamMeta{Name: "missing", JSONType: "string"})
		}, "reserved_matrix_param", ""},
		{"reserved param max_drop_share", func(r *MatrixOpRegistration) {
			r.Params = append(r.Params, ParamMeta{Name: "max_drop_share", JSONType: "number"})
		}, "reserved_matrix_param", ""},
		{"reserved param fit", func(r *MatrixOpRegistration) {
			r.Params = append(r.Params, ParamMeta{Name: "fit", JSONType: "string"})
		}, "reserved_matrix_param", ""},
		{"pvalues undeclared", func(r *MatrixOpRegistration) { r.PValues = "q" }, "invalid_pvalues", ""},
		{"adjusted twin declared", func(r *MatrixOpRegistration) {
			r.OutputKeys.Auxiliary = append(r.OutputKeys.Auxiliary, "p_adjusted")
		}, "reserved_output_key", ""},
		{"significant twin declared", func(r *MatrixOpRegistration) {
			r.OutputKeys.Auxiliary = append(r.OutputKeys.Auxiliary, "significant_adjusted")
		}, "reserved_output_key", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			reg := pvalueReg("p", "p")
			c.mut(&reg)
			_, err := New(Options{FS: memFsWith(t, nil), Extensions: Extensions{MatrixOps: []MatrixOpRegistration{reg}}})
			var ce *perr.CodedError
			if c.code != "" {
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
				return
			}
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_PARAM_INVALID || ce.Details["reason"] != c.reason {
				t.Fatalf("err = %v, want PULSE_EXTENSION_PARAM_INVALID %s", err, c.reason)
			}
		})
	}
}
