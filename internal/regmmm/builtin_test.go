package regmmm_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"sort"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// builtin_test.go pins what being BUILT IN means for REG_MMM_HILL
// (mmm-followups E2-S1): the engine's output keys are the descriptor's
// applicability row; predict and the runtime refuse the same shared
// slots with the same code; predict validates a coef() curve term
// statically from params.channels (the term catalog); an embedder can
// no longer register the name itself.

// TestMMMHill_OutputsMatchApplicability is the end-to-end twin of the
// engine's TestRegressionOutputsHoldAtRuntime over the stock
// instance's full path (materialize, predict, envelope): on a stock instance a plain fit
// emits exactly the fit-output keys its applicability row lists (the
// echoes name and type aside).
func TestMMMHill_OutputsMatchApplicability(t *testing.T) {
	t.Parallel()

	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, fit(t, p, spec(twoChannels, paramsTwo)))), &keys); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range keys {
		if k != "name" && k != "type" {
			got = append(got, k)
		}
	}
	sort.Strings(got)
	want := descx.RegressionOutputKeys(string(regmmm.Name))
	if !slices.Equal(got, want) {
		t.Fatalf("a plain fit emits %v, the applicability row lists %v", got, want)
	}
}

// TestMMMHill_SharedSlotsRefusedLikePredict: a shared slot the model
// does not declare — a penalty, a selection, a robust covariance, a GLM
// family, a row weight — and a conf_level outside (0, 1) are refused by
// predict and by the runtime with the same single code, and the
// declared ones (resample, vcov, conf_level) run.
func TestMMMHill_SharedSlotsRefusedLikePredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	req := func(extra string) *types.Request {
		var s types.RegressionSpec
		body := `{"type":"REG_MMM_HILL","name":"mmm","target":"y","predictors":["tv_ad","search_ad","price"],"params":` + paramsTwo + extra + `}`
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		return &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{&s}}
	}
	for name, extra := range map[string]string{
		"penalty":          `,"penalty":"l2","alpha":0.1`,
		"selection":        `,"selection":"forward","criterion":"aic"`,
		"covariance":       `,"covariance":{"type":"hc3"}`,
		"family":           `,"family":"poisson"`,
		"weight":           `,"weight":{"field":"price"}`,
		"conf_level_range": `,"conf_level":2`,
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := p.Predict(ctx, req(extra))
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.Process(ctx, req(extra))
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Fatalf("runtime: %v, want a coded refusal", rerr)
			}
			if len(pr.Errors) != 1 || pr.Errors[0].Code != string(ce.Code) {
				t.Fatalf("predict errors %+v, runtime %s", pr.Errors, ce.Code)
			}
		})
	}
	for name, extra := range map[string]string{
		"resample":   `,"resample":"jackknife","jackknife_groups":4`,
		"vcov":       `,"vcov":true`,
		"conf_level": `,"conf_level":0.9`,
	} {
		t.Run(name, func(t *testing.T) {
			pr, err := p.Predict(ctx, req(extra))
			if err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict: %v %+v", err, pr)
			}
			resp, err := p.Process(ctx, req(extra))
			if err != nil || resp.Regressions[0].Error != nil {
				t.Fatalf("runtime: %v %s", err, mustJSON(t, resp))
			}
		})
	}
}

