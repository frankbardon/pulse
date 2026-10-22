package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// contractPilots are the op skills carrying `<!-- generated: contract -->`
// — one per op family — with facts each served `## Contract` must state.
var contractPilots = []struct {
	stem, op string
	want     []string
}{
	{"op-agg-sum", "AGG_SUM", []string{
		"- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.",
		"- Weight: weighted under `frequency` and `probability` weights.",
		"- Streaming: streamable; components `mergeable`.",
		"- Components: `n`, `n_null`, `sum`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.",
	}},
	{"op-attr-zscore", "ATTR_ZSCORE", []string{"- Types (`field`): ", "`PULSE_FIELD_TYPE_UNSUPPORTED`", "- Weight: weighted under"}},
	{"op-feat-log", "FEAT_LOG", []string{"- Types (`field`): `date`, `datetime`, `decimal128`, ", "- Weight: not read; a weight in force is skipped.", "- Streaming: streamable."}},
	{"op-filter-range", "FILTER_RANGE", []string{"- Components: `n_in`, `n_out`, `n_null_input`.", "- Weight: not read"}},
	{"op-group-category", "GROUP_CATEGORY", []string{"- Types (`field`): ", "- Components: `total_n`, `n_null`"}},
	{"op-mat-correlation", "MAT_CORRELATION", []string{
		"- Member types (`vector`, `fields`): ", "`PULSE_VECTOR_MEMBER_TYPE`",
		"- Multiplicity: a spec `multiplicity` block (implying `params.inference`; an inherited method reaches only a spec under `params.inference`) adds `p_adjusted` and `significant_adjusted` beside `p`; families `request`, `matrix` (default).",
		"partial states merge",
	}},
	{"op-overlay-t-cell", "OVERLAY_T_CELL", []string{
		"- Multiplicity: a `multiplicity` method adds `p_adjusted` and `significant_adjusted` beside the raw p; families `layer` (default)",
		"- Streaming: buffered only.",
	}},
	{"op-reg-ols", "REG_OLS", []string{"- Types (`target`, `predictor`): ", "per-coefficient `p_adjusted`", "`compose` (inside Compose)"}},
	{"op-synth-normal", "normal", []string{"- Applies to: `numeric` fields."}},
	{"op-test-welch", "TEST_WELCH", []string{
		"any other scalar type is `PULSE_TEST_FIELD_NOT_NUMERIC`",
		"- Types (`split_by`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.",
		"families `request` (default), `compose` (inside Compose).",
	}},
	{"op-win-lag", "WIN_LAG", []string{"- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.", "- Streaming: buffered only."}},
}

// TestSkillContract_Pilots: each pilot carries the marker once, alone
// on a line just before `## Gotchas`, and the served body (skills.Get)
// carries the rendered section in its place, stating the family's
// facts.
func TestSkillContract_Pilots(t *testing.T) {
	families := map[string]bool{}
	for _, p := range contractPilots {
		raw, ok := skills.Raw(p.stem)
		if !ok {
			t.Fatalf("%s: no raw body", p.stem)
		}
		if op := skills.ParseFrontmatter(raw)["operator"]; op != p.op {
			t.Fatalf("%s: operator %q, want %q", p.stem, op, p.op)
		}
		marker := skills.GeneratedMarker(skills.SectionContract)
		if strings.Count(raw, marker) != 1 || !strings.Contains(raw, "\n"+marker+"\n\n## Gotchas\n") {
			t.Errorf("%s: want one %s line just before ## Gotchas", p.stem, marker)
		}
		served, _ := skills.Get(p.stem)
		r := RenderContractSection(p.op)
		if r == "" || !strings.Contains(served, r+"\n\n## Gotchas\n") {
			t.Errorf("%s: served body lacks the rendered contract before ## Gotchas:\n%s", p.stem, served)
		}
		if strings.Contains(served, "<!-- generated") {
			t.Errorf("%s: a marker survived the render", p.stem)
		}
		for _, w := range p.want {
			if !strings.Contains(r, w) {
				t.Errorf("%s: contract lacks %q:\n%s", p.stem, w, r)
			}
		}
		families[strings.SplitN(strings.TrimPrefix(p.stem, "op-"), "-", 2)[0]] = true
	}
	for _, stem := range skills.Names() {
		if fam, ok := strings.CutPrefix(stem, "op-"); ok && !families[strings.SplitN(fam, "-", 2)[0]] {
			t.Errorf("op family of %s has no contract pilot", stem)
		}
	}
}

