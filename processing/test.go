package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// RowTest is a tier-1 statistical test consumed during the row scan
// alongside online aggregators. Implementations fold each filter-passing
// record into running state via UpdateRow and produce a TestResult via
// Finalize after the iterator is exhausted.
//
// Per-test state is per-instance; callers construct a fresh instance per
// Process call via a RowTestFactory.
//
// UpdateRow MUST be safe to call zero times (Finalize handles the empty
// input case). Implementations decide whether a row contributes; null
// values are typically skipped via the Record's NumericValue / StringValue
// helpers.
type RowTest interface {
	UpdateRow(record *Record) error
	Finalize() (*types.TestResult, error)
}

// PostTest is a tier-2 statistical test consumed after the window stage
// on the materialized result row set. Run is called once over the post-
// pipeline data and returns a TestResult; tier-2 tests are always
// buffered.
type PostTest interface {
	Run(rows []map[string]any) (*types.TestResult, error)
}

// RowTestFactory creates a RowTest from a specification.
type RowTestFactory func(spec *types.Test, schema *encoding.Schema) (RowTest, error)

// PostTestFactory creates a PostTest from a specification.
type PostTestFactory func(spec *types.Test, schema *encoding.Schema) (PostTest, error)

// rowTestRegistry maps test types to their row-test factory functions.
// Only tier-1-capable tests appear here. A type missing from this registry
// either has no tier-1 implementation yet or is exclusively tier 2.
var rowTestRegistry = map[types.TestType]RowTestFactory{
	types.TEST_T:              newTTestRow,
	types.TEST_WELCH:          newTTestRow,
	types.TEST_CHISQ:          newChiSqRow,
	types.TEST_ANOVA_F:        newAnovaRow,
	types.TEST_KS:             newKSRow,
	types.TEST_PAIRED_T:       newPairedTRow,
	types.TEST_PROP_Z:         newPropZRow,
	types.TEST_PEARSON_R:      newPearsonRRow,
	types.TEST_MANN_WHITNEY_U: newMannWhitneyRow,
	types.TEST_WILCOXON_SR:    newWilcoxonSRRow,
	types.TEST_KRUSKAL_WALLIS: newKruskalWallisRow,
	types.TEST_SPEARMAN_R:     newSpearmanRRow,
	types.TEST_KENDALL_TAU:    newKendallTauRow,
	types.TEST_ANOVA_WELCH:    newAnovaWelchRow,
	types.TEST_ANOVA_RM:       newAnovaRMRow,
	types.TEST_BROWN_FORSYTHE: newBrownForsytheRow,
	types.TEST_FISHER_EXACT:   newFisherExactRow,
	types.TEST_SHAPIRO_WILK:   newShapiroWilkRow,
	types.TEST_Z_TWO_SAMPLE:   newZTestRow,
}

// postTestRegistry maps test types to their post-test factory functions.
// Tier-2 implementations consume the materialized result row set after
// windows. TEST_TUKEY_HSD is intentionally absent in this iteration —
// the studentized-range distribution required for its p-values is
// non-trivial and lands separately.
var postTestRegistry = map[types.TestType]PostTestFactory{
	types.TEST_ANOVA_F:        newAnovaPost,
	types.TEST_TREND:          newTrendPost,
	types.TEST_TUKEY_HSD:      newTukeyHSDPost,
	types.TEST_PEARSON_R:      newPearsonRPost,
	types.TEST_PAIRED_T:       newPairedTPost,
	types.TEST_SPEARMAN_R:     newSpearmanRPost,
	types.TEST_KENDALL_TAU:    newKendallTauPost,
	types.TEST_WILCOXON_SR:    newWilcoxonSRPost,
	types.TEST_ANOVA_WELCH:    newAnovaWelchPost,
	types.TEST_BROWN_FORSYTHE: newBrownForsythePost,
	types.TEST_SHAPIRO_WILK:   newShapiroWilkPost,
	types.TEST_KS:             newKSPost,
}