// TestMMMHill_SpecRefusalsPredictMatchRuntime: every factory refusal
// reads the spec alone, so predict raises it with the runtime's code,
// message and details — no channels, a channel that is not a predictor
// or is listed twice, an invalid prior (a key typo inside it included).
// A negative channel value, which only the rows show, stays
// runtime-only. (A predictor named like a curve coefficient names a
// field no cohort carries, so the unknown-field rule refuses it first;
// TestMMMHill_FactoryRefusesCurveNameCollision covers the factory's.)
func TestMMMHill_SpecRefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	req := func(predictors []string, params string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{spec(predictors, params)}}
	}
	for name, tc := range map[string]struct {
		predictors []string
		params     string
	}{
		"no params":               {twoChannels, ``},
		"no channels":             {twoChannels, `{"nonnegative":true}`},
		"channel not a predictor": {twoChannels, `{"channels":["tv_ad","radio"]}`},
		"channel twice":           {twoChannels, `{"channels":["tv_ad","tv_ad"]}`},
		"prior on a control":      {twoChannels, `{"channels":["tv_ad"],"prior":{"channel":"price","mean":1,"precision":1}}`},
		"prior key typo":          {twoChannels, `{"channels":["tv_ad"],"prior":{"channel":"tv_ad","mean":1,"precison":1}}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.Process(ctx, req(tc.predictors, tc.params))
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_CONFIG {
				t.Fatalf("runtime: %v, want PROCESSING_CONFIG", rerr)
			}
			pr, err := p.Predict(ctx, req(tc.predictors, tc.params))
			if err != nil {
				t.Fatal(err)
			}
			if len(pr.Errors) != 1 || pr.Errors[0].Code != string(ce.Code) || pr.Errors[0].Message != ce.Message ||
				mustJSON(t, pr.Errors[0].Details) != mustJSON(t, ce.Details) {
				t.Fatalf("predict errors %s, runtime %s %q %s", mustJSON(t, pr.Errors), ce.Code, ce.Message, mustJSON(t, ce.Details))
			}
		})
	}
	// Rows only: predict cannot see a negative channel value.
	pr, err := p.Predict(ctx, req([]string{"tv_ad", "price"}, `{"channels":["tv_ad","price"]}`))
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict on a data-only refusal: %v %+v", err, pr)
	}
}

// TestMMMHill_TermCatalogStatic: predict knows a REG_MMM_HILL fit's
// curve coefficients from its spec alone (k:<channel> / s:<channel> for
// params.channels), so over a `fits` entry it refuses an unknown curve
// term with the runtime's code and message — before, an extension fit's
// own names were unknown to predict and any term passed — and accepts
// every catalogued one.
func TestMMMHill_TermCatalogStatic(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := instance(t, afero.NewMemMapFs(), nil)
	cohort(t, p, defaultTruth, 7)
	s := spec(twoChannels, paramsTwo)
	good := composeScore(s, "")
	if env, err := p.PredictCompose(ctx, good); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict compose over the catalogued terms: %v %+v", err, env)
	}
	for _, term := range []string{"k:radio", "s:price", "k:price"} {
		bad := composeScore(s, "")
		bad.Requests[0].Attributes[2].Expression = `coef("mmm", "` + term + `")`
		env, err := p.PredictCompose(ctx, bad)
		if err != nil {
			t.Fatal(err)
		}
		_, rerr := p.Compose(ctx, bad)
		var ce *perr.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_MODEL_TERM_UNKNOWN {
			t.Fatalf("%s: runtime %v, want PULSE_MODEL_TERM_UNKNOWN", term, rerr)
		}
		if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_MODEL_TERM_UNKNOWN) || env.Errors[0].Message != ce.Message {
			t.Fatalf("%s: predict errors %+v, want the runtime's %s %q", term, env.Errors, ce.Code, ce.Message)
		}
	}
}

// TestMMMHill_EmbedderCannotRegisterName: REG_MMM_HILL is a built-in
// name, so an embedder registration under it (the retired pack's) is
// refused at pulse.New with PULSE_EXTENSION_NAME_COLLISION.
func TestMMMHill_EmbedderCannotRegisterName(t *testing.T) {
	t.Parallel()

	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{
		Regressions: []pulse.RegressionRegistration{{Name: regmmm.Name, Factory: regmmm.Factory}},
	}})
	if !perr.HasCode(err, perr.PULSE_EXTENSION_NAME_COLLISION) {
		t.Fatalf("pulse.New: %v, want PULSE_EXTENSION_NAME_COLLISION", err)
	}
}
