package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func multBlock(method types.MultiplicityMethod, family types.MultiplicityFamily, alpha float64) *types.Multiplicity {
	return &types.Multiplicity{Method: method, Family: family, Alpha: alpha}
}

func multPulse(t *testing.T, fs afero.Fs, def *types.Multiplicity) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, DefaultMultiplicity: def})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// multRefusalCase is one request the resolver refuses, on an instance
// with (def) or without an instance default.
type multRefusalCase struct {
	def  *types.Multiplicity
	code errors.Code
	mk   func(cohort string) *types.Request
}

func multRefusalCases() map[string]multRefusalCase {
	base := func(cohort string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
		}
	}
	tt := func(m *types.Multiplicity) *types.Test {
		return &types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g", Multiplicity: m}
	}
	with := func(f func(r *types.Request)) func(string) *types.Request {
		return func(cohort string) *types.Request {
			r := base(cohort)
			f(r)
			return r
		}
	}
	holm, bh := types.MultiplicityMethodHolm, types.MultiplicityMethodBH
	inv, conf := errors.PULSE_MULTIPLICITY_INVALID, errors.PULSE_MULTIPLICITY_CONFLICT
	return map[string]multRefusalCase{
		"unknown method": {nil, inv, with(func(r *types.Request) { r.Multiplicity = multBlock("sidak", "", 0) })},
		"unknown family": {nil, inv, with(func(r *types.Request) { r.Tests = []*types.Test{tt(multBlock("", "everything", 0))} })},
		"alpha out of range": {nil, inv, with(func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow, Multiplicity: multBlock("", "", 1.5)}}
		})},
		"family invalid for a test": {nil, inv, with(func(r *types.Request) { r.Tests = []*types.Test{tt(multBlock("", types.MultiplicityFamilyLayer, 0))} })},
		"alpha on a test":           {nil, inv, with(func(r *types.Request) { r.PostTests = []*types.Test{tt(multBlock("", "", 0.01))} })},
		"compose outside Compose":   {nil, inv, with(func(r *types.Request) { r.Multiplicity = multBlock(holm, types.MultiplicityFamilyCompose, 0) })},
		"row on a non-MATRIX payload": {nil, inv, with(func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow, Multiplicity: multBlock("", types.MultiplicityFamilyRow, 0)}}
		})},
		"column on a SCALAR payload": {nil, inv, with(func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix, Multiplicity: multBlock("", types.MultiplicityFamilyColumn, 0)}}
		})},
		"row on a SCALAR payload": {nil, inv, with(func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix, Multiplicity: multBlock(holm, types.MultiplicityFamilyRow, 0)}}
		})},
		"explicit method on Tukey": {nil, inv, with(func(r *types.Request) {
			r.PostTests = []*types.Test{{Type: types.TEST_TUKEY_HSD, Field: "s", Multiplicity: multBlock(holm, "", 0)}}
		})},
		"request family mixes methods":           {nil, conf, with(func(r *types.Request) { r.Tests = []*types.Test{tt(multBlock(holm, "", 0)), tt(multBlock(bh, "", 0))} })},
		"instance default conflicts with a slot": {multBlock(holm, "", 0), conf, with(func(r *types.Request) { r.Tests = []*types.Test{tt(nil), tt(multBlock(bh, "", 0))} })},
	}
}

// TestMultiplicity_RefusalsMatchPredict is the predict-vs-runtime table:
// every resolver refusal is raised by Process (and ProcessStream, and a
// ProcessChain stage, located) with the code, message and details
// PredictBytes reports first.
func TestMultiplicity_RefusalsMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	hosts := map[string]*pulse.Pulse{}
	host := func(def *types.Multiplicity) *pulse.Pulse {
		key := "none"
		if def != nil {
			key = string(def.Method)
		}
		if hosts[key] == nil {
			hosts[key] = multPulse(t, fs, def)
		}
		return hosts[key]
	}
	cases := multRefusalCases()
	names := make([]string, 0, len(cases))
	for n := range cases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := cases[name]
		t.Run(name, func(t *testing.T) {
			p := host(c.def)
			_, rerr := p.Process(ctx, c.mk(cohort))
			ce := requireCode(t, rerr, c.code)
			sameEntry(t, predictEnvelope(t, p, fs, cohort, c.mk(cohort)), rerr)

			_, serr := p.ProcessStream(ctx, c.mk(cohort))
			if sce := requireCode(t, serr, c.code); sce.Message != ce.Message {
				t.Errorf("ProcessStream message %q, Process %q", sce.Message, ce.Message)
			}

			// A later chain stage resolves standalone too, located by stage.
			stage1 := c.mk(cohort)
			stage1.Cohort = nil
			chain := &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}},
					{Request: stage1},
				},
			}
			_, cerr := p.ProcessChain(ctx, chain)
			if cce := requireCode(t, cerr, c.code); cce.Details["stage"] != 1 || cce.Message != ce.Message {
				t.Errorf("chain refusal = %q %v", cce.Message, cce.Details)
			}
		})
	}
}

