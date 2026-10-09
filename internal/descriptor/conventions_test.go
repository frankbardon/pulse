package descriptor

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// conventionFixture is testdata/conventions.json: effect-size
// conventions transcribed independently from their published sources.
type conventionFixture struct {
	VerifiedAt  string `json:"verified_at"`
	Conventions []struct {
		ID           string    `json:"id"`
		Citation     string    `json:"citation"`
		Statistics   []string  `json:"statistics"`
		Thresholds   []float64 `json:"thresholds"`
		Labels       []string  `json:"labels"`
		Abs          bool      `json:"abs"`
		SymmetricLog bool      `json:"symmetric_log"`
		Derivation   *struct {
			Kind   string    `json:"kind"`
			Inputs []float64 `json:"inputs"`
		} `json:"derivation"`
		Sources []struct {
			URL     string `json:"url"`
			Locator string `json:"locator"`
		} `json:"sources"`
	} `json:"conventions"`
	StatisticBindings map[string]string `json:"statistic_bindings"`
	Excluded          []struct {
		Operator string `json:"operator"`
		Field    string `json:"field"`
		Reason   string `json:"reason"`
	} `json:"excluded"`
}

func loadConventionFixture(t *testing.T) conventionFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/conventions.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var wrapper struct {
		Comment string `json:"_comment"`
		conventionFixture
	}
	if err := dec.Decode(&wrapper); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	fx := wrapper.conventionFixture
	if len(fx.Conventions) == 0 {
		t.Fatal("fixture declares no conventions")
	}
	return fx
}

// round2 rounds to two decimals, the precision the published benchmarks
// are quoted at.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// TestConventionRegistryMatchesFixture is the binding "second pair of
// eyes" on built-in effect-size bands (U08 PRD FR-1..FR-3): the
// registry in conventions.go must equal the independently transcribed
// fixture — IDs, citation, thresholds, labels, Abs and SymmetricLog —
// and every fixture entry must carry a source URL with a locator. A
// derived convention (eta squared from Cohen's f, odds ratio from
// Cohen's d) is recomputed from its stated inputs.
func TestConventionRegistryMatchesFixture(t *testing.T) {
	fx := loadConventionFixture(t)

	seen := map[string]bool{}
	for _, want := range fx.Conventions {
		if seen[want.ID] {
			t.Errorf("fixture declares %s twice", want.ID)
		}
		seen[want.ID] = true
		got, ok := builtinConventions[want.ID]
		if !ok {
			t.Errorf("fixture convention %s is not registered", want.ID)
			continue
		}
		if got.ID != want.ID {
			t.Errorf("%s: registry entry carries ID %q", want.ID, got.ID)
		}
		if got.Citation != want.Citation {
			t.Errorf("%s: citation %q, fixture %q", want.ID, got.Citation, want.Citation)
		}
		if !reflect.DeepEqual(got.Thresholds, want.Thresholds) {
			t.Errorf("%s: thresholds %v, fixture %v", want.ID, got.Thresholds, want.Thresholds)
		}
		if !reflect.DeepEqual(got.Labels, want.Labels) {
			t.Errorf("%s: labels %v, fixture %v", want.ID, got.Labels, want.Labels)
		}
		if !reflect.DeepEqual(got.Statistics, want.Statistics) {
			t.Errorf("%s: statistics %v, fixture %v", want.ID, got.Statistics, want.Statistics)
		}
		if got.Abs != want.Abs {
			t.Errorf("%s: Abs %v, fixture %v", want.ID, got.Abs, want.Abs)
		}
		if got.SymmetricLog != want.SymmetricLog {
			t.Errorf("%s: SymmetricLog %v, fixture %v", want.ID, got.SymmetricLog, want.SymmetricLog)
		}
		if len(want.Labels) != len(want.Thresholds)+1 {
			t.Errorf("%s: fixture has %d labels for %d thresholds", want.ID, len(want.Labels), len(want.Thresholds))
		}
		if len(want.Statistics) == 0 {
			t.Errorf("%s: fixture names no statistic", want.ID)
		}
		if len(want.Sources) == 0 {
			t.Errorf("%s: fixture cites no source", want.ID)
		}
		for i, s := range want.Sources {
			if !strings.HasPrefix(s.URL, "https://") || strings.TrimSpace(s.Locator) == "" {
				t.Errorf("%s: source %d needs an https URL and a page/section locator, got %+v", want.ID, i, s)
			}
		}
		if d := want.Derivation; d != nil {
			if len(d.Inputs) != len(want.Thresholds) {
				t.Errorf("%s: derivation has %d inputs for %d thresholds", want.ID, len(d.Inputs), len(want.Thresholds))
				continue
			}
			for i, in := range d.Inputs {
				var v float64
				switch d.Kind {
				case "eta2_from_f": // Cohen (1988): eta^2 = f^2 / (1 + f^2)
					v = in * in / (1 + in*in)
				case "or_from_d": // Chinn (2000): d = ln(OR) * sqrt(3) / pi
					v = math.Exp(in * math.Pi / math.Sqrt(3))
				case "r2_from_f2": // Cohen (1988): f^2 = R^2 / (1 - R^2)
					v = in / (1 + in)
				default:
					t.Fatalf("%s: unknown derivation kind %q", want.ID, d.Kind)
				}
				if round2(v) != want.Thresholds[i] {
					t.Errorf("%s: %s(%g) = %.4f rounds to %.2f, fixture threshold %g", want.ID, d.Kind, in, v, round2(v), want.Thresholds[i])
				}
			}
		}
	}
	for _, id := range conventionIDs() {
		if !seen[id] {
			t.Errorf("registered convention %s has no fixture entry", id)
		}
	}

	t.Run("bands_are_valid", func(t *testing.T) {
		for _, id := range conventionIDs() {
			for _, p := range bandProblems(builtinConventions[id].Bands()) {
				t.Errorf("%s: %s", id, p)
			}
		}
	})
	t.Run("keys_are_distinct", func(t *testing.T) {
		keys := map[string]string{}
		for _, id := range conventionIDs() {
			k := conventionKey(conventionCitation(id), conventionBands(id), builtinConventions[id].Abs)
			if prev, dup := keys[k]; dup {
				t.Errorf("%s and %s are indistinguishable on (Convention, Bands, Abs)", prev, id)
			}
			keys[k] = id
		}
	})
}

