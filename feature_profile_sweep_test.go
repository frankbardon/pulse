package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// profileSweepRequests are predict probes chosen to reach the refusal
// and suggestion text built in code: a clean request, a near-miss type
// name (did-you-mean), a categorical numeric aggregation (proposed
// replacements), a `tz` on a non-zone-capable grouper (the zone-capable
// list), the retired ATTR_RANK (its migration hint), and the pairwise
// Welford-kind param refusals (their alternative-kind advice).
func profileSweepRequests() map[string]*types.Request {
	region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	pairwise := func(params string) *types.Request {
		return &types.Request{Crosstab: &types.CrosstabSpec{
			Rows: region, Columns: region,
			Cell: &types.Aggregation{Type: types.AGG_WELFORD, Field: "age"},
		}, Overlays: []types.OverlaySpec{{
			Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow,
			Params: json.RawMessage(params),
		}}}
	}
	return map[string]*types.Request{
		"clean": {Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age"}}, Groups: region},
		"near_miss": {Aggregations: []*types.Aggregation{{Type: "AGG_SUMM", Field: "age"}},
			Groups: []*types.Group{{Type: "GROUP_CATEGORI", Field: "region"}}},
		"categorical_numeric": {Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "region"}}},
		"tz_not_capable": {Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age"}},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", TimeZone: "UTC"}}},
		"attr_rank":         {Attributes: []*types.Attribute{{Type: "ATTR_RANK", Field: "age"}}},
		"pairwise_n_source": pairwise(`{"n_source":"cell_n_unweighted"}`),
		"pairwise_p_source": pairwise(`{"p_source":"cell_value"}`),
		"pairwise_n_basis":  pairwise(`{"n_basis":"kish"}`),
	}
}