// TestSkillContract_FullRegistryRenders: every built-in operator,
// kind and distribution renders a non-empty section, deterministically
// (two renders and two index builds agree), with a lead heading and
// one fact per line.
func TestSkillContract_FullRegistryRenders(t *testing.T) {
	again := buildContractIndex()
	names := make([]string, 0, len(contractIndex()))
	for n := range contractIndex() {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("empty contract index")
	}
	for _, n := range names {
		a, b := RenderContractSection(n), RenderContractSection(n)
		if a == "" || a != b {
			t.Errorf("%s: render empty or not deterministic", n)
		}
		if c := renderContractSection(n, nil, fullContractVisibility); c != a {
			t.Errorf("%s: full visibility render differs", n)
		}
		f := again[n]
		if got := renderFacts(f, fullContractVisibility); got != a {
			t.Errorf("%s: a rebuilt index renders differently", n)
		}
		if !strings.HasPrefix(a, "## Contract\n\n- ") {
			t.Errorf("%s: section does not open with the heading and a fact:\n%s", n, a)
		}
		for _, line := range strings.Split(strings.TrimPrefix(a, "## Contract\n\n"), "\n") {
			if !strings.HasPrefix(line, "- ") || !strings.HasSuffix(line, ".") {
				t.Errorf("%s: malformed line %q", n, line)
			}
		}
	}
	if RenderContractSection("AGG_NO_SUCH") != "" {
		t.Error("an unknown operator rendered a contract")
	}
}

// renderFacts renders f as renderContractSection would for an indexed
// name.
func renderFacts(f contractFacts, v contractVisibility) string {
	const probe = "\x00contract-probe"
	contractIndex()[probe] = f
	defer delete(contractIndex(), probe)
	return renderContractSection(probe, nil, v)
}

// TestSkillContract_TypesMatchFieldAccept: a tabled slot's line names
// exactly the codes the shared table raises and admits exactly its
// accepted types (both sides refuse from internal/fieldaccept).
func TestSkillContract_TypesMatchFieldAccept(t *testing.T) {
	cases := []struct{ op, want string }{
		{"AGG_MEDIAN", "- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`."},
		{"AGG_SET_UNION", "- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`."},
		{"AGG_RATIO", "- Types (`numerator_field`, `denominator_field`): "},
		{"WIN_ROW_NUMBER", "- Types: reads no field; naming one is `PULSE_WINDOW_INVALID`."},
		{"GROUP_DATE", "- Types: `date`, `datetime`."},
		{"AGG_COUNT", "- Types: any type."},
		{"TEST_CHISQ", "- Types (`rows`, `cols`): `categorical_*`, `packed_bool`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`."},
		{"TEST_PAIRED_T", "- Types (`field2`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_TEST_FIELD2_NOT_NUMERIC`."},
		{"AGG_VEC_MEAN", "- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: \"binary\"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`."},
	}
	for _, c := range cases {
		if got := RenderContractSection(c.op); !strings.Contains(got, c.want) {
			t.Errorf("%s: want line %q in:\n%s", c.op, c.want, got)
		}
	}
	// A slot with no tabled admission never invents a code.
	if got := RenderContractSection("GROUP_DATE"); strings.Contains(got, "PULSE_FIELD_TYPE_UNSUPPORTED") {
		t.Errorf("untabled slot rendered a refusal code:\n%s", got)
	}
}

// TestSkillContract_MemberTypesMatchVectorRule: the member line is the
// vector rule's own set, and every built-in matrix declaration agrees
// with it.
func TestSkillContract_MemberTypesMatchVectorRule(t *testing.T) {
	var rule []string
	for _, ft := range knownScalarTypes {
		if vectors.MemberTypeAllowed(ft, types.VectorCoerceBinary) {
			rule = append(rule, ft.String())
		}
	}
	sort.Strings(rule)
	if got := vectorMemberTypes().admit; got != renderTypeList(rule) {
		t.Errorf("member line %q, rule %v", got, rule)
	}
	for _, m := range matrixCapabilities() {
		if !slices.Equal(m.AcceptsTypes, rule) {
			t.Errorf("%s accepts_types %v, vector rule %v", m.Name, m.AcceptsTypes, rule)
		}
	}
}