// conventionKey identifies what an Interpretation can say about a
// convention: its Convention text, its Bands and its Abs flag.
func conventionKey(citation string, bands []descriptor.Band, abs bool) string {
	b, _ := json.Marshal(struct {
		C string
		B []descriptor.Band
		A bool
	}{citation, bands, abs})
	return string(b)
}

// TestBuiltinBandsCiteRegisteredConvention binds every built-in
// Interpretation that declares Bands to exactly one registered
// convention (same Convention text, Bands and Abs) whose Statistics
// name the banded output — the effect-size key for
// details.effect_size.<key>, else the fixture's statistic_bindings
// entry for the operator — and holds the
// fixture's exclusions: Cramer's V (df-scaled rule), Kendall's tau and
// rank-biserial r carry no bands. Interpretations without Bands are
// unaffected, and extension guidance is never consulted.
func TestBuiltinBandsCiteRegisteredConvention(t *testing.T) {
	byKey := map[string][]string{}
	for _, id := range conventionIDs() {
		k := conventionKey(conventionCitation(id), conventionBands(id), builtinConventions[id].Abs)
		byKey[k] = append(byKey[k], id)
	}
	fx := loadConventionFixture(t)
	names := make([]string, 0, len(builtinInterpretations))
	for name := range builtinInterpretations {
		names = append(names, name)
	}
	sort.Strings(names)
	banded := 0
	for _, name := range names {
		for _, in := range builtinInterpretations[name] {
			if len(in.Bands) == 0 {
				continue
			}
			banded++
			ids := byKey[conventionKey(in.Convention, in.Bands, in.Abs)]
			if len(ids) != 1 {
				t.Errorf("%s %s: Bands %s / Convention %q / Abs %v match %d registered conventions %v, want exactly 1",
					name, in.Field, mustJSON(in.Bands), in.Convention, in.Abs, len(ids), ids)
				continue
			}
			stat, ok := strings.CutPrefix(in.Field, "details.effect_size.")
			if !ok {
				stat = fx.StatisticBindings[name]
			}
			if stat == "" {
				t.Errorf("%s %s: banded, but the fixture's statistic_bindings names no statistic for it", name, in.Field)
			} else if !slices.Contains(builtinConventions[ids[0]].Statistics, stat) {
				t.Errorf("%s %s: bands follow %s, which does not cover statistic %q (covers %v)",
					name, in.Field, ids[0], stat, builtinConventions[ids[0]].Statistics)
			}
		}
	}
	if banded == 0 {
		t.Error("no built-in Interpretation declares Bands; the binding check is vacuous")
	}

	for _, ex := range fx.Excluded {
		if strings.TrimSpace(ex.Reason) == "" {
			t.Errorf("exclusion %s %s has no reason", ex.Operator, ex.Field)
		}
		for _, in := range builtinInterpretations[ex.Operator] {
			if in.Field == ex.Field && (len(in.Bands) > 0 || in.Convention != "") {
				t.Errorf("%s %s: excluded from banding (%s), but declares Bands/Convention", ex.Operator, ex.Field, ex.Reason)
			}
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestBandOf(t *testing.T) {
	d := bandedBy(ConventionCohenD, descriptor.Interpretation{Field: "details.effect_size.cohens_d"})
	or := bandedBy(ConventionCohenOR, descriptor.Interpretation{Field: "statistic"})
	eta := bandedBy(ConventionCohenEta2, descriptor.Interpretation{Field: "details.effect_size.eta_squared"})
	for _, c := range []struct {
		name string
		in   descriptor.Interpretation
		v    float64
		want string
		ok   bool
	}{
		{"d small", d, 0.3, "small", true},
		{"d negative reads abs", d, -0.9, "large", true},
		{"d lower bound inclusive", d, 0.5, "medium", true},
		{"d very small", d, 0.05, "very small", true},
		{"or reciprocal", or, 0.1, "large", true},
		{"or above one", or, 2.0, "small", true},
		{"or zero", or, 0, "", false},
		{"eta not abs", eta, -0.2, "very small", true},
		{"nan", d, math.NaN(), "", false},
		{"inf", d, math.Inf(1), "", false},
		{"no bands", descriptor.Interpretation{Field: "statistic"}, 0.5, "", false},
		{"bands without convention", descriptor.Interpretation{Bands: d.Bands}, 0.5, "", false},
	} {
		got, ok := BandOf(c.in, c.v)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: BandOf(%v) = %q, %v; want %q, %v", c.name, c.v, got, ok, c.want, c.ok)
		}
	}
}