func parityCohortBytes(t *testing.T) []byte {
	t.Helper()
	data, err := afero.ReadFile(parityFS(t), parityCohort)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// authoredTokens is every word token in req's JSON: a name the caller
// wrote is echoed by a refusal on any instance (a never-registered name
// is too), so it is not a leak.
func authoredTokens(t *testing.T, req *types.Request) map[string]bool {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	s := string(b)
	for start, i := -1, 0; i <= len(s); i++ {
		w := i < len(s) && isWordByte(s[i])
		switch {
		case w && start < 0:
			start = i
		case !w && start >= 0:
			out[s[start:i]] = true
			start = -1
		}
	}
	return out
}

// TestProfileDefaultIsFull: an instance with no feature profile is the
// full registry on every self-description surface — manifest, payload
// schema, errors list / lookup / search — and on predict, byte for byte
// against the profile-free builders.
func TestProfileDefaultIsFull(t *testing.T) {
	p, err := New(Options{FS: parityFS(t)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	snap := p.svc.InstanceSnapshot()
	if h := snap.HiddenNames(); len(h) != 0 {
		t.Errorf("default instance hides %v", h)
	}
	full := descx.FeatureNames()
	slices.Sort(full)
	if !slices.Equal(snap.EnabledNames(), full) {
		t.Error("default enabled set is not the full feature table")
	}

	if errorsJSON(t, p.Manifest(context.Background())) != errorsJSON(t, descx.BuildManifest()) {
		t.Error("manifest differs from the full-registry BuildManifest")
	}
	got, err := p.PayloadSchema()
	if err != nil {
		t.Fatalf("PayloadSchema: %v", err)
	}
	if !bytes.Equal(got, descx.BuildPayloadSchema()) {
		t.Error("payload schema differs from the full-registry BuildPayloadSchema")
	}

	for _, c := range errors.AllCodes() {
		want, _ := errors.Lookup(string(c))
		if r, ok := p.ErrorLookup(string(c)); !ok || !reflect.DeepEqual(r, want) {
			t.Errorf("ErrorLookup(%s) differs from the registry", c)
		}
	}
	for _, d := range errors.AllDomains() {
		if errorsJSON(t, p.ErrorsByDomain(d)) != errorsJSON(t, errors.ByDomain(d)) {
			t.Errorf("ErrorsByDomain(%s) differs from the registry", d)
		}
	}
	for _, q := range []string{"overlay", "AGG_WELFORD", "GROUP_DATE", "zone", "spss", "crosstab"} {
		if errorsJSON(t, p.ErrorsSearch(q)) != errorsJSON(t, errors.Search(q)) {
			t.Errorf("ErrorsSearch(%q) differs from the registry", q)
		}
	}

	data := parityCohortBytes(t)
	for name, req := range profileSweepRequests() {
		env, err := p.PredictBytes(context.Background(), data, req)
		if err != nil {
			t.Fatalf("%s: PredictBytes: %v", name, err)
		}
		want := descx.Predict(bytes.NewReader(data), req, nil)
		if g, w := errorsJSON(t, env), errorsJSON(t, want); g != w {
			t.Errorf("%s: predict differs from the full-registry predict\ngot:  %s\nwant: %s", name, g, w)
		}
	}
}

const (
	sweepLabelTable = "sweep_label_tbl"
	sweepRangeTable = "sweep_range_tbl"
)

// sweepNamedTables registers one label table and one range table so the
// sweep can prove a hidden capability's table names never surface.
func sweepNamedTables() Extensions {
	start := "2024-01-01"
	return Extensions{
		LabelTables: map[string]LabelTable{sweepLabelTable: {Rows: map[string]string{"1": "one"}}},
		RangeTables: map[string]RangeTable{sweepRangeTable: {Ranges: []DateRangeSpec{{Label: "all", Start: &start}}}},
	}
}

// TestProfileHiddenNameSweep: for every fixture profile, no hidden
// operator name, hidden-feature MCP tool or hidden-capability named
// table appears on any instance
// self-description surface — the manifest (outside the U10-owned
// skills and examples sections), the payload schema, the error list,
// lookup and search — nor in any predict refusal or suggestion beyond
// the names the request itself authored.
func TestProfileHiddenNameSweep(t *testing.T) {
	data := parityCohortBytes(t)
	for _, name := range featureSetFixtures {
		t.Run(name, func(t *testing.T) {
			p := newFixturePulse(t, name, Options{Extensions: sweepNamedTables()})
			hidden := errorsHiddenTokens(p)
			if len(hidden) == 0 {
				t.Fatal("fixture hides no named feature: vacuous")
			}
			// A named table rides its capability: hidden capability,
			// hidden table names.
			inst := p.svc.InstanceSnapshot()
			for feat, tbl := range map[string]string{
				"capability:labels":       sweepLabelTable,
				"capability:range_tables": sweepRangeTable,
			} {
				if !inst.Enabled(feat) {
					hidden[tbl] = true
				}
			}
			check := func(surface, text string, allowed map[string]bool) {
				t.Helper()
				h := hidden
				if len(allowed) > 0 {
					h = map[string]bool{}
					for k := range hidden {
						if !allowed[k] {
							h[k] = true
						}
					}
				}
				if tok := namesHiddenToken(text, h); tok != "" {
					t.Errorf("%s names hidden %s", surface, tok)
				}
			}

			// Manifest: skills and examples are U10's (they still name
			// every built-in); everything else, error lists included.
			var m map[string]any
			if err := json.Unmarshal([]byte(errorsJSON(t, p.Manifest(context.Background()))), &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"skills", "examples_count", "example_categories", "example_tags"} {
				delete(m, k)
			}
			check("manifest", errorsJSON(t, m), nil)

			schema, err := p.PayloadSchema()
			if err != nil {
				t.Fatalf("PayloadSchema: %v", err)
			}
			check("payload schema", string(schema), nil)

			for _, c := range errors.AllCodes() {
				if r, ok := p.ErrorLookup(string(c)); ok {
					check("ErrorLookup("+string(c)+")", errorsJSON(t, r), nil)
				}
			}
			for _, d := range errors.AllDomains() {
				check("ErrorsByDomain("+d+")", errorsJSON(t, p.ErrorsByDomain(d)), nil)
			}
			queries := []string{"overlay", "aggregat", "group", "filter", "zone", "test", "regression"}
			for tok := range hidden {
				queries = append(queries, tok)
			}
			for _, q := range queries {
				check("ErrorsSearch("+q+")", errorsJSON(t, p.ErrorsSearch(q)), nil)
			}

			for rname, req := range profileSweepRequests() {
				env, err := p.PredictBytes(context.Background(), data, req)
				if err != nil {
					t.Fatalf("%s: PredictBytes: %v", rname, err)
				}
				check("predict "+rname, errorsJSON(t, env), authoredTokens(t, req))
			}
		})
	}
}

// multiplicitySlotTokens are capability:multiplicity's wire tokens,
// spelled here independently of the scrub's table so dropping them
// from hiddenProseNames fails TestProfileHiddenSlotTokenSweep.
var multiplicitySlotTokens = map[string]bool{"multiplicity": true, "p_adjusted": true, "significant_adjusted": true}

// slotTokenLeaks returns each sentence (split at ". " and newlines) of
// text naming a token, skipping a sentence about pulse_lookup's
// same-spelled duplicate-key mode (it names `assert_unique`).
func slotTokenLeaks(text string, tokens map[string]bool) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, sentence := range strings.Split(line, ". ") {
			if namesHiddenToken(sentence, tokens) != "" && namesHiddenToken(sentence, map[string]bool{"assert_unique": true}) == "" {
				out = append(out, sentence)
			}
		}
	}
	return out
}

// TestProfileHiddenSlotTokenSweep: an instance offering everything but
// capability:multiplicity serves no prose naming `multiplicity`,
// `p_adjusted` or `significant_adjusted` — not in the manifest (outside
// the skills and examples sections), the skill metadata, the generated
// Use when / Reading the output sections, nor the Purpose /
// Interpretation prose of any operator it offers (and the shared
// p-value rule set) once its scrub runs, as docgen serves it. The
// unscrubbed guidance names them (non-vacuous).
func TestProfileHiddenSlotTokenSweep(t *testing.T) {
	var features []string
	for _, n := range descx.FeatureNames() {
		if n != descx.FeatureMultiplicity {
			features = append(features, n)
		}
	}
	p, err := New(Options{FS: parityFS(t), FeatureProfile: &FeatureProfile{Features: features}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inst := p.svc.InstanceSnapshot()
	scrub := descx.NewProseScrub(inst)
	keep := func(use string) bool { return inst.Enabled(use) }

	var sections, guidance, raw strings.Builder
	addProse := func(v any) {
		for _, s := range descx.CollectProse(v) {
			raw.WriteString(s + "\n")
			guidance.WriteString(scrub.Text(s) + "\n")
		}
	}
	for _, op := range features {
		if strings.Contains(op, ":") {
			continue
		}
		for _, sec := range []string{skills.SectionUseWhen, skills.SectionReadingTheOutput} {
			sections.WriteString(descx.RenderGuidanceSection(op, sec, nil, keep, scrub) + "\n")
		}
		if pu, ok := descx.PurposeOf(op); ok {
			addProse(pu)
		}
		if ins, ok := descx.InterpretationsOf(op); ok {
			addProse(ins)
		}
	}
	if pv, ok := descx.SharedInterpretation(descx.SharedPValue); ok {
		addProse(pv)
	}
	if len(slotTokenLeaks(raw.String(), multiplicitySlotTokens)) == 0 {
		t.Fatal("unscrubbed guidance names no multiplicity token: the sweep is vacuous")
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(errorsJSON(t, p.Manifest(context.Background()))), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"skills", "examples_count", "example_categories", "example_tags"} {
		delete(m, k)
	}
	surfaces := map[string]string{
		"manifest":          errorsJSON(t, m),
		"skill metadata":    errorsJSON(t, p.Skills()),
		"guidance sections": sections.String(),
		"guidance prose":    guidance.String(),
	}
	for name, text := range surfaces {
		for _, s := range slotTokenLeaks(text, multiplicitySlotTokens) {
			t.Errorf("%s names a hidden multiplicity token: %q", name, s)
		}
	}
}
