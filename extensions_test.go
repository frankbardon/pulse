package pulse_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// newTestOptions returns a hermetic Options with an in-memory
// afero FS so pulse.New does not consult PULSE_DATA_DIR. Tests that
// inspect Extensions validation only need the FS slot populated.
func newTestOptions(ext pulse.Extensions) pulse.Options {
	return pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext}
}

// stubAggregator is a minimal Aggregator + OnlineAggregator used as a
// probe target in extension tests. It implements both interfaces so a
// registration declaring Streamable=true OR false validates the same
// way at the type level.
type stubAggregator struct{ value float64 }

func (s *stubAggregator) Aggregate(records []*processing.Record, field string) (float64, error) {
	_, _ = records, field
	return s.value, nil
}

func (s *stubAggregator) UpdateRow(record *processing.Record, field string) error {
	_, _ = record, field
	return nil
}

func (s *stubAggregator) Finalize() (float64, error) { return s.value, nil }

func stubAggregatorFactory(*types.Aggregation, *encoding.Schema) (processing.Aggregator, error) {
	return &stubAggregator{value: 1.0}, nil
}

// assertCodedError fails the test unless err is a CodedError with the
// expected Code. The Details map is consulted for context on
// mismatches so debugging a regression is mechanical.
func assertCodedError(t *testing.T, err error, want perr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var ce *perr.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError with code %s, got %T: %v", want, err, err)
	}
	if ce.Code != want {
		t.Fatalf("expected code %s, got %s (msg=%q details=%v)", want, ce.Code, ce.Message, ce.Details)
	}
}

func TestExtensions_ValidRegistrationPasses(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{
				Name:        "AGG_ACME_BRAND_SCORE",
				Description: "ACME brand composite score.",
				Factory:     stubAggregatorFactory,
				Streamable:  true,
				Params: []pulse.ParamMeta{
					{Name: "weights", JSONType: "array"},
					{Name: "label", JSONType: "string", Required: true},
				},
			},
		},
	}
	if _, err := pulse.New(newTestOptions(ext)); err != nil {
		t.Fatalf("valid extension registration rejected: %v", err)
	}
}

func TestExtensions_NameInvalidRegex(t *testing.T) {
	cases := []struct {
		label string
		name  string
	}{
		{"missing namespace segment", "AGG_BRANDSCORE"},
		{"lowercase namespace", "AGG_acme_BRAND"},
		{"lowercase suffix", "AGG_ACME_brand"},
		{"leading underscore", "_AGG_ACME_BRAND"},
		{"trailing underscore", "AGG_ACME_BRAND_"},
		{"unknown category", "BLAH_ACME_BRAND"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			ext := pulse.Extensions{
				Aggregators: []pulse.AggregatorRegistration{{
					Name:    types.AggregationType(tc.name),
					Factory: stubAggregatorFactory,
				}},
			}
			_, err := pulse.New(newTestOptions(ext))
			assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_INVALID)
		})
	}
}

func TestExtensions_NameWrongCategoryPrefix(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "ATTR_ACME_BRAND",
			Factory: stubAggregatorFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_INVALID)
}

func TestExtensions_NameReservedNamespace(t *testing.T) {
	for _, ns := range []string{"BUILTIN", "STANDARD", "CORE", "PULSE"} {
		t.Run(ns, func(t *testing.T) {
			ext := pulse.Extensions{
				Aggregators: []pulse.AggregatorRegistration{{
					Name:    types.AggregationType("AGG_" + ns + "_FOO"),
					Factory: stubAggregatorFactory,
				}},
			}
			_, err := pulse.New(newTestOptions(ext))
			assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_RESERVED)
		})
	}
}

func TestExtensions_CollisionWithBuiltin(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    types.AGG_COUNT,
			Factory: stubAggregatorFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_NAME_COLLISION)
}

func TestExtensions_DuplicateWithinCategory(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: "AGG_ACME_BRAND_SCORE", Factory: stubAggregatorFactory},
			{Name: "AGG_ACME_BRAND_SCORE", Factory: stubAggregatorFactory},
		},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_DUPLICATE)
}

