package processing

import (
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestPairwiseCellAggregatorSignaturesMatchCapabilities is the parity
// gate for cellAggregatorIdentitySignatures.
//
// That table RESTATES the operator half of four aggregators'
// ComponentSchema from descriptor/capabilities_aggregators.go. Nothing
// cross-checked the two before, so renaming a component key — say
// AGG_DISTINCT_SUM's "distinct_count" — would leave the runtime
// classifying every AGG_DISTINCT_SUM cell as UNIDENTIFIED and silently
// refusing n_within_distinct on the one aggregator it exists for. The
// opposite drift is worse: a capability schema that grows toward an
// existing signature would promote an aggregator into the admitted set.
//
// Reads the public BuildManifest projection (via
// manifestAggregatorOperatorKeys) rather than the private capabilities
// table, so it gates the same surface LLM clients consume. processing/
// may import descriptor/ from a test — the no-execute import ban runs
// the other way and only forbids descriptor/ importing processing/.
func TestPairwiseCellAggregatorSignaturesMatchCapabilities(t *testing.T) {
	if len(cellAggregatorIdentitySignatures) == 0 {
		t.Fatal("cellAggregatorIdentitySignatures is empty")
	}
	for _, sig := range cellAggregatorIdentitySignatures {
		sig := sig
		t.Run(string(sig.agg), func(t *testing.T) {
			want := manifestAggregatorOperatorKeys(t, string(sig.agg))
			got := append([]string(nil), sig.keys...)
			sort.Strings(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("restated signature for %s = %v, but the declared "+
					"ComponentSchema operator keys are %v — update "+
					"cellAggregatorIdentitySignatures in the same change as "+
					"internal/descriptor/capabilities_aggregators.go",
					sig.agg, got, want)
			}
		})
	}
}

// TestPairwiseCellAggregatorSignaturesCoverDistinctBearers is the other
// half of the parity contract: every aggregator whose declared
// ComponentSchema carries a key the distinct-n reader would consume
// ("distinct_count" / "cardinality") MUST have a signature row, or it
// classifies as unidentified — or, worse, the reader picks its key up
// off a cell that was never identified.
func TestPairwiseCellAggregatorSignaturesCoverDistinctBearers(t *testing.T) {
	listed := map[types.AggregationType]bool{}
	for _, sig := range cellAggregatorIdentitySignatures {
		listed[sig.agg] = true
	}
	for _, agg := range types.AllAggregationTypes() {
		keys := manifestAggregatorOperatorKeys(t, string(agg))
		bears := false
		for _, k := range keys {
			if k == "distinct_count" || k == "cardinality" {
				bears = true
				break
			}
		}
		if bears && !listed[agg] {
			t.Errorf("%s declares a distinct-bearing component key %v but has no "+
				"cellAggregatorIdentitySignatures row", agg, keys)
		}
		if !bears && listed[agg] {
			t.Errorf("%s has a signature row but declares no distinct-bearing "+
				"component key (%v) — the row is dead weight", agg, keys)
		}
	}
}

// TestPairwiseCellAggregatorIdentity_ExactKeySetOnly pins the
// subset-to-exact hardening.
//
// The classifier used to admit a cell whose keys merely CONTAINED a
// signature. An extension aggregator declaring {sum, distinct_count,
// ...} — a plausible shape for any custom keyed sum — therefore
// classified as AGG_DISTINCT_SUM, and its "distinct_count" was read as
// a distinct RESPONDENT count. That is a silently wrong sample size,
// the exact failure class n_within_distinct exists to remove.
//
// Known residual, deliberately not closed here: an extension declaring
// EXACTLY {sum, distinct_count} still classifies as AGG_DISTINCT_SUM.
// Distinguishing it needs per-registration provenance the components
// block does not carry.
func TestPairwiseCellAggregatorIdentity_ExactKeySetOnly(t *testing.T) {
	cases := []struct {
		name     string
		cell     map[string]any
		wantAgg  types.AggregationType
		wantOK   bool
		admitted bool
	}{
		{
			name:     "exact AGG_DISTINCT_SUM keys classify and admit",
			cell:     map[string]any{"n": 4, "n_null": 0, "sum": 80.0, "distinct_count": 2},
			wantAgg:  types.AGG_DISTINCT_SUM,
			wantOK:   true,
			admitted: true,
		},
		{
			// The hardening. A superset used to classify as
			// AGG_DISTINCT_SUM and be admitted.
			name:     "a SUPERSET of the AGG_DISTINCT_SUM keys is unidentified",
			cell:     map[string]any{"n": 4, "n_null": 0, "sum": 80.0, "distinct_count": 2, "weighted_sum": 9.0},
			wantOK:   false,
			admitted: false,
		},
		{
			name:     "a superset of the AGG_DISTINCT_COUNT keys is unidentified",
			cell:     map[string]any{"n": 4, "cardinality": 3, "hll_registers": 16},
			wantOK:   false,
			admitted: false,
		},
		{
			// Refusing a superset must not accidentally promote a
			// never-admitted aggregator either.
			name:    "a superset of the AGG_FREQUENCY keys is unidentified",
			cell:    map[string]any{"n": 4, "distinct_count": 9, "mode_value": "a", "mode_count": 3, "extra": 1},
			wantOK:  false,
			wantAgg: "",
		},
		{
			name:     "a SUBSET of the AGG_DISTINCT_SUM keys is unidentified",
			cell:     map[string]any{"n": 4, "distinct_count": 2},
			wantOK:   false,
			admitted: false,
		},
		{
			// The universal floor is stripped before comparison, so a
			// cell emitting only the operator keys still classifies.
			name:     "the universal floor is optional, not part of the signature",
			cell:     map[string]any{"sum": 80.0, "distinct_count": 2},
			wantAgg:  types.AGG_DISTINCT_SUM,
			wantOK:   true,
			admitted: true,
		},
		{
			name:    "AGG_FREQUENCY still classifies as itself and is never admitted",
			cell:    map[string]any{"n": 4, "n_null": 0, "distinct_count": 9, "mode_value": "a", "mode_count": 3},
			wantAgg: types.AGG_FREQUENCY,
			wantOK:  true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			host := newCrosstabHostViewWithComponents(
				&types.MatrixPayload{
					RowKeys:    []types.AxisKey{{"r"}},
					ColumnKeys: []types.AxisKey{{"c"}},
				},
				&types.CrosstabComponents{CellComponents: [][]map[string]any{{tc.cell}}},
			)
			gotAgg, gotOK := host.CellAggregatorIdentity()
			if gotOK != tc.wantOK {
				t.Fatalf("CellAggregatorIdentity ok = %v, want %v (agg %q)", gotOK, tc.wantOK, gotAgg)
			}
			if tc.wantOK && gotAgg != tc.wantAgg {
				t.Fatalf("CellAggregatorIdentity = %q, want %q", gotAgg, tc.wantAgg)
			}
			if _, _, admitted := host.AdmitsDistinctKeyN(); admitted != tc.admitted {
				t.Fatalf("AdmitsDistinctKeyN admitted = %v, want %v", admitted, tc.admitted)
			}
		})
	}
}
