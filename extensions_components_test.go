package pulse_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// brandScoreSchema returns the ComponentSchema declaration used by
// the AGG_ACME_BRAND_SCORE test registration. Two operator-specific
// keys (sum, weights_applied) layered on top of the universal floor
// the orchestrator fills unconditionally.
func brandScoreSchema() descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "n", Type: "int", Description: "Records aggregated (universal floor mirror)."},
			{Name: "n_null", Type: "int", Description: "Null inputs encountered (universal floor mirror)."},
			{Name: "sum", Type: "float64", Description: "Running sum of brand scores."},
			{Name: "weights_applied", Type: "int", Description: "Number of weight multipliers applied."},
		},
		Mergeability: descriptor.Mergeable,
	}
}

// brandScoreCohort writes a 5-row f64 cohort and returns a pulse
// instance backed by the same fs so process can read it back.
func brandScoreCohort(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, v := range []float64{10, 20, 30, 40, 50} {
		var rec bytes.Buffer
		if err := encoding.WriteFieldValue(&rec, schema.Fields[0].Type, math.Float64bits(v)); err != nil {
			t.Fatalf("WriteFieldValue: %v", err)
		}
		buf.Write(rec.Bytes())
	}
	path := "brand.pulse"
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, path
}

// brandScoreAggregator is a minimal Aggregator + OnlineAggregator used
// as the underlying instance for the AGG_ACME_BRAND_SCORE registration.
// Its running state is (sum, count, weights_applied) — components
// emission picks the matching subset.
type brandScoreAggregator struct {
	sum     float64
	count   int
	weights int
}

func (a *brandScoreAggregator) Aggregate(records []*processing.Record, field string) (float64, error) {
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		a.sum += v
		a.count++
		a.weights++
	}
	if a.count == 0 {
		return 0, nil
	}
	return a.sum / float64(a.count), nil
}

func (a *brandScoreAggregator) UpdateRow(r *processing.Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.sum += v
	a.count++
	a.weights++
	return nil
}

func (a *brandScoreAggregator) Finalize() (float64, error) {
	if a.count == 0 {
		return 0, nil
	}
	return a.sum / float64(a.count), nil
}

func brandScoreFactory(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
	return &brandScoreAggregator{}, nil
}

func brandScoreEmit(instance processing.Aggregator) (map[string]any, error) {
	a, ok := instance.(*brandScoreAggregator)
	if !ok {
		return nil, errors.New("brandScoreEmit: unexpected instance type")
	}
	return map[string]any{
		"sum":             a.sum,
		"weights_applied": a.weights,
	}, nil
}

// TestExtensions_AggregatorComponents_EmittedToResponse asserts the
// runtime wrapper plumbs ComponentsFunc output onto
// Response.Components.Aggregations[i].Operator. The universal floor
// ({n, n_null}) is owned by the orchestrator and stays present in the
// AggregationComponents wrapper; the operator-specific keys (sum,
// weights_applied) ride the Operator map.
func TestExtensions_AggregatorComponents_EmittedToResponse(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Description:     "ACME brand composite score with weight emission.",
			Factory:         brandScoreFactory,
			Streamable:      true,
			Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
			ComponentSchema: brandScoreSchema(),
			ComponentsFunc:  brandScoreEmit,
		}},
	}
	p, path := brandScoreCohort(t, ext)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BRAND_SCORE", Field: "score", Label: "brand_score"},
		},
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if got := len(resp.Components.Aggregations); got != 1 {
		t.Fatalf("Aggregations slots = %d, want 1", got)
	}
	entry := resp.Components.Aggregations[0]
	if entry.N != 5 {
		t.Errorf("N = %d, want 5", entry.N)
	}
	if entry.Label != "brand_score" {
		t.Errorf("Label = %q, want brand_score", entry.Label)
	}
	if entry.Operator == nil {
		t.Fatalf("Operator map nil; want sum + weights_applied keys")
	}
	if _, ok := entry.Operator["sum"]; !ok {
		t.Errorf("Operator missing 'sum' key; got %v", entry.Operator)
	}
	if _, ok := entry.Operator["weights_applied"]; !ok {
		t.Errorf("Operator missing 'weights_applied' key; got %v", entry.Operator)
	}
}