func TestExtensions_DuplicateAcrossCategoriesAllowed(t *testing.T) {
	// Two registrations with parallel names in different categories
	// have distinct namespaces (different category prefix) and must
	// both register cleanly.
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "AGG_ACME_SAME",
			Factory: stubAggregatorFactory,
		}},
		// Same "namespace_suffix" but different category prefix → no
		// real collision.
		Filterers: []pulse.FiltererRegistration{{
			Name:    "FILTER_ACME_SAME",
			Factory: stubFiltererFactory,
		}},
	}
	if _, err := pulse.New(newTestOptions(ext)); err != nil {
		t.Fatalf("cross-category parallel names rejected: %v", err)
	}
}

func TestExtensions_ParamMetaInvalidJSONType(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "AGG_ACME_BRAND_SCORE",
			Factory: stubAggregatorFactory,
			Params: []pulse.ParamMeta{
				{Name: "weights", JSONType: "vector"},
			},
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ParamMetaEmptyName(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "AGG_ACME_BRAND_SCORE",
			Factory: stubAggregatorFactory,
			Params:  []pulse.ParamMeta{{JSONType: "string"}},
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ParamMetaRequiredWithDefault(t *testing.T) {
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:    "AGG_ACME_BRAND_SCORE",
			Factory: stubAggregatorFactory,
			Params: []pulse.ParamMeta{
				{Name: "weights", JSONType: "array", Required: true, Default: []float64{1, 2, 3}},
			},
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_AttributeModeRequired(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_ADJUSTMENT",
			Factory: stubAttributeFactory,
			// Mode left empty
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_AttributeModeUnknown(t *testing.T) {
	ext := pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_ADJUSTMENT",
			Factory: stubAttributeFactory,
			Mode:    pulse.AttributeMode("streaming"),
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_TestTierMissingFactory(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name: "TEST_ACME_BRAND_TEST",
			Tier: pulse.TestTierRow,
			// RowFactory missing
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_TestTierBothFactoriesSet(t *testing.T) {
	ext := pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name:        "TEST_ACME_BRAND_TEST",
			Tier:        pulse.TestTierRow,
			RowFactory:  stubRowTestFactory,
			PostFactory: stubPostTestFactory,
		}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ExprFunctionEmptyName(t *testing.T) {
	ext := pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{{Fn: func() float64 { return 1 }}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ExprFunctionNilFn(t *testing.T) {
	ext := pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{{Name: "rank_familiarity"}},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
}

func TestExtensions_ExprFunctionDuplicate(t *testing.T) {
	fn := func(v float64) float64 { return v }
	ext := pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{
			{Name: "rank_familiarity", Fn: fn},
			{Name: "rank_familiarity", Fn: fn},
		},
	}
	_, err := pulse.New(newTestOptions(ext))
	assertCodedError(t, err, perr.PULSE_EXTENSION_DUPLICATE)
}

func TestExtensions_LookupTableRowsOK(t *testing.T) {
	ext := pulse.Extensions{
		LookupTables: map[string]pulse.LookupTable{
			"adjustments": {
				Description: "calibration multipliers",
				Rows:        map[string]float64{"study_a|2025-01-01": 1.07},
			},
		},
	}
	if _, err := pulse.New(newTestOptions(ext)); err != nil {
		t.Fatalf("rows-backed lookup table rejected: %v", err)
	}
}

func TestExtensions_LookupTableFuncOK(t *testing.T) {
	ext := pulse.Extensions{
		LookupTables: map[string]pulse.LookupTable{
			"adjustments": {
				Lookup: func(keys ...string) (float64, bool, error) {
					return 1.0, len(keys) > 0, nil
				},
			},
		},
	}
	if _, err := pulse.New(newTestOptions(ext)); err != nil {
		t.Fatalf("function-backed lookup table rejected: %v", err)
	}
}

func TestExtensions_LookupTableNeitherOrBoth(t *testing.T) {
	t.Run("neither", func(t *testing.T) {
		ext := pulse.Extensions{
			LookupTables: map[string]pulse.LookupTable{"x": {}},
		}
		_, err := pulse.New(newTestOptions(ext))
		assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
	})
	t.Run("both", func(t *testing.T) {
		ext := pulse.Extensions{
			LookupTables: map[string]pulse.LookupTable{
				"x": {
					Rows:   map[string]float64{"k": 1},
					Lookup: func(keys ...string) (float64, bool, error) { return 1, true, nil },
				},
			},
		}
		_, err := pulse.New(newTestOptions(ext))
		assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
	})
}

func TestExtensions_SynthDistributionCollidesWithBuiltin(t *testing.T) {
	// "uniform" is in synth.AllDistributions; registering it as an
	// extension must collide.
	ext := pulse.Extensions{
		SynthDistributions: []pulse.DistributionRegistration{
			{Name: "uniform"},
		},
	}
	_, err := pulse.New(newTestOptions(ext))
	// "uniform" fails the regex first (no underscores, lowercase) so
	// the name-validation error fires before collision detection.
	// Either coded error proves the slot is wired; we accept the
	// stricter check.
	if err == nil {
		t.Fatalf("expected error for synth distribution name collision/invalid, got nil")
	}
	var ce *perr.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T", err)
	}
	if ce.Code != perr.PULSE_EXTENSION_NAME_INVALID && ce.Code != perr.PULSE_EXTENSION_NAME_COLLISION {
		t.Fatalf("expected name_invalid or collision, got %s", ce.Code)
	}
}

func TestExtensions_ZeroValuePassesThrough(t *testing.T) {
	// Zero-value Extensions must be a no-op — the existing pulse.New
	// contract for embedders that do not opt in.
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()}); err != nil {
		t.Fatalf("zero-value Extensions rejected: %v", err)
	}
}

func TestExtensions_ErrorDetailsCarryContext(t *testing.T) {
	// Error details must surface category + index + name for
	// debugging; downstream tooling (predict suggestions, MCP error
	// surface) consumes the structured payload.
	ext := pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: "AGG_ACME_BRAND_SCORE", Factory: stubAggregatorFactory},
			{Name: "BAD_NAME", Factory: stubAggregatorFactory},
		},
	}
	_, err := pulse.New(newTestOptions(ext))
	var ce *perr.CodedError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T", err)
	}
	if ce.Details == nil {
		t.Fatalf("expected details on extension error; got nil")
	}
	if !strings.Contains(strings.ToLower(ce.Message), "aggregator") {
		t.Errorf("expected message to mention aggregator category; got %q", ce.Message)
	}
}