// TestMultiplicity_ComposeFamilyOnlyInsideCompose: the request-level
// `compose` family is refused standalone and accepted as a Compose slot
// (serial and parallel), where a cross-slot conflict is refused
// located by the batch.
func TestMultiplicity_ComposeFamilyOnlyInsideCompose(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	p := multPulse(t, fs, nil)
	slot := func(m *types.Multiplicity) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			Multiplicity: m,
		}
	}
	composeHolm := multBlock(types.MultiplicityMethodHolm, types.MultiplicityFamilyCompose, 0)
	if _, err := p.Process(ctx, slot(composeHolm)); err == nil {
		t.Fatal("standalone compose family accepted")
	} else {
		requireCode(t, err, errors.PULSE_MULTIPLICITY_INVALID)
	}
	ok := &types.ComposedRequest{Requests: []*types.Request{slot(composeHolm), slot(nil)}}
	if _, err := p.Compose(ctx, ok); err != nil {
		t.Fatalf("Compose refused the compose family: %v", err)
	}
	if _, err := p.ComposeParallel(ctx, ok, pulse.ComposeOptions{MaxWorkers: 2}); err != nil {
		t.Fatalf("ComposeParallel refused the compose family: %v", err)
	}

	tt := func(m types.MultiplicityMethod) *types.Request {
		r := slot(multBlock(m, types.MultiplicityFamilyCompose, 0))
		r.Tests = []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g"}}
		return r
	}
	bad := &types.ComposedRequest{Requests: []*types.Request{tt(types.MultiplicityMethodHolm), tt(types.MultiplicityMethodBH)}}
	_, serr := p.Compose(ctx, bad)
	ce := requireCode(t, serr, errors.PULSE_MULTIPLICITY_CONFLICT)
	if !reflect.DeepEqual(ce.Details["slots"], []string{"requests[1].tests[0]", "requests[0].tests[0]"}) {
		t.Errorf("conflict slots = %v", ce.Details["slots"])
	}
	_, perr := p.ComposeParallel(ctx, bad, pulse.ComposeOptions{MaxWorkers: 2})
	if pce := requireCode(t, perr, errors.PULSE_MULTIPLICITY_CONFLICT); pce.Message != ce.Message {
		t.Errorf("parallel message %q, serial %q", pce.Message, ce.Message)
	}
}

// TestMultiplicity_DefaultValidatedAtNew: pulse.New refuses a bad
// Options.DefaultMultiplicity before building anything; a valid one is
// installed (the conflict case of the parity table proves it reaches
// predict and the runtime).
func TestMultiplicity_DefaultValidatedAtNew(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, bad := range []*types.Multiplicity{
		multBlock("holm-bonferroni", "", 0),
		multBlock("", "matrix", 0),
		multBlock("", "", 1),
	} {
		_, err := pulse.New(pulse.Options{FS: fs, DefaultMultiplicity: bad})
		ce := requireCode(t, err, errors.PULSE_MULTIPLICITY_INVALID)
		if ce.Details["slot"] != "Options.DefaultMultiplicity" {
			t.Errorf("details = %v", ce.Details)
		}
	}
	for _, good := range []*types.Multiplicity{nil, multBlock(types.MultiplicityMethodNone, "", 0), multBlock(types.MultiplicityMethodBY, types.MultiplicityFamilyCompose, 0.1)} {
		if _, err := pulse.New(pulse.Options{FS: fs, DefaultMultiplicity: good}); err != nil {
			t.Errorf("New refused %+v: %v", good, err)
		}
	}
}