// TestExtensions_AggregatorComponents_FloorOnly asserts that an
// extension aggregator without ComponentSchema / ComponentsFunc still
// gets the universal floor populated unconditionally by the
// orchestrator. The Operator map stays nil — the floor-only contract.
func TestExtensions_AggregatorComponents_FloorOnly(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:       "AGG_ACME_FLOOR",
			Factory:    brandScoreFactory,
			Streamable: true,
			Accepts:    []encoding.FieldType{encoding.FieldTypeF64},
			// No ComponentSchema, no ComponentsFunc — floor-only.
		}},
	}
	p, path := brandScoreCohort(t, ext)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_FLOOR", Field: "score"},
		},
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated (universal floor)")
	}
	if got := len(resp.Components.Aggregations); got != 1 {
		t.Fatalf("Aggregations slots = %d, want 1", got)
	}
	entry := resp.Components.Aggregations[0]
	if entry.N != 5 {
		t.Errorf("N = %d, want 5 (universal floor populated)", entry.N)
	}
	if entry.Operator != nil {
		t.Errorf("Operator = %v, want nil (floor-only)", entry.Operator)
	}
}

func TestExtensions_ComponentsFunc_WithoutSchemaRejected(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:           "AGG_ACME_BRAND_SCORE",
			Factory:        brandScoreFactory,
			Streamable:     true,
			ComponentsFunc: brandScoreEmit,
			// ComponentSchema intentionally empty.
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA)
}

// TestExtensions_ComponentSchema_WithoutEmitterRejected asserts that
// declaring a ComponentSchema with non-empty Keys but no
// ComponentsFunc is rejected — the schema would never be satisfied at
// runtime.
func TestExtensions_ComponentSchema_WithoutEmitterRejected(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: brandScoreSchema(),
			// ComponentsFunc intentionally nil.
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

// TestExtensions_ComponentSchema_MissingMergeabilityRejected asserts
// that declaring Keys without a Mergeability classification is
// rejected — every schema must declare intent.
func TestExtensions_ComponentSchema_MissingMergeabilityRejected(t *testing.T) {
	schema := descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "sum", Type: "float64", Description: "Running sum."},
		},
		// Mergeability intentionally empty.
	}
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: schema,
			ComponentsFunc:  brandScoreEmit,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ComponentsFunc_EmittedKeyMismatchRejected(t *testing.T) {
	emit := func(processing.Aggregator) (map[string]any, error) {
		return map[string]any{"undeclared_key": 1.0}, nil
	}
	schema := descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "sum", Type: "float64", Description: "Running sum."},
		},
		Mergeability: descriptor.Mergeable,
	}
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: schema,
			ComponentsFunc:  emit,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH)
}

// TestExtensions_Manifest_CarriesExtensionComponentSchema asserts the
// manifest's ComponentsSchemas.Aggregators map carries the embedder-
// declared schema alongside built-ins. LLM clients consuming the
// manifest can plan ResponseComponents consumption for extension
// operators without crawling Components.Aggregators per-entry.
func TestExtensions_Manifest_CarriesExtensionComponentSchema(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: brandScoreSchema(),
			ComponentsFunc:  brandScoreEmit,
		}},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	m := p.Manifest(context.Background())
	if m.ComponentsSchemas.Aggregators == nil {
		t.Fatalf("ComponentsSchemas.Aggregators nil; want non-empty")
	}
	got, ok := m.ComponentsSchemas.Aggregators["AGG_ACME_BRAND_SCORE"]
	if !ok {
		t.Fatalf("extension aggregator missing from manifest ComponentsSchemas; have keys: %v", manifestAggKeys(m))
	}
	if got.Mergeability != descriptor.Mergeable {
		t.Errorf("Mergeability = %q, want mergeable", got.Mergeability)
	}
	if len(got.Keys) != 4 {
		t.Errorf("Keys count = %d, want 4 (n, n_null, sum, weights_applied)", len(got.Keys))
	}
}

// manifestAggKeys returns the sorted Aggregators keys for diagnostic
// output on test failure.
func manifestAggKeys(m *descriptor.Manifest) []string {
	out := make([]string, 0, len(m.ComponentsSchemas.Aggregators))
	for k := range m.ComponentsSchemas.Aggregators {
		out = append(out, k)
	}
	return out
}