// ---- stubs shared across the extension test suite -----------------

// stubTwoPassAttribute implements AttributeComputer + RowLocalAttribute
// + TwoPassAttribute so it satisfies every AttributeMode probe target.
// The default stubAttributeFactory returns this to keep simple test
// cases mode-agnostic.
type stubTwoPassAttribute struct{}

func (stubTwoPassAttribute) Compute(records []*processing.Record, field string) ([]float64, error) {
	_ = field
	return make([]float64, len(records)), nil
}
func (stubTwoPassAttribute) Row(*processing.Record, string) (float64, error) { return 0, nil }
func (stubTwoPassAttribute) PrePass(*processing.Record, string) error         { return nil }
func (stubTwoPassAttribute) Finalize() error                                  { return nil }

func stubAttributeFactory(*types.Attribute, *encoding.Schema) (processing.AttributeComputer, error) {
	return stubTwoPassAttribute{}, nil
}

type stubFilter struct{}

func (stubFilter) Build(*types.Filterer, *encoding.Schema) (processing.FilterFunc, error) {
	return func(*processing.Record) (bool, error) { return true, nil }, nil
}

func stubFiltererFactory() processing.FiltererBuilder { return stubFilter{} }

type stubRowTest struct{}

func (stubRowTest) UpdateRow(*processing.Record) error              { return nil }
func (stubRowTest) Finalize() (*types.TestResult, error)            { return &types.TestResult{}, nil }

func stubRowTestFactory(*types.Test, *encoding.Schema) (processing.RowTest, error) {
	return stubRowTest{}, nil
}

type stubPostTest struct{}

func (stubPostTest) Run(rows []map[string]any) (*types.TestResult, error) {
	_ = rows
	return &types.TestResult{}, nil
}

func stubPostTestFactory(*types.Test, *encoding.Schema) (processing.PostTest, error) {
	return stubPostTest{}, nil
}
