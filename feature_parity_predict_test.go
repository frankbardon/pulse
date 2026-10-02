package pulse

// The predict rows of the hidden-name parity harness
// (feature_parity_harness_test.go). Predict is the no-execute layer:
// it never returns a coded error for a request fault, it reports the
// fault on the envelope with PredictResult.Valid false. So its outcome
// is the whole result (or envelope) and a never-registered name may
// "succeed" — but the hidden name must still predict byte-identically
// to it, and differently from the same name on an unprofiled instance.

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// predictNoErrorChannel is why a never-registered name may succeed at
// a predict entry point: predict reports every request fault on the
// envelope, never as a returned error, and some categories (an
// aggregator, a grouper, a filterer, an attribute, a regression) it
// does not judge by name at all.
const predictNoErrorChannel = "predict reports request faults on the envelope, never as a returned error"

// predictCrosstabVacuous names why a crosstab cell is vacuous at a
// predict entry point: predict judges a crosstab axis grouper or cell
// aggregator by name only where a crosstab rule applies to it (a
// numeric cell over a categorical field, normalize on a recompute or
// map-valued cell), and the harness probe trips none of them — so every
// name predicts the same. The routed rules are pinned by
// internal/descriptor's TestPredict_Hidden* tests instead.
var predictCrosstabVacuous = map[string]string{
	"crosstab_axis": "predict applies no name-keyed rule to a crosstab axis grouper",
	"crosstab_cell": "the probe's numeric cell trips no name-keyed crosstab rule in predict",
}

// predictParityEntryPoints drive the two public predict entry points
// over the category's Request slot.
var predictParityEntryPoints = []parityEntryPoint{
	{name: "Predict", neverOK: predictNoErrorChannel, vacuousOK: predictCrosstabVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		res, err := h.p.Predict(context.Background(), req)
		return parityOutcome(res, err), true
	}},
	{name: "PredictBytes", neverOK: predictNoErrorChannel, vacuousOK: predictCrosstabVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		data, err := afero.ReadFile(h.p.fsys, h.cohort)
		if err != nil {
			t.Fatalf("read %s: %v", h.cohort, err)
		}
		env, err := h.p.PredictBytes(context.Background(), data, req)
		return parityOutcome(env, err), true
	}},
}

// TestHiddenOperatorSharedRuleParity: the rules predict and the runtime
// share — zone resolution, the field-reference walk and the strict
// numeric-on-categorical refusal — read the instance, so Process
// refuses a hidden name exactly as a never-registered one at the
// shared rule's own site (not later, at the operator's).
func TestHiddenOperatorSharedRuleParity(t *testing.T) {
	fsys := parityFS(t)
	raw, err := os.ReadFile(featureSetFixtureDir + "minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := ParseFeatureProfile(raw)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := New(Options{FS: fsys, FeatureProfile: fp, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	open, err := New(Options{FS: fsys, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	base := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: parityCohort},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		}
	}
	cases := []struct {
		name, hidden, never string
		req                 func(op string) *types.Request
	}{
		{"zone-capable grouper with tz", "GROUP_DATE", "GROUP_NEVER_REGISTERED", func(op string) *types.Request {
			r := base()
			r.Groups = append(r.Groups, &types.Group{Type: types.GroupType(op), Field: "age", TimeZone: "UTC"})
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}}
			return r
		}},
		{"aggregation params naming a field", "AGG_RATIO", "AGG_NEVER_REGISTERED", func(op string) *types.Request {
			r := base()
			r.Aggregations = []*types.Aggregation{{Type: types.AggregationType(op), Field: "age", Label: "r",
				Params: json.RawMessage(`{"numerator_field":"nope","denominator_field":"age"}`)}}
			return r
		}},
		{"strict numeric aggregation on a categorical field", "AGG_AVERAGE", "AGG_NEVER_REGISTERED", func(op string) *types.Request {
			r := base()
			r.Aggregations = []*types.Aggregation{{Type: types.AggregationType(op), Field: "region", Label: "a"}}
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !scoped.svc.InstanceSnapshot().Hidden(tc.hidden) {
				t.Fatalf("minimal does not hide %s", tc.hidden)
			}
			// The zone refusal's message lists the zone-capable
			// operators the instance offers — not the slot's name —
			// so it is elided before substitution.
			zoneList := regexp.MustCompile(`only zone-capable operators \([^)]*\)`)
			run := func(p *Pulse, op string) string {
				resp, err := p.Process(context.Background(), tc.req(op))
				return zoneList.ReplaceAllString(string(parityOutcome(resp, err)), "only zone-capable operators (…)")
			}
			got, want, control := run(scoped, tc.hidden), run(scoped, tc.never), run(open, tc.hidden)
			if sub := strings.ReplaceAll(got, tc.hidden, tc.never); sub != want {
				t.Errorf("hidden %s diverges from never-registered %s\nhidden: %s\nnever:  %s", tc.hidden, tc.never, got, want)
			}
			if strings.ReplaceAll(control, tc.hidden, tc.never) == want {
				t.Errorf("vacuous: unscoped %s is refused like a never-registered name\ncontrol: %s", tc.hidden, control)
			}
		})
	}
}