// TestExtensions_Predict_CarriesExtensionComponentSchema asserts
// predict surfaces the extension operator's ComponentSchema on the
// per-slot AggregationPredict descriptor. The lookup must reach
// through the ExtensionsSnapshot.ComponentSchemas projection — predict
// stays no-execute.
func TestExtensions_Predict_CarriesExtensionComponentSchema(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
			ComponentSchema: brandScoreSchema(),
			ComponentsFunc:  brandScoreEmit,
		}},
	}
	p, path := brandScoreCohort(t, ext)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BRAND_SCORE", Field: "score"},
		},
	}
	result, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(result.Aggregations) != 1 {
		t.Fatalf("predict Aggregations slots = %d, want 1", len(result.Aggregations))
	}
	got := result.Aggregations[0].ComponentSchema
	if got.Mergeability != descriptor.Mergeable {
		t.Errorf("Predict ComponentSchema.Mergeability = %q, want mergeable", got.Mergeability)
	}
	if len(got.Keys) != 4 {
		t.Errorf("Predict ComponentSchema.Keys count = %d, want 4", len(got.Keys))
	}
}

// TestExtensions_ComponentSchemaParity is the happy-path round-trip:
// a registration with matching ComponentSchema + ComponentsFunc passes
// pulse.New() and yields a working process.
func TestExtensions_ComponentSchemaParity(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
			ComponentSchema: brandScoreSchema(),
			ComponentsFunc:  brandScoreEmit,
		}},
	}
	p, path := brandScoreCohort(t, ext)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BRAND_SCORE", Field: "score"},
		},
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
		t.Fatalf("Components.Aggregations missing; resp.Components=%+v", resp.Components)
	}
	entry := resp.Components.Aggregations[0]
	if entry.Operator == nil {
		t.Fatalf("Operator map nil; want sum + weights_applied keys")
	}
}

// TestExtensions_MissingComponentSchema asserts a registration that
// supplies a ComponentsFunc without ComponentSchema.Keys is rejected at
// pulse.New() time with PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA.
func TestExtensions_MissingComponentSchema(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:           "AGG_ACME_BRAND_SCORE",
			Factory:        brandScoreFactory,
			Streamable:     true,
			ComponentsFunc: brandScoreEmit,
			// ComponentSchema.Keys intentionally empty.
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA)
}

// TestExtensions_MissingComponentSchema_MergeabilityWithoutKeys asserts
// a registration that declares ComponentSchema.Mergeability with no
// Keys is rejected with PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA — the
// "Mergeability set but no Keys (incoherent)" branch of the contract.
func TestExtensions_MissingComponentSchema_MergeabilityWithoutKeys(t *testing.T) {
	schema := descriptor.ComponentSchema{
		// Keys intentionally empty.
		Mergeability: descriptor.Mergeable,
	}
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: schema,
			// ComponentsFunc intentionally nil.
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA)
}

// TestExtensions_ComponentSchemaMismatch asserts a registration whose
// ComponentsFunc returns keys not declared in ComponentSchema.Keys is
// rejected at pulse.New() time with
// PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH.
func TestExtensions_ComponentSchemaMismatch(t *testing.T) {
	emit := func(processing.Aggregator) (map[string]any, error) {
		return map[string]any{
			"sum":         42.0,
			"extra_field": "uh oh",
		}, nil
	}
	schema := descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "sum", Type: "float64", Description: "Running sum."},
		},
		Mergeability: descriptor.Mergeable,
	}
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: schema,
			ComponentsFunc:  emit,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH)
}

// TestExtensions_ComponentsFunc_PanicDuringProbe asserts the existing
// PULSE_EXTENSION_FACTORY_PANIC surfaces when ComponentsFunc panics
// during probe-validation. The probe wraps the emitter in a deferred
// recover, so an embedder bug never crashes pulse.New().
func TestExtensions_ComponentsFunc_PanicDuringProbe(t *testing.T) {
	emit := func(processing.Aggregator) (map[string]any, error) {
		panic("boom")
	}
	schema := descriptor.ComponentSchema{
		Keys: []descriptor.ComponentKey{
			{Name: "sum", Type: "float64", Description: "Running sum."},
		},
		Mergeability: descriptor.Mergeable,
	}
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:            "AGG_ACME_BRAND_SCORE",
			Factory:         brandScoreFactory,
			Streamable:      true,
			ComponentSchema: schema,
			ComponentsFunc:  emit,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
}
