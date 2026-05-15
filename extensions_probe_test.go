package pulse_test

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// nonOnlineAggregator implements only Aggregator. Used to assert that
// a Streamable=true registration whose factory returns a buffered-only
// aggregator surfaces PULSE_EXTENSION_STREAMABLE_MISMATCH from
// pulse.New.
type nonOnlineAggregator struct{}

func (nonOnlineAggregator) Aggregate([]*processing.Record, string) (float64, error) {
	return 0, nil
}

func nonOnlineAggregatorFactory(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
	return nonOnlineAggregator{}, nil
}

func TestExtensions_ProbeAggregator_StreamableMismatch(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:       "AGG_ACME_BAD",
			Factory:    nonOnlineAggregatorFactory,
			Streamable: true,
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	if ce.Code != perr.PULSE_EXTENSION_STREAMABLE_MISMATCH {
		t.Errorf("expected PULSE_EXTENSION_STREAMABLE_MISMATCH, got %s (msg=%q)", ce.Code, ce.Message)
	}
}

func TestExtensions_ProbeAggregator_NonStreamableAccepted(t *testing.T) {
	// Streamable=false with a buffered-only factory MUST register cleanly.
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "AGG_ACME_GOOD",
			Factory: nonOnlineAggregatorFactory,
		}},
	}
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext}); err != nil {
		t.Fatalf("non-streamable registration rejected: %v", err)
	}
}

func TestExtensions_ProbeAggregator_FactoryPanicCaught(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: "AGG_ACME_PANICKY",
			Factory: func(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
				panic("boom")
			},
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
}

func TestExtensions_ProbeAggregator_FactoryReturnsError(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: "AGG_ACME_ERRORS",
			Factory: func(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
				return nil, stderrors.New("factory said no")
			},
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
}

func TestExtensions_ProbeAggregator_FactoryReturnsNil(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: "AGG_ACME_NIL",
			Factory: func(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
				return nil, nil
			},
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
}

// rowLocalOnlyAttribute implements RowLocalAttribute but not
// TwoPassAttribute — used to assert Mode=two_pass probe fails when
// the factory only emits row-local capability.
type rowLocalOnlyAttribute struct{}

func (rowLocalOnlyAttribute) Compute(records []*processing.Record, field string) ([]float64, error) {
	_ = field
	return make([]float64, len(records)), nil
}
func (rowLocalOnlyAttribute) Row(*processing.Record, string) (float64, error) { return 0, nil }

func rowLocalOnlyFactory(*types.Attribute, *encoding.Schema) (processing.AttributeComputer, error) {
	return rowLocalOnlyAttribute{}, nil
}

// computeOnlyAttribute implements only AttributeComputer. Used as the
// must-fail probe target for row_local and two_pass modes.
type computeOnlyAttribute struct{}

func (computeOnlyAttribute) Compute(records []*processing.Record, field string) ([]float64, error) {
	_ = field
	return make([]float64, len(records)), nil
}

func computeOnlyFactory(*types.Attribute, *encoding.Schema) (processing.AttributeComputer, error) {
	return computeOnlyAttribute{}, nil
}

func TestExtensions_ProbeAttribute_RowLocalMismatch(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_BAD",
			Factory: computeOnlyFactory,
			Mode:    pulse.AttributeModeRowLocal,
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	assertCodedError(t, err, perr.PULSE_EXTENSION_STREAMABLE_MISMATCH)
}

func TestExtensions_ProbeAttribute_TwoPassMismatch(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_BAD",
			Factory: rowLocalOnlyFactory,
			Mode:    pulse.AttributeModeTwoPass,
		}},
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	assertCodedError(t, err, perr.PULSE_EXTENSION_STREAMABLE_MISMATCH)
}

func TestExtensions_ProbeAttribute_BufferedAcceptsAnyComputer(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_OK",
			Factory: computeOnlyFactory,
			Mode:    pulse.AttributeModeBuffered,
		}},
	}
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext}); err != nil {
		t.Fatalf("buffered attribute rejected: %v", err)
	}
}