// contractRequest builds a one-slot request for the indexed operator
// op of category (false for a category with no request slot).
func contractRequest(op, category string) (*types.Request, string, bool) {
	req := &types.Request{}
	switch category {
	case "aggregator":
		req.Aggregations = []*types.Aggregation{{Type: types.AggregationType(op), Field: "x"}}
		return req, "aggregations[0]", true
	case "attribute":
		req.Attributes = []*types.Attribute{{Type: types.AttributeType(op), Field: "x"}}
		return req, "attributes[0]", true
	case "grouper":
		req.Groups = []*types.Group{{Type: types.GroupType(op), Field: "x"}}
		return req, "groups[0]", true
	case "window":
		req.Windows = []*types.Window{{Type: types.WindowType(op), Field: "x"}}
		return req, "windows[0]", true
	case "test":
		req.Tests = []*types.Test{{Type: types.TestType(op), Field: "x"}}
		return req, "tests[0]", true
	case "post_test":
		req.PostTests = []*types.Test{{Type: types.TestType(op), Field: "x"}}
		return req, "post_tests[0]", true
	case "regression":
		req.Regressions = []*types.RegressionSpec{{Type: types.RegressionType(op), Target: "y", Predictors: []string{"x"}}}
		return req, "regressions[0]", true
	case "matrix":
		req.Matrices = []types.MatrixSpec{{Type: types.MatrixType(op), Params: json.RawMessage(`{"inference":true}`)}}
		return req, "matrices[0]", true
	case "overlay":
		req.Overlays = []types.OverlaySpec{{Kind: types.OverlayKind(op)}}
		return req, "overlays[0]", true
	}
	return nil, "", false
}

// TestSkillContract_WeightMatchesResolver is the predict-parity pin of
// the Weight line: for every built-in operator with a request slot,
// ResolveWeights under a request frequency weight, a request
// probability weight and an instance default classes the slot exactly
// as the rendered contract says.
func TestSkillContract_WeightMatchesResolver(t *testing.T) {
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	prob := &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	outcome := func(req *types.Request, slot string, reqW, def *types.WeightSpec) string {
		r := *req
		r.Weight = reqW
		got, err := ResolveWeights(&r, nil, def, nil)
		if err != nil {
			var ce *errors.CodedError
			if stderrors.As(err, &ce) {
				return string(ce.Code)
			}
			return err.Error()
		}
		for _, rw := range got {
			if rw.Slot == slot {
				return string(rw.Status)
			}
		}
		return "absent"
	}
	checked := 0
	for name, f := range contractIndex() {
		req, slot, ok := contractRequest(name, f.category)
		if !ok {
			if f.category != "synth" && f.weight.class != weighting.ClassNone {
				t.Errorf("%s (%s): no request slot, yet class %v", name, f.category, f.weight.class)
			}
			continue
		}
		checked++
		gotF, gotP, gotD := outcome(req, slot, freq, nil), outcome(req, slot, prob, nil), outcome(req, slot, nil, freq)
		var wantF, wantP, wantD string
		applied, skipped := "applied", "skipped_not_weight_aware"
		unsup, cfg := string(errors.PULSE_WEIGHT_UNSUPPORTED), string(errors.PROCESSING_CONFIG)
		switch f.weight.class {
		case weighting.ClassAware:
			wantF, wantP, wantD = applied, applied, applied
		case weighting.ClassFrequencyOnly:
			wantF, wantP, wantD = applied, unsup, applied
		case weighting.ClassRefuse:
			wantF, wantP, wantD = unsup, unsup, unsup
		case weighting.ClassNotWeightable:
			wantF, wantP, wantD = cfg, cfg, skipped
		default:
			wantF, wantP, wantD = skipped, skipped, skipped
		}
		if gotF != wantF || gotP != wantP || gotD != wantD {
			t.Errorf("%s (%s): resolver frequency/probability/default = %s/%s/%s, contract class %v says %s/%s/%s\n%s",
				name, f.category, gotF, gotP, gotD, f.weight.class, wantF, wantP, wantD, RenderContractSection(name))
		}
	}
	if checked < 100 {
		t.Fatalf("only %d operators checked", checked)
	}
}