func multiplicityProfile(with bool) *pulse.FeatureProfile {
	f := []string{"capability:process", "capability:compose", "AGG_SUM", "TEST_T"}
	if with {
		f = append(f, descx.FeatureMultiplicity)
	}
	return &pulse.FeatureProfile{Features: f}
}

// TestHiddenMultiplicity_RefusedAsUnknownField: with
// capability:multiplicity hidden every `multiplicity` key is an
// unknown field (root and nested, Process and Compose, runtime and
// predict), the payload schema carries none, and
// Options.DefaultMultiplicity fails pulse.New as a profile dependency.
func TestHiddenMultiplicity_RefusedAsUnknownField(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	hidden, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: multiplicityProfile(false)})
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: multiplicityProfile(true)})
	if err != nil {
		t.Fatal(err)
	}
	holm := multBlock(types.MultiplicityMethodHolm, "", 0)
	mk := map[string]func() *types.Request{
		"root": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}, Multiplicity: holm}
		},
		"tests[0]": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}},
				Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g", Multiplicity: holm}}}
		},
	}
	for path, f := range mk {
		t.Run(path, func(t *testing.T) {
			_, rerr := hidden.Process(ctx, f())
			ce := requireCode(t, rerr, errors.PULSE_REQUEST_UNKNOWN_FIELD)
			if !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"multiplicity"}) {
				t.Errorf("unknown_keys = %v", ce.Details["unknown_keys"])
			}
			sameEntry(t, predictEnvelope(t, hidden, fs, cohort, f()), rerr)
			_, cerr := hidden.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{f()}})
			if cce := requireCode(t, cerr, errors.PULSE_REQUEST_UNKNOWN_FIELD); cce.Details["request"] != 0 {
				t.Errorf("compose details = %v", cce.Details)
			}
			_, eerr := enabled.Process(ctx, f())
			var ece *errors.CodedError
			if stderrors.As(eerr, &ece) && (ece.Code == errors.PULSE_REQUEST_UNKNOWN_FIELD || strings.HasPrefix(string(ece.Code), "PULSE_MULTIPLICITY")) {
				t.Fatalf("enabled instance refused the slot: %v", eerr)
			}
		})
	}
	_, cerr := hidden.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk["root"]()}, Multiplicity: holm})
	if ce := requireCode(t, cerr, errors.PULSE_REQUEST_UNKNOWN_FIELD); ce.Details["request"] != nil {
		t.Errorf("composed-root refusal located at a slot: %v", ce.Details)
	}

	schema, err := hidden.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schema), "multiplicity") || strings.Contains(string(schema), "Multiplicity") {
		t.Error("hidden instance payload schema names multiplicity")
	}
	if schema, _ := enabled.PayloadSchema(); !strings.Contains(string(schema), `"multiplicity"`) {
		t.Error("enabled instance payload schema lacks multiplicity")
	}

	_, err = pulse.New(pulse.Options{FS: fs, FeatureProfile: multiplicityProfile(false), DefaultMultiplicity: holm})
	ce := requireCode(t, err, errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	wantUnmet := []map[string]any{{"option": "Options.DefaultMultiplicity", "requires_any_of": []string{descx.FeatureMultiplicity}}}
	if !reflect.DeepEqual(ce.Details["unmet"], wantUnmet) || !reflect.DeepEqual(ce.Details["options"], []string{"Options.DefaultMultiplicity"}) {
		t.Errorf("details = %v", ce.Details)
	}
	_, err = pulse.New(pulse.Options{FS: fs, FeatureProfile: multiplicityProfile(false), DefaultMultiplicity: holm,
		DefaultWeight: &types.WeightSpec{Field: "y"}})
	ce = requireCode(t, err, errors.PULSE_FEATURE_PROFILE_DEPENDENCY)
	if !reflect.DeepEqual(ce.Details["options"], []string{"Options.DefaultWeight", "Options.DefaultMultiplicity"}) {
		t.Errorf("both unmet options: %v", ce.Details["options"])
	}
	if _, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: multiplicityProfile(true), DefaultMultiplicity: holm}); err != nil {
		t.Errorf("enabled profile refused the default: %v", err)
	}
}