// rowTestEntry pairs a Test spec with its constructed RowTest instance
// and resolved output label. The processor drives one entry per filter-
// passing record (UpdateRow) and finalizes once per entry at end of pass.
type rowTestEntry struct {
	spec  *types.Test
	test  RowTest
	label string
}

// postTestEntry pairs a Test spec with its constructed PostTest instance
// and resolved output label.
type postTestEntry struct {
	spec  *types.Test
	test  PostTest
	label string
}

// buildRowTests constructs RowTest instances for tier-1 tests. Returns
// PULSE_TEST_UNKNOWN_TYPE if any TestType is not registered as a row test.
func (p *Processor) buildRowTests(tests []*types.Test) ([]rowTestEntry, error) {
	if len(tests) == 0 {
		return nil, nil
	}
	out := make([]rowTestEntry, 0, len(tests))
	for _, t := range tests {
		factory, ok := p.exts.LookupRowTest(t.Type)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_UNKNOWN_TYPE,
				fmt.Sprintf("test type %s is not registered as a row test", t.Type),
				map[string]any{"type": string(t.Type), "tier": "tests"})
		}
		inst, err := factory(t, p.schema)
		if err != nil {
			return nil, err
		}
		out = append(out, rowTestEntry{spec: t, test: inst, label: testLabel(t)})
	}
	return out, nil
}

// buildPostTests constructs PostTest instances for tier-2 tests. Returns
// PULSE_TEST_UNKNOWN_TYPE if any TestType is not registered as a post test.
func (p *Processor) buildPostTests(tests []*types.Test) ([]postTestEntry, error) {
	if len(tests) == 0 {
		return nil, nil
	}
	out := make([]postTestEntry, 0, len(tests))
	for _, t := range tests {
		factory, ok := p.exts.LookupPostTest(t.Type)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_TEST_UNKNOWN_TYPE,
				fmt.Sprintf("test type %s is not registered as a post test", t.Type),
				map[string]any{"type": string(t.Type), "tier": "post_tests"})
		}
		inst, err := factory(t, p.schema)
		if err != nil {
			return nil, err
		}
		out = append(out, postTestEntry{spec: t, test: inst, label: testLabel(t)})
	}
	return out, nil
}

// testLabel returns the output label for a test result. Caller-supplied
// Label wins; otherwise synthesize "<TYPE>" or "<TYPE>_<field>".
func testLabel(t *types.Test) string {
	if t.Label != "" {
		return t.Label
	}
	if t.Field != "" {
		return fmt.Sprintf("%s_%s", t.Type, t.Field)
	}
	return string(t.Type)
}

// canRunRowTests reports whether every test in `tests` has a registered
// row-test factory (built-in or extension). Returns false on the first
// miss so the caller can fall through to the buffered path or surface
// PULSE_TEST_UNKNOWN_TYPE.
func canRunRowTests(tests []*types.Test, exts *ExtensionRegistry) bool {
	for _, t := range tests {
		if _, ok := exts.LookupRowTest(t.Type); !ok {
			return false
		}
	}
	return true
}

// finalizeRowTests calls Finalize on every entry and collects the
// TestResults in the same order as the request. Each entry's resolved
// label is written onto its TestResult so callers can look results up by
// caller-supplied alias.
func finalizeRowTests(entries []rowTestEntry) ([]*types.TestResult, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]*types.TestResult, 0, len(entries))
	for _, e := range entries {
		res, err := e.test.Finalize()
		if err != nil {
			return nil, err
		}
		if res != nil && res.Label == "" {
			res.Label = e.label
		}
		out = append(out, res)
	}
	return out, nil
}

// runPostTests constructs the post-test instances and runs each one over
// the materialized result row set. Returns nil when tests is empty so
// the caller can leave Response.PostTests as the zero value.
func (p *Processor) runPostTests(tests []*types.Test, rows []map[string]any) ([]*types.TestResult, error) {
	entries, err := p.buildPostTests(tests)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]*types.TestResult, 0, len(entries))
	for _, e := range entries {
		res, err := e.test.Run(rows)
		if err != nil {
			return nil, err
		}
		if res != nil && res.Label == "" {
			res.Label = e.label
		}
		out = append(out, res)
	}
	return out, nil
}