// TestSkillContract_MultiplicityMatchesResolver pins the Multiplicity
// line against the resolver: under an instance default method, the
// slot joins a family exactly when the contract names the outputs; the
// Tukey line exactly when the resolver skips a test; no line where the
// resolver refuses (REG_BAYES_LINEAR) or the operator has no
// multiplicity slot.
func TestSkillContract_MultiplicityMatchesResolver(t *testing.T) {
	def := &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	for name, f := range contractIndex() {
		r := RenderContractSection(name)
		hasLine := strings.Contains(r, "- Multiplicity: ")
		joins := hasLine && !strings.Contains(r, "already corrected")
		req, _, ok := contractRequest(name, f.category)
		if ok && f.category == "regression" {
			// A regression joins only through its own block.
			req.Regressions[0].Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
		}
		member, resolved := false, false
		if ok {
			plan, err := ResolveMultiplicity(req, def, nil)
			if err == nil && plan != nil {
				for _, list := range plan.lists() {
					for _, rm := range list {
						resolved = true
						member = member || rm.Member
					}
				}
			}
		}
		switch {
		case member != joins:
			t.Errorf("%s (%s): resolver member=%v, contract joins=%v:\n%s", name, f.category, member, joins, r)
		case hasLine && !joins && (!resolved || member):
			t.Errorf("%s: non-joining multiplicity line without a resolver skip:\n%s", name, r)
		}
	}
	if r := RenderContractSection("TEST_TUKEY_HSD"); !strings.Contains(r, "already corrected") {
		t.Errorf("TEST_TUKEY_HSD lacks the already-corrected line:\n%s", r)
	}
}

// contractBody is a minimal atomic body carrying the contract marker.
func contractBody(op string) string {
	return "---\nname: op-x\noperator: " + op + "\n---\n\nLead.\n\n<!-- generated: contract -->\n\n## Gotchas\n\nNone.\n"
}

// TestSkillContract_ProfileAware: on a feature-profiled instance the
// contract names no hidden feature — no Weight line (and the unweighted
// components) with capability:weighting hidden, no Multiplicity line
// with capability:multiplicity hidden, no `compose` family with
// capability:compose hidden, no hidden weighted twin — while the
// full-registry render carries each.
func TestSkillContract_ProfileAware(t *testing.T) {
	render := func(op string, hide ...string) string {
		return hidingSnapshot(hide...).Discovery().renderGenerated(contractBody(op))
	}
	cases := []struct {
		op, hide string
		gone     []string
		kept     []string
	}{
		{"AGG_SUM", "capability:weighting", []string{"- Weight:", "sum_weights", "n_eff", "n_weight_invalid"}, []string{"- Components: `n`, `n_null`, `sum`.", "- Types (`field`)"}},
		{"MAT_CORRELATION", "capability:weighting", []string{"- Weight:", "sum_weights"}, []string{"- Multiplicity:"}},
		{"TEST_WELCH", "capability:multiplicity", []string{"- Multiplicity:", "p_adjusted"}, []string{"- Weight:"}},
		{"TEST_WELCH", "capability:compose", []string{"compose"}, []string{"- Multiplicity: ", "`request` (default)."}},
		{"OVERLAY_PAIRWISE_TWO_MEANS_Z", "OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z", []string{"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z"}, []string{"- Weight: refused"}},
	}
	for _, c := range cases {
		full := skills.RenderFull(contractBody(c.op))
		got := render(c.op, c.hide)
		if strings.Contains(got, "<!-- generated") || !strings.Contains(got, "## Contract") {
			t.Errorf("%s hiding %s: section not rendered:\n%s", c.op, c.hide, got)
		}
		for _, g := range c.gone {
			if !strings.Contains(full, g) {
				t.Errorf("%s: full render lacks %q:\n%s", c.op, g, full)
			}
			if strings.Contains(got, g) {
				t.Errorf("%s hiding %s: %q rendered:\n%s", c.op, c.hide, g, got)
			}
		}
		for _, k := range c.kept {
			if !strings.Contains(got, k) {
				t.Errorf("%s hiding %s: lost %q:\n%s", c.op, c.hide, k, got)
			}
		}
	}
	// A profile hiding nothing contract-relevant renders the full text.
	if got, full := render("AGG_SUM", "TEST_WELCH"), skills.RenderFull(contractBody("AGG_SUM")); got != full {
		t.Errorf("unrelated hide changed the contract:\n%s\nvs\n%s", got, full)
	}
}

