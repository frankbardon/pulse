package feature

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// fakeRecord is a minimal Record for testing the Apply orchestrator without
// the full processing.Record type.
type fakeRecord struct {
	num    map[string]float64
	str    map[string]string
	nulls  map[string]bool
	writes map[string]float64
}

func newFakeRecord() *fakeRecord {
	return &fakeRecord{
		num:    map[string]float64{},
		str:    map[string]string{},
		nulls:  map[string]bool{},
		writes: map[string]float64{},
	}
}

func (r *fakeRecord) NumericValue(name string) (float64, bool) {
	if r.nulls[name] {
		return 0, false
	}
	v, ok := r.num[name]
	return v, ok
}

func (r *fakeRecord) StringValue(name string) (string, bool) {
	if r.nulls[name] {
		return "", false
	}
	v, ok := r.str[name]
	return v, ok
}

func (r *fakeRecord) Set(name string, value float64) {
	r.writes[name] = value
	r.num[name] = value
	delete(r.nulls, name)
}

func (r *fakeRecord) SetNull(name string) {
	r.nulls[name] = true
	delete(r.num, name)
}

// noopComputer returns a constant zero column for verifying Apply plumbing.
type noopComputer struct{ outputLabel string }

func (c *noopComputer) Compute(records []Record, _ string) (map[string]Output, error) {
	out := make([]float64, len(records))
	return map[string]Output{c.outputLabel: {Values: out}}, nil
}

func TestRegistry_LookupAfterRegister(t *testing.T) {
	const fakeType = types.FeatureType("FEAT_TEST_REGISTRY")
	defer func() { delete(featureRegistry, fakeType) }()

	register(fakeType, func(_ *types.Feature, _ *encoding.Schema) (Computer, error) {
		return &noopComputer{outputLabel: "x"}, nil
	})

	got, ok := Lookup(fakeType)
	if !ok {
		t.Fatal("expected Lookup to find registered factory")
	}
	if got == nil {
		t.Fatal("expected non-nil factory")
	}
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	const fakeType = types.FeatureType("FEAT_TEST_DUPLICATE")
	defer func() { delete(featureRegistry, fakeType) }()

	register(fakeType, func(_ *types.Feature, _ *encoding.Schema) (Computer, error) {
		return nil, nil
	})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	register(fakeType, func(_ *types.Feature, _ *encoding.Schema) (Computer, error) {
		return nil, nil
	})
}

func TestApply_EmptyFeaturesIsNoop(t *testing.T) {
	records := []Record{newFakeRecord()}
	if err := Apply(records, nil, nil); err != nil {
		t.Errorf("Apply with no features returned %v", err)
	}
}

func TestApply_RoutesThroughFactoryAndWritesOutput(t *testing.T) {
	const fakeType = types.FeatureType("FEAT_TEST_APPLY")
	defer func() { delete(featureRegistry, fakeType) }()

	register(fakeType, func(_ *types.Feature, _ *encoding.Schema) (Computer, error) {
		return &noopComputer{outputLabel: "fake_out"}, nil
	})

	records := []Record{newFakeRecord(), newFakeRecord()}
	feats := []*types.Feature{{Type: fakeType, Field: "ignored"}}

	if err := Apply(records, feats, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for i, r := range records {
		fr := r.(*fakeRecord)
		if _, ok := fr.writes["fake_out"]; !ok {
			t.Errorf("record %d did not receive fake_out", i)
		}
	}
}

func TestApply_UnknownFeatureTypeReturnsCodedError(t *testing.T) {
	records := []Record{newFakeRecord()}
	feats := []*types.Feature{{Type: types.FeatureType("FEAT_DEFINITELY_NOT_REAL")}}

	err := Apply(records, feats, nil)
	if err == nil {
		t.Fatal("expected error for unknown feature type")
	}
}
