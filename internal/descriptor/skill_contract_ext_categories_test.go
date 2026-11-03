package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestSkillContract_ExtensionOperatorsEveryCategory extends
// TestSkillContract_ExtensionOperators past aggregators and tests: an
// extension grouper, filterer, feature, window, weight-aware test,
// matrix operator (co-moment and buffered) and regression (declared and
// bare) each render the `## Contract` its registration implies — the
// category's weight rule, its streaming tier, a matrix's member types,
// p-value key and component keys, a regression's slot types and its
// own-block multiplicity.
func TestSkillContract_ExtensionOperatorsEveryCategory(t *testing.T) {
	t.Parallel()

	ext := &ExtensionsSnapshot{
		Groupers:  []descriptor.OperatorMeta{{Name: "GROUP_ACME_BAND", Streamable: true, Mergeable: true, Accepts: []string{"u8"}}},
		Filterers: []descriptor.OperatorMeta{{Name: "FILTER_ACME_KEEP", Streamable: true}},
		Features:  []descriptor.OperatorMeta{{Name: "FEAT_ACME_LOG", Streamable: true}},
		Windows:   []descriptor.OperatorMeta{{Name: "WIN_ACME_LAG"}},
		Tests:     []descriptor.OperatorMeta{{Name: "TEST_ACME_AWARE", WeightAware: true}},
		MatrixOps: []descriptor.OperatorMeta{{Name: "MAT_ACME_COV", Streamable: true, WeightAware: true}, {Name: "MAT_ACME_BUF"}},
		MatrixDetails: map[string]ExtensionMatrixOp{
			"MAT_ACME_COV": {Accepts: []encoding.FieldType{encoding.FieldTypeU8, encoding.FieldTypeF64}, PValues: "p"},
		},
		Regressions:       []descriptor.OperatorMeta{{Name: "REG_ACME_FIT", Streamable: true}, {Name: "REG_ACME_BARE"}},
		RegressionDetails: map[string]ExtensionRegression{"REG_ACME_FIT": {PValues: true, Accepts: []encoding.FieldType{encoding.FieldTypeF64}}},
		ComponentSchemas: map[string]descriptor.ComponentSchema{
			"MAT_ACME_COV":    {Mergeability: types.ComponentsMergeable, Keys: []descriptor.ComponentKey{{Name: "acme_n"}}},
			"GROUP_ACME_BAND": {Mergeability: types.ComponentsPartial, Keys: []descriptor.ComponentKey{{Name: "bands"}}},
		},
	}
	const notRead = "- Weight: not read; a weight in force is skipped."
	const notAware = "- Weight: not weight-aware; any weight in force, an instance default included, is `PULSE_EXTENSION_NOT_WEIGHT_AWARE`"
	for _, c := range []struct {
		op   string
		want []string
		not  []string
	}{
		{"GROUP_ACME_BAND", []string{"- Types: `u8`.", notRead, "- Streaming: streamable; partial states merge; components `partial`.", "- Components: `bands`."}, []string{"- Multiplicity:"}},
		{"FILTER_ACME_KEEP", []string{notRead, "- Streaming: streamable."}, []string{"partial states", "- Types"}},
		{"FEAT_ACME_LOG", []string{notRead, "- Streaming: streamable."}, []string{"partial states"}},
		{"WIN_ACME_LAG", []string{notRead, "- Streaming: buffered only."}, []string{"partial states"}},
		{"TEST_ACME_AWARE", []string{"- Weight: weighted under `frequency` and `probability` weights.", "- Multiplicity: a `multiplicity` method adds `p_adjusted`"}, []string{notAware}},
		{"MAT_ACME_COV", []string{
			"- Member types: `f64`, `u8`; any other is `PULSE_VECTOR_MEMBER_TYPE`.",
			"- Weight: weighted under `frequency` and `probability` weights.",
			"beside `p`; families `request`, `matrix` (default).",
			"- Streaming: streamable; partial states merge; components `mergeable`.",
			"`n_listwise_dropped`, `acme_n`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.",
		}, nil},
		{"MAT_ACME_BUF", []string{notAware, "- Streaming: buffered only; partial states do not merge"}, []string{"- Member types", "- Multiplicity:", "sum_weights"}},
		{"REG_ACME_FIT", []string{
			"- Types (`target`, `predictor`): `f64`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.",
			notAware,
			"- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted`",
			"- Streaming: streamable.",
		}, nil},
		{"REG_ACME_BARE", []string{notAware, "- Streaming: buffered only."}, []string{"- Types", "- Multiplicity:"}},
	} {
		got := renderContractSection(c.op, ext, fullContractVisibility)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: lacks %q:\n%s", c.op, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: carries %q:\n%s", c.op, n, got)
			}
		}
	}
	if got := renderContractSection("GROUP_ACME_ABSENT", ext, fullContractVisibility); got != "" {
		t.Errorf("unregistered name rendered %q", got)
	}
	if got := renderContractSection("GROUP_ACME_BAND", nil, fullContractVisibility); got != "" {
		t.Errorf("nil snapshot rendered %q", got)
	}
}