// TestSkillContract_ExtensionOperators: an extension operator renders
// from its registration (accepts, WeightAware, Streamable / Mergeable,
// ComponentSchema), on the instance's Discovery only.
func TestSkillContract_ExtensionOperators(t *testing.T) {
	ext := &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{
			{Name: "AGG_ACME_AWARE", Streamable: true, Mergeable: true, WeightAware: true, Accepts: []string{"f64"}},
			{Name: "AGG_ACME_PLAIN", Streamable: false},
		},
		Tests: []descriptor.OperatorMeta{{Name: "TEST_ACME_GAP"}},
		ComponentSchemas: map[string]descriptor.ComponentSchema{
			"AGG_ACME_AWARE": {Mergeability: types.ComponentsMergeable, Keys: []descriptor.ComponentKey{{Name: "n"}, {Name: "acme_total"}, {Name: "acme_hint", Optional: true}}},
		},
	}
	for _, c := range []struct {
		op   string
		want []string
		not  []string
	}{
		{"AGG_ACME_AWARE", []string{
			"- Types: `f64`.",
			"- Weight: weighted under `frequency` and `probability` weights.",
			"- Streaming: streamable; partial states merge; components `mergeable`.",
			"- Components: `n`, `acme_total`; optional `acme_hint`.",
		}, []string{"- Multiplicity:"}},
		{"AGG_ACME_PLAIN", []string{
			"- Weight: not weight-aware; an explicit weight is `PULSE_EXTENSION_NOT_WEIGHT_AWARE`, an instance default is skipped.",
			"- Streaming: buffered only; partial states do not merge.",
		}, []string{"- Types", "- Components"}},
		{"TEST_ACME_GAP", []string{
			"any weight in force, an instance default included, is `PULSE_EXTENSION_NOT_WEIGHT_AWARE`",
			"- Multiplicity: a `multiplicity` method adds `p_adjusted`",
		}, nil},
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
		if RenderContractSection(c.op) != "" {
			t.Errorf("%s: the full-registry render knows an extension operator", c.op)
		}
	}
}

// TestSkillContract_EveryOpSkillCarriesMarker: every built-in op skill
// carries the contract marker once, alone on its line, directly before
// `## Gotchas`, and its served body renders a non-empty `## Contract`
// from its frontmatter operator (E2-S2: no hand-written type, weight,
// multiplicity, streaming or component boilerplate remains to drift).
func TestSkillContract_EveryOpSkillCarriesMarker(t *testing.T) {
	marker := skills.GeneratedMarker(skills.SectionContract)
	n := 0
	for _, stem := range skills.Names() {
		if !strings.HasPrefix(stem, "op-") {
			continue
		}
		n++
		raw, _ := skills.Raw(stem)
		if op := skills.ParseFrontmatter(raw)["operator"]; RenderContractSection(op) == "" {
			// A REG spec modifier (REG_RESAMPLE / REG_SELECTION) is no
			// registered operator: no registry declares facts to render.
			if strings.Contains(raw, marker) {
				t.Errorf("%s: carries the marker but %q has no contract to render", stem, op)
			}
			continue
		}
		if strings.Count(raw, marker) != 1 || !strings.Contains(raw, "\n\n"+marker+"\n\n## Gotchas\n") {
			t.Errorf("%s: want exactly one %s line, blank lines either side, just before ## Gotchas", stem, marker)
			continue
		}
		op := skills.ParseFrontmatter(raw)["operator"]
		r := RenderContractSection(op)
		served, _ := skills.Get(stem)
		if r == "" || !strings.Contains(served, r+"\n\n## Gotchas\n") {
			t.Errorf("%s: served body lacks the rendered contract of %q", stem, op)
		}
	}
	if n == 0 {
		t.Fatal("no op skills walked")
	}
}
