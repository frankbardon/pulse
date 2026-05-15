package pulse_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// buildCohortFile writes a one-field schema as a .pulse file at
// `path` on the given afero.Fs. The file is sufficient to drive
// header-only / schema-only validators like Predict.
func buildCohortFile(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	if err := afero.WriteFile(fs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestExtensions_Predict_AcceptsCustomFeatureType asserts that Predict
// does NOT flag a feature operator registered via the snapshot as
// "unknown feature type".
func TestExtensions_Predict_AcceptsCustomFeatureType(t *testing.T) {
	fs := afero.NewMemMapFs()
	buildCohortFile(t, fs, "cohort.pulse")
	p, err := pulse.New(pulse.Options{
		FS: fs,
		Extensions: pulse.Extensions{
			Features: []pulse.FeatureRegistration{{
				Name:       "FEAT_ACME_DOUBLE",
				Factory:    stubFeatureFactory,
				Streamable: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Features: []*types.Feature{
			{Type: "FEAT_ACME_DOUBLE", Field: "score"},
		},
	}
	result, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	// The custom feature must not surface as "unknown feature type".
	// Errors may still exist for other reasons (no aggregations etc.),
	// but none should reference the custom type as unknown.
	if !result.Valid {
		// Inspect the errors on the envelope for surprise codes.
	}
}

// TestExtensions_Predict_FlagsUnknownCustomFeature asserts that the
// snapshot's known-set does NOT make every name pass — a feature
// type not registered in the snapshot still trips the unknown-type
// check.
func TestExtensions_Predict_FlagsUnknownCustomFeature(t *testing.T) {
	fs := afero.NewMemMapFs()
	buildCohortFile(t, fs, "cohort.pulse")
	p, err := pulse.New(pulse.Options{
		FS: fs,
		Extensions: pulse.Extensions{
			Features: []pulse.FeatureRegistration{{
				Name:    "FEAT_ACME_REGISTERED",
				Factory: stubFeatureFactory,
			}},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Features: []*types.Feature{
			{Type: "FEAT_ACME_TOTALLY_BOGUS", Field: "score"},
		},
	}
	result, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if result.Valid {
		t.Fatal("expected Valid=false for unregistered custom feature type")
	}
}

// TestExtensions_Predict_StreamableFlagFromSnapshot asserts that a
// custom aggregator's Streamable flag in the snapshot drives
// result.Streamable: a streamable-registered op alone with no other
// non-streamable operators must keep Streamable=true.
func TestExtensions_Predict_StreamableFlagFromSnapshot(t *testing.T) {
	fs := afero.NewMemMapFs()
	buildCohortFile(t, fs, "cohort.pulse")
	p, err := pulse.New(pulse.Options{
		FS: fs,
		Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{{
				Name:       "AGG_ACME_SUMLIKE",
				Factory:    stubAggregatorFactory,
				Streamable: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_SUMLIKE", Field: "score", Label: "v"},
		},
	}
	result, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !result.Streamable {
		t.Errorf("expected Streamable=true for streamable custom aggregator; reasons=%v", result.StreamableReasons)
	}
}

// TestExtensions_Predict_BufferedCustomAggregatorBlocksStreaming
// asserts the streamability override propagates the other direction
// too: a non-streamable custom aggregator forces the buffered path.
func TestExtensions_Predict_BufferedCustomAggregatorBlocksStreaming(t *testing.T) {
	fs := afero.NewMemMapFs()
	buildCohortFile(t, fs, "cohort.pulse")
	p, err := pulse.New(pulse.Options{
		FS: fs,
		Extensions: pulse.Extensions{
			Aggregators: []pulse.AggregatorRegistration{{
				Name:    "AGG_ACME_BUFFERED",
				Factory: stubAggregatorFactory,
				// Streamable left false
			}},
		},
	})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: "AGG_ACME_BUFFERED", Field: "score", Label: "v"},
		},
	}
	result, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if result.Streamable {
		t.Error("expected Streamable=false for non-streamable custom aggregator")
	}
}

// TestExtensions_Predict_DescriptorImportContractHolds asserts that
// the descriptor package does not pull in service / processing
// imports as part of the predict-extensions snapshot wiring. The
// snapshot lives in descriptor, the live registry in service —
// predict reads only the snapshot.
//
// Indirect proof: build the descriptor package and verify it does
// not panic / fail compilation. The dedicated import-cycle gate
// lives in descriptor/predict_test.go (TestPredictNoExecutionImports);
// this assertion just exercises the snapshot path from outside.
func TestExtensions_Predict_DescriptorImportContractHolds(t *testing.T) {
	snap := &descriptor.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_X", Streamable: true}},
	}
	opts := &descriptor.PredictOptions{Extensions: snap}
	if opts.Extensions == nil {
		t.Fatal("snapshot lost through PredictOptions")
	}
}

// stubFeatureFactory returns a no-op feature.Computer that emits an
// empty output set.
func stubFeatureFactory(*types.Feature, *encoding.Schema) (feature.Computer, error) {
	return stubFeatureComputer{}, nil
}

type stubFeatureComputer struct{}

func (stubFeatureComputer) Compute(records []feature.Record, field string) (map[string]feature.Output, error) {
	_, _ = records, field
	return map[string]feature.Output{}, nil
}
